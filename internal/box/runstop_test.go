package box

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/box/runs"
	"github.com/cosscom/shipyard/internal/hooks"
)

// stopFixture is a box with a repo "cal" and its worktrees, each with an
// agent in it: a session running claude, so the box counts it as one,
// which is cat here.
type stopFixture struct {
	b      *Box
	do     func(method, path string, in, out any, header ...string) int
	repo   string
	count  string // a line per archive script run
	agents map[string]string
}

func newStopFixture(t *testing.T, script string, worktrees ...string) *stopFixture {
	t.Helper()
	t.Setenv("SHELL", "/bin/sh")
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexec cat\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	b, do := runsBox(t)
	f := &stopFixture{b: b, do: do, repo: gitRepo(t), count: filepath.Join(t.TempDir(), "archived"), agents: map[string]string{}}
	if st := do("POST", "/v1/locations", map[string]string{"name": "cal", "path": f.repo}, nil); st != 200 {
		t.Fatalf("add location: %d", st)
	}
	for _, wt := range worktrees {
		if st := do("POST", "/v1/locations/cal/worktrees", WorktreeRequest{Name: wt}, nil); st != 200 {
			t.Fatalf("add worktree %s: %d", wt, st)
		}
		f.agents[wt] = f.agent(t, wt, "agent-"+wt)
	}
	if script != "" {
		if st := do("PUT", "/v1/locations/cal/scripts", map[string]string{"archive": strings.ReplaceAll(script, "COUNT", f.count)}, nil); st != 200 {
			t.Fatalf("set scripts: %d", st)
		}
	}
	return f
}

func (f *stopFixture) agent(t *testing.T, wt, name string) string {
	t.Helper()
	var s Session
	if st := f.do("POST", "/v1/sessions", SessionRequest{Name: name, Location: "cal/" + wt, Command: "claude"}, &s); st != 200 {
		t.Fatalf("start %s in %s: %d", name, wt, st)
	}
	return s.Name
}

func (f *stopFixture) worktree(name string) Worktree {
	loc, _ := f.b.Locations.Get(context.Background(), "cal")
	for _, w := range loc.Worktrees {
		if w.Name == name {
			return w
		}
	}
	return Worktree{}
}

func (f *stopFixture) item(t *testing.T, name string) map[string]string {
	t.Helper()
	w := f.worktree(name)
	if w.Path == "" {
		t.Fatalf("no worktree %s", name)
	}
	return map[string]string{"location": "cal", "name": name, "path": w.Path}
}

func (f *stopFixture) start(t *testing.T, sessions []string, worktrees []map[string]string) runs.Summary {
	t.Helper()
	var s runs.Summary
	if st := f.do("POST", "/v1/runs", RunRequest{Template: "stop", Params: map[string]any{"sessions": sessions, "worktrees": worktrees}}, &s); st != 202 {
		t.Fatalf("start stop run: %d", st)
	}
	return s
}

func (f *stopFixture) archives() int {
	b, _ := os.ReadFile(f.count)
	return strings.Count(string(b), "\n")
}

func (f *stopFixture) live(t *testing.T) map[string]bool {
	t.Helper()
	all, err := f.b.Sessions.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, s := range all {
		if !s.Exited {
			out[s.Name] = true
		}
	}
	return out
}

// stepAt finds a step by its path in a run's tree.
func stepAt(steps []runs.StepRun, path string) (runs.StepRun, bool) {
	for _, s := range steps {
		if s.Path == path {
			return s, true
		}
		if strings.HasPrefix(path, s.Path+".") {
			return stepAt(s.Children, path)
		}
	}
	return runs.StepRun{}, false
}

// A stop run stops its agents, then archives each worktree where that's
// safe: one with uncommitted work, one where another agent still runs and
// one whose agent the box wouldn't stop are kept as they are, with why.
// Branches stay; a session or worktree already gone counts as done.
func TestAStopRunArchivesOnlyWhatIsSafe(t *testing.T) {
	f := newStopFixture(t, "", "clean", "dirty", "shared", "held")
	os.WriteFile(filepath.Join(f.worktree("dirty").Path, "notes.txt"), []byte("wip"), 0o600)
	other := f.agent(t, "shared", "outsider")
	cfg := filepath.Join(t.TempDir(), "hooks.json")
	os.WriteFile(cfg, []byte(`{"hooks":[{"on":"before:session.stop","run":"if grep -q agent-held; then echo held back; exit 1; fi"}]}`), 0o600)
	f.b.Hooks = &hooks.Runner{Path: cfg}

	sessions := []string{f.agents["clean"], f.agents["dirty"], f.agents["shared"], f.agents["held"], "never-was"}
	items := []map[string]string{f.item(t, "clean"), f.item(t, "dirty"), f.item(t, "shared"), f.item(t, "held"), {"location": "cal", "name": "gone-already", "path": "/nowhere"}}
	s := f.start(t, sessions, items)
	r := waitRunStatus(t, f.b, s.ID, runs.Failed, runs.Succeeded)

	want := map[string][2]string{
		"0.i0.0": {runs.Succeeded, "stopped"},
		"0.i3.0": {runs.Failed, ""},
		"0.i4.0": {runs.Succeeded, "already stopped"},
		"1.i0.0": {runs.Succeeded, stopArchived},
		"1.i1.0": {runs.Skipped, keptDirty},
		"1.i2.0": {runs.Skipped, keptOtherAgent},
		"1.i3.0": {runs.Skipped, keptStopFailed},
		"1.i4.0": {runs.Succeeded, "already gone"},
	}
	for path, w := range want {
		st, ok := stepAt(r.Steps, path)
		if !ok || st.Status != w[0] || (w[1] != "" && st.Output != w[1]) {
			t.Errorf("step %s = %+v, want %s %q", path, st, w[0], w[1])
		}
	}
	if st, _ := stepAt(r.Steps, "0.i3.0"); !strings.Contains(st.Error, "held back") {
		t.Errorf("the refused stop says %q", st.Error)
	}
	if f.worktree("clean").Path != "" {
		t.Error("the clean worktree is still there")
	}
	for _, kept := range []string{"dirty", "shared", "held"} {
		if f.worktree(kept).Path == "" {
			t.Errorf("%s was archived", kept)
		}
	}
	if out, err := git(context.Background(), "-C", f.repo, "branch", "--list", "clean"); err != nil || !strings.Contains(string(out), "clean") {
		t.Errorf("the archived worktree's branch went: %q %v", out, err)
	}
	live := f.live(t)
	if !live[other] || !live[f.agents["held"]] || live[f.agents["clean"]] || live[f.agents["dirty"]] {
		t.Errorf("live sessions after the run: %v", live)
	}
}

