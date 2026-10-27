package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/hooks"
	"github.com/cosscom/shipyard/internal/statefile"
)

// A helper does not outlive its plugin. When a plugin that brings one stops
// being allowed, this computer stops the helper and removes it from every
// paired box that has it:
//
//	turned off                         removed; its data on the box is kept
//	uninstalled                        removed, and its data with it
//	changed, and its helper with it    removed; its data is kept
//	its folder is gone                 removed; its data is kept
//
// One case is left alone. A plugin whose files changed (an update from the
// listing, or an edit) is off until the person allows the new files, and
// the app asks at once. If the helper the new manifest names is the very
// one that was allowed, down to its checksums, the one on the boxes is what
// both versions run: it is held, not removed, until the person answers.
// Allowing the new version keeps it; turning the plugin off removes it.
//
// A box that cannot be reached is done when it next connects. What is still
// to do is kept in helpers.json in the agent's state directory, so it is
// done by the next agent too. A box that was forgotten meanwhile is let go.
//
// Turning a plugin back on installs nothing: its helper is turned on for
// each box again, by the person, as the first time.

// helperGrant is a plugin that was on with a helper: the helper may be on
// boxes, and is this computer's to take away.
type helperGrant struct {
	// Spec names the helper as the allowed manifest described it
	// (helperManifestDigest): its words, and every platform's build.
	Spec string `json:"spec"`
	// Awaiting: an update replaced the plugin's files and its permission
	// was forgotten with them, so the person is being asked again.
	Awaiting bool `json:"awaiting,omitempty"`
}

// helperRemoval is a helper still to be removed from a box.
type helperRemoval struct {
	Plugin string `json:"plugin"`
	Box    string `json:"box"`
	// Purge: its data goes too (the plugin was uninstalled).
	Purge bool      `json:"purge,omitempty"`
	Since time.Time `json:"since"`
}

type helperLedgerDoc struct {
	Granted map[string]helperGrant `json:"granted,omitempty"`
	Pending []helperRemoval        `json:"pending,omitempty"`
}

// helperLedger is helpers.json: the plugins whose helpers are this
// computer's to remove, and the removals not done yet.
type helperLedger struct {
	once   sync.Once
	mu     sync.Mutex
	path   string
	loaded bool
	doc    helperLedgerDoc
	// wake has the loop look again now (keepHelpersTidy).
	wake chan struct{}
}

func (a *Agent) ledger() *helperLedger {
	l := &a.helperLedger
	l.once.Do(func() {
		l.path = filepath.Join(a.cfg.Dir, "helpers.json")
		l.wake = make(chan struct{}, 1)
	})
	return l
}

func (l *helperLedger) load() {
	if l.loaded {
		return
	}
	l.loaded = true
	if b, err := os.ReadFile(l.path); err == nil {
		// One that cannot be read is started over: nothing is removed on
		// the word of a file that is not whole.
		if json.Unmarshal(b, &l.doc) != nil {
			l.doc = helperLedgerDoc{}
		}
	}
	if l.doc.Granted == nil {
		l.doc.Granted = map[string]helperGrant{}
	}
}

// change reads the ledger, lets fn change it, and writes it if fn says it
// did.
func (l *helperLedger) change(fn func(doc *helperLedgerDoc) (changed bool)) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.load()
	if !fn(&l.doc) {
		return nil
	}
	if len(l.doc.Granted) == 0 && len(l.doc.Pending) == 0 {
		if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	b, err := json.MarshalIndent(l.doc, "", "  ")
	if err != nil {
		return err
	}
	return statefile.Write(l.path, append(b, '\n'))
}

func (l *helperLedger) grant(id string) (helperGrant, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.load()
	g, ok := l.doc.Granted[id]
	return g, ok
}

func (l *helperLedger) granted() map[string]helperGrant {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.load()
	out := make(map[string]helperGrant, len(l.doc.Granted))
	for id, g := range l.doc.Granted {
		out[id] = g
	}
	return out
}

func (l *helperLedger) pending() []helperRemoval {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.load()
	return append([]helperRemoval(nil), l.doc.Pending...)
}

