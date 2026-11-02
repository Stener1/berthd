package box

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/events"
)

const phoneAddr = "100.101.102.103:1379"

func phoneBox(t *testing.T) (*Box, *Phone, http.Handler) {
	t.Helper()
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(t.TempDir(), "locations.json")), Events: &events.Bus{}, Sessions: testSessions(t), Turns: &Turns{}}
	p := &Phone{Path: filepath.Join(t.TempDir(), "phone.json")}
	if err := p.save(PhoneConfig{Enabled: true, Token: "right-token"}); err != nil {
		t.Fatal(err)
	}
	return b, p, p.Handler(b, phoneAddr)
}

func phoneReq(t *testing.T, h http.Handler, method, path, host, token, body string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
	r.Host = host
	r.RemoteAddr = "100.90.1.2:51000"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestThePhoneAppNeedsTheTokenAndATailnetHost(t *testing.T) {
	_, _, h := phoneBox(t)
	if w := phoneReq(t, h, "GET", "/phone/v1/sessions", "evil.example:1379", "right-token", ""); w.Code != 403 {
		t.Fatalf("a rebound host got %d", w.Code)
	}
	if w := phoneReq(t, h, "GET", "/phone/v1/sessions", phoneAddr, "", ""); w.Code != 401 {
		t.Fatalf("no token got %d", w.Code)
	}
	if w := phoneReq(t, h, "GET", "/phone/v1/sessions", phoneAddr, "right-token", ""); w.Code != 200 {
		t.Fatalf("the right token got %d: %s", w.Code, w.Body)
	}
	if w := phoneReq(t, h, "GET", "/phone/v1/sessions", "devbox.tail1234.ts.net:1379", "right-token", ""); w.Code != 200 {
		t.Fatalf("a MagicDNS name got %d", w.Code)
	}
	// The app itself is not secret, and asks for the token.
	w := phoneReq(t, h, "GET", "/", phoneAddr, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "app.js") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("index: %d %s", w.Code, w.Header())
	}
}

func TestThePhoneReachesOnlyItsOwnSmallAPI(t *testing.T) {
	_, _, h := phoneBox(t)
	for _, path := range []string{"/v1/sessions", "/v1/hooks", "/v1/stop", "/v1/upgrade", "/v1/locations", "/phone/v1/hooks", "/phone/v1/exec", "/v1/phone"} {
		for _, method := range []string{"GET", "POST", "PUT"} {
			w := phoneReq(t, h, method, path, phoneAddr, "right-token", "{}")
			if w.Code < 400 {
				t.Errorf("%s %s answered %d", method, path, w.Code)
			}
		}
	}
}

func TestWrongTokensLockAPhoneOutForAMinute(t *testing.T) {
	_, _, h := phoneBox(t)
	for range maxPhoneFailures {
		phoneReq(t, h, "GET", "/phone/v1/info", phoneAddr, "guess", "")
	}
	if w := phoneReq(t, h, "GET", "/phone/v1/info", phoneAddr, "right-token", ""); w.Code != 429 {
		t.Fatalf("after %d wrong tokens the right one got %d", maxPhoneFailures, w.Code)
	}
}

func TestOnlyOtherTailnetPagesMayReadTheBox(t *testing.T) {
	_, _, h := phoneBox(t)
	w := phoneReq(t, h, "OPTIONS", "/phone/v1/sessions", phoneAddr, "", "", "Origin", "http://100.70.1.2:1379")
	if w.Header().Get("Access-Control-Allow-Origin") != "http://100.70.1.2:1379" {
		t.Fatalf("another box's page was not allowed: %v", w.Header())
	}
	for _, o := range []string{"https://evil.example", "http://192.168.1.5:1379", "null"} {
		w := phoneReq(t, h, "GET", "/phone/v1/sessions", phoneAddr, "right-token", "", "Origin", o)
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s was allowed", o)
		}
	}
}

