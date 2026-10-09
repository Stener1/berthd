package box

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cosscom/shipyard/internal/events"
	"github.com/cosscom/shipyard/internal/prreview"
	"github.com/cosscom/shipyard/internal/statefile"
)

// Review worktrees: a pull request opened for review on this box, from a
// review link (berth://review?repo=…&pr=…) the reviewer confirmed on their
// laptop. The laptop decided who may be reviewed, with the reviewer's own
// gh; the box makes the worktree, and holds to these rules itself:
//
//   - The worktree is the commit the reviewer was shown: refs/pull/N/head is
//     fetched and must equal it, or nothing is made. When the PR moves on
//     the worktree stays put until the reviewer updates it, and confirms
//     again.
//   - Setup, services, hooks and kit come from the location: the kit and
//     the main checkout's trusted .berth/config.json. The PR head's .berth/
//     is never read, and git hooks are off while the PR is checked out.
//   - The worktree gets the team's shared keys, as any worktree does, and
//     never the reviewer's own (team.json's "ask" keys).
//   - It is marked as a review, named "Review: #N title", and cleaned up
//     when its PR is merged or closed, or after a week idle (per box), but
//     never with uncommitted changes without asking.
//   - Each step is in the journal: review.opened, review.updated,
//     review.removed and review.waiting.

// ReviewMark says a worktree is a pull request opened for review.
type ReviewMark struct {
	Repo        string     `json:"repo"`
	PR          int        `json:"pr"`
	SHA         string     `json:"sha"`
	Title       string     `json:"title,omitempty"`
	Author      string     `json:"author,omitempty"`
	Association string     `json:"association,omitempty"`
	HeadBranch  string     `json:"head_branch,omitempty"`
	URL         string     `json:"url,omitempty"`
	Reviewer    string     `json:"reviewer,omitempty"`
	Opened      time.Time  `json:"opened"`
	Updated     *time.Time `json:"updated,omitempty"`
	// Touched is when it was last seen in use (a session running in it).
	Touched *time.Time `json:"touched,omitempty"`
	// Withheld are the environment variables it does not get: the
	// reviewer's own keys.
	Withheld []string `json:"withheld,omitempty"`
	// Cleanup is set when it is due for clean-up (merged, closed, idle)
	// but has uncommitted changes, so it waits for the reviewer.
	Cleanup string `json:"cleanup,omitempty"`
}

// lastActive is the latest of when it was opened, updated or in use.
func (m ReviewMark) lastActive() time.Time {
	t := m.Opened
	for _, o := range []*time.Time{m.Updated, m.Touched} {
		if o != nil && o.After(t) {
			t = *o
		}
	}
	return t
}

// ReviewOpen is POST /v1/reviews: what the reviewer confirmed.
type ReviewOpen struct {
	Location    string `json:"location"`
	Repo        string `json:"repo"`
	PR          int    `json:"pr"`
	SHA         string `json:"sha"`
	Title       string `json:"title,omitempty"`
	Author      string `json:"author,omitempty"`
	Association string `json:"association,omitempty"`
	HeadBranch  string `json:"head_branch,omitempty"`
	URL         string `json:"url,omitempty"`
	Reviewer    string `json:"reviewer,omitempty"`
	// Withhold names the reviewer's own keys (team.json's "ask"), which
	// the worktree never gets; the box adds those its team setups name.
	Withhold []string `json:"withhold,omitempty"`
}

// ReviewUpdate is POST /v1/reviews/update: move a review to a newer head
// the reviewer confirmed.
type ReviewUpdate struct {
	Location    string   `json:"location"`
	Worktree    string   `json:"worktree"`
	SHA         string   `json:"sha"`
	Title       string   `json:"title,omitempty"`
	Author      string   `json:"author,omitempty"`
	Association string   `json:"association,omitempty"`
	Reviewer    string   `json:"reviewer,omitempty"`
	Withhold    []string `json:"withhold,omitempty"`
}

