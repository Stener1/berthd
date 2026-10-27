package box

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cosscom/shipyard/internal/events"
)

// The agent browser: a headless Chromium on the box, one per active
// worktree, started on first use and closed after 5 idle minutes. It opens
// exactly the URL the human sees for the worktree, through the worktree's
// browser proxy, which is its only way out. berthd drives it over a pipe
// and gives agents short text: a compact accessibility snapshot with refs,
// a delta after each action, screenshots as files, console errors only.

// BrowserIdle is how long a browser lives unused.
const BrowserIdle = 5 * time.Minute

// Output caps: what an agent reads costs it tokens every step.
const (
	snapCap      = 8 << 10  // a snapshot, about 2k tokens; more goes to a file
	snapFullCap  = 16 << 10 // --full, about 4k tokens
	firstLookCap = 6 << 10  // the snapshot open prints
	deltaLines   = 40       // a delta after an action
	consoleAfter = 5        // console errors after an action
	consoleLines = 20       // berth browser console
	netAfter     = 3        // failed requests after an action
	netLines     = 10       // berth browser network
	evalCap      = 2 << 10  // eval output
	lineCap      = 200      // one console or network line
	keepShots    = 20       // shots kept per worktree
)

// Browsers runs the box's agent browsers.
type Browsers struct {
	// Dir keeps shots (<Dir>/<location>/<worktree>/) and profiles.
	Dir string
	// Max caps how many run at once (0: by the box's memory).
	Max int
	// Chromium is the binary; empty finds one.
	Chromium string
	// Idle closes a browser unused this long (default BrowserIdle).
	Idle time.Duration

	// MaxTabs caps the tabs one worktree's browser has (0: by the box's
	// memory).
	MaxTabs int

	mu   sync.Mutex
	vpMu sync.Mutex // viewports.json
	b    *Box
	open map[string]*chromium // worktree path →
	stop chan struct{}

	failure   *startFailure // the last start that failed, until one starts
	startedOK bool          // a browser has started since berthd did
	likely    bool          // likelyBlocked, as of likelyAt
	likelyAt  time.Time
}

type consoleEntry struct {
	seq   int
	level string
	text  string
	count int
}

type netFailure struct {
	seq  int
	text string
}

// frame is a screencast frame: a JPEG of the page, w×h CSS pixels at the
// page's scale (the image itself is up to w·scale × h·scale).
type frame struct {
	Data   string  `json:"data"`
	Width  int     `json:"w"`
	Height int     `json:"h"`
	Scale  float64 `json:"scale,omitempty"`
	URL    string  `json:"url,omitempty"`
}

// browser is one tab of a worktree's Chromium: the page an agent drives,
// with its own refs, console, requests, size and watchers. The main tab
// (tab "") is the one agents get without --tab; a named tab has a browser
// context of its own, so its cookies and storage are its own too.
type browser struct {
	*chromium
	tab     string
	target  string
	session string
	bctx    string // its browser context; "" for the main tab's, the default
	opened  time.Time

	run  sync.Mutex // one command at a time
	busy atomic.Int32

	mu          sync.Mutex
	url, title  string
	lastUsed    time.Time
	refs        map[int64]string
	nextRef     int
	lastSnap    []string
	lastSnapURL string
	console     []consoleEntry
	consoleSeq  int
	consoleSeen int
	reqs        map[string]string
	inflight    map[string]bool
	lastNet     time.Time
	failures    []netFailure
	failSeq     int
	failSeen    int
	loaded      chan struct{}
	watchers    map[chan frame]struct{}
	casting     bool
	refusedSeen int
	viewport    Viewport
	recapturing bool // a sharp frame is being taken
	dirty       bool // and the page changed again since
}

// NewBrowsers makes the box's browser manager.
func (b *Box) NewBrowsers(dir string, max int) *Browsers {
	m := &Browsers{Dir: dir, Max: max, b: b, open: map[string]*chromium{}, stop: make(chan struct{})}
	b.Browsers = m
	return m
}

// Run closes idle browsers until ctx ends, then every browser.
func (m *Browsers) Run(ctx context.Context) {
	idle := m.Idle
	if idle <= 0 {
		idle = BrowserIdle
	}
	t := time.NewTicker(min(30*time.Second, idle/2))
	defer t.Stop()
	evs, unsub := m.b.Events.SubscribeNamed("browsers")
	defer unsub()
	for {
		select {
		case <-ctx.Done():
			m.CloseAll("berthd stopped")
			return
		case e, ok := <-evs:
			// A worktree that goes takes its browser and proxy with it.
			if ok && e.Type == "worktree.removed" {
				if p, _ := e.Data["path"].(string); p != "" {
					m.Close(p, "its worktree was removed")
					m.forgetViewport(p)
					if m.b.BrowserProxies != nil {
						m.b.BrowserProxies.Close(p)
					}
				}
			}
		case <-t.C:
			// A named tab goes when it is idle; the browser when all of
			// its tabs are.
			m.mu.Lock()
			all := make([]*chromium, 0, len(m.open))
			for _, c := range m.open {
				all = append(all, c)
			}
			m.mu.Unlock()
			for _, c := range all {
				for _, br := range c.list() {
					if br.tab != "" && br.idleFor() > idle {
						c.closeTab(br, "idle")
					}
				}
				if c.idleFor() > idle {
					m.close(c, "idle")
				}
			}
		}
	}
}

// limit is how many browsers may run: by the box's memory, 1 under 2 GB,
// 2 to 4 GB, 3 to 8 GB, 4 above.
func (m *Browsers) limit() int {
	if m.Max > 0 {
		return m.Max
	}
	total := memTotal()
	switch {
	case total == 0:
		return 2
	case total < 2<<30:
		return 1
	case total < 4<<30:
		return 2
	case total < 8<<30:
		return 3
	}
	return 4
}

// memTotal is the box's memory in bytes.
func memTotal() uint64 {
	if t := collectStats("/proc").Memory.Total; t > 0 {
		return t
	}
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
			n, _ := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
			return n
		}
	}
	return 0
}

