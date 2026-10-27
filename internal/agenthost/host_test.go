package agenthost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// echoAgent says it is ready, warns once on stderr, then answers each line
// it is sent with the same line, as a structured agent answers JSON.
const echoAgent = `echo ready; echo warming up >&2; while IFS= read -r l; do printf '%s\n' "$l"; done`

// started runs a host for script in its own folder, and returns the
// folder, its socket and the host's result once it ends.
func started(t *testing.T, dir, script string) (sock string, result <-chan int) {
	t.Helper()
	// A unix socket's path is short: never under the test's long temp dir.
	sd, err := os.MkdirTemp("/tmp", "ah")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sd) })
	sock = filepath.Join(sd, "s")
	ctx, cancel := context.WithCancel(context.Background())
	res, done := make(chan int, 1), make(chan struct{})
	go func() {
		defer close(done)
		code, err := Run(ctx, Options{Dir: dir, Socket: sock, Command: []string{"sh", "-c", script}, StopGrace: 500 * time.Millisecond})
		if err != nil {
			t.Error(err)
		}
		res <- code
	}()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if c, err := dial(context.Background(), sock); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the host never listened")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return sock, res
}

// collect attaches from from in the background, gathering entries.
type collected struct {
	mu   sync.Mutex
	all  []Entry
	done chan error
}

func collect(sock string, from int64) *collected {
	c := &collected{done: make(chan error, 1)}
	go func() {
		c.done <- Attach(context.Background(), sock, from, func(e Entry) error {
			c.mu.Lock()
			c.all = append(c.all, e)
			c.mu.Unlock()
			return nil
		})
	}()
	return c
}

func (c *collected) waitFor(t *testing.T, ok func([]Entry) bool) []Entry {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		got := append([]Entry(nil), c.all...)
		c.mu.Unlock()
		if ok(got) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t.Fatalf("never saw it; entries: %+v", c.all)
	return nil
}

func has(es []Entry, f func(Entry) bool) bool {
	for _, e := range es {
		if f(e) {
			return true
		}
	}
	return false
}

func TestTheHostLogsWhatPassesBetweenItAndTheAgent(t *testing.T) {
	dir := t.TempDir()
	sock, result := started(t, dir, echoAgent)
	live := collect(sock, 0)

	seq, err := Write(context.Background(), sock, json.RawMessage(`{"type": "user", "text": "hi"}`))
	if err != nil || seq == 0 {
		t.Fatalf("write: %d, %v", seq, err)
	}
	got := live.waitFor(t, func(es []Entry) bool {
		return has(es, func(e Entry) bool { return e.Dir == "out" && e.Data != nil })
	})
	for i, e := range got {
		if e.Seq != int64(i+1) {
			t.Fatalf("entries out of order or missing: %+v", got)
		}
	}
	if got[0].Dir != "host" || got[0].Event != "started" || got[0].PID == 0 {
		t.Fatalf("the first entry isn't the start: %+v", got[0])
	}
	if !has(got, func(e Entry) bool { return e.Dir == "out" && e.Text == "ready" }) ||
		!has(got, func(e Entry) bool { return e.Dir == "err" && e.Text == "warming up" }) {
		t.Fatalf("a line that isn't JSON wasn't kept as text: %+v", got)
	}
	var in, out Entry
	for _, e := range got {
		if e.Dir == "in" {
			in = e
		}
		if e.Dir == "out" && e.Data != nil {
			out = e
		}
	}
	if in.Seq != seq || string(in.Data) != `{"type":"user","text":"hi"}` || string(out.Data) != string(in.Data) || out.Seq <= in.Seq {
		t.Fatalf("in = %+v, out = %+v", in, out)
	}

	if _, err := Write(context.Background(), sock, json.RawMessage(`not json`)); err == nil {
		t.Fatal("a line that isn't JSON was written to the agent")
	}

	// A reader attaching later from where another left off misses nothing.
	late := collect(sock, in.Seq)
	lateGot := late.waitFor(t, func(es []Entry) bool { return len(es) >= len(got)-int(in.Seq) })
	if lateGot[0].Seq != in.Seq+1 {
		t.Fatalf("attaching from %d began at %d", in.Seq, lateGot[0].Seq)
	}

	if err := Stop(context.Background(), sock); err != nil {
		t.Fatal(err)
	}
	if code := <-result; code != 0 {
		// The agent ends at its stdin's end, as structured agents do.
		t.Fatalf("exit code %d", code)
	}
	if err := <-live.done; err != nil {
		t.Fatal(err)
	}
	final := live.waitFor(t, func(es []Entry) bool { return len(es) > 0 && es[len(es)-1].Event == "exited" })
	if c := final[len(final)-1].Code; c == nil || *c != 0 {
		t.Fatalf("the exit wasn't logged last: %+v", final[len(final)-1])
	}
	if err := Attach(context.Background(), sock, 0, func(Entry) error { return nil }); !errors.Is(err, ErrNoHost) {
		t.Fatalf("attach after the end: %v", err)
	}

	// The log outlives the host, and a host started again carries it on.
	var fromFile []Entry
	ReadLog(filepath.Join(dir, LogName), 0, func(e Entry) error { fromFile = append(fromFile, e); return nil })
	if len(fromFile) != len(final) {
		t.Fatalf("the log file has %d entries, the stream had %d", len(fromFile), len(final))
	}
	sock2, result2 := started(t, dir, `sleep 0.5; exit 3`)
	_ = sock2
	if code := <-result2; code != 3 {
		t.Fatalf("exit code %d", code)
	}
	var again []Entry
	ReadLog(filepath.Join(dir, LogName), int64(len(final)), func(e Entry) error { again = append(again, e); return nil })
	if len(again) != 2 || again[0].Seq != int64(len(final)+1) || again[0].Event != "started" || *again[1].Code != 3 {
		t.Fatalf("a second host didn't carry the log on: %+v", again)
	}
}

