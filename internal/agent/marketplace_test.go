package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/hooks"
)

// fakeRegistry is a plugin listing on this computer: an index.json and the
// files it lists, as site/plugins/ is on the public site.
type fakeRegistry struct {
	srv   *httptest.Server
	mu    sync.Mutex
	files map[string][]byte
	hits  map[string]int
}

func newRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	r := &fakeRegistry{files: map[string][]byte{}, hits: map[string]int{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		b, ok := r.files[req.URL.Path]
		r.hits[req.URL.Path]++
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *fakeRegistry) url() string { return r.srv.URL + "/plugins/index.json" }

func (r *fakeRegistry) serve(path string, body []byte) {
	r.mu.Lock()
	r.files[path] = body
	r.mu.Unlock()
}

func (r *fakeRegistry) asked(path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hits[path]
}

func sum(b string) string {
	s := sha256.Sum256([]byte(b))
	return hex.EncodeToString(s[:])
}

func manifestOf(name, version string) string {
	return `{"name":"` + name + `","version":"` + version + `","main":"dist/index.js"}`
}

// plugin serves a plugin's files and returns its entry for the listing, for
// a test to change before it is published.
func (r *fakeRegistry) plugin(id, version string, files map[string]string) map[string]any {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	list := []map[string]any{}
	for _, p := range paths {
		rel := id + "/" + version + "/" + p
		r.serve("/plugins/"+rel, []byte(files[p]))
		list = append(list, map[string]any{"path": p, "url": rel, "sha256": sum(files[p]), "size": len(files[p])})
	}
	return map[string]any{"id": id, "name": "Acme " + id, "description": "A plugin for tests.", "version": version, "author": "Acme", "homepage": "https://example.com/" + id, "files": list}
}

func (r *fakeRegistry) publish(plugins ...map[string]any) {
	b, _ := json.Marshal(map[string]any{"plugins": plugins})
	r.serve("/plugins/index.json", b)
}

// hello is a small plugin: a manifest and its main module.
func (r *fakeRegistry) hello(version string) map[string]any {
	return r.plugin("hello-ports", version, map[string]string{
		"berth-plugin.json": manifestOf("Hello ports", version),
		"dist/index.js":     "export default function activate() { /* " + version + " */ }\n",
	})
}

// marketAgent is an agent with only the marketplace's routes, over a
// throwaway user folder.
type marketAgent struct {
	*Agent
	srv  *httptest.Server
	user string
	logs *bytes.Buffer
}

func newMarketAgent(t *testing.T, registry string) *marketAgent {
	t.Helper()
	user := filepath.Join(t.TempDir(), "user")
	logs := &bytes.Buffer{}
	a := &Agent{cfg: Config{Dir: t.TempDir(), UserDir: user, PluginRegistry: registry, Now: time.Now, Log: log.New(logs, "", 0)}}
	mux := http.NewServeMux()
	a.marketplaceRoutes(mux)
	mux.HandleFunc("GET /v1/plugins", a.uiPlugins)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &marketAgent{Agent: a, srv: srv, user: user, logs: logs}
}

func (m *marketAgent) call(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, m.srv.URL+path, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (m *marketAgent) listed(t *testing.T, query string) Marketplace {
	t.Helper()
	status, body := m.call(t, "GET", "/v1/marketplace"+query, "")
	var out Marketplace
	if status != 200 || json.Unmarshal([]byte(body), &out) != nil {
		t.Fatalf("listing = %d %s", status, body)
	}
	return out
}

func (m *marketAgent) entry(t *testing.T, id string) MarketPlugin {
	t.Helper()
	for _, p := range m.listed(t, "?fresh=1").Plugins {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("%s is not listed", id)
	return MarketPlugin{}
}

func (m *marketAgent) pluginDir(id string) string { return filepath.Join(m.user, "plugins", id) }

// leftovers are what an install left beside the plugins folder that is not
// meant to stay.
func (m *marketAgent) leftovers(t *testing.T) []string {
	t.Helper()
	var out []string
	entries, _ := os.ReadDir(m.user)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTheMarketplaceListsPluginsWithWhatIsInstalled(t *testing.T) {
	r := newRegistry(t)
	hello := r.hello("0.1.0")
	// A newer listing says more than this agent knows: it reads the rest.
	hello["helper"] = map[string]any{"box": "bin/helper", "sha256": "00"}
	notes := r.plugin("acme-notes", "2.0.0", map[string]string{"berth-plugin.json": manifestOf("Acme notes", "2.0.0"), "dist/index.js": "export default () => {}\n"})
	bad := r.hello("0.1.0")
	bad["id"] = "../escape"
	b, _ := json.Marshal(map[string]any{"schema": 2, "plugins": []any{hello, notes, bad, r.hello("9.9.9")}})
	r.serve("/plugins/index.json", b)
	m := newMarketAgent(t, r.url())
	// Someone wrote acme-notes themselves; nothing installed hello-ports.
	write(t, filepath.Join(m.pluginDir("acme-notes"), "berth-plugin.json"), manifestOf("Acme notes", "1.5.0"))

	got := m.listed(t, "")
	if got.Host != strings.TrimPrefix(r.srv.URL, "http://") || got.Registry != r.url() {
		t.Fatalf("registry = %q on %q", got.Registry, got.Host)
	}
	// An id that is not a folder's name, and the same id again, are left out.
	if len(got.Plugins) != 2 {
		t.Fatalf("listed %d plugins: %+v", len(got.Plugins), got.Plugins)
	}
	h, n := got.Plugins[0], got.Plugins[1]
	if h.ID != "hello-ports" || h.Version != "0.1.0" || h.Name != "Acme hello-ports" || h.Author != "Acme" || h.Homepage != "https://example.com/hello-ports" || h.Installed || h.InstalledVersion != "" || h.Update || h.ByHand || h.Error != "" || len(h.Files) != 2 || h.Size == 0 {
		t.Fatalf("hello-ports = %+v", h)
	}
	if n.ID != "acme-notes" || !n.Installed || n.InstalledVersion != "1.5.0" || !n.ByHand || n.Update {
		t.Fatalf("acme-notes = %+v", n)
	}

	// The listing is kept for a while, and read again when asked.
	m.listed(t, "")
	if n := r.asked("/plugins/index.json"); n != 1 {
		t.Fatalf("the listing was fetched %d times for two reads", n)
	}
	m.listed(t, "?fresh=1")
	if n := r.asked("/plugins/index.json"); n != 2 {
		t.Fatalf("a fresh read fetched the listing %d times in all", n)
	}
}

func TestInstallingAPluginPutsItInItsFolderAndLeavesItOff(t *testing.T) {
	r := newRegistry(t)
	r.publish(r.hello("0.1.0"))
	m := newMarketAgent(t, r.url())

	status, body := m.call(t, "POST", "/v1/marketplace/hello-ports/install", `{"version":"0.1.0"}`)
	var p MarketPlugin
	if status != 200 || json.Unmarshal([]byte(body), &p) != nil || !p.Installed || p.InstalledVersion != "0.1.0" || p.Update || p.ByHand {
		t.Fatalf("install = %d %s", status, body)
	}
	dir := m.pluginDir("hello-ports")
	if b, _ := os.ReadFile(filepath.Join(dir, "berth-plugin.json")); string(b) != manifestOf("Hello ports", "0.1.0") {
		t.Fatalf("manifest = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "dist", "index.js")); !strings.Contains(string(b), "0.1.0") {
		t.Fatalf("main = %q", b)
	}
	// Installed, listed as the marketplace's, and off: nobody allowed it.
	_, body = m.call(t, "GET", "/v1/plugins", "")
	var plugins []PluginInfo
	json.Unmarshal([]byte(body), &plugins)
	if len(plugins) != 1 || plugins[0].ID != "hello-ports" || plugins[0].Source != "marketplace" || plugins[0].Enabled || plugins[0].Allowed != "" {
		t.Fatalf("plugins = %s", body)
	}
	if _, err := os.Stat(filepath.Join(m.user, "plugins.json")); !os.IsNotExist(err) {
		t.Fatalf("an install wrote the permissions file: %v", err)
	}
	// Where it came from is kept beside the plugins folder, not inside it.
	src := m.readPluginSources()["hello-ports"]
	if src.Version != "0.1.0" || src.Registry != r.url() || len(src.Files) != 2 {
		t.Fatalf("source = %+v", src)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("the plugin's folder holds %d entries", len(entries))
	}
	if left := m.leftovers(t); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
	// Installing it again changes nothing, and needs no body.
	if status, body := m.call(t, "POST", "/v1/marketplace/hello-ports/install", ""); status != 200 {
		t.Fatalf("a second install = %d %s", status, body)
	}
	// The listing moved on from what the person was shown.
	if status, body := m.call(t, "POST", "/v1/marketplace/hello-ports/install", `{"version":"0.0.9"}`); status != 409 || !strings.Contains(body, "listing_changed") {
		t.Fatalf("an install of a version no longer listed = %d %s", status, body)
	}
	if status, _ := m.call(t, "POST", "/v1/marketplace/acme-missing/install", "{}"); status != 404 {
		t.Fatalf("an unlisted plugin = %d", status)
	}
}

// ~/.berth/plugins may be a link to a folder kept somewhere else: the plugin
// is put together beside the real folder, and lands in it.
func TestAPluginInstallsIntoAPluginsFolderThatIsALink(t *testing.T) {
	r := newRegistry(t)
	r.publish(r.hello("0.1.0"))
	m := newMarketAgent(t, r.url())
	elsewhere := filepath.Join(t.TempDir(), "my-plugins")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(m.user, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(m.user, "plugins")); err != nil {
		t.Fatal(err)
	}
	if status, body := m.call(t, "POST", "/v1/marketplace/hello-ports/install", "{}"); status != 200 {
		t.Fatalf("install = %d %s", status, body)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "hello-ports", "dist", "index.js")); err != nil {
		t.Fatal(err)
	}
	if p := m.entry(t, "hello-ports"); !p.Installed || p.ByHand {
		t.Fatalf("after the install = %+v", p)
	}
	if status, body := m.call(t, "DELETE", "/v1/marketplace/hello-ports", ""); status != 200 {
		t.Fatalf("remove = %d %s", status, body)
	}
	entries, _ := os.ReadDir(filepath.Dir(elsewhere))
	if len(entries) != 1 {
		t.Fatalf("left beside the real plugins folder: %v", entries)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("left in the plugins folder: %v", entries)
	}
}

func TestADownloadThatDoesNotMatchItsChecksumInstallsNothing(t *testing.T) {
	r := newRegistry(t)
	hello := r.hello("0.1.0")
	r.publish(hello)
	// The file changed after the listing was written.
	r.serve("/plugins/hello-ports/0.1.0/dist/index.js", []byte("export default function activate() { /* 0.1.X */ }\n"))
	m := newMarketAgent(t, r.url())

	status, body := m.call(t, "POST", "/v1/marketplace/hello-ports/install", "{}")
	if status != 502 || !strings.Contains(body, "plugin_mismatch") || !strings.Contains(body, "checksum") {
		t.Fatalf("install = %d %s", status, body)
	}
	if _, err := os.Stat(m.pluginDir("hello-ports")); !os.IsNotExist(err) {
		t.Fatalf("a plugin that failed its checksum was installed: %v", err)
	}
	if left := m.leftovers(t); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
	if len(m.readPluginSources()) != 0 {
		t.Fatal("a failed install was recorded")
	}

	// And one already installed stays as it was.
	r.publish(r.hello("0.1.0"))
	if status, body := m.call(t, "POST", "/v1/marketplace/hello-ports/install", "{}"); status != 200 {
		t.Fatalf("install = %d %s", status, body)
	}
	next := r.hello("0.2.0")
	r.publish(next)
	r.serve("/plugins/hello-ports/0.2.0/dist/index.js", []byte("export default 1 /* not what was listed, 0.2.0 */\n"))
	m.listed(t, "?fresh=1")
	if status, body := m.call(t, "POST", "/v1/marketplace/hello-ports/install", "{}"); status != 502 {
		t.Fatalf("a bad update = %d %s", status, body)
	}
	if b, _ := os.ReadFile(filepath.Join(m.pluginDir("hello-ports"), "berth-plugin.json")); string(b) != manifestOf("Hello ports", "0.1.0") {
		t.Fatalf("a bad update replaced the plugin: %q", b)
	}
}

func TestAListingCannotWriteOutsideThePluginsOwnFolder(t *testing.T) {
	r := newRegistry(t)
	// evil is a plugin whose main module's entry says something else.
	evil := func(id, key, value string) map[string]any {
		p := r.plugin(id, "1.0.0", map[string]string{"berth-plugin.json": manifestOf(id, "1.0.0"), "dist/index.js": "export default 1\n"})
		p["files"].([]map[string]any)[1][key] = value
		return p
	}
	r.publish(
		evil("acme-up", "path", "../acme-up.js"),
		evil("acme-deep", "path", "dist/../../../acme-deep.js"),
		evil("acme-abs", "path", "/tmp/acme-abs.js"),
		evil("acme-backslash", "path", `..\acme-backslash.js`),
		evil("acme-dot", "path", "./dist/index.js"),
		evil("acme-empty", "path", ""),
		evil("acme-elsewhere", "url", "https://example.com/acme.js"),
		evil("acme-scheme", "url", "file:///etc/hosts"),
		evil("acme-sum", "sha256", "not-a-checksum"),
	)
	m := newMarketAgent(t, r.url())
	got := m.listed(t, "")
	if len(got.Plugins) != 9 {
		t.Fatalf("listed %d plugins", len(got.Plugins))
	}
	for _, p := range got.Plugins {
		if p.Error == "" {
			t.Errorf("%s is offered though it reaches outside its folder: %+v", p.ID, p.Files)
		}
		status, body := m.call(t, "POST", "/v1/marketplace/"+p.ID+"/install", "{}")
		if status != 400 || !strings.Contains(body, "plugin_invalid") {
			t.Errorf("installing %s = %d %s", p.ID, status, body)
		}
	}
	// An id is a folder's name, nothing else.
	for _, id := range []string{"..", "..%2Fescape", "Acme", ".hidden", "a%2Fb", strings.Repeat("a", 49)} {
		if status, _ := m.call(t, "POST", "/v1/marketplace/"+id+"/install", "{}"); status != 400 && status != 404 {
			t.Errorf("an install of the id %q = %d", id, status)
		}
		if status, _ := m.call(t, "DELETE", "/v1/marketplace/"+id, ""); status != 400 && status != 404 {
			t.Errorf("a removal of the id %q = %d", id, status)
		}
	}
	// Nothing but the plugins folder appeared, and nothing in it.
	entries, _ := os.ReadDir(m.user)
	for _, e := range entries {
		if e.Name() != "plugins" {
			t.Errorf("wrote %s beside the plugins folder", e.Name())
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(m.user, "plugins")); len(entries) != 0 {
		t.Errorf("installed %d plugins that should have been refused", len(entries))
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(m.user), "acme-*")); len(matches) != 0 {
		t.Errorf("wrote above the user folder: %v", matches)
	}
}

func TestAPluginLargerThanTheListingSaysOrThanAllowedIsRefused(t *testing.T) {
	r := newRegistry(t)
	// The listing asks for more than a plugin's file may be.
	huge := r.plugin("acme-huge", "1.0.0", map[string]string{"berth-plugin.json": manifestOf("Huge", "1.0.0"), "dist/index.js": "export default 1\n"})
	huge["files"].([]map[string]any)[1]["size"] = marketFileMax + 1
	// Many files, each allowed, that are too much together.
	files := map[string]string{"berth-plugin.json": manifestOf("Many", "1.0.0")}
	many := r.plugin("acme-many", "1.0.0", files)
	list := many["files"].([]map[string]any)
	for i := range 5 {
		list = append(list, map[string]any{"path": "chunk-" + string(rune('a'+i)) + ".js", "url": "x.js", "sha256": sum("x"), "size": marketFileMax})
	}
	many["files"] = list
	// The server sends more than the listing gave.
	long := r.plugin("acme-long", "1.0.0", map[string]string{"berth-plugin.json": manifestOf("Long", "1.0.0"), "dist/index.js": "export default 1\n"})
	r.serve("/plugins/acme-long/1.0.0/dist/index.js", []byte(strings.Repeat("export default 1\n", 4096)))
	r.publish(huge, many, long)
	m := newMarketAgent(t, r.url())

	for _, id := range []string{"acme-huge", "acme-many"} {
		if p := m.entry(t, id); !strings.Contains(p.Error, "larger than") {
			t.Errorf("%s = %+v", id, p)
		}
		before := r.asked("/plugins/" + id + "/1.0.0/berth-plugin.json")
		if status, body := m.call(t, "POST", "/v1/marketplace/"+id+"/install", "{}"); status != 400 {
			t.Errorf("installing %s = %d %s", id, status, body)
		}
		if r.asked("/plugins/"+id+"/1.0.0/berth-plugin.json") != before {
			t.Errorf("%s was downloaded before it was refused", id)
		}
	}
	status, body := m.call(t, "POST", "/v1/marketplace/acme-long/install", "{}")
	if status != 502 || !strings.Contains(body, "plugin_mismatch") {
		t.Fatalf("a file longer than listed = %d %s", status, body)
	}
	if entries, _ := os.ReadDir(filepath.Join(m.user, "plugins")); len(entries) != 0 {
		t.Fatalf("installed %d plugins that should have been refused", len(entries))
	}
	// A listing itself is small.
	r.serve("/plugins/index.json", []byte(`{"plugins":[],"pad":"`+strings.Repeat("x", marketListingMax)+`"}`))
	if status, body := m.call(t, "GET", "/v1/marketplace?fresh=1", ""); status != 502 || !strings.Contains(body, "registry_invalid") {
		t.Fatalf("an oversized listing = %d %s", status, body)
	}
}

func TestOnlyAnHTTPSRegistryIsRead(t *testing.T) {
	asked := 0
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { asked++ }))
	defer plain.Close()
	for _, registry := range []string{
		"http://example.com/plugins/index.json",
		"http://192.0.2.1/plugins/index.json",
		"ftp://example.com/plugins/index.json",
		"file:///etc/hosts",
		"example.com/plugins/index.json",
		"//example.com/plugins/index.json",
		"http://localhost.example.com/index.json",
	} {
		m := newMarketAgent(t, registry)
		status, body := m.call(t, "GET", "/v1/marketplace", "")
		if status != 400 || !strings.Contains(body, "registry_invalid") {
			t.Errorf("%s = %d %s", registry, status, body)
		}
		if status, body := m.call(t, "POST", "/v1/marketplace/hello-ports/install", "{}"); status != 400 || !strings.Contains(body, "registry_invalid") {
			t.Errorf("an install from %s = %d %s", registry, status, body)
		}
	}
	if asked != 0 {
		t.Fatal("a registry that is not https was asked")
	}

	// A registry may not hand over to a plain one either, and what it lists
	// is fetched from its own address only (see the traversal test).
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "http://example.com/plugins/index.json", http.StatusFound)
	}))
	defer hop.Close()
	m := newMarketAgent(t, hop.URL+"/plugins/index.json")
	if status, body := m.call(t, "GET", "/v1/marketplace", ""); status != 502 || !strings.Contains(body, "not https") {
		t.Fatalf("a redirect to http = %d %s", status, body)
	}
}

