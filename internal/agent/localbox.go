package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sean-brydon/berthd/internal/identity"
	"github.com/sean-brydon/berthd/internal/service"
	"github.com/sean-brydon/berthd/internal/statefile"
	"github.com/sean-brydon/berthd/internal/trust"
	"github.com/sean-brydon/berthd/internal/version"
)

// Use this Mac: the laptop as a box of its own, set up from the app with no
// terminal. The app carries a berthd for macOS (Berth.app/Contents/Resources
// /berthd; bin/berthd beside bin/berth in a checkout). Setting up copies it
// to BERTH_HOME/bin/berthd, which on a Mac is
// ~/Library/Application Support/berth/bin/berthd, and runs `berthd install`
// from there listening on loopback only, then pairs this laptop with it
// through `berthd pair --json` and `berth pair`: nothing to copy or paste.
//
// Why that copy, and why there: launchd re-runs the program its plist
// names, and a path inside Berth.app changes or disappears when the app is
// updated, moved or run from its disk image. Application Support is where a
// Mac app keeps what it manages for itself, beside the box's own state
// (BERTH_HOME/box). It is deliberately not ~/.local/bin, where the install
// script puts the berthd a person manages by hand: Berth never shadows or
// overwrites that one. When the install script did put berthd on this Mac,
// that install is found by its service (dev.berth.berthd) and reused, not
// installed a second time.
//
// The agent keeps the copy current: at start, then every few minutes, it
// compares the berthd the app carries with the copy (by build ID) and, when
// the app was updated, replaces the copy and re-runs `berthd install
// --keep-listen`. Agent sessions keep running through it.

// The first port tried for this Mac's berthd. 7444, berthd's usual port, is
// often held by another berthd on a developer's Mac; Berth never takes it.
const defaultLocalBoxPort = 7445

// How often the agent checks whether the app now carries a newer berthd.
const localRefreshEvery = 10 * time.Minute

// These are replaced in tests.
var (
	localGOOS = runtime.GOOS
	// localTool runs the platform tools the copy is checked with: codesign
	// and xattr.
	localTool = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
)

// localBoxRecord is the box this agent set up on its own computer, kept in
// Dir/localbox.json.
type localBoxRecord struct {
	Name        string `json:"name,omitempty"`
	Fingerprint string `json:"fingerprint"`
	// Program is the berthd the service runs, and Home its BERTH_HOME.
	Program string `json:"program"`
	Home    string `json:"home"`
	// Owned is whether Berth put Program there (BERTH_HOME/bin/berthd), as
	// opposed to reusing a berthd the install script installed.
	Owned bool `json:"owned"`
}

type localBox struct {
	// mu holds one set up, removal or refresh at a time.
	mu sync.Mutex

	recMu  sync.Mutex
	loaded bool
	rec    *localBoxRecord
	// seen is the bundled berthd last compared with the installed one, by
	// size and modification time, so the periodic check reads and hashes it
	// only after the app was updated.
	seen string
}

// LocalBoxStatus answers GET /v1/boxes/local: whether this computer can be
// a box, and how far it is.
type LocalBoxStatus struct {
	// Supported is false on a platform berthd does not run on as a service.
	Supported bool `json:"supported"`
	// Available is whether there is a berthd to install; Reason says why not.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// Installed: a berthd service is installed for this user. Owned: Berth
	// installed it, rather than the install script.
	Installed bool   `json:"installed"`
	Owned     bool   `json:"owned"`
	Running   bool   `json:"running"`
	Listen    string `json:"listen,omitempty"`
	Program   string `json:"program,omitempty"`
	// Box is the name this laptop knows it by, once paired.
	Box string `json:"box,omitempty"`
	// Name is what setting up will call it: this computer's hostname.
	Name string `json:"name"`
}

type sayFunc func(format string, args ...any)

func (a *Agent) localRecordPath() string { return filepath.Join(a.cfg.Dir, "localbox.json") }

