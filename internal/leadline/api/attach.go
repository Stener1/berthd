package api

import (
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/cosscom/shipyard/internal/leadline"
	"github.com/cosscom/shipyard/internal/leadline/capture"
)

// AttachRequest is what the box daemon says of a worktree's command it is
// about to start: which worktree, which command, and the variables of its
// environment the helper may look at and answer with.
type AttachRequest struct {
	Box     string `json:"box"`
	Project string `json:"project"`
	// Worktree is empty for the project's main checkout.
	Worktree string `json:"worktree"`
	Branch   string `json:"branch"`
	Path     string `json:"path"`
	// Command names this command among several run in the worktree.
	Command string `json:"command"`
	// Env is the command's environment, or the part of it the caller shows.
	Env map[string]string `json:"env"`
}

// Attached is the answer of POST /api/attach.
type Attached struct {
	// Env is what to set in the command's environment: over what it has.
	Env map[string]string `json:"env"`
	// Service is the name the command's telemetry is filed under, and
	// Source the worktree it belongs to.
	Service string `json:"service"`
	Source  string `json:"source"`
	// Proxies are the proxies the database URLs in Env go through.
	Proxies []string `json:"proxies"`
}

// ownEndpoints are the variables that say where an app sends its
// OpenTelemetry. A project that sets one has a collector of its own.
var ownEndpoints = []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"}

// attach points a worktree's command at the helper: it answers with the
// variables that send its telemetry here under the worktree's name, and,
// for each database URL of a Postgres on this machine, opens the worktree's
// proxy in front of it and answers with the URL through the proxy.
//
// It is the attach helper (package leadline) and POST /api/proxies in one
// call, for a caller that knows nothing of either. A project that sends its
// telemetry somewhere itself keeps doing so: its endpoint is never replaced,
// and nothing else of its OpenTelemetry is touched either.
func (s *Server) attach(w http.ResponseWriter, r *http.Request) {
	var in AttachRequest
	if !readBody(w, r, &in) {
		return
	}
	wt := leadline.Worktree{Project: in.Project, Name: in.Worktree, Branch: in.Branch, Box: in.Box}
	if wt.Service() == "" {
		writeProblem(w, http.StatusBadRequest, "bad_request", "project is required.", "It is the repository's name, as in acme.")
		return
	}
	out := Attached{Env: map[string]string{}, Service: wt.ServiceFor(in.Command), Source: wt.Service(), Proxies: []string{}}

	own := false
	for _, name := range ownEndpoints {
		own = own || in.Env[name] != ""
	}
	if otlp := s.appEndpoint(); otlp != "" && !own {
		for _, v := range leadline.Env(wt, in.Command, leadline.Helper{OTLP: otlp}, in.Env["OTEL_RESOURCE_ATTRIBUTES"]) {
			switch v.Name {
			case "OTEL_SERVICE_NAME", "OTEL_BSP_SCHEDULE_DELAY", "OTEL_BLRP_SCHEDULE_DELAY":
				// What the project chose stands. Its own name is then its
				// source, as for any app that names itself.
				if in.Env[v.Name] != "" {
					if v.Name == "OTEL_SERVICE_NAME" {
						out.Service = in.Env[v.Name]
					}
					continue
				}
			}
			out.Env[v.Name] = v.Value
		}
	}

	// The ports that are proxies of this helper's already: a URL that names
	// one is through a proxy, and gets no second one in front of it.
	proxied := map[string]bool{}
	for _, px := range s.proxies.Proxies(time.Time{}) {
		_, port, _ := net.SplitHostPort(px.Listen)
		proxied[port] = true
	}
	names := make([]string, 0, len(in.Env))
	for name := range in.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	opened := false
	for _, name := range names {
		upstream, ok := leadline.LocalPostgres(in.Env[name])
		if !ok {
			continue
		}
		if _, port, _ := net.SplitHostPort(upstream); proxied[port] {
			continue
		}
		view, created, err := s.proxies.OpenProxy(capture.Request{Upstream: upstream, Label: wt.Service(),
			Project: in.Project, Worktree: in.Worktree, Branch: in.Branch, Box: in.Box})
		// A proxy that could not be had, or is not listening, is no place to
		// send an app's database connections: the URL stays as it is.
		if err != nil || view.State != "listening" {
			continue
		}
		opened = opened || created
		rewritten, ok := leadline.ThroughProxy(in.Env[name], view.Listen)
		if !ok {
			continue
		}
		out.Env[name] = rewritten
		if !contains(out.Proxies, view.ID) {
			out.Proxies = append(out.Proxies, view.ID)
		}
	}
	if opened {
		s.hub.notify()
	}
	writeJSON(w, out)
}

// appEndpoint is the base URL to give an app for its OTLP: the address that
// takes nothing else, when the helper was started with one, or its own.
func (s *Server) appEndpoint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.otlp) == 0 {
		return ""
	}
	return s.otlp[len(s.otlp)-1]
}
