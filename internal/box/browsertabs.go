package box

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cosscom/shipyard/internal/events"
)

// Tabs: several agents can share a worktree's browser, each in a tab of its
// own (berthd browser --tab NAME). Without tabs they shared one page, and
// each one's open took the page from under the others: their refs went
// stale, a shot could be of another agent's page, and the live view in the
// app jumped from one agent's page to the next.
//
// A tab is a page with its own CDP session, so its own refs, console,
// requests, size and watchers, and its own browser context, so its own
// cookies: one agent logging in as a user doesn't log the others in or
// out. Every tab is its own window, so every tab is visible to its page and
// renders: an agent's timers, animations and shots are real whether or not
// someone watches. Only watched tabs are cast to the app.
//
// The main tab, "", is the one Chromium starts with, in the default
// context: what agents had before tabs, and still get without --tab. Named
// tabs open on first use and close after BrowserIdle unused; the browser
// closes when all of its tabs are idle.

// chromium is a worktree's Chromium: the process, its CDP pipe, and its
// tabs.
type chromium struct {
	m        *Browsers
	path     string
	location string
	worktree string
	cmd      *exec.Cmd
	cdp      *cdpConn
	profile  string
	started  time.Time
	done     chan struct{}

	opening  sync.Mutex // one tab opens at a time
	tmu      sync.Mutex
	tabs     map[string]*browser // by name
	sessions map[string]*browser // by CDP session, for events
}

