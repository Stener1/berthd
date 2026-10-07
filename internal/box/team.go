package box

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sean-brydon/berthd/internal/agentcli"
	"github.com/sean-brydon/berthd/internal/events"
	"github.com/sean-brydon/berthd/internal/groups"
	"github.com/sean-brydon/berthd/internal/statefile"
	"github.com/sean-brydon/berthd/internal/team"
)

// A team setup on a box. The laptop sends what it read of <org>/.berth
// (TeamBundle); the box keeps it under BERTH_HOME/box/team/<id>/ and runs
// it in two phases:
//
//  1. Steps. One terminal session on the box, in the home folder, as the
//     box's user, runs the team's script once per step (`<script> check
//     <step>` first, to skip what is done), then Berth's own step: the box's
//     own `gh auth login`. It is a terminal the engineer can see and type in,
//     because steps that need root call sudo, which asks them for their
//     password there; Berth never sees it. The runner script appends each
//     step's state to a progress file, which berthd follows, so progress
//     survives berthd restarting (tmux keeps the session) and a failure
//     leaves the steps before it done: Retry starts again from the step.
//  2. Projects, in berthd: clone each repository with the box's gh sign-in,
//     add it as a location, trust its own .berth/config.json if it is the
//     one reviewed, else install the team's kit for it, lay its keys into
//     its own config, and run its init script in a terminal of its own.
//
// Every change is saved in state.json and published as a team.* event.

// TeamRunner keeps and runs a box's team setups.
type TeamRunner struct {
	// Dir holds one folder per team setup.
	Dir string
	// Poll is how often a running setup is checked; tests shorten it.
	Poll time.Duration
	// GH is the gh the steps' terminal runs; empty is the one on its PATH.
	// Tests give a fake, since a login shell's PATH may put another first.
	GH string
	// Berthd is the berthd the 1Password step signs op in with; empty is
	// this one. Tests give a fake.
	Berthd string

	mu      sync.Mutex
	locks   map[string]*sync.Mutex
	running map[string]context.CancelFunc
}

var teamIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

func (t *TeamRunner) poll() time.Duration {
	if t.Poll > 0 {
		return t.Poll
	}
	return 500 * time.Millisecond
}

func (t *TeamRunner) lock(id string) func() {
	t.mu.Lock()
	if t.locks == nil {
		t.locks = map[string]*sync.Mutex{}
	}
	m, ok := t.locks[id]
	if !ok {
		m = &sync.Mutex{}
		t.locks[id] = m
	}
	t.mu.Unlock()
	m.Lock()
	return m.Unlock
}

func (t *TeamRunner) dir(id string) string { return filepath.Join(t.Dir, id) }

func (t *TeamRunner) readState(id string) (TeamStatus, error) {
	b, err := os.ReadFile(filepath.Join(t.dir(id), "state.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return TeamStatus{}, httpError{http.StatusNotFound, "no team setup " + id + " on this box"}
		}
		return TeamStatus{}, err
	}
	var st TeamStatus
	if err := json.Unmarshal(b, &st); err != nil {
		return TeamStatus{}, fmt.Errorf("team %s: state.json is unreadable: %w", id, err)
	}
	return st, nil
}

func (t *TeamRunner) writeState(st TeamStatus) error {
	st.Updated = time.Now().UTC()
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return statefile.Write(filepath.Join(t.dir(st.ID), "state.json"), append(b, '\n'))
}

func (t *TeamRunner) readBundle(id string) (TeamBundle, error) {
	var tb TeamBundle
	b, err := os.ReadFile(filepath.Join(t.dir(id), "bundle.json"))
	if err != nil {
		return tb, err
	}
	return tb, json.Unmarshal(b, &tb)
}

// teamSession is the terminal a team's steps run in.
func teamSession(id string) string { return "team-" + id }

