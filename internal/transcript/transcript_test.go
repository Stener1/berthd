package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path string, lines ...any) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		b, _ := json.Marshal(l)
		f.Write(append(b, '\n'))
	}
}

type m = map[string]any

func user(content any) m {
	return m{"type": "user", "timestamp": "2026-10-04T12:00:00Z", "message": m{"role": "user", "content": content}}
}
func assistant(blocks ...m) m {
	return m{"type": "assistant", "timestamp": "2026-10-04T12:00:01Z", "message": m{"role": "assistant", "content": blocks}}
}
func tool(id, name string, input m) m {
	return m{"type": "tool_use", "id": id, "name": name, "input": input}
}
func result(id string) m {
	return user([]m{{"type": "tool_result", "tool_use_id": id, "content": "secret output"}})
}

func kinds(items []Item) string {
	var k []string
	for _, it := range items {
		k = append(k, it.Kind)
	}
	return strings.Join(k, ",")
}

func TestClaudeTurn(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	write(t, p,
		m{"type": "mode", "mode": "x"},
		user("Make webhook retries safe"),
		user("<command-name>/clear</command-name>"),
		assistant(m{"type": "thinking", "thinking": "private"}, m{"type": "text", "text": "I'll trace the webhook."}),
		assistant(tool("r1", "Read", m{"file_path": "/w/shop/apps/web/webhook.ts"})),
		assistant(tool("r2", "Read", m{"file_path": "/w/shop/apps/web/order.ts"})),
		result("r1"),
	)
	r := NewReader()
	res, err := r.Read("claude", p, "/w/shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(res.Items); got != "user,command,text,tools" {
		t.Fatalf("kinds = %s", got)
	}
	if c := res.Items[1]; c.Command != "/clear" {
		t.Fatalf("command = %+v", c)
	}
	g := res.Items[3]
	if g.Done || len(g.Items) != 2 || g.Items[0].Target != "webhook.ts" || !g.Items[0].File {
		t.Fatalf("open group = %+v", g)
	}
	for _, it := range res.Items {
		if strings.Contains(it.Text, "private") || strings.Contains(it.Text, "secret") {
			t.Fatalf("thinking or tool output leaked: %+v", it)
		}
	}

	// The file grows: the open group comes again, now done, then the rest.
	write(t, p,
		result("r2"),
		assistant(tool("a1", "Agent", m{"description": "Explore retry paths", "subagent_type": "Explore"})),
		assistant(tool("e1", "Edit", m{"file_path": "/w/shop/apps/web/webhook.ts", "old_string": "a\nb", "new_string": "a\nb\nc\nd"})),
		assistant(tool("b1", "Bash", m{"command": "pnpm test payments\necho done"})),
		result("a1"),
		result("b1"),
		assistant(m{"type": "text", "text": "Done."}),
	)
	res2, err := r.Read("claude", p, "/w/shop", res.Next)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(res2.Items); got != "tools,crew,edit,tools,text" {
		t.Fatalf("kinds after growth = %s", got)
	}
	if !res2.Items[0].Done || res2.Items[0].ID != g.ID {
		t.Fatalf("the open group should come again, done, with its ID: %+v", res2.Items[0])
	}
	e := res2.Items[2]
	if e.File != "apps/web/webhook.ts" || e.Added != 4 || e.Removed != 2 {
		t.Fatalf("edit = %+v", e)
	}
	if run := res2.Items[3]; run.Items[0].Target != "pnpm test payments …" || !run.Done {
		t.Fatalf("run = %+v", run)
	}
	if len(res2.Crew) != 1 || res2.Crew[0].State != "finished" || res2.Crew[0].Name != "Explore retry paths" {
		t.Fatalf("crew = %+v", res2.Crew)
	}

	// Nothing new: nothing sent.
	res3, _ := r.Read("claude", p, "/w/shop", res2.Next)
	if len(res3.Items) != 0 || res3.Next != res2.Next {
		t.Fatalf("idle read = %+v", res3)
	}
}

