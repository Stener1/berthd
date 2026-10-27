package box

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTabNames(t *testing.T) {
	for _, ok := range []string{"", "a", "L2-l2-base-a", "signup_b.2"} {
		if err := CheckTabName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"-a", "a b", "a/b", "ü", strings.Repeat("a", 49)} {
		if CheckTabName(bad) == nil {
			t.Errorf("%q is a name", bad)
		}
	}
	if k := viewportKey("/w/cal-billing/", "signup-a"); k != "/w/cal-billing#tab=signup-a" {
		t.Errorf("key %q", k)
	}
}

func TestATabKeepsItsOwnSizeAndOpensAtTheWorktreesOtherwise(t *testing.T) {
	m := &Browsers{Dir: t.TempDir()}
	phone, tablet := Viewport{390, 844, 3}, Viewport{820, 1180, 2}
	m.setViewport("/w/cal-billing", "", tablet)
	if v := m.Viewport("/w/cal-billing", "signup-a"); v != tablet {
		t.Fatalf("a tab without a size of its own: %v", v)
	}
	m.setViewport("/w/cal-billing", "signup-a", phone)
	if v := m.Viewport("/w/cal-billing", "signup-a"); v != phone {
		t.Fatalf("the tab's own: %v", v)
	}
	if v := m.Viewport("/w/cal-billing", ""); v != tablet {
		t.Fatalf("the main tab took the tab's size: %v", v)
	}
	m.forgetViewport("/w/cal-billing")
	if len(m.viewports()) != 0 {
		t.Fatalf("a removed worktree's sizes are kept: %v", m.viewports())
	}
}

