package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cosscom/shipyard/internal/database"
)

// ConnectionsFile is where the connections are kept across a restart: the
// one file of the helper's that holds a database's password. It is this
// user's alone (0600, in a folder that is 0700), the entry for a worktree
// goes when the worktree is detached, and the file goes with the helper's
// data when the helper is removed with its data.
//
// Why it is kept at all: the box daemon says which database a worktree has
// once, when it starts the worktree's service. A helper that forgot it at
// every restart (the daemon's update is one) would show an empty tab until
// each dev server was restarted. What is written is what the project's own
// configuration already holds on this disk, for the same user.
const ConnectionsFile = "connections.json"

// saved is a connection as ConnectionsFile keeps it.
type saved struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Engine string `json:"engine"`
	// URL is the database's URL as the service has it, password and all.
	URL string `json:"url"`
	// Claims are the commands of the worktree that have this database, and
	// the variable each has it in. A connection nobody claims is dropped.
	Claims []claim `json:"claims"`
}

type claim struct {
	Command string `json:"command"`
	Var     string `json:"var"`
}

// connection is one database of one worktree.
type connection struct {
	saved
	engine database.Engine
	target database.Target

	// Under the server's lock.
	db          database.Conn // nil until it is first used
	writesUntil time.Time
}

func (c *connection) close() {
	if c.db != nil {
		c.db.Close()
		c.db = nil
	}
}

// label is what the connection is called on screen: the variable the
// service has it in.
func (c *connection) label() string {
	if len(c.Claims) == 0 {
		return c.target.Database
	}
	vars := map[string]bool{}
	for _, cl := range c.Claims {
		vars[cl.Var] = true
	}
	names := make([]string, 0, len(vars))
	for v := range vars {
		names = append(names, v)
	}
	sort.Strings(names)
	return names[0]
}

// connID names a connection by what it is, so the same database of the
// same worktree has the same id every time it is attached, and after a
// restart. The password is no part of it.
func connID(source, engine string, t database.Target) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{source, engine, t.Host, strconv.Itoa(t.Port), t.Database, t.User}, "\x00")))
	return "c_" + hex.EncodeToString(sum[:6])
}

// resolve reads a URL into a connection of a source, or reports why it is
// not one the editor opens. The reason never repeats the URL.
func (s *Server) resolve(source, url string) (*connection, string) {
	engine, ok := s.reg.For(url)
	if !ok {
		return nil, "it is not a database this helper opens"
	}
	target, err := engine.Target(url)
	if err != nil {
		return nil, err.Error()
	}
	if !target.Local() {
		return nil, "the database is not on this machine"
	}
	return &connection{saved: saved{ID: connID(source, engine.Name(), target), Source: source, Engine: engine.Name(), URL: url},
		engine: engine, target: target}, ""
}

