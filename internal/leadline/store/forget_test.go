package store_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/leadline/store"
)

// These tests are about two things a worktree brought: taking one source's
// data out of the store without touching another's, and a query run outside
// any request not being counted as a request.

// held counts what the store has for a service, straight from the tables.
func held(t *testing.T, st *store.Store, service string) (spans, logs int64) {
	t.Helper()
	st.Flush()
	err := st.DB().QueryRow(`
SELECT (SELECT count(*) FROM spans WHERE service = ?1), (SELECT count(*) FROM logs WHERE service = ?1)`, service).
		Scan(&spans, &logs)
	if err != nil {
		t.Fatal(err)
	}
	return
}

// fill gives a service n requests, each a server span with a query under
// it and a log line.
func fill(t *testing.T, st *store.Store, service string, salt, n int) {
	t.Helper()
	at := time.Now().Add(-time.Minute).UnixNano()
	var spans []store.Span
	var logs []store.Log
	for i := 0; i < n; i++ {
		trace := fmt.Sprintf("%016x%016x", salt, i+1)
		root := fmt.Sprintf("%08x%08x", salt, i*2+1)
		start := at + int64(i)*int64(time.Millisecond)
		spans = append(spans,
			store.Span{TraceID: trace, SpanID: root, Service: service, Name: "GET /orders", Kind: "server",
				StartNs: start, DurationNs: ms(5), Status: "unset", Attrs: "{}"},
			store.Span{TraceID: trace, SpanID: fmt.Sprintf("%08x%08x", salt, i*2+2), ParentID: root, Service: service, Name: "pg.query", Kind: "client",
				StartNs: start + ms(1), DurationNs: ms(2), Status: "unset", DBSystem: "postgresql",
				DBStatement: "SELECT id FROM orders", DBFingerprint: "SELECT id FROM orders", Attrs: "{}"})
		logs = append(logs, store.Log{TsNs: start, Service: service, Severity: "INFO", SeverityNum: 9, Body: "order listed", TraceID: trace, Attrs: "{}"})
	}
	if err := st.AddSpans(spans); err != nil {
		t.Fatal(err)
	}
	if err := st.AddLogs(logs); err != nil {
		t.Fatal(err)
	}
	st.Flush()
}

// Forgetting a worktree takes its rows from every table, and those of the
// commands run in it, and nothing of a source whose name only looks alike.
func TestForgetTakesOneServiceAndNoOther(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()

	const gone = "acme/fix-orders"
	keep := []string{"acme/fix-orders-2", "acme/fix", "acme", "acme/add-refunds", "acme/add-refunds:worker"}
	fill(t, st, gone, 1, 40)
	fill(t, st, gone+":worker", 2, 10)
	for i, name := range keep {
		fill(t, st, name, 10+i, 20)
	}
	if err := st.NoteWorktrees([]store.Worktree{
		{Service: gone, Project: "acme", Worktree: "fix-orders", Branch: "fix-orders", Box: "devbox"},
		{Service: "acme/add-refunds", Project: "acme", Worktree: "add-refunds", Branch: "add-refunds", Box: "devbox"},
	}); err != nil {
		t.Fatal(err)
	}

	f, err := st.Forget(ctx, gone, gone+":")
	if err != nil {
		t.Fatal(err)
	}
	if f.Spans != 100 || f.Logs != 50 {
		t.Errorf("forgot %+v; want 100 spans and 50 logs", f)
	}
	for _, name := range []string{gone, gone + ":worker"} {
		if s, l := held(t, st, name); s+l != 0 {
			t.Errorf("%s still has %d spans and %d logs", name, s, l)
		}
	}
	for _, name := range keep {
		if s, l := held(t, st, name); s != 40 || l != 20 {
			t.Errorf("%s has %d spans and %d logs; want all of its 40 and 20", name, s, l)
		}
	}
	// What the app asks sees the same.
	sum, err := st.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sum.Services {
		if s == gone || s == gone+":worker" {
			t.Errorf("the summary still lists %s: %v", s, sum.Services)
		}
	}
	// What was known of its worktree went with it, and the other's stayed.
	known := st.Worktrees()
	if _, ok := known[gone]; ok || known["acme/add-refunds"].Worktree != "add-refunds" {
		t.Errorf("worktrees after the forget = %+v", known)
	}
	// Again, and for a name the store never held: nothing, no error.
	for _, name := range []string{gone, "acme/never-seen"} {
		if f, err := st.Forget(ctx, name, name+":"); err != nil || f != (store.Forgotten{}) {
			t.Errorf("forgetting %s again: %+v, %v", name, f, err)
		}
	}
	// The name is free to be used afresh.
	fill(t, st, gone, 3, 5)
	if s, l := held(t, st, gone); s != 10 || l != 5 {
		t.Errorf("after starting again %s has %d spans and %d logs", gone, s, l)
	}
}

