package box

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sean-brydon/berthd/internal/events"
	"github.com/sean-brydon/berthd/internal/service"
	"github.com/sean-brydon/berthd/internal/wire"
)

// No test may ever reach a real 1Password account: the shared resolver and
// the install-folder search can never find a real op, and every test that
// resolves a reference runs the stub below.
func init() {
	opFolders = nil
	defaultSecrets.Op = "/nonexistent/op-is-disabled-in-tests"
}

// The values the stub op prints. The password has a $ so a test can see it is
// never expanded.
const (
	stubPassword = "pa$$word-from-the-stub-op"
	stubToken    = "ops_stub-service-account-token"
)

// stubOp puts a fake op on PATH that prints canned values, logs each call,
// and fails like op does for anything it does not know.
func stubOp(t *testing.T) (op, calls string) {
	t.Helper()
	op, calls = stubOpFile(t)
	t.Setenv("PATH", filepath.Dir(op)+":/usr/bin:/bin")
	return op, calls
}

// stubOpFile writes the fake op without changing PATH.
func stubOpFile(t *testing.T) (op, calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	flaky := filepath.Join(dir, "flaky-ok")
	script := `#!/bin/sh
echo "$*" >> "` + calls + `"
if [ "$1" != read ]; then echo '[ERROR] 2024/01/01 00:00:00 unknown command' >&2; exit 1; fi
case "$2" in
op://dev/db/password) printf '%s\n' '` + stubPassword + `' ;;
op://dev/token/credential) printf '%s' "$OP_SERVICE_ACCOUNT_TOKEN" ;;
op://dev/slow/field) sleep 5 ;;
op://dev/session/key) printf 'session-only-secret-value-77' ;;
op://dev/flaky/field)
  if [ -f "` + flaky + `" ]; then printf 'flaky-value-now-resolves'; exit 0; fi
  echo '[ERROR] 2024/01/01 00:00:00 You are not currently signed in.' >&2; exit 1 ;;
*) echo "[ERROR] 2024/01/01 00:00:00 \"$2\" isn't an item in the \"dev\" vault" >&2; exit 1 ;;
esac
`
	op = filepath.Join(dir, "op")
	if err := os.WriteFile(op, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return op, calls
}

func opCalls(t *testing.T, path string) int {
	t.Helper()
	b, _ := os.ReadFile(path)
	return strings.Count(string(b), "\n")
}

func TestSecretReferencesAreRecognisedAndChecked(t *testing.T) {
	for _, v := range []string{"op://dev/db/password", "op://Private Vault/Stripe/test key", "op://dev/db/section/field", "op://dev/github/one-time password?attribute=otp", "env://STRIPE_KEY"} {
		if !IsSecretRef(v) {
			t.Errorf("%q is not a reference", v)
		}
		if err := ValidateSecretRef(v); err != nil {
			t.Errorf("%q: %v", v, err)
		}
	}
	for _, v := range []string{"postgres://localhost/db", "http://x/y/z", "op:dev/db/password", "plain", "$BERTH_PORT", ""} {
		if IsSecretRef(v) {
			t.Errorf("%q was taken for a reference", v)
		}
	}
	for _, v := range []string{"op://dev", "op://dev/db", "op://dev//password", "op://a/b/c/d/e", "env://", "env://1BAD", "env://A-B"} {
		if !IsSecretRef(v) {
			t.Errorf("%q should be read as a reference", v)
		}
		if ValidateSecretRef(v) == nil {
			t.Errorf("%q was accepted", v)
		}
	}
	// A config with a broken reference is refused when it is saved.
	l := NewLocations(filepath.Join(t.TempDir(), "locations.json"))
	repo := gitRepo(t)
	l.Add(context.Background(), "cal", repo)
	if err := l.SetLocalConfig("cal", RepoConfig{Env: map[string]string{"DB": "op://dev/db"}}); err == nil {
		t.Fatal("a broken reference was saved")
	}
	if err := l.SetLocalConfig("cal", RepoConfig{Env: map[string]string{"DB": "op://dev/db/password"}}); err != nil {
		t.Fatal(err)
	}
	if err := saveBoxEnv(filepath.Join(t.TempDir(), "env.json"), BoxEnv{Env: map[string]string{"X": "env://not a name"}}); err == nil {
		t.Fatal("a broken reference was saved to the box environment")
	}
}

func TestOpResolvesAndIsKeptBriefly(t *testing.T) {
	ctx := context.Background()
	op, calls := stubOp(t)
	s := &Secrets{Op: op, TTL: 300 * time.Millisecond, FailTTL: 300 * time.Millisecond}

	v, err := s.Resolve(ctx, "op://dev/db/password", nil, false)
	if err != nil || v != stubPassword {
		t.Fatalf("resolve = %q, %v", v, err)
	}
	if again, _ := s.Resolve(ctx, "op://dev/db/password", nil, false); again != v || opCalls(t, calls) != 1 {
		t.Fatalf("a second resolve ran op again (%d calls)", opCalls(t, calls))
	}
	// A failure is remembered too, so a broken reference does not run op
	// for every hook, and its error is op's own words, cleaned up.
	_, err = s.Resolve(ctx, "op://dev/nope/field", nil, false)
	if err == nil || err.Error() != `op: "op://dev/nope/field" isn't an item in the "dev" vault` {
		t.Fatalf("failure = %v", err)
	}
	s.Resolve(ctx, "op://dev/nope/field", nil, false)
	if opCalls(t, calls) != 2 {
		t.Fatalf("a remembered failure ran op again (%d calls)", opCalls(t, calls))
	}
	// Fresh asks op again, as a Test button does.
	s.Resolve(ctx, "op://dev/db/password", nil, true)
	if opCalls(t, calls) != 3 {
		t.Fatalf("fresh did not run op (%d calls)", opCalls(t, calls))
	}
	// After the TTL it is forgotten, not just stale.
	time.Sleep(450 * time.Millisecond)
	s.mu.Lock()
	left := len(s.cache)
	s.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d values outlived their TTL", left)
	}
	s.Resolve(ctx, "op://dev/db/password", nil, false)
	if opCalls(t, calls) != 4 {
		t.Fatalf("an expired value was served (%d calls)", opCalls(t, calls))
	}
	// The service-account token reaches op.
	if v, err := s.Resolve(ctx, "op://dev/token/credential", []string{"OP_SERVICE_ACCOUNT_TOKEN=" + stubToken}, false); err != nil || v != stubToken {
		t.Fatalf("token = %q, %v", v, err)
	}
}

