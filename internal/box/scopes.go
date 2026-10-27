package box

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Sessions clean up after themselves. Whatever a session's program starts
// (a repository's Playwright tests and their browsers, a dev server, a
// watcher) goes when berth ends the session, even what double-forked away
// from the pane.
//
// On Linux with a systemd user manager, each new session's program starts
// in a transient scope of its own (systemd-run --user --scope), a cgroup
// that holds everything it ever starts. Ending the session stops the scope.
// The scope belongs to the user's manager, not to berthd's service, so the
// session outlives berthd's restarts and upgrades as before; and the cgroup
// says what the session uses (memory.current, cpu.stat) and can hold an
// optional memory ceiling (MemoryHigh), which slows a session down near it
// rather than killing anything.
//
// Without one (macOS, containers, sessions from before), berth finds what a
// session started when it ends it: the pane's descendants and, on Linux,
// processes that left the tree but still carry the session's BERTH_SESSION
// and the TMUX of berth's own tmux server. Nothing else is ever touched.

// scopeManager makes and stops sessions' scopes; tests use a fake.
type scopeManager interface {
	// Available says whether a new session can get a scope now.
	Available(ctx context.Context) bool
	// Wrap is the command that starts a program in a new scope unit.
	Wrap(unit, description string, memoryHigh uint64) []string
	// Stop ends the scope and everything in it.
	Stop(ctx context.Context, unit string) error
	// SetMemoryHigh changes a running scope's ceiling (0: none).
	SetMemoryHigh(ctx context.Context, unit string, bytes uint64) error
	// Cgroup is the scope's cgroup folder, "" when it is gone.
	Cgroup(ctx context.Context, unit string) string
}

// scopeUnit names a new session's scope. The time keeps a name used again
// from meeting a scope that is still stopping.
func scopeUnit(session string, now time.Time) string {
	return "berth-" + session + "-" + strconv.FormatInt(now.Unix(), 36) + ".scope"
}

// systemdScopes uses the user's systemd manager.
type systemdScopes struct {
	mu        sync.Mutex
	ok        bool
	checkedAt time.Time
	run       string // systemd-run
	ctl       string // systemctl
	cgroups   sync.Map
	// missing remembers for a minute that a unit had no cgroup (its
	// session's program ended), so lists don't ask systemd every time.
	missing sync.Map
}

// NewSystemdScopes is the scope manager for this box: nil off Linux, or
// when BERTH_SESSION_SCOPES=0.
func NewSystemdScopes() scopeManager {
	if runtime.GOOS != "linux" || os.Getenv("BERTH_SESSION_SCOPES") == "0" {
		return nil
	}
	return &systemdScopes{}
}

// scopeTimeoutStop is how long a scope's processes get after SIGTERM.
const scopeTimeoutStop = "10s"

// Available tries a scope once (and again every ten minutes while none
// works): systemd-run starting `true` in one.
func (s *systemdScopes) Available(ctx context.Context) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.checkedAt.IsZero() && (s.ok || time.Since(s.checkedAt) < 10*time.Minute) {
		return s.ok
	}
	s.checkedAt = time.Now()
	s.ok = false
	run, err := exec.LookPath("systemd-run")
	if err != nil {
		return false
	}
	ctl, err := exec.LookPath("systemctl")
	if err != nil {
		return false
	}
	s.run, s.ctl = run, ctl
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	probe := append(s.Wrap("berth-probe-"+strconv.Itoa(os.Getpid())+".scope", "Shipyard checks scopes work", 0), "true")
	s.ok = exec.CommandContext(cctx, probe[0], probe[1:]...).Run() == nil
	return s.ok
}

// Wrap asks for no CPUAccounting: cgroup v2 always counts a scope's CPU
// time (cpu.stat's usage_usec), and systemd 258 and later print a warning
// for it in every new pane.
func (s *systemdScopes) Wrap(unit, description string, memoryHigh uint64) []string {
	run := s.run
	if run == "" {
		run = "systemd-run"
	}
	args := []string{run, "--user", "--scope", "--quiet", "--collect", "--unit=" + unit, "--description=" + description,
		"-p", "TimeoutStopSec=" + scopeTimeoutStop, "-p", "MemoryAccounting=yes"}
	if memoryHigh > 0 {
		args = append(args, "-p", "MemoryHigh="+strconv.FormatUint(memoryHigh, 10))
	}
	return append(args, "--")
}

