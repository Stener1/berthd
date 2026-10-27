package box

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cosscom/shipyard/internal/statefile"
)

// A plugin can bring a helper: a program that runs on the box beside the
// worktrees' dev servers and answers an HTTP API on loopback, which the
// plugin's panel in the app reads through berthd (helpersapi.go). The
// plugin's manifest names it ("box": {"helper": …} in berth-plugin.json),
// with the SHA-256 of its build for each platform. The person allowed that
// manifest on their laptop, so those checksums are what they allowed; the
// laptop downloads the build, checks it, and uploads it here over Shipyard's
// own connection, and berthd checks it again before it keeps a byte.
//
// A helper is the box user's own program: it runs as them, with what they
// can reach. What berthd holds it to is small and exact: its files live in
// its own folder under the state directory, the program that runs is the
// one whose checksum was sent, its arguments are the manifest's and nothing
// else, and it is reached only on a loopback address it names itself.
//
//	helpers/<plugin>/install.json          what was installed, and how to run it
//	helpers/<plugin>/bin/<version>/<name>  the program
//	helpers/<plugin>/data/                 its own data; {dir} in its arguments
//	helpers/<plugin>/helper.log            what it printed (helper.log.1: before that)
//
// berthd starts every installed helper when it starts, starts one again
// when it exits, and stops them all when it stops.

const (
	// maxHelperSize bounds an uploaded helper.
	maxHelperSize = 128 << 20
	// maxHelperLog is where a helper's log starts over, the old one kept
	// once as helper.log.1.
	maxHelperLog = 4 << 20
	// HelperSpecHeader carries the HelperSpec of an upload: JSON, in
	// base64 (the body is the program itself).
	HelperSpecHeader = "X-Berth-Helper"
	// helperFailures is how many times in a row a helper has to fail
	// before it is shown as failed rather than starting again.
	helperFailures = 3
	// helperHealthy is how long a helper has to stay up for its failures
	// before that to be forgotten.
	helperHealthy = time.Minute
)

// The states of a helper on a box.
const (
	HelperOff        = "off"
	HelperInstalling = "installing"
	HelperStarting   = "starting"
	HelperRunning    = "running"
	HelperFailed     = "failed"
)

var (
	// A plugin's id and its helper's name and version become folder and
	// file names, so they are restricted rather than escaped.
	helperPlugin  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	helperName    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	helperVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$`)
	helperReady   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	helperAttach  = regexp.MustCompile(`^/[A-Za-z0-9._~/-]{0,127}$`)
	helperEnvGlob = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}\*?$`)
	helperSHA256  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// HelperPlatform names this box for a helper's build: "linux-arm64".
func HelperPlatform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// HelperAsset is one platform's build of a helper, in a plugin's manifest.
type HelperAsset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// HelperManifest is "box.helper" of a plugin's berth-plugin.json.
type HelperManifest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Args are the program's arguments; "{dir}" in one is its data
	// directory on the box.
	Args []string `json:"args"`
	// Ready names a JSON file the helper writes in {dir} once it listens,
	// with "api": "http://127.0.0.1:PORT".
	Ready string `json:"ready"`
	// Attach, when set, is the path on the helper's API berthd POSTs to
	// before it starts a worktree's service; AttachEnv the variables of
	// the service's environment it is told and may answer with, each a
	// name or a prefix ending in "*".
	Attach    string   `json:"attach,omitempty"`
	AttachEnv []string `json:"attach_env,omitempty"`
	// Detach, when set, is the path on the helper's API berthd POSTs to
	// when a worktree is removed (helperdetach.go).
	Detach string `json:"detach,omitempty"`
	// Assets are its builds, by platform ("linux-amd64").
	Assets map[string]HelperAsset `json:"assets,omitempty"`
}

// HelperSpec is a helper as a box is asked to run it: the manifest's words,
// and the size and SHA-256 of the build for this box.
type HelperSpec struct {
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Args      []string `json:"args"`
	Ready     string   `json:"ready"`
	Attach    string   `json:"attach,omitempty"`
	AttachEnv []string `json:"attach_env,omitempty"`
	Detach    string   `json:"detach,omitempty"`
	SHA256    string   `json:"sha256"`
	Size      int64    `json:"size"`
}

