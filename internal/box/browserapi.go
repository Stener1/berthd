package box

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The browser API, per worktree:
//
//	POST /v1/worktrees/{loc}/{wt}/browser/open      {url, size, scale, as}: a path, a URL, or "" for the
//	     worktree's own; size (1280x800, phone) and scale resize it first; as
//	     logs it in as that email first (login.go)
//	POST /v1/worktrees/{loc}/{wt}/browser/resize    {size, scale}: kept for the worktree, applied now
//	     if its browser runs
//	POST /v1/worktrees/{loc}/{wt}/browser/act       {action, target, value}
//	POST /v1/worktrees/{loc}/{wt}/browser/snapshot  {full, delta, selector, depth}
//	POST /v1/worktrees/{loc}/{wt}/browser/wait      {text, url, idle, timeout}
//	POST /v1/worktrees/{loc}/{wt}/browser/shot      {el, full, width, native}
//	POST /v1/worktrees/{loc}/{wt}/browser/eval      {js}
//	GET  /v1/worktrees/{loc}/{wt}/browser/console?all=1
//	GET  /v1/worktrees/{loc}/{wt}/browser/network
//	GET  /v1/worktrees/{loc}/{wt}/browser/status
//	POST /v1/worktrees/{loc}/{wt}/browser/close
//	GET  /v1/worktrees/{loc}/{wt}/browser/screencast  NDJSON frames while you watch
//	GET  /v1/worktrees/{loc}/{wt}/browser/shots/{name}
//	GET  /v1/browsers, POST /v1/browser/allow {origin}
//	POST /v1/browser/reap {dry_run}: agent-browser sessions ended berth
//	     sessions left
//	GET  /v1/browser/health, PUT /v1/browser/settings, POST /v1/browser/check (browsersandbox.go)
//
// Every answer an agent reads is {text}: short, capped, as the CLI prints it.

func (b *Box) browserTarget(r *http.Request) (Location, Worktree, error) {
	if b.Browsers == nil {
		return Location{}, Worktree{}, httpError{http.StatusNotFound, "this box has no agent browser"}
	}
	loc, err := b.Locations.Get(r.Context(), r.PathValue("loc"))
	if err != nil {
		return Location{}, Worktree{}, err
	}
	for _, w := range loc.Worktrees {
		if w.Name == r.PathValue("wt") {
			return loc, w, nil
		}
	}
	return Location{}, Worktree{}, ErrUnknownWorktree
}

// browserFor returns the worktree's running browser, or starts one when
// start (open does; reading commands don't).
func (b *Box) browserFor(r *http.Request, start bool) (*browser, Location, Worktree, error) {
	loc, wt, err := b.browserTarget(r)
	if err != nil {
		return nil, loc, wt, err
	}
	if br := b.Browsers.Lookup(wt.Path); br != nil {
		br.touch()
		return br, loc, wt, nil
	}
	if !start {
		return nil, loc, wt, httpError{http.StatusConflict, "no browser is open for " + loc.Name + "/" + wt.Name + "; open one first (browser open)"}
	}
	br, err := b.Browsers.get(r.Context(), loc, wt)
	return br, loc, wt, err
}

func (b *Box) browserOpen(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		URL   string `json:"url"`
		Size  string `json:"size"`
		Scale string `json:"scale"`
		As    string `json:"as"`
	}
	decode(r, &req)
	loc, wt, err := b.browserTarget(r)
	if err != nil {
		return err
	}
	var size *Viewport
	if req.Size != "" || req.Scale != "" {
		v, err := b.Browsers.Viewport(wt.Path).Resolve(req.Size, req.Scale)
		if err != nil {
			return badRequest("%v", err)
		}
		size = &v
	}
	target := worktreeURL(b.Name, loc.Name, wt)
	if target == "" {
		return badRequest("%s/%s has no URL (its name is not a hostname label)", loc.Name, wt.Name)
	}
	switch u := strings.TrimSpace(req.URL); {
	case u == "":
	case strings.HasPrefix(u, "/"):
		target = strings.TrimSuffix(target, "/") + u
	case strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://"):
		target = u
	default:
		return badRequest("open a path (/settings) or an http(s) URL")
	}
	if _, err := url.Parse(target); err != nil {
		return badRequest("%q is not a URL", target)
	}
	if err := b.before(r, "browser.open", map[string]any{"location": loc.Name, "name": wt.Name, "path": wt.Path, "url": target}); err != nil {
		return err
	}
	// The size first: a browser that starts, starts at it.
	if size != nil {
		if _, err := b.Browsers.Resize(r.Context(), wt.Path, *size); err != nil {
			return err
		}
	}
	br, _, _, err := b.browserFor(r, true)
	if err != nil {
		return err
	}
	if req.As != "" {
		// Logged in first, as the laptop's login route does: the
		// worktree's login runs, its cookies go in for the worktree's
		// host, and the page opens (where the login says, without a path).
		site := worktreeURL(b.Name, loc.Name, wt)
		login, err := b.loginIn(r.Context(), origin(r), loc, wt, req.As)
		if err != nil {
			return err
		}
		if err := setLoginCookies(r.Context(), br.cdp, br.session, site, login.Cookies); err != nil {
			return err
		}
		if strings.TrimSpace(req.URL) == "" && login.Redirect != "" {
			target = strings.TrimSuffix(site, "/") + login.Redirect
		}
	}
	res, err := br.Open(r.Context(), target)
	if err != nil {
		return err
	}
	writeJSON(w, res)
	return nil
}

