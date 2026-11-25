package box

import (
	"cmp"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
)

// A thread is whatever a key names: an issue, a pull request, a service. The
// box never reads a key's meaning (BRA-123, shop#12, service:api, file:a/b),
// it only compares them. Sessions hold keys two ways:
//
//   - claimed: someone said so, when the session started or with a claim verb.
//     Only this can make a session a thread's owner.
//   - mentioned: the id rule found it in a title or a worktree. A title can be
//     a guess, so this groups sessions and subscribes them, and nothing more.
//
// Claims live on a session's line, so they go wherever grouping moves it, and
// grouping's rules never write them. Records stay per box: only its own box
// sees a session end, so a caller asks each box in turn.

const (
	KeyClaimed   = "claimed"
	KeyMentioned = "mentioned"

	// routerPrefix names sessions that route work: they can claim a key, and
	// never own it.
	routerPrefix = "router-"

	DeliveryHeld       = "held"
	DeliveryTyped      = "typed"
	DeliveryCancelled  = "cancelled"
	DeliveryUndone     = "undone"
	DeliveryHandedBack = "handed_back"
	DeliveryLost       = "lost"

	// deliveriesKept is how many a box remembers; ones still held always stay.
	// They share work.json, which every change writes whole.
	deliveriesKept = 500
)

// WorkClaim says a session took a key on, and when: the newest claim wins.
type WorkClaim struct {
	Key string    `json:"key"`
	At  time.Time `json:"at"`
}

// WorkKey is one of a record's keys and how it got there.
type WorkKey struct {
	Key        string `json:"key"`
	Provenance string `json:"provenance"`
}

