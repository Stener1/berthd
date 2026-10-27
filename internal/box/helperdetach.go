package box

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/cosscom/shipyard/internal/events"
	"github.com/cosscom/shipyard/internal/statefile"
)

// When a worktree is removed, berthd tells each running helper that asked to
// be told (its manifest's "detach"), so the helper can close what it opened
// for the worktree: a recording proxy, what it kept under the worktree's
// name. It also forgets the worktree in its own note of which services point
// at each helper (attached.json), whether or not a helper answered.
//
// A helper is never in the way of a removal. It is told after the worktree
// has gone, in the background, with a couple of seconds to answer; one that
// is down, slow or answers an error changes nothing.

// HelperDetach is what a helper is told of a worktree that is gone: what it
// was told when a service of the worktree started (HelperAttach), without
// the service and its environment.
type HelperDetach struct {
	Box     string `json:"box"`
	Project string `json:"project"`
	// Worktree is the worktree's name; empty for the project's own checkout.
	Worktree string `json:"worktree"`
	Branch   string `json:"branch,omitempty"`
	Path     string `json:"path,omitempty"`
}

// Watch has the helpers told of every worktree that is removed, however it
// was: through the API or the CLI, when a review is cleaned up, after an
// archive script, or by git itself. Each of those ends in a worktree.removed
// event, which is what this listens for. The returned func stops it.
func (h *Helpers) Watch(bus *events.Bus) (stop func()) {
	if h == nil || bus == nil {
		return func() {}
	}
	return bus.Observe(func(e events.Event) {
		if e.Type != "worktree.removed" {
			return
		}
		location, _ := e.Data["location"].(string)
		name, _ := e.Data["name"].(string)
		if location == "" || name == "" {
			return
		}
		path, _ := e.Data["path"].(string)
		branch, _ := e.Data["branch"].(string)
		// Not here: an observer must be quick, and a helper may not be.
		h.DetachSoon(location, Worktree{Name: name, Path: path, Branch: branch})
	})
}

// DetachSoon is Detach in the background: whoever removed the worktrees
// does not wait for a helper.
func (h *Helpers) DetachSoon(location string, worktrees ...Worktree) {
	if h == nil || len(worktrees) == 0 {
		return
	}
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		// berthd is stopping, and its helpers with it: only its own note.
		h.forgetAttached(location, worktrees)
		return
	}
	h.detaching.Add(1)
	h.mu.Unlock()
	go func() {
		defer h.detaching.Done()
		h.Detach(context.Background(), location, worktrees...)
	}()
}

// Detach forgets worktrees of a location that are gone: in attached.json
// first, whatever follows, and then by telling every running helper with a
// detach path. A helper that does not answer one is not asked about the
// rest: it is down, and each would only wait as long.
func (h *Helpers) Detach(ctx context.Context, location string, worktrees ...Worktree) {
	if h == nil || len(worktrees) == 0 {
		return
	}
	h.forgetAttached(location, worktrees)
	h.mu.Lock()
	procs := make([]*helperProc, 0, len(h.procs))
	for _, p := range h.procs {
		if p.rec.Detach != "" {
			procs = append(procs, p)
		}
	}
	h.mu.Unlock()
	sort.Slice(procs, func(i, j int) bool { return procs[i].rec.Plugin < procs[j].rec.Plugin })
	// Each helper on its own, so a slow one does not hold up the others.
	var wg sync.WaitGroup
	for _, p := range procs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, wt := range worktrees {
				about := HelperDetach{Box: h.Box, Project: location, Worktree: wt.Name, Branch: wt.Branch, Path: wt.Path}
				if wt.Main {
					about.Worktree = ""
				}
				if err, gone := h.detachOne(ctx, p, about); err != nil {
					h.logf("helper %s: not told that %s/%s was removed: %v", p.rec.Plugin, location, wt.Name, err)
					if gone {
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}

// detachOne tells one helper of one worktree. gone says the helper is not
// there to be told (not running, or it did not answer at all), as against
// one that answered with an error.
func (h *Helpers) detachOne(ctx context.Context, p *helperProc, about HelperDetach) (err error, gone bool) {
	state, api := p.snapshot()
	if state != HelperRunning || api == nil {
		return errors.New("it is not running"), true
	}
	ctx, cancel := context.WithTimeout(ctx, orDuration(h.detachTimeout, 2*time.Second))
	defer cancel()
	body, err := json.Marshal(about)
	if err != nil {
		return err, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api.String()+p.rec.Detach, bytes.NewReader(body))
	if err != nil {
		return errors.New("its detach address cannot be used"), true
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := helperClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return errors.New("it did not answer in time"), true
		}
		// Without the address: the error is logged.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return err, true
	}
	defer resp.Body.Close()
	// Its answer is the helper's own business; only read so the connection
	// can be used again.
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxAttachAnswer))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return errors.New("it answered " + resp.Status), false
	}
	return nil, false
}

