package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/hooks"
	"github.com/cosscom/shipyard/internal/identity"
	"github.com/cosscom/shipyard/internal/pairing"
	"github.com/cosscom/shipyard/internal/trust"
	"github.com/cosscom/shipyard/internal/wire"
)

// cleanupRig is an agent paired with boxes that take helpers, and a
// listing the plugin "acme" is installed and updated from. Its helper's
// builds are served by the same server.
type cleanupRig struct {
	t      *testing.T
	dir    string
	agent  *runningAgent
	token  string
	market *fakeRegistry
	logs   *bytes.Buffer
	logMu  *sync.Mutex
	// boxes by name: "devbox" always, and the others asked for.
	boxes map[string]*helperBox
	hosts map[string]*testBox
}

var helperOne = []byte("#!/bin/sh\n# the acme helper, version one\n")
var helperTwo = []byte("#!/bin/sh\n# the acme helper, version two\n")

func newCleanupRig(t *testing.T, more ...string) *cleanupRig {
	t.Helper()
	r := &cleanupRig{t: t, market: newRegistry(t), logs: &bytes.Buffer{}, logMu: &sync.Mutex{}, boxes: map[string]*helperBox{}, hosts: map[string]*testBox{}}
	r.market.serve("/releases/acme-helper-1", helperOne)
	r.market.serve("/releases/acme-helper-2", helperTwo)
	host := func(name string) *testBox {
		hb := &helperBox{platform: "linux-arm64", have: map[string]box.HelperStatus{}}
		b := newBoxWith(t, hb.mount)
		r.boxes[name], r.hosts[name] = hb, b
		return b
	}
	r.dir = host("devbox").pairLaptop()
	for _, name := range more {
		pairAlso(t, r.dir, host(name), name)
	}
	r.start()
	return r
}

