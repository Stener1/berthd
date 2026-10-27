// Package prreview holds the rules of "review a PR in one click": reading a
// review link, deciding whether a pull request may be opened for review on
// the reviewer's own box, finding the changes in it that touch how a
// worktree is set up, and deciding when a review worktree is cleaned up.
//
// A review link carries no authority. It names a repository and a pull
// request number (and, at most, a head commit the sharer saw, which is only
// ever a hint); everything that decides what happens comes from GitHub, read
// with the reviewer's own gh, from the team's setup and from the box.
// Nothing in a link is a command, a path, a setting or an environment
// variable, and anything that is not exactly a repository and a number is
// refused.
//
// This package only parses and decides. The laptop agent (internal/agent)
// reads GitHub and the box (internal/box) makes the worktree.
package prreview

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Scheme and Host make a review link: berth://review?repo=OWNER/NAME&pr=N.
const (
	Scheme = "berth"
	Host   = "review"
)

// maxLink bounds a link before it is parsed at all.
const maxLink = 512

// Ref is what a review link names.
type Ref struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
	PR    int    `json:"pr"`
	// SHA is the head commit the link's author saw, if the link says. It
	// is a hint: the commit reviewed is the one GitHub reports, shown to
	// the reviewer and confirmed; a hint that differs only warns.
	SHA string `json:"sha,omitempty"`
	// As and Path say how to open the review once it is ready: logged in
	// as a dev user the project lists, at a page (login.go). Requests only.
	As   string `json:"as,omitempty"`
	Path string `json:"path,omitempty"`
}

// Repo is "owner/name".
func (r Ref) Repo() string { return r.Owner + "/" + r.Name }

// Link is the canonical link for r, without its hint.
func (r Ref) Link() string { return Link(r.Repo(), r.PR) }

// String is "owner/name#N".
func (r Ref) String() string { return fmt.Sprintf("%s#%d", r.Repo(), r.PR) }

var (
	partPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	prPattern   = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)
	shaPattern  = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)
	argPattern  = regexp.MustCompile(`^([^/#\s]+)/([^/#\s]+)#([^#\s]+)$`)
	fullSHA     = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// ErrBadLink is every refusal of a link: what is wrong goes after it.
var ErrBadLink = errors.New("this isn't a review link Shipyard can open")

func bad(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBadLink, fmt.Sprintf(format, args...))
}

// ValidRepo reports whether s is "owner/name" as a review link may name it.
func ValidRepo(s string) bool {
	owner, name, ok := strings.Cut(s, "/")
	return ok && validOwner(owner) && validName(name)
}

func validOwner(s string) bool {
	return len(s) <= 39 && partPattern.MatchString(s) && s != "." && s != ".."
}

func validName(s string) bool {
	return len(s) <= 100 && partPattern.MatchString(s) && s != "." && s != ".."
}

// ValidPR reports whether n is a pull request number.
func ValidPR(n int) bool { return n > 0 && n <= math.MaxInt32 }

// ValidSHA reports whether s is a full commit hash, lowercase.
func ValidSHA(s string) bool { return fullSHA.MatchString(s) }

func parsePR(s string) (int, error) {
	if !prPattern.MatchString(s) {
		return 0, bad("the PR number %q is not a positive whole number", clip(s))
	}
	n, err := strconv.Atoi(s)
	if err != nil || !ValidPR(n) {
		return 0, bad("the PR number %q is too large", clip(s))
	}
	return n, nil
}

func parseRepo(s string) (string, string, error) {
	owner, name, ok := strings.Cut(s, "/")
	if !ok || !validOwner(owner) || !validName(name) {
		return "", "", bad("the repository %q is not OWNER/NAME", clip(s))
	}
	return owner, name, nil
}

