package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Retention says how much the store keeps. Without it nothing is ever
// deleted and the file only grows.
type Retention struct {
	// MaxAge is how long a row is kept. Zero keeps rows whatever their age.
	MaxAge time.Duration
	// MaxBytes is how large the database may get before its oldest rows go
	// early. Zero is no cap.
	MaxBytes int64
	// Every is how often to look. Zero means once a minute.
	Every time.Duration
}

// aged is every table that grows with time, and the column that says when a
// row is from. A feature that adds such a table adds it here, or its rows
// outlive every span and log.
var aged = []struct{ table, column string }{
	{"spans", "start_ns"},
	{"logs", "ts_ns"},
}

// Pruned counts what a prune deleted.
type Pruned struct {
	Spans, Logs int64
}

func (p Pruned) rows() int64 { return p.Spans + p.Logs }

// pruneAtATime is how many rows one delete takes. Between two of them the
// flusher gets its turn, so ingest is never held up for longer than one.
const pruneAtATime = 20_000

// Prune deletes everything older than cutoff and gives the space back to
// the file system.
//
// The old rows are found from the time index and deleted a few thousand at
// a time, each lot in a transaction of its own, so rows keep arriving and
// reads keep being answered beside it. The pages they leave empty are then
// handed back (the file was made to allow that), so the file gets smaller.
func (s *Store) Prune(ctx context.Context, cutoff time.Time) (p Pruned, err error) {
	// Whatever went, went: a prune cut short has still changed the rows.
	defer func() {
		if p.rows() > 0 {
			s.gen.Add(1)
		}
	}()
	for _, t := range aged {
		n, err := s.deleteWhere(ctx, t.table, t.column+` < ?`, cutoff.UnixNano())
		switch t.table {
		case "spans":
			p.Spans = n
		case "logs":
			p.Logs = n
		}
		if err != nil {
			return p, fmt.Errorf("prune %s: %w", t.table, err)
		}
	}
	// A worktree that has not been heard of since before the cutoff, and
	// has nothing left, is forgotten with its rows.
	s.wmu.Lock()
	res, err := s.wdb.ExecContext(ctx, `
DELETE FROM worktrees WHERE seen_ns < ?
	AND NOT EXISTS (SELECT 1 FROM spans WHERE spans.service = worktrees.service)
	AND NOT EXISTS (SELECT 1 FROM logs WHERE logs.service = worktrees.service)`, cutoff.UnixNano())
	s.wmu.Unlock()
	if err != nil {
		return p, fmt.Errorf("prune worktrees: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		if err := s.loadWorktrees(); err != nil {
			return p, err
		}
	}
	if p.rows() == 0 {
		return p, nil
	}
	return p, s.giveBack(ctx)
}

// deleteWhere deletes the rows of table that match cond, in lots, and
// returns how many went.
func (s *Store) deleteWhere(ctx context.Context, table, cond string, args ...any) (int64, error) {
	var total int64
	for {
		s.wmu.Lock()
		res, err := s.wdb.ExecContext(ctx, `DELETE FROM `+table+` WHERE rowid IN (SELECT rowid FROM `+table+` WHERE `+cond+
			` LIMIT `+strconv.Itoa(pruneAtATime)+`)`, args...)
		s.wmu.Unlock()
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n < pruneAtATime {
			return total, nil
		}
	}
}

// giveBack returns the pages deletes left empty to the file system, and
// folds the write-ahead log into the file so it starts again from nothing.
func (s *Store) giveBack(ctx context.Context) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	// Readers in the middle of a question keep the log from being folded
	// all the way; then it is folded as far as it can be, which is no error.
	for _, q := range []string{`PRAGMA incremental_vacuum`, `PRAGMA wal_checkpoint(TRUNCATE)`} {
		if err := drain(ctx, s.wdb, q); err != nil {
			return err
		}
	}
	return nil
}

// drain runs a statement to its end. incremental_vacuum frees one page for
// each row it returns, so it has to be read to the last row to do its work.
func drain(ctx context.Context, db *sql.DB, q string) error {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	return rows.Close()
}

// Forgotten counts what Forget deleted.
type Forgotten struct {
	Spans int64 `json:"spans"`
	Logs  int64 `json:"logs"`
}

func (f Forgotten) rows() int64 { return f.Spans + f.Logs }

