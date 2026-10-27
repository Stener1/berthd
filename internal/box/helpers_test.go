package box

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/events"
	"github.com/cosscom/shipyard/internal/service"
	"github.com/cosscom/shipyard/internal/wire"
)

// The fake helper (testdata/fakehelper) is built once for all the tests.
var fakeHelper struct {
	once sync.Once
	dir  string
	path string
	data []byte
	err  error
}

// removeFakeHelper removes the build; TestMain calls it.
func removeFakeHelper() {
	if fakeHelper.dir != "" {
		os.RemoveAll(fakeHelper.dir)
	}
}

// fakeHelperBuild is the fake helper's program: its path and its bytes.
func fakeHelperBuild(t testing.TB) (string, []byte) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("helpers run on Linux and macOS")
	}
	fakeHelper.once.Do(func() {
		if fakeHelper.dir, fakeHelper.err = os.MkdirTemp("", "berth-fakehelper-"); fakeHelper.err != nil {
			return
		}
		fakeHelper.path = filepath.Join(fakeHelper.dir, "fakehelper")
		cmd := exec.Command("go", "build", "-o", fakeHelper.path, "./testdata/fakehelper")
		if out, err := cmd.CombinedOutput(); err != nil {
			fakeHelper.err = fmt.Errorf("building the fake helper: %v\n%s", err, out)
			return
		}
		fakeHelper.data, fakeHelper.err = os.ReadFile(fakeHelper.path)
	})
	if fakeHelper.err != nil {
		t.Fatal(fakeHelper.err)
	}
	return fakeHelper.path, fakeHelper.data
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// fakeSpec is the fake helper as a plugin's manifest would describe it.
func fakeSpec(program []byte) HelperSpec {
	return HelperSpec{
		Name: "acme-helper", Version: "1.0.0",
		Args:  []string{"serve", "--dir", "{dir}"},
		Ready: "helper.json", Attach: "/api/attach",
		AttachEnv: []string{"DATABASE_URL", "OTEL_*", "API_TOKEN"},
		SHA256:    sha256Hex(program), Size: int64(len(program)),
	}
}

// logLines collects what Helpers logs, for a test to read after it.
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *logLines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// testHelpers is a box's helpers in a folder of the test's own, quick to
// restart and to give up, and stopped when the test ends.
func testHelpers(t *testing.T) (*Helpers, *logLines) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logs := &logLines{}
	h := &Helpers{
		Dir: filepath.Join(root, "helpers"), Box: "devbox", Logf: logs.logf,
		readyTimeout: 20 * time.Second, stopTimeout: 3 * time.Second, attachTimeout: 5 * time.Second,
		backoff: func(int) time.Duration { return 20 * time.Millisecond },
	}
	t.Cleanup(h.Stop)
	return h, logs
}

// installFake installs the fake helper for the plugin "acme" and waits
// until it runs.
func installFake(t *testing.T, h *Helpers) HelperStatus {
	t.Helper()
	_, program := fakeHelperBuild(t)
	st, err := h.Install(context.Background(), "acme", fakeSpec(program), bytes.NewReader(program))
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the helper to run", 20*time.Second, func() bool { return h.Status("acme").State == HelperRunning })
	if st.Plugin != "acme" || st.Platform != runtime.GOOS+"-"+runtime.GOARCH {
		t.Fatalf("status = %+v", st)
	}
	return h.Status("acme")
}

