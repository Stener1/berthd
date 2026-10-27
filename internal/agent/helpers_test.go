package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/hooks"
	"github.com/cosscom/shipyard/internal/wire"
)

// helperBox is a box's helper routes as a laptop sees them: it keeps what
// it is sent, and says what it then has.
type helperBox struct {
	mu       sync.Mutex
	platform string
	have     map[string]box.HelperStatus
	// uploads are the programs sent, each with the spec it came with.
	uploads []helperUpload
	// removed is each removal asked for, with its query (purge or not).
	removed []string
	// failRemove has the box answer a removal with an error of its own.
	failRemove bool
}

type helperUpload struct {
	plugin  string
	spec    box.HelperSpec
	program []byte
}

func (hb *helperBox) mount(s *wire.Server) {
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	status := func(plugin string) box.HelperStatus {
		if st, ok := hb.have[plugin]; ok {
			return st
		}
		return box.HelperStatus{Plugin: plugin, State: box.HelperOff, Platform: hb.platform}
	}
	s.Handle("GET /v1/helpers/{plugin}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hb.mu.Lock()
		defer hb.mu.Unlock()
		reply(w, status(r.PathValue("plugin")))
	}))
	s.Handle("PUT /v1/helpers/{plugin}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hb.mu.Lock()
		defer hb.mu.Unlock()
		plugin := r.PathValue("plugin")
		var spec box.HelperSpec
		raw, _ := base64.StdEncoding.DecodeString(r.Header.Get(box.HelperSpecHeader))
		json.Unmarshal(raw, &spec)
		program, _ := io.ReadAll(r.Body)
		hb.uploads = append(hb.uploads, helperUpload{plugin: plugin, spec: spec, program: program})
		// As a box does: no program is a start of the one it holds.
		if len(program) == 0 && hb.have[plugin].SHA256 != spec.SHA256 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"error":"this box does not hold it","code":"helper_missing"}`))
			return
		}
		hb.have[plugin] = box.HelperStatus{Plugin: plugin, State: box.HelperRunning, Platform: hb.platform,
			Name: spec.Name, Version: spec.Version, SHA256: spec.SHA256, Digest: spec.Digest(), PID: 4242}
		reply(w, hb.have[plugin])
	}))
	s.Handle("DELETE /v1/helpers/{plugin}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hb.mu.Lock()
		defer hb.mu.Unlock()
		plugin := r.PathValue("plugin")
		if hb.failRemove {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"its folder could not be removed","code":"internal"}`))
			return
		}
		_, had := hb.have[plugin]
		delete(hb.have, plugin)
		hb.removed = append(hb.removed, plugin+"?"+r.URL.RawQuery)
		st := status(plugin)
		if had {
			// A service that was started with what the helper answered.
			st.Attached = []box.HelperService{{Location: "shop", Worktree: "fix-orders", Service: "web"}}
		}
		reply(w, st)
	}))
}

func (hb *helperBox) sent() []helperUpload {
	hb.mu.Lock()
	defer hb.mu.Unlock()
	return append([]helperUpload{}, hb.uploads...)
}