func TestAnUnreachableRegistryIsAnErrorThatKeepsItsCredentialsToItself(t *testing.T) {
	// A registry that is gone: nothing listens there any more.
	gone := httptest.NewServer(http.NotFoundHandler())
	addr := strings.TrimPrefix(gone.URL, "http://")
	gone.Close()
	m := newMarketAgent(t, "http://acme:hunter2secret@"+addr+"/plugins/index.json?token=querysecret")

	status, body := m.call(t, "GET", "/v1/marketplace", "")
	if status != 502 || !strings.Contains(body, "registry_unreachable") || !strings.Contains(body, "http://"+addr+"/plugins/index.json") {
		t.Fatalf("an unreachable registry = %d %s", status, body)
	}
	status, installBody := m.call(t, "POST", "/v1/marketplace/hello-ports/install", "{}")
	if status != 502 {
		t.Fatalf("an install with no registry = %d %s", status, installBody)
	}
	said := body + installBody + m.logs.String()
	for _, secret := range []string{"hunter2secret", "querysecret", "acme:"} {
		if strings.Contains(said, secret) {
			t.Fatalf("%q is in an answer or the log:\n%s", secret, said)
		}
	}
	// A registry that answers, but not with a listing.
	r := newRegistry(t)
	r.serve("/plugins/index.json", []byte("<html>not found</html>"))
	m = newMarketAgent(t, r.url())
	if status, body := m.call(t, "GET", "/v1/marketplace", ""); status != 502 || !strings.Contains(body, "registry_invalid") {
		t.Fatalf("a page instead of a listing = %d %s", status, body)
	}
	m = newMarketAgent(t, r.srv.URL+"/plugins/missing.json")
	if status, body := m.call(t, "GET", "/v1/marketplace", ""); status != 502 || !strings.Contains(body, "404") {
		t.Fatalf("a missing listing = %d %s", status, body)
	}
}