// Forget deletes everything filed under one service: its spans, its logs
// and what is known of its worktree. With a prefix it also takes the
// services whose names begin with it, which is how a source that files
// under several names ("acme/fix-orders" and "acme/fix-orders:worker")
// goes as one.
//
// It is the delete Prune does, by name and not by age, in the same lots, so
// ingest and reads carry on beside it. The rows stop being seen at once.
//
// Rows of the service that arrive after the call are kept: Forget is for a
// source that has stopped.
func (s *Store) Forget(ctx context.Context, service, prefix string) (f Forgotten, err error) {
	if service == "" {
		return f, nil
	}
	defer func() {
		if f.rows() > 0 {
			s.gen.Add(1)
		}
	}()
	// Rows on their way in belong to "before the call" too.
	s.Flush()
	cond, args := `service = ?`, []any{service}
	if prefix != "" {
		cond += ` OR substr(service, 1, ?) = ?`
		args = append(args, len(prefix), prefix)
	}
	if f.Spans, err = s.deleteWhere(ctx, "spans", cond, args...); err != nil {
		return f, fmt.Errorf("forget %s in spans: %w", service, err)
	}
	if f.Logs, err = s.deleteWhere(ctx, "logs", cond, args...); err != nil {
		return f, fmt.Errorf("forget %s in logs: %w", service, err)
	}
	s.wmu.Lock()
	_, err = s.wdb.ExecContext(ctx, `DELETE FROM worktrees WHERE `+cond, args...)
	s.wmu.Unlock()
	if err != nil {
		return f, fmt.Errorf("forget %s: %w", service, err)
	}
	if err := s.loadWorktrees(); err != nil {
		return f, err
	}
	if f.rows() == 0 {
		return f, nil
	}
	return f, s.giveBack(ctx)
}

// Size is how many bytes of the database hold data: its pages, less the
// ones a delete left empty and a prune has yet to hand back. Pages that are
// still only in the write-ahead log are counted, once.
func (s *Store) Size(ctx context.Context) (int64, error) {
	var pages, free, pageSize int64
	for q, into := range map[string]*int64{`PRAGMA page_count`: &pages, `PRAGMA freelist_count`: &free, `PRAGMA page_size`: &pageSize} {
		if err := s.rdb.QueryRowContext(ctx, q).Scan(into); err != nil {
			return 0, err
		}
	}
	return (pages - free) * pageSize, nil
}

// Storage is how much the store holds and how much it is allowed to.
type Storage struct {
	// SizeBytes is the data held: the pages of the database in use, which
	// is what the size cap is measured against. FileBytes is what the
	// database file and its write-ahead log take on disk; the two differ
	// by what is waiting in the log and what a delete has just freed.
	SizeBytes int64 `json:"size_bytes"`
	FileBytes int64 `json:"file_bytes"`
	// MaxBytes is the size past which the oldest data goes early, and
	// RetainMs how long a row is kept; 0 is no cap and for ever.
	MaxBytes int64   `json:"max_bytes"`
	RetainMs float64 `json:"retain_ms"`
	// OldestMs is the time of the oldest span or log held; 0 when there is
	// none.
	OldestMs float64 `json:"oldest_ms"`
}

// Storage reports what is kept. It reads the file's own bookkeeping and the
// ends of two indexes, so it is cheap to ask every few seconds.
func (s *Store) Storage(ctx context.Context) (*Storage, error) {
	out := &Storage{}
	if r := s.keep.Load(); r != nil {
		out.MaxBytes, out.RetainMs = r.MaxBytes, float64(r.MaxAge.Milliseconds())
	}
	s.sync()
	var err error
	if out.SizeBytes, err = s.Size(ctx); err != nil {
		return nil, err
	}
	for _, p := range []string{s.path, s.path + "-wal"} {
		if fi, err := os.Stat(p); err == nil {
			out.FileBytes += fi.Size()
		}
	}
	var spans, logs sql.NullInt64
	err = s.rdb.QueryRowContext(ctx, `SELECT (SELECT min(start_ns) FROM spans), (SELECT min(ts_ns) FROM logs)`).Scan(&spans, &logs)
	if err != nil {
		return nil, err
	}
	switch {
	case spans.Valid && logs.Valid:
		out.OldestMs = float64(min(spans.Int64, logs.Int64)) / 1e6
	case spans.Valid:
		out.OldestMs = float64(spans.Int64) / 1e6
	case logs.Valid:
		out.OldestMs = float64(logs.Int64) / 1e6
	}
	return out, nil
}