// tabName is what a tab may be called: a teammate's or a task's name.
var tabName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,47}$`)

// CheckTabName says what is wrong with a tab's name, if anything.
func CheckTabName(name string) error {
	if name != "" && !tabName.MatchString(name) {
		return fmt.Errorf("%q can't name a tab: use letters, digits, dots, dashes and underscores, at most 48", name)
	}
	return nil
}

// tabLimit is how many tabs one worktree's browser may have: by the box's
// memory, 2 under 4 GB, 4 to 8 GB, 6 to 16 GB, 8 above. Each tab is a
// renderer of its own, rendering in software.
func (m *Browsers) tabLimit() int {
	if m.MaxTabs > 0 {
		return m.MaxTabs
	}
	total := memTotal()
	switch {
	case total == 0:
		return 4
	case total < 4<<30:
		return 2
	case total < 8<<30:
		return 4
	case total < 16<<30:
		return 6
	}
	return 8
}

// get returns the worktree's tab, starting the browser and opening the tab
// if need be.
func (m *Browsers) get(ctx context.Context, loc Location, wt Worktree, tab string) (*browser, error) {
	c, err := m.chromiumFor(ctx, loc, wt)
	if err != nil {
		return nil, err
	}
	if br := c.lookup(tab); br != nil {
		br.touch()
		return br, nil
	}
	return c.openTab(ctx, tab, m.Viewport(wt.Path, tab))
}

// chromiumFor returns the worktree's browser, starting one if need be.
func (m *Browsers) chromiumFor(ctx context.Context, loc Location, wt Worktree) (*chromium, error) {
	m.mu.Lock()
	if c := m.open[wt.Path]; c != nil {
		select {
		case <-c.done:
			delete(m.open, wt.Path)
		default:
			m.mu.Unlock()
			return c, nil
		}
	}
	// At the cap, the least recently used goes.
	var lru *chromium
	if len(m.open) >= m.limit() {
		for _, c := range m.open {
			if lru == nil || c.lastUsed().Before(lru.lastUsed()) {
				lru = c
			}
		}
	}
	m.mu.Unlock()
	if lru != nil {
		m.close(lru, "another worktree needed a browser")
	}
	c, err := m.launch(ctx, loc, wt)
	m.noteStart(err)
	if err != nil {
		reason := "error"
		if errors.Is(err, ErrBrowserSandbox) {
			reason = "sandbox"
		}
		m.b.Events.Publish(events.Event{Type: "browser.failed", Box: m.b.Name, Origin: "browser", Data: map[string]any{"location": loc.Name, "name": wt.Name, "path": wt.Path, "reason": reason}})
		return nil, err
	}
	m.mu.Lock()
	m.open[wt.Path] = c
	m.mu.Unlock()
	m.b.Events.Publish(events.Event{Type: "browser.opened", Box: m.b.Name, Origin: "browser", Data: map[string]any{"location": loc.Name, "name": wt.Name, "path": wt.Path}})
	return c, nil
}

func (m *Browsers) launch(ctx context.Context, loc Location, wt Worktree) (*chromium, error) {
	if err := m.simulatedSandbox(); err != nil {
		return nil, err
	}
	bin := m.Chromium
	if bin == "" {
		var err error
		if bin, err = FindChromium(); err != nil {
			return nil, err
		}
	}
	px, err := m.b.BrowserProxies.For(m.b, wt.Path)
	if err != nil {
		return nil, err
	}
	c, err := m.start(ctx, bin, px.Addr(), wt.Path, m.Viewport(wt.Path, ""))
	if err != nil {
		return nil, err
	}
	c.location, c.worktree = loc.Name, wt.Name
	return c, nil
}

// lookupChromium returns the worktree's browser if it runs.
func (m *Browsers) lookupChromium(path string) *chromium {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c := m.open[filepath.Clean(path)]; c != nil {
		select {
		case <-c.done:
			return nil
		default:
			return c
		}
	}
	return nil
}

// Lookup returns the worktree's tab if it is open.
func (m *Browsers) Lookup(path, tab string) *browser {
	if c := m.lookupChromium(path); c != nil {
		return c.lookup(tab)
	}
	return nil
}

func (m *Browsers) close(c *chromium, why string) {
	m.mu.Lock()
	if m.open[c.path] == c {
		delete(m.open, c.path)
	}
	m.mu.Unlock()
	c.shutdown()
	m.b.Events.Publish(events.Event{Type: "browser.closed", Box: m.b.Name, Origin: "browser", Data: map[string]any{"location": c.location, "name": c.worktree, "path": c.path, "reason": why}})
}

// Close closes a worktree's browser, every tab of it.
func (m *Browsers) Close(path, why string) bool {
	if c := m.lookupChromium(path); c != nil {
		m.close(c, why)
		return true
	}
	return false
}

// CloseTab closes one named tab of a worktree's browser.
func (m *Browsers) CloseTab(path, tab, why string) bool {
	if br := m.Lookup(path, tab); br != nil && tab != "" {
		br.chromium.closeTab(br, why)
		return true
	}
	return false
}

// CloseAll closes every browser.
func (m *Browsers) CloseAll(why string) {
	m.mu.Lock()
	all := make([]*chromium, 0, len(m.open))
	for _, c := range m.open {
		all = append(all, c)
	}
	m.mu.Unlock()
	for _, c := range all {
		m.close(c, why)
	}
}

// CloseLRU closes the least recently used browser nobody watches, for the
// memory guard, which closes browsers before it pauses agents.
func (m *Browsers) CloseLRU(why string) (*BrowserStatus, bool) {
	m.mu.Lock()
	var lru *chromium
	for _, c := range m.open {
		if c.watchers() == 0 && (lru == nil || c.lastUsed().Before(lru.lastUsed())) {
			lru = c
		}
	}
	m.mu.Unlock()
	if lru == nil {
		return nil, false
	}
	st := lru.status()
	m.close(lru, why)
	return &st, true
}

// mains is every running browser's main process and worktree, without
// the cost of status (its memory, its proxy).
func (m *Browsers) mains() []BrowserStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []BrowserStatus
	for _, c := range m.open {
		if c.cmd.Process != nil {
			out = append(out, BrowserStatus{Location: c.location, Worktree: c.worktree, Path: c.path, PID: c.cmd.Process.Pid})
		}
	}
	return out
}

// List is every running browser's status.
func (m *Browsers) List() []BrowserStatus {
	m.mu.Lock()
	all := make([]*chromium, 0, len(m.open))
	for _, c := range m.open {
		all = append(all, c)
	}
	m.mu.Unlock()
	out := []BrowserStatus{}
	for _, c := range all {
		out = append(out, c.status())
	}
	return out
}

// --- a worktree's Chromium -------------------------------------------------

// attachMain takes the page Chromium started with as the main tab.
func (c *chromium) attachMain(ctx context.Context, v Viewport) error {
	var targets struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfos"`
	}
	if err := c.cdp.call(ctx, "", "Target.getTargets", nil, &targets); err != nil {
		return err
	}
	target := ""
	for _, t := range targets.TargetInfos {
		if t.Type == "page" {
			target = t.TargetID
			break
		}
	}
	if target == "" {
		var created struct {
			TargetID string `json:"targetId"`
		}
		if err := c.cdp.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &created); err != nil {
			return err
		}
		target = created.TargetID
	}
	_, err := c.attachTab(ctx, "", target, "", v)
	return err
}

