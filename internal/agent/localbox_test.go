package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sean-brydon/berthd/internal/identity"
	"github.com/sean-brydon/berthd/internal/service"
	"github.com/sean-brydon/berthd/internal/trust"
)

// A fake berthd for Use this Mac: it logs each run, and install writes the
// service unit from a template (as `berthd install` would, minus launchd),
// records where it listens and marks itself serving. Nothing here touches
// launchd or systemd.
const fakeBerthdScript = `#!/bin/sh
VERSION=%VERSION%
echo "$BERTH_HOME|$*" >> "$FAKE_BERTHD_LOG"
case "$1" in
  version) echo "berthd $VERSION (build x, test)" ;;
  id) echo "$FAKE_FP" ;;
  install)
    if [ -n "$FAKE_NO_SESSION" ]; then
      echo "berthd: launchd has no login session for you on this Mac (Could not find domain), so berthd cannot run as your launch agent." >&2
      exit 1
    fi
    if [ "$2" = "--listen" ]; then listen="$3"; else listen="$(cat "$BERTH_HOME/box/listen")"; fi
    mkdir -p "$BERTH_HOME/box" "$(dirname "$FAKE_UNIT")"
    sed -e "s|__LISTEN__|$listen|" -e "s|__PROGRAM__|$0|" -e "s|__HOME__|$BERTH_HOME|" "$FAKE_UNIT_TEMPLATE" > "$FAKE_UNIT"
    printf '%s' "$listen" > "$BERTH_HOME/box/listen"
    touch "$BERTH_HOME/box/serving"
    echo "Installed $FAKE_UNIT; berthd is serving on $listen."
    echo "Next: berthd pair"
    ;;
  pair) printf '{"link":"berth://%s?code=c0de&fp=%s","address":"%s"}\n' "$6" "$FAKE_FP" "$6" ;;
  uninstall) rm -f "$FAKE_UNIT" "$BERTH_HOME/box/serving"; echo "Removed $FAKE_UNIT" ;;
esac
`

// A fake berth CLI: pair prints the peer it would save; forget forgets.
const fakeBerthForLocal = `#!/bin/sh
echo "$BERTH_HOME|$*" >> "$FAKE_BERTH_LOG"
case "$1" in
  pair) name=from-box; [ "$4" = "--name" ] && name="$5"; printf '{"name":"%s","address":"127.0.0.1:1"}\n' "$name" ;;
  forget) echo "Forgot $2" ;;
esac
`

type localEnv struct {
	t       *testing.T
	root    string // BERTH_HOME
	dir     string // the agent's state, root/client
	home    string // HOME
	unit    string
	fp      identity.Fingerprint
	berthd  string // the log of berthd runs
	berth   string // the log of berth runs
	stable  string
	bundled string
}

