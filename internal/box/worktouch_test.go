package box

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/events"
	"github.com/cosscom/shipyard/internal/integrations/adapters"
)

func writeTestFile(path, text string) error { return os.WriteFile(path, []byte(text), 0o600) }

func readTestFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

var touchT0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func ev(typ, origin string, secs int, data map[string]any) events.Event {
	return events.Event{Type: typ, Origin: origin, Time: touchT0.Add(time.Duration(secs) * time.Second), Data: data}
}

// run feeds events and settles everything; typed answers for the transcript.
func runTracker(typed func(string, time.Time) bool, evs ...events.Event) []touchNote {
	t := newTouchTracker(typed)
	var out []touchNote
	for _, e := range evs {
		out = append(out, t.feed(e)...)
		out = append(out, t.due(e.Time)...)
	}
	return append(out, t.due(touchT0.Add(24*time.Hour))...)
}

func TestLastSeenCountsAPersonsPromptsAndAnswers(t *testing.T) {
	yes := func(string, time.Time) bool { return true }
	for _, c := range []struct {
		name string
		evs  []events.Event
		want string
	}{
		{"the app's send", []events.Event{ev("session.sent", "app", 0, map[string]any{"name": "s", "from": "laptop:mac"})}, "send"},
		{"a laptop's berth", []events.Event{ev("session.sent", "berth", 0, map[string]any{"name": "s", "from": "laptop:mac"})}, "send"},
		{"the phone", []events.Event{ev("session.sent", "phone", 0, map[string]any{"name": "s", "from": "phone"})}, "send"},
		{"the app's answer", []events.Event{ev("session.sent", "app", 0, map[string]any{"name": "s", "from": "laptop:mac", "answer": true})}, "send"},
		{"started from the app", []events.Event{ev("session.started", "app", 0, map[string]any{"name": "s"})}, "start"},
		{"typed at its terminal", []events.Event{ev(adapters.Started, "claude", 0, map[string]any{"session": "s", "signal": "prompt"})}, "prompt"},
		{"answered at its terminal", []events.Event{
			ev(adapters.Waiting, "claude", 0, map[string]any{"session": "s", "reason": "permission"}),
			ev(adapters.Started, "claude", 30, map[string]any{"session": "s", "signal": "tool"}),
		}, "answer"},
	} {
		got := runTracker(yes, c.evs...)
		if len(got) != 1 || got[0].source != c.want || got[0].session != "s" {
			t.Errorf("%s: got %+v, want one %s", c.name, got, c.want)
		}
	}
}

func TestLastSeenIgnoresAgentsAndWakeups(t *testing.T) {
	yes := func(string, time.Time) bool { return true }
	no := func(string, time.Time) bool { return false }
	started := func(secs int) events.Event {
		return ev(adapters.Started, "claude", secs, map[string]any{"session": "s", "signal": "prompt"})
	}
	waited := ev(adapters.Waiting, "claude", 0, map[string]any{"session": "s", "reason": "question"})
	tool := func(secs int, extra ...string) events.Event {
		d := map[string]any{"session": "s", "signal": "tool"}
		for _, k := range extra {
			d[k] = true
		}
		return ev(adapters.Started, "claude", secs, d)
	}
	for _, c := range []struct {
		name  string
		typed func(string, time.Time) bool
		evs   []events.Event
	}{
		{"an agent's send and the prompt it starts", yes, []events.Event{ev("session.sent", "berth", 0, map[string]any{"name": "s", "from": "berth"}), started(2)}},
		{"an MCP send", yes, []events.Event{ev("session.sent", "mcp", 0, map[string]any{"name": "s", "from": "mcp"}), started(2)}},
		{"a report from a child", yes, []events.Event{ev("session.sent", "berth", 0, map[string]any{"name": "s", "from": "berth:report"}), started(2)}},
		{"a flow's send", yes, []events.Event{ev("session.sent", "flow:nightly", 0, map[string]any{"name": "s", "from": "flow:nightly"})}},
		{"a /loop wakeup: no person in the transcript", no, []events.Event{started(0)}},
		{"an agent's answer: its event comes after the tool's", yes, []events.Event{waited, tool(30), ev("session.sent", "berth", 31, map[string]any{"name": "s", "from": "berth", "answer": true})}},
		{"a rule allowing a permission at once", yes, []events.Event{waited, tool(1)}},
		{"a tool with no wait before it", yes, []events.Event{tool(5)}},
		{"a helper's tool during the wait", yes, []events.Event{waited, tool(30, "helper")}},
		{"a turn check's wait", yes, []events.Event{ev(adapters.Waiting, "claude", 0, map[string]any{"session": "s", "reason": "turn check"}), tool(30)}},
		{"the screen's guess", yes, []events.Event{ev(adapters.Started, "screen", 0, map[string]any{"session": "s", "signal": "prompt", "source": "screen"})}},
		{"started by an agent", yes, []events.Event{ev("session.started", "berth", 0, map[string]any{"name": "s"})}},
	} {
		if got := runTracker(c.typed, c.evs...); len(got) != 0 {
			t.Errorf("%s: counted %+v", c.name, got)
		}
	}
}

