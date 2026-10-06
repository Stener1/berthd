package box

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sean-brydon/berthd/internal/events"
	"github.com/sean-brydon/berthd/internal/team"
	"github.com/sean-brydon/berthd/internal/team/teamtest"
)

// The team's box script, as Cal.com's is written: one subcommand per
// step, and check <step>. Steps leave marks in $MARKS, and a step fails
// while a file named fail-<step> is there.
const testSetupSh = `#!/bin/sh
set -eu
M="$MARKS"
case "$1" in
  check) [ -f "$M/done-$2" ] ;;
  plan) echo tools sudo; echo db ;;
  tools)
    printf '[sudo] password for dev: '
    read -r pw
    [ "$pw" = hunter2 ] || exit 1
    echo ran >> "$M/runs-tools"; touch "$M/done-tools" ;;
  db)
    echo ran >> "$M/runs-db"
    [ ! -f "$M/fail-db" ] || { echo "port 5450 is taken" >&2; exit 3; }
    touch "$M/done-db" ;;
  slow)
    while [ ! -f "$M/go" ]; do sleep 0.1; done
    touch "$M/done-slow" ;;
esac
`

type teamFixture struct {
	t     *testing.T
	b     *Box
	gh    *teamtest.GitHub
	marks string
	home  string
	bus   *events.Bus
}