// browserResize sets the worktree's browser size: kept until it changes,
// applied at once to a browser that runs, else when one starts.
func (b *Box) browserResize(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Size  string `json:"size"`
		Scale string `json:"scale"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	_, wt, err := b.browserTarget(r)
	if err != nil {
		return err
	}
	v, err := b.Browsers.Viewport(wt.Path).Resolve(req.Size, req.Scale)
	if err != nil {
		return badRequest("%v", err)
	}
	br, err := b.Browsers.Resize(r.Context(), wt.Path, v)
	if err != nil {
		return err
	}
	text := "size: " + v.String() + "; no browser is open, so it opens at this size"
	if br != nil {
		text = "size: " + v.String()
	}
	writeJSON(w, map[string]any{"viewport": v, "size": v.String(), "running": br != nil, "text": text})
	return nil
}

func (b *Box) browserAct(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Action string `json:"action"`
		Target string `json:"target"`
		Value  string `json:"value"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	br, _, _, err := b.browserFor(r, false)
	if err != nil {
		return err
	}
	res, err := br.Act(r.Context(), req.Action, req.Target, req.Value)
	if err != nil {
		return badRequest("%v", err)
	}
	writeJSON(w, res)
	return nil
}

func (b *Box) browserSnapshot(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Full     bool   `json:"full"`
		Delta    bool   `json:"delta"`
		Selector string `json:"selector"`
		Depth    int    `json:"depth"`
	}
	decode(r, &req)
	br, _, _, err := b.browserFor(r, false)
	if err != nil {
		return err
	}
	res, err := br.Snapshot(r.Context(), req.Full, req.Delta, req.Selector, req.Depth)
	if err != nil {
		return badRequest("%v", err)
	}
	writeJSON(w, res)
	return nil
}

func (b *Box) browserWait(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Text    string `json:"text"`
		URL     string `json:"url"`
		Idle    bool   `json:"idle"`
		Timeout string `json:"timeout"`
	}
	decode(r, &req)
	br, _, _, err := b.browserFor(r, false)
	if err != nil {
		return err
	}
	d, _ := time.ParseDuration(req.Timeout)
	res, err := br.Wait(r.Context(), req.Text, req.URL, req.Idle, d)
	if err != nil {
		return err
	}
	writeJSON(w, res)
	return nil
}

func (b *Box) browserShot(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		El     string `json:"el"`
		Full   bool   `json:"full"`
		Width  int    `json:"width"`
		Native bool   `json:"native"`
	}
	decode(r, &req)
	br, _, _, err := b.browserFor(r, false)
	if err != nil {
		return err
	}
	res, err := br.Shot(r.Context(), req.El, req.Full, req.Width, req.Native)
	if err != nil {
		return badRequest("%v", err)
	}
	writeJSON(w, res)
	return nil
}

