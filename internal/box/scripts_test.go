package box

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/events"
)

// repoConfig writes the repository's own .berth/config.json.
func repoConfig(t *testing.T, repo, setup, archive string) {
	t.Helper()
	os.MkdirAll(filepath.Join(repo, ".berth"), 0o755)
	b, _ := json.Marshal(RepoConfig{Setup: setup, Archive: archive})
	if err := os.WriteFile(filepath.Join(repo, RepoConfigFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScriptsComeFromTheRepoUnlessTheLocationSetsItsOwn(t *testing.T) {
	repo := gitRepo(t)
	repoConfig(t, repo, "echo repo-setup", "echo repo-archive")
	ctx := context.Background()
	l := NewLocations(filepath.Join(t.TempDir(), "locations.json"))
	loc, err := l.Add(ctx, "cal", repo)
	if err != nil {
		t.Fatal(err)
	}
	if loc.Scripts != (Scripts{}) || loc.RepoTrust != RepoTrustUntrusted {
		t.Fatalf("untrusted scripts = %+v (%s), want none", loc.Scripts, loc.RepoTrust)
	}
	trustRepo(t, l, "cal")
	loc, _ = l.Get(ctx, "cal")
	if loc.Scripts != (Scripts{Setup: "echo repo-setup", Archive: "echo repo-archive", From: "repo"}) {
		t.Fatalf("scripts = %+v, want the repository's", loc.Scripts)
	}
	if err := l.SetScripts("cal", "echo own", ""); err != nil {
		t.Fatal(err)
	}
	loc, _ = l.Get(ctx, "cal")
	// Each script layers on its own: the box's setup, the repository's
	// teardown.
	if loc.Scripts != (Scripts{Setup: "echo own", Archive: "echo repo-archive", From: "berth"}) {
		t.Fatalf("scripts = %+v, want the location's setup over the repository's", loc.Scripts)
	}
	l.SetScripts("cal", "", "")
	loc, _ = l.Get(ctx, "cal")
	if loc.Scripts.From != "repo" {
		t.Fatalf("clearing did not fall back to the repository: %+v", loc.Scripts)
	}
}

func waitFor(t *testing.T, ch <-chan events.Event, typ string) events.Event {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Type == typ {
				return e
			}
		case <-deadline:
			t.Fatalf("never saw %s", typ)
		}
	}
}

func TestSetupRunsInTheNewWorktreeWithOrcaCompatibleEnvironment(t *testing.T) {
	repo := gitRepo(t)
	t.Setenv("SHELL", "/bin/sh")
	repoConfig(t, repo, `echo "$ORCA_ROOT_PATH|$ORCA_WORKTREE_PATH|$ORCA_WORKSPACE_NAME" > setup-ran`, "")
	ctx := context.Background()
	bus := &events.Bus{}
	ch, stop := bus.Subscribe()
	defer stop()
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(t.TempDir(), "locations.json")), Events: bus, LogDir: t.TempDir()}
	b.Locations.Add(ctx, "cal", repo)
	trustRepo(t, b.Locations, "cal")
	loc, _ := b.Locations.Get(ctx, "cal")
	wt, err := b.Locations.CreateWorktree(ctx, "cal", "billing", "", "")
	if err != nil {
		t.Fatal(err)
	}
	go b.lifecycle("berth", "setup", loc, wt.Path, wt.Name, loc.Scripts.Setup, nil)
	waitFor(t, ch, "worktree.setup.finished")
	got, err := os.ReadFile(filepath.Join(wt.Path, "setup-ran"))
	if err != nil {
		t.Fatal(err)
	}
	if want := repo + "|" + wt.Path + "|billing"; strings.TrimSpace(string(got)) != want {
		t.Fatalf("setup saw %q, want %q", got, want)
	}
}

func TestArchiveRunsBeforeRemovalAndAFailureKeepsTheWorktree(t *testing.T) {
	repo := gitRepo(t)
	t.Setenv("SHELL", "/bin/sh")
	ctx := context.Background()
	bus := &events.Bus{}
	ch, stop := bus.Subscribe()
	defer stop()
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(t.TempDir(), "locations.json")), Events: bus, LogDir: t.TempDir()}
	loc, _ := b.Locations.Add(ctx, "cal", repo)
	wt, _ := b.Locations.CreateWorktree(ctx, "cal", "billing", "", "")

	b.lifecycle("berth", "archive", loc, wt.Path, wt.Name, "echo dropping shop_billing; echo could not drop shop_billing >&2; exit 3", func() error {
		return b.Locations.RemoveWorktree(ctx, "cal", "billing", true)
	})
	failed := waitFor(t, ch, "worktree.archive.failed")
	if !strings.Contains(failed.Error, "log:") {
		t.Fatalf("failure does not point at the log: %q", failed.Error)
	}
	// What the script said comes with it, for the app's Details.
	if !strings.Contains(failed.Error, "could not drop shop_billing") || !strings.Contains(failed.Error, "dropping shop_billing") {
		t.Fatalf("failure does not carry the script's output: %q", failed.Error)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatal("a failed archive still removed the worktree")
	}

	b.lifecycle("berth", "archive", loc, wt.Path, wt.Name, "true", func() error {
		return b.Locations.RemoveWorktree(ctx, "cal", "billing", true)
	})
	waitFor(t, ch, "worktree.archive.finished")
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatal("a successful archive did not remove the worktree")
	}
}

func TestADirtyWorktreeIsRefusedBeforeItsArchiveScriptRuns(t *testing.T) {
	repo := gitRepo(t)
	t.Setenv("SHELL", "/bin/sh")
	ctx := context.Background()
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(t.TempDir(), "locations.json")), Events: &events.Bus{}, LogDir: t.TempDir()}
	loc, _ := b.Locations.Add(ctx, "cal", repo)
	wt, _ := b.Locations.CreateWorktree(ctx, "cal", "billing", "", "")
	if err := os.WriteFile(filepath.Join(wt.Path, "notes.txt"), []byte("not committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ran := filepath.Join(t.TempDir(), "ran")
	loc.Scripts.Archive = "touch " + ran

	async, err := b.dropWorktree("app", loc, wt.Name, wt.Path, "", false, "removed", nil)
	// git's own words, as its refusal would have been: the app keys on them.
	if err == nil || async || !strings.Contains(err.Error(), "contains modified or untracked files") {
		t.Fatalf("a dirty worktree was not refused up front: async=%v err=%v", async, err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(ran); !os.IsNotExist(err) {
		t.Fatal("the archive script ran on a dirty worktree")
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "notes.txt")); err != nil {
		t.Fatal("the uncommitted file is gone")
	}
}
