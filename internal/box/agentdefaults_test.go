package box

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/box/runs"
	"github.com/cosscom/shipyard/internal/events"
	"github.com/cosscom/shipyard/internal/transcript"
	"github.com/cosscom/shipyard/internal/wire"
)

// What starts an agent without naming a driver gets the box's default; a
// driver named, "terminal" included, is kept.
func TestStartDriverFollowsTheBoxDefault(t *testing.T) {
	claude, codex := builtinAgents[0], builtinAgents[1]
	opencode := AgentPreset{ID: "opencode", Name: "OpenCode", Command: "opencode", ACP: "acp"}
	own := AgentPreset{ID: "aider", Name: "Aider", Command: "aider"}
	for _, c := range []struct {
		name           string
		saved          *AgentDefaults
		convs          bool
		p              AgentPreset
		driver, mode   string
		kind, wantMode string
	}{
		{name: "nothing saved", convs: true, p: claude},
		{name: "off", saved: &AgentDefaults{}, convs: true, p: claude},
		{name: "on", saved: &AgentDefaults{Structured: true}, convs: true, p: claude, kind: DriverClaude, wantMode: "default"},
		{name: "on, ask", saved: &AgentDefaults{Structured: true, Mode: "default"}, convs: true, p: codex, kind: DriverCodex, wantMode: "default"},
		{name: "on, bypass", saved: &AgentDefaults{Structured: true, Mode: "bypassPermissions"}, convs: true, p: claude, kind: DriverClaude, wantMode: "bypassPermissions"},
		{name: "the request's own mode", saved: &AgentDefaults{Structured: true, Mode: "bypassPermissions"}, convs: true, p: claude, mode: "plan", kind: DriverClaude, wantMode: "plan"},
		{name: "ACP, ask: its own modes", saved: &AgentDefaults{Structured: true}, convs: true, p: opencode, kind: DriverACP},
		{name: "ACP, bypass", saved: &AgentDefaults{Structured: true, Mode: "bypassPermissions"}, convs: true, p: opencode, kind: DriverACP, wantMode: "bypassPermissions"},
		{name: "an agent with no structured driver", saved: &AgentDefaults{Structured: true}, convs: true, p: own},
		{name: "a box keeping no conversations", saved: &AgentDefaults{Structured: true}, p: claude},
		{name: "terminal named", saved: &AgentDefaults{Structured: true}, convs: true, p: claude, driver: DriverTerminal},
		{name: "claude named, default off", saved: &AgentDefaults{}, convs: true, p: claude, driver: DriverClaude, kind: DriverClaude},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := &Box{AgentDefaults: &AgentDefaultsStore{Path: filepath.Join(t.TempDir(), "agent-defaults.json")}}
			if c.convs {
				b.ConversationsDir = t.TempDir()
			}
			if c.saved != nil {
				if err := b.AgentDefaults.Set(*c.saved); err != nil {
					t.Fatal(err)
				}
			}
			kind, mode, err := b.startDriver(c.p, c.driver, c.mode)
			if err != nil || kind != c.kind || mode != c.wantMode {
				t.Fatalf("got %q %q %v, want %q %q", kind, mode, err, c.kind, c.wantMode)
			}
		})
	}
	var none *Box = &Box{ConversationsDir: t.TempDir()}
	if kind, _, _ := none.startDriver(claude, "", ""); kind != "" {
		t.Fatalf("a box without the store started %q", kind)
	}
	if _, _, err := none.startDriver(claude, "vim", ""); err == nil {
		t.Fatal("an unknown driver was taken")
	}
}

// The defaults are read and written over the API, kept across a restart,
// and advertised.
func TestAgentDefaultsAreKeptAndAdvertised(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-defaults.json")
	exe, _ := os.Executable()
	var first *Box
	c, _ := servedBox(t, func(b *Box) {
		b.ConversationsDir = t.TempDir()
		b.AgentDefaults = &AgentDefaultsStore{Path: path}
		b.Update = &SelfUpdate{Executable: exe}
		first = b
	})
	if !slices.Contains(first.Capabilities(), "agents.defaults") {
		t.Fatalf("capabilities %v", first.Capabilities())
	}
	var d AgentDefaults
	if st := call(t, c, "GET", "/v1/agents/defaults", "", nil, &d); st != 200 || d.Structured || d.Mode != "" {
		t.Fatalf("before any PUT: %d %+v", st, d)
	}
	if st := call(t, c, "PUT", "/v1/agents/defaults", "", map[string]any{"structured": true, "mode": "sideways"}, nil); st != 400 {
		t.Fatalf("an unknown mode: %d", st)
	}
	if st := call(t, c, "PUT", "/v1/agents/defaults", "", AgentDefaults{Structured: true, Mode: "bypassPermissions"}, &d); st != 200 || !d.Structured || d.Mode != "bypassPermissions" {
		t.Fatalf("PUT: %d %+v", st, d)
	}
	var info Info
	if st := call(t, c, "GET", "/v1/info", "", nil, &info); st != 200 || info.AgentDefaults == nil || !info.AgentDefaults.Structured || !slices.Contains(info.Capabilities, "agents.defaults") {
		t.Fatalf("info: %d %+v", st, info.AgentDefaults)
	}

	// berthd starting again on the same state reads them back.
	again, _ := servedBox(t, func(b *Box) {
		b.ConversationsDir = t.TempDir()
		b.AgentDefaults = &AgentDefaultsStore{Path: path}
	})
	d = AgentDefaults{}
	if st := call(t, again, "GET", "/v1/agents/defaults", "", nil, &d); st != 200 || !d.Structured || d.Mode != "bypassPermissions" {
		t.Fatalf("after a restart: %d %+v", st, d)
	}

	// A box without them says so, and doesn't advertise them.
	bare, _ := servedBox(t, func(b *Box) { b.ConversationsDir = t.TempDir() })
	if st := call(t, bare, "GET", "/v1/agents/defaults", "", nil, nil); st != 404 {
		t.Fatalf("a box without them: %d", st)
	}
	if slices.Contains((&Box{ConversationsDir: "x", Events: &events.Bus{}}).Capabilities(), "agents.defaults") {
		t.Fatal("advertised without a store")
	}
}

