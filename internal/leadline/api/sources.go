package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/cosscom/shipyard/internal/leadline"
	"github.com/cosscom/shipyard/internal/leadline/capture"
	"github.com/cosscom/shipyard/internal/leadline/store"
)

// Sources is everything feeding the helper: the worktrees it holds data
// for, the services filed under each, and the proxies carrying their
// queries.
type Sources struct {
	NowMs float64 `json:"now_ms"`
	// SinceMs is where the proxies' windowed counts start
	// (window_statements): the since_ms asked for, or 15 minutes before now.
	SinceMs float64 `json:"since_ms"`
	// OTLP is where an app should send OpenTelemetry.
	OTLP OTLPInfo `json:"otlp"`
	// Total is how many sources there are, before limit and offset.
	Total int `json:"total"`
	// Sources is one row for each worktree: a page of them, by name.
	Sources []SourceView `json:"sources"`
	// Services is every service of the sources listed, by name.
	Services []ServiceSource `json:"services"`
	// Proxies is every proxy opened for the sources listed, and, when no
	// one source was asked for, those opened for nobody.
	Proxies []capture.View `json:"proxies"`
}

type OTLPInfo struct {
	// Endpoints take OTLP/HTTP: POST to /v1/traces and /v1/logs.
	Endpoints []string `json:"endpoints"`
}