// helperCall makes one request of a box's API as a paired laptop.
func helperCall(t *testing.T, c *wire.Client, method, path string, body []byte, header http.Header) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	resp, err := c.DoWithHeader(ctx, method, path, r, header)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func specHeader(spec HelperSpec) http.Header {
	return http.Header{HelperSpecHeader: {EncodeHelperSpec(spec)}, "Content-Type": {"application/octet-stream"}}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestAHelperUploadedWithAWrongChecksumIsNotKept(t *testing.T) {
	_, program := fakeHelperBuild(t)
	h, _ := testHelpers(t)
	c, _ := servedBox(t, func(b *Box) { b.Helpers = h })

	// The checksum of some other program.
	spec := fakeSpec(program)
	spec.SHA256 = sha256Hex([]byte("another program"))
	status, body := helperCall(t, c, "PUT", "/v1/helpers/acme", program, specHeader(spec))
	if status != 400 || !strings.Contains(body, `"code":"helper_mismatch"`) {
		t.Fatalf("wrong checksum: %d %s", status, body)
	}
	// The right checksum for bytes that are not all there.
	spec = fakeSpec(program)
	status, body = helperCall(t, c, "PUT", "/v1/helpers/acme", program[:len(program)-1], specHeader(spec))
	if status != 400 || !strings.Contains(body, `"code":"helper_mismatch"`) {
		t.Fatalf("wrong size: %d %s", status, body)
	}
	// No spec at all, and one that names a path.
	if status, body = helperCall(t, c, "PUT", "/v1/helpers/acme", program, nil); status != 400 || !strings.Contains(body, `"code":"helper_invalid"`) {
		t.Fatalf("no spec: %d %s", status, body)
	}
	spec.Name = "../acme"
	if status, body = helperCall(t, c, "PUT", "/v1/helpers/acme", program, specHeader(spec)); status != 400 || !strings.Contains(body, `"code":"helper_invalid"`) {
		t.Fatalf("a name that is a path: %d %s", status, body)
	}

	if st := h.Status("acme"); st.State != HelperOff {
		t.Fatalf("after refused uploads the helper is %s", st.State)
	}
	left, _ := filepath.Glob(filepath.Join(h.Dir, "acme", "*"))
	hidden, _ := filepath.Glob(filepath.Join(h.Dir, "acme", ".*"))
	if len(left)+len(hidden) != 0 {
		t.Fatalf("refused uploads left files behind: %v %v", left, hidden)
	}
	if status, body = helperCall(t, c, "GET", "/v1/helpers", nil, nil); status != 200 || !strings.Contains(body, `"helpers":[]`) {
		t.Fatalf("list: %d %s", status, body)
	}
	if exists(filepath.Join(h.Dir, "acme")) {
		t.Fatal("refused uploads left the plugin's folder behind")
	}
}

// A helper is installed from a paired laptop, where the plugin was allowed.
// What runs on the box itself (an agent, a script) reaches berthd through
// its socket, and can see and remove helpers but not add one.
func TestOnlyAPairedLaptopInstallsAHelper(t *testing.T) {
	_, program := fakeHelperBuild(t)
	h, _ := testHelpers(t)
	bx := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(t.TempDir(), "locations.json")), Events: &events.Bus{}, Helpers: h}
	local := localBox(t, bx)
	ctx := context.Background()

	_, err := local.InstallHelper(ctx, "acme", fakeSpec(program), program)
	var he *HelperError
	if !errors.As(err, &he) || he.Status != 403 || he.Code != CodeRefused {
		t.Fatalf("an install from the box itself: %v", err)
	}
	if h.Status("acme").State != HelperOff || exists(filepath.Join(h.Dir, "acme")) {
		t.Fatal("a helper was installed from the box itself")
	}

	st := installFake(t, h)
	list, err := local.Helpers(ctx)
	if err != nil || len(list.Helpers) != 1 || list.Helpers[0].PID != st.PID {
		t.Fatalf("the box's own list: %+v, %v", list, err)
	}
	waitUntil(t, "the helper's log", 5*time.Second, func() bool {
		log, err := local.HelperLog(ctx, "acme", 1<<20)
		return err == nil && strings.Contains(string(log), "fakehelper listening")
	})
	if now, err := local.RemoveHelper(ctx, "acme", true); err != nil || now.State != HelperOff || processAlive(st.PID) {
		t.Fatalf("removing it from the box itself: %+v, %v", now, err)
	}
	if _, err := local.HelperLog(ctx, "acme", 1<<20); !errors.As(err, &he) || he.Status != 404 {
		t.Fatalf("the log of a helper that is gone: %v", err)
	}
}

