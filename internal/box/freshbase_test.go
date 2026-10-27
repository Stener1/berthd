package box

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// staleClone makes an origin, a clone of it as the location "app", and then
// a commit on origin's main the clone hasn't fetched.
func staleClone(t *testing.T) (*Locations, string, string) {
	t.Helper()
	origin := gitRepo(t)
	clone := filepath.Join(t.TempDir(), "app")
	gitIn(t, filepath.Dir(clone), "clone", "-q", origin, clone)
	l := NewLocations(filepath.Join(t.TempDir(), "locations.json"))
	if _, err := l.Add(context.Background(), "app", clone); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(origin, "upstream.txt"), []byte("theirs"), 0o644)
	gitIn(t, origin, "add", ".")
	gitIn(t, origin, "commit", "-q", "-m", "Upstream change")
	return l, origin, clone
}

func revOf(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", ref).CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse %s: %s", ref, out)
	}
	return strings.TrimSpace(string(out))
}

func TestANewBranchStartsFromOriginsMainAndMainCatchesUp(t *testing.T) {
	l, origin, clone := staleClone(t)
	wt, err := l.CreateWorktree(context.Background(), "app", "feature", "", "main")
	if err != nil {
		t.Fatal(err)
	}
	want := revOf(t, origin, "main")
	if got := revOf(t, wt.Path, "HEAD"); got != want {
		t.Fatalf("worktree started at %s, want origin's %s", got, want)
	}
	if got := revOf(t, clone, "main"); got != want {
		t.Fatalf("local main = %s, want it fast-forwarded to %s", got, want)
	}
	if _, err := os.Stat(filepath.Join(clone, "upstream.txt")); err != nil {
		t.Fatal("the main checkout's files didn't move with its branch")
	}
	if r := wt.MainRefresh; r == nil || r.Status != "updated" || r.Behind != 1 || r.Branch != "main" {
		t.Fatalf("refresh = %+v, want main updated, 1 behind", r)
	}
}

func TestNoBaseMeansTheMainCheckoutsBranchKeptCurrent(t *testing.T) {
	l, origin, _ := staleClone(t)
	wt, err := l.CreateWorktree(context.Background(), "app", "feature", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := revOf(t, wt.Path, "HEAD"), revOf(t, origin, "main"); got != want {
		t.Fatalf("worktree started at %s, want origin's %s", got, want)
	}
}

func TestAMainCheckoutWithChangesIsLeftAlone(t *testing.T) {
	l, origin, clone := staleClone(t)
	stale := revOf(t, clone, "main")
	os.WriteFile(filepath.Join(clone, "README"), []byte("my edit"), 0o644)
	wt, err := l.CreateWorktree(context.Background(), "app", "feature", "", "main")
	if err != nil {
		t.Fatal(err)
	}
	if got := revOf(t, clone, "main"); got != stale {
		t.Fatalf("local main moved under uncommitted changes: %s", got)
	}
	if b, _ := os.ReadFile(filepath.Join(clone, "README")); string(b) != "my edit" {
		t.Fatalf("the edit is gone: %q", b)
	}
	// The new branch still starts from what origin has.
	if got, want := revOf(t, wt.Path, "HEAD"), revOf(t, origin, "main"); got != want {
		t.Fatalf("worktree started at %s, want origin's %s", got, want)
	}
	if out, err := exec.Command("git", "-C", wt.Path, "rev-parse", "--abbrev-ref", "@{upstream}").CombinedOutput(); err == nil {
		t.Fatalf("branch from origin/main tracks %s; it should wait for its first push", out)
	}
	if r := wt.MainRefresh; r == nil || r.Status != "changes" {
		t.Fatalf("refresh = %+v, want changes", r)
	}
}

func TestLocalCommitsOnMainAreKept(t *testing.T) {
	l, _, clone := staleClone(t)
	os.WriteFile(filepath.Join(clone, "mine.txt"), []byte("mine"), 0o644)
	gitIn(t, clone, "add", ".")
	gitIn(t, clone, "commit", "-q", "-m", "Local only")
	mine := revOf(t, clone, "main")
	wt, err := l.CreateWorktree(context.Background(), "app", "feature", "", "main")
	if err != nil {
		t.Fatal(err)
	}
	if got := revOf(t, clone, "main"); got != mine {
		t.Fatalf("local main moved off its own commit: %s", got)
	}
	if got := revOf(t, wt.Path, "HEAD"); got != mine {
		t.Fatalf("worktree started at %s, want local main %s", got, mine)
	}
	if r := wt.MainRefresh; r == nil || r.Status != "ahead" {
		t.Fatalf("refresh = %+v, want ahead", r)
	}
}

func TestMainNobodyHasCheckedOutMovesToo(t *testing.T) {
	l, origin, clone := staleClone(t)
	gitIn(t, clone, "switch", "-q", "-c", "elsewhere")
	wt, err := l.CreateWorktree(context.Background(), "app", "feature", "", "main")
	if err != nil {
		t.Fatal(err)
	}
	want := revOf(t, origin, "main")
	if got := revOf(t, clone, "main"); got != want {
		t.Fatalf("local main = %s, want %s", got, want)
	}
	if got := revOf(t, wt.Path, "HEAD"); got != want {
		t.Fatalf("worktree started at %s, want %s", got, want)
	}
}

func TestKeepMainOffLeavesMainAsItIs(t *testing.T) {
	l, _, clone := staleClone(t)
	off := false
	if err := l.SetLocalConfig("app", RepoConfig{KeepMain: &off}); err != nil {
		t.Fatal(err)
	}
	stale := revOf(t, clone, "main")
	wt, err := l.CreateWorktree(context.Background(), "app", "feature", "", "main")
	if err != nil {
		t.Fatal(err)
	}
	if got := revOf(t, wt.Path, "HEAD"); got != stale || wt.MainRefresh != nil {
		t.Fatalf("worktree at %s with refresh %+v, want local main %s untouched", got, wt.MainRefresh, stale)
	}
}

func TestAFetchThatFailsStillMakesTheWorktree(t *testing.T) {
	l, origin, clone := staleClone(t)
	stale := revOf(t, clone, "main")
	os.RemoveAll(origin)
	wt, err := l.CreateWorktree(context.Background(), "app", "feature", "", "main")
	if err != nil {
		t.Fatal(err)
	}
	if got := revOf(t, wt.Path, "HEAD"); got != stale {
		t.Fatalf("worktree started at %s, want local main %s", got, stale)
	}
	if r := wt.MainRefresh; r == nil || r.Status != "failed" {
		t.Fatalf("refresh = %+v, want failed", r)
	}
}

func TestOtherBasesAreNotFetched(t *testing.T) {
	l, _, clone := staleClone(t)
	gitIn(t, clone, "branch", "release")
	wt, err := l.CreateWorktree(context.Background(), "app", "feature", "", "release")
	if err != nil {
		t.Fatal(err)
	}
	if wt.MainRefresh != nil {
		t.Fatalf("refresh = %+v for a base that isn't the default branch", wt.MainRefresh)
	}
}