// WorkDelivery is one prompt sent to a session on a thread's behalf. Its text
// stays in the inbox's private file; this only says what became of it.
type WorkDelivery struct {
	ID  string `json:"id"`
	Key string `json:"key"`
	// To is the session it was sent to; Turn the turn it held or started.
	To     WorkSessionRef `json:"to"`
	Turn   string         `json:"turn,omitempty"`
	From   string         `json:"from,omitempty"`
	Source string         `json:"source,omitempty"`
	State  string         `json:"state"`
	// Retracted, on an undone one, says it never reached the agent.
	Retracted bool `json:"retracted,omitempty"`
	// Why is who handed it back's reason, or why it could not be typed.
	Why     string    `json:"why,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// ThreadMember is a session of a thread, and what it is doing now.
type ThreadMember struct {
	Box      string `json:"box,omitempty"`
	Name     string `json:"name"`
	Title    string `json:"title,omitempty"`
	Worktree string `json:"worktree,omitempty"`
	// Live says it runs, with its agent still up. A session that says it's
	// done is still live: only its ending counts.
	Live       bool      `json:"live"`
	Exited     bool      `json:"exited,omitempty"`
	Ended      time.Time `json:"ended,omitzero"`
	AgentState string    `json:"agent_state,omitempty"`
	Queued     int       `json:"queued,omitempty"`
	ClaimedAt  time.Time `json:"claimed_at,omitzero"`
	Router     bool      `json:"router,omitempty"`
	// Conversation is what a structured session's work resumes from once the
	// session has gone.
	Conversation string `json:"conversation,omitempty"`
}

// Thread is what a box knows of a key. Owner is nil with no live claimant;
// Claimants, newest first, then say who last had it. Provenance is empty when
// nothing here names the key.
type Thread struct {
	Key         string         `json:"key"`
	Provenance  string         `json:"provenance,omitempty"`
	Owner       *ThreadMember  `json:"owner"`
	Claimants   []ThreadMember `json:"claimants"`
	Subscribers []ThreadMember `json:"subscribers"`
	Record      *WorkRecord    `json:"record"`
	// Also names the other records that hold it.
	Also []string `json:"also,omitempty"`
}

// cleanThreadKeys checks keys and drops repeats. A key is opaque: it's only
// trimmed, and never changes case.
func cleanThreadKeys(keys []string) ([]string, error) {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		k, err := cleanThreadKey(k)
		if err != nil {
			return nil, err
		}
		addMissing(&out, []string{k})
	}
	return out, nil
}

func cleanThreadKey(k string) (string, error) {
	k = strings.TrimSpace(k)
	if k == "" {
		return "", badRequest("a key can't be empty")
	}
	if len(k) > workTextMax {
		return "", badRequest("a key is at most %d bytes", workTextMax)
	}
	if strings.ContainsFunc(k, unicode.IsControl) {
		return "", badRequest("a key is one line of text")
	}
	return k, nil
}

func (m WorkSession) claim(key string) (WorkClaim, bool) {
	i := slices.IndexFunc(m.Claims, func(c WorkClaim) bool { return c.Key == key })
	if i < 0 {
		return WorkClaim{}, false
	}
	return m.Claims[i], true
}

// names says a record holds a key, claimed or mentioned: what sessions join by.
func (r *WorkRecord) names(id string) bool {
	return slices.Contains(r.IssueIDs, id) || slices.ContainsFunc(r.Sessions, func(m WorkSession) bool {
		_, ok := m.claim(id)
		return ok
	})
}

// keysOf is a record's keys, each with how it got there. A key both claimed
// and mentioned is claimed.
func keysOf(r *WorkRecord) []WorkKey {
	var out []WorkKey
	for _, m := range r.Sessions {
		for _, c := range m.Claims {
			if !slices.ContainsFunc(out, func(k WorkKey) bool { return k.Key == c.Key }) {
				out = append(out, WorkKey{c.Key, KeyClaimed})
			}
		}
	}
	for _, id := range r.IssueIDs {
		if !slices.ContainsFunc(out, func(k WorkKey) bool { return k.Key == id }) {
			out = append(out, WorkKey{id, KeyMentioned})
		}
	}
	return out
}

// isThisSession says a line is this session's, not an earlier one's of the
// same name.
func (s *WorkStore) isThisSession(m WorkSession, sess Session) bool {
	return m.Name == sess.Name && s.isOurs(m) && m.sameRun(sess.Created)
}

// sameRun says a line is of the session started then, as far as it knows.
func (m WorkSession) sameRun(created time.Time) bool {
	return m.Created.IsZero() || m.Created.Equal(created)
}

// preferOpen says r is a better record to show than rec.
func preferOpen(rec, r *WorkRecord) bool {
	return rec == nil || (rec.Status == WorkClosed && r.Status != WorkClosed)
}

// lineOf is a running session's record and line, an open record's first.
func (s *WorkStore) lineOf(sess Session) (*WorkRecord, *WorkSession) {
	var rec *WorkRecord
	var line *WorkSession
	for _, r := range s.st.Records {
		for i := range r.Sessions {
			if s.isThisSession(r.Sessions[i], sess) && preferOpen(rec, r) {
				rec, line = r, &r.Sessions[i]
			}
		}
	}
	return rec, line
}

// ensureLine is lineOf, giving a session with no record one by the rules:
// a shell gets none by itself, and can still be named on a thread.
func (s *WorkStore) ensureLine(sess Session, now time.Time, evs *[]workEvent) (*WorkRecord, *WorkSession, error) {
	r, m := s.lineOf(sess)
	if r == nil {
		s.place(s.facts(sess), now, evs)
		r, m = s.lineOf(sess)
	}
	if r == nil {
		return nil, nil, httpError{http.StatusConflict, sess.Name + " has no line on a work record"}
	}
	return r, m, nil
}

// Claim has a running session take keys on. Claiming again makes it the newest
// claim, which is how a session takes a thread back. A session on its own, in
// a record nobody has touched, joins the record that already holds the key, as
// one naming it would; otherwise the two are proposed as a group.
func (s *WorkStore) Claim(sess Session, keys []string) (WorkRecord, []workEvent, error) {
	defer s.lock()()
	if s.loadErr != nil {
		return WorkRecord{}, nil, s.loadErr
	}
	now := s.now()
	claims := make([]WorkClaim, 0, len(keys))
	for _, key := range keys {
		claims = append(claims, WorkClaim{Key: key, At: now})
	}
	return s.addClaims(sess, claims, now)
}

func (s *WorkStore) addClaims(sess Session, claims []WorkClaim, now time.Time) (WorkRecord, []workEvent, error) {
	var evs []workEvent
	r, m, err := s.ensureLine(sess, now, &evs)
	if err != nil {
		return WorkRecord{}, nil, err
	}
	f := s.facts(sess)
	for _, c := range claims {
		key := c.Key
		if i := slices.IndexFunc(m.Claims, func(have WorkClaim) bool { return have.Key == key }); i >= 0 {
			m.Claims[i].At = c.At
		} else {
			m.Claims = append(m.Claims, c)
		}
		evs = append(evs, workEvent{"work.claimed", map[string]any{"id": r.ID, "key": key, "session": sess.Name}})
		hits := s.matching([]string{key}, r)
		switch {
		case len(hits) == 0:
		case len(r.Sessions) == 1 && !r.Manual && r.Status != WorkClosed:
			line := *m
			s.remove(r.ID)
			s.proposeBridge(hits[0], hits[1:], &evs)
			s.join(hits[0], line, f, "claim", now, &evs)
			r = hits[0]
			m = &r.Sessions[len(r.Sessions)-1]
		case r.Status != WorkClosed:
			s.proposeBridge(r, hits, &evs)
		}
	}
	r.Updated = now
	if err := s.save(); err != nil {
		return WorkRecord{}, nil, err
	}
	return s.view(r), evs, nil
}

// Unclaim has a session let keys go; it stays a subscriber if it was one.
func (s *WorkStore) Unclaim(sess Session, keys []string) (WorkRecord, []workEvent, error) {
	defer s.lock()()
	if s.loadErr != nil {
		return WorkRecord{}, nil, s.loadErr
	}
	r, m := s.lineOf(sess)
	if r == nil {
		return WorkRecord{}, nil, httpError{http.StatusNotFound, sess.Name + " has claimed nothing"}
	}
	var evs []workEvent
	for _, key := range keys {
		n := len(m.Claims)
		m.Claims = slices.DeleteFunc(m.Claims, func(c WorkClaim) bool { return c.Key == key })
		if len(m.Claims) != n {
			evs = append(evs, workEvent{"work.unclaimed", map[string]any{"id": r.ID, "key": key, "session": sess.Name}})
		}
	}
	if len(evs) == 0 {
		return s.view(r), nil, nil
	}
	r.Updated = s.now()
	if err := s.save(); err != nil {
		return WorkRecord{}, nil, err
	}
	return s.view(r), evs, nil
}

// Subscribe has a session follow keys without owning them.
func (s *WorkStore) Subscribe(sess Session, keys []string) (WorkRecord, []workEvent, error) {
	return s.subscription(sess, keys, "work.subscribed", func(m *WorkSession, key string) {
		m.Unsubscribed = slices.DeleteFunc(m.Unsubscribed, func(k string) bool { return k == key })
		addMissing(&m.Subscribed, []string{key})
	})
}

// Unsubscribe has a session stop following keys, for good: its title naming
// one again doesn't bring it back.
func (s *WorkStore) Unsubscribe(sess Session, keys []string) (WorkRecord, []workEvent, error) {
	return s.subscription(sess, keys, "work.unsubscribed", func(m *WorkSession, key string) {
		m.Subscribed = slices.DeleteFunc(m.Subscribed, func(k string) bool { return k == key })
		addMissing(&m.Unsubscribed, []string{key})
	})
}

func (s *WorkStore) subscription(sess Session, keys []string, event string, change func(*WorkSession, string)) (WorkRecord, []workEvent, error) {
	defer s.lock()()
	if s.loadErr != nil {
		return WorkRecord{}, nil, s.loadErr
	}
	now := s.now()
	var evs []workEvent
	r, m, err := s.ensureLine(sess, now, &evs)
	if err != nil {
		return WorkRecord{}, nil, err
	}
	for _, key := range keys {
		change(m, key)
		evs = append(evs, workEvent{event, map[string]any{"id": r.ID, "key": key, "session": sess.Name}})
	}
	r.Updated = now
	if err := s.save(); err != nil {
		return WorkRecord{}, nil, err
	}
	return s.view(r), evs, nil
}

// CarryClaims gives a conversation's new session what its last session held:
// resumed under a new name, it's the same work. What that session let go stays
// gone. Each claim keeps its time, so a session that has claimed the key since
// stays the owner.
func (s *WorkStore) CarryClaims(conversation string, sess Session) ([]workEvent, error) {
	if conversation == "" {
		return nil, nil
	}
	defer s.lock()()
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	var last *WorkSession
	for _, r := range s.st.Records {
		for i := range r.Sessions {
			m := &r.Sessions[i]
			if m.Conversation == conversation && m.Name != sess.Name && (last == nil || !m.Created.Before(last.Created)) {
				last = m
			}
		}
	}
	if last == nil || len(last.Claims) == 0 {
		return nil, nil
	}
	_, evs, err := s.addClaims(sess, slices.Clone(last.Claims), s.now())
	return evs, err
}

// Thread reads a key against the sessions running now. The owner is the newest
// live session that claimed it, leaving out routers; every other session on it
// is a subscriber. What runs isn't a line's say: a line marked ended may only
// have read a list older than its session.
func (s *WorkStore) Thread(key string, running []Session) Thread {
	defer s.lock()()
	live := make(map[string]Session, len(running))
	for _, sess := range running {
		live[sess.Name] = sess
	}
	member := func(m WorkSession) ThreadMember {
		tm := ThreadMember{Box: m.Box, Name: m.Name, Title: m.Title, Worktree: m.Worktree, Ended: m.Ended, Router: strings.HasPrefix(m.Name, routerPrefix), Conversation: m.Conversation}
		if sess, ok := live[m.Name]; ok && s.isThisSession(m, sess) {
			tm.Exited, tm.Live, tm.Ended = sess.Exited, !sess.Exited, time.Time{}
		}
		return tm
	}
	type claimant struct {
		ThreadMember
		created time.Time
		rec     *WorkRecord
	}
	var claims []claimant
	subs := []ThreadMember{}
	for _, r := range s.st.Records {
		for _, m := range r.Sessions {
			if c, ok := m.claim(key); ok {
				tm := member(m)
				tm.ClaimedAt = c.At
				claims = append(claims, claimant{tm, m.Created, r})
			} else if slices.Contains(m.Subscribed, key) {
				subs = append(subs, member(m))
			}
		}
	}
	slices.SortStableFunc(claims, func(a, b claimant) int {
		return cmp.Or(b.ClaimedAt.Compare(a.ClaimedAt), b.created.Compare(a.created), cmp.Compare(a.Name, b.Name))
	})
	t := Thread{Key: key, Claimants: []ThreadMember{}, Subscribers: subs}
	var rec *WorkRecord
	for _, c := range claims {
		t.Claimants = append(t.Claimants, c.ThreadMember)
		switch {
		case !c.Live:
		case t.Owner == nil && !c.Router:
			owner := c.ThreadMember
			t.Owner, rec = &owner, c.rec
		case !slices.ContainsFunc(t.Subscribers, func(m ThreadMember) bool { return m.Name == c.Name }):
			// Another live claimant follows the thread too.
			t.Subscribers = append(t.Subscribers, c.ThreadMember)
		}
	}
	if t.Owner != nil {
		t.Subscribers = slices.DeleteFunc(t.Subscribers, func(m ThreadMember) bool { return m.Name == t.Owner.Name })
	}
	if rec == nil && len(claims) > 0 {
		rec = claims[0].rec
	}
	for _, r := range s.st.Records {
		if rec == nil || len(claims) == 0 {
			if r.names(key) && preferOpen(rec, r) {
				rec = r
			}
		}
	}
	switch {
	case len(claims) > 0:
		t.Provenance = KeyClaimed
	case rec != nil:
		t.Provenance = KeyMentioned
	}
	if rec != nil {
		v := s.view(rec)
		t.Record = &v
	}
	for _, r := range s.st.Records {
		if r != rec && r.names(key) {
			t.Also = append(t.Also, r.ID)
		}
	}
	return t
}

func logWork(err error) { log.Printf("work: %v", err) }

func (s *WorkStore) findDelivery(id string) (*WorkDelivery, error) {
	for _, d := range s.st.Deliveries {
		if d.ID == id {
			return d, nil
		}
	}
	return nil, httpError{http.StatusNotFound, "no delivery " + id}
}

func deliveryEvent(typ string, d *WorkDelivery) workEvent {
	return workEvent{typ, map[string]any{"id": d.ID, "key": d.Key, "session": d.To.Name, "turn": d.Turn, "state": d.State, "source": d.Source}}
}

// NewDelivery keeps a prompt just sent or held.
func (s *WorkStore) NewDelivery(key, session, turn, from, source, state string) (WorkDelivery, []workEvent, error) {
	defer s.lock()()
	now := s.now()
	d := &WorkDelivery{ID: "d_" + newArtifactID(), Key: key, To: WorkSessionRef{Box: s.Box, Name: session}, Turn: turn, From: from, Source: clipWork(source), State: state, Created: now, Updated: now}
	s.st.Deliveries = append(s.st.Deliveries, d)
	s.pruneDeliveries()
	if err := s.save(); err != nil {
		return WorkDelivery{}, nil, err
	}
	return *d, []workEvent{deliveryEvent("work.delivered", d)}, nil
}

// pruneDeliveries forgets the oldest it may, once there are too many.
func (s *WorkStore) pruneDeliveries() {
	over := len(s.st.Deliveries) - deliveriesKept
	if over <= 0 {
		return
	}
	s.st.Deliveries = slices.DeleteFunc(s.st.Deliveries, func(d *WorkDelivery) bool {
		if over > 0 && d.State != DeliveryHeld {
			over--
			return true
		}
		return false
	})
}

// deliveryForTurn is the delivery a retried send made. A turn's id can come
// back after a restart under a reused name, so the newest is the one meant.
func (s *WorkStore) deliveryForTurn(turn, key string) string {
	defer s.lock()()
	for _, d := range slices.Backward(s.st.Deliveries) {
		if d.Turn == turn && d.Key == key {
			return d.ID
		}
	}
	return ""
}

// Deliveries lists them, oldest first; each filter left empty passes all.
func (s *WorkStore) Deliveries(key, session, state string) []WorkDelivery {
	defer s.lock()()
	out := []WorkDelivery{}
	for _, d := range s.st.Deliveries {
		if (key == "" || d.Key == key) && (session == "" || d.To.Name == session) && (state == "" || d.State == state) {
			out = append(out, *d)
		}
	}
	return out
}

// Delivery is one of them.
func (s *WorkStore) Delivery(id string) (WorkDelivery, error) {
	defer s.lock()()
	d, err := s.findDelivery(id)
	if err != nil {
		return WorkDelivery{}, err
	}
	return *d, nil
}

func validDeliveryState(st string) bool {
	switch st {
	case DeliveryHeld, DeliveryTyped, DeliveryCancelled, DeliveryUndone, DeliveryHandedBack, DeliveryLost:
		return true
	}
	return false
}

// Undo says a delivery's prompt shouldn't count: retracted when it was still
// held and has been taken out, or else typed, which the caller then disowns
// with a message of its own.
func (s *WorkStore) Undo(id string, retracted bool) (WorkDelivery, []workEvent, error) {
	defer s.lock()()
	d, err := s.findDelivery(id)
	if err != nil {
		return WorkDelivery{}, nil, err
	}
	switch {
	case d.State == DeliveryUndone:
		return *d, nil, nil
	case d.State == DeliveryHeld, d.State == DeliveryTyped:
	case d.State == DeliveryCancelled && retracted:
		// The ledger was read between taking it out and getting here.
	default:
		return WorkDelivery{}, nil, httpError{http.StatusConflict, id + " is " + d.State + ": there's nothing to undo"}
	}
	d.State, d.Retracted, d.Updated = DeliveryUndone, retracted, s.now()
	if err := s.save(); err != nil {
		return WorkDelivery{}, nil, err
	}
	return *d, []workEvent{deliveryEvent("work.undone", d)}, nil
}

// HandBack is the session it reached saying it isn't its work.
func (s *WorkStore) HandBack(id, why string) (WorkDelivery, []workEvent, error) {
	defer s.lock()()
	d, err := s.findDelivery(id)
	if err != nil {
		return WorkDelivery{}, nil, err
	}
	switch d.State {
	case DeliveryHandedBack:
		return *d, nil, nil
	case DeliveryTyped:
	case DeliveryHeld:
		return WorkDelivery{}, nil, httpError{http.StatusConflict, id + " is still held, so its session hasn't seen it: undo it instead"}
	default:
		return WorkDelivery{}, nil, httpError{http.StatusConflict, id + " is " + d.State + ": there's nothing to hand back"}
	}
	d.State, d.Why, d.Updated = DeliveryHandedBack, clipWork(why), s.now()
	if err := s.save(); err != nil {
		return WorkDelivery{}, nil, err
	}
	return *d, []workEvent{deliveryEvent("work.handed_back", d)}, nil
}

// heldFate is what the ledger says became of a held delivery's turn: the state
// it has moved to, the event that says so, and why. A turn the ledger has
// forgotten went with its session, as the last of a gone session is all it keeps.
func heldFate(tr Turn, known bool) (state, event, why string) {
	switch {
	case !known:
		return DeliveryLost, "work.lost", ""
	case tr.SentSeq != 0 || tr.Status == turnSentAsAnswer:
		return DeliveryTyped, "work.delivered", ""
	case tr.Status == turnCancelled:
		return DeliveryCancelled, "work.cancelled", ""
	case tr.State == "queued":
		return DeliveryHeld, "", ""
	}
	return DeliveryLost, "work.lost", clipWork(tr.Status)
}

// SyncHeld brings held deliveries up to date with the turns they wait on. The
// ledger is what knows: every way a held prompt ends passes through it, some
// with no event at all (a session gone unannounced, a prompt that can't be
// typed). Only what's held is looked at, and nothing moves back.
func (s *WorkStore) SyncHeld(turn func(string) (Turn, bool)) ([]workEvent, error) {
	defer s.lock()()
	var evs []workEvent
	for _, d := range s.st.Deliveries {
		if d.State != DeliveryHeld {
			continue
		}
		tr, known := turn(d.Turn)
		state, event, why := heldFate(tr, known)
		if state == DeliveryHeld {
			continue
		}
		d.State, d.Why, d.Updated = state, why, s.now()
		evs = append(evs, deliveryEvent(event, d))
	}
	if len(evs) == 0 {
		return nil, nil
	}
	return evs, s.save()
}

// conversationOf is a structured session's conversation id, its log folder's
// name.
func conversationOf(sess Session) string {
	if sess.conv == "" {
		return ""
	}
	return filepath.Base(sess.conv)
}

// syncDeliveries moves held deliveries on and says so. The events stand even if
// the file can't be written: they tell what happened to the prompt.
func (b *Box) syncDeliveries() {
	if b.Work == nil || b.Turns == nil {
		return
	}
	evs, err := b.Work.SyncHeld(b.Turns.Get)
	if err != nil {
		logWork(err)
	}
	b.publishWork("work", evs)
}

// noteDelivery keeps a send made on a thread's behalf, and says which; a send
// with no key keeps none. The prompt has gone by now, so a store that can't
// write loses only the record of it: that's said in the log, and the send stands.
func (b *Box) noteDelivery(req SendRequest, origin, session, turn, state string) string {
	if req.Key == "" {
		return ""
	}
	d, evs, err := b.Work.NewDelivery(req.Key, session, turn, firstNonEmpty(req.from, origin), req.Source, state)
	if err != nil {
		logWork(err)
		return ""
	}
	b.publishWork(origin, evs)
	return d.ID
}

func (b *Box) mountThreads(work func(string, func(*WorkStore, http.ResponseWriter, *http.Request) error)) {
	work("GET /v1/work/threads", b.findThread)
	work("POST /v1/work/threads/claim", b.threadVerb((*WorkStore).Claim))
	work("POST /v1/work/threads/unclaim", b.threadVerb((*WorkStore).Unclaim))
	work("POST /v1/work/threads/subscribe", b.threadVerb((*WorkStore).Subscribe))
	work("POST /v1/work/threads/unsubscribe", b.threadVerb((*WorkStore).Unsubscribe))
	work("GET /v1/work/deliveries", b.listDeliveries)
	work("GET /v1/work/deliveries/{id}", func(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
		b.syncDeliveries()
		d, err := s.Delivery(r.PathValue("id"))
		if err != nil {
			return err
		}
		writeJSON(w, d)
		return nil
	})
	work("POST /v1/work/deliveries/{id}/undo", b.undoDelivery)
	work("POST /v1/work/deliveries/{id}/hand-back", b.handBackDelivery)
}

// findThread answers GET /v1/work/threads?key=K. The key is a parameter since
// a key can hold anything a path can't. A box that can't list its sessions
// doesn't say a thread has no owner: it doesn't know.
func (b *Box) findThread(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
	key, err := cleanThreadKey(r.URL.Query().Get("key"))
	if err != nil {
		return err
	}
	all, err := b.Sessions.List(r.Context())
	if err != nil {
		return err
	}
	evs, err := s.Reconcile(all)
	if err != nil {
		return err
	}
	b.publishWork("work", evs)
	t := s.Thread(key, all)
	b.enrichThread(r, &t, all)
	writeJSON(w, t)
	return nil
}

// enrichThread adds what a thread's running sessions are doing, reading only
// those.
func (b *Box) enrichThread(r *http.Request, t *Thread, all []Session) {
	names := map[string]bool{}
	for _, m := range slices.Concat(t.Claimants, t.Subscribers) {
		names[m.Name] = true
	}
	var picked []Session
	for _, sess := range all {
		if names[sess.Name] {
			picked = append(picked, sess)
		}
	}
	state := make(map[string]Session, len(picked))
	for _, sess := range b.enrich(r.Context(), picked) {
		state[sess.Name] = sess
	}
	fill := func(m *ThreadMember) {
		if sess, ok := state[m.Name]; ok && (m.Live || m.Exited) {
			m.AgentState, m.Queued = sess.AgentState, sess.Queued
		}
	}
	if t.Owner != nil {
		fill(t.Owner)
	}
	for i := range t.Claimants {
		fill(&t.Claimants[i])
	}
	for i := range t.Subscribers {
		fill(&t.Subscribers[i])
	}
}

// threadVerb answers the verbs that put a session on a key or take it off. No
// session named is the caller's own, when it's a session on this box.
func (b *Box) threadVerb(act func(*WorkStore, Session, []string) (WorkRecord, []workEvent, error)) func(*WorkStore, http.ResponseWriter, *http.Request) error {
	return func(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
		var req struct {
			Session string   `json:"session"`
			Keys    []string `json:"keys"`
		}
		if err := decode(r, &req); err != nil {
			return err
		}
		keys, err := cleanThreadKeys(req.Keys)
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			return badRequest("say which key")
		}
		name := req.Session
		if name == "" {
			name = b.callerSession(r, "")
		}
		if name == "" {
			return badRequest("say which session")
		}
		sess, all, err := b.Sessions.getWithAll(r.Context(), name)
		if err != nil {
			return err
		}
		evs, err := s.Reconcile(all)
		if err != nil {
			return err
		}
		b.publishWork("work", evs)
		rec, evs, err := act(s, sess, keys)
		return b.replyWork(w, r, rec, evs, err)
	}
}

// claimNew claims a new session's keys. The session is running by now, so a
// failure is left to the log: a caller finding no owner starts new, which is
// what it would do with no claims at all.
func (b *Box) claimNew(r *http.Request, sess Session, keys []string) {
	if len(keys) == 0 {
		return
	}
	if err := b.reconcileWork(r.Context()); err != nil {
		logWork(err)
	}
	_, evs, err := b.Work.Claim(sess, keys)
	if err != nil {
		logWork(err)
		return
	}
	b.publishWork(origin(r), evs)
}

// carryClaims hands a conversation's claims to the new session it resumed in.
// Left out, the next caller would find no owner and start the same work again.
func (b *Box) carryClaims(r *http.Request, conversation string, sess Session) {
	if b.Work == nil {
		return
	}
	if err := b.reconcileWork(r.Context()); err != nil {
		logWork(err)
	}
	evs, err := b.Work.CarryClaims(conversation, sess)
	if err != nil {
		logWork(err)
		return
	}
	b.publishWork(origin(r), evs)
}

// checkClaims refuses what can't be claimed before anything has started.
func (b *Box) checkClaims(raw []string) ([]string, error) {
	keys, err := cleanThreadKeys(raw)
	if err != nil || len(keys) == 0 {
		return nil, err
	}
	if err := b.workReady(); err != nil {
		return nil, err
	}
	return keys, nil
}

func (b *Box) listDeliveries(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if st := q.Get("state"); st != "" && !validDeliveryState(st) {
		return badRequest("state must be held, typed, cancelled, undone, handed_back or lost")
	}
	b.syncDeliveries()
	writeJSON(w, s.Deliveries(q.Get("key"), q.Get("session"), q.Get("state")))
	return nil
}

// undoDelivery answers POST /v1/work/deliveries/{id}/undo: a prompt still held
// comes out of the inbox and is never typed. One already typed can't be, and
// the answer says which it was, in retracted.
func (b *Box) undoDelivery(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	b.syncDeliveries()
	d, err := s.Delivery(id)
	if err != nil {
		return err
	}
	retracted := false
	if d.State == DeliveryHeld {
		switch err := b.cancelHeld(d.To.Name, d.Turn); {
		case err == nil:
			retracted = true
		case err == errNotQueued, errors.Is(err, ErrUnknownSession):
			// It left the inbox just now. The ledger says how: still held means
			// it's being typed, and Undo refuses anything cancelled or lost.
			b.syncDeliveries()
		default:
			return err
		}
	}
	out, evs, err := s.Undo(id, retracted)
	if err != nil {
		return err
	}
	if retracted {
		b.publish(r, "session.unqueued", map[string]any{"name": d.To.Name, "turn": d.Turn})
	}
	return b.replyWork(w, r, out, evs, nil)
}

func (b *Box) handBackDelivery(s *WorkStore, w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Why string `json:"why"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil {
			return err
		}
	}
	b.syncDeliveries()
	out, evs, err := s.HandBack(r.PathValue("id"), req.Why)
	return b.replyWork(w, r, out, evs, err)
}
