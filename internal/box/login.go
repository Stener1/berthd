package box

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cosscom/shipyard/internal/agentpath"
	"github.com/cosscom/shipyard/internal/events"
	"github.com/cosscom/shipyard/internal/proxy"
)

// Logging a worktree in as a dev user. A project's kit, or its config,
// names a login script and the users it may log in:
//
//	"login": {
//	  "script": "scripts/login.sh",
//	  "users": ["pro@acme.test", {"email": "admin@acme.test", "label": "Team admin"}],
//	  "any": false
//	}
//
// The script runs in the worktree with the worktree's environment
// ($BERTH_PORT, $BERTH_URL, its database) and BERTH_LOGIN_EMAIL, against the
// worktree's own dev server, and prints the session on stdout:
//
//	{"cookies": [{"name": "session", "value": "…", "httpOnly": true}], "redirect": "/dashboard"}
//
// The laptop's proxy sets those cookies for the worktree's private host
// (proxy/login.go); the agent browser and `berthd shots compare --as` set
// them in their Chromium.
//
// Where a login comes from is the same trust as anything else that runs:
// the kit (the user's own), the box's own config, or the repository's
// committed config in the main checkout once trusted. Never a worktree's
// checkout, so a pull request can't add a user or change the script.
// The email reaches the script as an environment variable only, never on
// a command line, and it must be a plain address. Cookie values never
// reach a log, an event or an error.

// LoginConfig is a project's "login".
type LoginConfig struct {
	// Script is a path inside the config's folder: the kit's own folder
	// for a kit, the main checkout's .berth folder otherwise.
	Script string      `json:"script"`
	Users  []LoginUser `json:"users,omitempty"`
	// Any lets a login name any valid email, not only Users.
	Any bool `json:"any,omitempty"`
}

// LoginUser is a user a project lists: an email, and a label for people.
type LoginUser struct {
	Email string `json:"email"`
	Label string `json:"label,omitempty"`
}

// UnmarshalJSON takes "pro@acme.test" or {"email": …, "label": …}.
func (u *LoginUser) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*u = LoginUser{Email: s}
		return nil
	}
	var o struct {
		Email string `json:"email"`
		Label string `json:"label"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return errors.New(`a login user is an email or {"email": …, "label": …}`)
	}
	*u = LoginUser{Email: o.Email, Label: o.Label}
	return nil
}

const (
	maxLoginUsers  = 50
	maxEmailLength = 254
	maxLoginLabel  = 48
	maxLoginOutput = 64 << 10
)

// loginTimeout bounds a login script; tests shorten it.
var loginTimeout = 30 * time.Second

// emailPattern is a conservative subset of RFC 5321 addresses: a local
// part of letters, digits and . _ + - that starts with a letter or digit
// (so it never reads as a flag), with no dot at its end or two in a row,
// and a domain of at least two DNS labels.
var emailPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_+-]*(\.[A-Za-z0-9_+-]+)*@[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`)

// ValidLoginEmail reports whether s is an email a login takes.
func ValidLoginEmail(s string) bool {
	if len(s) == 0 || len(s) > maxEmailLength {
		return false
	}
	local, _, ok := strings.Cut(s, "@")
	if !ok || len(local) > 64 {
		return false
	}
	return emailPattern.MatchString(s)
}

func (c *LoginConfig) validate() error {
	if _, err := cleanKitPath(c.Script); err != nil || strings.TrimSpace(c.Script) == "" || filepath.IsAbs(c.Script) {
		return fmt.Errorf("login.script must be a path inside the config's folder, such as scripts/login.sh")
	}
	if len(c.Users) > maxLoginUsers {
		return fmt.Errorf("login lists at most %d users", maxLoginUsers)
	}
	if len(c.Users) == 0 && !c.Any {
		return errors.New("login has no users: list some, or set \"any\": true")
	}
	seen := map[string]bool{}
	for _, u := range c.Users {
		if !ValidLoginEmail(u.Email) {
			return fmt.Errorf("login user %q is not a plain email address", clip(u.Email, 80))
		}
		k := strings.ToLower(u.Email)
		if seen[k] {
			return fmt.Errorf("login lists %s twice", u.Email)
		}
		seen[k] = true
		if len([]rune(u.Label)) > maxLoginLabel || strings.ContainsAny(u.Label, "\r\n\t") {
			return fmt.Errorf("login user %s: label must be one line of at most %d characters", u.Email, maxLoginLabel)
		}
	}
	return nil
}

