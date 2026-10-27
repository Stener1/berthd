package agent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/events"
	"github.com/cosscom/shipyard/internal/wire"
)

// The docs' example login script: it signs in through the app's own
// credentials endpoint, on the worktree's dev server, and prints the
// session cookie the app set.
const acmeLoginScript = `#!/bin/sh
set -eu
jar=$(mktemp)
trap 'rm -f "$jar"' EXIT
body=$(printf '{"email":"%s","password":"%s"}' "$BERTH_LOGIN_EMAIL" "${ACME_DEV_PASSWORD:-acme-dev}")
curl -fsS -o /dev/null -c "$jar" -H 'content-type: application/json' \
  --data "$body" "http://localhost:$BERTH_PORT/api/auth/login"
session=$(awk -F '\t' '$6 == "session" { print $7 }' "$jar")
[ -n "$session" ] || { echo "the app set no session cookie" >&2; exit 1; }
printf '{"cookies":[{"name":"session","value":"%s","httpOnly":true,"sameSite":"Lax"}],"redirect":"/dashboard"}\n' "$session"
`

// acmeApp is a tiny app with a credentials endpoint that sets a session
// cookie, and a page that says who the session is.
type acmeApp struct {
	mu       sync.Mutex
	sessions map[string]string
}

func (a *acmeApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/auth/login":
		var in struct{ Email, Password string }
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&in) != nil || in.Password != "acme-dev" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		b := make([]byte, 16)
		rand.Read(b)
		tok := hex.EncodeToString(b)
		a.mu.Lock()
		a.sessions[tok] = in.Email
		a.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "session", Value: tok, Path: "/", HttpOnly: true})
		io.WriteString(w, `{"ok":true}`)
	default:
		who := "nobody"
		if c, err := r.Cookie("session"); err == nil {
			a.mu.Lock()
			if e, ok := a.sessions[c.Value]; ok {
				who = e
			}
			a.mu.Unlock()
		}
		io.WriteString(w, r.URL.Path+": signed in as "+who)
	}
}

func agentGitRepo(t *testing.T) string {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(root, "shop")
	os.MkdirAll(repo, 0o755)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"commit", "-q", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@acme.test", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@acme.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	return repo
}

