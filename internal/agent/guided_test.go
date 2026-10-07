package agent

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/sean-brydon/berthd/internal/guided"
)

// The guided install runs berth add ssh in a terminal here: the app gets
// its screen as bytes and its steps as JSON, and what the person types
// reaches the command (sudo's password, on a real box) without the agent
// keeping it anywhere.
func TestGuidedInstallTerminal(t *testing.T) {
	b := newBox(t)
	a := startAgent(t, b.pairLaptop())
	tok := uiToken(t, a)
	script := `#!/bin/sh
echo "args: $*"
echo "steps: $BERTH_STEPS"
echo '::berth-step connect start'
echo '::berth-step connect done me@box, linux/arm64'
printf 'Press Enter to start '
read -r go
echo '::berth-step tools start'
stty -echo 2>/dev/null
printf '[sudo] password for me: '
read -r pw
stty echo 2>/dev/null
echo
echo "sudo got ${#pw} characters"
echo '::berth-step tools done'
printf 'half a line::berth-step pair done devl\n'
exit 0
`
	if err := os.WriteFile(filepath.Join(a.dir, "fake-berth"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	q := url.Values{"host": {"me@box"}, "agents": {"claude,codex"}, "from": {"tools"}, "cols": {"100"}, "rows": {"30"}, "token": {tok}}
	ws, _, err := websocket.Dial(ctx, "ws://"+a.ui+"/v1/boxes/add-ssh/terminal?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	var screen strings.Builder
	var steps []string
	exit := -2
	read := func(until func() bool) {
		t.Helper()
		for !until() {
			typ, msg, err := ws.Read(ctx)
			if err != nil {
				t.Fatalf("%v; screen so far:\n%s\nsteps %v", err, screen.String(), steps)
			}
			if typ == websocket.MessageBinary {
				screen.Write(msg)
				continue
			}
			var m installMessage
			if err := json.Unmarshal(msg, &m); err != nil {
				t.Fatalf("text message %q: %v", msg, err)
			}
			switch m.Type {
			case "step":
				steps = append(steps, m.Step+":"+m.State+":"+m.Message)
			case "exit":
				exit = m.Code
			}
		}
	}
	read(func() bool { return strings.Contains(screen.String(), "Press Enter") })
	if !strings.Contains(screen.String(), "args: add ssh me@box --agents claude,codex --from tools") || !strings.Contains(screen.String(), "steps: 1") {
		t.Fatalf("screen:\n%s", screen.String())
	}
	if strings.Contains(screen.String(), "::berth-step") {
		t.Fatalf("a marker reached the screen:\n%s", screen.String())
	}
	ws.Write(ctx, websocket.MessageBinary, []byte("\r"))
	read(func() bool { return strings.Contains(screen.String(), "password for me") })
	ws.Write(ctx, websocket.MessageBinary, []byte("hunter2\r"))
	read(func() bool { return exit != -2 })
	if exit != 0 {
		t.Errorf("exit = %d", exit)
	}
	want := []string{"connect:start:", "connect:done:me@box, linux/arm64", "tools:start:", "tools:done:", "pair:done:devl"}
	if strings.Join(steps, "|") != strings.Join(want, "|") {
		t.Errorf("steps = %q", steps)
	}
	if !strings.Contains(screen.String(), "sudo got 7 characters") || strings.Contains(screen.String(), "hunter2") {
		t.Errorf("the password echoed, or didn't arrive:\n%s", screen.String())
	}
	if !strings.Contains(screen.String(), "half a line") {
		t.Errorf("the text before a marker was lost:\n%s", screen.String())
	}
}

func TestGuidedInstallRefusesFlags(t *testing.T) {
	for _, q := range []url.Values{{"host": {"--help"}}, {"host": {"me@box"}, "agents": {"gemini"}}, {"host": {"me@box"}, "from": {"nope"}}, {}} {
		if _, err := readInstallRequest(q); err == nil {
			t.Errorf("%v was accepted", q)
		}
	}
	r, err := readInstallRequest(url.Values{"host": {"me@box"}, "agents": {""}})
	if err != nil || strings.Join(r.args(), " ") != "add ssh me@box --agents none" {
		t.Errorf("args = %v %v", r.args(), err)
	}
}

func TestInstallPlanBeforeConnecting(t *testing.T) {
	b := newBox(t)
	a := startAgent(t, b.pairLaptop())
	tok := uiToken(t, a)
	resp, body := uiSend(t, a, "GET", "/v1/ssh/install-plan?host=demo@box&agents=claude", tok, "")
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	var plan struct {
		Steps  []guided.Step `json:"steps"`
		Agents []struct {
			ID      string `json:"id"`
			Offered bool   `json:"offered"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 7 || plan.Steps[0].ID != "connect" || plan.Steps[4].Title != "Claude Code" || len(plan.Agents) < 4 {
		t.Errorf("plan = %s", body)
	}
}