func newTeamFixture(t *testing.T) *teamFixture {
	t.Helper()
	sessions := testSessions(t)
	gh := teamtest.New(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	marks := t.TempDir()
	t.Setenv("MARKS", marks)
	// Clones of https://github.com/… come from the fake GitHub's folders.
	gitconfig := filepath.Join(t.TempDir(), "gitconfig")
	os.WriteFile(gitconfig, []byte("[url \"file://"+gh.Root+"/\"]\n\tinsteadOf = https://github.com/\n"), 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	old := GitProtocols
	GitProtocols += ":file"
	t.Cleanup(func() { GitProtocols = old })
	state := t.TempDir()
	bus := &events.Bus{}
	b := &Box{
		Name:      "devbox",
		Locations: NewLocations(filepath.Join(state, "locations.json")),
		Sessions:  sessions,
		Events:    bus,
		KitsDir:   filepath.Join(state, "kits"),
		Team:      &TeamRunner{Dir: filepath.Join(state, "team"), Poll: 30 * time.Millisecond, GH: filepath.Join(gh.Bin, "gh")},
	}
	t.Cleanup(b.Team.Stop)
	return &teamFixture{t: t, b: b, gh: gh, marks: marks, home: home, bus: bus}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

const teamRepoConfig = `{"setup":"true","env":{"FROM_REPO":"1"}}`

// bundle is a team setup with two steps, the box's GitHub sign-in, and
// two repositories: web with its own config, api with a kit and an init.
func (f *teamFixture) bundle() TeamBundle {
	f.gh.Repo("acme/web", map[string]string{".berth/config.json": teamRepoConfig, "README.md": "web"}, teamtest.Repo{})
	f.gh.Repo("acme/api", map[string]string{"main.go": "package main"}, teamtest.Repo{})
	return TeamBundle{
		ID: "acme", Name: "Acme", Org: "acme", Commit: "4e1c9a2aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Script: "box/setup.sh",
		Steps:  []team.Step{{ID: "tools", Title: "Tools", Sudo: true}, {ID: "db", Title: "Database"}},
		GitHub: true,
		Files: map[string]string{
			"team.json":       b64("{}"),
			"box/setup.sh":    b64(testSetupSh),
			"box/init-api.sh": b64("#!/bin/sh\necho init > \"$MARKS/init-api\"\npwd >> \"$MARKS/init-api\"\n"),
		},
		Projects: []TeamProjectPlan{
			{ID: "web", Repo: "acme/web", Source: "repo", TrustHash: sha(teamRepoConfig), Env: map[string]string{"STRIPE_KEY": "op://Dev/Stripe/key", "MAIL_KEY": "SG.mine"}},
			{ID: "api", Repo: "acme/api", Path: "~/src/api", Source: "kit", Init: "box/init-api.sh",
				Kit: &KitInstall{Kit: Kit{ID: "acme-api", Name: "Acme API", Config: RepoConfig{Ports: 2}}, Hash: "abc123"}},
		},
	}
}

func (f *teamFixture) status() TeamStatus {
	st, err := f.b.Team.readState("acme")
	if err != nil {
		f.t.Fatal(err)
	}
	return st
}

func (f *teamFixture) step(id string) TeamStepStatus {
	for _, s := range f.status().Steps {
		if s.ID == id {
			return s
		}
	}
	f.t.Fatalf("no step %s", id)
	return TeamStepStatus{}
}

func (f *teamFixture) waitFor(what string, cond func(TeamStatus) bool) TeamStatus {
	f.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		st := f.status()
		if cond(st) {
			return st
		}
		if time.Now().After(deadline) {
			screen, _ := f.b.Sessions.Screen(context.Background(), "team-acme", 50)
			f.t.Fatalf("timed out waiting for %s: %+v\nterminal:\n%s", what, st, screen)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func (f *teamFixture) typeIn(text string) {
	f.t.Helper()
	if err := f.b.Sessions.Send(context.Background(), "team-acme", text, true); err != nil {
		f.t.Fatal(err)
	}
}

func TestTeamSetupRunsItsStepsInATerminalThenSetsUpEachRepo(t *testing.T) {
	f := newTeamFixture(t)
	ch, cancel := f.bus.Subscribe()
	defer cancel()
	seen := map[string]bool{}
	var seenMu sync.Mutex
	go func() {
		for e := range ch {
			seenMu.Lock()
			seen[e.Type] = true
			seenMu.Unlock()
		}
	}()
	st, err := f.b.StartTeam(context.Background(), f.bundle())
	if err != nil {
		t.Fatal(err)
	}
	if st.Session != "team-acme" || st.Phase != "steps" || len(st.Steps) != 3 || st.Steps[2].ID != "github" || len(st.Projects) != 2 {
		t.Fatalf("started: %+v", st)
	}
	// The steps run in a terminal on the box, in the home folder.
	sess, err := f.b.Sessions.Get(context.Background(), "team-acme")
	if err != nil || !samePath(sess.Dir, f.home) || sess.Location != "" {
		t.Fatalf("session %+v %v", sess, err)
	}
	// sudo asks for the password there; the step says it waits for it, and
	// the engineer types it in the terminal.
	f.waitFor("the password prompt", func(st TeamStatus) bool { return st.Steps[0].State == TeamWaiting })
	f.typeIn("hunter2")
	// The box signs in to GitHub with its own gh: the device code shows.
	st = f.waitFor("GitHub's device code", func(st TeamStatus) bool { return st.Steps[2].Code != "" })
	if st.Steps[0].State != TeamDone || st.Steps[1].State != TeamDone || st.Steps[2].State != TeamWaiting || st.Steps[2].Code != "4F2A-9C1E" || st.Steps[2].URL == "" {
		t.Fatalf("at the sign-in: %+v", st.Steps)
	}
	f.typeIn("")
	st = f.waitFor("done", func(st TeamStatus) bool { return st.Phase == "done" || st.Phase == "failed" })
	if st.Phase != "done" {
		t.Fatalf("finished %+v", st)
	}
	if st.Steps[2].State != TeamDone || st.Steps[2].Code != "" {
		t.Fatalf("github step: %+v", st.Steps[2])
	}
	// web: cloned to ~/code/web, its own config trusted as reviewed, keys
	// laid into the box's own config for it.
	web, err := f.b.Locations.saved("web")
	if err != nil || web.Path != filepath.Join(evalHome(f.home), "code", "web") {
		t.Fatalf("web %+v %v", web, err)
	}
	if web.RepoTrust != sha(teamRepoConfig) {
		t.Fatalf("web's config is not trusted at the reviewed hash: %q", web.RepoTrust)
	}
	if web.Config == nil || web.Config.Env["STRIPE_KEY"] != "op://Dev/Stripe/key" || web.Config.Env["MAIL_KEY"] != "SG.mine" {
		t.Fatalf("web's keys: %+v", web.Config)
	}
	// api: its kit, then its init in the clone.
	api, err := f.b.Locations.saved("api")
	if err != nil || api.Kit == nil || api.Kit.ID != "acme-api" || !strings.HasSuffix(api.Path, "/src/api") {
		t.Fatalf("api %+v %v", api, err)
	}
	init, _ := os.ReadFile(filepath.Join(f.marks, "init-api"))
	if !strings.Contains(string(init), "src/api") {
		t.Fatalf("init ran: %q", init)
	}
	for _, p := range st.Projects {
		if p.State != TeamReady || p.Location == "" {
			t.Fatalf("project %+v", p)
		}
	}
	if strings.Join(st.KeysSet, ",") != "web/MAIL_KEY,web/STRIPE_KEY" {
		t.Fatalf("keys set: %v", st.KeysSet)
	}
	time.Sleep(100 * time.Millisecond)
	seenMu.Lock()
	defer seenMu.Unlock()
	for _, typ := range []string{"team.started", "team.step", "team.project", "team.done", "location.added"} {
		if !seen[typ] {
			t.Errorf("no %s event; saw %v", typ, seen)
		}
	}
	// Running it again (an update, or a second look) skips what is done:
	// the checks pass, gh is signed in, the clones are there.
	if _, err := f.b.StartTeam(context.Background(), f.bundle()); err != nil {
		t.Fatal(err)
	}
	st = f.waitFor("done again", func(st TeamStatus) bool { return st.Phase == "done" || st.Phase == "failed" })
	for _, s := range st.Steps {
		if s.State != TeamSkipped {
			t.Fatalf("step %s ran again: %+v", s.ID, st.Steps)
		}
	}
	if st.Phase != "done" {
		t.Fatalf("again: %+v", st)
	}
	runs, _ := os.ReadFile(filepath.Join(f.marks, "runs-tools"))
	if strings.Count(string(runs), "ran") != 1 {
		t.Fatalf("tools ran %q", runs)
	}
}

func evalHome(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return r
}

func TestAFailedStepKeepsTheStepsBeforeItAndRetriesFromIt(t *testing.T) {
	f := newTeamFixture(t)
	f.gh.SignIn("engineer")
	os.WriteFile(filepath.Join(f.marks, "fail-db"), nil, 0o644)
	if _, err := f.b.StartTeam(context.Background(), f.bundle()); err != nil {
		t.Fatal(err)
	}
	f.waitFor("the password prompt", func(st TeamStatus) bool { return st.Steps[0].State == TeamWaiting })
	f.typeIn("hunter2")
	st := f.waitFor("the failure", func(st TeamStatus) bool { return st.Phase == "failed" })
	if st.Steps[0].State != TeamDone || st.Steps[1].State != TeamFailed || st.Steps[1].Error != "exited with 3" || st.Steps[2].State != TeamTodo {
		t.Fatalf("failed: %+v", st.Steps)
	}
	if st.Projects[0].State != TeamQueued || !strings.Contains(st.Error, "db") {
		t.Fatalf("failed: %+v", st)
	}
	// The terminal says what happened and stays, with the output.
	screen, _ := f.b.Sessions.Screen(context.Background(), "team-acme", 50)
	if !strings.Contains(screen, "port 5450 is taken") || !strings.Contains(screen, "Berth stopped at db") {
		t.Fatalf("terminal:\n%s", screen)
	}
	// Persisted: a new runner (berthd restarted) reads the same state.
	f.b.Team.Stop()
	f.b.Team = &TeamRunner{Dir: f.b.Team.Dir, Poll: 30 * time.Millisecond, GH: f.b.Team.GH}
	f.b.ResumeTeams()
	if s := f.step("db"); s.State != TeamFailed {
		t.Fatalf("after restart: %+v", s)
	}
	os.Remove(filepath.Join(f.marks, "fail-db"))
	if _, err := f.b.RetryTeam("acme", ""); err != nil {
		t.Fatal(err)
	}
	st = f.waitFor("done", func(st TeamStatus) bool { return st.Phase == "done" || st.Phase == "failed" })
	if st.Phase != "done" || st.Steps[0].State != TeamDone || st.Steps[1].State != TeamDone || st.Steps[2].State != TeamSkipped {
		t.Fatalf("after retry: %+v", st)
	}
	// Retrying from db did not run tools again.
	if runs, _ := os.ReadFile(filepath.Join(f.marks, "runs-tools")); strings.Count(string(runs), "ran") != 1 {
		t.Fatalf("tools ran %q", runs)
	}
	if runs, _ := os.ReadFile(filepath.Join(f.marks, "runs-db")); strings.Count(string(runs), "ran") != 2 {
		t.Fatalf("db ran %q", runs)
	}
}

func TestATeamSetupResumesAfterBerthdRestarts(t *testing.T) {
	f := newTeamFixture(t)
	f.gh.SignIn("engineer")
	tb := f.bundle()
	tb.Steps = []team.Step{{ID: "slow", Title: "Slow"}}
	if _, err := f.b.StartTeam(context.Background(), tb); err != nil {
		t.Fatal(err)
	}
	f.waitFor("slow running", func(st TeamStatus) bool { return st.Steps[0].State == TeamRunning })
	// berthd stops; tmux keeps the terminal, and the step finishes while
	// nobody follows it.
	f.b.Team.Stop()
	time.Sleep(100 * time.Millisecond)
	os.WriteFile(filepath.Join(f.marks, "go"), nil, 0o644)
	time.Sleep(300 * time.Millisecond)
	if s := f.step("slow"); s.State != TeamRunning {
		t.Fatalf("nobody followed it, yet: %+v", s)
	}
	f.b.Team = &TeamRunner{Dir: f.b.Team.Dir, Poll: 30 * time.Millisecond, GH: f.b.Team.GH}
	f.b.ResumeTeams()
	st := f.waitFor("done", func(st TeamStatus) bool { return st.Phase == "done" || st.Phase == "failed" })
	if st.Phase != "done" || st.Steps[0].State != TeamDone {
		t.Fatalf("resumed: %+v", st)
	}
}

func TestAStepWhoseTerminalDiesIsInterrupted(t *testing.T) {
	f := newTeamFixture(t)
	tb := f.bundle()
	tb.Steps = []team.Step{{ID: "slow", Title: "Slow"}}
	if _, err := f.b.StartTeam(context.Background(), tb); err != nil {
		t.Fatal(err)
	}
	f.waitFor("slow running", func(st TeamStatus) bool { return st.Steps[0].State == TeamRunning })
	f.b.Sessions.Kill(context.Background(), "team-acme")
	st := f.waitFor("failed", func(st TeamStatus) bool { return st.Phase == "failed" })
	if st.Steps[0].State != TeamFailed || !strings.Contains(st.Steps[0].Error, "interrupted") {
		t.Fatalf("%+v", st.Steps)
	}
}

func TestOnlyTheReviewedRepoConfigIsTrusted(t *testing.T) {
	f := newTeamFixture(t)
	f.gh.SignIn("engineer")
	tb := f.bundle()
	tb.Steps, tb.GitHub = nil, false
	// The repository's config changed after the engineer reviewed it.
	tb.Projects[0].TrustHash = sha(`{"setup":"true"}`)
	tb.Projects = tb.Projects[:1]
	if _, err := f.b.StartTeam(context.Background(), tb); err != nil {
		t.Fatal(err)
	}
	st := f.waitFor("done", func(st TeamStatus) bool { return st.Phase == "done" || st.Phase == "failed" })
	web, _ := f.b.Locations.saved("web")
	if st.Projects[0].Trust != RepoTrustUntrusted || web.RepoTrust != "" || len(st.Projects[0].Warnings) == 0 || !strings.Contains(st.Projects[0].Warnings[0], "changed since you reviewed") {
		t.Fatalf("trusted a config nobody reviewed: %+v %q", st.Projects[0], web.RepoTrust)
	}
}

func TestAProjectThatFailsCanBeRetried(t *testing.T) {
	f := newTeamFixture(t)
	tb := f.bundle()
	tb.Steps, tb.GitHub = nil, false
	// Something else is already at ~/code/web.
	os.MkdirAll(filepath.Join(f.home, "code", "web"), 0o755)
	if _, err := f.b.StartTeam(context.Background(), tb); err != nil {
		t.Fatal(err)
	}
	st := f.waitFor("failed", func(st TeamStatus) bool { return st.Phase == "failed" })
	if st.Projects[0].State != TeamFailed || !strings.Contains(st.Projects[0].Error, "not a clone of acme/web") || st.Projects[1].State != TeamReady {
		t.Fatalf("%+v", st.Projects)
	}
	os.RemoveAll(filepath.Join(f.home, "code", "web"))
	if _, err := f.b.RetryTeam("acme", "web"); err != nil {
		t.Fatal(err)
	}
	st = f.waitFor("done", func(st TeamStatus) bool { return st.Phase == "done" })
	if st.Projects[0].State != TeamReady {
		t.Fatalf("%+v", st.Projects)
	}
}

func TestTeamBundlesAreChecked(t *testing.T) {
	f := newTeamFixture(t)
	for name, change := range map[string]func(*TeamBundle){
		"another host":     func(tb *TeamBundle) { tb.Projects[0].URL = "https://evil.example/acme/web.git" },
		"a file outside":   func(tb *TeamBundle) { tb.Files["../x"] = b64("x") },
		"a path upward":    func(tb *TeamBundle) { tb.Projects[0].Path = "~/../../etc" },
		"a missing script": func(tb *TeamBundle) { delete(tb.Files, "box/setup.sh") },
		"berth's own step": func(tb *TeamBundle) { tb.Steps[0].ID = "github" },
		"a bad id":         func(tb *TeamBundle) { tb.ID = "../x" },
	} {
		tb := f.bundle()
		change(&tb)
		if _, err := f.b.StartTeam(context.Background(), tb); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTheTeamRunnerRefusesRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("runs as root")
	}
	script := teamRunScript(TeamBundle{Name: "Acme", Org: "acme", Commit: "abc", Script: "box/setup.sh"})
	if !strings.Contains(script, `"$(id -u)" -eq 0`) || !strings.Contains(script, "Berth never sees it") {
		t.Fatal(script)
	}
	if out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("the runner script does not parse: %s", out)
	}
}

func TestWaitingPrompts(t *testing.T) {
	for line, want := range map[string]bool{
		"[sudo] password for dev:": true,
		"Password:":                true,
		"? Authenticate Git with your GitHub credentials? (Y/n)":                  true,
		"Press Enter to open https://github.com/login/device in your browser... ": true,
		"==> Docker":                    false,
		"Reading package lists... Done": false,
	} {
		if got := sudoPrompt.MatchString(strings.TrimSpace(line)); got != want {
			t.Errorf("%q: %v", line, got)
		}
	}
}
