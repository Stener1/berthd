package box

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/events"
)

// The secret every fake login script prints as its session: it must never
// show up in an error, a log or an event.
const loginSecret = "s3cr3t-session-0f-acme"

// loginBox is a box with the project "shop", a worktree "fix-x", and a kit
// whose login is script (kit-relative) with users, carrying files.
func loginBox(t *testing.T, login *LoginConfig, files map[string]string) (*Box, Worktree) {
	t.Helper()
	ctx := context.Background()
	repo := gitRepo(t)
	dir := t.TempDir()
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(dir, "locations.json")), Events: &events.Bus{}, KitsDir: filepath.Join(dir, "kits")}
	if _, err := b.Locations.Add(ctx, "shop", repo); err != nil {
		t.Fatal(err)
	}
	wt, err := b.Locations.CreateWorktree(ctx, "shop", "fix-x", "", "")
	if err != nil {
		t.Fatal(err)
	}
	enc := map[string]string{}
	for p, c := range files {
		enc[p] = base64.StdEncoding.EncodeToString([]byte(c))
	}
	if _, err := b.InstallKit(ctx, "shop", KitInstall{Kit: Kit{ID: "shop-dev", Name: "Shop", Config: RepoConfig{Login: login}}, Files: enc}); err != nil {
		t.Fatal(err)
	}
	return b, wt
}

// echoScript prints a session for $BERTH_LOGIN_EMAIL and notes how it ran.
const echoScript = `#!/bin/sh
printf '%s|%s|%s|%s\n' "$#" "$BERTH_LOGIN_EMAIL" "$BERTH_WORKTREE_NAME" "$PWD" > "$BERTH_KIT_DIR/ran"
printf '{"cookies":[{"name":"session","value":"` + loginSecret + `","httpOnly":true,"sameSite":"Lax"}],"redirect":"/dashboard"}\n'
`

var acmeUsers = []LoginUser{{Email: "pro@acme.test", Label: "Pro user"}, {Email: "admin@acme.test"}}

func kitDir(b *Box) string { return filepath.Join(b.KitsDir, "shop", "shop-dev") }

func ran(b *Box) string {
	out, _ := os.ReadFile(filepath.Join(kitDir(b), "ran"))
	return strings.TrimSpace(string(out))
}