// ReviewSetup is what a review of a location's PR would run, for the
// review sheet: GET /v1/locations/{name}/review-setup.
type ReviewSetup struct {
	Location string `json:"location"`
	// From is where the setup script comes from: kit, repo (the main
	// checkout's trusted .berth/config.json), box (this box's own config)
	// or none.
	From       string     `json:"from"`
	Kit        *ReviewKit `json:"kit,omitempty"`
	RepoConfig string     `json:"repo_config"`
	// DefaultBranch is origin's default branch, and MatchesDefault whether
	// the main checkout's .berth/config.json is that branch's (nil when
	// neither has one, or it can't be told).
	DefaultBranch  string          `json:"default_branch,omitempty"`
	MatchesDefault *bool           `json:"matches_default,omitempty"`
	Script         string          `json:"script,omitempty"`
	Archive        string          `json:"archive,omitempty"`
	Services       []ReviewService `json:"services"`
	Hooks          int             `json:"hooks"`
	Ports          int             `json:"ports"`
	// Watch are the files the kit says affect setup.
	Watch   []string      `json:"watch,omitempty"`
	Secrets ReviewSecrets `json:"secrets"`
	// Login is the project's trusted login: the users a review may open
	// logged in as. nil without one.
	Login    *ReviewLogin `json:"login,omitempty"`
	IdleDays int          `json:"idle_days"`
	// Existing are the reviews already on the box for this location.
	Existing []ReviewEntry `json:"existing"`
}

// ReviewLogin is a project's login as a review link may use it: the users'
// emails, and whether any email is taken.
type ReviewLogin struct {
	Users []string `json:"users"`
	Any   bool     `json:"any,omitempty"`
}

type ReviewKit struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type ReviewService struct {
	Name      string `json:"name"`
	Title     string `json:"title,omitempty"`
	Run       string `json:"run"`
	Autostart bool   `json:"autostart"`
}

// ReviewSecrets names the keys a review gets and those it doesn't. Names
// only, never values.
type ReviewSecrets struct {
	Shared   []string `json:"shared"`
	Withheld []string `json:"withheld"`
}

// ReviewEntry is one review worktree on the box.
type ReviewEntry struct {
	Location string     `json:"location"`
	Worktree string     `json:"worktree"`
	Path     string     `json:"path"`
	Review   ReviewMark `json:"review"`
}

// ReviewSettings are a box's review settings.
type ReviewSettings struct {
	// IdleDays is how many days a review may sit unused before it is
	// removed; 0 never removes one for being idle. Unset is 7.
	IdleDays *int `json:"idle_days,omitempty"`
}

// ReviewStore keeps a box's review settings and finds its gh.
type ReviewStore struct {
	Path string
	// GH is the gh that reads a review's PR state for clean-up; empty is
	// the one on PATH. Tests give a fake.
	GH string
	mu sync.Mutex
}