func (b *Box) browserEval(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		JS string `json:"js"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	br, loc, wt, err := b.browserFor(r, false)
	if err != nil {
		return err
	}
	if err := b.before(r, "browser.eval", map[string]any{"location": loc.Name, "name": wt.Name, "path": wt.Path}); err != nil {
		return err
	}
	res, err := br.Eval(r.Context(), req.JS)
	if err != nil {
		return err
	}
	writeJSON(w, res)
	return nil
}

func (b *Box) browserConsole(w http.ResponseWriter, r *http.Request) error {
	br, _, _, err := b.browserFor(r, false)
	if err != nil {
		return err
	}
	writeJSON(w, br.Console(r.URL.Query().Get("all") == "1"))
	return nil
}

func (b *Box) browserNetwork(w http.ResponseWriter, r *http.Request) error {
	br, _, _, err := b.browserFor(r, false)
	if err != nil {
		return err
	}
	writeJSON(w, br.Network())
	return nil
}

func (b *Box) browserStatus(w http.ResponseWriter, r *http.Request) error {
	_, wt, err := b.browserTarget(r)
	if err != nil {
		return err
	}
	br := b.Browsers.Lookup(wt.Path)
	if br == nil {
		// Why one can't start, if it can't: cheap, without starting it.
		v := b.Browsers.Viewport(wt.Path)
		res := map[string]any{"running": false, "viewport": v, "size": v.String(), "text": "no browser open; it opens at " + v.String()}
		if h := b.Browsers.Health(); h.State != "ok" {
			res["health"], res["text"] = h, "no browser open; "+h.Text
		}
		writeJSON(w, res)
		return nil
	}
	st := br.status()
	text := "open: " + st.URL + " · " + st.Size
	if st.RSS > 0 {
		text += " (" + strconvMB(st.RSS) + ")"
	}
	writeJSON(w, map[string]any{"running": true, "status": st, "viewport": st.Viewport, "size": st.Size, "text": text})
	return nil
}

func strconvMB(n uint64) string { return fmt.Sprintf("%.0f MB", float64(n)/(1<<20)) }

func (b *Box) browserClose(w http.ResponseWriter, r *http.Request) error {
	_, wt, err := b.browserTarget(r)
	if err != nil {
		return err
	}
	closed := b.Browsers.Close(wt.Path, "closed")
	writeJSON(w, map[string]any{"closed": closed, "text": map[bool]string{true: "closed", false: "no browser was open"}[closed]})
	return nil
}

// browserScreencast streams frames while the request lasts: the page is
// cast only while someone watches.
func (b *Box) browserScreencast(w http.ResponseWriter, r *http.Request) error {
	loc, wt, err := b.browserTarget(r)
	if err != nil {
		return err
	}
	if err := b.before(r, "browser.watch", map[string]any{"location": loc.Name, "name": wt.Name, "path": wt.Path}); err != nil {
		return err
	}
	br := b.Browsers.Lookup(wt.Path)
	if br == nil {
		return httpError{http.StatusConflict, "no browser is open for " + loc.Name + "/" + wt.Name}
	}
	frames, stop := br.Watch(r.Context())
	defer stop()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	flusher, _ := w.(http.Flusher)
	// The answer starts at once, so the app knows it is watching before
	// the first frame.
	w.WriteHeader(http.StatusOK)
	if flusher != nil {
		flusher.Flush()
	}
	send := func(f frame) bool {
		if enc.Encode(f) != nil || bw.Flush() != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	// At most 8 frames a second reach the laptop, and the last of a burst
	// always does: the page where it came to rest.
	const every = castEvery
	var (
		last    time.Time
		pending *frame
		later   <-chan time.Time
	)
	for {
		select {
		case <-r.Context().Done():
			return nil
		case f, ok := <-frames:
			if !ok {
				return nil
			}
			if wait := every - time.Since(last); wait > 0 {
				pending = &f
				if later == nil {
					later = time.After(wait)
				}
				continue
			}
			last = time.Now()
			if !send(f) {
				return nil
			}
		case <-later:
			later = nil
			if pending != nil {
				last = time.Now()
				if !send(*pending) {
					return nil
				}
				pending = nil
			}
		}
	}
}

var shotName = regexp.MustCompile(`^shot-[0-9.-]+\.png$`)

func (b *Box) browserShotFile(w http.ResponseWriter, r *http.Request) error {
	loc, wt, err := b.browserTarget(r)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	if !shotName.MatchString(name) {
		return httpError{http.StatusNotFound, "no such shot"}
	}
	f, err := os.Open(filepath.Join(b.Browsers.shotDir(loc.Name, wt.Name), name))
	if err != nil {
		return httpError{http.StatusNotFound, "no such shot"}
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	bufio.NewReader(f).WriteTo(w)
	return nil
}

func (b *Box) listBrowsers(w http.ResponseWriter, r *http.Request) error {
	if b.Browsers == nil {
		writeJSON(w, []BrowserStatus{})
		return nil
	}
	writeJSON(w, b.Browsers.List())
	return nil
}

func (b *Box) browserAllow(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Origin string `json:"origin"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := b.before(r, "browser.allow", map[string]any{"origin": req.Origin}); err != nil {
		return err
	}
	if err := b.AllowBrowserOrigin(req.Origin); err != nil {
		return badRequest("%v", err)
	}
	b.publish(r, "config.changed", map[string]any{"browser_allow": allowHost(req.Origin)})
	writeJSON(w, map[string]string{"allowed": allowHost(req.Origin), "text": "allowed " + allowHost(req.Origin) + " for agents' browsers on this box"})
	return nil
}

func (b *Box) mountBrowser(route func(string, func(http.ResponseWriter, *http.Request) error)) {
	p := "/v1/worktrees/{loc}/{wt}/browser/"
	route("POST "+p+"open", b.browserOpen)
	route("POST "+p+"resize", b.browserResize)
	route("POST "+p+"act", b.browserAct)
	route("POST "+p+"snapshot", b.browserSnapshot)
	route("POST "+p+"wait", b.browserWait)
	route("POST "+p+"shot", b.browserShot)
	route("POST "+p+"eval", b.browserEval)
	route("GET "+p+"console", b.browserConsole)
	route("GET "+p+"network", b.browserNetwork)
	route("GET "+p+"devtools", b.browserDevtools)
	route("GET "+p+"status", b.browserStatus)
	route("POST "+p+"close", b.browserClose)
	route("GET "+p+"screencast", b.browserScreencast)
	route("GET "+p+"shots/{name}", b.browserShotFile)
	route("POST /v1/worktrees/{loc}/{wt}/login", b.postLogin)
	route("GET /v1/browsers", b.listBrowsers)
	route("POST /v1/browser/allow", b.browserAllow)
	route("GET /v1/browser/health", b.browserHealth)
	route("PUT /v1/browser/settings", b.putBrowserSettings)
	route("POST /v1/browser/check", b.checkBrowser)
	route("POST /v1/browser/reap", b.browserReap)
	b.mountShots(route)
}
