package box

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/events"
)

func TestReviewListsAFinishedAgentsChangesAndCommits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states := &Turns{}
	bus := &events.Bus{}
	states.Attach(bus)
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(t.TempDir(), "locations.json")), Events: bus, Sessions: testSessions(t), Turns: states}
	repo := gitRepo(t)
	if _, err := b.Locations.Add(ctx, "cal", repo); err != nil {
		t.Fatal(err)
	}
	wt, err := b.Locations.CreateWorktree(ctx, "cal", "billing", "", "")
	if err != nil {
		t.Fatal(err)
	}
	quiet, err := b.Locations.CreateWorktree(ctx, "cal", "quiet", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// The agent committed once, then left an edit and a new file.
	os.WriteFile(filepath.Join(wt.Path, "charge.go"), []byte("package billing\n\nfunc Charge() {}\n"), 0o644)
	gitIn(t, wt.Path, "add", "charge.go")
	gitIn(t, wt.Path, "commit", "-q", "-m", "Add a charge stub")
	os.WriteFile(filepath.Join(wt.Path, "README"), []byte("hi\nthere\n"), 0o644)
	os.WriteFile(filepath.Join(wt.Path, "retry.go"), []byte("a\nb\nc"), 0o644)

	fake := filepath.Join(t.TempDir(), "claude")
	os.WriteFile(fake, []byte("#!/bin/sh\nexec cat\n"), 0o755)
	ends := map[string]events.Event{}
	for _, w := range []Worktree{wt, quiet} {
		if _, err := b.Sessions.Create(ctx, "agent-"+w.Name, "cal/"+w.Name, w.Path, fake, nil); err != nil {
			t.Fatal(err)
		}
		ends[w.Name] = bus.Publish(events.Event{Type: "agent.finished", Data: map[string]any{"path": w.Path}})
	}
	deadline := time.Now().Add(5 * time.Second)
	var items []ReviewItem
	for time.Now().Before(deadline) {
		items, err = b.Review(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v, want only the worktree with changes", items)
	}
	it := items[0]
	if it.Worktree != "billing" || it.Session != "agent-billing" || it.Agent != "claude" || it.AgentState != "finished" {
		t.Fatalf("item = %+v", it)
	}
	if it.Base != "main" || it.BaseAhead != 1 || len(it.Commits) != 1 || it.Commits[0].Subject != "Add a charge stub" {
		t.Fatalf("commits: base %q ahead %d %+v", it.Base, it.BaseAhead, it.Commits)
	}
	if len(it.Committed) != 1 || it.Committed[0].Path != "charge.go" || it.Committed[0].Added != 3 {
		t.Fatalf("committed = %+v", it.Committed)
	}
	files := map[string]ReviewFile{}
	for _, f := range it.Files {
		files[f.Path] = f
	}
	if files["README"].Code != " M" || files["README"].Added != 2 || files["README"].Removed != 1 {
		t.Fatalf("README = %+v", files["README"])
	}
	if files["retry.go"].Code != "??" || files["retry.go"].Added != 3 {
		t.Fatalf("retry.go = %+v", files["retry.go"])
	}
	if it.Added != 5 || it.Removed != 1 {
		t.Fatalf("totals +%d -%d", it.Added, it.Removed)
	}

	// Turn check read its finished turn as work still going on (a deploy to
	// watch): not ready to review yet, unless all is asked for.
	bus.Publish(events.Event{Type: "agent.ready", Data: map[string]any{"session": "agent-billing", "path": wt.Path, "source": "turncheck", "choice": "working", "seq": ends["billing"].Seq}})
	if items, err := b.Review(ctx, false); err != nil || len(items) != 0 {
		t.Fatalf("unfinished work is up for review: %+v %v", items, err)
	}
	if items, err := b.Review(ctx, true); err != nil || len(items) != 1 {
		t.Fatalf("all: %+v %v", items, err)
	}
}

func TestPorcelainAndNameStatusParsing(t *testing.T) {
	var it ReviewItem
	files := parsePorcelain([]byte("## fix...origin/fix [ahead 2, behind 1]\x00R  new.go\x00old.go\x00?? a b.txt\x00"), &it)
	if it.Upstream != "origin/fix" || it.Ahead != 2 || it.Behind != 1 || it.Branch != "fix" {
		t.Fatalf("branch = %+v", it)
	}
	if len(files) != 2 || files[0].From != "old.go" || files[0].Path != "new.go" || files[1].Path != "a b.txt" {
		t.Fatalf("files = %+v", files)
	}
	applyNumstat(files, []byte("4\t1\t{old.go => new.go}\n"))
	if files[0].Added != 4 {
		t.Fatalf("rename numstat = %+v", files[0])
	}
	ns := parseNameStatus([]byte("M\x00a.go\x00R087\x00x.go\x00y.go\x00A\x00z.go\x00"))
	if len(ns) != 3 || ns[1].From != "x.go" || ns[1].Path != "y.go" || ns[2].Code != "A " {
		t.Fatalf("name-status = %+v", ns)
	}
}
