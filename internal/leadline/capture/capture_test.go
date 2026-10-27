package capture

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/leadline/pgproxy"
	"github.com/cosscom/shipyard/internal/leadline/store"
)

// The proxies' behaviour over the API, and across a restart, is tested with
// the API (internal/leadline/api); what is here is what only this package
// can see.

func code(err error) string {
	var p *Problem
	if errors.As(err, &p) {
		return p.Code
	}
	return ""
}

// Ports come from the range given, the first free one, and never one
// another program is already listening on.
func TestPortsComeFromTheRange(t *testing.T) {
	// A range of three ports the system says are free now.
	var held []net.Listener
	first := 0
	for try := 0; try < 50 && first == 0; try++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		base := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		if base+2 > 65535 || !portFree(base) || !portFree(base+1) || !portFree(base+2) {
			continue
		}
		first = base
	}
	if first == 0 {
		t.Skip("no three free ports in a row")
	}
	// Another program has the first of them.
	other, err := net.Listen("tcp", listenOn(first))
	if err != nil {
		t.Fatal(err)
	}
	held = append(held, other)
	defer func() {
		for _, ln := range held {
			ln.Close()
		}
	}()

	m, err := Open(Options{Ports: Ports{First: first, Last: first + 2}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a, created, err := m.OpenProxy(Request{Upstream: "127.0.0.1:5432", Label: "acme/fix-orders"})
	if err != nil || !created || a.Port != first+1 {
		t.Fatalf("the first proxy = %+v, %v; want port %d, past the one that is taken", a, err, first+1)
	}
	b, _, err := m.OpenProxy(Request{Upstream: "127.0.0.1:5432", Label: "acme/add-refunds"})
	if err != nil || b.Port != first+2 {
		t.Fatalf("the second proxy = %+v, %v; want port %d", b, err, first+2)
	}
	if _, _, err := m.OpenProxy(Request{Upstream: "127.0.0.1:5432", Label: "acme/one-too-many"}); code(err) != "no_port" {
		t.Errorf("a third proxy in a range with two free ports: %v", err)
	}
	// Closing one frees its port for the next.
	if err := m.CloseProxy(a.ID); err != nil {
		t.Fatal(err)
	}
	if c, _, err := m.OpenProxy(Request{Upstream: "/var/run/postgresql/.s.PGSQL.5432", Label: "acme/one-too-many"}); err != nil || c.Port != first+1 {
		t.Errorf("after one closed: %+v, %v", c, err)
	}
}

// A proxy whose port is taken at the start is shown as failed, not left
// out, and the helper starts all the same. The flag's proxies are the
// helper's own and are not closed over the API.
func TestAProxyThatCannotListenIsShown(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	dir := t.TempDir()
	saved := `{"proxies":[{"id":"p_0000000001","port":` + portOf(taken) + `,"upstream":"127.0.0.1:5432","label":"acme/fix-orders"}]}`
	if err := os.WriteFile(filepath.Join(dir, "proxies.json"), []byte(saved), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	list := m.Proxies(time.Time{})
	if len(list) != 1 || list[0].State != "error" || list[0].Error == "" || list[0].ID != "p_0000000001" {
		t.Fatalf("proxies = %+v, want the saved one, in error", list)
	}
	// The other program lets go; the proxy takes its port back by itself.
	taken.Close()
	m.mu.Lock()
	m.listenLocked(m.proxies[0])
	m.mu.Unlock()
	if list := m.Proxies(time.Time{}); list[0].State != "listening" || list[0].Error != "" {
		t.Errorf("after the port was freed: %+v", list[0])
	}

	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := free.Addr().String()
	free.Close()
	if err := m.Declare([]pgproxy.Route{{Listen: listen, Upstream: "127.0.0.1:5432"}}); err != nil {
		t.Fatal(err)
	}
	list = m.Proxies(time.Time{})
	if len(list) != 2 || list[1].Listen != listen || list[1].Origins[0] != "flag" || list[1].Owner.Kind != "flag" {
		t.Fatalf("with the flag's proxy: %+v", list)
	}
	if err := m.CloseProxy(list[1].ID); code(err) != "bad_request" {
		t.Errorf("closing the flag's proxy: %v", err)
	}
	if err := m.Declare([]pgproxy.Route{{Listen: "127.0.0.1:0", Upstream: "db.acme.example:5432"}}); code(err) != "capture_remote" {
		t.Errorf("a flag for a database that is not on this machine: %v", err)
	}
	// A file that is not the helper's is said, not overwritten.
	if err := os.WriteFile(filepath.Join(dir, "proxies.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if again, err := Open(Options{Dir: dir}); err == nil {
		again.Close()
		t.Error("a proxies.json that is not JSON was taken")
	}
}

func portOf(ln net.Listener) string {
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

func TestUnderAndOwners(t *testing.T) {
	for _, c := range []struct {
		service, source string
		want            bool
	}{
		{"acme/fix-orders", "acme/fix-orders", true},
		{"acme/fix-orders:worker", "acme/fix-orders", true},
		{"acme/fix-orders-2", "acme/fix-orders", false},
		{"acme/fix", "acme/fix-orders", false},
		{"acme", "", false},
		{"", "", false},
	} {
		if got := Under(c.service, c.source); got != c.want {
			t.Errorf("Under(%q, %q) = %v", c.service, c.source, got)
		}
	}
	for want, w := range map[string]store.Worktree{
		"from Shipyard, worktree fix-orders": {Project: "acme", Worktree: "fix-orders"},
		"from Shipyard, project acme":        {Project: "acme"},
		"from Shipyard, acme/fix-orders":     {},
	} {
		if o := OwnerOf("acme/fix-orders", w); o.Label != want || o.Kind != "worktree" || o.Service != "acme/fix-orders" {
			t.Errorf("OwnerOf(%+v) = %+v, want the label %q", w, o, want)
		}
	}
}

// The windowed count: what was recorded since a time, to the minute, for a
// day back.
func TestTally(t *testing.T) {
	var tl tally
	now := time.Unix(1_800_000_000, 0)
	tl.add(now.Add(-20*time.Minute), 5)
	tl.add(now.Add(-10*time.Minute), 3)
	tl.add(now, 2)
	tl.add(now, 0)
	if got := tl.since(now, time.Time{}); got != 5 {
		t.Errorf("the last 15 minutes hold %d, want 5", got)
	}
	if got := tl.since(now, now.Add(-time.Hour)); got != 10 {
		t.Errorf("the last hour holds %d, want 10", got)
	}
	// A day on, yesterday's minutes have been cleared for today's.
	later := now.Add(25 * time.Hour)
	tl.add(later, 1)
	if got := tl.since(later, later.Add(-48*time.Hour)); got != 1 {
		t.Errorf("a day later the count is %d, want 1", got)
	}
}
