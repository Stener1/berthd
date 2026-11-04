package boxcmd

import (
	"strings"
	"testing"
)

// Only the flags given are sent, so an update changes only what it names,
// and one given empty clears.
func TestWorkUpdateSendsOnlyWhatWasGiven(t *testing.T) {
	r, _ := run(t, `{"id":"w_1","status":"parked","sessions":[]}`, "work", "update", "w_1", "--not-until", "2026-05-01", "--waiting-on", "")
	if r.path != "/v1/work/w_1" || r.method != "PATCH" {
		t.Fatalf("%s %s", r.method, r.path)
	}
	if r.body["not_until"] != "2026-05-01" {
		t.Errorf("body %v", r.body)
	}
	if v, ok := r.body["waiting_on"]; !ok || v != "" {
		t.Errorf("an empty flag should clear: %v", r.body)
	}
	if _, ok := r.body["title"]; ok {
		t.Errorf("title wasn't given: %v", r.body)
	}
}

func TestWorkListAndGroup(t *testing.T) {
	r, out := run(t, `[{"id":"w_1","title":"Fix the header","status":"open","issue":"X-1","sessions":[{"name":"a"}]}]`, "work", "list", "--all")
	if r.path != "/v1/work?all=1" || !strings.Contains(out, "w_1") || !strings.Contains(out, "Fix the header") || !strings.Contains(out, "never") {
		t.Errorf("%s: %s", r.path, out)
	}
	r, _ = run(t, `{"id":"w_1","status":"open","sessions":[]}`, "work", "group", "w_1", "--with", "w_2,w_3", "--session", "a")
	if r.path != "/v1/work/w_1/group" || len(r.body["with"].([]any)) != 2 || len(r.body["sessions"].([]any)) != 1 {
		t.Errorf("%s %v", r.path, r.body)
	}
	r, _ = run(t, `{"removed":true}`, "work", "unpin", "project", "Big launch")
	if r.method != "DELETE" || !strings.HasPrefix(r.path, "/v1/work/pins?") || !strings.Contains(r.path, "target=Big+launch") {
		t.Errorf("%s %s", r.method, r.path)
	}
}

func TestSessionNewNamesTheSessionItRunsIn(t *testing.T) {
	t.Setenv("BERTH_SESSION", "parent-1")
	r, _ := run(t, `{"name":"s","dir":"/w"}`, "session", "new", "shop/x", "--agent", "claude")
	if r.body["from_session"] != "parent-1" {
		t.Errorf("body %v", r.body)
	}
}