func (s *ReviewStore) settings() ReviewSettings {
	var out ReviewSettings
	if s == nil || s.Path == "" {
		return out
	}
	if b, err := os.ReadFile(s.Path); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

// IdleDays is how long a review may idle, in days.
func (s *ReviewStore) IdleDays() int {
	if d := s.settings().IdleDays; d != nil {
		return *d
	}
	return int(prreview.DefaultIdle / (24 * time.Hour))
}

func (s *ReviewStore) setIdleDays(days int) error {
	if s == nil || s.Path == "" {
		return httpError{http.StatusNotImplemented, "this box keeps no review settings"}
	}
	if days < 0 || days > 365 {
		return badRequest("idle_days must be between 0 (never) and 365")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.settings()
	st.IdleDays = &days
	b, _ := json.MarshalIndent(st, "", "  ")
	return statefile.Write(s.Path, append(b, '\n'))
}

// reviewCode is an error the app branches on: moved, dirty or exists.
type reviewCode struct {
	status int
	code   string
	msg    string
}

func (e reviewCode) Error() string { return e.msg }

// errMoved: refs/pull/N/head is not the commit the reviewer confirmed.
func errMoved(pr int, want, got string) error {
	return reviewCode{http.StatusConflict, "moved", fmt.Sprintf("PR #%d moved on: its head is %s now, not %s, which you confirmed. Open it again to see what changed", pr, short7(got), short7(want))}
}

func short7(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

var (
	loginPattern = regexp.MustCompile(`^[A-Za-z0-9_\[\]-]{0,64}$`)
	assocPattern = regexp.MustCompile(`^[A-Z_]{0,32}$`)
)

// oneLine keeps a title from GitHub one line of printable text.
func oneLine(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s
}

func (o *ReviewOpen) validate() error {
	o.SHA = strings.ToLower(strings.TrimSpace(o.SHA))
	switch {
	case !prreview.ValidRepo(o.Repo):
		return badRequest("repo %q is not owner/name", o.Repo)
	case !prreview.ValidPR(o.PR):
		return badRequest("pr must be a positive number")
	case !prreview.ValidSHA(o.SHA):
		return badRequest("sha must be the full commit the reviewer confirmed")
	case !loginPattern.MatchString(o.Author) || !loginPattern.MatchString(o.Reviewer):
		return badRequest("author and reviewer are GitHub logins")
	case !assocPattern.MatchString(o.Association):
		return badRequest("association is GitHub's author association")
	}
	if o.URL != "" && o.URL != fmt.Sprintf("https://github.com/%s/pull/%d", o.Repo, o.PR) {
		o.URL = ""
	}
	o.Title = oneLine(o.Title, 200)
	o.HeadBranch = oneLine(o.HeadBranch, 200)
	for _, k := range o.Withhold {
		if !envName.MatchString(k) {
			return badRequest("%q is not an environment variable name", k)
		}
	}
	return nil
}

// reviewTitle is a review worktree's display name.
func reviewTitle(pr int, title string) string {
	t := fmt.Sprintf("Review: #%d", pr)
	if title != "" {
		t += " " + title
	}
	return t
}

// reviewRef is where a review's pinned commit is kept in the repository,
// so git does not collect it while the worktree uses it.
func reviewRef(pr int) string { return "refs/berth/review/" + strconv.Itoa(pr) }

// noHooks keeps git from running hooks while a PR is checked out: a
// repository whose core.hooksPath points into its own tree (husky) would
// otherwise run the PR's post-checkout.
var noHooks = []string{"-c", "core.hooksPath=/dev/null"}

// fetchPinned fetches refs/pull/N/head and checks it is sha, the commit the
// reviewer confirmed.
func fetchPinned(ctx context.Context, repo string, pr int, sha string) error {
	ref := reviewRef(pr)
	spec := fmt.Sprintf("+refs/pull/%d/head:%s", pr, ref)
	args := append([]string{"-C", repo}, noHooks...)
	args = append(args, "fetch", "--quiet", "--no-tags", "origin", spec)
	// A first fetch of a big PR can take longer than git()'s minute.
	fctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(fctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "couldn't find remote ref") {
			return httpError{http.StatusNotFound, fmt.Sprintf("origin has no PR #%d (git fetch: %s)", pr, msg)}
		}
		return fmt.Errorf("git fetch refs/pull/%d/head: %s", pr, msg)
	}
	got, err := git(ctx, "-C", repo, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return fmt.Errorf("git rev-parse %s: %s", ref, strings.TrimSpace(string(got)))
	}
	if g := strings.TrimSpace(string(got)); g != sha {
		return errMoved(pr, sha, g)
	}
	return nil
}

// originSlug is "owner/name" of the repository's origin as configured,
// before any url.*.insteadOf.
func originSlug(ctx context.Context, repo string) string {
	raw, _ := git(ctx, "-C", repo, "config", "--get", "remote.origin.url")
	if s := slugOf(strings.TrimSpace(string(raw))); s != "" {
		return s
	}
	return slugOf(remoteURL(ctx, repo))
}

// reviewLocation finds the location for a review and checks it is a clone
// of the repository.
func (b *Box) reviewLocation(ctx context.Context, name, repo string) (Location, error) {
	loc, err := b.Locations.Get(ctx, name)
	if err != nil {
		return Location{}, err
	}
	if !loc.Repo {
		return Location{}, badRequest("%s is not a git repository", name)
	}
	if got := originSlug(ctx, loc.Path); !strings.EqualFold(got, repo) {
		return Location{}, badRequest("%s is a clone of %s, not %s", name, orNone(got), repo)
	}
	return loc, nil
}

func orNone(s string) string {
	if s == "" {
		return "no GitHub repository"
	}
	return s
}

// findReview is the review worktree of repo's PR in loc, if there is one.
func findReview(loc Location, repo string, pr int) (Worktree, bool) {
	for _, w := range loc.Worktrees {
		if w.Review != nil && w.Review.PR == pr && strings.EqualFold(w.Review.Repo, repo) {
			return w, true
		}
	}
	return Worktree{}, false
}

// freeReviewName is review-N, or review-N-2… when a worktree, folder or
// branch has it.
func freeReviewName(ctx context.Context, loc Location, pr int) string {
	base := "review-" + strconv.Itoa(pr)
	taken := func(n string) bool {
		for _, w := range loc.Worktrees {
			if w.Name == n {
				return true
			}
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(loc.Path), filepath.Base(loc.Path)+"-"+n)); err == nil {
			return true
		}
		return branchExists(ctx, loc.Path, "refs/heads/review/"+n)
	}
	n := base
	for i := 2; taken(n); i++ {
		n = base + "-" + strconv.Itoa(i)
	}
	return n
}

// createReviewWorktree adds the worktree at sha, on a branch of its own,
// with git hooks off.
func (l *Locations) createReviewWorktree(ctx context.Context, loc Location, name, branch, sha string) (Worktree, error) {
	path := filepath.Join(filepath.Dir(loc.Path), filepath.Base(loc.Path)+"-"+name)
	hadBranch := branchExists(ctx, loc.Path, "refs/heads/"+branch)
	_, statErr := os.Stat(path)
	args := append([]string{"-C", loc.Path}, noHooks...)
	args = append(args, "worktree", "add", "-b", branch, path, sha)
	if out, err := git(context.WithoutCancel(ctx), args...); err != nil {
		cleanUpFailedAdd(loc.Path, path, branch, statErr == nil, hadBranch)
		return Worktree{}, fmt.Errorf("git worktree add: %s", strings.TrimSpace(string(out)))
	}
	for _, w := range describe(ctx, savedLocation{Name: loc.Name, Path: loc.Path}).Worktrees {
		if w.Path == path {
			return w, nil
		}
	}
	return Worktree{Name: name, Path: path, Branch: branch}, nil
}

// setReview marks the worktree at path as a review, or with nil forgets it.
func (l *Locations) setReview(location, path string, m *ReviewMark) error {
	return l.update(func(all []savedLocation) ([]savedLocation, error) {
		for i := range all {
			if all[i].Name != location {
				continue
			}
			if m == nil {
				delete(all[i].Reviews, path)
			} else {
				if all[i].Reviews == nil {
					all[i].Reviews = map[string]*ReviewMark{}
				}
				c := *m
				all[i].Reviews[path] = &c
			}
			if len(all[i].Reviews) == 0 {
				all[i].Reviews = nil
			}
			return all, nil
		}
		return nil, ErrUnknownLocation
	})
}

// reviewAt is the review mark of the worktree at path, if it is one.
func (l *Locations) reviewAt(path string) (string, *ReviewMark) {
	all, _ := l.read()
	for _, s := range all {
		if m, ok := s.Reviews[path]; ok && m != nil {
			c := *m
			return s.Name, &c
		}
	}
	return "", nil
}

// teamAskKeys are the keys a team setup on this box asks each engineer for
// (their own), for the project cloned at path.
func (b *Box) teamAskKeys(path string) []string {
	if b.Team == nil || b.Team.Dir == "" {
		return nil
	}
	entries, _ := os.ReadDir(b.Team.Dir)
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		tb, err := b.Team.readBundle(e.Name())
		if err != nil {
			continue
		}
		for _, p := range tb.Projects {
			dest, err := filepath.Abs(expandHome(p.Path))
			if err != nil || !samePath(dest, path) {
				continue
			}
			if len(p.Ask) > 0 {
				out = append(out, p.Ask...)
				continue
			}
			// A bundle from before "ask" was sent: every key that is not a
			// shared reference counts as the engineer's own.
			for _, k := range p.Keys {
				if v, ok := p.Env[k]; ok && IsSecretRef(v) {
					continue
				}
				if _, ok := p.Deferred[k]; ok {
					continue
				}
				out = append(out, k)
			}
		}
	}
	return out
}