// clip keeps what a refusal quotes short and on one line.
func clip(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

// ParseLink reads berth://review?repo=OWNER/NAME&pr=N[&sha=HEX][&as=EMAIL]
// [&path=/x]. It refuses
// anything else: another scheme or host, a path, a user, a fragment, a
// parameter it does not know or one given twice, and values that are not
// exactly a repository, a number and a commit.
func ParseLink(s string) (Ref, error) {
	if len(s) > maxLink {
		return Ref{}, bad("it is longer than %d characters", maxLink)
	}
	if strings.ContainsAny(s, " \t\r\n\\") {
		return Ref{}, bad("it has spaces or backslashes")
	}
	u, err := url.Parse(s)
	if err != nil {
		return Ref{}, bad("it is not a link")
	}
	if !strings.EqualFold(u.Scheme, Scheme) || u.Opaque != "" || u.User != nil || !strings.EqualFold(u.Host, Host) || (u.Path != "" && u.Path != "/") || u.Fragment != "" || u.RawFragment != "" {
		return Ref{}, bad("it is not berth://review?repo=OWNER/NAME&pr=N")
	}
	seen := map[string]string{}
	if u.RawQuery == "" {
		return Ref{}, bad("it names no repository or PR")
	}
	for _, part := range strings.Split(u.RawQuery, "&") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return Ref{}, bad("%q has no value", clip(part))
		}
		switch k {
		case "repo", "pr", "sha", "as", "path":
		default:
			return Ref{}, bad("it has a parameter %q; a review link takes only repo, pr, sha, as and path", clip(k))
		}
		if _, dup := seen[k]; dup {
			return Ref{}, bad("it gives %s twice", k)
		}
		dec, err := url.QueryUnescape(v)
		if err != nil {
			return Ref{}, bad("%s is not readable", k)
		}
		seen[k] = dec
	}
	repo, ok := seen["repo"]
	if !ok {
		return Ref{}, bad("it names no repository")
	}
	pr, ok := seen["pr"]
	if !ok {
		return Ref{}, bad("it names no PR")
	}
	owner, name, err := parseRepo(repo)
	if err != nil {
		return Ref{}, err
	}
	n, err := parsePR(pr)
	if err != nil {
		return Ref{}, err
	}
	ref := Ref{Owner: owner, Name: name, PR: n}
	if sha, ok := seen["sha"]; ok {
		if !shaPattern.MatchString(sha) {
			return Ref{}, bad("the commit %q is not a commit hash", clip(sha))
		}
		ref.SHA = strings.ToLower(sha)
	}
	if as, ok := seen["as"]; ok {
		if !ValidEmail(as) {
			return Ref{}, bad("as %q is not a plain email address", clip(as))
		}
		ref.As = as
	}
	if p, ok := seen["path"]; ok {
		if !ValidPath(p) {
			return Ref{}, bad("path %q is not a page on the worktree's own address", clip(p))
		}
		ref.Path = p
	}
	return ref, nil
}

// ParseArg reads what `berth review` takes: OWNER/NAME#N, or a review link.
func ParseArg(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), Scheme+"://") {
		return ParseLink(s)
	}
	if len(s) > maxLink {
		return Ref{}, bad("it is longer than %d characters", maxLink)
	}
	m := argPattern.FindStringSubmatch(s)
	if m == nil {
		return Ref{}, bad("%q is not OWNER/NAME#N", clip(s))
	}
	owner, name, err := parseRepo(m[1] + "/" + m[2])
	if err != nil {
		return Ref{}, err
	}
	n, err := parsePR(m[3])
	if err != nil {
		return Ref{}, err
	}
	return Ref{Owner: owner, Name: name, PR: n}, nil
}

// Link builds the review link for repo's pull request pr.
func Link(repo string, pr int) string {
	return Scheme + "://" + Host + "?repo=" + repo + "&pr=" + strconv.Itoa(pr)
}

// PR is what GitHub says about a pull request, as the reviewer's gh reads
// it.
type PR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	// State is OPEN, CLOSED or MERGED.
	State string `json:"state"`
	Draft bool   `json:"draft,omitempty"`
	URL   string `json:"url,omitempty"`
	// Author is the author's login and Association their relation to the
	// repository: OWNER, MEMBER, COLLABORATOR, CONTRIBUTOR,
	// FIRST_TIME_CONTRIBUTOR, FIRST_TIMER, MANNEQUIN or NONE.
	Author      string `json:"author"`
	Association string `json:"association"`
	HeadBranch  string `json:"head_branch"`
	HeadSHA     string `json:"head_sha"`
	// HeadRepo is the repository the head branch lives in, and Cross says
	// it is not the base repository: a fork.
	HeadRepo   string `json:"head_repo,omitempty"`
	Cross      bool   `json:"cross"`
	BaseBranch string `json:"base_branch"`
}

// PR states.
const (
	StateOpen   = "OPEN"
	StateClosed = "CLOSED"
	StateMerged = "MERGED"
)

// trusted are the author associations whose pull requests can be opened
// for review in one click: people the repository's org already lets in.
var trusted = map[string]bool{"OWNER": true, "MEMBER": true, "COLLABORATOR": true}

// TrustedAssociation reports whether an author association may be reviewed
// in one click.
func TrustedAssociation(a string) bool { return trusted[strings.ToUpper(a)] }

// Verdict codes.
const (
	CodeNotAProject = "not_a_project"
	CodeFork        = "fork"
	CodeOutsider    = "outsider"
	CodeClosed      = "closed"
	CodeUnreadable  = "unreadable"
	CodeNoBox       = "no_box"
	CodeBadPR       = "bad_pr"
)