// FindChromium finds a Chromium: $BERTH_CHROMIUM, then on PATH, then
// Playwright's downloads (the small headless shell first), then Chrome.
func FindChromium() (string, error) {
	if p := os.Getenv("BERTH_CHROMIUM"); p != "" {
		return p, nil
	}
	for _, name := range []string{"chrome-headless-shell", "chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	home, _ := os.UserHomeDir()
	var globs []string
	for _, cache := range []string{filepath.Join(home, ".cache", "ms-playwright"), filepath.Join(home, "Library", "Caches", "ms-playwright")} {
		globs = append(globs,
			filepath.Join(cache, "chromium_headless_shell-*", "chrome-headless-shell-*", "chrome-headless-shell"),
			filepath.Join(cache, "chromium_headless_shell-*", "chrome-*", "headless_shell"),
			filepath.Join(cache, "chromium-*", "chrome-linux*", "chrome"),
			filepath.Join(cache, "chromium-*", "chrome-mac*", "Chromium.app", "Contents", "MacOS", "Chromium"),
		)
	}
	for _, g := range globs {
		m, _ := filepath.Glob(g)
		sort.Sort(sort.Reverse(sort.StringSlice(m)))
		if len(m) > 0 {
			return m[0], nil
		}
	}
	for _, p := range []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Applications/Chromium.app/Contents/MacOS/Chromium"} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errNoChromium
}

var errNoChromium = errors.New("no Chromium on this box; install one with `berthd browser install` (Playwright's headless shell) or set BERTH_CHROMIUM")

func (m *Browsers) shotDir(location, worktree string) string {
	return filepath.Join(m.Dir, "shots", location, worktree)
}

// Check starts Chromium once, on a blank page with nowhere to go, and
// closes it: whether a browser starts now, after a fix.
func (m *Browsers) Check(ctx context.Context) error {
	err := m.simulatedSandbox()
	if err == nil {
		bin := m.Chromium
		if bin == "" {
			bin, err = FindChromium()
		}
		if err == nil {
			var c *chromium
			// A proxy that refuses everything: the page is about:blank.
			if c, err = m.start(ctx, bin, "127.0.0.1:9", "", DefaultViewport); err == nil {
				c.shutdown()
			}
		}
	}
	m.noteStart(err)
	return err
}

// simulatedSandbox is Ubuntu's refusal, under BERTH_TEST_USERNS_SYSCTL.
func (m *Browsers) simulatedSandbox() error {
	if testUsernsPath() == "" || readUserns() != "1" {
		return nil
	}
	if off, _ := m.noSandbox(); off {
		return nil
	}
	return sandboxError("1")
}

// start runs Chromium with its only way out through proxy, for the
// worktree at path (where downloads go), its main tab at the size v.
func (m *Browsers) start(ctx context.Context, bin, proxy, path string, v Viewport) (*chromium, error) {
	profile, err := os.MkdirTemp(filepath.Join(m.Dir, "profiles"), "p-")
	if err != nil {
		os.MkdirAll(filepath.Join(m.Dir, "profiles"), 0o700)
		if profile, err = os.MkdirTemp(filepath.Join(m.Dir, "profiles"), "p-"); err != nil {
			return nil, err
		}
	}
	off, _ := m.noSandbox()
	args := append(append(chromiumArgs(profile, proxy, off, v), agentBrowserArgs()...), "about:blank")
	toChrome, ours, err := os.Pipe() // chrome reads fd 3
	if err != nil {
		return nil, err
	}
	theirs, fromChrome, err := os.Pipe() // chrome writes fd 4
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, args...)
	cmd.ExtraFiles = []*os.File{toChrome, fromChrome}
	cmd.Dir = profile
	// BERTH_BROWSER marks it as berth's own, in the box's processes list.
	cmd.Env = append(os.Environ(), "HOME="+profile, "BERTH_BROWSER=agent")
	stderr := &stderrTail{max: 4 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		os.RemoveAll(profile)
		return nil, err
	}
	toChrome.Close()
	fromChrome.Close()
	c := &chromium{m: m, cmd: cmd, path: path, profile: profile, started: time.Now(), done: make(chan struct{}), tabs: map[string]*browser{}, sessions: map[string]*browser{}}
	c.cdp = newCDP(ours, theirs, c.onEvent)
	go func() {
		cmd.Wait()
		ours.Close()
		theirs.Close()
		close(c.done)
	}()
	cctx, cancel := context.WithTimeout(ctx, browserStartTimeout())
	defer cancel()
	if err := c.attachMain(cctx, v); err != nil {
		c.shutdown()
		if out := strings.ToLower(stderr.String()); strings.Contains(out, "sandbox") || strings.Contains(out, "namespace") {
			return nil, sandboxError(readUserns())
		}
		if out := strings.TrimSpace(stderr.String()); out != "" {
			return nil, fmt.Errorf("starting Chromium: %w; it said: %s", err, strings.ReplaceAll(lastLines(out, 6), "\n", " | "))
		}
		return nil, fmt.Errorf("starting Chromium: %w", err)
	}
	return c, nil
}

// chromiumArgs is how berthd runs Chromium, the agents' browser and the
// shots browser alike: headless, its only way out through proxy, without
// its background services, with a window of size v.
func chromiumArgs(profile, proxy string, noSandbox bool, v Viewport) []string {
	args := []string{
		"--headless=new", "--remote-debugging-pipe", "--user-data-dir=" + profile,
		"--proxy-server=" + proxy, "--proxy-bypass-list=<-loopback>",
		"--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-sync",
		"--disable-extensions", "--disable-component-update", "--disable-default-apps", "--mute-audio",
		"--disable-features=DnsOverHttps,Translate,MediaRouter,OptimizationHints,AutofillServerCommunication,CertificateTransparencyComponentUpdater,InterestFeedContentSuggestions,PrivacySandboxSettings4",
		"--dns-over-https-mode=off", "--disable-client-side-phishing-detection", "--disable-domain-reliability", "--no-pings", "--disable-breakpad",
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp", fmt.Sprintf("--window-size=%d,%d", v.Width, v.Height), "--hide-scrollbars",
	}
	if runtime.GOOS == "linux" {
		args = append(args, "--disable-dev-shm-usage")
	}
	// Ubuntu 24.04 and others stop Chromium making the user namespaces its
	// sandbox needs. The owner can choose to run without it (the box's
	// setting, or BERTH_BROWSER_NO_SANDBOX): the browser is still confined
	// to its worktree by the proxy.
	if noSandbox {
		args = append(args, "--no-sandbox")
	}
	return args
}

