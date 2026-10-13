package box

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/events"
	"github.com/cosscom/shipyard/internal/integrations"
)

// What a waiting agent asks for, from its PermissionRequest hook, goes on
// the turn's wait and is shown on the session; it never reaches the bus
// (so never the journal, hooks or flows).
func TestAPermissionRequestIsKeptOnTheWaitAndNeverPublished(t *testing.T) {
	turns := &Turns{}
	c, bus := servedBox(t, func(b *Box) { b.Turns = turns })
	turns.Attach(bus)
	var seen []events.Event
	bus.Observe(func(e events.Event) { seen = append(seen, e) })
	bus.Publish(events.Event{Type: "session.started", Data: map[string]any{"name": "s", "path": "/w", "agent": "claude"}})
	hook(bus, "agent.ready", "s", "/w", "claude")
	hook(bus, "agent.started", "s", "/w", "claude", "signal", "prompt")

	// What `berthd hook claude PermissionRequest` sends.
	e, ok := integrations.Translate("claude", "PermissionRequest", []byte(`{"session_id":"x","cwd":"/w","hook_event_name":"PermissionRequest",
		"tool_name":"Bash","tool_input":{"command":"pnpm test --filter checkout","description":"Run the checkout tests"},"tool_use_id":"t1"}`))
	if !ok {
		t.Fatal("PermissionRequest was not translated")
	}
	e.Data["session"] = "s"
	if st := call(t, c, "POST", "/v1/events", "claude", map[string]any{"type": e.Type, "data": e.Data}, nil); st != 200 {
		t.Fatalf("emit = %d", st)
	}
	// Its Notification comes after, with the message.
	n, _ := integrations.Translate("claude", "Notification", []byte(`{"cwd":"/w","notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}`))
	n.Data["session"] = "s"
	call(t, c, "POST", "/v1/events", "claude", map[string]any{"type": n.Type, "data": n.Data}, nil)

	want := Ask{Tool: "Bash", Input: "pnpm test --filter checkout", Why: "Run the checkout tests", Message: "Claude needs your permission to use Bash"}
	st, _ := turns.State("s")
	if st.State != "waiting" || st.Ask == nil || !reflect.DeepEqual(*st.Ask, want) {
		t.Fatalf("state = %+v ask=%+v", st, st.Ask)
	}
	for _, e := range seen {
		b, _ := json.Marshal(e.Data)
		if strings.Contains(string(b), "pnpm test") || strings.Contains(string(b), "needs your permission") || e.Data["ask"] != nil {
			t.Fatalf("the ask was published: %s %s", e.Type, b)
		}
	}

	// Approved: the tool runs. The wait closes and keeps what was asked;
	// the session no longer shows an ask.
	hook(bus, "agent.started", "s", "/w", "claude", "signal", "tool")
	got, _ := turns.Get("s#1")
	if len(got.Waits) != 1 || got.Waits[0].End.IsZero() || got.Waits[0].Ask == nil || got.Waits[0].Ask.Input != want.Input {
		t.Fatalf("after approval: %+v", got.Waits)
	}
	if st, _ := turns.State("s"); st.Ask != nil {
		t.Fatalf("a running agent still asks: %+v", st.Ask)
	}
	// An ask with no wait open (late, or for an agent working again) is
	// dropped.
	turns.NoteAsk(map[string]any{"session": "s"}, map[string]any{"tool": "Bash", "input": "rm -rf /"})
	if got, _ := turns.Get("s#1"); got.Waits[0].Ask.Input != want.Input {
		t.Fatalf("a late ask changed the wait: %+v", got.Waits[0].Ask)
	}
}