func TestAHelperIsInstalledStartedAndReported(t *testing.T) {
	_, program := fakeHelperBuild(t)
	t.Setenv("ACME_DEPLOY_TOKEN", "sekret-acme-token")
	h, _ := testHelpers(t)
	c, bus := servedBox(t, func(b *Box) { b.Helpers = h })
	ch, stop := bus.Subscribe()
	defer stop()

	spec := fakeSpec(program)
	status, body := helperCall(t, c, "PUT", "/v1/helpers/acme", program, specHeader(spec))
	if status != 200 {
		t.Fatalf("install: %d %s", status, body)
	}
	var st HelperStatus
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatal(err)
	}
	// The upload waits for it to come up, so the answer says it runs.
	if st.State != HelperRunning || st.Plugin != "acme" || st.Version != "1.0.0" || st.Name != "acme-helper" || st.PID == 0 || st.SHA256 != spec.SHA256 || !st.Attach {
		t.Fatalf("status after install = %+v", st)
	}
	if n := countEvents(ch, "helper.installed"); n != 1 {
		t.Fatalf("helper.installed was published %d times", n)
	}

	// Where it lives: its program for its owner alone, its data beside it.
	bin := filepath.Join(h.Dir, "acme", "bin", "1.0.0", "acme-helper")
	fi, err := os.Stat(bin)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("program: %v %v", fi, err)
	}
	data := filepath.Join(h.Dir, "acme", "data")
	if !exists(filepath.Join(data, "helper.json")) || !exists(filepath.Join(h.Dir, "acme", "install.json")) {
		t.Fatal("the helper's ready file or install record is missing")
	}

	status, body = helperCall(t, c, "GET", "/v1/helpers", nil, nil)
	var list HelperList
	if json.Unmarshal([]byte(body), &list) != nil || status != 200 || len(list.Helpers) != 1 || list.Helpers[0].State != HelperRunning || list.Platform != HelperPlatform() {
		t.Fatalf("list: %d %s", status, body)
	}
	if status, body = helperCall(t, c, "GET", "/v1/helpers/acme", nil, nil); status != 200 || !strings.Contains(body, `"state":"running"`) {
		t.Fatalf("get: %d %s", status, body)
	}
	if status, body = helperCall(t, c, "GET", "/v1/helpers/other", nil, nil); status != 200 || !strings.Contains(body, `"state":"off"`) {
		t.Fatalf("a plugin with no helper here: %d %s", status, body)
	}
	if status, body = helperCall(t, c, "GET", "/v1/helpers/Not%20An%20Id", nil, nil); status != 400 {
		t.Fatalf("a bad plugin id: %d %s", status, body)
	}

	// Its log: the arguments were the manifest's with {dir} filled in, it
	// ran in its data directory, and its environment is small. What berthd
	// itself was told (a token, here) is not passed on.
	waitUntil(t, "the helper's log", 5*time.Second, func() bool {
		_, body = helperCall(t, c, "GET", "/v1/helpers/acme/log", nil, nil)
		return strings.Contains(body, "fakehelper listening")
	})
	for _, want := range []string{
		fmt.Sprintf(`args=["serve" "--dir" %q]`, data), "cwd=" + data,
		"env BERTH_HELPER=acme", "env BERTH_HELPER_DIR=" + data, "env BERTH_BOX=devbox", "berthd: started acme-helper 1.0.0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the log lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "sekret-acme-token") || strings.Contains(body, "ACME_DEPLOY_TOKEN") {
		t.Errorf("the helper was given berthd's own environment:\n%s", body)
	}

	// Asked again for exactly what runs, with no program sent: it is left
	// running, not stopped and started.
	if st.Digest != spec.Digest() {
		t.Fatalf("the status's digest is not the spec's: %s", st.Digest)
	}
	status, body = helperCall(t, c, "PUT", "/v1/helpers/acme", nil, specHeader(spec))
	var same HelperStatus
	if json.Unmarshal([]byte(body), &same) != nil || status != 200 || same.State != HelperRunning || same.PID != st.PID {
		t.Fatalf("asking again for what runs: %d %s", status, body)
	}
	// The same program run another way starts again, with what the box
	// holds; naming another checksum with no program is refused.
	changed := spec
	changed.AttachEnv = []string{"DATABASE_URL"}
	status, body = helperCall(t, c, "PUT", "/v1/helpers/acme", nil, specHeader(changed))
	var again HelperStatus
	if json.Unmarshal([]byte(body), &again) != nil || status != 200 || again.State != HelperRunning || again.PID == st.PID || again.Digest != changed.Digest() {
		t.Fatalf("install with no program: %d %s", status, body)
	}
	other := spec
	other.SHA256 = sha256Hex([]byte("another program"))
	if status, body = helperCall(t, c, "PUT", "/v1/helpers/acme", nil, specHeader(other)); status != 409 || !strings.Contains(body, `"code":"helper_missing"`) {
		t.Fatalf("install of a program the box does not hold: %d %s", status, body)
	}
	if h.Status("acme").State != HelperRunning {
		t.Fatal("a refused install stopped the helper that was running")
	}
}

