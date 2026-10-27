package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// attachTo asks the helper to attach a command, as the box daemon does.
func attachTo(t *testing.T, r *rig, body map[string]any) (Attached, string) {
	t.Helper()
	code, raw := send(t, "POST", r.URL+"/api/attach", "", body)
	if code != 200 {
		t.Fatalf("POST /api/attach %v: %d %s", body, code, raw)
	}
	var out Attached
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out, raw
}

func TestAttach(t *testing.T) {
	r := newRig(t)
	about := func(command string, env map[string]string) map[string]any {
		return map[string]any{"box": "devbox", "project": "acme", "worktree": "fix-orders", "branch": "fix/orders-n-plus-one",
			"path": "/home/dev/acme-fix-orders", "command": command, "env": env}
	}

	// A dev server with a Postgres on this machine and no telemetry of its
	// own: it is told where to send, and its URL goes through a proxy.
	const databaseURL = "postgres://app:hunter2@localhost:5432/acme?sslmode=prefer"
	got, _ := attachTo(t, r, about("web", map[string]string{
		"DATABASE_URL":             databaseURL,
		"OTEL_RESOURCE_ATTRIBUTES": "deployment.environment=dev",
	}))
	proxies := r.proxies.Proxies(time.Time{})
	if len(proxies) != 1 || proxies[0].Label != "acme/fix-orders" || proxies[0].Upstream != "localhost:5432" ||
		proxies[0].Owner.Worktree != "fix-orders" || proxies[0].Owner.Box != "devbox" {
		t.Fatalf("proxies after attach = %+v", proxies)
	}
	for name, want := range map[string]string{
		"DATABASE_URL":                "postgres://app:hunter2@" + proxies[0].Listen + "/acme?sslmode=prefer",
		"OTEL_EXPORTER_OTLP_ENDPOINT": r.URL,
		"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
		"OTEL_SERVICE_NAME":           "acme/fix-orders:web",
		"OTEL_RESOURCE_ATTRIBUTES":    "deployment.environment=dev,shipyard.project=acme,shipyard.worktree=fix-orders,shipyard.branch=fix%2Forders-n-plus-one,shipyard.box=devbox",
		"OTEL_BSP_SCHEDULE_DELAY":     "500",
		"OTEL_BLRP_SCHEDULE_DELAY":    "500",
	} {
		if got.Env[name] != want {
			t.Errorf("%s = %q, want %q", name, got.Env[name], want)
		}
	}
	if len(got.Env) != 7 || got.Service != "acme/fix-orders:web" || got.Source != "acme/fix-orders" || len(got.Proxies) != 1 || got.Proxies[0] != proxies[0].ID {
		t.Errorf("attached = %+v", got)
	}

	// Attaching again (the service restarts) gives the same proxy, and a
	// second command of the worktree shares it.
	again, _ := attachTo(t, r, about("worker", map[string]string{"DATABASE_URL": databaseURL, "POSTGRES_URL": "postgresql://app@127.0.0.1/acme"}))
	if again.Env["DATABASE_URL"] != got.Env["DATABASE_URL"] || again.Env["POSTGRES_URL"] != "postgresql://app@"+proxies[0].Listen+"/acme" ||
		again.Service != "acme/fix-orders:worker" || len(r.proxies.Proxies(time.Time{})) != 1 {
		t.Errorf("attached again = %+v, with %d proxies", again, len(r.proxies.Proxies(time.Time{})))
	}
	// A URL that already goes through the proxy gets no proxy of its own.
	through, _ := attachTo(t, r, about("web", map[string]string{"DATABASE_URL": got.Env["DATABASE_URL"]}))
	if _, set := through.Env["DATABASE_URL"]; set || len(r.proxies.Proxies(time.Time{})) != 1 {
		t.Errorf("a URL through the proxy was proxied again: %+v", through)
	}

	// A project that sends its telemetry somewhere itself keeps doing so:
	// its endpoint is never replaced, and nothing else of its
	// OpenTelemetry is touched. Its database is still recorded.
	for _, name := range ownEndpoints {
		own, _ := attachTo(t, r, about("web", map[string]string{
			name: "http://collector.acme.example:4318", "OTEL_SERVICE_NAME": "acme-api", "DATABASE_URL": databaseURL,
		}))
		for k := range own.Env {
			if strings.HasPrefix(k, "OTEL_") {
				t.Errorf("with its own %s the answer sets %s", name, k)
			}
		}
		if own.Env["DATABASE_URL"] != got.Env["DATABASE_URL"] {
			t.Errorf("with its own %s the database URL = %q", name, own.Env["DATABASE_URL"])
		}
	}
	// A name the project chose for itself stands, with the rest set.
	named, _ := attachTo(t, r, about("web", map[string]string{"OTEL_SERVICE_NAME": "acme-api", "OTEL_BSP_SCHEDULE_DELAY": "2000"}))
	if _, set := named.Env["OTEL_SERVICE_NAME"]; set || named.Service != "acme-api" || named.Env["OTEL_EXPORTER_OTLP_ENDPOINT"] != r.URL ||
		named.Env["OTEL_BSP_SCHEDULE_DELAY"] != "" || named.Env["OTEL_BLRP_SCHEDULE_DELAY"] != "500" {
		t.Errorf("with its own service name: %+v", named)
	}

	// What cannot go through the proxy is left alone, with no proxy opened:
	// a remote database, one that insists on TLS, and what is no URL.
	left, _ := attachTo(t, r, about("web", map[string]string{
		"DATABASE_URL": "postgres://app:hunter2@db.acme.example/acme",
		"POSTGRES_URL": "postgres://app:hunter2@localhost/acme?sslmode=require",
		"REDIS_URL":    "redis://localhost:6379",
	}))
	if len(left.Proxies) != 0 || left.Env["DATABASE_URL"] != "" || left.Env["POSTGRES_URL"] != "" || left.Env["REDIS_URL"] != "" || len(r.proxies.Proxies(time.Time{})) != 1 {
		t.Errorf("URLs that cannot be proxied: %+v", left)
	}

	// The project's main checkout is filed under the project alone.
	main, _ := attachTo(t, r, map[string]any{"project": "acme", "command": ""})
	if main.Service != "acme" || main.Source != "acme" || main.Env["OTEL_SERVICE_NAME"] != "acme" {
		t.Errorf("the main checkout: %+v", main)
	}

	// No worktree at all is a mistake, and a page that is not Shipyard's
	// may not attach anything.
	if code, raw := send(t, "POST", r.URL+"/api/attach", "", map[string]any{"env": map[string]string{}}); code != 400 || !strings.Contains(raw, `"bad_request"`) {
		t.Errorf("attach with no project: %d %s", code, raw)
	}
	if code, _ := send(t, "POST", r.URL+"/api/attach", "http://localhost:3000", about("web", nil)); code != 403 {
		t.Errorf("attach from an app's own page: %d, want 403", code)
	}
}
