package helper_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/cosscom/shipyard/internal/leadline"
	"github.com/cosscom/shipyard/internal/leadline/api"
	"github.com/cosscom/shipyard/internal/leadline/capture"
	"github.com/cosscom/shipyard/internal/leadline/helper"
	"github.com/cosscom/shipyard/internal/leadline/store"
)

func start(t *testing.T, dir string) *helper.Helper {
	t.Helper()
	// Port 0 everywhere: the API, and (the zero Ports) the proxies.
	h, err := helper.Start(helper.Options{Dir: dir, Addr: "127.0.0.1:0", Keep: store.Retention{MaxAge: 72 * time.Hour, MaxBytes: 2 << 30}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

func get(t *testing.T, base, path string, into any) {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s %s", path, resp.Status, raw)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("GET %s: %v in %s", path, err, raw)
	}
}

func postProto(t *testing.T, url string, msg proto.Message) {
	t.Helper()
	body, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s: %s %s", url, resp.Status, raw)
	}
}

func str(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}

// span is one statement an app ran and when: what its instrumentation
// reports of it.
type span struct {
	sql        string
	start, end time.Time
}

// The whole helper, as the box daemon will use it: a worktree's dev server
// is told where to send and how to reach its database (the attach helper),
// it runs an N+1 through the proxy and reports the same request over OTLP
// with a log line, and all of it is read back through the API, each
// statement once.
func TestAWorktreesDevServerEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := t.TempDir()
	h := start(t, dir)
	base := h.State().API
	pg := newFakePG(t)

	// ---- What the box daemon does when a worktree's dev server starts.
	wt := leadline.Worktree{Project: "acme", Name: "fix-orders", Branch: "fix/orders-n-plus-one", Box: "devbox"}
	env := map[string]string{}
	for _, v := range leadline.Env(wt, "", leadline.Helper{OTLP: h.State().OTLP[0]}, "") {
		env[v.Name] = v.Value
	}
	if env["OTEL_SERVICE_NAME"] != "acme/fix-orders" || env["OTEL_EXPORTER_OTLP_PROTOCOL"] != "http/protobuf" {
		t.Fatalf("env = %v", env)
	}
	// Its DATABASE_URL names a Postgres on this machine: a proxy is opened
	// in front of it for the worktree, and the URL is pointed at the proxy.
	_, port, _ := net.SplitHostPort(pg.addr())
	databaseURL := dbURL("localhost:" + port)
	upstream, ok := leadline.LocalPostgres(databaseURL)
	if !ok || upstream != "localhost:"+port {
		t.Fatalf("LocalPostgres = %q, %v", upstream, ok)
	}
	body, _ := json.Marshal(map[string]string{"upstream": upstream, "label": wt.Service(),
		"project": wt.Project, "worktree": wt.Name, "branch": wt.Branch, "box": wt.Box})
	resp, err := http.Post(base+"/api/proxies", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var opened struct {
		Created bool         `json:"created"`
		Proxy   capture.View `json:"proxy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&opened); err != nil || resp.StatusCode != 200 || !opened.Created {
		t.Fatalf("POST /api/proxies: %s, %+v, %v", resp.Status, opened, err)
	}
	resp.Body.Close()
	proxied, ok := leadline.ThroughProxy(databaseURL, opened.Proxy.Listen)
	if !ok || !strings.Contains(proxied, "@"+opened.Proxy.Listen+"/acme") || !strings.Contains(proxied, fakePassword) {
		t.Fatalf("the URL through the proxy is wrong (ok %v)", ok)
	}

	// ---- The dev server handles GET /orders: one statement five times,
	// an N+1, through the proxy.
	conn, err := pgx.Connect(ctx, proxied)
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	var ran []span
	const customerSQL = "SELECT name FROM customers WHERE id = $1"
	for id := 100; id < 105; id++ {
		at := time.Now()
		var name string
		if err := conn.QueryRow(ctx, customerSQL, fmt.Sprint(id)).Scan(&name); err != nil || name != fmt.Sprintf("Acme customer %d", id) {
			t.Fatalf("customer %d through the proxy: %q, %v", id, name, err)
		}
		ran = append(ran, span{customerSQL, at, time.Now()})
	}
	ended := time.Now()
	// And, outside any request, a transaction its instrumentation does not
	// report: only the proxy sees it.
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO orders (customer_id, note) VALUES ($1, $2)", "100", "leave at the door"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	conn.Close(ctx)
	// The proxy hands on what it recorded within a fifth of a second; the
	// app's own spans come later, as an SDK's do.
	h.Flush()

	// ---- The same request, as the app's OpenTelemetry SDK reports it.
	traceID, rootID := bytes.Repeat([]byte{0xa1}, 16), bytes.Repeat([]byte{0xb1}, 8)
	resource := &resourcepb.Resource{Attributes: []*commonpb.KeyValue{str("service.name", env["OTEL_SERVICE_NAME"])}}
	for _, pair := range strings.Split(env["OTEL_RESOURCE_ATTRIBUTES"], ",") {
		key, value, _ := strings.Cut(pair, "=")
		value, _ = url.PathUnescape(value)
		resource.Attributes = append(resource.Attributes, str(key, value))
	}
	spans := []*tracepb.Span{{
		TraceId: traceID, SpanId: rootID, Name: "GET /orders", Kind: tracepb.Span_SPAN_KIND_SERVER,
		StartTimeUnixNano: uint64(began.Add(-time.Millisecond).UnixNano()), EndTimeUnixNano: uint64(ended.Add(time.Millisecond).UnixNano()),
		Attributes: []*commonpb.KeyValue{str("http.request.method", "GET"), str("http.route", "/orders")},
	}}
	for i, s := range ran {
		spans = append(spans, &tracepb.Span{
			TraceId: traceID, SpanId: []byte{0xc0, 0, 0, 0, 0, 0, 0, byte(i + 1)}, ParentSpanId: rootID,
			Name: "pg.query", Kind: tracepb.Span_SPAN_KIND_CLIENT,
			StartTimeUnixNano: uint64(s.start.UnixNano()), EndTimeUnixNano: uint64(s.end.UnixNano()),
			Attributes: []*commonpb.KeyValue{str("db.system.name", "postgresql"), str("db.namespace", "acme"), str("db.query.text", s.sql)},
		})
	}
	otlp := env["OTEL_EXPORTER_OTLP_ENDPOINT"]
	postProto(t, otlp+"/v1/traces", &tracepb.TracesData{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource: resource, ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}},
	}}})
	postProto(t, otlp+"/v1/logs", &logspb.LogsData{ResourceLogs: []*logspb.ResourceLogs{{
		Resource: resource, ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{{
			TimeUnixNano: uint64(ended.UnixNano()), SeverityNumber: logspb.SeverityNumber_SEVERITY_NUMBER_WARN,
			Body:    &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "listed 5 orders, one query each"}},
			TraceId: traceID, SpanId: rootID,
		}}}},
	}}})

	// ---- Read back through the API.
	source := "?source=" + url.QueryEscape("acme/fix-orders")
	trace := hex.EncodeToString(traceID)

	var health struct {
		OK   bool   `json:"ok"`
		Name string `json:"name"`
	}
	get(t, base, "/api/health", &health)
	if !health.OK || health.Name != "berth-leadline" {
		t.Errorf("health = %+v", health)
	}

	// The source: one worktree, fed both ways, with what it said it is.
	var sources api.Sources
	get(t, base, "/api/sources", &sources)
	if len(sources.Sources) != 1 {
		t.Fatalf("sources = %+v", sources.Sources)
	}
	src := sources.Sources[0]
	if src.Name != "acme/fix-orders" || src.Project != "acme" || src.Worktree != "fix-orders" || src.Branch != "fix/orders-n-plus-one" ||
		src.Box != "devbox" || !slices.Equal(src.Via, []string{"otlp", "postgres-proxy"}) || !slices.Equal(src.ProxyIDs, []string{opened.Proxy.ID}) ||
		src.Spans != 15 || src.Logs != 1 {
		t.Errorf("the source = %+v", src)
	}
	if len(sources.Proxies) != 1 || sources.Proxies[0].Statements != 8 || sources.Proxies[0].ConnectionsTotal != 1 ||
		sources.Proxies[0].WindowStatements != 8 || sources.Proxies[0].UnreadConnections != 0 {
		t.Errorf("the proxy = %+v, want the 8 statements of one connection", sources.Proxies)
	}
	if len(sources.Services) != 1 || len(sources.Services[0].Through) != 1 || sources.Services[0].Through[0].ProxyID != opened.Proxy.ID {
		t.Errorf("services = %+v", sources.Services)
	}

	// The queries, by shape. The app's five spans and the proxy's five are
	// five calls, not ten: an N+1 of five, with the proxy's row counts.
	var queries []store.QueryRow
	get(t, base, "/api/queries"+source, &queries)
	by := map[string]store.QueryRow{}
	for _, q := range queries {
		by[q.Fingerprint] = q
	}
	customers := by["SELECT name FROM customers WHERE id = ?"]
	if len(queries) != 2 || customers.Calls != 5 || customers.Traces != 1 || customers.MaxPerTrace != 5 || !customers.NPlusOne ||
		customers.AvgMs <= 0 || customers.P95Ms < customers.P50Ms || customers.TotalMs < customers.MaxMs || customers.DBSystem != "postgresql" {
		t.Errorf("the customers query = %+v, of %d shapes; want 5 calls in one trace, an N+1", customers, len(queries))
	}
	insert := by["INSERT INTO orders (customer_id, note) VALUES (?, ?)"]
	if insert.Calls != 1 || insert.NPlusOne {
		t.Errorf("the insert = %+v", insert)
	}
	var control []store.QueryRow
	get(t, base, "/api/queries"+source+"&control=1", &control)
	if len(control) != 2 {
		t.Errorf("control statements = %+v, want begin and commit", control)
	}

	var detail store.QueryDetail
	get(t, base, "/api/queries/detail"+source+"&db_system=postgresql&fingerprint="+url.QueryEscape(customers.Fingerprint), &detail)
	if detail.Stats == nil || detail.Stats.Calls != 5 || !detail.Stats.NPlusOne ||
		len(detail.NPlusOne) != 1 || detail.NPlusOne[0].TraceID != trace || detail.NPlusOne[0].Name != "GET /orders" || detail.NPlusOne[0].Calls != 5 ||
		len(detail.Endpoints) != 1 || detail.Endpoints[0].Name != "GET /orders" || detail.Endpoints[0].MaxPerRequest != 5 || !detail.Endpoints[0].NPlusOne {
		t.Errorf("the query's detail: stats %+v, the requests that show the N+1 %+v, endpoints %+v", detail.Stats, detail.NPlusOne, detail.Endpoints)
	}

	// The traces: the request, and the transaction only the proxy saw.
	var traces []store.TraceRow
	get(t, base, "/api/traces"+source+"&limit=10", &traces)
	if len(traces) != 2 {
		t.Fatalf("traces = %+v, want the request and the transaction", traces)
	}
	var request, transaction store.TraceRow
	for _, tr := range traces {
		if tr.TraceID == trace {
			request = tr
		} else {
			transaction = tr
		}
	}
	if request.Name != "GET /orders" || request.Service != "acme/fix-orders" || request.Spans != 11 || request.Queries != 5 || request.DBMs <= 0 || request.Cursor == "" {
		t.Errorf("the request's trace = %+v, want 11 spans and 5 queries", request)
	}
	if transaction.Name != "transaction" || transaction.Spans != 4 || transaction.Queries != 1 || transaction.Service != "acme/fix-orders" {
		t.Errorf("the transaction's trace = %+v, want its span, begin, the insert and commit", transaction)
	}

	// The request opened: a tree, with each statement the proxy recorded
	// under the app's span for it, its SQL, and the log written during it.
	var open struct {
		Spans []store.SpanRow   `json:"spans"`
		Tree  []store.TraceNode `json:"tree"`
		Logs  []store.LogRow    `json:"logs"`
	}
	get(t, base, "/api/traces/"+trace, &open)
	if len(open.Tree) != 1 || open.Tree[0].SpanID != hex.EncodeToString(rootID) || len(open.Tree[0].Children) != 5 {
		t.Fatalf("tree = %+v, want the request with its five queries", open.Tree)
	}
	spanByID := map[string]store.SpanRow{}
	for _, sp := range open.Spans {
		spanByID[sp.SpanID] = sp
	}
	for i, node := range open.Tree[0].Children {
		app := spanByID[node.SpanID]
		if app.Name != "pg.query" || app.DBRole != "duplicate" || app.Depth != 1 || len(node.Children) != 1 {
			t.Fatalf("query %d: the app's span = %+v with %d under it", i, app, len(node.Children))
		}
		wire := spanByID[node.Children[0].SpanID]
		var attrs struct {
			Rows   int    `json:"db.rows"`
			Params []any  `json:"db.query.parameters"`
			Source string `json:"leadline.source"`
			User   string `json:"db.user"`
		}
		if err := json.Unmarshal(wire.Attrs, &attrs); err != nil {
			t.Fatal(err)
		}
		if wire.Name != "SELECT" || wire.DBRole != "query" || wire.Depth != 2 || wire.DBStatement != customerSQL ||
			wire.Service != "acme/fix-orders" || wire.DBServer != "127.0.0.1:"+port ||
			attrs.Rows != 1 || attrs.Source != "pgproxy" || attrs.User != "app" || len(attrs.Params) != 1 || attrs.Params[0] != fmt.Sprint(100+i) {
			t.Errorf("query %d: the proxy's span = %+v, attrs %s", i, wire, wire.Attrs)
		}
	}
	if !spanByID[hex.EncodeToString(rootID)].Entry || spanByID[hex.EncodeToString(rootID)].HTTPRoute != "/orders" {
		t.Errorf("the request's own span = %+v", spanByID[hex.EncodeToString(rootID)])
	}
	if len(open.Logs) != 1 || open.Logs[0].Body != "listed 5 orders, one query each" || open.Logs[0].Severity != "warn" {
		t.Errorf("the trace's logs = %+v", open.Logs)
	}

	// The logs, searched.
	var logs []store.LogRow
	get(t, base, "/api/logs"+source+"&q=ORDERS&min_severity=13", &logs)
	if len(logs) != 1 || logs[0].TraceID != trace || logs[0].Cursor == "" {
		t.Errorf("logs = %+v", logs)
	}
	get(t, base, "/api/logs"+source+"&q=nothing+like+this", &logs)
	if len(logs) != 0 {
		t.Errorf("a search for nothing found %+v", logs)
	}

	// The route: one request, five queries a request, some of its time in
	// the database.
	var eps store.Endpoints
	get(t, base, "/api/endpoints"+source+"&spark=6", &eps)
	if len(eps.Rows) != 1 || eps.Total != 1 || eps.Rows[0].Name != "GET /orders" || eps.Rows[0].Requests != 1 || eps.Rows[0].Queries != 5 ||
		eps.Rows[0].DBMs <= 0 || eps.Rows[0].DBMs > eps.Rows[0].AvgMs || eps.Rows[0].Errors != 0 || len(eps.Rows[0].Spark) != 6 {
		t.Errorf("endpoints = %+v", eps)
	}
	var route store.EndpointDetail
	get(t, base, "/api/endpoints/detail"+source+"&endpoint_service="+url.QueryEscape("acme/fix-orders")+"&name="+url.QueryEscape("GET /orders"), &route)
	if route.Stats == nil || route.Stats.Requests != 1 || len(route.Queries) != 1 || !route.Queries[0].NPlusOne || route.Queries[0].PerRequest != 5 ||
		len(route.Slowest) != 1 || route.Slowest[0].TraceID != trace {
		t.Errorf("the route's detail = %+v, queries %+v", route.Stats, route.Queries)
	}
	var storage store.Storage
	get(t, base, "/api/storage", &storage)
	if storage.SizeBytes == 0 || storage.RetainMs != float64((72*time.Hour).Milliseconds()) || storage.MaxBytes != 2<<30 || storage.OldestMs == 0 {
		t.Errorf("storage = %+v", storage)
	}

	// ---- What is on disk: where it listens, while it runs; and never the
	// database's password, which went through the proxy on its way.
	var state helper.State
	raw, err := os.ReadFile(filepath.Join(dir, helper.StateFile))
	if err != nil || json.Unmarshal(raw, &state) != nil || state.API != base || state.PID != os.Getpid() {
		t.Errorf("%s = %s (%v)", helper.StateFile, raw, err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, helper.StateFile)); !os.IsNotExist(err) {
		t.Errorf("%s is still there after the helper stopped (%v)", helper.StateFile, err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		content, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(content, []byte(fakePassword)) {
			t.Errorf("%s holds the database's password", e.Name())
		}
		if fi, _ := e.Info(); fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s can be read by others: %v", e.Name(), fi.Mode().Perm())
		}
	}
	if len(entries) != 3 {
		t.Errorf("the data directory holds %d files after a stop, want the database, proxies.json and %s", len(entries), helper.ListenFile)
	}

	// ---- Started again in the same directory, it has what it had, and
	// the worktree's proxy is on the port the app was pointed at.
	h2 := start(t, dir)
	get(t, h2.State().API, "/api/sources", &sources)
	if len(sources.Sources) != 1 || sources.Sources[0].Spans != 15 || len(sources.Proxies) != 1 ||
		sources.Proxies[0].Listen != opened.Proxy.Listen || sources.Proxies[0].State != "listening" {
		t.Errorf("after a restart: %+v", sources)
	}
	conn, err = pgx.Connect(ctx, proxied)
	if err != nil {
		t.Fatalf("the app's URL no longer reaches its database after a restart: %v", err)
	}
	var one string
	if err := conn.QueryRow(ctx, customerSQL, "7").Scan(&one); err != nil {
		t.Fatal(err)
	}
	conn.Close(ctx)
	h2.Flush()
	get(t, h2.State().API, "/api/queries"+source, &queries)
	for _, q := range queries {
		if strings.Contains(q.Fingerprint, "customers") && q.Calls != 6 {
			t.Errorf("after one more call the customers query has %d, want 6", q.Calls)
		}
	}
}

// The helper listens on this machine only, and says so when asked for more.
func TestItRefusesToListenBeyondThisMachine(t *testing.T) {
	for _, o := range []helper.Options{
		{Dir: t.TempDir(), Addr: "0.0.0.0:0"},
		{Dir: t.TempDir(), Addr: ":0"},
		{Dir: t.TempDir(), Addr: "192.168.1.20:4325"},
		{Dir: t.TempDir(), Addr: "127.0.0.1:0", OTLPAddr: "0.0.0.0:0"},
		{Dir: t.TempDir(), Addr: "nonsense"},
		{Dir: t.TempDir()},
		{Addr: "127.0.0.1:0"},
	} {
		if h, err := helper.Start(o); err == nil {
			h.Close()
			t.Errorf("started with %+v", o)
		}
	}
	// A second address for OTLP alone takes telemetry and answers nothing
	// else.
	h, err := helper.Start(helper.Options{Dir: t.TempDir(), Addr: "localhost:0", OTLPAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if len(h.State().OTLP) != 2 {
		t.Fatalf("otlp = %v", h.State().OTLP)
	}
	postProto(t, h.State().OTLP[1]+"/v1/traces", &tracepb.TracesData{})
	resp, err := http.Get(h.State().OTLP[1] + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("the OTLP address answered the API: %s", resp.Status)
	}
}

// A second helper that cannot have the address fails, and leaves the first
// one's word of where it listens.
func TestASecondHelperOnTheSameAddressFails(t *testing.T) {
	dir := t.TempDir()
	h := start(t, dir)
	addr := strings.TrimPrefix(h.State().API, "http://")
	if second, err := helper.Start(helper.Options{Dir: dir, Addr: addr}); err == nil {
		second.Close()
		t.Fatal("a second helper started on an address that is taken")
	}
	var state helper.State
	raw, err := os.ReadFile(filepath.Join(dir, helper.StateFile))
	if err != nil || json.Unmarshal(raw, &state) != nil || state.API != h.State().API {
		t.Errorf("%s after the second one failed = %s (%v)", helper.StateFile, raw, err)
	}
	var health api.Health
	get(t, h.State().API, "/api/health", &health)
	if !health.OK {
		t.Errorf("the first helper no longer answers: %+v", health)
	}
}

// A dev server is told where the helper is once, when it starts. So a
// helper started on "any port" comes back on the port it had: what was
// pointed at it still reaches it after it restarts.
func TestAHelperOnAnyPortComesBackOnTheOneItHad(t *testing.T) {
	dir := t.TempDir()
	opts := helper.Options{Dir: dir, Addr: "127.0.0.1:0", OTLPAddr: "127.0.0.1:0"}
	first, err := helper.Start(opts)
	if err != nil {
		t.Fatal(err)
	}
	was := first.State()
	if len(was.OTLP) != 2 || was.OTLP[1] == was.API {
		t.Fatalf("state = %+v", was)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := helper.Start(opts)
	if err != nil {
		t.Fatal(err)
	}
	if now := second.State(); now.API != was.API || now.OTLP[1] != was.OTLP[1] {
		t.Errorf("after a restart the helper listens on %s and %s, not %s and %s as before", now.API, now.OTLP[1], was.API, was.OTLP[1])
	}
	second.Close()

	// Something else has the port by the next start: any other will do.
	taken, err := net.Listen("tcp", strings.TrimPrefix(was.API, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	third, err := helper.Start(opts)
	if err != nil {
		t.Fatalf("the helper did not start with its old port taken: %v", err)
	}
	defer third.Close()
	if now := third.State(); now.API == was.API || now.OTLP[1] != was.OTLP[1] {
		t.Errorf("with its API port taken the helper listens on %s and %s", now.API, now.OTLP[1])
	}
	var health api.Health
	get(t, third.State().API, "/api/health", &health)
	if !health.OK {
		t.Errorf("the helper does not answer on its new port: %+v", health)
	}
}
