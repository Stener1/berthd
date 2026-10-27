package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/cosscom/shipyard/internal/leadline/capture"
	"github.com/cosscom/shipyard/internal/leadline/demo"
	"github.com/cosscom/shipyard/internal/leadline/store"
)

// These tests are about what is this helper's own in the API: the sources,
// the proxies, who may change things, and how an error is said. The ones
// beside them (server, paging, breakdown) are Leadline's, run against it.

// rig is a helper's API over a store in a temporary folder, with proxies
// on whatever ports the system gives.
type rig struct {
	*httptest.Server
	store   *store.Store
	proxies *capture.Manager
	dir     string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "leadline.db"), store.Options{ReadWait: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	proxies, err := capture.Open(capture.Options{Dir: dir, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(st, proxies)
	ts := httptest.NewServer(srv.Handler())
	srv.SetOTLP(ts.URL)
	t.Cleanup(func() {
		ts.Close()
		proxies.Close()
		st.Close()
	})
	return &rig{Server: ts, store: st, proxies: proxies, dir: dir}
}

func newTestServer(t *testing.T) *httptest.Server { return newRig(t).Server }

// send makes one request and returns the status and the body.
func send(t *testing.T, method, url, origin string, body any) (int, string) {
	t.Helper()
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, payload)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// otlpTraces is an OTLP/JSON trace export of one request with n queries.
func otlpTraces(service, trace string, at time.Time, n int, resource string) string {
	spans := fmt.Sprintf(`{"traceId":%q,"spanId":"00000000000000a1","name":"GET /orders","kind":2,
		"startTimeUnixNano":"%d","endTimeUnixNano":"%d","attributes":[{"key":"http.route","value":{"stringValue":"/orders"}}]}`,
		trace, at.UnixNano(), at.Add(40*time.Millisecond).UnixNano())
	for i := 0; i < n; i++ {
		start := at.Add(time.Duration(i+1) * 2 * time.Millisecond)
		spans += fmt.Sprintf(`,{"traceId":%q,"spanId":"%016x","parentSpanId":"00000000000000a1","name":"pg.query","kind":3,
		"startTimeUnixNano":"%d","endTimeUnixNano":"%d","attributes":[
			{"key":"db.system.name","value":{"stringValue":"postgresql"}},
			{"key":"db.query.text","value":{"stringValue":"SELECT name FROM customers WHERE id = %d"}}]}`,
			trace, 0xb0+i, start.UnixNano(), start.Add(time.Millisecond).UnixNano(), 100+i)
	}
	return `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"` + service + `"}}` + resource + `]},
		"scopeSpans":[{"spans":[` + spans + `]}]}]}`
}

const acmeResource = `,{"key":"shipyard.project","value":{"stringValue":"acme"}},{"key":"shipyard.worktree","value":{"stringValue":"fix-orders"}},
	{"key":"shipyard.branch","value":{"stringValue":"fix/orders"}},{"key":"shipyard.box","value":{"stringValue":"devbox"}}`

// An export request as an SDK sends it, built by hand: the resource spans
// at field 1, and a field from some later version of the protocol, which
// is passed over.
func TestAnExportRequestIsReadAsItIsSent(t *testing.T) {
	ts := newTestServer(t)
	now := time.Now()
	data, logs := demo.New(5).Batch(3, now.Add(-time.Minute), now)
	var body []byte
	for _, rs := range data.ResourceSpans {
		raw, err := proto.Marshal(rs)
		if err != nil {
			t.Fatal(err)
		}
		body = protowire.AppendBytes(protowire.AppendTag(body, 1, protowire.BytesType), raw)
	}
	body = protowire.AppendString(protowire.AppendTag(body, 77, protowire.BytesType), "from the future")
	resp := post(t, ts.URL+"/v1/traces", "application/x-protobuf", body)
	answer, _ := io.ReadAll(resp.Body)
	// The answer is an ExportTraceServiceResponse with nothing to say,
	// which in protobuf is no bytes.
	if resp.StatusCode != http.StatusOK || len(answer) != 0 || resp.Header.Get("Content-Type") != "application/x-protobuf" {
		t.Fatalf("POST /v1/traces: %s, %q, %q", resp.Status, answer, resp.Header.Get("Content-Type"))
	}
	raw, _ := proto.Marshal(logs)
	if resp := post(t, ts.URL+"/v1/logs", "application/x-protobuf", raw); resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/logs: %s", resp.Status)
	}
	var sum store.Summary
	getJSON(t, ts.URL+"/api/summary", &sum)
	if sum.Traces != 3 || sum.Logs == 0 {
		t.Errorf("summary = %+v, want the 3 traces and their logs", sum)
	}
	// Metrics are taken and dropped, so an SDK told the endpoint does not
	// complain; what is not OTLP is refused.
	if resp := post(t, ts.URL+"/v1/metrics", "application/x-protobuf", []byte{1, 2, 3}); resp.StatusCode != http.StatusOK {
		t.Errorf("POST /v1/metrics: %s", resp.Status)
	}
	if resp := post(t, ts.URL+"/v1/traces", "application/x-protobuf", []byte("not protobuf")); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("garbage: %s, want 400", resp.Status)
	}
	if resp := post(t, ts.URL+"/v1/traces", "application/json", []byte(`{"resourceSpans":`)); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("half a JSON body: %s, want 400", resp.Status)
	}
}

