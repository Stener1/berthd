package prreview

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cosscom/shipyard/internal/proxy"
)

// A review link may also say how to open the review once it is ready:
// logged in as one of the project's dev users (as=EMAIL) and at a page
// (path=/x). Neither carries authority. The email is only a request: it is
// used only when the project's trusted login config (its kit, the box's
// own config, or the main checkout's trusted .berth/config.json) lists it
// or allows any, and the review opens without logging in otherwise. The
// path stays on the worktree's own host. Neither changes what is fetched,
// set up or trusted.

// MaxPath bounds a review link's path.
const MaxPath = 512

// emailPattern is the login's own rule (box/login.go): a conservative
// subset of RFC 5321, a local part of letters, digits and . _ + - starting
// with a letter or digit, and a domain of at least two DNS labels.
var emailPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_+-]*(\.[A-Za-z0-9_+-]+)*@[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`)

// ValidEmail reports whether s is an email a login takes.
func ValidEmail(s string) bool {
	if len(s) == 0 || len(s) > 254 {
		return false
	}
	local, _, ok := strings.Cut(s, "@")
	if !ok || len(local) > 64 {
		return false
	}
	return emailPattern.MatchString(s)
}

// ValidPath reports whether p is a page on the worktree's own host:
// one the login route's next takes unchanged, at most MaxPath long.
func ValidPath(p string) bool {
	return p != "" && len(p) <= MaxPath && proxy.SafeNext(p) == p
}

// ShareLink is r's link with its as and path, for pasting.
func (r Ref) ShareLink() string { return LinkFor(r.Repo(), r.PR, r.As, r.Path) }

// LinkFor builds a review link that opens logged in as as, at path; either
// may be "".
func LinkFor(repo string, pr int, as, path string) string {
	l := Link(repo, pr)
	if as != "" {
		l += "&as=" + escape(as)
	}
	if path != "" {
		l += "&path=" + escape(path)
	}
	return l
}

// escape keeps / and @ readable, and escapes what a query value needs to.
func escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte("-._~/@", c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// Login is how a project on a box logs worktrees in: its users' emails and
// whether it takes any; nil when it has no login.
type Login struct {
	Users []string
	Any   bool
}

// LoginCheck says whether a review of repo may open logged in as email,
// given the project's trusted login on the box, and if not why, in words
// that say it opens anyway.
func LoginCheck(repo, email string, l *Login) (bool, string) {
	if l == nil || (len(l.Users) == 0 && !l.Any) {
		return false, fmt.Sprintf("%s has no login set up on this box, so it opens without logging in", repo)
	}
	if !ValidEmail(email) {
		return false, fmt.Sprintf("%q isn't an email a login takes, so it opens without logging in", clip(email))
	}
	if l.Any {
		return true, ""
	}
	for _, u := range l.Users {
		if strings.EqualFold(u, email) {
			return true, ""
		}
	}
	return false, fmt.Sprintf("%s isn't one of %s's login users, so it opens without logging in", email, repo)
}
