package box

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/events"
)

func claimOK(t *testing.T, s *WorkStore, sess Session, keys ...string) WorkRecord {
	t.Helper()
	rec, _, err := s.Claim(sess, keys)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func reconcileOK(t *testing.T, s *WorkStore, sessions ...Session) []workEvent {
	t.Helper()
	evs, err := s.Reconcile(sessions)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func keyProvenance(r WorkRecord, key string) string {
	i := slices.IndexFunc(r.Keys, func(k WorkKey) bool { return k.Key == key })
	if i < 0 {
		return ""
	}
	return r.Keys[i].Provenance
}

func ownerName(t Thread) string {
	if t.Owner == nil {
		return ""
	}
	return t.Owner.Name
}

// A title can be a guess: it groups and it subscribes, and it never owns.
func TestATitleOnlyMentionsAKeyAndNeverOwnsIt(t *testing.T) {
	s := newTestWork(t)
	guess := agentSession("guess", "shop/a", "🔨 X-7 titler's guess")
	rng := agentSession("sweep", "shop/b", "X-1..5 sweep")
	reconcileOK(t, s, guess, rng)
	all := []Session{guess, rng}
	if p := keyProvenance(recordOf(t, s, "guess"), "X-7"); p != KeyMentioned {
		t.Errorf("a title's id = %q, want mentioned", p)
	}
	for _, id := range []string{"X-1", "X-3", "X-5"} {
		th := s.Thread(id, all)
		if th.Owner != nil || len(th.Claimants) != 0 {
			t.Errorf("%s from a range title has an owner: %+v", id, th)
		}
		if th.Provenance != KeyMentioned || th.Record == nil || th.Record.Title != "X-1..5 sweep" {
			t.Errorf("%s: %+v", id, th)
		}
		if len(th.Subscribers) != 1 || th.Subscribers[0].Name != "sweep" {
			t.Errorf("a session naming %s subscribes to it: %+v", id, th.Subscribers)
		}
	}
	if th := s.Thread("X-7", all); th.Owner != nil {
		t.Errorf("a guessed title made an owner: %+v", th.Owner)
	}
}

func TestJoiningByTheIDRuleSubscribesAndOnlyAClaimOwns(t *testing.T) {
	s := newTestWork(t)
	owner := agentSession("owner", "shop/a", "the work")
	reconcileOK(t, s, owner)
	rec := claimOK(t, s, owner, "X-1")
	if p := keyProvenance(rec, "X-1"); p != KeyClaimed {
		t.Fatalf("X-1 = %q, want claimed", p)
	}
	later := agentSession("later", "shop/b", "X-1 another look")
	later.Created = owner.Created.Add(time.Hour)
	reconcileOK(t, s, owner, later)
	if recordOf(t, s, "later").ID != rec.ID {
		t.Fatalf("a session naming a claimed key should join its record: %+v", s.List("", true))
	}
	th := s.Thread("X-1", []Session{owner, later})
	if ownerName(th) != "owner" {
		t.Errorf("owner = %q: newer, but it only named the key", ownerName(th))
	}
	if len(th.Subscribers) != 1 || th.Subscribers[0].Name != "later" {
		t.Errorf("subscribers: %+v", th.Subscribers)
	}
	if keyProvenance(recordOf(t, s, "later"), "X-1") != KeyClaimed {
		t.Errorf("a mention can't take a claim's provenance away")
	}
}

func TestFollowDissolvingALoneRecordLeavesItsClaims(t *testing.T) {
	s := newTestWork(t)
	first := agentSession("first", "shop/a", "X-1 first")
	lone := agentSession("lone", "shop/b", "no ids yet")
	reconcileOK(t, s, first, lone)
	was := recordOf(t, s, "lone").ID
	claimOK(t, s, lone, "service:api")
	if recordOf(t, s, "lone").ID != was {
		t.Fatalf("no one else holds service:api: it stays where it is")
	}
	// Its title comes to name another record's id: follow dissolves its record.
	lone.Title = "X-1 now"
	reconcileOK(t, s, first, lone)
	rec := recordOf(t, s, "lone")
	if rec.ID == was || rec.ID != recordOf(t, s, "first").ID {
		t.Fatalf("follow should have moved it: %+v", s.List("", true))
	}
	if keyProvenance(rec, "service:api") != KeyClaimed {
		t.Errorf("the claim didn't move with it: %+v", rec.Keys)
	}
	th := s.Thread("service:api", []Session{first, lone})
	if ownerName(th) != "lone" {
		t.Errorf("owner = %q, want lone", ownerName(th))
	}
	// And it didn't become first's, or X-1's.
	if th := s.Thread("X-1", []Session{first, lone}); th.Owner != nil {
		t.Errorf("following made an owner of X-1: %+v", th.Owner)
	}
}

func TestGroupingNeverMovesAClaimOffItsSession(t *testing.T) {
	s := newTestWork(t)
	a, b := agentSession("a", "shop/a", "one"), agentSession("b", "shop/b", "two")
	reconcileOK(t, s, a, b)
	claimOK(t, s, a, "k")
	ra, rb := recordOf(t, s, "a"), recordOf(t, s, "b")
	if _, _, err := s.Group(rb.ID, []string{ra.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if ownerName(s.Thread("k", []Session{a, b})) != "a" {
		t.Errorf("after grouping")
	}
	if _, _, err := s.Ungroup(rb.ID, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if ownerName(s.Thread("k", []Session{a, b})) != "a" {
		t.Errorf("after ungrouping")
	}
	if _, _, err := s.Update(recordOf(t, s, "b").ID, WorkPatch{Issue: ptr("k")}); err != nil {
		t.Fatal(err)
	}
	if ownerName(s.Thread("k", []Session{a, b})) != "a" {
		t.Errorf("an issue set by hand moved the claim")
	}
}

func ptr[T any](v T) *T { return &v }

func TestTheOwnerIsTheNewestLiveClaimant(t *testing.T) {
	const key = "shop#12"
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	mk := func(name, title string, after time.Duration) Session {
		sess := agentSession(name, "shop/"+name, title)
		sess.Created = at.Add(after)
		return sess
	}
	t.Run("no claimant, no owner", func(t *testing.T) {
		s := newTestWork(t)
		a := mk("a", "the work", 0)
		reconcileOK(t, s, a)
		th := s.Thread(key, []Session{a})
		if th.Owner != nil || th.Record != nil || th.Provenance != "" {
			t.Errorf("%+v", th)
		}
	})
	t.Run("newest wins, an older one follows", func(t *testing.T) {
		s := newTestWork(t)
		build, tail := mk("build", "build", 0), mk("tail", "tail", time.Minute)
		all := []Session{build, tail}
		reconcileOK(t, s, all...)
		claimOK(t, s, build, key)
		claimOK(t, s, tail, key)
		th := s.Thread(key, all)
		if ownerName(th) != "tail" {
			t.Fatalf("owner = %q", ownerName(th))
		}
		if len(th.Subscribers) != 1 || th.Subscribers[0].Name != "build" || len(th.Claimants) != 2 || th.Claimants[0].Name != "tail" {
			t.Errorf("%+v", th)
		}
		// A session can take it back by claiming again.
		claimOK(t, s, build, key)
		if got := ownerName(s.Thread(key, all)); got != "build" {
			t.Errorf("after build claims again, owner = %q", got)
		}
	})
	t.Run("a tail wins over its killed build, and the ended one is still named", func(t *testing.T) {
		s := newTestWork(t)
		tail, build := mk("tail", "tail", 0), mk("build", "build", time.Minute)
		reconcileOK(t, s, tail, build)
		claimOK(t, s, tail, key)
		claimOK(t, s, build, key)
		reconcileOK(t, s, tail) // build is gone
		th := s.Thread(key, []Session{tail})
		if ownerName(th) != "tail" {
			t.Fatalf("owner = %q", ownerName(th))
		}
		if len(th.Claimants) != 2 || th.Claimants[0].Name != "build" || th.Claimants[0].Live || th.Claimants[0].Ended.IsZero() {
			t.Errorf("the newest claimant has ended, and says so: %+v", th.Claimants)
		}
		if len(th.Subscribers) != 0 {
			t.Errorf("an ended claimant follows nothing: %+v", th.Subscribers)
		}
	})
	t.Run("routers are skipped", func(t *testing.T) {
		s := newTestWork(t)
		w, router := mk("worker", "w", 0), mk("router-drop-1", "r", time.Minute)
		all := []Session{w, router}
		reconcileOK(t, s, all...)
		claimOK(t, s, w, key)
		claimOK(t, s, router, key)
		th := s.Thread(key, all)
		if ownerName(th) != "worker" {
			t.Errorf("owner = %q", ownerName(th))
		}
		if th.Claimants[0].Name != "router-drop-1" || !th.Claimants[0].Router {
			t.Errorf("%+v", th.Claimants)
		}
		only := s.Thread(key, []Session{router})
		if only.Owner != nil {
			t.Errorf("a router alone owns nothing: %+v", only.Owner)
		}
	})
	t.Run("a done title is still live", func(t *testing.T) {
		s := newTestWork(t)
		done := mk("done", "✅ SHIPPED X-1 the work", 0)
		reconcileOK(t, s, done)
		claimOK(t, s, done, key)
		if got := ownerName(s.Thread(key, []Session{done})); got != "done" {
			t.Errorf("owner = %q", got)
		}
	})
	t.Run("an agent that ended isn't live, and a resumed one is again", func(t *testing.T) {
		s := newTestWork(t)
		a := mk("a", "a", 0)
		reconcileOK(t, s, a)
		claimOK(t, s, a, key)
		a.Exited = true
		th := s.Thread(key, []Session{a})
		if th.Owner != nil || !th.Claimants[0].Exited || th.Claimants[0].Live {
			t.Errorf("%+v", th)
		}
		a.Exited = false
		if got := ownerName(s.Thread(key, []Session{a})); got != "a" {
			t.Errorf("owner = %q", got)
		}
	})
	t.Run("a new session under an old name didn't claim anything", func(t *testing.T) {
		s := newTestWork(t)
		a := mk("a", "a", 0)
		reconcileOK(t, s, a)
		claimOK(t, s, a, key)
		again := mk("a", "a", time.Hour)
		if th := s.Thread(key, []Session{again}); th.Owner != nil {
			t.Errorf("before a reconcile: %+v", th.Owner)
		}
		reconcileOK(t, s, again)
		if th := s.Thread(key, []Session{again}); th.Owner != nil || len(th.Claimants) != 0 {
			t.Errorf("after one: %+v", th)
		}
	})
}

func TestAClaimAtStartJoinsTheThreadsRecord(t *testing.T) {
	s := newTestWork(t)
	first := agentSession("first", "shop/a", "first")
	reconcileOK(t, s, first)
	rec := claimOK(t, s, first, "k")
	reconcileOK(t, s) // first ended
	// A plain command gets no record by itself; a claim gives it one.
	second := Session{Name: "second", Location: "shop", Created: first.Created.Add(time.Hour)}
	got := claimOK(t, s, second, "k")
	if got.ID != rec.ID {
		t.Errorf("a new claimant should join the record: %s, %s", got.ID, rec.ID)
	}
	if got.Keys[0].Provenance != KeyClaimed || len(got.Sessions) != 2 {
		t.Errorf("%+v", got)
	}
	if owner := ownerName(s.Thread("k", []Session{second})); owner != "second" {
		t.Errorf("owner = %q", owner)
	}
}

func TestAClaimOnAManualRecordSuggestsAGroupInstead(t *testing.T) {
	s := newTestWork(t)
	a, b := agentSession("a", "shop/a", "a"), agentSession("b", "shop/b", "b")
	reconcileOK(t, s, a, b)
	claimOK(t, s, a, "k")
	rb := recordOf(t, s, "b")
	if _, _, err := s.Update(rb.ID, WorkPatch{Title: ptr("mine")}); err != nil {
		t.Fatal(err)
	}
	got := claimOK(t, s, b, "k")
	if got.ID != rb.ID || !slices.Contains(got.SuggestedWith, recordOf(t, s, "a").ID) {
		t.Errorf("%+v", got)
	}
}

func TestSubscribingAndUnsubscribing(t *testing.T) {
	s := newTestWork(t)
	a := agentSession("a", "shop/a", "X-1 notes")
	all := []Session{a}
	reconcileOK(t, s, all...)
	if subs := s.Thread("X-1", all).Subscribers; len(subs) != 1 {
		t.Fatalf("%+v", subs)
	}
	if _, _, err := s.Unsubscribe(a, []string{"X-1"}); err != nil {
		t.Fatal(err)
	}
	reconcileOK(t, s, all...)
	if subs := s.Thread("X-1", all).Subscribers; len(subs) != 0 {
		t.Errorf("its title brought it back: %+v", subs)
	}
	if _, _, err := s.Subscribe(a, []string{"shop#3", "X-1"}); err != nil {
		t.Fatal(err)
	}
	reconcileOK(t, s, all...)
	th := s.Thread("shop#3", all)
	if len(th.Subscribers) != 1 || th.Owner != nil {
		t.Errorf("%+v", th)
	}
	if len(s.Thread("X-1", all).Subscribers) != 1 {
		t.Errorf("subscribing again didn't take")
	}
	// A shell has no record by itself, and can still subscribe.
	sh := Session{Name: "sh", Location: "shop", Created: a.Created}
	if _, _, err := s.Subscribe(sh, []string{"shop#3"}); err != nil {
		t.Fatal(err)
	}
	if len(s.Thread("shop#3", []Session{a, sh}).Subscribers) != 2 {
		t.Errorf("%+v", s.Thread("shop#3", []Session{a, sh}).Subscribers)
	}
}

func TestUnclaimLetsAKeyGo(t *testing.T) {
	s := newTestWork(t)
	a := agentSession("a", "shop/a", "a")
	reconcileOK(t, s, a)
	claimOK(t, s, a, "k")
	_, evs, err := s.Unclaim(a, []string{"k", "never-claimed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Type != "work.unclaimed" {
		t.Errorf("%+v", evs)
	}
	if th := s.Thread("k", []Session{a}); th.Owner != nil || th.Provenance != "" {
		t.Errorf("%+v", th)
	}
}

func TestCarryClaimsToAConversationsNewSession(t *testing.T) {
	s := newTestWork(t)
	old := agentSession("old", "shop/a", "a")
	old.conv = "/conv/c-1"
	reconcileOK(t, s, old)
	claimOK(t, s, old, "k")
	reconcileOK(t, s)
	fresh := agentSession("fresh", "shop/a", "a")
	fresh.conv = "/conv/c-1"
	fresh.Created = old.Created.Add(time.Hour)
	reconcileOK(t, s, fresh)
	if evs, err := s.CarryClaims("c-1", fresh); err != nil || !slices.Contains(eventTypes(evs), "work.claimed") {
		t.Fatalf("carried %v, %v", evs, err)
	}
	th := s.Thread("k", []Session{fresh})
	if ownerName(th) != "fresh" {
		t.Errorf("owner = %q", ownerName(th))
	}
	if evs, _ := s.CarryClaims("c-other", fresh); len(evs) != 0 {
		t.Errorf("another conversation has nothing to carry: %v", evs)
	}
	if th.Claimants[1].Conversation != "c-1" {
		t.Errorf("an ended claimant keeps what it resumes from: %+v", th.Claimants)
	}
}

func TestCarryingClaimsTakesTheLastSessionsAndKeepsTheirTimes(t *testing.T) {
	s := newTestWork(t)
	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	run := func(name string, after time.Duration) Session {
		sess := agentSession(name, "shop/"+name, "the work")
		sess.conv, sess.Created = "/conv/c-1", start.Add(after)
		return sess
	}
	a := run("a", 0)
	reconcileOK(t, s, a)
	claimOK(t, s, a, "kept", "let-go", "taken")
	reconcileOK(t, s)

	b := run("b", time.Hour)
	reconcileOK(t, s, b)
	if _, err := s.CarryClaims("c-1", b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Unclaim(b, []string{"let-go"}); err != nil {
		t.Fatal(err)
	}
	reconcileOK(t, s)

	// Someone else claims one of them while the conversation sleeps.
	other := agentSession("other", "shop/other", "other work")
	other.Created = start.Add(2 * time.Hour)
	reconcileOK(t, s, other)
	claimOK(t, s, other, "taken")

	c := run("c", 3*time.Hour)
	reconcileOK(t, s, other, c)
	if _, err := s.CarryClaims("c-1", c); err != nil {
		t.Fatal(err)
	}
	all := []Session{other, c}
	if got := ownerName(s.Thread("kept", all)); got != "c" {
		t.Errorf("kept: owner = %q, want the resumed session", got)
	}
	if got := ownerName(s.Thread("let-go", all)); got != "" {
		t.Errorf("let-go: owner = %q: b let it go, so it stays gone", got)
	}
	if got := ownerName(s.Thread("taken", all)); got != "other" {
		t.Errorf("taken: owner = %q: a resume mustn't take back what's since been claimed", got)
	}
}

func TestARetriedSendFindsItsOwnDelivery(t *testing.T) {
	s := newTestWork(t)
	old := heldDelivery(t, s, "a", "a#1")
	fresh := heldDelivery(t, s, "a", "a#1")
	if got := s.deliveryForTurn("a#1", "k"); got != fresh.ID || got == old.ID {
		t.Errorf("a reused turn id should find the newest: %s", got)
	}
	if got := s.deliveryForTurn("a#1", "other"); got != "" {
		t.Errorf("another key's: %s", got)
	}
}

// oldWorkFile is a work.json as the release before claims wrote it.
const oldWorkFile = `{
  "records": [
    {"id": "w_old", "title": "Earlier work", "sessions": [{"box": "acme-box", "name": "a", "title": "X-1 work", "created": "2026-03-01T10:00:00Z", "last_seen": "2026-03-01T11:00:00Z"}],
     "status": "open", "issue": "X-1", "issue_ids": ["X-1"], "created": "2026-03-01T10:00:00Z", "updated": "2026-03-01T10:00:00Z"}
  ],
  "pins": [{"kind": "project", "target": "Big launch", "at": "2026-03-01T10:00:00Z"}],
  "cursor": 7
}
`

func TestAnOlderFileLoadsWithNoClaims(t *testing.T) {
	s := newTestWork(t)
	if err := os.WriteFile(s.Path, []byte(oldWorkFile), 0o600); err != nil {
		t.Fatal(err)
	}
	a := agentSession("a", "shop/a", "X-1 work")
	a.Created = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	all := []Session{a}
	r, err := s.Get("w_old")
	if err != nil {
		t.Fatal(err)
	}
	if r.Title != "Earlier work" || len(r.Sessions[0].Claims) != 0 || s.Cursor() != 7 || len(s.Pins()) != 1 {
		t.Errorf("%+v", r)
	}
	if p := keyProvenance(r, "X-1"); p != KeyMentioned {
		t.Errorf("an old record's id = %q, want mentioned", p)
	}
	reconcileOK(t, s, all...)
	th := s.Thread("X-1", all)
	if th.Owner != nil || len(th.Claimants) != 0 {
		t.Errorf("an old record made an owner: %+v", th)
	}
	if r, _ := s.Get("w_old"); !r.Sessions[0].LastSeen.Equal(time.Date(2026, 3, 1, 11, 0, 0, 0, time.UTC)) {
		t.Errorf("last_seen: %+v", r.Sessions[0])
	}
}

// prev* are the previous release's types: what its binary reads this file as.
type prevWorkSession struct {
	Box       string    `json:"box,omitempty"`
	Name      string    `json:"name"`
	Title     string    `json:"title,omitempty"`
	Worktree  string    `json:"worktree,omitempty"`
	StartedBy string    `json:"started_by,omitempty"`
	Created   time.Time `json:"created,omitzero"`
	Ended     time.Time `json:"ended,omitzero"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
}

type prevWorkRecord struct {
	ID       string            `json:"id"`
	Title    string            `json:"title,omitempty"`
	Sessions []prevWorkSession `json:"sessions"`
	Status   string            `json:"status"`
	Issue    string            `json:"issue,omitempty"`
	IssueIDs []string          `json:"issue_ids,omitempty"`
	Manual   bool              `json:"manual,omitempty"`
	Created  time.Time         `json:"created"`
	Updated  time.Time         `json:"updated"`
}

type prevWorkState struct {
	Records []*prevWorkRecord `json:"records"`
	Pins    []WorkPin         `json:"pins,omitempty"`
	Cursor  int64             `json:"cursor,omitempty"`
}

func TestAFileThisVersionWroteLoadsInThePreviousOne(t *testing.T) {
	s := newTestWork(t)
	a := agentSession("a", "shop/a", "X-1 work")
	reconcileOK(t, s, a)
	claimOK(t, s, a, "shop#2")
	if _, _, err := s.NewDelivery("shop#2", "a", "a#1", "berth", "drop", DeliveryHeld); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Pin(PinProject, "Big launch"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"claims"`, `"deliveries"`, `"subscribed"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("the file has no %s, so this proves nothing:\n%s", want, b)
		}
	}
	var prev prevWorkState
	if err := json.Unmarshal(b, &prev); err != nil {
		t.Fatalf("the previous binary can't read it, and would set it aside: %v", err)
	}
	if len(prev.Records) != 1 || prev.Records[0].Sessions[0].Name != "a" || prev.Records[0].Issue != "X-1" || len(prev.Pins) != 1 {
		t.Errorf("%+v", prev)
	}
	if strings.Contains(string(b), `"keys"`) {
		t.Errorf("a record's keys are read from its sessions, and never kept:\n%s", b)
	}
}

func TestClaimsAndDeliveriesSurviveARestart(t *testing.T) {
	s := newTestWork(t)
	a := agentSession("a", "shop/a", "a")
	reconcileOK(t, s, a)
	claimOK(t, s, a, "k")
	d, _, err := s.NewDelivery("k", "a", "a#1", "berth", "drop", DeliveryHeld)
	if err != nil {
		t.Fatal(err)
	}
	again := &WorkStore{Path: s.Path, Box: s.Box}
	if got := ownerName(again.Thread("k", []Session{a})); got != "a" {
		t.Errorf("owner after a restart = %q", got)
	}
	got, err := again.Delivery(d.ID)
	if err != nil || got.State != DeliveryHeld || got.To.Name != "a" || got.To.Box != "acme-box" || got.Source != "drop" || got.From != "berth" {
		t.Errorf("%+v, %v", got, err)
	}
}

func heldDelivery(t *testing.T, s *WorkStore, session, turn string) WorkDelivery {
	t.Helper()
	d, evs, err := s.NewDelivery("k", session, turn, "berth", "drop", DeliveryHeld)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Type != "work.delivered" || evs[0].Data["state"] != DeliveryHeld {
		t.Fatalf("%+v", evs)
	}
	return d
}

func deliveryState(t *testing.T, s *WorkStore, id string) string {
	t.Helper()
	d, err := s.Delivery(id)
	if err != nil {
		t.Fatal(err)
	}
	return d.State
}

func eventTypes(evs []workEvent) []string {
	out := []string{}
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return out
}

func TestADeliveryReadsItsFateFromTheLedger(t *testing.T) {
	setup := func(t *testing.T) (*WorkStore, *Turns) {
		t.Helper()
		turns := &Turns{}
		for _, name := range []string{"a", "b"} {
			turns.Observe(events.Event{Type: "session.started", Data: map[string]any{"name": name, "agent": "claude", "path": "/w/" + name}})
		}
		return newTestWork(t), turns
	}
	hold := func(t *testing.T, s *WorkStore, turns *Turns, session string) WorkDelivery {
		t.Helper()
		tr, err := turns.Queue(session, "a prompt", true, "berth", "")
		if err != nil {
			t.Fatal(err)
		}
		return heldDelivery(t, s, session, tr.ID)
	}
	sync := func(t *testing.T, s *WorkStore, turns *Turns) []string {
		t.Helper()
		evs, err := s.SyncHeld(turns.Get)
		if err != nil {
			t.Fatal(err)
		}
		return eventTypes(evs)
	}
	t.Run("still held", func(t *testing.T) {
		s, turns := setup(t)
		d := hold(t, s, turns, "a")
		if got := sync(t, s, turns); len(got) != 0 {
			t.Errorf("%v", got)
		}
		// Taken from the inbox to be typed, it's still held until it is.
		if _, err := turns.takeQueued("a", d.Turn); err != nil {
			t.Fatal(err)
		}
		if got := sync(t, s, turns); len(got) != 0 || deliveryState(t, s, d.ID) != DeliveryHeld {
			t.Errorf("%v, %s", got, deliveryState(t, s, d.ID))
		}
	})
	t.Run("typed", func(t *testing.T) {
		s, turns := setup(t)
		d := hold(t, s, turns, "a")
		turns.Observe(events.Event{Seq: 1, Type: "session.sent", Time: time.Now(), Data: map[string]any{"name": "a", "turn": d.Turn}})
		if got := sync(t, s, turns); !slices.Equal(got, []string{"work.delivered"}) {
			t.Errorf("%v", got)
		}
		if st := deliveryState(t, s, d.ID); st != DeliveryTyped {
			t.Errorf("%s", st)
		}
		if got := sync(t, s, turns); len(got) != 0 {
			t.Errorf("moved twice: %v", got)
		}
	})
	t.Run("typed as an answer", func(t *testing.T) {
		s, turns := setup(t)
		d := hold(t, s, turns, "a")
		if _, err := turns.takeQueued("a", d.Turn); err != nil {
			t.Fatal(err)
		}
		turns.endQueued("a", d.Turn, turnSentAsAnswer)
		sync(t, s, turns)
		if st := deliveryState(t, s, d.ID); st != DeliveryTyped {
			t.Errorf("%s", st)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		s, turns := setup(t)
		d, other := hold(t, s, turns, "a"), hold(t, s, turns, "a")
		if err := turns.Cancel("a", d.Turn); err != nil {
			t.Fatal(err)
		}
		if got := sync(t, s, turns); !slices.Equal(got, []string{"work.cancelled"}) {
			t.Errorf("%v", got)
		}
		if st := deliveryState(t, s, d.ID); st != DeliveryCancelled {
			t.Errorf("%s", st)
		}
		if st := deliveryState(t, s, other.ID); st != DeliveryHeld {
			t.Errorf("another turn: %s", st)
		}
	})
	t.Run("lost when its session stops, every prompt held", func(t *testing.T) {
		s, turns := setup(t)
		first, second := hold(t, s, turns, "a"), hold(t, s, turns, "a")
		typed, _, err := s.NewDelivery("k", "a", "a#0", "berth", "", DeliveryTyped)
		if err != nil {
			t.Fatal(err)
		}
		elsewhere := hold(t, s, turns, "b")
		turns.Observe(events.Event{Seq: 1, Type: "session.stopped", Time: time.Now(), Data: map[string]any{"name": "a"}})
		if got := sync(t, s, turns); !slices.Equal(got, []string{"work.lost", "work.lost"}) {
			t.Errorf("%v", got)
		}
		for _, d := range []WorkDelivery{first, second} {
			if st := deliveryState(t, s, d.ID); st != DeliveryLost {
				t.Errorf("%s: %s", d.Turn, st)
			}
		}
		if st := deliveryState(t, s, typed.ID); st != DeliveryTyped {
			t.Errorf("one already typed isn't lost: %s", st)
		}
		if st := deliveryState(t, s, elsewhere.ID); st != DeliveryHeld {
			t.Errorf("another session's: %s", st)
		}
	})
	t.Run("lost when its session is gone unannounced", func(t *testing.T) {
		s, turns := setup(t)
		d, kept := hold(t, s, turns, "a"), hold(t, s, turns, "b")
		turns.Prune([]Session{{Name: "b"}})
		if got := sync(t, s, turns); !slices.Equal(got, []string{"work.lost"}) {
			t.Errorf("%v", got)
		}
		if st := deliveryState(t, s, d.ID); st != DeliveryLost {
			t.Errorf("%s", st)
		}
		if st := deliveryState(t, s, kept.ID); st != DeliveryHeld {
			t.Errorf("%s", st)
		}
	})
	t.Run("lost when it can't be typed", func(t *testing.T) {
		s, turns := setup(t)
		d := hold(t, s, turns, "a")
		it, err := turns.takeQueued("a", d.Turn)
		if err != nil {
			t.Fatal(err)
		}
		turns.failQueued(it, "no pane")
		sync(t, s, turns)
		got, _ := s.Delivery(d.ID)
		if got.State != DeliveryLost || got.Why != "no pane" {
			t.Errorf("%+v", got)
		}
	})
	t.Run("lost when the ledger has forgotten it", func(t *testing.T) {
		s, turns := setup(t)
		d := heldDelivery(t, s, "a", "a#99")
		sync(t, s, turns)
		if st := deliveryState(t, s, d.ID); st != DeliveryLost {
			t.Errorf("%s", st)
		}
	})
}

func TestUndoAndHandBack(t *testing.T) {
	s := newTestWork(t)
	held := heldDelivery(t, s, "a", "a#1")
	if _, _, err := s.HandBack(held.ID, "not mine"); !isHTTP(err, 409) {
		t.Errorf("a held one can't be handed back: %v", err)
	}
	got, evs, err := s.Undo(held.ID, true)
	if err != nil || got.State != DeliveryUndone || !got.Retracted || !slices.Equal(eventTypes(evs), []string{"work.undone"}) {
		t.Errorf("%+v %v %v", got, evs, err)
	}
	if _, evs, err := s.Undo(held.ID, true); err != nil || len(evs) != 0 {
		t.Errorf("undoing twice: %v %v", evs, err)
	}
	if _, _, err := s.HandBack(held.ID, ""); !isHTTP(err, 409) {
		t.Errorf("an undone one can't be handed back: %v", err)
	}
	// Once undone, what the ledger says of its cancelled turn changes nothing.
	evs, err = s.SyncHeld(func(string) (Turn, bool) { return Turn{State: "lost", Status: turnCancelled}, true })
	if err != nil || len(evs) != 0 {
		t.Errorf("%+v %v", evs, err)
	}

	typed, _, err := s.NewDelivery("k", "a", "a#2", "berth", "", DeliveryTyped)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err = s.Undo(typed.ID, false)
	if err != nil || got.Retracted || got.State != DeliveryUndone {
		t.Errorf("%+v %v", got, err)
	}

	// A read between taking it out and undoing it finds it cancelled.
	raced := heldDelivery(t, s, "a", "a#4")
	if _, err := s.SyncHeld(func(string) (Turn, bool) { return Turn{State: "lost", Status: turnCancelled}, true }); err != nil {
		t.Fatal(err)
	}
	if got, _, err = s.Undo(raced.ID, true); err != nil || got.State != DeliveryUndone || !got.Retracted {
		t.Errorf("%+v %v", got, err)
	}
	cancelled := heldDelivery(t, s, "a", "a#5")
	if _, err := s.SyncHeld(func(string) (Turn, bool) { return Turn{State: "lost", Status: turnCancelled}, true }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Undo(cancelled.ID, false); !isHTTP(err, 409) {
		t.Errorf("one cancelled by someone else has nothing to undo: %v", err)
	}

	back, _, err := s.NewDelivery("k", "a", "a#3", "berth", "", DeliveryTyped)
	if err != nil {
		t.Fatal(err)
	}
	got, evs, err = s.HandBack(back.ID, "not mine")
	if err != nil || got.State != DeliveryHandedBack || got.Why != "not mine" || !slices.Equal(eventTypes(evs), []string{"work.handed_back"}) {
		t.Errorf("%+v %v %v", got, evs, err)
	}
	if _, _, err := s.Undo(back.ID, false); !isHTTP(err, 409) {
		t.Errorf("a handed back one can't be undone: %v", err)
	}
	if _, err := s.Delivery("d_none"); !isHTTP(err, 404) {
		t.Errorf("%v", err)
	}
}

func isHTTP(err error, code int) bool {
	h, ok := err.(httpError)
	return ok && h.status == code
}

func TestDeliveriesFilterAndHeldOnesAreNeverForgotten(t *testing.T) {
	s := newTestWork(t)
	keep := heldDelivery(t, s, "a", "a#1")
	for range deliveriesKept + 5 {
		if _, _, err := s.NewDelivery("k2", "b", "", "berth", "", DeliveryTyped); err != nil {
			t.Fatal(err)
		}
	}
	all := s.Deliveries("", "", "")
	if len(all) != deliveriesKept {
		t.Errorf("kept %d", len(all))
	}
	if all[0].ID != keep.ID {
		t.Errorf("the held one went: %+v", all[0])
	}
	if got := s.Deliveries("k", "a", DeliveryHeld); len(got) != 1 || got[0].ID != keep.ID {
		t.Errorf("%+v", got)
	}
	if got := s.Deliveries("k2", "a", ""); len(got) != 0 {
		t.Errorf("%+v", got)
	}
}

func TestThreadKeysAreOpaque(t *testing.T) {
	got, err := cleanThreadKeys([]string{"BRA-123", " shop#12 ", "service:api", "gap:a b", "file:src/a,b.go", "shop#12", "bra-123"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"BRA-123", "shop#12", "service:api", "gap:a b", "file:src/a,b.go", "bra-123"}
	if !slices.Equal(got, want) {
		t.Errorf("%q", got)
	}
	for _, bad := range []string{"", "  ", "a\nb", strings.Repeat("k", workTextMax+1)} {
		if _, err := cleanThreadKey(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}