func TestStoppingKillsWhatTheAgentStartedToo(t *testing.T) {
	// An agent that ignores its stdin's end and SIGTERM, with a child that
	// left its group, as a tool's process can.
	script := `trap '' TERM; perl -e 'use POSIX; setsid(); exec "sleep", "300"' & echo "child $!"; while :; do sleep 1; done`
	sock, result := started(t, t.TempDir(), script)
	live := collect(sock, 0)
	got := live.waitFor(t, func(es []Entry) bool {
		return has(es, func(e Entry) bool { return strings.HasPrefix(e.Text, "child ") })
	})
	var child int
	for _, e := range got {
		if strings.HasPrefix(e.Text, "child ") {
			child, _ = strconv.Atoi(strings.TrimPrefix(e.Text, "child "))
		}
	}
	if child == 0 || syscall.Kill(child, 0) != nil {
		t.Fatalf("the child isn't running: %d", child)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Stop(ctx, sock); err != nil {
		t.Fatal(err)
	}
	if code := <-result; code != -1 {
		t.Fatalf("a killed agent's exit code is %d", code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(child, 0) == nil {
		if time.Now().After(deadline) {
			syscall.Kill(child, syscall.SIGKILL)
			t.Fatal("the agent's child outlived the stop")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestALogCutShortCarriesOnFromItsLastWholeEntry(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	l.Append(Entry{Dir: "out", Text: "one"})
	l.Append(Entry{Dir: "out", Text: "two"})
	l.Close()
	f, _ := os.OpenFile(filepath.Join(dir, LogName), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"seq":3,"dir":"out","te`)
	f.Close()

	l, err = OpenLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := l.Append(Entry{Dir: "out", Text: "three"})
	l.Close()
	if e.Seq != 3 {
		t.Fatalf("seq %d after a cut line", e.Seq)
	}
	b, _ := os.ReadFile(filepath.Join(dir, LogName))
	if n := bytes.Count(b, []byte("\n")); n != 3 || bytes.Contains(b, []byte(`"te`+"\n")) {
		t.Fatalf("the log:\n%s", b)
	}
}

func TestAReaderThatFallsBehindIsLetGo(t *testing.T) {
	l, err := OpenLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ch, stop, err := l.Follow(0)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	for range subBuffer + 1 {
		l.Append(Entry{Dir: "out", Text: "x"})
	}
	n := 0
	for range ch {
		n++
	}
	if n != subBuffer {
		t.Fatalf("read %d entries before being let go", n)
	}
}
