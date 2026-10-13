package box

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// longPrompt is n bytes of a prompt with everything a shell or tmux could
// trip on: quotes, $, backslashes, ;, line breaks, tabs and non-ASCII.
func longPrompt(n int) string {
	const piece = "Fix the user's \"billing\" bug: $HOME `pwd` \\n; a\tb ünïcødé → done.\n"
	p := strings.Repeat(piece, n/len(piece)+1)[:n]
	for !utf8.ValidString(p) {
		p = p[:len(p)-1] // never end inside a character
	}
	return p
}

// fakeAgent is a stand-in for an agent CLI named claude: it writes the
// prompt it was given to $OUT and waits, spending no tokens.
func fakeAgent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nprintf '%s' \"$1\" > \"$OUT.tmp\" && mv \"$OUT.tmp\" \"$OUT\"\nexec sleep 60\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// tracedArgs records the longest argument berth ever gave tmux.
func tracedArgs(s *Sessions) func() int {
	var mu sync.Mutex
	longest := 0
	s.trace = func(args []string) {
		mu.Lock()
		defer mu.Unlock()
		for _, a := range args {
			longest = max(longest, len(a))
		}
	}
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return longest
	}
}

// A task's command carries its prompt, which can be far longer than the
// ~16 KB tmux takes on its command line (or Linux in one argument): it
// starts from a file, reads back whole, and the agent gets it exactly.
func TestLongPromptsStartAndReadBack(t *testing.T) {
	s := testSessions(t)
	longest := tracedArgs(s)
	b := &Box{Sessions: s}
	ctx := context.Background()
	agent := fakeAgent(t)
	for _, size := range []int{8 << 10, 64 << 10, 1 << 20} {
		t.Run(fmt.Sprintf("%dKB", size>>10), func(t *testing.T) {
			prompt := longPrompt(size)
			command := agent + " " + shellQuote(prompt)
			name := fmt.Sprintf("task-%d", size>>10)
			out := filepath.Join(t.TempDir(), "prompt")
			sess, err := s.create(ctx, name, "demo/big", t.TempDir(), command, "claude", []string{"OUT=" + out}, nil, nil)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if sess.Command != command || sess.Preset != "claude" || sess.Location != "demo/big" {
				t.Fatalf("created session: command %d bytes (want %d), preset %q, location %q", len(sess.Command), len(command), sess.Preset, sess.Location)
			}
			got, err := s.Get(ctx, name)
			if err != nil || got.Command != command {
				t.Fatalf("get: %d bytes, %v", len(got.Command), err)
			}
			all, err := s.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, x := range b.enrich(ctx, all) {
				if x.Name == name {
					found = true
					if x.Command != command || x.Agent != "claude" {
						t.Fatalf("listed: agent %q, command %d bytes", x.Agent, len(x.Command))
					}
				}
			}
			if !found {
				t.Fatalf("%s is not listed", name)
			}
			if n := longest(); n > 4096 {
				t.Fatalf("tmux got an argument of %d bytes", n)
			}
			info, err := os.Stat(s.commandPath(name))
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("command file: %v %v", info, err)
			}
			// A program gets one argument of up to 128 KB on Linux, and all
			// of them in 1 MB on macOS; within that, the agent has it whole.
			if size <= 64<<10 {
				waitUntil(t, "the agent's prompt", 10*time.Second, func() bool {
					b, err := os.ReadFile(out)
					return err == nil && len(b) == len(prompt)
				})
				if b, _ := os.ReadFile(out); string(b) != prompt {
					t.Fatalf("the agent got a different prompt (%d bytes)", len(b))
				}
			}
			if err := s.Kill(ctx, name); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(s.commandPath(name)); !os.IsNotExist(err) {
				t.Fatalf("the command file outlived its session: %v", err)
			}
		})
	}
}