func TestAnUpdateReplacesThePluginAndIsAskedAboutAgain(t *testing.T) {
	r := newRegistry(t)
	v1 := r.plugin("hello-ports", "0.1.0", map[string]string{
		"berth-plugin.json": manifestOf("Hello ports", "0.1.0"),
		"dist/index.js":     "export default function activate() { /* 0.1.0 */ }\n",
		"dist/old-chunk.js": "export const old = 1\n",
	})
	r.publish(v1)
	m := newMarketAgent(t, r.url())
	if status, body := m.call(t, "POST", "/v1/marketplace/hello-ports/install", "{}"); status != 200 {
		t.Fatalf("install = %d %s", status, body)
	}
	dir := m.pluginDir("hello-ports")
	allow := func() string {
		t.Helper()
		h, err := hooks.PluginHash(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := hooks.AllowPlugin(dir, h); err != nil {
			t.Fatal(err)
		}
		if !hooks.PluginEnabled(dir) {
			t.Fatal("an allowed plugin is not on")
		}
		return h
	}
	allowed := allow()
	if p := m.entry(t, "hello-ports"); p.Update || p.InstalledVersion != "0.1.0" || p.ByHand {
		t.Fatalf("up to date = %+v", p)
	}

	// A newer version is listed.
	r.publish(r.hello("0.2.0"))
	if p := m.entry(t, "hello-ports"); !p.Update || p.InstalledVersion != "0.1.0" || p.Version != "0.2.0" {
		t.Fatalf("an update = %+v", p)
	}
	status, body := m.call(t, "POST", "/v1/marketplace/hello-ports/install", `{"version":"0.2.0"}`)
	var p MarketPlugin
	if status != 200 || json.Unmarshal([]byte(body), &p) != nil || p.Update || p.InstalledVersion != "0.2.0" {
		t.Fatalf("update = %d %s", status, body)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "dist", "index.js")); !strings.Contains(string(b), "0.2.0") {
		t.Fatalf("main after the update = %q", b)
	}
	// The whole folder was replaced: a file the new version dropped is gone.
	if _, err := os.Stat(filepath.Join(dir, "dist", "old-chunk.js")); !os.IsNotExist(err) {
		t.Fatalf("a file the update dropped is still there: %v", err)
	}
	// It is off: what was allowed is the old code, so the app asks again.
	st := hooks.PluginStatus(dir)
	if st.Enabled || !st.Changed || st.Allowed != allowed {
		t.Fatalf("after an update the plugin is %+v", st)
	}
	if left := m.leftovers(t); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}

	// The same version published again with another file beside the same
	// manifest and main module: the hash that was allowed still fits, so the
	// permission is forgotten rather than carried over to code nobody saw.
	allow()
	again := r.plugin("hello-ports", "0.2.0", map[string]string{
		"berth-plugin.json": manifestOf("Hello ports", "0.2.0"),
		"dist/index.js":     "export default function activate() { /* 0.2.0 */ }\n",
		"dist/chunk.js":     "export const added = 1\n",
	})
	r.publish(again)
	if p := m.entry(t, "hello-ports"); !p.Update {
		t.Fatalf("other files under the same version = %+v", p)
	}
	if status, body := m.call(t, "POST", "/v1/marketplace/hello-ports/install", "{}"); status != 200 {
		t.Fatalf("update = %d %s", status, body)
	}
	if st := hooks.PluginStatus(dir); st.Enabled || st.Allowed != "" {
		t.Fatalf("a plugin whose other files changed is still allowed: %+v", st)
	}
	// Putting back exactly what is there leaves an allowed plugin on.
	allow()
	if status, body := m.call(t, "POST", "/v1/marketplace/hello-ports/install", "{}"); status != 200 {
		t.Fatalf("reinstall = %d %s", status, body)
	}
	if !hooks.PluginEnabled(dir) {
		t.Fatal("installing the same files again turned the plugin off")
	}
}

