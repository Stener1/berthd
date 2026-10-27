package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLinear is Linear's GraphQL API for one key: it answers the documents
// the agent sends and records what it was asked.
type fakeLinear struct {
	key   string
	asked []map[string]any
}

func (f *fakeLinear) start(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != f.key {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"errors":[{"message":"Authentication required, not authenticated","extensions":{"type":"authentication error","code":"AUTHENTICATION_ERROR"}}]}`)
			return
		}
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		f.asked = append(f.asked, map[string]any{"query": req.Query, "variables": req.Variables})
		issue := `{"id":"uuid-1","identifier":"WEB-12","title":"Checkout double-charges on retry","url":"https://linear.app/acme/issue/WEB-12","priority":2,"priorityLabel":"High","createdAt":"2026-10-01T09:00:00Z","updatedAt":"2026-10-10T09:00:00Z","branchName":"dana/web-12-checkout-double-charges","state":{"name":"In Progress","type":"started","color":"#f2c94c"},"team":{"id":"t1","key":"WEB","name":"Web"},"assignee":{"id":"u1","name":"Dana Reyes","displayName":"dana"},"creator":null,"labels":{"nodes":[{"name":"Bug","color":"#eb5757"}]}`
		switch {
		case strings.Contains(req.Query, "viewer"):
			io.WriteString(w, `{"data":{"viewer":{"id":"u1","name":"Dana Reyes","displayName":"dana","email":"dana@acme.test"},"organization":{"name":"Acme","urlKey":"acme"}}}`)
		case strings.Contains(req.Query, "teams("):
			io.WriteString(w, `{"data":{"teams":{"nodes":[{"id":"t1","key":"WEB","name":"Web"},{"id":"t2","key":"API","name":"API"}]}}}`)
		case strings.Contains(req.Query, "commentCreate"):
			io.WriteString(w, `{"data":{"commentCreate":{"success":true}}}`)
		case strings.Contains(req.Query, "issue(id"):
			if req.Variables["id"] != "WEB-12" {
				io.WriteString(w, `{"errors":[{"message":"Entity not found: Issue"}]}`)
				return
			}
			io.WriteString(w, `{"data":{"issue":`+issue+`,"description":"Retrying a failed payment charges twice.","comments":{"nodes":[{"body":"second","createdAt":"2026-10-03T00:00:00Z","url":"","user":{"name":"Sam Ortiz","displayName":"sam"}},{"body":"first","createdAt":"2026-10-02T00:00:00Z","url":"","user":null}]},"attachments":{"nodes":[{"title":"acme/shop#41","url":"https://github.com/acme/shop/pull/41","sourceType":"github"}]}}}}`)
		case strings.Contains(req.Query, "issues("):
			io.WriteString(w, `{"data":{"issues":{"nodes":[`+issue+`}]}}}`)
		default:
			io.WriteString(w, `{"errors":[{"message":"unexpected query"}]}`)
		}
	}))
	old := linearEndpoint
	linearEndpoint = srv.URL
	t.Cleanup(func() {
		linearEndpoint = old
		srv.Close()
	})
}

func linearMux(a *Agent) *http.ServeMux {
	mux := http.NewServeMux()
	a.linearRoutes(mux)
	return mux
}

func linearCall(t *testing.T, mux *http.ServeMux, method, path, body string, out any) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	raw := rec.Body.String()
	if out != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), out); err != nil {
			t.Fatalf("%s %s: %v in %s", method, path, err, raw)
		}
	}
	return rec.Code, raw
}

func TestLinearKeepsAKeyOnlyLinearAccepts(t *testing.T) {
	f := &fakeLinear{key: "lin_api_good"}
	f.start(t)
	a := testAgent(t)
	mux := linearMux(a)

	var st LinearStatus
	if code, _ := linearCall(t, mux, "GET", "/v1/linear", "", &st); code != 200 || st.Connected || st.File != filepath.Join(a.cfg.Dir, linearFile) {
		t.Fatalf("before a key: %d %+v", code, st)
	}
	// A refused key is not kept, and is said to be refused.
	code, raw := linearCall(t, mux, "PUT", "/v1/linear", `{"api_key":"lin_api_bad"}`, nil)
	if code != http.StatusUnauthorized || !strings.Contains(raw, `"linear_auth"`) {
		t.Fatalf("bad key: %d %s", code, raw)
	}
	if _, err := os.Stat(filepath.Join(a.cfg.Dir, linearFile)); !os.IsNotExist(err) {
		t.Fatalf("a refused key was written: %v", err)
	}
	if code, raw := linearCall(t, mux, "PUT", "/v1/linear", `{"api_key":"has a space"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("not a key: %d %s", code, raw)
	}

	code, raw = linearCall(t, mux, "PUT", "/v1/linear", `{"api_key":"  lin_api_good\n"}`, &st)
	if code != 200 || !st.Connected || st.Viewer == nil || st.Viewer.Name != "Dana Reyes" || st.Workspace == nil || st.Workspace.URLKey != "acme" {
		t.Fatalf("good key: %d %s", code, raw)
	}
	// The app is never told the key, and the file is the owner's alone.
	if strings.Contains(raw, "lin_api_good") {
		t.Fatalf("the key came back: %s", raw)
	}
	if _, raw := linearCall(t, mux, "GET", "/v1/linear", "", &st); strings.Contains(raw, "lin_api_good") || !st.Connected {
		t.Fatalf("status: %s", raw)
	}
	fi, err := os.Stat(filepath.Join(a.cfg.Dir, linearFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("linear.json: %v %v", fi, err)
	}

	if code, _ := linearCall(t, mux, "DELETE", "/v1/linear", "", &st); code != 200 || st.Connected {
		t.Fatalf("disconnect: %d %+v", code, st)
	}
	if code, raw := linearCall(t, mux, "GET", "/v1/linear/issues", "", nil); code != http.StatusConflict || !strings.Contains(raw, `"linear_off"`) {
		t.Fatalf("issues without a key: %d %s", code, raw)
	}
}

