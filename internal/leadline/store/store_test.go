package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/leadline/store"
)

// These tests are about what is this store's own and not Leadline's: the
// file it keeps, how its lists are paged, the source filter, the trace as a
// tree, and how old rows leave.

// requestOf is one request of a service: a server span, n queries under it
// and a log line, starting at at.
func requestOf(service, name string, i int, at int64, n int) ([]store.Span, store.Log) {
	trace := fmt.Sprintf("%016x%016x", hash(service), i+1)
	root := fmt.Sprintf("%016x", i*100+1)
	spans := []store.Span{{TraceID: trace, SpanID: root, Service: service, Name: name, Kind: "server",
		StartNs: at, DurationNs: ms(5) + int64(n)*ms(2), Status: "unset", Attrs: "{}"}}
	for k := 0; k < n; k++ {
		spans = append(spans, store.Span{TraceID: trace, SpanID: fmt.Sprintf("%016x", i*100+2+k), ParentID: root, Service: service,
			Name: "pg.query", Kind: "client", StartNs: at + ms(1) + int64(k)*ms(2), DurationNs: ms(1), Status: "unset",
			DBSystem: "postgresql", DBStatement: fmt.Sprintf("SELECT name FROM customers WHERE id = %d", k),
			DBFingerprint: "SELECT name FROM customers WHERE id = ?", Attrs: "{}"})
	}
	return spans, store.Log{TsNs: at + ms(2), Service: service, Severity: "info", SeverityNum: 9,
		Body: "handled " + name, TraceID: trace, SpanID: root, Attrs: "{}"}
}

func hash(s string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h = (h ^ uint64(s[i])) * 1099511628211
	}
	return h
}

func TestTheFileIsPrivateAndKeepsWhatItWasGiven(t *testing.T) {
	ctx := context.Background()
	// A folder name a URI would read as something else.
	dir := filepath.Join(t.TempDir(), "acme 50% #1?")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "leadline.db")
	st, err := store.Open(path, store.Options{ReadWait: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	spans, log := requestOf("acme/fix-orders", "GET /orders", 1, linkEpoch, 3)
	add(t, st, spans)
	if err := st.AddLogs([]store.Log{log}); err != nil {
		t.Fatal(err)
	}
	if err := st.NoteWorktrees([]store.Worktree{{Service: "acme/fix-orders", Project: "acme", Worktree: "fix-orders", Branch: "fix/orders", Box: "devbox"}}); err != nil {
		t.Fatal(err)
	}
	var vacuum int
	var journal string
	if err := st.DB().QueryRow(`PRAGMA auto_vacuum`).Scan(&vacuum); err != nil || vacuum != 2 {
		t.Errorf("auto_vacuum = %d (%v), want 2: a prune could not give space back", vacuum, err)
	}
	if err := st.DB().QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil || journal != "wal" {
		t.Errorf("journal_mode = %q (%v), want wal", journal, err)
	}
	// Rows added and not yet flushed are written by Close.
	more, _ := requestOf("acme/fix-orders", "GET /orders", 2, linkEpoch+ms(50), 1)
	if err := st.AddSpans(more); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSpans(more); err == nil {
		t.Error("a closed store took rows")
	}

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("the database's mode is %v (%v), want 0600: it holds SQL and logs", fi.Mode().Perm(), err)
		}
	}
	// Closed, the store is the one file.
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("after Close the folder holds %d files, want the database alone", len(entries))
	}

	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sum, err := st.Summary(ctx)
	if err != nil || sum.Spans != 6 || sum.Logs != 1 || sum.Traces != 2 || !slices.Equal(sum.Services, []string{"acme/fix-orders"}) {
		t.Errorf("after opening it again: %+v, %v; want 6 spans, 1 log, 2 traces", sum, err)
	}
	if w := st.Worktrees()["acme/fix-orders"]; w.Branch != "fix/orders" || w.Box != "devbox" {
		t.Errorf("the worktree was not remembered: %+v", w)
	}
}

func TestAFileFromANewerHelperIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leadline.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if st, err := store.Open(path); err == nil {
		st.Close()
		t.Error("a file of a format this helper does not know was opened")
	} else if !strings.Contains(err.Error(), "newer") {
		t.Errorf("err = %v, want it to say the file is from a newer helper", err)
	}
	if _, err := store.Open(""); err == nil {
		t.Error("a store with no file was opened")
	}
}

// Traces come newest first, a page at a time, each page starting where the
// last one stopped: none twice and none missed, over hours of them and
// among traces that began in the same nanosecond.
func TestTracesPageByCursor(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	const n = 620
	base := time.Now().Add(-6 * time.Hour).UnixNano()
	var all []store.Span
	want := 0
	for i := 0; i < n; i++ {
		// Thirty seconds apart, and every tenth pair at the very same time.
		at := base + int64(i/2*2)*int64(30*time.Second)
		if i%20 > 1 {
			at = base + int64(i)*int64(30*time.Second)
		}
		service := "acme/fix-orders"
		if i%4 == 0 {
			service = "acme/add-refunds"
		}
		spans, _ := requestOf(service, fmt.Sprintf("GET /orders/%d", i%7), i, at, 2)
		if i%50 == 0 {
			spans[2].Status = "error"
		}
		all = append(all, spans...)
		want++
	}
	// Out of order, as exports arrive.
	slices.Reverse(all)
	for len(all) > 0 {
		k := min(len(all), 400)
		add(t, st, all[:k])
		all = all[k:]
	}

	page := func(f store.Filter) []store.TraceRow {
		t.Helper()
		var got []store.TraceRow
		seen := map[string]bool{}
		for pages := 0; ; pages++ {
			rows, err := st.Traces(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if seen[r.TraceID] {
					t.Fatalf("trace %s came twice", r.TraceID)
				}
				seen[r.TraceID] = true
				if len(got) > 0 {
					prev := got[len(got)-1]
					if r.StartMs > prev.StartMs || (r.StartMs == prev.StartMs && r.TraceID > prev.TraceID) {
						t.Fatalf("out of order: %s at %v after %s at %v", r.TraceID, r.StartMs, prev.TraceID, prev.StartMs)
					}
				}
				got = append(got, r)
			}
			if len(rows) < f.Limit {
				return got
			}
			if pages > 100 {
				t.Fatal("the pages never end")
			}
			f.Before = rows[len(rows)-1].Cursor
		}
	}

	if got := page(store.Filter{Limit: 90}); len(got) != want {
		t.Errorf("paging found %d traces, want %d", len(got), want)
	} else if got[0].Spans != 3 || got[0].Queries != 2 || got[0].DurationMs != 9 {
		t.Errorf("the newest trace = %+v, want 3 spans, 2 queries and 9ms", got[0])
	}
	if got := page(store.Filter{Limit: 90, Service: "acme/add-refunds"}); len(got) != n/4 {
		t.Errorf("acme/add-refunds has %d traces, want %d", len(got), n/4)
	}
	if got := page(store.Filter{Limit: 25, ErrorsOnly: true}); len(got) != 13 {
		t.Errorf("%d traces with an error, want 13", len(got))
	}
	if got := page(store.Filter{Limit: 40, Search: "orders/3"}); len(got) == 0 || len(got) > n/7+1 {
		t.Errorf("searching the name found %d", len(got))
	} else {
		for _, r := range got {
			if r.Name != "GET /orders/3" {
				t.Errorf("search found %q", r.Name)
			}
		}
	}
	// Since: only the last hour of them.
	sinceMs := float64(base+int64(n-120)*int64(30*time.Second))/1e6 - 1
	if got := page(store.Filter{Limit: 50, SinceMs: sinceMs}); len(got) != 120 {
		t.Errorf("%d traces since the last hour began, want 120", len(got))
	}
	if got := page(store.Filter{Limit: 50, MinDurationMs: 100}); len(got) != 0 {
		t.Errorf("%d traces took 100ms, want none", len(got))
	}
	if _, err := st.Traces(ctx, store.Filter{Before: "yesterday"}); !errors.Is(err, store.ErrBadCursor) {
		t.Errorf("a cursor that is none: %v", err)
	}
}

