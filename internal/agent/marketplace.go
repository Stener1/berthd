package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cosscom/shipyard/internal/hooks"
	"github.com/cosscom/shipyard/internal/statefile"
)

// The plugin marketplace: plugins that do not ship inside the app, listed in
// an index.json on the public site (site/plugins/, built by
// app/scripts/build-marketplace.mjs) and installed into
// ~/.berth/plugins/<id>/. The app's page may talk only to this agent (its
// CSP), so the agent fetches the listing and every file.
//
// Installing never turns a plugin on. The files land in the plugin's folder
// and the consent flow (internal/hooks/plugins.go) decides whether they run:
// a plugin installed for the first time is off until the person allows it,
// and an update changes the hash they allowed, so the app asks again.
//
// What a listing says is checked, not trusted: each file is downloaded from
// the registry's own address, must be exactly the size and SHA-256 the
// listing gives, and is written only inside the plugin's own folder. That
// proves the files are the ones the listing describes. It does not prove the
// listing is honest: whoever can change the registry can list anything, which
// is why consent stays with the person.

// DefaultPluginRegistry is the listing the app browses.
const DefaultPluginRegistry = "https://berthd.app/plugins/index.json"

const (
	// marketFresh is how long a fetched listing stands.
	marketFresh = 5 * time.Minute
	// A listing, one file of a plugin, and a whole plugin are no larger than
	// these, and a plugin has no more files than marketFilesMax.
	marketListingMax = 1 << 20
	marketFileMax    = 16 << 20
	marketPluginMax  = 64 << 20
	marketFilesMax   = 256
	marketPluginsMax = 1000
)