// helperManifest is a plugin's manifest with a helper whose build for
// linux-arm64 is program, at url.
func helperManifest(version, url string, program []byte, change func(asset map[string]any)) string {
	asset := map[string]any{"url": url, "sha256": sum(string(program)), "size": len(program)}
	if change != nil {
		change(asset)
	}
	m := map[string]any{
		"name": "Acme traces", "version": version, "main": "dist/index.js",
		"box": map[string]any{"helper": map[string]any{
			"name": "acme-helper", "version": version,
			"args":  []string{"serve", "--dir", "{dir}", "--addr", "127.0.0.1:0"},
			"ready": "helper.json", "attach": "/api/attach",
			"attach_env": []string{"DATABASE_URL", "OTEL_*"},
			"assets":     map[string]any{"linux-arm64": asset, "linux-amd64": map[string]any{}},
		}},
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return string(b)
}

// installPlugin writes a plugin into the agent's plugins folder, as the
// marketplace or a person does; allowPlugin is the person allowing it.
func installPlugin(t *testing.T, a *runningAgent, id, manifest string) string {
	t.Helper()
	dir := filepath.Join(a.dir, "user", "plugins", id)
	write(t, filepath.Join(dir, hooks.PluginManifest), manifest)
	write(t, filepath.Join(dir, "dist", "index.js"), "export default function activate() {}\n")
	return dir
}

func allowPlugin(t *testing.T, dir string) {
	t.Helper()
	hash, err := hooks.PluginHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := hooks.AllowPlugin(dir, hash); err != nil {
		t.Fatal(err)
	}
}

// helperRig is an agent paired with a box that takes helpers, and a server
// the helper's builds are downloaded from.
type helperRig struct {
	box    *helperBox
	agent  *runningAgent
	token  string
	assets *fakeRegistry
	logs   *bytes.Buffer
}

func newHelperRig(t *testing.T, mount bool) *helperRig {
	t.Helper()
	hb := &helperBox{platform: "linux-arm64", have: map[string]box.HelperStatus{}}
	b := newBoxWith(t, func(s *wire.Server) {
		if mount {
			hb.mount(s)
		}
	})
	logs := &bytes.Buffer{}
	var mu sync.Mutex
	a := startAgentConfig(t, b.pairLaptop(), &fakeNetworks{}, func(c *Config) {
		c.Log = log.New(lockedWriter{&mu, logs}, "", 0)
	})
	r := &helperRig{box: hb, agent: a, token: uiToken(t, a), assets: newRegistry(t), logs: logs}
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })
	return r
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// call makes one of the helper's requests and reads the status it answers.
func (r *helperRig) call(t *testing.T, method, path string) (int, HelperStatus, string) {
	t.Helper()
	resp, body := uiCall(t, r.agent, method, path, r.token)
	var st HelperStatus
	json.Unmarshal([]byte(body), &st)
	return resp.StatusCode, st, body
}

