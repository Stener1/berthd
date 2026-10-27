// Package store keeps spans and logs in one SQLite file and answers the
// helper's questions about them.
//
// It is Leadline's store rebuilt on SQLite: Leadline keeps its rows in
// DuckDB, which needs cgo, and this helper is built without. The rules for
// what a span counts as (link.go) are Leadline's own; the tables, the
// indexes and every query are written for SQLite, where a question is
// answered by walking an index and not by scanning columns.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // the database, in Go: no cgo
)

// Times are stored as unix nanoseconds, as OTLP sends them. The API reports
// milliseconds as floats: a nanosecond timestamp doesn't fit a JS number.
//
// The indexes are the questions: spans_start finds a stretch of time (and
// names the traces in it without reading a row), spans_trace finds one
// trace and what ran inside a request, spans_service counts what each
// service holds from the index alone.
//
// db_shape is a number that stands for a database call's system and
// fingerprint together (see shapeOf). Queries are grouped by shape, and a
// number is grouped and compared many times faster than the statement's
// text; the text is kept beside it and is what is shown.
const schema = `
CREATE TABLE IF NOT EXISTS spans (
	trace_id TEXT NOT NULL, span_id TEXT NOT NULL, parent_id TEXT NOT NULL,
	service TEXT NOT NULL, name TEXT NOT NULL, kind TEXT NOT NULL,
	start_ns INTEGER NOT NULL, duration_ns INTEGER NOT NULL,
	status TEXT NOT NULL, status_message TEXT NOT NULL,
	db_system TEXT NOT NULL, db_statement TEXT NOT NULL, db_fingerprint TEXT NOT NULL,
	http_method TEXT NOT NULL, http_route TEXT NOT NULL, http_status INTEGER NOT NULL,
	attrs TEXT NOT NULL,
	db_role INTEGER NOT NULL DEFAULT 0, entry INTEGER NOT NULL DEFAULT 0,
	user_id TEXT NOT NULL DEFAULT '', db_server TEXT NOT NULL DEFAULT '',
	via INTEGER NOT NULL DEFAULT 0, db_shape INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS spans_start ON spans (start_ns, trace_id);
CREATE INDEX IF NOT EXISTS spans_trace ON spans (trace_id, start_ns);
CREATE INDEX IF NOT EXISTS spans_service ON spans (service, via, start_ns);
CREATE TABLE IF NOT EXISTS logs (
	id INTEGER PRIMARY KEY,
	ts_ns INTEGER NOT NULL, service TEXT NOT NULL,
	severity TEXT NOT NULL, severity_num INTEGER NOT NULL,
	body TEXT NOT NULL, trace_id TEXT NOT NULL, span_id TEXT NOT NULL,
	attrs TEXT NOT NULL,
	kind TEXT NOT NULL DEFAULT '', user_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS logs_ts ON logs (ts_ns);
CREATE INDEX IF NOT EXISTS logs_trace ON logs (trace_id, ts_ns) WHERE trace_id <> '';
CREATE INDEX IF NOT EXISTS logs_service ON logs (service, ts_ns);
CREATE TABLE IF NOT EXISTS worktrees (
	service TEXT PRIMARY KEY,
	project TEXT NOT NULL, worktree TEXT NOT NULL, branch TEXT NOT NULL, box TEXT NOT NULL,
	seen_ns INTEGER NOT NULL
) WITHOUT ROWID;
`

// schemaVersion is written to the file (PRAGMA user_version). A file from a
// newer helper is refused rather than guessed at.
const schemaVersion = 1

