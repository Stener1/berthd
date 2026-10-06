package box

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sean-brydon/berthd/internal/groups"
)

// A service with "terminal": true runs in a terminal of its own instead of
// in the background: a session in berth's tmux server, which the app shows
// as a tab named after the service. It starts, stops and reports like any
// service, with the worktree's environment, its secrets resolved by
// `berthd secret exec`, and its output in the service's log too.
//
// The session outlives the program. Ctrl-C in the tab ends the program as
// a stop would, the pane stays (berth's tmux keeps dead panes) with what it
// printed, and the next start runs it again in the same pane, so the tab
// that showed it carries on. Only removing the worktree ends the session.
//
// It is never an agent: its session says which service it is
// (@berth_service), and everything that looks for agents skips it.

// serviceSession is the tmux session a terminal service runs in. Session
// names are at most 63 characters; a longer one keeps its start and the
// service's name, with a hash of the whole in between so it stays unique.
func serviceSession(loc, wt, svc string) string {
	name := "svc-" + slug(loc, 30) + "-" + slug(wt, 60) + "-" + svc
	if len(name) <= 63 {
		return name
	}
	sum := sha1.Sum([]byte(loc + "/" + wt + "/" + svc))
	tail := "-" + hex.EncodeToString(sum[:3]) + "-" + svc
	return strings.TrimRight(name[:63-len(tail)], "-") + tail
}

// serviceTitle names a service's tab.
func serviceTitle(s WorktreeService) string {
	if t := strings.TrimSpace(s.Title); t != "" {
		return t
	}
	return s.Name
}

// terminalState is a terminal service's state, from its session: running
// while its program runs, stopped once it has ended or with no session.
func terminalState(sessions []Session, name string) string {
	for _, s := range sessions {
		if s.Name == name {
			if s.Exited {
				return "stopped"
			}
			return "running"
		}
	}
	return "stopped"
}

// serviceRun is what a terminal service's session runs, and how it is
// labelled.
type serviceRun struct {
	Name, Location, Dir string
	Service, Title      string
	// Command is the service's run line, which the session runs in Dir
	// through the login shell, behind Wrap when there is one.
	Command string
	Env     []string
	Wrap    []string
	// Log, when set, gets a copy of everything the program prints.
	Log string
}

// tmuxArg keeps an argument whole: tmux reads one ending in ";" as the end
// of a command, unless the ";" is escaped.
func tmuxArg(a string) string {
	if strings.HasSuffix(a, ";") {
		return a[:len(a)-1] + `\;`
	}
	return a
}

// runService starts a terminal service: a new session, or its program
// again in the session it ended in. One that is running is left as it is.
// The session's labels are set in the same tmux command that makes it, so
// no list ever sees it unlabelled (as an agent, say).
func (s *Sessions) runService(ctx context.Context, r serviceRun) error {
	if _, err := tmuxPath(); err != nil {
		return err
	}
	if !sessionName.MatchString(r.Name) {
		return fmt.Errorf("invalid session name %q", r.Name)
	}
	cur, err := s.Get(ctx, r.Name)
	target := "=" + r.Name + ":"
	var args []string
	fresh := false
	switch {
	case err == nil && !cur.Exited:
		return nil
	case err == nil:
		args = []string{"respawn-pane", "-t", target, "-c", r.Dir}
	case errors.Is(err, ErrUnknownSession):
		fresh = true
		args = []string{"new-session", "-d", "-s", r.Name, "-c", r.Dir, "-x", "200", "-y", "50"}
	default:
		return err
	}
	for _, kv := range r.Env {
		args = append(args, "-e", kv)
	}
	// The run line is kept in a file, as an agent's command is, and
	// written again at every start: its config may have changed.
	file, err := s.writeCommand(r.Name, r.Command)
	if err != nil {
		return err
	}
	shell := loginShell()
	argv := []string{shell, "-lc", "cd " + shellQuote(r.Dir) + " && " + sourceCommand(shell, file)}
	args = append(append(args, "--"), groups.Wrap(append(append([]string(nil), r.Wrap...), argv...))...)
	set := func(k, v string) {
		args = append(args, ";", "set-option", "-t", target, k, v)
	}
	set("@berth_location", r.Location)
	set("@berth_command", plainCommand(r.Command))
	set("@berth_command_file", file)
	set("@berth_service", r.Service)
	if fresh {
		// A title someone gave the tab since stays when it starts again.
		set("@berth_title", r.Title)
	}
	if r.Log != "" {
		args = append(args, ";", "pipe-pane", "-t", target, "cat >> "+shellQuote(r.Log))
	}
	for i, a := range args {
		if a != ";" {
			args[i] = tmuxArg(a)
		}
	}
	if out, err := s.tmux(ctx, args...); err != nil {
		if fresh {
			s.tmux(ctx, "kill-session", "-t", "="+r.Name)
			s.removeCommand(r.Name)
		}
		return tmuxError(args[0], out, err)
	}
	// What the pane says once the program ends (tmux 3.3 and later; older
	// ones say "Pane is dead").
	s.tmux(ctx, "set-option", "-w", "-t", target, "remain-on-exit-format", tmuxArg("#[fg=yellow]■#[default] "+r.Title+" stopped (#{?pane_dead_signal,signal #{pane_dead_signal},exit #{pane_dead_status}}). Start it again from Berth to run it here."))
	return nil
}