func (l *helperLedger) pendingFor(plugin, boxName string) (helperRemoval, bool) {
	for _, e := range l.pending() {
		if e.Plugin == plugin && e.Box == boxName {
			return e, true
		}
	}
	return helperRemoval{}, false
}

// settle takes a removal off the list: it was done, or given up.
func (l *helperLedger) settle(plugin, boxName string) error {
	return l.change(func(doc *helperLedgerDoc) bool {
		for i, e := range doc.Pending {
			if e.Plugin == plugin && e.Box == boxName {
				doc.Pending = append(doc.Pending[:i:i], doc.Pending[i+1:]...)
				return true
			}
		}
		return false
	})
}

// wakeHelperTidy has the agent look now: at the removals still to do, and at
// plugins that changed.
func (a *Agent) wakeHelperTidy() {
	select {
	case a.ledger().wake <- struct{}{}:
	default:
	}
}

// manifestHelper is the helper a plugin's manifest names, whoever allowed
// it: has is false for none, and for one that cannot be used.
func manifestHelper(manifest []byte) (m box.HelperManifest, has bool) {
	var doc struct {
		Box *struct {
			Helper *box.HelperManifest `json:"helper"`
		} `json:"box"`
	}
	if json.Unmarshal(manifest, &doc) != nil || doc.Box == nil || doc.Box.Helper == nil || doc.Box.Helper.Validate() != nil {
		return m, false
	}
	return *doc.Box.Helper, true
}