// openTab opens a named tab: a browser context of its own, a window of its
// own in it, at the size v. At the box's cap, a tab nobody used for a
// minute makes room; else it is refused, rather than take a page another
// agent is on.
func (c *chromium) openTab(ctx context.Context, name string, v Viewport) (*browser, error) {
	c.opening.Lock()
	defer c.opening.Unlock()
	if br := c.lookup(name); br != nil {
		return br, nil
	}
	if name == "" {
		return nil, errors.New("the browser has no main tab")
	}
	if limit := c.m.tabLimit(); c.counted() >= limit {
		var lru *browser
		for _, br := range c.list() {
			if br.tab != "" && br.idleFor() > time.Minute && (lru == nil || br.lastUsedAt().Before(lru.lastUsedAt())) {
				lru = br
			}
		}
		if lru == nil {
			return nil, httpError{http.StatusConflict, fmt.Sprintf("the browser for %s/%s has %d tabs open, the most this box runs at once; close one (berthd browser close --tab NAME) or wait for one to go idle", c.location, c.worktree, limit)}
		}
		c.closeTab(lru, "another tab needed room")
	}
	var bc struct {
		BrowserContextID string `json:"browserContextId"`
	}
	if err := c.cdp.call(ctx, "", "Target.createBrowserContext", map[string]any{}, &bc); err != nil {
		return nil, fmt.Errorf("opening tab %s: %w", name, err)
	}
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := c.cdp.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank", "browserContextId": bc.BrowserContextID, "newWindow": true}, &created); err != nil {
		c.cdp.call(context.Background(), "", "Target.disposeBrowserContext", map[string]any{"browserContextId": bc.BrowserContextID}, nil)
		return nil, fmt.Errorf("opening tab %s: %w", name, err)
	}
	br, err := c.attachTab(ctx, name, created.TargetID, bc.BrowserContextID, v)
	if err != nil {
		c.cdp.call(context.Background(), "", "Target.closeTarget", map[string]any{"targetId": created.TargetID}, nil)
		c.cdp.call(context.Background(), "", "Target.disposeBrowserContext", map[string]any{"browserContextId": bc.BrowserContextID}, nil)
		return nil, err
	}
	c.m.b.Events.Publish(events.Event{Type: "browser.tab.opened", Box: c.m.b.Name, Origin: "browser", Data: map[string]any{"location": c.location, "name": c.worktree, "path": c.path, "tab": name}})
	return br, nil
}

// attachTab attaches to a page and makes it the tab name.
func (c *chromium) attachTab(ctx context.Context, name, target, bctx string, v Viewport) (*browser, error) {
	var att struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.cdp.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true}, &att); err != nil {
		return nil, err
	}
	now := time.Now()
	br := &browser{chromium: c, tab: name, target: target, session: att.SessionID, bctx: bctx, opened: now, lastUsed: now, viewport: v,
		refs: map[int64]string{}, reqs: map[string]string{}, inflight: map[string]bool{}, watchers: map[chan frame]struct{}{}}
	// Known by its session before its first event.
	c.tmu.Lock()
	c.sessions[br.session] = br
	c.tmu.Unlock()
	fail := func(err error) (*browser, error) {
		c.tmu.Lock()
		delete(c.sessions, br.session)
		c.tmu.Unlock()
		return nil, err
	}
	for _, m := range []string{"Page.enable", "Runtime.enable", "Network.enable", "Log.enable", "DOM.enable", "Accessibility.enable"} {
		if err := c.cdp.call(ctx, br.session, m, nil, nil); err != nil && !strings.HasPrefix(m, "Log") {
			return fail(err)
		}
	}
	if err := br.applyViewport(ctx, v); err != nil {
		return fail(err)
	}
	// Downloads land in the worktree, where the agent can read them.
	if c.path != "" {
		dl := filepath.Join(c.path, ".berth", "browser")
		os.MkdirAll(dl, 0o755)
		p := map[string]any{"behavior": "allow", "downloadPath": dl}
		if bctx != "" {
			p["browserContextId"] = bctx
		}
		c.cdp.call(ctx, "", "Browser.setDownloadBehavior", p, nil)
	}
	c.tmu.Lock()
	c.tabs[name] = br
	c.tmu.Unlock()
	return br, nil
}

