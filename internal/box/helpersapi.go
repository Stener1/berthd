package box

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/cosscom/shipyard/internal/wire"
)

// The routes for plugins' helpers (helpers.go):
//
//	GET    /v1/helpers                     the box's platform and its helpers
//	GET    /v1/helpers/{plugin}            one plugin's helper; "off" when it has none
//	PUT    /v1/helpers/{plugin}            install it and turn it on (the laptop agent's upload)
//	DELETE /v1/helpers/{plugin}[?purge=1]  stop and remove it; its data stays unless purge
//	GET    /v1/helpers/{plugin}/log        the end of what it printed, as text
//	POST   /v1/helpers/{plugin}/attach-env tell it what a worktree's .env files have (helperenv.go)
//	ANY    /v1/helpers/{plugin}/api/…      its own API, relayed

func (b *Box) mountHelpers(s *wire.Server, route func(string, func(http.ResponseWriter, *http.Request) error)) {
	route("GET /v1/helpers", b.listPluginHelpers)
	route("GET /v1/helpers/{plugin}", b.getHelper)
	route("PUT /v1/helpers/{plugin}", b.putHelper)
	route("DELETE /v1/helpers/{plugin}", b.deleteHelper)
	route("GET /v1/helpers/{plugin}/log", b.helperLog)
	route("POST /v1/helpers/{plugin}/attach-env", b.helperAttachEnv)
	// Not through route: a helper's answers are its own (an event stream
	// among them), and go back as they are.
	s.Handle("/v1/helpers/{plugin}/api/{path...}", http.HandlerFunc(b.helperAPI))
}

func (b *Box) helpers(plugin string) (*Helpers, error) {
	if b.Helpers == nil {
		return nil, helperProblem(http.StatusNotImplemented, CodeUnsupported, "this box cannot run helpers")
	}
	if !helperPlugin.MatchString(plugin) {
		return nil, helperProblem(http.StatusBadRequest, "helper_invalid", "%q is not a plugin's id", clip(plugin, 80))
	}
	return b.Helpers, nil
}

func (b *Box) listPluginHelpers(w http.ResponseWriter, r *http.Request) error {
	if b.Helpers == nil {
		return helperProblem(http.StatusNotImplemented, CodeUnsupported, "this box cannot run helpers")
	}
	writeJSON(w, b.Helpers.List())
	return nil
}

func (b *Box) getHelper(w http.ResponseWriter, r *http.Request) error {
	h, err := b.helpers(r.PathValue("plugin"))
	if err != nil {
		return err
	}
	writeJSON(w, h.Status(r.PathValue("plugin")))
	return nil
}