// pairAlso pairs the laptop in dir with one more box, under a name.
func pairAlso(t *testing.T, dir string, b *testBox, name string) {
	t.Helper()
	id, err := identity.LoadOrCreate(filepath.Join(dir, "identity.pem"))
	if err != nil {
		t.Fatal(err)
	}
	code, err := b.server.Pending.Issue(time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	tok := pairing.Token{Address: b.address, Fingerprint: b.server.Identity.Fingerprint(), Code: code}
	if _, err := wire.Pair(context.Background(), id, tok, "laptop"); err != nil {
		t.Fatal(err)
	}
	if err := trust.NewStore(filepath.Join(dir, "boxes.json")).Add(trust.Peer{Name: name, Address: b.address, Fingerprint: tok.Fingerprint, PairedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// start runs the agent: the first one, or the next after stop.
func (r *cleanupRig) start() {
	r.t.Helper()
	r.agent = startAgentConfig(r.t, r.dir, &fakeNetworks{}, func(c *Config) {
		c.PluginRegistry = r.market.url()
		c.Log = log.New(lockedWriter{r.logMu, r.logs}, "", 0)
	})
	r.token = uiToken(r.t, r.agent)
}

// logged is what the agent has logged so far.
func (r *cleanupRig) logged() string {
	r.logMu.Lock()
	defer r.logMu.Unlock()
	return r.logs.String()
}

func (r *cleanupRig) online(name string) bool {
	s, err := r.agent.client.Status(context.Background())
	if err != nil {
		return false
	}
	for _, b := range s.Boxes {
		if b.Name == name {
			return b.State == StateOnline
		}
	}
	return false
}

func (r *cleanupRig) waitOnline(names ...string) {
	r.t.Helper()
	for _, name := range names {
		eventually(r.t, name+" online", func() bool { return r.online(name) })
	}
}

func (r *cleanupRig) send(method, path, body string) (int, string) {
	r.t.Helper()
	req, _ := http.NewRequest(method, "http://"+r.agent.ui+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// acmeManifest is the plugin's manifest at one version, with one of the
// two builds of its helper. The plugin's version and its helper's are apart,
// as they are when only the plugin's page changes.
func (r *cleanupRig) acmeManifest(plugin, helper string) string {
	program, path := helperOne, "/releases/acme-helper-1"
	if helper != "1.0.0" {
		program, path = helperTwo, "/releases/acme-helper-2"
	}
	m := map[string]any{
		"name": "Acme traces", "version": plugin, "main": "dist/index.js",
		"box": map[string]any{"helper": map[string]any{
			"name": "acme-helper", "version": helper,
			"args":  []string{"serve", "--dir", "{dir}"},
			"ready": "helper.json", "attach": "/api/attach", "detach": "/api/detach",
			"attach_env": []string{"DATABASE_URL", "OTEL_*"},
			"assets":     map[string]any{"linux-arm64": map[string]any{"url": r.market.srv.URL + path, "sha256": sum(string(program)), "size": len(program)}},
		}},
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return string(b)
}

// publish lists a version of the plugin, and install installs what is
// listed, as the app's Browse does.
func (r *cleanupRig) publish(plugin, helper string) {
	r.market.publish(r.market.plugin("acme", plugin, map[string]string{
		"berth-plugin.json": r.acmeManifest(plugin, helper),
		"dist/index.js":     "export default function activate() { /* " + plugin + " */ }\n",
	}))
}

func (r *cleanupRig) install(plugin, helper string) {
	r.t.Helper()
	r.publish(plugin, helper)
	if status, body := r.send("POST", "/v1/marketplace/acme/install", `{"version":"`+plugin+`"}`); status != 200 {
		r.t.Fatalf("installing acme %s: %d %s", plugin, status, body)
	}
}

func (r *cleanupRig) pluginDir() string { return filepath.Join(r.dir, "user", "plugins", "acme") }

// allow is the person allowing the plugin as it is now, in the app.
func (r *cleanupRig) allow() {
	r.t.Helper()
	hash, err := hooks.PluginHash(r.pluginDir())
	if err != nil {
		r.t.Fatal(err)
	}
	if status, body := r.send("POST", "/v1/plugins/acme/enable", `{"hash":"`+hash+`"}`); status != 200 {
		r.t.Fatalf("allowing acme: %d %s", status, body)
	}
}

// turnOn installs the helper on a box, as the plugin's own screen does.
func (r *cleanupRig) turnOn(name string) {
	r.t.Helper()
	if status, body := r.send("POST", "/v1/plugins/acme/helper?box="+name, ""); status != 200 || !strings.Contains(body, `"state":"running"`) {
		r.t.Fatalf("turning the helper on for %s: %d %s", name, status, body)
	}
}

// removed is what a box was asked to remove, in order.
func (r *cleanupRig) removed(name string) string {
	hb := r.boxes[name]
	hb.mu.Lock()
	defer hb.mu.Unlock()
	return strings.Join(hb.removed, " ")
}

func (r *cleanupRig) runs(name string) bool {
	hb := r.boxes[name]
	hb.mu.Lock()
	defer hb.mu.Unlock()
	return hb.have["acme"].State == box.HelperRunning
}

// ledger is helpers.json, or a zero one when there is none.
func (r *cleanupRig) ledger() helperLedgerDoc {
	var doc helperLedgerDoc
	if b, err := os.ReadFile(filepath.Join(r.dir, "helpers.json")); err == nil {
		json.Unmarshal(b, &doc)
	}
	return doc
}

// off is the answer of turning the plugin off, or of uninstalling it.
type offAnswer struct {
	Enabled bool            `json:"enabled"`
	Removed string          `json:"removed"`
	Helpers []HelperRemoved `json:"helpers"`
}

func (r *cleanupRig) off(method, path string) offAnswer {
	r.t.Helper()
	status, body := r.send(method, path, "")
	var out offAnswer
	if status != 200 || json.Unmarshal([]byte(body), &out) != nil {
		r.t.Fatalf("%s %s: %d %s", method, path, status, body)
	}
	return out
}

func (r *cleanupRig) where() PluginHelpers {
	r.t.Helper()
	status, body := r.send("GET", "/v1/plugins/acme/helpers", "")
	var out PluginHelpers
	if status != 200 || json.Unmarshal([]byte(body), &out) != nil {
		r.t.Fatalf("where the helper is: %d %s", status, body)
	}
	return out
}

func on(list []PluginHelperOn, name string) PluginHelperOn {
	for _, b := range list {
		if b.Box == name {
			return b
		}
	}
	return PluginHelperOn{}
}

func TestTurningAPluginOffStopsItsHelperOnEveryBoxThatHasIt(t *testing.T) {
	r := newCleanupRig(t, "buildbox")
	r.waitOnline("devbox", "buildbox")
	r.install("1.0.0", "1.0.0")
	r.allow()
	r.turnOn("devbox")

	// What the app says before it turns the plugin off: where it runs.
	where := r.where()
	if where.Name != "acme-helper" || where.Held || len(where.Boxes) != 2 || on(where.Boxes, "devbox").State != "running" || on(where.Boxes, "buildbox").State != "off" {
		t.Fatalf("where the helper is = %+v", where)
	}

	out := r.off("POST", "/v1/plugins/acme/disable")
	if out.Enabled || len(out.Helpers) != 2 {
		t.Fatalf("turning acme off answered %+v", out)
	}
	// Sorted by box: buildbox had none, devbox had it removed.
	if got := out.Helpers[0]; got.Box != "buildbox" || got.State != "none" {
		t.Errorf("on the box without the helper: %+v", got)
	}
	got := out.Helpers[1]
	if got.Box != "devbox" || got.State != "removed" || got.Purged {
		t.Errorf("on the box with the helper: %+v", got)
	}
	// The box says which services still point at it; so does the answer.
	if len(got.Attached) != 1 || got.Attached[0] != (box.HelperService{Location: "shop", Worktree: "fix-orders", Service: "web"}) {
		t.Errorf("the services to restart = %+v", got.Attached)
	}
	// Its data is kept: no purge. The box that never had it was not asked.
	if r.removed("devbox") != "acme?" || r.removed("buildbox") != "" || r.runs("devbox") {
		t.Fatalf("the boxes were asked to remove %q and %q", r.removed("devbox"), r.removed("buildbox"))
	}
	if doc := r.ledger(); len(doc.Granted) != 0 || len(doc.Pending) != 0 {
		t.Fatalf("with everything done the agent still keeps %+v", doc)
	}
	if !strings.Contains(r.logged(), "helper acme removed from devbox (data kept)") {
		t.Errorf("the agent's log does not say what it removed:\n%s", r.logged())
	}

	// Turned on again, nothing is installed by itself: it is off on the
	// box until the person turns it on there.
	r.allow()
	if status, body := r.send("GET", "/v1/plugins/acme/helper?box=devbox", ""); status != 200 || !strings.Contains(body, `"state":"off"`) {
		t.Fatalf("after turning the plugin back on: %d %s", status, body)
	}
	if n := len(r.boxes["devbox"].sent()); n != 1 {
		t.Fatalf("turning the plugin back on uploaded a helper: %d uploads", n)
	}
	// A plugin with no helper asks no box anything.
	allowPlugin(t, installPlugin(t, r.agent, "plain", manifestOf("Plain", "1.0.0")))
	if status, body := r.send("POST", "/v1/plugins/plain/disable", ""); status != 200 || strings.Contains(body, `"helpers"`) {
		t.Fatalf("turning off a plugin with no helper: %d %s", status, body)
	}
}

func TestUninstallingAPluginRemovesItsHelperAndItsData(t *testing.T) {
	r := newCleanupRig(t, "buildbox")
	r.waitOnline("devbox", "buildbox")
	r.install("1.0.0", "1.0.0")
	r.allow()
	r.turnOn("devbox")

	out := r.off("DELETE", "/v1/marketplace/acme")
	if out.Removed != "acme" || len(out.Helpers) != 2 {
		t.Fatalf("uninstalling acme answered %+v", out)
	}
	if got := out.Helpers[1]; got.Box != "devbox" || got.State != "removed" || !got.Purged {
		t.Errorf("on the box with the helper: %+v", got)
	}
	// Every box is asked to remove what a helper may have left there.
	if r.removed("devbox") != "acme?purge=1" || r.removed("buildbox") != "acme?purge=1" {
		t.Fatalf("the boxes were asked to remove %q and %q", r.removed("devbox"), r.removed("buildbox"))
	}
	if doc := r.ledger(); len(doc.Granted) != 0 || len(doc.Pending) != 0 {
		t.Fatalf("with everything done the agent still keeps %+v", doc)
	}

	// Turned off first, its data stayed on the box; uninstalling it later
	// takes that too, though nothing runs there any more.
	r.install("1.0.0", "1.0.0")
	r.allow()
	r.turnOn("devbox")
	r.off("POST", "/v1/plugins/acme/disable")
	out = r.off("DELETE", "/v1/marketplace/acme")
	if got := out.Helpers[1]; got.Box != "devbox" || got.State != "none" || !got.Purged {
		t.Errorf("uninstalling after turning off: %+v", got)
	}
	if got := r.removed("devbox"); got != "acme?purge=1 acme? acme?purge=1" {
		t.Fatalf("devbox was asked to remove %q", got)
	}
}

func TestABoxThatIsOfflineHasItsHelperRemovedWhenItNextConnects(t *testing.T) {
	r := newCleanupRig(t)
	r.waitOnline("devbox")
	r.install("1.0.0", "1.0.0")
	r.allow()
	r.turnOn("devbox")

	host := r.hosts["devbox"]
	addr := host.address
	host.stop()
	eventually(t, "devbox offline", func() bool { return stateOf(t, r.agent) == StateOffline })

	out := r.off("POST", "/v1/plugins/acme/disable")
	if len(out.Helpers) != 1 || out.Helpers[0].Box != "devbox" || out.Helpers[0].State != "pending" {
		t.Fatalf("turning acme off with its box away answered %+v", out)
	}
	// Written down, for this agent and the next.
	doc := r.ledger()
	if len(doc.Pending) != 1 || doc.Pending[0].Plugin != "acme" || doc.Pending[0].Box != "devbox" || doc.Pending[0].Purge {
		t.Fatalf("helpers.json = %+v", doc)
	}
	if where := r.where(); on(where.Boxes, "devbox").State != "offline" || on(where.Boxes, "devbox").Pending != "remove" {
		t.Fatalf("where the helper is, with its box away = %+v", where)
	}
	// Turning the plugin back on does not call it off: the helper is off
	// everywhere until it is turned on for a box again.
	r.allow()
	// Uninstalled meanwhile: its data is to go as well.
	r.off("DELETE", "/v1/marketplace/acme")
	if doc = r.ledger(); len(doc.Pending) != 1 || !doc.Pending[0].Purge {
		t.Fatalf("helpers.json after uninstalling = %+v", doc)
	}

	host.start(addr)
	eventually(t, "the helper to be removed once devbox is back", func() bool { return r.removed("devbox") == "acme?purge=1" })
	eventually(t, "the removal to be struck off", func() bool { return len(r.ledger().Pending) == 0 })
	if r.runs("devbox") {
		t.Fatal("the helper still runs on the box")
	}
}

func TestARemovalStillToDoIsDoneByTheNextAgent(t *testing.T) {
	r := newCleanupRig(t)
	r.waitOnline("devbox")
	r.install("1.0.0", "1.0.0")
	r.allow()
	r.turnOn("devbox")

	host := r.hosts["devbox"]
	addr := host.address
	host.stop()
	eventually(t, "devbox offline", func() bool { return stateOf(t, r.agent) == StateOffline })
	if out := r.off("POST", "/v1/plugins/acme/disable"); len(out.Helpers) != 1 || out.Helpers[0].State != "pending" {
		t.Fatalf("turning acme off with its box away answered %+v", out)
	}

	// The agent stops (an update, a restart of the computer) before the
	// box comes back.
	r.agent.stop()
	if doc := r.ledger(); len(doc.Pending) != 1 {
		t.Fatalf("helpers.json after the agent stopped = %+v", doc)
	}
	host.start(addr)
	r.start()
	eventually(t, "the next agent to remove the helper", func() bool { return r.removed("devbox") == "acme?" })
	eventually(t, "the removal to be struck off", func() bool { return len(r.ledger().Pending) == 0 })
}

func TestARemovalForABoxThatWasForgottenIsLetGo(t *testing.T) {
	r := newCleanupRig(t)
	r.waitOnline("devbox")
	r.install("1.0.0", "1.0.0")
	r.allow()
	r.turnOn("devbox")

	host := r.hosts["devbox"]
	addr := host.address
	host.stop()
	eventually(t, "devbox offline", func() bool { return stateOf(t, r.agent) == StateOffline })
	r.off("POST", "/v1/plugins/acme/disable")
	if len(r.ledger().Pending) != 1 {
		t.Fatal("nothing was written down")
	}

	// The person forgets the box, as `berth forget` does.
	if _, err := trust.NewStore(filepath.Join(r.dir, "boxes.json")).Remove("devbox"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the removal to be let go", func() bool {
		// The app looking at its plugins has the agent look too.
		r.send("GET", "/v1/plugins", "")
		return len(r.ledger().Pending) == 0
	})
	if !strings.Contains(r.logged(), "devbox is no longer paired") {
		t.Errorf("the agent's log does not say it let the removal go:\n%s", r.logged())
	}
	// And the box, back and unpaired, is asked nothing.
	host.start(addr)
	time.Sleep(300 * time.Millisecond)
	if got := r.removed("devbox"); got != "" {
		t.Fatalf("a forgotten box was asked to remove %q", got)
	}
}

func TestAnUpdateHoldsAHelperThatIsTheSameAndRemovesOneThatChanged(t *testing.T) {
	r := newCleanupRig(t)
	r.waitOnline("devbox")
	r.install("1.0.0", "1.0.0")
	r.allow()
	r.turnOn("devbox")

	// The plugin is updated, and its helper is the one that runs: the
	// plugin is off until the person allows it, and the helper is held.
	r.install("1.1.0", "1.0.0")
	if status, body := r.send("GET", "/v1/plugins/acme/helper?box=devbox", ""); status != 409 || !strings.Contains(body, `"code":"plugin_not_allowed"`) {
		t.Fatalf("the plugin is still on after an update: %d %s", status, body)
	}
	if where := r.where(); !where.Held || on(where.Boxes, "devbox").State != "running" {
		t.Fatalf("after an update with the same helper = %+v", where)
	}
	if r.removed("devbox") != "" || len(r.ledger().Granted) != 1 {
		t.Fatalf("a helper that did not change was removed (%q), or let go: %+v", r.removed("devbox"), r.ledger())
	}
	// Allowed: nothing happened to the helper in between.
	r.allow()
	if status, body := r.send("GET", "/v1/plugins/acme/helper?box=devbox", ""); status != 200 || !strings.Contains(body, `"state":"running"`) || strings.Contains(body, `"update":true`) {
		t.Fatalf("the helper after the update was allowed: %d %s", status, body)
	}
	if r.removed("devbox") != "" || r.where().Held {
		t.Fatal("allowing the update removed the helper, or still holds it")
	}

	// An update whose helper is another: the one on the boxes is no longer
	// what any allowed plugin names, so it goes. Its data stays.
	r.install("2.0.0", "2.0.0")
	eventually(t, "the helper the update replaced to be removed", func() bool { return r.removed("devbox") == "acme?" })
	eventually(t, "the agent to let go of it", func() bool { doc := r.ledger(); return len(doc.Granted) == 0 && len(doc.Pending) == 0 })
	if !strings.Contains(r.logged(), "helper acme: its plugin changed, and names another helper") {
		t.Errorf("the agent's log does not say why:\n%s", r.logged())
	}
	// The new version, allowed, installs nothing by itself.
	r.allow()
	if n := len(r.boxes["devbox"].sent()); n != 1 {
		t.Fatalf("allowing the update uploaded a helper: %d uploads", n)
	}

	// Held, and then turned off rather than allowed: removed.
	r.turnOn("devbox")
	r.install("2.1.0", "2.0.0")
	if !r.where().Held || r.removed("devbox") != "acme?" {
		t.Fatalf("after the second update with the same helper: held %v, removed %q", r.where().Held, r.removed("devbox"))
	}
	out := r.off("POST", "/v1/plugins/acme/disable")
	if len(out.Helpers) != 1 || out.Helpers[0].State != "removed" || r.removed("devbox") != "acme? acme?" {
		t.Fatalf("turning off a plugin whose helper was held: %+v, removed %q", out, r.removed("devbox"))
	}
}

// What a plugin's state means for its helper, decided with no box in the
// way: a grant that is kept is a helper left on the boxes, one that is gone
// is a helper removed from them.
func TestWhichChangesToAPluginTakeItsHelperAway(t *testing.T) {
	helper := func(version string) string {
		return `{"name": "acme-helper", "version": "` + version + `", "args": ["serve"], "ready": "helper.json",
			"assets": {"linux-arm64": {"url": "https://example.com/acme-helper", "sha256": "` + sum("helper "+version) + `", "size": 12}}}`
	}
	manifest := func(description, box string) string {
		if box != "" {
			box = `, "box": {"helper": ` + box + `}`
		}
		return `{"name": "Acme", "version": "1.0.0", "description": "` + description + `", "main": "dist/index.js"` + box + `}`
	}
	for _, c := range []struct {
		name   string
		change func(t *testing.T, dir string)
		// kept: the helper stays on the boxes; on: the plugin is still on.
		kept, on bool
		why      string
	}{
		{name: "nothing changed", change: func(*testing.T, string) {}, kept: true, on: true},
		{name: "its code was edited", kept: true, change: func(t *testing.T, dir string) {
			write(t, filepath.Join(dir, "dist", "index.js"), "export default function activate() { /* edited */ }\n")
		}},
		{name: "its manifest was edited, the helper in it was not", kept: true, change: func(t *testing.T, dir string) {
			write(t, filepath.Join(dir, hooks.PluginManifest), manifest("says more", helper("1.0.0")))
		}},
		{name: "its manifest names another build of the helper", why: "names another helper", change: func(t *testing.T, dir string) {
			write(t, filepath.Join(dir, hooks.PluginManifest), manifest("", helper("1.0.1")))
		}},
		{name: "its manifest names no helper any more", why: "no longer brings a helper", change: func(t *testing.T, dir string) {
			write(t, filepath.Join(dir, hooks.PluginManifest), manifest("", ""))
		}},
		{name: "its folder was deleted", why: "folder is gone", change: func(t *testing.T, dir string) {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "its permission was taken away by hand", why: "its plugin is off", change: func(t *testing.T, dir string) {
			if err := hooks.ForbidPlugin(dir); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "it was marked disabled by hand", why: "its plugin is off", change: func(t *testing.T, dir string) {
			write(t, filepath.Join(dir, "disabled"), "")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			state, user := t.TempDir(), filepath.Join(t.TempDir(), "user")
			logs := &bytes.Buffer{}
			a := &Agent{
				cfg:     Config{Dir: state, UserDir: user, Now: time.Now, Log: log.New(logs, "", 0)},
				boxes:   trust.NewStore(filepath.Join(state, "boxes.json")),
				clients: map[string]*boxState{},
			}
			a.ctx = context.Background()
			dir := filepath.Join(user, "plugins", "acme")
			write(t, filepath.Join(dir, hooks.PluginManifest), manifest("", helper("1.0.0")))
			write(t, filepath.Join(dir, "dist", "index.js"), "export default function activate() {}\n")
			allowPlugin(t, dir)
			// Found on, with a helper: from here it is the agent's to remove.
			a.sweepHelperGrants()
			if _, ok := a.ledger().grant("acme"); !ok {
				t.Fatal("a plugin that is on with a helper was not noted")
			}

			c.change(t, dir)
			a.sweepHelperGrants()
			if _, kept := a.ledger().grant("acme"); kept != c.kept {
				t.Fatalf("the helper was kept = %v, want %v\n%s", kept, c.kept, logs)
			}
			if c.why != "" && !strings.Contains(logs.String(), c.why) {
				t.Errorf("the log does not say why (%q):\n%s", c.why, logs)
			}
			if on := hooks.PluginEnabled(dir); on != c.on {
				t.Fatalf("the plugin is on = %v, want %v", on, c.on)
			}
			// Looked at again, the answer is the same.
			a.sweepHelperGrants()
			if _, kept := a.ledger().grant("acme"); kept != c.kept {
				t.Fatalf("looked at again, the helper was kept = %v, want %v", kept, c.kept)
			}
			// And the next agent reads what this one wrote.
			next := &Agent{cfg: a.cfg}
			if _, kept := next.ledger().grant("acme"); kept != c.kept {
				t.Fatalf("as the next agent reads it, the helper was kept = %v, want %v", kept, c.kept)
			}
		})
	}
}

// Turning the helper on for a box is the person's newer word: a removal
// still waiting for that box is called off.
func TestTurningAHelperOnCallsOffARemovalStillWaiting(t *testing.T) {
	r := newCleanupRig(t)
	r.waitOnline("devbox")
	r.install("1.0.0", "1.0.0")
	r.allow()
	r.turnOn("devbox")

	// The box is there but in trouble: it could not remove the helper. That
	// is not a refusal, so the removal stays on the list for another try.
	hb := r.boxes["devbox"]
	hb.mu.Lock()
	hb.failRemove = true
	hb.mu.Unlock()
	out := r.off("POST", "/v1/plugins/acme/disable")
	if len(out.Helpers) != 1 || out.Helpers[0].State != "pending" || !strings.Contains(out.Helpers[0].Error, "could not be removed") {
		t.Fatalf("turning acme off on a box in trouble answered %+v", out)
	}
	if len(r.ledger().Pending) != 1 || !r.runs("devbox") {
		t.Fatalf("helpers.json = %+v", r.ledger())
	}

	// The person turns the plugin on again, and its helper on for the box.
	r.allow()
	r.turnOn("devbox")
	if doc := r.ledger(); len(doc.Pending) != 0 || len(doc.Granted) != 1 {
		t.Fatalf("after the helper was turned on again, helpers.json = %+v", doc)
	}
	// The box is well again, and the agent looks: there is nothing to do.
	hb.mu.Lock()
	hb.failRemove = false
	hb.mu.Unlock()
	r.send("GET", "/v1/plugins", "")
	time.Sleep(300 * time.Millisecond)
	if r.removed("devbox") != "" || !r.runs("devbox") {
		t.Fatalf("a removal that was called off was done: %q", r.removed("devbox"))
	}
}
