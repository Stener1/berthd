package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/cosscom/shipyard/internal/leadline/store"
)

// The breakdowns: endpoints, one endpoint, one query, and the charts that
// cut requests by latency band or by a dimension. All of them take the same
// window (since_ms, until_ms, service, source); until_ms defaults to now.

func (s *Server) breakdownRoutes(mux *routes) {
	mux.HandleFunc("GET /api/endpoints", s.endpoints)
	mux.HandleFunc("GET /api/endpoints/detail", s.endpointDetail)
	mux.HandleFunc("GET /api/queries/detail", s.queryDetail)
	mux.HandleFunc("GET /api/heatmap", s.heatmap)
	mux.HandleFunc("GET /api/breakdown", s.groupedSeries)
}

// window reads the time window and the services of a request. It writes
// the error itself and reports false when the window is empty or backwards.
func window(w http.ResponseWriter, r *http.Request) (store.Window, bool) {
	q := r.URL.Query()
	win := store.Window{Service: q.Get("service"), Source: q.Get("source"), UntilMs: float64(time.Now().UnixNano()) / 1e6,
		DBSystem: q.Get("db_system"), DBServer: q.Get("db_server")}
	if v, err := strconv.ParseFloat(q.Get("until_ms"), 64); err == nil {
		win.UntilMs = v
	}
	win.SinceMs = win.UntilMs - 15*60*1000
	if v, err := strconv.ParseFloat(q.Get("since_ms"), 64); err == nil {
		win.SinceMs = v
	}
	if win.UntilMs <= win.SinceMs {
		writeProblem(w, http.StatusBadRequest, "bad_request", "until_ms must be after since_ms.", "")
		return win, false
	}
	return win, true
}

// intParam reads a whole number between 1 and max, or returns def.
func intParam(r *http.Request, name string, def, max int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil && v > 0 && v <= max {
		return v
	}
	return def
}

func (s *Server) endpoints(w http.ResponseWriter, r *http.Request) {
	win, ok := window(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	spark := 0
	if q.Has("spark") {
		spark = intParam(r, "spark", 24, 200)
	}
	out, err := s.store.Endpoints(r.Context(), win, store.EndpointQuery{
		Search: q.Get("q"),
		Sort:   q.Get("sort"),
		Asc:    q.Get("dir") == "asc",
		Limit:  intParam(r, "limit", 200, 1000),
		Offset: intParam(r, "offset", 0, 1<<30),
		Spark:  spark,
	})
	respond(w, out, err)
}

func (s *Server) endpointDetail(w http.ResponseWriter, r *http.Request) {
	win, ok := window(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if q.Get("name") == "" {
		writeProblem(w, http.StatusBadRequest, "bad_request", "name is required.", "It is an endpoint's name, as /api/endpoints lists it.")
		return
	}
	// endpoint_service is the endpoint's own; service stays the list's filter.
	ep := store.Endpoint{Service: q.Get("endpoint_service"), Name: q.Get("name")}
	out, err := s.store.EndpointDetail(r.Context(), win, ep, intParam(r, "buckets", 60, 2000))
	respond(w, out, err)
}

func (s *Server) queryDetail(w http.ResponseWriter, r *http.Request) {
	win, ok := window(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if q.Get("fingerprint") == "" {
		writeProblem(w, http.StatusBadRequest, "bad_request", "fingerprint is required.", "It is a query's fingerprint, as /api/queries lists it.")
		return
	}
	// db_system here names the query, with its fingerprint; it does not
	// narrow the window as it does for the endpoints.
	key := store.QueryKey{DBSystem: q.Get("db_system"), Fingerprint: q.Get("fingerprint")}
	win.DBSystem, win.DBServer = "", ""
	out, err := s.store.QueryDetail(r.Context(), win, key, intParam(r, "buckets", 60, 2000))
	respond(w, out, err)
}

func (s *Server) heatmap(w http.ResponseWriter, r *http.Request) {
	win, ok := window(w, r)
	if !ok {
		return
	}
	out, err := s.store.RequestHeatmap(r.Context(), win, intParam(r, "buckets", 90, 2000))
	respond(w, out, err)
}

func (s *Server) groupedSeries(w http.ResponseWriter, r *http.Request) {
	win, ok := window(w, r)
	if !ok {
		return
	}
	by := r.URL.Query().Get("by")
	if !store.KnownGrouping(by) {
		writeProblem(w, http.StatusBadRequest, "bad_request", "by must be one of "+store.Groupings()+".", "")
		return
	}
	out, err := s.store.GroupedSeries(r.Context(), win, by, intParam(r, "buckets", 60, 2000), intParam(r, "limit", 6, 20))
	respond(w, out, err)
}
