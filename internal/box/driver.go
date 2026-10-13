package box

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cosscom/shipyard/internal/integrations/adapters"
	"github.com/cosscom/shipyard/internal/transcript"
)

// A Driver is how berthd reaches the agent in a session: how a prompt gets
// to it, how it is stopped, how its permission mode is read and changed,
// how its questions are answered, and where the chat reads its
// conversation. The API's handlers keep what is the same for every agent
// (gates, turns, the inbox, events) and leave the agent itself to its
// session's driver.
//
// Every session has the terminal driver today: the agent's own TUI in
// tmux, typed into, read from its screen, and its conversation read from
// the transcript file it writes. Structured drivers (Claude Code's
// stream-json, Codex's app-server, ACP) plug in beside it
// (design/structured-agents/plan.md). Claude Code and Codex write their
// transcript files under any driver, so what reads those files (a tool
// call's details, older pages, helpers, fork and rewind, touched files)
// keeps working for every session.
type Driver interface {
	// Kind names the driver in the session list ("terminal").
	Kind() string
	// Send gives the agent text, and submits it when enter is set. A
	// single key with no Enter is an answer to a menu.
	Send(ctx context.Context, sess Session, text string, enter bool) error
	// Interrupt stops the agent's current turn, and says whether it did.
	Interrupt(ctx context.Context, sess Session) (stopped bool, err error)
	// Controls are the agent's permission mode and the modes it offers.
	Controls(ctx context.Context, sess Session) (Controls, error)
	// SetMode switches the permission mode, and returns the mode it is in.
	SetMode(ctx context.Context, sess Session, mode string) (string, error)
	// Answer answers the questions one AskUserQuestion call asked, and
	// returns each answer as the agent shows it.
	Answer(ctx context.Context, sess Session, req AnswerRequest) ([]string, error)
	// Conversation is the chat's view of the session from item since.
	// all is the box's sessions when the caller has just listed them.
	Conversation(ctx context.Context, sess Session, all []Session, since int, gen string) (transcript.Result, error)
	// Record is the file the conversation is read from, and how: its
	// older pages are read from it too. An empty path is none yet.
	Record(ctx context.Context, sess Session, all []Session) (source, path string)
	// Draft is the reply the agent is writing, before its record has it.
	Draft(ctx context.Context, sess Session) (Draft, error)
}

// DriverTerminal is a session's agent in its own terminal: every session
// but a structured one (DriverClaude).
const DriverTerminal = "terminal"

// driverFor is the driver for a session's agent.
func (b *Box) driverFor(sess Session) Driver {
	kind := firstNonEmpty(sess.Driver, DriverTerminal)
	if d := b.Drivers[kind]; d != nil {
		return d
	}
	if kind == DriverClaude {
		return b.claude()
	}
	return terminalDriver{b}
}

// sendText gives the named session's agent text through its driver.
func (b *Box) sendText(ctx context.Context, name, text string, enter bool) error {
	sess, err := b.Sessions.Get(ctx, name)
	if err != nil {
		return err
	}
	if sess.Exited {
		return ErrSessionExited
	}
	return b.driverFor(sess).Send(ctx, sess, text, enter)
}

// terminalDriver is the agent's TUI in its tmux pane.
type terminalDriver struct{ b *Box }

func (terminalDriver) Kind() string { return DriverTerminal }

func (t terminalDriver) Send(ctx context.Context, sess Session, text string, enter bool) error {
	return t.b.Sessions.Send(ctx, sess.Name, text, enter)
}

// Interrupt presses Esc, as stopping an agent at its terminal does, and
// waits for the screen to say it stopped: its "esc to interrupt" goes, and
// Claude Code says "Interrupted".
func (t terminalDriver) Interrupt(ctx context.Context, sess Session) (bool, error) {
	if err := t.b.pressKeys(ctx, sess.Name, "Escape"); err != nil {
		return false, err
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(150 * time.Millisecond)
		screen, err := t.b.Sessions.Screen(ctx, sess.Name, 0)
		if err != nil {
			return false, nil
		}
		if !working.MatchString(lastLines(screen, 14)) && adapters.ScreenState(screen) != "waiting" {
			return true, nil
		}
	}
	return false, nil
}

// Controls reads the agent's footer.
func (t terminalDriver) Controls(ctx context.Context, sess Session) (Controls, error) {
	agent := controlAgent(sess)
	out := Controls{Agent: agent}
	if sess.Exited {
		return out, nil
	}
	screen, err := t.b.Sessions.capture(ctx, sess.Name, 0)
	if err != nil {
		return out, err
	}
	if agent == "claude" {
		out = claudeControls(screen)
		out.Modes = []string{"default", "acceptEdits", "plan", "auto", "bypassPermissions"}
	} else if m := screenLimit.FindAllString(lastLines(screen, 20), -1); m != nil {
		out.Limit = strings.TrimSpace(m[len(m)-1])
	}
	return out, nil
}