// newLocalEnv is a temporary HOME and BERTH_HOME, a fake berthd beside the
// agent's berth, and serving read from the fake's marker file.
func newLocalEnv(t *testing.T) *localEnv {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "lb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	e := &localEnv{t: t, root: root, dir: filepath.Join(root, "client"), home: filepath.Join(root, "home")}
	for _, d := range []string{e.dir, e.home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", e.home)
	t.Setenv("XDG_CONFIG_HOME", "")
	e.unit, err = service.UnitPath(service.Spec{Name: service.BerthdName()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.unit, e.home) {
		t.Fatalf("unit path %s is outside the test HOME", e.unit)
	}
	// The unit berthd install would write, with the values it is given.
	tmpl, err := service.Render(service.Spec{Name: service.BerthdName(), Program: "__PROGRAM__", Args: []string{"serve", "--listen", "__LISTEN__"}, Env: map[string]string{"BERTH_HOME": "__HOME__"}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "unit.tmpl"), string(tmpl), 0o644)
	e.fp = identity.Fingerprint{1, 2, 3, 4}
	e.berthd, e.berth = filepath.Join(root, "berthd.log"), filepath.Join(root, "berth.log")
	t.Setenv("FAKE_BERTHD_LOG", e.berthd)
	t.Setenv("FAKE_BERTH_LOG", e.berth)
	t.Setenv("FAKE_FP", e.fp.String())
	t.Setenv("FAKE_UNIT", e.unit)
	t.Setenv("FAKE_UNIT_TEMPLATE", filepath.Join(root, "unit.tmpl"))
	e.stable = filepath.Join(root, "bin", "berthd")
	e.bundled = filepath.Join(e.dir, "berthd")
	writeFile(t, e.bundled, fakeBerthd("dev-new"), 0o755)
	writeFile(t, filepath.Join(e.dir, "fake-berth"), fakeBerthForLocal, 0o755)
	old := serving
	serving = func(home string) bool { return isFile(filepath.Join(home, "box", "serving")) }
	t.Cleanup(func() { serving = old })
	return e
}

func fakeBerthd(v string) string { return strings.Replace(fakeBerthdScript, "%VERSION%", v, 1) }

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func (e *localEnv) log(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// installUnit writes a unit as berthd install would have.
func (e *localEnv) installUnit(program, listen, home string) {
	e.t.Helper()
	b, err := service.Render(service.Spec{Name: service.BerthdName(), Program: program, Args: []string{"serve", "--listen", listen}, Env: map[string]string{"BERTH_HOME": home}})
	if err != nil {
		e.t.Fatal(err)
	}
	writeFile(e.t, e.unit, string(b), 0o644)
	writeFile(e.t, filepath.Join(home, "box", "listen"), listen, 0o600)
}

func streamed(t *testing.T, body string) (lines []string, last StreamLine) {
	t.Helper()
	for _, l := range strings.Split(strings.TrimSpace(body), "\n") {
		var sl StreamLine
		if err := json.Unmarshal([]byte(l), &sl); err != nil {
			t.Fatalf("not a stream line: %q", l)
		}
		if sl.Done {
			last = sl
			continue
		}
		lines = append(lines, sl.Line)
	}
	if !last.Done {
		t.Fatalf("the stream did not finish:\n%s", body)
	}
	return lines, last
}

func TestUseThisMacSetsUpPairsReconnectsAndUninstalls(t *testing.T) {
	e := newLocalEnv(t)
	a := startAgent(t, e.dir)
	tok := uiToken(t, a)

	resp, body := uiSend(t, a, "GET", "/v1/boxes/local", tok, "")
	var st LocalBoxStatus
	json.Unmarshal([]byte(body), &st)
	if resp.StatusCode != 200 || !st.Available || st.Installed || st.Name != localName() {
		t.Fatalf("before: %d %s", resp.StatusCode, body)
	}

	_, body = uiSend(t, a, "POST", "/v1/boxes/local", tok, "")
	lines, last := streamed(t, body)
	if last.Error != "" || last.Box != localName() {
		t.Fatalf("set up: %s", body)
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{"Copying berthd to " + e.stable, "Installing the berthd service, listening on 127.0.0.1:", "  Installed " + e.unit, "Pairing this laptop with it…", "Paired as " + localName() + "."} {
		if !strings.Contains(all, want) {
			t.Errorf("set up did not say %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "Next: berthd pair") {
		t.Error("the set up passed on berthd's advice to pair by hand")
	}
	// The service runs Berth's own copy, on loopback, in BERTH_HOME.
	unit, ok, err := service.Read(service.BerthdName())
	if err != nil || !ok || unit.Program != e.stable || unit.Env["BERTH_HOME"] != e.root || !loopback(unit.Arg("--listen")) {
		t.Fatalf("installed unit = %+v, %v, %v", unit, ok, err)
	}
	if same, _ := sameBuild(e.bundled, e.stable); !same {
		t.Fatal("the copy is not the bundled berthd")
	}
	listen := unit.Arg("--listen")
	berthdRuns := e.log(e.berthd)
	for _, want := range []string{
		e.root + "|install --listen " + listen + " --no-tools",
		e.root + "|id",
		e.root + "|pair --json --ttl 2m --address " + listen,
	} {
		if !strings.Contains(berthdRuns, want+"\n") {
			t.Errorf("berthd did not run %q:\n%s", want, berthdRuns)
		}
	}
	if want := e.root + "|pair berth://" + listen + "?code=c0de&fp=" + e.fp.String() + " --json --name " + localName(); !strings.Contains(e.log(e.berth), want) {
		t.Errorf("berth pair was not run with the link berthd printed:\n%s", e.log(e.berth))
	}

	// What berth pair saved; the box is this Mac's.
	if err := trust.NewStore(filepath.Join(e.dir, "boxes.json")).Add(trust.Peer{Name: localName(), Address: listen, Fingerprint: e.fp, PairedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the box marked local", func() bool {
		s, err := a.client.Status(context.Background())
		return err == nil && len(s.Boxes) == 1 && s.Boxes[0].Local
	})

	// Again: nothing is installed twice; it only reconnects.
	_, body = uiSend(t, a, "POST", "/v1/boxes/local", tok, "")
	lines, last = streamed(t, body)
	all = strings.Join(lines, "\n")
	if last.Error != "" || last.Box != localName() || !strings.Contains(all, "berthd is installed and running.") || !strings.Contains(all, "Already paired as "+localName()+"; reconnecting.") {
		t.Fatalf("set up again: %s", body)
	}
	if n := strings.Count(e.log(e.berthd), "|install "); n != 1 {
		t.Fatalf("berthd install ran %d times", n)
	}
	_, body = uiSend(t, a, "GET", "/v1/boxes/local", tok, "")
	st = LocalBoxStatus{}
	json.Unmarshal([]byte(body), &st)
	if !st.Installed || !st.Owned || !st.Running || st.Box != localName() || st.Listen != listen {
		t.Fatalf("after: %s", body)
	}

	// Removing it keeps or deletes its data, as asked.
	_, body = uiSend(t, a, "POST", "/v1/boxes/local/uninstall", tok, `{"remove_data":true}`)
	lines, last = streamed(t, body)
	if last.Error != "" {
		t.Fatalf("uninstall: %s", body)
	}
	if !strings.Contains(e.log(e.berth), "|forget "+localName()) || !strings.Contains(e.log(e.berthd), e.root+"|uninstall") {
		t.Fatalf("uninstall ran:\n%s\n%s", e.log(e.berth), e.log(e.berthd))
	}
	for _, gone := range []string{e.unit, e.stable, filepath.Join(e.root, "box"), filepath.Join(e.dir, "localbox.json")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s is still there after removing with its data", gone)
		}
	}
	if resp, _ := uiSend(t, a, "POST", "/v1/boxes/local/uninstall", tok, ""); resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
}

func TestUseThisMacReusesABerthdTheInstallScriptInstalled(t *testing.T) {
	e := newLocalEnv(t)
	cliHome := filepath.Join(e.root, "cli-home")
	cliBerthd := filepath.Join(e.home, ".local", "bin", "berthd")
	writeFile(t, cliBerthd, fakeBerthd("v0.1.0"), 0o755)
	e.installUnit(cliBerthd, "100.101.102.103:7444", cliHome)
	writeFile(t, filepath.Join(cliHome, "box", "serving"), "", 0o600)
	// The app carries an older release: the install is used as it is.
	writeFile(t, e.bundled, fakeBerthd("v0.0.9"), 0o755)
	a := startAgent(t, e.dir)
	tok := uiToken(t, a)

	_, body := uiSend(t, a, "POST", "/v1/boxes/local", tok, "")
	lines, last := streamed(t, body)
	all := strings.Join(lines, "\n")
	if last.Error != "" || !strings.Contains(all, "berthd is already installed on this computer ("+cliBerthd+")") || !strings.Contains(all, "berthd is installed and running.") {
		t.Fatalf("set up: %s", body)
	}
	if strings.Contains(e.log(e.berthd), "|install") || isFile(e.stable) {
		t.Fatalf("installed a second berthd:\n%s", e.log(e.berthd))
	}
	if !strings.Contains(e.log(e.berthd), cliHome+"|pair --json --ttl 2m --address 100.101.102.103:7444") {
		t.Fatalf("did not pair with the installed berthd, in its home:\n%s", e.log(e.berthd))
	}

	// A newer release in the app upgrades that install in place, keeping
	// where it listens.
	writeFile(t, e.bundled, fakeBerthd("v0.2.0"), 0o755)
	_, body = uiSend(t, a, "POST", "/v1/boxes/local", tok, "")
	lines, last = streamed(t, body)
	all = strings.Join(lines, "\n")
	if last.Error != "" || !strings.Contains(all, "Updating berthd at "+cliBerthd+" (v0.1.0 → v0.2.0)") {
		t.Fatalf("upgrade: %s", body)
	}
	if !strings.Contains(e.log(e.berthd), cliHome+"|install --keep-listen") || isFile(e.stable) {
		t.Fatalf("upgrade ran:\n%s", e.log(e.berthd))
	}
	if same, _ := sameBuild(e.bundled, cliBerthd); !same {
		t.Fatal("the install script's berthd was not upgraded")
	}
	// Removing it stops that service but never deletes the person's berthd.
	_, body = uiSend(t, a, "POST", "/v1/boxes/local/uninstall", tok, `{"remove_data":false}`)
	if _, last := streamed(t, body); last.Error != "" || !isFile(cliBerthd) || !isFile(filepath.Join(cliHome, "box", "listen")) {
		t.Fatalf("uninstall: %s", body)
	}
}

func TestUseThisMacMovesOffAPortSomethingElseTook(t *testing.T) {
	e := newLocalEnv(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	taken := ln.Addr().String()
	// Set up before, but not running now, and its port is someone else's.
	writeFile(t, e.stable, fakeBerthd("dev-new"), 0o755)
	e.installUnit(e.stable, taken, e.root)
	a := startAgent(t, e.dir)
	tok := uiToken(t, a)
	_, body := uiSend(t, a, "POST", "/v1/boxes/local", tok, "")
	lines, last := streamed(t, body)
	unit, _, _ := service.Read(service.BerthdName())
	if last.Error != "" || !strings.Contains(strings.Join(lines, "\n"), "Something else is using "+taken) || unit.Arg("--listen") == taken || !loopback(unit.Arg("--listen")) {
		t.Fatalf("set up: %s\nlistens on %s", body, unit.Arg("--listen"))
	}
}

func TestUseThisMacSaysWhyLaunchdRefused(t *testing.T) {
	e := newLocalEnv(t)
	t.Setenv("FAKE_NO_SESSION", "1")
	a := startAgent(t, e.dir)
	tok := uiToken(t, a)
	_, body := uiSend(t, a, "POST", "/v1/boxes/local", tok, "")
	_, last := streamed(t, body)
	if !strings.HasPrefix(last.Error, "launchd has no login session for you on this Mac") {
		t.Fatalf("set up: %s", body)
	}
	if strings.Contains(e.log(e.berth), "pair") {
		t.Fatal("paired with a berthd that did not start")
	}
}

func TestUseThisMacWithoutABundledBerthd(t *testing.T) {
	e := newLocalEnv(t)
	os.Remove(e.bundled)
	a := startAgent(t, e.dir)
	tok := uiToken(t, a)
	_, body := uiSend(t, a, "GET", "/v1/boxes/local", tok, "")
	var st LocalBoxStatus
	json.Unmarshal([]byte(body), &st)
	if st.Available || st.Reason == "" {
		t.Fatalf("status: %s", body)
	}
	_, body = uiSend(t, a, "POST", "/v1/boxes/local", tok, "")
	if _, last := streamed(t, body); last.Error != errNoBerthd.Error() {
		t.Fatalf("set up: %s", body)
	}
}

// After the app updates, the agent replaces its copy and reinstalls the
// service where it listens; it never downgrades a newer release.
func TestAnAppUpdateRefreshesTheCopy(t *testing.T) {
	e := newLocalEnv(t)
	writeFile(t, e.stable, fakeBerthd("dev-old"), 0o755)
	e.installUnit(e.stable, "127.0.0.1:7445", e.root)
	a := &Agent{cfg: Config{Dir: e.dir, CLI: filepath.Join(e.dir, "fake-berth"), Log: log.New(io.Discard, "", 0)}, boxes: trust.NewStore(filepath.Join(e.dir, "boxes.json"))}
	a.refreshLocalBox(context.Background())
	if e.log(e.berthd) != "" {
		t.Fatal("refreshed a box this agent never set up")
	}
	if err := a.saveLocalRecord(&localBoxRecord{Fingerprint: e.fp.String(), Program: e.stable, Home: e.root, Owned: true}); err != nil {
		t.Fatal(err)
	}
	a.refreshLocalBox(context.Background())
	if same, _ := sameBuild(e.bundled, e.stable); !same || !strings.Contains(e.log(e.berthd), e.root+"|install --keep-listen") {
		t.Fatalf("not refreshed:\n%s", e.log(e.berthd))
	}
	before := e.log(e.berthd)
	a.refreshLocalBox(context.Background())
	if e.log(e.berthd) != before {
		t.Fatal("checked again with nothing new in the app")
	}
	// The box was upgraded past what the app now carries.
	writeFile(t, e.stable, fakeBerthd("v0.3.0"), 0o755)
	writeFile(t, e.bundled, fakeBerthd("v0.2.0"), 0o755)
	os.Chtimes(e.bundled, time.Now(), time.Now().Add(time.Minute))
	a.refreshLocalBox(context.Background())
	if b, _ := os.ReadFile(e.stable); !strings.Contains(string(b), "VERSION=v0.3.0") {
		t.Fatal("downgraded the box")
	}
}

// A Mac's berthd installed before plists carried a PATH can't find
// Homebrew's tmux: the agent's next start installs it again, once, which
// writes the user's PATH into the plist.
func TestAPlistWithoutAPATHIsInstalledAgainOnce(t *testing.T) {
	e := newLocalEnv(t)
	old := localGOOS
	t.Cleanup(func() { localGOOS = old })
	localGOOS = "darwin"
	copyFile(t, e.bundled, e.stable)
	e.installUnit(e.stable, "127.0.0.1:7445", e.root)
	a := &Agent{cfg: Config{Dir: e.dir, CLI: filepath.Join(e.dir, "fake-berth"), Log: log.New(io.Discard, "", 0)}, boxes: trust.NewStore(filepath.Join(e.dir, "boxes.json"))}
	if err := a.saveLocalRecord(&localBoxRecord{Fingerprint: e.fp.String(), Program: e.stable, Home: e.root, Owned: true}); err != nil {
		t.Fatal(err)
	}
	a.refreshLocalBox(context.Background())
	if n := strings.Count(e.log(e.berthd), "install --keep-listen"); n != 1 {
		t.Fatalf("installed %d times:\n%s", n, e.log(e.berthd))
	}
	a.refreshLocalBox(context.Background())
	if n := strings.Count(e.log(e.berthd), "install --keep-listen"); n != 1 {
		t.Fatalf("installed again (%d times)", n)
	}

	// One that has a PATH is left alone.
	b := &Agent{cfg: a.cfg, boxes: a.boxes}
	unit, _ := os.ReadFile(e.unit)
	withPath := strings.Replace(string(unit), "<key>BERTH_HOME</key>", "<key>PATH</key><string>/opt/homebrew/bin:/usr/bin</string>\n<key>BERTH_HOME</key>", 1)
	if goos := runtime.GOOS; goos != "darwin" {
		withPath = strings.Replace(string(unit), "Environment=", "Environment=PATH=/usr/bin\nEnvironment=", 1)
	}
	writeFile(t, e.unit, withPath, 0o644)
	b.refreshLocalBox(context.Background())
	if n := strings.Count(e.log(e.berthd), "install --keep-listen"); n != 1 {
		t.Fatalf("installed one with a PATH again:\n%s", e.log(e.berthd))
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dst, string(b), 0o755)
}

func TestTheCopyKeepsItsSignatureAndLosesAQuarantine(t *testing.T) {
	oldOS, oldTool := localGOOS, localTool
	t.Cleanup(func() { localGOOS, localTool = oldOS, oldTool })
	localGOOS = "darwin"
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	writeFile(t, src, "signed", 0o755)
	writeFile(t, dst, "signed", 0o755)
	var mu sync.Mutex
	var calls []string
	verifies, quarantined := true, true
	localTool = func(_ context.Context, name string, args ...string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		call := name + " " + strings.Join(args, " ")
		calls = append(calls, call)
		switch {
		case strings.HasPrefix(call, "/usr/bin/codesign --verify --strict "+dst) && !verifies:
			return []byte(dst + ": invalid signature (code or signature have been modified)"), errors.New("exit status 1")
		case strings.HasPrefix(call, "/usr/bin/xattr -p") && !quarantined:
			return []byte("No such xattr"), errors.New("exit status 1")
		}
		return nil, nil
	}
	var said []string
	say := func(f string, args ...any) { said = append(said, f) }
	if err := checkCopy(context.Background(), src, dst, say); err != nil {
		t.Fatal(err)
	}
	if want := "/usr/bin/xattr -d com.apple.quarantine " + dst; calls[len(calls)-1] != want || len(said) != 1 {
		t.Fatalf("calls %q, said %q", calls, said)
	}
	calls, quarantined = nil, false
	if err := checkCopy(context.Background(), src, dst, say); err != nil || len(calls) != 3 {
		t.Fatalf("an unquarantined copy: %v, %q", err, calls)
	}
	verifies = false
	if err := checkCopy(context.Background(), src, dst, say); err == nil || !strings.Contains(err.Error(), "fails its code signature check") || isFile(dst) {
		t.Fatalf("a copy that fails its signature check: %v", err)
	}
}

func TestNewerRelease(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.9", true},
		{"v0.10.0", "v0.9.0", true},
		{"v1.0.0", "v1.0.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"dev", "v0.1.0", false},
		{"v0.1.0", "dev", false},
		{"v0.1", "v0.0.1", false},
	} {
		if got := newerRelease(c.a, c.b); got != c.want {
			t.Errorf("newerRelease(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// Inside Berth.app the agent takes only the app's own berthd, and only when
// the app's team signed it (security audit M-4).
func TestTheBundledBerthdIsTheAppsOwnAndSignedByItsTeam(t *testing.T) {
	dir := t.TempDir()
	macos := filepath.Join(dir, "Berth.app", "Contents", "MacOS")
	cli := filepath.Join(macos, "berth-cli")
	writeFile(t, cli, "cli", 0o755)
	writeFile(t, filepath.Join(macos, "berthd"), "planted", 0o755)
	a := &Agent{cfg: Config{CLI: cli}}
	if _, err := a.bundledBerthd(); err == nil {
		t.Fatal("took a berthd beside berth-cli instead of the app's Resources")
	}
	resource := filepath.Join(dir, "Berth.app", "Contents", "Resources", "berthd")
	writeFile(t, resource, "bundled", 0o755)
	resource, _ = filepath.EvalSymlinks(resource) // /var is /private/var on a Mac
	if got, err := a.bundledBerthd(); err != nil || got != resource {
		t.Fatalf("bundledBerthd = %q, %v", got, err)
	}

	oldOS, oldTool := localGOOS, localTool
	t.Cleanup(func() { localGOOS, localTool = oldOS, oldTool })
	localGOOS = "darwin"
	team, signedByTeam := "TeamIdentifier=ABCDE12345", true
	var verified string
	localTool = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if args[0] == "-dv" {
			return []byte("Executable=" + cli + "\n" + team + "\n"), nil
		}
		verified = strings.Join(args, " ")
		if !signedByTeam {
			return []byte("test-requirement: code failed to satisfy specified code requirement(s)"), errors.New("exit status 3")
		}
		return nil, nil
	}
	if err := a.checkBundled(context.Background(), resource); err != nil || !strings.Contains(verified, `certificate leaf[subject.OU] = "ABCDE12345"`) {
		t.Fatalf("a berthd signed by the team: %v (verified %q)", err, verified)
	}
	signedByTeam = false
	if err := a.checkBundled(context.Background(), resource); err == nil || !strings.Contains(err.Error(), "not signed by Berth's team") {
		t.Fatalf("a berthd from someone else: %v", err)
	}
	// An unsigned build has no team to hold berthd to.
	team, verified = "TeamIdentifier=not set", ""
	if err := a.checkBundled(context.Background(), resource); err != nil || verified != "" {
		t.Fatalf("an unsigned build: %v, %q", err, verified)
	}
}
