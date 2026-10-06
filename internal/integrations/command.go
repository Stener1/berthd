package integrations

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sean-brydon/berthd/internal/events"
)

const Usage = `Integrations
  %[1]s integrations install claude|cursor|codex|gemini|opencode|all|present
                         Install berth's skills and agent hooks for a tool
  %[1]s hook TOOL EVENT [PAYLOAD]
                         What those hooks run: turns a tool's hook into a
                         berth event (agent.finished, agent.waiting)
`

// Emit publishes an event on this machine's berth: the laptop agent or the
// box daemon, whichever the running binary talks to.
type Emit func(events.Event) error

// Hook handles `hook TOOL EVENT [PAYLOAD]`. It never fails the calling tool:
// problems are reported on stderr and the tool's expected reply is printed.
func Hook(args []string, stdin *os.File, stdout, stderr io.Writer, emit Emit) {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "usage: hook TOOL EVENT [PAYLOAD]")
		return
	}
	tool, event := args[0], args[1]
	defer fmt.Fprint(stdout, Reply(tool))
	var payload []byte
	if len(args) >= 3 {
		payload = []byte(args[2])
	} else if info, err := stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
		// Read stdin only when something is piped in: a terminal would block.
		payload, _ = io.ReadAll(io.LimitReader(stdin, 1<<20))
	}
	e, ok := Translate(tool, event, payload)
	if !ok {
		return
	}
	// The berth session the agent runs in, so the box knows which of the
	// agents in a worktree this is.
	if name := berthSession(); name != "" {
		e.Data["session"] = name
	}
	// Stamped now: a hook spooled while berthd is down keeps its time.
	e.Time = time.Now().UTC()
	if err := emit(e); err != nil {
		fmt.Fprintf(stderr, "berth hook: %v\n", err)
	}
}

// Install handles `integrations install TOOL...` for the binary at bin.
func Install(args []string, bin string, out io.Writer) error {
	if len(args) < 2 || args[0] != "install" {
		return errors.New("usage: integrations install claude|cursor|codex|gemini|opencode|all|present")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	tools := args[1:]
	if len(tools) == 1 && tools[0] == "all" {
		tools = AllTools
	}
	// present: every agent CLI on this machine, saying nothing of the
	// others (the guided install runs this after installing the agents
	// the person chose).
	if len(tools) == 1 && tools[0] == "present" {
		return InstallPresent(home, bin, out)
	}
	for _, tool := range tools {
		if err := InstallTool(home, tool, bin, out); err != nil {
			return err
		}
	}
	return nil
}

// InstallTool installs berth's skills and hooks for one tool in home, for
// the binary at bin, and says what it did on out. Running it again changes
// nothing that is already in place.
func InstallTool(home, tool, bin string, out io.Writer) error {
	switch tool {
	case "claude":
		skills, err := installAllSkills(home, "claude")
		if err != nil {
			return err
		}
		settings := filepath.Join(home, ".claude", "settings.json")
		changed, err := InstallClaudeHooks(settings, bin)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Claude Code: skills in %s; hooks %s in %s\n", skills, verb(changed), settings)
		if mcpBin(bin) {
			cfg := filepath.Join(home, ".claude.json")
			changed, err := InstallMCP(cfg, bin, true)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Claude Code: MCP server %s in %s\n", verb(changed), cfg)
		}
	case "cursor":
		hooks := filepath.Join(home, ".cursor", "hooks.json")
		changed, err := InstallCursorHooks(hooks, bin)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Cursor: hooks %s in %s (existing hooks kept)\n", verb(changed), hooks)
	case "codex":
		skills, err := installAllSkills(home, "codex")
		if err != nil {
			return err
		}
		config := filepath.Join(home, ".codex", "config.toml")
		hooksFile := filepath.Join(home, ".codex", "hooks.json")
		hooksChanged, herr := InstallCodexHooks(hooksFile, bin)
		if herr != nil {
			return herr
		}
		fmt.Fprintf(out, "Codex: hooks %s in %s (trust them once in Codex with /hooks)\n", verb(hooksChanged), hooksFile)
		changed, err := InstallCodexNotify(config, bin)
		switch {
		case errors.Is(err, ErrNotifyTaken):
			// config.toml has a single notify setting, and the user already
			// uses it for something else: suggest rather than overwrite.
			fmt.Fprintf(out, "Codex: skills in %s\n", skills)
			fmt.Fprintf(out, "  %s already sets notify, so berth left it alone. To announce finished turns, make it:\n    %s\n", config, codexNotify(bin))
		case err != nil:
			return err
		default:
			fmt.Fprintf(out, "Codex: skills in %s; notify %s in %s\n", skills, verb(changed), config)
		}
		if mcpBin(bin) {
			changed, err := InstallCodexMCP(config, bin)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Codex: MCP server %s in %s\n", verb(changed), config)
		}
	case "gemini":
		settings := filepath.Join(home, ".gemini", "settings.json")
		changed, err := InstallGeminiHooks(settings, bin)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Gemini CLI: hooks %s in %s\n", verb(changed), settings)
		if mcpBin(bin) {
			changed, err := InstallMCP(settings, bin, false)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Gemini CLI: MCP server %s in %s\n", verb(changed), settings)
		}
	case "opencode":
		plugin := filepath.Join(home, ".config", "opencode", "plugin", "berth.js")
		changed, err := InstallOpenCodePlugin(plugin, bin)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "OpenCode: plugin %s at %s\n", verb(changed), plugin)
	default:
		return fmt.Errorf("unknown tool %q; use claude, cursor, codex, gemini, opencode, or all", tool)
	}
	return nil
}

// installAllSkills installs every skill berth ships for agent in home and
// returns the folder they went to.
func installAllSkills(home, agent string) (string, error) {
	names, _ := SkillNames(nil)
	if _, err := InstallSkills(home, agent, names); err != nil {
		return "", err
	}
	return SkillDir(home, agent)
}

func verb(changed bool) string {
	if changed {
		return "added"
	}
	return "already present"
}

// AllTools are the agents `integrations install all` covers.
var AllTools = []string{"claude", "cursor", "codex", "gemini", "opencode"}

var validSession = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`)

// berthSession is the berth session this hook runs in: $BERTH_SESSION,
// which berth sets in every session it starts, or, for sessions started
// before it did, the tmux session when the pane is on berth's own server.
func berthSession() string {
	if s := os.Getenv("BERTH_SESSION"); validSession.MatchString(s) {
		return s
	}
	tmux, pane := os.Getenv("TMUX"), os.Getenv("TMUX_PANE")
	sock, _, _ := strings.Cut(tmux, ",")
	if sock == "" || pane == "" || filepath.Base(sock) != "berth" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "-S", sock, "display-message", "-p", "-t", pane, "#S").Output()
	if s := strings.TrimSpace(string(out)); err == nil && validSession.MatchString(s) {
		return s
	}
	return ""
}