func TestATypedPromptWaitsForItsTranscriptLine(t *testing.T) {
	asked := 0
	tr := newTouchTracker(func(string, time.Time) bool { asked++; return true })
	e := ev(adapters.Started, "claude", 0, map[string]any{"session": "s", "signal": "prompt"})
	tr.feed(e)
	if got := tr.due(e.Time.Add(time.Second)); len(got) != 0 || asked != 0 {
		t.Fatalf("settled too soon: %+v", got)
	}
	if got := tr.due(e.Time.Add(touchSettle)); len(got) != 1 || !got[0].at.Equal(e.Time) {
		t.Fatalf("got %+v", got)
	}
}

func TestTypedPromptsAreAPersons(t *testing.T) {
	for _, c := range []struct {
		line string
		want bool
	}{
		{`{"type":"user","timestamp":"2026-03-01T12:00:00Z","message":{"content":"fix the header"}}`, true},
		{`{"type":"user","timestamp":"2026-03-01T12:01:00Z","message":{"content":[{"type":"text","text":"and the footer"}]}}`, true},
		{`{"type":"user","timestamp":"2026-03-01T12:01:30Z","message":{"content":[{"type":"text","text":"this one"},{"type":"image"}]}}`, true},
		{`{"type":"user","isMeta":true,"timestamp":"2026-03-01T12:02:00Z","message":{"content":"wake up, loop"}}`, false},
		{`{"type":"user","timestamp":"2026-03-01T12:03:00Z","message":{"content":[{"type":"tool_result","content":"ok"}]}}`, false},
		{`{"type":"user","timestamp":"2026-03-01T12:04:00Z","message":{"content":"<berth-notification>done</berth-notification>"}}`, false},
		{`{"type":"user","timestamp":"2026-03-01T12:04:30Z","message":{"content":"[Request interrupted by user]"}}`, false},
		{`{"type":"assistant","timestamp":"2026-03-01T12:05:00Z","message":{"content":"\"user\""}}`, false},
		{`not json "user"`, false},
	} {
		if _, got := typedPromptAt([]byte(c.line)); got != c.want {
			t.Errorf("%s: got %v", c.line, got)
		}
	}
}

func TestPromptIndexReadsOnlyWhatsNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	one := `{"type":"user","timestamp":"2026-03-01T12:00:00Z","message":{"content":"one"}}` + "\n"
	two := `{"type":"user","timestamp":"2026-03-01T13:00:00Z","message":{"content":"two"}}`
	if err := writeTestFile(path, one+two); err != nil { // the last line is half written
		t.Fatal(err)
	}
	x := &promptIndex{}
	if !x.has(path, touchT0.Add(5*time.Second)) || x.has(path, touchT0.Add(time.Hour)) {
		t.Error("first read")
	}
	if err := writeTestFile(path, one+two+"\n"); err != nil {
		t.Fatal(err)
	}
	if !x.has(path, touchT0.Add(time.Hour)) || !x.has(path, touchT0) {
		t.Error("second read")
	}
	if got := len(x.files[path].times); got != 2 {
		t.Errorf("read the file again: %d times", got)
	}
	if x.has(filepath.Join(t.TempDir(), "gone"), touchT0) {
		t.Error("no file, no prompt")
	}
}

