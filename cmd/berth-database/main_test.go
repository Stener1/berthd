package main

import (
	"io"
	"strings"
	"testing"
	"time"
)

func envOf(pairs ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return func(key string) string { return m[key] }
}

// Every flag of serve has a variable, and a flag wins over its variable.
func TestOptions(t *testing.T) {
	o, err := options(nil, envOf("BERTH_DATABASE_DIR", "/data/database"), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if o.Dir != "/data/database" || o.Addr != "127.0.0.1:4326" || o.Timeout != 10*time.Second {
		t.Errorf("the defaults = %+v", o)
	}
	o, err = options([]string{"--dir", "/tmp/x", "--addr", "127.0.0.1:0", "--statement-timeout", "2m"},
		envOf("BERTH_DATABASE_DIR", "/data/database", "BERTH_DATABASE_ADDR", "127.0.0.1:4400", "BERTH_DATABASE_STATEMENT_TIMEOUT", "30s"), io.Discard)
	if err != nil || o.Dir != "/tmp/x" || o.Addr != "127.0.0.1:0" || o.Timeout != 2*time.Minute {
		t.Errorf("from flags = %+v, %v", o, err)
	}
	o, err = options(nil, envOf("BERTH_DATABASE_ADDR", "127.0.0.1:4400", "BERTH_DATABASE_STATEMENT_TIMEOUT", "30s"), io.Discard)
	if err != nil || o.Addr != "127.0.0.1:4400" || o.Timeout != 30*time.Second {
		t.Errorf("from the environment = %+v, %v", o, err)
	}
	for _, bad := range [][]string{
		{"--statement-timeout", "soon"}, {"--statement-timeout", "0"}, {"--statement-timeout", "1ms"}, {"--statement-timeout", "48h"},
		{"--nonsense"}, {"extra"},
	} {
		if _, err := options(bad, envOf(), io.Discard); err == nil {
			t.Errorf("serve %s was taken", strings.Join(bad, " "))
		}
	}
}

// The helper is built with Postgres and nothing else, and what the UI is
// told it can do is Postgres's.
func TestTheEnginesThisBuildOpens(t *testing.T) {
	reg := engines()
	if names := strings.Join(reg.Names(), " "); names != "postgres" {
		t.Fatalf("engines = %s", names)
	}
	e, ok := reg.For("postgresql://app@localhost/acme")
	if !ok || e.Capabilities().Dialect != "postgresql" {
		t.Fatal("no engine for a Postgres URL")
	}
	if _, ok := reg.For("mysql://app@localhost/acme"); ok {
		t.Fatal("an engine for a MySQL URL")
	}
}
