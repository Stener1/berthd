package box

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNoSetupSkipsSetupUntilAskedFor(t *testing.T) {
	c, bus := servedBox(t)
	seen, stop := bus.Subscribe()
	defer stop()
	repo := gitRepo(t)
	call(t, c, "POST", "/v1/locations", "", map[string]string{"name": "cal", "path": repo}, nil)
	if status := call(t, c, "PUT", "/v1/locations/cal/scripts", "", map[string]string{"setup": "echo ran >> setup-count"}, nil); status != 200 {
		t.Fatalf("set scripts: %d", status)
	}

	var wt Worktree
	if status := call(t, c, "POST", "/v1/locations/cal/worktrees", "", WorktreeRequest{Name: "quick", NoSetup: true}, &wt); status != 200 || !wt.SetupPending {
		t.Fatalf("add worktree: %d %+v, want setup_pending", status, wt)
	}
	waitFor(t, seen, "worktree.created")
	// A session starting there does not set it up either.
	call(t, c, "POST", "/v1/sessions", "", map[string]string{"location": "cal/quick", "command": "cat"}, nil)
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(wt.Path, "setup-count")); err == nil {
		t.Fatal("setup ran for a worktree made with no_setup")
	}
	listed := func() Worktree {
		var all []Location
		call(t, c, "GET", "/v1/locations", "", nil, &all)
		for _, w := range all[0].Worktrees {
			if w.Name == "quick" {
				return w
			}
		}
		t.Fatal("worktree not listed")
		return Worktree{}
	}
	if !listed().SetupPending {
		t.Fatal("locations does not show setup_pending")
	}

	var started SetupStarted
	if status := call(t, c, "POST", "/v1/locations/cal/worktrees/quick/setup", "", nil, &started); status != 200 || started.Name != "quick" {
		t.Fatalf("setup: %d %+v", status, started)
	}
	waitFor(t, seen, "worktree.setup.finished")
	if b, err := os.ReadFile(filepath.Join(wt.Path, "setup-count")); err != nil || string(b) != "ran\n" {
		t.Fatalf("setup-count = %q %v", b, err)
	}
	if listed().SetupPending {
		t.Fatal("setup_pending stays after setup ran")
	}
	// Setup is safe to repeat.
	call(t, c, "POST", "/v1/locations/cal/worktrees/quick/setup", "", nil, nil)
	waitFor(t, seen, "worktree.setup.finished")
}

func TestSetupRouteDoesNotShadowPauseActions(t *testing.T) {
	c, _ := servedBox(t)
	repo := gitRepo(t)
	call(t, c, "POST", "/v1/locations", "", map[string]string{"name": "cal", "path": repo}, nil)
	call(t, c, "POST", "/v1/locations/cal/worktrees", "", WorktreeRequest{Name: "w", NoSetup: true}, nil)
	// No Paused hook on this box: pauseAction answers 501, so it still got the request.
	if status := call(t, c, "POST", "/v1/locations/cal/worktrees/w/pause", "", nil, nil); status != 501 {
		t.Fatalf("pause: %d, want 501 from pauseAction", status)
	}
	// With no setup script, setup succeeds and runs nothing.
	var started SetupStarted
	if status := call(t, c, "POST", "/v1/locations/cal/worktrees/w/setup", "", nil, &started); status != 200 || started.Script != "" {
		t.Fatalf("setup without a script: %d %+v", status, started)
	}
	if status := call(t, c, "POST", "/v1/locations/cal/worktrees/nope/setup", "", nil, nil); status == 200 {
		t.Fatal("setup of an unknown worktree succeeded")
	}
}
