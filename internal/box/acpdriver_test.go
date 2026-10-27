package box

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/agenthost"
	"github.com/cosscom/shipyard/internal/transcript"
)

// fakeACPAgent stands in for an agent speaking ACP, with the messages the
// spike recorded from opencode and Cursor Agent (design/structured-agents):
// it wants authenticate before a session, streams its replies as chunks,
// asks permission to "run the tests", works until cancelled on "work", has
// build and plan modes, resumes a session by its id, and lists a past one
// (ses_old) that it replays when loaded.
const fakeACPAgent = `#!/usr/bin/env python3
import json, sys
state = {"authed": False, "session": None, "mode": "build", "cancelled": False}
srv = [0]
def out(d):
    print(json.dumps(d), flush=True)
def update(u):
    out({"jsonrpc": "2.0", "method": "session/update", "params": {"sessionId": state["session"], "update": u}})
def chunks(text):
    for w in text.split(" "):
        update({"sessionUpdate": "agent_message_chunk", "messageId": "m", "content": {"type": "text", "text": w + " "}})
modes = {"currentModeId": "build", "availableModes": [{"id": "build", "name": "Build"}, {"id": "plan", "name": "Plan"}]}
def handle(m):
    method, rid, p = m.get("method"), m.get("id"), m.get("params") or {}
    if method == "initialize":
        out({"jsonrpc": "2.0", "id": rid, "result": {"protocolVersion": 1, "agentCapabilities": {"loadSession": True, "sessionCapabilities": {"resume": {}, "list": {}}}, "authMethods": [{"id": "fake_login", "name": "Login"}]}})
    elif method == "authenticate":
        state["authed"] = True
        out({"jsonrpc": "2.0", "id": rid, "result": {}})
    elif method in ("session/new", "session/resume", "session/load"):
        if not state["authed"]:
            out({"jsonrpc": "2.0", "id": rid, "error": {"code": -32000, "message": "Authentication required"}})
            return
        state["session"] = p.get("sessionId") or "ses_fake1"
        if method == "session/load" and state["session"] == "ses_old":
            update({"sessionUpdate": "user_message_chunk", "content": {"type": "text", "text": "earlier question"}})
            chunks("earlier answer")
        res = {"modes": modes} if method == "session/new" else {}
        if method == "session/new":
            res["sessionId"] = state["session"]
        out({"jsonrpc": "2.0", "id": rid, "result": res})
    elif method == "session/list":
        out({"jsonrpc": "2.0", "id": rid, "result": {"sessions": [
            {"sessionId": "ses_old", "cwd": p.get("cwd"), "title": "Old work", "updatedAt": "2026-10-01T09:00:00Z"},
            {"sessionId": "ses_fake1", "cwd": p.get("cwd"), "title": "This one", "updatedAt": "2026-10-02T09:00:00Z"},
            {"sessionId": "ses_away", "cwd": "/elsewhere", "title": "Another folder's"}]}})
    elif method == "session/set_mode":
        state["mode"] = p["modeId"]
        out({"jsonrpc": "2.0", "id": rid, "result": {}})
        update({"sessionUpdate": "current_mode_update", "currentModeId": p["modeId"]})
    elif method == "session/cancel":
        state["cancelled"] = True
    elif method == "session/prompt":
        text = p["prompt"][0]["text"]
        if text == "run the tests":
            update({"sessionUpdate": "tool_call", "toolCallId": "call_1", "title": "bash", "kind": "execute", "status": "pending", "rawInput": {}})
            update({"sessionUpdate": "tool_call_update", "toolCallId": "call_1", "title": "pnpm test", "kind": "execute", "status": "in_progress", "rawInput": {"command": "pnpm test"}})
            rid0 = srv[0]
            srv[0] += 1
            out({"jsonrpc": "2.0", "id": rid0, "method": "session/request_permission", "params": {"sessionId": state["session"], "toolCall": {"toolCallId": "call_1", "title": "pnpm test", "kind": "execute", "rawInput": {"command": "pnpm test"}}, "options": [{"optionId": "allow-once", "kind": "allow_once", "name": "Allow once"}, {"optionId": "allow-always", "kind": "allow_always", "name": "Always"}, {"optionId": "reject-once", "kind": "reject_once", "name": "Reject"}]}})
            r = None
            for line in sys.stdin:
                x = json.loads(line)
                if x.get("id") == rid0 and "method" not in x:
                    r = x["result"]["outcome"]
                    break
                handle(x)
            ok = r.get("outcome") == "selected" and r.get("optionId", "").startswith("allow")
            update({"sessionUpdate": "tool_call_update", "toolCallId": "call_1", "status": "completed" if ok else "failed"})
            chunks("The tests pass." if ok else "I didn't run them.")
            out({"jsonrpc": "2.0", "id": rid, "result": {"stopReason": "end_turn"}})
        elif text == "work":
            chunks("Working through it")
            for line in sys.stdin:
                handle(json.loads(line))
                if state["cancelled"]:
                    break
            state["cancelled"] = False
            out({"jsonrpc": "2.0", "id": rid, "result": {"stopReason": "cancelled"}})
        else:
            chunks("echo: " + text + " (" + state["mode"] + ")")
            out({"jsonrpc": "2.0", "id": rid, "result": {"stopReason": "end_turn"}})
    elif rid is not None and method:
        out({"jsonrpc": "2.0", "id": rid, "error": {"code": -32601, "message": "unknown"}})
for line in sys.stdin:
    handle(json.loads(line))
`