// allows reports whether the config lets email log in, and the email as
// the config spells it.
func (c *LoginConfig) allows(email string) (string, bool) {
	for _, u := range c.Users {
		if strings.EqualFold(u.Email, email) {
			return u.Email, true
		}
	}
	return email, c.Any
}

// loginSource is the login a location runs, and the folder its script is
// in.
type loginSource struct {
	cfg  LoginConfig
	dir  string
	from string // repo, kit or box
}

// loginFor is a location's login, from its trusted layers alone: the
// repository's committed config in the main checkout (when trusted), its
// kit, then the box's own config. The last that sets one wins, whole.
func (l *Locations) loginFor(name string) (*loginSource, error) {
	saved, err := l.saved(name)
	if err != nil {
		return nil, err
	}
	var src *loginSource
	repo, _, err := repoLayer(saved)
	if err == nil && repo.Login != nil {
		src = &loginSource{cfg: *repo.Login, dir: filepath.Join(saved.Path, filepath.Dir(RepoConfigFile)), from: "repo"}
	}
	if saved.Kit != nil && saved.Kit.Config.Login != nil {
		src = &loginSource{cfg: *saved.Kit.Config.Login, dir: saved.Kit.Dir, from: "kit"}
	}
	if saved.Config != nil && saved.Config.Login != nil {
		src = &loginSource{cfg: *saved.Config.Login, dir: filepath.Join(saved.Path, filepath.Dir(RepoConfigFile)), from: "box"}
	}
	return src, nil
}

// resolveLoginScript is the script's real path, which must be a file
// inside dir once every symlink is followed.
func resolveLoginScript(dir, script string) (string, error) {
	rel, err := cleanKitPath(script)
	if err != nil || filepath.IsAbs(script) {
		return "", errors.New("the login script must be inside its config's folder")
	}
	base, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("the login script's folder %s is missing", dir)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(base, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("there is no login script at %s", filepath.Join(dir, filepath.FromSlash(rel)))
	}
	if r, err := filepath.Rel(base, real); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return "", errors.New("the login script leads outside its config's folder")
	}
	st, err := os.Stat(real)
	if err != nil || !st.Mode().IsRegular() {
		return "", fmt.Errorf("the login script %s is not a file", rel)
	}
	return real, nil
}

// loginPATH is the box user's login shell's PATH, so a script finds the
// tools a terminal would (node, psql); tests replace it.
var loginPATH = func(ctx context.Context) string {
	loginPathCache.Lock()
	defer loginPathCache.Unlock()
	if loginPathCache.at.IsZero() || time.Since(loginPathCache.at) > 5*time.Minute {
		ans, _ := agentpath.AskShell(ctx, loginShell(), nil, os.Environ(), 10*time.Second)
		loginPathCache.path, loginPathCache.at = ans.PATH, time.Now()
	}
	return loginPathCache.path
}

var loginPathCache struct {
	sync.Mutex
	path string
	at   time.Time
}

// RunLogin runs a worktree's login for email and returns the cookies it
// printed, checked. Its errors never hold the script's output.
func (b *Box) RunLogin(ctx context.Context, location, worktree, email string) (proxy.LoginResult, error) {
	if !ValidLoginEmail(email) {
		return proxy.LoginResult{}, badRequest("%q is not a plain email address", clip(strings.ToValidUTF8(email, "?"), 80))
	}
	loc, err := b.Locations.Get(ctx, location)
	if err != nil {
		return proxy.LoginResult{}, err
	}
	var wt Worktree
	for _, w := range loc.Worktrees {
		if w.Name == worktree {
			wt = w
		}
	}
	if wt.Path == "" {
		return proxy.LoginResult{}, ErrUnknownWorktree
	}
	src, err := b.Locations.loginFor(location)
	if err != nil {
		return proxy.LoginResult{}, err
	}
	if src == nil {
		return proxy.LoginResult{}, httpError{http.StatusNotFound, loc.Name + " has no login: its kit or config names a login script and users (\"login\")"}
	}
	email, ok := src.cfg.allows(email)
	if !ok {
		return proxy.LoginResult{}, httpError{http.StatusForbidden, email + " is not one of " + loc.Name + "'s login users"}
	}
	script, err := resolveLoginScript(src.dir, src.cfg.Script)
	if err != nil {
		return proxy.LoginResult{}, httpError{http.StatusConflict, err.Error()}
	}
	env, err := b.WorktreeEnv(ctx, loc.Name, wt)
	if err != nil {
		return proxy.LoginResult{}, err
	}
	out, err := runLoginScript(ctx, script, wt.Path, env, email)
	if err != nil {
		return proxy.LoginResult{}, httpError{http.StatusBadGateway, err.Error()}
	}
	res, err := parseLoginOutput(out)
	if err != nil {
		return proxy.LoginResult{}, httpError{http.StatusBadGateway, "the login script's output: " + err.Error()}
	}
	res.Email = email
	return res, nil
}