// A new agent can read the prompt it started with long after the send, past
// the tracker's window: the ledger still knows it was sent.
func TestASentPromptIsntTypedHoweverLateItStarts(t *testing.T) {
	turns := &Turns{}
	bx := &Box{Turns: turns}
	sent := touchT0
	// An agent whose hooks are known to say when a prompt starts.
	turns.Observe(events.Event{Seq: 0, Type: adapters.Ready, Origin: "claude", Time: sent.Add(-time.Hour), Data: map[string]any{"session": "s", "agent": "claude", "path": "/w"}})
	turns.Observe(events.Event{Seq: 1, Type: "session.sent", Origin: "berth", Time: sent, Data: map[string]any{"name": "s", "from": "berth", "agent": "claude"}})
	started := sent.Add(time.Minute)
	turns.Observe(events.Event{Seq: 2, Type: adapters.Started, Origin: "claude", Time: started, Data: map[string]any{"session": "s", "signal": "prompt", "path": "/w"}})
	if !bx.promptWasSent("s", started) {
		t.Errorf("the turn from a send: %+v", turns.List("s", 5))
	}
	typed := started.Add(time.Hour)
	turns.Observe(events.Event{Seq: 3, Type: adapters.Finished, Origin: "claude", Time: typed.Add(-time.Minute), Data: map[string]any{"session": "s", "path": "/w"}})
	turns.Observe(events.Event{Seq: 4, Type: adapters.Started, Origin: "claude", Time: typed, Data: map[string]any{"session": "s", "signal": "prompt", "path": "/w"}})
	if bx.promptWasSent("s", typed) {
		t.Errorf("a prompt with no send: %+v", turns.List("s", 5))
	}
}

// The commonest late start: the prompt a new agent starts with, sent before any
// hook of its has been heard, so the ledger counts its turn from the send.
func TestAFirstPromptReadLateIsntTyped(t *testing.T) {
	turns := &Turns{}
	bx := &Box{Turns: turns}
	sent := touchT0
	turns.Observe(events.Event{Seq: 1, Type: "session.sent", Origin: "berth", Time: sent, Data: map[string]any{"name": "s", "from": "berth", "agent": "claude", "startup": true}})
	late := sent.Add(time.Minute)
	turns.Observe(events.Event{Seq: 2, Type: adapters.Started, Origin: "claude", Time: late, Data: map[string]any{"session": "s", "signal": "prompt", "path": "/w"}})
	if !bx.promptWasSent("s", late) {
		t.Errorf("its hook, a minute after the send: %+v", turns.List("s", 5))
	}
	ended := late.Add(time.Minute)
	turns.Observe(events.Event{Seq: 3, Type: adapters.Finished, Origin: "claude", Time: ended, Data: map[string]any{"session": "s", "path": "/w"}})
	typed := ended.Add(time.Hour)
	turns.Observe(events.Event{Seq: 4, Type: adapters.Started, Origin: "claude", Time: typed, Data: map[string]any{"session": "s", "signal": "prompt", "path": "/w"}})
	if bx.promptWasSent("s", typed) {
		t.Errorf("a later prompt with no send: %+v", turns.List("s", 5))
	}
}

// A restart between a wait and its answer, or between a prompt and its
// transcript line, loses neither: the store keeps them with its cursor.
func TestARestartKeepsWhatTheReaderHeld(t *testing.T) {
	yes := func(string, time.Time) bool { return true }
	s := newTestWork(t)
	tr := newTouchTracker(yes)
	tr.feed(ev(adapters.Waiting, "claude", 0, map[string]any{"session": "waited", "reason": "question"}))
	tr.feed(ev(adapters.Started, "claude", 1, map[string]any{"session": "typed", "signal": "prompt"}))
	tr.feed(ev("session.sent", "berth", 2, map[string]any{"name": "agent", "from": "berth"}))
	if err := s.Flush(42, tr.state(touchT0.Add(3*time.Second))); err != nil {
		t.Fatal(err)
	}

	cursor, kept := (&WorkStore{Path: s.Path, Box: s.Box}).resume()
	if cursor != 42 {
		t.Fatalf("cursor %d", cursor)
	}
	again := newTouchTracker(yes)
	again.restore(kept)
	got := again.feed(ev(adapters.Started, "claude", 60, map[string]any{"session": "waited", "signal": "tool"}))
	// The agent's send still explains the prompt it starts.
	got = append(got, again.feed(ev(adapters.Started, "claude", 4, map[string]any{"session": "agent", "signal": "prompt"}))...)
	got = append(got, again.due(touchT0.Add(time.Hour))...)
	sources := map[string]string{}
	for _, n := range got {
		sources[n.session] = n.source
	}
	if len(got) != 2 || sources["waited"] != "answer" || sources["typed"] != "prompt" {
		t.Errorf("after a restart: %+v", got)
	}
}

