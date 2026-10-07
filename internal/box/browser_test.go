package box

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sean-brydon/berthd/internal/events"
	"github.com/sean-brydon/berthd/internal/trust"
)

// browserBox is a box with one repository, a worktree "billing", and a dev
// server listening on the worktree's own port.
func browserBox(t *testing.T, page http.HandlerFunc) (*Box, Worktree, int) {
	t.Helper()
	ctx := context.Background()
	repo := gitRepo(t)
	dir := t.TempDir()
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(dir, "locations.json")), Events: &events.Bus{}, Sessions: testSessions(t),
		Flows: &Flows{Path: filepath.Join(dir, "flows.json")}}
	b.Locations.Add(ctx, "cal", repo)
	wt, err := b.Locations.CreateWorktree(ctx, "cal", "billing", "", "")
	if err != nil {
		t.Fatal(err)
	}
	port, err := b.Locations.Ports.For(wt.Path)
	if err != nil || port == 0 {
		t.Fatalf("no port block: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Skipf("the worktree's port %d is taken: %v", port, err)
	}
	srv := &http.Server{Handler: page}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	b.BrowserProxies = &BrowserProxies{Path: filepath.Join(dir, "proxies.json")}
	t.Cleanup(b.BrowserProxies.CloseAll)
	return b, wt, port
}

func viaProxy(t *testing.T, proxyAddr string) *http.Client {
	u, _ := url.Parse(proxyAddr)
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 10 * time.Second}
}

func TestTheBrowserProxyReachesOnlyItsWorktree(t *testing.T) {
	var seenHost string
	var port int
	b, wt, p := browserBox(t, func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.Host
		if r.URL.Path == "/login" {
			http.Redirect(w, r, "http://localhost:"+strconv.Itoa(port)+"/me", http.StatusFound)
			return
		}
		fmt.Fprint(w, "page "+r.URL.Path)
	})
	port = p
	px, err := b.BrowserProxies.For(b, wt.Path)
	if err != nil {
		t.Fatal(err)
	}
	c := viaProxy(t, px.Addr())
	get := func(u string) (int, string, http.Header) {
		resp, err := c.Get(u)
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body), resp.Header
	}
	// The human's URL, with whatever the laptop calls the box, any port.
	for _, u := range []string{"http://billing.cal.mybox.localhost:1377/x", "http://billing.cal.localhost/x", "http://localhost:" + strconv.Itoa(port) + "/x"} {
		if code, body, _ := get(u); code != 200 || body != "page /x" {
			t.Fatalf("%s: %d %q", u, code, body)
		}
	}
	if seenHost != "localhost:"+strconv.Itoa(port) {
		t.Fatalf("the dev server saw Host %q", seenHost)
	}
	// A redirect to localhost maps back to the name the page was opened by.
	if code, _, h := get("http://billing.cal.mybox.localhost:1377/login"); code != 302 || h.Get("Location") != "http://billing.cal.mybox.localhost:1377/me" {
		t.Fatalf("redirect: %d %q", code, h.Get("Location"))
	}
	// Everything else is refused: other worktrees, berthd, other ports,
	// metadata, the internet.
	for _, u := range []string{"http://main.cal.mybox.localhost/", "http://cal.mybox.localhost/", "http://localhost:7444/", "http://127.0.0.1:5432/", "http://169.254.169.254/latest", "http://example.com/", "http://10.0.0.1/"} {
		if code, body, _ := get(u); code != 403 || !strings.Contains(body, "only reaches this worktree") {
			t.Fatalf("%s was not refused: %d %q", u, code, body)
		}
	}
	if n, last := px.Refused(); n < 7 || len(last) != 5 {
		t.Fatalf("refused %d, last %v", n, last)
	}
	// The same port after a restart, so BERTH_BROWSER_PROXY stays right.
	b.BrowserProxies.CloseAll()
	b.BrowserProxies = &BrowserProxies{Path: b.BrowserProxies.Path}
	again, err := b.BrowserProxies.For(b, wt.Path)
	if err != nil || again.port != px.port {
		t.Fatalf("port %d then %d (%v)", px.port, again.port, err)
	}
}

