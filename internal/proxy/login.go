package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Logging in as a dev user: a project's kit (or its committed config) can
// name a login script and the users it logs in (box/login.go). On a
// worktree's private host,
//
//	GET /__berth/login?as=pro%40acme.test&next=/settings
//
// asks the box to run that worktree's login script for the email, against
// the worktree's own dev server and database, and answers 302 with the
// cookies it printed, set for this host only, then Location: next. The
// Browser tab, a real browser and a review link all log in this way.
//
// The route is the proxy's own: it is answered here and never reaches the
// dev server, on any host. It takes GET alone, from this machine alone, and
// only when nothing else started it: a request another site (or another
// worktree's page) sent is refused, so a page can't log you in as someone
// behind your back. Cookie values never reach a log, an event or an error.

// LoginPath is the proxy's login route on every worktree host.
const LoginPath = "/__berth/login"

// LoginCookie is one cookie a login script asks for. There is no Domain:
// the cookie is for the worktree's own host, always.
type LoginCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Path     string `json:"path,omitempty"`
	HTTPOnly bool   `json:"httpOnly,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
	SameSite string `json:"sameSite,omitempty"`
	MaxAge   *int   `json:"maxAge,omitempty"`
}

// LoginResult is what a login gives: the cookies to set and, optionally,
// where the app would send a user who just logged in.
type LoginResult struct {
	Email    string        `json:"email,omitempty"`
	Cookies  []LoginCookie `json:"cookies"`
	Redirect string        `json:"redirect,omitempty"`
}

// LoginError is a refusal with the status the route answers with.
type LoginError struct {
	Status int
	Msg    string
}

func (e *LoginError) Error() string { return e.Msg }

// LoginFunc runs the login of the worktree a host names (its labels, as
// for Worktree) for an email.
type LoginFunc func(ctx context.Context, labels []string, email string) (LoginResult, error)

const (
	maxLoginCookies   = 32
	maxCookieName     = 256
	maxCookieValue    = 4096
	maxCookiePath     = 1024
	maxCookieMaxAge   = 400 * 24 * 3600 // what browsers cap Max-Age at
	maxNext           = 2048
	maxDecodeAttempts = 4
)

// RFC 6265: a cookie's name is a token, its value cookie-octets.
var (
	cookieName  = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")
	cookieValue = regexp.MustCompile(`^[\x21\x23-\x2B\x2D-\x3A\x3C-\x5B\x5D-\x7E]*$`)
	cookiePath  = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,=:@%/-]*$`)
)

// Check says what is wrong with a cookie, never quoting its value.
func (c LoginCookie) Check() error {
	switch {
	case c.Name == "" || len(c.Name) > maxCookieName || !cookieName.MatchString(c.Name):
		return errors.New("a cookie's name must be a token (letters, digits and !#$%&'*+-.^_`|~), at most 256 characters")
	case len(c.Value) > maxCookieValue:
		return fmt.Errorf("cookie %s: its value is longer than %d bytes", c.Name, maxCookieValue)
	case !cookieValue.MatchString(c.Value):
		return fmt.Errorf("cookie %s: its value has a character a cookie can't hold (a space, a quote, a comma, a semicolon, a backslash or a control character)", c.Name)
	case c.Path != "" && (len(c.Path) > maxCookiePath || !cookiePath.MatchString(c.Path)):
		return fmt.Errorf("cookie %s: path must be a plain path starting with /", c.Name)
	case c.MaxAge != nil && (*c.MaxAge < 1 || *c.MaxAge > maxCookieMaxAge):
		return fmt.Errorf("cookie %s: maxAge must be from 1 to %d seconds", c.Name, maxCookieMaxAge)
	}
	if _, ok := sameSite(c.SameSite); !ok {
		return fmt.Errorf("cookie %s: sameSite must be Lax, Strict or None", c.Name)
	}
	if strings.HasPrefix(c.Name, "__Host-") && (c.Path != "" && c.Path != "/") {
		return fmt.Errorf("cookie %s: a __Host- cookie's path must be /", c.Name)
	}
	return nil
}

func sameSite(s string) (http.SameSite, bool) {
	switch strings.ToLower(s) {
	case "":
		return http.SameSiteDefaultMode, true
	case "lax":
		return http.SameSiteLaxMode, true
	case "strict":
		return http.SameSiteStrictMode, true
	case "none":
		return http.SameSiteNoneMode, true
	}
	return 0, false
}

// HTTP is the cookie as a Set-Cookie header sets it: host-only, since it
// has no Domain.
func (c LoginCookie) HTTP() *http.Cookie {
	hc := &http.Cookie{Name: c.Name, Value: c.Value, Path: c.Path, HttpOnly: c.HTTPOnly, Secure: c.Secure}
	if hc.Path == "" {
		hc.Path = "/"
	}
	hc.SameSite, _ = sameSite(c.SameSite)
	if c.MaxAge != nil {
		hc.MaxAge = *c.MaxAge
	}
	return hc
}