func TestPartialLineWaits(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	b, _ := json.Marshal(user("hello there"))
	os.WriteFile(p, b[:10], 0o600) // half a line
	r := NewReader()
	res, _ := r.Read("claude", p, "", 0)
	if len(res.Items) != 0 {
		t.Fatalf("half a line parsed: %+v", res.Items)
	}
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.Write(append(b[10:], '\n'))
	f.Close()
	res, _ = r.Read("claude", p, "", 0)
	if kinds(res.Items) != "user" || res.Items[0].Text != "hello there" {
		t.Fatalf("after the rest = %+v", res.Items)
	}
}

func TestKeepsOnlyTheEnd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	var lines []any
	for i := 0; i < keep+50; i++ {
		lines = append(lines, assistant(m{"type": "text", "text": "line"}))
	}
	write(t, p, lines...)
	res, _ := NewReader().Read("claude", p, "", 0)
	if len(res.Items) != keep || res.Next != keep+50 || !res.Truncated {
		t.Fatalf("items %d next %d truncated %v", len(res.Items), res.Next, res.Truncated)
	}
}

func TestCodex(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout.jsonl")
	ri := func(payload m) m {
		return m{"type": "response_item", "timestamp": "2026-10-04T12:00:00Z", "payload": payload}
	}
	args := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	write(t, p,
		m{"type": "session_meta", "payload": m{"cwd": "/w/shop"}},
		ri(m{"type": "message", "role": "user", "content": []m{{"type": "input_text", "text": "<environment_context>x</environment_context>"}}}),
		ri(m{"type": "message", "role": "user", "content": []m{{"type": "input_text", "text": "Export orders as CSV"}}}),
		ri(m{"type": "function_call", "name": "shell", "call_id": "c1", "arguments": args(m{"command": []string{"bash", "-lc", "sed -n 1,80p apps/web/orders.ts"}})}),
		ri(m{"type": "function_call_output", "call_id": "c1", "output": "secret"}),
		ri(m{"type": "function_call", "name": "shell", "call_id": "c2", "arguments": args(m{"command": []string{"apply_patch", "*** Begin Patch\n*** Update File: apps/web/orders.ts\n@@\n-old\n+new\n+more\n*** End Patch"}})}),
		ri(m{"type": "function_call", "name": "shell", "call_id": "c3", "arguments": args(m{"command": []string{"bash", "-lc", "pnpm test"}})}),
		ri(m{"type": "message", "role": "assistant", "content": []m{{"type": "output_text", "text": "Added the export."}}}),
	)
	res, err := NewReader().Read("codex", p, "/w/shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(res.Items); got != "user,tools,edit,tools,text" {
		t.Fatalf("kinds = %s", got)
	}
	if r := res.Items[1].Items[0]; r.Verb != "Read" || r.Target != "orders.ts" {
		t.Fatalf("read = %+v", r)
	}
	if e := res.Items[2]; e.File != "apps/web/orders.ts" || e.Added != 2 || e.Removed != 1 {
		t.Fatalf("edit = %+v", e)
	}
	if cwd := codexCwd(p); cwd != "/w/shop" {
		t.Fatalf("cwd = %q", cwd)
	}
}

func TestLongLineIsSkipped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, p, user(strings.Repeat("x", maxLine+10)), user("after"))
	res, _ := NewReader().Read("claude", p, "", 0)
	if kinds(res.Items) != "user" || res.Items[0].Text != "after" {
		t.Fatalf("items = %+v", kinds(res.Items))
	}
}

// Two agents in one worktree read their own conversations, not one shared
// file: by ID when the hooks gave it, else by when each began.
func TestAssignClaudeGivesEachAgentItsOwn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	dir := "/w/shop"
	proj := ClaudeDir(dir)
	os.MkdirAll(proj, 0o700)
	at := func(ts string) m {
		return m{"type": "user", "timestamp": ts, "message": m{"role": "user", "content": "hi"}}
	}
	write(t, filepath.Join(proj, "aaaaaaaa-1.jsonl"), at("2026-10-04T10:00:05Z"))
	write(t, filepath.Join(proj, "bbbbbbbb-2.jsonl"), at("2026-10-04T10:05:05Z"))
	write(t, filepath.Join(proj, "cccccccc-3.jsonl"), at("2026-10-04T10:09:00Z"))
	t0, _ := time.Parse(time.RFC3339, "2026-10-04T10:00:00Z")
	got := AssignClaude(dir, []Claim{
		{Name: "first", Started: t0},
		{Name: "second", Started: t0.Add(5 * time.Minute)},
		{Name: "named", ID: "cccccccc-3", Started: t0},
	})
	if filepath.Base(got["first"]) != "aaaaaaaa-1.jsonl" || filepath.Base(got["second"]) != "bbbbbbbb-2.jsonl" || filepath.Base(got["named"]) != "cccccccc-3.jsonl" {
		t.Fatalf("%v", got)
	}
}