func TestAPluginsHelperIsInstalledOnABoxFromItsManifest(t *testing.T) {
	r := newHelperRig(t, true)
	program := []byte("#!/bin/sh\n# the acme helper, version one\n")
	r.assets.serve("/releases/acme-helper-linux-arm64", program)
	url := r.assets.srv.URL + "/releases/acme-helper-linux-arm64"
	dir := installPlugin(t, r.agent, "acme", helperManifest("1.0.0", url, program, nil))
	const path = "/v1/plugins/acme/helper?box=devbox"

	// Installed but not allowed: it gets no helper, and nothing is fetched.
	for _, method := range []string{"GET", "POST"} {
		if status, _, body := r.call(t, method, path); status != 409 || !strings.Contains(body, `"code":"plugin_not_allowed"`) {
			t.Fatalf("%s before the plugin is allowed: %d %s", method, status, body)
		}
	}
	if status, _, body := r.call(t, "POST", "/v1/plugins/nothing-here/helper?box=devbox"); status != 409 || !strings.Contains(body, `"code":"plugin_not_installed"`) {
		t.Fatalf("a plugin that is not installed: %d %s", status, body)
	}
	if r.assets.asked("/releases/acme-helper-linux-arm64") != 0 || len(r.box.sent()) != 0 {
		t.Fatal("a helper was fetched or uploaded for a plugin that is not allowed")
	}

	allowPlugin(t, dir)
	status, st, body := r.call(t, "GET", path)
	if status != 200 || !reflect.DeepEqual(st, HelperStatus{Plugin: "acme", Box: "devbox", State: "off", Platform: "linux-arm64", Latest: "1.0.0"}) {
		t.Fatalf("status before install: %d %s", status, body)
	}

	// Turned on: downloaded here, checked, and uploaded to the box with the
	// manifest's own words.
	status, st, body = r.call(t, "POST", path)
	if status != 200 || !reflect.DeepEqual(st, HelperStatus{Plugin: "acme", Box: "devbox", State: "running", Platform: "linux-arm64", Version: "1.0.0", Latest: "1.0.0"}) {
		t.Fatalf("install: %d %s", status, body)
	}
	sent := r.box.sent()
	if len(sent) != 1 || sent[0].plugin != "acme" || !bytes.Equal(sent[0].program, program) {
		t.Fatalf("the box was sent %d uploads", len(sent))
	}
	spec := sent[0].spec
	if spec.Name != "acme-helper" || spec.Version != "1.0.0" || spec.SHA256 != sum(string(program)) || spec.Size != int64(len(program)) ||
		strings.Join(spec.Args, " ") != "serve --dir {dir} --addr 127.0.0.1:0" || spec.Ready != "helper.json" || spec.Attach != "/api/attach" ||
		strings.Join(spec.AttachEnv, ",") != "DATABASE_URL,OTEL_*" {
		t.Fatalf("the spec the box was sent = %+v", spec)
	}
	if status, st, _ = r.call(t, "GET", path); status != 200 || st.State != "running" || st.Update {
		t.Fatalf("status after install = %+v", st)
	}
	// Asked again, there is nothing to do: no download, no upload.
	if status, st, body = r.call(t, "POST", path); status != 200 || st.State != "running" {
		t.Fatalf("install again: %d %s", status, body)
	}
	if r.assets.asked("/releases/acme-helper-linux-arm64") != 1 || len(r.box.sent()) != 1 {
		t.Fatalf("installing what is there fetched %d times and uploaded %d times", r.assets.asked("/releases/acme-helper-linux-arm64"), len(r.box.sent()))
	}

	// The plugin updates: its manifest names a new build. Until the person
	// allows the new manifest, its helper is not touched.
	next := []byte("#!/bin/sh\n# the acme helper, version two\n")
	r.assets.serve("/releases/acme-helper-2-linux-arm64", next)
	installPlugin(t, r.agent, "acme", helperManifest("1.1.0", r.assets.srv.URL+"/releases/acme-helper-2-linux-arm64", next, nil))
	if status, _, body = r.call(t, "POST", path); status != 409 || !strings.Contains(body, `"code":"plugin_not_allowed"`) {
		t.Fatalf("install after the plugin changed: %d %s", status, body)
	}
	allowPlugin(t, dir)
	status, st, body = r.call(t, "GET", path)
	if status != 200 || !reflect.DeepEqual(st, HelperStatus{Plugin: "acme", Box: "devbox", State: "running", Platform: "linux-arm64", Version: "1.0.0", Latest: "1.1.0", Update: true}) {
		t.Fatalf("status with an update waiting: %d %s", status, body)
	}
	status, st, body = r.call(t, "POST", path)
	if status != 200 || st.Version != "1.1.0" || st.Update || st.State != "running" {
		t.Fatalf("update: %d %s", status, body)
	}
	if sent = r.box.sent(); len(sent) != 2 || !bytes.Equal(sent[1].program, next) || sent[1].spec.Version != "1.1.0" {
		t.Fatalf("the update sent %d uploads", len(sent))
	}

	// Turned off with its data kept, then removed for good. A plugin that
	// is off can still have its helper taken away.
	status, st, body = r.call(t, "DELETE", path)
	if status != 200 || st.State != "off" || st.Latest != "1.1.0" {
		t.Fatalf("remove: %d %s", status, body)
	}
	// The box says which services still point at the helper; so does this.
	if len(st.Attached) != 1 || st.Attached[0] != (box.HelperService{Location: "shop", Worktree: "fix-orders", Service: "web"}) {
		t.Fatalf("the services to restart after a remove = %+v", st.Attached)
	}
	if err := hooks.ForbidPlugin(dir); err != nil {
		t.Fatal(err)
	}
	if status, st, body = r.call(t, "DELETE", path+"&purge=1"); status != 200 || st.State != "off" || len(st.Attached) != 0 {
		t.Fatalf("purge for a plugin that is off: %d %s", status, body)
	}
	r.box.mu.Lock()
	removed := strings.Join(r.box.removed, " ")
	r.box.mu.Unlock()
	if removed != "acme? acme?purge=1" {
		t.Fatalf("the box was asked to remove %q", removed)
	}
	if !strings.Contains(r.logs.String(), "helper acme 1.1.0 (linux-arm64) installed on devbox") {
		t.Fatalf("the agent's log does not say what it installed:\n%s", r.logs)
	}
}