// runLoginScript runs the script in dir and returns its stdout. The email
// is an environment variable; the script's path is the only argument.
func runLoginScript(ctx context.Context, script, dir string, env []string, email string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	argv := []string{script}
	if st, err := os.Stat(script); err == nil && st.Mode()&0o111 == 0 {
		// Not executable: a shell reads it, as `sh script` would.
		argv = []string{"/bin/sh", script}
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	base := os.Environ()
	if p := loginPATH(ctx); p != "" {
		base = append(base, "PATH="+p)
	}
	cmd.Env = append(append(append(base, quietEnv...), env...), "BERTH_LOGIN_EMAIL="+email)
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var stdout capped
	stdout.max = maxLoginOutput
	cmd.Stdout = &stdout
	// What it says on stderr is the script's own business: it can hold
	// anything, a session included, so it is dropped.
	cmd.Stderr = io.Discard
	err := cmd.Run()
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return nil, fmt.Errorf("the login script took longer than %s; is the worktree's dev server running?", loginTimeout)
	case err != nil:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("the login script failed (exit status %d); run it in the worktree with BERTH_LOGIN_EMAIL set to see why", ee.ExitCode())
		}
		return nil, errors.New("the login script could not start")
	case stdout.over:
		return nil, fmt.Errorf("the login script printed more than %d KB", maxLoginOutput>>10)
	}
	return stdout.Bytes(), nil
}

// capped keeps the first max bytes written and notes more.
type capped struct {
	bytes.Buffer
	max  int
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.Len(); len(p) > room {
		c.over = true
		if room > 0 {
			c.Buffer.Write(p[:room])
		}
		return len(p), nil
	}
	return c.Buffer.Write(p)
}

// parseLoginOutput reads a login script's stdout strictly: one JSON
// object, {"cookies": […], "redirect"?: "/path"}, every cookie checked.
// Its errors name fields, never values.
func parseLoginOutput(out []byte) (proxy.LoginResult, error) {
	var res struct {
		Cookies  []proxy.LoginCookie `json:"cookies"`
		Redirect *string             `json:"redirect"`
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&res); err != nil {
		if f, ok := unknownField(err); ok {
			if strings.EqualFold(f, "domain") {
				return proxy.LoginResult{}, errors.New("a cookie has a domain; leave it out: the cookie is set for the worktree's own address")
			}
			return proxy.LoginResult{}, fmt.Errorf("%s is not a field Shipyard takes; print {\"cookies\": [{\"name\", \"value\", \"path\", \"httpOnly\", \"secure\", \"sameSite\", \"maxAge\"}], \"redirect\": \"/path\"}", f)
		}
		return proxy.LoginResult{}, errors.New("it is not the JSON Shipyard expects: {\"cookies\": [{\"name\": …, \"value\": …}], \"redirect\": \"/path\"}")
	}
	if _, err := dec.Token(); err != io.EOF {
		return proxy.LoginResult{}, errors.New("it has more than one JSON value; print one object")
	}
	if len(res.Cookies) == 0 {
		return proxy.LoginResult{}, errors.New("it has no cookies")
	}
	if len(res.Cookies) > 32 {
		return proxy.LoginResult{}, errors.New("it has more than 32 cookies")
	}
	seen := map[string]bool{}
	for _, c := range res.Cookies {
		if err := c.Check(); err != nil {
			return proxy.LoginResult{}, err
		}
		k := c.Name + "\x00" + c.Path
		if seen[k] {
			return proxy.LoginResult{}, fmt.Errorf("cookie %s is set twice", c.Name)
		}
		seen[k] = true
	}
	out2 := proxy.LoginResult{Cookies: res.Cookies}
	if res.Redirect != nil {
		if r := *res.Redirect; r == "" || proxy.SafeNext(r) != r {
			return proxy.LoginResult{}, errors.New("redirect must be a path on the worktree, such as /dashboard")
		}
		out2.Redirect = *res.Redirect
	}
	return out2, nil
}

