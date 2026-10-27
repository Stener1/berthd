package box

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// nvmClaude is Claude Code installed with npm under nvm in home: a CLI
// whose `#!/usr/bin/env node` needs the node beside it, in a folder only an
// interactive shell's PATH has.
func nvmClaude(t *testing.T, home string) string {
	t.Helper()
	bin := filepath.Join(home, ".nvm", "versions", "node", "v22.9.0", "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\necho node-ran\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n[ \"$1\" = --version ] && { echo '2.1.3 (Claude Code)'; exit 0; }\necho \"claude from nvm: $(node)\"\nexec sleep 30\n"), 0o755)
	return filepath.Join(bin, "claude")
}

// Claude Code installed with npm under nvm, on no PATH berthd or a login
// shell has, is offered and starts, with node found beside it.
func TestAnNPMInstalledAgentIsOfferedAndStarts(t *testing.T) {
	s := testSessions(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin:"+filepath.Dir(mustTmux(t)))
	want := nvmClaude(t, home)

	found := false
	for _, p := range Presets(nil) {
		found = found || p.ID == "claude"
	}
	if !found {
		t.Fatalf("Claude Code is not offered: %+v", Presets(nil))
	}
	var path AgentPath
	for _, a := range AgentPaths() {
		if a.ID == "claude" {
			path = a
		}
	}
	if path.Path != want || path.Install != "npm" {
		t.Fatalf("agent path = %+v, want %s from npm", path, want)
	}
	if lp := launchPATH("claude --model opus", ""); !strings.HasPrefix(lp, filepath.Dir(want)+":") {
		t.Fatalf("launch PATH = %q", lp)
	}

	// The session's login shell (sh -l) knows nothing of nvm.
	os.WriteFile(filepath.Join(home, ".profile"), []byte("PATH=/usr/bin:/bin:$PATH\n"), 0o644)
	ctx := context.Background()
	if _, err := s.create(ctx, "agent", "", home, "claude --model opus", "claude", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		screen, _ := s.Screen(ctx, "agent", 0)
		if strings.Contains(screen, "claude from nvm: node-ran") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent did not start with node beside it: %q", screen)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// What the session says it runs is still the command asked for.
	if sess, err := s.Get(ctx, "agent"); err != nil || sess.Command != "claude --model opus" {
		t.Fatalf("session = %+v, %v", sess, err)
	}
}

// A plain command gets no agent's PATH.
func TestLaunchPATHIsOnlyForAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	nvmClaude(t, home)
	if lp := launchPATH("pnpm dev", ""); lp != "" {
		t.Fatalf("pnpm dev got %q", lp)
	}
	if lp := launchPATH("env FOO=1 claude", ""); lp == "" {
		t.Fatalf("claude behind env got no PATH")
	}
	if got := withPATH("/bin/zsh", "/a:/b", ". 'f'"); got != `PATH='/a:/b':"$PATH"; export PATH; . 'f'` {
		t.Errorf("zsh: %s", got)
	}
	if got := withPATH("/usr/bin/fish", "/a:/b", "source 'f'"); got != "set -gx PATH '/a' '/b' $PATH; source 'f'" {
		t.Errorf("fish: %s", got)
	}
}

func mustTmux(t *testing.T) string {
	t.Helper()
	p, err := tmuxPath()
	if err != nil {
		t.Skip("tmux not installed")
	}
	return p
}
