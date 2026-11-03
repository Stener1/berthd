package box

import (
	"context"
	"net/http"

	"github.com/cosscom/shipyard/internal/events"
)

// The work API: /v1/work lists, makes and changes work records (work.go).

func (b *Box) mountWork(route func(string, func(http.ResponseWriter, *http.Request) error)) {
	work := func(pattern string, h func(*WorkStore, http.ResponseWriter, *http.Request) error) {
		route(pattern, func(w http.ResponseWriter, r *http.Request) error {
			if b.Work == nil {
				return httpError{http.StatusNotImplemented, "this box keeps no work records"}
			}
			if err := b.Work.ready(); err != nil {
				return httpError{http.StatusServiceUnavailable, err.Error()}
			}
			return h(b.Work, w, r)
		})
	}
	work("GET /v1/work", b.listWork)
	work("POST /v1/work", b.createWork)
	work("GET /v1/work/settings", b.getWorkSettings)
	work("PUT /v1/work/settings", b.putWorkSettings)
	work("GET /v1/work/pins", b.listWorkPins)
	work("POST /v1/work/pins", b.addWorkPin)
	work("DELETE /v1/work/pins", b.removeWorkPin)
	work("GET /v1/work/{id}", b.getWork)
	work("PATCH /v1/work/{id}", b.updateWork)
	work("POST /v1/work/{id}/group", b.groupWork)
	work("POST /v1/work/{id}/ungroup", b.ungroupWork)
	work("POST /v1/work/{id}/accept", func(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
		rec, evs, err := s.Accept(r.PathValue("id"))
		return b.replyWork(w, r, rec, evs, err)
	})
	work("POST /v1/work/{id}/reject", func(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
		rec, evs, err := s.Reject(r.PathValue("id"))
		return b.replyWork(w, r, rec, evs, err)
	})
	// An explicit visit from the app.
	work("POST /v1/work/{id}/seen", func(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
		rec, evs, err := s.Seen(r.PathValue("id"))
		return b.replyWork(w, r, rec, evs, err)
	})
}

// replyWork ends a change: what it owes the journal, then its answer.
func (b *Box) replyWork(w http.ResponseWriter, r *http.Request, v any, evs []workEvent, err error) error {
	if err != nil {
		return err
	}
	b.publishWork(origin(r), evs)
	writeJSON(w, v)
	return nil
}

func (b *Box) publishWork(from string, evs []workEvent) {
	for _, e := range evs {
		b.Events.Publish(events.Event{Type: e.Type, Box: b.Name, Origin: from, Data: e.Data})
	}
}

// reconcileWork brings the records up to date with the box's sessions.
func (b *Box) reconcileWork(ctx context.Context) error {
	all, err := b.Sessions.List(ctx)
	if err != nil {
		return err
	}
	evs, err := b.Work.Reconcile(all)
	if err != nil {
		return err
	}
	b.publishWork("work", evs)
	return nil
}

// checkWorkSessions says a request's sessions on this box exist: one on
// another box is that box's to know.
func (b *Box) checkWorkSessions(ctx context.Context, refs []WorkSessionRef) error {
	if len(refs) == 0 {
		return nil
	}
	all, err := b.Sessions.List(ctx)
	if err != nil {
		return err
	}
	running := make(map[string]bool, len(all))
	for _, sess := range all {
		running[sess.Name] = true
	}
	for _, ref := range refs {
		if !sessionName.MatchString(ref.Name) {
			return badRequest("invalid session name %q", ref.Name)
		}
		if (ref.Box == "" || ref.Box == b.Name) && !running[ref.Name] {
			return ErrUnknownSession
		}
	}
	return nil
}

func (b *Box) listWork(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
	status := r.URL.Query().Get("status")
	if status != "" && !validWorkStatus(status) {
		return badRequest("status must be open, parked, waiting or closed")
	}
	// Reads what's running now; a box whose sessions can't be listed still
	// has its records.
	_ = b.reconcileWork(r.Context())
	writeJSON(w, s.List(status, r.URL.Query().Get("all") == "1"))
	return nil
}

func (b *Box) getWork(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
	_ = b.reconcileWork(r.Context())
	rec, err := s.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, rec)
	return nil
}

func (b *Box) createWork(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
	var req WorkInput
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := b.checkWorkSessions(r.Context(), req.Sessions); err != nil {
		return err
	}
	rec, evs, err := s.Create(req)
	return b.replyWork(w, r, rec, evs, err)
}

func (b *Box) updateWork(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
	var req WorkPatch
	if err := decode(r, &req); err != nil {
		return err
	}
	rec, evs, err := s.Update(r.PathValue("id"), req)
	return b.replyWork(w, r, rec, evs, err)
}

func (b *Box) groupWork(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
	var req struct {
		With     []string         `json:"with"`
		Sessions []WorkSessionRef `json:"sessions"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := b.checkWorkSessions(r.Context(), req.Sessions); err != nil {
		return err
	}
	rec, evs, err := s.Group(r.PathValue("id"), req.With, req.Sessions)
	return b.replyWork(w, r, rec, evs, err)
}

func (b *Box) ungroupWork(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Sessions []string `json:"sessions"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	recs, evs, err := s.Ungroup(r.PathValue("id"), req.Sessions)
	return b.replyWork(w, r, recs, evs, err)
}

func (b *Box) listWorkPins(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, s.Pins())
	return nil
}

func (b *Box) addWorkPin(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
	var req struct{ Kind, Target string }
	if err := decode(r, &req); err != nil {
		return err
	}
	pin, evs, err := s.Pin(req.Kind, req.Target)
	return b.replyWork(w, r, pin, evs, err)
}

func (b *Box) removeWorkPin(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
	evs, err := s.Unpin(r.URL.Query().Get("kind"), r.URL.Query().Get("target"))
	return b.replyWork(w, r, map[string]bool{"removed": len(evs) > 0}, evs, err)
}

func (b *Box) getWorkSettings(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, s.Settings())
	return nil
}

func (b *Box) putWorkSettings(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
	var req WorkSettings
	if err := decode(r, &req); err != nil {
		return err
	}
	out, err := s.SetSettings(req)
	return b.replyWork(w, r, out, []workEvent{{"work.settings", map[string]any{"id_pattern": out.IDPattern != ""}}}, err)
}
