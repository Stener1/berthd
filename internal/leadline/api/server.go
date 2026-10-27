// Package api is the helper's HTTP surface: OTLP/HTTP in, and the JSON a
// UI reads out. API.md, one folder up, describes every route; a UI is
// written from that file, so change them together.
package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/cosscom/shipyard/internal/leadline/capture"
	"github.com/cosscom/shipyard/internal/leadline/ingest"
	"github.com/cosscom/shipyard/internal/leadline/store"
	"github.com/cosscom/shipyard/internal/version"
)

const (
	maxBody = 64 << 20
	// Version is the API's own number, in /api/health. It goes up when a
	// route or a field is taken away or changes meaning, not when one is
	// added.
	Version = 1
)

// Health is the answer of /api/health.
type Health struct {
	OK bool `json:"ok"`
	// Name is "berth-leadline": how to tell this helper from whatever else
	// may answer on a port.
	Name string `json:"name"`
	// Version is the build's, as `berth-leadline version` says it; API the
	// API's own number (see Version).
	Version string `json:"version"`
	API     int    `json:"api"`
}

// Trace is the answer of /api/traces/{id}.
type Trace struct {
	TraceID string `json:"trace_id"`
	// Spans are the trace's spans in start order, each with its depth.
	Spans []store.SpanRow `json:"spans"`
	// Tree is the same spans by id, each under its parent.
	Tree []store.TraceNode `json:"tree"`
	// Logs are the records written during the trace, oldest first.
	Logs []store.LogRow `json:"logs"`
}

type Server struct {
	store   *store.Store
	hub     *hub
	proxies *capture.Manager

	mu   sync.Mutex
	otlp []string // where an app should send OTLP, for /api/sources
}

// New makes the server for a store and the proxies recording into it.
func New(st *store.Store, proxies *capture.Manager) *Server {
	return &Server{store: st, hub: newHub(), proxies: proxies}
}

// Notify tells open event streams that data arrived by some way other than
// OTLP (a proxy recorded statements), so a UI refetches.
func (s *Server) Notify() { s.hub.notify() }

// SetOTLP says where the helper takes OTLP, as base URLs.
func (s *Server) SetOTLP(endpoints ...string) {
	s.mu.Lock()
	s.otlp = append([]string(nil), endpoints...)
	s.mu.Unlock()
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

// Handler serves OTLP and the API together.
func (s *Server) Handler() http.Handler {
	h, _ := s.handler()
	return h
}

// handler is Handler, with the routes it serves.
func (s *Server) handler() (http.Handler, []string) {
	mux := &routes{ServeMux: http.NewServeMux()}
	s.otlpRoutes(mux)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, Health{OK: true, Name: "berth-leadline", Version: version.Version, API: Version})
	})
	mux.HandleFunc("GET /api/summary", s.summary)
	mux.HandleFunc("GET /api/sources", s.sources)
	mux.HandleFunc("GET /api/traces", s.traces)
	mux.HandleFunc("GET /api/traces/{id}", s.trace)
	mux.HandleFunc("GET /api/queries", s.queries)
	mux.HandleFunc("GET /api/logs", s.logs)
	mux.HandleFunc("GET /api/series", s.series)
	mux.HandleFunc("GET /api/storage", s.storage)
	s.breakdownRoutes(mux)
	s.proxyRoutes(mux)
	mux.HandleFunc("GET /api/events", s.events)
	// Not through the list: this is every route there is not.
	mux.ServeMux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusNotFound, "not_found", "There is no such route.", "internal/leadline/API.md lists them.")
	})
	return guard(mux), mux.patterns
}

// OTLPHandler serves only the OTLP routes, for a second address that is
// given to apps and to nothing else.
func (s *Server) OTLPHandler() http.Handler {
	mux := &routes{ServeMux: http.NewServeMux()}
	s.otlpRoutes(mux)
	return guard(mux)
}

func (s *Server) otlpRoutes(mux *routes) {
	mux.HandleFunc("POST /v1/traces", s.ingestTraces)
	mux.HandleFunc("POST /v1/logs", s.ingestLogs)
	// An SDK that is given the endpoint sends its metrics there too. They
	// are taken and dropped, so it does not log an error every few seconds.
	mux.HandleFunc("POST /v1/metrics", s.dropMetrics)
}