func TestLinearIssuesTeamsAndComments(t *testing.T) {
	f := &fakeLinear{key: "lin_api_good"}
	f.start(t)
	a := testAgent(t)
	mux := linearMux(a)
	if code, raw := linearCall(t, mux, "PUT", "/v1/linear", `{"api_key":"lin_api_good"}`, nil); code != 200 {
		t.Fatalf("connect: %d %s", code, raw)
	}

	var teams struct{ Teams []linearTeam }
	linearCall(t, mux, "GET", "/v1/linear/teams", "", &teams)
	if len(teams.Teams) != 2 || teams.Teams[0].Key != "API" {
		t.Fatalf("teams by name: %+v", teams.Teams)
	}

	var list struct{ Issues []LinearIssue }
	if code, raw := linearCall(t, mux, "GET", "/v1/linear/issues?team=WEB&mine=1", "", &list); code != 200 || len(list.Issues) != 1 {
		t.Fatalf("issues: %d %s", code, raw)
	}
	i := list.Issues[0]
	if i.Identifier != "WEB-12" || i.Priority != 2 || i.State.Type != "started" || i.Assignee == nil || i.Assignee.ID != "u1" || i.Branch != "dana/web-12-checkout-double-charges" || i.Creator != "" || len(i.Labels) != 1 {
		t.Fatalf("issue: %+v", i)
	}
	// Only open work is asked for, narrowed to the team and to the owner.
	filter, _ := json.Marshal(f.asked[len(f.asked)-1]["variables"])
	for _, want := range []string{`"nin":["completed","canceled"]`, `"key":{"eq":"WEB"}`, `"isMe":{"eq":true}`} {
		if !strings.Contains(string(filter), want) {
			t.Fatalf("filter %s lacks %s", filter, want)
		}
	}

	var d LinearIssueDetail
	if code, raw := linearCall(t, mux, "GET", "/v1/linear/issues/WEB-12", "", &d); code != 200 {
		t.Fatalf("detail: %d %s", code, raw)
	}
	if d.Description == "" || len(d.Comments) != 2 || d.Comments[0].Body != "first" || d.Comments[1].Author != "Sam Ortiz" || len(d.Links) != 1 || d.Links[0].Source != "github" {
		t.Fatalf("detail: %+v", d)
	}
	if code, raw := linearCall(t, mux, "GET", "/v1/linear/issues/WEB-404", "", nil); code != http.StatusBadGateway || !strings.Contains(raw, "Entity not found") {
		t.Fatalf("missing issue: %d %s", code, raw)
	}

	if code, raw := linearCall(t, mux, "POST", "/v1/linear/issues/uuid-1/comments", `{"body":"  "}`, nil); code != http.StatusBadRequest {
		t.Fatalf("empty comment: %d %s", code, raw)
	}
	if code, raw := linearCall(t, mux, "POST", "/v1/linear/issues/uuid-1/comments", `{"body":"On it."}`, nil); code != 200 {
		t.Fatalf("comment: %d %s", code, raw)
	}
	last := f.asked[len(f.asked)-1]["variables"].(map[string]any)
	if last["id"] != "uuid-1" || last["body"] != "On it." {
		t.Fatalf("comment sent: %+v", last)
	}

	// A key Linear stops accepting reads as that, so the app can say so.
	f.key = "lin_api_rotated"
	if code, raw := linearCall(t, mux, "GET", "/v1/linear/issues", "", nil); code != http.StatusUnauthorized || !strings.Contains(raw, `"linear_auth"`) {
		t.Fatalf("rotated key: %d %s", code, raw)
	}
}