// Sending types text into a running agent as one paste, through tmux's
// stdin, so a long message arrives whole.
func TestSendingALongMessage(t *testing.T) {
	s := testSessions(t)
	longest := tracedArgs(s)
	ctx := context.Background()
	text := longPrompt(64 << 10)
	out := filepath.Join(t.TempDir(), "got")
	// A raw terminal takes the paste as it comes; tmux pastes line breaks
	// as carriage returns, as a terminal's Enter is.
	command := fmt.Sprintf("stty raw -echo; head -c %d > %s; sleep 60", len(text), shellQuote(out))
	if _, err := s.Create(ctx, "reader", "", t.TempDir(), command, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := s.Send(ctx, "reader", text, false); err != nil {
		t.Fatalf("send: %v", err)
	}
	want := strings.ReplaceAll(text, "\n", "\r")
	waitUntil(t, "the message", 15*time.Second, func() bool {
		b, err := os.ReadFile(out)
		return err == nil && len(b) == len(want)
	})
	if b, _ := os.ReadFile(out); string(b) != want {
		t.Fatalf("the session got a different message (%d bytes)", len(b))
	}
	if n := longest(); n > 4096 {
		t.Fatalf("tmux got an argument of %d bytes", n)
	}
}

// Typed text (a free answer to an agent's question) goes as keystrokes, in
// pieces tmux takes, split between characters.
func TestLiteralChunksSplitBetweenCharacters(t *testing.T) {
	text := strings.Repeat("ab→", 3000)
	parts := literalChunks(text)
	if len(parts) < 2 || strings.Join(parts, "") != text {
		t.Fatalf("%d parts", len(parts))
	}
	for _, p := range parts {
		if len(p) > literalChunk || !utf8.ValidString(p) {
			t.Fatalf("a part of %d bytes, valid %v", len(p), utf8.ValidString(p))
		}
	}
}

// Sessions an older berthd started keep their command in tmux options:
// still read, base64 first, plain otherwise.
func TestOldStyleSessionsStillRead(t *testing.T) {
	s := testSessions(t)
	ctx := context.Background()
	dir := t.TempDir()
	command := `claude 'say "${HOME}"'`
	start := func(name string, opts ...string) {
		args := []string{"new-session", "-d", "-s", name, "-c", dir, "--", "/bin/sh", "-lc", "sleep 60"}
		for i := 0; i < len(opts); i += 2 {
			args = append(args, ";", "set-option", "-t", "="+name+":", opts[i], opts[i+1])
		}
		if out, err := s.tmux(ctx, args...); err != nil {
			t.Fatal(tmuxError("new-session", out, err))
		}
	}
	start("old64", "@berth_location", "demo", "@berth_command", plainCommand(command), "@berth_command64", base64.StdEncoding.EncodeToString([]byte(command)), "@berth_agent", "claude")
	start("oldplain", "@berth_location", "demo", "@berth_command", "claude 'hi'")
	b := &Box{Sessions: s}
	all, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Session{}
	for _, x := range b.enrich(ctx, all) {
		got[x.Name] = x
	}
	if got["old64"].Command != command || got["old64"].Agent != "claude" {
		t.Errorf("old64 = %+v", got["old64"])
	}
	if got["oldplain"].Command != "claude 'hi'" || got["oldplain"].Agent != "claude" {
		t.Errorf("oldplain = %+v", got["oldplain"])
	}
	// A session whose command file went away still lists, with the start
	// of its command.
	if _, err := s.Create(ctx, "lost", "demo", dir, "claude 'gone'", nil); err != nil {
		t.Fatal(err)
	}
	os.Remove(s.commandPath("lost"))
	commandCache.Range(func(k, _ any) bool { commandCache.Delete(k); return true })
	if x, err := s.Get(ctx, "lost"); err != nil || x.Command != "claude 'gone'" {
		t.Errorf("lost = %+v, %v", x, err)
	}
}

// Command files of sessions that are gone are swept; live ones stay.
func TestCommandFilesOfGoneSessionsAreSwept(t *testing.T) {
	s := testSessions(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "live", "", t.TempDir(), "sleep 60", nil); err != nil {
		t.Fatal(err)
	}
	stale := s.commandPath("gone")
	os.WriteFile(stale, []byte("claude\n"), 0o600)
	s.sweepCommands(ctx, 0)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("a gone session's command stayed: %v", err)
	}
	if _, err := os.Stat(s.commandPath("live")); err != nil {
		t.Fatalf("a live session's command went: %v", err)
	}
	// A box starting again sweeps too.
	os.WriteFile(stale, []byte("claude\n"), 0o600)
	if _, err := NewSessions(filepath.Dir(s.Commands)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("starting kept a gone session's command: %v", err)
	}
}

