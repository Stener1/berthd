// Package hooks runs user commands when berth events happen, which is how
// berth drives other tools and how people script it.
//
// Hooks live in ~/.berth/hooks.json on the machine that should run them, and
// plugins add their own from their manifests. Most hooks follow events after
// the fact. A hook on "before:ACTION" runs first instead, and can refuse the
// action by exiting non-zero: its output becomes the error the caller sees.
//
// Loops are the risk when integrations run in both directions: berth creates
// a worktree, a hook tells Orca, Orca tells berth, and so on. A hook names
// the tool it drives; events that came from that tool never trigger it, and
// everything the hook does in berth is stamped with that tool as its origin.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sean-brydon/berthd/internal/events"
	"github.com/sean-brydon/berthd/internal/groups"
	"github.com/sean-brydon/berthd/internal/statefile"
)

type Hook struct {
	// On is an event type, a prefix pattern such as "worktree.*", or "*".
	// "before:" in front, as in "before:worktree.create", makes it a gate
	// that runs before the action and can stop it.
	On string `json:"on"`
	// Run is a shell command. It gets the event as JSON on stdin and as
	// BERTH_* environment variables.
	Run string `json:"run"`
	// Tool is the tool this hook drives. Events from it are skipped.
	Tool    string `json:"tool,omitempty"`
	Timeout string `json:"timeout,omitempty"`
	// Dir is where Run runs: the plugin's folder for a plugin's hooks.
	Dir string `json:"-"`
	// Source is "plugin:<id>" for a plugin's hooks, which are edited by
	// changing the plugin, never through hooks.json.
	Source string `json:"source,omitempty"`
}

// BeforePrefix marks a hook that gates an action rather than following it.
const BeforePrefix = "before:"

type Config struct {
	Hooks []Hook `json:"hooks"`
}

// OriginEnv carries the origin into commands hooks run, so berth requests
// they make are attributed to the tool rather than to berth itself.
const OriginEnv = "BERTH_ORIGIN"

// Matches reports whether h should run for e.
func Matches(h Hook, e events.Event) bool {
	if h.Run == "" || strings.HasPrefix(h.On, BeforePrefix) {
		return false
	}
	if h.Tool != "" && e.Origin == h.Tool {
		return false
	}
	return pattern(h.On, e.Type)
}

// MatchesBefore reports whether h gates the action e describes.
func MatchesBefore(h Hook, e events.Event) bool {
	on, ok := strings.CutPrefix(h.On, BeforePrefix)
	if h.Run == "" || !ok {
		return false
	}
	if h.Tool != "" && e.Origin == h.Tool {
		return false
	}
	return pattern(on, e.Type)
}

func pattern(on, typ string) bool {
	switch {
	case on == "*":
		return !events.Chatty(typ)
	case strings.HasSuffix(on, ".*"):
		return strings.HasPrefix(typ, strings.TrimSuffix(on, "*"))
	}
	return on == typ
}

// Env describes e to a hook command.
func Env(e events.Event, tool string) []string {
	origin := tool
	if origin == "" {
		origin = "hook"
	}
	env := []string{
		"BERTH_EVENT=" + e.Type,
		"BERTH_EVENT_BOX=" + e.Box,
		"BERTH_EVENT_ORIGIN=" + e.Origin,
		OriginEnv + "=" + origin,
	}
	reserved := map[string]bool{}
	for _, kv := range env {
		reserved[strings.SplitN(kv, "=", 2)[0]] = true
	}
	for k, v := range e.Data {
		key := strings.ToUpper(strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				return r
			}
			return '_'
		}, k))
		// Event data must not replace the variables above: the origin is
		// what stops a hook from reacting to its own events.
		if reserved["BERTH_"+key] {
			continue
		}
		env = append(env, fmt.Sprintf("BERTH_%s=%v", key, v))
	}
	return env
}

// Runner re-reads its config for every event, so edits apply without a
// restart, and runs matching hooks one at a time off the event path.
type Runner struct {
	Path string
	// PluginsDir holds plugins, whose berth-plugin.json may list hooks.
	PluginsDir string
	Log        *log.Logger
}

// It reads the bus with a cursor and hands events to a few workers, so a
// slow hook neither loses events nor holds up the ones for other worktrees.
func (r *Runner) Run(ctx context.Context, bus *events.Bus) {
	cur := bus.SubscribeFrom(-1).Named("hooks")
	defer cur.Close()
	RunWorkers(ctx, cur, 4, func(e events.Event) { r.handle(ctx, e) })
}

// RunWorkers feeds a cursor's events to n workers. Events about the same
// worktree or session go to the same worker, in order. A full queue makes
// the cursor fall behind, which the journal catches up.
func RunWorkers(ctx context.Context, cur *events.Cursor, n int, handle func(events.Event)) {
	queues := make([]chan events.Event, n)
	var wg sync.WaitGroup
	for i := range queues {
		queues[i] = make(chan events.Event, 32)
		wg.Add(1)
		go func(q chan events.Event) {
			defer wg.Done()
			for e := range q {
				handle(e)
			}
		}(queues[i])
	}
	defer func() {
		for _, q := range queues {
			close(q)
		}
		wg.Wait()
	}()
	for {
		e, err := cur.Next(ctx)
		if err != nil {
			return
		}
		key, _ := e.Data["path"].(string)
		if key == "" {
			key, _ = e.Data["session"].(string)
		}
		h := fnv.New32a()
		h.Write([]byte(key))
		select {
		case queues[int(h.Sum32()%uint32(n))] <- e:
		case <-ctx.Done():
			return
		}
	}
}

