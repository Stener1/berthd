package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// ServiceTotals is one service and how much of each signal the store holds
// for it. The counts are of what is stored now, so they fall as old data
// is dropped.
type ServiceTotals struct {
	Name  string
	Spans int64
	// WireSpans are those of Spans the Postgres proxy recorded. A span the
	// proxy recorded for a statement the app also reported is filed under
	// the app's own name, and counted here under it.
	WireSpans int64
	Logs      int64
	// LastNs is the time of its newest span or log.
	LastNs int64
}

// totalsCache holds the last per-service totals and when they were taken.
type totalsCache struct {
	mu     sync.Mutex
	known  bool
	gen    int64 // the store's gen when they were counted
	at     time.Time
	took   time.Duration
	rows   []ServiceTotals
	traces int64
}

const (
	// quick is how long a count may take and still be done every time it
	// is asked for.
	quick = 5 * time.Millisecond
	// Past that, the totals are counted again when the store has changed,
	// but no more often than twenty times what the last count took, and
	// never less often than this.
	totalsMaxEvery = 10 * time.Second
)

// totals counts what each service holds, and the traces there are. SQLite
// keeps no row counts, so these are read off the indexes from end to end:
// a millisecond or two on a working day's data, more on a full store. They
// are counted again only when rows have changed, and on a large store no
// more often than keeps the counting to a twentieth of a core. The first
// rows always show at once.
func (s *Store) totals(ctx context.Context) ([]ServiceTotals, int64, error) {
	s.sync()
	c := &s.counted
	c.mu.Lock()
	defer c.mu.Unlock()
	gen := s.gen.Load()
	if c.known && c.gen == gen {
		return c.rows, c.traces, nil
	}
	if c.known && len(c.rows) > 0 && c.took > quick && time.Since(c.at) < min(20*c.took, totalsMaxEvery) {
		return c.rows, c.traces, nil
	}

	began := time.Now()
	by := map[string]*ServiceTotals{}
	of := func(name string) *ServiceTotals {
		if by[name] == nil {
			by[name] = &ServiceTotals{Name: name}
		}
		return by[name]
	}
	rows, err := s.rdb.QueryContext(ctx, `SELECT service, via, count(*), max(start_ns) FROM spans INDEXED BY spans_service GROUP BY service, via`)
	if err != nil {
		return nil, 0, err
	}
	for rows.Next() {
		var name string
		var via int
		var n, last int64
		if err := rows.Scan(&name, &via, &n, &last); err != nil {
			rows.Close()
			return nil, 0, err
		}
		t := of(name)
		t.Spans += n
		if via == viaWire {
			t.WireSpans += n
		}
		t.LastNs = max(t.LastNs, last)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	rows, err = s.rdb.QueryContext(ctx, `SELECT service, count(*), max(ts_ns) FROM logs INDEXED BY logs_service GROUP BY service`)
	if err != nil {
		return nil, 0, err
	}
	for rows.Next() {
		var name string
		var n, last int64
		if err := rows.Scan(&name, &n, &last); err != nil {
			rows.Close()
			return nil, 0, err
		}
		t := of(name)
		t.Logs = n
		t.LastNs = max(t.LastNs, last)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	var traces int64
	if err := s.rdb.QueryRowContext(ctx, `SELECT count(DISTINCT trace_id) FROM spans INDEXED BY spans_trace`).Scan(&traces); err != nil {
		return nil, 0, err
	}
	out := make([]ServiceTotals, 0, len(by))
	for _, t := range by {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	c.known, c.gen, c.rows, c.traces = true, gen, out, traces
	c.at, c.took = time.Now(), time.Since(began)
	return out, traces, nil
}

// Services lists every service the store holds rows for, by name.
func (s *Store) Services(ctx context.Context) ([]ServiceTotals, error) {
	rows, _, err := s.totals(ctx)
	return rows, err
}

type Summary struct {
	Spans    int64    `json:"spans"`
	Logs     int64    `json:"logs"`
	Traces   int64    `json:"traces"`
	Services []string `json:"services"`
}

// Summary counts what the store holds and names the services seen.
func (s *Store) Summary(ctx context.Context) (*Summary, error) {
	rows, traces, err := s.totals(ctx)
	if err != nil {
		return nil, err
	}
	out := &Summary{Traces: traces, Services: []string{}}
	for _, r := range rows {
		out.Spans += r.Spans
		out.Logs += r.Logs
		out.Services = append(out.Services, r.Name)
	}
	return out, nil
}

// Worktree is what a service says of where it runs: the Shipyard project it
// is a checkout of, the worktree's own name, its branch and the box it is
// on. An app says so in the resource of its telemetry (the shipyard.*
// attributes the attach helper sets), and whoever opens a proxy for a
// worktree may say so too.
type Worktree struct {
	Service  string
	Project  string
	Worktree string
	Branch   string
	Box      string
}

func (w Worktree) empty() bool {
	return w.Project == "" && w.Worktree == "" && w.Branch == "" && w.Box == ""
}

// NoteWorktrees remembers what services said of their worktrees. One that
// says nothing is passed over; one that says what is already known costs a
// map lookup.
func (s *Store) NoteWorktrees(list []Worktree) error {
	var changed []Worktree
	s.kmu.Lock()
	for _, w := range list {
		w.Service = strings.TrimSpace(w.Service)
		if w.Service == "" || w.empty() || s.known[w.Service] == w {
			continue
		}
		s.known[w.Service] = w
		changed = append(changed, w)
	}
	s.kmu.Unlock()
	if len(changed) == 0 {
		return nil
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	now := time.Now().UnixNano()
	for _, w := range changed {
		if _, err := s.wdb.Exec(`
INSERT INTO worktrees (service, project, worktree, branch, box, seen_ns) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (service) DO UPDATE SET project = excluded.project, worktree = excluded.worktree,
	branch = excluded.branch, box = excluded.box, seen_ns = excluded.seen_ns`,
			w.Service, w.Project, w.Worktree, w.Branch, w.Box, now); err != nil {
			return err
		}
	}
	s.gen.Add(1)
	return nil
}

// Worktrees is what is known of each service's worktree, by service.
func (s *Store) Worktrees() map[string]Worktree {
	s.kmu.Lock()
	defer s.kmu.Unlock()
	out := make(map[string]Worktree, len(s.known))
	for k, v := range s.known {
		out[k] = v
	}
	return out
}

// loadWorktrees reads the table into memory: at the start, and after rows
// of it were deleted.
func (s *Store) loadWorktrees() error {
	rows, err := s.rdb.Query(`SELECT service, project, worktree, branch, box FROM worktrees`)
	if err != nil {
		return err
	}
	defer rows.Close()
	known := map[string]Worktree{}
	for rows.Next() {
		var w Worktree
		if err := rows.Scan(&w.Service, &w.Project, &w.Worktree, &w.Branch, &w.Box); err != nil {
			return err
		}
		known[w.Service] = w
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.kmu.Lock()
	s.known = known
	s.kmu.Unlock()
	return nil
}
