package box

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Add agents from a box's settings: the box runs its own berthd agents
// install and streams it, each agent's marker as a step.
func TestAgentsInstallStreamsSteps(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "berthd")
	os.WriteFile(exe, []byte(`#!/bin/sh
echo "args: $*"
echo '::berth-step agent-codex start'
printf 'Downloading 10%%\rDownloading 100%%\n'
echo '::berth-step agent-codex done /home/me/.local/bin/codex'
[ "$5" = cursor ] && { echo "Cursor Agent did not install: 404"; exit 1; }
exit 0
`), 0o755)
	c, _ := servedBox(t, func(b *Box) { b.Update = &SelfUpdate{Executable: exe} })
	resp, err := c.Do(context.Background(), "POST", "/v1/agents/install", strings.NewReader(`{"agents":["codex"]}`))
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	for _, want := range []string{`"line":"args: agents install --integrations --markers codex"`, `"step":{"step":"agent-codex","state":"start"}`, `"line":"Downloading 100%"`, `"state":"done","message":"/home/me/.local/bin/codex"`, `{"done":true}`} {
		if !strings.Contains(body, want) {
			t.Errorf("no %s in\n%s", want, body)
		}
	}
	resp, _ = c.Do(context.Background(), "POST", "/v1/agents/install", strings.NewReader(`{"agents":["cursor"]}`))
	if body := readAll(t, resp); !strings.Contains(body, `"error":"Cursor Agent did not install: 404"`) {
		t.Errorf("a failed install streamed %s", body)
	}
	resp, _ = c.Do(context.Background(), "POST", "/v1/agents/install", strings.NewReader(`{"agents":["gemini"]}`))
	if resp.StatusCode != 400 {
		t.Errorf("gemini gave %d", resp.StatusCode)
	}
}
