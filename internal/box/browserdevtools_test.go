package box

import (
	"encoding/json"
	"testing"
)

func TestAgentBrowserDevtoolsLeaveTheAgentsLinesUnseen(t *testing.T) {
	br := &browser{reqs: map[string]string{}, inflight: map[string]bool{}}
	ev := func(method string, params any) {
		raw, _ := json.Marshal(params)
		br.onEvent(method, raw)
	}
	ev("Runtime.consoleAPICalled", map[string]any{"type": "error", "args": []map[string]any{{"value": "cart total is"}, {"description": "undefined"}}})
	ev("Runtime.consoleAPICalled", map[string]any{"type": "error", "args": []map[string]any{{"value": "cart total is"}, {"description": "undefined"}}})
	ev("Runtime.consoleAPICalled", map[string]any{"type": "log", "args": []map[string]any{{"value": "ready"}}})
	ev("Network.requestWillBeSent", map[string]any{"requestId": "1", "type": "Fetch", "request": map[string]any{"url": "http://shop.localhost/api/cart", "method": "POST"}})
	ev("Network.responseReceived", map[string]any{"requestId": "1", "response": map[string]any{"status": 500, "url": "http://shop.localhost/api/cart"}})

	d := br.devtools()
	if !d.Running || len(d.Console) != 2 || d.Console[0].Text != "cart total is undefined" || d.Console[0].Count != 2 || d.Console[0].Level != "error" || d.Console[1].Level != "log" {
		t.Fatalf("console: %+v", d.Console)
	}
	if len(d.Failures) != 1 || d.Failures[0].Text != "500 POST http://shop.localhost/api/cart" {
		t.Fatalf("failures: %+v", d.Failures)
	}
	// The agent's own look still finds them new.
	if got := br.newConsole(10, false); len(got) != 1 || got[0] != "error: cart total is undefined (×2)" {
		t.Errorf("the agent's console look: %q", got)
	}
	if got := br.newFailures(10); len(got) != 1 {
		t.Errorf("the agent's network look: %q", got)
	}
	// And the app's look still sees everything after the agent's.
	if d := br.devtools(); len(d.Console) != 2 || len(d.Failures) != 1 {
		t.Errorf("after the agent looked: %+v", d)
	}
}