func TestAHelperThatExitsIsStartedAgain(t *testing.T) {
	h, logs := testHelpers(t)
	st := installFake(t, h)
	data := h.dataDir("acme")

	// It crashes: berthd starts it again.
	syscall.Kill(st.PID, syscall.SIGKILL)
	waitUntil(t, "the helper to come back", 20*time.Second, func() bool {
		now := h.Status("acme")
		return now.State == HelperRunning && now.PID != st.PID
	})
	now := h.Status("acme")
	if now.Restarts != 1 {
		t.Fatalf("restarts = %d, want 1", now.Restarts)
	}
	if starts, _ := os.ReadFile(filepath.Join(data, "starts")); strings.Count(string(starts), "\n") != 2 {
		t.Fatalf("the helper started %q times, want twice", starts)
	}

	// It keeps failing: it is shown as failed, with why, and still tried.
	if err := os.WriteFile(filepath.Join(data, "fail"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	syscall.Kill(now.PID, syscall.SIGKILL)
	waitUntil(t, "the helper to be shown as failed", 20*time.Second, func() bool { return h.Status("acme").State == HelperFailed })
	failed := h.Status("acme")
	if !strings.Contains(failed.Error, "exit status 3") || !strings.Contains(failed.Error, "tries again") || failed.PID != 0 {
		t.Fatalf("failed status = %+v", failed)
	}
	if _, ok := h.API("acme"); ok {
		t.Fatal("a failed helper still has an API to relay to")
	}
	// What made it fail goes away: it comes back by itself.
	os.Remove(filepath.Join(data, "fail"))
	waitUntil(t, "the helper to recover", 20*time.Second, func() bool { return h.Status("acme").State == HelperRunning })
	if !strings.Contains(logs.String(), "starting it again") {
		t.Fatalf("berthd's log does not say it restarted the helper:\n%s", logs)
	}
}

func TestAHelpersAPIIsRelayedAsALocalRequest(t *testing.T) {
	h, _ := testHelpers(t)
	c, _ := servedBox(t, func(b *Box) { b.Helpers = h })

	// Nothing is installed: there is nothing to relay to.
	status, body := helperCall(t, c, "GET", "/v1/helpers/acme/api/api/echo/x", nil, nil)
	if status != 409 || !strings.Contains(body, `"code":"helper_off"`) {
		t.Fatalf("relay with no helper: %d %s", status, body)
	}

	installFake(t, h)
	api, ok := h.API("acme")
	if !ok {
		t.Fatal("no API for a running helper")
	}
	header := http.Header{
		"Origin": {"https://evil.example"}, "Referer": {"https://evil.example/page"}, "Cookie": {"session=1"},
		OriginHeader: {"app"}, "Content-Type": {"application/json"},
	}
	status, body = helperCall(t, c, "POST", "/v1/helpers/acme/api/api/echo/a%2Fb/c?since_ms=5&q=a+b", []byte(`{"n":1}`), header)
	var echo map[string]string
	if status != 200 || json.Unmarshal([]byte(body), &echo) != nil {
		t.Fatalf("relay: %d %s", status, body)
	}
	want := map[string]string{
		"method": "POST", "path": "/api/echo/a%2Fb/c", "query": "since_ms=5&q=a+b",
		// The helper sees a request from this machine: its own address as
		// the Host, and no Origin.
		"host": api.Host, "origin": "", "berth_origin": "", "forwarded_for": "",
		"content_type": "application/json", "body": `{"n":1}`,
	}
	for k, v := range want {
		if echo[k] != v {
			t.Errorf("the helper saw %s = %q, want %q", k, echo[k], v)
		}
	}

	// A stream is passed on as it comes, not when it ends.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := c.Do(ctx, "GET", "/v1/helpers/acme/api/api/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	resp.Body.Close()
	if err != nil || line != "event: hello\n" {
		t.Fatalf("stream: %q %v", line, err)
	}

	// The helper's own 404 comes back as it is.
	if status, _ = helperCall(t, c, "GET", "/v1/helpers/acme/api/nothing/here", nil, nil); status != 404 {
		t.Fatalf("a path the helper does not serve: %d", status)
	}

	// Removed, it is off again.
	if status, body = helperCall(t, c, "DELETE", "/v1/helpers/acme", nil, nil); status != 200 || !strings.Contains(body, `"state":"off"`) {
		t.Fatalf("delete: %d %s", status, body)
	}
	status, body = helperCall(t, c, "GET", "/v1/helpers/acme/api/api/echo/x", nil, nil)
	if status != 409 || !strings.Contains(body, `"code":"helper_off"`) {
		t.Fatalf("relay after delete: %d %s", status, body)
	}
}

// unitSpecs is a service manager that only remembers what it was asked to
// run, so a test can read a service's environment.
type unitSpecs struct {
	mu    sync.Mutex
	specs map[string]service.Spec
}

func (u *unitSpecs) ops() *serviceOps {
	return &serviceOps{
		install: func(s service.Spec) (string, error) {
			u.mu.Lock()
			defer u.mu.Unlock()
			u.specs[s.Name] = s
			return "/fake/" + s.Name, nil
		},
		start: func(service.Spec) error { return nil },
		uninstall: func(s service.Spec) (string, error) {
			u.mu.Lock()
			defer u.mu.Unlock()
			delete(u.specs, s.Name)
			return "/fake/" + s.Name, nil
		},
		installed:       func(service.Spec) bool { return false },
		installedByName: func(name string) bool { u.mu.Lock(); defer u.mu.Unlock(); _, ok := u.specs[name]; return ok },
		running:         func(service.Spec) bool { return true },
	}
}

func (u *unitSpecs) env(name string) map[string]string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.specs[name].Env
}

// serviceBox is a box with a worktree whose web service runs as a unit the
// test can read, and the config env given.
func serviceBox(t *testing.T, h *Helpers, env map[string]string) (*Box, Worktree, *unitSpecs) {
	t.Helper()
	ctx := context.Background()
	repo := gitRepo(t)
	units := &unitSpecs{specs: map[string]service.Spec{}}
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(t.TempDir(), "locations.json")), Events: &events.Bus{}, Helpers: h}
	b.Units = &Units{Dir: t.TempDir(), svc: units.ops()}
	if _, err := b.Locations.Add(ctx, "cal", repo); err != nil {
		t.Fatal(err)
	}
	if err := b.Locations.SetLocalConfig("cal", RepoConfig{Env: env, Services: []WorktreeService{{Name: "web", Run: "pnpm dev"}}}); err != nil {
		t.Fatal(err)
	}
	wt, err := b.Locations.CreateWorktree(ctx, "cal", "billing", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return b, wt, units
}

