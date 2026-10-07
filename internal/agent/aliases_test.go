package agent

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"

	"github.com/sean-brydon/berthd/internal/box"
	"github.com/sean-brydon/berthd/internal/wire"
)

// The box calls itself dev-sean; this laptop paired it as devbox. A URL the
// box wrote ($BERTH_URL) names dev-sean, and still reaches the worktree.
func TestTheProxyAcceptsTheBoxsOwnName(t *testing.T) {
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "billing saw "+r.Host)
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	b := newBoxWith(t, func(s *wire.Server) {
		s.Handle("GET /v1/info", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(box.Info{Name: "Dev-Sean"})
		}))
	})
	b.services = []box.Service{{Location: "cal", Worktree: "billing", Port: port}}
	a := startAgent(t, b.pairLaptop())
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })

	fetch := func(host string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+a.proxy+"/", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	want := "billing saw localhost:" + strconv.Itoa(port)
	eventually(t, "the box's own name reaches it", func() bool {
		_, body := fetch("billing.cal.dev-sean.localhost:1377")
		return body == want
	})
	if _, body := fetch("billing.cal.devbox.localhost:1377"); body != want {
		t.Errorf("the paired name: %q", body)
	}
	if code, _ := fetch("billing.cal.someone-else.localhost:1377"); code != http.StatusNotFound {
		t.Errorf("an unknown box name got %d", code)
	}
}

// Two cloud VMs called devbox in different zones both name themselves
// devbox, the first label of their hostnames. A laptop that paired them as
// devbox-a and devbox-b cannot tell which one devbox means, so it leads to
// neither; once one is paired as devbox, that name is its own.
func TestABoxsOwnNameClaimedTwiceIsNoAlias(t *testing.T) {
	a := &Agent{clients: map[string]*boxState{"devbox-a": {}, "devbox-b": {}}}
	a.selfNames.set("devbox-a", "devbox")
	if paired, ok := a.boxAlias("devbox"); !ok || paired != "devbox-a" {
		t.Fatalf("one box: %q %v", paired, ok)
	}
	a.selfNames.set("devbox-b", "devbox")
	if paired, ok := a.boxAlias("devbox"); ok {
		t.Fatalf("two boxes claim devbox, and it led to %q", paired)
	}
	a.clients["devbox"] = &boxState{}
	if paired, ok := a.boxAlias("devbox"); ok {
		t.Fatalf("a paired box's name is no alias, and it led to %q", paired)
	}
}
