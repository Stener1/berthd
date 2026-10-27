package leadline

import (
	"reflect"
	"strings"
	"testing"
)

// A worktree is filed under "<project>/<worktree>", the project alone for
// its main checkout, and ":<name>" after it for a second command.
func TestServiceNames(t *testing.T) {
	for _, tc := range []struct {
		w    Worktree
		want string
	}{
		{Worktree{Project: "acme", Name: "fix-orders"}, "acme/fix-orders"},
		// The main checkout is a worktree to Shipyard, named for its folder.
		{Worktree{Project: "acme"}, "acme"},
		{Worktree{Project: "acme", Name: "acme"}, "acme"},
		{Worktree{Name: "fix-orders"}, "fix-orders"},
		// Names that would break the joins.
		{Worktree{Project: "acme shop", Name: "feat/fix:orders"}, "acme-shop/feat-fix-orders"},
		{Worktree{Project: " acme ", Name: `a\b`}, "acme/a-b"},
		{Worktree{Project: "acme", Name: strings.Repeat("x", 80)}, "acme/" + strings.Repeat("x", 60)},
	} {
		if got := tc.w.Service(); got != tc.want {
			t.Errorf("%+v is filed under %q, want %q", tc.w, got, tc.want)
		}
	}
	w := Worktree{Project: "acme", Name: "fix-orders"}
	for name, want := range map[string]string{"": "acme/fix-orders", "worker": "acme/fix-orders:worker", " acme/fix-orders ": "acme/fix-orders", "a b": "acme/fix-orders:a-b", "x:y/z": "acme/fix-orders:x-y-z"} {
		if got := w.ServiceFor(name); got != want {
			t.Errorf("ServiceFor(%q) = %q, want %q", name, got, want)
		}
	}
	if got := (Worktree{Project: "acme"}).ServiceFor("worker"); got != "acme:worker" {
		t.Errorf("a command in the main checkout is filed under %q", got)
	}
}