func TestAttachPutsWhatAHelperAnswersIntoTheServicesEnvironment(t *testing.T) {
	ctx := context.Background()
	h, _ := testHelpers(t)
	t.Setenv("ACME_API_TOKEN", "sekret-acme-token")
	b, wt, units := serviceBox(t, h, map[string]string{
		"DATABASE_URL":             "postgres://app:hunter2@localhost:5432/acme",
		"OTEL_RESOURCE_ATTRIBUTES": "team=acme",
		"UNRELATED":                "as-the-project-set-it",
		"API_TOKEN":                "env://ACME_API_TOKEN",
	})
	unit := serviceUnit("cal", "billing", "web")

	// With no helper the service starts with the project's environment.
	if _, err := b.StartService(ctx, "cal", "billing", "web"); err != nil {
		t.Fatal(err)
	}
	plain := units.env(unit)
	if plain["DATABASE_URL"] != "postgres://app:hunter2@localhost:5432/acme" || plain["OTEL_SERVICE_NAME"] != "" {
		t.Fatalf("env with no helper = %v", plain)
	}

	installFake(t, h)
	data := h.dataDir("acme")
	answer := `{"env": {
		"DATABASE_URL": "postgres://app:hunter2@127.0.0.1:25432/acme",
		"OTEL_SERVICE_NAME": "cal/billing:web",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4325",
		"PATH": "/somewhere/else",
		"UNRELATED": "changed-by-the-helper",
		"BERTH_PORT": "1",
		"API_TOKEN": "replaced-by-the-helper",
		"not a name": "x"
	}}`
	if err := os.WriteFile(filepath.Join(data, "attach-answer"), []byte(answer), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.StartService(ctx, "cal", "billing", "web"); err != nil {
		t.Fatal(err)
	}
	got := units.env(unit)
	for k, v := range map[string]string{
		// What the helper answered, for the variables its manifest names.
		"DATABASE_URL":                "postgres://app:hunter2@127.0.0.1:25432/acme",
		"OTEL_SERVICE_NAME":           "cal/billing:web",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4325",
		"OTEL_RESOURCE_ATTRIBUTES":    "team=acme",
		// A variable it did not name stays the project's; so do Shipyard's
		// own, and one that names a secret keeps its reference.
		"UNRELATED":  "as-the-project-set-it",
		"BERTH_PORT": plain["BERTH_PORT"],
		"API_TOKEN":  "env://ACME_API_TOKEN",
		"PATH":       plain["PATH"],
	} {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if _, set := got["not a name"]; set {
		t.Error("a name that is no variable was set")
	}

	// What the helper was told: the service, and only the variables its
	// manifest names. A secret's reference is not one of them.
	raw, err := os.ReadFile(filepath.Join(data, "attach-request"))
	if err != nil {
		t.Fatal(err)
	}
	var told HelperAttach
	if err := json.Unmarshal(raw, &told); err != nil {
		t.Fatal(err)
	}
	if told.Box != "devbox" || told.Project != "cal" || told.Worktree != "billing" || told.Branch != wt.Branch || told.Path != wt.Path || told.Command != "web" {
		t.Errorf("the helper was told %+v", told)
	}
	if len(told.Env) != 2 || told.Env["DATABASE_URL"] != "postgres://app:hunter2@localhost:5432/acme" || told.Env["OTEL_RESOURCE_ATTRIBUTES"] != "team=acme" {
		t.Errorf("the helper was given the variables %v", told.Env)
	}
	if strings.Contains(string(raw), "sekret-acme-token") {
		t.Error("the helper was given a secret's value")
	}

	// berthd remembers which services a helper changed, in a file: the
	// next berthd knows too. A service started again with nothing changed
	// is forgotten.
	web := HelperService{Location: "cal", Worktree: "billing", Service: "web"}
	if got := h.attachedTo("acme"); len(got) != 1 || got[0] != web {
		t.Fatalf("attached = %+v", got)
	}
	if got := (&Helpers{Dir: h.Dir}).attachedTo("acme"); len(got) != 1 || got[0] != web {
		t.Fatalf("attached, as another berthd reads it = %+v", got)
	}
	if err := os.WriteFile(filepath.Join(data, "attach-answer"), []byte(`{"env": {"OTEL_RESOURCE_ATTRIBUTES": "team=acme"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.StartService(ctx, "cal", "billing", "web"); err != nil {
		t.Fatal(err)
	}
	if got := h.attachedTo("acme"); len(got) != 0 {
		t.Fatalf("attached after a start the helper changed nothing of = %+v", got)
	}

	// The project's own checkout is told as the project with no worktree,
	// whatever its folder is called: a helper files it under the project.
	if _, err := b.StartService(ctx, "cal", "cal", "web"); err != nil {
		t.Fatal(err)
	}
	if raw, err = os.ReadFile(filepath.Join(data, "attach-request")); err != nil {
		t.Fatal(err)
	}
	told = HelperAttach{}
	if err := json.Unmarshal(raw, &told); err != nil || told.Project != "cal" || told.Worktree != "" || told.Command != "web" || told.Path == wt.Path {
		t.Errorf("for the main checkout the helper was told %+v (%v)", told, err)
	}
}

func TestAHelperThatIsSlowDownOrWrongChangesNothing(t *testing.T) {
	ctx := context.Background()
	h, logs := testHelpers(t)
	h.attachTimeout = 200 * time.Millisecond
	b, _, units := serviceBox(t, h, map[string]string{"DATABASE_URL": "postgres://app:hunter2@localhost:5432/acme"})
	unit := serviceUnit("cal", "billing", "web")
	if _, err := b.StartService(ctx, "cal", "billing", "web"); err != nil {
		t.Fatal(err)
	}
	plain := units.env(unit)

	installFake(t, h)
	data := h.dataDir("acme")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(data, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("attach-answer", `{"env": {"DATABASE_URL": "postgres://app:hunter2@127.0.0.1:25432/acme"}}`)
	same := func(what string) {
		t.Helper()
		began := time.Now()
		if _, err := b.StartService(ctx, "cal", "billing", "web"); err != nil {
			t.Fatalf("%s: the service did not start: %v", what, err)
		}
		if took := time.Since(began); took > 5*time.Second {
			t.Errorf("%s: the service waited %s for the helper", what, took)
		}
		got := units.env(unit)
		if len(got) != len(plain) {
			t.Errorf("%s: env = %v, want %v", what, got, plain)
		}
		for k, v := range plain {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q", what, k, got[k], v)
			}
		}
	}

	// Slow: it answers long after berthd has stopped waiting.
	write("attach-delay", "10s")
	same("a slow helper")
	if !strings.Contains(logs.String(), "did not answer in time") {
		t.Errorf("berthd's log does not say the helper was slow:\n%s", logs)
	}
	os.Remove(filepath.Join(data, "attach-delay"))

	// An error.
	write("attach-status", "500")
	same("a helper that answers an error")
	os.Remove(filepath.Join(data, "attach-status"))

	// Not what berthd expects.
	write("attach-answer", `<html>`)
	same("a helper that answers something else")

	// And nothing berthd logged holds the environment.
	if strings.Contains(logs.String(), "hunter2") || strings.Contains(logs.String(), "postgres://") {
		t.Errorf("berthd logged a database URL:\n%s", logs)
	}

	// It works again once the helper does.
	write("attach-answer", `{"env": {"DATABASE_URL": "postgres://app:hunter2@127.0.0.1:25432/acme"}}`)
	if _, err := b.StartService(ctx, "cal", "billing", "web"); err != nil {
		t.Fatal(err)
	}
	if got := units.env(unit)["DATABASE_URL"]; got != "postgres://app:hunter2@127.0.0.1:25432/acme" {
		t.Fatalf("DATABASE_URL = %q once the helper answers again", got)
	}

	// Down: stopped and removed.
	if _, err := h.Remove("acme", false); err != nil {
		t.Fatal(err)
	}
	same("a helper that is not running")
}

func TestRemovingAHelperKeepsItsDataUnlessPurged(t *testing.T) {
	h, _ := testHelpers(t)
	c, bus := servedBox(t, func(b *Box) { b.Helpers = h })
	ch, stop := bus.Subscribe()
	defer stop()
	st := installFake(t, h)
	home := filepath.Join(h.Dir, "acme")
	data := filepath.Join(home, "data")
	if err := os.WriteFile(filepath.Join(data, "kept.db"), []byte("rows"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, body := helperCall(t, c, "DELETE", "/v1/helpers/acme", nil, nil)
	if status != 200 || !strings.Contains(body, `"state":"off"`) {
		t.Fatalf("delete: %d %s", status, body)
	}
	if processAlive(st.PID) {
		t.Fatal("the helper is still running after it was removed")
	}
	if exists(filepath.Join(home, "bin")) || exists(filepath.Join(home, "install.json")) {
		t.Fatal("the program or its install record is still there")
	}
	if kept, _ := os.ReadFile(filepath.Join(data, "kept.db")); string(kept) != "rows" {
		t.Fatal("the helper's data went with it")
	}
	// The helper took its ready file with it when it was asked to stop.
	if exists(filepath.Join(data, "helper.json")) {
		t.Fatal("the helper was killed, not asked to stop: its ready file is still there")
	}
	if n := countEvents(ch, "helper.removed"); n != 1 {
		t.Fatalf("helper.removed was published %d times", n)
	}
	// Removing what is not there is no error.
	if status, body = helperCall(t, c, "DELETE", "/v1/helpers/acme", nil, nil); status != 200 || !strings.Contains(body, `"state":"off"`) {
		t.Fatalf("delete again: %d %s", status, body)
	}

	// Installed again, it finds its data; purged, everything goes.
	installFake(t, h)
	if kept, _ := os.ReadFile(filepath.Join(data, "kept.db")); string(kept) != "rows" {
		t.Fatal("the data was not there for the helper installed again")
	}
	if status, body = helperCall(t, c, "DELETE", "/v1/helpers/acme?purge=1", nil, nil); status != 200 || !strings.Contains(body, `"state":"off"`) {
		t.Fatalf("purge: %d %s", status, body)
	}
	if exists(home) {
		t.Fatal("the helper's folder is still there after a purge")
	}
}

func TestAHelperThatWillNotStopIsKilled(t *testing.T) {
	h, _ := testHelpers(t)
	h.stopTimeout = 300 * time.Millisecond
	st := installFake(t, h)
	data := h.dataDir("acme")
	// The next one it starts ignores being asked to stop.
	if err := os.WriteFile(filepath.Join(data, "ignore-term"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	syscall.Kill(st.PID, syscall.SIGKILL)
	waitUntil(t, "the helper to come back", 20*time.Second, func() bool {
		now := h.Status("acme")
		return now.State == HelperRunning && now.PID != st.PID
	})
	stubborn := h.Status("acme").PID
	began := time.Now()
	if _, err := h.Remove("acme", true); err != nil {
		t.Fatal(err)
	}
	if processAlive(stubborn) {
		t.Fatal("the helper outlived its removal")
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Fatalf("removing a helper that would not stop took %s", took)
	}
}

func TestHelpersStopWithBerthdAndStartWithItAgain(t *testing.T) {
	h, _ := testHelpers(t)
	st := installFake(t, h)
	h.Stop()
	if processAlive(st.PID) {
		t.Fatal("the helper outlived berthd")
	}
	_, program := fakeHelperBuild(t)
	if _, err := h.Install(context.Background(), "acme", fakeSpec(program), bytes.NewReader(program)); err == nil {
		t.Fatal("a helper was installed after berthd stopped")
	}

	// The next berthd starts what is installed, before it does anything else.
	next := &Helpers{Dir: h.Dir, Box: "devbox", readyTimeout: 20 * time.Second, stopTimeout: 3 * time.Second}
	t.Cleanup(next.Stop)
	next.Start()
	now := next.Status("acme")
	if now.State != HelperRunning || now.PID == st.PID || now.Version != "1.0.0" {
		t.Fatalf("after berthd started again the helper is %+v", now)
	}
}

func TestAHelperLeftRunningByAnEarlierBerthdIsEndedFirst(t *testing.T) {
	path, _ := fakeHelperBuild(t)
	h, _ := testHelpers(t)
	if err := os.MkdirAll(h.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Two helpers with no berthd: one of this folder, one of another's.
	orphan := func(home string) (*exec.Cmd, chan error) {
		data := t.TempDir()
		cmd := exec.Command(path, "serve", "--dir", data)
		cmd.Env = append(os.Environ(), "BERTH_HELPER=acme", "BERTH_HELPER_HOME="+home)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		t.Cleanup(func() { cmd.Process.Kill() })
		waitUntil(t, "an orphan to listen", 20*time.Second, func() bool { return exists(filepath.Join(data, "helper.json")) })
		return cmd, done
	}
	_, mine := orphan(h.Dir)
	other, others := orphan(filepath.Join(t.TempDir(), "helpers"))

	h.Start()
	select {
	case <-mine:
	case <-time.After(10 * time.Second):
		t.Fatal("the helper an earlier berthd left running was not ended")
	}
	select {
	case <-others:
		t.Fatal("another folder's helper was ended")
	default:
	}
	other.Process.Kill()
}

func TestAHelperWhoseProgramChangedIsNotRun(t *testing.T) {
	h, _ := testHelpers(t)
	st := installFake(t, h)
	h.Stop()
	if processAlive(st.PID) {
		t.Fatal("the helper outlived berthd")
	}
	bin := filepath.Join(h.Dir, "acme", "bin", "1.0.0", "acme-helper")
	marker := filepath.Join(t.TempDir(), "ran")
	start := func() *Helpers {
		next := &Helpers{Dir: h.Dir, Box: "devbox", readyTimeout: 5 * time.Second, stopTimeout: time.Second,
			backoff: func(int) time.Duration { return 10 * time.Millisecond }}
		t.Cleanup(next.Stop)
		next.Start()
		waitUntil(t, "the helper to be shown as failed", 20*time.Second, func() bool { return next.Status("acme").State == HelperFailed })
		return next
	}

	// Other bytes under its name: not the program whose checksum was sent.
	script := "#!/bin/sh\ntouch " + shellQuote(marker) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	next := start()
	if got := next.Status("acme").Error; !strings.Contains(got, "no longer the one that was installed") {
		t.Fatalf("error = %q", got)
	}
	next.Stop()

	// A link to a program somewhere else.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(elsewhere, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	os.Remove(bin)
	if err := os.Symlink(elsewhere, bin); err != nil {
		t.Fatal(err)
	}
	next = start()
	if got := next.Status("acme").Error; !strings.Contains(got, "not a plain file") {
		t.Fatalf("error = %q", got)
	}
	next.Stop()
	if exists(marker) {
		t.Fatal("a program that was not the one installed was run")
	}

	// An install record that names a path is not used at all.
	rec := `{"plugin":"acme","name":"../../../../bin/sh","version":"1.0.0","args":[],"ready":"helper.json","sha256":"` + sha256Hex([]byte(script)) + `","size":10}`
	if err := os.WriteFile(filepath.Join(h.Dir, "acme", "install.json"), []byte(rec), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := &logLines{}
	last := &Helpers{Dir: h.Dir, Box: "devbox", Logf: logs.logf}
	t.Cleanup(last.Stop)
	last.Start()
	if st := last.Status("acme"); st.State != HelperOff || !strings.Contains(logs.String(), "install.json cannot be used") {
		t.Fatalf("with a bad install record: %+v\n%s", st, logs)
	}
}

func TestHelperSpecsAreHeldToStrictNames(t *testing.T) {
	good := HelperSpec{Name: "berth-leadline", Version: "0.1.0", Args: []string{"serve", "--dir", "{dir}"}, Ready: "helper.json",
		Attach: "/api/attach", AttachEnv: []string{"DATABASE_URL", "OTEL_*"}, SHA256: sha256Hex(nil), Size: 1}
	if err := good.Validate(); err != nil {
		t.Fatalf("a good spec was refused: %v", err)
	}
	for what, change := range map[string]func(*HelperSpec){
		"a name with a slash":            func(s *HelperSpec) { s.Name = "bin/helper" },
		"a name that climbs":             func(s *HelperSpec) { s.Name = ".." },
		"a hidden name":                  func(s *HelperSpec) { s.Name = ".helper" },
		"a name in capitals":             func(s *HelperSpec) { s.Name = "Helper" },
		"no name":                        func(s *HelperSpec) { s.Name = "" },
		"a version with a slash":         func(s *HelperSpec) { s.Version = "1/2" },
		"a version that climbs":          func(s *HelperSpec) { s.Version = ".." },
		"no version":                     func(s *HelperSpec) { s.Version = "" },
		"a ready file in a folder":       func(s *HelperSpec) { s.Ready = "sub/helper.json" },
		"a ready file that climbs":       func(s *HelperSpec) { s.Ready = "../helper.json" },
		"no ready file":                  func(s *HelperSpec) { s.Ready = "" },
		"an attach path that is a URL":   func(s *HelperSpec) { s.Attach = "http://example.com/attach" },
		"an attach path that climbs":     func(s *HelperSpec) { s.Attach = "/api/../attach" },
		"an attach path with a query":    func(s *HelperSpec) { s.Attach = "/api/attach?x=1" },
		"an attach path to another host": func(s *HelperSpec) { s.Attach = "//example.com/attach" },
		"every variable":                 func(s *HelperSpec) { s.AttachEnv = []string{"*"} },
		"a variable with a star inside":  func(s *HelperSpec) { s.AttachEnv = []string{"OTEL_*_URL"} },
		"a variable that is no name":     func(s *HelperSpec) { s.AttachEnv = []string{"A=B"} },
		"too many arguments":             func(s *HelperSpec) { s.Args = make([]string, 33) },
		"an argument with a NUL":         func(s *HelperSpec) { s.Args = []string{"a\x00b"} },
		"no checksum":                    func(s *HelperSpec) { s.SHA256 = "" },
		"a checksum in capitals":         func(s *HelperSpec) { s.SHA256 = strings.ToUpper(s.SHA256) },
		"no size":                        func(s *HelperSpec) { s.Size = 0 },
		"a size past the cap":            func(s *HelperSpec) { s.Size = maxHelperSize + 1 },
	} {
		spec := good
		change(&spec)
		if err := spec.Validate(); err == nil {
			t.Errorf("%s was accepted", what)
		}
	}
	h, _ := testHelpers(t)
	for _, plugin := range []string{"", "..", "a/b", "Acme", ".acme", "a b", strings.Repeat("a", 49)} {
		if _, err := h.Install(context.Background(), plugin, good, strings.NewReader("x")); err == nil {
			t.Errorf("the plugin id %q was accepted", plugin)
		}
		if _, err := h.Remove(plugin, true); err == nil {
			t.Errorf("the plugin id %q was accepted for removal", plugin)
		}
	}
	if !good.matchesEnv("OTEL_SERVICE_NAME") || !good.matchesEnv("DATABASE_URL") || good.matchesEnv("DATABASE_URL_2") || good.matchesEnv("PATH") {
		t.Error("attach_env does not match by name and by prefix")
	}
}
