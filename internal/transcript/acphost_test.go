package transcript

import (
	"path/filepath"
	"strings"
	"testing"
)

// acp_host_opencode.jsonl is an agent-host log of opencode speaking ACP
// (recorded, then cut down and made anonymous): a prompt, a shell command
// it asked permission for and ran, and its streamed reply.
func TestAStructuredACPSessionsLogReadsAsItsConversation(t *testing.T) {
	path := filepath.Join("testdata", "acp_host_opencode.jsonl")
	res, err := NewReader().Read(SourceACPHost, path, "/home/dev/spike/repo-acp", 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range res.Items {
		s := it.Kind
		switch it.Kind {
		case "text", "user":
			s += ":" + it.Text
		case "tools":
			for _, c := range it.Items {
				s += ":" + c.Verb + " " + c.Target
			}
			if !it.Done {
				s += " (running)"
			}
		}
		got = append(got, s)
	}
	want := "user:Run the shell command `echo spike > made_by_agent.txt` in the current directory, then reply DONE. Do not ask me questions.\n" +
		"tools:Run echo spike > made_by_agent.txt\n" +
		"text:DONE"
	if res.Source != "acp" || strings.Join(got, "\n") != want {
		t.Fatalf("source %q, items:\n%s", res.Source, strings.Join(got, "\n"))
	}
}