func (a *Agent) localRecord() *localBoxRecord {
	a.local.recMu.Lock()
	defer a.local.recMu.Unlock()
	if !a.local.loaded {
		a.local.loaded = true
		if b, err := os.ReadFile(a.localRecordPath()); err == nil {
			var r localBoxRecord
			if json.Unmarshal(b, &r) == nil && r.Fingerprint != "" {
				a.local.rec = &r
			}
		}
	}
	if a.local.rec == nil {
		return nil
	}
	r := *a.local.rec
	return &r
}

func (a *Agent) saveLocalRecord(r *localBoxRecord) error {
	a.local.recMu.Lock()
	defer a.local.recMu.Unlock()
	a.local.loaded = true
	if r == nil {
		a.local.rec = nil
		if err := os.Remove(a.localRecordPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := statefile.Write(a.localRecordPath(), append(b, '\n')); err != nil {
		return err
	}
	c := *r
	a.local.rec = &c
	return nil
}

// isLocal is whether a paired box is the one Use this Mac set up on this
// computer. A berthd reached over loopback for another reason (one run by
// hand for testing, say) is not: removing "this Mac's box" must mean the
// service this agent manages.
func (a *Agent) isLocal(p trust.Peer) bool {
	r := a.localRecord()
	return r != nil && r.Fingerprint == p.Fingerprint.String()
}

var errNoBerthd = errors.New("this copy of Berth carries no berthd for this computer; install Berth from its disk image, or build one with make build")

// bundledBerthd is the berthd the app carries: Config.Berthd (tests), else,
// when this agent is the berth inside Berth.app, only the app's own
// Contents/Resources/berthd (checkBundled holds it to the app's signature),
// else the berthd beside this berth (bin/ in a checkout). Nothing on PATH
// or in another folder is ever considered (security audit M-4).
func (a *Agent) bundledBerthd() (string, error) {
	if a.cfg.Berthd != "" {
		if isFile(a.cfg.Berthd) {
			return a.cfg.Berthd, nil
		}
		return "", errNoBerthd
	}
	exe, err := a.cliPath()
	if err != nil {
		return "", errNoBerthd
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	p := filepath.Join(dir, "berthd")
	if strings.HasSuffix(dir, ".app/Contents/MacOS") {
		p = filepath.Join(dir, "..", "Resources", "berthd")
	}
	if isFile(p) {
		return filepath.Clean(p), nil
	}
	return "", errNoBerthd
}

// checkBundled refuses a bundled berthd that is not signed by the team that
// signed this agent: the app's files are the person's to change, and a
// berthd swapped into them would run as a launch agent. A build with no
// team (unsigned, from a checkout or make app-build) has nothing to compare.
func (a *Agent) checkBundled(ctx context.Context, src string) error {
	if localGOOS != "darwin" {
		return nil
	}
	exe, err := a.cliPath()
	if err != nil {
		return nil
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	out, _ := localTool(ctx, "/usr/bin/codesign", "-dv", "--verbose=2", exe)
	team := ""
	for _, l := range strings.Split(string(out), "\n") {
		if t, ok := strings.CutPrefix(strings.TrimSpace(l), "TeamIdentifier="); ok && t != "not set" {
			team = t
		}
	}
	if team == "" {
		return nil
	}
	req := fmt.Sprintf("=anchor apple generic and certificate leaf[subject.OU] = %q", team)
	if out, err := localTool(ctx, "/usr/bin/codesign", "--verify", "--strict", "-R", req, src); err != nil {
		return fmt.Errorf("%s is not signed by Berth's team (%s), so Berth will not install it; reinstall Berth from its disk image", src, strings.TrimSpace(string(out)))
	}
	return nil
}

// stableBerthd is where Berth keeps its own copy of berthd for this
// computer's box.
func stableBerthd(home string) string { return filepath.Join(home, "bin", "berthd") }

func (a *Agent) localHome() string { return filepath.Dir(a.cfg.Dir) }

func (a *Agent) localPort() int {
	if a.cfg.LocalBoxPort > 0 {
		return a.cfg.LocalBoxPort
	}
	return defaultLocalBoxPort
}

func localName() string {
	hostname, _ := os.Hostname()
	return trust.NameFromHostname(hostname, "this-mac")
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// serving is whether a berthd answers on the local socket under home.
// Tests replace it.
var serving = func(home string) bool {
	c, err := net.DialTimeout("unix", filepath.Join(home, "box", "berthd.sock"), time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// installedUnit reads the berthd service installed for this user, taking
// its BERTH_HOME when it names one.
func (a *Agent) installedUnit() (unit service.Unit, have bool, home string, err error) {
	home = a.localHome()
	unit, have, err = service.Read(service.BerthdName())
	if have {
		if h := unit.Env["BERTH_HOME"]; filepath.IsAbs(h) {
			home = h
		}
	}
	return unit, have, home, err
}

func (a *Agent) localBoxStatus() LocalBoxStatus {
	st := LocalBoxStatus{Supported: localGOOS == "darwin" || localGOOS == "linux", Name: localName()}
	if _, err := a.bundledBerthd(); err != nil {
		st.Reason = err.Error()
	} else {
		st.Available = st.Supported
	}
	if !st.Supported {
		st.Reason = fmt.Sprintf("berthd runs as a service on macOS and Linux, not %s", localGOOS)
	}
	unit, have, home, err := a.installedUnit()
	if err == nil && have {
		st.Installed = true
		st.Program = unit.Program
		st.Listen = unit.Arg("--listen")
		st.Owned = unit.Program == stableBerthd(a.localHome())
		st.Running = serving(home)
	}
	if r := a.localRecord(); r != nil {
		if fp, err := identity.ParseFingerprint(r.Fingerprint); err == nil {
			if peer, ok, _ := a.boxes.Trusted(fp); ok {
				st.Box = peer.Name
			}
		}
	}
	return st
}

// setUpLocalBox makes this computer a box and pairs with it, saying what it
// does as it goes. Run again, it reconnects: each step is skipped when it is
// already done.
func (a *Agent) setUpLocalBox(ctx context.Context, say sayFunc) (string, error) {
	if localGOOS != "darwin" && localGOOS != "linux" {
		return "", fmt.Errorf("berthd runs as a service on macOS and Linux, not %s", localGOOS)
	}
	src, err := a.bundledBerthd()
	if err != nil {
		return "", err
	}
	if err := a.checkBundled(ctx, src); err != nil {
		return "", err
	}
	stable := stableBerthd(a.localHome())
	prog, owned := stable, true
	unit, have, home, err := a.installedUnit()
	if err != nil {
		say("The installed berthd service can't be read (%v); Berth replaces it.", err)
		have, home = false, a.localHome()
	}
	if have && unit.Program != stable {
		if isFile(unit.Program) {
			// The install script (or berth add ssh) installed berthd here
			// already: one berthd per computer, so use that one.
			prog, owned = unit.Program, false
			say("berthd is already installed on this computer (%s); using it rather than installing a second one.", unit.Program)
		} else {
			say("A berthd service is installed, but its program (%s) is gone; Berth replaces it.", unit.Program)
			have, home = false, a.localHome()
		}
	}

	updated, err := a.updateLocalBerthd(ctx, src, prog, home, owned, say)
	if err != nil {
		return "", err
	}
	running := serving(home)
	switch {
	case owned && (!have || updated || !running):
		listen := ""
		if have {
			cur := unit.Arg("--listen")
			if loopback(cur) && (running || portFree(cur)) {
				listen = cur
			} else if loopback(cur) {
				say("Something else is using %s now; berthd moves to another port.", cur)
			}
		}
		if listen == "" {
			if listen, err = freeLoopbackPort(a.localPort()); err != nil {
				return "", err
			}
		}
		say("Installing the berthd service, listening on %s: this computer only…", listen)
		if err := runBerthd(ctx, say, prog, home, "install", "--listen", listen, "--no-tools"); err != nil {
			return "", err
		}
	case !owned && (updated || !running):
		say("Restarting berthd…")
		if err := runBerthd(ctx, say, prog, home, "install", "--keep-listen", "--no-tools"); err != nil {
			return "", err
		}
	default:
		say("berthd is installed and running.")
	}

	name, err := a.pairLocal(ctx, prog, home, owned, say)
	if err != nil {
		return "", err
	}
	a.sync()
	a.checkAll(ctx)
	return name, nil
}

// updateLocalBerthd puts the bundled berthd at dst when dst is missing or
// another build, and says whether it changed anything. Berth's own copy
// follows the app (but is never downgraded below a newer release someone
// upgraded it to); a berthd the install script put there is only replaced
// by a newer release.
func (a *Agent) updateLocalBerthd(ctx context.Context, src, dst, home string, owned bool, say sayFunc) (bool, error) {
	if isFile(dst) {
		if same, err := sameBuild(src, dst); err != nil || same {
			return false, err
		}
		have, _ := berthdVersion(ctx, dst, home)
		next, _ := berthdVersion(ctx, src, home)
		if owned && newerRelease(have, next) {
			say("Keeping berthd %s at %s: it is newer than the %s Berth carries.", have, dst, next)
			return false, nil
		}
		if !owned && !newerRelease(next, have) {
			return false, nil
		}
		say("Updating berthd at %s (%s → %s)…", dst, orUnknown(have), orUnknown(next))
	} else {
		say("Copying berthd to %s…", dst)
	}
	if err := copyExecutable(src, dst); err != nil {
		return false, fmt.Errorf("copying berthd to %s: %w", dst, err)
	}
	if err := checkCopy(ctx, src, dst, say); err != nil {
		return false, err
	}
	return true, nil
}

func orUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

// checkCopy makes sure macOS will run the copy as it runs the app's: a
// signed berthd must still verify after the copy (copying keeps the
// signature, which is inside the file). A download quarantine flag on the
// copy is cleared, and only then: the copy is byte for byte the berthd
// inside an app Gatekeeper already let run, its signature was just
// verified, and launchd starts it with no window to ask for approval in, so
// a quarantined copy could be refused silently. Berth writes the copy
// itself, so macOS normally sets no such flag; this is a guard.
func checkCopy(ctx context.Context, src, dst string, say sayFunc) error {
	if localGOOS != "darwin" {
		return nil
	}
	if _, err := localTool(ctx, "/usr/bin/codesign", "--verify", "--strict", src); err != nil {
		// An unsigned build from a checkout: nothing to keep.
		return nil
	}
	if out, err := localTool(ctx, "/usr/bin/codesign", "--verify", "--strict", dst); err != nil {
		os.Remove(dst)
		return fmt.Errorf("the copy of berthd at %s fails its code signature check (%s), so it was removed; reinstall Berth from its disk image and try again", dst, strings.TrimSpace(string(out)))
	}
	if _, err := localTool(ctx, "/usr/bin/xattr", "-p", "com.apple.quarantine", dst); err == nil {
		if out, err := localTool(ctx, "/usr/bin/xattr", "-d", "com.apple.quarantine", dst); err != nil {
			return fmt.Errorf("clearing the quarantine flag on %s: %s", dst, strings.TrimSpace(string(out)))
		}
		say("Its signature checks out; cleared the download quarantine flag so launchd can start it.")
	}
	return nil
}

// copyExecutable replaces dst with src atomically: a running berthd keeps
// the file it started from, and the service picks up the new one when it
// restarts.
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".berthd-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = io.Copy(tmp, in)
	if err == nil {
		err = tmp.Chmod(0o755)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func sameBuild(a, b string) (bool, error) {
	x, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	y, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return version.BuildID(x) == version.BuildID(y), nil
}

// berthdVersion is the release a berthd says it is: "v0.1.2", or "dev".
func berthdVersion(ctx context.Context, prog, home string) (string, error) {
	out, err := berthdOutput(ctx, prog, home, "version")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) < 2 {
		return "", fmt.Errorf("unexpected version %q", out)
	}
	return fields[1], nil
}

// newerRelease is whether release a is newer than release b. Builds from a
// checkout ("dev") are newer than nothing and older than nothing.
func newerRelease(a, b string) bool {
	x, okA := parseRelease(a)
	y, okB := parseRelease(b)
	if !okA || !okB {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

func parseRelease(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if !strings.HasPrefix(v, "v") || len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func loopback(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func portFree(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// freeLoopbackPort is the first port from first up that nothing listens on
// at 127.0.0.1.
func freeLoopbackPort(first int) (string, error) {
	for p := first; p < first+100 && p <= 65535; p++ {
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(p))
		if portFree(addr) {
			return addr, nil
		}
	}
	return "", fmt.Errorf("no free port on 127.0.0.1 from %d to %d for berthd", first, first+99)
}

// dialAddress is where this laptop reaches the berthd under home: where it
// says it listens, with 127.0.0.1 for an address on every interface.
func dialAddress(home string) string {
	b, err := os.ReadFile(filepath.Join(home, "box", "listen"))
	if err != nil {
		return ""
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(string(b)))
	if err != nil {
		return ""
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// pairLocal pairs this laptop with the berthd under home, unless it already
// is, and returns the box's name here. The pairing link goes from berthd
// straight to berth pair: the same pairing a pasted link makes.
func (a *Agent) pairLocal(ctx context.Context, prog, home string, owned bool, say sayFunc) (string, error) {
	out, err := berthdOutput(ctx, prog, home, "id")
	if err != nil {
		return "", fmt.Errorf("reading berthd's fingerprint: %w", err)
	}
	fp, err := identity.ParseFingerprint(strings.TrimSpace(out))
	if err != nil {
		return "", fmt.Errorf("berthd printed an unexpected fingerprint: %w", err)
	}
	rec := &localBoxRecord{Fingerprint: fp.String(), Program: prog, Home: home, Owned: owned}
	if prev := a.localRecord(); prev != nil && prev.Fingerprint == rec.Fingerprint {
		rec.Name = prev.Name
	}
	// Saved before pairing, so the box is marked as this computer's from
	// the moment it is listed.
	if err := a.saveLocalRecord(rec); err != nil {
		return "", err
	}
	addr := dialAddress(home)
	if peer, ok, err := a.boxes.Trusted(fp); err != nil {
		return "", err
	} else if ok {
		if addr != "" && peer.Network == "" && peer.Address != addr {
			peer.Address = addr
			if err := a.boxes.Add(peer); err != nil {
				return "", err
			}
			say("It listens on %s now; updated.", addr)
		}
		say("Already paired as %s; reconnecting.", peer.Name)
		rec.Name = peer.Name
		return peer.Name, a.saveLocalRecord(rec)
	}

	say("Pairing this laptop with it…")
	args := []string{"pair", "--json", "--ttl", "2m"}
	if addr != "" {
		args = append(args, "--address", addr)
	}
	out, err = berthdOutput(ctx, prog, home, args...)
	if err != nil {
		return "", fmt.Errorf("berthd pair: %w", err)
	}
	var tok struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal([]byte(out), &tok); err != nil || tok.Link == "" {
		return "", fmt.Errorf("berthd pair printed no link: %q", strings.TrimSpace(out))
	}
	cliArgs := []string{"pair", tok.Link, "--json"}
	// Named after this computer, unless a box elsewhere has that name; then
	// the box's own name, with a number if need be.
	if name := localName(); name != "" {
		if _, taken, _ := a.boxes.ByName(name); !taken {
			cliArgs = append(cliArgs, "--name", name)
		}
	}
	paired, err := a.cli(ctx, cliArgs...).Output()
	if err != nil {
		return "", errors.New(cliError(err))
	}
	var peer struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(paired, &peer); err != nil || peer.Name == "" {
		return "", fmt.Errorf("berth pair printed %q", strings.TrimSpace(string(paired)))
	}
	say("Paired as %s.", peer.Name)
	rec.Name = peer.Name
	return peer.Name, a.saveLocalRecord(rec)
}

// removeLocalBox undoes setUpLocalBox: this laptop forgets the box, and the
// berthd service stops and is removed. With removeData, the box's state
// (BERTH_HOME/box: its keys, project list and session records) goes too.
// Repositories are never touched, and nor is a berthd the install script
// put on this computer: only Berth's own copy is deleted.
func (a *Agent) removeLocalBox(ctx context.Context, removeData bool, say sayFunc) error {
	rec := a.localRecord()
	unit, have, home, err := a.installedUnit()
	if err != nil {
		return err
	}
	if rec == nil && !have {
		return errors.New("this computer is not set up as a box")
	}
	prog := ""
	if rec != nil {
		prog = rec.Program
		if !have {
			home = rec.Home
		}
	}
	if have {
		prog = unit.Program
	}
	if rec != nil {
		if fp, err := identity.ParseFingerprint(rec.Fingerprint); err == nil {
			if peer, ok, _ := a.boxes.Trusted(fp); ok {
				say("Forgetting %s on this laptop…", peer.Name)
				if _, err := a.cli(ctx, "forget", peer.Name).Output(); err != nil {
					return errors.New(cliError(err))
				}
				a.sync()
			}
		}
	}
	if have {
		say("Stopping berthd and removing its service…")
		if isFile(prog) {
			if err := runBerthd(ctx, say, prog, home, "uninstall"); err != nil {
				return err
			}
		} else if _, err := service.Uninstall(service.Spec{Name: service.BerthdName()}); err != nil {
			return err
		}
	}
	if stable := stableBerthd(a.localHome()); isFile(stable) {
		if err := os.Remove(stable); err != nil {
			return err
		}
		say("Removed %s.", stable)
	}
	dir := filepath.Join(home, "box")
	if removeData {
		say("Deleting %s…", dir)
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	} else if _, err := os.Stat(dir); err == nil {
		say("Kept its data in %s; setting this computer up again uses it.", dir)
	}
	return a.saveLocalRecord(nil)
}

// keepLocalBoxCurrent refreshes this computer's berthd when the app carries
// a new one: shortly after the agent starts, then every few minutes, since
// the agent outlives the app being updated.
func (a *Agent) keepLocalBoxCurrent(ctx context.Context) {
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		a.refreshLocalBox(ctx)
		timer.Reset(localRefreshEvery)
	}
}

// refreshLocalBox is one such check: it does nothing unless this computer
// was set up as a box and the bundled berthd changed since the last check.
func (a *Agent) refreshLocalBox(ctx context.Context) {
	rec := a.localRecord()
	if rec == nil {
		return
	}
	src, err := a.bundledBerthd()
	if err != nil {
		return
	}
	st, err := os.Stat(src)
	if err != nil {
		return
	}
	stamp := fmt.Sprint(st.Size(), st.ModTime().UnixNano())
	// A restart waits for an update under way (restart.go).
	done, err := a.work.begin("updating this computer's box")
	if err != nil {
		return
	}
	defer done()
	// A set up or removal running now does the same work.
	if !a.local.mu.TryLock() {
		return
	}
	defer a.local.mu.Unlock()
	if a.local.seen == stamp {
		return
	}
	unit, have, home, err := a.installedUnit()
	if err != nil || !have || !isFile(unit.Program) {
		return
	}
	say := func(format string, args ...any) { a.cfg.Log.Printf("this computer's box: "+format, args...) }
	if err := a.checkBundled(ctx, src); err != nil {
		say("%v", err)
		return
	}
	owned := unit.Program == stableBerthd(a.localHome())
	updated, err := a.updateLocalBerthd(ctx, src, unit.Program, home, owned, say)
	if err != nil {
		say("%v", err)
		return
	}
	// A plist from before berthd wrote one has launchd's bare PATH, under
	// which berthd can't find Homebrew's tmux, so no agent starts:
	// installing again writes the user's PATH into it. Once per agent run
	// (seen), so a berthd that writes none isn't reinstalled again and again.
	heal := !updated && localGOOS == "darwin" && unit.Env["PATH"] == ""
	if heal {
		say("berthd's service has no PATH, so it can't find tools such as Homebrew's tmux; installing it again with yours")
	}
	if updated || heal {
		// This Mac's own box says how to get tmux in the app, rather than
		// install it unasked. A berthd healed in place may predate --no-tools.
		args := []string{"install", "--keep-listen"}
		if updated {
			args = []string{"install", "--keep-listen", "--no-tools"}
		}
		if err := runBerthd(ctx, say, unit.Program, home, args...); err != nil {
			say("reinstalling berthd: %v", err)
			return
		}
		a.checkSoon()
	}
	a.local.seen = stamp
}

func berthdCmd(ctx context.Context, prog, home string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, prog, args...)
	cmd.Env = append(os.Environ(), "BERTH_HOME="+home)
	return cmd
}

// berthdOutput runs berthd and returns what it printed, or its own error.
func berthdOutput(ctx context.Context, prog, home string, args ...string) (string, error) {
	out, err := berthdCmd(ctx, prog, home, args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", errors.New(strings.TrimPrefix(strings.TrimSpace(string(ee.Stderr)), "berthd: "))
		}
		return "", err
	}
	return string(out), nil
}

// runBerthd runs berthd, passing its output on line by line; its own
// "berthd: …" error (and what follows it) is the error returned, not output.
func runBerthd(ctx context.Context, say sayFunc, prog, home string, args ...string) error {
	cmd := berthdCmd(ctx, prog, home, args...)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); pw.Close() }()
	var failure []string
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if rest, ok := strings.CutPrefix(line, "berthd: "); ok || failure != nil {
			if ok {
				line = rest
			}
			failure = append(failure, line)
			continue
		}
		// Berth pairs on its own; there is no link to make.
		if strings.HasPrefix(line, "Next: berthd pair") || strings.TrimSpace(line) == "" {
			continue
		}
		say("  %s", line)
	}
	if err := <-done; err != nil {
		if len(failure) > 0 {
			return errors.New(strings.Join(failure, "\n"))
		}
		return fmt.Errorf("berthd %s: %w", args[0], err)
	}
	for _, l := range failure {
		say("  %s", l)
	}
	return nil
}

// streamProgress answers with NDJSON StreamLines, as runStream does.
func streamProgress(w http.ResponseWriter) (say sayFunc, finish func(box string, err error)) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)
	var mu sync.Mutex
	send := func(l StreamLine) {
		mu.Lock()
		defer mu.Unlock()
		enc.Encode(l)
		rc.Flush()
	}
	say = func(format string, args ...any) { send(StreamLine{Line: fmt.Sprintf(format, args...)}) }
	finish = func(box string, err error) {
		if err != nil {
			send(StreamLine{Done: true, Error: err.Error()})
			return
		}
		send(StreamLine{Done: true, Box: box})
	}
	return say, finish
}

func (a *Agent) localBoxRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/boxes/local", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.localBoxStatus())
	})
	mux.HandleFunc("POST /v1/boxes/local", func(w http.ResponseWriter, r *http.Request) {
		done, err := a.work.begin("setting up this computer's box")
		if err != nil {
			writeCoded(w, http.StatusServiceUnavailable, err.Error(), "agent_restarting")
			return
		}
		defer done()
		if !a.local.mu.TryLock() {
			writeError(w, http.StatusConflict, "this computer's box is being set up or removed already")
			return
		}
		defer a.local.mu.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		say, finish := streamProgress(w)
		name, err := a.setUpLocalBox(ctx, say)
		finish(name, err)
	})
	mux.HandleFunc("POST /v1/boxes/local/uninstall", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			RemoveData bool `json:"remove_data"`
		}
		if r.ContentLength != 0 && !decodeBody(w, r, &req) {
			return
		}
		done, err := a.work.begin("removing this computer's box")
		if err != nil {
			writeCoded(w, http.StatusServiceUnavailable, err.Error(), "agent_restarting")
			return
		}
		defer done()
		if !a.local.mu.TryLock() {
			writeError(w, http.StatusConflict, "this computer's box is being set up or removed already")
			return
		}
		defer a.local.mu.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		say, finish := streamProgress(w)
		finish("", a.removeLocalBox(ctx, req.RemoveData, say))
	})
}
