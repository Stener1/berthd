package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/hooks"
	"github.com/cosscom/shipyard/internal/wire"
)

// A plugin can bring a helper, a program that runs on a box
// ("box": {"helper": …} in its berth-plugin.json; internal/box/helpers.go
// runs it). These routes put it there and take it away:
//
//	GET    /v1/plugins/{id}/helper?box=B            its state on that box
//	POST   /v1/plugins/{id}/helper?box=B            install or update it, and turn it on
//	DELETE /v1/plugins/{id}/helper?box=B[&purge=1]  stop and remove it; purge: its data too
//
// The manifest names the helper's build for each platform by its address,
// size and SHA-256. The person allowed that manifest (its hash is the
// plugin's consent), so the checksum is what they allowed, wherever the
// bytes are fetched from. This computer does the fetching, checks them, and
// uploads them to the box over Shipyard's own connection: a box needs no
// way out to the internet, and never runs what was not checked. Only a
// plugin that is installed and allowed as it is now gets a helper.

// The states a helper has here that a box does not know: the plugin has no
// helper at all, and this box cannot have it.
const (
	helperNone        = "none"
	helperUnsupported = "unsupported"
)

// HelperStatus is a plugin's helper on one box, as the app shows it.
type HelperStatus struct {
	Plugin string `json:"plugin"`
	Box    string `json:"box"`
	// State is none (the plugin has no helper), unsupported (no build for
	// the box's platform, or its berthd cannot run helpers), off,
	// installing, starting, running or failed.
	State string `json:"state"`
	// Platform is the box's, as in "linux-arm64"; empty when it was not asked.
	Platform string `json:"platform"`
	// Version is what the box runs, Latest what the plugin's manifest has,
	// and Update says they are not the same helper.
	Version string `json:"version,omitempty"`
	Latest  string `json:"latest,omitempty"`
	Update  bool   `json:"update,omitempty"`
	Error   string `json:"error,omitempty"`
	// Attached, in the answer to DELETE, are the services running on the box
	// that were started with what the helper answered: they still point at
	// it, and need restarting to be as the project wrote them.
	Attached []box.HelperService `json:"attached,omitempty"`
}

// helperWork is the installs under way, by plugin and box.
type helperWork struct {
	mu   sync.Mutex
	busy map[string]bool
}

func (w *helperWork) begin(key string) (done func(), ok bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.busy[key] {
		return nil, false
	}
	if w.busy == nil {
		w.busy = map[string]bool{}
	}
	w.busy[key] = true
	return func() {
		w.mu.Lock()
		delete(w.busy, key)
		w.mu.Unlock()
	}, true
}

func (w *helperWork) running(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.busy[key]
}

// pluginHelper reads the helper an installed plugin declares, if the person
// has allowed the plugin as it is now. has is false for one with no helper.
func (a *Agent) pluginHelper(id string) (m box.HelperManifest, has bool, err error) {
	dir := filepath.Join(a.pluginsDir(), id)
	manifest, state, err := hooks.AllowedPluginManifest(dir)
	if err != nil {
		return m, false, marketErr(http.StatusConflict, "plugin_not_installed", "%s is not installed on this computer", id)
	}
	if !state.Enabled {
		if state.Changed {
			return m, false, marketErr(http.StatusConflict, "plugin_not_allowed", "%s has changed since you allowed it; review it again in Settings → Plugins before its helper is installed", id)
		}
		return m, false, marketErr(http.StatusConflict, "plugin_not_allowed", "%s is off; turn it on in Settings → Plugins before its helper is installed", id)
	}
	var doc struct {
		Box *struct {
			Helper *box.HelperManifest `json:"helper"`
		} `json:"box"`
	}
	if err := json.Unmarshal(manifest, &doc); err != nil {
		return m, false, marketErr(http.StatusConflict, "helper_invalid", "%s's %s cannot be read", id, hooks.PluginManifest)
	}
	if doc.Box == nil || doc.Box.Helper == nil {
		return m, false, nil
	}
	if err := doc.Box.Helper.Validate(); err != nil {
		return m, false, marketErr(http.StatusConflict, "helper_invalid", "%s's helper cannot be used: %v", id, err)
	}
	return *doc.Box.Helper, true, nil
}

// helperBox is the client for a box's helper routes, or why it cannot be
// reached: as the app's other box calls say it.
func (a *Agent) helperBox(name string) (*box.Client, error) {
	if name == "" {
		return nil, marketErr(http.StatusBadRequest, "", "which box? add ?box=NAME")
	}
	a.sync()
	c, ok := a.client(name)
	if !ok {
		return nil, marketErr(http.StatusNotFound, "box_unknown", "no paired box named %s", name)
	}
	if msg, _, away := a.away(name); away {
		return nil, marketErr(http.StatusServiceUnavailable, "box_unreachable", "%s", msg)
	}
	bc := box.NewClient(c)
	bc.Origin = "app"
	return bc, nil
}

