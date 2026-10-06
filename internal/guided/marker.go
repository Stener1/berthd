// Package guided is Berth's guided install of a box over SSH: the plan a
// person reads before anything runs, the script that runs the steps on the
// box (in a terminal the person can type sudo's password into), and the
// step markers that script prints for the checklist beside it.
//
// The same flow serves `berth add ssh` in the person's own terminal and the
// app, which runs that command in a pseudo-terminal on the laptop and shows
// it full screen. Berth never sees the password: sudo asks for it on the
// box's terminal, and the bytes only pass through.
package guided

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// MarkerPrefix starts a step marker, one line each:
//
//	::berth-step <id> <state> [message]
//
// States are start, done, fail, skip; open (the message is a page for the
// person to open, such as Tailscale SSH's login approval); and cmd (the
// message is the command to run by hand, when a step can't do its work
// without a person, as with sudo's password and no terminal).
const MarkerPrefix = "::berth-step "

// FailurePrefix starts the line `berth add ssh` prints a failed SSH login
// on as JSON (sshsetup.FailurePrefix); the filter takes it out too.
const FailurePrefix = "berth-failure: "

// StepsEnv set to 1 asks `berth add ssh` for its steps as markers, for the
// laptop agent to turn into the app's checklist, rather than drawn as lines
// for a person.
const StepsEnv = "BERTH_STEPS"

// Step states.
const (
	Start = "start"
	Done  = "done"
	Fail  = "fail"
	Skip  = "skip"
	Open  = "open"
	Cmd   = "cmd"
)

// Event is one marker.
type Event struct {
	Step    string `json:"step"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

// Marker is the line for one step's state, ending in a newline. A message
// is one line: newlines in it become spaces.
func Marker(step, state, msg string) string {
	msg = strings.Join(strings.Fields(msg), " ")
	if msg == "" {
		return MarkerPrefix + step + " " + state + "\n"
	}
	return MarkerPrefix + step + " " + state + " " + msg + "\n"
}

// ParseMarker reads a marker line (with or without its prefix's
// surroundings: a trailing \r or \n is fine).
func ParseMarker(line string) (Event, bool) {
	rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), MarkerPrefix)
	if !ok {
		return Event{}, false
	}
	f := strings.SplitN(strings.TrimSpace(rest), " ", 3)
	if len(f) < 2 || !validID(f[0]) {
		return Event{}, false
	}
	switch f[1] {
	case Start, Done, Fail, Skip, Open, Cmd:
	default:
		return Event{}, false
	}
	e := Event{Step: f[0], State: f[1]}
	if len(f) == 3 {
		e.Message = strings.TrimSpace(f[2])
	}
	return e, true
}

func validID(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// maxLine bounds a line the filter holds back while it may be a marker.
const maxLine = 64 << 10

// Filter passes a terminal's output through to Out, taking out step
// markers and the failure line wherever they appear, and hands them to
// OnStep and OnFailure. Markers can arrive split across writes; the bytes
// of a possible marker are held until its line ends. A marker found in the
// middle of a line ends that line on the screen.
type Filter struct {
	Out       io.Writer
	OnStep    func(Event)
	OnFailure func(json.RawMessage)

	// pending holds bytes that may be the start of a marker, or a whole
	// marker up to its newline.
	pending []byte
	// midLine is whether the last byte passed through was not a newline.
	midLine bool
}

var prefixes = [][]byte{[]byte(MarkerPrefix), []byte(FailurePrefix)}

// Write never fails because of a marker; it returns Out's error.
func (f *Filter) Write(p []byte) (int, error) {
	var out bytes.Buffer
	for _, c := range p {
		f.pending = append(f.pending, c)
		f.settle(&out)
	}
	if out.Len() > 0 {
		if _, err := f.Out.Write(out.Bytes()); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// settle decides what pending is: a whole marker (taken out), a marker
// still arriving (held), or output (passed on until what is left could
// begin a marker).
func (f *Filter) settle(out *bytes.Buffer) {
	for len(f.pending) > 0 {
		if full := fullPrefix(f.pending); full != nil {
			last := f.pending[len(f.pending)-1]
			if last == '\n' {
				f.take(out, string(f.pending), full)
				f.pending = f.pending[:0]
				return
			}
			if len(f.pending) > maxLine {
				f.pass(out, f.pending)
				f.pending = f.pending[:0]
			}
			return
		}
		if couldBegin(f.pending) {
			return
		}
		f.pass(out, f.pending[:1])
		f.pending = f.pending[1:]
	}
}

func (f *Filter) pass(out *bytes.Buffer, b []byte) {
	out.Write(b)
	if len(b) > 0 {
		f.midLine = b[len(b)-1] != '\n'
	}
}

func (f *Filter) take(out *bytes.Buffer, line string, prefix []byte) {
	if f.midLine {
		// What was on the line before the marker stays, on a line of its own.
		out.WriteString("\r\n")
		f.midLine = false
	}
	if string(prefix) == FailurePrefix {
		raw := strings.TrimSpace(strings.TrimPrefix(line, FailurePrefix))
		if json.Valid([]byte(raw)) && f.OnFailure != nil {
			f.OnFailure(json.RawMessage(raw))
		}
		return
	}
	if e, ok := ParseMarker(line); ok && f.OnStep != nil {
		f.OnStep(e)
	}
}

// Flush passes on what is held back: the end of the output, which no
// newline will follow. A complete marker without its newline still counts.
func (f *Filter) Flush() error {
	if len(f.pending) == 0 {
		return nil
	}
	var out bytes.Buffer
	if full := fullPrefix(f.pending); full != nil {
		f.take(&out, string(f.pending), full)
	} else {
		f.pass(&out, f.pending)
	}
	f.pending = f.pending[:0]
	if out.Len() == 0 {
		return nil
	}
	_, err := f.Out.Write(out.Bytes())
	return err
}

func fullPrefix(b []byte) []byte {
	for _, p := range prefixes {
		if bytes.HasPrefix(b, p) {
			return p
		}
	}
	return nil
}

func couldBegin(b []byte) bool {
	for _, p := range prefixes {
		if len(b) < len(p) && bytes.HasPrefix(p, b) {
			return true
		}
	}
	return false
}