// validateBundle checks what the laptop sent before anything is written.
func validateBundle(tb *TeamBundle) error {
	if !teamIDPattern.MatchString(tb.ID) {
		return badRequest("team id %q must be lowercase letters, digits and dashes", tb.ID)
	}
	if strings.TrimSpace(tb.Name) == "" {
		tb.Name = tb.ID
	}
	if !team.ValidOrg(tb.Org) {
		return badRequest("org %q is not a GitHub org name", tb.Org)
	}
	if len(tb.Steps) > 0 {
		c, err := team.InsidePath(tb.Script)
		if err != nil {
			return badRequest("script: %v", err)
		}
		if _, ok := tb.Files[c]; !ok {
			return badRequest("the bundle has no %s", c)
		}
		tb.Script = c
	}
	if err := team.Settings(tb.Settings).Validate(); err != nil {
		return badRequest("%v", err)
	}
	seen := map[string]bool{}
	for _, s := range tb.Steps {
		if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`).MatchString(s.ID) || s.ID == team.GitHubStep || s.ID == team.OnePasswordStep || s.ID == team.AgentsStep || seen[s.ID] {
			return badRequest("step %q is not a step name", s.ID)
		}
		seen[s.ID] = true
	}
	for p := range tb.Files {
		if _, err := team.InsidePath(p); err != nil {
			return badRequest("file %v", err)
		}
	}
	ids := map[string]bool{}
	for i := range tb.Projects {
		p := &tb.Projects[i]
		if !validFolder(p.ID) || ids[p.ID] {
			return badRequest("project %q is not a folder name, or is there twice", p.ID)
		}
		ids[p.ID] = true
		if !team.ValidRepo(p.Repo) {
			return badRequest("project %s: repo %q is not owner/name", p.ID, p.Repo)
		}
		// The box clones GitHub repositories, nothing else a bundle names.
		want := "https://github.com/" + p.Repo + ".git"
		if p.URL == "" {
			p.URL = want
		}
		if p.URL != want {
			return badRequest("project %s: clones only %s", p.ID, want)
		}
		if p.Path == "" {
			p.Path = "~/code/" + p.ID
		}
		if (!strings.HasPrefix(p.Path, "~/") && !strings.HasPrefix(p.Path, "/")) || strings.Contains(p.Path, "..") {
			return badRequest("project %s: path %q", p.ID, p.Path)
		}
		if p.Init != "" {
			c, err := team.InsidePath(p.Init)
			if err != nil {
				return badRequest("project %s: init: %v", p.ID, err)
			}
			if _, ok := tb.Files[c]; !ok {
				return badRequest("project %s: the bundle has no %s", p.ID, c)
			}
			p.Init = c
		}
		for k := range p.Env {
			if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(k) {
				return badRequest("project %s: %q is not a variable name", p.ID, k)
			}
		}
	}
	if _, err := agentcli.ParseList(strings.Join(tb.Agents, ",")); err != nil {
		return badRequest("agents: %v", err)
	}
	if tb.Start != "" && tb.Start != team.GitHubStep && tb.Start != team.OnePasswordStep && tb.Start != team.AgentsStep && !seen[tb.Start] {
		return badRequest("no step %q to start from", tb.Start)
	}
	return nil
}

// Start keeps a team setup and starts running it. A setup already running
// on this box is refused; one that finished or failed is replaced (an
// update, or a second run, skips what its checks find done).
func (b *Box) StartTeam(ctx context.Context, tb TeamBundle) (TeamStatus, error) {
	if b.Team == nil {
		return TeamStatus{}, httpError{http.StatusNotImplemented, "this box does not run team setups"}
	}
	if err := validateBundle(&tb); err != nil {
		return TeamStatus{}, err
	}
	t := b.Team
	unlock := t.lock(tb.ID)
	defer unlock()
	if prev, err := t.readState(tb.ID); err == nil && (prev.Phase == "steps" || prev.Phase == "projects") && t.isRunning(tb.ID) {
		return TeamStatus{}, httpError{http.StatusConflict, tb.Name + "'s team setup is already running on this box"}
	}
	dir := t.dir(tb.ID)
	files := filepath.Join(dir, "files")
	tmp := files + ".new"
	os.RemoveAll(tmp)
	for p, enc := range tb.Files {
		data, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return TeamStatus{}, badRequest("file %s is not base64", p)
		}
		full := filepath.Join(tmp, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return TeamStatus{}, err
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(string(data), "#!") {
			mode = 0o755
		}
		if err := os.WriteFile(full, data, mode); err != nil {
			return TeamStatus{}, err
		}
	}
	os.MkdirAll(tmp, 0o755)
	os.RemoveAll(files)
	if err := os.Rename(tmp, files); err != nil {
		return TeamStatus{}, err
	}
	raw, _ := json.Marshal(tb)
	// The bundle holds the keys the engineer typed: this user's only.
	if err := statefile.Write(filepath.Join(dir, "bundle.json"), raw); err != nil {
		return TeamStatus{}, err
	}
	os.Chmod(filepath.Join(dir, "bundle.json"), 0o600)
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(teamRunScript(tb)), 0o755); err != nil {
		return TeamStatus{}, err
	}
	st := TeamStatus{ID: tb.ID, Name: tb.Name, Org: tb.Org, Commit: tb.Commit, Box: b.Name, Phase: "steps", Started: time.Now().UTC(), KeysSet: []string{}}
	for _, s := range tb.Steps {
		st.Steps = append(st.Steps, TeamStepStatus{ID: s.ID, Title: s.Title, Sudo: s.Sudo, State: TeamTodo})
	}
	if len(tb.Agents) > 0 {
		st.Steps = append(st.Steps, TeamStepStatus{ID: team.AgentsStep, Title: agentcli.Names(tb.Agents) + " on " + b.Name, State: TeamTodo})
	}
	if tb.GitHub {
		st.Steps = append(st.Steps, TeamStepStatus{ID: team.GitHubStep, Title: "GitHub on " + b.Name, State: TeamTodo})
	}
	if tb.OnePassword {
		st.Steps = append(st.Steps, TeamStepStatus{ID: team.OnePasswordStep, Title: "1Password on " + b.Name, State: TeamTodo})
	}
	if st.Steps == nil {
		st.Steps = []TeamStepStatus{}
	}
	st.Projects = []TeamProjectStatus{}
	for _, p := range tb.Projects {
		st.Projects = append(st.Projects, TeamProjectStatus{ID: p.ID, Repo: p.Repo, State: TeamQueued})
	}
	from := 0
	if tb.Start != "" {
		for i, s := range st.Steps {
			if s.ID == tb.Start {
				from = i
			}
		}
		for i := 0; i < from; i++ {
			st.Steps[i].State = TeamSkipped
		}
	}
	st.KeysSet = b.teamKeysSet(ctx, tb)
	if err := t.writeState(st); err != nil {
		return TeamStatus{}, err
	}
	b.publishTeam("team.started", map[string]any{"team": tb.ID, "name": tb.Name, "commit": tb.Commit})
	return b.runSteps(st, from)
}

// runSteps starts the steps from index from in the team's terminal, or
// goes on to the projects when there are none left. Called with the
// team's lock held.
func (b *Box) runSteps(st TeamStatus, from int) (TeamStatus, error) {
	t := b.Team
	ids := []string{}
	for _, s := range st.Steps[from:] {
		ids = append(ids, s.ID)
	}
	if len(ids) == 0 {
		st.Phase = "projects"
		if err := t.writeState(st); err != nil {
			return st, err
		}
		b.goProjects(st.ID)
		return st, nil
	}
	ctx := context.Background()
	name := teamSession(st.ID)
	if s, err := b.Sessions.Get(ctx, name); err == nil {
		if !s.Exited {
			return st, httpError{http.StatusConflict, "the team setup's terminal is still running on this box"}
		}
		b.Sessions.Kill(ctx, name)
	}
	dir := t.dir(st.ID)
	os.Remove(filepath.Join(dir, "progress"))
	home, err := os.UserHomeDir()
	if err != nil {
		return st, err
	}
	command := "exec sh " + shellQuote(filepath.Join(dir, "run.sh")) + " " + strings.Join(ids, " ")
	env := []string{"BERTH_TEAM=" + st.ID}
	if t.GH != "" {
		env = append(env, "BERTH_TEAM_GH="+t.GH)
	}
	// The 1Password step signs op in with this berthd's own command.
	if t.Berthd != "" {
		env = append(env, "BERTH_TEAM_BERTHD="+t.Berthd)
	} else if self, err := b.self(); err == nil {
		env = append(env, "BERTH_TEAM_BERTHD="+self)
	}
	if tb, err := t.readBundle(st.ID); err == nil {
		env = append(env, team.Settings(tb.Settings).Env()...)
		if len(tb.Agents) > 0 {
			env = append(env, "BERTH_TEAM_AGENTS="+strings.Join(tb.Agents, " "))
		}
	}
	sess, err := b.Sessions.Create(ctx, name, "", home, command, env)
	if errors.Is(err, errTmuxMissing) {
		return st, fmt.Errorf("%w, and Berth runs the team setup's steps in a terminal there (tmux): install it with `%s`, then set up again", errTmuxMissing, tmuxInstallHint())
	}
	if err != nil {
		return st, err
	}
	b.Sessions.SetTitle(ctx, sess.Name, st.Name+" team setup")
	st.Session = sess.Name
	st.Phase = "steps"
	st.Error = ""
	if err := t.writeState(st); err != nil {
		return st, err
	}
	b.followSteps(st.ID)
	return st, nil
}

// teamRunScript is the script the team's terminal runs: each step through
// the team's script, its state appended to the progress file berthd
// follows, then the box's own GitHub sign-in and, when the keys need it,
// 1Password's. A step that adds the user to a group (docker) leaves this
// terminal without it, as any running process is; the steps left then run
// through sg, which gives a group the user is in without a password, as a
// new login would.
func teamRunScript(tb TeamBundle) string {
	short := tb.Commit
	if len(short) > 7 {
		short = short[:7]
	}
	return fmt.Sprintf(`#!/bin/sh
# Berth runs %[1]s's team setup here (%[2]s/.berth at %[3]s), as you, in
# this terminal. Steps that need root ask for your password with sudo: type
# it here. Berth never sees it or keeps it.
T=$(cd "$(dirname "$0")" && pwd)
P="$T/progress"
SCRIPT=%[4]s
GH=${BERTH_TEAM_GH:-gh}
BERTHD=${BERTH_TEAM_BERTHD:-berthd}
mark() { printf '%%s %%s %%s %%s\n' "$1" "$2" "$(date +%%s)" "${3:-}" >>"$P"; }
stop() {
  printf '\n\033[31mBerth stopped at %%s (exit %%s).\033[0m Fix it, then press Retry from it in Berth; the steps before it are kept.\n' "$1" "$2"
  mark - end fail
  exit "$2"
}
q() { printf "'%%s'" "$(printf '%%s' "$1" | sed "s/'/'\\\\''/g")"; }
# newgroups: groups the group database gives you that this terminal lacks,
# since a step added you to them after it started.
newgroups() {
  have=" $(id -nG) "
  for g in $(id -nG "$(id -un)" 2>/dev/null); do
    case "$have" in *" $g "*) continue ;; esac
    case " ${BERTH_TEAM_REGROUPED:-} " in *" $g "*) continue ;; esac
    printf '%%s ' "$g"
  done
}
# regroup carries on with the steps left (its arguments) in a shell that
# has those groups: sg gives each, then gives back your own group.
regroup() {
  command -v sg >/dev/null 2>&1 || return 0
  new=$(newgroups)
  [ -n "$new" ] || return 0
  printf '    \033[2mYou are in %%s now: the steps left run with it, as a new login would.\033[0m\n' "${new%% }"
  cmd="exec sh $(q "$T/run.sh")"
  for a in "$@"; do cmd="$cmd $(q "$a")"; done
  cmd="exec sg $(q "$(id -gn)") -c $(q "$cmd")"
  for g in $new; do cmd="exec sg $(q "$g") -c $(q "$cmd")"; done
  BERTH_TEAM_REGROUPED="${BERTH_TEAM_REGROUPED:-}$new"
  export BERTH_TEAM_REGROUPED
  eval "$cmd"
}
if [ "$(id -u)" -eq 0 ]; then
  echo "Berth runs team setups as you, not as root; steps that need root ask for your password with sudo."
  mark - end fail
  exit 1
