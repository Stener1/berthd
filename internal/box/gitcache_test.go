package box

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// countGit puts a git first on PATH that logs each call and runs the real
// one, and returns a function that says which calls were made since it was
// last asked.
func countGit(t *testing.T) func() []string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		b, _ := os.ReadFile(log)
		os.Remove(log)
		return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(b)), " ", "_"))
	}
}

func ranOnly(t *testing.T, calls []string, want ...string) {
	t.Helper()
	for _, c := range calls {
		ok := false
		for _, w := range want {
			ok = ok || strings.Contains(c, w)
		}
		if !ok {
			t.Errorf("ran git %s, want only %v", strings.ReplaceAll(c, "_", " "), want)
		}
	}
	if len(want) > 0 && len(calls) == 0 {
		t.Errorf("ran no git, want %v", want)
	}
}

func TestListingLocationsAgainRunsNoGitUntilSomethingChanges(t *testing.T) {
	repo := gitRepo(t)
	ctx := context.Background()
	l := NewLocations(filepath.Join(t.TempDir(), "locations.json"))
	if _, err := l.Add(ctx, "cal", repo); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "remote", "add", "origin", "https://example.com/acme/cal.git")
	calls := countGit(t)

	list := func() Location {
		t.Helper()
		all, err := l.List(ctx)
		if err != nil || len(all) != 1 {
			t.Fatalf("List = %+v, %v", all, err)
		}
		return all[0]
	}
	loc := list()
	if len(calls()) == 0 {
		t.Fatal("the first List ran no git")
	}
	if loc.Slug != "acme/cal" || loc.DefaultBranch != "main" || len(loc.Worktrees) != 1 {
		t.Fatalf("first List = %+v", loc)
	}
	for range 3 {
		list()
		l.Get(ctx, "cal")
		l.worktreePaths(ctx)
	}
	ranOnly(t, calls())

	// A commit moves HEAD: only the worktree list is asked again.
	before := loc.Worktrees[0].Head
	os.WriteFile(filepath.Join(repo, "more"), []byte("x"), 0o644)
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "more")
	calls()
	loc = list()
	ranOnly(t, calls(), "worktree_list")
	if loc.Worktrees[0].Head == before {
		t.Errorf("Head is still %s after a commit", before)
	}

	// A worktree made outside Shipyard shows up, and goes when removed.
	other := filepath.Join(filepath.Dir(repo), "cal-side")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "side", other)
	calls()
	if loc = list(); len(loc.Worktrees) != 2 || loc.Worktrees[1].Branch != "side" {
		t.Fatalf("after worktree add: %+v", loc.Worktrees)
	}
	ranOnly(t, calls(), "worktree_list")

	// A commit inside the linked worktree moves its HEAD.
	before = loc.Worktrees[1].Head
	os.WriteFile(filepath.Join(other, "side"), []byte("x"), 0o644)
	gitIn(t, other, "add", ".")
	gitIn(t, other, "commit", "-q", "-m", "side")
	calls()
	if loc = list(); loc.Worktrees[1].Head == before {
		t.Errorf("the linked worktree's Head is still %s after a commit in it", before)
	}

	// Switching its branch, and locking it.
	gitIn(t, other, "checkout", "-q", "-b", "side-2")
	gitIn(t, repo, "worktree", "lock", "--reason", "busy", other)
	calls()
	if loc = list(); loc.Worktrees[1].Branch != "side-2" || !loc.Worktrees[1].Locked {
		t.Errorf("after checkout and lock: %+v", loc.Worktrees[1])
	}
	gitIn(t, repo, "worktree", "unlock", other)
	gitIn(t, repo, "worktree", "remove", other)
	calls()
	if loc = list(); len(loc.Worktrees) != 1 {
		t.Fatalf("after worktree remove: %+v", loc.Worktrees)
	}

	// A new remote URL and a default branch from origin's HEAD.
	gitIn(t, repo, "remote", "set-url", "origin", "git@example.com:acme/shop.git")
	gitIn(t, repo, "update-ref", "refs/remotes/origin/trunk", "HEAD")
	gitIn(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	calls()
	if loc = list(); loc.Slug != "acme/shop" || loc.DefaultBranch != "trunk" {
		t.Errorf("after set-url and origin/HEAD: slug %q, default %q", loc.Slug, loc.DefaultBranch)
	}
	calls()
	list()
	ranOnly(t, calls())
}

func TestGitAnswersAreAskedAgainOnceTheyAreOld(t *testing.T) {
	repo := gitRepo(t)
	calls := countGit(t)
	remoteURL(context.Background(), repo)
	remoteURL(context.Background(), repo)
	if c := calls(); len(c) != 1 {
		t.Fatalf("two remoteURLs ran git %d times, want 1", len(c))
	}
	old := gitFactsMaxAge
	gitFactsMaxAge = 0
	t.Cleanup(func() { gitFactsMaxAge = old })
	remoteURL(context.Background(), repo)
	ranOnly(t, calls(), "remote_get-url")
}

func TestALocationThatIsALinkedWorktreeIsCachedByItsRepository(t *testing.T) {
	repo := gitRepo(t)
	side := filepath.Join(filepath.Dir(repo), "cal-side")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "side", side)
	g, ok := gitDirsOf(side)
	if !ok || g.common != filepath.Join(repo, ".git") || g.git != filepath.Join(repo, ".git", "worktrees", "cal-side") {
		t.Fatalf("gitDirsOf(linked worktree) = %+v, %v", g, ok)
	}
	if _, ok := gitDirsOf(t.TempDir()); ok {
		t.Fatal("a plain folder has git dirs")
	}
}

// A reftable repository keeps a linked worktree's HEAD in that worktree's
// own reftable, not in a file the main checkout's stamp covers.
func TestAReftableRepositorysLinkedWorktreeIsAskedAgainWhenItSwitches(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "cal")
	if out, err := exec.Command("git", "init", "-q", "--ref-format=reftable", "-b", "main", repo).CombinedOutput(); err != nil {
		t.Skipf("this git has no reftable: %s", out)
	}
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	gitIn(t, repo, "branch", "other")
	side := filepath.Join(root, "cal-side")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "side", side)
	ctx := context.Background()
	branchOf := func() string {
		out, _ := worktreeList(ctx, repo)
		for _, w := range parseWorktrees(out, repo) {
			if w.Path == side {
				return w.Branch
			}
		}
		return ""
	}
	if b := branchOf(); b != "side" {
		t.Fatalf("branch = %q, want side", b)
	}
	if b := defaultBranch(ctx, side); b != "side" {
		t.Fatalf("default branch of the linked worktree = %q, want side", b)
	}
	gitIn(t, side, "checkout", "-q", "other")
	if b := branchOf(); b != "other" {
		t.Errorf("after checkout in the linked worktree, branch = %q, want other", b)
	}
	if b := defaultBranch(ctx, side); b != "other" {
		t.Errorf("after checkout, its default branch = %q, want other", b)
	}
}
