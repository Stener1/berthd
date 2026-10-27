package box

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/events"
	"github.com/cosscom/shipyard/internal/hooks"
	"github.com/cosscom/shipyard/internal/service"
	"github.com/cosscom/shipyard/internal/wire"
)

// envRig is a box with helpers and a project "acme" that has no Shipyard
// environment at all: what its worktrees have is in their dotenv files.
type envRig struct {
	t     *testing.T
	c     *wire.Client
	box   *Box
	h     *Helpers
	logs  *logLines
	units *unitSpecs
	repo  string
	wt    Worktree
}

func newEnvRig(t *testing.T, env map[string]string, opts ...func(*Box)) *envRig {
	t.Helper()
	t.Setenv("SHELL", "/bin/sh")
	h, logs := testHelpers(t)
	r := &envRig{t: t, h: h, logs: logs, units: &unitSpecs{specs: map[string]service.Spec{}}, repo: gitRepo(t)}
	var bus *events.Bus
	r.c, bus = servedBox(t, append([]func(*Box){func(b *Box) {
		b.Helpers = h
		b.Units = &Units{Dir: t.TempDir(), svc: r.units.ops()}
		r.box = b
	}}, opts...)...)
	// As berthd wires it: every worktree.removed is one the helpers forget.
	t.Cleanup(h.Watch(bus))
	ctx := context.Background()
	if _, err := r.box.Locations.Add(ctx, "acme", r.repo); err != nil {
		t.Fatal(err)
	}
	if err := r.box.Locations.SetLocalConfig("acme", RepoConfig{Env: env, Services: []WorktreeService{{Name: "web", Run: "pnpm dev"}}}); err != nil {
		t.Fatal(err)
	}
	var err error
	if r.wt, err = r.box.Locations.CreateWorktree(ctx, "acme", "fix-orders", "", ""); err != nil {
		t.Fatal(err)
	}
	return r
}

// install installs the fake helper for a plugin, as its manifest is changed
// by mod, with files put in its data folder before it starts.
func (r *envRig) install(plugin string, mod func(*HelperSpec), files map[string]string) {
	r.t.Helper()
	data := r.h.dataDir(plugin)
	if err := os.MkdirAll(data, 0o700); err != nil {
		r.t.Fatal(err)
	}
	for name, content := range files {
		r.inData(plugin, name, content)
	}
	_, program := fakeHelperBuild(r.t)
	spec := fakeSpec(program)
	spec.Detach = "/api/detach"
	if mod != nil {
		mod(&spec)
	}
	if _, err := r.h.Install(context.Background(), plugin, spec, bytes.NewReader(program)); err != nil {
		r.t.Fatal(err)
	}
	waitUntil(r.t, "the "+plugin+" helper to run", 20*time.Second, func() bool { return r.h.Status(plugin).State == HelperRunning })
}