// helperManifestDigest names a helper by everything its manifest says of
// it: how it is run, and the address, size and checksum of each build. Two
// manifests with one digest ask every box for the same helper.
func helperManifestDigest(m box.HelperManifest) string {
	if m.Args == nil {
		m.Args = []string{}
	}
	if len(m.AttachEnv) == 0 {
		m.AttachEnv = nil
	}
	if len(m.Assets) == 0 {
		m.Assets = nil
	}
	b, _ := json.Marshal(m)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// carriesHelper says whether a plugin's helper may be on boxes: the plugin
// was on with one, or its manifest names one now.
func (a *Agent) carriesHelper(id string) bool {
	if _, ok := a.ledger().grant(id); ok {
		return true
	}
	manifest, err := os.ReadFile(filepath.Join(a.pluginsDir(), id, hooks.PluginManifest))
	if err != nil {
		return false
	}
	_, has := manifestHelper(manifest)
	return has
}

// noteHelperGrant records a plugin that is on now, if it brings a helper:
// from here on its helper is this computer's to remove.
func (a *Agent) noteHelperGrant(id string) {
	manifest, state, err := hooks.AllowedPluginManifest(filepath.Join(a.pluginsDir(), id))
	if err != nil || !state.Enabled {
		return
	}
	m, has := manifestHelper(manifest)
	if !has {
		return
	}
	want := helperGrant{Spec: helperManifestDigest(m)}
	if err := a.ledger().change(func(doc *helperLedgerDoc) bool {
		if doc.Granted[id] == want {
			return false
		}
		doc.Granted[id] = want
		return true
	}); err != nil {
		a.cfg.Log.Printf("helper %s: %v", id, err)
	}
}

// The states of a helper's removal from one box.
const (
	// removalDone: it was there, and is stopped and removed.
	removalDone = "removed"
	// removalNone: the box had no such helper.
	removalNone = "none"
	// removalPending: the box cannot be reached; it is done when it connects.
	removalPending = "pending"
	// removalFailed: the box refused, and asking again would not help.
	removalFailed = "failed"
)

// HelperRemoved is what became of a plugin's helper on one box when the
// plugin was turned off or uninstalled.
type HelperRemoved struct {
	Box string `json:"box"`
	// State is removed, none (the box had no such helper), pending (the
	// box is offline: it is removed when the box next connects) or failed.
	State string `json:"state"`
	// Purged: what the helper kept on the box was removed too.
	Purged bool   `json:"purged,omitempty"`
	Error  string `json:"error,omitempty"`
	// Attached are the services running on the box that were started with
	// what the helper answered: they need restarting.
	Attached []box.HelperService `json:"attached,omitempty"`
}

// pairedBoxes is the names of the boxes this computer is paired with.
func (a *Agent) pairedBoxes() []string {
	a.sync()
	a.mu.Lock()
	defer a.mu.Unlock()
	names := make([]string, 0, len(a.clients))
	for name := range a.clients {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// removeHelpers has a plugin's helper removed from every paired box: now
// where the box answers, later where it does not. It waits at most wait for
// the boxes and says what became of each; the rest go on in the background.
// The plugin's grant is forgotten: there is nothing left of it to hold.
func (a *Agent) removeHelpers(id string, purge bool, why string, wait time.Duration) []HelperRemoved {
	boxes := a.pairedBoxes()
	now := a.cfg.Now().UTC()
	if err := a.ledger().change(func(doc *helperLedgerDoc) bool {
		delete(doc.Granted, id)
		for _, name := range boxes {
			found := false
			for i := range doc.Pending {
				if e := &doc.Pending[i]; e.Plugin == id && e.Box == name {
					e.Purge, found = e.Purge || purge, true
				}
			}
			if !found {
				doc.Pending = append(doc.Pending, helperRemoval{Plugin: id, Box: name, Purge: purge, Since: now})
			}
		}
		return true
	}); err != nil {
		// Not remembered, but still tried now.
		a.cfg.Log.Printf("helper %s: could not note its removal: %v", id, err)
	}
	a.cfg.Log.Printf("helper %s: %s; removing it from %d paired box(es) (data %s)", id, why, len(boxes), map[bool]string{true: "removed", false: "kept"}[purge])
	results := make(chan HelperRemoved, len(boxes))
	for _, name := range boxes {
		go func() {
			// Under the agent's own context: whoever asked may stop waiting.
			ctx, cancel := context.WithTimeout(a.runCtx(), time.Minute)
			defer cancel()
			results <- a.removeHelperOn(ctx, helperRemoval{Plugin: id, Box: name, Purge: purge})
		}()
	}
	out := make([]HelperRemoved, 0, len(boxes))
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
	waiting:
		for range boxes {
			select {
			case r := <-results:
				out = append(out, r)
			case <-timer.C:
				break waiting
			}
		}
	}
	seen := map[string]bool{}
	for _, r := range out {
		seen[r.Box] = true
	}
	for _, name := range boxes {
		if !seen[name] {
			out = append(out, HelperRemoved{Box: name, State: removalPending})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Box < out[j].Box })
	return out
}

// removeHelperOn does one removal that is on the list, and takes it off
// once it is done or cannot be: the box has no such helper, was forgotten,
// or refused. One that could not be reached stays on the list.
func (a *Agent) removeHelperOn(ctx context.Context, want helperRemoval) HelperRemoved {
	out := HelperRemoved{Box: want.Box, State: removalPending}
	// One thing at a time for a plugin on a box: an install under way is
	// the person's newer word, and takes this off the list itself.
	done, ok := a.helperWork.begin(want.Plugin + "\x00" + want.Box)
	if !ok {
		return out
	}
	defer done()
	e, ok := a.ledger().pendingFor(want.Plugin, want.Box)
	if !ok {
		// Done already, or called off.
		out.State = removalNone
		return out
	}
	settle := func(state, msg string) HelperRemoved {
		if err := a.ledger().settle(e.Plugin, e.Box); err != nil {
			a.cfg.Log.Printf("helper %s on %s: %v", e.Plugin, e.Box, err)
		}
		out.State, out.Error = state, msg
		return out
	}
	c, ok := a.client(e.Box)
	if !ok {
		a.cfg.Log.Printf("helper %s: %s is no longer paired; its removal there is let go", e.Plugin, e.Box)
		return settle(removalNone, "")
	}
	if _, _, away := a.away(e.Box); away {
		return out
	}
	bc := box.NewClient(c)
	bc.Origin = "app"
	// refused says a box answered, and would answer the same again.
	refused := func(err error) (string, bool) {
		var he *box.HelperError
		if errors.As(err, &he) && he.Status >= 400 && he.Status < 500 && he.Status != http.StatusRequestTimeout && he.Status != http.StatusTooManyRequests {
			return he.Message, true
		}
		return "", false
	}
	failed := func(err error) HelperRemoved {
		if _, no := noHelpers(err); no {
			// It cannot run helpers, so it has none.
			return settle(removalNone, "")
		}
		if msg, no := refused(err); no {
			a.cfg.Log.Printf("helper %s: %s refused its removal: %s", e.Plugin, e.Box, msg)
			return settle(removalFailed, msg)
		}
		// Not reached, or in trouble: tried again when it connects.
		a.checkSoon()
		out.Error = err.Error()
		return out
	}
	st, err := bc.Helper(ctx, e.Plugin)
	if err != nil {
		return failed(err)
	}
	was := st.State != box.HelperOff
	if !was && !e.Purge {
		return settle(removalNone, "")
	}
	// With purge the box is asked even when nothing runs: what an earlier
	// helper kept there goes too.
	st, err = bc.RemoveHelper(ctx, e.Plugin, e.Purge)
	if err != nil {
		return failed(err)
	}
	out.Purged, out.Attached = e.Purge, st.Attached
	if !was {
		return settle(removalNone, "")
	}
	a.cfg.Log.Printf("helper %s removed from %s (data %s)", e.Plugin, e.Box, map[bool]string{true: "removed", false: "kept"}[e.Purge])
	return settle(removalDone, "")
}

// runHelperRemovals tries every removal on the list once.
func (a *Agent) runHelperRemovals(ctx context.Context) {
	pending := a.ledger().pending()
	if len(pending) == 0 {
		return
	}
	a.sync()
	var wg sync.WaitGroup
	for _, e := range pending {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			a.removeHelperOn(ctx, e)
		}()
	}
	wg.Wait()
}

// sweepHelperGrants looks at every plugin that was on with a helper, and at
// the ones that are on now: what changed behind the agent's back (a file
// edited, a folder deleted, a permission taken away by hand) is found here.
func (a *Agent) sweepHelperGrants() {
	// Not while a plugin is being installed or removed: its folder is
	// half-way between two versions, and that code settles it itself.
	if !a.market.change.TryLock() {
		return
	}
	defer a.market.change.Unlock()
	manifests, _ := filepath.Glob(filepath.Join(a.pluginsDir(), "*", hooks.PluginManifest))
	for _, m := range manifests {
		if id := filepath.Base(filepath.Dir(m)); pluginIDPattern.MatchString(id) {
			a.noteHelperGrant(id)
		}
	}
	for id := range a.ledger().granted() {
		a.settleHelperGrant(id)
	}
}

// settleHelperGrant decides what a plugin's state now means for its helper
// on the boxes: kept (the plugin is on), held (it waits for the person to
// allow a new version that names the same helper), or removed. The caller
// holds market.change.
func (a *Agent) settleHelperGrant(id string) {
	g, ok := a.ledger().grant(id)
	if !ok {
		return
	}
	manifest, state, err := hooks.AllowedPluginManifest(filepath.Join(a.pluginsDir(), id))
	if err != nil {
		a.removeHelpers(id, false, "its plugin's folder is gone", 0)
		return
	}
	m, has := manifestHelper(manifest)
	switch {
	case state.Enabled && has:
		a.noteHelperGrant(id)
	case state.Enabled:
		a.removeHelpers(id, false, "its plugin no longer brings a helper", 0)
	case !has:
		a.removeHelpers(id, false, "its plugin changed and no longer brings a helper", 0)
	case (state.Changed || g.Awaiting) && helperManifestDigest(m) == g.Spec:
		// Held: the helper on the boxes is the one the new files name too.
	case state.Changed || g.Awaiting:
		a.removeHelpers(id, false, "its plugin changed, and names another helper", 0)
	default:
		a.removeHelpers(id, false, "its plugin is off", 0)
	}
}

// helperPluginReplaced is called when an install has put other files in a
// plugin's folder. forgot says its permission was forgotten with them.
func (a *Agent) helperPluginReplaced(id string, forgot bool) {
	if _, ok := a.ledger().grant(id); !ok {
		return
	}
	if forgot {
		if err := a.ledger().change(func(doc *helperLedgerDoc) bool {
			g := doc.Granted[id]
			g.Awaiting = true
			doc.Granted[id] = g
			return true
		}); err != nil {
			a.cfg.Log.Printf("helper %s: %v", id, err)
		}
	}
	a.settleHelperGrant(id)
}

// keepHelpersTidy does what is on the list when the agent starts, when a
// box connects, and now and then; and looks for plugins that changed.
func (a *Agent) keepHelpersTidy(ctx context.Context) {
	stop := a.bus.Observe(func(e Event) {
		if e.Type == EventBoxConnected {
			a.wakeHelperTidy()
		}
	})
	defer stop()
	ticker := time.NewTicker(helperTidyEvery)
	defer ticker.Stop()
	for {
		a.sweepHelperGrants()
		a.runHelperRemovals(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-a.ledger().wake:
		}
	}
}

const (
	// helperTidyEvery is how often the agent looks without being told to.
	helperTidyEvery = time.Minute
	// helperRemovalWait is how long turning a plugin off, or uninstalling
	// it, waits for the boxes before it answers: the rest is done after.
	helperRemovalWait = 15 * time.Second
)

// PluginHelpers is where a plugin's helper is, for the app to say before
// the plugin is turned off or removed.
type PluginHelpers struct {
	Plugin string `json:"plugin"`
	// Name is the helper's, as the plugin's manifest names it; empty when
	// the manifest names none.
	Name string `json:"name,omitempty"`
	// Held: the plugin is off until its new files are allowed, and its
	// helper was left running on the boxes meanwhile.
	Held  bool             `json:"held,omitempty"`
	Boxes []PluginHelperOn `json:"boxes"`
}

// PluginHelperOn is a plugin's helper on one paired box.
type PluginHelperOn struct {
	Box string `json:"box"`
	// State is the box's word (off, installing, starting, running,
	// failed), offline for a box that cannot be asked now, or unsupported
	// for one that cannot run helpers.
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
	// Pending is a removal waiting for the box: "remove", or "purge" when
	// its data goes too.
	Pending string `json:"pending,omitempty"`
}

// pluginHelpers asks every paired box for a plugin's helper. It asks for
// any plugin, on or off: it is what the app shows before turning one off.
func (a *Agent) pluginHelpers(ctx context.Context, id string) PluginHelpers {
	out := PluginHelpers{Plugin: id, Boxes: []PluginHelperOn{}}
	if manifest, state, err := hooks.AllowedPluginManifest(filepath.Join(a.pluginsDir(), id)); err == nil {
		if m, has := manifestHelper(manifest); has {
			out.Name = m.Name
		}
		_, granted := a.ledger().grant(id)
		out.Held = granted && !state.Enabled
	}
	boxes := a.pairedBoxes()
	rows := make([]PluginHelperOn, len(boxes))
	var wg sync.WaitGroup
	for i, name := range boxes {
		rows[i] = PluginHelperOn{Box: name, State: StateOffline}
		if e, ok := a.ledger().pendingFor(id, name); ok {
			rows[i].Pending = map[bool]string{true: "purge", false: "remove"}[e.Purge]
		}
		c, ok := a.client(name)
		if _, _, away := a.away(name); !ok || away {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			bc := box.NewClient(c)
			bc.Origin = "app"
			st, err := bc.Helper(ctx, id)
			switch _, no := noHelpers(err); {
			case no:
				rows[i].State = helperUnsupported
			case err == nil:
				rows[i].State, rows[i].Version = st.State, st.Version
			}
		}()
	}
	wg.Wait()
	out.Boxes = rows
	return out
}

func (a *Agent) helperTidyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/plugins/{id}/helpers", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !pluginIDPattern.MatchString(id) {
			writeError(w, http.StatusBadRequest, "not a plugin id")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		writeJSON(w, http.StatusOK, a.pluginHelpers(ctx, id))
	})
}
