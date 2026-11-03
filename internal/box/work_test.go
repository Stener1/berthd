package box

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func newTestWork(t *testing.T) *WorkStore {
	t.Helper()
	clock := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	return &WorkStore{Path: filepath.Join(t.TempDir(), "work.json"), Box: "acme-box", Now: func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}}
}

// agentSession is a live agent session as the box lists it.
func agentSession(name, location, title string, startedBy ...string) Session {
	s := Session{Name: name, Location: location, Title: title, Preset: "claude", Dir: "/w/" + location, Created: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)}
	if len(startedBy) > 0 {
		s.StartedBy = startedBy[0]
	}
	return s
}

func recordOf(t *testing.T, s *WorkStore, session string) WorkRecord {
	t.Helper()
	for _, r := range s.List("", true) {
		for _, m := range r.Sessions {
			if m.Name == session && m.Ended.IsZero() {
				return r
			}
		}
	}
	t.Fatalf("no record holds %s: %+v", session, s.List("", true))
	return WorkRecord{}
}

func TestIssueIDsCoverSinglesRangesListsAndNone(t *testing.T) {
	re := regexp.MustCompile(DefaultIssuePattern)
	for _, c := range []struct {
		text  string
		ids   []string
		first string
	}{
		{"fix the header", nil, ""},
		{"X-1 fix", []string{"X-1"}, "X-1"},
		{"🔨 ACME-42 fix", []string{"ACME-42"}, "ACME-42"},
		{"X-1..5 sweep", []string{"X-1", "X-2", "X-3", "X-4", "X-5"}, "X-1..5"},
		{"X-1..X-3", []string{"X-1", "X-2", "X-3"}, "X-1..X-3"},
		{"X-1/2", []string{"X-1", "X-2"}, "X-1/2"},
		{"X-1/2/7", []string{"X-1", "X-2", "X-7"}, "X-1/2/7"},
		{"X-1/X-2", []string{"X-1", "X-2"}, "X-1/X-2"},
		{"X-1, Y-2 and X-1", []string{"X-1", "Y-2"}, "X-1"},
		{"X-1..3/9", []string{"X-1", "X-2", "X-3", "X-9"}, "X-1..3/9"},
		// A typo's range keeps its ends rather than filling the box.
		{"X-1..100000", []string{"X-1", "X-100000"}, "X-1..100000"},
		{"X-5..1", []string{"X-5", "X-1"}, "X-5..1"},
		// Lowercase isn't an id by default, nor is an encoding.
		{"phase-2 of utf-8", nil, ""},
		{"UTF-8 and SHA-256", nil, ""},
	} {
		ids, first := issueIDs(re, true, c.text)
		if !slices.Equal(ids, c.ids) || first != c.first {
			t.Errorf("%q: got %v %q, want %v %q", c.text, ids, first, c.ids, c.first)
		}
	}
}

