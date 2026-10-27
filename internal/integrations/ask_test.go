package integrations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cosscom/shipyard/internal/events"
	"github.com/cosscom/shipyard/internal/integrations/adapters"
)

// A PermissionRequest names the tool and sums up its input (Claude Code's
// hook gives tool_name and tool_input): the command, or the file's path,
// capped, and never what would be written. Only the box reads it; every
// other path strips it.
func TestPermissionRequestAsk(t *testing.T) {
	ask := func(payload string) map[string]any {
		t.Helper()
		e, ok := Translate("claude", "PermissionRequest", []byte(payload))
		if !ok || e.Type != "agent.waiting" || e.Data["reason"] != "permission" {
			t.Fatalf("PermissionRequest = %+v %v", e, ok)
		}
		a, _ := e.Data[adapters.AskKey].(map[string]any)
		return a
	}
	if a := ask(`{"cwd":"/w","tool_name":"Bash","tool_input":{"command":"ls -la","description":"List files"}}`); a["tool"] != "Bash" || a["input"] != "ls -la" || a["why"] != "List files" {
		t.Errorf("Bash ask = %v", a)
	}
	if a := ask(`{"cwd":"/w","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Hi or bye?","options":[{"label":"hi"},{"label":"bye"}]}]}}`); a["input"] != "Hi or bye?" {
		t.Errorf("AskUserQuestion ask = %v", a)
	}
	a := ask(`{"cwd":"/w","tool_name":"Write","tool_input":{"file_path":"/w/src/a.ts","content":"SECRET-CONTENT"}}`)
	if a["tool"] != "Write" || a["input"] != "src/a.ts" {
		t.Errorf("Write ask = %v", a)
	}
	a = ask(`{"cwd":"/w","tool_name":"Edit","tool_input":{"file_path":"/elsewhere/b.go","old_string":"SECRET-CONTENT","new_string":"SECRET-CONTENT"}}`)
	if a["input"] != "/elsewhere/b.go" {
		t.Errorf("Edit ask = %v", a)
	}
	if b, _ := json.Marshal(a); strings.Contains(string(b), "SECRET-CONTENT") {
		t.Errorf("file contents in the ask: %s", b)
	}
	long := strings.Repeat("x", 2000)
	a = ask(`{"cwd":"/w","tool_name":"Bash","tool_input":{"command":"echo ` + long + `"}}`)
	if s, _ := a["input"].(string); len(s) > adapters.AskLimit || !strings.HasSuffix(s, "…") {
		t.Errorf("long command kept %d bytes", len(s))
	}
	if a := ask(`{"cwd":"/w","tool_name":"mcp__linear__create_issue","tool_input":{"title":"SECRET-CONTENT"}}`); a["tool"] != "mcp__linear__create_issue" || a["input"] != nil {
		t.Errorf("MCP ask = %v", a)
	}
	if a := ask(`{"cwd":"/w"}`); a != nil {
		t.Errorf("an ask without a tool = %v", a)
	}

	// The laptop agent and the spool never see it.
	e, _ := Translate("claude", "PermissionRequest", []byte(`{"cwd":"/w","tool_name":"Bash","tool_input":{"command":"ls"}}`))
	if StripAsk(e).Data[adapters.AskKey] != nil {
		t.Error("StripAsk kept the ask")
	}
	e, _ = Translate("claude", "PermissionRequest", []byte(`{"cwd":"/w","tool_name":"Bash","tool_input":{"command":"SECRET-COMMAND"}}`))
	dir := t.TempDir()
	if err := Spool(dir, e); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(dir)
	for _, f := range ents {
		if b, _ := os.ReadFile(filepath.Join(dir, f.Name())); strings.Contains(string(b), "SECRET-COMMAND") {
			t.Errorf("the spool kept the ask: %s", b)
		}
	}
	var drained []events.Event
	DrainSpool(dir, func(e events.Event) { drained = append(drained, e) })
	if len(drained) != 1 || drained[0].Type != "agent.waiting" {
		t.Errorf("drained = %+v", drained)
	}
}

// A Notification's message rides with the ask, not the event.
func TestNotificationMessageIsAnAsk(t *testing.T) {
	e, ok := Translate("claude", "Notification", []byte(`{"cwd":"/w","notification_type":"elicitation_dialog","message":"Which database?"}`))
	if !ok || e.Data["reason"] != "question" {
		t.Fatalf("Notification = %+v %v", e, ok)
	}
	if a, _ := e.Data[adapters.AskKey].(map[string]any); a["message"] != "Which database?" {
		t.Fatalf("ask = %v", e.Data[adapters.AskKey])
	}
}

func TestAStructuredAgentsHooksSayNothing(t *testing.T) {
	t.Setenv("BERTH_DRIVER", "structured")
	var out, errs strings.Builder
	emitted := false
	Hook([]string{"claude", "Stop", `{"cwd":"/w"}`}, os.Stdin, &out, &errs, func(events.Event) error {
		emitted = true
		return nil
	})
	if emitted || errs.Len() > 0 {
		t.Fatalf("the hook spoke for a driven agent: %v %q", emitted, errs.String())
	}
	if out.String() != Reply("claude") {
		t.Fatalf("the hook's reply = %q", out.String())
	}
}