// A new agent that hasn't written its conversation yet shows none, not an
// older conversation from the same folder; a resumed one (its file began
// before the agent did) still gets the file it writes to.
func TestAssignClaudeNewAgentGetsNoOldConversation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	dir := "/w/shop"
	proj := ClaudeDir(dir)
	os.MkdirAll(proj, 0o700)
	old := filepath.Join(proj, "dddddddd-4.jsonl")
	write(t, old, m{"type": "user", "timestamp": "2026-10-04T08:00:00Z", "message": m{"role": "user", "content": "hi"}})
	hour := time.Now().Add(-time.Hour)
	os.Chtimes(old, hour, hour)
	if got := AssignClaude(dir, []Claim{{Name: "new", Started: time.Now()}}); got["new"] != "" {
		t.Fatalf("a new agent got %q", got["new"])
	}
	os.Chtimes(old, time.Now(), time.Now())
	if got := AssignClaude(dir, []Claim{{Name: "resumed", Started: time.Now().Add(-time.Minute)}}); got["resumed"] != old {
		t.Fatalf("a resumed agent got %q", got["resumed"])
	}
}

// Plan mode: loading tools and writing the plan file are bookkeeping, the
// plan presented for approval reads as the answer, a long reply is kept
// whole, and a helper started in the background is out until its
// notification says it is back.
func TestClaudePlanModeAndBackgroundHelper(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	long := strings.Repeat("All cancellations go through one handler. ", 200)
	write(t, p,
		user("Explain slots"),
		assistant(tool("a1", "Agent", m{"description": "Trace slots", "subagent_type": "Explore", "run_in_background": true})),
		user([]m{{"type": "tool_result", "tool_use_id": "a1", "content": []m{{"type": "text", "text": "Async agent launched successfully.\nagentId: x"}}}}),
	)
	r := NewReader()
	res, _ := r.Read("claude", p, "/w/shop", 0)
	if len(res.Crew) != 1 || res.Crew[0].State != "running" {
		t.Fatalf("a background helper is still out: %+v", res.Crew)
	}
	write(t, p,
		m{"type": "queue-operation", "operation": "enqueue", "timestamp": "2026-10-04T12:05:00Z", "content": "<task-notification>\n<task-id>x</task-id>\n<tool-use-id>a1</tool-use-id>\n<status>completed</status>\n</task-notification>"},
		assistant(m{"type": "text", "text": long}),
		assistant(tool("t1", "ToolSearch", m{"query": "select:ExitPlanMode"})),
		assistant(tool("w1", "Write", m{"file_path": "/home/u/.claude/plans/slots.md", "content": "# Plan\n"})),
		assistant(tool("x1", "ExitPlanMode", m{"plan": "# Slots\nHow they are computed."})),
	)
	res2, _ := r.Read("claude", p, "/w/shop", res.Next)
	if got := kinds(res2.Items); got != "text,text" {
		t.Fatalf("kinds = %s", got)
	}
	if res2.Items[0].Text != strings.TrimSpace(long) || res2.Items[1].Text != "# Slots\nHow they are computed." {
		t.Fatalf("texts = %q…, %q", res2.Items[0].Text[:40], res2.Items[1].Text)
	}
	if res2.Crew[0].State != "finished" || res2.Crew[0].Until == res2.Crew[0].Since {
		t.Fatalf("helper back = %+v", res2.Crew[0])
	}

	// The plan sent back with words: they read as the person's.
	write(t, p, user([]m{{"type": "tool_result", "tool_use_id": "x1", "is_error": true, "content": "The user doesn't want to proceed with this tool use. The tool use was rejected. To tell you how to proceed, the user said:\nNo changes, stop here."}}))
	res3, _ := r.Read("claude", p, "/w/shop", res2.Next)
	if kinds(res3.Items) != "user" || res3.Items[0].Text != "No changes, stop here." {
		t.Fatalf("feedback = %+v", res3.Items)
	}
}