// Load reads every hook: hooks.json first, then each plugin's.
func (r *Runner) Load() (Config, error) {
	var c Config
	b, err := os.ReadFile(r.Path)
	if err != nil && !os.IsNotExist(err) {
		return c, err
	}
	if err == nil {
		if err := json.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("%s: %w", r.Path, err)
		}
	}
	if r.PluginsDir != "" {
		manifests, _ := filepath.Glob(filepath.Join(r.PluginsDir, "*", PluginManifest))
		for _, m := range manifests {
			hooks, err := pluginHooks(m)
			if err != nil {
				r.logf("hooks: %v", err)
				continue
			}
			c.Hooks = append(c.Hooks, hooks...)
		}
	}
	return c, nil
}

var validOn = regexp.MustCompile(`^(before:)?(\*|[a-z][a-z0-9-]*\.(\*|[a-z][a-z0-9.-]*))$`)

// Validate reports the first thing wrong with hooks someone wrote.
func Validate(hooks []Hook) error {
	for i, h := range hooks {
		if !validOn.MatchString(h.On) {
			return fmt.Errorf("hook %d: %q is not an event, a prefix like worktree.*, *, or before: one of those", i+1, h.On)
		}
		if strings.TrimSpace(h.Run) == "" {
			return fmt.Errorf("hook %d (%s): nothing to run", i+1, h.On)
		}
		if h.Timeout != "" {
			if d, err := time.ParseDuration(h.Timeout); err != nil || d <= 0 {
				return fmt.Errorf("hook %d (%s): timeout %q is not a duration like 30s or 5m", i+1, h.On, h.Timeout)
			}
		}
	}
	return nil
}

// Save replaces the hooks in hooks.json. Plugin hooks are not written there:
// they belong to their plugins.
func (r *Runner) Save(hooks []Hook) error {
	own := []Hook{}
	for _, h := range hooks {
		if h.Source == "" {
			h.Dir = ""
			own = append(own, h)
		}
	}
	if err := Validate(own); err != nil {
		return err
	}
	b, err := json.MarshalIndent(Config{Hooks: own}, "", "  ")
	if err != nil {
		return err
	}
	return statefile.Write(r.Path, append(b, '\n'))
}

// PluginManifest is the file that makes a folder a plugin.
const PluginManifest = "berth-plugin.json"

// pluginHooks reads a plugin's hooks, which run in the plugin's folder, when
// the user has allowed the plugin as it is now (see PluginStatus).
func pluginHooks(manifest string) ([]Hook, error) {
	dir := filepath.Dir(manifest)
	b, err := os.ReadFile(manifest)
	if err != nil {
		return nil, err
	}
	if !pluginStatusWith(dir, b).Enabled {
		return nil, nil
	}
	var m struct {
		Hooks []Hook `json:"hooks"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", manifest, err)
	}
	for i := range m.Hooks {
		m.Hooks[i].Dir = dir
		m.Hooks[i].Source = "plugin:" + filepath.Base(dir)
	}
	return m.Hooks, nil
}

// Before runs the hooks gating e's action, in order, and returns the first
// refusal. A nil Runner allows everything.
func (r *Runner) Before(ctx context.Context, e events.Event) error {
	if r == nil {
		return nil
	}
	cfg, err := r.Load()
	if err != nil {
		// A config that cannot be read must not block work.
		r.logf("hooks: %v", err)
		return nil
	}
	for _, h := range cfg.Hooks {
		if !MatchesBefore(h, e) {
			continue
		}
		if out, err := r.command(ctx, h, e, 30*time.Second); err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("a %q hook stopped %s: %s", h.On, e.Type, msg)
		}
	}
	return nil
}

func (r *Runner) handle(ctx context.Context, e events.Event) {
	cfg, err := r.Load()
	if err != nil {
		r.logf("hooks: %v", err)
		return
	}
	for _, h := range cfg.Hooks {
		if Matches(h, e) {
			r.exec(ctx, h, e)
		}
	}
}

func (r *Runner) exec(ctx context.Context, h Hook, e events.Event) {
	out, err := r.command(ctx, h, e, time.Minute)
	if err != nil {
		r.logf("hook %q for %s failed: %v: %s", h.On, e.Type, err, strings.TrimSpace(string(out)))
		return
	}
	r.logf("hook %q ran for %s", h.On, e.Type)
}

// command runs h for e with the event on stdin, within h's timeout or def.
func (r *Runner) command(ctx context.Context, h Hook, e events.Event, def time.Duration) ([]byte, error) {
	return Exec(ctx, h, e, def, nil)
}

// Exec runs one hook for e: in h.Dir, with the event on stdin and in the
// environment, plus extra variables, within h's timeout or def.
func Exec(ctx context.Context, h Hook, e events.Event, def time.Duration, extra []string) ([]byte, error) {
	timeout := def
	if d, err := time.ParseDuration(h.Timeout); err == nil && d > 0 {
		timeout = d
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	payload, _ := json.Marshal(e)
	cmd := groups.CommandContext(ctx, "/bin/sh", "-c", h.Run)
	cmd.Dir = h.Dir
	cmd.Env = append(os.Environ(), Env(e, h.Tool)...)
	if strings.HasPrefix(h.Source, "plugin:") {
		cmd.Env = append(cmd.Env, "BERTH_PLUGIN_DIR="+h.Dir)
	}
	cmd.Env = append(cmd.Env, extra...)
	cmd.Stdin = bytes.NewReader(payload)
	return cmd.CombinedOutput()
}

func (r *Runner) logf(format string, args ...any) {
	if r.Log != nil {
		r.Log.Printf(format, args...)
	}
}