func TestAPluginMadeByHandIsNotInstalledOverUnlessAsked(t *testing.T) {
	r := newRegistry(t)
	r.publish(r.hello("0.2.0"))
	m := newMarketAgent(t, r.url())
	dir := m.pluginDir("hello-ports")
	write(t, filepath.Join(dir, "berth-plugin.json"), manifestOf("My hello", "0.0.1"))
	write(t, filepath.Join(dir, "dist", "index.js"), "export default function mine() {}\n")
	write(t, filepath.Join(dir, "notes.txt"), "mine")

	status, body := m.call(t, "POST", "/v1/marketplace/hello-ports/install", "{}")
	if status != 409 || !strings.Contains(body, "plugin_by_hand") {
		t.Fatalf("an install over a plugin made by hand = %d %s", status, body)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "notes.txt")); string(b) != "mine" {
		t.Fatal("a refused install touched the folder")
	}
	// It was allowed as it was; replacing it must not carry that over.
	h, _ := hooks.PluginHash(dir)
	if err := hooks.AllowPlugin(dir, h); err != nil {
		t.Fatal(err)
	}
	status, body = m.call(t, "POST", "/v1/marketplace/hello-ports/install", `{"replace":true}`)
	if status != 200 {
		t.Fatalf("a replace = %d %s", status, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); !os.IsNotExist(err) {
		t.Fatal("a replaced folder kept its old files")
	}
	if hooks.PluginEnabled(dir) {
		t.Fatal("a replaced plugin is on")
	}
	if p := m.entry(t, "hello-ports"); p.ByHand || p.Update || p.InstalledVersion != "0.2.0" {
		t.Fatalf("after a replace = %+v", p)
	}

	// Changed by hand since: it is someone's own again.
	write(t, filepath.Join(dir, "dist", "index.js"), "export default function mine() {}\n")
	if p := m.entry(t, "hello-ports"); !p.ByHand || p.Update {
		t.Fatalf("after an edit = %+v", p)
	}
	if status, _ := m.call(t, "POST", "/v1/marketplace/hello-ports/install", "{}"); status != 409 {
		t.Fatalf("an install over an edited plugin = %d", status)
	}

	// A link to a plugin being written, as the guide installs one: replacing
	// it removes the link, never the folder it points at.
	mine := filepath.Join(t.TempDir(), "my-plugin")
	write(t, filepath.Join(mine, "berth-plugin.json"), manifestOf("Mine", "0.0.1"))
	os.RemoveAll(dir)
	if err := os.Symlink(mine, dir); err != nil {
		t.Fatal(err)
	}
	if p := m.entry(t, "hello-ports"); !p.ByHand || p.InstalledVersion != "0.0.1" {
		t.Fatalf("a linked plugin = %+v", p)
	}
	_, body = m.call(t, "GET", "/v1/plugins", "")
	if strings.Contains(body, `"source"`) {
		t.Fatalf("a linked plugin is listed as the marketplace's: %s", body)
	}
	if status, _ := m.call(t, "POST", "/v1/marketplace/hello-ports/install", "{}"); status != 409 {
		t.Fatalf("an install over a link = %d", status)
	}
	if status, body := m.call(t, "POST", "/v1/marketplace/hello-ports/install", `{"replace":true}`); status != 200 {
		t.Fatalf("a replace of a link = %d %s", status, body)
	}
	if b, _ := os.ReadFile(filepath.Join(mine, "berth-plugin.json")); string(b) != manifestOf("Mine", "0.0.1") {
		t.Fatalf("replacing a link changed the folder it pointed at: %q", b)
	}
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("after replacing a link: %v", err)
	}
}