// Opening a call shows what the terminal does: the full command and its
// output, an edit's exact change, a written file.
func TestDetailOpensACall(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, p,
		assistant(tool("b1", "Bash", m{"command": "pnpm test payments\necho done"})),
		user([]m{{"type": "tool_result", "tool_use_id": "b1", "content": "Tests 2 passed (2)\ndone"}}),
		assistant(tool("e1", "Edit", m{"file_path": "/w/shop/apps/web/webhook.ts", "old_string": "retry()", "new_string": "retry({ max: 5 })"})),
		assistant(tool("w1", "Write", m{"file_path": "/w/shop/a.test.ts", "content": "line 1\nline 2"})),
		user([]m{{"type": "tool_result", "tool_use_id": "e1", "content": []m{{"type": "text", "text": "edited"}}, "is_error": false}}),
	)
	d, err := Detail("claude", p, "/w/shop", "b1")
	if err != nil || d.Name != "Bash" || d.Command != "pnpm test payments\necho done" || d.Output != "Tests 2 passed (2)\ndone" || d.Pending {
		t.Fatalf("bash %+v %v", d, err)
	}
	d, _ = Detail("claude", p, "/w/shop", "e1")
	if d.File != "apps/web/webhook.ts" || d.Old != "retry()" || d.New != "retry({ max: 5 })" || d.Output != "edited" {
		t.Fatalf("edit %+v", d)
	}
	d, _ = Detail("claude", p, "/w/shop", "w1")
	if d.New != "line 1\nline 2" || !d.Pending {
		t.Fatalf("write %+v", d)
	}
	if _, err := Detail("claude", p, "/w/shop", "nope"); err != ErrNoTool {
		t.Fatalf("missing: %v", err)
	}
	// The calls and edits in the stream carry their IDs.
	res, _ := NewReader().Read("claude", p, "/w/shop", 0)
	var ids []string
	for _, it := range res.Items {
		for _, c := range it.Items {
			ids = append(ids, c.ID)
		}
		if it.Kind == "edit" {
			ids = append(ids, it.Tool)
		}
	}
	if strings.Join(ids, ",") != "b1,e1,w1" {
		t.Fatalf("ids %v", ids)
	}
}

func TestCapOutputKeepsHeadAndTail(t *testing.T) {
	long := strings.Repeat("x\n", outputCap)
	out, cut := capOutput(long)
	if !cut || len(out) > outputCap+64 || !strings.Contains(out, "more lines") {
		t.Fatalf("len %d cut %v", len(out), cut)
	}
}

// A message typed while Claude works reaches the model mid-turn as a
// queued_command attachment: it shows there, before the reply to it, once.
func TestQueuedMessageShowsWhereItWasRead(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, p,
		user("first task"),
		m{"type": "queue-operation", "operation": "enqueue", "content": "also check CI"},
		assistant(m{"type": "text", "text": "Working on it."}),
		m{"type": "attachment", "attachment": m{"type": "queued_command", "prompt": []m{{"type": "text", "text": "also check CI"}}}},
		assistant(m{"type": "text", "text": "CI fails on the i18n test."}),
		user("also check CI"),
	)
	res, _ := NewReader().Read("claude", p, "", 0)
	var got []string
	for _, it := range res.Items {
		got = append(got, it.Kind+":"+it.Text)
	}
	want := "user:first task|text:Working on it.|user:also check CI|text:CI fails on the i18n test."
	if strings.Join(got, "|") != want {
		t.Fatalf("got %v", got)
	}
}

func TestPlainStripsTerminalCodes(t *testing.T) {
	in := "\x1b[38;2;5;5;5m─\x1b[m 🥊 lefthook v2.1.9 \x1b[1mpre-commit\x1b[m\r\n\x1b]0;title\x07ok"
	if got := plain(in); got != "─ 🥊 lefthook v2.1.9 pre-commit\nok" {
		t.Fatalf("%q", got)
	}
}