func (s *systemdScopes) systemctl(ctx context.Context, timeout time.Duration, args ...string) error {
	ctl := s.ctl
	if ctl == "" {
		ctl = "systemctl"
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(cctx, ctl, append([]string{"--user"}, args...)...).Run()
}

func (s *systemdScopes) Stop(ctx context.Context, unit string) error {
	s.cgroups.Delete(unit)
	return s.systemctl(ctx, 30*time.Second, "stop", unit)
}

func (s *systemdScopes) SetMemoryHigh(ctx context.Context, unit string, bytes uint64) error {
	v := "infinity"
	if bytes > 0 {
		v = strconv.FormatUint(bytes, 10)
	}
	return s.systemctl(ctx, 10*time.Second, "set-property", "--runtime", unit, "MemoryHigh="+v)
}

func (s *systemdScopes) Cgroup(ctx context.Context, unit string) string {
	if v, ok := s.cgroups.Load(unit); ok {
		if _, err := os.Stat(v.(string)); err == nil {
			return v.(string)
		}
		s.cgroups.Delete(unit)
	}
	if at, ok := s.missing.Load(unit); ok && time.Since(at.(time.Time)) < time.Minute {
		return ""
	}
	ctl := s.ctl
	if ctl == "" {
		ctl = "systemctl"
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, ctl, "--user", "show", "-p", "ControlGroup", "--value", unit).Output()
	cg := strings.TrimSpace(string(out))
	dir := filepath.Join("/sys/fs/cgroup", cg)
	if _, statErr := os.Stat(dir); err != nil || cg == "" || statErr != nil {
		s.missing.Store(unit, time.Now())
		return ""
	}
	s.missing.Delete(unit)
	s.cgroups.Store(unit, dir)
	return dir
}

// ProcUsage is what a session's processes use, from its scope's cgroup
// or, without one, summed over the processes berth finds for it.
type ProcUsage struct {
	// Memory is in bytes: the cgroup's memory.current (page cache
	// included, as its ceiling counts it), or resident memory summed.
	Memory uint64 `json:"memory"`
	// MemoryHigh is its ceiling, 0 for none.
	MemoryHigh uint64 `json:"memory_high,omitempty"`
	// CPUSeconds is the processor time it has used; CPUPercent how busy it
	// is now (100 is one core), when known.
	CPUSeconds float64 `json:"cpu_s"`
	CPUPercent float64 `json:"cpu_percent,omitempty"`
	Processes  int     `json:"processes,omitempty"`
	// NearLimit is set from 90% of its ceiling; Throttled counts the
	// times the kernel held it back at the ceiling.
	NearLimit bool   `json:"near_limit,omitempty"`
	Throttled uint64 `json:"throttled,omitempty"`
	// Scoped says the numbers are its cgroup's.
	Scoped bool `json:"scoped,omitempty"`
}

// nearLimit is the share of its ceiling a session must use to be shown as
// near it.
const nearLimit = 0.9

// readCgroupUsage reads a cgroup v2 folder.
func readCgroupUsage(dir string) (ProcUsage, bool) {
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return strings.TrimSpace(string(b))
	}
	cur := read("memory.current")
	if cur == "" {
		return ProcUsage{}, false
	}
	u := ProcUsage{Scoped: true}
	u.Memory, _ = strconv.ParseUint(cur, 10, 64)
	if h := read("memory.high"); h != "" && h != "max" {
		u.MemoryHigh, _ = strconv.ParseUint(h, 10, 64)
	}
	for _, l := range strings.Split(read("cpu.stat"), "\n") {
		if v, ok := strings.CutPrefix(l, "usage_usec "); ok {
			n, _ := strconv.ParseUint(v, 10, 64)
			u.CPUSeconds = float64(n) / 1e6
		}
	}
	for _, l := range strings.Split(read("memory.events"), "\n") {
		if v, ok := strings.CutPrefix(l, "high "); ok {
			u.Throttled, _ = strconv.ParseUint(v, 10, 64)
		}
	}
	u.Processes = len(strings.Fields(read("cgroup.procs")))
	u.NearLimit = u.MemoryHigh > 0 && float64(u.Memory) >= nearLimit*float64(u.MemoryHigh)
	return u, true
}

// scopeUsage is a scoped session's usage, if its scope runs.
func (s *Sessions) scopeUsage(ctx context.Context, sess Session) (ProcUsage, bool) {
	if s.Scopes == nil || sess.Scope == "" {
		return ProcUsage{}, false
	}
	dir := s.Scopes.Cgroup(ctx, sess.Scope)
	if dir == "" {
		return ProcUsage{}, false
	}
	return readCgroupUsage(dir)
}

// memoryHigh is the ceiling a new session's scope gets.
func (s *Sessions) memoryHigh() uint64 {
	if s.MemoryHigh == nil {
		return 0
	}
	return s.MemoryHigh()
}