// boxProblem turns an error from a box into the app's: the box's own
// refusal with its status and code, or that it could not be reached.
func (a *Agent) boxProblem(err error) error {
	var he *box.HelperError
	if errors.As(err, &he) {
		code := he.Code
		if code == "" {
			code = "box_error"
		}
		return marketErr(he.Status, code, "%s", he.Message)
	}
	a.checkSoon()
	status := http.StatusBadGateway
	if wire.Unsent(err) {
		status = http.StatusServiceUnavailable
	}
	return marketErr(status, "box_unreachable", "%v", err)
}

// noHelpers reports whether a box said it cannot run helpers at all.
func noHelpers(err error) (string, bool) {
	var he *box.HelperError
	if errors.As(err, &he) && he.NoHelpers {
		return he.Message, true
	}
	return "", false
}

// helperOn is a box's word on a helper, with what the plugin's manifest
// adds: whether there is a build for the box, and whether it is this one.
func helperOn(id, boxName string, m box.HelperManifest, st box.HelperStatus) HelperStatus {
	out := HelperStatus{Plugin: id, Box: boxName, State: st.State, Platform: st.Platform, Version: st.Version, Latest: m.Version, Error: st.Error, Attached: st.Attached}
	spec, _, ok := m.Spec(st.Platform)
	switch {
	case !ok && st.State == box.HelperOff:
		out.State = helperUnsupported
		out.Error = m.Name + " has no build for " + st.Platform
	case ok && st.State != box.HelperOff && st.State != box.HelperInstalling:
		out.Update = st.Digest != spec.Digest()
	}
	return out
}

func (a *Agent) helperStatus(ctx context.Context, id, boxName string) (HelperStatus, error) {
	m, has, err := a.pluginHelper(id)
	if err != nil {
		return HelperStatus{}, err
	}
	if !has {
		return HelperStatus{Plugin: id, Box: boxName, State: helperNone}, nil
	}
	bc, err := a.helperBox(boxName)
	if err != nil {
		return HelperStatus{}, err
	}
	st, err := bc.Helper(ctx, id)
	if msg, no := noHelpers(err); no {
		return HelperStatus{Plugin: id, Box: boxName, State: helperUnsupported, Latest: m.Version, Error: msg}, nil
	}
	if err != nil {
		return HelperStatus{}, a.boxProblem(err)
	}
	out := helperOn(id, boxName, m, st)
	// The download and the upload are this computer's part of installing.
	if a.helperWork.running(id+"\x00"+boxName) && out.State != helperUnsupported {
		out.State, out.Error = box.HelperInstalling, ""
	}
	return out, nil
}

// installHelper puts a plugin's helper on a box, or brings the one there up
// to the manifest's, and turns it on.
func (a *Agent) installHelper(ctx context.Context, id, boxName string) (HelperStatus, error) {
	m, has, err := a.pluginHelper(id)
	if err != nil {
		return HelperStatus{}, err
	}
	if !has {
		return HelperStatus{}, marketErr(http.StatusConflict, "helper_none", "%s has no helper to install", id)
	}
	bc, err := a.helperBox(boxName)
	if err != nil {
		return HelperStatus{}, err
	}
	done, ok := a.helperWork.begin(id + "\x00" + boxName)
	if !ok {
		return HelperStatus{}, marketErr(http.StatusConflict, "helper_busy", "%s's helper is already being installed on %s", id, boxName)
	}
	defer done()
	// Turned on here by the person: a removal still waiting for this box
	// is called off, and the helper is this computer's to remove later
	// (helpercleanup.go).
	a.ledger().settle(id, boxName)
	a.noteHelperGrant(id)

	st, err := bc.Helper(ctx, id)
	if msg, no := noHelpers(err); no {
		return HelperStatus{}, marketErr(http.StatusConflict, "helper_unsupported", "%s cannot run %s's helper: %s", boxName, id, msg)
	}
	if err != nil {
		return HelperStatus{}, a.boxProblem(err)
	}
	spec, asset, ok := m.Spec(st.Platform)
	if !ok {
		return HelperStatus{}, marketErr(http.StatusConflict, "helper_unsupported", "%s has no build for %s, which %s runs", m.Name, st.Platform, boxName)
	}
	if err := spec.Validate(); err != nil {
		return HelperStatus{}, marketErr(http.StatusConflict, "helper_invalid", "%s's helper cannot be used: %v", id, err)
	}
	// Already this helper, and running: there is nothing to do.
	if st.State == box.HelperRunning && st.Digest == spec.Digest() {
		return helperOn(id, boxName, m, st), nil
	}
	// The box holds this very program (it failed, or is run another way
	// now): it is started again without sending a byte.
	if st.State != box.HelperOff && st.SHA256 == spec.SHA256 && st.Name == spec.Name && st.Version == spec.Version {
		now, err := bc.InstallHelper(ctx, id, spec, nil)
		var he *box.HelperError
		if err == nil {
			a.cfg.Log.Printf("helper %s %s on %s: started again", id, spec.Version, boxName)
			return helperOn(id, boxName, m, now), nil
		}
		if !errors.As(err, &he) || he.Code != "helper_missing" {
			return HelperStatus{}, a.boxProblem(err)
		}
	}
	program, err := fetchHelper(ctx, asset)
	if err != nil {
		a.cfg.Log.Printf("helper %s for %s: %v", id, boxName, err)
		return HelperStatus{}, err
	}
	now, err := bc.InstallHelper(ctx, id, spec, program)
	if err != nil {
		return HelperStatus{}, a.boxProblem(err)
	}
	a.cfg.Log.Printf("helper %s %s (%s) installed on %s", id, spec.Version, st.Platform, boxName)
	return helperOn(id, boxName, m, now), nil
}