fi
cd "$T/files" || exit 1
[ -n "${BERTH_TEAM_REGROUPED:-}" ] || printf '\033[1mBerth: %[1]s team setup\033[0m (%[2]s/.berth at %[3]s)\n'
while [ $# -gt 0 ]; do
  s=$1
  shift
  if [ "$s" = github ]; then
    if command -v "$GH" >/dev/null 2>&1 && "$GH" auth status --hostname github.com >/dev/null 2>&1; then
      "$GH" auth setup-git --hostname github.com >/dev/null 2>&1
      mark github skipped
      printf '    GitHub: this box is already signed in\n'
      continue
    fi
    mark github running
    printf '\n\033[1m==> GitHub on this box\033[0m\n'
    printf '    This box signs in to GitHub with its own gh, so it clones as you,\n'
    printf '    and you can revoke it on its own. Press Enter, then open\n'
    printf '    https://github.com/login/device on your laptop and enter the code.\n\n'
    if ! command -v "$GH" >/dev/null 2>&1; then
      echo "gh is not installed on this box: install the GitHub CLI (https://cli.github.com), then Retry."
      mark github failed 127
      stop github 127
    fi
    if "$GH" auth login --hostname github.com --git-protocol https --web && "$GH" auth setup-git --hostname github.com; then
      mark github done
    else
      c=$?
      mark github failed "$c"
      stop github "$c"
    fi
    continue
  fi
  if [ "$s" = agents ]; then
    mark agents running
    printf '\n\033[1m==> Agent CLIs on this box\033[0m\n'
    printf '    Into ~/.local/bin, as you, without sudo. Signing in stays yours:\n'
    printf '    each agent asks the first time it starts.\n\n'
    # shellcheck disable=SC2086 # a list of agent ids
    if "$BERTHD" agents install --integrations ${BERTH_TEAM_AGENTS:-}; then
      mark agents done
    else
      c=$?
      mark agents failed "$c"
      stop agents "$c"
    fi
    continue
  fi
  if [ "$s" = 1password ]; then
    if "$BERTHD" secret signin --check >/dev/null 2>&1; then
      mark 1password skipped
      printf '    1Password: this box is already signed in\n'
      continue
    fi
    mark 1password running
    printf '\n\033[1m==> 1Password on this box\033[0m\n'
    printf "    The team's shared keys are 1Password references (op://...). Sign op in\n"
    printf '    here, once, so Berth can read them when a worktree needs them.\n\n'
    if "$BERTHD" secret signin; then
      mark 1password done
    else
      c=$?
      mark 1password failed "$c"
      stop 1password "$c"
    fi
    continue
  fi
  if "./$SCRIPT" check "$s" >/dev/null 2>&1; then
    mark "$s" skipped
    printf '    %%s (already done)\n' "$s"
    continue
  fi
  mark "$s" running
  if "./$SCRIPT" "$s"; then
    mark "$s" done
    regroup "$@"
  else
    c=$?
    mark "$s" failed "$c"
    stop "$s" "$c"
  fi
done
mark - end ok
printf '\n\033[32mBerth: the box is set up.\033[0m The repos are next, in Berth; you can close this tab.\n'
`, tb.Name, tb.Org, short, shellQuote(tb.Script))
}

func (t *TeamRunner) isRunning(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.running[id]
	return ok
}

func (t *TeamRunner) setRunning(id string, cancel context.CancelFunc) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running == nil {
		t.running = map[string]context.CancelFunc{}
	}
	if _, ok := t.running[id]; ok {
		return false
	}
	t.running[id] = cancel
	return true
}

func (t *TeamRunner) doneRunning(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.running, id)
}

// Stop ends every watcher (berthd stopping); the terminals run on.
func (t *TeamRunner) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range t.running {
		c()
	}
}

var (
	// A step waits for its person at a password prompt, a yes/no question
	// (gh asks one before its device code, op "[Y/n]" before adding an
	// account), gh's "Press Enter to open", or op's sign-in questions
	// ("Enter your sign-in address (example.1password.com):", "Enter the
	// Secret Key for …:", "Enter the password for … at …:").
	sudoPrompt  = regexp.MustCompile(`(?i)(\[sudo\] password for [^:]*:|^password:|\(y/n\)|\[y/n\]|press enter to open.*|^enter (your|the) [^\n]*:)\s*$`)
	deviceCode  = regexp.MustCompile(`one-time code: ([A-Z0-9]{4}-[A-Z0-9]{4})`)
	deviceURL   = "https://github.com/login/device"
	progressRow = regexp.MustCompile(`^(\S+) (\S+) (\d+) ?(\S*)$`)
)

// followSteps follows a team's terminal until its steps end.
func (b *Box) followSteps(id string) {
	t := b.Team
	ctx, cancel := context.WithCancel(context.Background())
	if !t.setRunning(id, cancel) {
		cancel()
		return
	}
	go func() {
		defer t.doneRunning(id)
		defer cancel()
		tick := time.NewTicker(t.poll())
		defer tick.Stop()
		for {
			if b.stepTick(id) {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}

// stepTick reads the progress file and the terminal once; it reports
// whether the steps have ended.
func (b *Box) stepTick(id string) bool {
	t := b.Team
	unlock := t.lock(id)
	defer unlock()
	st, err := t.readState(id)
	if err != nil || st.Phase != "steps" {
		return true
	}
	changed := false
	ended, ok := "", true
	// Whether the terminal has ended is read before the progress file: the
	// runner writes its last line before it exits, so an ended terminal
	// with no last line in the file was cut short.
	sess, serr := b.Sessions.Get(context.Background(), teamSession(id))
	dead := serr != nil || sess.Exited
	if f, err := os.Open(filepath.Join(t.dir(id), "progress")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			m := progressRow.FindStringSubmatch(strings.TrimSpace(sc.Text()))
			if m == nil {
				continue
			}
			if m[1] == "-" {
				ended, ok = m[2], m[4] == "ok"
				continue
			}
			for i := range st.Steps {
				s := &st.Steps[i]
				if s.ID != m[1] {
					continue
				}
				state := m[2]
				if state == TeamRunning && s.State == TeamWaiting {
					state = TeamWaiting
				}
				if s.State == state {
					continue
				}
				at := time.Now().UTC()
				if sec, err := strconv.ParseInt(m[3], 10, 64); err == nil {
					at = time.Unix(sec, 0).UTC()
				}
				switch state {
				case TeamRunning:
					s.Started, s.Error, s.Code, s.URL, s.Secs = &at, "", "", "", 0
				case TeamDone, TeamFailed:
					if s.Started != nil {
						s.Secs = int(at.Sub(*s.Started).Round(time.Second).Seconds())
					}
					s.Code, s.URL = "", ""
					if state == TeamFailed {
						s.Error = "exited with " + m[4]
					}
				}
				s.State = state
				changed = true
				b.publishTeam("team.step", map[string]any{"team": id, "step": s.ID, "state": s.State, "secs": s.Secs})
			}
		}
		f.Close()
	}
	// A running step waiting at a password prompt, or for GitHub's
	// device code to be entered, says so.
	for i := range st.Steps {
		s := &st.Steps[i]
		if s.State != TeamRunning && s.State != TeamWaiting {
			continue
		}
		screen, err := b.Sessions.Screen(context.Background(), teamSession(id), 40)
		if err != nil {
			break
		}
		state := TeamRunning
		if sudoPrompt.MatchString(lastLine(screen)) {
			state = TeamWaiting
		}
		code := ""
		if s.ID == team.GitHubStep {
			if m := deviceCode.FindAllStringSubmatch(screen, -1); m != nil && !strings.Contains(screen[strings.LastIndex(screen, m[len(m)-1][0]):], "Logged in") {
				code = m[len(m)-1][1]
				state = TeamWaiting
			}
		}
		if state != s.State || code != s.Code {
			s.State, s.Code = state, code
			if code != "" {
				s.URL = deviceURL
			} else {
				s.URL = ""
			}
			changed = true
			b.publishTeam("team.step", map[string]any{"team": id, "step": s.ID, "state": s.State, "code": s.Code})
		}
		break
	}
	done := false
	switch {
	case ended != "" && ok:
		st.Phase = "projects"
		changed, done = true, true
		// The steps may have installed tools (node, yarn) and added the
		// user to groups (docker): look again, rather than trust what was
		// found before they ran.
		groups.Forget()
		forgetLoginTools()
	case ended != "":
		st.Phase = "failed"
		st.Error = "stopped at " + failedStep(st)
		changed, done = true, true
	default:
		// The terminal is gone, or its program ended, without the
		// runner's last line: it was killed, or the box restarted.
		if dead {
			for i := range st.Steps {
				if s := &st.Steps[i]; s.State == TeamRunning || s.State == TeamWaiting {
					s.State, s.Error, s.Code, s.URL = TeamFailed, "interrupted: its terminal ended before it did", "", ""
				}
			}
			st.Phase, st.Error = "failed", "the team setup's terminal ended before its steps did"
			changed, done = true, true
		}
	}
	if changed {
		t.writeState(st)
	}
	if done {
		if st.Phase == "projects" {
			b.goProjects(id)
		} else {
			b.publishTeam("team.failed", map[string]any{"team": id, "step": failedStep(st), "error": st.Error})
		}
	}
	return done
}

func failedStep(st TeamStatus) string {
	for _, s := range st.Steps {
		if s.State == TeamFailed {
			return s.ID
		}
	}
	return ""
}

func lastLine(screen string) string {
	lines := strings.Split(strings.TrimRight(screen, "\n "), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// goProjects starts the projects phase in the background.
func (b *Box) goProjects(id string) {
	t := b.Team
	ctx, cancel := context.WithCancel(context.Background())
	key := id + "/projects"
	if !t.setRunning(key, cancel) {
		cancel()
		return
	}
	go func() {
		defer t.doneRunning(key)
		defer cancel()
		b.runProjects(ctx, id)
	}()
}

// updateProject changes one project's status under the team's lock.
func (b *Box) updateProject(id, project string, change func(*TeamProjectStatus)) {
	t := b.Team
	unlock := t.lock(id)
	defer unlock()
	st, err := t.readState(id)
	if err != nil {
		return
	}
	for i := range st.Projects {
		if st.Projects[i].ID == project {
			before := st.Projects[i].State
			change(&st.Projects[i])
			t.writeState(st)
			if p := st.Projects[i]; p.State != before {
				data := map[string]any{"team": id, "project": p.ID, "state": p.State}
				if p.Location != "" {
					data["location"] = p.Location
				}
				if p.Error != "" {
					data["error"] = p.Error
				}
				b.publishTeam("team.project", data)
			}
			return
		}
	}
}

func (b *Box) runProjects(ctx context.Context, id string) {
	t := b.Team
	tb, err := t.readBundle(id)
	if err != nil {
		return
	}
	st, err := t.readState(id)
	if err != nil {
		return
	}
	// Tools a kit asked for may be there now (the steps install them): a
	// project set up before keeps only the warnings still true.
	for _, p := range st.Projects {
		if len(p.Warnings) > 0 {
			b.updateProject(id, p.ID, func(s *TeamProjectStatus) { s.Warnings = recheckToolWarnings(s.Warnings) })
		}
	}
	for _, p := range tb.Projects {
		var cur TeamProjectStatus
		for _, s := range st.Projects {
			if s.ID == p.ID {
				cur = s
			}
		}
		if cur.State == TeamReady || cur.State == TeamSkipped {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if err := b.setUpProject(ctx, tb, p); err != nil {
			b.updateProject(id, p.ID, func(s *TeamProjectStatus) { s.State, s.Error, s.Line = TeamFailed, err.Error(), "" })
		}
	}
	unlock := t.lock(id)
	st, err = t.readState(id)
	if err != nil {
		unlock()
		return
	}
	st.Phase, st.Error = "done", ""
	failed := []string{}
	for _, p := range st.Projects {
		if p.State == TeamFailed {
			failed = append(failed, p.ID)
		}
	}
	if len(failed) > 0 {
		st.Phase, st.Error = "failed", strings.Join(failed, ", ")+" did not set up"
	}
	st.KeysSet = b.teamKeysSet(ctx, tb)
	t.writeState(st)
	unlock()
	if st.Phase == "done" {
		b.publishTeam("team.done", map[string]any{"team": id, "name": st.Name})
	} else {
		b.publishTeam("team.failed", map[string]any{"team": id, "project": failed[0], "error": st.Error})
	}
}

// setUpProject clones one repository (or finds the clone already there),
// adds it, and applies its setup.
func (b *Box) setUpProject(ctx context.Context, tb TeamBundle, p TeamProjectPlan) error {
	id := tb.ID
	dest, err := filepath.Abs(expandHome(p.Path))
	if err != nil {
		return err
	}
	b.updateProject(id, p.ID, func(s *TeamProjectStatus) { s.State, s.Error, s.Warnings, s.Line = TeamCloning, "", nil, "" })
	if _, err := os.Stat(dest); err == nil {
		// origin as written (get-url would apply url.*.insteadOf).
		raw, _ := git(ctx, "-C", dest, "config", "--get", "remote.origin.url")
		if got := slugOf(strings.TrimSpace(string(raw))); !strings.EqualFold(got, p.Repo) {
			return fmt.Errorf("%s is already there and is not a clone of %s", p.Path, p.Repo)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		err := gitClone(ctx, p.URL, dest, func(line string) {
			b.updateProject(id, p.ID, func(s *TeamProjectStatus) { s.Line = line })
		})
		if err != nil {
			return err
		}
	}
	name := ""
	all, _ := b.Locations.List(ctx)
	for _, l := range all {
		if samePath(l.Path, dest) {
			name = l.Name
		}
	}
	if name == "" {
		// The location's name is part of every worktree's URL
		// (<worktree>.<project>.<box>.localhost), so it is one URL label
		// even when an older laptop sent a project id with a dot.
		base := team.URLSafeName(p.ID)
		if base == "" {
			base = "project"
		}
		name = b.freeLocationName(ctx, base)
		if _, err := b.Locations.Add(ctx, name, dest); err != nil {
			return err
		}
		b.Events.Publish(events.Event{Type: "location.added", Box: b.Name, Data: map[string]any{"location": name, "path": dest, "url": p.URL, "team": id}})
	}
	b.updateProject(id, p.ID, func(s *TeamProjectStatus) { s.State, s.Location, s.Line = TeamSettingUp, name, "" })
	var warnings []string
	trust := RepoTrustNone
	// The repository's own config, trusted as the engineer reviewed it.
	if _, hash, ok, _ := readRepoFile(dest); ok {
		trust = RepoTrustUntrusted
		if p.TrustHash != "" && p.TrustHash == hash {
			if err := b.Locations.TrustRepo(name, hash); err != nil {
				return err
			}
			trust = RepoTrustTrusted
			b.Events.Publish(events.Event{Type: "config.changed", Box: b.Name, Data: map[string]any{"location": name, "trust": RepoTrustTrusted, "hash": hash, "team": id}})
		} else if p.TrustHash != "" {
			warnings = append(warnings, "its .berth/config.json changed since you reviewed it, so it waits for you to trust it in Project settings")
		} else {
			warnings = append(warnings, "it has a .berth/config.json of its own, which waits for you to trust it in Project settings")
		}
	}
	if p.Kit != nil && trust != RepoTrustTrusted {
		res, err := b.InstallKit(ctx, name, *p.Kit)
		if err != nil {
			return fmt.Errorf("kit %s: %w", p.Kit.Kit.ID, err)
		}
		warnings = append(warnings, res.Warnings...)
		b.Events.Publish(events.Event{Type: "kit.installed", Box: b.Name, Data: map[string]any{"location": name, "kit": res.Kit.ID, "source": res.Kit.Source, "team": id}})
	}
	if len(p.Env) > 0 {
		if err := b.mergeLocalEnv(name, p.Env); err != nil {
			return err
		}
	}
	b.updateProject(id, p.ID, func(s *TeamProjectStatus) { s.Trust, s.Warnings = trust, warnings })
	if p.Init != "" {
		if err := b.runInit(ctx, tb, p, name, dest); err != nil {
			return err
		}
	}
	b.updateProject(id, p.ID, func(s *TeamProjectStatus) { s.State, s.Line = TeamReady, "" })
	return nil
}

// gitClone clones url into dest, passing git's progress on; a failed clone
// leaves nothing behind.
func gitClone(ctx context.Context, url, dest string, line func(string)) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "clone", "--progress", "--", url, dest)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL="+GitProtocols)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stderr)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		for i, c := range data {
			if c == '\n' || c == '\r' {
				return i + 1, data[:i], nil
			}
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	var last string
	lastSent := time.Time{}
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			last = l
			if time.Since(lastSent) > time.Second {
				line(l)
				lastSent = time.Now()
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		os.RemoveAll(dest)
		msg := strings.TrimPrefix(last, "fatal: ")
		if strings.Contains(msg, "could not read Username") || strings.Contains(msg, "Authentication failed") || strings.Contains(msg, "not found") {
			msg += " (is this box's gh signed in to an account that can read it? Retry the GitHub step)"
		}
		return errors.New("git clone: " + msg)
	}
	return nil
}

// mergeLocalEnv lays env into the location's own config on this box: the
// keys stay here, and the repository's and kit's layers stay as they are.
func (b *Box) mergeLocalEnv(name string, env map[string]string) error {
	saved, err := b.Locations.saved(name)
	if err != nil {
		return err
	}
	c := RepoConfig{Setup: saved.Setup, Archive: saved.Archive}
	if saved.Config != nil {
		c = *saved.Config
	}
	if c.Env == nil {
		c.Env = map[string]string{}
	}
	for k, v := range env {
		if v == "" {
			continue
		}
		c.Env[k] = v
	}
	return b.Locations.SetLocalConfig(name, c)
}

// teamKeysSet lists "project/KEY" for a bundle's keys its projects'
// configs on this box already have.
func (b *Box) teamKeysSet(ctx context.Context, tb TeamBundle) []string {
	out := []string{}
	all, _ := b.Locations.read()
	for _, p := range tb.Projects {
		dest, _ := filepath.Abs(expandHome(p.Path))
		for _, s := range all {
			if !samePath(s.Path, dest) || s.Config == nil {
				continue
			}
			for k, v := range s.Config.Env {
				if v != "" {
					out = append(out, p.ID+"/"+k)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// runInit runs a project's init script once, in a terminal of its own
// that the app can show, and waits for it.
func (b *Box) runInit(ctx context.Context, tb TeamBundle, p TeamProjectPlan, location, dest string) error {
	t := b.Team
	status := filepath.Join(t.dir(tb.ID), "init-"+p.ID+".status")
	name := sessionSafe("team-" + tb.ID + "-" + p.ID)
	// An init still running from before berthd restarted is waited for,
	// not started twice.
	if s, err := b.Sessions.Get(ctx, name); err != nil || s.Exited {
		if err == nil {
			b.Sessions.Kill(ctx, name)
		}
		os.Remove(status)
		script := filepath.Join(t.dir(tb.ID), "files", filepath.FromSlash(p.Init))
		command := fmt.Sprintf("cd %s && %s; c=$?; echo $c > %s; [ $c = 0 ] && echo && echo 'Berth: %s is set up.'", shellQuote(dest), shellQuote(script), shellQuote(status), p.ID)
		env := initEnv(ctx, b, tb, location, dest, filepath.Join(t.dir(tb.ID), "files"))
		if _, err := b.Sessions.Create(ctx, name, location, dest, command, env); err != nil {
			return fmt.Errorf("init: %w", err)
		}
		b.Sessions.SetTitle(ctx, name, p.ID+" first-time setup")
	}
	for {
		if raw, err := os.ReadFile(status); err == nil {
			code := strings.TrimSpace(string(raw))
			if code == "0" {
				return nil
			}
			return fmt.Errorf("its init script (%s) exited with %s; its terminal on the box has the output", p.Init, code)
		}
		if s, err := b.Sessions.Get(ctx, name); err != nil || s.Exited {
			if _, err := os.Stat(status); err == nil {
				continue
			}
			return fmt.Errorf("its init script (%s) ended without finishing", p.Init)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(t.poll()):
		}
	}
}

// initEnv is what a project's init gets, as a kit's scripts get it: the
// box and the project (BERTH_BOX, BERTH_LOCATION, BERTH_ROOT_PATH and
// BERTH_LOCATION_PATH), the project's kit when it has one (BERTH_KIT_DIR),
// the team setup's files (BERTH_TEAM_DIR) and its box.settings.
func initEnv(ctx context.Context, b *Box, tb TeamBundle, location, dest, files string) []string {
	// The location's path, as kit scripts get it (symlinks resolved).
	if loc, err := b.Locations.Get(ctx, location); err == nil && loc.Path != "" {
		dest = loc.Path
	}
	env := []string{"BERTH_BOX=" + b.Name, "BERTH_LOCATION=" + location, "BERTH_ROOT_PATH=" + dest, "BERTH_LOCATION_PATH=" + dest,
		"BERTH_TEAM=" + tb.ID, "BERTH_TEAM_DIR=" + files}
	if cfg, err := b.Locations.Config(ctx, location); err == nil && cfg.Kit != nil && cfg.Kit.Dir != "" {
		env = append(env, "BERTH_KIT_DIR="+cfg.Kit.Dir)
	}
	return append(env, team.Settings(tb.Settings).Env()...)
}

var unsafeSession = regexp.MustCompile(`[^A-Za-z0-9_-]`)

func sessionSafe(s string) string {
	s = unsafeSession.ReplaceAllString(s, "-")
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

// RetryTeam starts a failed (or finished) team setup again from a step or a
// project.
func (b *Box) RetryTeam(id, from string) (TeamStatus, error) {
	if b.Team == nil {
		return TeamStatus{}, httpError{http.StatusNotImplemented, "this box does not run team setups"}
	}
	t := b.Team
	unlock := t.lock(id)
	defer unlock()
	st, err := t.readState(id)
	if err != nil {
		return TeamStatus{}, err
	}
	if st.Phase == "steps" && t.isRunning(id) || st.Phase == "projects" && t.isRunning(id+"/projects") {
		return st, httpError{http.StatusConflict, "it is still running"}
	}
	if from == "" {
		from = failedStep(st)
		if from == "" {
			for _, p := range st.Projects {
				if p.State == TeamFailed {
					from = p.ID
					break
				}
			}
		}
	}
	for i, s := range st.Steps {
		if s.ID != from {
			continue
		}
		for j := i; j < len(st.Steps); j++ {
			st.Steps[j].State, st.Steps[j].Error, st.Steps[j].Secs, st.Steps[j].Code, st.Steps[j].URL, st.Steps[j].Started = TeamTodo, "", 0, "", "", nil
		}
		for j := range st.Projects {
			if st.Projects[j].State != TeamReady {
				st.Projects[j].State, st.Projects[j].Error = TeamQueued, ""
			}
		}
		b.publishTeam("team.retry", map[string]any{"team": id, "from": from})
		return b.runSteps(st, i)
	}
	for i, p := range st.Projects {
		if p.ID != from {
			continue
		}
		for j := i; j < len(st.Projects); j++ {
			if st.Projects[j].State != TeamReady {
				st.Projects[j].State, st.Projects[j].Error, st.Projects[j].Line = TeamQueued, "", ""
			}
		}
		st.Projects[i].State = TeamQueued
		st.Phase, st.Error = "projects", ""
		if err := t.writeState(st); err != nil {
			return st, err
		}
		b.publishTeam("team.retry", map[string]any{"team": id, "from": from})
		b.goProjects(id)
		return st, nil
	}
	if from == "" {
		return st, badRequest("nothing failed to retry")
	}
	return st, badRequest("no step or project %q in %s's team setup", from, st.Name)
}

// ResumeTeams picks up team setups that were running when berthd stopped:
// steps whose terminal still runs are followed again (or settled from the
// progress file if it ended meanwhile), and projects carry on.
func (b *Box) ResumeTeams() {
	if b.Team == nil {
		return
	}
	dirs, _ := filepath.Glob(filepath.Join(b.Team.Dir, "*", "state.json"))
	for _, p := range dirs {
		id := filepath.Base(filepath.Dir(p))
		st, err := b.Team.readState(id)
		if err != nil {
			continue
		}
		switch st.Phase {
		case "steps":
			b.followSteps(id)
		case "projects":
			// What was half-done when berthd stopped starts again.
			unlock := b.Team.lock(id)
			if st, err := b.Team.readState(id); err == nil {
				for i := range st.Projects {
					if s := &st.Projects[i]; s.State == TeamCloning {
						s.State = TeamQueued
					}
				}
				b.Team.writeState(st)
			}
			unlock()
			b.goProjects(id)
		}
	}
}

// Teams lists the team setups on this box.
func (b *Box) Teams(ctx context.Context) []TeamStatus {
	out := []TeamStatus{}
	if b.Team == nil {
		return out
	}
	dirs, _ := filepath.Glob(filepath.Join(b.Team.Dir, "*", "state.json"))
	for _, p := range dirs {
		if st, err := b.Team.readState(filepath.Base(filepath.Dir(p))); err == nil {
			out = append(out, st)
		}
	}
	return out
}

func (b *Box) publishTeam(typ string, data map[string]any) {
	if b.Events != nil {
		b.Events.Publish(events.Event{Type: typ, Box: b.Name, Data: data})
	}
}

func (b *Box) postTeam(w http.ResponseWriter, r *http.Request) error {
	var tb TeamBundle
	if err := decodeLimit(r, &tb, 16<<20); err != nil {
		return err
	}
	if err := b.before(r, "team.setup", map[string]any{"team": tb.ID, "org": tb.Org, "commit": tb.Commit}); err != nil {
		return err
	}
	st, err := b.StartTeam(r.Context(), tb)
	if err != nil {
		return err
	}
	writeJSON(w, st)
	return nil
}

func (b *Box) listTeams(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, b.Teams(r.Context()))
	return nil
}

func (b *Box) getTeam(w http.ResponseWriter, r *http.Request) error {
	if b.Team == nil {
		return httpError{http.StatusNotFound, "this box does not run team setups"}
	}
	st, err := b.Team.readState(r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, st)
	return nil
}

func (b *Box) retryTeam(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		From string `json:"from"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	id := r.PathValue("id")
	if !teamIDPattern.MatchString(id) {
		return badRequest("no team setup %q", id)
	}
	if err := b.before(r, "team.retry", map[string]any{"team": id, "from": req.From}); err != nil {
		return err
	}
	st, err := b.RetryTeam(id, req.From)
	if err != nil {
		return err
	}
	writeJSON(w, st)
	return nil
}
