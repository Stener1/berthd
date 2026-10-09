package box

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cosscom/shipyard/internal/events"
	"github.com/cosscom/shipyard/internal/team"
)

// Team setup uses the clones an engineer already has. A project's
// repository is matched by its origin, not by its folder: a Shipyard
// location on the box whose main checkout is a clone of it, or a clone
// the scan finds (clonescan.go). Candidates are offered to the page, which
// decides; the box re-checks the chosen folder's origin before using it.
// Using one never changes it: no checkout, stash, reset or pull, and its
// .env and uncommitted changes stay as they are.

// ExistingClone is a clone of a project's repository already on the box.
type ExistingClone struct {
	// Path is where it is, absolute; Display the same with ~ for home.
	Path    string `json:"path"`
	Display string `json:"display"`
	// Branch is its current branch, or "" when HEAD is detached (Head).
	Branch string `json:"branch,omitempty"`
	Head   string `json:"head,omitempty"`
	// Dirty is how many files have uncommitted changes.
	Dirty int `json:"dirty"`
	// Behind is how many commits its upstream has that it lacks, as of its
	// last fetch (nothing is fetched to tell).
	Behind int `json:"behind,omitempty"`
	// Location is its Shipyard location's name, when it is one already.
	Location string `json:"location,omitempty"`
	// LastCommit is its HEAD commit's date.
	LastCommit *time.Time `json:"last_commit,omitempty"`
	// Used is when a Shipyard location was last worked in (its git index
	// changed), to prefer the most recent of several.
	Used *time.Time `json:"used,omitempty"`
	// AtPath says it is at the path team.json gives the project.
	AtPath bool `json:"at_path,omitempty"`
	// Worktrees are its git worktrees besides the main checkout.
	Worktrees int `json:"worktrees,omitempty"`
}

// ExistingProject is a project and the clones of it on the box, the
// preferred first.
type ExistingProject struct {
	ID     string          `json:"id"`
	Repo   string          `json:"repo"`
	Path   string          `json:"path"`
	Clones []ExistingClone `json:"clones"`
	// Note says why the first is preferred when there are several.
	Note string `json:"note,omitempty"`
}

// ExistingResult is what GET /v1/team/{id}/existing answers.
type ExistingResult struct {
	Projects []ExistingProject `json:"projects"`
	// Looked are the folders looked in; Truncated says the scan stopped at
	// its time or entries budget.
	Looked    []string `json:"looked"`
	Truncated bool     `json:"truncated,omitempty"`
}

// projectRef is one project to look for: its id, repository and path.
type projectRef struct{ ID, Repo, Path string }

// FindExisting looks for clones of each project's repository: Shipyard
// locations first, then the scan of the home folder's usual places.
func (b *Box) FindExisting(ctx context.Context, projects []projectRef, scan *cloneScan) ExistingResult {
	home, _ := os.UserHomeDir()
	res := ExistingResult{Projects: []ExistingProject{}, Looked: []string{}}
	if scan == nil {
		scan = &cloneScan{}
	}
	scan.Home, scan.Want = home, map[string]bool{}
	extra := map[string]bool{}
	for _, p := range projects {
		scan.Want[strings.ToLower(p.Repo)] = true
		if parent := filepath.Dir(expandHome(p.Path)); parent != "" && !extra[parent] {
			extra[parent] = true
			scan.Extra = append(scan.Extra, parent)
		}
	}
	found := scan.run()
	res.Truncated = scan.Truncated
	for _, f := range scanFolders {
		res.Looked = append(res.Looked, "~/"+f)
	}
	for _, e := range scan.Extra {
		res.Looked = append(res.Looked, tildePath(e))
	}
	res.Looked = append(res.Looked, "~/* (one level)")
	locs := b.locationClones(ctx)
	for _, p := range projects {
		ep := ExistingProject{ID: p.ID, Repo: p.Repo, Path: p.Path, Clones: []ExistingClone{}}
		dest, _ := filepath.Abs(expandHome(p.Path))
		seen := map[string]bool{}
		add := func(path, location string) {
			real := path
			if r, err := filepath.EvalSymlinks(path); err == nil {
				real = r
			}
			if seen[real] {
				return
			}
			seen[real] = true
			c := describeClone(ctx, real)
			c.Location = location
			c.AtPath = samePath(real, dest)
			if location != "" {
				c.Used = usedAt(real)
			}
			ep.Clones = append(ep.Clones, c)
		}
		for _, l := range locs {
			if strings.EqualFold(l.slug, p.Repo) {
				add(l.path, l.name)
			}
		}
		for _, path := range found[strings.ToLower(p.Repo)] {
			add(path, locationAt(locs, path))
		}
		ep.Note = preferClones(ep.Clones)
		res.Projects = append(res.Projects, ep)
	}
	return res
}