// Verdict says whether a pull request may be opened for review, and if not
// why, in words for the reviewer.
type Verdict struct {
	Allowed bool   `json:"allowed"`
	Code    string `json:"code,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func refuse(code, format string, args ...any) Verdict {
	return Verdict{Code: code, Reason: fmt.Sprintf(format, args...)}
}

// Allow is a verdict that lets it through.
var Allow = Verdict{Allowed: true}

// Policy is who decides: the repositories the team's setup lists (its
// team.json projects), those already on the reviewer's boxes, and the org
// an author must belong to.
type Policy struct {
	TeamRepos []string
	BoxRepos  []string
	// Org is the team's GitHub org, or the repository's owner without one.
	Org string
}

func has(list []string, repo string) bool {
	for _, r := range list {
		if strings.EqualFold(r, repo) {
			return true
		}
	}
	return false
}

// CheckRepo decides whether a repository may be reviewed at all, before
// anything about the pull request is read: it must be one of the team's
// projects or a project already on one of the reviewer's boxes.
func CheckRepo(repo string, p Policy) Verdict {
	if !ValidRepo(repo) {
		return refuse(CodeNotAProject, "%q is not a repository", clip(repo))
	}
	if has(p.TeamRepos, repo) || has(p.BoxRepos, repo) {
		return Allow
	}
	return refuse(CodeNotAProject, "%s isn't one of your team's projects or on any of your boxes, so Shipyard won't open it. Review it by hand", repo)
}

// Authorize decides whether pr, in repo, may be opened for review in one
// click. Every refusal says what to do instead.
func Authorize(repo string, pr PR, p Policy) Verdict {
	if v := CheckRepo(repo, p); !v.Allowed {
		return v
	}
	if !ValidPR(pr.Number) || !ValidSHA(strings.ToLower(pr.HeadSHA)) {
		return refuse(CodeBadPR, "GitHub's answer about %s#%d has no head commit", repo, pr.Number)
	}
	switch strings.ToUpper(pr.State) {
	case StateMerged:
		return refuse(CodeClosed, "This PR is merged, so there is nothing left to review")
	case StateClosed:
		return refuse(CodeClosed, "This PR is closed")
	case StateOpen:
	default:
		return refuse(CodeClosed, "This PR is %s", strings.ToLower(pr.State))
	}
	org := p.Org
	if org == "" {
		org, _, _ = strings.Cut(repo, "/")
	}
	// A fork's code was pushed by someone the org has not let in, whatever
	// their association: its branch lives outside the repository.
	if pr.Cross || (pr.HeadRepo != "" && !strings.EqualFold(pr.HeadRepo, repo)) {
		from := pr.HeadRepo
		if from == "" {
			from = "another repository"
		}
		return refuse(CodeFork, "Review this one by hand: it comes from a fork (%s)", from)
	}
	if !TrustedAssociation(pr.Association) {
		return refuse(CodeOutsider, "Review this one by hand: its author is outside %s", org)
	}
	return Allow
}

// Cleanup reasons.
const (
	ReasonMerged = "merged"
	ReasonClosed = "closed"
	ReasonIdle   = "idle"
)

// DefaultIdle is how long a review worktree may sit unused before it is
// cleaned up, unless the box says otherwise.
const DefaultIdle = 7 * 24 * time.Hour

// Cleanup is what to do with a review worktree now.
type Cleanup struct {
	// Reason is why it is due: merged, closed or idle; "" when it is not.
	Reason string
	// Remove: remove it now. Ask: it is due but has uncommitted changes,
	// so it waits for the reviewer to say.
	Remove bool
	Ask    bool
}

// CleanupDue decides about one review worktree: its PR's state ("" when
// unknown), when it was last used, how long it may idle (0: never), and
// whether it has uncommitted changes, which are never removed without
// asking.
func CleanupDue(state string, lastActive, now time.Time, idle time.Duration, dirty bool) Cleanup {
	reason := ""
	switch strings.ToUpper(state) {
	case StateMerged:
		reason = ReasonMerged
	case StateClosed:
		reason = ReasonClosed
	default:
		if idle > 0 && !lastActive.IsZero() && now.Sub(lastActive) >= idle {
			reason = ReasonIdle
		}
	}
	if reason == "" {
		return Cleanup{}
	}
	if dirty {
		return Cleanup{Reason: reason, Ask: true}
	}
	return Cleanup{Reason: reason, Remove: true}
}

// Withheld is which of env's names a review worktree does not get: the
// reviewer's own keys (team.json's "ask"), never the team's shared ones.
func Withheld(env map[string]string, own []string) []string {
	out := []string{}
	for _, k := range own {
		if _, ok := env[k]; ok {
			out = append(out, k)
		}
	}
	return out
}
