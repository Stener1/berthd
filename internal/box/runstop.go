package box

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/cosscom/shipyard/internal/box/runs"
	"github.com/cosscom/shipyard/internal/events"
)

// The stop template's leaves (runs/templates/stop.json): the app's bulk
// Stop, done by the box, so it carries on while the laptop sleeps. The words
// a worktree's step ends with are the ones the app shows on its row
// (app/src/lib/removing.ts, archive-after-stop.ts).
const (
	stopArchived   = "Archived; its branch is kept"
	stopArchiving  = "Archiving: its archive script runs, then it goes"
	keptDirty      = "Kept: uncommitted changes"
	keptStopFailed = "Kept: an agent there didn't stop"
	keptOtherAgent = "Kept: another agent is still running there"
)

func kept(why string) runs.Result { return runs.Result{Status: runs.Skipped, Out: why} }

// stopSession ends one session as DELETE /v1/sessions/{name} does, its
// session.stop gate included. One already gone is done. A cancelled run
// still finishes the stop it is in.
func (h *runHost) stopSession(ctx context.Context, x *runs.StepCtx) runs.Result {
	b := h.b
	name := target(x)
	if name == "" {
		return fail(errors.New("no session to stop"))
	}
	ctx = context.WithoutCancel(ctx)
	if err := b.beforeAs(ctx, origOf(x), "session.stop", map[string]any{"name": name}); err != nil {
		return fail(err)
	}
	if err := b.Sessions.Kill(ctx, name); err != nil {
		if errors.Is(err, ErrUnknownSession) {
			return runs.Result{Status: runs.Succeeded, Out: "already stopped"}
		}
		return fail(err)
	}
	b.Events.Publish(events.Event{Type: "session.stopped", Box: b.Name, Origin: origOf(x), Data: map[string]any{"name": name, "run": x.Run.ID}})
	return runs.Result{Status: runs.Succeeded, Out: "stopped"}
}

// archiveWorktree archives the worktree a stop run's item names, where it's
// safe: never forced and its branch kept, so a worktree with uncommitted
// changes stays, as does one where an agent still runs, which archiving
// would end (one of the run's whose stop failed, or anyone else's). Plain
// shells close with it, as when archiving by hand. One already gone is
// done; one already on its way out is waited for, not archived twice
// (dropWorktree).
func (h *runHost) archiveWorktree(ctx context.Context, x *runs.StepCtx) runs.Result {
	b := h.b
	location, name, path := x.Vars["item.location"], x.Vars["item.name"], x.Vars["item.path"]
	loc, err := b.Locations.Get(ctx, location)
	if errors.Is(err, ErrUnknownLocation) {
		return runs.Result{Status: runs.Succeeded, Out: "already gone"}
	}
	if err != nil {
		return fail(err)
	}
	var wt Worktree
	for _, w := range loc.Worktrees {
		if w.Name == name {
			wt = w
		}
	}
	if wt.Path == "" || (path != "" && wt.Path != path) {
		return runs.Result{Status: runs.Succeeded, Out: "already gone"}
	}
	if wt.Main {
		return fail(errors.New("the main checkout is never archived"))
	}
	all, err := b.Sessions.List(ctx)
	if err != nil {
		return fail(err)
	}
	batch := stopBatch(x.Vars["params.sessions"])
	why := ""
	for _, s := range all {
		if s.Exited || agentFor(s) == "" || (s.Dir != wt.Path && !strings.HasPrefix(s.Dir, wt.Path+string(filepath.Separator))) {
			continue
		}
		if batch[s.Name] {
			why = keptStopFailed
			break
		}
		why = keptOtherAgent
	}
	if why != "" {
		return kept(why)
	}
	if dirtyWorktree(wt.Path) {
		return kept(keptDirty)
	}
	if err := b.beforeAs(ctx, origOf(x), "worktree.remove", map[string]any{"location": loc.Name, "name": wt.Name, "path": wt.Path}); err != nil {
		return fail(err)
	}
	b.own(wt.Path)
	done := make(chan error, 1)
	if _, err := b.dropWorktree(origOf(x), loc, wt.Name, wt.Path, "", false, "archived", func(err error) { done <- err }); err != nil {
		if strings.Contains(err.Error(), "modified or untracked") {
			return kept(keptDirty)
		}
		return fail(err)
	}
	select {
	case err := <-done:
		if err != nil {
			return fail(err)
		}
		return runs.Result{Status: runs.Succeeded, Out: stopArchived}
	case <-ctx.Done():
		// Cancelled, or berthd is stopping: the archive goes on without the
		// run, and a resumed run's next ask joins it (dropWorktree).
		return runs.Result{Status: runs.Succeeded, Out: stopArchiving}
	}
}

// stopBatch is the run's sessions, by name: given as names or as
// {session, label, aside}.
func stopBatch(param string) map[string]bool {
	var items []any
	json.Unmarshal([]byte(param), &items)
	out := map[string]bool{}
	for _, it := range items {
		switch v := it.(type) {
		case string:
			out[v] = true
		case map[string]any:
			if s, ok := v["session"].(string); ok {
				out[s] = true
			}
		}
	}
	return out
}
