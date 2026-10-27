package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const session = "s3cr3t-session-0f-acme"

func TestSafeNextKeepsLoginsOnTheWorktree(t *testing.T) {
	for _, ok := range []string{"/", "/settings", "/event-types?tab=2#top", "/a%20b", "/a/b/c", "/x:y", "/@me", "/a%2Fb"} {
		if got := SafeNext(ok); got != ok {
			t.Errorf("SafeNext(%q) = %q, want it kept", ok, got)
		}
	}
	for _, bad := range []string{
		"", "//evil.example", "///evil.example", "https://evil.example", "http:evil.example", "javascript:alert(1)",
		`/\evil.example`, `\\evil.example`, `\/evil.example`, "/%2F/evil.example", "/%2f%2fevil.example", "%2F%2Fevil.example",
		"/%5Cevil.example", "/%5cevil.example", "/%252F%252Fevil.example", "/%25252F%25252Fevil.example", "/%2525252F%2525252Fevil",
		"/\t/evil.example", "/\n/evil.example", "/ /evil.example", "/%09/evil.example", "/%0a/evil.example", "/%00",
		"/x/https://evil.example", "evil.example", "./x", "?next=/x", "#/x", "/%zz", "/" + strings.Repeat("a", 3000),
	} {
		if got := SafeNext(bad); got != "/" {
			t.Errorf("SafeNext(%q) = %q, want /", bad, got)
		}
	}
}

func TestLoginURL(t *testing.T) {
	got := LoginURL("http://fix-x.shop.devl.localhost:1377/", "pro@acme.test", "/event-types?x=1")
	want := "http://fix-x.shop.devl.localhost:1377/__berth/login?as=pro%40acme.test&next=%2Fevent-types%3Fx%3D1"
	if got != want {
		t.Fatalf("LoginURL = %s\nwant %s", got, want)
	}
	if got := LoginURL("http://shop.devl.localhost:1377", "pro@acme.test", "//evil"); !strings.HasSuffix(got, "next=%2F") {
		t.Fatalf("an offsite next went into a login URL: %s", got)
	}
}

// loginRig is a proxy with a login and a worktree dev server that counts
// what reaches it.
type loginRig struct {
	srv     *httptest.Server
	mu      sync.Mutex
	logins  [][]string
	emails  []string
	upstrm  int
	result  LoginResult
	err     error
	proxy   *Proxy
	devPort int
}

func newLoginRig(t *testing.T) *loginRig {
	rig := &loginRig{result: LoginResult{Cookies: []LoginCookie{{Name: "session", Value: session, HTTPOnly: true, SameSite: "Lax"}, {Name: "theme", Value: "dark", Path: "/app"}}, Redirect: "/dashboard"}}
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rig.mu.Lock()
		rig.upstrm++
		rig.mu.Unlock()
		io.WriteString(w, "dev server")
	}))
	t.Cleanup(dev.Close)
	rig.devPort, _ = strconv.Atoi(dev.URL[strings.LastIndex(dev.URL, ":")+1:])
	dial := func(ctx context.Context, port int) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(port))
	}
	rig.proxy = &Proxy{
		Dialer:   func(box string) (DialFunc, bool) { return dial, box == "devl" },
		Worktree: func(labels []string) (string, int, bool) { return "devl", rig.devPort, true },
		Route: func(host string) (string, int, bool) {
			return "devl", rig.devPort, strings.HasSuffix(host, ".routed.localhost")
		},
		Login: func(ctx context.Context, labels []string, email string) (LoginResult, error) {
			rig.mu.Lock()
			defer rig.mu.Unlock()
			rig.logins = append(rig.logins, labels)
			rig.emails = append(rig.emails, email)
			return rig.result, rig.err
		},
	}
	rig.srv = httptest.NewServer(rig.proxy)
	t.Cleanup(rig.srv.Close)
	return rig
}