// Span is one row of spans. Empty strings stand for "not set": a root span
// has ParentID "", a span that isn't a database call has DBSystem "".
type Span struct {
	TraceID, SpanID, ParentID string
	Service, Name, Kind       string
	StartNs, DurationNs       int64
	Status, StatusMessage     string
	DBSystem, DBStatement     string
	DBFingerprint             string
	HTTPMethod, HTTPRoute     string
	HTTPStatus                int32
	Attrs                     string // a JSON object
	// UserID is who the span ran for (the "user.id" attribute), when the
	// app says.
	UserID string
	// DBServer is where a database call went, as host:port spelled one way
	// (see DBServer), when the span says: it tells two databases of one
	// system apart.
	DBServer string

	// Source says where the span came from, which decides what it counts
	// as (link.go): "" for a span the app exported, or one of the Source
	// constants. Of it only "came off the wire or not" is stored (via).
	Source string
	// DBName, Capture and Linked are not stored. DBName is the database a
	// database call went to, when known. Capture is which proxy recorded
	// the span: the id the helper gave it. Linked says a proxy's span
	// carries the app's own trace id and the id of the span that sent the
	// statement, read from a sqlcommenter comment on it, and not ids the
	// proxy made up.
	DBName  string
	Capture string
	Linked  bool
}

// Where a span came from, when not from the app's own telemetry.
const (
	SourceBrowser      = "browser" // the page, not the server: never a request of its own
	SourcePostgresWire = "pgproxy" // read off the wire by the Postgres proxy
)

// What the via column holds.
const (
	viaOTLP = 0 // the app exported it
	viaWire = 1 // the Postgres proxy recorded it
)

type Log struct {
	TsNs            int64
	Service         string
	Severity        string
	SeverityNum     int32
	Body            string
	TraceID, SpanID string
	Attrs           string // a JSON object
	// Kind is the "leadline.kind" attribute: what sort of record a provider
	// says this is ("browser.error", say). UserID is "user.id".
	Kind, UserID string
}

type Store struct {
	path string
	// wdb is the one connection that writes; rdb the few that read. SQLite
	// has one writer at a time whatever is asked of it, and in its
	// write-ahead mode a read never waits for that writer.
	wdb, rdb *sql.DB

	// Spans and logs wait in memory and one goroutine, the flusher, writes
	// them together a moment later. So an export costs the app that sent
	// it an append to a slice and nothing else: no commit.
	//
	// pmu guards what is waiting. added counts every row ever taken in and
	// written is that count as of the last finished write, so "written has
	// caught up with added as I saw it" means a read misses nothing.
	pmu       sync.Mutex
	waitSpans []Span
	waitLogs  []Log
	closed    bool
	added     atomic.Int64
	written   atomic.Int64
	flushed   chan struct{} // closed, and replaced, after every write
	wake      chan struct{} // rows are waiting
	urgent    chan struct{} // someone is waiting on the flusher: don't linger

	// wmu serialises everything that writes: the flusher, a prune, a
	// forget. The linker is the flusher's and is guarded by it too.
	wmu  sync.Mutex
	link *linker

	flushEvery time.Duration
	readWait   time.Duration
	stop       chan struct{} // closed by Close
	bg         sync.WaitGroup
	closeOnce  sync.Once
	closeErr   error

	// gen goes up whenever stored rows change, for answers worth caching.
	gen     atomic.Int64
	counted totalsCache
	// keep is what Retain was last given, for Storage to report.
	keep atomic.Pointer[Retention]

	// known is what the worktrees table holds, so that a resource seen a
	// thousand times a second is written once.
	kmu   sync.Mutex
	known map[string]Worktree
}

// Options tune a store. The zero value is what the helper runs with.
type Options struct {
	// FlushEvery is the longest a row waits in memory before it is written
	// to the database. A read never sees that wait, because every read
	// first has what is waiting written. Zero means 200ms.
	FlushEvery time.Duration
	// ReadWait is the longest a read waits for rows on their way in before
	// it answers with what is already written. Zero means one second. A
	// write takes milliseconds, so this only matters when the disk has
	// stalled, or on a machine slow enough that a large batch takes longer
	// to write than that: a test that must read what it just wrote sets it
	// long.
	ReadWait time.Duration
}

const (
	defaultFlushEvery = 200 * time.Millisecond
	// maxWaiting is how many rows may wait in memory. Whoever brings a
	// burst past it waits for the flusher, which is what slows a sender
	// that outruns the disk.
	maxWaiting = 20_000
	// defaultReadWait is Options.ReadWait when it is not given: with a
	// stalled disk, a read a moment behind beats no read.
	defaultReadWait = time.Second
	// readers is how many reads may run at once.
	readers = 4
)

