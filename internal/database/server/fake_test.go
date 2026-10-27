package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/database"
)

// fakeEngine is a database that is nothing like Postgres: no schemas, one
// operator, square brackets around its names, a made-up dialect. The HTTP
// layer and the registry are tested with it alone, which is how they are
// shown to depend on no engine in particular.
type fakeEngine struct {
	name   string
	writes bool

	mu    sync.Mutex
	conns []*fakeConn
}

func (e *fakeEngine) Name() string      { return e.name }
func (e *fakeEngine) Schemes() []string { return []string{e.name} }
func (e *fakeEngine) Capabilities() database.Capabilities {
	return database.Capabilities{Dialect: "fakeql", Schemas: false, Operators: []string{"=", "is_null"},
		Kinds: []string{database.KindTable}, Writes: e.writes, Transactions: false}
}
func (e *fakeEngine) QuoteIdent(name string) string { return "[" + name + "]" }
func (e *fakeEngine) Shape(sql string) string {
	return strings.ToUpper(strings.Fields(sql)[0]) + " <" + strconv.Itoa(len(strings.Fields(sql))-1) + " words>"
}

func (e *fakeEngine) Target(raw string) (database.Target, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return database.Target{}, errors.New("it cannot be read")
	}
	port, _ := strconv.Atoi(u.Port())
	return database.Target{Host: u.Hostname(), Port: port, Database: strings.TrimPrefix(u.Path, "/"), User: u.User.Username()}, nil
}

func (e *fakeEngine) Open(raw string, o database.Options) (database.Conn, error) {
	c := &fakeConn{url: raw, timeout: o.Timeout}
	e.mu.Lock()
	e.conns = append(e.conns, c)
	e.mu.Unlock()
	return c, nil
}

// last is the connection opened most recently.
func (e *fakeEngine) last() *fakeConn {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.conns) == 0 {
		return nil
	}
	return e.conns[len(e.conns)-1]
}

func (e *fakeEngine) opened() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.conns)
}

type fakeConn struct {
	url     string
	timeout time.Duration

	mu      sync.Mutex
	closed  bool
	rows    database.RowsRequest
	queries []database.QueryRequest
	edits   []database.EditRequest
}

func (c *fakeConn) Schema(context.Context) (*database.Schema, error) {
	return &database.Schema{Schemas: []database.SchemaInfo{{Name: "", Tables: []database.Table{{Name: "things", Kind: database.KindTable,
		Columns: []database.TableColumn{{Name: "k", Type: "number", PrimaryKey: true}}, PrimaryKey: []string{"k"},
		Indexes: []database.Index{}, ForeignKeys: []database.ForeignKey{}}}}}}, nil
}

func (c *fakeConn) Rows(_ context.Context, in database.RowsRequest) (*database.RowsResult, error) {
	c.mu.Lock()
	c.rows = in
	c.mu.Unlock()
	g := database.NewGrid()
	g.Add([]any{database.Int("1"), "one"})
	g.Add([]any{database.Int("9007199254740993"), strings.Repeat("x", database.MaxCell+5)})
	return &database.RowsResult{Columns: []database.Column{{Name: "k", Type: "number"}, {Name: "v", Type: "words"}},
		Rows: g.Rows, Cut: g.Cut, PrimaryKey: []string{"k"}, RowsEstimate: 2}, nil
}

func (c *fakeConn) Query(_ context.Context, in database.QueryRequest) (*database.QueryResult, error) {
	c.mu.Lock()
	c.queries = append(c.queries, in)
	c.mu.Unlock()
	if strings.HasPrefix(in.SQL, "write") && !in.Write {
		return nil, database.ReadOnly()
	}
	if strings.HasPrefix(in.SQL, "explode") {
		return nil, errors.New("an error with a row in it: s3cret-cell")
	}
	g := database.NewGrid()
	g.Add([]any{in.SQL, in.Write})
	return &database.QueryResult{Columns: []database.Column{{Name: "sql", Type: "words"}, {Name: "write", Type: "flag"}},
		Rows: g.Rows, Cut: g.Cut, RowCount: 1, Command: strings.ToUpper(strings.Fields(in.SQL)[0]), Notices: []string{}}, nil
}

func (c *fakeConn) Edit(_ context.Context, in database.EditRequest) (*database.EditResult, error) {
	c.mu.Lock()
	c.edits = append(c.edits, in)
	c.mu.Unlock()
	out := &database.EditResult{}
	for _, ch := range in.Changes {
		if v, ok := ch.Key["k"]; ok && v.Text == "404" {
			return nil, database.Conflict("Change 1: the row is not there any more.")
		}
		switch ch.Op {
		case "update":
			out.Updated++
		case "insert":
			out.Inserted++
		case "delete":
			out.Deleted++
		}
	}
	return out, nil
}

func (c *fakeConn) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}

func (c *fakeConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *fakeConn) lastQuery() database.QueryRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queries[len(c.queries)-1]
}

// rig is a server over the fake engines, with a clock that tests move and
// a log that tests read.
type rig struct {
	t      *testing.T
	srv    *Server
	h      http.Handler
	fake   *fakeEngine
	frozen *fakeEngine
	dir    string
	clock  time.Time
	log    *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

const testToken = "tok3n-0123456789abcdef"

func newRig(t *testing.T, dir string) *rig {
	t.Helper()
	r := &rig{t: t, dir: dir, clock: time.UnixMilli(1_791_754_000_000), log: &syncBuffer{},
		fake: &fakeEngine{name: "fake", writes: true}, frozen: &fakeEngine{name: "frozen"}}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(r.log, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	r.start()
	return r
}

// start makes the server, again from the same folder when it is called a
// second time: a restart.
func (r *rig) start() {
	r.t.Helper()
	if r.srv != nil {
		r.srv.Close()
	}
	srv, err := New(Options{Registry: database.NewRegistry(r.fake, r.frozen), Token: testToken, Dir: r.dir,
		Now: func() time.Time { return r.clock }})
	if err != nil {
		r.t.Fatal(err)
	}
	r.srv, r.h = srv, srv.Handler()
	r.t.Cleanup(srv.Close)
}

// call makes a request as the box daemon does: with the token, and no
// Origin. A body that is not a string is sent as its JSON.
func (r *rig) call(method, path string, body any) (int, string) {
	r.t.Helper()
	return r.send(method, path, body, map[string]string{"Authorization": "Bearer " + testToken})
}

func (r *rig) send(method, path string, body any, headers map[string]string) (int, string) {
	r.t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			r.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:4326"+path, reader)
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// attach attaches a command of acme/fix-orders with one database, and
// returns the connection's id.
func (r *rig) attach(env map[string]string) string {
	r.t.Helper()
	status, body := r.call("POST", "/api/attach", AttachRequest{Box: "devbox", Project: "acme", Worktree: "fix-orders", Command: "web", Env: env})
	var out Attached
	if status != 200 || json.Unmarshal([]byte(body), &out) != nil || len(out.Connections) != 1 {
		r.t.Fatalf("attach: %d %s", status, body)
	}
	return out.Connections[0]
}

func problemCode(body string) string {
	var p struct {
		Error struct{ Code, Message string }
	}
	json.Unmarshal([]byte(body), &p)
	return p.Error.Code
}