// EncodeHelperSpec is a spec as HelperSpecHeader carries it.
func EncodeHelperSpec(spec HelperSpec) string {
	b, _ := json.Marshal(spec)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeHelperSpec(header string) (HelperSpec, error) {
	var spec HelperSpec
	raw, err := base64.StdEncoding.DecodeString(header)
	if header == "" || err != nil || json.Unmarshal(raw, &spec) != nil {
		return spec, helperProblem(http.StatusBadRequest, "helper_invalid", "the %s header must be the helper's spec, as JSON in base64", HelperSpecHeader)
	}
	return spec, nil
}

// putHelper installs a helper and turns it on. The body is the program; the
// spec (its name, version, arguments, size and SHA-256) comes in
// HelperSpecHeader. With no body, the box starts the program of that
// checksum it already holds.
//
// Only a paired laptop installs a helper: the laptop is where the person
// allowed the plugin, checksums and all. What runs on the box itself (an
// agent in a session, a script) can list helpers and remove one, and cannot
// add a program that berthd would then keep running.
func (b *Box) putHelper(w http.ResponseWriter, r *http.Request) error {
	plugin := r.PathValue("plugin")
	h, err := b.helpers(plugin)
	if err != nil {
		return err
	}
	if wire.IsLocal(r.Context()) {
		return helperProblem(http.StatusForbidden, CodeRefused, "a plugin's helper is installed from a paired laptop, where the plugin was allowed")
	}
	spec, err := decodeHelperSpec(r.Header.Get(HelperSpecHeader))
	if err != nil {
		return err
	}
	if err := spec.Validate(); err != nil {
		return helperProblem(http.StatusBadRequest, "helper_invalid", "%v", err)
	}
	about := map[string]any{"plugin": plugin, "name": spec.Name, "version": spec.Version}
	if err := b.before(r, "helper.install", about); err != nil {
		return err
	}
	st, err := h.Install(r.Context(), plugin, spec, r.Body)
	if err != nil {
		return err
	}
	b.publish(r, "helper.installed", about)
	writeJSON(w, st)
	return nil
}

func (b *Box) deleteHelper(w http.ResponseWriter, r *http.Request) error {
	plugin := r.PathValue("plugin")
	h, err := b.helpers(plugin)
	if err != nil {
		return err
	}
	purge := r.URL.Query().Get("purge") == "1"
	was := h.Status(plugin)
	// Something of it is here to remove: the helper, or what one left. A
	// laptop that uninstalls a plugin asks every box it knows.
	_, statErr := os.Stat(h.home(plugin))
	there := was.State != HelperOff || purge && statErr == nil
	// The services it changed that are running now: they were told where
	// the helper is when they started, and still point there. Nothing is
	// restarted for someone; the answer says which need it.
	//
	// The ones that run stay in berthd's note, helper or no helper: turned
	// on and off again before they restart, it has to name them again. One
	// that has stopped is forgotten: it is looked at afresh when it starts.
	had := h.attachedTo(plugin)
	var attached, keep []HelperService
	for _, s := range had {
		now, err := b.serviceStatus(r.Context(), s.Location, s.Worktree, s.Service)
		switch {
		case err == nil && now.State == "running":
			attached, keep = append(attached, s), append(keep, s)
		case err != nil && !errors.Is(err, ErrUnknownLocation) && !errors.Is(err, ErrUnknownWorktree) && !errors.Is(err, ErrUnknownService):
			// Not known either way: kept, and not said to be running.
			keep = append(keep, s)
		}
	}
	st, err := h.Remove(plugin, purge)
	if err != nil {
		return err
	}
	if len(keep) != len(had) {
		h.setAttached(plugin, keep)
	}
	st.Attached = attached
	if there {
		b.publish(r, "helper.removed", map[string]any{"plugin": plugin, "purged": purge, "attached": len(attached)})
	}
	writeJSON(w, st)
	return nil
}

func (b *Box) helperLog(w http.ResponseWriter, r *http.Request) error {
	h, err := b.helpers(r.PathValue("plugin"))
	if err != nil {
		return err
	}
	limit := int64(256 << 10)
	if n, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64); err == nil && n > 0 {
		limit = min(n, 4<<20)
	}
	out, err := h.Tail(r.PathValue("plugin"), limit)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(out)
	return nil
}

// helperAPI relays a request to a running helper's own API. The helper
// answers only to this machine, so the request reaches it as one from this
// machine: its Host is the helper's loopback address, and it carries no
// Origin, since the page that asked is Shipyard's own and not one the helper
// could know. Nothing else of berthd's goes with it.
func (b *Box) helperAPI(w http.ResponseWriter, r *http.Request) {
	plugin := r.PathValue("plugin")
	h, err := b.helpers(plugin)
	if err != nil {
		writeErr(w, err)
		return
	}
	api, ok := h.API(plugin)
	if !ok {
		writeCoded(w, http.StatusConflict, "the "+plugin+" helper is not running on this box", "helper_off")
		return
	}
	// As it was sent, so an escaped slash in a segment stays one.
	rest := strings.TrimPrefix(r.URL.EscapedPath(), "/v1/helpers/"+plugin+"/api")
	path, err := url.PathUnescape(rest)
	if err != nil || !strings.HasPrefix(rest, "/") {
		writeCoded(w, http.StatusBadRequest, "not a path on the helper's API", CodeBadRequest)
		return
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = "http", api.Host
			pr.Out.URL.Path, pr.Out.URL.RawPath = path, rest
			pr.Out.URL.RawQuery = r.URL.RawQuery
			pr.Out.Host = api.Host
			for _, k := range []string{"Origin", "Referer", "Cookie", "Authorization", OriginHeader, CallerHeader} {
				pr.Out.Header.Del(k)
			}
		},
		Transport: helperTransport,
		// An event stream goes on as it comes.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() != nil {
				return // whoever asked has gone
			}
			writeCoded(w, http.StatusBadGateway, "the "+plugin+" helper did not answer", "helper_unreachable")
		},
	}
	proxy.ServeHTTP(w, r)
}
