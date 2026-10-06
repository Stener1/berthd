package box

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sean-brydon/berthd/internal/events"
)

func TestServiceSessionNamesAreValidUniqueAndShort(t *testing.T) {
	short := serviceSession("cal", "billing", "web")
	if short != "svc-cal-billing-web" {
		t.Fatalf("short name = %q", short)
	}
	long := strings.Repeat("a", 60)
	a := serviceSession("location", long+"-one", "web")
	b := serviceSession("location", long+"-two", "web")
	for _, n := range []string{a, b, serviceSession(strings.Repeat("x", 30), strings.Repeat("y", 60), strings.Repeat("z", 32))} {
		if !sessionName.MatchString(n) || len(n) > 63 {
			t.Errorf("%q is not a valid session name", n)
		}
	}
	if a == b || !strings.HasSuffix(a, "-web") {
		t.Fatalf("long names = %q, %q", a, b)
	}
}

func TestAServiceTitleIsOneShortLine(t *testing.T) {
	ok := RepoConfig{Services: []WorktreeService{{Name: "web", Run: "pnpm dev", Terminal: true, Title: "Next.js"}}}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"two\nlines", strings.Repeat("t", serviceTitleMax+1)} {
		bad := RepoConfig{Services: []WorktreeService{{Name: "web", Run: "pnpm dev", Terminal: true, Title: title}}}
		if err := bad.validate(); err == nil {
			t.Errorf("title %q was accepted", title)
		}
	}
	// The option rides through every layer: a kit's service, overridden by
	// the box, keeps what the box says.
	kit := RepoConfig{Services: []WorktreeService{{Name: "web", Run: "pnpm dev", Terminal: true, Title: "Next.js"}}}
	local := RepoConfig{Services: []WorktreeService{{Name: "web", Run: "pnpm dev --turbo", Terminal: true}}}
	got := layered(RepoConfig{}, &InstalledKit{Config: kit}, local).Services
	if len(got) != 1 || !got[0].Terminal || got[0].Title != "" || got[0].Run != "pnpm dev --turbo" {
		t.Fatalf("layered services = %+v", got)
	}
	if got := layered(RepoConfig{}, &InstalledKit{Config: kit}, RepoConfig{}).Services; len(got) != 1 || !got[0].Terminal || serviceTitle(got[0]) != "Next.js" {
		t.Fatalf("kit services = %+v", got)
	}
}

func TestAServicesTerminalIsNeverAnAgent(t *testing.T) {
	s := Session{Name: "svc-cal-billing-web", Command: "claude", Service: "web"}
	if a := agentFor(s); a != "" {
		t.Fatalf("agentFor = %q", a)
	}
	if a := sessionAgent(s); a != "" {
		t.Fatalf("sessionAgent = %q", a)
	}
	got := parseSessions([]byte("svc-cal-billing-web\t1759000000\t0\tcal/billing\t\t1\t/w\t\t\tNext.js\tweb\n"))
	if len(got) != 1 || got[0].Service != "web" || got[0].Title != "Next.js" || !got[0].Exited {
		t.Fatalf("parsed = %+v", got)
	}
}

func TestTmuxArgEscapesATrailingSemicolon(t *testing.T) {
	if got := tmuxArg("a;"); got != `a\;` {
		t.Fatalf("tmuxArg = %q", got)
	}
	if got := tmuxArg("a;b"); got != "a;b" {
		t.Fatalf("tmuxArg = %q", got)
	}
}

// terminalBox is a box with a worktree whose web service runs in a
// terminal, in a tmux server of the test's own.
func terminalBox(t *testing.T, run string) (*Box, Worktree) {
	t.Helper()
	ctx := context.Background()
	repo := gitRepo(t)
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(t.TempDir(), "locations.json")), Events: &events.Bus{}, Sessions: testSessions(t)}
	ops, _ := fakeService()
	b.Units = &Units{Dir: t.TempDir(), svc: ops}
	b.Locations.Add(ctx, "cal", repo)
	if err := b.Locations.SetLocalConfig("cal", RepoConfig{Services: []WorktreeService{{Name: "web", Run: run, Terminal: true, Title: "Next.js"}}}); err != nil {
		t.Fatal(err)
	}
	wt, err := b.Locations.CreateWorktree(ctx, "cal", "billing", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return b, wt
}

