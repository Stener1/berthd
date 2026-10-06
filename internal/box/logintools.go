package box

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// A kit's requires are checked the way its scripts and services will find
// the tools: through the box user's login shell. berthd's own PATH is the
// one it started with, often a service manager's, and misses what a team
// setup's steps just put on a login shell's PATH (fnm's node, corepack's
// yarn), which made a project warn "node is not installed" right after the
// steps installed it.

// loginToolTTL is how long a login shell's answer is kept: a kit lists a
// few tools, and a login shell can take a moment to start.
const loginToolTTL = 30 * time.Second

type loginToolEntry struct {
	path string
	at   time.Time
}

var loginTools = struct {
	sync.Mutex
	m map[string]loginToolEntry
}{m: map[string]loginToolEntry{}}

// loginLookup asks the login shell where name is: tests replace it.
var loginLookup = func(ctx context.Context, name string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// The name is an argument, never part of the script.
	cmd := exec.CommandContext(ctx, loginShell(), "-lc", `command -v "$0"`, name)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// findTool finds a tool on berthd's PATH or, failing that, as the box
// user's login shell does.
func findTool(ctx context.Context, name string) (string, bool) {
	if p, err := toolPath(name); err == nil {
		return p, true
	}
	loginTools.Lock()
	e, ok := loginTools.m[name]
	loginTools.Unlock()
	if !ok || time.Since(e.at) > loginToolTTL {
		e = loginToolEntry{path: loginLookup(ctx, name), at: time.Now()}
		loginTools.Lock()
		loginTools.m[name] = e
		loginTools.Unlock()
	}
	return e.path, e.path != ""
}

// forgetLoginTools drops what login shells said, after a team setup's
// steps changed what is installed.
func forgetLoginTools() {
	loginTools.Lock()
	loginTools.m = map[string]loginToolEntry{}
	loginTools.Unlock()
}

// toolWarning is how a missing tool a kit requires is reported.
func toolWarning(tool, hint string) string {
	w := tool + " is not installed on this box"
	if hint != "" {
		w += ": " + hint
	}
	return w
}

var toolWarningPattern = regexp.MustCompile(`^(\S+) is not installed on this box(: .*)?$`)

// recheckToolWarnings keeps the warnings that are still true: a missing
// tool that a login shell now finds is dropped.
func recheckToolWarnings(warnings []string) []string {
	var out []string
	for _, w := range warnings {
		if m := toolWarningPattern.FindStringSubmatch(w); m != nil {
			if _, ok := findTool(context.Background(), m[1]); ok {
				continue
			}
		}
		out = append(out, w)
	}
	return out
}