func TestAStructuredACPSessionIsDrivenThroughItsProtocol(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("the stand-in agent needs python3")
	}
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "opencode"), []byte(fakeACPAgent), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	exe, _ := os.Executable()
	states := &Turns{}
	var bx *Box
	c, bus := servedBox(t, func(b *Box) {
		bx = b
		b.Turns = states
		b.ConversationsDir = conversationsDir(t)
		b.AgentHost = []string{"env", "BERTH_TEST_AGENT_HOST=1", exe}
	})
	states.Attach(bus)
	repo := gitRepo(t)
	call(t, c, "POST", "/v1/locations", "", map[string]string{"name": "acme", "path": repo}, nil)
	var sess Session
	if status := call(t, c, "POST", "/v1/sessions", "", map[string]any{"location": "acme", "agent": "opencode", "driver": "acp", "prompt": "hello there"}, &sess); status != 200 {
		t.Fatalf("start: %d", status)
	}
	t.Cleanup(func() { call(t, c, "DELETE", "/v1/sessions/"+sess.Name, "", nil, nil) })
	if sess.Driver != DriverACP {
		t.Fatalf("session = %+v", sess)
	}
	name := sess.Name
	until := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				var res transcript.Result
				call(t, c, "GET", "/v1/sessions/"+name+"/transcript?since=0", "", nil, &res)
				st, _ := states.State(name)
				t.Fatalf("%s never happened: %+v / %+v", what, res.Items, st)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	count := func(text string) int {
		var res transcript.Result
		call(t, c, "GET", "/v1/sessions/"+name+"/transcript?since=0", "", nil, &res)
		n := 0
		for _, it := range res.Items {
			if strings.Contains(it.Text, text) {
				n++
			}
		}
		return n
	}
	state := func(want string) func() bool {
		return func() bool { st, _ := states.State(name); return st.State == want }
	}

	// It authenticated, opened its session and answered.
	until("the first reply", func() bool { return count("echo: hello there (build)") == 1 })
	until("the first turn's end", state("finished"))
	if st, _ := states.State(name); st.AgentSessionID != "ses_fake1" {
		t.Fatalf("its session = %q", st.AgentSessionID)
	}

	// Its own modes, and bypass beside them.
	var controls Controls
	call(t, c, "GET", "/v1/sessions/"+name+"/controls", "", nil, &controls)
	if controls.Mode != "build" || strings.Join(controls.Modes, ",") != "build,plan,bypassPermissions" {
		t.Fatalf("controls = %+v", controls)
	}
	call(t, c, "POST", "/v1/sessions/"+name+"/mode", "", map[string]string{"mode": "plan"}, nil)
	call(t, c, "POST", "/v1/sessions/"+name+"/send", "", map[string]any{"text": "and now", "when": "now"}, nil)
	until("a reply in plan mode", func() bool { return count("echo: and now (plan)") == 1 })
	until("its turn's end", state("finished"))

	// A permission, picked by its kind.
	call(t, c, "POST", "/v1/sessions/"+name+"/send", "", map[string]any{"text": "run the tests", "when": "now"}, nil)
	until("the permission", state("waiting"))
	if st, _ := states.State(name); st.Ask == nil || st.Ask.Tool != "Bash" || st.Ask.Input != "pnpm test" {
		t.Fatalf("ask = %+v", st.Ask)
	}
	var list []Session
	call(t, c, "GET", "/v1/sessions", "", nil, &list)
	if len(list) != 1 || list[0].Ask == nil || len(list[0].Ask.Choices) != 3 {
		t.Fatalf("choices: %+v", list)
	}
	call(t, c, "POST", "/v1/sessions/"+name+"/send", "", map[string]any{"text": "3", "enter": false, "when": "now", "force": true}, nil)
	until("the refusal's reply", func() bool { return count("I didn't run them.") == 1 })
	until("its turn's end", state("finished"))
	var res transcript.Result
	call(t, c, "GET", "/v1/sessions/"+name+"/transcript?since=0", "", nil, &res)
	ran := false
	for _, it := range res.Items {
		for _, tc := range it.Items {
			ran = ran || (tc.Verb == "Run" && tc.Target == "pnpm test")
		}
	}
	if !ran {
		t.Fatalf("the command isn't a step: %+v", res.Items)
	}

	// Bypass: berthd allows it.
	call(t, c, "POST", "/v1/sessions/"+name+"/mode", "", map[string]string{"mode": "bypassPermissions"}, nil)
	call(t, c, "POST", "/v1/sessions/"+name+"/send", "", map[string]any{"text": "run the tests", "when": "now"}, nil)
	until("the bypassed run's reply", func() bool { return count("The tests pass.") == 1 })
	until("its turn's end", state("finished"))

	// The reply being written, then a cancel.
	call(t, c, "POST", "/v1/sessions/"+name+"/send", "", map[string]any{"text": "work", "when": "now"}, nil)
	until("the draft", func() bool {
		var d Draft
		call(t, c, "GET", "/v1/sessions/"+name+"/draft", "", nil, &d)
		return strings.Contains(d.Text, "Working through it")
	})
	var stopped map[string]bool
	call(t, c, "POST", "/v1/sessions/"+name+"/interrupt", "", nil, &stopped)
	if !stopped["stopped"] {
		t.Fatalf("interrupt: %v", stopped)
	}
	until("the cancelled turn's end", state("finished"))

	// Ended and resumed: the same session, picked up by its id.
	live, err := bx.Sessions.Get(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	agenthost.Stop(t.Context(), live.sock)
	until("the session's end", func() bool {
		s, err := bx.Sessions.Get(t.Context(), name)
		return err == nil && s.Exited
	})
	if status := call(t, c, "POST", "/v1/sessions/"+name+"/resume", "", nil, nil); status != 200 {
		t.Fatalf("resume: %d", status)
	}
	call(t, c, "POST", "/v1/sessions/"+name+"/send", "", map[string]any{"text": "again", "when": "now"}, nil)
	until("a reply once resumed", func() bool { return count("echo: again") == 1 })
	if st, _ := states.State(name); st.AgentSessionID != "ses_fake1" {
		t.Fatalf("resumed into %q", st.AgentSessionID)
	}
}