func TestTheIDPatternIsTheBoxsSetting(t *testing.T) {
	s := newTestWork(t)
	// Not an issue by the default pattern, and joined by the box's own.
	if _, err := s.SetSettings(WorkSettings{IDPattern: `(?i)\bacme-[0-9]+\b`}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reconcile([]Session{agentSession("a", "shop/acme-7-header", ""), agentSession("b", "shop/other", "acme-6..8 sweep"), agentSession("c", "shop/third", "OTHER-7")}); err != nil {
		t.Fatal(err)
	}
	if a, b := recordOf(t, s, "a"), recordOf(t, s, "b"); a.ID != b.ID || !slices.Contains(a.IssueIDs, "ACME-7") {
		t.Errorf("a and b should share acme-7: %+v / %+v", a, b)
	}
	if c := recordOf(t, s, "c"); len(c.IssueIDs) != 0 {
		t.Errorf("OTHER-7 isn't an id here: %+v", c)
	}
	for _, bad := range []string{`(`, `x*`} {
		if _, err := s.SetSettings(WorkSettings{IDPattern: bad}); err == nil {
			t.Errorf("pattern %q should be refused", bad)
		}
	}
}

func TestRecordsFormByRule(t *testing.T) {
	s := newTestWork(t)
	sessions := []Session{
		agentSession("range", "shop/sweep", "X-1..3 sweep"),
		agentSession("single", "shop/x-2", "X-2 worker"),
		agentSession("same-tree-1", "shop/tree", "first"),
		agentSession("same-tree-2", "shop/tree", "second"),
		agentSession("parent", "shop/orch", "orchestrator"),
		agentSession("child", "shop/kid", "helper", "parent"),
		agentSession("alone", "shop/alone", "alone"),
		// A repository's main checkout is shared by unrelated work.
		agentSession("main-1", "shop", "one"),
		agentSession("main-2", "shop", "two"),
		// Not work: a shell, and a service's terminal.
		{Name: "shell", Location: "shop/tree", Created: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)},
		{Name: "svc", Location: "shop/tree", Preset: "claude", Service: "web"},
	}
	evs, err := s.Reconcile(sessions)
	if err != nil {
		t.Fatal(err)
	}
	same := func(a, b string) bool { return recordOf(t, s, a).ID == recordOf(t, s, b).ID }
	if !same("range", "single") {
		t.Error("X-2 should join the range's record")
	}
	if !same("same-tree-1", "same-tree-2") {
		t.Error("sessions in one worktree should share a record")
	}
	if !same("parent", "child") {
		t.Error("a child with nothing else to go by should join its parent's record")
	}
	for _, pair := range [][2]string{{"alone", "parent"}, {"main-1", "main-2"}, {"range", "same-tree-1"}} {
		if same(pair[0], pair[1]) {
			t.Errorf("%v should be apart", pair)
		}
	}
	for _, r := range s.List("", true) {
		for _, m := range r.Sessions {
			if m.Name == "shell" || m.Name == "svc" {
				t.Errorf("%s isn't work: %+v", m.Name, r)
			}
		}
	}
	if len(evs) == 0 {
		t.Error("forming records should publish")
	}
	// Nothing new: nothing changes, and nothing is said.
	if evs, err := s.Reconcile(sessions); err != nil || len(evs) != 0 {
		t.Errorf("second pass: %v %v", evs, err)
	}
}

func TestAChildComesAfterAParentListedLater(t *testing.T) {
	s := newTestWork(t)
	child := agentSession("child", "shop/kid", "helper", "parent")
	parent := agentSession("parent", "shop/orch", "orchestrator")
	parent.Created = child.Created.Add(time.Hour)
	if _, err := s.Reconcile([]Session{child, parent}); err != nil {
		t.Fatal(err)
	}
	if recordOf(t, s, "child").ID != recordOf(t, s, "parent").ID {
		t.Errorf("child should join its parent: %+v", s.List("", true))
	}
}

