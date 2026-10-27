package box

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/events"
	"github.com/cosscom/shipyard/internal/service"
	"github.com/cosscom/shipyard/internal/wire"
)

// installDetaching installs the fake helper for the plugin "acme" with a
// detach path beside its attach path, and waits until it runs.
func installDetaching(t *testing.T, h *Helpers) HelperStatus {
	t.Helper()
	_, program := fakeHelperBuild(t)
	spec := fakeSpec(program)
	spec.Detach = "/api/detach"
	if _, err := h.Install(context.Background(), "acme", spec, bytes.NewReader(program)); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the helper to run", 20*time.Second, func() bool { return h.Status("acme").State == HelperRunning })
	return h.Status("acme")
}

// detachRig is a box with helpers that are told of removed worktrees, a
// project "shop" on it, and units a test can read.
type detachRig struct {
	t     *testing.T
	c     *wire.Client
	bus   *events.Bus
	box   *Box
	h     *Helpers
	logs  *logLines
	units *unitSpecs
	repo  string
}

func newDetachRig(t *testing.T, env map[string]string) *detachRig {
	t.Helper()
	t.Setenv("SHELL", "/bin/sh")
	h, logs := testHelpers(t)
	r := &detachRig{t: t, h: h, logs: logs, units: &unitSpecs{specs: map[string]service.Spec{}}, repo: gitRepo(t)}
	r.c, r.bus = servedBox(t, func(b *Box) {
		b.Helpers = h
		b.Units = &Units{Dir: t.TempDir(), svc: r.units.ops()}
		r.box = b
	})
	// As berthd wires it: every worktree.removed is one the helpers forget.
	t.Cleanup(h.Watch(r.bus))
	ctx := context.Background()
	if _, err := r.box.Locations.Add(ctx, "shop", r.repo); err != nil {
		t.Fatal(err)
	}
	if err := r.box.Locations.SetLocalConfig("shop", RepoConfig{Env: env, Services: []WorktreeService{{Name: "web", Run: "pnpm dev"}}}); err != nil {
		t.Fatal(err)
	}
	return r
}

// worktree makes a worktree whose web service the helper is noted to have
// changed, as Attach notes it.
func (r *detachRig) worktree(name string) Worktree {
	r.t.Helper()
	wt, err := r.box.Locations.CreateWorktree(context.Background(), "shop", name, "", "")
	if err != nil {
		r.t.Fatal(err)
	}
	r.h.noteAttached("acme", HelperService{Location: "shop", Worktree: name, Service: "web"}, true)
	return wt
}

