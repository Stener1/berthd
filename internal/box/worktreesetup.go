package box

import (
	"net/http"
	"slices"
)

// Worktrees made with --no-setup skip the location's setup script and its
// autostart services. They report setup_pending until `worktree setup`
// runs them; opening a session there never does (unlike firstopen.go).

// setSetupPending marks or unmarks path, a worktree of location, as waiting
// for its setup.
func (l *Locations) setSetupPending(location, path string, pending bool) error {
	return l.update(func(all []savedLocation) ([]savedLocation, error) {
		for i := range all {
			if all[i].Name != location {
				continue
			}
			at := slices.Index(all[i].SetupPending, path)
			switch {
			case pending && at < 0:
				all[i].SetupPending = append(all[i].SetupPending, path)
			case !pending && at >= 0:
				all[i].SetupPending = slices.Delete(all[i].SetupPending, at, at+1)
				if len(all[i].SetupPending) == 0 {
					all[i].SetupPending = nil
				}
			}
			return all, nil
		}
		return nil, ErrUnknownLocation
	})
}

// SetupStarted is the reply to a setup request. Since is the newest event
// number before setup began: EventsSince(Since) sees its outcome even if
// it finishes before the caller starts listening.
type SetupStarted struct {
	Location string `json:"location"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Script   string `json:"script,omitempty"`
	Since    int64  `json:"since"`
}

// setupWorktree is POST /v1/locations/{name}/worktrees/{worktree}/setup:
// it starts the location's setup script and autostart services in the
// background and clears the pending mark. Watch for worktree.setup.finished
// or worktree.setup.failed. Running it again is safe; with no setup script
// there is nothing to run.
func (b *Box) setupWorktree(w http.ResponseWriter, r *http.Request) error {
	location, name := r.PathValue("name"), r.PathValue("worktree")
	dir, err := b.Locations.Dir(r.Context(), location+"/"+name)
	if err != nil {
		return err
	}
	loc, err := b.Locations.Get(r.Context(), location)
	if err != nil {
		return err
	}
	if err := b.before(r, "worktree.setup", map[string]any{"location": location, "name": name, "path": dir}); err != nil {
		return err
	}
	if err := b.Locations.setSetupPending(location, dir, false); err != nil {
		return err
	}
	since := b.Events.Head()
	b.runSetup(origin(r), loc, dir, name)
	writeJSON(w, SetupStarted{Location: location, Name: name, Path: dir, Script: loc.Scripts.Setup, Since: since})
	return nil
}