// TestALiveACPSessionWorks drives a real ACP agent: BERTH_LIVE_ACP names
// its preset (opencode, cursor, gemini), BERTH_LIVE_ACP_MODEL its model.
func TestALiveACPSessionWorks(t *testing.T) {
	agent := os.Getenv("BERTH_LIVE_ACP")
	if agent == "" {
		t.Skip("set BERTH_LIVE_ACP=opencode (or cursor, gemini) to drive a real ACP agent")
	}
	exe, _ := os.Executable()
	states := &Turns{}
	c, bus := servedBox(t, func(b *Box) {
		b.Turns = states
		b.ConversationsDir = conversationsDir(t)
		b.AgentHost = []string{"env", "BERTH_TEST_AGENT_HOST=1", exe}
	})
	states.Attach(bus)
	repo := gitRepo(t)
	call(t, c, "POST", "/v1/locations", "", map[string]string{"name": "acme", "path": repo}, nil)
	req := map[string]any{"location": "acme", "agent": agent, "driver": "acp", "mode": "bypassPermissions", "prompt": "Reply with just the word: harbour. Use no tools."}
	if m := os.Getenv("BERTH_LIVE_ACP_MODEL"); m != "" {
		req["model"] = m
	}
	var sess Session
	if status := call(t, c, "POST", "/v1/sessions", "", req, &sess); status != 200 {
		t.Fatalf("start: %d", status)
	}
	t.Cleanup(func() { call(t, c, "DELETE", "/v1/sessions/"+sess.Name, "", nil, nil) })
	deadline := time.Now().Add(4 * time.Minute)
	for {
		var res transcript.Result
		call(t, c, "GET", "/v1/sessions/"+sess.Name+"/transcript?since=0", "", nil, &res)
		st, _ := states.State(sess.Name)
		for _, it := range res.Items {
			if it.Kind == "text" && strings.Contains(strings.ToLower(it.Text), "harbour") && st.State == "finished" {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reply: %+v / %+v", res.Items, st)
		}
		time.Sleep(time.Second)
	}
}