// Spec is the helper for one platform's build, and whether the manifest has
// such a build at all.
func (m HelperManifest) Spec(platform string) (HelperSpec, HelperAsset, bool) {
	asset, ok := m.Assets[platform]
	if !ok || asset.URL == "" {
		return HelperSpec{}, HelperAsset{}, false
	}
	return HelperSpec{Name: m.Name, Version: m.Version, Args: m.Args, Ready: m.Ready,
		Attach: m.Attach, AttachEnv: m.AttachEnv, Detach: m.Detach, SHA256: asset.SHA256, Size: asset.Size}, asset, true
}

// Validate says what is wrong with the manifest's own words: everything but
// the builds, which Spec and HelperSpec.Validate check for one platform.
func (m HelperManifest) Validate() error {
	return HelperSpec{Name: m.Name, Version: m.Version, Args: m.Args, Ready: m.Ready, Attach: m.Attach, AttachEnv: m.AttachEnv, Detach: m.Detach}.validateWords()
}

// Validate says what is wrong with a helper someone asks a box to run.
func (s HelperSpec) Validate() error {
	if err := s.validateWords(); err != nil {
		return err
	}
	if !helperSHA256.MatchString(s.SHA256) {
		return errors.New("the helper has no SHA-256 checksum")
	}
	if s.Size <= 0 || s.Size > maxHelperSize {
		return fmt.Errorf("the helper's size must be between 1 byte and %d MB", maxHelperSize>>20)
	}
	return nil
}

func (s HelperSpec) validateWords() error {
	if !helperName.MatchString(s.Name) {
		return fmt.Errorf("the helper's name %q must be lowercase letters, digits, dots and dashes", clip(s.Name, 80))
	}
	if !helperVersion.MatchString(s.Version) {
		return fmt.Errorf("the helper's version %q is not a version", clip(s.Version, 80))
	}
	if len(s.Args) > 32 {
		return errors.New("the helper has more than 32 arguments")
	}
	for _, a := range s.Args {
		if len(a) > 1024 || strings.ContainsRune(a, 0) {
			return errors.New("one of the helper's arguments is longer than 1024 bytes, or is not text")
		}
	}
	if !helperReady.MatchString(s.Ready) {
		return fmt.Errorf("the helper's ready file %q must be a plain file name", clip(s.Ready, 80))
	}
	if s.Attach != "" {
		if !helperAttach.MatchString(s.Attach) || strings.Contains(s.Attach, "//") || strings.Contains(s.Attach, "..") {
			return fmt.Errorf("the helper's attach path %q must be a plain path such as /api/attach", clip(s.Attach, 80))
		}
	}
	if s.Detach != "" {
		if !helperAttach.MatchString(s.Detach) || strings.Contains(s.Detach, "//") || strings.Contains(s.Detach, "..") {
			return fmt.Errorf("the helper's detach path %q must be a plain path such as /api/detach", clip(s.Detach, 80))
		}
	}
	if len(s.AttachEnv) > 32 {
		return errors.New("the helper names more than 32 variables to attach with")
	}
	for _, p := range s.AttachEnv {
		if !helperEnvGlob.MatchString(p) {
			return fmt.Errorf("%q in the helper's attach_env must be a variable's name, or a prefix ending in *", clip(p, 80))
		}
	}
	return nil
}

// matchesEnv reports whether a variable is one the helper named in
// attach_env: by its name, or by a prefix ending in "*".
func (s HelperSpec) matchesEnv(name string) bool {
	for _, p := range s.AttachEnv {
		if prefix, glob := strings.CutSuffix(p, "*"); glob && strings.HasPrefix(name, prefix) || p == name {
			return true
		}
	}
	return false
}

