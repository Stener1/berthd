package transcript

import (
	"path/filepath"
	"strings"
	"testing"
)

// codex_host_approve.jsonl is an agent-host log of `codex app-server`
// (recorded, then cut down and made anonymous): a turn whose command was
// approved, then one whose command was declined.
func TestAStructuredCodexSessionsLogReadsAsItsConversation(t *testing.T) {
	path := filepath.Join("testdata", "codex_host_approve.jsonl")
	res, err := NewReader().Read(SourceCodexHost, path, "/home/dev/spike/repo-codex", 0)
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
		}
		got = append(got, s)
	}
	want := []string{
		"user:Run the shell command `touch approved.txt` in the current directory (exactly that, once). Then say DONE.",
		"tools:Run touch approved.txt",
		"text:DONE",
		"user:Now run the shell command `touch declined.txt` (exactly that, once). If it is refused, do not retry; just say REFUSED.",
		"text:I’ll run the exact command once.",
		"tools:Run touch declined.txt",
		"text:REFUSED",
	}
	if res.Source != "codex" || strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("source %q, items:\n%s", res.Source, strings.Join(got, "\n"))
	}
	// A command's details: what ran, and that the declined one failed.
	declined := res.Items[5].Items[0].ID
	d, err := Detail(SourceCodexHost, path, "/home/dev/spike/repo-codex", declined)
	if err != nil || d.Command != "touch declined.txt" || !d.Error {
		t.Fatalf("detail = %+v, %v", d, err)
	}
}