func TestAPhoneCanAnswerAnAgentButOnlyWithSafeKeys(t *testing.T) {
	b, _, h := phoneBox(t)
	if _, err := b.Sessions.Create(context.Background(), "agent", "", t.TempDir(), "cat", nil); err != nil {
		t.Fatal(err)
	}
	if w := phoneReq(t, h, "POST", "/phone/v1/sessions/agent/send", phoneAddr, "right-token", `{"text":"from-the-phone"}`); w.Code != 200 {
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	if w := phoneReq(t, h, "POST", "/phone/v1/sessions/agent/keys", phoneAddr, "right-token", `{"key":"2"}`); w.Code != 200 {
		t.Fatalf("key 2: %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{"C-x", "rm -rf", "Enter Enter", ""} {
		body, _ := json.Marshal(map[string]string{"key": bad})
		if w := phoneReq(t, h, "POST", "/phone/v1/sessions/agent/keys", phoneAddr, "right-token", string(body)); w.Code != 400 {
			t.Errorf("key %q answered %d", bad, w.Code)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := phoneReq(t, h, "GET", "/phone/v1/sessions/agent/screen", phoneAddr, "right-token", "")
		if strings.Contains(w.Body.String(), "from-the-phone") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the prompt never arrived: %s", w.Body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestALaptopTurnsPhoneAccessOnRotatesAndOff(t *testing.T) {
	dir := t.TempDir()
	c, _ := servedBox(t, func(b *Box) {
		b.Phone = &Phone{Path: filepath.Join(dir, "phone.json"), Addr: func() (string, error) { return "127.0.0.1", nil }, Port: freeTCPPort(t)}
	})
	var st PhoneStatus
	if status := call(t, c, "PUT", "/v1/phone", "", map[string]any{"enabled": true}, &st); status != 200 || st.Token == "" || !strings.HasPrefix(st.URL, "http://127.0.0.1:") {
		t.Fatalf("enable: %d %+v", status, st)
	}
	first := st.Token
	resp, err := http.Get(st.URL + "manifest.webmanifest")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("the phone app is not being served: %v", err)
	}
	resp.Body.Close()
	call(t, c, "PUT", "/v1/phone", "", map[string]any{"rotate": true}, &st)
	if st.Token == first || st.Token == "" {
		t.Fatal("rotating kept the token")
	}
	url := st.URL
	st = PhoneStatus{}
	call(t, c, "PUT", "/v1/phone", "", map[string]any{"enabled": false}, &st)
	if st.URL != "" {
		t.Fatalf("still listening after turning it off: %+v", st)
	}
	if resp, err := http.Get(url + "manifest.webmanifest"); err == nil {
		resp.Body.Close()
		t.Fatal("the phone app still answers after turning it off")
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestAnAgentThatNeedsYouIsPushedToNtfy(t *testing.T) {
	got := make(chan *http.Request, 1)
	bodies := make(chan string, 1)
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- r
		bodies <- string(b)
	}))
	defer ntfy.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, p, _ := phoneBox(t)
	// The test's ntfy is on loopback, which the box's owner must allow.
	b.Flows = &Flows{AllowOutbound: []string{"127.0.0.1"}}
	p.save(PhoneConfig{Enabled: false, Token: "t", Notify: &PhoneNotify{URL: ntfy.URL + "/berth-topic"}})
	repo := gitRepo(t)
	b.Locations.Add(ctx, "cal", repo)
	wt, _ := b.Locations.CreateWorktree(ctx, "cal", "billing", "", "")
	go p.Run(ctx, b)
	time.Sleep(100 * time.Millisecond)
	b.Events.Publish(events.Event{Type: "agent.finished", Data: map[string]any{"path": wt.Path, "agent": "claude"}})
	b.Events.Publish(events.Event{Type: "agent.waiting", Data: map[string]any{"path": wt.Path, "agent": "claude"}})
	select {
	case r := <-got:
		body := <-bodies
		if r.URL.Path != "/berth-topic" || r.Header.Get("Title") != "Claude Code needs you" || body != "cal/billing on devbox" || r.Header.Get("Priority") != "high" {
			t.Fatalf("pushed %s %v %q", r.URL.Path, r.Header, body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was pushed")
	}
	select {
	case r := <-got:
		t.Fatalf("finished was pushed too, without asking: %v", r.Header)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestThePhoneReadsASessionsChatAndNothingMore(t *testing.T) {
	b, _, h := phoneBox(t)
	if _, err := b.Sessions.Create(context.Background(), "agent", "", t.TempDir(), "cat", nil); err != nil {
		t.Fatal(err)
	}
	var info struct {
		Capabilities []string `json:"capabilities"`
	}
	w := phoneReq(t, h, "GET", "/phone/v1/info", phoneAddr, "right-token", "")
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil || strings.Join(info.Capabilities, ",") != "transcript,answer" {
		t.Fatalf("info: %s", w.Body)
	}
	path := "/phone/v1/sessions/agent/transcript?since=3&gen=g1"
	if w := phoneReq(t, h, "GET", path, phoneAddr, "", ""); w.Code != 401 {
		t.Fatalf("no token got %d", w.Code)
	}
	if w := phoneReq(t, h, "GET", "/phone/v1/sessions/nobody/transcript", phoneAddr, "right-token", ""); w.Code != 404 {
		t.Fatalf("an unknown session got %d: %s", w.Code, w.Body)
	}
	// A terminal-only session has no chat: the app falls back to its screen.
	w = phoneReq(t, h, "GET", path, phoneAddr, "right-token", "")
	var res struct {
		Source string           `json:"source"`
		Items  []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || w.Code != 200 || res.Source != "none" || res.Items == nil {
		t.Fatalf("transcript: %d %s", w.Code, w.Body)
	}
	// Tool output stays on the box: the detail route is not the phone's.
	if w := phoneReq(t, h, "GET", "/phone/v1/sessions/agent/transcript/tool/x", phoneAddr, "right-token", ""); w.Code != 404 {
		t.Fatalf("tool detail answered %d", w.Code)
	}
}

func TestAChoiceFromThePhoneIsTypedAsTheDesktopDoes(t *testing.T) {
	b, _, h := phoneBox(t)
	if _, err := b.Sessions.Create(context.Background(), "agent", "", t.TempDir(), "cat", nil); err != nil {
		t.Fatal(err)
	}
	// A numbered option is its key alone; a plain answer is the word and Enter.
	for _, body := range []string{`{"text":"2","enter":false}`, `{"text":"yes"}`} {
		if w := phoneReq(t, h, "POST", "/phone/v1/sessions/agent/send", phoneAddr, "right-token", body); w.Code != 200 {
			t.Fatalf("send %s: %d %s", body, w.Code, w.Body)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := phoneReq(t, h, "GET", "/phone/v1/sessions/agent/screen", phoneAddr, "right-token", "")
		if strings.Contains(w.Body.String(), "2yes") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the choices never arrived: %s", w.Body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAFormOfQuestionsIsAnsweredFromThePhoneThroughTheSameChecks(t *testing.T) {
	b, _, h := phoneBox(t)
	if _, err := b.Sessions.Create(context.Background(), "agent", "", t.TempDir(), "cat", nil); err != nil {
		t.Fatal(err)
	}
	answer := func(path, token, body string) int {
		return phoneReq(t, h, "POST", path, phoneAddr, token, body).Code
	}
	body := `{"tool":"toolu_1","answers":[{"picks":["Yes"]}]}`
	if c := answer("/phone/v1/sessions/agent/answer", "", body); c != 401 {
		t.Fatalf("no token got %d", c)
	}
	if c := answer("/phone/v1/sessions/nobody/answer", "right-token", body); c != 404 {
		t.Fatalf("an unknown session got %d", c)
	}
	if c := answer("/phone/v1/sessions/agent/answer", "right-token", `{"answers":[]}`); c != 400 {
		t.Fatalf("an answer to no question got %d", c)
	}
	// A terminal that asks nothing is the box's refusal, not the phone's.
	if c := answer("/phone/v1/sessions/agent/answer", "right-token", body); c < 400 {
		t.Fatalf("an answer to a session that asks nothing got %d", c)
	}
}