// fakeTmux puts a tmux that runs script first on PATH, for one test.
func fakeTmux(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	resetTmuxPath()
	t.Cleanup(resetTmuxPath)
}

// A failure always says why: tmux's words when it has any, and its exit
// status or a timeout when it has none, never an empty reason.
func TestTmuxErrorsSayWhy(t *testing.T) {
	if got := tmuxError("new-session", nil, errors.New("exit status 1")).Error(); got != "tmux new-session: exit status 1" {
		t.Errorf("no output: %q", got)
	}
	if got := tmuxError("new-session", []byte("command too long\n"), errors.New("exit status 1")).Error(); got != "tmux new-session: exit status 1: command too long" {
		t.Errorf("with output: %q", got)
	}
	if !errors.Is(tmuxError("x", nil, errTmuxMissing), errTmuxMissing) {
		t.Errorf("a missing tmux lost its error")
	}

	dir := t.TempDir()
	s, err := NewSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Run("silent exit", func(t *testing.T) {
		fakeTmux(t, `case "$*" in *new-session*) exit 3;; *) exit 1;; esac`)
		_, err := s.Create(ctx, "a", "", dir, "claude", nil)
		if err == nil || err.Error() != "tmux new-session: exit status 3" {
			t.Fatalf("err = %v", err)
		}
		if codeFor(err) != CodeCommandFailed {
			t.Fatalf("code = %s", codeFor(err))
		}
		if _, err := os.Stat(s.commandPath("a")); !os.IsNotExist(err) {
			t.Fatalf("a failed start kept its command file: %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		fakeTmux(t, `case "$*" in *new-session*) exec sleep 5;; *) exit 1;; esac`)
		old := tmuxTimeout
		tmuxTimeout = 300 * time.Millisecond
		defer func() { tmuxTimeout = old }()
		_, err := s.Create(ctx, "b", "", dir, "claude", nil)
		if err == nil || err.Error() != "tmux new-session: timed out after 300ms" {
			t.Fatalf("err = %v", err)
		}
	})
}

// Without tmux, starting anything says so (tmux_missing), and a tmux in a
// Homebrew folder is found when berthd's PATH lacks it, as it does under
// launchd.
func TestTmuxIsFoundOffPATHOrSaidMissing(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	oldDirs := tmuxDirs
	t.Cleanup(func() { tmuxDirs = oldDirs; resetTmuxPath() })

	tmuxDirs = []string{empty}
	resetTmuxPath()
	s := &Sessions{Config: filepath.Join(t.TempDir(), "tmux.conf"), Commands: t.TempDir()}
	ctx := context.Background()
	if _, err := s.Create(ctx, "a", "", t.TempDir(), "claude", nil); !errors.Is(err, errTmuxMissing) || codeFor(err) != CodeTmuxMissing || statusFor(err) != http.StatusServiceUnavailable {
		t.Fatalf("create without tmux: %v", err)
	}
	if _, err := s.List(ctx); !errors.Is(err, errTmuxMissing) {
		t.Fatalf("list without tmux: %v", err)
	}
	if err := s.Send(ctx, "a", "hi", true); !errors.Is(err, errTmuxMissing) {
		t.Fatalf("send without tmux: %v", err)
	}
	if r := tmuxRequirement(); r.Found {
		t.Fatalf("requirement = %+v", r)
	}

	brew := t.TempDir()
	if err := os.WriteFile(filepath.Join(brew, "tmux"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tmuxDirs = []string{"/nonexistent", brew}
	if p, err := tmuxPath(); err != nil || p != filepath.Join(brew, "tmux") {
		t.Fatalf("tmuxPath = %q, %v", p, err)
	}
	if r := tmuxRequirement(); !r.Found || r.Path != filepath.Join(brew, "tmux") {
		t.Fatalf("requirement = %+v", r)
	}
}

func TestTmuxInstallSaysHowOnEachSystem(t *testing.T) {
	has := func(names ...string) func(string) bool {
		return func(n string) bool {
			for _, x := range names {
				if x == n {
					return true
				}
			}
			return false
		}
	}
	for _, tc := range []struct {
		goos string
		have []string
		want ToolRequirement
	}{
		{"darwin", []string{"brew"}, ToolRequirement{Install: "brew install tmux", Manager: "brew"}},
		{"darwin", nil, ToolRequirement{Install: "brew install tmux", Manager: "brew", ManagerMissing: true, Help: "https://brew.sh"}},
		{"linux", []string{"apt-get"}, ToolRequirement{Install: "sudo apt install tmux", Manager: "apt"}},
		{"linux", []string{"dnf"}, ToolRequirement{Install: "sudo dnf install tmux", Manager: "dnf"}},
		{"linux", []string{"pacman"}, ToolRequirement{Install: "sudo pacman -S tmux", Manager: "pacman"}},
		{"linux", nil, ToolRequirement{Help: "https://github.com/tmux/tmux/wiki/Installing"}},
	} {
		if got := tmuxInstall(tc.goos, has(tc.have...)); got != tc.want {
			t.Errorf("%s %v: %+v, want %+v", tc.goos, tc.have, got, tc.want)
		}
	}
}

// Over the API: a session with a long command starts; a body over the
// limit is refused as too large; and without tmux a task fails before it
// makes its worktree.
func TestLongPromptsOverTheAPI(t *testing.T) {
	c, _ := servedBox(t)
	repo := gitRepo(t)
	if status := call(t, c, "POST", "/v1/locations", "", map[string]string{"name": "cal", "path": repo}, nil); status != 200 {
		t.Fatalf("add location: %d", status)
	}
	command := fakeAgent(t) + " " + shellQuote(longPrompt(200<<10))
	var sess Session
	if status := call(t, c, "POST", "/v1/sessions", "", map[string]string{"location": "cal", "command": command, "name": "big"}, &sess); status != 200 {
		t.Fatalf("add session: %d", status)
	}
	if sess.Command != command || sess.Agent != "claude" {
		t.Fatalf("session: agent %q, command %d bytes", sess.Agent, len(sess.Command))
	}
	var all []Session
	call(t, c, "GET", "/v1/sessions", "", nil, &all)
	if len(all) != 1 || all[0].Command != command || all[0].Agent != "claude" {
		t.Fatalf("listed %d sessions", len(all))
	}
	var e struct{ Error, Code string }
	if status := call(t, c, "POST", "/v1/sessions", "", map[string]string{"location": "cal", "command": strings.Repeat("x", 3<<20)}, &e); status != http.StatusRequestEntityTooLarge || e.Code != CodeBadRequest {
		t.Fatalf("a 3 MB body: %d %+v", status, e)
	}
	var reqs Requirements
	if status := call(t, c, "GET", "/v1/requirements", "", nil, &reqs); status != 200 || reqs.OS != runtime.GOOS || !reqs.Tmux.Found || len(reqs.Agents) == 0 {
		t.Fatalf("requirements: %d %+v", status, reqs)
	}

	empty := t.TempDir()
	t.Setenv("PATH", empty)
	oldDirs := tmuxDirs
	tmuxDirs = []string{empty}
	resetTmuxPath()
	t.Cleanup(func() { tmuxDirs = oldDirs; resetTmuxPath() })
	if status := call(t, c, "POST", "/v1/tasks", "", TaskRequest{Location: "cal", Name: "first", Command: "claude"}, &e); status != http.StatusServiceUnavailable || e.Code != CodeTmuxMissing {
		t.Fatalf("task without tmux: %d %+v", status, e)
	}
	if out, _ := exec.Command("git", "-C", repo, "worktree", "list").Output(); strings.Contains(string(out), "first") {
		t.Fatalf("a task without tmux made its worktree:\n%s", out)
	}
}

// An agent gets its first prompt as one argument, which the kernel caps:
// a longer one is refused up front, saying why, not left to fail in the
// pane.
func TestAFirstPromptOverTheArgumentCapIsRefused(t *testing.T) {
	claude := builtinAgents[0]
	if _, err := AgentCommandWith(claude, longPrompt(100<<10), "", ""); err != nil {
		t.Fatalf("100 KB: %v", err)
	}
	_, err := AgentCommandWith(claude, longPrompt(maxPromptArg()+1), "", "")
	var he httpError
	if !errors.As(err, &he) || he.status != http.StatusRequestEntityTooLarge || !strings.Contains(he.msg, "send the rest once it runs") {
		t.Fatalf("over the cap: %v", err)
	}
}