func TestEnv(t *testing.T) {
	w := Worktree{Project: "acme shop", Name: "fix-orders", Branch: "feat/x", Box: "devl"}
	got := Env(w, "", Helper{OTLP: "http://127.0.0.1:4325/"}, "")
	want := []Var{
		{"OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4325"},
		{"OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf"},
		{"OTEL_SERVICE_NAME", "acme-shop/fix-orders"},
		{"OTEL_RESOURCE_ATTRIBUTES", "shipyard.project=acme%20shop,shipyard.worktree=fix-orders,shipyard.branch=feat%2Fx,shipyard.box=devl"},
		{"OTEL_BSP_SCHEDULE_DELAY", "500"},
		{"OTEL_BLRP_SCHEDULE_DELAY", "500"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Env =\n %v\nwant\n %v", got, want)
	}
	// A second command, and attributes the project already sets: what is
	// there stays, and no key is set twice.
	got = Env(w, "worker", Helper{OTLP: "http://127.0.0.1:4325"}, "deployment.environment=dev,shipyard.box=mine,")
	if got[2].Value != "acme-shop/fix-orders:worker" {
		t.Errorf("the worker's service name = %q", got[2].Value)
	}
	if got[3].Value != "deployment.environment=dev,shipyard.box=mine,shipyard.project=acme%20shop,shipyard.worktree=fix-orders,shipyard.branch=feat%2Fx" {
		t.Errorf("resource attributes = %q", got[3].Value)
	}
	// The main checkout says its project and no worktree; with nothing to
	// say there is no variable at all.
	got = Env(Worktree{Project: "acme"}, "", Helper{OTLP: "http://127.0.0.1:4325"}, "")
	if got[2].Value != "acme" || got[3].Value != "shipyard.project=acme" {
		t.Errorf("the main checkout: %v", got)
	}
	for _, v := range Env(Worktree{}, "", Helper{OTLP: "http://127.0.0.1:4325"}, "") {
		if v.Name == "OTEL_RESOURCE_ATTRIBUTES" {
			t.Errorf("a worktree with nothing to say set %v", v)
		}
	}
}

func TestLocalPostgres(t *testing.T) {
	local := map[string]string{
		"postgres://app:hunter2@localhost:5432/acme":           "localhost:5432",
		"postgresql://app@127.0.0.1/acme":                      "127.0.0.1:5432",
		"postgres://app:hunter2@[::1]:5433/acme":               "[::1]:5433",
		"postgresql+psycopg://app:hunter2@localhost:5432/acme": "localhost:5432",
		"jdbc:postgresql://localhost:5432/acme?user=app":       "localhost:5432",
		"postgres://app@localhost:5432/acme?sslmode=prefer":    "localhost:5432",
		"postgres://app@0.0.0.0:5432/acme":                     "127.0.0.1:5432",
		"postgres://app@db.localhost:5432/acme":                "db.localhost:5432",
		"postgres://app:p@ss@localhost:5432/acme":              "localhost:5432",
	}
	for in, want := range local {
		if got, ok := LocalPostgres(in); !ok || got != want {
			t.Errorf("LocalPostgres(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{
		"postgres://app:hunter2@db.acme.example:5432/acme",
		"postgres://app:hunter2@10.0.0.5:5432/acme",
		"postgres://app:hunter2@localhost.acme.example:5432/acme",
		"postgres://localhost:hunter2@db.acme.example/acme",
		"postgres://app:@localhost@db.acme.example:5432/acme",
		"postgres:///acme?host=/var/run/postgresql",
		"postgres://localhost/acme?host=db.acme.example",
		"postgres://localhost:5432,localhost:5433/acme",
		"postgres://app@localhost:5432/acme?sslmode=require",
		"postgres://app@localhost:5432/acme?sslmode=verify-full",
		"postgres://app@localhost:5432/acme?ssl=true",
		"mysql://app:hunter2@localhost:3306/acme",
		"redis://localhost:6379",
		"http://localhost:5432",
		"localhost:5432",
		"/usr/bin:/bin",
		"",
	} {
		if got, ok := LocalPostgres(in); ok {
			t.Errorf("%q was taken for a local Postgres at %q", in, got)
		}
		if got, ok := ThroughProxy(in, "127.0.0.1:61000"); ok || got != in {
			t.Errorf("%q was rewritten to %q", in, got)
		}
	}
}

func TestThroughProxyKeepsEverythingButTheAddress(t *testing.T) {
	for in, want := range map[string]string{
		"postgres://app:hunter2@localhost:5432/acme":                               "postgres://app:hunter2@127.0.0.1:61000/acme",
		"postgresql://app@127.0.0.1/acme":                                          "postgresql://app@127.0.0.1:61000/acme",
		"postgres://app:hunter2@[::1]:5433/acme?application_name=x&sslmode=prefer": "postgres://app:hunter2@127.0.0.1:61000/acme?application_name=x&sslmode=prefer",
		// A password is carried byte for byte, odd escapes and all.
		"postgres://app:p%40ss%2fw%C3%B6rd+x@localhost:5432/acme": "postgres://app:p%40ss%2fw%C3%B6rd+x@127.0.0.1:61000/acme",
		"postgres://app:p@ss@localhost:5432/acme":                 "postgres://app:p@ss@127.0.0.1:61000/acme",
		"postgres://localhost":                                    "postgres://127.0.0.1:61000",
		"jdbc:postgresql://localhost:5432/acme?user=app":          "jdbc:postgresql://127.0.0.1:61000/acme?user=app",
		"postgresql+asyncpg://app:x@localhost/acme":               "postgresql+asyncpg://app:x@127.0.0.1:61000/acme",
	} {
		if got, ok := ThroughProxy(in, "127.0.0.1:61000"); !ok || got != want {
			t.Errorf("%s:\n got %s (%v)\nwant %s", in, got, ok, want)
		}
	}
}
