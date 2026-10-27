package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cosscom/shipyard/internal/leadline"
)

// SubSep joins a worktree's service name to the name of one of several
// commands run in it: "acme/fix-orders:worker". What is filed under such a
// name is the worktree's, and a source filter takes it with the rest. It is
// the attach helper's rule, said once.
const SubSep = leadline.SubSep

// SourceOf is the source a service belongs to: the name up to the first
// colon. "acme/fix-orders:worker" is of the source "acme/fix-orders".
func SourceOf(service string) string {
	source, _, _ := strings.Cut(service, SubSep)
	return source
}

// Filter narrows a query. Zero values mean "no limit on this".
type Filter struct {
	SinceMs float64 // only rows at or after this time
	// Service keeps one service, by its exact name. Source keeps one
	// worktree: the service of that name and those under it
	// ("acme/fix-orders" and "acme/fix-orders:worker").
	Service, Source string
	Search          string // substring, case-insensitive
	MinSeverity     int32  // logs only: OTLP severity number
	// Traces only.
	ErrorsOnly    bool
	MinDurationMs float64
	Limit         int
	// Offset skips that many rows of Queries, whose order is by cost and
	// has no cursor.
	Offset int
	// User keeps the traces that have a span for this user ("user.id"), and
	// the logs written for them.
	User string
	// DBSystem and DBServer narrow to one database: the queries sent to
	// it, the traces that sent it one, and the logs written during those
	// traces. DBServer is a host:port, however spelled (see DBServer).
	DBSystem, DBServer string
	// Kind keeps the logs of one "leadline.kind" (logs only).
	Kind string
	// Control lists transaction and session control (BEGIN, COMMIT, SET)
	// in place of the queries (Queries only).
	Control bool
	// Before pages Traces and Logs: it is the Cursor of the last row of
	// the page before, and the rows returned are the ones that follow it.
	Before string
}

func (f Filter) limit(def int) int {
	if f.Limit <= 0 || f.Limit > 5000 {
		return def
	}
	return f.Limit
}

// services is the SQL that keeps col to the filter's service, its source
// or both, with the values it needs; "" when neither is asked for.
func services(col, service, source string) (string, []any) {
	var conds []string
	var args []any
	if service != "" {
		conds = append(conds, col+` = ?`)
		args = append(args, service)
	}
	if source != "" {
		under := source + SubSep
		conds = append(conds, `(`+col+` = ? OR substr(`+col+`, 1, ?) = ?)`)
		args = append(args, source, len(under), under)
	}
	return strings.Join(conds, ` AND `), args
}

func nsFromMs(ms float64) int64 { return int64(ms * 1e6) }

// ErrBadCursor is returned for a Filter.Before that no row ever carried.
var ErrBadCursor = errors.New("not a cursor from an earlier page")

// A cursor is a row's exact place in its list: its time in nanoseconds,
// which a millisecond float can't hold, a dot, and what settles ties among
// rows of that time. cutCursor splits one.
func cutCursor(cursor string) (ns int64, tie string, err error) {
	at, tie, ok := strings.Cut(cursor, ".")
	if ns, err = strconv.ParseInt(at, 10, 64); err != nil || !ok || tie == "" {
		return 0, "", ErrBadCursor
	}
	return ns, tie, nil
}

type TraceRow struct {
	TraceID    string  `json:"trace_id"`
	Name       string  `json:"name"`
	Service    string  `json:"service"`
	StartMs    float64 `json:"start_ms"`
	DurationMs float64 `json:"duration_ms"`
	Spans      int64   `json:"spans"`
	Errors     int64   `json:"errors"`
	Queries    int64   `json:"queries"`
	DBMs       float64 `json:"db_ms"`
	// UserID is who the trace ran for, when the app says ("user.id").
	UserID string `json:"user_id,omitempty"`
	// Cursor is this row's place in the list, for Filter.Before.
	Cursor string `json:"cursor,omitempty"`
}

// A trace is named after its root span; until the root arrives (it ends
// last, so it is exported last) the earliest span stands in.
const (
	traceName    = `coalesce(ll_first(name, start_ns) FILTER (WHERE parent_id = ''), ll_first(name, start_ns))`
	traceService = `coalesce(ll_first(service, start_ns) FILTER (WHERE parent_id = ''), ll_first(service, start_ns))`
)

