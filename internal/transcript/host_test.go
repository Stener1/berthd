package transcript

import (
	"path/filepath"
	"strings"
	"testing"
)

// claude_host_ask.jsonl is an agent-host log of a structured Claude Code
// session (recorded, then cut down and made anonymous): a prompt, a
// question the agent asked with its own form and was answered, and its
// reply, among the stream's other lines.
func TestAStructuredSessionsLogReadsAsItsConversation(t *testing.T) {
	path := filepath.Join("testdata", "claude_host_ask.jsonl")
	res, err := NewReader().Read(SourceClaudeHost, path, "/home/dev/acme", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "claude" {
		t.Fatalf("source %q", res.Source)
	}
	var kinds []string
	for _, it := range res.Items {
		kinds = append(kinds, it.Kind)
	}
	if got := strings.Join(kinds, " "); got != "user question text" {
		t.Fatalf("items = %s: %+v", got, res.Items)
	}
	user, q, reply := res.Items[0], res.Items[1], res.Items[2]
	if !strings.HasPrefix(user.Text, "Ask me whether I prefer tea") || user.UUID == "" {
		t.Fatalf("the prompt (sent once, echoed once) reads %+v", user)
	}
	if len(q.Questions) != 1 || q.Questions[0].Question != "Do you prefer Tea or Coffee?" || len(q.Answers) != 1 || q.Answers[0] != "Tea" || !q.Done {
		t.Fatalf("question = %+v", q)
	}
	if reply.Text != "You chose Tea." {
		t.Fatalf("reply = %+v", reply)
	}

	// A tool call's details and older pages read the same log.
	d, err := Detail(SourceClaudeHost, path, "/home/dev/acme", "toolu_01Tct9HfJ6SKZik2AZ5F7FHV")
	if err != nil || d.ID == "" {
		t.Fatalf("detail = %+v, %v", d, err)
	}
	page, err := Before(SourceClaudeHost, path, "/home/dev/acme", reply.Off, 10)
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != user.ID {
		t.Fatalf("older page = %+v, %v", page, err)
	}
}

func TestOnlyTheConversationsOwnLinesAreUnwrapped(t *testing.T) {
	for _, tc := range []struct {
		line string
		ok   bool
	}{
		{`{"seq":1,"dir":"out","data":{"type":"assistant","message":{"content":"hi"}}}`, true},
		{`{"seq":1,"dir":"out","data":{"type":"assistant","parent_tool_use_id":"toolu_1","message":{"content":"a helper's"}}}`, false},
		{`{"seq":1,"dir":"in","data":{"type":"user","message":{"content":"sent, and echoed when replayed"}}}`, false},
		{`{"seq":1,"dir":"out","data":{"type":"stream_event"}}`, false},
		{`{"seq":1,"dir":"out","data":{"type":"control_request"}}`, false},
		{`{"seq":1,"dir":"out","data":{"type":"system","subtype":"compact_boundary"}}`, true},
		{`{"seq":1,"dir":"out","text":"not json"}`, false},
	} {
		if _, ok := unwrapHost([]byte(tc.line)); ok != tc.ok {
			t.Errorf("%s: %v", tc.line, ok)
		}
	}
	line, _ := unwrapHost([]byte(`{"seq":1,"at":1791900000000,"dir":"out","data":{"type":"user","message":{"content":"x"}}}`))
	if !strings.HasPrefix(string(line), `{"timestamp":"2026-`) {
		t.Fatalf("no time: %s", line)
	}
}