func (s *Server) load(dir string) error {
	if dir == "" {
		return nil
	}
	s.file = filepath.Join(dir, ConnectionsFile)
	raw, err := os.ReadFile(s.file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc struct {
		Connections []saved `json:"connections"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%s is not a list of connections this helper wrote", ConnectionsFile)
	}
	for _, sv := range doc.Connections {
		// Read again as if it had just been said: an engine this build
		// does not have, or a rule that changed, drops the entry.
		c, why := s.resolve(sv.Source, sv.URL)
		if c == nil || len(sv.Claims) == 0 {
			slog.Warn("a saved connection was dropped", "source", sv.Source, "why", why)
			continue
		}
		c.Claims = sv.Claims
		s.conns = append(s.conns, c)
	}
	s.sortLocked()
	return nil
}

// saveLocked writes the connections in one step: a helper stopped in the
// middle leaves the old file or the new one, never half, and never one
// that anybody else can read.
func (s *Server) saveLocked() error {
	if s.file == "" {
		return nil
	}
	doc := struct {
		Connections []saved `json:"connections"`
	}{Connections: []saved{}}
	for _, c := range s.conns {
		doc.Connections = append(doc.Connections, c.saved)
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(s.file, append(raw, '\n'))
}

// writePrivate replaces a file with one only its owner can read, in one
// step. The new file is made afresh, so it never inherits the mode of
// something left at its temporary name.
func writePrivate(file string, raw []byte) error {
	tmp := file + ".tmp"
	os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, file)
}

func (s *Server) sortLocked() {
	sort.SliceStable(s.conns, func(i, j int) bool {
		a, b := s.conns[i], s.conns[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if la, lb := a.label(), b.label(); la != lb {
			return la < lb
		}
		if a.target.Database != b.target.Database {
			return a.target.Database < b.target.Database
		}
		return a.ID < b.ID
	})
}

// AttachRequest is what the box daemon says of a worktree's command it is
// about to start: which worktree, which command, and the variables of its
// environment the plugin's manifest names.
type AttachRequest struct {
	Box     string `json:"box"`
	Project string `json:"project"`
	// Worktree is empty for the project's main checkout.
	Worktree string            `json:"worktree"`
	Branch   string            `json:"branch"`
	Path     string            `json:"path"`
	Command  string            `json:"command"`
	Env      map[string]string `json:"env"`
}

// Attached is the answer of POST /api/attach.
type Attached struct {
	// Env is always empty: this helper changes nothing of a service's
	// environment. It is here because the box daemon reads it.
	Env map[string]string `json:"env"`
	// Source is the worktree the connections are listed under.
	Source string `json:"source"`
	// Connections are the ids of the databases this command has.
	Connections []string `json:"connections"`
}

// attach notes which databases a worktree's command has. It is told the
// command's own values, reads the ones that are a database on this machine,
// and answers with nothing to set.
func (s *Server) attach(w http.ResponseWriter, r *http.Request) {
	var in AttachRequest
	if !readBody(w, r, &in, maxAttachBody) {
		return
	}
	source := database.Source(in.Project, in.Worktree)
	if source == "" || strings.TrimSpace(in.Project) == "" {
		writeProblem(w, database.Bad("project is required.").Hinted("It is the repository's name, as in acme."))
		return
	}
	names := make([]string, 0, len(in.Env))
	for name := range in.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	var has []*connection
	for _, name := range names {
		if in.Env[name] == "" {
			continue
		}
		c, why := s.resolve(source, in.Env[name])
		if c == nil {
			// The variable's name and why; never its value.
			slog.Info("a variable is not a database the editor opens", "source", source, "variable", name, "why", why)
			continue
		}
		c.Claims = []claim{{Command: in.Command, Var: name}}
		has = append(has, c)
	}

	out := Attached{Env: map[string]string{}, Source: source, Connections: []string{}}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		writeProblem(w, &database.Problem{Status: http.StatusServiceUnavailable, Code: "shutting_down", Message: "The helper is shutting down."})
		return
	}
	// What this command had before is forgotten: it has what it says now.
	for _, c := range s.conns {
		if c.Source == source {
			c.Claims = without(c.Claims, in.Command)
		}
	}
	for _, c := range has {
		at := s.byIDLocked(c.ID)
		if at == nil {
			s.conns = append(s.conns, c)
			slog.Info("a database was attached", "source", source, "connection", c.ID, "engine", c.Engine, "variable", c.Claims[0].Var)
		} else {
			if at.URL != c.URL {
				// The same database with another password or option: the
				// connections made with the old one go.
				at.close()
				at.URL = c.URL
			}
			at.Claims = append(at.Claims, c.Claims...)
		}
		if !contains(out.Connections, c.ID) {
			out.Connections = append(out.Connections, c.ID)
		}
	}
	gone := s.dropLocked(func(c *connection) bool { return c.Source == source && len(c.Claims) == 0 })
	s.sortLocked()
	err := s.saveLocked()
	s.mu.Unlock()
	for _, c := range gone {
		c.close()
		slog.Info("a database was forgotten: no command of the worktree has it now", "source", source, "connection", c.ID)
	}
	if err != nil {
		slog.Warn("the connections could not be saved: they are gone when the helper restarts", "kind", fmt.Sprintf("%T", err))
	}
	writeJSON(w, out)
}

// Detached is the answer of POST /api/detach.
type Detached struct {
	Source string `json:"source"`
	// Found says the helper knew a database of that worktree.
	Found bool `json:"found"`
	// Connections is how many were forgotten.
	Connections int `json:"connections"`
}

// detach forgets a worktree: its connections close, and what was kept of
// them (the URL, with its password) is taken out of ConnectionsFile.
func (s *Server) detach(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Project  string `json:"project"`
		Worktree string `json:"worktree"`
		// Source, or Service as Shipyard's other helpers call it, names
		// the worktree outright.
		Source  string `json:"source"`
		Service string `json:"service"`
	}
	if !readBody(w, r, &in, maxAttachBody) {
		return
	}
	source := in.Source
	if source == "" {
		// A command's name is the worktree's and a colon: the worktree.
		source, _, _ = strings.Cut(in.Service, ":")
	}
	if source == "" {
		source = database.Source(in.Project, in.Worktree)
	}
	if source == "" {
		writeProblem(w, database.Bad("Which worktree? Send project and worktree, or source.").Hinted("As in {\"project\": \"acme\", \"worktree\": \"fix-orders\"}."))
		return
	}
	s.mu.Lock()
	gone := s.dropLocked(func(c *connection) bool { return c.Source == source })
	err := s.saveLocked()
	s.mu.Unlock()
	for _, c := range gone {
		c.close()
	}
	if err != nil {
		// The password would stay on disk: that is not a detach.
		slog.Error("a detached worktree's connections could not be taken out of the file", "source", source, "kind", fmt.Sprintf("%T", err))
		writeProblem(w, &database.Problem{Status: http.StatusInternalServerError, Code: "not_saved",
			Message: "The worktree's connections are closed, but could not be removed from the helper's file."})
		return
	}
	if len(gone) > 0 {
		slog.Info("a worktree was detached", "source", source, "connections", len(gone))
	}
	writeJSON(w, Detached{Source: source, Found: len(gone) > 0, Connections: len(gone)})
}

// dropLocked takes out the connections that match, and returns them to be
// closed outside the lock.
func (s *Server) dropLocked(match func(*connection) bool) (gone []*connection) {
	kept := s.conns[:0]
	for _, c := range s.conns {
		if match(c) {
			gone = append(gone, c)
		} else {
			kept = append(kept, c)
		}
	}
	s.conns = kept
	return gone
}

func (s *Server) byIDLocked(id string) *connection {
	for _, c := range s.conns {
		if c.ID == id {
			return c
		}
	}
	return nil
}

func without(claims []claim, command string) []claim {
	kept := claims[:0]
	for _, cl := range claims {
		if cl.Command != command {
			kept = append(kept, cl)
		}
	}
	return kept
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ConnectionView is a connection as the API shows it. There is no password
// in it, and no URL.
type ConnectionView struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	// Label is the variable the service has the database in.
	Label    string `json:"label"`
	Engine   string `json:"engine"`
	Database string `json:"database"`
	// Host is a host name or address; the folder of a unix socket when the
	// database is reached through one.
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
	// WritesUntil is when writes go off again, in milliseconds since 1970;
	// left out while they are off.
	WritesUntil  int64                 `json:"writes_until,omitempty"`
	Capabilities database.Capabilities `json:"capabilities"`
}

// Connections is the answer of GET /api/db/connections.
type Connections struct {
	Connections []ConnectionView `json:"connections"`
}

func (s *Server) connections(w http.ResponseWriter, r *http.Request) {
	only := r.URL.Query().Get("source")
	out := Connections{Connections: []ConnectionView{}}
	now := s.now()
	s.mu.Lock()
	for _, c := range s.conns {
		if only != "" && c.Source != only {
			continue
		}
		v := ConnectionView{ID: c.ID, Source: c.Source, Label: c.label(), Engine: c.Engine, Database: c.target.Database,
			Host: c.target.Host, Port: c.target.Port, User: c.target.User, Capabilities: c.engine.Capabilities()}
		if c.writesUntil.After(now) {
			v.WritesUntil = c.writesUntil.UnixMilli()
		}
		out.Connections = append(out.Connections, v)
	}
	s.mu.Unlock()
	writeJSON(w, out)
}

// find is the connection a request names.
func (s *Server) find(w http.ResponseWriter, r *http.Request) (*connection, bool) {
	s.mu.Lock()
	c := s.byIDLocked(r.PathValue("conn"))
	s.mu.Unlock()
	if c == nil {
		writeProblem(w, database.NotFound("There is no such connection.").Hinted("GET /api/db/connections lists them; one goes when its worktree is removed."))
		return nil, false
	}
	return c, true
}

// open is the connection a request names, with its database opened.
func (s *Server) open(w http.ResponseWriter, r *http.Request) (*connection, database.Conn, bool) {
	c, ok := s.find(w, r)
	if !ok {
		return nil, nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Gone between the two looks: detached.
	if s.byIDLocked(c.ID) != c {
		writeProblem(w, database.NotFound("There is no such connection."))
		return nil, nil, false
	}
	if c.db == nil {
		db, err := c.engine.Open(c.URL, database.Options{Timeout: s.timeout})
		if err != nil {
			writeProblem(w, database.Unavailable("its URL cannot be read."))
			return nil, nil, false
		}
		c.db = db
	}
	return c, c.db, true
}

// writesOn reports whether writes are on for a connection, by this
// helper's own clock.
func (s *Server) writesOn(c *connection) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return c.writesUntil.After(s.now())
}
