// Package server is berth-database's HTTP surface: the routes that browse
// and edit a worktree's own development database, and the two the box
// daemon calls to say which database that is. API.md, one folder up,
// describes every route; the Database plugin's tab is written from that
// file, so change them together.
//
// Nothing here knows a dialect. A request is checked, the connection's
// engine (package database) does the work, and what it wrote is logged by
// its shape.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cosscom/shipyard/internal/database"
	"github.com/cosscom/shipyard/internal/version"
)

const (
	// Version is the API's own number, in /api/health. It goes up when a
	// route or a field is taken away or changes meaning, not when one is
	// added.
	Version = 1
	// WritesFor is how long writes stay on for a connection once they are
	// turned on.
	WritesFor = 15 * time.Minute

	maxAttachBody = 256 << 10
	// maxBody bounds a statement, or an edit with a document in a cell.
	maxBody = 4 << 20
)

// Options is what a Server is made with.
type Options struct {
	// Registry is the engines this helper was built with.
	Registry *database.Registry
	// Token is what every route but /api/health asks for.
	Token string
	// Dir is where connections.json is kept; "" keeps the connections in
	// memory only.
	Dir string
	// Timeout stops a statement; zero is database.DefaultTimeout.
	Timeout time.Duration
	// Now is the clock; nil is time.Now. Tests move it.
	Now func() time.Time
}

type Server struct {
	reg     *database.Registry
	token   []byte
	timeout time.Duration
	now     func() time.Time
	file    string

	mu     sync.Mutex
	conns  []*connection
	closed bool
}

// New makes a server, with the connections the last one was told of.
func New(o Options) (*Server, error) {
	if o.Token == "" {
		return nil, errors.New("the helper needs a token: nothing it serves is for whoever asks")
	}
	if o.Registry == nil {
		o.Registry = database.NewRegistry()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout <= 0 {
		o.Timeout = database.DefaultTimeout
	}
	s := &Server{reg: o.Registry, token: []byte(o.Token), timeout: o.Timeout, now: o.Now}
	if err := s.load(o.Dir); err != nil {
		return nil, err
	}
	return s, nil
}

// Close ends every connection to every database.
func (s *Server) Close() {
	s.mu.Lock()
	conns := s.conns
	s.conns, s.closed = nil, true
	s.mu.Unlock()
	for _, c := range conns {
		c.close()
	}
}

// routes is a mux that remembers what was registered on it: API.md is
// held to that list by a test.
type routes struct {
	*http.ServeMux
	patterns []string
}

func (r *routes) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	r.patterns = append(r.patterns, pattern)
	r.ServeMux.HandleFunc(pattern, handler)
}

// Handler serves the API.
func (s *Server) Handler() http.Handler {
	h, _ := s.handler()
	return h
}

func (s *Server) handler() (http.Handler, []string) {
	mux := &routes{ServeMux: http.NewServeMux()}
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("POST /api/attach", s.attach)
	mux.HandleFunc("POST /api/detach", s.detach)
	mux.HandleFunc("GET /api/db/connections", s.connections)
	mux.HandleFunc("GET /api/db/{conn}/schema", s.schema)
	mux.HandleFunc("POST /api/db/{conn}/rows", s.rows)
	mux.HandleFunc("POST /api/db/{conn}/query", s.query)
	mux.HandleFunc("POST /api/db/{conn}/edit", s.edit)
	mux.HandleFunc("POST /api/db/{conn}/writes", s.writes)
	// Not through the list: this is every route there is not.
	mux.ServeMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, database.NotFound("There is no such route.").Hinted("internal/database/API.md lists them."))
	})
	return s.guard(mux), mux.patterns
}

// Health is the answer of /api/health.
type Health struct {
	OK bool `json:"ok"`
	// Name is "berth-database": how to tell this helper from whatever else
	// may answer on a port.
	Name    string `json:"name"`
	Version string `json:"version"`
	API     int    `json:"api"`
	// Engines are the kinds of database this build opens.
	Engines []string `json:"engines"`
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, Health{OK: true, Name: "berth-database", Version: version.Version, API: Version, Engines: s.reg.Names()})
}

// guard is who may ask. The helper can run SQL, so it answers one caller:
// the box daemon, which read the token from the helper's ready file (mode
// 0600: this user's alone) and sends it with everything it relays.
//
//   - The Host must be loopback, which stops DNS rebinding.
//   - A request from a page is refused, whatever the page: Shipyard's own
//     window reaches the helper through the box daemon, which sends no
//     Origin. There are no CORS headers here to get wrong.
//   - Every route but /api/health needs the token.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			writeProblem(w, forbidden("The helper only answers on localhost."))
			return
		}
		if r.Header.Get("Origin") != "" {
			writeProblem(w, forbidden("The helper does not answer pages: it is reached through Shipyard's box daemon."))
			return
		}
		if !(r.Method == http.MethodGet && r.URL.Path == "/api/health") && !s.authorized(r) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeProblem(w, &database.Problem{Status: http.StatusUnauthorized, Code: "unauthorized",
				Message: "This route needs the helper's token.",
				Hint:    "The box daemon sends it with what it relays: ask through the box, not the helper."})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	return ok && strings.EqualFold(scheme, "Bearer") &&
		subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), s.token) == 1
}

