package transcript

import (
	"fmt"
	"os"
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

// A log that starts by loading one of the agent's past sessions shows what
// the agent replays for it; a log that has its conversation already, loaded
// again when its host starts again, doesn't show it twice.
func TestALoadedACPSessionShowsItsReplayOnlyOnce(t *testing.T) {
	replay := `{"seq":%d,"at":1791900003000,"dir":"in","data":{"jsonrpc":"2.0","id":"load","method":"session/load","params":{"sessionId":"ses_1","cwd":"/home/dev/acme"}}}
{"seq":%d,"at":1791900003100,"dir":"out","data":{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"ses_1","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"Fix the flaky test"}}}}}
{"seq":%d,"at":1791900003200,"dir":"out","data":{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"ses_1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Fixed it."}}}}}
{"seq":%d,"at":1791900003300,"dir":"out","data":{"jsonrpc":"2.0","id":"load","result":{}}}
`
	read := func(log string) []string {
		path := filepath.Join(t.TempDir(), "log.jsonl")
		if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
			t.Fatal(err)
		}
		res, err := NewReader().Read(SourceACPHost, path, "/home/dev/acme", 0)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, it := range res.Items {
			got = append(got, it.Kind+":"+it.Text)
		}
		return got
	}
	if got := read(fmt.Sprintf(replay, 1, 2, 3, 4)); strings.Join(got, "|") != "user:Fix the flaky test|text:Fixed it." {
		t.Errorf("picked session: %q, want what it replayed", got)
	}
	own := `{"seq":1,"at":1791900001000,"dir":"in","data":{"jsonrpc":"2.0","id":"p1","method":"session/prompt","params":{"sessionId":"ses_1","prompt":[{"type":"text","text":"Fix the flaky test"}]}}}
{"seq":2,"at":1791900001100,"dir":"out","data":{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"ses_1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Fixed it."}}}}}
{"seq":3,"at":1791900001200,"dir":"out","data":{"jsonrpc":"2.0","id":"p1","result":{"stopReason":"end_turn"}}}
`
	if got := read(own + fmt.Sprintf(replay, 4, 5, 6, 7)); strings.Join(got, "|") != "user:Fix the flaky test|text:Fixed it." {
		t.Errorf("own conversation loaded again: %q, want it once", got)
	}
}