// closeTab closes a named tab: its page, its context, its watchers.
func (c *chromium) closeTab(br *browser, why string) {
	if br.tab == "" {
		return
	}
	c.tmu.Lock()
	if c.tabs[br.tab] != br {
		c.tmu.Unlock()
		return
	}
	delete(c.tabs, br.tab)
	delete(c.sessions, br.session)
	c.tmu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	c.cdp.call(ctx, "", "Target.closeTarget", map[string]any{"targetId": br.target}, nil)
	c.cdp.call(ctx, "", "Target.disposeBrowserContext", map[string]any{"browserContextId": br.bctx}, nil)
	cancel()
	br.closeWatchers()
	c.m.b.Events.Publish(events.Event{Type: "browser.tab.closed", Box: c.m.b.Name, Origin: "browser", Data: map[string]any{"location": c.location, "name": c.worktree, "path": c.path, "tab": br.tab, "reason": why}})
}

func (c *chromium) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	c.cdp.call(ctx, "", "Browser.close", nil, nil)
	cancel()
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		if c.cmd.Process != nil {
			c.cmd.Process.Kill()
		}
		<-c.done
	}
	c.cdp.close()
	os.RemoveAll(c.profile)
	for _, br := range c.list() {
		br.closeWatchers()
	}
}

// onEvent hands an event to the tab whose session it came on; the
// browser's own events go to the main tab.
func (c *chromium) onEvent(method, session string, params json.RawMessage) {
	c.tmu.Lock()
	br := c.sessions[session]
	if session == "" {
		br = c.tabs[""]
	}
	c.tmu.Unlock()
	if br != nil {
		br.onEvent(method, params)
	}
}

func (c *chromium) lookup(name string) *browser {
	c.tmu.Lock()
	defer c.tmu.Unlock()
	return c.tabs[name]
}

// list is the tabs, the main one first, then by when they opened.
func (c *chromium) list() []*browser {
	c.tmu.Lock()
	out := make([]*browser, 0, len(c.tabs))
	for _, br := range c.tabs {
		out = append(out, br)
	}
	c.tmu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if (out[i].tab == "") != (out[j].tab == "") {
			return out[i].tab == ""
		}
		if !out[i].opened.Equal(out[j].opened) {
			return out[i].opened.Before(out[j].opened)
		}
		return out[i].tab < out[j].tab
	})
	return out
}

// counted is how many tabs count toward the cap: the main tab only once
// an agent has used it.
func (c *chromium) counted() int {
	n := 0
	for _, br := range c.list() {
		if br.tab != "" || br.tabStatus().used() {
			n++
		}
	}
	return n
}

// recent is the tab used last.
func (c *chromium) recent() *browser {
	var out *browser
	for _, br := range c.list() {
		if out == nil || br.lastUsedAt().After(out.lastUsedAt()) {
			out = br
		}
	}
	return out
}

func (c *chromium) lastUsed() time.Time {
	var t time.Time
	for _, br := range c.list() {
		if u := br.lastUsedAt(); u.After(t) {
			t = u
		}
	}
	return t
}

func (c *chromium) watchers() int {
	n := 0
	for _, br := range c.list() {
		br.mu.Lock()
		n += len(br.watchers)
		br.mu.Unlock()
	}
	return n
}

// idleFor is how long since any tab was used, 0 while one is watched.
func (c *chromium) idleFor() time.Duration {
	if c.watchers() > 0 {
		return 0
	}
	return time.Since(c.lastUsed())
}

// --- a tab ------------------------------------------------------------------

func (br *browser) touch() {
	br.mu.Lock()
	br.lastUsed = time.Now()
	br.mu.Unlock()
}

func (br *browser) lastUsedAt() time.Time {
	br.mu.Lock()
	defer br.mu.Unlock()
	return br.lastUsed
}