// Digest names a spec by everything in it: the program's checksum and how
// it is run. Two specs with one digest are one helper, run one way.
func (s HelperSpec) Digest() string {
	if s.Args == nil {
		s.Args = []string{}
	}
	if len(s.AttachEnv) == 0 {
		s.AttachEnv = nil
	}
	b, _ := json.Marshal(s)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// HelperStatus is one plugin's helper on this box.
type HelperStatus struct {
	Plugin string `json:"plugin"`
	// State is off (nothing installed), installing, starting, running or
	// failed. A failed helper is still started again, less and less often.
	State    string `json:"state"`
	Platform string `json:"platform"`
	Name     string `json:"name,omitempty"`
	Version  string `json:"version,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	// Digest is HelperSpec.Digest of what was installed: a laptop compares
	// it with its plugin's manifest to tell whether this is that helper.
	Digest string `json:"digest,omitempty"`
	PID    int    `json:"pid,omitempty"`
	// Since is when it came to be in this state.
	Since *time.Time `json:"since,omitempty"`
	// Restarts counts the times berthd has started it again.
	Restarts int `json:"restarts,omitempty"`
	// Attach: berthd asks it before a worktree's service starts.
	Attach bool   `json:"attach,omitempty"`
	Error  string `json:"error,omitempty"`
	// Attached, in the answer to removing a helper, are the services
	// running now that were started with what the helper answered: they
	// still point at it, and need starting again to be as the project
	// wrote them.
	Attached []HelperService `json:"attached,omitempty"`
}

// HelperService names a worktree's service a helper changed the environment
// of when it started.
type HelperService struct {
	Location string `json:"location"`
	Worktree string `json:"worktree"`
	Service  string `json:"service"`
}

// HelperList is GET /v1/helpers.
type HelperList struct {
	// Platform is the build this box runs: "linux-arm64".
	Platform string         `json:"platform"`
	Helpers  []HelperStatus `json:"helpers"`
}

// helperRecord is install.json: what was installed for a plugin.
type helperRecord struct {
	Plugin string `json:"plugin"`
	HelperSpec
	Installed time.Time `json:"installed"`
}

// helperProc is one installed helper and the goroutine that keeps it up.
type helperProc struct {
	rec  helperRecord
	stop chan struct{} // closed to stop it
	done chan struct{} // closed once it has stopped

	mu       sync.Mutex
	state    string
	since    time.Time
	pid      int
	api      *url.URL
	restarts int
	err      string
}

func (p *helperProc) set(state string, pid int, api *url.URL, err string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != state {
		p.since = time.Now()
	}
	p.state, p.pid, p.api, p.err = state, pid, api, err
}

// started notes that the helper's program was started. One that is shown as
// failed stays so, with why, while berthd tries it again: it is running
// again when it says it is ready, and not before.
func (p *helperProc) started(pid int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == HelperFailed {
		return
	}
	if p.state != HelperStarting {
		p.since = time.Now()
	}
	p.state, p.pid, p.api, p.err = HelperStarting, pid, nil, ""
}

func (p *helperProc) snapshot() (state string, api *url.URL) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state, p.api
}

// Helpers installs and runs the helpers of plugins on this box.
type Helpers struct {
	// Dir is the helpers folder in berthd's state directory.
	Dir string
	// Box is the box's name, which a helper is told.
	Box string
	// Logf, when set, takes a line when a helper's state changes.
	Logf func(format string, args ...any)

	// These are set by tests; zero is the default.
	readyTimeout  time.Duration             // how long a helper has to say it is ready (30s)
	stopTimeout   time.Duration             // how long one has to stop before it is killed (5s)
	attachTimeout time.Duration             // how long one has to answer an attach (2s)
	detachTimeout time.Duration             // how long one has to answer a detach (2s)
	backoff       func(n int) time.Duration // the wait before start n+1, after n failures

	mu        sync.Mutex
	procs     map[string]*helperProc
	uploading map[string]int
	stopped   bool
	// change serialises installing and removing.
	change sync.Mutex
	// attachMu guards each helper's attached.json.
	attachMu sync.Mutex
	// detaching is the helpers being told of removed worktrees
	// (helperdetach.go).
	detaching sync.WaitGroup
}

func (h *Helpers) logf(format string, args ...any) {
	if h.Logf != nil {
		h.Logf(format, args...)
	}
}

func orDuration(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// restartDelay is how long to wait before starting a helper that has failed
// n times in a row: 1s, 2s, 4s … up to a minute.
func (h *Helpers) restartDelay(n int) time.Duration {
	if h.backoff != nil {
		return h.backoff(n)
	}
	d := time.Second << min(max(n-1, 0), 6)
	return min(d, time.Minute)
}

func (h *Helpers) home(plugin string) string    { return filepath.Join(h.Dir, plugin) }
func (h *Helpers) dataDir(plugin string) string { return filepath.Join(h.Dir, plugin, "data") }
func (h *Helpers) logPath(plugin string) string { return filepath.Join(h.Dir, plugin, "helper.log") }
func (h *Helpers) recordPath(plugin string) string {
	return filepath.Join(h.Dir, plugin, "install.json")
}

func (h *Helpers) attachedPath(plugin string) string {
	return filepath.Join(h.Dir, plugin, "attached.json")
}

// attachedTo is the services a helper last changed the environment of. It
// is kept in a file, so berthd still knows after it restarts which services
// point at the helper.
func (h *Helpers) attachedTo(plugin string) []HelperService {
	h.attachMu.Lock()
	defer h.attachMu.Unlock()
	return h.readAttached(plugin)
}

func (h *Helpers) readAttached(plugin string) []HelperService {
	var list []HelperService
	if b, err := os.ReadFile(h.attachedPath(plugin)); err == nil {
		json.Unmarshal(b, &list)
	}
	return list
}

// noteAttached remembers whether a service was started with what a helper
// answered (changed), or as the project wrote it.
func (h *Helpers) noteAttached(plugin string, svc HelperService, changed bool) {
	h.attachMu.Lock()
	defer h.attachMu.Unlock()
	before := h.readAttached(plugin)
	had := false
	list := make([]HelperService, 0, len(before)+1)
	for _, s := range before {
		if s == svc {
			had = true
			continue
		}
		list = append(list, s)
	}
	if had == changed {
		return
	}
	if changed {
		list = append(list, svc)
	}
	h.saveAttached(plugin, list)
}

func (h *Helpers) binPath(rec helperRecord) string {
	return filepath.Join(h.Dir, rec.Plugin, "bin", rec.Version, rec.Name)
}

// readRecord reads what was installed for a plugin. The file is checked as
// an upload is: nothing in it is taken on trust because it is on disk.
func (h *Helpers) readRecord(plugin string) (helperRecord, error) {
	var rec helperRecord
	b, err := os.ReadFile(h.recordPath(plugin))
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return rec, err
	}
	if rec.Plugin != plugin {
		return rec, fmt.Errorf("it names the plugin %q", clip(rec.Plugin, 80))
	}
	return rec, rec.Validate()
}

// Start starts every installed helper, and waits a few seconds for them to
// say they are ready, so the first service berthd starts finds them up. It
// first ends what an earlier berthd left running: two of one helper must
// not share a data directory.
func (h *Helpers) Start() {
	if !helperPlatformOK() {
		return
	}
	entries, err := os.ReadDir(h.Dir)
	if err != nil {
		return
	}
	h.sweep("")
	var started []*helperProc
	for _, e := range entries {
		plugin := e.Name()
		if !e.IsDir() || !helperPlugin.MatchString(plugin) {
			continue
		}
		rec, err := h.readRecord(plugin)
		if errors.Is(err, os.ErrNotExist) {
			continue // only its data is left
		}
		if err != nil {
			h.logf("helper %s: not started: its install.json cannot be used: %v", plugin, err)
			continue
		}
		if p := h.launch(rec); p != nil {
			started = append(started, p)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for _, p := range started {
		p.waitStarted(context.Background(), time.Until(deadline))
	}
}

// launch starts keeping a helper up. The caller holds no lock.
func (h *Helpers) launch(rec helperRecord) *helperProc {
	p := &helperProc{rec: rec, stop: make(chan struct{}), done: make(chan struct{}), state: HelperStarting, since: time.Now()}
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return nil
	}
	if h.procs == nil {
		h.procs = map[string]*helperProc{}
	}
	h.procs[rec.Plugin] = p
	h.mu.Unlock()
	go h.supervise(p)
	return p
}

// waitStarted waits until the helper is no longer starting, for at most d.
func (p *helperProc) waitStarted(ctx context.Context, d time.Duration) {
	deadline := time.NewTimer(max(d, 0))
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if state, _ := p.snapshot(); state != HelperStarting {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-p.done:
			return
		case <-tick.C:
		}
	}
}

// halt stops a helper and waits until it has gone.
func (h *Helpers) halt(plugin string) {
	h.mu.Lock()
	p := h.procs[plugin]
	delete(h.procs, plugin)
	h.mu.Unlock()
	if p == nil {
		return
	}
	close(p.stop)
	<-p.done
}

// Stop stops every helper, when berthd stops or is about to be replaced by
// a new build. Nothing can be installed after it.
func (h *Helpers) Stop() {
	h.mu.Lock()
	h.stopped = true
	procs := h.procs
	h.procs = nil
	h.mu.Unlock()
	for _, p := range procs {
		close(p.stop)
	}
	for _, p := range procs {
		<-p.done
	}
	h.detaching.Wait()
}

// supervise runs a helper until it is told to stop, starting it again each
// time it exits: at once the first time, then less and less often.
func (h *Helpers) supervise(p *helperProc) {
	defer close(p.done)
	plugin := p.rec.Plugin
	failures := 0
	for {
		began := time.Now()
		err := h.runOnce(p)
		select {
		case <-p.stop:
			p.set(HelperOff, 0, nil, "")
			return
		default:
		}
		if time.Since(began) > helperHealthy {
			failures = 0
		}
		failures++
		delay := h.restartDelay(failures)
		state, msg := HelperStarting, ""
		if err != nil {
			msg = err.Error()
		}
		if failures >= helperFailures {
			state = HelperFailed
			msg = fmt.Sprintf("%s; berthd tries again in %s", msg, delay.Round(time.Second))
		}
		p.set(state, 0, nil, msg)
		// One that keeps failing is said to, once: not every minute for as
		// long as it is installed.
		switch {
		case failures < helperFailures:
			h.logf("helper %s: %v; starting it again in %s", plugin, err, delay)
		case failures == helperFailures:
			h.logf("helper %s: %v; it keeps failing, and is still started again, less and less often. Its log: %s", plugin, err, h.logPath(plugin))
		}
		select {
		case <-p.stop:
			p.set(HelperOff, 0, nil, "")
			return
		case <-time.After(delay):
		}
		p.mu.Lock()
		p.restarts++
		p.mu.Unlock()
	}
}

// helperEnv is the environment a helper runs in: enough to find its way
// about, and nothing berthd itself was told that a plugin has no business
// with. BERTH_HELPER and BERTH_HELPER_HOME mark the process as this
// berthd's helper, which is how one left running is found again.
func (h *Helpers) helperEnv(rec helperRecord) []string {
	var env []string
	for _, k := range []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "TMPDIR", "TZ"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return append(env,
		"BERTH_HELPER="+rec.Plugin,
		"BERTH_HELPER_HOME="+h.Dir,
		"BERTH_HELPER_DIR="+h.dataDir(rec.Plugin),
		"BERTH_BOX="+h.Box,
	)
}

// program is the path of a helper's program, after checking it is still
// the one that was installed: a plain file inside the helper's own folder
// with the checksum the laptop sent.
func (h *Helpers) program(rec helperRecord) (string, error) {
	bin := h.binPath(rec)
	fi, err := os.Lstat(bin)
	if err != nil {
		return "", errors.New("its program is missing from the box")
	}
	if !fi.Mode().IsRegular() {
		return "", errors.New("its program is not a plain file")
	}
	home, err := filepath.EvalSymlinks(h.home(rec.Plugin))
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(real, home+string(filepath.Separator)) {
		return "", errors.New("its program is outside its own folder")
	}
	sum, size, err := hashFile(bin)
	if err != nil {
		return "", err
	}
	if sum != rec.SHA256 || size != rec.Size {
		return "", errors.New("its program on the box is no longer the one that was installed; install it again")
	}
	return bin, nil
}

func hashFile(path string) (sum string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	hash := sha256.New()
	if size, err = io.Copy(hash, f); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

// runOnce runs a helper's program once, from its start to its end. It
// returns nil when it stopped the program because it was told to.
func (h *Helpers) runOnce(p *helperProc) error {
	rec := p.rec
	bin, err := h.program(rec)
	if err != nil {
		return err
	}
	data := h.dataDir(rec.Plugin)
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	// The file a helper that was killed left behind says nothing of this one.
	os.Remove(filepath.Join(data, rec.Ready))
	args := make([]string, len(rec.Args))
	for i, a := range rec.Args {
		args[i] = strings.ReplaceAll(a, "{dir}", data)
	}
	out, err := openHelperLog(h.logPath(rec.Plugin))
	if err != nil {
		return err
	}
	defer out.Close()
	cmd := exec.Command(bin, args...)
	cmd.Dir = data
	cmd.Env = h.helperEnv(rec)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = helperSysProcAttr()
	// Something the helper started may hold its output open after it.
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("it could not be started: %v", pathless(err))
	}
	pid := cmd.Process.Pid
	fmt.Fprintf(out, "%s berthd: started %s %s (pid %d)\n", time.Now().UTC().Format(time.RFC3339), rec.Name, rec.Version, pid)
	p.started(pid)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	stopNow := func() {
		terminateHelper(pid, exited, orDuration(h.stopTimeout, 5*time.Second))
		fmt.Fprintf(out, "%s berthd: stopped %s\n", time.Now().UTC().Format(time.RFC3339), rec.Name)
	}

	ready := time.NewTimer(orDuration(h.readyTimeout, 30*time.Second))
	defer ready.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	var notReady error
waiting:
	for {
		select {
		case err := <-exited:
			return exitReason(err, "before it was ready")
		case <-p.stop:
			stopNow()
			return nil
		case <-ready.C:
			stopNow()
			if notReady != nil {
				return notReady
			}
			return fmt.Errorf("it did not write %s to say it was ready", rec.Ready)
		case <-tick.C:
			api, token, err := readHelperReady(data, rec.Ready)
			if err != nil {
				notReady = err
				continue
			}
			if api != nil {
				// Before it is shown as running: the first request of it
				// already carries its token.
				holdHelperToken(api, token)
				defer holdHelperToken(api, "")
				p.set(HelperRunning, pid, api, "")
				h.logf("helper %s: %s %s is running (pid %d)", rec.Plugin, rec.Name, rec.Version, pid)
				break waiting
			}
		}
	}
	select {
	case err := <-exited:
		return exitReason(err, "")
	case <-p.stop:
		stopNow()
		return nil
	}
}

func exitReason(err error, when string) error {
	what := "it exited"
	if err != nil {
		what = "it ended (" + err.Error() + ")"
	}
	if when != "" {
		what += " " + when
	}
	return errors.New(what)
}

// pathless is an error without the path a *PathError repeats.
func pathless(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// readHelperReady reads the file a helper writes once it listens. It
// returns no address while the file is not there, and an error for one that
// names anything but a loopback address: berthd relays to the helper, and
// must not be pointed anywhere else.
//
// token is the file's "token", for a helper that has one: what berthd sends
// it as "Authorization: Bearer" with every request (helperAuth).
func readHelperReady(data, name string) (api *url.URL, token string, err error) {
	root, err := os.OpenRoot(data)
	if err != nil {
		return nil, "", nil
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		return nil, "", nil
	}
	defer f.Close()
	var doc struct {
		API   string `json:"api"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(f, 64<<10)).Decode(&doc); err != nil {
		// Half written: it is read again in a moment.
		return nil, "", nil
	}
	u, err := url.Parse(doc.API)
	if err != nil || u.Scheme != "http" || u.Port() == "" || !loopbackName(u.Hostname()) {
		return nil, "", fmt.Errorf("its %s does not name an API on this machine (http://127.0.0.1:PORT)", name)
	}
	if doc.Token != "" && !helperToken.MatchString(doc.Token) {
		// Never the token itself.
		return nil, "", fmt.Errorf("its %s has a token that cannot be sent in a header", name)
	}
	return &url.URL{Scheme: "http", Host: u.Host}, doc.Token, nil
}