// agentBrowserArgs are what only the agents' browser adds (not the
// visual-diff one, which opens a tab per shot): at most two renderers
// whatever a page opens, and a ceiling on each page's JavaScript heap,
// which makes V8 collect sooner. Measured on Linux with a React app's
// pages, a shot and the live view: about 35 MB less (PSS 418 → 380 MB);
// a 1 GB ceiling saved nothing measurable. BERTH_BROWSER_JS_HEAP_MB
// changes it (0: none) for a page that needs more.
func agentBrowserArgs() []string {
	args := []string{"--renderer-process-limit=2"}
	heap := 512
	if v, err := strconv.Atoi(os.Getenv("BERTH_BROWSER_JS_HEAP_MB")); err == nil && v >= 0 {
		heap = v
	}
	if heap > 0 {
		args = append(args, "--js-flags=--max-old-space-size="+strconv.Itoa(heap))
	}
	return args
}

// browserStartTimeout is how long Chromium gets to start: 20s, or
// BERTH_BROWSER_START_TIMEOUT (a duration) on a box where its first start
// is slow, such as a snap's.
func browserStartTimeout() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("BERTH_BROWSER_START_TIMEOUT")); err == nil && d > 0 {
		return d
	}
	return 20 * time.Second
}

// stderrTail keeps the last max bytes written to it: the end of Chromium's
// error output, for saying why it would not start.
type stderrTail struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *stderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if over := len(t.b) - t.max; over > 0 {
		t.b = append(t.b[:0], t.b[over:]...)
	}
	return len(p), nil
}