// structuredBox is a served box whose agents can run structured, with the
// stand-in Claude (claudedriver_test.go) on PATH, its agent defaults kept
// in the test's state, and a repository "acme".
func structuredBox(t *testing.T, opts ...func(*Box)) (*wire.Client, *Box) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("the stand-in Claude needs python3")
	}
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeStreamClaude), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	states := &Turns{}
	var bx *Box
	c, bus := servedBox(t, append([]func(*Box){func(b *Box) {
		b.Turns = states
		b.ConversationsDir = conversationsDir(t)
		b.AgentHost = []string{"env", "BERTH_TEST_AGENT_HOST=1", exe}
		b.AgentDefaults = &AgentDefaultsStore{Path: filepath.Join(t.TempDir(), "agent-defaults.json")}
		bx = b
	}}, opts...)...)
	states.Attach(bus)
	call(t, c, "POST", "/v1/locations", "", map[string]string{"name": "acme", "path": gitRepo(t)}, nil)
	return c, bx
}

// conversationSaid waits for a session's conversation to hold text.
func conversationSaid(t *testing.T, c *wire.Client, name, text string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var res transcript.Result
		call(t, c, "GET", "/v1/sessions/"+name+"/transcript?since=0", "", nil, &res)
		for _, it := range res.Items {
			if it.Kind == "text" && strings.Contains(it.Text, text) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never said %q: %+v (%s)", name, text, res.Items, res.Reason)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// conversationMode is the mode a session's conversation started in.
func conversationMode(t *testing.T, c *wire.Client, name string) string {
	t.Helper()
	var convs []Conversation
	call(t, c, "GET", "/v1/conversations", "", nil, &convs)
	for _, cv := range convs {
		if cv.Session == name {
			return cv.Mode
		}
	}
	t.Fatalf("no conversation for %s in %+v", name, convs)
	return ""
}

// An agent started with no driver named (another agent's task, the CLI)
// runs in its terminal until the person turns structured agents on, then
// structured, in the mode the default says unless it asked for its own.
func TestAnAgentStartedWithNoDriverFollowsTheDefault(t *testing.T) {
	c, bx := structuredBox(t)
	stop := func(name string) { t.Cleanup(func() { call(t, c, "DELETE", "/v1/sessions/"+name, "", nil, nil) }) }

	// Nothing saved: today's way, a terminal.
	var task Task
	if st := call(t, c, "POST", "/v1/tasks", "", TaskRequest{Location: "acme", Name: "before", Agent: "claude", Prompt: "hello"}, &task); st != 200 {
		t.Fatalf("task: %d", st)
	}
	stop(task.Session.Name)
	if task.Session.Driver != DriverTerminal || strings.Contains(task.Session.Command, "agent-host") {
		t.Fatalf("with no default, a task's agent ran %q (%s)", task.Session.Driver, task.Session.Command)
	}

	// Turned on: a task's agent runs structured.
	call(t, c, "PUT", "/v1/agents/defaults", "", AgentDefaults{Structured: true, Mode: "default"}, nil)
	task = Task{}
	if st := call(t, c, "POST", "/v1/tasks", "", TaskRequest{Location: "acme", Name: "after", Agent: "claude", Prompt: "hello"}, &task); st != 200 {
		t.Fatalf("task: %d", st)
	}
	stop(task.Session.Name)
	if task.Session.Driver != DriverClaude {
		t.Fatalf("with the default on, a task's agent ran %q", task.Session.Driver)
	}
	live, err := bx.Sessions.Get(t.Context(), task.Session.Name)
	if err != nil || live.conv == "" || live.sock == "" || live.bypass {
		t.Fatalf("its labels: %+v %v", live, err)
	}
	conversationSaid(t, c, task.Session.Name, "echo: hello")
	if m := conversationMode(t, c, task.Session.Name); m != "default" {
		t.Fatalf("its mode = %q", m)
	}

	// "terminal" named stays a terminal.
	var sess Session
	if st := call(t, c, "POST", "/v1/sessions", "", SessionRequest{Location: "acme", Name: "named-terminal", Agent: "claude", Driver: DriverTerminal, Prompt: "hello"}, &sess); st != 200 {
		t.Fatalf("session: %d", st)
	}
	stop(sess.Name)
	if sess.Driver != DriverTerminal {
		t.Fatalf("driver terminal ran %q", sess.Driver)
	}

	// A command is a command.
	sess = Session{}
	call(t, c, "POST", "/v1/sessions", "", SessionRequest{Location: "acme", Name: "shell", Command: "sleep 30"}, &sess)
	stop(sess.Name)
	if sess.Driver != "" || sess.Command != "sleep 30" {
		t.Fatalf("a command ran as %+v", sess)
	}

	// Bypass: a session's agent starts in it.
	call(t, c, "PUT", "/v1/agents/defaults", "", AgentDefaults{Structured: true, Mode: "bypassPermissions"}, nil)
	sess = Session{}
	call(t, c, "POST", "/v1/sessions", "", SessionRequest{Location: "acme", Name: "bypassed", Agent: "claude", Prompt: "hi"}, &sess)
	stop(sess.Name)
	if live, _ := bx.Sessions.Get(t.Context(), sess.Name); sess.Driver != DriverClaude || !live.bypass {
		t.Fatalf("bypass by default: %q, bypass %v", sess.Driver, live.bypass)
	}
	if m := conversationMode(t, c, sess.Name); m != "bypassPermissions" {
		t.Fatalf("its mode = %q", m)
	}

	// The request's own mode wins.
	sess = Session{}
	call(t, c, "POST", "/v1/sessions", "", SessionRequest{Location: "acme", Name: "planner", Agent: "claude", Mode: "plan"}, &sess)
	stop(sess.Name)
	if live, _ := bx.Sessions.Get(t.Context(), sess.Name); sess.Driver != DriverClaude || live.bypass {
		t.Fatalf("its own mode: %q, bypass %v", sess.Driver, live.bypass)
	}
	if m := conversationMode(t, c, sess.Name); m != "plan" {
		t.Fatalf("its mode = %q", m)
	}

	// Turned off again: a terminal.
	call(t, c, "PUT", "/v1/agents/defaults", "", AgentDefaults{}, nil)
	sess = Session{}
	call(t, c, "POST", "/v1/sessions", "", SessionRequest{Location: "acme", Name: "off-again", Agent: "claude"}, &sess)
	stop(sess.Name)
	if sess.Driver != DriverTerminal {
		t.Fatalf("turned off, it ran %q", sess.Driver)
	}
}

// A run's agent (attempts, a hand-off, an automation) follows the default
// too, and the run waits on the turn its first prompt started.
func TestARunsAgentFollowsTheDefault(t *testing.T) {
	var bx *Box
	_, do := runsBox(t, func(b *Box) {
		exe, _ := os.Executable()
		b.ConversationsDir = conversationsDir(t)
		b.AgentHost = []string{"env", "BERTH_TEST_AGENT_HOST=1", exe}
		b.AgentDefaults = &AgentDefaultsStore{Path: filepath.Join(t.TempDir(), "agent-defaults.json")}
		bx = b
	})
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("the stand-in Claude needs python3")
	}
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeStreamClaude), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	do("POST", "/v1/locations", map[string]string{"name": "acme", "path": gitRepo(t)}, nil)
	do("PUT", "/v1/agents/defaults", AgentDefaults{Structured: true, Mode: "bypassPermissions"}, nil)

	req := RunRequest{Flow: []runs.Step{{Kind: "start_agent", Agent: "claude", Location: "acme", Text: "hello from a run"}, {Kind: "wait", Timeout: "20s"}}}
	var s runs.Summary
	if st := do("POST", "/v1/runs", req, &s); st != 202 {
		t.Fatalf("start: %d %+v", st, s)
	}
	r := waitRunStatus(t, bx, s.ID, runs.Succeeded, runs.Failed)
	var list []Session
	do("GET", "/v1/sessions", nil, &list)
	for _, sess := range list {
		t.Cleanup(func() { do("DELETE", "/v1/sessions/"+sess.Name, nil, nil) })
	}
	if r.Status != runs.Succeeded {
		t.Fatalf("%s: %s %+v", r.Status, r.Error, r.Steps)
	}
	if len(list) != 1 || list[0].Driver != DriverClaude {
		t.Fatalf("the run's agent: %+v", list)
	}
	turns := bx.Turns.List(list[0].Name, 10)
	if len(turns) != 1 || turns[0].State != "finished" {
		t.Fatalf("its turns: %+v", turns)
	}
}