// locClone is a location whose path is a main checkout, with its origin.
type locClone struct{ name, path, slug string }

func (b *Box) locationClones(ctx context.Context) []locClone {
	all, _ := b.Locations.read()
	var out []locClone
	for _, s := range all {
		if k, _ := gitKind(s.Path); k != "checkout" {
			continue
		}
		if slug := slugOf(originInConfig(filepath.Join(s.Path, ".git", "config"))); slug != "" {
			out = append(out, locClone{name: s.Name, path: s.Path, slug: slug})
		}
	}
	return out
}

func locationAt(locs []locClone, path string) string {
	for _, l := range locs {
		if samePath(l.path, path) {
			return l.name
		}
	}
	return ""
}

// preferClones orders clones best first: Shipyard locations (the one at
// the team.json path, then the most recently used), then other clones (at
// the path, then the newest commit). It says why when several locations
// could be used.
func preferClones(cs []ExistingClone) string {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if (a.Location != "") != (b.Location != "") {
			return a.Location != ""
		}
		if a.AtPath != b.AtPath {
			return a.AtPath
		}
		if a.Location != "" {
			return timeOf(a.Used).After(timeOf(b.Used))
		}
		return timeOf(a.LastCommit).After(timeOf(b.LastCommit))
	})
	n := 0
	for _, c := range cs {
		if c.Location != "" {
			n++
		}
	}
	if n < 2 {
		return ""
	}
	why := "it was used most recently"
	if cs[0].AtPath {
		why = "it is at the path team.json gives it"
	}
	return fmt.Sprintf("%d Shipyard projects are clones of this repo: %s (%s) is used, since %s", n, cs[0].Location, cs[0].Display, why)
}

func timeOf(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if r, err := filepath.EvalSymlinks(home); err == nil && strings.HasPrefix(p, r+"/") {
			return "~/" + strings.TrimPrefix(p, r+"/")
		}
		if strings.HasPrefix(p, home+"/") {
			return "~/" + strings.TrimPrefix(p, home+"/")
		}
	}
	return p
}

// gitRead runs a read-only git command in dir: without git's optional
// locks, so even `git status` doesn't write the index.
func gitRead(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	return strings.TrimRight(string(out), "\n"), err
}

// describeClone reads a matched clone's branch, status and last commit.
func describeClone(ctx context.Context, path string) ExistingClone {
	c := ExistingClone{Path: path, Display: tildePath(path)}
	if br, err := gitRead(ctx, path, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		c.Branch = br
	} else if h, err := gitRead(ctx, path, "rev-parse", "--short", "HEAD"); err == nil {
		c.Head = h
	}
	if st, err := gitRead(ctx, path, "status", "--porcelain"); err == nil && st != "" {
		c.Dirty = len(strings.Split(st, "\n"))
	}
	if d, err := gitRead(ctx, path, "log", "-1", "--format=%cI"); err == nil {
		if t, err := time.Parse(time.RFC3339, d); err == nil {
			c.LastCommit = &t
		}
	}
	if n, err := gitRead(ctx, path, "rev-list", "--count", "HEAD..@{upstream}"); err == nil {
		c.Behind, _ = strconv.Atoi(n)
	}
	if wl, err := gitRead(ctx, path, "worktree", "list", "--porcelain"); err == nil {
		c.Worktrees = len(parseWorktrees([]byte(wl), path)) - 1
		if c.Worktrees < 0 {
			c.Worktrees = 0
		}
	}
	return c
}

// usedAt is when a checkout was last worked in: its index changes with
// every status, commit and checkout.
func usedAt(path string) *time.Time {
	if info, err := os.Stat(filepath.Join(path, ".git", "index")); err == nil {
		t := info.ModTime().UTC()
		return &t
	}
	return nil
}

// verifyClone checks, at setup, that the folder chosen is still a main
// checkout of repo: the page chose it from a scan, and it may have changed
// since.
func verifyClone(path, repo string) (string, error) {
	abs, err := filepath.Abs(expandHome(path))
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	switch kind, _ := gitKind(abs); kind {
	case "checkout":
	case "worktree":
		return "", fmt.Errorf("%s is a git worktree, not a main checkout of %s; choose its main checkout", path, repo)
	case "bare":
		return "", fmt.Errorf("%s is a bare repository, with no checkout to work in", path)
	default:
		return "", fmt.Errorf("%s is not a git checkout any more; choose again, or clone a fresh copy", path)
	}
	// origin as written (get-url would apply url.*.insteadOf).
	raw, _ := git(context.Background(), "-C", abs, "config", "--get", "remote.origin.url")
	origin := strings.TrimSpace(string(raw))
	if got := slugOf(origin); !strings.EqualFold(got, repo) {
		if origin == "" {
			origin = "none"
		}
		return "", fmt.Errorf("%s is no longer a clone of %s (its origin is %s); choose again, or clone a fresh copy", path, repo, origin)
	}
	return abs, nil
}