// inData writes a file the fake helper reads; "" removes it.
func (r *envRig) inData(plugin, name, content string) {
	r.t.Helper()
	path := filepath.Join(r.h.dataDir(plugin), name)
	if content == "" {
		os.Remove(path)
		return
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// dotenv writes a dotenv file in a folder; "" removes it.
func (r *envRig) dotenv(dir, name, text string) {
	r.t.Helper()
	path := filepath.Join(dir, name)
	if text == "" {
		os.Remove(path)
		return
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// look asks the box to tell a plugin's helper of a worktree's dotenv files.
func (r *envRig) look(plugin, body string) (int, string, HelperEnvLook) {
	r.t.Helper()
	status, raw := helperCall(r.t, r.c, "POST", "/v1/helpers/"+plugin+"/attach-env", []byte(body), nil)
	var look HelperEnvLook
	if status == 200 {
		if err := json.Unmarshal([]byte(raw), &look); err != nil {
			r.t.Fatalf("the answer is not a look: %v\n%s", err, raw)
		}
	}
	return status, raw, look
}

// told is the last attach the fake helper answered.
func (r *envRig) told(plugin string) HelperAttach {
	r.t.Helper()
	var told HelperAttach
	if err := json.Unmarshal([]byte(readIn(r.t, r.h, plugin, "attach-request")), &told); err != nil {
		r.t.Fatal(err)
	}
	return told
}

// The values these tests put in dotenv files. None may be in an answer, a
// log or an error.
var dotenvSecrets = []string{"hunter2", "postgres://", "s3cret", "sekret", "op://", "team=acme"}

func holdsNone(t *testing.T, what, text string) {
	t.Helper()
	for _, secret := range dotenvSecrets {
		if strings.Contains(text, secret) {
			t.Errorf("%s holds a value of the worktree's .env files (%q):\n%s", what, secret, text)
		}
	}
}

func TestAHelperIsToldOfAWorktreesDotenvFilesWhenAsked(t *testing.T) {
	r := newEnvRig(t, nil)
	const token = "tok3n_For-berthd.only"
	r.install("acme", nil, map[string]string{"token": token})
	const body = `{"location": "acme", "worktree": "fix-orders"}`

	// ---- No dotenv file: that is an answer, and the helper is still told,
	// with nothing, so what an earlier look found is no longer so.
	status, raw, look := r.look("acme", body)
	if status != 200 {
		t.Fatalf("a worktree with no dotenv file: %d %s", status, raw)
	}
	want := HelperEnvLook{Plugin: "acme", Location: "acme", Worktree: "fix-orders", Command: ".env",
		Files: []string{}, Skipped: []dotenvSkipped{}, Variables: []HelperEnvVariable{}, Answer: map[string]json.RawMessage{}}
	if !reflect.DeepEqual(look, want) {
		t.Errorf("with no dotenv file the answer is %+v", look)
	}
	// Lists are lists, never null.
	for _, field := range []string{`"files":[]`, `"skipped":[]`, `"variables":[]`, `"answer":{}`} {
		if !strings.Contains(raw, field) {
			t.Errorf("the answer has no %s: %s", field, raw)
		}
	}
	told := r.told("acme")
	if told.Command != ".env" || told.Env == nil || len(told.Env) != 0 {
		t.Errorf("with no dotenv file the helper was told %+v", told)
	}

	// ---- Found: the variables the manifest names, the last file's value
	// for each, and nothing else of the files.
	r.dotenv(r.wt.Path, ".env", "# acme\nDATABASE_URL=postgres://app:hunter2@localhost:5432/acme\nexport OTEL_RESOURCE_ATTRIBUTES='team=acme'\nUNRELATED=s3cret-unrelated\nSTRIPE_KEY=sekret\n")
	r.dotenv(r.wt.Path, ".env.local", "DB_NAME=acme_fix_orders\nDATABASE_URL=\"postgres://app:hunter2@127.0.0.1:5432/${DB_NAME}\"\n")
	r.dotenv(r.wt.Path, ".env.production", "DATABASE_URL=postgres://app:hunter2@db.acme.example/prod\n")
	r.inData("acme", "attach-answer", `{"env": {"DATABASE_URL": "postgres://app:hunter2@127.0.0.1:25432/acme"},
		"source": "acme/fix-orders", "connections": ["c_1c28ba20e1b6"]}`)
	status, raw, look = r.look("acme", body)
	if status != 200 {
		t.Fatalf("a worktree with dotenv files: %d %s", status, raw)
	}
	if !reflect.DeepEqual(look.Files, []string{".env", ".env.local"}) || len(look.Skipped) != 0 {
		t.Errorf("files read = %v, skipped %v", look.Files, look.Skipped)
	}
	if !reflect.DeepEqual(look.Variables, []HelperEnvVariable{
		{Name: "DATABASE_URL", File: ".env.local", Sent: true},
		{Name: "OTEL_RESOURCE_ATTRIBUTES", File: ".env", Sent: true},
	}) {
		t.Errorf("variables = %+v", look.Variables)
	}
	// The helper's own answer, without what it would set for a service.
	if string(look.Answer["source"]) != `"acme/fix-orders"` || string(look.Answer["connections"]) != `["c_1c28ba20e1b6"]` || len(look.Answer) != 2 {
		t.Errorf("the helper's answer came back as %s", raw)
	}
	holdsNone(t, "the answer", raw)

	// What the helper was told: the worktree as for a service, ".env" where
	// the service's name would be, and its token.
	told = r.told("acme")
	if told.Box != "devbox" || told.Project != "acme" || told.Worktree != "fix-orders" || told.Branch != r.wt.Branch || told.Path != r.wt.Path || told.Command != ".env" {
		t.Errorf("the helper was told %+v", told)
	}
	if !reflect.DeepEqual(told.Env, map[string]string{
		"DATABASE_URL":             "postgres://app:hunter2@127.0.0.1:5432/acme_fix_orders",
		"OTEL_RESOURCE_ATTRIBUTES": "team=acme",
	}) {
		t.Errorf("the helper was given the variables %v", told.Env)
	}
	if got := readIn(t, r.h, "acme", "attach-authorization"); got != "Bearer "+token {
		t.Errorf("the helper was sent the Authorization %q", got)
	}

	// Nothing was started, so no service is noted as pointing at the helper.
	if got := r.h.attachedTo("acme"); len(got) != 0 || exists(r.h.attachedPath("acme")) {
		t.Errorf("attached.json names %+v after a look", got)
	}
	if env := r.units.env(serviceUnit("acme", "fix-orders", "web")); env != nil {
		t.Errorf("a look started a service: %v", env)
	}

	// ---- Found, and not one to tell a helper: a secret's name, a
	// reference these files do not fill in, and nothing at all.
	r.dotenv(r.wt.Path, ".env", "DATABASE_URL=postgres://${PGUSER}:hunter2@localhost/acme\nAPI_TOKEN=op://dev/acme-api/token\nOTEL_SERVICE_NAME=\nOTEL_LONG="+strings.Repeat("x", maxAttachValue+1)+"\n")
	r.dotenv(r.wt.Path, ".env.local", "")
	status, raw, look = r.look("acme", body)
	if status != 200 {
		t.Fatalf("a worktree whose variables are not usable: %d %s", status, raw)
	}
	if !reflect.DeepEqual(look.Variables, []HelperEnvVariable{
		{Name: "API_TOKEN", File: ".env", Reason: "secret"},
		{Name: "DATABASE_URL", File: ".env", Reason: "unresolved"},
		{Name: "OTEL_LONG", File: ".env", Reason: "too_long"},
		{Name: "OTEL_SERVICE_NAME", File: ".env", Reason: "empty"},
	}) {
		t.Errorf("variables = %+v", look.Variables)
	}
	if told = r.told("acme"); len(told.Env) != 0 || told.Command != ".env" {
		t.Errorf("the helper was given %v", told.Env)
	}
	holdsNone(t, "the answer", raw)

	// ---- A file that is a link out of the worktree is said, not read.
	outside := filepath.Join(filepath.Dir(r.repo), "outside.env")
	r.dotenv(filepath.Dir(outside), filepath.Base(outside), "DATABASE_URL=postgres://app:hunter2@localhost/elsewhere\n")
	r.dotenv(r.wt.Path, ".env", "")
	if err := os.Symlink(outside, filepath.Join(r.wt.Path, ".env")); err != nil {
		t.Fatal(err)
	}
	status, raw, look = r.look("acme", body)
	if status != 200 || len(look.Files) != 0 || len(look.Variables) != 0 || !reflect.DeepEqual(look.Skipped, []dotenvSkipped{{".env", "outside"}}) {
		t.Errorf("a link out of the worktree: %d %s", status, raw)
	}
	if told = r.told("acme"); len(told.Env) != 0 {
		t.Errorf("the helper was given what a link out of the worktree holds: %v", told.Env)
	}
	os.Remove(filepath.Join(r.wt.Path, ".env"))

	// ---- The project's own checkout, by its name or by none: the helper
	// files it under the project.
	r.dotenv(r.repo, ".env", "DATABASE_URL=postgres://app:hunter2@localhost:5432/acme\n")
	for _, name := range []string{"", filepath.Base(r.repo)} {
		status, raw, look = r.look("acme", `{"location": "acme", "worktree": "`+name+`"}`)
		if status != 200 || look.Worktree != filepath.Base(r.repo) || len(look.Variables) != 1 || !look.Variables[0].Sent {
			t.Fatalf("the main checkout as %q: %d %s", name, status, raw)
		}
		if told = r.told("acme"); told.Project != "acme" || told.Worktree != "" || told.Path != r.repo || told.Env["DATABASE_URL"] == "" {
			t.Errorf("for the main checkout the helper was told %+v", told.Project+"/"+told.Worktree)
		}
	}

	// ---- What cannot be looked at.
	for _, tc := range []struct {
		plugin, body string
		status       int
		code         string
	}{
		{"acme", `{"location": "acme", "worktree": "no-such-worktree"}`, 404, CodeNotFound},
		{"acme", `{"location": "no-such-project", "worktree": "fix-orders"}`, 404, CodeNotFound},
		{"acme", `{"worktree": "fix-orders"}`, 400, CodeBadRequest},
		{"acme", `not json`, 400, CodeBadRequest},
		{"Not_A_Plugin", body, 400, "helper_invalid"},
		// Another plugin has no helper here.
		{"other", body, 409, "helper_off"},
	} {
		if status, raw, _ := r.look(tc.plugin, tc.body); status != tc.status || !strings.Contains(raw, `"code":"`+tc.code+`"`) {
			t.Errorf("%s asked with %s: %d %s, want %d %s", tc.plugin, tc.body, status, raw, tc.status, tc.code)
		}
	}
	// A path is not a worktree's name, and no other file is ever read.
	for _, name := range []string{"../" + filepath.Base(filepath.Dir(r.repo)), "/etc", r.wt.Path, "fix-orders/../.."} {
		quoted, _ := json.Marshal(name)
		if status, raw, _ := r.look("acme", `{"location": "acme", "worktree": `+string(quoted)+`}`); status != 404 {
			t.Errorf("the worktree %q: %d %s", name, status, raw)
		}
	}

	// ---- When the worktree is removed the helper is told, as for one
	// whose service it knew.
	if status := call(t, r.c, "DELETE", "/v1/locations/acme/worktrees/fix-orders?force=1", "", nil, nil); status != 200 {
		t.Fatalf("removing the worktree: %d", status)
	}
	waitUntil(t, "the helper to be told that fix-orders is gone", 20*time.Second, func() bool {
		return strings.Contains(string(mustRead(filepath.Join(r.h.dataDir("acme"), "detach-requests"))), `"worktree":"fix-orders"`)
	})
	if status, raw, _ := r.look("acme", body); status != 404 {
		t.Errorf("a look at a worktree that is gone: %d %s", status, raw)
	}

	holdsNone(t, "berthd's log", r.logs.String())
}

func mustRead(path string) []byte {
	raw, _ := os.ReadFile(path)
	return raw
}

func TestAHelperThatIsOffSlowOrWrongIsAnErrorOfALookAndNoFileIsRead(t *testing.T) {
	r := newEnvRig(t, nil)
	r.h.attachTimeout = 300 * time.Millisecond
	r.dotenv(r.wt.Path, ".env", "DATABASE_URL=postgres://app:hunter2@localhost:5432/acme\n")
	const body = `{"location": "acme", "worktree": "fix-orders"}`
	refused := func(what string, status int, code string) string {
		t.Helper()
		began := time.Now()
		got, raw, _ := r.look("acme", body)
		if got != status || !strings.Contains(raw, `"code":"`+code+`"`) {
			t.Errorf("%s: %d %s, want %d %s", what, got, raw, status, code)
		}
		if took := time.Since(began); took > 5*time.Second {
			t.Errorf("%s: the look waited %s", what, took)
		}
		holdsNone(t, what+": the answer", raw)
		return raw
	}

	// No helper: nothing to tell, so nothing is read.
	reads := dotenvReads.Load()
	refused("no helper", 409, "helper_off")

	// A helper from a manifest that names no attach path: one berthd never
	// tells of a worktree.
	r.install("acme", func(s *HelperSpec) { s.Attach, s.AttachEnv = "", nil }, nil)
	raw := refused("a helper with no attach path", 409, "helper_no_attach")
	if !strings.Contains(raw, "names no attach path") {
		t.Errorf("it does not say why: %s", raw)
	}
	if got := dotenvReads.Load(); got != reads {
		t.Errorf("the worktree's dotenv files were read %d times with no helper to tell", got-reads)
	}
	if exists(filepath.Join(r.h.dataDir("acme"), "attach-request")) {
		t.Error("a helper with no attach path was told")
	}

	// One that is told, and is slow, answers an error, or answers something
	// that is not an answer.
	r.install("acme", nil, nil)
	if status, raw, look := r.look("acme", body); status != 200 || len(look.Variables) != 1 {
		t.Fatalf("a helper that answers: %d %s", status, raw)
	}
	r.inData("acme", "attach-delay", "10s")
	if raw = refused("a slow helper", 502, "helper_unreachable"); !strings.Contains(raw, "did not answer in time") {
		t.Errorf("a slow helper: %s", raw)
	}
	r.inData("acme", "attach-delay", "")
	r.inData("acme", "attach-status", "500")
	refused("a helper that answers an error", 502, "helper_unreachable")
	r.inData("acme", "attach-status", "")
	for _, answer := range []string{`<html>`, `["a list"]`, `"postgres://app:hunter2@localhost/acme"`} {
		r.inData("acme", "attach-answer", answer)
		refused("a helper that answers "+answer, 502, "helper_unreachable")
	}
	// It works again once the helper does.
	r.inData("acme", "attach-answer", `{"connections": []}`)
	if status, raw, look := r.look("acme", body); status != 200 || string(look.Answer["connections"]) != `[]` {
		t.Fatalf("once the helper answers again: %d %s", status, raw)
	}

	// Removed.
	if _, err := r.h.Remove("acme", false); err != nil {
		t.Fatal(err)
	}
	reads = dotenvReads.Load()
	refused("a helper that was removed", 409, "helper_off")
	if got := dotenvReads.Load(); got != reads {
		t.Error("the worktree's dotenv files were read for a helper that was removed")
	}
	if !strings.Contains(r.logs.String(), "did not answer in time") {
		t.Errorf("berthd's log does not say the helper was slow:\n%s", r.logs)
	}
	holdsNone(t, "berthd's log", r.logs.String())
}

// Like installing one, having a helper told of a worktree is a paired
// laptop's to ask, and a gate on the box can refuse it.
func TestOnlyAPairedLaptopHasAHelperToldOfDotenvFilesAndAGateCanRefuseIt(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "hooks.json")
	if err := os.WriteFile(cfg, []byte(`{"hooks":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newEnvRig(t, nil, func(b *Box) { b.Hooks = &hooks.Runner{Path: cfg} })
	r.install("acme", nil, nil)
	r.dotenv(r.wt.Path, ".env", "DATABASE_URL=postgres://app:hunter2@localhost:5432/acme\n")
	ctx := context.Background()

	local := localBox(t, r.box)
	_, err := local.AttachHelperEnv(ctx, "acme", "acme", "fix-orders")
	var he *HelperError
	if !errors.As(err, &he) || he.Status != 403 || he.Code != CodeRefused {
		t.Fatalf("a look asked from the box itself: %v", err)
	}
	if exists(filepath.Join(r.h.dataDir("acme"), "attach-request")) {
		t.Fatal("the helper was told from the box itself")
	}

	// A paired laptop's look goes through, and names what it found.
	remote := NewClient(r.c)
	look, err := remote.AttachHelperEnv(ctx, "acme", "acme", "fix-orders")
	if err != nil || len(look.Variables) != 1 || look.Variables[0].Name != "DATABASE_URL" || !look.Variables[0].Sent {
		t.Fatalf("a look from a paired laptop: %+v, %v", look, err)
	}

	// Nor is a database said by hand from the box itself.
	const byHand = "postgres://app:hunter2@localhost:5432/acme_by_hand"
	os.Remove(filepath.Join(r.h.dataDir("acme"), "attach-request"))
	_, err = local.AttachHelperGiven(ctx, "acme", "acme", "fix-orders", map[string]string{"DATABASE_URL": byHand})
	if !errors.As(err, &he) || he.Status != 403 || he.Code != CodeRefused || strings.Contains(he.Message, "hunter2") {
		t.Fatalf("a database given from the box itself: %v", err)
	}
	if exists(filepath.Join(r.h.dataDir("acme"), "attach-request")) {
		t.Fatal("the helper was told from the box itself")
	}

	// Until a gate says no. It is told which helper, which worktree and
	// how, and nothing of what was given.
	seen := filepath.Join(t.TempDir(), "gate-saw")
	hook := `{"hooks":[{"on":"before:helper.attach","run":"env | grep -e '^BERTH_' > ` + seen + `; echo not $BERTH_PLUGIN for $BERTH_WORKTREE from $BERTH_FROM; exit 1"}]}`
	if err := os.WriteFile(cfg, []byte(hook), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(r.h.dataDir("acme"), "attach-request"))
	reads := dotenvReads.Load()
	_, err = remote.AttachHelperEnv(ctx, "acme", "acme", "fix-orders")
	if !errors.As(err, &he) || he.Status != 403 || he.Code != CodeRefused || !strings.Contains(he.Message, "not acme for fix-orders from .env") {
		t.Fatalf("a look a gate refuses: %v", err)
	}
	_, err = remote.AttachHelperGiven(ctx, "acme", "acme", "fix-orders", map[string]string{"DATABASE_URL": byHand})
	if !errors.As(err, &he) || he.Status != 403 || he.Code != CodeRefused || !strings.Contains(he.Message, "not acme for fix-orders from .manual") {
		t.Fatalf("a database given by hand that a gate refuses: %v", err)
	}
	if exists(filepath.Join(r.h.dataDir("acme"), "attach-request")) || dotenvReads.Load() != reads {
		t.Fatal("what a gate refused read the files or told the helper")
	}
	holdsNone(t, "what the gate was told", string(mustRead(seen)))
	holdsNone(t, "berthd's log", r.logs.String())
}

// The person may say which database a worktree has: the helper is told
// that, under ".manual", and no file is read. What was said is a secret: it
// is in no answer, no log and no error.
func TestAHelperIsToldOfADatabaseGivenByHand(t *testing.T) {
	r := newEnvRig(t, nil)
	const token = "tok3n_For-berthd.only"
	r.install("acme", nil, map[string]string{"token": token})
	// What the worktree's own file says is another matter, and is not read.
	r.dotenv(r.wt.Path, ".env", "DATABASE_URL=postgres://app:hunter2@localhost:5432/acme_in_the_file\n")
	const byHand = "postgres://app:s3cret-by-hand@localhost:5432/acme_by_hand"
	give := func(env string) (int, string, HelperEnvLook) {
		t.Helper()
		status, raw, look := r.look("acme", `{"location": "acme", "worktree": "fix-orders", "env": `+env+`}`)
		holdsNone(t, "the answer", raw)
		if strings.Contains(raw, "acme_by_hand") {
			t.Errorf("the answer holds the address that was given: %s", raw)
		}
		return status, raw, look
	}

	// ---- Given: the helper is told it, as it is told of a service.
	r.inData("acme", "attach-answer", `{"env": {"DATABASE_URL": "postgres://app:hunter2@127.0.0.1:25432/acme"}, "source": "acme/fix-orders", "connections": ["c_7d90a3b1e44f"]}`)
	reads := dotenvReads.Load()
	status, raw, look := give(`{"DATABASE_URL": "` + byHand + `"}`)
	if status != 200 {
		t.Fatalf("a database given by hand: %d %s", status, raw)
	}
	if got := dotenvReads.Load(); got != reads {
		t.Errorf("the worktree's dotenv files were read %d times for a database given by hand", got-reads)
	}
	if look.Command != ".manual" || len(look.Files) != 0 || len(look.Skipped) != 0 ||
		!reflect.DeepEqual(look.Variables, []HelperEnvVariable{{Name: "DATABASE_URL", Sent: true}}) ||
		string(look.Answer["connections"]) != `["c_7d90a3b1e44f"]` || len(look.Answer) != 2 {
		t.Errorf("the answer to a database given by hand: %s", raw)
	}
	told := r.told("acme")
	if told.Box != "devbox" || told.Project != "acme" || told.Worktree != "fix-orders" || told.Path != r.wt.Path || told.Command != ".manual" ||
		!reflect.DeepEqual(told.Env, map[string]string{"DATABASE_URL": byHand}) {
		t.Errorf("the helper was told %s %s/%s with %d variables", told.Command, told.Project, told.Worktree, len(told.Env))
	}
	if got := readIn(t, r.h, "acme", "attach-authorization"); got != "Bearer "+token {
		t.Errorf("the helper was sent the Authorization %q", got)
	}
	if got := r.h.attachedTo("acme"); len(got) != 0 || exists(r.h.attachedPath("acme")) {
		t.Errorf("attached.json names %+v after a database was given by hand", got)
	}

	// ---- A look in the files afterwards is its own: ".env", with the
	// file's value, and what was given by hand is not sent again.
	if status, raw, look = r.look("acme", `{"location": "acme", "worktree": "fix-orders", "env": null}`); status != 200 || look.Command != ".env" || len(look.Files) != 1 {
		t.Fatalf("a look with env null: %d %s", status, raw)
	}
	if told = r.told("acme"); told.Command != ".env" || told.Env["DATABASE_URL"] != "postgres://app:hunter2@localhost:5432/acme_in_the_file" {
		t.Errorf("a look after a database was given by hand told the helper %s", told.Command)
	}

	// ---- Not given: a variable the manifest does not name, a name that is
	// none (an address put where the name goes is not repeated), a secret's
	// name, nothing, and too much. The helper is not told any of them.
	os.Remove(filepath.Join(r.h.dataDir("acme"), "attach-request"))
	for what, tc := range map[string]struct{ env, says string }{
		"a variable the manifest does not name": {`{"UNRELATED": "` + byHand + `"}`, "UNRELATED is not a variable the acme helper is told"},
		"one it names beside one it does not":   {`{"DATABASE_URL": "` + byHand + `", "PGPASSWORD": "s3cret"}`, "PGPASSWORD is not a variable the acme helper is told"},
		"an address where the name goes":        {`{"` + byHand + `": "DATABASE_URL"}`, "env has a name that is not a variable's"},
		"one of Shipyard's own":                 {`{"BERTH_PORT": "1"}`, "BERTH_PORT is not a variable"},
		"a secret's name":                       {`{"DATABASE_URL": "op://dev/acme-db/url"}`, "DATABASE_URL names a secret"},
		"nothing":                               {`{"DATABASE_URL": ""}`, "DATABASE_URL must be text"},
		"too much":                              {`{"DATABASE_URL": "postgres://` + strings.Repeat("x", maxAttachValue) + `"}`, "DATABASE_URL must be text"},
		"not text":                              {`{"DATABASE_URL": 5432}`, "invalid request body"},
		"not variables":                         {`["` + byHand + `"]`, "invalid request body"},
	} {
		status, raw, _ := give(tc.env)
		if status != 400 || !strings.Contains(raw, `"code":"`+CodeBadRequest+`"`) || !strings.Contains(raw, tc.says) {
			t.Errorf("%s: %d %s, want 400 saying %q", what, status, raw, tc.says)
		}
	}
	if exists(filepath.Join(r.h.dataDir("acme"), "attach-request")) {
		t.Error("the helper was told something that was refused")
	}

	// ---- Taken back: the helper is told ".manual" with nothing.
	r.inData("acme", "attach-answer", `{"source": "acme/fix-orders", "connections": []}`)
	status, raw, look = give(`{}`)
	if status != 200 || look.Command != ".manual" || len(look.Variables) != 0 || string(look.Answer["connections"]) != `[]` {
		t.Fatalf("taking back what was given: %d %s", status, raw)
	}
	if told = r.told("acme"); told.Command != ".manual" || told.Env == nil || len(told.Env) != 0 {
		t.Errorf("to take it back the helper was told %s with %d variables", told.Command, len(told.Env))
	}

	// ---- A helper that fails, is slow or is off is an error, in which the
	// address is not repeated; nor is it in berthd's log.
	r.h.attachTimeout = 300 * time.Millisecond
	r.inData("acme", "attach-status", "500")
	if status, raw, _ = give(`{"DATABASE_URL": "` + byHand + `"}`); status != 502 || !strings.Contains(raw, `"code":"helper_unreachable"`) {
		t.Errorf("a helper that answers an error: %d %s", status, raw)
	}
	r.inData("acme", "attach-status", "")
	r.inData("acme", "attach-delay", "10s")
	if status, raw, _ = give(`{"DATABASE_URL": "` + byHand + `"}`); status != 502 || !strings.Contains(raw, "did not answer in time") {
		t.Errorf("a slow helper: %d %s", status, raw)
	}
	r.inData("acme", "attach-delay", "")
	// One that echoes what it was told has its "env" dropped, as ever.
	r.inData("acme", "attach-answer", `{"env": {"DATABASE_URL": "`+byHand+`"}, "connections": ["c_7d90a3b1e44f"]}`)
	if status, raw, look = give(`{"DATABASE_URL": "` + byHand + `"}`); status != 200 || len(look.Answer) != 1 {
		t.Errorf("a helper that answers with what it was told: %d %s", status, raw)
	}
	if _, err := r.h.Remove("acme", false); err != nil {
		t.Fatal(err)
	}
	if status, raw, _ = give(`{"DATABASE_URL": "` + byHand + `"}`); status != 409 || !strings.Contains(raw, `"code":"helper_off"`) {
		t.Errorf("a helper that is off: %d %s", status, raw)
	}
	logs := r.logs.String()
	if !strings.Contains(logs, "not told of acme/fix-orders's .manual") {
		t.Errorf("berthd's log does not say the helper was not told:\n%s", logs)
	}
	holdsNone(t, "berthd's log", logs)
	if strings.Contains(logs, "acme_by_hand") {
		t.Errorf("berthd's log holds the address that was given:\n%s", logs)
	}
}

// At a service's start a helper is shown what the worktree's dotenv files
// have of the variables it names, when Shipyard's own environment for the
// service does not have them, and what it answers is what the service
// starts with.
func TestAServiceIsShownItsWorktreesDotenvValues(t *testing.T) {
	ctx := context.Background()
	const inFile = "postgres://app:hunter2@localhost:5432/acme_fix_orders"
	const proxied = "postgres://app:hunter2@127.0.0.1:25432/acme_fix_orders"
	t.Setenv("ACME_API_TOKEN", "sekret-acme-token")
	r := newEnvRig(t, map[string]string{
		// The project's own settings, which a file never replaces: a value,
		// and a secret's name.
		"OTEL_SERVICE_NAME": "the-projects-own",
		"API_TOKEN":         "env://ACME_API_TOKEN",
		"DB_NAME":           "acme_fix_orders",
	})
	unit := serviceUnit("acme", "fix-orders", "web")
	web := HelperService{Location: "acme", Worktree: "fix-orders", Service: "web"}
	r.dotenv(r.wt.Path, ".env", "DATABASE_URL=postgres://app:hunter2@localhost:5432/${DB_NAME}\nDB_NAME=the_files_own\n"+
		"OTEL_SERVICE_NAME=from-the-file\nOTEL_RESOURCE_ATTRIBUTES=team=acme\nOTEL_EXPORTER_OTLP_HEADERS=op://dev/acme-otel/headers\n"+
		"API_TOKEN=s3cret-in-the-file\nUNRELATED=s3cret-unrelated\nBERTH_PORT=1\n")
	start := func() map[string]string {
		t.Helper()
		if _, err := r.box.StartService(ctx, "acme", "fix-orders", "web"); err != nil {
			t.Fatal(err)
		}
		return r.units.env(unit)
	}

	// ---- No helper is running: no file is opened, and the service starts
	// as the project wrote it.
	reads := dotenvReads.Load()
	plain := start()
	if got := dotenvReads.Load(); got != reads {
		t.Fatalf("the worktree's dotenv files were read %d times with no helper running", got-reads)
	}
	if _, set := plain["DATABASE_URL"]; set || plain["OTEL_SERVICE_NAME"] != "the-projects-own" {
		t.Fatalf("env with no helper = %v", plain)
	}
	// Nor for a helper that is told of services and names no variable.
	r.install("acme", func(s *HelperSpec) { s.AttachEnv = nil }, nil)
	start()
	if got := dotenvReads.Load(); got != reads {
		t.Fatal("the worktree's dotenv files were read for a helper that names no variable")
	}
	if told := r.told("acme"); len(told.Env) != 0 || told.Command != "web" {
		t.Fatalf("a helper that names no variable was told %+v", told)
	}
	// Nor for one whose every variable the project already sets, as a value
	// or as a secret's name: there is nothing a file could add.
	r.install("acme", func(s *HelperSpec) { s.AttachEnv = []string{"OTEL_SERVICE_NAME", "API_TOKEN"} }, nil)
	start()
	if got := dotenvReads.Load(); got != reads {
		t.Fatal("the worktree's dotenv files were read for a helper whose variables the project all sets")
	}
	if told := r.told("acme"); !reflect.DeepEqual(told.Env, map[string]string{"OTEL_SERVICE_NAME": "the-projects-own"}) {
		t.Fatalf("a helper whose variables the project sets was shown %v", told.Env)
	}

	// ---- Two helpers, both of which read the database's address. By
	// plugin id: "aaa-recorder" is asked first, and answers its proxy.
	if _, err := r.h.Remove("acme", true); err != nil {
		t.Fatal(err)
	}
	r.install("aaa-recorder", nil, map[string]string{"attach-answer": `{"env": {"DATABASE_URL": "` + proxied + `", "UNRELATED": "changed"}}`})
	r.install("zzz-editor", nil, nil)
	got := start()
	if got := dotenvReads.Load(); got != reads+1 {
		t.Errorf("the worktree's dotenv files were read %d times for one start with two helpers", got-reads)
	}
	for _, plugin := range []string{"aaa-recorder", "zzz-editor"} {
		told := r.told(plugin)
		if told.Command != "web" || told.Worktree != "fix-orders" {
			t.Errorf("%s was told %+v", plugin, told)
		}
		// The file's own value for what Shipyard does not set, a reference
		// in it filled in from the service's environment; the project's own
		// for what it does; and no secret, in the file or of the project's.
		if !reflect.DeepEqual(told.Env, map[string]string{
			"DATABASE_URL":             inFile,
			"OTEL_SERVICE_NAME":        "the-projects-own",
			"OTEL_RESOURCE_ATTRIBUTES": "team=acme",
		}) {
			t.Errorf("%s was shown %v", plugin, told.Env)
		}
	}
	// The service starts with what the first answered. What a file has and
	// no helper changed is left to the dev server to read there.
	if got["DATABASE_URL"] != proxied {
		t.Errorf("the service's DATABASE_URL = %q, want the helper's", got["DATABASE_URL"])
	}
	for _, name := range []string{"OTEL_RESOURCE_ATTRIBUTES", "OTEL_EXPORTER_OTLP_HEADERS", "UNRELATED"} {
		if v, set := got[name]; set {
			t.Errorf("%s was put in the service's environment from a file: %q", name, v)
		}
	}
	if got["OTEL_SERVICE_NAME"] != "the-projects-own" || got["API_TOKEN"] != "env://ACME_API_TOKEN" || got["BERTH_PORT"] != plain["BERTH_PORT"] {
		t.Errorf("the project's own settings changed: %v", got)
	}
	// berthd notes the service as one that points at the helper, as ever.
	if at := r.h.attachedTo("aaa-recorder"); len(at) != 1 || at[0] != web {
		t.Errorf("attached.json for the helper that changed the service: %+v", at)
	}
	if at := r.h.attachedTo("zzz-editor"); len(at) != 0 {
		t.Errorf("attached.json for the helper that changed nothing: %+v", at)
	}

	// ---- A helper that answers the file's own value back changes nothing.
	r.inData("aaa-recorder", "attach-answer", `{"env": {"DATABASE_URL": "`+inFile+`"}}`)
	if got = start(); got["DATABASE_URL"] != "" {
		t.Errorf("a value answered as the file has it was set: %q", got["DATABASE_URL"])
	}
	if at := r.h.attachedTo("aaa-recorder"); len(at) != 0 {
		t.Errorf("attached.json after a start the helper changed nothing of: %+v", at)
	}

	// ---- The project's own DATABASE_URL wins over the file's.
	r.inData("aaa-recorder", "attach-answer", "")
	const assembled = "postgres://app:hunter2@localhost:5432/as_the_project_set_it"
	if err := r.box.Locations.SetLocalConfig("acme", RepoConfig{Env: map[string]string{"DATABASE_URL": assembled}, Services: []WorktreeService{{Name: "web", Run: "pnpm dev"}}}); err != nil {
		t.Fatal(err)
	}
	got = start()
	if told := r.told("zzz-editor"); told.Env["DATABASE_URL"] != assembled {
		t.Errorf("with a DATABASE_URL of the project's own, the helper was shown %q", told.Env["DATABASE_URL"])
	}
	if got["DATABASE_URL"] != assembled {
		t.Errorf("the service's DATABASE_URL = %q", got["DATABASE_URL"])
	}

	// ---- A file that is a link out of the worktree is not read here either.
	if err := r.box.Locations.SetLocalConfig("acme", RepoConfig{Services: []WorktreeService{{Name: "web", Run: "pnpm dev"}}}); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(r.repo), "outside.env")
	r.dotenv(filepath.Dir(outside), filepath.Base(outside), "DATABASE_URL=postgres://app:hunter2@localhost/elsewhere\n")
	os.Remove(filepath.Join(r.wt.Path, ".env"))
	if err := os.Symlink(outside, filepath.Join(r.wt.Path, ".env")); err != nil {
		t.Fatal(err)
	}
	start()
	if told := r.told("zzz-editor"); len(told.Env) != 0 {
		t.Errorf("a helper was shown what a link out of the worktree holds: %v", told.Env)
	}

	// Nothing berthd logged, and no event, holds a value.
	holdsNone(t, "berthd's log", r.logs.String())
}