// SourceView is one worktree and what the helper holds for it.
type SourceView struct {
	// Name is the worktree's service name, "<project>/<worktree>": what
	// every list route's source parameter takes.
	Name string `json:"name"`
	// What the worktree is, when anyone said: from the resource of its
	// telemetry, or from whoever opened a proxy for it.
	Project  string `json:"project,omitempty"`
	Worktree string `json:"worktree,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Box      string `json:"box,omitempty"`
	// Services are the names filed under it: its own, and one for each
	// named command run in it ("acme/fix-orders:worker").
	Services []string `json:"services"`
	Spans    int64    `json:"spans"`
	Logs     int64    `json:"logs"`
	// LastSeenMs is the time of its newest span or log; 0 for none.
	LastSeenMs float64 `json:"last_seen_ms"`
	// Via is how its data arrives: "otlp" (the app exports it),
	// "postgres-proxy" (the helper recorded it off the wire), or both.
	Via []string `json:"via"`
	// ProxyIDs are the proxies opened for it; see proxies.
	ProxyIDs []string `json:"proxy_ids"`
}

// ServiceSource is one service and how much of each signal the helper
// holds for it. The counts are of what is stored now, so they fall as old
// data is dropped.
type ServiceSource struct {
	Name string `json:"name"`
	// Source is the worktree it is filed under: its own name up to a colon.
	Source     string  `json:"source"`
	Spans      int64   `json:"spans"`
	Logs       int64   `json:"logs"`
	LastSeenMs float64 `json:"last_seen_ms"`
	// Owner is the worktree the service belongs to, when it said which.
	Owner *capture.Owner `json:"owner,omitempty"`
	Via   []string       `json:"via"`
	// Through names the proxies some of its database calls came through.
	Through []Through `json:"through"`
}

// Through is one proxy a service's database calls were recorded by.
type Through struct {
	// Via is "postgres-proxy".
	Via string `json:"via"`
	// ProxyID is the proxy's id (see proxies).
	ProxyID string `json:"proxy_id"`
}

func (s *Server) sources(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now()
	since := now.Add(-15 * time.Minute)
	if ms, err := strconv.ParseFloat(q.Get("since_ms"), 64); err == nil && ms > 0 {
		since = time.UnixMicro(int64(ms * 1000))
	}
	only := q.Get("source")

	totals, err := s.store.Services(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	known := s.store.Worktrees()
	proxies := s.proxies.Proxies(since)

	out := &Sources{NowMs: unixMs(now), SinceMs: unixMs(since), Sources: []SourceView{}, Services: []ServiceSource{}, Proxies: []capture.View{}}
	s.mu.Lock()
	out.OTLP.Endpoints = append([]string{}, s.otlp...)
	s.mu.Unlock()

	// Every service there is anything to say of: the ones holding rows,
	// and the ones a proxy was opened for, which are sources from that
	// moment, before their first statement.
	services := map[string]*ServiceSource{}
	of := func(name string) *ServiceSource {
		if services[name] == nil {
			services[name] = &ServiceSource{Name: name, Source: store.SourceOf(name), Via: []string{}, Through: []Through{}}
		}
		return services[name]
	}
	for _, t := range totals {
		svc := of(t.Name)
		svc.Spans, svc.Logs, svc.LastSeenMs = t.Spans, t.Logs, float64(t.LastNs)/1e6
		// It sends OTLP if it has anything the proxy can't have written.
		if t.Logs > 0 || t.Spans > t.WireSpans {
			svc.Via = append(svc.Via, capture.ViaOTLP)
		}
		if t.WireSpans > 0 {
			svc.Via = append(svc.Via, capture.ViaPostgresProxy)
		}
	}
	// A statement the app also reported is filed under the app's own name:
	// which proxy saw it is what the store remembers of the pairing.
	through := map[string]map[string]bool{}
	note := func(service, proxy string) {
		if through[service] == nil {
			through[service] = map[string]bool{}
		}
		through[service][proxy] = true
	}
	live := map[string]bool{}
	for _, px := range proxies {
		live[px.ID] = true
		if px.Label != "" {
			of(px.Label)
			note(px.Label, px.ID)
		}
	}
	for pair := range s.store.Paired() {
		if live[pair.Capture] {
			note(pair.Service, pair.Capture)
		}
	}
	for name, svc := range services {
		if wt, ok := known[name]; ok {
			o := capture.OwnerOf(name, wt)
			svc.Owner = &o
		}
		for id := range through[name] {
			svc.Through = append(svc.Through, Through{Via: capture.ViaPostgresProxy, ProxyID: id})
		}
		sort.Slice(svc.Through, func(i, j int) bool { return svc.Through[i].ProxyID < svc.Through[j].ProxyID })
	}

	// The sources: one for each worktree, with what its services add up to.
	bySource := map[string]*SourceView{}
	for name, svc := range services {
		src := bySource[svc.Source]
		if src == nil {
			src = &SourceView{Name: svc.Source, Services: []string{}, Via: []string{}, ProxyIDs: []string{}}
			bySource[svc.Source] = src
		}
		src.Services = append(src.Services, name)
		src.Spans += svc.Spans
		src.Logs += svc.Logs
		src.LastSeenMs = max(src.LastSeenMs, svc.LastSeenMs)
		for _, via := range svc.Via {
			if !contains(src.Via, via) {
				src.Via = append(src.Via, via)
			}
		}
		// The worktree's own service says what it is; a command's, when
		// the worktree's own has not been heard from.
		if wt, ok := known[name]; ok && (name == svc.Source || src.Project+src.Worktree+src.Branch+src.Box == "") {
			src.Project, src.Worktree, src.Branch, src.Box = wt.Project, wt.Worktree, wt.Branch, wt.Box
		}
	}
	names := make([]string, 0, len(bySource))
	for name, src := range bySource {
		if only != "" && name != only {
			continue
		}
		sort.Strings(src.Services)
		sort.Strings(src.Via)
		for _, px := range proxies {
			if px.Label != "" && store.SourceOf(px.Label) == name {
				src.ProxyIDs = append(src.ProxyIDs, px.ID)
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	out.Total = len(names)
	offset := min(intParam(r, "offset", 0, 1<<30), len(names))
	names = names[offset:]
	names = names[:min(len(names), intParam(r, "limit", 200, 1000))]
	listed := map[string]bool{}
	for _, name := range names {
		listed[name] = true
		out.Sources = append(out.Sources, *bySource[name])
		for _, svc := range bySource[name].Services {
			out.Services = append(out.Services, *services[svc])
		}
	}
	sort.Slice(out.Services, func(i, j int) bool { return out.Services[i].Name < out.Services[j].Name })
	for _, px := range proxies {
		if (px.Label == "" && only == "") || listed[store.SourceOf(px.Label)] {
			out.Proxies = append(out.Proxies, px)
		}
	}
	writeJSON(w, out)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func unixMs(t time.Time) float64 { return float64(t.UnixMicro()) / 1000 }

// The proxies: one port in front of one Postgres, for one source.
func (s *Server) proxyRoutes(mux *routes) {
	mux.HandleFunc("GET /api/proxies", s.listProxies)
	mux.HandleFunc("POST /api/proxies", s.openProxy)
	mux.HandleFunc("DELETE /api/proxies/{id}", s.closeProxy)
	mux.HandleFunc("POST /api/attach", s.attach)
	mux.HandleFunc("POST /api/detach", s.detach)
}

func (s *Server) listProxies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var since time.Time
	if ms, err := strconv.ParseFloat(q.Get("since_ms"), 64); err == nil && ms > 0 {
		since = time.UnixMicro(int64(ms * 1000))
	}
	only := q.Get("source")
	out := []capture.View{}
	for _, px := range s.proxies.Proxies(since) {
		if only == "" || capture.Under(px.Label, only) {
			out = append(out, px)
		}
	}
	total := len(out)
	out = out[min(intParam(r, "offset", 0, 1<<30), len(out)):]
	out = out[:min(len(out), intParam(r, "limit", 200, 1000))]
	writeJSON(w, Proxies{Total: total, Proxies: out})
}

// Proxies is the answer of GET /api/proxies.
type Proxies struct {
	// Total is how many proxies there are, before limit and offset.
	Total   int            `json:"total"`
	Proxies []capture.View `json:"proxies"`
}

// Opened is the answer of POST /api/proxies.
type Opened struct {
	// Created says this call opened the proxy; false, that it was there.
	Created bool         `json:"created"`
	Proxy   capture.View `json:"proxy"`
}

// Detached is the answer of POST /api/detach.
type Detached struct {
	Service string `json:"service"`
	// Found says the helper had anything of that name: a proxy, rows, or
	// word of its worktree.
	Found bool `json:"found"`
	// Proxies is how many proxies were closed.
	Proxies  int             `json:"proxies"`
	KeptData bool            `json:"kept_data"`
	Deleted  store.Forgotten `json:"deleted"`
}

func (s *Server) openProxy(w http.ResponseWriter, r *http.Request) {
	var in capture.Request
	if !readBody(w, r, &in) {
		return
	}
	view, created, err := s.proxies.OpenProxy(in)
	if err != nil {
		proxyProblem(w, err)
		return
	}
	if created {
		s.hub.notify()
	}
	writeJSON(w, Opened{Created: created, Proxy: view})
}

func (s *Server) closeProxy(w http.ResponseWriter, r *http.Request) {
	if err := s.proxies.CloseProxy(r.PathValue("id")); err != nil {
		proxyProblem(w, err)
		return
	}
	s.hub.notify()
	writeJSON(w, struct {
		OK     bool `json:"ok"`
		Closed bool `json:"closed"`
	}{true, true})
}

// detach has the helper forget a worktree: its proxies and, unless
// keep_data, what was recorded under its name and the names under it.
func (s *Server) detach(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Service  string `json:"service"`
		KeepData bool   `json:"keep_data"`
		// What the box daemon says of a worktree it removed, in the words
		// it attached with: the source's name is worked out from them.
		Project  string `json:"project"`
		Worktree string `json:"worktree"`
	}
	if !readBody(w, r, &in) {
		return
	}
	if in.Service == "" && in.Project != "" {
		in.Service = leadline.Worktree{Project: in.Project, Name: in.Worktree}.Service()
	}
	if in.Service == "" {
		writeProblem(w, http.StatusBadRequest, "bad_request", "service is required.", "It is the source's name, as in acme/fix-orders; or give the worktree's project and worktree.")
		return
	}
	_, known := s.store.Worktrees()[in.Service]
	closed, err := s.proxies.CloseFor(in.Service)
	if err != nil {
		proxyProblem(w, err)
		return
	}
	out := Detached{Service: in.Service, Proxies: closed, KeptData: in.KeepData}
	if !in.KeepData {
		// What its proxies still held is written first, so it goes too.
		s.proxies.Flush()
		if out.Deleted, err = s.store.Forget(r.Context(), in.Service, in.Service+store.SubSep); err != nil {
			fail(w, err)
			return
		}
	}
	out.Found = known || closed > 0 || out.Deleted != (store.Forgotten{})
	s.hub.notify()
	writeJSON(w, out)
}

// proxyProblem answers with a proxy's error and the status that fits it.
func proxyProblem(w http.ResponseWriter, err error) {
	var p *capture.Problem
	if !errors.As(err, &p) {
		fail(w, err)
		return
	}
	status := http.StatusInternalServerError
	switch p.Code {
	case "not_found":
		status = http.StatusNotFound
	case "bad_request", "capture_remote", "too_many":
		status = http.StatusBadRequest
	case "port_in_use", "no_port":
		status = http.StatusConflict
	case "shutting_down":
		status = http.StatusServiceUnavailable
	}
	writeProblem(w, status, p.Code, p.Message, p.Hint)
}

// readBody decodes a small JSON body, writing the error itself on failure.
func readBody(w http.ResponseWriter, r *http.Request, into any) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(into)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "bad_request", "The request body is not JSON the helper can read.", "")
		return false
	}
	return true
}
