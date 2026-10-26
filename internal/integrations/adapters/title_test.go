package adapters

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTitle(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"\n\n   \n", ""},
		{"Fix checkout webhook", "Fix checkout webhook"},
		{"  Fix   the\tcheckout webhook  \nand add a test", "Fix the checkout webhook"},
		{"Add a health check endpoint to the API server so the load balancer can probe it", "Add a health check endpoint to the API server…"},
		{strings.Repeat("x", 60), strings.Repeat("x", 47) + "…"},
		{"Ünïcödé façade résumé naïve coöperate élan déjà vu café crème brûlée", "Ünïcödé façade résumé naïve coöperate élan déjà…"},
	} {
		got := Title(c.in)
		if got != c.want {
			t.Errorf("Title(%q) = %q, want %q", c.in, got, c.want)
		}
		if n := utf8.RuneCountInString(got); n > TitleMax {
			t.Errorf("Title(%q) is %d characters", c.in, n)
		}
	}
}

func TestClaudePromptCarriesOnlyItsTitle(t *testing.T) {
	typ, d, ok := Claude.Translate("UserPromptSubmit", Payload{"cwd": "/w", "session_id": "s", "prompt": "Fix the cart\nsecret details here"})
	if !ok || typ != Started || d["title"] != "Fix the cart" {
		t.Fatalf("got %s %+v", typ, d)
	}
	for k, v := range d {
		if s, _ := v.(string); strings.Contains(s, "secret") {
			t.Errorf("%s carries the prompt: %q", k, s)
		}
	}
}

// Claude Code gives a subagent's tool hooks its agent_id; the agent's own
// have none.
func TestClaudeHelpersToolSaysItIsAHelpers(t *testing.T) {
	_, own, _ := Claude.Translate("PostToolUse", Payload{"cwd": "/w", "session_id": "s", "tool_name": "Bash"})
	_, helper, _ := Claude.Translate("PostToolUse", Payload{"cwd": "/w", "session_id": "s", "tool_name": "Bash", "agent_id": "a61361a194d223429", "agent_type": "general-purpose"})
	if _, has := own["helper"]; has || helper["helper"] != true || helper["signal"] != "tool" {
		t.Fatalf("own %+v, helper %+v", own, helper)
	}
}