// tracesAtATime is how many traces are worked out in one statement.
const tracesAtATime = 300

// Traces lists traces, newest first, and among traces that start in the
// same nanosecond by id, so a page always ends at an exact place.
//
// Grouping every span of the window by trace, to keep the newest couple of
// hundred, is the whole table read for one page. So it reads backwards in
// widening stretches of time and stops once it has enough: the spans_start
// index names the traces with a span in a stretch without reading a row,
// spans_trace says where each of those began, and a trace is listed in the
// stretch it began in. Only then are a page's worth worked out in full. The
// newest page costs the same on a day of spans as on a minute; only a
// filter that matches almost nothing reads the whole window.
func (s *Store) Traces(ctx context.Context, f Filter) ([]TraceRow, error) {
	limit := f.limit(200)
	since := nsFromMs(f.SinceMs)
	out := []TraceRow{}

	var before int64
	var beforeID string
	paged := f.Before != ""
	if paged {
		var err error
		if before, beforeID, err = cutCursor(f.Before); err != nil {
			return nil, err
		}
	}
	// Read off the two ends of the index: where to start, and when there
	// is nothing left.
	var oldest, newest sql.NullInt64
	if err := s.queryRow(ctx, `SELECT min(start_ns), max(start_ns) FROM spans`).Scan(&oldest, &newest); err != nil {
		return nil, err
	}
	if !newest.Valid {
		return out, nil
	}
	top := newest.Int64
	if paged {
		top = min(top, before)
	}
	since = max(since, oldest.Int64)
	having, hargs := traceHaving(f)

	for width := int64(time.Minute); len(out) < limit && top >= since; {
		// The last stretch takes whatever is left; so does one whose
		// width has outgrown an int64.
		from := since
		if lo := top - width + 1; width > 0 && lo > since && lo <= top {
			from = lo
		}
		// The newest page has no upper bound: a span that landed since the
		// index's end was read belongs on it.
		upper := top
		if !paged && len(out) == 0 && top == newest.Int64 {
			upper = math.MaxInt64
		}
		// The traces that begin in this stretch, newest first. One that
		// began before it has spans here too, and is left for the stretch
		// it began in.
		q := `
SELECT trace_id, s FROM (
	SELECT c.trace_id, (SELECT min(x.start_ns) FROM spans x INDEXED BY spans_trace
		WHERE x.trace_id = c.trace_id AND x.start_ns >= ?1) AS s
	FROM (SELECT DISTINCT trace_id FROM spans INDEXED BY spans_start WHERE start_ns >= ?2 AND start_ns <= ?3) c
) WHERE s >= ?2`
		args := []any{since, from, upper}
		if paged {
			q += ` AND (s < ?4 OR (s = ?4 AND trace_id < ?5))`
			args = append(args, before, beforeID)
		}
		q += ` ORDER BY s DESC, trace_id DESC`
		rows, err := s.query(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		var ids []string
		for rows.Next() {
			var id string
			var start int64
			if err := rows.Scan(&id, &start); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		for len(ids) > 0 && len(out) < limit {
			n := min(len(ids), tracesAtATime)
			page, err := s.traceRows(ctx, ids[:n], since, having, hargs)
			if err != nil {
				return nil, err
			}
			for _, id := range ids[:n] {
				if t, ok := page[id]; ok && len(out) < limit {
					out = append(out, t)
				}
			}
			ids = ids[n:]
		}
		if from <= since {
			break
		}
		top = from - 1
		// How much further back to read: a lot further when a stretch gave
		// little, and never less than eight times the last one, so a wrong
		// guess still gets there in a few goes.
		width *= 8
	}
	return out, nil
}

// traceHaving is the filter as conditions on a trace's own figures.
func traceHaving(f Filter) (string, []any) {
	var having []string
	var args []any
	if cond, a := services(traceService, f.Service, f.Source); cond != "" {
		having = append(having, cond)
		args = append(args, a...)
	}
	if f.Search != "" {
		having = append(having, traceName+` LIKE ?`)
		args = append(args, like(f.Search))
	}
	if f.ErrorsOnly {
		having = append(having, `count(*) FILTER (WHERE status = 'error') > 0`)
	}
	if f.User != "" {
		having = append(having, `count(*) FILTER (WHERE user_id = ?) > 0`)
		args = append(args, f.User)
	}
	if db := dbFilter("", f.DBSystem, f.DBServer); db != "" {
		having = append(having, `count(*) FILTER (WHERE `+db+`) > 0`)
	}
	if f.MinDurationMs > 0 {
		having = append(having, `(max(start_ns + duration_ns) - min(start_ns)) / 1e6 >= ?`)
		args = append(args, f.MinDurationMs)
	}
	return strings.Join(having, ` AND `), args
}

// traceRows works out the list's columns for the traces named, counting
// their spans from since on, and leaves out the ones having turns away.
func (s *Store) traceRows(ctx context.Context, ids []string, since int64, having string, hargs []any) (map[string]TraceRow, error) {
	args := make([]any, 0, len(ids)+1+len(hargs))
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, since)
	q := `
SELECT trace_id, ` + traceName + `, ` + traceService + `,
	min(start_ns),
	(max(start_ns + duration_ns) - min(start_ns)) / 1e6,
	count(*),
	count(*) FILTER (WHERE ` + isFailed + `),
	count(*) FILTER (WHERE ` + isQuery + `),
	coalesce(sum(duration_ns) FILTER (WHERE ` + isDBTime + `), 0) / 1e6,
	coalesce(max(user_id), '')
FROM spans INDEXED BY spans_trace
WHERE trace_id IN (?` + strings.Repeat(`,?`, len(ids)-1) + `) AND start_ns >= ?
GROUP BY trace_id`
	if having != "" {
		q += ` HAVING ` + having
		args = append(args, hargs...)
	}
	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]TraceRow, len(ids))
	for rows.Next() {
		var t TraceRow
		var start int64
		if err := rows.Scan(&t.TraceID, &t.Name, &t.Service, &start, &t.DurationMs,
			&t.Spans, &t.Errors, &t.Queries, &t.DBMs, &t.UserID); err != nil {
			return nil, err
		}
		t.StartMs = float64(start) / 1e6
		t.Cursor = strconv.FormatInt(start, 10) + "." + t.TraceID
		out[t.TraceID] = t
	}
	return out, rows.Err()
}