func (rig *loginRig) get(t *testing.T, method, host, path string, header http.Header) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, rig.srv.URL+path, nil)
	req.Host = host
	for k, v := range header {
		req.Header[k] = v
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

const wtHost = "fix-x.shop.devl.localhost:1377"

func TestLoginRouteSetsCookiesForThatHostOnlyAndRedirects(t *testing.T) {
	rig := newLoginRig(t)
	resp := rig.get(t, "GET", wtHost, "/__berth/login?as=pro%40acme.test&next=%2Fevent-types%3Ftab%3D2", nil)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/event-types?tab=2" {
		t.Fatalf("%d Location %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if len(rig.logins) != 1 || strings.Join(rig.logins[0], ".") != "fix-x.shop.devl" || rig.emails[0] != "pro@acme.test" {
		t.Fatalf("login ran for %v as %v", rig.logins, rig.emails)
	}
	set := resp.Header.Values("Set-Cookie")
	if len(set) != 2 {
		t.Fatalf("Set-Cookie = %q", set)
	}
	for _, c := range set {
		if strings.Contains(strings.ToLower(c), "domain=") {
			t.Fatalf("a cookie with a Domain, for more than this host: %q", c)
		}
	}
	if !strings.HasPrefix(set[0], "session="+session+"; Path=/; HttpOnly; SameSite=Lax") || !strings.HasPrefix(set[1], "theme=dark; Path=/app") {
		t.Fatalf("Set-Cookie = %q", set)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("a login answer may be cached")
	}
	if rig.upstrm != 0 {
		t.Fatal("the login route reached the dev server")
	}
	// The host's Preview frames get the session too.
	jar := rig.proxy.preview.jar("fix-x.shop.devl.localhost")
	if cs := jar.Cookies(jarURL("fix-x.shop.devl.localhost", "/")); len(cs) != 1 || cs[0].Value != session {
		t.Fatalf("preview jar = %v", cs)
	}
	if cs := rig.proxy.preview.jar("other.shop.devl.localhost").Cookies(jarURL("other.shop.devl.localhost", "/")); len(cs) != 0 {
		t.Fatal("another host's jar got the session")
	}
	// No next: the login's own redirect.
	resp = rig.get(t, "GET", wtHost, "/__berth/login?as=pro%40acme.test", nil)
	if resp.Header.Get("Location") != "/dashboard" {
		t.Fatalf("Location %q, want the script's /dashboard", resp.Header.Get("Location"))
	}
	// The cookies go to the host that asked, and the login runs for it.
	resp = rig.get(t, "GET", "other.shop.devl.localhost:1377", "/__berth/login?as=pro%40acme.test", nil)
	if strings.Join(rig.logins[2], ".") != "other.shop.devl" {
		t.Fatalf("logged in %v for other.shop.devl", rig.logins[2])
	}
}

func TestLoginRouteNextNeverLeadsOffTheWorktree(t *testing.T) {
	rig := newLoginRig(t)
	for _, next := range []string{"//evil.example", "https://evil.example", `/\evil.example`, "/%2F%2Fevil.example", "%2F%2Fevil.example", "/%5Cevil.example", "/%252F%252Fevil.example", "/%09/evil.example", "http:evil.example"} {
		resp := rig.get(t, "GET", wtHost, "/__berth/login?as=pro%40acme.test&next="+url.QueryEscape(next), nil)
		if loc := resp.Header.Get("Location"); loc != "/" {
			t.Errorf("next=%q led to %q", next, loc)
		}
	}
	// Nor does a login script's redirect.
	rig.result.Redirect = "//evil.example"
	resp := rig.get(t, "GET", wtHost, "/__berth/login?as=pro%40acme.test", nil)
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("the script's redirect led to %q", loc)
	}
}

func TestLoginRouteRefusesWhatAnotherPageStarted(t *testing.T) {
	rig := newLoginRig(t)
	refused := map[string]http.Header{
		"cross-site":             {"Sec-Fetch-Site": {"cross-site"}},
		"same-site":              {"Sec-Fetch-Site": {"same-site"}},
		"another origin":         {"Origin": {"https://evil.example"}},
		"null origin":            {"Origin": {"null"}},
		"another worktree":       {"Origin": {"http://other.shop.devl.localhost:1377"}},
		"another port":           {"Referer": {"http://fix-x.shop.devl.localhost:9999/"}},
		"another referer":        {"Referer": {"https://evil.example/page"}},
		"lying fetch site":       {"Sec-Fetch-Site": {"same-origin"}, "Referer": {"https://evil.example/"}},
		"subdomain lookalike":    {"Referer": {"http://fix-x.shop.devl.localhost.evil.example:1377/"}},
		"referer without a host": {"Referer": {"/relative"}},
	}
	for name, h := range refused {
		resp := rig.get(t, "GET", wtHost, "/__berth/login?as=pro%40acme.test", h)
		if resp.StatusCode != http.StatusForbidden || len(resp.Header.Values("Set-Cookie")) != 0 {
			t.Errorf("%s: %d, cookies %q", name, resp.StatusCode, resp.Header.Values("Set-Cookie"))
		}
	}
	if len(rig.logins) != 0 {
		t.Fatalf("a refused request ran the login %d times", len(rig.logins))
	}
	for name, h := range map[string]http.Header{
		"typed":        {"Sec-Fetch-Site": {"none"}},
		"its own page": {"Sec-Fetch-Site": {"same-origin"}, "Referer": {"http://" + wtHost + "/settings"}, "Origin": {"http://" + wtHost}},
		"no headers":   nil,
	} {
		if resp := rig.get(t, "GET", wtHost, "/__berth/login?as=pro%40acme.test", h); resp.StatusCode != http.StatusFound {
			t.Errorf("%s: %d", name, resp.StatusCode)
		}
	}
}