func TestAMarketplacePluginIsRemovedWithItsPermission(t *testing.T) {
	r := newRegistry(t)
	r.publish(r.hello("0.1.0"))
	m := newMarketAgent(t, r.url())
	// One made by hand is not the marketplace's to remove.
	mine := m.pluginDir("acme-mine")
	write(t, filepath.Join(mine, "berth-plugin.json"), manifestOf("Mine", "1.0.0"))
	if status, body := m.call(t, "DELETE", "/v1/marketplace/acme-mine", ""); status != 409 || !strings.Contains(body, "plugin_by_hand") {
		t.Fatalf("removing a plugin made by hand = %d %s", status, body)
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatal("a refused removal removed the folder")
	}

	if status, body := m.call(t, "POST", "/v1/marketplace/hello-ports/install", "{}"); status != 200 {
		t.Fatalf("install = %d %s", status, body)
	}
	dir := m.pluginDir("hello-ports")
	h, _ := hooks.PluginHash(dir)
	if err := hooks.AllowPlugin(dir, h); err != nil {
		t.Fatal(err)
	}
	// Removing needs no registry.
	r.srv.Close()
	status, body := m.call(t, "DELETE", "/v1/marketplace/hello-ports", "")
	if status != 200 || !strings.Contains(body, `"removed":"hello-ports"`) {
		t.Fatalf("remove = %d %s", status, body)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("the folder is still there: %v", err)
	}
	if _, ok := hooks.PluginGrantFor(dir); ok {
		t.Fatal("its permission is still there")
	}
	if len(m.readPluginSources()) != 0 {
		t.Fatal("its source is still recorded")
	}
	if left := m.leftovers(t); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
	if status, _ := m.call(t, "DELETE", "/v1/marketplace/hello-ports", ""); status != 409 {
		t.Fatalf("a second removal = %d", status)
	}
}