// Chromium sends ws:// through the proxy as CONNECT: the tunnel reaches the
// worktree's server; a tunnel anywhere else is refused.
func TestTheBrowserProxyTunnelsOnlyToTheWorktree(t *testing.T) {
	b, wt, _ := browserBox(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "tunnelled "+r.URL.Path) })
	px, _ := b.BrowserProxies.For(b, wt.Path)
	connect := func(target string) (string, net.Conn) {
		conn, err := net.Dial("tcp", strings.TrimPrefix(px.Addr(), "http://"))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
		line, _ := bufio.NewReader(conn).ReadString('\n')
		return line, conn
	}
	line, conn := connect("billing.cal.box.localhost:1377")
	defer conn.Close()
	if !strings.Contains(line, "200") {
		t.Fatalf("CONNECT: %q", line)
	}
	// Read the blank line after the status, then speak HTTP in the tunnel.
	r := bufio.NewReader(conn)
	fmt.Fprintf(conn, "GET /hmr HTTP/1.1\r\nHost: billing.cal.box.localhost:1377\r\nConnection: close\r\n\r\n")
	all, _ := io.ReadAll(r)
	if !strings.Contains(string(all), "tunnelled /hmr") {
		t.Fatalf("through the tunnel: %q", all)
	}
	if line, c := connect("example.com:443"); !strings.Contains(line, "403") {
		c.Close()
		t.Fatalf("a tunnel to the internet: %q", line)
	} else {
		c.Close()
	}
}

func TestBrowserAllowListAndRoutes(t *testing.T) {
	s := browserScope{loc: Location{Name: "cal"}, wt: Worktree{Name: "billing"}, ports: []int{4100, 4101}, dev: 4100, allow: []string{"https://accounts.example.com", "auth.example.org"}}
	for host, want := range map[string]bool{
		"billing.cal.x.localhost:1377": true, "billing.cal.localhost": true, "4101.x.localhost": true, "localhost:4101": true,
		"4102.x.localhost": false, "localhost:4102": false, "other.cal.x.localhost": false, "accounts.example.com:443": true, "auth.example.org": true,
		"example.com": false, "cal.x.localhost": false,
	} {
		if _, ok := s.route(host); ok != want {
			t.Errorf("%s allowed = %v, want %v", host, ok, want)
		}
	}
	main := browserScope{loc: Location{Name: "cal"}, wt: Worktree{Name: "cal", Main: true}, ports: []int{4000}, dev: 4000}
	if _, ok := main.route("cal.x.localhost"); !ok {
		t.Error("the main checkout's own name was refused")
	}
	if allowHost("https://Accounts.Example.com:443/path") != "accounts.example.com" {
		t.Error(allowHost("https://Accounts.Example.com:443/path"))
	}
}

func TestWorktreeURL(t *testing.T) {
	if u := worktreeURL("devbox", "shop", Worktree{Name: "checkout"}); u != "http://checkout.shop.devbox.localhost:1377" {
		t.Fatal(u)
	}
	if u := worktreeURL("devbox", "shop", Worktree{Name: "shop", Main: true}); u != "http://shop.devbox.localhost:1377" {
		t.Fatal(u)
	}
	if u := worktreeURL("devbox", "shop", Worktree{Name: "Fix_Login"}); u != "" {
		t.Fatal(u)
	}
}

// A box on a cloud VM has a fully qualified hostname; berthd names itself
// by its first label, so its worktrees still have URLs.
func TestWorktreeURLOnAnFQDNBox(t *testing.T) {
	name := trust.NameFromHostname("devbox.europe-north1-a.c.sales-moitoring.internal", "box")
	if u := worktreeURL(name, "shop", Worktree{Name: "checkout"}); u != "http://checkout.shop.devbox.localhost:1377" {
		t.Fatal(u)
	}
	if u := worktreeURL(name, "shop", Worktree{Name: "shop", Main: true}); u != "http://shop.devbox.localhost:1377" {
		t.Fatal(u)
	}
	// A name with dots, as berthd took from such a hostname before, has none.
	if u := worktreeURL("devbox.europe-north1-a.c.sales-moitoring.internal", "shop", Worktree{Name: "checkout"}); u != "" {
		t.Fatal(u)
	}
}