// unknownField reads the field name out of encoding/json's error for one.
func unknownField(err error) (string, bool) {
	f, ok := strings.CutPrefix(err.Error(), "json: unknown field ")
	if !ok {
		return "", false
	}
	return strings.Trim(f, `"`), true
}

// postLogin is POST /v1/worktrees/{loc}/{wt}/login {email}: run the
// worktree's login and answer its cookies. The journal gets who logged in
// where, and whether it worked: never a cookie.
func (b *Box) postLogin(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Email string `json:"email"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	loc, wt := r.PathValue("loc"), r.PathValue("wt")
	email := req.Email
	if !ValidLoginEmail(email) {
		email = ""
	}
	if err := b.before(r, "worktree.login", map[string]any{"location": loc, "worktree": wt, "email": email}); err != nil {
		return err
	}
	res, err := b.RunLogin(r.Context(), loc, wt, req.Email)
	b.publish(r, "worktree.login", map[string]any{"location": loc, "worktree": wt, "email": email, "ok": err == nil})
	if err != nil {
		return err
	}
	writeJSON(w, res)
	return nil
}

// HandleLogin serves POST /v1/worktrees/{loc}/{wt}/login on its own, for
// a test that mounts it beside a stand-in box.
func (b *Box) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if err := b.postLogin(w, r); err != nil {
		writeErr(w, err)
	}
}

// setLoginCookies sets a login's cookies in a Chromium (a CDP session with
// Network enabled) for origin's host alone, as the laptop's proxy does.
func setLoginCookies(ctx context.Context, c *cdpConn, session, origin string, cookies []proxy.LoginCookie) error {
	for _, ck := range cookies {
		if err := ck.Check(); err != nil {
			return err
		}
		hc := ck.HTTP()
		p := map[string]any{"name": hc.Name, "value": hc.Value, "url": strings.TrimRight(origin, "/") + hc.Path, "path": hc.Path, "httpOnly": hc.HttpOnly, "secure": hc.Secure}
		switch hc.SameSite {
		case http.SameSiteLaxMode:
			p["sameSite"] = "Lax"
		case http.SameSiteStrictMode:
			p["sameSite"] = "Strict"
		case http.SameSiteNoneMode:
			p["sameSite"] = "None"
		}
		if ck.MaxAge != nil {
			p["expires"] = time.Now().Add(time.Duration(*ck.MaxAge) * time.Second).Unix()
		}
		var r struct {
			Success *bool `json:"success"`
		}
		if err := c.call(ctx, session, "Network.setCookie", p, &r); err != nil {
			return fmt.Errorf("the browser did not take cookie %s", ck.Name)
		}
		if r.Success != nil && !*r.Success {
			return fmt.Errorf("the browser refused cookie %s", ck.Name)
		}
	}
	return nil
}

// loginIn runs a worktree's login and announces it, for the agent browser
// and shots: the same record the laptop's login route leaves.
func (b *Box) loginIn(ctx context.Context, origin string, loc Location, wt Worktree, email string) (proxy.LoginResult, error) {
	res, err := b.RunLogin(ctx, loc.Name, wt.Name, email)
	shown := email
	if !ValidLoginEmail(shown) {
		shown = ""
	}
	if b.Events != nil {
		b.Events.Publish(events.Event{Type: "worktree.login", Box: b.Name, Origin: origin, Data: map[string]any{"location": loc.Name, "worktree": wt.Name, "email": shown, "ok": err == nil}})
	}
	return res, err
}