func TestWorkFindAsksByKeyAndSaysWhoOwnsIt(t *testing.T) {
	r, out := run(t, `{"key":"shop#12","provenance":"claimed","owner":{"name":"worker","live":true,"agent_state":"running"},"claimants":[{"name":"worker","live":true},{"name":"build"}],"subscribers":[{"name":"notes","live":true}],"record":{"id":"w_1","title":"Fix it","status":"open","sessions":[]}}`, "work", "find", "--key", "shop#12")
	if r.method != "GET" || r.path != "/v1/work/threads?key=shop%2312" {
		t.Fatalf("%s %s", r.method, r.path)
	}
	for _, want := range []string{"shop#12 is owned by worker (running)", "record: w_1 (claimed) Fix it", "claimed by build (ended)", "subscriber notes (live)"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	_, out = run(t, `{"key":"X-1","owner":null,"claimants":[],"subscribers":[],"record":null}`, "work", "find", "--key", "X-1")
	if !strings.Contains(out, "X-1 has no owner") {
		t.Errorf("%s", out)
	}
}

func TestThreadVerbsDefaultToThisSession(t *testing.T) {
	t.Setenv("BERTH_SESSION", "me-1")
	r, _ := run(t, `{"id":"w_1","status":"open","sessions":[]}`, "work", "claim", "--key", "a,b", "--key", "file:c d")
	if r.path != "/v1/work/threads/claim" || r.body["session"] != "me-1" {
		t.Fatalf("%s %v", r.path, r.body)
	}
	if keys := r.body["keys"].([]any); len(keys) != 2 || keys[0] != "a,b" || keys[1] != "file:c d" {
		t.Errorf("a key is opaque, commas and all: %v", keys)
	}
	r, _ = run(t, `{"id":"w_1","status":"open","sessions":[]}`, "work", "unsubscribe", "--key", "a", "--session", "other")
	if r.path != "/v1/work/threads/unsubscribe" || r.body["session"] != "other" {
		t.Errorf("%s %v", r.path, r.body)
	}
}

func TestClaimAtStartAndSendWithAKey(t *testing.T) {
	r, _ := run(t, `{"name":"s","dir":"/w"}`, "session", "new", "shop", "--claim", "X-1", "--claim", "service:api", "--", "sleep", "9")
	if claims := r.body["claims"].([]any); len(claims) != 2 || claims[1] != "service:api" {
		t.Errorf("session new: %v", r.body)
	}
	r, _ = run(t, `{"worktree":{"name":"x"},"session":{"name":"s"}}`, "task", "new", "shop/x", "--claim", "X-1")
	if claims := r.body["claims"].([]any); len(claims) != 1 || claims[0] != "X-1" {
		t.Errorf("task new: %v", r.body)
	}
	r, _ = run(t, `{"queued":true,"turn":"s#1","delivery":"d_1"}`, "session", "send", "s", "go", "--when", "idle", "--key", "X-1", "--source", "drop")
	if r.path != "/v1/sessions/s/send" || r.body["key"] != "X-1" || r.body["source"] != "drop" || r.body["when"] != "idle" {
		t.Errorf("session send: %s %v", r.path, r.body)
	}
}

func TestHeldPromptsAndResumeUseTheBoxsRoutes(t *testing.T) {
	r, out := run(t, `[{"turn":"s#3","preview":"do\nthis","length":7,"origin":"berth","at":"2026-03-01T12:00:00Z"}]`, "session", "queue", "s")
	if r.method != "GET" || r.path != "/v1/sessions/s/queue" || !strings.Contains(out, "s#3") || !strings.Contains(out, "do this") {
		t.Errorf("%s %s: %s", r.method, r.path, out)
	}
	r, _ = run(t, `{"cancelled":"s#3"}`, "session", "unqueue", "s", "s#3")
	if r.method != "DELETE" || r.path != "/v1/sessions/s/queue/s%233" {
		t.Errorf("%s %s", r.method, r.path)
	}
	r, out = run(t, `{"name":"s","dir":"/w"}`, "session", "resume", "s")
	if r.method != "POST" || r.path != "/v1/sessions/s/resume" || !strings.Contains(out, "Resumed in session s") {
		t.Errorf("%s %s: %s", r.method, r.path, out)
	}
	r, out = run(t, `{"name":"new-1","dir":"/w"}`, "conversation", "resume", "c1", "--open", "tab")
	if r.path != "/v1/conversations/c1/resume" || r.body["open"] != "tab" || !strings.Contains(out, "new-1") {
		t.Errorf("%s %v: %s", r.path, r.body, out)
	}
}

func TestDeliveryVerbs(t *testing.T) {
	reply := `{"id":"d_1","key":"X-1","to":{"box":"b","name":"s"},"turn":"s#1","state":"undone","retracted":true}`
	r, out := run(t, reply, "work", "undo", "d_1")
	if r.method != "POST" || r.path != "/v1/work/deliveries/d_1/undo" || !strings.Contains(out, "never typed") {
		t.Errorf("%s %s: %s", r.method, r.path, out)
	}
	r, _ = run(t, `{"id":"d_1","key":"X-1","state":"handed_back","why":"not mine"}`, "work", "hand-back", "d_1", "--why", "not mine")
	if r.path != "/v1/work/deliveries/d_1/hand-back" || r.body["why"] != "not mine" {
		t.Errorf("%s %v", r.path, r.body)
	}
	r, _ = run(t, `[]`, "work", "deliveries", "--key", "shop#1", "--state", "held")
	if !strings.Contains(r.path, "key=shop%231") || !strings.Contains(r.path, "state=held") {
		t.Errorf("%s", r.path)
	}
}