// Logs page the same way, and are found by what they say, how bad they are
// and who wrote them.
func TestLogsPageByCursor(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	base := time.Now().Add(-2 * time.Hour).UnixNano()
	var logs []store.Log
	for i := 0; i < 1100; i++ {
		// Three records to each instant.
		l := store.Log{TsNs: base + int64(i/3)*int64(time.Second), Service: "acme/fix-orders", Severity: "info", SeverityNum: 9,
			Body: fmt.Sprintf("listed %d Orders", i), Attrs: "{}"}
		switch {
		case i%100 == 7:
			l.Severity, l.SeverityNum, l.Body = "error", 17, "checkout failed: card declined"
		case i%5 == 0:
			l.Service = "acme/fix-orders:worker"
		case i%5 == 1:
			l.Service = "acme/fix-orders-2"
		}
		logs = append(logs, l)
	}
	if err := st.AddLogs(logs); err != nil {
		t.Fatal(err)
	}

	page := func(f store.Filter) []store.LogRow {
		t.Helper()
		var got []store.LogRow
		seen := map[string]bool{}
		for pages := 0; ; pages++ {
			rows, err := st.Logs(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if seen[r.Cursor] {
					t.Fatalf("log %s came twice", r.Cursor)
				}
				seen[r.Cursor] = true
				if len(got) > 0 && r.TsMs > got[len(got)-1].TsMs {
					t.Fatalf("out of order: %v after %v", r.TsMs, got[len(got)-1].TsMs)
				}
				got = append(got, r)
			}
			if len(rows) < f.Limit {
				return got
			}
			if pages > 100 {
				t.Fatal("the pages never end")
			}
			f.Before = rows[len(rows)-1].Cursor
		}
	}
	all := page(store.Filter{Limit: 250})
	if len(all) != 1100 {
		t.Fatalf("paging found %d logs, want 1100", len(all))
	}
	// Newest first, and among the records of one instant the last stored
	// first: read backwards, the list is the order they were written in.
	if all[0].Body != "listed 1099 Orders" || all[1].Body != "listed 1098 Orders" || all[len(all)-1].Body != "listed 0 Orders" {
		t.Errorf("the list runs %q, %q ... %q", all[0].Body, all[1].Body, all[len(all)-1].Body)
	}
	if got := page(store.Filter{Limit: 4, MinSeverity: 17}); len(got) != 11 {
		t.Errorf("%d errors, want 11", len(got))
	}
	// A search takes no notice of case.
	if got := page(store.Filter{Limit: 100, Search: "CARD declined"}); len(got) != 11 {
		t.Errorf("searching found %d, want 11", len(got))
	}
	if got := page(store.Filter{Limit: 300, Search: "orders", Service: "acme/fix-orders:worker"}); len(got) != 220 {
		t.Errorf("the worker wrote %d, want 220", len(got))
	}
	// The worktree and the command run in it, and not the worktree whose
	// name only begins the same.
	if got := page(store.Filter{Limit: 300, Source: "acme/fix-orders"}); len(got) != 1100-220 {
		t.Errorf("the source has %d logs, want %d", len(got), 1100-220)
	}
	for _, bad := range []string{"12", "12.", "x.3", "12.0", "12.-4", "12.abc"} {
		if _, err := st.Logs(ctx, store.Filter{Before: bad}); !errors.Is(err, store.ErrBadCursor) {
			t.Errorf("cursor %q: %v", bad, err)
		}
	}
}

