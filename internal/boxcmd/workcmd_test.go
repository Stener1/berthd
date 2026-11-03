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