func TestOpIsFoundOnPathAndMissingOrSlowOpFailsClearly(t *testing.T) {
	ctx := context.Background()
	stubOp(t)
	if v, err := (&Secrets{}).Resolve(ctx, "op://dev/db/password", nil, false); err != nil || v != stubPassword {
		t.Fatalf("op on PATH: %q, %v", v, err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := (&Secrets{}).Resolve(ctx, "op://dev/db/password", nil, false); err != errNoOp {
		t.Fatalf("no op gave %v", err)
	}
	if _, err := (&Secrets{Op: "/nonexistent/op"}).Resolve(ctx, "op://dev/db/password", nil, false); err != errNoOp {
		t.Fatalf("a missing op gave %v", err)
	}
	op, _ := stubOp(t)
	start := time.Now()
	_, err := (&Secrets{Op: op, Timeout: 200 * time.Millisecond}).Resolve(ctx, "op://dev/slow/field", nil, false)
	if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(start) > 4*time.Second {
		t.Fatalf("slow op: %v after %s", err, time.Since(start))
	}
}

func TestEnvReferencesPassThroughBerthdsEnvironment(t *testing.T) {
	t.Setenv("BERTH_TEST_PASSTHROUGH", "from-berthd-env-value")
	s := &Secrets{}
	if v, err := s.Resolve(context.Background(), "env://BERTH_TEST_PASSTHROUGH", nil, false); err != nil || v != "from-berthd-env-value" {
		t.Fatalf("env = %q, %v", v, err)
	}
	if _, err := s.Resolve(context.Background(), "env://BERTH_TEST_UNSET_VARIABLE", nil, false); err == nil || !strings.Contains(err.Error(), "BERTH_TEST_UNSET_VARIABLE is not set") {
		t.Fatalf("unset = %v", err)
	}
}

func secretBox(t *testing.T) (*Box, Worktree, string) {
	t.Helper()
	ctx := context.Background()
	op, calls := stubOp(t)
	repo := gitRepo(t)
	dir := t.TempDir()
	writeRepoConfig(t, repo, RepoConfig{Env: map[string]string{
		"DB_PASSWORD": "op://dev/db/password",
		"MISSING":     "op://dev/nope/field",
		"PASSED":      "env://BERTH_TEST_PASSTHROUGH",
		"PLAIN":       "db-$BERTH_WORKTREE_SLUG",
	}})
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(dir, "locations.json")), Events: &events.Bus{}, EnvFile: filepath.Join(dir, "env.json"), Secrets: &Secrets{Op: op, FailTTL: 100 * time.Millisecond}}
	if err := saveBoxEnv(b.EnvFile, BoxEnv{Env: map[string]string{"OP_SERVICE_ACCOUNT_TOKEN": stubToken, "TOKEN_SEEN_BY_OP": "op://dev/token/credential"}}); err != nil {
		t.Fatal(err)
	}
	b.Locations.Add(ctx, "cal", repo)
	trustRepo(t, b.Locations, "cal")
	wt, err := b.Locations.CreateWorktree(ctx, "cal", "billing", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return b, wt, calls
}

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func TestWorktreeEnvironmentResolvesReferencesAndReportsFailuresOnce(t *testing.T) {
	ctx := context.Background()
	t.Setenv("BERTH_TEST_PASSTHROUGH", "passed-through-value")
	b, wt, _ := secretBox(t)
	ch, stop := b.Events.Subscribe()
	defer stop()

	env, err := b.WorktreeEnv(ctx, "cal", wt)
	if err != nil {
		t.Fatal(err)
	}
	got := envMap(env)
	if got["DB_PASSWORD"] != stubPassword || got["PASSED"] != "passed-through-value" || got["PLAIN"] != "db-cal_billing" || got["TOKEN_SEEN_BY_OP"] != stubToken {
		t.Fatalf("env = %v", got)
	}
	if _, ok := got["MISSING"]; ok {
		t.Fatal("a variable whose secret failed was set")
	}
	// Building it again does not announce the same failure again.
	b.WorktreeEnv(ctx, "cal", wt)
	time.Sleep(150 * time.Millisecond) // past FailTTL: op runs again, still no second event
	b.WorktreeEnv(ctx, "cal", wt)

	var failed []events.Event
	for len(ch) > 0 {
		if e := <-ch; e.Type == "secret.failed" {
			failed = append(failed, e)
		}
	}
	if len(failed) != 1 {
		t.Fatalf("%d secret.failed events, want 1", len(failed))
	}
	e := failed[0]
	if e.Data["variable"] != "MISSING" || e.Data["ref"] != "op://dev/nope/field" || e.Data["location"] != "cal" || e.Data["path"] != wt.Path || !strings.Contains(e.Error, "isn't an item") || e.Data["reason"] != e.Error {
		t.Fatalf("event = %+v", e)
	}
	raw, _ := json.Marshal(e)
	for _, v := range []string{stubPassword, stubToken, "passed-through-value"} {
		if strings.Contains(string(raw), v) {
			t.Fatalf("the event carries a value: %s", raw)
		}
	}
}

func TestARecoveredSecretIsAnnounced(t *testing.T) {
	ctx := context.Background()
	b, wt, calls := secretBox(t)
	if err := b.Locations.SetLocalConfig("cal", RepoConfig{Env: map[string]string{"FLAKY": "op://dev/flaky/field"}}); err != nil {
		t.Fatal(err)
	}
	ch, stop := b.Events.Subscribe()
	defer stop()
	b.WorktreeEnv(ctx, "cal", wt)
	if e := waitEvent(t, ch, "secret.failed", "FLAKY"); !strings.Contains(e.Error, "1Password isn't signed in on") || !strings.Contains(e.Error, "berthd secret signin") {
		t.Fatalf("event = %+v", e)
	}
	os.WriteFile(filepath.Join(filepath.Dir(calls), "flaky-ok"), nil, 0o600)
	time.Sleep(150 * time.Millisecond)
	env, _ := b.WorktreeEnv(ctx, "cal", wt)
	if envMap(env)["FLAKY"] != "flaky-value-now-resolves" {
		t.Fatalf("env = %v", env)
	}
	waitEvent(t, ch, "secret.resolved", "FLAKY")
}

func waitEvent(t *testing.T, ch <-chan events.Event, typ, variable string) events.Event {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Type == typ && e.Data["variable"] == variable {
				return e
			}
		case <-timeout:
			t.Fatalf("no %s event for %s", typ, variable)
		}
	}
}