// SafeNext is next when it is a path on this host ("/settings?tab=2"), and
// "/" otherwise: nothing with a scheme or a host, nothing a browser could
// read as one ("//evil", "/\evil", "/%2F%2Fevil", a tab or a newline it
// would drop), so a login link never leads off the worktree.
func SafeNext(next string) string {
	if next == "" || len(next) > maxNext || !safePath(next, true) {
		return "/"
	}
	s := next
	for range maxDecodeAttempts {
		d, err := url.PathUnescape(s)
		if err != nil {
			// Not valid percent-encoding: no browser reads it as a host
			// either, but nothing needs it.
			return "/"
		}
		if d == s {
			return next
		}
		if !safePath(d, false) {
			return "/"
		}
		s = d
	}
	// Encoded more times than any real link is.
	return "/"
}

// safePath reports whether s is a path no browser reads as another host.
// raw is the path as sent, which takes no spaces either.
func safePath(s string, raw bool) bool {
	if !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < ' ' || c == 0x7f || c == '\\' || (raw && c == ' ') {
			return false
		}
	}
	// A path never needs "://".
	return !strings.Contains(s, "://")
}

// LoginURL is the address that opens a worktree logged in as email, at
// next: origin is the worktree's (or its service's) private URL, such as
// http://fix-x.shop.devl.localhost:1377. Review links, the app and agents
// build their links with it.
func LoginURL(origin, email, next string) string {
	q := url.Values{}
	q.Set("as", email)
	q.Set("next", SafeNext(next))
	return strings.TrimRight(origin, "/") + LoginPath + "?" + q.Encode()
}

// isLoginRoute reports whether a request is for the login route, however
// its path is spelled ("/__berth/login/", "//__berth//login", any case).
func isLoginRoute(r *http.Request) bool {
	p := path.Clean("/" + strings.ToLower(r.URL.Path))
	return p == LoginPath || strings.HasPrefix(p, LoginPath+"/")
}

// serveLogin answers the login route. It never forwards anything.
func (p *Proxy) serveLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		page(w, http.StatusMethodNotAllowed, "Logging in takes GET", "Open the link in a browser.")
		return
	}
	if !fromLoopback(r) {
		page(w, http.StatusForbidden, "Logging in works on this computer only", "")
		return
	}
	if why := crossSite(r); why != "" {
		page(w, http.StatusForbidden, "Shipyard didn't log you in", why+" Open the link yourself: paste it in the address bar, or use Log in as… in the Browser tab.")
		return
	}
	labels, ok := localhostLabels(r.Host)
	if !ok || p.Login == nil {
		page(w, http.StatusNotFound, "Not a worktree's address", "Log in on a worktree's own address, such as http://WORKTREE.LOCATION.BOX.localhost:1377"+LoginPath+"?as=EMAIL")
		return
	}
	labels = p.pairedLabels(labels)
	q := r.URL.Query()
	email := q.Get("as")
	if email == "" {
		page(w, http.StatusBadRequest, "Log in as whom?", "Add ?as=EMAIL, one of the users the project's login lists.")
		return
	}
	next := q.Get("next")
	res, err := p.Login(r.Context(), labels, email)
	if err != nil {
		status := http.StatusBadGateway
		var le *LoginError
		if errors.As(err, &le) && le.Status != 0 {
			status = le.Status
		}
		page(w, status, "Could not log in as "+email, err.Error())
		return
	}
	// Every cookie is checked before any is set: all or nothing.
	if len(res.Cookies) == 0 || len(res.Cookies) > maxLoginCookies {
		page(w, http.StatusBadGateway, "Could not log in as "+email, fmt.Sprintf("the login gave %d cookies; it gives 1 to %d", len(res.Cookies), maxLoginCookies))
		return
	}
	cookies := make([]*http.Cookie, 0, len(res.Cookies))
	for _, c := range res.Cookies {
		if err := c.Check(); err != nil {
			page(w, http.StatusBadGateway, "Could not log in as "+email, err.Error())
			return
		}
		cookies = append(cookies, c.HTTP())
	}
	host := hostOnly(r.Host)
	for _, c := range cookies {
		http.SetCookie(w, c)
	}
	// Preview frames get them too (preview.go).
	p.preview.jar(host).SetCookies(jarURL(host, "/"), cookies)
	to := SafeNext(next)
	if next == "" && res.Redirect != "" {
		to = SafeNext(res.Redirect)
	}
	w.Header().Set("Location", to)
	w.WriteHeader(http.StatusFound)
}

func fromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// crossSite says why a request looks started by another page, or "".
// Only a person (typing, pasting, the app: Sec-Fetch-Site none) or the
// worktree's own page (same-origin) may log in.
func crossSite(r *http.Request) string {
	switch s := strings.ToLower(r.Header.Get("Sec-Fetch-Site")); s {
	case "", "none", "same-origin":
	default:
		return "Another page (" + s + ") sent this request."
	}
	own := hostOnly(r.Host)
	for _, h := range []string{"Origin", "Referer"} {
		v := r.Header.Get(h)
		if v == "" {
			continue
		}
		u, err := url.Parse(v)
		if err != nil || u.Host == "" || hostOnly(u.Host) != own || !sameAuthority(u.Host, r.Host) {
			return "Another page sent this request (its " + h + " is another address)."
		}
	}
	return ""
}

// sameAuthority compares two hosts with their ports.
func sameAuthority(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(a, "."), strings.TrimSuffix(b, "."))
}