// attachedPlugins is the plugins berthd has a note of attached services
// for: helpers that run, and ones that were removed while services they
// changed were still running.
func (h *Helpers) attachedPlugins() []string {
	entries, err := os.ReadDir(h.Dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || !helperPlugin.MatchString(e.Name()) {
			continue
		}
		if _, err := os.Stat(h.attachedPath(e.Name())); err == nil {
			out = append(out, e.Name())
		}
	}
	return out
}

// saveAttached writes a helper's list of attached services, or removes the
// file when there are none: and the helper's folder with it, when the file
// was all that was left of a helper that is gone. attachMu is held.
func (h *Helpers) saveAttached(plugin string, list []HelperService) {
	if len(list) == 0 {
		os.Remove(h.attachedPath(plugin))
		// Only an empty folder goes: one with a helper or its data stays.
		os.Remove(h.home(plugin))
		return
	}
	if err := os.MkdirAll(h.home(plugin), 0o700); err != nil {
		return
	}
	if b, err := json.Marshal(list); err == nil {
		statefile.Write(h.attachedPath(plugin), append(b, '\n'))
	}
}

// setAttached replaces a helper's list of attached services.
func (h *Helpers) setAttached(plugin string, list []HelperService) {
	h.attachMu.Lock()
	defer h.attachMu.Unlock()
	h.saveAttached(plugin, list)
}

// forgetAttached takes worktrees that are gone out of every helper's list
// of attached services; with no worktrees, the whole location.
func (h *Helpers) forgetAttached(location string, worktrees []Worktree) {
	gone := map[string]bool{}
	for _, wt := range worktrees {
		gone[wt.Name] = true
	}
	h.attachMu.Lock()
	defer h.attachMu.Unlock()
	for _, plugin := range h.attachedPlugins() {
		before := h.readAttached(plugin)
		list := make([]HelperService, 0, len(before))
		for _, s := range before {
			if s.Location == location && (len(worktrees) == 0 || gone[s.Worktree]) {
				continue
			}
			list = append(list, s)
		}
		if len(list) != len(before) {
			h.saveAttached(plugin, list)
		}
	}
}

// startedPlain notes that a service started with no word from the helpers
// in but: ones that are off, or have nothing to say of services. A service
// that one of them changed before is as the project wrote it again, and no
// longer points at that helper.
func (h *Helpers) startedPlain(svc HelperService, but map[string]bool) {
	for _, plugin := range h.attachedPlugins() {
		if !but[plugin] {
			h.noteAttached(plugin, svc, false)
		}
	}
}

// detachLocation tells the helpers that every worktree of a location is
// gone, the project's own checkout among them: the location was removed.
func (b *Box) detachLocation(loc Location) {
	if b.Helpers == nil {
		return
	}
	// In its note the whole location goes, also a worktree git no longer
	// lists; the helpers are told of the ones that are known.
	b.Helpers.forgetAttached(loc.Name, nil)
	b.Helpers.DetachSoon(loc.Name, loc.Worktrees...)
}