// ApplyMemoryHigh gives every running session's scope the ceiling: the box
// owner changed it.
func (s *Sessions) ApplyMemoryHigh(ctx context.Context, bytes uint64) {
	if s.Scopes == nil {
		return
	}
	all, err := s.list(ctx)
	if err != nil {
		return
	}
	for _, sess := range all {
		if sess.Scope != "" && s.Scopes.Cgroup(ctx, sess.Scope) != "" {
			s.Scopes.SetMemoryHigh(ctx, sess.Scope, bytes)
		}
	}
}

// --- ending a session ---------------------------------------------------

func (s *Sessions) snapshot() ([]procStat, error) {
	if s.procs != nil {
		return s.procs()
	}
	return snapshotProcs()
}

func (s *Sessions) kill(pid int, sig syscall.Signal) {
	if s.signal != nil {
		s.signal(pid, sig)
		return
	}
	syscall.Kill(pid, sig)
}

// sessionProcs are the processes that belong to session name as berth can
// tell without a scope: descendants of its panes, and on Linux those that
// carry its BERTH_SESSION from berth's own tmux server and started before
// before (a session started later under the same name is not this one's).
// macOS boxes use the tree only.
func sessionProcs(ps []procStat, name string, panes []int, socket string, before time.Time, markers bool) []procStat {
	kids := map[int][]int{}
	byPID := map[int]procStat{}
	for _, p := range ps {
		kids[p.PPID] = append(kids[p.PPID], p.PID)
		byPID[p.PID] = p
	}
	seen := map[int]bool{}
	var out []procStat
	var walk func(int)
	walk = func(pid int) {
		if seen[pid] {
			return
		}
		seen[pid] = true
		if p, ok := byPID[pid]; ok {
			out = append(out, p)
		}
		for _, k := range kids[pid] {
			walk(k)
		}
	}
	for _, pid := range panes {
		if pid > 1 {
			walk(pid)
		}
	}
	if markers {
		for _, p := range ps {
			if seen[p.PID] || p.Env["BERTH_SESSION"] != name || !sameSocket(p.Env["TMUX"], socket) {
				continue
			}
			if !before.IsZero() && !p.Start.IsZero() && p.Start.After(before) {
				continue
			}
			walk(p.PID)
		}
	}
	return out
}

// useMarkers says whether ending a session looks for what left its tree by
// the session's environment marker.
var useMarkers = runtime.GOOS == "linux"

// panePIDs are the processes running in a session's panes.
func (s *Sessions) panePIDs(ctx context.Context, name string) []int {
	out, err := s.tmux(ctx, "list-panes", "-s", "-t", "="+name, "-F", "#{pane_pid}")
	if err != nil {
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if n, err := strconv.Atoi(f); err == nil && n > 1 {
			pids = append(pids, n)
		}
	}
	return pids
}

// treeGrace is how long processes get after SIGTERM before SIGKILL.
var treeGrace = 3 * time.Second

// stopProcs ends procs: SIGTERM, then SIGKILL to what is left after the
// grace. Each is looked up again first, so a process id the system has
// since given to something else is never signalled.
func (s *Sessions) stopProcs(procs []procStat) {
	if len(procs) == 0 {
		return
	}
	want := map[procKey]bool{}
	for _, p := range procs {
		want[procKey{p.PID, p.StartKey}] = true
	}
	alive := func() []int {
		ps, err := s.snapshot()
		if err != nil {
			return nil
		}
		var pids []int
		for _, p := range ps {
			if want[procKey{p.PID, p.StartKey}] {
				pids = append(pids, p.PID)
			}
		}
		return pids
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		left := alive()
		if len(left) == 0 {
			return
		}
		for _, pid := range left {
			s.kill(pid, sig)
		}
		deadline := time.Now().Add(treeGrace)
		for time.Now().Before(deadline) && len(alive()) > 0 {
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// endSession takes what session sess started with it, after berth killed
// its tmux session: its agent-browser sessions closed their own way, then
// its scope stopped or, without one, the processes found before and since.
func (s *Sessions) endSession(sess Session, found []procStat, at time.Time) {
	ctx := context.Background()
	if s.Reap != nil {
		s.Reap(ctx, sess.Name)
	}
	if sess.Scope != "" && s.Scopes != nil {
		if err := s.Scopes.Stop(ctx, sess.Scope); err == nil {
			return
		}
	}
	// What appeared since the first look, by its marker.
	if ps, err := s.snapshot(); err == nil && useMarkers {
		have := map[procKey]bool{}
		for _, p := range found {
			have[procKey{p.PID, p.StartKey}] = true
		}
		for _, p := range sessionProcs(ps, sess.Name, nil, tmuxSocketPath(), at, true) {
			if !have[procKey{p.PID, p.StartKey}] {
				found = append(found, p)
			}
		}
	}
	s.stopProcs(found)
}

// WaitCleanup waits for sessions being ended to finish cleaning up, for
// tests and shutdown.
func (s *Sessions) WaitCleanup() { s.cleanup.Wait() }