// guard keeps the helper to this machine's own pages. What it holds (SQL,
// logs, request data) is not for any website the developer happens to open:
// a request must name a loopback host, which stops DNS rebinding, and only
// loopback and app origins get CORS headers. A request that changes
// something must come from no page at all (the box daemon, curl) or from
// Shipyard's own window: the app being debugged is also a page on this
// machine, and must not be able to close a proxy or delete a worktree's
// data.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			writeProblem(w, http.StatusForbidden, "forbidden", "The helper only answers on localhost.", "")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if !allowedOrigin(origin) {
				writeProblem(w, http.StatusForbidden, "forbidden", "This origin is not allowed.", "")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Encoding")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			if r.Method != http.MethodOptions && changes(r) && !appOrigin(origin) {
				writeProblem(w, http.StatusForbidden, "forbidden", "Only Shipyard's own window may change what the helper holds.", "")
				return
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// changes reports whether a request is one that changes what the helper
// holds: anything under /api/ that is not a read. Sending telemetry (the
// /v1/ routes) is what an app under test is meant to do, from its pages
// too.
func changes(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet && r.Method != http.MethodHead
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func allowedOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// The desktop app's webview: tauri://localhost on macOS and Linux.
	if u.Scheme == "tauri" {
		return true
	}
	return (u.Scheme == "http" || u.Scheme == "https") && loopbackHost(u.Host)
}

// appOrigin reports whether a request's Origin is Shipyard's own window:
// the desktop app's webview, or the app's dev server on 1420.
// BERTH_LEADLINE_APP_ORIGINS adds origins, comma separated, for running the
// interface somewhere else.
func appOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme == "tauri" || (u.Scheme == "http" && u.Host == "tauri.localhost") {
		return true
	}
	if u.Scheme == "http" && (u.Host == "localhost:1420" || u.Host == "127.0.0.1:1420") {
		return true
	}
	for _, extra := range strings.Split(os.Getenv("BERTH_LEADLINE_APP_ORIGINS"), ",") {
		if extra = strings.TrimSpace(extra); extra != "" && extra == origin {
			return true
		}
	}
	return false
}

func (s *Server) ingestTraces(w http.ResponseWriter, r *http.Request) {
	req := &tracepb.TracesData{}
	isJSON, ok := readOTLP(w, r, req)
	if !ok {
		return
	}
	spans := ingest.Spans(req, isJSON)
	if err := s.store.AddSpans(spans); err != nil {
		failOTLP(w, err)
		return
	}
	s.note(ingest.Worktrees(ingest.SpanResources(req)...))
	s.hub.notify()
	writeOTLP(w, isJSON)
}

func (s *Server) ingestLogs(w http.ResponseWriter, r *http.Request) {
	req := &logspb.LogsData{}
	isJSON, ok := readOTLP(w, r, req)
	if !ok {
		return
	}
	logs := ingest.Logs(req, isJSON)
	if err := s.store.AddLogs(logs); err != nil {
		failOTLP(w, err)
		return
	}
	s.note(ingest.Worktrees(ingest.LogResources(req)...))
	s.hub.notify()
	writeOTLP(w, isJSON)
}

// note remembers what an export's resources said of their worktrees.
// Failing to is no reason to refuse the export.
func (s *Server) note(list []store.Worktree) {
	if len(list) == 0 {
		return
	}
	if err := s.store.NoteWorktrees(list); err != nil {
		slog.Warn("could not note a worktree", "err", err)
	}
}

func (s *Server) dropMetrics(w http.ResponseWriter, r *http.Request) {
	io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, maxBody))
	writeOTLP(w, strings.HasPrefix(r.Header.Get("Content-Type"), "application/json"))
}

// readOTLP decodes an export request, protobuf or JSON, gzipped or not. It
// reports whether the body was JSON, and writes the error itself on failure.
func readOTLP(w http.ResponseWriter, r *http.Request, into proto.Message) (isJSON, ok bool) {
	var body io.Reader = http.MaxBytesReader(w, r.Body, maxBody)
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(body)
		if err != nil {
			http.Error(w, "bad gzip body", http.StatusBadRequest)
			return false, false
		}
		defer zr.Close()
		body = io.LimitReader(zr, maxBody)
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		http.Error(w, "could not read body", http.StatusBadRequest)
		return false, false
	}
	isJSON = strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
	if isJSON {
		err = protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(raw, into)
	} else {
		// An export request has fields this message does not (none yet,
		// but the protocol may grow them); unknown ones are kept aside,
		// not refused.
		err = proto.Unmarshal(raw, into)
	}
	if err != nil {
		http.Error(w, "not a valid OTLP request: "+err.Error(), http.StatusBadRequest)
		return isJSON, false
	}
	return isJSON, true
}