// quote writes a string as a SQL literal.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// dbFilter is the SQL that keeps the database calls to one system, one
// server or both, among the spans called alias ("" for the table itself).
// It is empty when neither is asked for. The values go in as literals, so
// the breakdowns' numbered parameters stay as they are.
func dbFilter(alias, system, server string) string {
	if system == "" && server == "" {
		return ""
	}
	if alias != "" {
		alias += "."
	}
	cond := alias + `db_role BETWEEN 1 AND 2`
	if system != "" {
		cond += ` AND ` + alias + `db_system = ` + quote(system)
	}
	if server != "" {
		cond += ` AND ` + alias + `db_server = ` + quote(DBServer(server, 0))
	}
	return cond
}

type SpanRow struct {
	SpanID        string          `json:"span_id"`
	ParentID      string          `json:"parent_id"`
	Service       string          `json:"service"`
	Name          string          `json:"name"`
	Kind          string          `json:"kind"`
	StartMs       float64         `json:"start_ms"`
	DurationMs    float64         `json:"duration_ms"`
	Status        string          `json:"status"`
	StatusMessage string          `json:"status_message"`
	DBSystem      string          `json:"db_system"`
	DBStatement   string          `json:"db_statement"`
	HTTPMethod    string          `json:"http_method"`
	HTTPRoute     string          `json:"http_route"`
	HTTPStatus    int32           `json:"http_status"`
	Attrs         json.RawMessage `json:"attrs"`
	// DBRole is what a database call counts as: "query"; "control" for
	// transaction and session bookkeeping (BEGIN, COMMIT, SET), which is
	// kept out of the query list; "duplicate" for a span that reports a
	// call another span of the trace already counts (the app's own span
	// around a statement the Postgres proxy timed). Empty otherwise.
	DBRole string `json:"db_role,omitempty"`
	// Entry says this span is a request: what the endpoints count.
	Entry bool `json:"entry,omitempty"`
	// DBServer is the host:port a database call went to, when known.
	DBServer string `json:"db_server,omitempty"`
	// Depth is how far under a root the span is in the trace's tree: 0 for
	// a span with no parent in the trace. Only Trace sets it.
	Depth int `json:"depth"`
}