func TestAHelperThatIsNotWhatTheManifestSaysIsNeverUploaded(t *testing.T) {
	r := newHelperRig(t, true)
	program := []byte("#!/bin/sh\n# the acme helper\n")
	const path = "/v1/plugins/acme/helper?box=devbox"
	try := func(what, manifest string, wantStatus int, wantCode string) string {
		t.Helper()
		allowPlugin(t, installPlugin(t, r.agent, "acme", manifest))
		status, _, body := r.call(t, "POST", path)
		if status != wantStatus || !strings.Contains(body, `"code":"`+wantCode+`"`) {
			t.Fatalf("%s: %d %s, want %d %s", what, status, body, wantStatus, wantCode)
		}
		if n := len(r.box.sent()); n != 0 {
			t.Fatalf("%s: %d uploads reached the box", what, n)
		}
		return body
	}
	url := r.assets.srv.URL + "/releases/acme-helper"

	// Other bytes than the manifest's checksum names.
	r.assets.serve("/releases/acme-helper", []byte("#!/bin/sh\n# something else\n"))
	try("other bytes of the same size", helperManifest("1.0.0", url, program, func(a map[string]any) { a["size"] = len("#!/bin/sh\n# something else\n") }), 502, "helper_mismatch")
	// More bytes than the manifest's size.
	r.assets.serve("/releases/acme-helper", append(append([]byte{}, program...), "and more"...))
	try("a longer download", helperManifest("1.0.0", url, program, nil), 502, "helper_mismatch")
	// Nothing there.
	body := try("a missing download", helperManifest("1.0.0", r.assets.srv.URL+"/releases/gone?token=sekret-acme-token", program, nil), 502, "helper_download")
	if strings.Contains(body, "sekret-acme-token") || strings.Contains(r.logs.String(), "sekret-acme-token") {
		t.Fatalf("an address's query was repeated: %s\n%s", body, r.logs)
	}

	// Addresses that are never fetched: plain http somewhere else, one with
	// a password in it, a file.
	r.assets.serve("/releases/acme-helper", program)
	before := r.assets.asked("/releases/acme-helper")
	for what, address := range map[string]string{
		"plain http":            "http://downloads.acme.example/acme-helper",
		"a password in the URL": strings.Replace(url, "http://", "http://user:pw@", 1),
		"a file":                "file:///etc/passwd",
		"no address":            "releases/acme-helper",
	} {
		try(what, helperManifest("1.0.0", address, program, nil), 409, "helper_invalid")
	}
	// A manifest whose helper is not well formed at all.
	for what, change := range map[string]func(map[string]any){
		"no checksum":    func(a map[string]any) { a["sha256"] = "" },
		"a size of zero": func(a map[string]any) { a["size"] = 0 },
		"a huge size":    func(a map[string]any) { a["size"] = 1 << 40 },
	} {
		try(what, helperManifest("1.0.0", url, program, change), 409, "helper_invalid")
	}
	bad := strings.Replace(helperManifest("1.0.0", url, program, nil), `"name": "acme-helper"`, `"name": "../../bin/sh"`, 1)
	try("a name that is a path", bad, 409, "helper_invalid")
	if r.assets.asked("/releases/acme-helper") != before {
		t.Fatal("a helper that could not be used was downloaded anyway")
	}

	// And with the manifest's own bytes there, it installs.
	allowPlugin(t, installPlugin(t, r.agent, "acme", helperManifest("1.0.0", url, program, nil)))
	if status, st, body := r.call(t, "POST", path); status != 200 || st.State != "running" {
		t.Fatalf("install of the right bytes: %d %s", status, body)
	}
}