// The listing committed in site/plugins/ (pnpm -C app build:marketplace) is
// one this agent installs: its checksums are its files', and what lands is a
// plugin the consent flow can hash.
func TestTheCommittedListingInstalls(t *testing.T) {
	site := filepath.Join("..", "..", "site")
	b, err := os.ReadFile(filepath.Join(site, "plugins", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index struct {
		Plugins []listedPlugin `json:"plugins"`
	}
	if err := json.Unmarshal(b, &index); err != nil || len(index.Plugins) == 0 {
		t.Fatalf("site/plugins/index.json lists nothing: %v", err)
	}
	// The example is always listed; where is the listing's own order (by id).
	if !slices.ContainsFunc(index.Plugins, func(p listedPlugin) bool { return p.ID == "hello-ports" }) {
		t.Fatalf("hello-ports is not in the listing")
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(site)))
	defer srv.Close()
	m := newMarketAgent(t, srv.URL+"/plugins/index.json")
	for _, p := range m.listed(t, "").Plugins {
		if p.Error != "" {
			t.Fatalf("%s: %s", p.ID, p.Error)
		}
		status, body := m.call(t, "POST", "/v1/marketplace/"+p.ID+"/install", `{"version":"`+p.Version+`"}`)
		if status != 200 {
			t.Fatalf("installing %s = %d %s", p.ID, status, body)
		}
		if _, err := hooks.PluginHash(m.pluginDir(p.ID)); err != nil {
			t.Fatalf("%s: %v", p.ID, err)
		}
		if hooks.PluginEnabled(m.pluginDir(p.ID)) {
			t.Fatalf("%s is on after an install", p.ID)
		}
	}
}

func TestTheAppReachesTheMarketplaceWithItsToken(t *testing.T) {
	r := newRegistry(t)
	r.publish(r.hello("0.1.0"))
	b := newBox(t)
	a := startAgentConfig(t, b.pairLaptop(), &fakeNetworks{}, func(c *Config) { c.PluginRegistry = r.url() })
	tok := uiToken(t, a)
	if resp, _ := uiCall(t, a, "GET", "/v1/marketplace", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without a token = %d", resp.StatusCode)
	}
	resp, body := uiCall(t, a, "GET", "/v1/marketplace", tok)
	if resp.StatusCode != 200 || !strings.Contains(body, `"id":"hello-ports"`) {
		t.Fatalf("listing = %d %s", resp.StatusCode, body)
	}
	if resp, body := uiSend(t, a, "POST", "/v1/marketplace/hello-ports/install", tok, `{"version":"0.1.0"}`); resp.StatusCode != 200 {
		t.Fatalf("install = %d %s", resp.StatusCode, body)
	}
	_, body = uiCall(t, a, "GET", "/v1/plugins", tok)
	if !strings.Contains(body, `"source":"marketplace"`) || !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("plugins = %s", body)
	}
	if resp, body := uiSend(t, a, "DELETE", "/v1/marketplace/hello-ports", tok, ""); resp.StatusCode != 200 {
		t.Fatalf("remove = %d %s", resp.StatusCode, body)
	}
}