// stopService ends a terminal service's program the way Ctrl-C in its tab
// would, then more firmly if it lingers. The session stays, with what the
// program printed, for the next start.
func (s *Sessions) stopService(ctx context.Context, name string) error {
	sess, err := s.Get(ctx, name)
	if errors.Is(err, ErrUnknownSession) || errors.Is(err, errTmuxMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if sess.Exited {
		return nil
	}
	serviceStops.Store(svcKey{s, name}, true)
	pid := s.panePID(ctx, name)
	if pid <= 1 {
		return nil
	}
	steps := []struct {
		sig  syscall.Signal
		wait time.Duration
	}{{syscall.SIGINT, 5 * time.Second}, {syscall.SIGTERM, 3 * time.Second}, {syscall.SIGKILL, 2 * time.Second}}
	for i, st := range steps {
		// The pane's program leads its process group, as the terminal's
		// foreground group: Ctrl-C signals all of it.
		syscall.Kill(-pid, st.sig)
		if i > 0 {
			// Children that left the group go too.
			for _, p := range processTree(pid) {
				syscall.Kill(p, st.sig)
			}
		}
		if s.waitDead(ctx, name, st.wait) {
			return nil
		}
	}
	return fmt.Errorf("%s did not stop", name)
}

func (s *Sessions) panePID(ctx context.Context, name string) int {
	out, err := s.tmux(ctx, "display-message", "-p", "-t", "="+name+":", "#{pane_pid}")
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return pid
}

// paneDead says whether a session's program has ended; ok is false when
// there is no such session. A program a signal ended (dash, Ubuntu's sh,
// dies of the Ctrl-C it traps) has no exit status in tmux, only a signal:
// its status is then the shell's 128+signal.
func (s *Sessions) paneDead(ctx context.Context, name string) (dead bool, status string, ok bool) {
	out, err := s.tmux(ctx, "display-message", "-p", "-t", "="+name+":", "#{pane_dead}|#{pane_dead_status}|#{pane_dead_signal}")
	if err != nil {
		return false, "", false
	}
	f := strings.Split(strings.TrimSpace(string(out)), "|")
	if f[0] == "" {
		return false, "", false
	}
	if len(f) > 1 {
		status = f[1]
	}
	if len(f) > 2 && status == "" {
		if n := signalNumber(f[2]); n > 0 {
			status = strconv.Itoa(128 + n)
		}
	}
	return f[0] == "1", status, true
}

// signalNumber is a signal tmux names ("int", or a number on older tmux).
func signalNumber(sig string) int {
	if n, err := strconv.Atoi(sig); err == nil {
		return n
	}
	switch strings.ToLower(strings.TrimPrefix(strings.ToUpper(sig), "SIG")) {
	case "hup":
		return 1
	case "int":
		return 2
	case "quit":
		return 3
	case "kill":
		return 9
	case "term":
		return 15
	}
	return 0
}

func (s *Sessions) waitDead(ctx context.Context, name string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		dead, _, ok := s.paneDead(ctx, name)
		if dead || !ok {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

var (
	// serviceStops are terminal services berth is stopping, so their
	// watcher leaves the announcing to StopService.
	serviceStops sync.Map
	// serviceWatches are the terminal services being watched for their
	// program ending, by session.
	serviceWatches sync.Map
)

// svcKey is a terminal service's session in one Sessions: a box has one,
// but tests run many, one after another, with the same session names.
type svcKey struct {
	s    *Sessions
	name string
}

// startTerminalService runs a service in its session, with the worktree's
// environment. A worktree with secrets passes only their references, as a
// unit does, and the pane's program resolves them in `berthd secret exec`.
func (b *Box) startTerminalService(ctx context.Context, loc Location, wt Worktree, svc WorktreeService, env, refs map[string]string) error {
	// It may have run in the background before its config changed.
	if b.Units != nil {
		if _, err := b.Units.Remove(serviceUnit(loc.Name, wt.Name, svc.Name)); err != nil && !errors.Is(err, ErrUnknownUnit) {
			return err
		}
	}
	name := serviceSession(loc.Name, wt.Name, svc.Name)
	var wrap []string
	if len(refs) > 0 {
		var err error
		if wrap, err = b.secretWrap(env, refs); err != nil {
			return err
		}
	}
	env["BERTH_SESSION"] = name
	env["BERTH_SERVICE"] = svc.Name
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	kv := make([]string, 0, len(keys))
	for _, k := range keys {
		kv = append(kv, k+"="+env[k])
	}
	r := serviceRun{Name: name, Location: loc.Name + "/" + wt.Name, Dir: wt.Path, Service: svc.Name, Title: serviceTitle(svc), Command: svc.Run, Env: kv, Wrap: wrap}
	if wt.Main {
		r.Location = loc.Name
	}
	if b.Units != nil && b.Units.Dir != "" {
		if err := os.MkdirAll(b.Units.Dir, 0o700); err == nil {
			r.Log = b.Units.logPath(serviceUnit(loc.Name, wt.Name, svc.Name))
		}
	}
	serviceStops.Delete(svcKey{b.Sessions, name})
	if err := b.Sessions.runService(ctx, r); err != nil {
		return err
	}
	b.watchTerminalService(loc.Name, wt, svc.Name, name)
	return nil
}

// watchTerminalService announces a terminal service whose program ended by
// itself (Ctrl-C in its tab, a crash) with service.stopped, so the app
// shows it stopped at once. One watcher per session.
func (b *Box) watchTerminalService(location string, wt Worktree, service, name string) {
	if b.Sessions == nil {
		return
	}
	key := svcKey{b.Sessions, name}
	if _, busy := serviceWatches.LoadOrStore(key, true); busy {
		return
	}
	go func() {
		defer serviceWatches.Delete(key)
		ctx := context.Background()
		for {
			time.Sleep(time.Second)
			dead, status, ok := b.Sessions.paneDead(ctx, name)
			if !ok {
				return
			}
			if !dead {
				continue
			}
			if _, ours := serviceStops.LoadAndDelete(key); !ours {
				data := map[string]any{"location": location, "name": wt.Name, "path": wt.Path, "service": service, "session": name}
				if n, err := strconv.Atoi(status); err == nil {
					data["exit_status"] = n
				}
				b.Events.Publish(eventf(b, "service.stopped", data))
			}
			return
		}
	}()
}

// killServiceSession ends a service's terminal, session and all.
func (b *Box) killServiceSession(ctx context.Context, location, worktree, service string) {
	if b.Sessions == nil {
		return
	}
	name := serviceSession(location, worktree, service)
	if sess, err := b.Sessions.Get(ctx, name); err == nil && sess.Service != "" {
		serviceStops.Store(svcKey{b.Sessions, name}, true)
		b.Sessions.Kill(ctx, name)
	}
}

// terminalServiceLog is a terminal service's output: its screen and
// history while its session is there, else its log without the terminal's
// escape codes. ok is false for a service that does not run in a terminal.
func (b *Box) terminalServiceLog(ctx context.Context, location, worktree, service string, limit int) ([]byte, bool) {
	if b.Sessions != nil {
		name := serviceSession(location, worktree, service)
		if sess, err := b.Sessions.Get(ctx, name); err == nil && sess.Service != "" {
			if screen, err := b.Sessions.Screen(ctx, name, 50000); err == nil {
				out := []byte(screen)
				if len(out) > limit {
					out = out[len(out)-limit:]
				}
				return out, true
			}
		}
	}
	cfg, err := b.Locations.Config(ctx, location)
	if err != nil {
		return nil, false
	}
	for _, s := range cfg.Effective.Services {
		if s.Name == service && s.Terminal && b.Units != nil {
			out, err := b.Units.Tail(serviceUnit(location, worktree, service), int64(limit))
			if err != nil {
				return []byte{}, true
			}
			return stripANSI(out), true
		}
	}
	return nil, false
}

var ansiCodes = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b[=>78DEHMc]`)

// stripANSI takes a terminal's colours, cursor moves and titles out of what
// it printed, and its carriage returns before line ends.
func stripANSI(b []byte) []byte {
	out := ansiCodes.ReplaceAll(b, nil)
	return []byte(strings.ReplaceAll(string(out), "\r\n", "\n"))
}