func TestABoxThatCannotHaveAHelperIsSaidToBeUnsupported(t *testing.T) {
	program := []byte("#!/bin/sh\n# the acme helper\n")

	// A box whose berthd is older than helpers: it has no such routes.
	old := newHelperRig(t, false)
	old.assets.serve("/releases/acme-helper", program)
	url := old.assets.srv.URL + "/releases/acme-helper"
	allowPlugin(t, installPlugin(t, old.agent, "acme", helperManifest("1.0.0", url, program, nil)))
	status, st, body := old.call(t, "GET", "/v1/plugins/acme/helper?box=devbox")
	if status != 200 || st.State != "unsupported" || st.Latest != "1.0.0" || !strings.Contains(st.Error, "older berthd") {
		t.Fatalf("status on an older box: %d %s", status, body)
	}
	if status, _, body = old.call(t, "POST", "/v1/plugins/acme/helper?box=devbox"); status != 409 || !strings.Contains(body, `"code":"helper_unsupported"`) {
		t.Fatalf("install on an older box: %d %s", status, body)
	}
	if status, st, body = old.call(t, "DELETE", "/v1/plugins/acme/helper?box=devbox"); status != 200 || st.State != "unsupported" {
		t.Fatalf("remove on an older box: %d %s", status, body)
	}
	if old.assets.asked("/releases/acme-helper") != 0 {
		t.Fatal("a helper was downloaded for a box that cannot run it")
	}

	// A box of a platform the plugin has no build for.
	r := newHelperRig(t, true)
	r.box.platform = "darwin-amd64"
	allowPlugin(t, installPlugin(t, r.agent, "acme", helperManifest("1.0.0", url, program, nil)))
	status, st, body = r.call(t, "GET", "/v1/plugins/acme/helper?box=devbox")
	if status != 200 || st.State != "unsupported" || st.Platform != "darwin-amd64" || !strings.Contains(st.Error, "no build for darwin-amd64") {
		t.Fatalf("status on a platform with no build: %d %s", status, body)
	}
	if status, _, body = r.call(t, "POST", "/v1/plugins/acme/helper?box=devbox"); status != 409 || !strings.Contains(body, `"code":"helper_unsupported"`) {
		t.Fatalf("install on a platform with no build: %d %s", status, body)
	}
	// An asset that is there in name only ({}), as for linux-amd64 here.
	r.box.platform = "linux-amd64"
	if status, st, body = r.call(t, "GET", "/v1/plugins/acme/helper?box=devbox"); status != 200 || st.State != "unsupported" {
		t.Fatalf("status for an empty asset: %d %s", status, body)
	}

	// A plugin with no helper, a box that is not paired, and no box named.
	allowPlugin(t, installPlugin(t, r.agent, "plain", manifestOf("Plain", "1.0.0")))
	status, st, body = r.call(t, "GET", "/v1/plugins/plain/helper?box=devbox")
	if status != 200 || !reflect.DeepEqual(st, HelperStatus{Plugin: "plain", Box: "devbox", State: "none"}) || !strings.Contains(body, `"platform":""`) {
		t.Fatalf("a plugin with no helper: %d %s", status, body)
	}
	if status, _, body = r.call(t, "POST", "/v1/plugins/plain/helper?box=devbox"); status != 409 || !strings.Contains(body, `"code":"helper_none"`) {
		t.Fatalf("install for a plugin with no helper: %d %s", status, body)
	}
	if status, _, body = r.call(t, "GET", "/v1/plugins/acme/helper?box=nobox"); status != 404 || !strings.Contains(body, `"code":"box_unknown"`) {
		t.Fatalf("a box that is not paired: %d %s", status, body)
	}
	if status, _, body = r.call(t, "GET", "/v1/plugins/acme/helper"); status != 400 {
		t.Fatalf("no box named: %d %s", status, body)
	}
	if status, _, body = r.call(t, "GET", "/v1/plugins/Not%20An%20Id/helper?box=devbox"); status != 400 {
		t.Fatalf("a bad plugin id: %d %s", status, body)
	}
	// The plugin's files are still served beside the helper's routes.
	if resp, body := uiCall(t, r.agent, "GET", "/v1/plugins/acme/dist/index.js", r.token); resp.StatusCode != 200 || !strings.Contains(body, "activate") {
		t.Fatalf("a plugin's file: %d %s", resp.StatusCode, body)
	}
}