// Open opens the database file at path, creating it if needed. The file is
// made readable by its owner alone: it holds SQL and log lines.
func Open(path string, opts ...Options) (*Store, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.FlushEvery <= 0 {
		o.FlushEvery = defaultFlushEvery
	}
	if o.ReadWait <= 0 {
		o.ReadWait = defaultReadWait
	}
	if path == "" {
		return nil, errors.New("the store needs a file to keep its rows in")
	}
	// Made before SQLite makes it, so it is never there with wider
	// permissions, not even for a moment.
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600); err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	} else {
		f.Close()
	}
	s := &Store{
		path:       path,
		flushEvery: o.FlushEvery,
		readWait:   o.ReadWait,
		flushed:    make(chan struct{}),
		wake:       make(chan struct{}, 1),
		urgent:     make(chan struct{}, 1),
		stop:       make(chan struct{}),
		link:       newLinker(),
		known:      map[string]Worktree{},
	}
	var err error
	if s.wdb, err = sql.Open("sqlite", dsn(path, false)); err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	s.wdb.SetMaxOpenConns(1)
	if err := s.setUp(); err != nil {
		s.wdb.Close()
		return nil, fmt.Errorf("set up the database %q: %w", path, err)
	}
	if s.rdb, err = sql.Open("sqlite", dsn(path, true)); err != nil {
		s.wdb.Close()
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	s.rdb.SetMaxOpenConns(readers)
	if err := s.loadWorktrees(); err != nil {
		s.wdb.Close()
		s.rdb.Close()
		return nil, fmt.Errorf("read the database %q: %w", path, err)
	}
	s.bg.Add(1)
	go s.flushLoop()
	return s, nil
}

// dsn is how the driver is told which file, and what every connection to it
// needs said: wait for a lock instead of failing, and (the readers) change
// nothing.
func dsn(path string, readOnly bool) string {
	// The path is given as a URI, where these three mean something else.
	name := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	out := "file:" + name + "?_pragma=busy_timeout(10000)&_pragma=synchronous(normal)"
	if readOnly {
		out += "&_pragma=query_only(1)"
	}
	return out
}

// setUp makes a new file's tables and checks an old file is one this
// helper can read.
func (s *Store) setUp() error {
	var version int
	if err := s.wdb.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("it was made by a newer berth-leadline (its format is %d, this one writes %d)", version, schemaVersion)
	}
	for _, q := range []string{
		// Before the first table: a file can only be told at its birth to
		// give deleted pages back, which is what lets a prune shrink it.
		`PRAGMA auto_vacuum = INCREMENTAL`,
		// Readers and the writer side by side.
		`PRAGMA journal_mode = WAL`,
		schema,
		fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion),
	} {
		if _, err := s.wdb.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// DB is the database itself, for reading: a test's own questions. It sees
// every row written; call Flush first for the rows still on their way.
func (s *Store) DB() *sql.DB { return s.rdb }

// Close writes what is waiting and closes the database. Rows added before
// Close are kept; adding after it is an error.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		close(s.stop)
		s.bg.Wait()
		s.pmu.Lock()
		s.closed = true
		s.pmu.Unlock()
		err := s.write()
		s.wmu.Lock()
		// Fold the write-ahead log into the file, so what is left behind
		// is the one file.
		err = errors.Join(err, drain(context.Background(), s.wdb, `PRAGMA wal_checkpoint(TRUNCATE)`))
		s.wmu.Unlock()
		s.closeErr = errors.Join(err, s.rdb.Close(), s.wdb.Close())
	})
	return s.closeErr
}

var errClosed = errors.New("store is closed")

// AddSpans takes spans in. They are written within Options.FlushEvery, and
// before the next read, so the next question sees them.
func (s *Store) AddSpans(spans []Span) error {
	if len(spans) == 0 {
		return nil
	}
	s.pmu.Lock()
	if s.closed {
		s.pmu.Unlock()
		return errClosed
	}
	s.waitSpans = append(s.waitSpans, spans...)
	return s.took(len(spans))
}