func TestSnapshotRefsAndDeltas(t *testing.T) {
	nodes := func(label string) []axNode {
		mk := func(id, role, name, parent string, backend int64, kids ...string) axNode {
			return axNode{NodeID: id, Role: &axValue{role}, Name: &axValue{name}, ParentID: parent, ChildIDs: kids, BackendDOMNodeID: backend}
		}
		return []axNode{
			mk("1", "RootWebArea", "Shop", "", 1, "2", "3", "4", "5"),
			mk("2", "heading", "Cart", "1", 2),
			mk("3", "generic", "", "1", 3, "6"),
			mk("6", "button", label, "3", 6),
			mk("4", "textbox", "Email", "1", 4),
			mk("5", "StaticText", "lots of prose", "1", 5),
		}
	}
	refs, next := map[int64]string{}, 0
	first := renderAX(nodes("Add (0)"), refs, &next, snapOptions{})
	want := []string{`- heading "Cart"`, `- button "Add (0)" [@e1]`, `- textbox "Email" [@e2]`}
	if strings.Join(first, "\n") != strings.Join(want, "\n") {
		t.Fatalf("snapshot:\n%s", strings.Join(first, "\n"))
	}
	second := renderAX(nodes("Add (1)"), refs, &next, snapOptions{})
	d := diffLines(first, second)
	if len(d) != 2 || d[0] != `- button "Add (0)" [@e1]` || d[1] != `+ button "Add (1)" [@e1]` {
		t.Fatalf("delta %q", d)
	}
	full := renderAX(nodes("x"), refs, &next, snapOptions{full: true})
	if !strings.Contains(strings.Join(full, "\n"), `text "lots of prose"`) {
		t.Fatalf("full snapshot %q", full)
	}
}

const testPage = `<!doctype html><title>Shop cart</title>
<h1>Cart</h1>
<p>Items: <span id=n>0</span></p>
<button onclick="document.getElementById('n').textContent = ++window.c || (window.c = 1); this.textContent = 'Add (' + window.c + ')'">Add (0)</button>
<form onsubmit="event.preventDefault(); document.body.insertAdjacentHTML('beforeend', '<p role=status>Saved ' + email.value + '</p>')"><label>Email <input id=email></label><button>Save</button></form>
<a href="/next">Next page</a>
<script>console.error("boom from the page"); fetch("http://example.com/leak").catch(()=>{});</script>`