// Commands typed to Claude Code read as one item each: the command, what
// followed it, and the output its program printed, colours dropped; /context's
// Markdown wins over its drawing; a shell command reads the same way.
func TestClaudeCommands(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	sys := func(content string) m {
		return m{"type": "system", "subtype": "local_command", "content": content, "timestamp": "2026-10-04T12:00:00Z"}
	}
	write(t, p,
		user("<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args>opus</command-args>"),
		user("<local-command-stdout>Set model to \x1b[1mOpus 5.5\x1b[22m</local-command-stdout>"),
		sys("<command-name>/context</command-name>\n<command-message>context</command-message>\n<command-args></command-args>"),
		sys("<local-command-stdout>\x1b[1mContext Usage\x1b[22m ⛁ ⛁</local-command-stdout>"),
		m{"type": "user", "isMeta": true, "message": m{"role": "user", "content": "## Context Usage\n\n**Tokens:** 46k"}},
		user("<bash-input>ls</bash-input>"),
		user("<bash-stdout>README.md</bash-stdout><bash-stderr></bash-stderr>"),
		m{"type": "user", "isMeta": true, "message": m{"role": "user", "content": "A goal hook is active"}},
	)
	r := NewReader()
	res, err := r.Read("claude", p, dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(res.Items); got != "command,command,command" {
		t.Fatalf("kinds = %s", got)
	}
	if c := res.Items[0]; c.Command != "/model" || c.Args != "opus" || c.Text != "Set model to Opus 5.5" {
		t.Errorf("model = %+v", c)
	}
	if c := res.Items[1]; c.Command != "/context" || !c.Markdown || !strings.HasPrefix(c.Text, "## Context Usage") {
		t.Errorf("context = %+v", c)
	}
	if c := res.Items[2]; c.Command != "!" || c.Args != "ls" || c.Text != "README.md" || c.Error {
		t.Errorf("shell = %+v", c)
	}

	// /compact: its boundary says it; the entry after it and its hooks'
	// reports add nothing.
	write(t, p,
		m{"type": "system", "subtype": "compact_boundary", "content": "Conversation compacted"},
		m{"type": "user", "isCompactSummary": true, "message": m{"role": "user", "content": "This session is being continued…"}},
		sys("<command-name>/compact</command-name><command-args></command-args>"),
		sys("<local-command-stdout>Compacted\nPostCompact [sh hook.sh] completed successfully: {}</local-command-stdout>"),
	)
	res, _ = r.Read("claude", p, dir, res.Next)
	if len(res.Items) != 2 || res.Items[1].Command != "/compact" || strings.Contains(res.Items[1].Text, "PostCompact") {
		t.Errorf("compact = %+v", res.Items)
	}

	// Output written after its command is read: the command comes again.
	write(t, p, user("<command-name>/usage</command-name><command-args></command-args>"))
	res, _ = r.Read("claude", p, dir, res.Next)
	write(t, p, sys("<local-command-stdout>Status dialog dismissed</local-command-stdout>"))
	res2, _ := r.Read("claude", p, dir, res.Next)
	if len(res2.Items) != 1 || res2.Items[0].ID != res.Items[len(res.Items)-1].ID || res2.Items[0].Text != "Status dialog dismissed" {
		t.Errorf("again = %+v", res2.Items)
	}
}

// Start again: a new agent in the folder of one that just ended (its file
// written seconds ago) doesn't take that conversation, with or without the
// ID its hooks gave.
func TestAssignClaudeNewAgentDoesNotTakeAJustEndedSiblings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	dir := "/w/shop"
	proj := ClaudeDir(dir)
	os.MkdirAll(proj, 0o700)
	ended := time.Now().Add(-10 * time.Second)
	old := filepath.Join(proj, "eeeeeeee-5.jsonl")
	write(t, old, m{"type": "user", "timestamp": ended.Format(time.RFC3339Nano), "message": m{"role": "user", "content": "end"}})
	os.Chtimes(old, ended.Add(3*time.Second), ended.Add(3*time.Second))
	now := time.Now()
	got := AssignClaude(dir, []Claim{{Name: "again", ID: "ffffffff-6", Started: now}, {Name: "nohooks", Started: now}})
	if got["again"] != "" || got["nohooks"] != "" {
		t.Fatalf("a new agent took the ended one's conversation: %v", got)
	}
}