// withheldFor is the union of names, sorted and valid.
func withheldFor(lists ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, l := range lists {
		for _, k := range l {
			if envName.MatchString(k) && !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// OpenReview makes a review worktree of a PR at the commit the reviewer
// confirmed, then sets it up as the location sets up any worktree.
func (b *Box) OpenReview(ctx context.Context, from string, req ReviewOpen) (Worktree, error) {
	if err := req.validate(); err != nil {
		return Worktree{}, err
	}
	loc, err := b.reviewLocation(ctx, req.Location, req.Repo)
	if err != nil {
		return Worktree{}, err
	}
	// The same PR again: the worktree there, moved to the confirmed commit
	// if it is not on it.
	if wt, ok := findReview(loc, req.Repo, req.PR); ok {
		if wt.Review.SHA == req.SHA {
			return wt, nil
		}
		return b.UpdateReview(ctx, from, ReviewUpdate{Location: loc.Name, Worktree: wt.Name, SHA: req.SHA, Title: req.Title, Author: req.Author, Association: req.Association, Reviewer: req.Reviewer, Withhold: req.Withhold})
	}
	name := freeReviewName(ctx, loc, req.PR)
	branch := "review/" + name
	if err := b.beforeAs(ctx, from, "worktree.create", map[string]any{
		"location": loc.Name, "name": name, "branch": branch, "base": "", "review": true, "repo": req.Repo, "pr": req.PR, "sha": req.SHA,
	}); err != nil {
		return Worktree{}, err
	}
	if err := fetchPinned(ctx, loc.Path, req.PR, req.SHA); err != nil {
		return Worktree{}, err
	}
	wt, err := b.Locations.createReviewWorktree(ctx, loc, name, branch, req.SHA)
	if err != nil {
		return Worktree{}, err
	}
	b.own(wt.Path)
	mark := ReviewMark{Repo: req.Repo, PR: req.PR, SHA: req.SHA, Title: req.Title, Author: req.Author, Association: req.Association,
		HeadBranch: req.HeadBranch, URL: req.URL, Reviewer: req.Reviewer, Opened: time.Now().UTC(),
		Withheld: withheldFor(req.Withhold, b.teamAskKeys(loc.Path))}
	// Marked before anything runs in it, so its setup already goes without
	// the reviewer's own keys.
	if err := b.Locations.setReview(loc.Name, wt.Path, &mark); err != nil {
		return Worktree{}, err
	}
	if t, err := b.Locations.SetWorktreeTitle(ctx, loc.Name, wt.Name, reviewTitle(req.PR, req.Title)); err == nil {
		wt.Title = t.Title
	}
	wt.Review = &mark
	b.Events.Publish(events.Event{Type: "worktree.created", Box: b.Name, Origin: from, Data: map[string]any{
		"location": loc.Name, "name": wt.Name, "path": wt.Path, "branch": wt.Branch, "review": true,
	}})
	b.Events.Publish(events.Event{Type: "review.opened", Box: b.Name, Origin: from, Data: map[string]any{
		"location": loc.Name, "name": wt.Name, "path": wt.Path, "repo": req.Repo, "pr": req.PR, "sha": req.SHA,
		"author": req.Author, "association": req.Association, "box": b.Name, "reviewer": req.Reviewer,
		"secrets": "shared", "withheld": mark.Withheld,
	}})
	// Set up as every worktree of the location is: its kit and its trusted
	// config, read from the main checkout, never from this worktree.
	if loc.Scripts.Setup != "" {
		go b.lifecycle(from, "setup", loc, wt.Path, wt.Name, loc.Scripts.Setup, func() error {
			go b.startAutostart(loc.Name, wt.Name)
			return nil
		})
	} else {
		go b.startAutostart(loc.Name, wt.Name)
	}
	return wt, nil
}

// worktreeDirty reports whether a worktree has uncommitted changes,
// untracked files included.
func worktreeDirty(ctx context.Context, path string) (bool, error) {
	out, err := git(ctx, "-C", path, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("git status: %s", strings.TrimSpace(string(out)))
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

// UpdateReview moves a review worktree to a newer head of its PR, which the
// reviewer was shown and confirmed. One with uncommitted changes is left
// as it is.
func (b *Box) UpdateReview(ctx context.Context, from string, req ReviewUpdate) (Worktree, error) {
	req.SHA = strings.ToLower(strings.TrimSpace(req.SHA))
	if !prreview.ValidSHA(req.SHA) {
		return Worktree{}, badRequest("sha must be the full commit the reviewer confirmed")
	}
	if !loginPattern.MatchString(req.Author) || !loginPattern.MatchString(req.Reviewer) || !assocPattern.MatchString(req.Association) {
		return Worktree{}, badRequest("author, reviewer and association are GitHub's")
	}
	loc, wt, err := b.worktreeRef(ctx, req.Location, req.Worktree)
	if err != nil {
		return Worktree{}, err
	}
	if wt.Review == nil {
		return Worktree{}, badRequest("%s is not a review", wt.Name)
	}
	mark := *wt.Review
	if mark.SHA == req.SHA {
		return wt, nil
	}
	if dirty, err := worktreeDirty(ctx, wt.Path); err != nil {
		return Worktree{}, err
	} else if dirty {
		return Worktree{}, reviewCode{http.StatusConflict, "dirty", wt.Name + " has uncommitted changes, so it stays where it is. Commit or discard them, then update"}
	}
	if err := fetchPinned(ctx, loc.Path, mark.PR, req.SHA); err != nil {
		return Worktree{}, err
	}
	args := append([]string{"-C", wt.Path}, noHooks...)
	args = append(args, "reset", "--quiet", "--keep", req.SHA)
	if out, err := git(ctx, args...); err != nil {
		return Worktree{}, fmt.Errorf("git reset: %s", strings.TrimSpace(string(out)))
	}
	prev := mark.SHA
	now := time.Now().UTC()
	mark.SHA, mark.Updated, mark.Cleanup = req.SHA, &now, ""
	if req.Title != "" {
		mark.Title = oneLine(req.Title, 200)
	}
	if req.Author != "" {
		mark.Author, mark.Association = req.Author, req.Association
	}
	if req.Reviewer != "" {
		mark.Reviewer = req.Reviewer
	}
	mark.Withheld = withheldFor(mark.Withheld, req.Withhold, b.teamAskKeys(loc.Path))
	if err := b.Locations.setReview(loc.Name, wt.Path, &mark); err != nil {
		return Worktree{}, err
	}
	if t, err := b.Locations.SetWorktreeTitle(ctx, loc.Name, wt.Name, reviewTitle(mark.PR, mark.Title)); err == nil {
		wt.Title = t.Title
	}
	wt.Review, wt.Head = &mark, short10(req.SHA)
	b.Events.Publish(events.Event{Type: "review.updated", Box: b.Name, Origin: from, Data: map[string]any{
		"location": loc.Name, "name": wt.Name, "path": wt.Path, "repo": mark.Repo, "pr": mark.PR, "from": prev, "sha": req.SHA,
		"author": mark.Author, "association": mark.Association, "reviewer": mark.Reviewer,
	}})
	return wt, nil
}

func short10(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

// ReviewSetupFor says what a review in location would run and get.
func (b *Box) ReviewSetupFor(ctx context.Context, location string, withhold []string) (ReviewSetup, error) {
	loc, err := b.Locations.Get(ctx, location)
	if err != nil {
		return ReviewSetup{}, err
	}
	cfg, err := b.Locations.Config(ctx, location)
	if err != nil {
		return ReviewSetup{}, err
	}
	out := ReviewSetup{Location: loc.Name, From: loc.Scripts.From, RepoConfig: cfg.RepoTrust.State, DefaultBranch: loc.DefaultBranch,
		Script: cfg.Effective.Setup, Archive: cfg.Effective.Archive, Services: []ReviewService{}, Hooks: len(cfg.Effective.Hooks),
		Ports: max(cfg.Effective.Ports, 1), IdleDays: b.Reviews.IdleDays(), Existing: []ReviewEntry{}}
	if out.From == "berth" {
		out.From = "box"
	}
	if out.From == "" {
		out.From = "none"
	}
	if cfg.Kit != nil {
		out.Kit = &ReviewKit{ID: cfg.Kit.ID, Name: cfg.Kit.Name, Version: cfg.Kit.Version}
		out.Watch = append([]string{}, cfg.Kit.Watch...)
	}
	for _, s := range cfg.Effective.Services {
		out.Services = append(out.Services, ReviewService{Name: s.Name, Title: s.Title, Run: s.Run, Autostart: s.Autostart})
	}
	out.MatchesDefault = configMatchesDefault(ctx, loc.Path, loc.DefaultBranch)
	// The login as every worktree of the project has it: the kit's, the
	// box's own, or the main checkout's trusted config, never a worktree's.
	if l := cfg.Effective.Login; l != nil && (len(l.Users) > 0 || l.Any) {
		out.Login = &ReviewLogin{Users: []string{}, Any: l.Any}
		for _, u := range l.Users {
			out.Login.Users = append(out.Login.Users, u.Email)
		}
	}
	withheld := withheldFor(withhold, b.teamAskKeys(loc.Path))
	out.Secrets = ReviewSecrets{Shared: []string{}, Withheld: []string{}}
	boxEnv, _ := loadBoxEnv(b.EnvFile)
	all := map[string]string{}
	for k, v := range boxEnv.Env {
		all[k] = v
	}
	for k, v := range cfg.Effective.Env {
		all[k] = v
	}
	held := map[string]bool{}
	for _, k := range withheld {
		held[k] = true
		if _, ok := all[k]; ok {
			out.Secrets.Withheld = append(out.Secrets.Withheld, k)
		}
	}
	for k, v := range all {
		if !held[k] && IsSecretRef(v) {
			out.Secrets.Shared = append(out.Secrets.Shared, k)
		}
	}
	sort.Strings(out.Secrets.Shared)
	for _, w := range loc.Worktrees {
		if w.Review != nil {
			out.Existing = append(out.Existing, ReviewEntry{Location: loc.Name, Worktree: w.Name, Path: w.Path, Review: *w.Review})
		}
	}
	return out, nil
}

// configMatchesDefault reports whether the main checkout's
// .berth/config.json is the default branch's, as origin last said.
func configMatchesDefault(ctx context.Context, repo, def string) *bool {
	if def == "" {
		return nil
	}
	local, lerr := os.ReadFile(filepath.Join(repo, RepoConfigFile))
	remote, rerr := git(ctx, "-C", repo, "show", "refs/remotes/origin/"+def+":"+RepoConfigFile)
	if rerr != nil {
		// No remote branch to tell by, or no file on it.
		if !branchExists(ctx, repo, "refs/remotes/origin/"+def) {
			return nil
		}
		remote = nil
	}
	if lerr != nil && rerr != nil {
		return nil
	}
	same := lerr == nil && rerr == nil && bytes.Equal(local, remote)
	return &same
}

// Reviews lists the review worktrees on the box.
func (b *Box) ReviewList(ctx context.Context) []ReviewEntry {
	out := []ReviewEntry{}
	locs, _ := b.Locations.List(ctx)
	for _, l := range locs {
		for _, w := range l.Worktrees {
			if w.Review != nil {
				out = append(out, ReviewEntry{Location: l.Name, Worktree: w.Name, Path: w.Path, Review: *w.Review})
			}
		}
	}
	return out
}

// prState asks the box's gh for a PR's state: OPEN, CLOSED or MERGED, or
// "" when it can't tell (no gh, signed out, offline).
func (b *Box) prState(ctx context.Context, repo string, pr int) string {
	bin := ""
	if b.Reviews != nil {
		bin = b.Reviews.GH
	}
	if bin == "" {
		p, err := toolPath("gh")
		if err != nil {
			return ""
		}
		bin = p
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "pr", "view", strconv.Itoa(pr), "-R", repo, "--json", "state")
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	var v struct {
		State string `json:"state"`
	}
	if json.Unmarshal(out, &v) != nil {
		return ""
	}
	return strings.ToUpper(v.State)
}

// ReviewSweep is what one clean-up pass did with a review.
type ReviewSweep struct {
	Location string `json:"location"`
	Worktree string `json:"worktree"`
	Reason   string `json:"reason"`
	// Action is removed, waiting (has uncommitted changes or a session
	// running) or failed.
	Action string `json:"action"`
	Error  string `json:"error,omitempty"`
}

// SweepReviews cleans review worktrees up: those whose PR is merged or
// closed, and those idle longer than the box allows, through the location's
// archive script (which drops a kit's database). One with uncommitted
// changes, or a session running in it, is never removed: it is marked to
// ask the reviewer instead.
func (b *Box) SweepReviews(ctx context.Context, now time.Time) []ReviewSweep {
	out := []ReviewSweep{}
	locs, err := b.Locations.List(ctx)
	if err != nil {
		return out
	}
	var sessions []Session
	if b.Sessions != nil {
		sessions, _ = b.Sessions.List(ctx)
	}
	idle := time.Duration(b.Reviews.IdleDays()) * 24 * time.Hour
	for _, loc := range locs {
		for _, wt := range loc.Worktrees {
			if wt.Review == nil {
				continue
			}
			mark := *wt.Review
			live := false
			for _, s := range sessions {
				if !s.Exited && (s.Dir == wt.Path || strings.HasPrefix(s.Dir, wt.Path+string(filepath.Separator))) {
					live = true
				}
			}
			if live {
				t := now.UTC()
				mark.Touched = &t
				b.Locations.setReview(loc.Name, wt.Path, &mark)
			}
			dirty, err := worktreeDirty(ctx, wt.Path)
			if err != nil {
				continue
			}
			due := prreview.CleanupDue(b.prState(ctx, mark.Repo, mark.PR), mark.lastActive(), now, idle, dirty || live)
			switch {
			case due.Remove:
				res := ReviewSweep{Location: loc.Name, Worktree: wt.Name, Reason: due.Reason, Action: "removed"}
				if err := b.removeReview(ctx, "berth", loc, wt, due.Reason); err != nil {
					res.Action, res.Error = "failed", err.Error()
				}
				out = append(out, res)
			case due.Ask:
				out = append(out, ReviewSweep{Location: loc.Name, Worktree: wt.Name, Reason: due.Reason, Action: "waiting"})
				if mark.Cleanup != due.Reason {
					mark.Cleanup = due.Reason
					b.Locations.setReview(loc.Name, wt.Path, &mark)
					b.Events.Publish(events.Event{Type: "review.waiting", Box: b.Name, Origin: "berth", Data: map[string]any{
						"location": loc.Name, "name": wt.Name, "path": wt.Path, "repo": mark.Repo, "pr": mark.PR, "reason": due.Reason,
					}})
				}
			}
		}
	}
	return out
}

// removeReview removes a review worktree the way Remove does: its services
// and sessions stop, the archive script runs (dropping its database), then
// its folder and its review branch go.
func (b *Box) removeReview(ctx context.Context, from string, loc Location, wt Worktree, reason string) error {
	if err := b.beforeAs(ctx, from, "worktree.remove", map[string]any{"location": loc.Name, "name": wt.Name, "path": wt.Path}); err != nil {
		return err
	}
	b.own(wt.Path)
	done := make(chan error, 1)
	_, err := b.dropWorktree(from, loc, wt.Name, wt.Path, wt.Branch, false, reason, func(err error) { done <- err })
	if err != nil {
		return err
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RunReviewSweeps sweeps now and then until ctx ends.
func (b *Box) RunReviewSweeps(ctx context.Context) {
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		b.SweepReviews(ctx, time.Now())
		timer.Reset(time.Hour)
	}
}

func (b *Box) reviewSetup(w http.ResponseWriter, r *http.Request) error {
	var withhold []string
	if v := r.URL.Query().Get("withhold"); v != "" {
		withhold = strings.Split(v, ",")
	}
	out, err := b.ReviewSetupFor(r.Context(), r.PathValue("name"), withhold)
	if err != nil {
		return err
	}
	writeJSON(w, out)
	return nil
}

func (b *Box) postReview(w http.ResponseWriter, r *http.Request) error {
	var req ReviewOpen
	if err := decode(r, &req); err != nil {
		return err
	}
	wt, err := b.OpenReview(r.Context(), origin(r), req)
	if err != nil {
		return err
	}
	writeJSON(w, wt)
	return nil
}

func (b *Box) postReviewUpdate(w http.ResponseWriter, r *http.Request) error {
	var req ReviewUpdate
	if err := decode(r, &req); err != nil {
		return err
	}
	wt, err := b.UpdateReview(r.Context(), origin(r), req)
	if err != nil {
		return err
	}
	writeJSON(w, wt)
	return nil
}

func (b *Box) listReviews(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, b.ReviewList(r.Context()))
	return nil
}

func (b *Box) getReviewSettings(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, map[string]int{"idle_days": b.Reviews.IdleDays()})
	return nil
}

func (b *Box) putReviewSettings(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		IdleDays *int `json:"idle_days"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.IdleDays == nil {
		return badRequest("say idle_days: how many days a review may sit unused (0: never removed for it)")
	}
	if err := b.Reviews.setIdleDays(*req.IdleDays); err != nil {
		return err
	}
	return b.getReviewSettings(w, r)
}