// A source is a worktree: the service of its name and the commands run in
// it. Every list and every breakdown can be narrowed to one.
func TestASourceIsAWorktreeAndItsCommands(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	var logs []store.Log
	for i, c := range []struct {
		service string
		queries int
	}{
		{"acme/fix-orders", 6}, {"acme/fix-orders:worker", 2}, {"acme/fix-orders-2", 1}, {"acme/add-refunds", 3}, {"acme", 1},
	} {
		spans, log := requestOf(c.service, "GET /orders", i, linkEpoch+int64(i)*ms(100), c.queries)
		add(t, st, spans)
		logs = append(logs, log)
	}
	if err := st.AddLogs(logs); err != nil {
		t.Fatal(err)
	}
	if store.SourceOf("acme/fix-orders:worker") != "acme/fix-orders" || store.SourceOf("acme") != "acme" {
		t.Error("SourceOf does not cut a service at its colon")
	}

	for _, c := range []struct {
		source           string
		requests, calls  int64
		services, traces int
	}{
		{"acme/fix-orders", 2, 8, 2, 2},
		{"acme/fix-orders-2", 1, 1, 1, 1},
		{"acme", 1, 1, 1, 1},
		{"acme/nothing", 0, 0, 0, 0},
		{"", 5, 13, 5, 5},
	} {
		w := window()
		w.Source = c.source
		f := store.Filter{Source: c.source}
		queries, err := st.Queries(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var calls int64
		for _, q := range queries {
			calls += q.Calls
		}
		traces, err := st.Traces(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		got, err := st.Logs(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		eps, err := st.Endpoints(ctx, w, store.EndpointQuery{Spark: 3})
		if err != nil {
			t.Fatal(err)
		}
		series, err := st.SeriesBetween(ctx, w, 1)
		if err != nil {
			t.Fatal(err)
		}
		heat, err := st.RequestHeatmap(ctx, w, 4)
		if err != nil {
			t.Fatal(err)
		}
		var requests, heated int64
		for _, e := range eps.Rows {
			requests += e.Requests
		}
		for _, n := range heat.N {
			heated += n
		}
		if calls != c.calls || len(traces) != c.traces || len(got) != c.traces || len(eps.Rows) != c.services ||
			requests != c.requests || int64(series.Requests[0]) != c.requests || int64(series.Queries[0]) != c.calls ||
			int64(series.Logs[0]) != c.requests || heated != c.requests {
			t.Errorf("source %q: %d calls, %d traces, %d logs, %d endpoints with %d requests, a series of %v requests and %v queries, a heatmap of %d; want %d calls, %d traces, %d services, %d requests",
				c.source, calls, len(traces), len(got), len(eps.Rows), requests, series.Requests[0], series.Queries[0], heated,
				c.calls, c.traces, c.services, c.requests)
		}
	}
	// A source and a service together: the one command of the worktree.
	w := window()
	w.Source, w.Service = "acme/fix-orders", "acme/fix-orders:worker"
	if eps, err := st.Endpoints(ctx, w, store.EndpointQuery{}); err != nil || len(eps.Rows) != 1 || eps.Rows[0].Queries != 2 {
		t.Errorf("the worker alone: %+v, %v", eps, err)
	}
}

// The query list is by cost, so it pages by offset; and a query one request
// ran five times or more is an N+1, with the requests that show it.
func TestQueriesPageAndFlagAnNPlusOne(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	// Twelve shapes, each costlier than the last, and one N+1.
	var spans []store.Span
	for k := 0; k < 12; k++ {
		spans = append(spans, store.Span{TraceID: fmt.Sprintf("%032x", 0x100+k), SpanID: fmt.Sprintf("%016x", 0x100+k), Service: "acme/fix-orders",
			Name: "pg.query", Kind: "client", StartNs: linkEpoch + int64(k)*ms(10), DurationNs: ms(float64(k + 1)), Status: "unset",
			DBSystem: "postgresql", DBStatement: fmt.Sprintf("SELECT %d FROM t%d", k, k), DBFingerprint: fmt.Sprintf("SELECT ? FROM t%d", k), Attrs: "{}"})
	}
	add(t, st, spans)
	for i, n := range []int{25, 5, 4, 1} {
		r, _ := requestOf("acme/fix-orders", "GET /orders", i, linkEpoch+ms(500)+int64(i)*ms(200), n)
		add(t, st, r)
	}

	var seen []string
	for offset := 0; ; offset += 5 {
		rows, err := st.Queries(ctx, store.Filter{Limit: 5, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if slices.Contains(seen, r.Fingerprint) {
				t.Fatalf("%q came twice", r.Fingerprint)
			}
			seen = append(seen, r.Fingerprint)
			if r.NPlusOne != strings.Contains(r.Fingerprint, "customers") {
				t.Errorf("%q: n_plus_one = %v (at most %d in one trace)", r.Fingerprint, r.NPlusOne, r.MaxPerTrace)
			}
		}
		if len(rows) < 5 {
			break
		}
	}
	// The N+1's 35 calls of 1ms cost the most; then t11 down to t0.
	if len(seen) != 13 || !strings.Contains(seen[0], "customers") || seen[1] != "SELECT ? FROM t11" || seen[12] != "SELECT ? FROM t0" {
		t.Errorf("paged through %v", seen)
	}

	d, err := st.QueryDetail(ctx, window(), store.QueryKey{DBSystem: "postgresql", Fingerprint: "SELECT name FROM customers WHERE id = ?"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if d.Stats == nil || !d.Stats.NPlusOne || d.Stats.MaxPerTrace != 25 || d.Stats.Calls != 35 {
		t.Fatalf("stats = %+v, want an N+1 of 25 in 35 calls", d.Stats)
	}
	// The two requests that ran it five times or more, the worst first; the
	// ones that ran it four times and once are no part of the N+1.
	if len(d.NPlusOne) != 2 || d.NPlusOne[0].Calls != 25 || d.NPlusOne[1].Calls != 5 || d.NPlusOne[0].Name != "GET /orders" {
		t.Errorf("the requests that show it = %+v", d.NPlusOne)
	}
	if len(d.Traces) != 4 {
		t.Errorf("traces = %+v, want all four that ran it", d.Traces)
	}
	if len(d.Endpoints) != 1 || !d.Endpoints[0].NPlusOne || d.Endpoints[0].MaxPerRequest != 25 {
		t.Errorf("endpoints = %+v", d.Endpoints)
	}
	ep, err := st.EndpointDetail(ctx, window(), store.Endpoint{Service: "acme/fix-orders", Name: "GET /orders"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ep.Queries) != 1 || !ep.Queries[0].NPlusOne || ep.Queries[0].Calls != 35 {
		t.Errorf("the endpoint's queries = %+v", ep.Queries)
	}
	// A query nothing repeats has no such list.
	one, err := st.QueryDetail(ctx, window(), store.QueryKey{DBSystem: "postgresql", Fingerprint: "SELECT ? FROM t3"}, 10)
	if err != nil || one.Stats == nil || one.Stats.NPlusOne || one.NPlusOne == nil || len(one.NPlusOne) != 0 {
		t.Errorf("a query run once: %+v, %v", one, err)
	}
	// And the endpoints page by offset too.
	for i := 0; i < 7; i++ {
		r, _ := requestOf("acme/fix-orders", fmt.Sprintf("GET /r%d", i), 100+i, linkEpoch+ms(2000)+int64(i)*ms(50), 0)
		add(t, st, r)
	}
	var names []string
	for offset := 0; ; offset += 3 {
		eps, err := st.Endpoints(ctx, window(), store.EndpointQuery{Sort: "name", Asc: true, Limit: 3, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range eps.Rows {
			names = append(names, e.Name)
		}
		if len(eps.Rows) > 0 && eps.Total != 8 {
			t.Errorf("total = %d on the page at %d, want 8", eps.Total, offset)
		}
		if len(eps.Rows) < 3 {
			break
		}
	}
	if !slices.Equal(names, []string{"GET /orders", "GET /r0", "GET /r1", "GET /r2", "GET /r3", "GET /r4", "GET /r5", "GET /r6"}) {
		t.Errorf("endpoints paged through %v", names)
	}
}

// One trace opened: its spans in start order, the same spans as a tree,
// and the logs written during it.
func TestATraceIsATree(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	const trace = "000000000000000000000000000000f1"
	sp := func(id, parent, name string, at float64) store.Span {
		return store.Span{TraceID: trace, SpanID: id, ParentID: parent, Service: "acme/fix-orders", Name: name, Kind: "internal",
			StartNs: linkEpoch + ms(at), DurationNs: ms(1), Status: "unset", Attrs: `{"k":"v"}`}
	}
	query := sp("d", "b", "pg.query", 3)
	query.DBSystem, query.DBStatement, query.DBFingerprint = "postgresql", "SELECT 1", "SELECT ?"
	root := sp("a", "", "GET /orders", 0)
	root.Kind = "server"
	add(t, st, []store.Span{
		query, sp("c", "a", "render", 5), sp("b", "a", "load", 2), root,
		// Its parent never arrived, so it stands as a root of its own.
		sp("e", "zz", "orphan", 6),
	})
	if err := st.AddLogs([]store.Log{
		{TsNs: linkEpoch + ms(4), Service: "acme/fix-orders", Severity: "info", SeverityNum: 9, Body: "second", TraceID: trace, SpanID: "b", Attrs: "{}"},
		{TsNs: linkEpoch + ms(1), Service: "acme/fix-orders", Severity: "info", SeverityNum: 9, Body: "first", TraceID: trace, SpanID: "a", Attrs: "{}"},
		{TsNs: linkEpoch + ms(1), Service: "acme/fix-orders", Severity: "info", SeverityNum: 9, Body: "another trace's", TraceID: "ff", Attrs: "{}"},
	}); err != nil {
		t.Fatal(err)
	}

	spans, tree, logs, err := st.Trace(ctx, trace)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	depth := map[string]int{}
	for _, s := range spans {
		order = append(order, s.SpanID)
		depth[s.SpanID] = s.Depth
	}
	if !slices.Equal(order, []string{"a", "b", "d", "c", "e"}) {
		t.Errorf("spans in the order %v, want start order", order)
	}
	if depth["a"] != 0 || depth["b"] != 1 || depth["c"] != 1 || depth["d"] != 2 || depth["e"] != 0 {
		t.Errorf("depths = %v", depth)
	}
	var walk func(nodes []store.TraceNode) string
	walk = func(nodes []store.TraceNode) string {
		var out []string
		for _, n := range nodes {
			out = append(out, n.SpanID+"("+walk(n.Children)+")")
		}
		return strings.Join(out, " ")
	}
	if got := walk(tree); got != "a(b(d()) c()) e()" {
		t.Errorf("tree = %s", got)
	}
	if spans[2].DBStatement != "SELECT 1" || spans[2].DBRole != "query" || string(spans[2].Attrs) != `{"k":"v"}` || !spans[0].Entry {
		t.Errorf("the query's span = %+v", spans[2])
	}
	if len(logs) != 2 || logs[0].Body != "first" || logs[1].Body != "second" || logs[1].SpanID != "b" {
		t.Errorf("logs = %+v, want this trace's two, oldest first", logs)
	}
	if spans, tree, logs, err := st.Trace(ctx, "no-such-trace"); err != nil || len(spans) != 0 || tree == nil || logs == nil {
		t.Errorf("a trace that is not there: %v, %v, %v, %v", spans, tree, logs, err)
	}
}

func rowCounts(t *testing.T, st *store.Store) (spans, logs int64) {
	t.Helper()
	st.Flush()
	if err := st.DB().QueryRow(`SELECT (SELECT count(*) FROM spans), (SELECT count(*) FROM logs)`).Scan(&spans, &logs); err != nil {
		t.Fatal(err)
	}
	return
}

// bulk adds n requests of two queries each, a minute apart ending at end.
func bulk(t *testing.T, st *store.Store, service string, n int, end time.Time, every time.Duration) {
	t.Helper()
	var spans []store.Span
	var logs []store.Log
	for i := 0; i < n; i++ {
		s, l := requestOf(service, "GET /orders", i, end.Add(-time.Duration(n-i)*every).UnixNano(), 2)
		// Rows of a size like real ones: a statement and its attributes.
		for k := range s {
			s[k].Attrs = `{"db.query.parameters":["` + strings.Repeat("x", 300) + `"]}`
		}
		spans = append(spans, s...)
		logs = append(logs, l)
	}
	if err := st.AddSpans(spans); err != nil {
		t.Fatal(err)
	}
	if err := st.AddLogs(logs); err != nil {
		t.Fatal(err)
	}
	st.Flush()
}

// Rows older than the cutoff go, the rest stay, and the file gets smaller.
func TestPruneDropsOldRowsAndGivesTheSpaceBack(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	now := time.Now()
	bulk(t, st, "acme/old", 5000, now.Add(-80*time.Hour), time.Second)
	bulk(t, st, "acme/new", 300, now.Add(-time.Minute), time.Second)
	// A worktree heard of long ago that has nothing left goes too; the
	// other's stays.
	if err := st.NoteWorktrees([]store.Worktree{{Service: "acme/new", Project: "acme", Worktree: "new"}}); err != nil {
		t.Fatal(err)
	}
	before, err := st.Storage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.OldestMs == 0 || before.OldestMs > float64(now.Add(-80*time.Hour).UnixMilli()) || before.SizeBytes < 5<<20 {
		t.Fatalf("before: %+v", before)
	}

	p, err := st.Prune(ctx, now.Add(-72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if p.Spans != 15000 || p.Logs != 5000 {
		t.Errorf("pruned %+v, want 15000 spans and 5000 logs", p)
	}
	if spans, logs := rowCounts(t, st); spans != 900 || logs != 300 {
		t.Errorf("%d spans and %d logs are left, want 900 and 300", spans, logs)
	}
	after, err := st.Storage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.SizeBytes > before.SizeBytes/5 || after.FileBytes > before.FileBytes/5 {
		t.Errorf("the store held %d bytes in a file of %d and now holds %d in %d: the space did not come back",
			before.SizeBytes, before.FileBytes, after.SizeBytes, after.FileBytes)
	}
	if after.OldestMs < float64(now.Add(-10*time.Minute).UnixMilli()) {
		t.Errorf("the oldest row is from %v", after.OldestMs)
	}
	sum, err := st.Summary(ctx)
	if err != nil || !slices.Equal(sum.Services, []string{"acme/new"}) || sum.Traces != 300 {
		t.Errorf("summary after the prune = %+v, %v", sum, err)
	}
	if _, ok := st.Worktrees()["acme/new"]; !ok {
		t.Error("a worktree that still has rows was forgotten")
	}
	// Nothing to do the second time.
	if p, err := st.Prune(ctx, now.Add(-72*time.Hour)); err != nil || p != (store.Pruned{}) {
		t.Errorf("pruning again: %+v, %v", p, err)
	}
}

// Past the size cap the oldest rows go early, whatever their age, and the
// newest are the ones kept.
func TestTheSizeCapDropsTheOldestEarly(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "leadline.db"), store.Options{ReadWait: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	bulk(t, st, "acme/fix-orders", 4000, now, time.Second)
	full, err := st.Size(ctx)
	if err != nil {
		t.Fatal(err)
	}
	limit := full / 3
	st.Retain(store.Retention{MaxAge: 72 * time.Hour, MaxBytes: limit, Every: 20 * time.Millisecond})

	deadline := time.Now().Add(20 * time.Second)
	for {
		size, err := st.Size(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if size <= limit {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the store is still %d bytes, over its cap of %d", size, limit)
		}
		time.Sleep(20 * time.Millisecond)
	}
	spans, logs := rowCounts(t, st)
	if spans == 0 || spans >= 12000/2 || logs == 0 {
		t.Errorf("%d spans and %d logs are left of 12000 and 4000: want some, and well under half", spans, logs)
	}
	// What is left is the newest: nothing before the oldest kept is there,
	// and the last request is.
	traces, err := st.Traces(ctx, store.Filter{Limit: 1})
	if err != nil || len(traces) != 1 || traces[0].StartMs < float64(now.Add(-2*time.Second).UnixMilli()) {
		t.Errorf("the newest trace after the cut = %+v, %v", traces, err)
	}
	storage, err := st.Storage(ctx)
	if err != nil || storage.MaxBytes != limit || storage.RetainMs != float64((72*time.Hour).Milliseconds()) {
		t.Errorf("storage = %+v, %v", storage, err)
	}
	if storage.OldestMs < float64(now.Add(-4000*time.Second).UnixMilli())+1000 {
		t.Errorf("the oldest row kept is from the very start: %v", storage.OldestMs)
	}
}

func TestParseAgeAndSize(t *testing.T) {
	for in, want := range map[string]time.Duration{"72h": 72 * time.Hour, "7d": 7 * 24 * time.Hour, "0.5d": 12 * time.Hour, "0": 0, "off": 0, "": 0, "90m": 90 * time.Minute} {
		if got, err := store.ParseAge(in); err != nil || got != want {
			t.Errorf("ParseAge(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"soon", "-1h", "-2d", "xd"} {
		if _, err := store.ParseAge(in); err == nil {
			t.Errorf("ParseAge(%q) was taken", in)
		}
	}
	for in, want := range map[string]int64{"2GB": 2 << 30, "500MB": 500 << 20, "1.5g": 3 << 29, "64k": 64 << 10, "1000": 1000, "12B": 12, "0": 0, "off": 0} {
		if got, err := store.ParseSize(in); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"big", "-1GB", "GB"} {
		if _, err := store.ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) was taken", in)
		}
	}
}

// What a service says of its worktree is kept, the newest word of it.
func TestWorktreesAreNoted(t *testing.T) {
	st := openStore(t)
	note := func(w ...store.Worktree) {
		t.Helper()
		if err := st.NoteWorktrees(w); err != nil {
			t.Fatal(err)
		}
	}
	note(store.Worktree{Service: "acme/fix-orders", Project: "acme", Worktree: "fix-orders", Branch: "fix-orders", Box: "devbox"},
		store.Worktree{Service: "acme-api"}, // says nothing: not a worktree
		store.Worktree{Project: "acme"})     // no service to file it under
	if got := st.Worktrees(); len(got) != 1 || got["acme/fix-orders"].Box != "devbox" {
		t.Errorf("worktrees = %+v", got)
	}
	// The branch was renamed.
	note(store.Worktree{Service: "acme/fix-orders", Project: "acme", Worktree: "fix-orders", Branch: "fix/orders-n-plus-one", Box: "devbox"})
	var branch string
	if err := st.DB().QueryRow(`SELECT branch FROM worktrees WHERE service = 'acme/fix-orders'`).Scan(&branch); err != nil || branch != "fix/orders-n-plus-one" {
		t.Errorf("the stored branch = %q, %v", branch, err)
	}
}

// Every service the store holds rows for is counted, and its rows from the
// proxy told from its own.
func TestServicesAreCounted(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	spans, log := requestOf("acme/fix-orders", "GET /orders", 1, linkEpoch, 2)
	add(t, st, spans)
	add(t, st, []store.Span{{TraceID: fmt.Sprintf("%032x", 0x77), SpanID: fmt.Sprintf("%016x", 0x77), Service: "acme/add-refunds", Name: "SELECT",
		Kind: "client", StartNs: linkEpoch + ms(900), DurationNs: ms(1), Status: "ok", DBSystem: "postgresql", DBStatement: "SELECT 1",
		DBFingerprint: "SELECT ?", Attrs: `{"leadline.source":"pgproxy"}`, Source: store.SourcePostgresWire}})
	if err := st.AddLogs([]store.Log{log, {TsNs: linkEpoch + ms(2000), Service: "acme/logs-only", Severity: "info", SeverityNum: 9, Body: "hello", Attrs: "{}"}}); err != nil {
		t.Fatal(err)
	}
	got, err := st.Services(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []store.ServiceTotals{
		{Name: "acme/add-refunds", Spans: 1, WireSpans: 1, LastNs: linkEpoch + ms(900)},
		{Name: "acme/fix-orders", Spans: 3, Logs: 1, LastNs: linkEpoch + ms(3)},
		{Name: "acme/logs-only", Logs: 1, LastNs: linkEpoch + ms(2000)},
	}
	if !slices.Equal(got, want) {
		t.Errorf("services = %+v\nwant %+v", got, want)
	}
	// The next row shows at once.
	more, _ := requestOf("acme/fix-orders", "GET /orders", 2, linkEpoch+ms(100), 0)
	add(t, st, more)
	if sum, err := st.Summary(ctx); err != nil || sum.Spans != 5 || sum.Logs != 2 || sum.Traces != 3 || len(sum.Services) != 3 {
		t.Errorf("summary = %+v, %v", sum, err)
	}
}