// helperURL is where a helper's build is fetched from: any https address
// (the checksum, not the host, is what was allowed), or plain http on this
// computer, which tests use. An address that carries a user or a password
// is refused: it would be a credential in a manifest.
func helperURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return nil, errors.New("is not a plain address")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopbackHost(u.Hostname())) {
		return nil, errors.New("is not an https:// address")
	}
	return u, nil
}

// fetchHelper downloads one build of a helper and returns it only if it is
// exactly the size and SHA-256 the manifest gives.
func fetchHelper(ctx context.Context, asset box.HelperAsset) ([]byte, error) {
	u, err := helperURL(asset.URL)
	if err != nil {
		return nil, marketErr(http.StatusConflict, "helper_invalid", "the helper's address %v", err)
	}
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			// A release's download leads to wherever its files are kept:
			// anywhere, as long as it is https, or stays on this computer.
			if req.URL.Scheme != "https" && !(req.URL.Scheme == u.Scheme && req.URL.Host == u.Host) {
				return errors.New("it redirects to an address that is not https")
			}
			return nil
		},
	}
	failed := func(err error) error {
		// A *url.Error repeats the whole address, a query's token and all.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return marketErr(http.StatusBadGateway, "helper_download", "could not download the helper from %s: %v", safeURL(u), err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, failed(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, failed(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, marketErr(http.StatusBadGateway, "helper_download", "could not download the helper: %s answered %s", safeURL(u), resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, asset.Size+1))
	if err != nil {
		return nil, failed(err)
	}
	if int64(len(data)) != asset.Size {
		return nil, marketErr(http.StatusBadGateway, "helper_mismatch", "the helper at %s is not the %d bytes the plugin's manifest gives; nothing was installed", safeURL(u), asset.Size)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != asset.SHA256 {
		return nil, marketErr(http.StatusBadGateway, "helper_mismatch", "the helper at %s does not match the checksum in the plugin's manifest; nothing was installed", safeURL(u))
	}
	return data, nil
}

// removeHelper stops a plugin's helper on a box and removes it. Removing is
// never held back by the plugin's own state: a plugin that was turned off,
// changed or uninstalled must still be able to have its helper taken away.
func (a *Agent) removeHelper(ctx context.Context, id, boxName string, purge bool) (HelperStatus, error) {
	bc, err := a.helperBox(boxName)
	if err != nil {
		return HelperStatus{}, err
	}
	m, has, _ := a.pluginHelper(id)
	st, err := bc.RemoveHelper(ctx, id, purge)
	if msg, no := noHelpers(err); no {
		return HelperStatus{Plugin: id, Box: boxName, State: helperUnsupported, Latest: m.Version, Error: msg}, nil
	}
	if err != nil {
		return HelperStatus{}, a.boxProblem(err)
	}
	a.cfg.Log.Printf("helper %s removed from %s (data %s)", id, boxName, map[bool]string{true: "removed", false: "kept"}[purge])
	if !has {
		return HelperStatus{Plugin: id, Box: boxName, State: st.State, Platform: st.Platform, Attached: st.Attached}, nil
	}
	return helperOn(id, boxName, m, st), nil
}

func (a *Agent) helperRoutes(mux *http.ServeMux) {
	handle := func(pattern string, timeout time.Duration, do func(ctx context.Context, id, boxName string, r *http.Request) (HelperStatus, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			if !pluginIDPattern.MatchString(id) {
				writeError(w, http.StatusBadRequest, "not a plugin id")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			st, err := do(ctx, id, r.URL.Query().Get("box"), r)
			if err != nil {
				writeMarketError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, st)
		})
	}
	handle("GET /v1/plugins/{id}/helper", 20*time.Second, func(ctx context.Context, id, boxName string, r *http.Request) (HelperStatus, error) {
		return a.helperStatus(ctx, id, boxName)
	})
	handle("POST /v1/plugins/{id}/helper", 15*time.Minute, func(ctx context.Context, id, boxName string, r *http.Request) (HelperStatus, error) {
		done, err := a.work.begin("install the helper of " + id + " on " + boxName)
		if err != nil {
			return HelperStatus{}, marketErr(http.StatusServiceUnavailable, "agent_restarting", "%v", err)
		}
		defer done()
		return a.installHelper(ctx, id, boxName)
	})
	handle("DELETE /v1/plugins/{id}/helper", time.Minute, func(ctx context.Context, id, boxName string, r *http.Request) (HelperStatus, error) {
		return a.removeHelper(ctx, id, boxName, r.URL.Query().Get("purge") == "1")
	})
}
