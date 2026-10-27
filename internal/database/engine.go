// Package database is the engine-neutral half of berth-database, the helper
// that browses and edits a worktree's own development database: what an
// engine must be able to do, the shapes the API answers with, and the rules
// every engine shares (how a cell becomes JSON, what a request may ask for,
// which databases count as this machine's).
//
// An engine is one package beside this one (postgres is the first) and one
// call to Registry.Register. Nothing here, and nothing in the HTTP layer
// (package server), knows a dialect. API.md, in this folder, describes the
// routes and what a new engine has to implement.
package database

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Engine is one kind of database the editor can open.
type Engine interface {
	// Name is what a connection's "engine" says: "postgres".
	Name() string
	// Schemes are the URL schemes it opens, lower case: "postgres",
	// "postgresql". A driver suffix (postgresql+psycopg) and a jdbc: prefix
	// are taken off before the scheme is looked up.
	Schemes() []string
	// Capabilities says what the UI may offer for its connections.
	Capabilities() Capabilities
	// Target reads a URL, without dialling: where it points and as whom.
	// The error it returns must not repeat the URL.
	Target(url string) (Target, error)
	// Open makes a Conn for a URL Target accepted. It need not dial yet.
	Open(url string, o Options) (Conn, error)
	// QuoteIdent writes a name as an identifier of this dialect.
	QuoteIdent(name string) string
	// Shape is a statement with every value taken out, for the log: a write
	// is logged by what it was, never by what it held.
	Shape(sql string) string
}

// Conn is one database, opened. It keeps a small pool of its own, and every
// method may be called at once from several requests.
//
// What a Conn must hold to, whatever its dialect: identifiers are quoted by
// the engine and never pasted in from a request; values go as parameters;
// Query and Rows change nothing unless QueryRequest.Write says so, and that
// is enforced by the database itself (a read-only transaction), not by
// reading the statement; a statement is stopped at Options.Timeout by the
// database; cells are made with Int, Float and Grid, so that every engine
// answers in the same JSON.
type Conn interface {
	Schema(ctx context.Context) (*Schema, error)
	Rows(ctx context.Context, in RowsRequest) (*RowsResult, error)
	Query(ctx context.Context, in QueryRequest) (*QueryResult, error)
	// Edit applies every change in one transaction, or none of them.
	Edit(ctx context.Context, in EditRequest) (*EditResult, error)
	// Close ends its connections, those in the middle of a statement too.
	Close()
}

// Options is what a Conn is opened with.
type Options struct {
	// Timeout stops a statement, in the database. Zero is DefaultTimeout.
	Timeout time.Duration
	// MaxConns is the most connections the Conn keeps. Zero is 4.
	MaxConns int
}

// DefaultTimeout is how long a statement may run when nobody says.
const DefaultTimeout = 10 * time.Second

// Capabilities is what the UI reads in place of assuming a dialect.
type Capabilities struct {
	// Dialect names the SQL for an editor's highlighting: "postgresql".
	Dialect string `json:"dialect"`
	// Schemas says tables live in named schemas. Without it there is one
	// schema, named "".
	Schemas bool `json:"schemas"`
	// Operators are the filter operators Rows takes.
	Operators []string `json:"operators"`
	// Kinds are the kinds of table Schema may list.
	Kinds []string `json:"kinds"`
	// Writes says Edit, and Query with Write, can work at all.
	Writes bool `json:"writes"`
	// Transactions says an Edit's changes are applied together or not at all.
	Transactions bool `json:"transactions"`
}

// Target is where a URL points: never its password.
type Target struct {
	// Host is a host name or address, or the folder of a unix socket when
	// Socket is set.
	Host     string
	Port     int
	Socket   bool
	Database string
	User     string
}

// Local reports whether a target is on this machine: a unix socket, or a
// loopback address. The editor opens nothing else: it is for a worktree's
// own development database, never one shared with anybody.
func (t Target) Local() bool {
	return t.Socket || LocalHost(t.Host)
}

// Registry is the engines a helper was built with, by URL scheme.
type Registry struct {
	byScheme map[string]Engine
	byName   map[string]Engine
}

// NewRegistry makes a registry of the engines given.
func NewRegistry(engines ...Engine) *Registry {
	r := &Registry{byScheme: map[string]Engine{}, byName: map[string]Engine{}}
	for _, e := range engines {
		r.Register(e)
	}
	return r
}

// Register adds an engine: the one call a new engine needs.
func (r *Registry) Register(e Engine) {
	r.byName[e.Name()] = e
	for _, scheme := range e.Schemes() {
		r.byScheme[strings.ToLower(scheme)] = e
	}
}

// For finds the engine that opens a URL, by its scheme.
func (r *Registry) For(url string) (Engine, bool) {
	scheme, ok := Scheme(url)
	if !ok {
		return nil, false
	}
	e, ok := r.byScheme[scheme]
	return e, ok
}

// Named finds an engine by its name.
func (r *Registry) Named(name string) (Engine, bool) {
	e, ok := r.byName[name]
	return e, ok
}

// Names lists the engines, sorted.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.byName))
	for name := range r.byName {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Scheme is a database URL's scheme as engines are registered under it:
// lower case, without a jdbc: prefix or a driver suffix, so that
// "jdbc:postgresql://…" and "postgresql+psycopg://…" are both "postgresql".
func Scheme(url string) (string, bool) {
	url = strings.TrimPrefix(strings.TrimSpace(url), "jdbc:")
	scheme, _, ok := strings.Cut(url, "://")
	if !ok || scheme == "" {
		return "", false
	}
	scheme, _, _ = strings.Cut(strings.ToLower(scheme), "+")
	for _, r := range scheme {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '.' {
			return "", false
		}
	}
	return scheme, true
}