// TraceSpans returns every span of one trace in start order.
func (s *Store) TraceSpans(ctx context.Context, traceID string) ([]SpanRow, error) {
	rows, err := s.query(ctx, `
SELECT span_id, parent_id, service, name, kind, start_ns / 1e6, duration_ns / 1e6,
	status, status_message, db_system, db_statement, http_method, http_route, http_status, attrs,
	db_role, entry, db_server
FROM spans INDEXED BY spans_trace WHERE trace_id = ? ORDER BY start_ns, rowid`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SpanRow{}
	for rows.Next() {
		var sp SpanRow
		var attrs string
		var role int8
		if err := rows.Scan(&sp.SpanID, &sp.ParentID, &sp.Service, &sp.Name, &sp.Kind,
			&sp.StartMs, &sp.DurationMs, &sp.Status, &sp.StatusMessage, &sp.DBSystem,
			&sp.DBStatement, &sp.HTTPMethod, &sp.HTTPRoute, &sp.HTTPStatus, &attrs,
			&role, &sp.Entry, &sp.DBServer); err != nil {
			return nil, err
		}
		sp.Attrs, sp.DBRole = rawJSON(attrs), roleName(role)
		out = append(out, sp)
	}
	return out, rows.Err()
}

type QueryRow struct {
	DBSystem    string  `json:"db_system"`
	Fingerprint string  `json:"fingerprint"`
	Example     string  `json:"example"`
	Calls       int64   `json:"calls"`
	TotalMs     float64 `json:"total_ms"`
	AvgMs       float64 `json:"avg_ms"`
	P50Ms       float64 `json:"p50_ms"`
	P95Ms       float64 `json:"p95_ms"`
	MaxMs       float64 `json:"max_ms"`
	Errors      int64   `json:"errors"`
	LastMs      float64 `json:"last_ms"`
	Traces      int64   `json:"traces"`
	// MaxPerTrace is the most times one trace ran this query: a high number
	// is the mark of an N+1.
	MaxPerTrace int64 `json:"max_per_trace"`
	// NPlusOne says one trace ran it NPlusOne times or more.
	NPlusOne bool `json:"n_plus_one"`
}

// NPlusOne is how many times one request must run the same query before it
// is called an N+1: Leadline's number.
const NPlusOne = 5

// Queries groups database spans by fingerprint, costliest first. Only the
// spans that count as queries are in it: a statement seen twice is there
// once, and BEGIN, COMMIT and their like are left to Filter.Control.
func (s *Store) Queries(ctx context.Context, f Filter) ([]QueryRow, error) {
	where := isQuery + ` AND start_ns >= ?`
	if f.Control {
		where = `db_role = 2 AND start_ns >= ?`
	}
	args := []any{nsFromMs(f.SinceMs)}
	if f.DBSystem != "" {
		where += ` AND db_system = ?`
		args = append(args, f.DBSystem)
	}
	if f.DBServer != "" {
		where += ` AND db_server = ?`
		args = append(args, DBServer(f.DBServer, 0))
	}
	if cond, a := services(`service`, f.Service, f.Source); cond != "" {
		where += ` AND ` + cond
		args = append(args, a...)
	}
	if f.Search != "" {
		where += ` AND db_statement LIKE ?`
		args = append(args, like(f.Search))
	}
	// A stretch of time is read by the time index; all of time, from the
	// table as it lies.
	by := ` INDEXED BY spans_start`
	if f.SinceMs <= 0 {
		by = ` NOT INDEXED`
	}
	q := `
WITH q AS (
	SELECT db_shape, db_system, db_fingerprint, db_statement, trace_id, duration_ns, start_ns, status
	FROM spans` + by + ` WHERE ` + where + `
), timing AS (
	-- The system, the fingerprint and the example are read off any one row
	-- of the shape: the first two are the same on every row of it.
	SELECT db_shape, db_system, db_fingerprint, db_statement AS example,
		count(*) AS calls, sum(duration_ns) AS total_ns, avg(duration_ns) AS avg_ns,
		ll_quantile(duration_ns, 0.5) AS p50_ns, ll_quantile(duration_ns, 0.95) AS p95_ns, max(duration_ns) AS max_ns,
		count(*) FILTER (WHERE status = 'error') AS errors, max(start_ns) AS last_ns
	FROM q GROUP BY db_shape
), per_trace AS (
	SELECT db_shape, count(*) AS traces, max(n) AS max_n FROM (
		SELECT db_shape, trace_id, count(*) AS n FROM q GROUP BY db_shape, trace_id
	) GROUP BY db_shape
)
SELECT t.db_system, t.db_fingerprint, t.example, t.calls, t.total_ns / 1e6, t.avg_ns / 1e6,
	t.p50_ns / 1e6, t.p95_ns / 1e6, t.max_ns / 1e6, t.errors, t.last_ns / 1e6, p.traces, p.max_n
FROM timing t JOIN per_trace p ON p.db_shape = t.db_shape
ORDER BY t.total_ns DESC, t.db_system, t.db_fingerprint
LIMIT ? OFFSET ?`
	rows, err := s.query(ctx, q, append(args, f.limit(200), max(f.Offset, 0))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QueryRow{}
	for rows.Next() {
		var r QueryRow
		if err := rows.Scan(&r.DBSystem, &r.Fingerprint, &r.Example, &r.Calls, &r.TotalMs,
			&r.AvgMs, &r.P50Ms, &r.P95Ms, &r.MaxMs, &r.Errors, &r.LastMs, &r.Traces,
			&r.MaxPerTrace); err != nil {
			return nil, err
		}
		r.NPlusOne = r.MaxPerTrace >= NPlusOne
		out = append(out, r)
	}
	return out, rows.Err()
}

type LogRow struct {
	TsMs     float64         `json:"ts_ms"`
	Service  string          `json:"service"`
	Severity string          `json:"severity"`
	Body     string          `json:"body"`
	TraceID  string          `json:"trace_id"`
	SpanID   string          `json:"span_id"`
	Attrs    json.RawMessage `json:"attrs"`
	// Kind is what sort of record a provider says this is ("leadline.kind":
	// "browser.error", "browser.web_vital"), and UserID who it was written
	// for ("user.id"). Only Logs sets them.
	Kind   string `json:"kind,omitempty"`
	UserID string `json:"user_id,omitempty"`
	// Cursor is this row's place in the list, for Filter.Before. Only Logs
	// sets it.
	Cursor string `json:"cursor,omitempty"`
}

// Logs lists log records, newest first; among records of one nanosecond,
// the one stored last comes first, so the list read backwards is the order
// they were written in.
//
// It walks the time index backwards and stops at the limit, so the newest
// page costs the same on a day of logs as on a minute. Only a search that
// matches almost nothing reads the whole window.
func (s *Store) Logs(ctx context.Context, f Filter) ([]LogRow, error) {
	where := `ts_ns >= ?`
	args := []any{nsFromMs(f.SinceMs)}
	if f.Before != "" {
		// A log's place among records of its own nanosecond is the id it
		// was stored under.
		at, tie, err := cutCursor(f.Before)
		if err != nil {
			return nil, err
		}
		id, err := strconv.ParseInt(tie, 10, 64)
		if err != nil || id < 1 {
			return nil, ErrBadCursor
		}
		where += ` AND (ts_ns < ? OR (ts_ns = ? AND id < ?))`
		args = append(args, at, at, id)
	}
	if cond, a := services(`service`, f.Service, f.Source); cond != "" {
		where += ` AND ` + cond
		args = append(args, a...)
	}
	if f.Search != "" {
		where += ` AND body LIKE ?`
		args = append(args, like(f.Search))
	}
	if f.MinSeverity > 0 {
		where += ` AND severity_num >= ?`
		args = append(args, f.MinSeverity)
	}
	if f.Kind != "" {
		where += ` AND kind = ?`
		args = append(args, f.Kind)
	}
	if f.User != "" {
		where += ` AND user_id = ?`
		args = append(args, f.User)
	}
	// Logs written during the traces that touched the database.
	if db := dbFilter("d", f.DBSystem, f.DBServer); db != "" {
		where += ` AND trace_id <> '' AND EXISTS (SELECT 1 FROM spans d INDEXED BY spans_trace WHERE d.trace_id = logs.trace_id AND ` + db + `)`
	}
	rows, err := s.query(ctx, `
SELECT id, ts_ns, service, severity, body, trace_id, span_id, attrs, kind, user_id
FROM logs WHERE `+where+`
ORDER BY ts_ns DESC, id DESC LIMIT ?`, append(args, f.limit(500))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogRow{}
	for rows.Next() {
		var l LogRow
		var id, ts int64
		var attrs string
		if err := rows.Scan(&id, &ts, &l.Service, &l.Severity, &l.Body, &l.TraceID, &l.SpanID, &attrs, &l.Kind, &l.UserID); err != nil {
			return nil, err
		}
		l.TsMs = float64(ts) / 1e6
		l.Attrs = rawJSON(attrs)
		l.Cursor = strconv.FormatInt(ts, 10) + "." + strconv.FormatInt(id, 10)
		out = append(out, l)
	}
	return out, rows.Err()
}

// TraceNode is one span in a trace's tree, by id, with the spans under it
// in start order.
type TraceNode struct {
	SpanID   string      `json:"span_id"`
	Children []TraceNode `json:"children"`
}

// Trace is one trace opened: its spans in start order, each with how deep
// it is; the same spans as a tree; and the logs written during it, oldest
// first.
func (s *Store) Trace(ctx context.Context, traceID string) ([]SpanRow, []TraceNode, []LogRow, error) {
	spans, err := s.TraceSpans(ctx, traceID)
	if err != nil || len(spans) == 0 {
		return spans, []TraceNode{}, []LogRow{}, err
	}
	tree := treeOf(spans)
	rows, err := s.query(ctx, `
SELECT ts_ns / 1e6, service, severity, body, trace_id, span_id, attrs FROM logs
WHERE trace_id = ? ORDER BY ts_ns, id LIMIT 1000`, traceID)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	logs := []LogRow{}
	for rows.Next() {
		var l LogRow
		var attrs string
		if err := rows.Scan(&l.TsMs, &l.Service, &l.Severity, &l.Body, &l.TraceID, &l.SpanID, &attrs); err != nil {
			return nil, nil, nil, err
		}
		l.Attrs = rawJSON(attrs)
		logs = append(logs, l)
	}
	return spans, tree, logs, rows.Err()
}

// treeOf arranges a trace's spans, which are in start order, as a tree,
// and writes each span's depth into it. A span whose parent is not in the
// trace (not yet exported, or never) is a root. Parents that name each
// other in a ring, which no tracer makes but a broken one might, are cut
// where the ring closes.
func treeOf(spans []SpanRow) []TraceNode {
	at := make(map[string]int, len(spans))
	for i := range spans {
		if _, dup := at[spans[i].SpanID]; !dup {
			at[spans[i].SpanID] = i
		}
	}
	kids := make(map[int][]int, len(spans))
	var roots []int
	for i := range spans {
		p, ok := at[spans[i].ParentID]
		if spans[i].ParentID == "" || !ok || p == i {
			roots = append(roots, i)
			continue
		}
		kids[p] = append(kids[p], i)
	}
	placed := make([]bool, len(spans))
	var build func(i, depth int) TraceNode
	build = func(i, depth int) TraceNode {
		placed[i] = true
		spans[i].Depth = depth
		node := TraceNode{SpanID: spans[i].SpanID, Children: []TraceNode{}}
		for _, k := range kids[i] {
			if !placed[k] {
				node.Children = append(node.Children, build(k, depth+1))
			}
		}
		return node
	}
	tree := []TraceNode{}
	for _, r := range roots {
		tree = append(tree, build(r, 0))
	}
	// What is left is in a ring with no way in: start each ring at its
	// earliest span.
	for i := range spans {
		if !placed[i] {
			tree = append(tree, build(i, 0))
		}
	}
	sort.SliceStable(tree, func(a, b int) bool { return at[tree[a].SpanID] < at[tree[b].SpanID] })
	return tree
}

// Series is a set of aligned time series: every slice has one value per
// bucket, and T holds each bucket's start.
type Series struct {
	BucketMs  float64   `json:"bucket_ms"`
	T         []float64 `json:"t"`
	Requests  []float64 `json:"requests"`
	Errors    []float64 `json:"errors"`
	Queries   []float64 `json:"queries"`
	P50Ms     []float64 `json:"p50_ms"`
	P95Ms     []float64 `json:"p95_ms"`
	DBMs      []float64 `json:"db_ms"`
	Logs      []float64 `json:"logs"`
	LogErrors []float64 `json:"log_errors"`
}

// SeriesBetween buckets everything in the window into n buckets. Requests
// and latency are of entry spans, the same requests the endpoint breakdown
// counts, so the two agree with a service filter and without.
func (s *Store) SeriesBetween(ctx context.Context, w Window, n int) (*Series, error) {
	if n < 1 {
		n = 1
	}
	since, until := nsFromMs(w.SinceMs), nsFromMs(w.UntilMs)
	bucket := (until - since) / int64(n)
	if bucket < 1 {
		bucket = 1
	}
	out := &Series{BucketMs: float64(bucket) / 1e6}
	for _, p := range []*[]float64{&out.T, &out.Requests, &out.Errors, &out.Queries,
		&out.P50Ms, &out.P95Ms, &out.DBMs, &out.Logs, &out.LogErrors} {
		*p = make([]float64, n)
	}
	for i := range out.T {
		out.T[i] = float64(since+int64(i)*bucket) / 1e6
	}

	rows, err := s.query(ctx, `
SELECT (start_ns - ?1) / ?6 AS b,
	count(*) FILTER (WHERE `+isEntry+`),
	count(*) FILTER (WHERE `+isFailed+`),
	count(*) FILTER (WHERE `+isQuery+`),
	coalesce(ll_quantile(duration_ns, 0.5) FILTER (WHERE `+isEntry+`), 0) / 1e6,
	coalesce(ll_quantile(duration_ns, 0.95) FILTER (WHERE `+isEntry+`), 0) / 1e6,
	coalesce(sum(duration_ns) FILTER (WHERE `+isDBTime+`), 0) / 1e6
FROM `+spansInWindow+`
GROUP BY b`, w.args(bucket)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var b int64
		var req, errs, queries int64
		var p50, p95, db float64
		if err := rows.Scan(&b, &req, &errs, &queries, &p50, &p95, &db); err != nil {
			rows.Close()
			return nil, err
		}
		if b < 0 || b >= int64(n) {
			continue
		}
		out.Requests[b], out.Errors[b], out.Queries[b] = float64(req), float64(errs), float64(queries)
		out.P50Ms[b], out.P95Ms[b], out.DBMs[b] = p50, p95, db
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// Severity 17 and up is error and fatal.
	lrows, err := s.query(ctx, `
SELECT (ts_ns - ?1) / ?6 AS b, count(*), count(*) FILTER (WHERE severity_num >= 17)
FROM logs
WHERE ts_ns >= ?1 AND ts_ns < ?2 AND `+inServices+`
GROUP BY b`, w.args(bucket)...)
	if err != nil {
		return nil, err
	}
	defer lrows.Close()
	for lrows.Next() {
		var b, all, errs int64
		if err := lrows.Scan(&b, &all, &errs); err != nil {
			return nil, err
		}
		if b < 0 || b >= int64(n) {
			continue
		}
		out.Logs[b], out.LogErrors[b] = float64(all), float64(errs)
	}
	return out, lrows.Err()
}

// like turns a search term into a LIKE pattern, with the term's own
// wildcards left as they are: a dev tool's search box can afford that.
// SQLite's LIKE takes no notice of the case of ASCII letters.
func like(term string) string {
	if term == "" {
		return ""
	}
	return "%" + term + "%"
}

func rawJSON(s string) json.RawMessage {
	if strings.TrimSpace(s) == "" || !json.Valid([]byte(s)) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(s)
}