// berthd stopping in the middle of a worktree's archive script: the
// resumed run goes on from there and never runs a script twice.
func TestAResumedStopRunNeverArchivesTwice(t *testing.T) {
	f := newStopFixture(t, "echo run >> COUNT; sleep 1", "one", "two")
	dir := filepath.Join(t.TempDir(), "runs")
	ctx, stop := context.WithCancel(context.Background())
	f.b.NewRuns(dir, 0, 0, nil)
	f.b.Runs.Resume(ctx)

	s := f.start(t, []string{f.agents["one"], f.agents["two"]}, []map[string]string{f.item(t, "one"), f.item(t, "two")})
	deadline := time.Now().Add(10 * time.Second)
	for f.archives() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	f.b.Runs.Wait()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); f.b.Runs.Wait() })
	f.b.NewRuns(dir, 0, 0, nil)
	f.b.Runs.Resume(ctx)
	waitRunStatus(t, f.b, s.ID, runs.Succeeded)
	deadline = time.Now().Add(10 * time.Second)
	for (f.worktree("one").Path != "" || f.worktree("two").Path != "") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if f.worktree("one").Path != "" || f.worktree("two").Path != "" {
		t.Fatal("a worktree is still there")
	}
	if n := f.archives(); n != 2 {
		t.Fatalf("the archive script ran %d times for two worktrees", n)
	}
}

// Two runs archiving the same worktree: the second waits for the first's
// archive and says how it went, rather than running the script again.
func TestADuplicateStopRunJoinsTheArchiveInFlight(t *testing.T) {
	f := newStopFixture(t, "echo run >> COUNT; sleep 1", "dup")
	item := f.item(t, "dup")
	a := f.start(t, []string{f.agents["dup"]}, []map[string]string{item})
	deadline := time.Now().Add(10 * time.Second)
	for f.archives() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	b := f.start(t, []string{f.agents["dup"]}, []map[string]string{item})
	for _, id := range []string{a.ID, b.ID} {
		r := waitRunStatus(t, f.b, id, runs.Succeeded)
		if st, _ := stepAt(r.Steps, "1.i0.0"); st.Output != stopArchived && st.Output != "already gone" {
			t.Errorf("run %s's archive = %+v", id, st)
		}
	}
	if n := f.archives(); n != 1 {
		t.Fatalf("the archive script ran %d times", n)
	}
}

// Cancelling a stop run: the archive in flight finishes on the box, and
// the worktrees not yet started are left as they are.
func TestCancellingAStopRunSkipsWhatHasNotStarted(t *testing.T) {
	f := newStopFixture(t, "echo run >> COUNT; sleep 1", "first", "second", "third")
	s := f.start(t, []string{f.agents["first"], f.agents["second"], f.agents["third"]}, []map[string]string{f.item(t, "first"), f.item(t, "second"), f.item(t, "third")})
	deadline := time.Now().Add(10 * time.Second)
	for f.archives() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if st := f.do("POST", "/v1/runs/"+s.ID+"/cancel", nil, nil); st != 200 {
		t.Fatalf("cancel: %d", st)
	}
	r := waitRunStatus(t, f.b, s.ID, runs.Cancelled)
	if st, _ := stepAt(r.Steps, "1.i0.0"); st.Output != stopArchiving {
		t.Errorf("the archive in flight = %+v", st)
	}
	for _, p := range []string{"1.i1.0", "1.i2.0"} {
		if st, ok := stepAt(r.Steps, p); ok {
			t.Errorf("step %s ran after the cancel: %+v", p, st)
		}
	}
	deadline = time.Now().Add(10 * time.Second)
	for f.worktree("first").Path != "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if f.worktree("first").Path != "" || f.worktree("second").Path == "" || f.worktree("third").Path == "" {
		t.Fatalf("after the cancel: first %q, second %q, third %q", f.worktree("first").Path, f.worktree("second").Path, f.worktree("third").Path)
	}
	if n := f.archives(); n != 1 {
		t.Fatalf("the archive script ran %d times", n)
	}
}