func (t *stderrTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

// onEvent keeps what the page says: navigations, console errors, failed
// requests, in-flight requests, screencast frames. Buffers are bounded.
func (br *browser) onEvent(method string, params json.RawMessage) {
	switch method {
	case "Page.frameNavigated":
		var p struct {
			Frame struct {
				ID       string `json:"id"`
				ParentID string `json:"parentId"`
				URL      string `json:"url"`
			} `json:"frame"`
		}
		json.Unmarshal(params, &p)
		if p.Frame.ParentID != "" {
			return
		}
		br.mu.Lock()
		br.url = p.Frame.URL
		// A new document: new elements, new refs.
		br.refs, br.nextRef, br.lastSnap, br.title = map[int64]string{}, 0, nil, ""
		br.mu.Unlock()
		if !strings.HasPrefix(p.Frame.URL, "about:") {
			br.m.b.Events.Publish(events.Event{Type: "browser.navigated", Box: br.m.b.Name, Origin: "browser", Data: map[string]any{"location": br.location, "name": br.worktree, "path": br.path, "tab": br.tab, "url": p.Frame.URL}})
		}
	case "Page.navigatedWithinDocument":
		var p struct {
			URL string `json:"url"`
		}
		json.Unmarshal(params, &p)
		br.mu.Lock()
		br.url = p.URL
		br.mu.Unlock()
	case "Page.loadEventFired":
		br.mu.Lock()
		if br.loaded != nil {
			close(br.loaded)
			br.loaded = nil
		}
		br.mu.Unlock()
	case "Runtime.consoleAPICalled":
		var p struct {
			Type string `json:"type"`
			Args []struct {
				Value       any    `json:"value"`
				Description string `json:"description"`
			} `json:"args"`
		}
		json.Unmarshal(params, &p)
		var parts []string
		for _, a := range p.Args {
			if a.Value != nil {
				parts = append(parts, fmt.Sprint(a.Value))
			} else {
				parts = append(parts, a.Description)
			}
		}
		br.addConsole(p.Type, strings.Join(parts, " "))
	case "Runtime.exceptionThrown":
		var p struct {
			ExceptionDetails struct {
				Text      string `json:"text"`
				Exception struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		json.Unmarshal(params, &p)
		d := p.ExceptionDetails.Exception.Description
		if d == "" {
			d = p.ExceptionDetails.Text
		}
		br.addConsole("error", firstLineOf(d))
	case "Log.entryAdded":
		var p struct {
			Entry struct {
				Level string `json:"level"`
				Text  string `json:"text"`
				URL   string `json:"url"`
			} `json:"entry"`
		}
		json.Unmarshal(params, &p)
		br.addConsole(p.Entry.Level, strings.TrimSpace(p.Entry.Text+" "+p.Entry.URL))
	case "Network.requestWillBeSent":
		var p struct {
			RequestID string `json:"requestId"`
			Type      string `json:"type"`
			Request   struct {
				URL    string `json:"url"`
				Method string `json:"method"`
			} `json:"request"`
		}
		json.Unmarshal(params, &p)
		br.mu.Lock()
		if len(br.reqs) > 500 {
			br.reqs = map[string]string{}
		}
		br.reqs[p.RequestID] = p.Request.Method + " " + p.Request.URL
		if p.Type != "EventSource" && p.Type != "WebSocket" {
			br.inflight[p.RequestID] = true
		}
		br.lastNet = time.Now()
		br.mu.Unlock()
	case "Network.loadingFinished":
		var p struct {
			RequestID string `json:"requestId"`
		}
		json.Unmarshal(params, &p)
		br.mu.Lock()
		delete(br.inflight, p.RequestID)
		br.lastNet = time.Now()
		br.mu.Unlock()
	case "Network.responseReceived":
		var p struct {
			RequestID string `json:"requestId"`
			Response  struct {
				Status int    `json:"status"`
				URL    string `json:"url"`
			} `json:"response"`
		}
		json.Unmarshal(params, &p)
		if p.Response.Status >= 400 {
			br.mu.Lock()
			what := br.reqs[p.RequestID]
			if what == "" {
				what = p.Response.URL
			}
			br.addFailureLocked(fmt.Sprintf("%d %s", p.Response.Status, what))
			br.mu.Unlock()
		}
	case "Network.loadingFailed":
		var p struct {
			RequestID string `json:"requestId"`
			ErrorText string `json:"errorText"`
			Canceled  bool   `json:"canceled"`
		}
		json.Unmarshal(params, &p)
		br.mu.Lock()
		delete(br.inflight, p.RequestID)
		if !p.Canceled {
			br.addFailureLocked(p.ErrorText + " " + br.reqs[p.RequestID])
		}
		br.mu.Unlock()
	case "Page.screencastFrame":
		var p struct {
			Data      string `json:"data"`
			SessionID int    `json:"sessionId"`
			Metadata  struct {
				DeviceWidth  float64 `json:"deviceWidth"`
				DeviceHeight float64 `json:"deviceHeight"`
			} `json:"metadata"`
		}
		json.Unmarshal(params, &p)
		go br.cdp.call(context.Background(), br.session, "Page.screencastFrameAck", map[string]any{"sessionId": p.SessionID}, nil)
		br.mu.Lock()
		if br.viewport.scale() != 1 {
			// Chromium casts at the window's own scale, 1, whatever the
			// page's: a frame says the page changed, and a capture at
			// the page's scale is sent instead, so the view stays sharp.
			br.recaptureLocked()
			br.mu.Unlock()
			return
		}
		f := frame{Data: p.Data, Width: int(p.Metadata.DeviceWidth), Height: int(p.Metadata.DeviceHeight), Scale: br.viewport.scale(), URL: br.url}
		for ch := range br.watchers {
			// A slow watcher misses frames rather than holding the rest.
			select {
			case ch <- f:
			default:
			}
		}
		br.mu.Unlock()
	}
}

func (br *browser) addConsole(level, text string) {
	if level != "error" && level != "warning" && level != "warn" && level != "assert" {
		level = "log"
	}
	if level == "warn" {
		level = "warning"
	}
	text = clip(strings.Join(strings.Fields(text), " "), lineCap)
	br.mu.Lock()
	defer br.mu.Unlock()
	// The same line again counts, rather than repeats.
	for i := len(br.console) - 1; i >= 0 && i >= len(br.console)-20; i-- {
		if br.console[i].text == text && br.console[i].level == level {
			br.console[i].count++
			if br.console[i].seq <= br.consoleSeen {
				br.consoleSeq++
				br.console[i].seq = br.consoleSeq
			}
			return
		}
	}
	br.consoleSeq++
	br.console = append(br.console, consoleEntry{seq: br.consoleSeq, level: level, text: text, count: 1})
	if len(br.console) > 200 {
		br.console = br.console[len(br.console)-200:]
	}
}

func (br *browser) addFailureLocked(text string) {
	br.failSeq++
	br.failures = append(br.failures, netFailure{seq: br.failSeq, text: clip(strings.TrimSpace(text), lineCap)})
	if len(br.failures) > 100 {
		br.failures = br.failures[len(br.failures)-100:]
	}
}

// newConsole takes the errors and warnings since the last look, at most n
// (all levels with all).
func (br *browser) newConsole(n int, all bool) []string {
	br.mu.Lock()
	defer br.mu.Unlock()
	var out []string
	more := 0
	for _, e := range br.console {
		if e.seq <= br.consoleSeen || (!all && e.level == "log") {
			continue
		}
		if len(out) >= n {
			more++
			continue
		}
		line := e.level + ": " + e.text
		if e.count > 1 {
			line += fmt.Sprintf(" (×%d)", e.count)
		}
		out = append(out, line)
	}
	if more > 0 {
		out = append(out, fmt.Sprintf("… %d more", more))
	}
	br.consoleSeen = br.consoleSeq
	return out
}

func (br *browser) newFailures(n int) []string {
	br.mu.Lock()
	defer br.mu.Unlock()
	var out []string
	more := 0
	for _, f := range br.failures {
		if f.seq <= br.failSeen {
			continue
		}
		if len(out) >= n {
			more++
			continue
		}
		out = append(out, f.text)
	}
	if more > 0 {
		out = append(out, fmt.Sprintf("… %d more", more))
	}
	br.failSeen = br.failSeq
	return out
}

// settle waits for the page to go quiet: no request in flight for 400ms,
// at most max.
func (br *browser) settle(ctx context.Context, max time.Duration) {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		br.mu.Lock()
		quiet := len(br.inflight) == 0 && time.Since(br.lastNet) > 400*time.Millisecond
		br.mu.Unlock()
		if quiet {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	// Long-lived requests (streams) never finish: forget them.
	br.mu.Lock()
	br.inflight = map[string]bool{}
	br.mu.Unlock()
}

// treeRSS is the resident memory of pid and its descendants (Chromium's
// renderers and GPU process), in bytes.
func treeRSS(pid int) uint64 {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,rss=").Output()
	if err != nil {
		return 0
	}
	kids := map[int][]int{}
	rss := map[int]uint64{}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) != 3 {
			continue
		}
		p, _ := strconv.Atoi(f[0])
		pp, _ := strconv.Atoi(f[1])
		r, _ := strconv.ParseUint(f[2], 10, 64)
		kids[pp] = append(kids[pp], p)
		rss[p] = r * 1024
	}
	var total uint64
	var walk func(int)
	walk = func(p int) {
		total += rss[p]
		for _, k := range kids[p] {
			walk(k)
		}
	}
	walk(pid)
	return total
}

// --- commands -----------------------------------------------------------

// BrowserResult is what a command returns: the text an agent reads, and
// for files (shots, overflowing snapshots) where they are.
type BrowserResult struct {
	Text string `json:"text"`
	URL  string `json:"url,omitempty"`
	File string `json:"file,omitempty"`
}

func (br *browser) evaluate(ctx context.Context, expr string) (json.RawMessage, error) {
	var r struct {
		Result struct {
			Value       json.RawMessage `json:"value"`
			Description string          `json:"description"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := br.cdp.call(ctx, br.session, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true, "timeout": 10000}, &r); err != nil {
		return nil, err
	}
	if r.ExceptionDetails != nil {
		d := r.ExceptionDetails.Exception.Description
		if d == "" {
			d = r.ExceptionDetails.Text
		}
		return nil, errors.New(firstLineOf(d))
	}
	if len(r.Result.Value) == 0 && r.Result.Description != "" {
		b, _ := json.Marshal(r.Result.Description)
		return b, nil
	}
	return r.Result.Value, nil
}

func (br *browser) pageTitle(ctx context.Context) string {
	raw, err := br.evaluate(ctx, "document.title")
	if err != nil {
		return ""
	}
	var s string
	json.Unmarshal(raw, &s)
	return s
}

// Open navigates and prints a first look: title, URL and a compact
// interactive snapshot, with any errors.
func (br *browser) Open(ctx context.Context, url string) (BrowserResult, error) {
	defer br.begin()()
	loaded := make(chan struct{})
	br.mu.Lock()
	br.loaded = loaded
	br.mu.Unlock()
	var nav struct {
		ErrorText string `json:"errorText"`
	}
	if err := br.cdp.call(ctx, br.session, "Page.navigate", map[string]any{"url": url}, &nav); err != nil {
		return BrowserResult{}, err
	}
	if nav.ErrorText != "" {
		return BrowserResult{Text: "could not open " + url + ": " + nav.ErrorText + br.refusedNote()}, nil
	}
	select {
	case <-loaded:
	case <-time.After(20 * time.Second):
	case <-ctx.Done():
		return BrowserResult{}, ctx.Err()
	}
	br.settle(ctx, 3*time.Second)
	lines, full, err := br.snapBoth(ctx, snapOptions{maxLines: 2000})
	if err != nil {
		return BrowserResult{}, err
	}
	var b strings.Builder
	cur := br.currentURL()
	br.mu.Lock()
	br.lastSnap, br.lastSnapURL = full, cur
	br.mu.Unlock()
	title := clip(br.pageTitle(ctx), 120)
	br.mu.Lock()
	br.title = title
	br.mu.Unlock()
	fmt.Fprintf(&b, "url: %s\ntitle: %s\nsize: %s\n", cur, title, br.currentViewport())
	text, file := br.capLines(lines, firstLookCap)
	b.WriteString(strings.TrimRight(text, "\n"))
	br.appendLogs(&b, consoleAfter, netAfter)
	return BrowserResult{Text: b.String(), URL: cur, File: file}, nil
}

func (br *browser) currentURL() string {
	br.mu.Lock()
	defer br.mu.Unlock()
	return br.url
}

// refusedNote names what the proxy refused since the last note.
func (br *browser) refusedNote() string {
	px, err := br.m.b.BrowserProxies.For(br.m.b, br.path)
	if err != nil {
		return ""
	}
	n, last := px.Refused()
	br.mu.Lock()
	fresh := n - br.refusedSeen
	br.refusedSeen = n
	br.mu.Unlock()
	if fresh <= 0 {
		return ""
	}
	if fresh < len(last) {
		last = last[len(last)-fresh:]
	}
	return "\nrefused by the browser's proxy: " + strings.Join(dedupe(last), ", ") + " (only this worktree's pages; the box's owner can allow an origin)"
}

func dedupe(xs []string) []string {
	var out []string
	for _, x := range xs {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func (br *browser) appendLogs(b *strings.Builder, nConsole, nNet int) {
	b.WriteString(br.refusedNote())
	if c := br.newConsole(nConsole, false); len(c) > 0 {
		b.WriteString("\nconsole:\n  " + strings.Join(c, "\n  "))
	}
	if f := br.newFailures(nNet); len(f) > 0 {
		b.WriteString("\nfailed requests:\n  " + strings.Join(f, "\n  "))
	}
}

func (br *browser) snapLines(ctx context.Context, o snapOptions) ([]string, error) {
	lines, _, err := br.snapBoth(ctx, o)
	return lines, err
}

// snapBoth renders the page as asked, and in full (text included) as the
// baseline the next delta compares with: a click that changes only text
// still shows in the delta.
func (br *browser) snapBoth(ctx context.Context, o snapOptions) (lines, full []string, err error) {
	var tree struct {
		Nodes []axNode `json:"nodes"`
	}
	if err := br.cdp.call(ctx, br.session, "Accessibility.getFullAXTree", nil, &tree); err != nil {
		return nil, nil, err
	}
	br.mu.Lock()
	defer br.mu.Unlock()
	lines = renderAX(tree.Nodes, br.refs, &br.nextRef, o)
	if o.full {
		return lines, lines, nil
	}
	fo := o
	fo.full = true
	full = renderAX(tree.Nodes, br.refs, &br.nextRef, fo)
	return lines, full, nil
}

// capLines keeps text within limit; the rest goes to a file in the
// worktree whose path is printed.
func (br *browser) capLines(lines []string, limit int) (string, string) {
	var b strings.Builder
	for i, l := range lines {
		if b.Len()+len(l)+1 > limit {
			dir := filepath.Join(br.path, ".berth", "browser")
			os.MkdirAll(dir, 0o755)
			file := filepath.Join(dir, fmt.Sprintf("snapshot-%d.txt", time.Now().UnixMilli()))
			os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
			fmt.Fprintf(&b, "… %d more lines in %s (or narrow it: snapshot -s SELECTOR)\n", len(lines)-i, file)
			return b.String(), file
		}
		b.WriteString(l + "\n")
	}
	if len(lines) == 0 {
		b.WriteString("(nothing interactive on the page)\n")
	}
	return b.String(), ""
}

// Snapshot prints the page, or with delta what changed since the last
// snapshot of this page.
func (br *browser) Snapshot(ctx context.Context, full, delta bool, selector string, depth int) (BrowserResult, error) {
	defer br.begin()()
	o := snapOptions{full: full, depth: depth, maxLines: 2000}
	if selector != "" {
		id, err := br.resolve(ctx, selector)
		if err != nil {
			return BrowserResult{}, err
		}
		o.root = id
	}
	lines, fullLines, err := br.snapBoth(ctx, o)
	if err != nil {
		return BrowserResult{}, err
	}
	cur := br.currentURL()
	br.mu.Lock()
	prev, prevURL := br.lastSnap, br.lastSnapURL
	if selector == "" && depth == 0 {
		br.lastSnap, br.lastSnapURL = fullLines, cur
	}
	br.mu.Unlock()
	if delta && prev != nil && prevURL == cur {
		d := diffLines(prev, fullLines)
		return BrowserResult{Text: deltaText(d), URL: cur}, nil
	}
	limit := snapCap
	if full {
		limit = snapFullCap
	}
	text, file := br.capLines(lines, limit)
	return BrowserResult{Text: "url: " + cur + "\n" + strings.TrimRight(text, "\n"), URL: cur, File: file}, nil
}

func deltaText(d []string) string {
	if len(d) == 0 {
		return "(no change)"
	}
	if len(d) > deltaLines {
		return strings.Join(d[:deltaLines], "\n") + fmt.Sprintf("\n… %d more changes (snapshot to see the page)", len(d)-deltaLines)
	}
	return strings.Join(d, "\n")
}

// resolve finds an element by ref (@e3) or CSS selector, as a backend
// node ID.
func (br *browser) resolve(ctx context.Context, target string) (int64, error) {
	if ref, ok := strings.CutPrefix(target, "@"); ok {
		br.mu.Lock()
		defer br.mu.Unlock()
		for id, r := range br.refs {
			if r == ref {
				return id, nil
			}
		}
		return 0, fmt.Errorf("no element %s on this page; take a new snapshot", target)
	}
	var doc struct {
		Root struct {
			NodeID int64 `json:"nodeId"`
		} `json:"root"`
	}
	if err := br.cdp.call(ctx, br.session, "DOM.getDocument", map[string]any{"depth": 0}, &doc); err != nil {
		return 0, err
	}
	var q struct {
		NodeID int64 `json:"nodeId"`
	}
	if err := br.cdp.call(ctx, br.session, "DOM.querySelector", map[string]any{"nodeId": doc.Root.NodeID, "selector": target}, &q); err != nil || q.NodeID == 0 {
		return 0, fmt.Errorf("nothing matches %q", target)
	}
	var d struct {
		Node struct {
			BackendNodeID int64 `json:"backendNodeId"`
		} `json:"node"`
	}
	if err := br.cdp.call(ctx, br.session, "DOM.describeNode", map[string]any{"nodeId": q.NodeID}, &d); err != nil {
		return 0, err
	}
	return d.Node.BackendNodeID, nil
}

func (br *browser) center(ctx context.Context, id int64) (float64, float64, error) {
	br.cdp.call(ctx, br.session, "DOM.scrollIntoViewIfNeeded", map[string]any{"backendNodeId": id}, nil)
	var q struct {
		Quads [][]float64 `json:"quads"`
	}
	if err := br.cdp.call(ctx, br.session, "DOM.getContentQuads", map[string]any{"backendNodeId": id}, &q); err != nil || len(q.Quads) == 0 || len(q.Quads[0]) < 8 {
		return 0, 0, errors.New("the element is not visible")
	}
	p := q.Quads[0]
	return (p[0] + p[2] + p[4] + p[6]) / 4, (p[1] + p[3] + p[5] + p[7]) / 4, nil
}

func (br *browser) callOn(ctx context.Context, id int64, fn string, args ...any) error {
	var obj struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	if err := br.cdp.call(ctx, br.session, "DOM.resolveNode", map[string]any{"backendNodeId": id}, &obj); err != nil {
		return err
	}
	var as []map[string]any
	for _, a := range args {
		as = append(as, map[string]any{"value": a})
	}
	var r struct {
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := br.cdp.call(ctx, br.session, "Runtime.callFunctionOn", map[string]any{"objectId": obj.Object.ObjectID, "functionDeclaration": fn, "arguments": as, "awaitPromise": true}, &r); err != nil {
		return err
	}
	if r.ExceptionDetails != nil {
		return errors.New(r.ExceptionDetails.Text)
	}
	return nil
}

func (br *browser) mouse(ctx context.Context, typ string, x, y float64) error {
	p := map[string]any{"type": typ, "x": x, "y": y}
	if typ != "mouseMoved" {
		p["button"], p["clickCount"] = "left", 1
	}
	return br.cdp.call(ctx, br.session, "Input.dispatchMouseEvent", p, nil)
}

var keyCodes = map[string]int{"Enter": 13, "Tab": 9, "Escape": 27, "Backspace": 8, "Delete": 46, "ArrowUp": 38, "ArrowDown": 40, "ArrowLeft": 37, "ArrowRight": 39, "Home": 36, "End": 35, "PageUp": 33, "PageDown": 34, "Space": 32}

// Act clicks, fills, presses, selects or hovers, then prints what changed:
// a delta of the snapshot, new console errors and failed requests.
func (br *browser) Act(ctx context.Context, action, target, value string) (BrowserResult, error) {
	defer br.begin()()
	before := br.currentURL()
	var id int64
	if action != "press" || target != "" {
		if target == "" {
			return BrowserResult{}, errors.New(action + " needs an element: @eN or a CSS selector")
		}
		var err error
		if id, err = br.resolve(ctx, target); err != nil {
			return BrowserResult{}, err
		}
	}
	switch action {
	case "click", "check":
		x, y, err := br.center(ctx, id)
		if err != nil {
			if err := br.callOn(ctx, id, "function(){this.click()}"); err != nil {
				return BrowserResult{}, err
			}
			break
		}
		for _, t := range []string{"mouseMoved", "mousePressed", "mouseReleased"} {
			if err := br.mouse(ctx, t, x, y); err != nil {
				return BrowserResult{}, err
			}
		}
	case "hover":
		x, y, err := br.center(ctx, id)
		if err != nil {
			return BrowserResult{}, err
		}
		br.mouse(ctx, "mouseMoved", x, y)
	case "fill":
		br.cdp.call(ctx, br.session, "DOM.focus", map[string]any{"backendNodeId": id}, nil)
		err := br.callOn(ctx, id, `function(v){
  this.focus();
  if ("value" in this) {
    const d = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(this), "value");
    if (d && d.set) d.set.call(this, v); else this.value = v;
  } else { this.textContent = v; }
  this.dispatchEvent(new Event("input", {bubbles: true}));
  this.dispatchEvent(new Event("change", {bubbles: true}));
}`, value)
		if err != nil {
			return BrowserResult{}, err
		}
	case "select":
		err := br.callOn(ctx, id, `function(v){
  const o = [...this.options].find(o => o.value === v || o.label === v || o.textContent.trim() === v);
  if (!o) throw new Error("no option " + v);
  this.value = o.value;
  this.dispatchEvent(new Event("input", {bubbles: true}));
  this.dispatchEvent(new Event("change", {bubbles: true}));
}`, value)
		if err != nil {
			return BrowserResult{}, err
		}
	case "press":
		key := value
		if key == "" {
			key = target
		}
		if id != 0 {
			br.cdp.call(ctx, br.session, "DOM.focus", map[string]any{"backendNodeId": id}, nil)
		}
		code := keyCodes[key]
		down := map[string]any{"type": "keyDown", "key": key, "code": key, "windowsVirtualKeyCode": code, "nativeVirtualKeyCode": code}
		if key == "Enter" {
			down["text"] = "\r"
		} else if len(key) == 1 {
			down["text"] = key
			down["type"] = "keyDown"
		}
		if err := br.cdp.call(ctx, br.session, "Input.dispatchKeyEvent", down, nil); err != nil {
			return BrowserResult{}, err
		}
		br.cdp.call(ctx, br.session, "Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": key, "code": key, "windowsVirtualKeyCode": code, "nativeVirtualKeyCode": code}, nil)
	default:
		return BrowserResult{}, fmt.Errorf("unknown action %q", action)
	}
	// A click may start a navigation: give it a moment, then the network.
	time.Sleep(150 * time.Millisecond)
	br.settle(ctx, 3*time.Second)
	var b strings.Builder
	cur := br.currentURL()
	if cur != before {
		fmt.Fprintf(&b, "url: %s\n", cur)
	}
	lines, full, err := br.snapBoth(ctx, snapOptions{maxLines: 2000})
	if err != nil {
		return BrowserResult{}, err
	}
	br.mu.Lock()
	prev, prevURL := br.lastSnap, br.lastSnapURL
	br.lastSnap, br.lastSnapURL = full, cur
	br.mu.Unlock()
	if prev != nil && prevURL == cur {
		b.WriteString(deltaText(diffLines(prev, full)))
	} else {
		text, _ := br.capLines(lines, firstLookCap)
		b.WriteString(strings.TrimRight(text, "\n"))
	}
	br.appendLogs(&b, consoleAfter, netAfter)
	return BrowserResult{Text: strings.TrimRight(b.String(), "\n"), URL: cur}, nil
}

// Wait waits for text on the page, a URL containing url, or the network
// to go quiet.
func (br *browser) Wait(ctx context.Context, text, url string, idle bool, timeout time.Duration) (BrowserResult, error) {
	defer br.begin()()
	if timeout <= 0 || timeout > time.Minute {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	if idle {
		br.settle(ctx, timeout)
		return BrowserResult{Text: "idle", URL: br.currentURL()}, nil
	}
	for {
		ok := false
		if url != "" && strings.Contains(br.currentURL(), url) {
			ok = true
		}
		if text != "" {
			raw, err := br.evaluate(ctx, "document.body ? document.body.innerText.includes("+jsString(text)+") : false")
			if err == nil && string(raw) == "true" {
				ok = true
			}
		}
		if ok {
			return BrowserResult{Text: "ok", URL: br.currentURL()}, nil
		}
		if time.Now().After(deadline) {
			res := BrowserResult{Text: fmt.Sprintf("timed out after %v", timeout), URL: br.currentURL()}
			var b strings.Builder
			b.WriteString(res.Text)
			br.appendLogs(&b, consoleAfter, netAfter)
			res.Text = b.String()
			return res, nil
		}
		select {
		case <-ctx.Done():
			return BrowserResult{}, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Shot saves a screenshot and prints its path, never the image: at most
// width pixels wide (800 by default, which an agent reads for about 530
// tokens), or with native the page's own pixels, its size at its scale.
func (br *browser) Shot(ctx context.Context, el string, full bool, width int, native bool) (BrowserResult, error) {
	defer br.begin()()
	if width <= 0 {
		width = 800
	}
	width = min(width, maxViewportW*int(maxViewportScale))
	v := br.currentViewport()
	dpr := v.scale()
	var metrics struct {
		CSSLayoutViewport struct {
			ClientWidth  float64 `json:"clientWidth"`
			ClientHeight float64 `json:"clientHeight"`
		} `json:"cssLayoutViewport"`
		CSSContentSize struct {
			Width  float64 `json:"width"`
			Height float64 `json:"height"`
		} `json:"cssContentSize"`
	}
	if err := br.cdp.call(ctx, br.session, "Page.getLayoutMetrics", nil, &metrics); err != nil {
		return BrowserResult{}, err
	}
	x, y, w, h := 0.0, 0.0, metrics.CSSLayoutViewport.ClientWidth, metrics.CSSLayoutViewport.ClientHeight
	if full {
		w, h = metrics.CSSContentSize.Width, min(metrics.CSSContentSize.Height, 8000)
	}
	if el != "" {
		id, err := br.resolve(ctx, el)
		if err != nil {
			return BrowserResult{}, err
		}
		br.cdp.call(ctx, br.session, "DOM.scrollIntoViewIfNeeded", map[string]any{"backendNodeId": id}, nil)
		var box struct {
			Model struct {
				Border []float64 `json:"border"`
			} `json:"model"`
		}
		if err := br.cdp.call(ctx, br.session, "DOM.getBoxModel", map[string]any{"backendNodeId": id}, &box); err != nil || len(box.Model.Border) < 8 {
			return BrowserResult{}, errors.New("the element is not visible")
		}
		q := box.Model.Border
		x, y, w, h = q[0], q[1], q[2]-q[0], q[5]-q[1]
	}
	if w <= 0 || h <= 0 {
		w, h = float64(v.Width), float64(v.Height)
	}
	// The image is w·scale·dpr pixels wide: the page's pixels, or fewer.
	scale := 1.0
	if !native {
		scale = min(1, float64(width)/(w*dpr))
	}
	var shot struct {
		Data string `json:"data"`
	}
	params := map[string]any{"format": "png", "clip": map[string]any{"x": x, "y": y, "width": w, "height": h, "scale": scale}, "captureBeyondViewport": full}
	if err := br.cdp.call(ctx, br.session, "Page.captureScreenshot", params, &shot); err != nil {
		return BrowserResult{}, err
	}
	raw, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return BrowserResult{}, err
	}
	dir := br.m.shotDir(br.location, br.worktree)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return BrowserResult{}, err
	}
	name := fmt.Sprintf("shot-%s.png", time.Now().Format("20060102-150405.000"))
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		return BrowserResult{}, err
	}
	pruneShots(dir, keepShots)
	cur := br.currentURL()
	br.m.b.Events.Publish(events.Event{Type: "browser.shot", Box: br.m.b.Name, Origin: "browser", Data: map[string]any{"location": br.location, "name": br.worktree, "path": br.path, "tab": br.tab, "file": name, "url": cur}})
	pw, ph := pngSize(raw)
	if pw == 0 {
		pw, ph = int(math.Round(w*scale*dpr)), int(math.Round(h*scale*dpr))
	}
	return BrowserResult{Text: fmt.Sprintf("shot: %s (%dx%d of the %s page)", file, pw, ph, v), URL: cur, File: file}, nil
}

// pngSize reads a PNG's width and height from its header.
func pngSize(b []byte) (int, int) {
	if len(b) < 24 || string(b[1:4]) != "PNG" {
		return 0, 0
	}
	return int(binary.BigEndian.Uint32(b[16:20])), int(binary.BigEndian.Uint32(b[20:24]))
}

func pruneShots(dir string, keep int) {
	m, _ := filepath.Glob(filepath.Join(dir, "shot-*.png"))
	sort.Strings(m)
	for len(m) > keep {
		os.Remove(m[0])
		m = m[1:]
	}
}

// Console prints errors and warnings since the last look (all levels with
// all).
func (br *browser) Console(all bool) BrowserResult {
	br.touch()
	n := consoleLines
	if all {
		n = 50
	}
	c := br.newConsole(n, all)
	if len(c) == 0 {
		return BrowserResult{Text: "(no new errors)"}
	}
	return BrowserResult{Text: strings.Join(c, "\n")}
}

// Network prints failed requests since the last look.
func (br *browser) Network() BrowserResult {
	br.touch()
	f := br.newFailures(netLines)
	if len(f) == 0 {
		return BrowserResult{Text: "(no failed requests)"}
	}
	return BrowserResult{Text: strings.Join(f, "\n")}
}

// Eval runs JavaScript in the page and prints its value, at most 2 KB.
func (br *browser) Eval(ctx context.Context, js string) (BrowserResult, error) {
	defer br.begin()()
	raw, err := br.evaluate(ctx, js)
	if err != nil {
		return BrowserResult{Text: "error: " + err.Error()}, nil
	}
	s := string(raw)
	if len(s) > evalCap {
		s = s[:evalCap] + fmt.Sprintf("… (%d bytes in all)", len(raw))
	}
	if s == "" {
		s = "undefined"
	}
	return BrowserResult{Text: s, URL: br.currentURL()}, nil
}

// Watch adds a screencast watcher. The page is cast only while someone
// watches: the first starts it, the last stops it. Chromium casts a frame
// only when the page repaints, so a page sitting still would show nothing:
// each new watcher gets the page as it is now first.
func (br *browser) Watch(ctx context.Context) (chan frame, func()) {
	ch := make(chan frame, 2)
	br.mu.Lock()
	br.watchers[ch] = struct{}{}
	start := !br.casting
	br.casting = true
	br.mu.Unlock()
	if start {
		br.cdp.call(ctx, br.session, "Page.startScreencast", br.castParams(br.currentViewport()), nil)
	}
	go br.firstFrame(ctx, ch)
	stop := func() {
		br.mu.Lock()
		if _, ok := br.watchers[ch]; ok {
			delete(br.watchers, ch)
			close(ch)
		}
		last := len(br.watchers) == 0 && br.casting
		if last {
			br.casting = false
		}
		br.mu.Unlock()
		if last {
			c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			br.cdp.call(c, br.session, "Page.stopScreencast", nil, nil)
			cancel()
		}
	}
	return ch, stop
}

// firstFrame sends a watcher the page as it is, unless a cast frame got
// there first.
func (br *browser) firstFrame(ctx context.Context, ch chan frame) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	f, err := br.captureFrame(ctx)
	if err != nil {
		return
	}
	br.mu.Lock()
	defer br.mu.Unlock()
	if _, ok := br.watchers[ch]; ok && len(ch) == 0 {
		select {
		case ch <- f:
		default:
		}
	}
}

// Artifacts are what a worktree's browser left for Review: the last
// shots, the URL it was on, and its last console errors.
type BrowserArtifacts struct {
	URL    string   `json:"url,omitempty"`
	Shots  []string `json:"shots,omitempty"`
	Errors []string `json:"errors,omitempty"`
}

func (m *Browsers) Artifacts(location, worktree, path string) *BrowserArtifacts {
	a := &BrowserArtifacts{}
	shots, _ := filepath.Glob(filepath.Join(m.shotDir(location, worktree), "shot-*.png"))
	sort.Sort(sort.Reverse(sort.StringSlice(shots)))
	for i, s := range shots {
		if i == 3 {
			break
		}
		a.Shots = append(a.Shots, filepath.Base(s))
	}
	if c := m.lookupChromium(path); c != nil && c.recent() != nil {
		br := c.recent()
		br.mu.Lock()
		a.URL = br.url
		for i := len(br.console) - 1; i >= 0 && len(a.Errors) < 5; i-- {
			if br.console[i].level == "error" {
				a.Errors = append(a.Errors, br.console[i].text)
			}
		}
		br.mu.Unlock()
	}
	if a.URL == "" && len(a.Shots) == 0 && len(a.Errors) == 0 {
		return nil
	}
	return a
}

// InstallChromium downloads Playwright's Chromium headless shell (about 100
// MB on disk) with npx, for agents' browsers.
func InstallChromium(out io.Writer) error {
	if p, err := FindChromium(); err == nil {
		fmt.Fprintf(out, "Chromium is already here: %s\n", p)
		return nil
	}
	npx, err := exec.LookPath("npx")
	if err != nil {
		return errors.New("npx is not installed; install Node.js, or a Chromium package, or set BERTH_CHROMIUM to a Chromium binary")
	}
	cmd := exec.Command(npx, "-y", "playwright@latest", "install", "chromium-headless-shell")
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return err
	}
	p, err := FindChromium()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Agents' browsers will use %s\n", p)
	return nil
}