func TestTrackerStateForgetsWhatCantMatter(t *testing.T) {
	tr := newTouchTracker(nil)
	tr.feed(ev("session.sent", "berth", 0, map[string]any{"name": "old", "from": "berth"}))
	tr.feed(ev(adapters.Waiting, "claude", 0, map[string]any{"session": "stale", "reason": "question"}))
	st := tr.state(touchT0.Add(time.Hour))
	if len(st.Sent) != 0 || len(st.Waits) != 1 {
		t.Errorf("an hour on: %+v", st)
	}
	if st = tr.state(touchT0.Add(touchWaitMax + time.Hour)); len(st.Waits) != 0 {
		t.Errorf("past the journal's reach: %+v", st)
	}
}

func TestTouchMovesOnlyForward(t *testing.T) {
	s := newTestWork(t)
	if _, err := s.Reconcile([]Session{agentSession("a", "shop/t", "x")}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 3, 1, 11, 0, 0, 0, time.UTC)
	if id, err := s.Touch("a", at); err != nil || id == "" {
		t.Fatalf("%q %v", id, err)
	}
	if id, _ := s.Touch("a", at); id != "" {
		t.Error("a repeat moved it")
	}
	if id, _ := s.Touch("a", at.Add(-time.Minute)); id != "" {
		t.Error("an older one moved it")
	}
	if id, _ := s.Touch("nobody", at); id != "" {
		t.Error("no record, no touch")
	}
	// From before the session existed: an earlier session of that name.
	if _, err := s.Reconcile(nil); err != nil {
		t.Fatal(err)
	}
	s2 := newTestWork(t)
	if _, err := s2.Reconcile([]Session{agentSession("a", "shop/t", "x")}); err != nil {
		t.Fatal(err)
	}
	if id, _ := s2.Touch("a", time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)); id != "" {
		t.Error("a touch from before it started moved it")
	}
	if got := recordOf(t, s2, "a"); !got.LastSeen.IsZero() {
		t.Errorf("got %+v", got)
	}
}

// waitWork polls until cond holds, on a real journal and a real goroutine.
func waitWork(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A person's prompt is kept; an agent's send isn't; and a restart reading the
// same journal again moves nothing. This is RunWork with a real journal.
func TestLastSeenIgnoresAgentSendsAndARestart(t *testing.T) {
	dir := t.TempDir()
	j, err := events.OpenJournal(filepath.Join(dir, "journal"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	bus := &events.Bus{Journal: j}
	sessions := testSessions(t)
	bx := &Box{Name: "acme-box", Sessions: sessions, Events: bus}
	bx.Work = &WorkStore{Path: filepath.Join(dir, "work.json"), Box: bx.Name}
	if _, err := sessions.create(context.Background(), "acme-agent", "shop/fix", dir, "sleep 300", "claude", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	seen := func() time.Time {
		if all := bx.Work.List("", true); len(all) == 1 {
			return all[0].LastSeen
		}
		return time.Time{}
	}
	human := bus.Publish(events.Event{Type: "session.sent", Origin: "app", Data: map[string]any{"name": "acme-agent", "from": "laptop:mac"}})

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { bx.RunWork(ctx); close(done) }()
	// Caught up from the journal: the person's send, before it was running.
	waitWork(t, func() bool { return seen().Equal(human.Time) })

	bus.Publish(events.Event{Type: "session.sent", Origin: "mcp", Data: map[string]any{"name": "acme-agent", "from": "mcp"}})
	bus.Publish(events.Event{Type: adapters.Started, Origin: "claude", Data: map[string]any{"session": "acme-agent", "signal": "prompt"}})
	stop()
	<-done
	if !seen().Equal(human.Time) {
		t.Fatalf("an agent's send moved last_seen to %v", seen())
	}

	// A restart: a new store over the same file, the journal read again.
	bx.Work = &WorkStore{Path: bx.Work.Path, Box: bx.Name}
	ctx, stop = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { bx.RunWork(ctx); close(done) }()
	waitWork(t, func() bool { return bx.Work.Cursor() > 0 })
	time.Sleep(100 * time.Millisecond)
	if !seen().Equal(human.Time) {
		t.Errorf("a restart moved last_seen to %v", seen())
	}
	later := bus.Publish(events.Event{Type: "session.sent", Origin: "app", Data: map[string]any{"name": "acme-agent", "from": "laptop:mac", "answer": true}})
	waitWork(t, func() bool { return seen().Equal(later.Time) })
	stop()
	<-done
}
