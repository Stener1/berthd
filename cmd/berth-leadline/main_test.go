package main

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/leadline/capture"
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
	o, err := options(nil, envOf("BERTH_LEADLINE_DIR", "/data/leadline"), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if o.Dir != "/data/leadline" || o.Addr != "127.0.0.1:4325" || o.OTLPAddr != "" || o.Keep.MaxAge != 72*time.Hour ||
		o.Keep.MaxBytes != 2<<30 || o.Ports != capture.DefaultPorts || len(o.Routes) != 0 {
		t.Errorf("the defaults = %+v", o)
	}

	o, err = options([]string{"--dir", "/tmp/x", "--addr", "127.0.0.1:0", "--retain", "7d", "--max-size", "500MB",
		"--pg", "15433=5432", "--pg", "127.0.0.1:15434=localhost:5433", "--proxy-ports", "30000-30010", "--otlp", "127.0.0.1:4399"},
		envOf("BERTH_LEADLINE_DIR", "/data/leadline", "BERTH_LEADLINE_RETAIN", "1h", "BERTH_LEADLINE_PG", "1=2"), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if o.Dir != "/tmp/x" || o.Addr != "127.0.0.1:0" || o.OTLPAddr != "127.0.0.1:4399" || o.Keep.MaxAge != 7*24*time.Hour ||
		o.Keep.MaxBytes != 500<<20 || o.Ports != (capture.Ports{First: 30000, Last: 30010}) || len(o.Routes) != 2 ||
		o.Routes[0].Listen != "127.0.0.1:15433" || o.Routes[0].Upstream != "127.0.0.1:5432" || o.Routes[1].Upstream != "localhost:5433" {
		t.Errorf("from flags = %+v", o)
	}

	o, err = options(nil, envOf("BERTH_LEADLINE_ADDR", "127.0.0.1:4400", "BERTH_LEADLINE_OTLP_ADDR", "off", "BERTH_LEADLINE_RETAIN", "0",
		"BERTH_LEADLINE_MAX_SIZE", "off", "BERTH_LEADLINE_PG", "15433=5432", "BERTH_LEADLINE_PROXY_PORTS", "30000-30010"), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if o.Addr != "127.0.0.1:4400" || o.OTLPAddr != "" || o.Keep.MaxAge != 0 || o.Keep.MaxBytes != 0 || len(o.Routes) != 1 || o.Ports.First != 30000 {
		t.Errorf("from the environment = %+v", o)
	}

	for _, bad := range [][]string{
		{"--retain", "soon"}, {"--max-size", "big"}, {"--proxy-ports", "80-90"}, {"--proxy-ports", "30010-30000"}, {"--proxy-ports", "30000"},
		// A proxy listens on this machine only.
		{"--pg", "0.0.0.0:15433=5432"}, {"--pg", "15433"}, {"--nonsense"}, {"extra"},
	} {
		if _, err := options(bad, envOf(), io.Discard); err == nil {
			t.Errorf("serve %s was taken", strings.Join(bad, " "))
		}
	}
}