// Retain applies r now and then every r.Every, until the store closes. The
// first pass may have a lot to delete, after days switched off.
func (s *Store) Retain(r Retention) {
	s.keep.Store(&r)
	if r.MaxAge <= 0 && r.MaxBytes <= 0 {
		return
	}
	if r.Every <= 0 {
		r.Every = time.Minute
	}
	// A pass ends with the store: the first one after days switched off
	// may have a lot to delete, and Close must not wait for all of it.
	// What it has deleted by then stays deleted; the rest goes next time.
	ctx, cancel := context.WithCancel(context.Background())
	s.bg.Add(2)
	go func() {
		defer s.bg.Done()
		<-s.stop
		cancel()
	}()
	go func() {
		defer s.bg.Done()
		next := time.NewTimer(0)
		defer next.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-next.C:
			}
			if err := s.retain(ctx, r); err != nil && ctx.Err() == nil {
				slog.Error("retention", "err", err)
			}
			next.Reset(r.Every)
		}
	}()
}

// retain is one pass.
func (s *Store) retain(ctx context.Context, r Retention) error {
	if r.MaxAge > 0 {
		p, err := s.Prune(ctx, time.Now().Add(-r.MaxAge))
		if err != nil {
			return err
		}
		if p.rows() > 0 {
			slog.Info("dropped old data", "older_than", r.MaxAge.String(), "spans", p.Spans, "logs", p.Logs)
		}
	}
	if r.MaxBytes <= 0 {
		return nil
	}
	size, err := s.Size(ctx)
	if err != nil || size <= r.MaxBytes {
		return err
	}
	// Drop the oldest share of rows that should bring the size to nine
	// tenths of the cap. The share is measured in the spans' time, or the
	// logs' for an app that sends none; every table is cut at that time.
	share := min(0.95, max(0.05, 1-0.9*float64(r.MaxBytes)/float64(size)))
	cutoff, ok, err := s.quantileTime(ctx, share)
	if err != nil || !ok {
		return err
	}
	p, err := s.Prune(ctx, time.Unix(0, cutoff))
	if err != nil {
		return err
	}
	left, err := s.Size(ctx)
	if err != nil {
		return err
	}
	slog.Info("dropped the oldest data to stay under the size cap", "size_mb", size>>20, "cap_mb", r.MaxBytes>>20, "now_mb", left>>20,
		"older_than", time.Unix(0, cutoff).Format(time.RFC3339), "spans", p.Spans, "logs", p.Logs)
	return nil
}

// quantileTime is the time below which share of the rows lie: of the spans,
// or of the logs when there are no spans. It counts along the time index.
// The row at that place is itself to go, so the time is one past its own.
func (s *Store) quantileTime(ctx context.Context, share float64) (int64, bool, error) {
	for _, t := range aged {
		var n int64
		if err := s.queryRow(ctx, `SELECT count(*) FROM `+t.table).Scan(&n); err != nil {
			return 0, false, err
		}
		if n == 0 {
			continue
		}
		at := min(int64(float64(n)*share), n-1)
		var ns int64
		err := s.queryRow(ctx, `SELECT `+t.column+` FROM `+t.table+` ORDER BY `+t.column+` LIMIT 1 OFFSET ?`, at).Scan(&ns)
		if err != nil {
			return 0, false, err
		}
		return ns + 1, true, nil
	}
	return 0, false, nil
}

// ParseAge reads how long to keep data: a Go duration ("72h"), or days
// ("7d"). "0" and "off" mean forever.
func ParseAge(v string) (time.Duration, error) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" || v == "0" || v == "off" {
		return 0, nil
	}
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%q is not a number of days", v)
		}
		return time.Duration(n * 24 * float64(time.Hour)), nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%q is not a length of time like 72h or 7d", v)
	}
	return d, nil
}

// ParseSize reads a size cap: "2GB", "500MB", or plain bytes. "0" and
// "off" mean no cap.
func ParseSize(v string) (int64, error) {
	v = strings.TrimSpace(strings.ToUpper(v))
	if v == "" || v == "0" || v == "OFF" {
		return 0, nil
	}
	unit := int64(1)
	for _, u := range []struct {
		suffix string
		bytes  int64
	}{{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}, {"B", 1}} {
		if rest, ok := strings.CutSuffix(v, u.suffix); ok {
			v, unit = strings.TrimSpace(rest), u.bytes
			break
		}
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("not a size like 2GB or 500MB")
	}
	return int64(n * float64(unit)), nil
}