func waitUntil(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestATerminalServiceRunsInItsOwnSessionStopsWithCtrlCAndStartsAgainInIt(t *testing.T) {
	ctx := context.Background()
	b, wt := terminalBox(t, `echo "ready on $PORT in $BERTH_SERVICE"; trap 'echo bye; exit 130' INT; while :; do sleep 1; done`)
	ch, unsubscribe := b.Events.Subscribe()
	defer unsubscribe()

	st, err := b.StartService(ctx, "cal", "billing", "web")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Terminal || st.State != "running" || st.Session != "svc-cal-billing-web" || st.Title != "Next.js" || st.Port < 41000 {
		t.Fatalf("status = %+v", st)
	}
	sess, err := b.Sessions.Get(ctx, st.Session)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(wt.Path)
	if sess.Service != "web" || sess.Title != "Next.js" || sess.Location != "cal/billing" || (sess.Dir != wt.Path && sess.Dir != resolved) {
		t.Fatalf("session = %+v", sess)
	}
	if listed := b.enrich(ctx, []Session{sess}); listed[0].Agent != "" || listed[0].AgentState != "" {
		t.Fatalf("listed as an agent: %+v", listed[0])
	}
	// No unit: it runs in the terminal alone.
	if _, err := b.Units.Get(st.Unit); err == nil {
		t.Fatal("a unit was installed too")
	}
	want := "ready on " + strings.TrimPrefix(envOfWt(t, b, wt)["BERTH_PORT"], "") + " in web"
	waitUntil(t, "its output", 5*time.Second, func() bool {
		out, ok := b.terminalServiceLog(ctx, "cal", "billing", "web", 1<<20)
		return ok && strings.Contains(string(out), want)
	})
	// The log file gets it too.
	waitUntil(t, "the log file", 5*time.Second, func() bool {
		raw, _ := os.ReadFile(b.Units.logPath(st.Unit))
		return strings.Contains(string(raw), want)
	})
	// Starting a running one leaves it be.
	if _, err := b.StartService(ctx, "cal", "billing", "web"); err != nil {
		t.Fatal(err)
	}

	// Ctrl-C in the tab: the program ends, the terminal stays, and the box
	// says it stopped.
	if out, err := b.Sessions.tmux(ctx, "send-keys", "-t", "="+st.Session+":", "C-c"); err != nil {
		t.Fatalf("send-keys: %s", out)
	}
	// What arrived instead, said when it times out: the exit status and
	// events differ by shell and tmux, and CI is the place that shows it.
	var seen []string
	deadline := time.Now().Add(15 * time.Second)
	for stopped := false; !stopped; {
		select {
		case e := <-ch:
			seen = append(seen, fmt.Sprintf("%s %v", e.Type, e.Data))
			// The status is 130 wherever tmux learns it; in CI's container
			// PID 1 can reap the pane first and tmux has none to give
			// (TestPaneDeadOfASignalIsItsShellStatus covers reading it).
			st, known := e.Data["exit_status"]
			stopped = e.Type == "service.stopped" && e.Data["service"] == "web" && (!known || st == 130)
		case <-time.After(50 * time.Millisecond):
			if time.Now().After(deadline) {
				pane, _ := b.Sessions.tmux(ctx, "list-panes", "-t", "="+st.Session+":", "-F", "#{pane_dead} #{pane_dead_status} #{pane_current_command}")
				screen, _ := b.Sessions.Screen(ctx, st.Session, 20)
				t.Fatalf("timed out waiting for the stop event\nevents: %q\npane: %s\nscreen:\n%s", seen, pane, screen)
			}
		}
	}
	all, _ := b.WorktreeServices(ctx, "cal", "billing")
	if all[0].State != "stopped" {
		t.Fatalf("after Ctrl-C: %+v", all[0])
	}
	if s, err := b.Sessions.Get(ctx, st.Session); err != nil || !s.Exited {
		t.Fatalf("the terminal went with it: %+v %v", s, err)
	}

	// Start runs it again in the same terminal, with the title someone gave
	// the tab kept.
	b.Sessions.SetTitle(ctx, st.Session, "Web (mine)")
	if st, err = b.StartService(ctx, "cal", "billing", "web"); err != nil || st.State != "running" {
		t.Fatalf("start again = %+v, %v", st, err)
	}
	if s, _ := b.Sessions.Get(ctx, st.Session); s.Exited || s.Title != "Web (mine)" || s.Service != "web" {
		t.Fatalf("restarted session = %+v", s)
	}
	waitUntil(t, "its output again", 5*time.Second, func() bool {
		out, _ := b.terminalServiceLog(ctx, "cal", "billing", "web", 1<<20)
		return strings.Count(string(out), want) >= 2 || strings.Contains(string(out), "bye")
	})

	// Stop from the app stops it the same way, with one event.
	drainEvents(ch)
	if st, err = b.StopService(ctx, "cal", "billing", "web"); err != nil || st.State != "stopped" {
		t.Fatalf("stop = %+v, %v", st, err)
	}
	if s, err := b.Sessions.Get(ctx, st.Session); err != nil || !s.Exited {
		t.Fatalf("after stop: %+v %v", s, err)
	}
	time.Sleep(1500 * time.Millisecond)
	if n := countEvents(ch, "service.stopped"); n != 1 {
		t.Fatalf("%d service.stopped events, want 1", n)
	}

	// Restart from stopped and running alike.
	if st, err = b.StartService(ctx, "cal", "billing", "web"); err != nil || st.State != "running" {
		t.Fatalf("start = %+v, %v", st, err)
	}

	// Removing the worktree ends the terminal.
	b.stopServices("cal", "billing")
	if _, err := b.Sessions.Get(ctx, st.Session); err == nil {
		t.Fatal("the terminal outlived its worktree")
	}
	if out, ok := b.terminalServiceLog(ctx, "cal", "billing", "web", 1<<20); !ok || !strings.Contains(string(out), want) || strings.Contains(string(out), "\x1b[") {
		t.Fatalf("log after the session = %q, %v", out, ok)
	}
}

func TestAServiceThatTurnsIntoATerminalOneLeavesItsUnit(t *testing.T) {
	ctx := context.Background()
	b, _ := terminalBox(t, "sleep 300")
	b.Locations.SetLocalConfig("cal", RepoConfig{Services: []WorktreeService{{Name: "web", Run: "sleep 300"}}})
	if _, err := b.StartService(ctx, "cal", "billing", "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Units.Get("svc-cal-billing-web"); err != nil {
		t.Fatalf("background service has no unit: %v", err)
	}
	b.Locations.SetLocalConfig("cal", RepoConfig{Services: []WorktreeService{{Name: "web", Run: "sleep 300", Terminal: true}}})
	st, err := b.StartService(ctx, "cal", "billing", "web")
	if err != nil || st.State != "running" {
		t.Fatalf("terminal start = %+v, %v", st, err)
	}
	if _, err := b.Units.Get("svc-cal-billing-web"); err == nil {
		t.Fatal("the old unit still runs")
	}
	// And back: the terminal goes.
	b.Locations.SetLocalConfig("cal", RepoConfig{Services: []WorktreeService{{Name: "web", Run: "sleep 300"}}})
	if _, err := b.StartService(ctx, "cal", "billing", "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Sessions.Get(ctx, st.Session); err == nil {
		t.Fatal("the terminal still runs")
	}
}

func TestATerminalServiceWithSecretsPassesOnlyTheirReferences(t *testing.T) {
	ctx := context.Background()
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	b, _, _ := secretBox(t)
	// The stub op is all the PATH has now; tmux comes back.
	t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+filepath.Dir(tmux))
	b.Sessions = testSessions(t)
	// The pane runs the wrapper: here a program that does nothing.
	b.Update = &SelfUpdate{Executable: "/usr/bin/true"}
	b.Socket = "/run/berthd.sock"
	var mu sync.Mutex
	var traced [][]string
	b.Sessions.trace = func(args []string) {
		mu.Lock()
		traced = append(traced, append([]string{}, args...))
		mu.Unlock()
	}
	b.Locations.SetLocalConfig("cal", RepoConfig{Services: []WorktreeService{{Name: "web", Run: "pnpm dev", Terminal: true}}})
	if _, err := b.StartService(ctx, "cal", "billing", "web"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	var started []string
	for _, a := range traced {
		if len(a) > 0 && a[0] == "new-session" {
			started = a
		}
	}
	line := strings.Join(started, " ")
	if !strings.Contains(line, "/usr/bin/true secret exec --socket /run/berthd.sock -- ") || !strings.Contains(line, "DB_PASSWORD=op://dev/db/password") || !strings.Contains(line, "&& . '"+b.Sessions.commandPath(serviceSession("cal", "billing", "web"))+"'") {
		t.Fatalf("new-session = %q", started)
	}
	if strings.Contains(line, stubPassword) {
		t.Fatalf("tmux's arguments hold a value: %q", started)
	}
}

func envOfWt(t *testing.T, b *Box, wt Worktree) map[string]string {
	t.Helper()
	env, err := b.WorktreeEnv(context.Background(), "cal", wt)
	if err != nil {
		t.Fatal(err)
	}
	return envMap(env)
}

func drainEvents(ch <-chan events.Event) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func countEvents(ch <-chan events.Event, typ string) int {
	n := 0
	for {
		select {
		case e := <-ch:
			if e.Type == typ {
				n++
			}
		default:
			return n
		}
	}
}

func TestSignalNumberReadsTmuxNames(t *testing.T) {
	for in, want := range map[string]int{"int": 2, "INT": 2, "SIGTERM": 15, "9": 9, "": 0, "usr1": 0} {
		if got := signalNumber(in); got != want {
			t.Errorf("signalNumber(%q) = %d, want %d", in, got, want)
		}
	}
}

// A program a signal ends has no exit status in tmux, only the signal (CI's
// dash died of the Ctrl-C a service traps): it reads as the shell's
// 128+signal, so the stop event still says 130.
func TestPaneDeadOfASignalIsItsShellStatus(t *testing.T) {
	s := testSessions(t)
	ctx := context.Background()
	if out, err := s.tmux(ctx, "new-session", "-d", "-s", "sig", "sh -c 'sleep 0.2; kill -INT $$; sleep 5'", ";", "set-option", "-t", "sig", "remain-on-exit", "on"); err != nil {
		t.Fatalf("new-session: %s", out)
	}
	var status string
	waitUntil(t, "the pane to die", 5*time.Second, func() bool {
		dead, st, ok := s.paneDead(ctx, "sig")
		status = st
		return ok && dead
	})
	if status != "130" {
		t.Fatalf("status = %q, want 130", status)
	}
}