func TestLoginRouteTakesGetFromThisMachineAndIsNeverForwarded(t *testing.T) {
	rig := newLoginRig(t)
	for _, m := range []string{"POST", "PUT", "DELETE", "HEAD"} {
		if resp := rig.get(t, m, wtHost, "/__berth/login?as=pro%40acme.test", nil); resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", m, resp.StatusCode)
		}
	}
	// However the path is spelled, and on a host a router handles, the
	// route is the proxy's: the dev server never sees it.
	for _, p := range []string{"/__berth/login/", "//__berth/login", "/__BERTH/Login", "/__berth//login", "/__berth/login/x", "/x/../__berth/login", "/__berth/%6Cogin"} {
		rig.get(t, "GET", wtHost, p+"?as=pro%40acme.test", nil)
		rig.get(t, "GET", "app.routed.localhost:1377", p+"?as=pro%40acme.test", nil)
	}
	if rig.upstrm != 0 {
		t.Fatalf("the login route reached the dev server %d times", rig.upstrm)
	}
	if rig.get(t, "GET", wtHost, "/__berth/loginx", nil); rig.upstrm != 1 {
		t.Fatal("another path didn't reach the dev server")
	}
	// From another machine: refused before anything runs.
	req := httptest.NewRequest("GET", "http://"+wtHost+"/__berth/login?as=pro%40acme.test", nil)
	req.RemoteAddr = "100.64.0.7:51234"
	w := httptest.NewRecorder()
	n := len(rig.logins)
	rig.proxy.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || len(rig.logins) != n || len(w.Result().Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("a request from another machine: %d", w.Code)
	}
	// Not a worktree's host.
	if resp := rig.get(t, "GET", "localhost:1377", "/__berth/login?as=pro%40acme.test", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("plain localhost: %d", resp.StatusCode)
	}
}

func TestLoginRouteFailuresSetNoCookieAndShowNoSession(t *testing.T) {
	rig := newLoginRig(t)
	check := func(name string, wantStatus int) {
		t.Helper()
		resp := rig.get(t, "GET", wtHost, "/__berth/login?as=pro%40acme.test", nil)
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != wantStatus || len(resp.Header.Values("Set-Cookie")) != 0 || strings.Contains(string(body), session) {
			t.Errorf("%s: %d, cookies %q, body leaks: %v", name, resp.StatusCode, resp.Header.Values("Set-Cookie"), strings.Contains(string(body), session))
		}
	}
	rig.err = &LoginError{Status: http.StatusForbidden, Msg: "pro@acme.test is not one of shop's login users"}
	check("refused", http.StatusForbidden)
	rig.err = errors.New("the box went away")
	check("box error", http.StatusBadGateway)
	rig.err = nil
	// One bad cookie and none is set, not even the good one before it.
	rig.result = LoginResult{Cookies: []LoginCookie{{Name: "session", Value: session}, {Name: "x", Value: session + "; Domain=evil.example"}}}
	check("bad cookie", http.StatusBadGateway)
	rig.result = LoginResult{}
	check("no cookies", http.StatusBadGateway)
	// The Network drawer's log has the request, never a cookie.
	reqs, _ := rig.proxy.Requests("fix-x.shop.devl.localhost", 0)
	for _, r := range reqs {
		if strings.Contains(r.Path+r.Body+r.Error, session) {
			t.Fatalf("the request log has the session: %+v", r)
		}
	}
	if len(reqs) == 0 {
		t.Fatal("logins aren't in the request log")
	}
}

func TestLoginCookieCheck(t *testing.T) {
	age := 3600
	if err := (LoginCookie{Name: "__Host-s", Value: "abc", Path: "/", MaxAge: &age, SameSite: "None", Secure: true}).Check(); err != nil {
		t.Fatal(err)
	}
	zero, huge := 0, maxCookieMaxAge+1
	for name, c := range map[string]LoginCookie{
		"empty name":     {Value: "v4lue"},
		"name with =":    {Name: "a=b", Value: "v4lue"},
		"semicolon":      {Name: "a", Value: "x;Domain=evil"},
		"newline":        {Name: "a", Value: "x\nSet-Cookie: b=c"},
		"space":          {Name: "a", Value: "x y"},
		"relative path":  {Name: "a", Value: "v4lue", Path: "app"},
		"path injection": {Name: "a", Value: "v4lue", Path: "/;Domain=evil"},
		"zero max age":   {Name: "a", Value: "v4lue", MaxAge: &zero},
		"huge max age":   {Name: "a", Value: "v4lue", MaxAge: &huge},
		"samesite":       {Name: "a", Value: "v4lue", SameSite: "sometimes"},
		"host path":      {Name: "__Host-a", Value: "v4lue", Path: "/app"},
		"long value":     {Name: "a", Value: strings.Repeat("v", maxCookieValue+1)},
	} {
		err := c.Check()
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if c.Value != "" && strings.Contains(err.Error(), c.Value) {
			t.Errorf("%s: the error quotes the value", name)
		}
	}
}