func TestConfigAndEnvironmentAPIsShowReferencesNeverValues(t *testing.T) {
	ctx := context.Background()
	repo := gitRepo(t)
	envFile := filepath.Join(t.TempDir(), "env.json")
	var bx *Box
	c, _ := servedBox(t, func(b *Box) {
		b.EnvFile = envFile
		bx = b
	})
	op, _ := stubOp(t)
	bx.Secrets = &Secrets{Op: op}
	bx.Locations.Add(ctx, "cal", repo)
	wt, _ := bx.Locations.CreateWorktree(ctx, "cal", "billing", "", "")
	call(t, c, "PUT", "/v1/locations/cal/config", "", map[string]any{"local": RepoConfig{Env: map[string]string{"DB_PASSWORD": "op://dev/db/password"}}}, nil)
	call(t, c, "PUT", "/v1/env", "", boxEnvDoc{Env: map[string]string{"TOKEN": "op://dev/token/credential", "OP_SERVICE_ACCOUNT_TOKEN": stubToken}}, nil)
	// Resolved and in memory now.
	if env, _ := bx.WorktreeEnv(ctx, "cal", wt); envMap(env)["DB_PASSWORD"] != stubPassword {
		t.Fatalf("env = %v", env)
	}
	get := func(path string) string {
		resp, err := c.Do(ctx, "GET", path, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	cfg := get("/v1/locations/cal/config")
	if strings.Contains(cfg, stubPassword) || !strings.Contains(cfg, "op://dev/db/password") {
		t.Fatalf("config = %s", cfg)
	}
	if env := get("/v1/env"); strings.Contains(env, stubPassword) || !strings.Contains(env, "op://dev/token/credential") {
		t.Fatalf("env = %s", env)
	}

	// Testing a reference says whether it resolved and how long it is.
	var res SecretTest
	if status := call(t, c, "POST", "/v1/secrets/test", "", map[string]string{"ref": "op://dev/db/password"}, &res); status != 200 || !res.OK || res.Length == nil || *res.Length != len(stubPassword) || res.Error != "" {
		t.Fatalf("test = %d %+v", status, res)
	}
	// The box environment's token reaches op for a test too.
	if call(t, c, "POST", "/v1/secrets/test", "", map[string]string{"ref": "op://dev/token/credential"}, &res); !res.OK || *res.Length != len(stubToken) {
		t.Fatalf("token test = %+v", res)
	}
	res = SecretTest{}
	if call(t, c, "POST", "/v1/secrets/test", "", map[string]string{"ref": "op://dev/nope/field"}, &res); res.OK || res.Length != nil || !strings.Contains(res.Error, "isn't an item") {
		t.Fatalf("failed test = %+v", res)
	}
	res = SecretTest{}
	if call(t, c, "POST", "/v1/secrets/test", "", map[string]string{"ref": "hunter2"}, &res); res.OK || !strings.Contains(res.Error, "not a secret reference") {
		t.Fatalf("not a reference = %+v", res)
	}
	resp, err := c.Do(ctx, "POST", "/v1/secrets/test", strings.NewReader(`{"ref":"op://dev/db/password"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), stubPassword) || strings.Contains(string(body), "pa$$") {
		t.Fatalf("the test response carries the value: %s", body)
	}
}

func TestAServiceUnitHoldsReferencesAndResolvesThemWhenItStarts(t *testing.T) {
	ctx := context.Background()
	b, _, _ := secretBox(t)
	b.Socket = "/run/berthd.sock"
	b.Locations.SetLocalConfig("cal", RepoConfig{Services: []WorktreeService{{Name: "web", Run: "pnpm dev"}}})
	specs := map[string]service.Spec{}
	ops, _ := fakeService()
	install := ops.install
	ops.install = func(s service.Spec) (string, error) {
		specs[s.Name] = s
		return install(s)
	}
	b.Units = &Units{Dir: t.TempDir(), svc: ops}
	if _, err := b.StartService(ctx, "cal", "billing", "web"); err != nil {
		t.Fatal(err)
	}
	spec := specs["svc-cal-billing-web"]
	exe, _ := os.Executable()
	if spec.Program != exe || len(spec.Args) != 8 || !reflect.DeepEqual(spec.Args[:5], []string{"secret", "exec", "--socket", "/run/berthd.sock", "--"}) || spec.Args[5] != loginShell() || spec.Args[6] != "-lc" || !strings.HasSuffix(spec.Args[7], " && pnpm dev") {
		t.Fatalf("unit = %s %q", spec.Program, spec.Args)
	}
	if spec.Env["DB_PASSWORD"] != "op://dev/db/password" || spec.Env["MISSING"] != "op://dev/nope/field" || spec.Env[SecretVarsEnv] != "DB_PASSWORD,MISSING,PASSED,TOKEN_SEEN_BY_OP" || spec.Env["PLAIN"] != "db-cal_billing" {
		t.Fatalf("unit env = %v", spec.Env)
	}
	raw, _ := json.Marshal(spec)
	if strings.Contains(string(raw), stubPassword) {
		t.Fatalf("the unit file would hold a value: %s", raw)
	}

	// A service with no references runs as it always did.
	b.Locations.SetLocalConfig("cal", RepoConfig{Env: map[string]string{"DB_PASSWORD": "x", "MISSING": "y", "PASSED": "z"}, Services: []WorktreeService{{Name: "web", Run: "pnpm dev"}}})
	saveBoxEnv(b.EnvFile, BoxEnv{})
	if _, err := b.StartService(ctx, "cal", "billing", "web"); err != nil {
		t.Fatal(err)
	}
	if spec := specs["svc-cal-billing-web"]; spec.Program == exe || spec.Env[SecretVarsEnv] != "" {
		t.Fatalf("plain unit = %s %v", spec.Program, spec.Env)
	}
}

func TestSessionsWithSecretsNeverPutAValueInTmuxsArguments(t *testing.T) {
	ctx := context.Background()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	// The real wrapper, built from this tree.
	bin := filepath.Join(t.TempDir(), "berthd")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/sean-brydon/berthd/cmd/berthd").CombinedOutput(); err != nil {
		t.Fatalf("building berthd: %v\n%s", err, out)
	}
	op, calls := stubOpFile(t)
	// The tmux server, and so the wrapper in each pane, runs the stub.
	t.Setenv("BERTH_OP", op)
	repo := gitRepo(t)
	dir := t.TempDir()
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(dir, "locations.json")), Events: &events.Bus{}, EnvFile: filepath.Join(dir, "env.json"),
		Sessions: testSessions(t), Secrets: &Secrets{Op: op}, Update: &SelfUpdate{Executable: bin}}
	var mu sync.Mutex
	var traced [][]string
	b.Sessions.trace = func(args []string) {
		mu.Lock()
		traced = append(traced, append([]string{}, args...))
		mu.Unlock()
	}
	// The box's own socket, which the wrapper reports through.
	sockDir, err := os.MkdirTemp("/tmp", "bss")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	b.Socket = filepath.Join(sockDir, "berthd.sock")
	ln, err := net.Listen("unix", b.Socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &wire.Server{}
	b.Mount(srv)
	sctx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	go srv.ServeLocal(sctx, ln)

	b.Locations.Add(ctx, "cal", repo)
	writeRepoConfig(t, repo, RepoConfig{Env: map[string]string{"SESSION_KEY": "op://dev/session/key", "MISSING": "op://dev/nope/field", "PLAIN": "plain-$BERTH_WORKTREE_NAME"}})
	trustRepo(t, b.Locations, "cal")
	wt, err := b.Locations.CreateWorktree(ctx, "cal", "billing", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ch, unsubscribe := b.Events.Subscribe()
	defer unsubscribe()

	const command = `echo "key=${#SESSION_KEY} missing=${MISSING-unset} plain=$PLAIN vars=${BERTH_SECRET_VARS-unset}"; exec sleep 60`
	sess, err := b.createSession(ctx, "agent", "cal/billing", wt.Path, command)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Command != command {
		t.Fatalf("the session's command is %q", sess.Command)
	}
	var screen string
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(screen, "key=") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		screen, _ = b.Sessions.Screen(ctx, "agent", 0)
	}
	if !strings.Contains(screen, "key=28 missing=unset plain=plain-billing vars=unset") {
		t.Fatalf("the pane saw: %s", screen)
	}
	if !strings.Contains(screen, "MISSING is not set") {
		t.Fatalf("the pane did not say what is missing: %s", screen)
	}
	// The wrapper replaced itself: the pane's own process is the program.
	out, _ := b.Sessions.tmux(ctx, "display-message", "-p", "-t", "=agent:", "#{pane_pid}")
	comm, _ := exec.Command("ps", "-o", "comm=", "-p", strings.TrimSpace(string(out))).Output()
	if got := filepath.Base(strings.TrimSpace(string(comm))); got != "sleep" {
		t.Fatalf("the pane's process is %q", got)
	}

	// No tmux command line carries a value; the new-session one carries the
	// references and the wrapper.
	mu.Lock()
	var created []string
	for _, args := range traced {
		joined := strings.Join(args, "\x00")
		if strings.Contains(joined, "session-only-secret-value-77") {
			t.Errorf("tmux got a value: %q", args)
		}
		if len(args) > 0 && args[0] == "new-session" {
			created = args
		}
	}
	mu.Unlock()
	joined := strings.Join(created, "\x00")
	for _, want := range []string{"SESSION_KEY=op://dev/session/key", "MISSING=op://dev/nope/field", SecretVarsEnv + "=MISSING,SESSION_KEY", "PLAIN=plain-billing", "\x00--\x00" + bin + "\x00secret\x00exec\x00--socket\x00" + b.Socket + "\x00--\x00/bin/sh\x00-lc\x00. '" + b.Sessions.commandPath("agent") + "'\x00"} {
		if !strings.Contains(joined, want) {
			t.Errorf("new-session lacks %q: %q", want, created)
		}
	}

	// The wrapper told the box, and the failure became one event.
	e := waitEvent(t, ch, "secret.failed", "MISSING")
	if e.Data["location"] != "cal" || e.Data["path"] != wt.Path || !strings.Contains(e.Error, "isn't an item") {
		t.Fatalf("event = %+v", e)
	}
	b.secrets().mu.Lock()
	cached := len(b.secrets().cache)
	b.secrets().mu.Unlock()
	if cached != 0 || opCalls(t, calls) != 2 {
		t.Fatalf("the box resolved the session's secrets itself (%d cached, %d op calls)", cached, opCalls(t, calls))
	}

	// A remote peer may not report.
	if call := func() int {
		c, _ := servedBox(t)
		return call(t, c, "POST", "/v1/secrets/report", "", SecretReport{Results: []SecretResult{{Variable: "X", Ref: "op://a/b/c", Reason: "no"}}}, nil)
	}(); call != 403 {
		t.Fatalf("a remote report gave %d", call)
	}
}

func TestSessionsWithoutSecretsStartAsTheyAlwaysDid(t *testing.T) {
	ctx := context.Background()
	repo := gitRepo(t)
	dir := t.TempDir()
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(dir, "locations.json")), Events: &events.Bus{}, Sessions: testSessions(t)}
	var created []string
	b.Sessions.trace = func(args []string) {
		if len(args) > 0 && args[0] == "new-session" {
			created = append([]string{}, args...)
		}
	}
	writeRepoConfig(t, repo, RepoConfig{Env: map[string]string{"PLAIN": "plain"}})
	b.Locations.Add(ctx, "cal", repo)
	trustRepo(t, b.Locations, "cal")
	wt, _ := b.Locations.CreateWorktree(ctx, "cal", "billing", "", "")
	if _, err := b.createSession(ctx, "plain", "cal/billing", wt.Path, "sleep 30"); err != nil {
		t.Fatal(err)
	}
	env, _ := b.WorktreeEnv(ctx, "cal", wt)
	want := []string{"new-session", "-d", "-s", "plain", "-c", wt.Path, "-x", "200", "-y", "50"}
	for _, kv := range env {
		want = append(want, "-e", kv)
	}
	// Every session tells its agent's hooks which session they are in.
	// The command runs from its file, never from tmux's command line.
	want = append(want, "-e", "BERTH_SESSION=plain", "--", "/bin/sh", "-lc", ". '"+b.Sessions.commandPath("plain")+"'")
	if len(created) < len(want) || !reflect.DeepEqual(created[:len(want)], want) {
		t.Fatalf("new-session = %q\nwant %q", created, want)
	}
	if got, err := readCommand(b.Sessions.commandPath("plain")); err != nil || got != "sleep 30" {
		t.Fatalf("command file = %q, %v", got, err)
	}
}

// op asks at its own terminal (it opens /dev/tty) when it isn't signed in:
// "add an account? [Y/n]". berthd runs it with no terminal at all, so a
// read fails at once, saying how to sign in, and never waits for a person.
func TestOpNeverAsksAndSaysHowToSignIn(t *testing.T) {
	dir := t.TempDir()
	op := filepath.Join(dir, "op")
	os.WriteFile(op, []byte(`#!/bin/sh
if [ -n "$OP_SESSION_abc" ]; then
  [ "$1 $2" = "read op://dev/db/password" ] && { printf 'from-the-session'; exit 0; }
  [ "$1" = whoami ] && { echo "dev@acme.test"; exit 0; }
fi
if (exec 3<>/dev/tty) 2>/dev/null; then
  echo "asked" >> "`+dir+`/asked"
  printf 'No accounts configured for use with 1Password CLI. Do you want to add an account manually now? [Y/n] ' >/dev/tty
  read -r answer </dev/tty
fi
echo '[ERROR] 2024/01/01 00:00:00 no accounts configured for use with 1Password CLI' >&2
exit 1
`), 0o755)
	session := filepath.Join(dir, "box", "op-session")
	s := &Secrets{Op: op, SessionFile: session, Box: "devbox", Timeout: 5 * time.Second}
	start := time.Now()
	_, err := s.Resolve(context.Background(), "op://dev/db/password", nil, true)
	if err == nil || !errors.Is(err, ErrOpSignedOut) || err.Error() != "1Password isn't signed in on devbox: sign in from Team setup or run `berthd secret signin` on the box" {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("op waited %s", time.Since(start))
	}
	if _, err := os.Stat(filepath.Join(dir, "asked")); err == nil {
		t.Fatal("op had a terminal to ask on")
	}
	// Whatever terminal berthd's caller has (a service's, through berthd
	// secret exec), op runs in a session of its own, without it.
	if c := s.opCommand(context.Background(), op, nil, "read", "x"); c.SysProcAttr == nil || !c.SysProcAttr.Setsid || c.Stdin != nil {
		t.Fatalf("op could reach a terminal: %+v", c.SysProcAttr)
	}
	if _, err := s.OpWhoami(context.Background(), nil); !errors.Is(err, ErrOpSignedOut) {
		t.Fatalf("whoami: %v", err)
	}
	// berthd secret signin keeps op signin's session; every read then
	// passes it to op.
	vars := ParseOpSignin("export OP_SESSION_abc=\"tok-123\"\n# This command is meant to be used with your shell's eval function.\n")
	if len(vars) != 1 || vars[0] != "OP_SESSION_abc=tok-123" {
		t.Fatalf("parsed %v", vars)
	}
	if err := SaveOpSession(session, append(vars, "PATH=/evil")); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(session); st.Mode().Perm() != 0o600 {
		t.Fatalf("session file mode %v", st.Mode())
	}
	if got := ReadOpSession(session); len(got) != 1 || got[0] != "OP_SESSION_abc=tok-123" {
		t.Fatalf("read %v", got)
	}
	if v, err := s.Resolve(context.Background(), "op://dev/db/password", nil, true); err != nil || v != "from-the-session" {
		t.Fatalf("with the session: %q %v", v, err)
	}
	if who, err := s.OpWhoami(context.Background(), nil); err != nil || who != "dev@acme.test" {
		t.Fatalf("whoami: %q %v", who, err)
	}
}