func loopbackName(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sweep ends the helpers of this folder that are running with no berthd
// keeping them: left by one that was killed. plugin "" is all of them.
func (h *Helpers) sweep(plugin string) {
	procs, err := scanProcs("BERTH_HELPER=", "BERTH_HELPER_HOME=")
	if err != nil {
		return
	}
	var pids []int
	for _, p := range procs {
		if p.PID == os.Getpid() || p.Env["BERTH_HELPER_HOME"] != h.Dir {
			continue
		}
		if plugin != "" && p.Env["BERTH_HELPER"] != plugin {
			continue
		}
		pids = append(pids, p.PID)
	}
	if len(pids) == 0 {
		return
	}
	h.logf("helpers: ending %d left running by an earlier berthd", len(pids))
	for _, pid := range pids {
		signalProcess(pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(orDuration(h.stopTimeout, 5*time.Second))
	for time.Now().Before(deadline) {
		alive := false
		for _, pid := range pids {
			alive = alive || processAlive(pid)
		}
		if !alive {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, pid := range pids {
		signalProcess(pid, syscall.SIGKILL)
	}
}

// Status is one plugin's helper.
func (h *Helpers) Status(plugin string) HelperStatus {
	st := HelperStatus{Plugin: plugin, State: HelperOff, Platform: HelperPlatform()}
	h.mu.Lock()
	p := h.procs[plugin]
	uploading := h.uploading[plugin] > 0
	h.mu.Unlock()
	if p == nil {
		if uploading {
			st.State = HelperInstalling
		}
		return st
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	since := p.since
	st.State, st.PID, st.Since, st.Restarts, st.Error = p.state, p.pid, &since, p.restarts, p.err
	st.Name, st.Version, st.SHA256, st.Attach = p.rec.Name, p.rec.Version, p.rec.SHA256, p.rec.Attach != ""
	st.Digest = p.rec.HelperSpec.Digest()
	return st
}

// List is every installed helper, by plugin.
func (h *Helpers) List() HelperList {
	h.mu.Lock()
	plugins := make([]string, 0, len(h.procs))
	for plugin := range h.procs {
		plugins = append(plugins, plugin)
	}
	h.mu.Unlock()
	sort.Strings(plugins)
	out := HelperList{Platform: HelperPlatform(), Helpers: []HelperStatus{}}
	for _, plugin := range plugins {
		if st := h.Status(plugin); st.State != HelperOff {
			out.Helpers = append(out.Helpers, st)
		}
	}
	return out
}

// API is where a running helper answers, and whether it is running.
func (h *Helpers) API(plugin string) (*url.URL, bool) {
	h.mu.Lock()
	p := h.procs[plugin]
	h.mu.Unlock()
	if p == nil {
		return nil, false
	}
	state, api := p.snapshot()
	return api, state == HelperRunning && api != nil
}

// helperProblem is an error of a helper's routes, with the code a client
// branches on. (reviewCode is the box's error that carries its own code.)
func helperProblem(status int, code, format string, args ...any) error {
	return reviewCode{status, code, fmt.Sprintf(format, args...)}
}

// Install keeps an uploaded helper for a plugin and starts it, in place of
// whatever that plugin had running. program is the build itself; with none
// (nil, or empty) the box must already hold a program of that checksum,
// which is then started again with the spec given. Nothing is kept unless
// the bytes are exactly the size and SHA-256 the spec names.
func (h *Helpers) Install(ctx context.Context, plugin string, spec HelperSpec, program io.Reader) (HelperStatus, error) {
	if !helperPlatformOK() {
		return HelperStatus{}, helperProblem(http.StatusNotImplemented, CodeUnsupported, "this box cannot run helpers")
	}
	if !helperPlugin.MatchString(plugin) {
		return HelperStatus{}, helperProblem(http.StatusBadRequest, "helper_invalid", "%q is not a plugin's id", clip(plugin, 80))
	}
	if err := spec.Validate(); err != nil {
		return HelperStatus{}, helperProblem(http.StatusBadRequest, "helper_invalid", "%v", err)
	}
	home := h.home(plugin)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return HelperStatus{}, err
	}
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return HelperStatus{}, helperProblem(http.StatusServiceUnavailable, CodeInternal, "berthd is stopping")
	}
	if h.uploading == nil {
		h.uploading = map[string]int{}
	}
	h.uploading[plugin]++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.uploading[plugin]--
		h.mu.Unlock()
	}()

	// The upload lands beside where it will live, so moving it in is one
	// step, and is checked before anything running is touched.
	upload, got, err := receiveHelper(home, spec, program)
	if err != nil {
		// A folder made for an upload that was refused is not left behind.
		os.Remove(home)
		return HelperStatus{}, err
	}
	if upload != "" {
		defer os.Remove(upload)
	}
	p, err := h.swapIn(plugin, spec, upload, got)
	if err != nil {
		os.Remove(home)
		return HelperStatus{}, err
	}
	// Whoever asked mostly wants to know it came up. (No p: what was asked
	// for is what runs, and it was left running.)
	if p != nil {
		p.waitStarted(ctx, 10*time.Second)
	}
	return h.Status(plugin), nil
}

// swapIn puts an uploaded program (or, with none, the one the box holds) in
// the place of whatever the plugin had running, and starts it. It returns no
// process when what was asked for already runs.
func (h *Helpers) swapIn(plugin string, spec HelperSpec, upload string, got bool) (*helperProc, error) {
	h.change.Lock()
	defer h.change.Unlock()
	home := h.home(plugin)
	rec := helperRecord{Plugin: plugin, HelperSpec: spec, Installed: time.Now().UTC()}
	if !got {
		// No bytes were sent: the box has to hold that very program.
		old, err := h.readRecord(plugin)
		if err != nil || old.SHA256 != spec.SHA256 || old.Version != spec.Version || old.Name != spec.Name {
			return nil, helperProblem(http.StatusConflict, "helper_missing", "this box does not hold %s %s; send the program itself", spec.Name, spec.Version)
		}
		if _, err := h.program(rec); err != nil {
			return nil, helperProblem(http.StatusConflict, "helper_missing", "this box's %s cannot be used: %v", spec.Name, err)
		}
		// Asked for exactly what is running: it is left running. Stopping
		// a helper cuts what goes through it, so it is not done for nothing.
		if st := h.Status(plugin); st.State == HelperRunning && st.Digest == spec.Digest() {
			return nil, nil
		}
	}
	h.halt(plugin)
	if got {
		bin := h.binPath(rec)
		// One version is kept: the folders of the others go.
		if err := os.RemoveAll(filepath.Join(home, "bin")); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
			return nil, err
		}
		if err := os.Rename(upload, bin); err != nil {
			return nil, err
		}
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := statefile.Write(h.recordPath(plugin), append(b, '\n')); err != nil {
		return nil, err
	}
	p := h.launch(rec)
	if p == nil {
		return nil, helperProblem(http.StatusServiceUnavailable, CodeInternal, "berthd is stopping")
	}
	h.logf("helper %s: installed %s %s", plugin, spec.Name, spec.Version)
	return p, nil
}

// receiveHelper writes an uploaded program into a new file in home, for its
// owner to run and nobody else, and checks its size and checksum. got is
// false, and nothing was written, when no bytes were sent.
func receiveHelper(home string, spec HelperSpec, program io.Reader) (path string, got bool, err error) {
	if program == nil {
		return "", false, nil
	}
	f, err := os.CreateTemp(home, ".upload-*")
	if err != nil {
		return "", false, err
	}
	path = f.Name()
	fail := func(err error) (string, bool, error) {
		f.Close()
		os.Remove(path)
		return "", false, err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(program, maxHelperSize+1))
	if err != nil {
		return fail(err)
	}
	if n == 0 {
		return fail(nil)
	}
	if n != spec.Size {
		return fail(helperProblem(http.StatusBadRequest, "helper_mismatch", "the upload is %d bytes, not the %d the plugin's manifest gives; nothing was installed", n, spec.Size))
	}
	if hex.EncodeToString(hash.Sum(nil)) != spec.SHA256 {
		return fail(helperProblem(http.StatusBadRequest, "helper_mismatch", "the upload does not match the checksum in the plugin's manifest; nothing was installed"))
	}
	if err := f.Chmod(0o700); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", false, err
	}
	return path, true, nil
}

// Remove stops a plugin's helper and removes its program. Its data and its
// log stay for the next time unless purge, which removes the whole folder.
//
// berthd's own note of the services the helper changed (attached.json)
// outlives both: those services still run with what the helper answered,
// and a helper turned on again and off again has to be able to say so.
func (h *Helpers) Remove(plugin string, purge bool) (HelperStatus, error) {
	if !helperPlugin.MatchString(plugin) {
		return HelperStatus{}, helperProblem(http.StatusBadRequest, "helper_invalid", "%q is not a plugin's id", clip(plugin, 80))
	}
	h.change.Lock()
	defer h.change.Unlock()
	h.halt(plugin)
	home := h.home(plugin)
	var err error
	if purge {
		h.attachMu.Lock()
		attached := h.readAttached(plugin)
		if err = os.RemoveAll(home); err == nil {
			h.saveAttached(plugin, attached)
		}
		h.attachMu.Unlock()
	} else {
		err = errors.Join(os.RemoveAll(filepath.Join(home, "bin")), removeIfThere(h.recordPath(plugin)))
		// A folder with nothing in it is no data worth keeping.
		os.Remove(home)
	}
	if err != nil {
		return HelperStatus{}, err
	}
	h.logf("helper %s: removed (data %s)", plugin, map[bool]string{true: "removed", false: "kept"}[purge])
	return h.Status(plugin), nil
}

func removeIfThere(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Tail is the end of a helper's log.
func (h *Helpers) Tail(plugin string, limit int64) ([]byte, error) {
	if !helperPlugin.MatchString(plugin) {
		return nil, helperProblem(http.StatusBadRequest, "helper_invalid", "%q is not a plugin's id", clip(plugin, 80))
	}
	f, err := os.Open(h.logPath(plugin))
	if errors.Is(err, os.ErrNotExist) {
		return nil, helperProblem(http.StatusNotFound, CodeNotFound, "the %s helper has written no log on this box", plugin)
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		if _, err := f.Seek(info.Size()-limit, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(io.LimitReader(f, limit))
}

// helperLog is a helper's log file, which starts over once it is large:
// a helper runs for weeks, and nothing else trims what it prints.
type helperLog struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

func openHelperLog(path string) (*helperLog, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	l := &helperLog{path: path, f: f}
	if fi, err := f.Stat(); err == nil {
		l.size = fi.Size()
	}
	return l, nil
}

func (l *helperLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return len(p), nil
	}
	if l.size+int64(len(p)) > maxHelperLog {
		l.f.Close()
		os.Rename(l.path, l.path+".1")
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			// The helper must not stop because its log cannot be written.
			l.f = nil
			return len(p), nil
		}
		l.f, l.size = f, 0
	}
	n, _ := l.f.Write(p)
	l.size += int64(n)
	return len(p), nil
}

func (l *helperLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