// told is every detach the fake helper answered.
func (r *detachRig) told() []HelperDetach {
	f, err := os.Open(filepath.Join(r.h.dataDir("acme"), "detach-requests"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []HelperDetach
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var d HelperDetach
		if json.Unmarshal(sc.Bytes(), &d) == nil {
			out = append(out, d)
		}
	}
	return out
}

func (r *detachRig) toldOf(worktree string) (HelperDetach, bool) {
	for _, d := range r.told() {
		if d.Worktree == worktree {
			return d, true
		}
	}
	return HelperDetach{}, false
}

// attached is whether berthd's note still names a worktree.
func (r *detachRig) attached(worktree string) bool {
	for _, s := range r.h.attachedTo("acme") {
		if s.Location == "shop" && s.Worktree == worktree {
			return true
		}
	}
	return false
}

// detached waits until the helper was told the worktree is gone, and checks
// what it was told and that berthd's own note forgot it.
func (r *detachRig) detached(how string, wt Worktree) {
	r.t.Helper()
	var d HelperDetach
	waitUntil(r.t, how+": the helper to be told that "+wt.Name+" is gone", 20*time.Second, func() bool {
		var ok bool
		d, ok = r.toldOf(wt.Name)
		return ok
	})
	if d.Box != "devbox" || d.Project != "shop" || d.Path != wt.Path {
		r.t.Errorf("%s: the helper was told %+v", how, d)
	}
	if r.attached(wt.Name) {
		r.t.Errorf("%s: attached.json still names %s", how, wt.Name)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		r.t.Errorf("%s: %s is still there", how, wt.Name)
	}
}

func TestEveryWayAWorktreeIsRemovedTellsTheHelpersAndForgetsIt(t *testing.T) {
	ctx := context.Background()
	r := newDetachRig(t, nil)
	installDetaching(t, r.h)

	// The app, through the API.
	wt := r.worktree("by-api")
	if status := call(t, r.c, "DELETE", "/v1/locations/shop/worktrees/by-api?force=1", "", nil, nil); status != 200 {
		t.Fatalf("removing through the API: %d", status)
	}
	r.detached("the API", wt)

	// The CLI, with the client `berthd worktree remove` uses.
	wt = r.worktree("by-cli")
	if _, err := NewClient(r.c).RemoveWorktree(ctx, "shop", "by-cli", RemoveOptions{Force: true}); err != nil {
		t.Fatalf("removing as the CLI does: %v", err)
	}
	r.detached("the CLI", wt)

	// A review's clean-up.
	wt = r.worktree("by-review")
	loc, err := r.box.Locations.Get(ctx, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.box.removeReview(ctx, "berth", loc, wt, "merged"); err != nil {
		t.Fatalf("a review's clean-up: %v", err)
	}
	r.detached("a review's clean-up", wt)

	// Git itself, behind berthd's back: the watcher notices.
	w := &Watcher{Locations: r.box.Locations, Events: r.bus, Box: "devbox"}
	wt = r.worktree("by-git")
	w.Scan(ctx, false)
	if out, err := exec.Command("git", "-C", r.repo, "worktree", "remove", "--force", wt.Path).CombinedOutput(); err != nil {
		t.Fatalf("git worktree remove: %s", out)
	}
	w.Scan(ctx, true)
	r.detached("git itself", wt)

	// A run's clean-up and a task that failed remove the worktree themselves
	// and say so with the same event, which is all the helpers listen for.
	wt = r.worktree("by-run")
	if err := r.box.Locations.RemoveWorktree(ctx, "shop", "by-run", true); err != nil {
		t.Fatal(err)
	}
	r.bus.Publish(events.Event{Type: "worktree.removed", Box: "devbox", Data: map[string]any{"location": "shop", "name": "by-run", "path": wt.Path, "reason": "attempt not picked"}})
	r.detached("a run's clean-up", wt)

	// With an archive script the worktree goes later, in the background.
	if status := call(t, r.c, "PUT", "/v1/locations/shop/scripts", "", map[string]string{"archive": "true"}, nil); status != 200 {
		t.Fatalf("set scripts: %d", status)
	}
	wt = r.worktree("by-archive")
	if status := call(t, r.c, "DELETE", "/v1/locations/shop/worktrees/by-archive?force=1", "", nil, nil); status != 202 {
		t.Fatalf("removing with an archive script: %d", status)
	}
	r.detached("after an archive script", wt)

	// A worktree that is still there was never mentioned.
	kept := r.worktree("kept")
	if _, ok := r.toldOf("kept"); ok || !r.attached("kept") {
		t.Fatal("a worktree that was not removed was detached")
	}

	// The location itself: all of its worktrees, its own checkout among
	// them, which a helper knows as the project with no worktree.
	other := r.worktree("also-kept")
	if status := call(t, r.c, "DELETE", "/v1/locations/shop", "", nil, nil); status != 200 {
		t.Fatalf("removing the location: %d", status)
	}
	for _, wt := range []Worktree{kept, other} {
		waitUntil(t, "the helper to be told of "+wt.Name, 20*time.Second, func() bool { _, ok := r.toldOf(wt.Name); return ok })
	}
	var main HelperDetach
	waitUntil(t, "the helper to be told of the project's own checkout", 20*time.Second, func() bool {
		var ok bool
		main, ok = r.toldOf("")
		return ok
	})
	if main.Project != "shop" || main.Path != r.repo {
		t.Errorf("for the project's own checkout the helper was told %+v", main)
	}
	if got := r.h.attachedTo("acme"); len(got) != 0 || exists(r.h.attachedPath("acme")) {
		t.Fatalf("after the location was removed, attached = %+v", got)
	}
}

func TestAHelperThatIsDownSlowOrWrongNeverHoldsUpARemoval(t *testing.T) {
	ctx := context.Background()
	r := newDetachRig(t, nil)
	r.h.detachTimeout = time.Second
	st := installDetaching(t, r.h)
	data := r.h.dataDir("acme")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(data, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	remove := func(what, name string) {
		t.Helper()
		began := time.Now()
		if status := call(t, r.c, "DELETE", "/v1/locations/shop/worktrees/"+name+"?force=1", "", nil, nil); status != 200 {
			t.Fatalf("%s: removing %s: %d", what, name, status)
		}
		if took := time.Since(began); took > 5*time.Second {
			t.Errorf("%s: the removal waited %s", what, took)
		}
		waitUntil(t, what+": attached.json to forget "+name, 10*time.Second, func() bool { return !r.attached(name) })
	}

	// Slow: it answers long after berthd has stopped waiting.
	write("detach-delay", "20s")
	wt := r.worktree("slow")
	remove("a slow helper", "slow")
	waitUntil(t, "berthd to say the helper was slow", 10*time.Second, func() bool {
		return strings.Contains(r.logs.String(), "not told that shop/slow was removed: it did not answer in time")
	})
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Error("the worktree is still there")
	}
	// Several at once wait for it once, not once each: it is not asked
	// about the rest.
	began := time.Now()
	r.h.Detach(ctx, "shop", Worktree{Name: "one"}, Worktree{Name: "two"}, Worktree{Name: "three"})
	if took := time.Since(began); took < 900*time.Millisecond || took > 2500*time.Millisecond {
		t.Errorf("three worktrees waited %s for a helper with a second to answer", took)
	}
	os.Remove(filepath.Join(data, "detach-delay"))

	// An error: said in the log, and the next worktree is still told.
	write("detach-status", "500")
	r.worktree("refused")
	r.worktree("refused-too")
	r.h.Detach(ctx, "shop", Worktree{Name: "refused"}, Worktree{Name: "refused-too"})
	if _, ok := r.toldOf("refused-too"); !ok || r.attached("refused") || r.attached("refused-too") {
		t.Errorf("after a helper that answers an error: told %+v, attached %+v", r.told(), r.h.attachedTo("acme"))
	}
	if !strings.Contains(r.logs.String(), "not told that shop/refused was removed: it answered 500") {
		t.Errorf("berthd's log does not say the helper refused:\n%s", r.logs)
	}
	os.Remove(filepath.Join(data, "detach-status"))

	// Down: its program keeps failing, so berthd has nothing to tell.
	write("fail", "")
	syscall.Kill(st.PID, syscall.SIGKILL)
	waitUntil(t, "the helper to be down", 20*time.Second, func() bool { return r.h.Status("acme").State != HelperRunning })
	r.worktree("down")
	remove("a helper that is down", "down")
	waitUntil(t, "berthd to say the helper was down", 10*time.Second, func() bool {
		return strings.Contains(r.logs.String(), "not told that shop/down was removed: it is not running")
	})

	// Off altogether: nothing to tell, and its note is still kept right.
	if _, err := r.h.Remove("acme", false); err != nil {
		t.Fatal(err)
	}
	r.worktree("off")
	if !r.attached("off") {
		t.Fatal("the note of attached services went with the helper")
	}
	remove("a helper that is off", "off")
}

// Turned off, on and off again while a service keeps running with what the
// helper answered the first time: each turning off names it, until it is
// started again without the helper.
func TestTurningAHelperOffTwiceStillNamesTheServicesThatPointAtIt(t *testing.T) {
	r := newDetachRig(t, map[string]string{"DATABASE_URL": "postgres://app:hunter2@localhost:5432/acme"})
	if _, err := r.box.Locations.CreateWorktree(context.Background(), "shop", "fix-orders", "", ""); err != nil {
		t.Fatal(err)
	}
	web := HelperService{Location: "shop", Worktree: "fix-orders", Service: "web"}
	home := r.h.home("acme")
	on := func() {
		t.Helper()
		installDetaching(t, r.h)
		if err := os.WriteFile(filepath.Join(r.h.dataDir("acme"), "attach-answer"), []byte(`{"env": {"DATABASE_URL": "postgres://app:hunter2@127.0.0.1:25432/acme"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	off := func(what, query string, want ...HelperService) {
		t.Helper()
		status, body := helperCall(t, r.c, "DELETE", "/v1/helpers/acme"+query, nil, nil)
		var st HelperStatus
		if status != 200 || json.Unmarshal([]byte(body), &st) != nil || st.State != HelperOff {
			t.Fatalf("%s: %d %s", what, status, body)
		}
		if len(st.Attached) != len(want) || len(want) == 1 && st.Attached[0] != want[0] {
			t.Fatalf("%s: the services said to still point at the helper = %+v, want %+v", what, st.Attached, want)
		}
	}
	service := func(action string) {
		t.Helper()
		if status, body := helperCall(t, r.c, "POST", "/v1/locations/shop/worktrees/fix-orders/services/web/"+action, nil, nil); status != 200 {
			t.Fatalf("%s the service: %d %s", action, status, body)
		}
	}
	url := func() string { return r.units.env(serviceUnit("shop", "fix-orders", "web"))["DATABASE_URL"] }

	on()
	service("start")
	if url() != "postgres://app:hunter2@127.0.0.1:25432/acme" {
		t.Fatalf("the service was not attached: %q", url())
	}
	off("the first turning off", "", web)
	// Not restarted, and the helper is back where the service sends.
	on()
	off("turned on and off again", "", web)
	if !exists(r.h.attachedPath("acme")) {
		t.Fatal("berthd's note of the attached service went with the helper")
	}

	// Started again while the helper is off, it is as the project wrote
	// it: no turning off names it after that.
	service("restart")
	if url() != "postgres://app:hunter2@localhost:5432/acme" {
		t.Fatalf("with the helper off the service got %q", url())
	}
	if exists(r.h.attachedPath("acme")) || !exists(r.h.dataDir("acme")) {
		t.Fatal("after the service started again: the note should be gone, and the helper's data kept")
	}
	on()
	off("after the service started again without the helper", "")

	// The same when its data goes too: the note outlives a purge, and
	// nothing else of the helper does.
	on()
	service("restart")
	if err := os.WriteFile(filepath.Join(r.h.dataDir("acme"), "kept.db"), []byte("rows"), 0o600); err != nil {
		t.Fatal(err)
	}
	off("turned off with its data removed", "?purge=1", web)
	if entries, _ := os.ReadDir(home); len(entries) != 1 || entries[0].Name() != "attached.json" {
		t.Fatalf("after a purge the helper's folder holds %v", entries)
	}
	on()
	off("turned on and off again after a purge", "?purge=1", web)

	// A service that has stopped points nowhere, and is forgotten.
	service("stop")
	on()
	off("after the service stopped", "?purge=1")
	if exists(home) {
		t.Fatal("the helper's folder is still there after a purge with nothing attached")
	}

	// A helper that never ran here: removing its data again says nothing.
	ch, stop := r.bus.Subscribe()
	defer stop()
	off("purging what is not there", "?purge=1")
	if n := countEvents(ch, "helper.removed"); n != 0 {
		t.Fatalf("helper.removed was published %d times for a helper that was not there", n)
	}
}

func TestAHelpersDetachPathIsHeldToAPlainPath(t *testing.T) {
	_, program := fakeHelperBuild(t)
	for _, path := range []string{"api/detach", "/api//detach", "/api/../detach", "http://127.0.0.1/detach", "/api/detach?x=1"} {
		spec := fakeSpec(program)
		spec.Detach = path
		if err := spec.Validate(); err == nil || !strings.Contains(err.Error(), "detach path") {
			t.Errorf("detach %q: %v", path, err)
		}
	}
	spec := fakeSpec(program)
	plain := spec.Digest()
	spec.Detach = "/api/detach"
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	// It is part of how the helper is run: a box that has the helper
	// without it is brought up to the manifest's.
	if spec.Digest() == plain {
		t.Fatal("a helper with a detach path has the digest of one without")
	}
	m := HelperManifest{Name: spec.Name, Version: spec.Version, Args: spec.Args, Ready: spec.Ready, Detach: "/api/detach",
		Assets: map[string]HelperAsset{"linux-arm64": {URL: "https://example.com/acme-helper", SHA256: spec.SHA256, Size: spec.Size}}}
	if got, _, ok := m.Spec("linux-arm64"); !ok || got.Detach != "/api/detach" {
		t.Fatalf("the spec made from a manifest with detach = %+v", got)
	}
}

// With the real berth-leadline: a worktree that is removed has its proxy
// closed and what was recorded under its name deleted.
func TestLeadlineForgetsAWorktreeThatIsRemoved(t *testing.T) {
	if testing.Short() {
		t.Skip("builds berth-leadline")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("helpers run on Linux and macOS")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	built := filepath.Join(t.TempDir(), "berth-leadline")
	args := []string{"build", "-o", built}
	if raceBuild {
		args = append(args, "-race")
	}
	if out, err := exec.CommandContext(ctx, "go", append(args, "../../cmd/berth-leadline")...).CombinedOutput(); err != nil {
		t.Fatalf("building berth-leadline: %v\n%s", err, out)
	}
	program, err := os.ReadFile(built)
	if err != nil {
		t.Fatal(err)
	}
	// Two ports for the helper's proxies that nothing else has.
	var ports [2]int
	var held [2]net.Listener
	for i := range ports {
		free, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		held[i], ports[i] = free, free.Addr().(*net.TCPAddr).Port
	}
	for _, free := range held {
		free.Close()
	}
	lo, hi := min(ports[0], ports[1]), max(ports[0], ports[1])

	database := standInPostgres(t)
	_, dbPort, _ := net.SplitHostPort(database)
	r := newDetachRig(t, map[string]string{"DATABASE_URL": "postgres://app:hunter2@localhost:" + dbPort + "/acme?sslmode=disable"})
	for _, name := range []string{"fix-orders", "add-refunds"} {
		if _, err := r.box.Locations.CreateWorktree(ctx, "shop", name, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	// The manifest's own words, detach among them.
	manifest, err := os.ReadFile("../../plugins/leadline/berth-plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Box struct {
			Helper HelperManifest `json:"helper"`
		} `json:"box"`
	}
	if err := json.Unmarshal(manifest, &doc); err != nil || doc.Box.Helper.Detach != "/api/detach" {
		t.Fatalf("Leadline's manifest names the detach path %q (%v)", doc.Box.Helper.Detach, err)
	}
	m := doc.Box.Helper
	spec := HelperSpec{
		Name: m.Name, Version: m.Version,
		Args:  []string{"serve", "--dir", "{dir}", "--addr", "127.0.0.1:0", "--proxy-ports", fmt.Sprintf("%d-%d", lo, hi)},
		Ready: m.Ready, Attach: m.Attach, AttachEnv: m.AttachEnv, Detach: m.Detach,
		SHA256: sha256Hex(program), Size: int64(len(program)),
	}
	status, body := helperCall(t, r.c, "PUT", "/v1/helpers/leadline", program, specHeader(spec))
	if status != 200 || !strings.Contains(body, `"state":"running"`) {
		log, _ := r.h.Tail("leadline", 16<<10)
		t.Fatalf("install: %d %s\n%s", status, body, log)
	}
	for _, name := range []string{"fix-orders", "add-refunds"} {
		if status, body := helperCall(t, r.c, "POST", "/v1/locations/shop/worktrees/"+name+"/services/web/start", nil, nil); status != 200 {
			t.Fatalf("starting %s's service: %d %s", name, status, body)
		}
	}
	labels := func() []string {
		t.Helper()
		var out struct {
			Proxies []struct {
				Label string `json:"label"`
			} `json:"proxies"`
		}
		status, body := helperCall(t, r.c, "GET", "/v1/helpers/leadline/api/api/proxies", nil, nil)
		if status != 200 || json.Unmarshal([]byte(body), &out) != nil {
			t.Fatalf("the helper's proxies: %d %s", status, body)
		}
		var names []string
		for _, p := range out.Proxies {
			names = append(names, p.Label)
		}
		return names
	}
	if got := labels(); len(got) != 2 {
		t.Fatalf("with two worktrees attached the helper has the proxies %v", got)
	}

	// One worktree goes: its proxy closes, the other's stays open.
	if status := call(t, r.c, "DELETE", "/v1/locations/shop/worktrees/fix-orders?force=1", "", nil, nil); status != 200 {
		t.Fatalf("removing the worktree: %d", status)
	}
	waitUntil(t, "the removed worktree's proxy to close", 20*time.Second, func() bool {
		got := labels()
		return len(got) == 1 && got[0] == "shop/add-refunds"
	})
	for _, s := range r.h.attachedTo("leadline") {
		if s.Worktree == "fix-orders" {
			t.Fatalf("attached.json still names the removed worktree: %+v", s)
		}
	}
}