var (
	// pluginIDPattern is what a listed plugin's id must be: it becomes a
	// folder's name.
	pluginIDPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	pluginVersionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$`)
	sha256Pattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// MarketFile is one file of a listed plugin.
type MarketFile struct {
	// Path is where it goes inside the plugin's folder.
	Path string `json:"path"`
	// URL is where it is, relative to the listing.
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// listedPlugin is a plugin as index.json lists it. Fields a newer listing
// adds are ignored.
type listedPlugin struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Version     string       `json:"version"`
	Author      string       `json:"author,omitempty"`
	Homepage    string       `json:"homepage,omitempty"`
	Files       []MarketFile `json:"files"`
}

// MarketPlugin is a listed plugin with what this computer has of it.
type MarketPlugin struct {
	listedPlugin
	// Size is all its files together, in bytes.
	Size int64 `json:"size"`
	// Error is why it cannot be installed: the listing describes it wrongly.
	Error string `json:"error,omitempty"`
	// Installed: ~/.berth/plugins/<id> exists; InstalledVersion is the
	// version its manifest gives.
	Installed        bool   `json:"installed,omitempty"`
	InstalledVersion string `json:"installed_version,omitempty"`
	// Update: it was installed from a listing, and this one has other files.
	Update bool `json:"update,omitempty"`
	// ByHand: the folder was not installed from a listing, or was changed
	// since, so installing over it has to be asked for (replace).
	ByHand bool `json:"by_hand,omitempty"`
}

// Marketplace is the listing as the app shows it.
type Marketplace struct {
	// Registry is where the listing was read from, without credentials, and
	// Host its host.
	Registry string         `json:"registry"`
	Host     string         `json:"host"`
	Fetched  time.Time      `json:"fetched"`
	Plugins  []MarketPlugin `json:"plugins"`
}

// pluginSource records that a plugin's folder was installed from a listing,
// and exactly which files, so an update can replace it without asking and a
// folder someone made by hand is never overwritten unasked.
type pluginSource struct {
	Registry string       `json:"registry"`
	Version  string       `json:"version"`
	At       time.Time    `json:"at"`
	Files    []sourceFile `json:"files"`
}

type sourceFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type pluginSources struct {
	Plugins map[string]pluginSource `json:"plugins"`
}

// marketState is the listing last fetched, and the one install or removal
// under way.
type marketState struct {
	fetch   sync.Mutex
	at      time.Time
	from    string
	entries []MarketPlugin

	change sync.Mutex
}

// marketError is an error with the status and code the app branches on.
type marketError struct {
	status int
	code   string
	msg    string
}

func (e *marketError) Error() string { return e.msg }

func marketErr(status int, code, format string, args ...any) error {
	return &marketError{status: status, code: code, msg: fmt.Sprintf(format, args...)}
}

func writeMarketError(w http.ResponseWriter, err error) {
	var me *marketError
	switch {
	case !errors.As(err, &me):
		writeError(w, http.StatusInternalServerError, err.Error())
	case me.code == "":
		writeError(w, me.status, me.msg)
	default:
		writeCoded(w, me.status, me.msg, me.code)
	}
}

func (a *Agent) pluginsDir() string { return filepath.Join(a.cfg.UserDir, "plugins") }

// pluginSourcesPath is beside plugins.json, never inside a plugin's own
// folder, which the plugin ships.
func (a *Agent) pluginSourcesPath() string {
	return filepath.Join(a.cfg.UserDir, "plugin-sources.json")
}

func (a *Agent) readPluginSources() map[string]pluginSource {
	var s pluginSources
	if b, err := os.ReadFile(a.pluginSourcesPath()); err == nil && json.Unmarshal(b, &s) == nil && s.Plugins != nil {
		return s.Plugins
	}
	return map[string]pluginSource{}
}

func (a *Agent) writePluginSources(m map[string]pluginSource) error {
	b, err := json.MarshalIndent(pluginSources{Plugins: m}, "", "  ")
	if err != nil {
		return err
	}
	return statefile.Write(a.pluginSourcesPath(), append(b, '\n'))
}

// safeURL is u for a log, an error or the app: no user, password or query,
// any of which may be a credential.
func safeURL(u *url.URL) string {
	return u.Scheme + "://" + u.Host + u.EscapedPath()
}

func loopbackHost(h string) bool {
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

// pluginRegistry is where the listing is read: https only, or plain http on
// this computer, which tests use.
func (a *Agent) pluginRegistry() (*url.URL, error) {
	raw := strings.TrimSpace(a.cfg.PluginRegistry)
	if raw == "" {
		raw = DefaultPluginRegistry
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		// The address itself may hold a credential, so it is not repeated.
		return nil, marketErr(http.StatusBadRequest, "registry_invalid", "the plugin registry is not an address; set BERTH_PLUGIN_REGISTRY to an https:// link to an index.json")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopbackHost(u.Hostname())) {
		return nil, marketErr(http.StatusBadRequest, "registry_invalid", "the plugin registry must be an https:// address, not %s://%s", u.Scheme, u.Host)
	}
	return u, nil
}

// marketGet reads at most max+1 bytes of u, so the caller can tell a body
// that is too large from one that is as large as allowed. A redirect may only
// lead to https, or stay on the registry's own address.
func marketGet(ctx context.Context, registry, u *url.URL, max int64) ([]byte, error) {
	// ctx is the time limit: the listing's own, or the whole install's.
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" && !(req.URL.Scheme == registry.Scheme && req.URL.Host == registry.Host) {
				return errors.New("it redirects to an address that is not https")
			}
			return nil
		},
	}
	unreachable := func(err error) error {
		// A *url.Error repeats the whole address, credentials and all.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return marketErr(http.StatusBadGateway, "registry_unreachable", "could not reach %s: %v", safeURL(u), err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, unreachable(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, unreachable(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, marketErr(http.StatusBadGateway, "registry_unreachable", "%s answered %s", safeURL(u), resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, unreachable(err)
	}
	return b, nil
}

// fileURL is where a listed file is: relative to the listing, and on the
// registry's own address, so a listing cannot point the agent anywhere else.
func fileURL(registry *url.URL, ref string) (*url.URL, error) {
	r, err := url.Parse(ref)
	if err != nil || ref == "" {
		return nil, errors.New("is not a link")
	}
	u := registry.ResolveReference(r)
	if u.Scheme != registry.Scheme || u.Host != registry.Host {
		return nil, errors.New("is not on the registry's own address")
	}
	return u, nil
}

// localPath reports whether p is a plain relative path with forward slashes
// that stays inside the folder it is joined to.
func localPath(p string) bool {
	return p != "" && !strings.ContainsAny(p, "\\\x00") && path.Clean(p) == p && !path.IsAbs(p) && filepath.IsLocal(filepath.FromSlash(p))
}

// checkListed says why a listed plugin cannot be installed, or "" when it
// can. The id is checked before this: an entry without a usable one is not
// listed at all.
func checkListed(registry *url.URL, p listedPlugin) (size int64, problem string) {
	if !pluginVersionPattern.MatchString(p.Version) {
		return 0, "the listing gives it no usable version"
	}
	if len(p.Files) == 0 {
		return 0, "the listing gives it no files"
	}
	if len(p.Files) > marketFilesMax {
		return 0, fmt.Sprintf("it has more than %d files", marketFilesMax)
	}
	seen := map[string]bool{}
	for _, f := range p.Files {
		if !localPath(f.Path) {
			return 0, fmt.Sprintf("its file %q is not a path inside the plugin's folder", f.Path)
		}
		// One file per name, also on a disk that ignores case.
		key := strings.ToLower(f.Path)
		if seen[key] {
			return 0, fmt.Sprintf("its file %s is listed twice", f.Path)
		}
		seen[key] = true
		if !sha256Pattern.MatchString(f.SHA256) {
			return 0, fmt.Sprintf("its file %s has no SHA-256 checksum", f.Path)
		}
		if f.Size < 0 || f.Size > marketFileMax {
			return 0, fmt.Sprintf("its file %s is larger than %d MB", f.Path, marketFileMax>>20)
		}
		if size += f.Size; size > marketPluginMax {
			return 0, fmt.Sprintf("it is larger than %d MB", marketPluginMax>>20)
		}
		if _, err := fileURL(registry, f.URL); err != nil {
			return 0, fmt.Sprintf("its file %s %v", f.Path, err)
		}
	}
	if !seen[hooks.PluginManifest] {
		return 0, "it has no " + hooks.PluginManifest
	}
	return size, ""
}

// listing is the registry's index.json, checked, kept for marketFresh.
func (a *Agent) listing(ctx context.Context, fresh bool) ([]MarketPlugin, *url.URL, time.Time, error) {
	registry, err := a.pluginRegistry()
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	m := &a.market
	m.fetch.Lock()
	defer m.fetch.Unlock()
	if !fresh && m.entries != nil && m.from == registry.String() && a.cfg.Now().Sub(m.at) < marketFresh {
		return m.entries, registry, m.at, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	b, err := marketGet(ctx, registry, registry, marketListingMax)
	if err != nil {
		a.cfg.Log.Printf("plugin listing: %v", err)
		return nil, nil, time.Time{}, err
	}
	var doc struct {
		Plugins []listedPlugin `json:"plugins"`
	}
	if len(b) > marketListingMax || json.Unmarshal(b, &doc) != nil || doc.Plugins == nil {
		err := marketErr(http.StatusBadGateway, "registry_invalid", "%s is not a plugin listing", safeURL(registry))
		a.cfg.Log.Printf("plugin listing: %v", err)
		return nil, nil, time.Time{}, err
	}
	entries := []MarketPlugin{}
	seen := map[string]bool{}
	for _, p := range doc.Plugins {
		if len(entries) == marketPluginsMax {
			break
		}
		if !pluginIDPattern.MatchString(p.ID) || seen[p.ID] {
			a.cfg.Log.Printf("plugin listing: skipping an entry with the id %q", truncate(p.ID, 64))
			continue
		}
		seen[p.ID] = true
		if p.Name == "" {
			p.Name = p.ID
		}
		// A homepage is opened in the browser, so it is a web link or nothing.
		if h, err := url.Parse(p.Homepage); err != nil || h.Scheme != "https" || h.Host == "" {
			p.Homepage = ""
		}
		e := MarketPlugin{listedPlugin: p}
		e.Size, e.Error = checkListed(registry, p)
		entries = append(entries, e)
	}
	m.entries, m.from, m.at = entries, registry.String(), a.cfg.Now()
	return entries, registry, m.at, nil
}

// installedPlugin is what ~/.berth/plugins/<id> holds now.
type installedPlugin struct {
	// present: something is there, a folder or a link to one.
	present bool
	version string
	// managed: a listing installed it, and its files are still those.
	managed bool
	source  pluginSource
}

func (a *Agent) installedPlugin(id string, sources map[string]pluginSource) installedPlugin {
	dir := filepath.Join(a.pluginsDir(), id)
	fi, err := os.Lstat(dir)
	if err != nil {
		return installedPlugin{}
	}
	st := installedPlugin{present: true}
	if b, err := os.ReadFile(filepath.Join(dir, hooks.PluginManifest)); err == nil {
		var m struct {
			Version string `json:"version"`
		}
		json.Unmarshal(b, &m)
		st.version = m.Version
	}
	// A link (the way a plugin being written is installed) is someone's own.
	src, ok := sources[id]
	if !ok || !fi.IsDir() {
		return st
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return st
	}
	defer root.Close()
	for _, f := range src.Files {
		if sum, err := hashIn(root, f.Path); err != nil || sum != f.SHA256 {
			return st
		}
	}
	st.managed, st.source = true, src
	return st
}

func hashIn(root *os.Root, name string) (string, error) {
	f, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, marketFileMax+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// sameFiles reports whether a listing's files are the ones installed.
func sameFiles(installed []sourceFile, listed []MarketFile) bool {
	if len(installed) != len(listed) {
		return false
	}
	sums := map[string]string{}
	for _, f := range installed {
		sums[f.Path] = f.SHA256
	}
	for _, f := range listed {
		if sums[f.Path] != f.SHA256 {
			return false
		}
	}
	return true
}

// merge adds what this computer has to a listed plugin.
func merge(p MarketPlugin, st installedPlugin) MarketPlugin {
	p.Installed, p.InstalledVersion = st.present, st.version
	p.ByHand = st.present && !st.managed
	p.Update = st.managed && (st.source.Version != p.Version || !sameFiles(st.source.Files, p.Files))
	return p
}

// marketplace is the listing merged with what is installed.
func (a *Agent) marketplace(ctx context.Context, fresh bool) (Marketplace, error) {
	entries, registry, at, err := a.listing(ctx, fresh)
	if err != nil {
		return Marketplace{}, err
	}
	sources := a.readPluginSources()
	out := Marketplace{Registry: safeURL(registry), Host: registry.Host, Fetched: at, Plugins: make([]MarketPlugin, 0, len(entries))}
	for _, p := range entries {
		out.Plugins = append(out.Plugins, merge(p, a.installedPlugin(p.ID, sources)))
	}
	return out, nil
}

// pluginStage makes a folder to put a plugin together in, or to take one
// away to: beside the plugins folder, so it is on the same disk (a rename
// moves it in one step) and nothing that lists plugins sees it. plugins is
// the plugins folder itself, which may be a link to somewhere else.
func (a *Agent) pluginStage(id string) (stage, plugins string, err error) {
	plugins = a.pluginsDir()
	if err := os.MkdirAll(plugins, 0o700); err != nil {
		return "", "", err
	}
	if real, err := filepath.EvalSymlinks(plugins); err == nil {
		plugins = real
	}
	stage, err = os.MkdirTemp(filepath.Dir(plugins), ".plugin-"+id+"-")
	return stage, plugins, err
}

// installPlugin downloads a listed plugin and puts it in
// ~/.berth/plugins/<id>/, replacing what the marketplace installed there
// before. version, when given, is the one the person was shown; replace lets
// it take the place of a folder the marketplace did not install.
func (a *Agent) installPlugin(ctx context.Context, id, version string, replace bool) (MarketPlugin, error) {
	a.market.change.Lock()
	defer a.market.change.Unlock()
	// The listing is read again, not taken from the one kept: the site holds
	// only the versions it lists now, so an older listing's files may be gone.
	entries, registry, _, err := a.listing(ctx, true)
	if err != nil {
		return MarketPlugin{}, err
	}
	var p MarketPlugin
	found := false
	for _, e := range entries {
		if e.ID == id {
			p, found = e, true
		}
	}
	if !found {
		return p, marketErr(http.StatusNotFound, "", "the listing has no plugin called %s", id)
	}
	if p.Error != "" {
		return p, marketErr(http.StatusBadRequest, "plugin_invalid", "%s can't be installed: %s", id, p.Error)
	}
	if version != "" && version != p.Version {
		return p, marketErr(http.StatusConflict, "listing_changed", "the listing now has %s %s, not %s; look at it again", id, p.Version, version)
	}
	sources := a.readPluginSources()
	before := a.installedPlugin(id, sources)
	if before.present && !before.managed && !replace {
		return p, marketErr(http.StatusConflict, "plugin_by_hand", "~/.berth/plugins/%s wasn't installed from the listing, or was changed since; replace it to install over it", id)
	}

	// Everything is written beside the plugins folder first, so a plugin is
	// never half installed and nothing lists the folder while it fills.
	stage, plugins, err := a.pluginStage(id)
	if err != nil {
		return p, err
	}
	defer os.RemoveAll(stage)
	root, err := os.OpenRoot(stage)
	if err != nil {
		return p, err
	}
	defer root.Close()
	files := make([]sourceFile, 0, len(p.Files))
	for _, f := range p.Files {
		u, err := fileURL(registry, f.URL)
		if err != nil {
			return p, marketErr(http.StatusBadRequest, "plugin_invalid", "%s can't be installed: its file %s %v", id, f.Path, err)
		}
		data, err := marketGet(ctx, registry, u, f.Size)
		if err != nil {
			return p, err
		}
		if int64(len(data)) != f.Size {
			return p, marketErr(http.StatusBadGateway, "plugin_mismatch", "%s of %s is not the %d bytes the listing gives; nothing was installed", f.Path, id, f.Size)
		}
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != f.SHA256 {
			return p, marketErr(http.StatusBadGateway, "plugin_mismatch", "%s of %s does not match the listing's checksum; nothing was installed", f.Path, id)
		}
		name := filepath.FromSlash(f.Path)
		if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return p, err
		}
		if err := root.WriteFile(name, data, 0o644); err != nil {
			return p, err
		}
		files = append(files, sourceFile{Path: f.Path, SHA256: f.SHA256, Size: f.Size})
	}
	// The manifest is what the app and the hooks read, so it has to agree
	// with the listing that described it.
	var manifest struct {
		Version string `json:"version"`
		Main    string `json:"main"`
	}
	b, err := root.ReadFile(hooks.PluginManifest)
	if err != nil || json.Unmarshal(b, &manifest) != nil {
		return p, marketErr(http.StatusBadGateway, "plugin_invalid", "%s can't be installed: its %s is not JSON", id, hooks.PluginManifest)
	}
	if manifest.Version != p.Version {
		return p, marketErr(http.StatusBadGateway, "plugin_invalid", "%s can't be installed: the listing says %s, its %s says %q", id, p.Version, hooks.PluginManifest, manifest.Version)
	}
	if manifest.Main != "" {
		if _, err := root.Stat(filepath.FromSlash(path.Clean("/" + manifest.Main)[1:])); err != nil {
			return p, marketErr(http.StatusBadGateway, "plugin_invalid", "%s can't be installed: the listing has no %s, its main module", id, manifest.Main)
		}
	}

	// Swap: the old folder steps aside, the new one takes its name, and the
	// old one comes back if that fails.
	target := filepath.Join(plugins, id)
	old := stage + ".old"
	if before.present {
		if err := os.Rename(target, old); err != nil {
			return p, err
		}
		defer os.RemoveAll(old)
	}
	if err := os.Rename(stage, target); err != nil {
		if before.present {
			os.Rename(old, target)
		}
		return p, err
	}
	dest := filepath.Join(a.pluginsDir(), id)
	// Installing never turns a plugin on. A permission left from before
	// still fits when the manifest and main module are the same bytes, but
	// other files may have changed: unless this put back exactly what was
	// there, it is forgotten, and the person is asked again.
	unchanged := before.managed && sameFiles(before.source.Files, p.Files)
	forgot := false
	if !unchanged && hooks.PluginEnabled(dest) {
		if err := hooks.ForbidPlugin(dest); err != nil {
			return p, err
		}
		forgot = true
	}
	// Its helper on the boxes, if it was on with one: held while the
	// person is asked about the new files when they name the same helper,
	// removed when they name another or none (helpercleanup.go).
	if !unchanged {
		a.helperPluginReplaced(id, forgot)
	}
	sources[id] = pluginSource{Registry: safeURL(registry), Version: p.Version, At: a.cfg.Now().UTC(), Files: files}
	if err := a.writePluginSources(sources); err != nil {
		return p, err
	}
	a.cfg.Log.Printf("installed plugin %s %s from %s", id, p.Version, safeURL(registry))
	return merge(p, a.installedPlugin(id, sources)), nil
}

// uninstallPlugin removes a plugin the marketplace installed, and the
// permission it had. A folder someone made by hand is theirs to remove.
func (a *Agent) uninstallPlugin(id string) error {
	a.market.change.Lock()
	defer a.market.change.Unlock()
	sources := a.readPluginSources()
	if _, ok := sources[id]; !ok {
		return marketErr(http.StatusConflict, "plugin_by_hand", "%s wasn't installed from the listing; remove ~/.berth/plugins/%s yourself", id, id)
	}
	dest := filepath.Join(a.pluginsDir(), id)
	// Its permission goes first, so its hooks stop before its files do.
	if err := hooks.ForbidPlugin(dest); err != nil {
		return err
	}
	if _, err := os.Lstat(dest); err == nil {
		// Out of the plugins folder in one step, then deleted.
		gone, plugins, err := a.pluginStage(id)
		if err != nil {
			return err
		}
		defer os.RemoveAll(gone)
		if err := os.Rename(filepath.Join(plugins, id), filepath.Join(gone, id)); err != nil {
			return err
		}
	}
	delete(sources, id)
	if err := a.writePluginSources(sources); err != nil {
		return err
	}
	a.cfg.Log.Printf("removed plugin %s", id)
	return nil
}

func (a *Agent) marketplaceRoutes(mux *http.ServeMux) {
	// ?fresh=1 reads the registry again rather than the listing kept.
	mux.HandleFunc("GET /v1/marketplace", func(w http.ResponseWriter, r *http.Request) {
		m, err := a.marketplace(r.Context(), r.URL.Query().Get("fresh") == "1")
		if err != nil {
			writeMarketError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, m)
	})
	mux.HandleFunc("POST /v1/marketplace/{id}/install", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !pluginIDPattern.MatchString(id) {
			writeError(w, http.StatusBadRequest, "not a plugin id")
			return
		}
		var req struct {
			Version string `json:"version"`
			Replace bool   `json:"replace"`
		}
		// The body is optional: with none, the listed version is installed.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil || (len(strings.TrimSpace(string(body))) > 0 && json.Unmarshal(body, &req) != nil) {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		done, err := a.work.begin("install plugin " + id)
		if err != nil {
			writeCoded(w, http.StatusServiceUnavailable, err.Error(), "agent_restarting")
			return
		}
		defer done()
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		p, err := a.installPlugin(ctx, id, req.Version, req.Replace)
		if err != nil {
			a.cfg.Log.Printf("installing plugin %s: %v", id, err)
			writeMarketError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	})
	mux.HandleFunc("DELETE /v1/marketplace/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !pluginIDPattern.MatchString(id) {
			writeError(w, http.StatusBadRequest, "not a plugin id")
			return
		}
		// Read before its folder goes: whether it brings a helper.
		carries := a.carriesHelper(id)
		if err := a.uninstallPlugin(id); err != nil {
			writeMarketError(w, err)
			return
		}
		out := struct {
			Removed string          `json:"removed"`
			Helpers []HelperRemoved `json:"helpers,omitempty"`
		}{Removed: id}
		// Its helper goes from the boxes with it, and what it recorded
		// there too (helpercleanup.go).
		if carries {
			out.Helpers = a.removeHelpers(id, true, "its plugin was uninstalled", helperRemovalWait)
		}
		writeJSON(w, http.StatusOK, out)
	})
}
