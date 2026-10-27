package box_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/mcpserver"
)

// An agent making a task through the berth tools names no driver: the
// agent it starts runs structured, as the box's default says, even on a
// box the app has never given one.
func TestAnAgentsTaskThroughTheBerthToolsFollowsTheDefault(t *testing.T) {
	c, _ := box.StructuredBox(t)
	tools := &mcpserver.Server{Box: box.NewClient(c)}
	taskNew := func(name string) string {
		t.Helper()
		var out bytes.Buffer
		req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
			"name": "berth_task_new", "arguments": map[string]any{"location": "acme", "name": name, "agent": "claude", "prompt": "hello from the lead"},
		}}
		line, _ := json.Marshal(req)
		if err := tools.Serve(context.Background(), strings.NewReader(string(line)+"\n"), &out); err != nil {
			t.Fatal(err)
		}
		var res struct {
			Result struct {
				IsError bool `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		json.Unmarshal(out.Bytes(), &res)
		if res.Result.IsError || len(res.Result.Content) == 0 {
			t.Fatalf("berth_task_new: %s", out.String())
		}
		var made struct {
			Session string `json:"session"`
		}
		json.Unmarshal([]byte(res.Result.Content[0].Text), &made)
		if made.Session == "" {
			t.Fatalf("no session in %s", res.Result.Content[0].Text)
		}
		t.Cleanup(func() {
			if resp, err := c.Do(context.Background(), "DELETE", "/v1/sessions/"+made.Session, nil); err == nil {
				resp.Body.Close()
			}
		})
		return made.Session
	}
	driver := func(name string) string {
		t.Helper()
		var list []box.Session
		if err := box.NewClient(c).Call(t.Context(), "GET", "/v1/sessions", nil, &list); err != nil {
			t.Fatal(err)
		}
		for _, s := range list {
			if s.Name == name {
				return s.Driver
			}
		}
		t.Fatalf("%s is not among %+v", name, list)
		return ""
	}

	// A box the app never gave its defaults: structured anyway.
	if d := driver(taskNew("unsaved")); d != box.DriverClaude {
		t.Fatalf("with nothing saved, the tools' task ran %q", d)
	}
	put := func(d box.AgentDefaults) {
		t.Helper()
		if err := box.NewClient(c).Call(t.Context(), "PUT", "/v1/agents/defaults", d, nil); err != nil {
			t.Fatal(err)
		}
	}
	// Off (only an older Shipyard sends it): its terminal.
	put(box.AgentDefaults{})
	if d := driver(taskNew("off")); d != box.DriverTerminal {
		t.Fatalf("with the default off, the tools' task ran %q", d)
	}
	put(box.AgentDefaults{Structured: true, Mode: "bypassPermissions"})
	if d := driver(taskNew("on")); d != box.DriverClaude {
		t.Fatalf("with the default on, the tools' task ran %q", d)
	}
}