// writeOTLP answers an export: the empty response, which says all of it was
// taken. In protobuf that is no bytes at all, in JSON an empty object.
func writeOTLP(w http.ResponseWriter, isJSON bool) {
	if isJSON {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{}"))
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}

func failOTLP(w http.ResponseWriter, err error) {
	slog.Error("export failed", "err", err)
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Summary(r.Context())
	respond(w, out, err)
}

// storage says how much is kept and how much may be: the database's size,
// the size cap, how long rows are kept and how old the oldest is.
func (s *Server) storage(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Storage(r.Context())
	respond(w, out, err)
}

func (s *Server) traces(w http.ResponseWriter, r *http.Request) {
	f := filter(r)
	q := r.URL.Query()
	f.ErrorsOnly = q.Get("errors") == "1"
	f.MinDurationMs, _ = strconv.ParseFloat(q.Get("min_duration_ms"), 64)
	out, err := s.store.Traces(r.Context(), f)
	respondPage(w, out, err)
}

func (s *Server) trace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	spans, tree, logs, err := s.store.Trace(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	if len(spans) == 0 {
		writeProblem(w, http.StatusNotFound, "not_found", "There is no such trace.", "It may have aged out: traces are kept for a limited time.")
		return
	}
	writeJSON(w, Trace{TraceID: id, Spans: spans, Tree: tree, Logs: logs})
}

func (s *Server) queries(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Queries(r.Context(), filter(r))
	respond(w, out, err)
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Logs(r.Context(), filter(r))
	respondPage(w, out, err)
}

// respondPage answers a list that takes a cursor. A cursor no row ever
// carried is the caller's mistake, not the helper's.
func respondPage(w http.ResponseWriter, v any, err error) {
	if errors.Is(err, store.ErrBadCursor) {
		writeProblem(w, http.StatusBadRequest, "bad_request", "before is not a cursor from an earlier page.",
			"Send the cursor of the last row of the page before, as it came.")
		return
	}
	respond(w, v, err)
}

func (s *Server) series(w http.ResponseWriter, r *http.Request) {
	win, ok := window(w, r)
	if !ok {
		return
	}
	out, err := s.store.SeriesBetween(r.Context(), win, intParam(r, "buckets", 60, 2000))
	respond(w, out, err)
}

// events is a server-sent stream that says "new data arrived". A UI
// refetches what it shows; the stream carries no rows itself.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "failed", "This connection cannot stream.", "")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// Listen before greeting: "hello" tells the UI it may fetch, and data
	// that arrives between the greeting and the subscription would wake
	// nobody.
	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)
	fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	flusher.Flush()

	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			fmt.Fprint(w, "event: ingest\ndata: {}\n\n")
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
		}
		flusher.Flush()
	}
}

func filter(r *http.Request) store.Filter {
	q := r.URL.Query()
	f := store.Filter{Service: q.Get("service"), Source: q.Get("source"), Search: q.Get("q"), Before: q.Get("before"),
		User: q.Get("user"), Kind: q.Get("kind"), Control: q.Get("control") == "1",
		DBSystem: q.Get("db_system"), DBServer: q.Get("db_server")}
	f.SinceMs, _ = strconv.ParseFloat(q.Get("since_ms"), 64)
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))
	if n, err := strconv.Atoi(q.Get("min_severity")); err == nil {
		f.MinSeverity = int32(n)
	}
	return f
}

func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, v)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// writeProblem answers with an error a person can act on:
// {"error":{"code":"…","message":"…","hint":"…"}}.
func writeProblem(w http.ResponseWriter, status int, code, message, hint string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": capture.Problem{Code: code, Message: message, Hint: hint}})
}

// fail answers for an error that is the helper's own. What went wrong is
// logged here and not sent: an error from the database can quote the
// statement it was running.
func fail(w http.ResponseWriter, err error) {
	// Whoever asked has gone, or the helper is stopping: nothing failed.
	if !errors.Is(err, context.Canceled) {
		slog.Error("request failed", "err", err)
	}
	writeProblem(w, http.StatusInternalServerError, "failed", "The helper could not answer.", "Its log says why.")
}