// A call still running is timed from when it was made, and the result says
// when the agent last wrote, for "Thinking" between calls.
func TestCallsAndTheLastWriteCarryTheirTimes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	call := assistant(tool("b1", "Bash", m{"command": "sleep 60"}))
	call["timestamp"] = "2026-10-04T12:00:05Z"
	write(t, p, user("Run it"), call, m{"type": "last-prompt", "lastPrompt": "Run it"})
	res, err := NewReader().Read("claude", p, "/w/shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	g := res.Items[len(res.Items)-1]
	want := time.Date(2026, 10, 4, 12, 0, 5, 0, time.UTC).UnixMilli()
	if g.Kind != "tools" || g.Done || g.Items[0].At != want {
		t.Fatalf("running call = %+v, want at %d", g, want)
	}
	if res.Last != want {
		t.Fatalf("last = %d, want %d (a line without a time doesn't count)", res.Last, want)
	}
}

func TestPastedPromptShowsAsTyped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, p,
		user("\n\n<pasted_content id=\"60b8\">\nhttps://example.com/run/1\n\nFAIL i18n\n</pasted_content>\n\nWeird, can you fix it?"),
		assistant(m{"type": "text", "text": "Fixed."}),
	)
	res, _ := NewReader().Read("claude", p, "", 0)
	if len(res.Items) < 1 || res.Items[0].Kind != "user" || res.Items[0].Text != "https://example.com/run/1\n\nFAIL i18n\n\nWeird, can you fix it?" {
		t.Fatalf("got %+v", res.Items)
	}
}

// Agents on different accounts (another CLAUDE_CONFIG_DIR) in one worktree
// each read their own account's conversation.
func TestAssignClaudeLooksInEachSessionsAccount(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	personal := t.TempDir()
	dir := "/w/shop"
	at := m{"type": "user", "timestamp": "2026-10-04T10:00:05Z", "message": m{"role": "user", "content": "hi"}}
	os.MkdirAll(ClaudeDir(dir), 0o700)
	os.MkdirAll(ClaudeDirIn(personal, dir), 0o700)
	write(t, filepath.Join(ClaudeDir(dir), "aaaaaaaa-1.jsonl"), at)
	write(t, filepath.Join(ClaudeDirIn(personal, dir), "bbbbbbbb-2.jsonl"), at)
	t0, _ := time.Parse(time.RFC3339, "2026-10-04T10:00:00Z")
	got := AssignClaude(dir, []Claim{
		{Name: "default", Started: t0},
		{Name: "personal", Started: t0, ConfigDir: personal},
	})
	if filepath.Base(got["default"]) != "aaaaaaaa-1.jsonl" || got["personal"] != filepath.Join(ClaudeDirIn(personal, dir), "bbbbbbbb-2.jsonl") {
		t.Fatalf("%v", got)
	}
}

// A prompt and an edit carry when they were written, as a call does, so a
// turn and its steps can be timed (the dashboard's cards).
func TestPromptsAndEditsCarryTheirTimes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	prompt := user("Fix it")
	prompt["timestamp"] = "2026-10-04T12:00:00Z"
	edit := assistant(tool("e1", "Edit", m{"file_path": "/w/shop/a.ts", "old_string": "a", "new_string": "b"}))
	edit["timestamp"] = "2026-10-04T12:00:07Z"
	write(t, p, prompt, edit)
	res, err := NewReader().Read("claude", p, "/w/shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	at := func(s int) int64 { return time.Date(2026, 10, 4, 12, 0, s, 0, time.UTC).UnixMilli() }
	var u, e *Item
	for i := range res.Items {
		switch res.Items[i].Kind {
		case "user":
			u = &res.Items[i]
		case "edit":
			e = &res.Items[i]
		}
	}
	if u == nil || u.At != at(0) {
		t.Fatalf("prompt = %+v, want at %d", u, at(0))
	}
	if e == nil || e.At != at(7) {
		t.Fatalf("edit = %+v, want at %d", e, at(7))
	}
}