// A route list is no log, and a log says its kind and who it was for.
func TestKindsOfLog(t *testing.T) {
	ts := newTestServer(t)
	logs := fmt.Sprintf(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"acme-api"}}]},
"scopeLogs":[{"logRecords":[
 {"timeUnixNano":"%[1]d","severityNumber":5,"body":{"stringValue":"{\"routes\":[]}"},"attributes":[{"key":"leadline.kind","value":{"stringValue":"next.routes"}}]},
 {"timeUnixNano":"%[1]d","severityNumber":17,"body":{"stringValue":"TypeError: x is undefined"},"attributes":[
   {"key":"leadline.kind","value":{"stringValue":"browser.error"}},{"key":"user.id","value":{"stringValue":"u_7"}}]},
 {"timeUnixNano":"%[1]d","severityNumber":9,"body":{"stringValue":"plain"}}]}]}]}`, time.Now().UnixNano())
	if resp := post(t, ts.URL+"/v1/logs", "application/json", []byte(logs)); resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/logs: %s", resp.Status)
	}
	var all, errors, mine []store.LogRow
	getJSON(t, ts.URL+"/api/logs", &all)
	getJSON(t, ts.URL+"/api/logs?kind=browser.error", &errors)
	getJSON(t, ts.URL+"/api/logs?user=u_7", &mine)
	if len(all) != 2 {
		t.Errorf("%d logs, want the two that are logs and not the route list", len(all))
	}
	if len(errors) != 1 || errors[0].Kind != "browser.error" || errors[0].UserID != "u_7" || len(mine) != 1 {
		t.Errorf("by kind: %+v; by user: %+v", errors, mine)
	}
}

// Every worktree is a source: the service of its name, the commands run in
// it, what its telemetry said it is, and the proxies opened for it.
func TestSources(t *testing.T) {
	r := newRig(t)
	now := time.Now()
	for _, body := range []string{
		otlpTraces("acme/fix-orders", "000000000000000000000000000000a1", now.Add(-3*time.Second), 2, acmeResource),
		otlpTraces("acme/fix-orders:worker", "000000000000000000000000000000a2", now.Add(-2*time.Second), 0, acmeResource),
		otlpTraces("acme/add-refunds", "000000000000000000000000000000a3", now.Add(-time.Second), 1, ""),
	} {
		if resp := post(t, r.URL+"/v1/traces", "application/json", []byte(body)); resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /v1/traces: %s", resp.Status)
		}
	}
	// A proxy opened for a worktree that has sent nothing yet: a source
	// from that moment.
	code, body := send(t, "POST", r.URL+"/api/proxies", "", map[string]any{"upstream": "127.0.0.1:1", "label": "acme/new-checkout",
		"project": "acme", "worktree": "new-checkout", "branch": "new-checkout", "box": "devbox"})
	if code != 200 {
		t.Fatalf("POST /api/proxies: %d %s", code, body)
	}

	var got Sources
	getJSON(t, r.URL+"/api/sources", &got)
	var names []string
	by := map[string]SourceView{}
	for _, s := range got.Sources {
		names = append(names, s.Name)
		by[s.Name] = s
	}
	if !slices.Equal(names, []string{"acme/add-refunds", "acme/fix-orders", "acme/new-checkout"}) || got.Total != 3 {
		t.Fatalf("sources = %v (total %d)", names, got.Total)
	}
	fix := by["acme/fix-orders"]
	if !slices.Equal(fix.Services, []string{"acme/fix-orders", "acme/fix-orders:worker"}) || fix.Spans != 4 ||
		fix.Project != "acme" || fix.Worktree != "fix-orders" || fix.Branch != "fix/orders" || fix.Box != "devbox" ||
		!slices.Equal(fix.Via, []string{"otlp"}) || fix.LastSeenMs < float64(now.Add(-3*time.Second).UnixMilli()) {
		t.Errorf("acme/fix-orders = %+v", fix)
	}
	if other := by["acme/add-refunds"]; other.Project != "" || other.Spans != 2 || len(other.ProxyIDs) != 0 {
		t.Errorf("acme/add-refunds, which said nothing of its worktree = %+v", other)
	}
	fresh := by["acme/new-checkout"]
	if fresh.Spans != 0 || len(fresh.ProxyIDs) != 1 || fresh.Worktree != "new-checkout" || len(fresh.Via) != 0 || fresh.Via == nil {
		t.Errorf("acme/new-checkout, which has only a proxy = %+v", fresh)
	}
	if len(got.Services) != 4 || got.Services[1].Name != "acme/fix-orders" || got.Services[1].Owner == nil ||
		got.Services[1].Owner.Label != "from Shipyard, worktree fix-orders" || got.Services[2].Source != "acme/fix-orders" ||
		got.Services[0].Owner != nil {
		t.Errorf("services = %+v", got.Services)
	}
	if len(got.Proxies) != 1 || got.Proxies[0].Label != "acme/new-checkout" || got.Proxies[0].Owner.Worktree != "new-checkout" ||
		got.Proxies[0].State != "listening" || got.Proxies[0].ID != fresh.ProxyIDs[0] {
		t.Errorf("proxies = %+v", got.Proxies)
	}
	if len(got.OTLP.Endpoints) != 1 || got.OTLP.Endpoints[0] != r.URL || got.SinceMs >= got.NowMs {
		t.Errorf("otlp = %+v, since %v, now %v", got.OTLP, got.SinceMs, got.NowMs)
	}

	// One source, and a page of them.
	var one, page Sources
	getJSON(t, r.URL+"/api/sources?source="+url.QueryEscape("acme/fix-orders"), &one)
	if len(one.Sources) != 1 || len(one.Services) != 2 || len(one.Proxies) != 0 || one.Total != 1 {
		t.Errorf("one source = %+v", one)
	}
	getJSON(t, r.URL+"/api/sources?limit=1&offset=2", &page)
	if len(page.Sources) != 1 || page.Sources[0].Name != "acme/new-checkout" || page.Total != 3 || len(page.Proxies) != 1 || len(page.Services) != 1 {
		t.Errorf("the third source alone = %+v", page)
	}
	// Nothing held: empty lists, not nulls.
	_, raw := send(t, "GET", newTestServer(t).URL+"/api/sources", "", nil)
	for _, want := range []string{`"sources":[]`, `"services":[]`, `"proxies":[]`, `"total":0`} {
		if !strings.Contains(raw, want) {
			t.Errorf("an empty helper's sources lack %s: %s", want, raw)
		}
	}

	// The list routes take the source.
	var traces []store.TraceRow
	getJSON(t, r.URL+"/api/traces?source="+url.QueryEscape("acme/fix-orders"), &traces)
	var queries []store.QueryRow
	getJSON(t, r.URL+"/api/queries?source="+url.QueryEscape("acme/add-refunds"), &queries)
	var eps store.Endpoints
	getJSON(t, r.URL+"/api/endpoints?source="+url.QueryEscape("acme/fix-orders"), &eps)
	if len(traces) != 2 || len(queries) != 1 || queries[0].Calls != 1 || len(eps.Rows) != 2 {
		t.Errorf("by source: %d traces, queries %+v, %d endpoints", len(traces), queries, len(eps.Rows))
	}
}

// A proxy is opened for a source, found again by asking again, listed,
// kept across a restart on the port it had, and closed.
func TestProxies(t *testing.T) {
	r := newRig(t)
	open := func(body map[string]any) (created bool, v capture.View) {
		t.Helper()
		code, raw := send(t, "POST", r.URL+"/api/proxies", "", body)
		if code != 200 {
			t.Fatalf("POST /api/proxies %v: %d %s", body, code, raw)
		}
		var out struct {
			Created bool         `json:"created"`
			Proxy   capture.View `json:"proxy"`
		}
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(err)
		}
		return out.Created, out.Proxy
	}
	created, first := open(map[string]any{"kind": "postgres", "upstream": "localhost:5432", "label": "acme/fix-orders"})
	if !created || first.ID == "" || first.Port == 0 || first.Listen != fmt.Sprintf("127.0.0.1:%d", first.Port) ||
		first.Upstream != "localhost:5432" || first.State != "listening" || !slices.Equal(first.Origins, []string{"api"}) {
		t.Fatalf("the first proxy = %+v", first)
	}
	// The same server, spelled the other way, for the same worktree: the
	// proxy there is.
	if created, again := open(map[string]any{"upstream": "127.0.0.1:5432", "label": "acme/fix-orders"}); created || again.ID != first.ID {
		t.Errorf("asking again gave %+v (created %v), want the first", again, created)
	}
	// Another worktree on the same server gets a port of its own.
	_, second := open(map[string]any{"upstream": "5432", "label": "acme/add-refunds"})
	if second.ID == first.ID || second.Port == first.Port || second.Upstream != "127.0.0.1:5432" {
		t.Errorf("the second worktree's proxy = %+v", second)
	}
	_, third := open(map[string]any{"upstream": "127.0.0.1:5433", "label": "acme/fix-orders:worker"})

	var list struct {
		Total   int            `json:"total"`
		Proxies []capture.View `json:"proxies"`
	}
	getJSON(t, r.URL+"/api/proxies", &list)
	if list.Total != 3 || len(list.Proxies) != 3 || list.Proxies[0].ID != first.ID {
		t.Errorf("proxies = %+v", list)
	}
	getJSON(t, r.URL+"/api/proxies?source="+url.QueryEscape("acme/fix-orders")+"&limit=1&offset=1", &list)
	if list.Total != 2 || len(list.Proxies) != 1 || list.Proxies[0].ID != third.ID {
		t.Errorf("the worktree's second proxy = %+v", list)
	}

	// What cannot be a proxy says why, as JSON.
	for _, c := range []struct {
		body   map[string]any
		status int
		code   string
	}{
		{map[string]any{"upstream": "postgres://app:hunter2@localhost:5432/acme"}, 400, "bad_request"},
		{map[string]any{"upstream": "db.acme.example:5432"}, 400, "capture_remote"},
		{map[string]any{"upstream": "10.0.0.5:5432"}, 400, "capture_remote"},
		{map[string]any{"upstream": ""}, 400, "bad_request"},
		{map[string]any{"kind": "redis", "upstream": "localhost:6379"}, 400, "bad_request"},
		{map[string]any{"upstream": "localhost:5432", "label": "acme/x", "port": first.Port}, 409, "port_in_use"},
	} {
		code, raw := send(t, "POST", r.URL+"/api/proxies", "", c.body)
		var out struct {
			Error capture.Problem `json:"error"`
		}
		json.Unmarshal([]byte(raw), &out)
		if code != c.status || out.Error.Code != c.code || out.Error.Message == "" {
			t.Errorf("POST /api/proxies %v: %d %s, want %d %s", c.body, code, raw, c.status, c.code)
		}
		if strings.Contains(raw, "hunter2") {
			t.Errorf("the answer repeats the password it was sent: %s", raw)
		}
	}
	if code, raw := send(t, "POST", r.URL+"/api/proxies", "", "not an object"); code != 400 || !strings.Contains(raw, `"bad_request"`) {
		t.Errorf("a body that is no object: %d %s", code, raw)
	}

	// What is saved has no more than the addresses and the names.
	saved, err := os.ReadFile(filepath.Join(r.dir, "proxies.json"))
	if err != nil || !strings.Contains(string(saved), first.ID) || !strings.Contains(string(saved), `"acme/fix-orders"`) {
		t.Fatalf("proxies.json = %s, %v", saved, err)
	}
	if fi, _ := os.Stat(filepath.Join(r.dir, "proxies.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("proxies.json has mode %v", fi.Mode().Perm())
	}
	// After a restart the proxies are there again, each on its port.
	r.proxies.Close()
	again, err := capture.Open(capture.Options{Dir: r.dir, Store: r.store})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	back := again.Proxies(time.Time{})
	if len(back) != 3 || back[0].ID != first.ID || back[0].Port != first.Port || back[0].State != "listening" ||
		back[1].Port != second.Port || back[1].Label != "acme/add-refunds" {
		t.Errorf("after a restart: %+v", back)
	}
	if err := again.CloseProxy(second.ID); err != nil {
		t.Fatal(err)
	}
	if left := again.Proxies(time.Time{}); len(left) != 2 {
		t.Errorf("after closing one: %+v", left)
	}
	again.Close()

	// Over the API: closed once, and then there is no such proxy.
	r2 := newRig(t)
	code, raw := send(t, "POST", r2.URL+"/api/proxies", "", map[string]any{"upstream": "127.0.0.1:5432", "label": "acme/fix-orders"})
	var out struct {
		Proxy capture.View `json:"proxy"`
	}
	json.Unmarshal([]byte(raw), &out)
	if code != 200 || out.Proxy.ID == "" {
		t.Fatalf("POST /api/proxies: %d %s", code, raw)
	}
	if code, raw := send(t, "DELETE", r2.URL+"/api/proxies/"+out.Proxy.ID, "", nil); code != 200 || !strings.Contains(raw, `"closed":true`) {
		t.Errorf("DELETE: %d %s", code, raw)
	}
	if code, raw := send(t, "DELETE", r2.URL+"/api/proxies/"+out.Proxy.ID, "", nil); code != 404 || !strings.Contains(raw, `"not_found"`) {
		t.Errorf("DELETE again: %d %s", code, raw)
	}
}

// Detaching a worktree closes its proxies and deletes what was recorded
// under its name and the names under it, and nothing of another's.
func TestDetach(t *testing.T) {
	r := newRig(t)
	now := time.Now()
	for _, body := range []string{
		otlpTraces("acme/fix-orders", "000000000000000000000000000000a1", now.Add(-3*time.Second), 2, acmeResource),
		otlpTraces("acme/fix-orders:worker", "000000000000000000000000000000a2", now.Add(-2*time.Second), 1, acmeResource),
		otlpTraces("acme/fix-orders-2", "000000000000000000000000000000a3", now.Add(-time.Second), 1, ""),
	} {
		post(t, r.URL+"/v1/traces", "application/json", []byte(body))
	}
	for _, label := range []string{"acme/fix-orders", "acme/fix-orders:worker", "acme/fix-orders-2"} {
		if code, raw := send(t, "POST", r.URL+"/api/proxies", "", map[string]any{"upstream": "127.0.0.1:5432", "label": label}); code != 200 {
			t.Fatalf("POST /api/proxies: %d %s", code, raw)
		}
	}
	detach := func(body map[string]any) (int, string) {
		return send(t, "POST", r.URL+"/api/detach", "", body)
	}
	// Keeping the data: the proxies go, the rows stay.
	if code, raw := detach(map[string]any{"service": "acme/fix-orders", "keep_data": true}); code != 200 ||
		!strings.Contains(raw, `"proxies":2`) || !strings.Contains(raw, `"kept_data":true`) || !strings.Contains(raw, `"found":true`) {
		t.Errorf("detach, keeping the data: %d %s", code, raw)
	}
	var sum store.Summary
	getJSON(t, r.URL+"/api/summary", &sum)
	if sum.Spans != 7 {
		t.Errorf("%d spans after a detach that keeps them, want 7", sum.Spans)
	}
	code, raw := detach(map[string]any{"service": "acme/fix-orders"})
	var out struct {
		Found   bool            `json:"found"`
		Proxies int             `json:"proxies"`
		Deleted store.Forgotten `json:"deleted"`
	}
	json.Unmarshal([]byte(raw), &out)
	if code != 200 || !out.Found || out.Proxies != 0 || out.Deleted.Spans != 5 {
		t.Errorf("detach: %d %s", code, raw)
	}
	var got Sources
	getJSON(t, r.URL+"/api/sources", &got)
	if len(got.Sources) != 1 || got.Sources[0].Name != "acme/fix-orders-2" || got.Sources[0].Spans != 2 || len(got.Proxies) != 1 {
		t.Errorf("what is left = %+v", got)
	}
	// A worktree the helper never heard of: nothing found, no error.
	if code, raw := detach(map[string]any{"service": "acme/never-seen"}); code != 200 || !strings.Contains(raw, `"found":false`) {
		t.Errorf("detaching a stranger: %d %s", code, raw)
	}
	if code, raw := detach(map[string]any{}); code != 400 || !strings.Contains(raw, `"bad_request"`) {
		t.Errorf("detaching nobody: %d %s", code, raw)
	}
	// A worktree alone names nothing: its project is what files it.
	if code, raw := detach(map[string]any{"worktree": "fix-orders-2"}); code != 400 || !strings.Contains(raw, `"bad_request"`) {
		t.Errorf("detaching a worktree of no project: %d %s", code, raw)
	}
	// As the box daemon says it, in the words it attached with: the
	// project and the worktree, and the source's name follows from them.
	code, raw = detach(map[string]any{"box": "devbox", "project": "acme", "worktree": "fix-orders-2", "branch": "fix/orders", "path": "/srv/acme/fix-orders-2"})
	out.Found, out.Proxies = false, 0
	json.Unmarshal([]byte(raw), &out)
	if code != 200 || !out.Found || out.Proxies != 1 || out.Deleted.Spans != 2 || !strings.Contains(raw, `"service":"acme/fix-orders-2"`) {
		t.Errorf("detach, as the box daemon asks: %d %s", code, raw)
	}
	getJSON(t, r.URL+"/api/sources", &got)
	if len(got.Sources) != 0 || len(got.Proxies) != 0 {
		t.Errorf("what is left after the last worktree went = %+v", got)
	}
	// The project's own checkout has no worktree's name.
	if code, raw := detach(map[string]any{"project": "acme", "worktree": ""}); code != 200 || !strings.Contains(raw, `"service":"acme"`) {
		t.Errorf("detaching the main checkout: %d %s", code, raw)
	}
}

// Any page on this machine may read and may send telemetry: an app under
// test reports from its pages. Only Shipyard's own window, or no page at
// all, may change what the helper holds.
func TestOnlyShipyardsWindowMayChangeThings(t *testing.T) {
	r := newRig(t)
	body := map[string]any{"upstream": "127.0.0.1:5432", "label": "acme/fix-orders"}
	for _, c := range []struct {
		method, path, origin string
		body                 any
		want                 int
	}{
		{"GET", "/api/sources", "http://localhost:3000", nil, 200},
		{"POST", "/v1/traces", "http://localhost:3000", map[string]any{}, 200},
		{"POST", "/api/proxies", "http://localhost:3000", body, 403},
		{"POST", "/api/detach", "http://127.0.0.1:5173", map[string]any{"service": "acme/fix-orders"}, 403},
		{"DELETE", "/api/proxies/p_1", "http://localhost:3000", nil, 403},
		{"POST", "/api/proxies", "https://example.com", body, 403},
		{"POST", "/api/proxies", "tauri://localhost", body, 200},
		{"POST", "/api/proxies", "http://tauri.localhost", body, 200},
		{"POST", "/api/proxies", "http://localhost:1420", body, 200},
		{"POST", "/api/proxies", "", body, 200},
	} {
		req, _ := http.NewRequest(c.method, r.URL+c.path, nil)
		if c.body != nil {
			raw, _ := json.Marshal(c.body)
			req, _ = http.NewRequest(c.method, r.URL+c.path, bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
		}
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s %s from %q: %d, want %d", c.method, c.path, c.origin, resp.StatusCode, c.want)
		}
	}
	// The browser asks first; DELETE is allowed to the origins that may
	// ask at all.
	preflight := func(origin, method string) (int, string) {
		req, _ := http.NewRequest("OPTIONS", r.URL+"/api/proxies/p_1", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", method)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("Access-Control-Allow-Methods")
	}
	for _, origin := range []string{"http://localhost:1420", "tauri://localhost"} {
		if code, allowed := preflight(origin, "DELETE"); code != http.StatusNoContent || !strings.Contains(allowed, "DELETE") {
			t.Errorf("DELETE from %s: %d, allowed %q", origin, code, allowed)
		}
	}
	if code, allowed := preflight("https://example.com", "DELETE"); code != http.StatusForbidden || allowed != "" {
		t.Errorf("DELETE from a foreign origin: %d, allowed %q", code, allowed)
	}
	// An origin added by hand.
	t.Setenv("BERTH_LEADLINE_APP_ORIGINS", "http://localhost:5199 , http://localhost:5200")
	if code, raw := send(t, "POST", r.URL+"/api/proxies", "http://localhost:5200", body); code != 200 {
		t.Errorf("an origin from BERTH_LEADLINE_APP_ORIGINS: %d %s", code, raw)
	}
}

// What goes wrong is said one way: {"error":{"code","message","hint"}}.
func TestErrorsAreJSON(t *testing.T) {
	ts := newTestServer(t)
	for path, want := range map[string]int{
		"/api/traces/nope":                     404,
		"/api/nothing":                         404,
		"/api/traces?before=nonsense":          400,
		"/api/endpoints?since_ms=2&until_ms=1": 400,
		"/api/endpoints/detail":                400,
		"/api/queries/detail":                  400,
		"/api/breakdown?by=nonsense":           400,
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Error capture.Problem `json:"error"`
		}
		err = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if resp.StatusCode != want || err != nil || out.Error.Code == "" || out.Error.Message == "" ||
			resp.Header.Get("Content-Type") != "application/json" {
			t.Errorf("GET %s: %d %+v (%v), want %d and an error in JSON", path, resp.StatusCode, out, err, want)
		}
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/health", nil)
	req.Host = "evil.example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 403 || !strings.Contains(string(raw), `"forbidden"`) {
		t.Errorf("a foreign host: %d %s", resp.StatusCode, raw)
	}
	var health struct {
		OK      bool   `json:"ok"`
		Name    string `json:"name"`
		Version string `json:"version"`
		API     int    `json:"api"`
	}
	getJSON(t, ts.URL+"/api/health", &health)
	if !health.OK || health.Name != "berth-leadline" || health.Version == "" || health.API != Version {
		t.Errorf("health = %+v", health)
	}
}

// The event stream greets, and then says so each time data arrives.
func TestEvents(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	read := func(want string) {
		t.Helper()
		buf := make([]byte, 256)
		got := ""
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(got, want) {
			if time.Now().After(deadline) {
				t.Fatalf("the stream said %q, want %q", got, want)
			}
			n, err := resp.Body.Read(buf)
			got += string(buf[:n])
			if err != nil {
				t.Fatalf("the stream ended at %q: %v", got, err)
			}
		}
	}
	read("event: hello\n")
	post(t, ts.URL+"/v1/traces", "application/json",
		[]byte(otlpTraces("acme/fix-orders", "000000000000000000000000000000a1", time.Now(), 1, "")))
	read("event: ingest\n")
}

// A trace opened over the API is its spans, the same as a tree, and the
// logs written during it.
func TestATraceOverTheAPI(t *testing.T) {
	ts := newTestServer(t)
	now := time.Now().Add(-time.Second)
	const trace = "000000000000000000000000000000c1"
	post(t, ts.URL+"/v1/traces", "application/json", []byte(otlpTraces("acme/fix-orders", trace, now, 6, acmeResource)))
	logs := fmt.Sprintf(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"acme/fix-orders"}}]},
"scopeLogs":[{"logRecords":[{"timeUnixNano":"%d","severityNumber":13,"severityText":"WARN","body":{"stringValue":"slow order list"},
 "traceId":%q,"spanId":"00000000000000a1","attributes":[{"key":"orders","value":{"intValue":"6"}}]}]}]}]}`, now.Add(5*time.Millisecond).UnixNano(), trace)
	post(t, ts.URL+"/v1/logs", "application/json", []byte(logs))

	var got struct {
		TraceID string            `json:"trace_id"`
		Spans   []store.SpanRow   `json:"spans"`
		Tree    []store.TraceNode `json:"tree"`
		Logs    []store.LogRow    `json:"logs"`
	}
	getJSON(t, ts.URL+"/api/traces/"+trace, &got)
	if got.TraceID != trace || len(got.Spans) != 7 || len(got.Tree) != 1 || len(got.Tree[0].Children) != 6 ||
		got.Tree[0].SpanID != "00000000000000a1" || got.Spans[0].Depth != 0 || got.Spans[3].Depth != 1 {
		t.Fatalf("trace = %+v", got)
	}
	if q := got.Spans[1]; q.DBStatement != "SELECT name FROM customers WHERE id = 100" || q.DBRole != "query" || q.DBSystem != "postgresql" {
		t.Errorf("the first query's span = %+v", q)
	}
	if len(got.Logs) != 1 || got.Logs[0].Body != "slow order list" || got.Logs[0].Severity != "warn" || string(got.Logs[0].Attrs) != `{"orders":6}` {
		t.Errorf("logs = %+v", got.Logs)
	}
	// Six of one statement in one request: an N+1, with the request.
	var queries []store.QueryRow
	getJSON(t, ts.URL+"/api/queries", &queries)
	if len(queries) != 1 || !queries[0].NPlusOne || queries[0].MaxPerTrace != 6 || queries[0].Fingerprint != "SELECT name FROM customers WHERE id = ?" {
		t.Fatalf("queries = %+v", queries)
	}
	var detail store.QueryDetail
	getJSON(t, ts.URL+"/api/queries/detail?db_system=postgresql&fingerprint="+url.QueryEscape(queries[0].Fingerprint), &detail)
	if detail.Stats == nil || !detail.Stats.NPlusOne || len(detail.NPlusOne) != 1 || detail.NPlusOne[0].TraceID != trace ||
		detail.NPlusOne[0].Name != "GET /orders" || detail.NPlusOne[0].Calls != 6 {
		t.Errorf("the query's detail = %+v, %+v", detail.Stats, detail.NPlusOne)
	}
}