func TestLoggingInThroughTheProxyGivesTheAppItsSession(t *testing.T) {
	for _, tool := range []string{"git", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	ctx := context.Background()
	dir := t.TempDir()
	// A real box: the project shop, a worktree fix-x, and a kit with a
	// login script and its users.
	bx := &box.Box{Name: "devbox", Locations: box.NewLocations(filepath.Join(dir, "locations.json")), Events: &events.Bus{}, KitsDir: filepath.Join(dir, "kits")}
	if _, err := bx.Locations.Add(ctx, "shop", agentGitRepo(t)); err != nil {
		t.Fatal(err)
	}
	// Port blocks something on this machine already listens on are held
	// for nobody, so the worktree gets a free one.
	for i := range 50 {
		held := filepath.Join(dir, "held-"+strconv.Itoa(i))
		os.MkdirAll(held, 0o700)
		p, err := bx.Locations.Ports.For(held)
		if err != nil || p == 0 {
			break
		}
		if ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p)); err == nil {
			ln.Close()
			bx.Locations.Ports.Release(held)
			break
		}
	}
	wt, err := bx.Locations.CreateWorktree(ctx, "shop", "fix-x", "", "")
	if err != nil {
		t.Fatal(err)
	}
	kit := box.KitInstall{
		Kit: box.Kit{ID: "shop-dev", Name: "Shop", Config: box.RepoConfig{Login: &box.LoginConfig{
			Script: "scripts/login.sh",
			Users:  []box.LoginUser{{Email: "pro@acme.test", Label: "Pro user"}},
		}}},
		Files: map[string]string{"scripts/login.sh": base64.StdEncoding.EncodeToString([]byte(acmeLoginScript))},
	}
	if _, err := bx.InstallKit(ctx, "shop", kit); err != nil {
		t.Fatal(err)
	}
	// The app runs on the worktree's own port, as its dev server would.
	port, err := bx.Locations.Ports.For(wt.Path)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Skipf("the worktree's port %d is taken: %v", port, err)
	}
	app := &http.Server{Handler: &acmeApp{sessions: map[string]string{}}}
	go app.Serve(ln)
	t.Cleanup(func() { app.Close() })

	var mu sync.Mutex
	var logins []string
	b := newBoxWith(t, func(s *wire.Server) {
		s.Handle("POST /v1/worktrees/{loc}/{wt}/login", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			logins = append(logins, r.PathValue("loc")+"/"+r.PathValue("wt"))
			mu.Unlock()
			bx.HandleLogin(w, r)
		}))
		s.Handle("GET /v1/locations", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			locs, _ := bx.Locations.List(r.Context())
			json.NewEncoder(w).Encode(locs)
		}))
	})
	b.services = []box.Service{{Location: "shop", Worktree: "fix-x", Port: port}}
	a := startAgent(t, b.pairLaptop())
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })

	// A browser: every *.localhost name reaches the laptop's proxy, and it
	// keeps cookies per host.
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, Timeout: 30 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, a.proxy)
		},
	}}
	_, proxyPort, _ := net.SplitHostPort(a.proxy)
	origin := "http://fix-x.shop.devbox.localhost:" + proxyPort

	// Before: nobody.
	if got := fetch(t, browser, origin+"/me"); got != "/me: signed in as nobody" {
		t.Fatalf("before logging in: %q", got)
	}
	// Log in, follow the redirect: the app sees the session.
	if got := fetch(t, browser, origin+"/__berth/login?as=pro%40acme.test&next=%2Fevent-types"); got != "/event-types: signed in as pro@acme.test" {
		t.Fatalf("after logging in: %q", got)
	}
	if got := fetch(t, browser, origin+"/me"); got != "/me: signed in as pro@acme.test" {
		t.Fatalf("the session didn't hold: %q", got)
	}
	// Without next, the login script's redirect.
	if got := fetch(t, browser, origin+"/__berth/login?as=pro%40acme.test"); got != "/dashboard: signed in as pro@acme.test" {
		t.Fatalf("no next: %q", got)
	}
	mu.Lock()
	if len(logins) != 2 || logins[0] != "shop/fix-x" {
		t.Fatalf("the box ran logins for %v", logins)
	}
	mu.Unlock()
	// The cookie is the worktree host's alone.
	u, _ := url.Parse(origin)
	if cs := jar.Cookies(u); len(cs) != 1 || cs[0].Name != "session" {
		t.Fatalf("cookies for the worktree: %v", cs)
	}
	for _, other := range []string{"http://shop.devbox.localhost:" + proxyPort, "http://other.shop.devbox.localhost:" + proxyPort, "http://localhost:" + proxyPort} {
		o, _ := url.Parse(other)
		if cs := jar.Cookies(o); len(cs) != 0 {
			t.Fatalf("%s got the session too: %v", other, cs)
		}
	}

	// Someone the kit doesn't list: refused, no cookie.
	jar2, _ := cookiejar.New(nil)
	browser.Jar = jar2
	resp, err := browser.Get(origin + "/__berth/login?as=intruder%40acme.test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || len(jar2.Cookies(u)) != 0 {
		t.Fatalf("an unlisted user: %d, cookies %v", resp.StatusCode, jar2.Cookies(u))
	}
	// A link another site sent: refused before the box hears of it.
	req, _ := http.NewRequest(http.MethodGet, origin+"/__berth/login?as=pro%40acme.test", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Referer", "https://evil.example/")
	resp, err = browser.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	mu.Lock()
	n := len(logins)
	mu.Unlock()
	if resp.StatusCode != http.StatusForbidden || n != 3 {
		t.Fatalf("a cross-site login: %d, box logins %d", resp.StatusCode, n)
	}
}

// An unpaired computer reaches no box route, the login included: the box
// API's pairing is what stands between a stranger and a session.
func TestOnlyAPairedLaptopCanAskABoxToLogIn(t *testing.T) {
	var called bool
	b := newBoxWith(t, func(s *wire.Server) {
		s.Handle("POST /v1/worktrees/{loc}/{wt}/login", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))
	})
	// A plain HTTPS client with no paired identity: the handshake fails.
	c := &http.Client{Timeout: 5 * time.Second}
	if resp, err := c.Post("https://"+b.address+"/v1/worktrees/shop/fix-x/login", "application/json", strings.NewReader(`{"email":"pro@acme.test"}`)); err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("an unpaired client logged in")
		}
	}
	if resp, err := c.Post("http://"+b.address+"/v1/worktrees/shop/fix-x/login", "application/json", strings.NewReader(`{"email":"pro@acme.test"}`)); err == nil {
		resp.Body.Close()
	}
	if called {
		t.Fatal("the box ran a login for an unpaired client")
	}
}

func fetch(t *testing.T, c *http.Client, u string) string {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d %s", u, resp.StatusCode, body)
	}
	return string(body)
}
