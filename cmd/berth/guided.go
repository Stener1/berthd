package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/sean-brydon/berthd/internal/guided"
)

// stepReporter shows the guided install's steps: as markers, one line each,
// for the laptop agent to turn into the app's checklist (BERTH_STEPS=1), or
// drawn for a person in their own terminal.
type stepReporter struct {
	mu      sync.Mutex
	out     io.Writer
	markers bool
	color   bool
	titles  map[string]string
	// midLine is whether the last thing written did not end its line.
	midLine bool
	// failed is the step that failed and why; command, the command to run
	// by hand, when a step said one.
	failed, why, command string
	// current is the step that started last and has not ended.
	current string
}

func newStepReporter(out io.Writer, markers bool) *stepReporter {
	color := false
	if f, ok := out.(*os.File); ok && isTerminal(f) && os.Getenv("NO_COLOR") == "" {
		color = true
	}
	return &stepReporter{out: out, markers: markers, color: color, titles: map[string]string{}}
}

func (r *stepReporter) setPlan(steps []guided.Step) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range steps {
		r.titles[s.ID] = s.Title
	}
}

func (r *stepReporter) title(id string) string {
	if t := r.titles[id]; t != "" {
		return t
	}
	return id
}

func (r *stepReporter) paint(code, s string) string {
	if !r.color {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

// Write passes output through, remembering whether it ended a line, so a
// marker always starts on a line of its own.
func (r *stepReporter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(p) > 0 {
		r.midLine = p[len(p)-1] != '\n'
	}
	return r.out.Write(p)
}

func (r *stepReporter) event(e guided.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch e.State {
	case guided.Start:
		if r.current == e.Step {
			return
		}
		r.current = e.Step
	case guided.Done, guided.Skip:
		if r.current == e.Step {
			r.current = ""
		}
	case guided.Fail:
		r.failed, r.why = e.Step, e.Message
		r.current = ""
	case guided.Cmd:
		r.command = e.Message
	}
	if r.markers {
		if r.midLine {
			io.WriteString(r.out, "\r\n")
		}
		io.WriteString(r.out, strings.TrimSuffix(guided.Marker(e.Step, e.State, e.Message), "\n")+"\r\n")
		r.midLine = false
		return
	}
	lead := ""
	if r.midLine {
		lead = "\r\n"
	}
	r.midLine = false
	t := r.title(e.Step)
	switch e.State {
	case guided.Start:
		fmt.Fprintf(r.out, "%s%s %s\r\n", lead, r.paint("1", "==>"), r.paint("1", t))
	case guided.Done:
		msg := ""
		if e.Message != "" && e.Step != guided.StepPair {
			msg = r.paint("2", " ("+e.Message+")")
		}
		fmt.Fprintf(r.out, "%s%s %s%s\r\n", lead, r.paint("32", "✓"), t, msg)
	case guided.Skip:
		fmt.Fprintf(r.out, "%s%s %s%s\r\n", lead, r.paint("2", "–"), r.paint("2", t), r.paint("2", " ("+e.Message+")"))
	case guided.Fail:
		fmt.Fprintf(r.out, "%s%s %s: %s\r\n", lead, r.paint("31", "✗"), t, e.Message)
	case guided.Open:
		fmt.Fprintf(r.out, "%sTailscale SSH asks you to approve this login in your browser: %s\r\n", lead, e.Message)
	case guided.Cmd:
		// The script prints the command itself.
	}
}

func (r *stepReporter) start(id string) { r.event(guided.Event{Step: id, State: guided.Start}) }
func (r *stepReporter) done(id, msg string) {
	r.event(guided.Event{Step: id, State: guided.Done, Message: msg})
}
func (r *stepReporter) skip(id, msg string) {
	r.event(guided.Event{Step: id, State: guided.Skip, Message: msg})
}
func (r *stepReporter) fail(id, msg string) {
	r.event(guided.Event{Step: id, State: guided.Fail, Message: msg})
}
func (r *stepReporter) failCurrent(msg string) {
	r.mu.Lock()
	id := r.current
	r.mu.Unlock()
	if id != "" {
		r.fail(id, msg)
	}
}

// printPlan is the plan as a person reads it in a terminal: each step,
// which need sudo, and the commands under each.
func printPlan(w io.Writer, steps []guided.Step, color bool) {
	paint := func(code, s string) string {
		if !color {
			return s
		}
		return "\033[" + code + "m" + s + "\033[0m"
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, paint("1", "Berth will set this box up:"))
	n := 0
	for _, s := range steps {
		if s.Skip != "" {
			fmt.Fprintf(w, "   %s\n", paint("2", "– "+s.Title+" ("+s.Skip+")"))
			continue
		}
		n++
		badge := ""
		if s.Sudo {
			badge = paint("33", "  [sudo]")
			if s.When != "" {
				badge += paint("2", " "+s.When)
			}
		}
		fmt.Fprintf(w, "%2d. %s%s\n", n, s.Title, badge)
		for _, c := range s.Commands {
			fmt.Fprintf(w, "      %s\n", paint("2", c))
		}
	}
	if sudo := guided.SudoSteps(steps); len(sudo) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s needs root: sudo asks for your password on the box, in this terminal.\n", strings.Join(sudo, " and "))
		fmt.Fprintln(w, "Berth never sees it or keeps it.")
	}
}

// waitForEnter waits for the person to press Enter; false means they
// stopped (Ctrl-D, or the terminal closed).
func waitForEnter(in io.Reader, out io.Writer, color bool) bool {
	msg := "Press Enter to start, or Ctrl-C to stop."
	if color {
		msg = "\033[1m" + msg + "\033[0m"
	}
	fmt.Fprintf(out, "\n%s ", msg)
	_, err := bufio.NewReader(in).ReadString('\n')
	return err == nil
}

// askListenEverywhere asks, for a box with no tailnet address, whether
// berthd may listen on every interface. Enter means yes: the question is
// the start of the install, as "Press Enter to start" is otherwise.
func askListenEverywhere(in io.Reader, out io.Writer, host string, color bool) bool {
	fmt.Fprintf(out, "\r\n%s has no tailnet (Tailscale) address, where berthd listens by default.\r\n", host)
	fmt.Fprint(out, "berthd can listen on every interface instead, port 7444: only laptops you pair\r\n")
	fmt.Fprint(out, "can connect (both keys are pinned), but anything that reaches the box can reach the port.\r\n")
	msg := "Listen on every interface (0.0.0.0:7444) and start? [Y/n]"
	if color {
		msg = "\033[1m" + msg + "\033[0m"
	}
	fmt.Fprintf(out, "\r\n%s ", msg)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true
	}
	return false
}