// idleFor is how long since the tab was used, 0 while it is watched or a
// command runs on it.
func (br *browser) idleFor() time.Duration {
	br.mu.Lock()
	defer br.mu.Unlock()
	if len(br.watchers) > 0 || br.busy.Load() > 0 {
		return 0
	}
	return time.Since(br.lastUsed)
}

// begin starts a command on the tab, one at a time, and returns its end.
func (br *browser) begin() func() {
	br.run.Lock()
	br.busy.Add(1)
	br.touch()
	return func() {
		br.touch()
		br.busy.Add(-1)
		br.run.Unlock()
	}
}

func (br *browser) closeWatchers() {
	br.mu.Lock()
	for ch := range br.watchers {
		close(ch)
		delete(br.watchers, ch)
	}
	br.mu.Unlock()
}

// BrowserStatus is a browser as status and lists show it: its main tab's
// page (or, without one, the tab used last), and all of its tabs.
type BrowserStatus struct {
	Location string    `json:"location"`
	Worktree string    `json:"worktree"`
	Path     string    `json:"path"`
	URL      string    `json:"url,omitempty"`
	PID      int       `json:"pid"`
	RSS      uint64    `json:"rss_bytes,omitempty"`
	Started  time.Time `json:"started"`
	LastUsed time.Time `json:"last_used"`
	Watchers int       `json:"watchers"`
	Refused  int       `json:"refused,omitempty"`
	// Viewport is the page's size, and Size it as text (1920×1080).
	Viewport Viewport `json:"viewport"`
	Size     string   `json:"size"`
	// Tabs are its tabs: the main one ("") if an agent used it or there is
	// no other, then the named ones as they opened.
	Tabs []TabStatus `json:"tabs"`
}

// TabStatus is one tab of a browser.
type TabStatus struct {
	Name     string    `json:"name"`
	URL      string    `json:"url,omitempty"`
	Title    string    `json:"title,omitempty"`
	LastUsed time.Time `json:"last_used"`
	// IdleMS is how long since it was used by the box's clock, which the
	// laptop's may not agree with.
	IdleMS   int64    `json:"idle_ms"`
	Watchers int      `json:"watchers"`
	Viewport Viewport `json:"viewport"`
	Size     string   `json:"size"`
	// Busy is whether an agent's command runs on it now.
	Busy bool `json:"busy,omitempty"`
}

func (br *browser) tabStatus() TabStatus {
	br.mu.Lock()
	st := TabStatus{Name: br.tab, URL: br.url, Title: br.title, LastUsed: br.lastUsed, IdleMS: time.Since(br.lastUsed).Milliseconds(), Watchers: len(br.watchers), Viewport: br.viewport, Busy: br.busy.Load() > 0}
	br.mu.Unlock()
	if st.Viewport.Width == 0 {
		st.Viewport = DefaultViewport
	}
	st.Size = st.Viewport.String()
	return st
}

// used is whether an agent has been on the tab: a main tab still on
// about:blank, with named tabs beside it, is left out of lists, unless
// someone watches it (it would look closed to them).
func (t TabStatus) used() bool { return t.URL != "" && !strings.HasPrefix(t.URL, "about:") }

func (c *chromium) status() BrowserStatus {
	st := BrowserStatus{Location: c.location, Worktree: c.worktree, Path: c.path, Started: c.started, Tabs: []TabStatus{}}
	all := c.list()
	for _, br := range all {
		ts := br.tabStatus()
		st.Watchers += ts.Watchers
		if ts.LastUsed.After(st.LastUsed) {
			st.LastUsed = ts.LastUsed
		}
		if ts.Name == "" && !ts.used() && ts.Watchers == 0 && len(all) > 1 {
			continue
		}
		st.Tabs = append(st.Tabs, ts)
	}
	page := c.lookup("")
	if page == nil || (!page.tabStatus().used() && len(all) > 1) {
		page = c.recent()
	}
	if page != nil {
		ts := page.tabStatus()
		st.URL, st.Viewport, st.Size = ts.URL, ts.Viewport, ts.Size
	} else {
		st.Viewport, st.Size = DefaultViewport, DefaultViewport.String()
	}
	if c.cmd.Process != nil {
		st.PID = c.cmd.Process.Pid
		st.RSS = treeRSS(st.PID)
	}
	if px, err := c.m.b.BrowserProxies.For(c.m.b, c.path); err == nil {
		st.Refused, _ = px.Refused()
	}
	return st
}