// SetMode presses Shift+Tab, as a person cycling Claude Code's modes does,
// until its screen shows that mode. A mode the session can't reach
// (bypass, unless it was started allowing it) is a 409.
func (t terminalDriver) SetMode(ctx context.Context, sess Session, want string) (string, error) {
	if controlAgent(sess) != "claude" {
		return "", badRequest("only Claude Code's mode can be switched from here; use its terminal")
	}
	read := func() (string, error) {
		screen, err := t.b.Sessions.Screen(ctx, sess.Name, 0)
		if err != nil {
			return "", err
		}
		if adapters.ScreenState(screen) == "waiting" {
			return "", httpError{http.StatusConflict, "the agent is showing a question; answer it first"}
		}
		return claudeControls(screen).Mode, nil
	}
	mode, err := read()
	if err != nil {
		return "", err
	}
	seen := map[string]bool{mode: true}
	for presses := 0; mode != want && presses < 7; presses++ {
		if err := t.b.pressKeys(ctx, sess.Name, "BTab"); err != nil {
			return "", err
		}
		// The footer redraws within a moment.
		next := mode
		for wait := 0; wait < 10 && next == mode; wait++ {
			time.Sleep(100 * time.Millisecond)
			if next, err = read(); err != nil {
				return "", err
			}
		}
		mode = next
		if mode != want && seen[mode] && presses > 0 {
			break // round the whole cycle: not one this session has
		}
		seen[mode] = true
	}
	if mode != want {
		msg := "this session doesn't offer that mode; it is in " + mode + " mode"
		if want == "bypassPermissions" {
			msg = "bypass needs Claude Code started with --dangerously-skip-permissions; this session is in " + mode + " mode"
		}
		return "", httpError{http.StatusConflict, msg}
	}
	return mode, nil
}

// Answer finds the questions in Claude Code's record and fills in its form
// with keys (answer.go).
func (t terminalDriver) Answer(ctx context.Context, sess Session, req AnswerRequest) ([]string, error) {
	path, err := t.b.claudeRecordOf(ctx, sess, nil)
	if err != nil {
		return nil, err
	}
	res, err := reader().Read("claude", path, sess.Dir, 0)
	if err != nil {
		return nil, err
	}
	it, open := transcript.OpenQuestion(res, req.Tool)
	switch {
	case it.Kind == "":
		return nil, httpError{http.StatusNotFound, "that question isn't in Claude's conversation"}
	case !open:
		return nil, httpError{http.StatusConflict, "those questions were already answered"}
	}
	shown, err := checkAnswers(it.Questions, req.Answers)
	if err != nil {
		return nil, err
	}
	if err := driveAnswers(ctx, tmuxTerm{s: t.b.Sessions, target: "=" + sess.Name + ":"}, it.Questions, req.Answers); err != nil {
		return nil, err
	}
	return shown, nil
}

// Record is the agent's own transcript file.
func (t terminalDriver) Record(ctx context.Context, sess Session, all []Session) (string, string) {
	agent, path, _ := t.b.transcriptFile(ctx, sess, all)
	if agent != "claude" && agent != "codex" {
		return "", ""
	}
	return agent, path
}

// Draft reads the reply Claude Code is drawing from its styled screen
// (draft.go). Other agents aren't drafted.
func (t terminalDriver) Draft(ctx context.Context, sess Session) (Draft, error) {
	agent := firstNonEmpty(sess.Preset, firstNonEmpty(sess.Agent, agentOf(sess.Command)))
	out := Draft{Agent: agent}
	if agent != "claude" || sess.Service != "" || sess.Exited {
		return out, nil
	}
	_, raw, err := t.b.Sessions.getAndCapture(ctx, sess.Name, "-J", "-e")
	if err != nil {
		return out, err
	}
	out = ParseClaudeDraft(string(raw))
	out.Agent = agent
	return out, nil
}

// Conversation reads the agent's own transcript file (internal/transcript),
// and asks the watch to say when it is written to.
func (t terminalDriver) Conversation(ctx context.Context, sess Session, all []Session, since int, gen string) (transcript.Result, error) {
	agent, path, where := t.b.transcriptFile(ctx, sess, all)
	none := func(reason string) (transcript.Result, error) {
		return transcript.Result{Source: "none", Items: []transcript.Item{}, Crew: []transcript.CrewMember{}, Reason: reason}, nil
	}
	switch {
	case agent != "claude" && agent != "codex":
		return none("Shipyard reads Claude Code's and Codex's conversations; this session runs " + firstNonEmpty(agent, "no agent") + ".")
	case path == "":
		return none("No " + agent + " conversation for " + sess.Dir + " since " + sess.Created.Format(time.RFC3339) + " in " + where + ".")
	}
	// A chat reads this while it shows: tell it as soon as the agent writes.
	t.b.transcriptWatch().seen(sess.Name, sess.Dir, path)
	// gen is the reading since counts in: one the box let go is read
	// afresh, and answered whole (transcript.Result.Gen).
	res, err := reader().Follow(agent, path, sess.Dir, max(since, 0), gen)
	if err != nil {
		return none("Couldn't read " + path + ": " + err.Error())
	}
	return res, nil
}
