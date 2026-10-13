package box

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/cosscom/shipyard/internal/transcript"
)

// recordingDriver stands in for a session's driver and notes each call.
type recordingDriver struct {
	mu    sync.Mutex
	calls []string
}

func (d *recordingDriver) note(s string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, s)
}

func (d *recordingDriver) seen() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.calls)
}

func (d *recordingDriver) Kind() string { return DriverTerminal }

func (d *recordingDriver) Send(_ context.Context, sess Session, text string, enter bool) error {
	d.note("send " + sess.Name + " " + text)
	return nil
}

func (d *recordingDriver) Interrupt(_ context.Context, sess Session) (bool, error) {
	d.note("interrupt " + sess.Name)
	return true, nil
}

func (d *recordingDriver) Controls(_ context.Context, sess Session) (Controls, error) {
	d.note("controls " + sess.Name)
	return Controls{Agent: "claude", Mode: "plan"}, nil
}

func (d *recordingDriver) SetMode(_ context.Context, sess Session, mode string) (string, error) {
	d.note("mode " + sess.Name + " " + mode)
	return mode, nil
}

func (d *recordingDriver) Answer(_ context.Context, sess Session, req AnswerRequest) ([]string, error) {
	d.note("answer " + sess.Name + " " + req.Tool)
	return []string{"Tea"}, nil
}

func (d *recordingDriver) Conversation(_ context.Context, sess Session, _ []Session, since int, _ string) (transcript.Result, error) {
	d.note("conversation " + sess.Name)
	return transcript.Result{Source: "driver", Items: []transcript.Item{{Kind: "text", ID: "1", Text: "hello"}}}, nil
}

func (d *recordingDriver) Record(_ context.Context, sess Session, _ []Session) (string, string) {
	return "", ""
}

func (d *recordingDriver) Draft(_ context.Context, sess Session) (Draft, error) {
	d.note("draft " + sess.Name)
	return Draft{Agent: "claude", Text: "writing"}, nil
}

func TestTheAgentIsReachedThroughItsSessionsDriver(t *testing.T) {
	d := &recordingDriver{}
	c, _ := servedBox(t, func(b *Box) { b.Drivers = map[string]Driver{DriverTerminal: d} })
	repo := gitRepo(t)
	call(t, c, "POST", "/v1/locations", "", map[string]string{"name": "acme", "path": repo}, nil)
	bin := t.TempDir()
	fake := filepath.Join(bin, "claude")
	os.WriteFile(fake, []byte("#!/bin/sh\nexec cat\n"), 0o755)
	call(t, c, "POST", "/v1/sessions", "", map[string]string{"location": "acme", "name": "agent", "command": fake}, nil)

	var list []Session
	call(t, c, "GET", "/v1/sessions", "", nil, &list)
	if len(list) != 1 || list[0].Driver != DriverTerminal {
		t.Fatalf("the session doesn't say its driver: %+v", list)
	}

	for _, step := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/v1/sessions/agent/send", map[string]any{"text": "fix the build"}},
		{"POST", "/v1/sessions/agent/interrupt", nil},
		{"GET", "/v1/sessions/agent/controls", nil},
		{"POST", "/v1/sessions/agent/mode", map[string]string{"mode": "plan"}},
		{"POST", "/v1/sessions/agent/answer", map[string]any{"tool": "toolu_1", "answers": []any{map[string]any{"picks": []string{"Tea"}}}}},
	} {
		if status := call(t, c, step.method, step.path, "", step.body, nil); status != 200 {
			t.Fatalf("%s %s: %d", step.method, step.path, status)
		}
	}
	var conv transcript.Result
	call(t, c, "GET", "/v1/sessions/agent/transcript?since=0", "", nil, &conv)
	if conv.Source != "driver" || len(conv.Items) != 1 {
		t.Fatalf("the conversation didn't come from the driver: %+v", conv)
	}

	want := []string{"send agent fix the build", "interrupt agent", "controls agent", "mode agent plan", "answer agent toolu_1", "conversation agent"}
	if got := d.seen(); !slices.Equal(got, want) {
		t.Fatalf("driver calls = %q, want %q", got, want)
	}
}