// AddLogs takes log records in, as AddSpans does spans.
func (s *Store) AddLogs(logs []Log) error {
	if len(logs) == 0 {
		return nil
	}
	s.pmu.Lock()
	if s.closed {
		s.pmu.Unlock()
		return errClosed
	}
	s.waitLogs = append(s.waitLogs, logs...)
	return s.took(len(logs))
}

// took is the end of an Add of n rows, entered holding pmu.
func (s *Store) took(n int) error {
	waiting := len(s.waitSpans) + len(s.waitLogs)
	s.added.Add(int64(n))
	s.pmu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	if waiting >= maxWaiting {
		s.await(0)
	}
	return nil
}

// Flush returns once every row added before the call is in the database.
func (s *Store) Flush() { s.await(0) }

// sync is Flush for a read about to run. It costs two atomic loads when
// nothing is waiting, which is nearly always.
func (s *Store) sync() { s.await(s.readWait) }

// await waits for the flusher to write every row added so far, and tells it
// not to linger. With a limit it gives up after that long.
func (s *Store) await(limit time.Duration) {
	want := s.added.Load()
	if s.written.Load() >= want {
		return
	}
	var late <-chan time.Time
	if limit > 0 {
		t := time.NewTimer(limit)
		defer t.Stop()
		late = t.C
	}
	for {
		s.pmu.Lock()
		flushed := s.flushed
		s.pmu.Unlock()
		if s.written.Load() >= want {
			return
		}
		select {
		case s.urgent <- struct{}{}:
		default:
		}
		select {
		case <-flushed:
		case <-late:
			return
		case <-s.stop:
			return // Close writes what is left
		}
	}
}

// flushLoop is the flusher. It writes waiting rows a short while after the
// first of them arrives, so a burst of exports becomes one commit, and at
// once when a read or a full buffer is waiting on it. It sleeps while
// nothing arrives.
func (s *Store) flushLoop() {
	defer s.bg.Done()
	for {
		select {
		case <-s.stop:
			return
		case <-s.wake:
			linger := time.NewTimer(s.flushEvery)
			select {
			case <-s.stop:
				linger.Stop()
				return
			case <-s.urgent:
				linger.Stop()
			case <-linger.C:
			}
			if err := s.write(); err != nil {
				slog.Error("writing rows", "err", err)
			}
		}
	}
}

// write moves every waiting row into the database and wakes whoever was
// waiting for that.
func (s *Store) write() error {
	s.wmu.Lock()
	s.pmu.Lock()
	spans, logs, upTo := s.waitSpans, s.waitLogs, s.added.Load()
	s.waitSpans, s.waitLogs = nil, nil
	s.pmu.Unlock()

	var err error
	if len(spans)+len(logs) > 0 {
		err = s.insert(spans, logs)
	}
	s.wmu.Unlock()

	if len(spans)+len(logs) > 0 {
		s.gen.Add(1)
	}
	s.written.Store(upTo)
	s.announce()
	return err
}