func forbidden(message string) *database.Problem {
	return &database.Problem{Status: http.StatusForbidden, Code: "forbidden", Message: message}
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// ---- the editor's routes ----

func (s *Server) schema(w http.ResponseWriter, r *http.Request) {
	c, db, ok := s.open(w, r)
	if !ok {
		return
	}
	out, err := db.Schema(r.Context())
	s.respond(w, c, out, err)
}

func (s *Server) rows(w http.ResponseWriter, r *http.Request) {
	c, db, ok := s.open(w, r)
	if !ok {
		return
	}
	var in database.RowsRequest
	if !readBody(w, r, &in, maxBody) {
		return
	}
	if p := in.Check(c.engine.Capabilities()); p != nil {
		writeProblem(w, p)
		return
	}
	out, err := db.Rows(r.Context(), in)
	s.respond(w, c, out, err)
}

func (s *Server) query(w http.ResponseWriter, r *http.Request) {
	c, db, ok := s.open(w, r)
	if !ok {
		return
	}
	var in database.QueryRequest
	if !readBody(w, r, &in, maxBody) {
		return
	}
	if p := in.Check(); p != nil {
		writeProblem(w, p)
		return
	}
	// Whether it may write is this helper's to say, from its own clock:
	// nothing in the request can.
	in.Write = s.writesOn(c)
	out, err := db.Query(r.Context(), in)
	if in.Write {
		// A statement that could write is logged as what it was, never
		// as what it held.
		attrs := []any{"source", c.Source, "connection", c.ID, "shape", c.engine.Shape(in.SQL)}
		if err != nil {
			slog.Info("statement with writes on: refused", append(attrs, "code", codeOf(err))...)
		} else {
			slog.Info("statement with writes on", append(attrs, "command", out.Command, "rows", out.RowCount)...)
		}
	}
	s.respond(w, c, out, err)
}

func (s *Server) edit(w http.ResponseWriter, r *http.Request) {
	c, db, ok := s.open(w, r)
	if !ok {
		return
	}
	var in database.EditRequest
	if !readBody(w, r, &in, maxBody) {
		return
	}
	caps := c.engine.Capabilities()
	if p := in.Check(caps); p != nil {
		writeProblem(w, p)
		return
	}
	if !caps.Writes {
		writeProblem(w, database.Bad("This kind of database cannot be edited here."))
		return
	}
	if !s.writesOn(c) {
		writeProblem(w, database.ReadOnly())
		return
	}
	out, err := db.Edit(r.Context(), in)
	table, columns := in.Shape(c.engine.QuoteIdent)
	attrs := []any{"source", c.Source, "connection", c.ID, "table", table, "columns", strings.Join(columns, ", "), "changes", len(in.Changes)}
	if err != nil {
		slog.Info("edit: refused, nothing changed", append(attrs, "code", codeOf(err))...)
	} else {
		slog.Info("edit", append(attrs, "updated", out.Updated, "inserted", out.Inserted, "deleted", out.Deleted)...)
	}
	s.respond(w, c, out, err)
}

// Writes is the answer of POST /api/db/{conn}/writes.
type Writes struct {
	// WritesUntil is when writes go off again, in milliseconds since 1970;
	// left out while they are off.
	WritesUntil int64 `json:"writes_until,omitempty"`
}

func (s *Server) writes(w http.ResponseWriter, r *http.Request) {
	c, ok := s.find(w, r)
	if !ok {
		return
	}
	var in struct {
		On *bool `json:"on"`
	}
	if !readBody(w, r, &in, maxAttachBody) {
		return
	}
	if in.On == nil {
		writeProblem(w, database.Bad("on is required: true or false."))
		return
	}
	if *in.On && !c.engine.Capabilities().Writes {
		writeProblem(w, database.Bad("This kind of database cannot be edited here."))
		return
	}
	s.mu.Lock()
	c.writesUntil = time.Time{}
	if *in.On {
		c.writesUntil = s.now().Add(WritesFor)
	}
	until := c.writesUntil
	s.mu.Unlock()
	out := Writes{}
	if *in.On {
		out.WritesUntil = until.UnixMilli()
		slog.Info("writes on", "source", c.Source, "connection", c.ID, "for", WritesFor.String())
	} else {
		slog.Info("writes off", "source", c.Source, "connection", c.ID)
	}
	writeJSON(w, out)
}

// respond answers with what an engine gave, or with what went wrong.
func (s *Server) respond(w http.ResponseWriter, c *connection, v any, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, v)
}

func codeOf(err error) string {
	var p *database.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	return "failed"
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	// What a database holds is not for a cache to keep.
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

// writeProblem answers with an error a person can act on:
// {"error":{"code":"…","message":"…","hint":"…"}}.
func writeProblem(w http.ResponseWriter, p *database.Problem) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	status := p.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": p})
}

// fail answers for an error. A Problem is the caller's to read. Anything
// else is the helper's own fault: it is logged here, by its kind and not
// its text (a database's error can quote a row), and not sent.
func fail(w http.ResponseWriter, err error) {
	var p *database.Problem
	if errors.As(err, &p) {
		writeProblem(w, p)
		return
	}
	// Whoever asked has gone, or the helper is stopping: nothing failed.
	if !errors.Is(err, context.Canceled) {
		slog.Error("request failed", "kind", fmt.Sprintf("%T", err))
	}
	writeProblem(w, &database.Problem{Status: http.StatusInternalServerError, Code: "failed",
		Message: "The helper could not answer.", Hint: "Its log says what kind of error it was."})
}

// readBody decodes a JSON body, writing the error itself on failure.
func readBody(w http.ResponseWriter, r *http.Request, into any, most int64) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, most)).Decode(into)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeProblem(w, &database.Problem{Status: http.StatusRequestEntityTooLarge, Code: "bad_request", Message: "The request is too large."})
			return false
		}
		writeProblem(w, database.Bad("The request body is not JSON the helper can read."))
		return false
	}
	return true
}