func TestASessionNamingTwoRecordsJoinsOneAndSuggestsTheOther(t *testing.T) {
	s := newTestWork(t)
	if _, err := s.Reconcile([]Session{agentSession("one", "shop/a", "X-1"), agentSession("four", "shop/b", "X-4")}); err != nil {
		t.Fatal(err)
	}
	one, four := recordOf(t, s, "one"), recordOf(t, s, "four")
	if _, err := s.Reconcile([]Session{agentSession("one", "shop/a", "X-1"), agentSession("four", "shop/b", "X-4"), agentSession("all", "shop/c", "X-1..5")}); err != nil {
		t.Fatal(err)
	}
	got := recordOf(t, s, "all")
	if got.ID != one.ID || !slices.Equal(got.SuggestedWith, []string{four.ID}) {
		t.Fatalf("got %+v", got)
	}
	// Rejected, it isn't proposed again.
	if _, _, err := s.Reject(one.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reconcile([]Session{agentSession("one", "shop/a", "X-1"), agentSession("four", "shop/b", "X-4"), agentSession("all", "shop/c", "X-1..5"), agentSession("all2", "shop/d", "X-1/4")}); err != nil {
		t.Fatal(err)
	}
	if got := recordOf(t, s, "one"); len(got.SuggestedWith) != 0 || !slices.Contains(got.RejectedWith, four.ID) {
		t.Errorf("rejected suggestion came back: %+v", got)
	}
}

func TestAcceptingASuggestionGroupsAndKeepsTheTargetsState(t *testing.T) {
	s := newTestWork(t)
	a, _, _ := s.Create(WorkInput{Title: "a"})
	b, _, _ := s.Create(WorkInput{Title: "b", Issue: "X-9"})
	wait := "someone"
	ids := []string{b.ID}
	if _, _, err := s.Update(a.ID, WorkPatch{WaitingOn: &wait, SuggestedWith: &ids}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Pin(PinWork, b.ID); err != nil {
		t.Fatal(err)
	}
	got, evs, err := s.Accept(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != WorkWaiting || got.WaitingOn != wait || !slices.Contains(got.IssueIDs, "X-9") || len(got.SuggestedWith) != 0 || !got.Pinned {
		t.Errorf("got %+v", got)
	}
	if _, err := s.Get(b.ID); err == nil {
		t.Error("the grouped record should be gone")
	}
	if len(evs) != 1 || evs[0].Type != "work.accepted" {
		t.Errorf("events %+v", evs)
	}
	if _, _, err := s.Accept(a.ID); err == nil {
		t.Error("nothing left to accept")
	}
}

func TestUngroupStaysApart(t *testing.T) {
	s := newTestWork(t)
	sessions := []Session{agentSession("a", "shop/tree", "X-1"), agentSession("b", "shop/tree", "X-1")}
	if _, err := s.Reconcile(sessions); err != nil {
		t.Fatal(err)
	}
	rec := recordOf(t, s, "a")
	if rec.ID != recordOf(t, s, "b").ID {
		t.Fatalf("not grouped: %+v", s.List("", true))
	}
	if _, _, err := s.Ungroup(rec.ID, []string{"a", "b"}); err == nil {
		t.Error("taking every session out should be refused")
	}
	if _, _, err := s.Ungroup(rec.ID, []string{"nobody"}); err == nil {
		t.Error("a session it doesn't hold should be refused")
	}
	if _, _, err := s.Ungroup(rec.ID, []string{"b", "b"}); err != nil { // named twice is once

		t.Fatal(err)
	}
	for range 2 {
		if _, err := s.Reconcile(sessions); err != nil {
			t.Fatal(err)
		}
		if recordOf(t, s, "a").ID == recordOf(t, s, "b").ID {
			t.Fatal("rules put an ungrouped session back")
		}
	}
}

func TestARecordOutlivesItsSessionsAndARestart(t *testing.T) {
	s := newTestWork(t)
	sess := agentSession("a", "shop/tree", "X-1 fix")
	if _, err := s.Reconcile([]Session{sess}); err != nil {
		t.Fatal(err)
	}
	id := recordOf(t, s, "a").ID
	until := "2026-04-01"
	if _, _, err := s.Update(id, WorkPatch{NotUntil: &until}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Pin(PinWork, id); err != nil {
		t.Fatal(err)
	}
	// The session ends: only its line says so.
	if _, err := s.Reconcile(nil); err != nil {
		t.Fatal(err)
	}
	reread := &WorkStore{Path: s.Path, Box: s.Box}
	got, err := reread.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != WorkParked || got.NotUntil.IsZero() || !got.Pinned || got.Sessions[0].Ended.IsZero() {
		t.Errorf("after the session ended and a restart: %+v", got)
	}
	// A session resuming under its name is the same work.
	if _, err := reread.Reconcile([]Session{sess}); err != nil {
		t.Fatal(err)
	}
	got, _ = reread.Get(id)
	if !got.Sessions[0].Ended.IsZero() || len(reread.List("", true)) != 1 {
		t.Errorf("after it came back: %+v", reread.List("", true))
	}
}

func TestClosingDropsThePinAndNewWorkGetsANewRecord(t *testing.T) {
	s := newTestWork(t)
	sess := agentSession("a", "shop/tree", "X-1 fix")
	if _, err := s.Reconcile([]Session{sess}); err != nil {
		t.Fatal(err)
	}
	id := recordOf(t, s, "a").ID
	if _, _, err := s.Pin(PinWork, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Pin(PinRepo, "shop"); err != nil {
		t.Fatal(err)
	}
	closed := WorkClosed
	got, evs, err := s.Update(id, WorkPatch{Status: &closed})
	if err != nil {
		t.Fatal(err)
	}
	if got.Pinned {
		t.Error("a closed record keeps no pin")
	}
	types := []string{}
	for _, e := range evs {
		types = append(types, e.Type)
	}
	if !slices.Contains(types, "work.closed") || !slices.Contains(types, "work.unpinned") {
		t.Errorf("events %v", types)
	}
	if pins := s.Pins(); len(pins) != 1 || pins[0].Kind != PinRepo {
		t.Errorf("the repo's pin should stay: %+v", pins)
	}
	if _, _, err := s.Pin(PinWork, id); err == nil {
		t.Error("a closed record can't be pinned")
	}
	if len(s.List("", false)) != 0 || len(s.List("", true)) != 1 || len(s.List(WorkClosed, false)) != 1 {
		t.Errorf("closed records hide unless asked for: %+v", s.List("", true))
	}
	// Its live session stays with it: closing is the record's own state.
	if _, err := s.Reconcile([]Session{sess}); err != nil {
		t.Fatal(err)
	}
	if len(s.List("", true)) != 1 {
		t.Errorf("a live session of a closed record got a new one: %+v", s.List("", true))
	}
	// The same issue in another session, or that name started anew, is new work.
	again := sess
	again.Created = sess.Created.Add(time.Hour)
	if _, err := s.Reconcile([]Session{again}); err != nil {
		t.Fatal(err)
	}
	all := s.List("", true)
	if len(all) != 2 {
		t.Fatalf("got %+v", all)
	}
	if fresh := recordOf(t, s, "a"); fresh.ID == id || fresh.Status != WorkOpen {
		t.Errorf("got %+v", fresh)
	}
}

func TestARecordFollowsItsLoneSessionsLateTitle(t *testing.T) {
	s := newTestWork(t)
	if _, err := s.Reconcile([]Session{agentSession("old", "shop/a", "X-7 work")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reconcile([]Session{agentSession("old", "shop/a", "X-7 work"), agentSession("new", "shop/b", "")}); err != nil {
		t.Fatal(err)
	}
	if recordOf(t, s, "new").ID == recordOf(t, s, "old").ID {
		t.Fatal("no title yet: nothing to join by")
	}
	// Named just after it started: it joins, and its empty record goes.
	if _, err := s.Reconcile([]Session{agentSession("old", "shop/a", "X-7 work"), agentSession("new", "shop/b", "🔨 X-7 more")}); err != nil {
		t.Fatal(err)
	}
	if recordOf(t, s, "new").ID != recordOf(t, s, "old").ID || len(s.List("", true)) != 1 {
		t.Fatalf("got %+v", s.List("", true))
	}
	if _, err := s.Reconcile([]Session{agentSession("alone", "shop/c", "first"), agentSession("old", "shop/a", "X-7 work"), agentSession("new", "shop/b", "🔨 X-7 more")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reconcile([]Session{agentSession("alone", "shop/c", "second"), agentSession("old", "shop/a", "X-7 work"), agentSession("new", "shop/b", "🔨 X-7 more")}); err != nil {
		t.Fatal(err)
	}
	alone := recordOf(t, s, "alone")
	if alone.Title != "second" {
		t.Errorf("title should follow: %+v", alone)
	}
	// Once someone has changed it, it's theirs.
	title := "mine"
	if _, _, err := s.Update(alone.ID, WorkPatch{Title: &title}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reconcile([]Session{agentSession("alone", "shop/c", "third X-7"), agentSession("old", "shop/a", "X-7 work"), agentSession("new", "shop/b", "🔨 X-7 more")}); err != nil {
		t.Fatal(err)
	}
	if got := recordOf(t, s, "alone"); got.Title != "mine" || got.ID != alone.ID {
		t.Errorf("rules moved a record someone changed: %+v", got)
	}
}

// The range's title can come after its worker's record is formed: the group
// then reads as the range, however the two arrived.
func TestAWiderSessionDescribesTheGroup(t *testing.T) {
	s := newTestWork(t)
	worker := agentSession("worker", "shop/a", "X-2 worker")
	if _, err := s.Reconcile([]Session{worker, agentSession("sweep", "shop/b", "")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reconcile([]Session{worker, agentSession("sweep", "shop/b", "X-1..3 sweep")}); err != nil {
		t.Fatal(err)
	}
	got := recordOf(t, s, "sweep")
	if got.ID != recordOf(t, s, "worker").ID || got.Issue != "X-1..3" || got.Title != "X-1..3 sweep" || len(got.IssueIDs) != 3 {
		t.Fatalf("got %+v", got)
	}
	title := "mine"
	if _, _, err := s.Update(got.ID, WorkPatch{Title: &title}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reconcile([]Session{worker, agentSession("sweep", "shop/b", "X-1..3 sweep"), agentSession("all", "shop/c", "X-1..9 all")}); err != nil {
		t.Fatal(err)
	}
	if got := recordOf(t, s, "all"); got.Title != "mine" || got.Issue != "X-1..3" {
		t.Errorf("rules rewrote a record someone named: %+v", got)
	}
}

// Reopening a record whose session's name now runs in another leaves that
// session with one record: the one it's in.
func TestReopeningDoesntTakeASessionBack(t *testing.T) {
	s := newTestWork(t)
	sess := agentSession("a", "shop/tree", "X-1 fix")
	if _, err := s.Reconcile([]Session{sess}); err != nil {
		t.Fatal(err)
	}
	old := recordOf(t, s, "a").ID
	closed, open := WorkClosed, WorkOpen
	if _, _, err := s.Update(old, WorkPatch{Status: &closed}); err != nil {
		t.Fatal(err)
	}
	again := sess
	again.Created = sess.Created.Add(time.Hour)
	if _, err := s.Reconcile([]Session{again}); err != nil {
		t.Fatal(err)
	}
	fresh := recordOf(t, s, "a").ID
	if fresh == old {
		t.Fatal("it should have a new record")
	}
	if _, _, err := s.Update(old, WorkPatch{Status: &open}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := s.Reconcile([]Session{again}); err != nil {
			t.Fatal(err)
		}
	}
	live := 0
	for _, r := range s.List("", true) {
		for _, m := range r.Sessions {
			if m.Name == "a" && m.Ended.IsZero() {
				live++
			}
		}
	}
	if live != 1 || recordOf(t, s, "a").ID != fresh {
		t.Fatalf("two records run it: %+v", s.List("", true))
	}
	at := again.Created.Add(time.Minute)
	if id, err := s.Touch("a", at); err != nil || id != fresh {
		t.Errorf("a touch went to %q, not %q: %v", id, fresh, err)
	}
}

// A late title naming another record's issue proposes the group, from a record
// of several sessions as from one.
func TestALateTitleInAGroupSuggestsTheOtherRecord(t *testing.T) {
	s := newTestWork(t)
	sessions := []Session{agentSession("other", "shop/x", "X-5 work"), agentSession("a", "shop/tree", "first"), agentSession("b", "shop/tree", "second")}
	if _, err := s.Reconcile(sessions); err != nil {
		t.Fatal(err)
	}
	group, other := recordOf(t, s, "a"), recordOf(t, s, "other")
	if group.ID != recordOf(t, s, "b").ID || group.ID == other.ID {
		t.Fatalf("got %+v", s.List("", true))
	}
	sessions[2].Title = "X-4..6 sweep"
	evs, err := s.Reconcile(sessions)
	if err != nil {
		t.Fatal(err)
	}
	got := recordOf(t, s, "a")
	if !slices.Equal(got.SuggestedWith, []string{other.ID}) || len(s.List("", true)) != 2 {
		t.Fatalf("got %+v", s.List("", true))
	}
	if !slices.ContainsFunc(evs, func(e workEvent) bool { return e.Type == "work.suggested" }) {
		t.Errorf("events %+v", evs)
	}
	// Said once, and not back from the other side, even once turned down.
	if evs, err := s.Reconcile(sessions); err != nil || len(evs) != 0 {
		t.Errorf("second pass: %+v %v", evs, err)
	}
	if _, _, err := s.Reject(got.ID); err != nil {
		t.Fatal(err)
	}
	if evs, err := s.Reconcile(sessions); err != nil || len(evs) != 0 {
		t.Errorf("after rejecting: %+v %v", evs, err)
	}
	if a, b := recordOf(t, s, "a"), recordOf(t, s, "other"); len(a.SuggestedWith) != 0 || len(b.SuggestedWith) != 0 {
		t.Errorf("a rejection came back: %+v / %+v", a, b)
	}
}

func TestUpdateSetsAndClears(t *testing.T) {
	s := newTestWork(t)
	r, _, _ := s.Create(WorkInput{Title: "x"})
	who, date, empty, open, nope := "the vendor", "2026-05-01", "", WorkOpen, "later"
	got, _, err := s.Update(r.ID, WorkPatch{WaitingOn: &who, CheckBack: &date})
	if err != nil || got.Status != WorkWaiting || got.WaitingOn != who || got.CheckBack.IsZero() {
		t.Fatalf("waiting: %+v %v", got, err)
	}
	if got, _, err = s.Update(r.ID, WorkPatch{CheckBack: &empty}); err != nil || !got.CheckBack.IsZero() || got.Status != WorkWaiting {
		t.Fatalf("clearing: %+v %v", got, err)
	}
	if got, _, err = s.Update(r.ID, WorkPatch{Status: &open}); err != nil || got.WaitingOn != "" {
		t.Fatalf("open: %+v %v", got, err)
	}
	// A refused change changes nothing.
	bad := "not a date"
	if _, _, err := s.Update(r.ID, WorkPatch{Title: &who, NotUntil: &bad}); err == nil {
		t.Error("a bad date should be refused")
	}
	if _, _, err := s.Update(r.ID, WorkPatch{Title: &who, Status: &nope}); err == nil {
		t.Error("a bad status should be refused")
	}
	if got, _ := s.Get(r.ID); got.Title != "x" {
		t.Errorf("a refused change wrote: %+v", got)
	}
	long := strings.Repeat("é", workTextMax+50)
	if got, _, _ := s.Update(r.ID, WorkPatch{Title: &long}); len([]rune(got.Title)) != workTextMax {
		t.Errorf("title length %d", len([]rune(got.Title)))
	}
}

// A record a lone session moves into takes its last_seen with it.
func TestARuleMoveKeepsLastSeen(t *testing.T) {
	s := newTestWork(t)
	old := agentSession("old", "shop/a", "X-7 work")
	if _, err := s.Reconcile([]Session{old, agentSession("new", "shop/b", "")}); err != nil {
		t.Fatal(err)
	}
	at := old.Created.Add(time.Hour)
	if id, err := s.Touch("new", at); err != nil || id == "" {
		t.Fatalf("%q %v", id, err)
	}
	if _, err := s.Reconcile([]Session{old, agentSession("new", "shop/b", "X-7 more")}); err != nil {
		t.Fatal(err)
	}
	if got := recordOf(t, s, "old"); !got.LastSeen.Equal(at) || got.ID != recordOf(t, s, "new").ID {
		t.Errorf("got %+v", got)
	}
}

// A file that's there and can't be read is never replaced by an empty store.
func TestAFileThatCantBeReadIsNeverOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "work.json")
	if err := writeTestFile(path, `{"records":[{"id":"w_kept","status":"parked","sessions":[]}]}`); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("this user reads any file")
	}
	s := &WorkStore{Path: path, Box: "acme-box"}
	if s.ready() == nil {
		t.Fatal("it should say it can't read")
	}
	if evs, err := s.Reconcile([]Session{agentSession("a", "shop/t", "x")}); err == nil || len(evs) != 0 {
		t.Errorf("reconcile: %v %v", evs, err)
	}
	if _, _, err := s.Create(WorkInput{Title: "y"}); err == nil {
		t.Error("create should be refused")
	}
	if err := s.Flush(9, nil); err == nil {
		t.Error("flush should be refused")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	// Readable again, the store is what the file says.
	if err := s.ready(); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get("w_kept"); err != nil || got.Status != WorkParked {
		t.Errorf("got %+v %v", got, err)
	}
	if all := s.List("", true); len(all) != 1 {
		t.Errorf("what it did while it couldn't read came back: %+v", all)
	}
}

func TestAnUnreadableFileIsKept(t *testing.T) {
	s := newTestWork(t)
	if _, _, err := s.Create(WorkInput{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(s.Path, "{not json"); err != nil {
		t.Fatal(err)
	}
	fresh := &WorkStore{Path: s.Path, Box: s.Box}
	if _, _, err := fresh.Create(WorkInput{Title: "y"}); err != nil {
		t.Fatal(err)
	}
	if b, err := readTestFile(s.Path + ".bad"); err != nil || b != "{not json" {
		t.Errorf("the old file should be kept: %q %v", b, err)
	}
}