// Several agents in one worktree, each in its own tab, at once: each keeps
// its own page, refs, cookies and size, every tab renders whether or not it
// is in front, and the live view of one shows only that one.
func TestTabsKeepAgentsInOneWorktreeApart(t *testing.T) {
	if testing.Short() {
		t.Skip("starts Chromium")
	}
	if os.Getenv("BERTH_TEST_CHROMIUM") == "" && os.Getenv("CI") != "" {
		t.Skip("set BERTH_TEST_CHROMIUM=1 to start a real Chromium in CI")
	}
	if _, err := FindChromium(); err != nil {
		t.Skip(err)
	}
	b, wt, _ := browserBox(t, func(w http.ResponseWriter, r *http.Request) {
		n := strings.TrimPrefix(r.URL.Path, "/p/")
		// Each page counts animation frames and timer ticks, and tries
		// to reach outside the worktree.
		fmt.Fprintf(w, `<title>Page %[1]s</title><h1>Page %[1]s</h1><button onclick="this.textContent='Clicked %[1]s'">Go %[1]s</button>
<script>
window.frames_ = 0; window.ticks = 0;
(function f(){ window.frames_++; requestAnimationFrame(f); })();
setInterval(() => window.ticks++, 20);
fetch("http://example.com/").catch(() => {});
</script>`, n)
	})
	m := b.NewBrowsers(t.TempDir(), 2)
	m.MaxTabs = 2
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	loc, _ := b.Locations.Get(ctx, "cal")
	main, err := m.get(ctx, loc, wt, "")
	if errors.Is(err, ErrBrowserSandbox) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer m.CloseAll("test")
	evs, unsub := b.Events.SubscribeNamed("test")
	defer unsub()

	// Two agents open their own tabs and pages at the same time.
	site := "http://billing.cal.mybox.localhost:1377"
	tabs := map[string]*browser{}
	opened := map[string]BrowserResult{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, name := range []string{"signup-a", "signup-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			br, err := m.get(ctx, loc, wt, name)
			if err != nil {
				t.Error(err)
				return
			}
			res, err := br.Open(ctx, site+"/p/"+name)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			tabs[name], opened[name] = br, res
			mu.Unlock()
		}()
	}
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}
	a, bb := tabs["signup-a"], tabs["signup-b"]
	for name, res := range opened {
		if !strings.Contains(res.Text, "title: Page "+name) || !strings.Contains(res.Text, `button "Go `+name+`"`) {
			t.Fatalf("%s opened:\n%s", name, res.Text)
		}
		// The proxy keeps a tab's own context to the worktree too.
		if !strings.Contains(res.Text, "example.com") {
			t.Fatalf("%s: the refused fetch is not reported:\n%s", name, res.Text)
		}
	}
	if u := main.currentURL(); strings.Contains(u, "/p/") {
		t.Fatalf("the main tab moved to %s", u)
	}

	// Each tab's refs are its own: a click in one changes only that one.
	ref := opened["signup-a"].Text
	ref = ref[strings.Index(ref, `button "Go signup-a" [`)+len(`button "Go signup-a" [`):]
	ref = ref[:strings.Index(ref, "]")]
	if _, err := a.Act(ctx, "click", ref, ""); err != nil {
		t.Fatal(err)
	}
	if s, _ := a.Snapshot(ctx, false, false, "", 0); !strings.Contains(s.Text, "Clicked signup-a") {
		t.Fatalf("a after the click:\n%s", s.Text)
	}
	if s, _ := bb.Snapshot(ctx, false, false, "", 0); !strings.Contains(s.Text, "Go signup-b") || strings.Contains(s.Text, "Clicked") {
		t.Fatalf("b after a's click:\n%s", s.Text)
	}

	// Every tab is visible to its page and renders, the one opened first
	// as much as the last: animation frames and timers run in both.
	count := func(br *browser) (int, int) {
		r, _ := br.Eval(ctx, `document.visibilityState + " " + window.frames_ + " " + window.ticks`)
		f := strings.Fields(strings.Trim(r.Text, `"`))
		if len(f) != 3 || f[0] != "visible" {
			t.Fatalf("%s: %s", br.tab, r.Text)
		}
		fr, _ := strconv.Atoi(f[1])
		tk, _ := strconv.Atoi(f[2])
		return fr, tk
	}
	fa, ta := count(a)
	fb, tb := count(bb)
	time.Sleep(time.Second)
	fa2, ta2 := count(a)
	fb2, tb2 := count(bb)
	t.Logf("in a second: a %d frames %d ticks, b %d frames %d ticks", fa2-fa, ta2-ta, fb2-fb, tb2-tb)
	for _, d := range []int{fa2 - fa, fb2 - fb} {
		if d < 20 {
			t.Fatalf("a tab's animation frames are throttled: a %d, b %d a second", fa2-fa, fb2-fb)
		}
	}
	for _, d := range []int{ta2 - ta, tb2 - tb} {
		if d < 20 {
			t.Fatalf("a tab's timers are throttled: a %d, b %d a second", ta2-ta, tb2-tb)
		}
	}

	// Cookies are a tab's own.
	a.Eval(ctx, `document.cookie = "who=a; path=/"`)
	if r, _ := bb.Eval(ctx, "document.cookie"); strings.Contains(r.Text, "who=a") {
		t.Fatalf("b sees a's cookie: %s", r.Text)
	}
	if r, _ := a.Eval(ctx, "document.cookie"); !strings.Contains(r.Text, "who=a") {
		t.Fatalf("a lost its cookie: %s", r.Text)
	}

	// A size is a tab's own.
	phone := Viewport{390, 844, 3}
	if _, err := m.Resize(ctx, wt.Path, "signup-b", phone); err != nil {
		t.Fatal(err)
	}
	if v := a.currentViewport(); v == phone {
		t.Fatal("a took b's size")
	}
	if r, _ := bb.Eval(ctx, "innerWidth"); r.Text != "390" {
		t.Fatalf("b's width %s", r.Text)
	}

	// Watching b shows b, and a's page changing sends b's watcher nothing.
	frames, stop := bb.Watch(ctx)
	select {
	case f := <-frames:
		if !strings.HasSuffix(f.URL, "/p/signup-b") {
			t.Fatalf("b's watcher got %s", f.URL)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no frame of b")
	}
	if _, err := a.Open(ctx, site+"/p/elsewhere"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
watch:
	for {
		select {
		case f := <-frames:
			if !strings.HasSuffix(f.URL, "/p/signup-b") {
				t.Fatalf("b's watcher got a's page %s", f.URL)
			}
		case <-deadline:
			break watch
		}
	}
	stop()

	// The browser lists its tabs: the main one, unused, is left out.
	st := main.chromium.status()
	var names []string
	for _, ts := range st.Tabs {
		names = append(names, ts.Name)
	}
	// They opened at once, in either order.
	sort.Strings(names)
	if strings.Join(names, ",") != "signup-a,signup-b" {
		t.Fatalf("tabs %v", names)
	}

	// At the cap (2 here; the main tab, unused, doesn't count), a tab in
	// use is not taken for another: it is refused.
	if _, err := m.get(ctx, loc, wt, "onboarding"); err == nil || !strings.Contains(err.Error(), "close one") {
		t.Fatalf("a fourth tab: %v", err)
	}
	if !m.CloseTab(wt.Path, "signup-a", "closed") || m.Lookup(wt.Path, "signup-a") != nil {
		t.Fatal("signup-a didn't close")
	}
	if _, err := m.get(ctx, loc, wt, "onboarding"); err != nil {
		t.Fatalf("after closing one: %v", err)
	}
	seen := map[string]bool{}
	for len(evs) > 0 {
		e := <-evs
		if tab, _ := e.Data["tab"].(string); tab != "" {
			seen[e.Type+" "+tab] = true
		}
	}
	for _, want := range []string{"browser.tab.opened signup-a", "browser.tab.closed signup-a", "browser.navigated signup-b", "browser.tab.opened onboarding"} {
		if !seen[want] {
			t.Errorf("no event %s in %v", want, seen)
		}
	}
	if pid := main.cmd.Process.Pid; pid == 0 {
		t.Fatal("no process")
	} else {
		t.Logf("Chromium tree RSS with 3 tabs: %d MB", treeRSS(pid)>>20)
	}
}