// adoptable is the clone setUpProject uses for p, and whether one was
// found: the one the page chose (re-checked), else, unless a fresh clone
// was asked for, a Shipyard location that is a clone of it.
func (b *Box) adoptable(ctx context.Context, p TeamProjectPlan) (path, location, note string, err error) {
	if p.Use != "" {
		path, err = verifyClone(p.Use, p.Repo)
		if err != nil {
			return "", "", "", err
		}
		return path, locationAt(b.locationClones(ctx), path), "", nil
	}
	if p.Fresh {
		return "", "", "", nil
	}
	dest, _ := filepath.Abs(expandHome(p.Path))
	var cs []ExistingClone
	for _, l := range b.locationClones(ctx) {
		if strings.EqualFold(l.slug, p.Repo) {
			cs = append(cs, ExistingClone{Path: l.path, Display: tildePath(l.path), Location: l.name, AtPath: samePath(l.path, dest), Used: usedAt(l.path)})
		}
	}
	if len(cs) == 0 {
		return "", "", "", nil
	}
	note = preferClones(cs)
	return cs[0].Path, cs[0].Location, note, nil
}

// markFirstOpen leaves an adopted repository's existing git worktrees to be
// set up the first time each is opened, rather than all at once now: their
// files are not touched here.
func (b *Box) markFirstOpen(ctx context.Context, location, path string) int {
	out, err := git(ctx, "-C", path, "worktree", "list", "--porcelain")
	if err != nil {
		return 0
	}
	var paths []string
	for _, w := range parseWorktrees(out, path) {
		if !w.Main {
			paths = append(paths, w.Path)
		}
	}
	if len(paths) == 0 {
		return 0
	}
	if b.Locations.SetFirstOpen(location, paths) != nil {
		return 0
	}
	return len(paths)
}

// existingRefs reads the projects to look for from a request: each
// project=ID:OWNER/REPO:PATH, else the team setup's own bundle.
func (b *Box) existingRefs(r *http.Request) ([]projectRef, error) {
	var out []projectRef
	for _, v := range r.URL.Query()["project"] {
		parts := strings.SplitN(v, ":", 3)
		if len(parts) != 3 || !validFolder(parts[0]) || !team.ValidRepo(parts[1]) {
			return nil, badRequest("project %q is not ID:OWNER/REPO:PATH", v)
		}
		path := parts[2]
		if path == "" {
			path = "~/code/" + parts[0]
		}
		if (!strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, "/")) || strings.Contains(path, "..") {
			return nil, badRequest("project %s: path %q", parts[0], path)
		}
		out = append(out, projectRef{ID: parts[0], Repo: parts[1], Path: path})
	}
	if len(out) > 100 {
		return nil, badRequest("at most 100 projects at a time")
	}
	if len(out) > 0 || b.Team == nil {
		return out, nil
	}
	tb, err := b.Team.readBundle(r.PathValue("id"))
	if err != nil {
		return nil, httpError{http.StatusNotFound, "no team setup " + r.PathValue("id") + " on this box; name the projects to look for"}
	}
	for _, p := range tb.Projects {
		out = append(out, projectRef{ID: p.ID, Repo: p.Repo, Path: p.Path})
	}
	return out, nil
}

// getExisting answers GET /v1/team/{id}/existing.
func (b *Box) getExisting(w http.ResponseWriter, r *http.Request) error {
	if !teamIDPattern.MatchString(r.PathValue("id")) {
		return badRequest("no team setup %q", r.PathValue("id"))
	}
	refs, err := b.existingRefs(r)
	if err != nil {
		return err
	}
	writeJSON(w, b.FindExisting(r.Context(), refs, nil))
	return nil
}

// pullExisting is the separate Pull a found clone that is behind offers:
// a fast-forward of its current branch only, and only when asked.
func (b *Box) pullExisting(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Path string `json:"path"`
		Repo string `json:"repo"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if !team.ValidRepo(req.Repo) {
		return badRequest("repo %q is not owner/name", req.Repo)
	}
	path, err := verifyClone(req.Path, req.Repo)
	if err != nil {
		return badRequest("%v", err)
	}
	if err := b.before(r, "team.pull", map[string]any{"team": r.PathValue("id"), "path": path, "repo": req.Repo}); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", path, "pull", "--ff-only")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return badRequest("git pull --ff-only: %s", strings.TrimSpace(lastLine(string(out))))
	}
	b.Events.Publish(events.Event{Type: "team.pulled", Box: b.Name, Data: map[string]any{"team": r.PathValue("id"), "path": path, "repo": req.Repo}})
	writeJSON(w, describeClone(r.Context(), path))
	return nil
}
