package box

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// installNamed installs the fake helper for a plugin, with files put in its
// data folder before it starts, and waits until it runs.
func installNamed(t *testing.T, h *Helpers, plugin string, files map[string]string) {
	t.Helper()
	data := h.dataDir(plugin)
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(data, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, program := fakeHelperBuild(t)
	if _, err := h.Install(context.Background(), plugin, fakeSpec(program), bytes.NewReader(program)); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the "+plugin+" helper to run", 20*time.Second, func() bool { return h.Status(plugin).State == HelperRunning })
}

func readIn(t *testing.T, h *Helpers, plugin, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.dataDir(plugin), name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// A helper that writes a token in its ready file is sent it with everything
// berthd asks of it: what it relays for the app, and its own attach. What
// the app sent as Authorization never reaches a helper, with a token or
// without.
func TestAHelpersTokenGoesWithEveryRequestAndNothingElseDoes(t *testing.T) {
	ctx := context.Background()
	h, logs := testHelpers(t)
	b, _, _ := serviceBox(t, h, map[string]string{"DATABASE_URL": "postgres://app:hunter2@localhost:5432/acme"})
	c, _ := servedBox(t, func(sb *Box) { sb.Helpers = h })
	const token = "tok3n_For-berthd.only~+/="
	installNamed(t, h, "guarded", map[string]string{"token": token})
	installNamed(t, h, "open", nil)

	seen := func(plugin string, header http.Header) string {
		t.Helper()
		status, body := helperCall(t, c, "GET", "/v1/helpers/"+plugin+"/api/api/echo/x", nil, header)
		var echo map[string]string
		if status != 200 || json.Unmarshal([]byte(body), &echo) != nil {
			t.Fatalf("relay to %s: %d %s", plugin, status, body)
		}
		return echo["authorization"]
	}
	for name, header := range map[string]http.Header{
		"nothing":            nil,
		"a token of its own": {"Authorization": {"Bearer from-the-app"}},
		"two of them":        {"Authorization": {"Bearer one", "Basic dHdv"}},
	} {
		if got := seen("guarded", header); got != "Bearer "+token {
			t.Errorf("the app sent %s: the guarded helper saw %q", name, got)
		}
		if got := seen("open", header); got != "" {
			t.Errorf("the app sent %s: the helper with no token saw %q", name, got)
		}
	}

	// berthd's own call, before a service starts.
	if _, err := b.StartService(ctx, "cal", "billing", "web"); err != nil {
		t.Fatal(err)
	}
	if got := readIn(t, h, "guarded", "attach-authorization"); got != "Bearer "+token {
		t.Errorf("attach reached the guarded helper with %q", got)
	}
	if got := readIn(t, h, "open", "attach-authorization"); got != "" {
		t.Errorf("attach reached the helper with no token with %q", got)
	}
	if strings.Contains(logs.String(), token) {
		t.Errorf("berthd logged a helper's token:\n%s", logs)
	}

	// Stopped, its token is forgotten: whatever takes its port next is
	// not sent it.
	api, _ := h.API("guarded")
	if _, held := helperTokens.Load(api.Host); !held {
		t.Fatal("a running helper's token is not held")
	}
	if _, err := h.Remove("guarded", true); err != nil {
		t.Fatal(err)
	}
	if _, held := helperTokens.Load(api.Host); held {
		t.Fatal("a removed helper's token is still held")
	}
}

// A token that could not go in a header is never sent, and never shown.
func TestAHelperWhoseTokenCannotBeSentIsNotRun(t *testing.T) {
	h, logs := testHelpers(t)
	h.readyTimeout = time.Second
	const token = "not a token\r\nX-Injected: 1"
	data := h.dataDir("acme")
	os.MkdirAll(data, 0o700)
	os.WriteFile(filepath.Join(data, "token"), []byte(token), 0o600)
	_, program := fakeHelperBuild(t)
	if _, err := h.Install(context.Background(), "acme", fakeSpec(program), bytes.NewReader(program)); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the helper to be given up on", 20*time.Second, func() bool { return h.Status("acme").State == HelperFailed })
	st := h.Status("acme")
	if !strings.Contains(st.Error, "token that cannot be sent") || strings.Contains(st.Error, "X-Injected") || strings.Contains(logs.String(), "X-Injected") {
		t.Fatalf("status = %+v\n%s", st, logs)
	}
	if _, ok := h.API("acme"); ok {
		t.Fatal("it is relayed to")
	}
}

// Two helpers that both read a service's database URL are each shown the
// project's own: the second is not handed what the first rewrote. With a
// recording proxy and a database editor both on, the editor would otherwise
// connect through the recorder, and its statements be taken for the app's.
func TestEachHelperIsShownTheServicesOwnValues(t *testing.T) {
	ctx := context.Background()
	h, _ := testHelpers(t)
	const own = "postgres://app:hunter2@localhost:5432/acme"
	const proxied = "postgres://app:hunter2@127.0.0.1:25432/acme"
	b, _, units := serviceBox(t, h, map[string]string{"DATABASE_URL": own, "OTEL_SERVICE_NAME": "the-projects-own"})
	// By plugin id: "aaa-recorder" is asked first.
	installNamed(t, h, "aaa-recorder", map[string]string{
		"attach-answer": `{"env": {"DATABASE_URL": "` + proxied + `", "OTEL_SERVICE_NAME": "cal/billing:web"}}`})
	installNamed(t, h, "zzz-editor", nil)

	if _, err := b.StartService(ctx, "cal", "billing", "web"); err != nil {
		t.Fatal(err)
	}
	for _, plugin := range []string{"aaa-recorder", "zzz-editor"} {
		var asked HelperAttach
		if err := json.Unmarshal([]byte(readIn(t, h, plugin, "attach-request")), &asked); err != nil {
			t.Fatal(err)
		}
		if asked.Env["DATABASE_URL"] != own || asked.Env["OTEL_SERVICE_NAME"] != "the-projects-own" {
			t.Errorf("%s was shown DATABASE_URL %q and OTEL_SERVICE_NAME %q, not the project's own",
				plugin, asked.Env["DATABASE_URL"], asked.Env["OTEL_SERVICE_NAME"])
		}
	}
	// The service still gets what the first answered: the second, which
	// answered nothing, took nothing away.
	env := units.env(serviceUnit("cal", "billing", "web"))
	if env["DATABASE_URL"] != proxied || env["OTEL_SERVICE_NAME"] != "cal/billing:web" {
		t.Errorf("the service's environment: DATABASE_URL %q, OTEL_SERVICE_NAME %q", env["DATABASE_URL"], env["OTEL_SERVICE_NAME"])
	}
}