// Held prompts can be listed (a preview, never more), cancelled, and sent
// at once; an agent at a question refuses one without force.
func TestTheInboxCanBeListedCancelledAndSentNow(t *testing.T) {
	turns := &Turns{InboxPath: filepath.Join(t.TempDir(), "inbox.json")}
	var bx *Box
	c, bus := servedBox(t, func(b *Box) { b.Turns = turns; bx = b })
	turns.Attach(bus)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go turns.Run(ctx, bx)
	repo := gitRepo(t)
	call(t, c, "POST", "/v1/locations", "", map[string]string{"name": "shop", "path": repo}, nil)
	fake := filepath.Join(t.TempDir(), "claude")
	os.WriteFile(fake, []byte("#!/bin/sh\nexec cat\n"), 0o755)
	var sess Session
	call(t, c, "POST", "/v1/sessions", "", map[string]string{"location": "shop", "name": "agent", "command": fake}, &sess)
	hook(bus, "agent.ready", "agent", sess.Dir, "claude")
	hook(bus, "agent.started", "agent", sess.Dir, "claude", "signal", "prompt") // busy

	var a, b SendResult
	call(t, c, "POST", "/v1/sessions/agent/send", "", SendRequest{Text: "then write the docs", When: "idle"}, &a)
	long := strings.Repeat("é", 400)
	call(t, c, "POST", "/v1/sessions/agent/send", "", SendRequest{Text: long, When: "idle"}, &b)
	if !a.Queued || !b.Queued {
		t.Fatalf("sends = %+v %+v", a, b)
	}
	var q []QueuedPrompt
	call(t, c, "GET", "/v1/sessions/agent/queue", "", nil, &q)
	if len(q) != 2 || q[0].Turn != a.Turn || q[0].Preview != "then write the docs" || q[1].Length != 400 || len(q[1].Preview) > queuePreview+4 || !strings.HasSuffix(q[1].Preview, "…") {
		t.Fatalf("queue = %+v", q)
	}
	var list []Session
	call(t, c, "GET", "/v1/sessions", "", nil, &list)
	if len(list) != 1 || list[0].Queued != 2 {
		t.Fatalf("sessions = %+v", list)
	}

	// Cancel the second: its turn ends, the inbox file forgets it.
	if st := call(t, c, "DELETE", "/v1/sessions/agent/queue/"+strings.ReplaceAll(b.Turn, "#", "%23"), "", nil, nil); st != 200 {
		t.Fatalf("cancel = %d", st)
	}
	if got, _ := turns.Get(b.Turn); got.State != "lost" || got.Status != "cancelled" {
		t.Fatalf("cancelled turn = %+v", got)
	}
	if st := call(t, c, "DELETE", "/v1/sessions/agent/queue/"+strings.ReplaceAll(b.Turn, "#", "%23"), "", nil, nil); st != 404 {
		t.Fatalf("cancel twice = %d", st)
	}
	time.Sleep(250 * time.Millisecond) // the ledger writes every 100 ms
	if data, _ := os.ReadFile(turns.InboxPath); strings.Contains(string(data), "é") {
		t.Fatalf("the inbox file kept a cancelled prompt: %s", data)
	}

	// At a question, Send now needs force.
	hook(bus, "agent.waiting", "agent", sess.Dir, "claude", "reason", "question")
	path := "/v1/sessions/agent/queue/" + strings.ReplaceAll(a.Turn, "#", "%23") + "/send"
	var refused map[string]string
	if st := call(t, c, "POST", path, "", map[string]bool{"force": false}, &refused); st != 409 || !strings.Contains(refused["error"], "waiting") {
		t.Fatalf("send now at a question = %d %v", st, refused)
	}
	// Working again: it is typed now, ahead of the turn's end.
	hook(bus, "agent.started", "agent", sess.Dir, "claude", "signal", "tool")
	var now SendResult
	if st := call(t, c, "POST", path, "", nil, &now); st != 200 || !now.Sent || now.Turn != a.Turn {
		t.Fatalf("send now = %d %+v", st, now)
	}
	if got, _ := turns.Get(a.Turn); got.State != "pending" || got.SentSeq == 0 {
		t.Fatalf("sent turn = %+v", got)
	}
	call(t, c, "GET", "/v1/sessions/agent/queue", "", nil, &q)
	if len(q) != 0 {
		t.Fatalf("queue after send now = %+v", q)
	}
}

// A file's diff in the session's worktree: tracked or new, capped, and
// never a path outside it.
func TestASessionsFileDiff(t *testing.T) {
	c, _ := servedBox(t)
	repo := gitRepo(t)
	call(t, c, "POST", "/v1/locations", "", map[string]string{"name": "shop", "path": repo}, nil)
	call(t, c, "POST", "/v1/sessions", "", map[string]string{"location": "shop", "name": "agent", "command": "sh"}, nil)
	os.WriteFile(filepath.Join(repo, "README"), []byte("hi\nthere\n"), 0o644)
	os.MkdirAll(filepath.Join(repo, "src"), 0o755)
	os.WriteFile(filepath.Join(repo, "src", "new.ts"), []byte("export const a = 1;\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "big.txt"), []byte(strings.Repeat("a line of text\n", 10000)), 0o644)

	var d SessionDiff
	if st := call(t, c, "GET", "/v1/sessions/agent/diff?file=README", "", nil, &d); st != 200 || !strings.Contains(d.Diff, "+there") || d.Untracked {
		t.Fatalf("tracked = %d %+v", st, d)
	}
	d = SessionDiff{}
	call(t, c, "GET", "/v1/sessions/agent/diff?file="+filepath.Join(repo, "src", "new.ts"), "", nil, &d)
	if !d.Untracked || d.File != "src/new.ts" || !strings.Contains(d.Diff, "+export const a = 1;") {
		t.Fatalf("untracked = %+v", d)
	}
	d = SessionDiff{}
	call(t, c, "GET", "/v1/sessions/agent/diff?file=big.txt", "", nil, &d)
	if !d.Truncated || len(d.Diff) > sessionDiffLimit || !strings.HasSuffix(d.Diff, "\n") {
		t.Fatalf("big: truncated=%v len=%d", d.Truncated, len(d.Diff))
	}
	for _, bad := range []string{"", "../etc/passwd", "/etc/passwd"} {
		if st := call(t, c, "GET", "/v1/sessions/agent/diff?file="+bad, "", nil, nil); st != 400 {
			t.Errorf("file=%q: %d, want 400", bad, st)
		}
	}
}