// A helper that asks for a token answers 401 to a box whose berthd sends
// none, and that berthd relays the answer as it came. It must reach the app
// as the helper's refusal: to the laptop's connection a 401 otherwise means
// the box no longer trusts it.
func TestAHelpersOwnRefusalIsNotTheBoxDistrustingTheLaptop(t *testing.T) {
	refuse := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"code":"unauthorized","message":"This route needs the helper's token."}}`)
	})
	b := newBoxWith(t, func(s *wire.Server) {
		s.Handle("/v1/helpers/{plugin}/api/{path...}", refuse)
		// Anything else of the box's that answers 401 is the box's word.
		s.Handle("GET /v1/sessions", refuse)
	})
	a := startAgent(t, b.pairLaptop())
	token := uiToken(t, a)
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })
	said := func(body string) (out struct{ Error, Code string }) {
		json.Unmarshal([]byte(body), &out)
		return out
	}

	for _, path := range []string{"/v1/boxes/devbox/api/helpers/database/api/api/db/connections?source=acme%2Ffix-orders", "/v1/boxes/devbox/api/helpers/database/api/api/db/c_1/schema"} {
		resp, body := uiCall(t, a, "GET", path, token)
		got := said(body)
		if resp.StatusCode != http.StatusUnauthorized || got.Code != "unauthorized" {
			t.Fatalf("%s: %d %s, want the helper's 401 as code unauthorized", path, resp.StatusCode, body)
		}
		if !strings.Contains(got.Error, "database helper on devbox") || !strings.Contains(got.Error, "update the box") || strings.Contains(got.Error, "pair again") {
			t.Errorf("%s: it says %q", path, got.Error)
		}
	}
	// Nothing about the box changed: it is online, and trusted.
	if s := stateOf(t, a); s != StateOnline {
		t.Errorf("the box is %s after a helper's refusal, want it online", s)
	}

	// A 401 from any other route of the box is still the box's own.
	if resp, body := uiCall(t, a, "GET", "/v1/boxes/devbox/api/sessions", token); resp.StatusCode != http.StatusBadGateway || said(body).Code != "box_unreachable" || !strings.Contains(body, "no longer trusts") {
		t.Errorf("another route's 401: %d %s, want the box's distrust", resp.StatusCode, body)
	}

	// And a box that really stopped trusting this laptop says so on a
	// helper's route too: its own 401 is not taken for the helper's.
	if _, err := b.server.Clients.Remove("laptop"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "untrusted", func() bool { return stateOf(t, a) == StateUntrusted })
	if resp, body := uiCall(t, a, "GET", "/v1/boxes/devbox/api/helpers/database/api/api/db/connections", token); resp.StatusCode == http.StatusUnauthorized || said(body).Code == "unauthorized" {
		t.Errorf("a revoked laptop's request of a helper: %d %s, want the box's distrust, not a helper's refusal", resp.StatusCode, body)
	}
}

func TestOnlyAHelpersRelayIsTakenForOne(t *testing.T) {
	for target, want := range map[string]string{
		"/v1/helpers/database/api/api/db/connections?source=a": "database",
		"/v1/helpers/leadline/api/v1/traces":                   "leadline",
		"/v1/helpers/database/api":                             "database",
		"/v1/helpers/database":                                 "",
		"/v1/helpers/database/log":                             "",
		"/v1/helpers":                                          "",
		"/v1/helpers//api/x":                                   "",
		"/v1/sessions?x=/v1/helpers/database/api/":             "",
	} {
		plugin, ok := helperRelayed(target)
		if ok != (want != "") || (ok && plugin != want) {
			t.Errorf("helperRelayed(%q) = %q, %v; want %q", target, plugin, ok, want)
		}
	}
}