// A real headless Chromium through the proxy, driven over the pipe.
func TestAgentBrowserDrivesARealChromium(t *testing.T) {
	if testing.Short() {
		t.Skip("starts Chromium")
	}
	// A real browser is slow to start on a busy runner: CI opts in, and a
	// release build doesn't wait on it.
	if os.Getenv("BERTH_TEST_CHROMIUM") == "" && os.Getenv("CI") != "" {
		t.Skip("set BERTH_TEST_CHROMIUM=1 to start a real Chromium in CI")
	}
	if _, err := FindChromium(); err != nil {
		t.Skip(err)
	}
	b, wt, _ := browserBox(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/next" {
			fmt.Fprint(w, `<title>Next</title><h2>Second page</h2><a href="/">Back</a>`)
			return
		}
		fmt.Fprint(w, testPage)
	})
	m := b.NewBrowsers(t.TempDir(), 2)
	time.Sleep(100 * time.Millisecond)
	goroutines := runtime.NumGoroutine()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	loc, _ := b.Locations.Get(ctx, "cal")
	br, err := m.get(ctx, loc, wt)
	if errors.Is(err, ErrBrowserSandbox) {
		t.Skip(err) // CI sets BERTH_BROWSER_NO_SANDBOX=1 instead
	}
	if err != nil {
		t.Fatal(err)
	}
	defer m.CloseAll("test")
	res, err := br.Open(ctx, "http://billing.cal.mybox.localhost:1377/")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("open (%d bytes):\n%s", len(res.Text), res.Text)
	for _, want := range []string{"title: Shop cart", `heading "Cart"`, `button "Add (0)" [@e`, `textbox "Email"`, `link "Next page"`, "boom from the page"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("open lacks %q:\n%s", want, res.Text)
		}
	}
	ref := func(label string) string {
		for _, l := range strings.Split(res.Text, "\n") {
			if strings.Contains(l, label) {
				return l[strings.LastIndex(l, "[@")+1 : len(l)-1]
			}
		}
		t.Fatalf("no ref for %s", label)
		return ""
	}
	// A page sitting still still shows: a watcher gets it at once, not on
	// its next repaint.
	frames, stopWatching := br.Watch(ctx)
	select {
	case f := <-frames:
		if len(f.Data) < 1000 || !strings.HasSuffix(f.URL, "/") {
			t.Fatalf("first frame: %d bytes of %q", len(f.Data), f.URL)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no frame of a still page")
	}
	stopWatching()
	add, email := ref(`"Add (0)"`), ref(`"Email"`)
	act, err := br.Act(ctx, "click", add, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("click:\n%s", act.Text)
	if !strings.Contains(act.Text, `+ button "Add (1)" [`+add+`]`) || !strings.Contains(act.Text, `+ text "1"`) || len(act.Text) > 400 {
		t.Fatalf("click delta:\n%s", act.Text)
	}
	if _, err := br.Act(ctx, "fill", email, "ann@example.com"); err != nil {
		t.Fatal(err)
	}
	act, err = br.Act(ctx, "press", email, "Enter")
	if err != nil {
		t.Fatal(err)
	}
	if w, _ := br.Wait(ctx, "Saved ann@example.com", "", false, 5*time.Second); w.Text != "ok" {
		t.Fatalf("after Enter: %s / %s", w.Text, act.Text)
	}
	shot, err := br.Shot(ctx, "", false, 800)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(shot.File); err != nil || st.Size() < 1000 || !strings.Contains(shot.Text, "(800x") {
		t.Fatalf("shot %+v %v", shot, err)
	}
	if !strings.Contains(res.Text, "example.com") {
		t.Fatalf("the refused fetch is not reported:\n%s", res.Text)
	}
	if c := br.Console(false); c.Text != "(no new errors)" {
		t.Logf("console: %s", c.Text)
	}
	ev, _ := br.Eval(ctx, "document.querySelectorAll('button').length")
	if ev.Text != "2" {
		t.Fatalf("eval %q", ev.Text)
	}
	nav, _ := br.Act(ctx, "click", ref(`"Next page"`), "")
	if !strings.Contains(nav.Text, "url: http://billing.cal.mybox.localhost:1377/next") || !strings.Contains(nav.Text, "Second page") {
		t.Fatalf("navigation:\n%s", nav.Text)
	}
	if a := m.Artifacts("cal", "billing", wt.Path); a == nil || len(a.Shots) != 1 || !strings.HasSuffix(a.URL, "/next") {
		t.Fatalf("artifacts %+v", a)
	}
	pid := br.cmd.Process.Pid
	st := br.status()
	t.Logf("Chromium tree RSS: %d MB", st.RSS>>20)
	// Unused, it closes by itself, and its process tree goes with it.
	m.Idle = 500 * time.Millisecond
	rctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(rctx); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for m.Lookup(wt.Path) != nil && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	stop()
	<-done
	if m.Lookup(wt.Path) != nil || treeRSS(pid) != 0 {
		t.Fatal("an idle browser was not closed")
	}
	// Its goroutines (the pipe reader, the waiter, tunnels) went with it,
	// and the proxy's, once the worktree's proxy closes too.
	b.BrowserProxies.CloseAll()
	deadline = time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > goroutines+2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > goroutines+2 {
		buf := make([]byte, 1<<16)
		t.Fatalf("goroutines %d before, %d after:\n%s", goroutines, n, buf[:runtime.Stack(buf, true)])
	}
}