// A delete of one source's rows runs beside ingest and reads: nothing that
// arrives meanwhile is lost, held up or deleted with it.
func TestForgetWhileIngestContinues(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "leadline.db"), store.Options{FlushEvery: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	const gone, stays = "acme/fix-orders", "acme/add-refunds"
	fill(t, st, gone, 1, 3000)
	fill(t, st, stays, 2, 500)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var sent int
	var slowest time.Duration
	wg.Add(2)
	go func() { // an app that keeps sending
		defer wg.Done()
		at := time.Now().UnixNano()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			began := time.Now()
			err := st.AddSpans([]store.Span{{TraceID: fmt.Sprintf("%032x", 0xabc000+i), SpanID: fmt.Sprintf("%016x", 0xabc000+i), Service: stays,
				Name: "GET /refunds", Kind: "server", StartNs: at + int64(i), DurationNs: ms(1), Status: "unset", Attrs: "{}"}})
			if err != nil {
				t.Error(err)
				return
			}
			if d := time.Since(began); d > slowest {
				slowest = d
			}
			sent++
			time.Sleep(200 * time.Microsecond)
		}
	}()
	go func() { // the app's screens, asking
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := st.Summary(ctx); err != nil {
				t.Error(err)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	time.Sleep(20 * time.Millisecond)
	f, err := st.Forget(ctx, gone, gone+":")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()
	st.Flush()

	if f.Spans != 6000 || f.Logs != 3000 {
		t.Errorf("forgot %+v; want the 6000 spans and 3000 logs of %s", f, gone)
	}
	if s, l := held(t, st, gone); s+l != 0 {
		t.Errorf("%s still has %d spans and %d logs", gone, s, l)
	}
	if s, l := held(t, st, stays); s != int64(1000+sent) || l != 500 {
		t.Errorf("%s has %d spans and %d logs; want %d and 500: what arrived during the delete must all be there", stays, s, l, 1000+sent)
	}
	if sent == 0 {
		t.Error("nothing was sent while the delete ran")
	}
	// An Add is an append to a slice. It must not have waited on the delete.
	if slowest > 2*time.Second {
		t.Errorf("an export waited %v while a source was being forgotten", slowest)
	}
}

// A statement an app runs outside any request (a seed, a migration, a
// query at startup) is a root span with a database system. It is a query
// and it has a trace; it is not an endpoint.
func TestARootDatabaseSpanIsNoRequest(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	at := linkEpoch
	const seedTrace, reqTrace = "000000000000000000000000000000a1", "000000000000000000000000000000a2"
	seed := store.Span{TraceID: seedTrace, SpanID: "00000000000000a1", Service: "acme-api", Name: "pg.query:CREATE acme", Kind: "client",
		StartNs: at, DurationNs: ms(4), Status: "unset", DBSystem: "postgresql",
		DBStatement: "CREATE TABLE IF NOT EXISTS orders (id serial primary key)", DBFingerprint: "CREATE TABLE IF NOT EXISTS orders (id serial primary key)", DBName: "acme", Attrs: "{}"}
	// An internal root span is still a request (a job), and so is a server
	// span; a query inside one counts for it.
	job := store.Span{TraceID: "000000000000000000000000000000a3", SpanID: "00000000000000a3", Service: "acme-api", Name: "job send-receipt", Kind: "internal",
		StartNs: at + ms(10), DurationNs: ms(8), Status: "unset", Attrs: "{}"}
	req := store.Span{TraceID: reqTrace, SpanID: "00000000000000a4", Service: "acme-api", Name: "GET /orders", Kind: "server",
		StartNs: at + ms(20), DurationNs: ms(9), Status: "unset", HTTPRoute: "/orders", Attrs: "{}"}
	inReq := store.Span{TraceID: reqTrace, SpanID: "00000000000000a5", ParentID: req.SpanID, Service: "acme-api", Name: "pg.query:SELECT acme", Kind: "client",
		StartNs: at + ms(21), DurationNs: ms(2), Status: "unset", DBSystem: "postgresql",
		DBStatement: "SELECT id FROM orders", DBFingerprint: "SELECT id FROM orders", DBName: "acme", Attrs: "{}"}
	add(t, st, []store.Span{seed, job, inReq, req})

	eps, err := st.Endpoints(ctx, window(), store.EndpointQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]int64{}
	for _, e := range eps.Rows {
		names[e.Name] = e.Requests
	}
	if len(names) != 2 || names["GET /orders"] != 1 || names["job send-receipt"] != 1 {
		t.Errorf("endpoints = %v; want the request and the job, and not the seed statement", names)
	}

	// It is still a query...
	queries, err := st.Queries(ctx, store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, q := range queries {
		found = found || q.Fingerprint == seed.DBFingerprint
	}
	if !found {
		t.Errorf("the seed statement is not among the queries: %+v", queries)
	}
	// ...and still a trace, with its span, which says it is no entry.
	spans, err := st.TraceSpans(ctx, seedTrace)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 || spans[0].Entry || spans[0].DBRole != "query" {
		t.Errorf("the seed's trace: %+v", spans)
	}
	traces, err := st.Traces(ctx, store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, tr := range traces {
		listed = listed || tr.TraceID == seedTrace
	}
	if !listed {
		t.Errorf("the seed's trace is not in the list of traces: %+v", traces)
	}
	// The query inside the request is unchanged: no entry either, and the
	// request around it is one.
	spans, err = st.TraceSpans(ctx, reqTrace)
	if err != nil {
		t.Fatal(err)
	}
	for _, sp := range spans {
		if sp.Entry != (sp.Name == "GET /orders") {
			t.Errorf("%s: entry = %v", sp.Name, sp.Entry)
		}
	}
}