const (
	insertSpan = `INSERT INTO spans (trace_id, span_id, parent_id, service, name, kind, start_ns, duration_ns,
	status, status_message, db_system, db_statement, db_fingerprint, http_method, http_route, http_status,
	attrs, db_role, entry, user_id, db_server, via, db_shape)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	insertLog = `INSERT INTO logs (ts_ns, service, severity, severity_num, body, trace_id, span_id, attrs, kind, user_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
)

// insert writes one batch in one transaction: the rows, and the
// corrections their arrival makes to rows already stored. Holding wmu.
func (s *Store) insert(spans []Span, logs []Log) (err error) {
	// What each span counts as, and which stored rows that changes.
	marks, fixes := s.link.place(spans, time.Now().UnixNano())
	tx, err := s.wdb.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	// A row that is refused is left out and said; the rest are kept.
	var refused error
	if len(spans) > 0 {
		stmt, err := tx.Prepare(insertSpan)
		if err != nil {
			return err
		}
		for i := range spans {
			sp := &spans[i]
			via := viaOTLP
			if sp.Source == SourcePostgresWire {
				via = viaWire
			}
			if _, err := stmt.Exec(
				sp.TraceID, sp.SpanID, sp.ParentID,
				sp.Service, sp.Name, sp.Kind,
				sp.StartNs, sp.DurationNs,
				sp.Status, sp.StatusMessage,
				sp.DBSystem, sp.DBStatement, sp.DBFingerprint,
				sp.HTTPMethod, sp.HTTPRoute, sp.HTTPStatus,
				jsonObject(sp.Attrs),
				marks[i].role, marks[i].entry, sp.UserID, sp.DBServer, via, shapeOf(sp.DBSystem, sp.DBFingerprint),
			); err != nil && refused == nil {
				refused = fmt.Errorf("store span: %w", err)
			}
		}
		stmt.Close()
	}
	if len(logs) > 0 {
		stmt, err := tx.Prepare(insertLog)
		if err != nil {
			return err
		}
		for i := range logs {
			l := &logs[i]
			if _, err := stmt.Exec(
				l.TsNs, l.Service,
				l.Severity, l.SeverityNum,
				l.Body, l.TraceID, l.SpanID,
				jsonObject(l.Attrs),
				l.Kind, l.UserID,
			); err != nil && refused == nil {
				refused = fmt.Errorf("store log: %w", err)
			}
		}
		stmt.Close()
	}
	// After the rows: a correction may be to a row of this batch's
	// neighbours, never to one of its own.
	if err := correct(tx, fixes); err != nil {
		refused = errors.Join(refused, err)
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return refused
}

// shapeOf is the number a database call's shape goes by: a hash of its
// system and its fingerprint, the same in every helper and every run, and
// 0 for a span that is no database call. Two shapes with one number would
// be shown as one query; with 64 bits that is not going to happen.
func shapeOf(system, fingerprint string) int64 {
	if system == "" {
		return 0
	}
	h := fnv.New64a()
	h.Write([]byte(system))
	h.Write([]byte{0})
	h.Write([]byte(fingerprint))
	if sum := int64(h.Sum64()); sum != 0 {
		return sum
	}
	return 1
}

// jsonObject is attrs as it is stored: always something json_extract and
// the API can read.
func jsonObject(attrs string) string {
	if attrs == "" {
		return "{}"
	}
	return attrs
}

// correct applies the linker's corrections to stored rows. Each names its
// row by start time and id, which the spans_start index finds at once.
func correct(tx *sql.Tx, fixes []fix) error {
	var errs error
	for _, f := range fixes {
		var err error
		switch f.kind {
		case fixRehome:
			_, err = tx.Exec(`UPDATE spans SET trace_id = ?, parent_id = ?, service = ? WHERE start_ns = ? AND span_id = ?`,
				f.trace, f.parent, f.service, f.at, f.id)
		case fixRole:
			_, err = tx.Exec(`UPDATE spans SET db_role = 3 WHERE start_ns = ? AND span_id = ?`, f.at, f.id)
		case fixEntry:
			_, err = tx.Exec(`UPDATE spans SET entry = 0 WHERE start_ns = ? AND span_id = ?`, f.at, f.id)
		case fixMove:
			_, err = tx.Exec(`UPDATE spans SET trace_id = ?1, service = ?2,
	parent_id = CASE WHEN span_id = ?3 THEN ?4 ELSE parent_id END
WHERE trace_id = ?5 AND start_ns >= ?6`, f.trace, f.service, f.frame, f.parent, f.id, f.at)
		}
		errs = errors.Join(errs, err)
	}
	if errs != nil {
		return fmt.Errorf("correct stored spans: %w", errs)
	}
	return nil
}

// announce wakes everyone in await to look again.
func (s *Store) announce() {
	s.pmu.Lock()
	close(s.flushed)
	s.flushed = make(chan struct{})
	s.pmu.Unlock()
}

// query is a read: of every row added before it was asked.
func (s *Store) query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	s.sync()
	return s.rdb.QueryContext(ctx, q, args...)
}

// queryRow is query for one row.
func (s *Store) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	s.sync()
	return s.rdb.QueryRowContext(ctx, q, args...)
}