func TestLoginEmails(t *testing.T) {
	for _, ok := range []string{"pro@acme.test", "Team.Admin+2@mail.acme.co", "a_b-c@x-y.example.com", "x@a.io"} {
		if !ValidLoginEmail(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{
		"", "pro", "pro@acme", "a@b.c; rm -rf /", "a@b.c\n", "a@b.c\r\nX: y", "$(id)@acme.test", "`id`@acme.test",
		"a b@acme.test", "a@b.c ", " a@b.c", "a\x00@acme.test", "a\t@acme.test", "'a'@acme.test", `"a"@acme.test`,
		".a@acme.test", "a.@acme.test", "a..b@acme.test", "a@-acme.test", "a@acme-.test", "a@acme..test",
		"a@b.c|x", "a@b.c&x", "a@b.c>x", "a@[127.0.0.1]", "ü@acme.test", "a@acme.test/x", "--help@acme.test",
		strings.Repeat("a", 65) + "@acme.test", "a@" + strings.Repeat("b", 250) + ".test",
	} {
		if ValidLoginEmail(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestLoginConfigReadsUsersAndRefusesBadOnes(t *testing.T) {
	var c LoginConfig
	if err := json.Unmarshal([]byte(`{"script":"scripts/login.sh","users":["pro@acme.test",{"email":"admin@acme.test","label":"Team admin"}]}`), &c); err != nil {
		t.Fatal(err)
	}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	if len(c.Users) != 2 || c.Users[0] != (LoginUser{Email: "pro@acme.test"}) || c.Users[1] != (LoginUser{Email: "admin@acme.test", Label: "Team admin"}) {
		t.Fatalf("users = %+v", c.Users)
	}
	for name, bad := range map[string]LoginConfig{
		"script escapes":      {Script: "../outside.sh", Users: acmeUsers},
		"script deep":         {Script: "scripts/../../outside.sh", Users: acmeUsers},
		"absolute script":     {Script: "/bin/sh", Users: acmeUsers},
		"no script":           {Users: acmeUsers},
		"no users":            {Script: "login.sh"},
		"injected email":      {Script: "login.sh", Users: []LoginUser{{Email: "a@b.c; rm -rf ~"}}},
		"newline in email":    {Script: "login.sh", Users: []LoginUser{{Email: "a@b.c\nb@c.d"}}},
		"twice":               {Script: "login.sh", Users: []LoginUser{{Email: "a@b.cd"}, {Email: "A@b.cd"}}},
		"label two lines":     {Script: "login.sh", Users: []LoginUser{{Email: "a@b.cd", Label: "x\ny"}}},
		"label much too long": {Script: "login.sh", Users: []LoginUser{{Email: "a@b.cd", Label: strings.Repeat("x", 49)}}},
	} {
		if err := bad.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := (&LoginConfig{Script: "login.sh", Any: true}).validate(); err != nil {
		t.Errorf("any with no users: %v", err)
	}
	// A kit carrying a script outside its folder is refused at install.
	ctx := context.Background()
	b := &Box{Locations: NewLocations(filepath.Join(t.TempDir(), "l.json")), KitsDir: t.TempDir()}
	b.Locations.Add(ctx, "shop", gitRepo(t))
	if _, err := b.InstallKit(ctx, "shop", KitInstall{Kit: Kit{ID: "k", Config: RepoConfig{Login: &LoginConfig{Script: "../../x.sh", Users: acmeUsers}}}}); err == nil {
		t.Error("a kit whose login script is outside it installed")
	}
	// login at the top of kit.json is config.login.
	var k Kit
	json.Unmarshal([]byte(`{"id":"k","login":{"script":"login.sh","users":["pro@acme.test"]},"config":{}}`), &k)
	if _, err := b.InstallKit(ctx, "shop", KitInstall{Kit: k}); err != nil {
		t.Fatal(err)
	}
	src, _ := b.Locations.loginFor("shop")
	if src == nil || src.from != "kit" || src.cfg.Users[0].Email != "pro@acme.test" {
		t.Fatalf("top-level kit login = %+v", src)
	}
}

func TestLoginRunsTheKitScriptWithTheEmailInItsEnvironmentOnly(t *testing.T) {
	b, wt := loginBox(t, &LoginConfig{Script: "scripts/login.sh", Users: acmeUsers}, map[string]string{"scripts/login.sh": echoScript})
	res, err := b.RunLogin(context.Background(), "shop", "fix-x", "PRO@acme.test")
	if err != nil {
		t.Fatal(err)
	}
	if res.Email != "pro@acme.test" || len(res.Cookies) != 1 || res.Cookies[0].Value != loginSecret || !res.Cookies[0].HTTPOnly || res.Redirect != "/dashboard" {
		t.Fatalf("login = %+v", res)
	}
	// No arguments: the email is BERTH_LOGIN_EMAIL, and it ran in the
	// worktree with the worktree's environment.
	want := "0|pro@acme.test|fix-x|" + wt.Path
	if got := ran(b); got != want {
		t.Fatalf("the script saw %q, want %q", got, want)
	}
}

func TestLoginRefusesWhoTheConfigDoesNotList(t *testing.T) {
	b, _ := loginBox(t, &LoginConfig{Script: "login.sh", Users: acmeUsers}, map[string]string{"login.sh": echoScript})
	ctx := context.Background()
	for _, email := range []string{"intruder@acme.test", "pro@acme.test.evil.example"} {
		_, err := b.RunLogin(ctx, "shop", "fix-x", email)
		if statusFor(err) != http.StatusForbidden {
			t.Errorf("%s: %v (status %d), want 403", email, err, statusFor(err))
		}
	}
	for _, email := range []string{"a@b.c; rm -rf /", "pro@acme.test\nx", "$(touch /tmp/x)@acme.test", "pro@acme.test --flag", "`id`@acme.test", ""} {
		_, err := b.RunLogin(ctx, "shop", "fix-x", email)
		if statusFor(err) != http.StatusBadRequest {
			t.Errorf("%q: %v, want 400", email, err)
		}
	}
	if got := ran(b); got != "" {
		t.Fatalf("the script ran for a refused email: %q", got)
	}
	// With any, a valid email that isn't listed may log in; an injected one
	// still may not.
	b2, _ := loginBox(t, &LoginConfig{Script: "login.sh", Any: true}, map[string]string{"login.sh": echoScript})
	if _, err := b2.RunLogin(ctx, "shop", "fix-x", "someone@acme.test"); err != nil {
		t.Fatalf("any: %v", err)
	}
	if _, err := b2.RunLogin(ctx, "shop", "fix-x", "a@b.c;id"); statusFor(err) != http.StatusBadRequest {
		t.Fatalf("any took an injected email: %v", err)
	}
	// An unknown worktree is not another worktree's login.
	if _, err := b.RunLogin(ctx, "shop", "nope", "pro@acme.test"); !errors.Is(err, ErrUnknownWorktree) {
		t.Fatalf("unknown worktree: %v", err)
	}
}

func TestLoginScriptCannotLeadOutsideItsFolder(t *testing.T) {
	b, _ := loginBox(t, &LoginConfig{Script: "scripts/login.sh", Users: acmeUsers}, map[string]string{"scripts/keep": "x"})
	outside := filepath.Join(t.TempDir(), "outside.sh")
	os.WriteFile(outside, []byte(echoScript), 0o755)
	if err := os.Symlink(outside, filepath.Join(kitDir(b), "scripts", "login.sh")); err != nil {
		t.Fatal(err)
	}
	_, err := b.RunLogin(context.Background(), "shop", "fix-x", "pro@acme.test")
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("a symlink out of the kit ran: %v", err)
	}
	// A symlinked folder on the way out is the same.
	os.Remove(filepath.Join(kitDir(b), "scripts", "login.sh"))
	os.RemoveAll(filepath.Join(kitDir(b), "scripts"))
	os.Symlink(filepath.Dir(outside), filepath.Join(kitDir(b), "scripts"))
	os.Rename(outside, filepath.Join(filepath.Dir(outside), "login.sh"))
	if _, err := b.RunLogin(context.Background(), "shop", "fix-x", "pro@acme.test"); err == nil {
		t.Fatal("a symlinked folder out of the kit ran")
	}
	if _, err := resolveLoginScript(kitDir(b), "../shop-dev/x"); err == nil {
		t.Fatal(".. resolved")
	}
}

func TestLoginScriptFailuresSayWhyWithoutTheSession(t *testing.T) {
	old := loginTimeout
	loginTimeout = 500 * time.Millisecond
	t.Cleanup(func() { loginTimeout = old })
	cookie := `{"name":"session","value":"` + loginSecret + `"}`
	for name, script := range map[string]string{
		"not json":         "#!/bin/sh\necho 'Set-Cookie: session=" + loginSecret + "'\n",
		"broken json":      "#!/bin/sh\necho '{\"cookies\":[" + cookie + "'\n",
		"two values":       "#!/bin/sh\necho '{\"cookies\":[" + cookie + "]} {}'\n",
		"oversize":         "#!/bin/sh\ni=0; while [ $i -lt 5000 ]; do printf '" + loginSecret + "%s' \"$i\"; i=$((i+1)); done\n",
		"timeout":          "#!/bin/sh\necho '{\"cookies\":[" + cookie + "]}'\nsleep 5\n",
		"exit status":      "#!/bin/sh\necho '{\"cookies\":[" + cookie + "]}'\necho " + loginSecret + " >&2\nexit 3\n",
		"domain":           "#!/bin/sh\necho '{\"cookies\":[{\"name\":\"session\",\"value\":\"" + loginSecret + "\",\"domain\":\"evil.example\"}]}'\n",
		"expires":          "#!/bin/sh\necho '{\"cookies\":[{\"name\":\"session\",\"value\":\"" + loginSecret + "\",\"expires\":\"x\"}]}'\n",
		"bad value":        "#!/bin/sh\necho '{\"cookies\":[{\"name\":\"session\",\"value\":\"" + loginSecret + "; Domain=evil.example\"}]}'\n",
		"bad name":         "#!/bin/sh\necho '{\"cookies\":[{\"name\":\"a b\",\"value\":\"" + loginSecret + "\"}]}'\n",
		"wrong type":       "#!/bin/sh\necho '{\"cookies\":[{\"name\":\"session\",\"value\":\"" + loginSecret + "\",\"httpOnly\":\"" + loginSecret + "\"}]}'\n",
		"no cookies":       "#!/bin/sh\necho '{\"cookies\":[]}'\n",
		"offsite redirect": "#!/bin/sh\necho '{\"cookies\":[" + cookie + "],\"redirect\":\"//evil.example/" + loginSecret + "\"}'\n",
		"scheme redirect":  "#!/bin/sh\necho '{\"cookies\":[" + cookie + "],\"redirect\":\"https://evil.example\"}'\n",
		"bad samesite":     "#!/bin/sh\necho '{\"cookies\":[{\"name\":\"session\",\"value\":\"" + loginSecret + "\",\"sameSite\":\"" + loginSecret + "\"}]}'\n",
	} {
		t.Run(name, func(t *testing.T) {
			b, _ := loginBox(t, &LoginConfig{Script: "login.sh", Users: acmeUsers}, map[string]string{"login.sh": script})
			var logs bytes.Buffer
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })
			ch, stop := b.Events.Subscribe()
			defer stop()
			srv := loginServer(b)
			defer srv.Close()
			resp, err := http.Post(srv.URL+"/v1/worktrees/shop/fix-x/login", "application/json", strings.NewReader(`{"email":"pro@acme.test"}`))
			if err != nil {
				t.Fatal(err)
			}
			var body bytes.Buffer
			body.ReadFrom(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				t.Fatalf("a broken login answered 200: %s", body.String())
			}
			if strings.Contains(body.String(), loginSecret) || strings.Contains(body.String(), "cookies\":[{") {
				t.Fatalf("the error leaks the session: %s", body.String())
			}
			e := <-ch
			raw, _ := json.Marshal(e)
			if e.Type != "worktree.login" || e.Data["ok"] != false || e.Data["email"] != "pro@acme.test" || e.Data["worktree"] != "fix-x" {
				t.Fatalf("event = %s", raw)
			}
			if strings.Contains(string(raw), loginSecret) || strings.Contains(logs.String(), loginSecret) {
				t.Fatalf("the session leaked: event %s, logs %q", raw, logs.String())
			}
			t.Logf("%d %s", resp.StatusCode, strings.TrimSpace(body.String()))
		})
	}
}

func loginServer(b *Box) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/worktrees/{loc}/{wt}/login", b.HandleLogin)
	return httptest.NewServer(mux)
}

func TestLoginOverTheAPIJournalsWhoNotTheSession(t *testing.T) {
	b, _ := loginBox(t, &LoginConfig{Script: "login.sh", Users: acmeUsers}, map[string]string{"login.sh": echoScript})
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	ch, stop := b.Events.Subscribe()
	defer stop()
	srv := loginServer(b)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/v1/worktrees/shop/fix-x/login", "application/json", strings.NewReader(`{"email":"pro@acme.test"}`))
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Email   string `json:"email"`
		Cookies []struct{ Name, Value string }
	}
	json.NewDecoder(resp.Body).Decode(&res)
	resp.Body.Close()
	if resp.StatusCode != 200 || res.Email != "pro@acme.test" || len(res.Cookies) != 1 || res.Cookies[0].Value != loginSecret {
		t.Fatalf("%d %+v", resp.StatusCode, res)
	}
	e := <-ch
	raw, _ := json.Marshal(e)
	if e.Type != "worktree.login" || e.Data["ok"] != true || e.Data["email"] != "pro@acme.test" || e.Data["location"] != "shop" || e.Data["worktree"] != "fix-x" {
		t.Fatalf("event = %s", raw)
	}
	if strings.Contains(string(raw), loginSecret) || strings.Contains(logs.String(), loginSecret) {
		t.Fatalf("the session reached the journal or a log: %s %q", raw, logs.String())
	}
	// A refused email is journaled as refused, an injected one without it.
	resp, _ = http.Post(srv.URL+"/v1/worktrees/shop/fix-x/login", "application/json", strings.NewReader(`{"email":"x@y.z\n; rm -rf /"}`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("injected email: %d", resp.StatusCode)
	}
	if e := <-ch; e.Data["ok"] != false || e.Data["email"] != "" {
		t.Fatalf("event for an injected email = %+v", e.Data)
	}
}

// writeLoginRepoConfig commits nothing: the main checkout's file is what a
// default branch has.
func writeLoginRepoConfig(t *testing.T, dir string, c RepoConfig) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, ".berth"), 0o755)
	raw, _ := json.Marshal(c)
	if err := os.WriteFile(filepath.Join(dir, RepoConfigFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoginComesOnlyFromTrustedConfigNeverAPullRequest(t *testing.T) {
	ctx := context.Background()
	repo := gitRepo(t)
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(t.TempDir(), "locations.json")), Events: &events.Bus{}}
	b.Locations.Add(ctx, "shop", repo)
	wt, err := b.Locations.CreateWorktree(ctx, "shop", "pr-42", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// The pull request's checkout brings a login and a user of its own.
	writeLoginRepoConfig(t, wt.Path, RepoConfig{Login: &LoginConfig{Script: "login.sh", Users: []LoginUser{{Email: "evil@acme.test"}}, Any: true}})
	os.WriteFile(filepath.Join(wt.Path, ".berth", "login.sh"), []byte(echoScript), 0o755)
	if _, err := b.RunLogin(ctx, "shop", "pr-42", "evil@acme.test"); statusFor(err) != http.StatusNotFound {
		t.Fatalf("a PR head's login applied: %v", err)
	}

	// The default branch's config, not yet trusted: shown, never run.
	writeLoginRepoConfig(t, repo, RepoConfig{Login: &LoginConfig{Script: "login.sh", Users: []LoginUser{{Email: "pro@acme.test"}}}})
	os.WriteFile(filepath.Join(repo, ".berth", "login.sh"), []byte(strings.ReplaceAll(echoScript, "$BERTH_KIT_DIR", "$BERTH_ROOT_PATH/.berth")), 0o755)
	if _, err := b.RunLogin(ctx, "shop", "pr-42", "pro@acme.test"); statusFor(err) != http.StatusNotFound {
		t.Fatalf("an untrusted repo config's login applied: %v", err)
	}
	cfg, _ := b.Locations.Config(ctx, "shop")
	if err := b.Locations.TrustRepo("shop", cfg.RepoTrust.Hash); err != nil {
		t.Fatal(err)
	}
	// Trusted: the main checkout's users and script, whatever the PR says.
	if _, err := b.RunLogin(ctx, "shop", "pr-42", "pro@acme.test"); err != nil {
		t.Fatalf("trusted default-branch login: %v", err)
	}
	if _, err := b.RunLogin(ctx, "shop", "pr-42", "evil@acme.test"); statusFor(err) != http.StatusForbidden {
		t.Fatalf("the PR's user logged in: %v", err)
	}
	// It ran the main checkout's script, in the PR's worktree.
	out, _ := os.ReadFile(filepath.Join(repo, ".berth", "ran"))
	if !strings.Contains(string(out), "|pro@acme.test|pr-42|"+wt.Path) {
		t.Fatalf("ran = %q", out)
	}
}

func TestLoginOutputParsing(t *testing.T) {
	res, err := parseLoginOutput([]byte(`{"cookies":[{"name":"a","value":"b","path":"/app","secure":true,"sameSite":"strict","maxAge":3600}]}` + "\n"))
	if err != nil || len(res.Cookies) != 1 || res.Cookies[0].Path != "/app" || *res.Cookies[0].MaxAge != 3600 {
		t.Fatalf("%+v %v", res, err)
	}
	for _, bad := range []string{
		`{"cookies":[{"name":"a","value":"b","domain":"x.localhost"}]}`,
		`{"cookies":[{"name":"a","value":"b","Domain":"x.localhost"}]}`,
		`{"cookies":[{"name":"a","value":"b","path":"x"}]}`,
		`{"cookies":[{"name":"a","value":"b","maxAge":-1}]}`,
		`{"cookies":[{"name":"a","value":"b"},{"name":"a","value":"c"}]}`,
		`{"cookies":[{"name":"a","value":"b"}],"redirect":"/\\evil"}`,
		`{"cookies":[{"name":"a","value":"b"}],"redirect":""}`,
		`{"cookies":[{"name":"a","value":"b"}],"extra":1}`,
		`[]`, `null`, ``,
	} {
		if _, err := parseLoginOutput([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

// sessionPage says who a request's session cookie is, per side.
func sessionPage(side string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		who := "nobody"
		if c, err := r.Cookie("session"); err == nil {
			who = c.Value
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<!doctype html><title>"+side+"</title><h1>"+side+" "+r.URL.Path+": signed in as "+who+"</h1>")
	}
}

// sideScript logs in by printing a session named after the worktree, so
// a test can tell which side's login ran for which host.
const sideScript = `#!/bin/sh
printf '{"cookies":[{"name":"session","value":"%s-%s"}],"redirect":"/home"}\n' "$BERTH_WORKTREE_NAME" "$(echo "$BERTH_LOGIN_EMAIL" | cut -d@ -f1)"
`

func installLoginKit(t *testing.T, b *Box, location string) {
	t.Helper()
	b.KitsDir = t.TempDir()
	enc := base64.StdEncoding.EncodeToString([]byte(sideScript))
	if _, err := b.InstallKit(context.Background(), location, KitInstall{Kit: Kit{ID: "k", Config: RepoConfig{Login: &LoginConfig{Script: "login.sh", Users: acmeUsers}}}, Files: map[string]string{"login.sh": enc}}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentBrowserOpensLoggedIn(t *testing.T) {
	if testing.Short() {
		t.Skip("starts Chromium")
	}
	if os.Getenv("BERTH_TEST_CHROMIUM") == "" && os.Getenv("CI") != "" {
		t.Skip("set BERTH_TEST_CHROMIUM=1 to start a real Chromium in CI")
	}
	if _, err := FindChromium(); err != nil {
		t.Skip(err)
	}
	b, _, _ := browserBox(t, sessionPage("head"))
	installLoginKit(t, b, "cal")
	m := b.NewBrowsers(t.TempDir(), 2)
	defer m.CloseAll("test")
	mux := http.NewServeMux()
	b.mountBrowser(func(p string, h func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			if err := h(w, r); err != nil {
				writeErr(w, err)
			}
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	open := func(body string) (int, string) {
		resp, err := http.Post(srv.URL+"/v1/worktrees/cal/billing/browser/open", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct{ Text, Error string }
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out.Text + out.Error
	}
	if code, text := open(`{"as":"pro@acme.test","url":"/settings"}`); code != 200 || !strings.Contains(text, "head /settings: signed in as billing-pro") {
		if errors.Is(fmt.Errorf("%s", text), ErrBrowserSandbox) || strings.Contains(text, "sandbox") {
			t.Skip(text)
		}
		t.Fatalf("%d %s", code, text)
	}
	// No path: where the login says.
	if code, text := open(`{"as":"admin@acme.test"}`); code != 200 || !strings.Contains(text, "head /home: signed in as billing-admin") {
		t.Fatalf("%d %s", code, text)
	}
	// Someone the kit doesn't list: refused, and the session stays.
	if code, text := open(`{"as":"intruder@acme.test"}`); code != http.StatusForbidden {
		t.Fatalf("%d %s", code, text)
	}
}

func TestShotsCompareLogsEachSideInWithItsOwnLogin(t *testing.T) {
	if os.Getenv("BERTH_TEST_SHOTS") == "" {
		t.Skip("set BERTH_TEST_SHOTS=1 to shoot pages with a real Chromium")
	}
	if _, err := FindChromium(); err != nil {
		t.Skip(err)
	}
	var mu sync.Mutex
	seen := map[string]string{}
	record := func(side string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie("session"); err == nil {
				mu.Lock()
				seen[side] = c.Value
				mu.Unlock()
			}
			sessionPage(side)(w, r)
		}
	}
	b, wt, _ := shotsBox(t, record("head"))
	installLoginKit(t, b, "cal")
	b.Artifacts = &ArtifactStore{Dir: t.TempDir()}
	b.NewBrowsers(t.TempDir(), 2)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	loc, _ := b.Locations.Get(ctx, "cal")
	var main Worktree
	for _, w := range loc.Worktrees {
		if w.Main {
			main = w
		}
	}
	mport, _ := b.Locations.Ports.For(main.Path)
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(mport))
	if err != nil {
		t.Skipf("main's port %d is taken: %v", mport, err)
	}
	srv := &http.Server{Handler: record("main")}
	go srv.Serve(ln)
	defer srv.Close()
	res, err := b.ShotsCompare(ctx, "cal", "billing", ShotsRequest{Pages: []string{"/"}, Sizes: []int{375}, As: "pro@acme.test"})
	if err != nil {
		t.Fatal(err)
	}
	// Each side was logged in by its own login, on its own host.
	if seen["head"] != "billing-pro" || seen["main"] != main.Name+"-pro" {
		t.Fatalf("sessions seen: %v", seen)
	}
	if !strings.Contains(res.Text, "logged in as pro@acme.test") {
		t.Fatalf("text:\n%s", res.Text)
	}
	vd, _, _, err := b.latestDiff(wt, res.Artifact)
	if err != nil || vd.Settings.Login != "pro@acme.test" {
		t.Fatalf("settings: %+v %v", vd.Settings, err)
	}
}
