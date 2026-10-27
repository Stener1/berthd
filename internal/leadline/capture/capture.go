// Package capture runs the helper's Postgres proxies: one port in front of
// one Postgres for one source, opened and closed over the API, kept across
// a restart, and counted for whoever shows a person that their app really
// is connected through it.
//
// It is the part of Leadline's integrations manager that proxies, without
// the saved connections around it: here whoever supervises the helper
// knows the worktrees and their databases, and asks for a proxy for each.
package capture

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cosscom/shipyard/internal/leadline/pgproxy"
	"github.com/cosscom/shipyard/internal/leadline/store"
)

// DefaultPorts is where the proxies' ports are picked from when nobody
// says: easy to recognise (a 2 before Postgres's 5432), outside the range
// systems hand out for outgoing connections, so the same port is still free
// after a restart, and clear of Leadline's own 15432 and up.
var DefaultPorts = Ports{First: 25432, Last: 25531}

// Ports is a range to pick proxy ports from. The zero value takes any free
// port the system gives, which is what tests want.
type Ports struct{ First, Last int }

const (
	systemPostgres = "postgresql"
	retryEvery     = 5 * time.Second
	maxProxies     = 200
	maxLabel       = 200

	// How a service's data arrives, as Sources says it.
	ViaOTLP          = "otlp"
	ViaPostgresProxy = "postgres-proxy"
)

// Problem is an error a person can act on, and the status it answers with.
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func (p *Problem) Error() string { return p.Message }

func problem(code, message, hint string) *Problem {
	return &Problem{Code: code, Message: message, Hint: hint}
}

// Owner says whose a proxy or a service is: the worktree it was opened
// for. It is Leadline's owner, for the one kind this helper has.
type Owner struct {
	// Kind is "worktree", or "flag" for a proxy the helper was started
	// with (--pg).
	Kind string `json:"kind"`
	// Via is the tool that made it: "shipyard".
	Via string `json:"via,omitempty"`
	// Service is the source this belongs to: the worktree's service name.
	Service string `json:"service,omitempty"`
	// What the worktree is, as far as anyone said.
	Project  string `json:"project,omitempty"`
	Worktree string `json:"worktree,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Box      string `json:"box,omitempty"`
	// Label says it in a few words, for showing as it is.
	Label string `json:"label,omitempty"`
}

// OwnerOf is the owner a worktree's service or proxy is shown with.
func OwnerOf(service string, w store.Worktree) Owner {
	o := Owner{Kind: "worktree", Via: "shipyard", Service: service,
		Project: w.Project, Worktree: w.Worktree, Branch: w.Branch, Box: w.Box}
	switch {
	case o.Worktree != "":
		o.Label = "from Shipyard, worktree " + o.Worktree
	case o.Project != "":
		o.Label = "from Shipyard, project " + o.Project
	default:
		o.Label = "from Shipyard, " + service
	}
	return o
}

// View is a proxy as the API shows it.
type View struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Listen   string `json:"listen"`
	Port     int    `json:"port"`
	Upstream string `json:"upstream"`
	// Label is the source the proxy was opened for: everything through it
	// is filed under that name. Empty for one opened for nobody, which
	// files under what each connection calls itself.
	Label string `json:"label"`
	// Origins says who asked for it: "api" (POST /api/proxies), "flag"
	// (the helper's --pg).
	Origins []string `json:"origins"`
	// State is "listening", or "error" with Error saying why.
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	Owner Owner  `json:"owner"`

	ConnectionsOpen  int64 `json:"connections_open"`
	ConnectionsTotal int64 `json:"connections_total"`
	Statements       int64 `json:"statements"`
	FailedStatements int64 `json:"failed_statements"`
	// UnreadConnections were relayed without being understood (a
	// replication stream, say): their statements are not recorded.
	UnreadConnections int64 `json:"unread_connections"`
	// LastActivityMs is when a connection last arrived or a statement was
	// last recorded; 0 when nothing has come through.
	LastActivityMs float64 `json:"last_activity_ms"`
	// WindowStatements is the statements recorded since the time asked
	// about (the last 15 minutes when nobody says), to the minute.
	WindowStatements int64 `json:"window_statements"`
}

// saved is a proxy as proxies.json keeps it: where it listens, what it is
// in front of and whose it is. No URL, no user, no password: the proxy
// never has them.
type saved struct {
	ID       string `json:"id"`
	Port     int    `json:"port"`
	Upstream string `json:"upstream"`
	Label    string `json:"label,omitempty"`
}

type proxy struct {
	saved
	listen   string
	declared bool // from --pg: not saved, not closed over the API
	m        *Manager

	// p is nil while the port can't be had; err says why.
	p     *pgproxy.Proxy
	rec   *pgproxy.Recorder
	err   string
	tried time.Time

	statements, failed atomic.Int64
	lastNs             atomic.Int64
	recent             tally
}

// Options is what a Manager is made with.
type Options struct {
	// Dir is where proxies.json is kept; "" keeps nothing.
	Dir string
	// Store takes what the proxies record.
	Store *store.Store
	// Notify, if set, is called after each write of recorded statements.
	Notify func()
	// Ports is where to pick ports from; see Ports.
	Ports Ports
}

type Manager struct {
	store  *store.Store
	notify func()
	file   string
	ports  Ports

	mu      sync.Mutex
	proxies []*proxy
	closed  bool
	stop    chan struct{}
	done    chan struct{}
}

// Open makes a manager and opens again the proxies the last one left
// listening, each on the port it had: an app that was pointed at one
// before a restart still reaches its database after it.
func Open(o Options) (*Manager, error) {
	m := &Manager{store: o.Store, notify: o.Notify, ports: o.Ports, stop: make(chan struct{}), done: make(chan struct{})}
	if o.Dir != "" {
		m.file = filepath.Join(o.Dir, "proxies.json")
		list, err := readSaved(m.file)
		if err != nil {
			return nil, err
		}
		m.mu.Lock()
		for _, s := range list {
			px := &proxy{saved: s, listen: listenOn(s.Port), m: m}
			m.proxies = append(m.proxies, px)
			m.listenLocked(px)
		}
		m.mu.Unlock()
	}
	go m.retryLoop()
	return m, nil
}

func readSaved(file string) ([]saved, error) {
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Proxies []saved `json:"proxies"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s is not a list of proxies this helper wrote: %w", file, err)
	}
	return doc.Proxies, nil
}

// saveLocked writes the proxies opened over the API, in one step: a helper
// stopped in the middle leaves the old file or the new one, never half.
func (m *Manager) saveLocked() error {
	if m.file == "" {
		return nil
	}
	doc := struct {
		Proxies []saved `json:"proxies"`
	}{Proxies: []saved{}}
	for _, px := range m.proxies {
		if !px.declared {
			doc.Proxies = append(doc.Proxies, px.saved)
		}
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.file + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.file)
}

// retryLoop tries again, now and then, for the proxies whose port was busy:
// after a restart the old helper may still have been letting go.
func (m *Manager) retryLoop() {
	defer close(m.done)
	tick := time.NewTicker(retryEvery)
	defer tick.Stop()
	for {
		select {
		case <-m.stop:
			return
		case now := <-tick.C:
			m.mu.Lock()
			for _, px := range m.proxies {
				if !m.closed && px.p == nil && now.Sub(px.tried) >= retryEvery {
					m.listenLocked(px)
				}
			}
			m.mu.Unlock()
		}
	}
}

// Close stops every proxy, ends their connections and writes what they
// still held. The list on disk stays: the next helper opens them again.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	proxies := m.proxies
	m.proxies = nil
	m.mu.Unlock()
	close(m.stop)
	<-m.done
	for _, px := range proxies {
		px.close()
	}
}

func (px *proxy) close() {
	if px.p != nil {
		px.p.Close()
		px.p = nil
		if px.m.store != nil {
			px.m.store.Capturing(systemPostgres, false)
		}
	}
	if px.rec != nil {
		px.rec.Close()
		px.rec = nil
	}
}

// sink counts what a proxy records on its way to the store, and files it
// under the source the proxy was opened for.
type sink struct{ px *proxy }

func (s sink) AddSpans(spans []store.Span) error {
	px := s.px
	var statements, failed int64
	for i := range spans {
		spans[i].Capture = px.ID
		// Everything through a source's proxy is that source's, whatever
		// a connection calls itself.
		if px.Label != "" {
			spans[i].Service = px.Label
		}
		// A transaction's own span has no db_system: it frames the
		// statements, it isn't one.
		if spans[i].DBSystem == "" {
			continue
		}
		statements++
		if spans[i].Status == "error" {
			failed++
		}
	}
	now := time.Now()
	px.recent.add(now, statements)
	px.statements.Add(statements)
	px.failed.Add(failed)
	px.lastNs.Store(now.UnixNano())
	if px.m.store == nil {
		return nil
	}
	return px.m.store.AddSpans(spans)
}

// listenLocked takes the proxy's port and starts serving, or leaves the
// proxy in error with the reason.
func (m *Manager) listenLocked(px *proxy) {
	px.tried = time.Now()
	fail := func(why string) {
		if px.err != why {
			slog.Error("pg proxy: can't listen; queries sent to this port will not arrive", "listen", px.listen, "err", why)
		}
		px.err = why
	}
	if answering(px.listen) {
		fail(fmt.Sprintf("Port %d is in use by another program.", px.Port))
		return
	}
	rec := pgproxy.NewRecorder(sink{px}, m.notify)
	p, err := pgproxy.Listen(pgproxy.Route{Listen: px.listen, Upstream: px.Upstream, Service: px.Label}, rec)
	if err != nil {
		rec.Close()
		fail(sentence(lastClause(err)))
		return
	}
	px.p, px.rec, px.err = p, rec, ""
	if m.store != nil {
		m.store.Capturing(systemPostgres, true)
	}
	px.listen = p.Addr().String()
	if tcp, ok := p.Addr().(*net.TCPAddr); ok {
		px.Port = tcp.Port
	}
	go p.Serve()
	slog.Info("proxying postgres", "listen", px.listen, "upstream", px.Upstream, "label", px.Label)
}

// Declare opens the proxies the helper was started with (--pg). They are
// not saved: the flag is where they live. One that can't listen is shown
// as failed rather than stopping the helper.
func (m *Manager) Declare(routes []pgproxy.Route) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range routes {
		upstream, err := localUpstream(r.Upstream)
		if err != nil {
			return err
		}
		px := &proxy{saved: saved{ID: newID(), Upstream: upstream, Label: r.Service}, listen: r.Listen, declared: true, m: m}
		if _, port, err := net.SplitHostPort(r.Listen); err == nil {
			px.Port, _ = strconv.Atoi(port)
		}
		m.proxies = append(m.proxies, px)
		m.listenLocked(px)
	}
	return nil
}

// Request is one POST /api/proxies.
type Request struct {
	// Kind is "postgres"; left out, it is that.
	Kind string `json:"kind"`
	// Upstream is where the Postgres listens: host:port on this machine,
	// a bare port, or the path of a unix socket.
	Upstream string `json:"upstream"`
	// Label is the source to file the statements under: the worktree's
	// service name. Left out, they go under what each connection calls
	// itself (application_name), or the database's name.
	Label string `json:"label"`
	// Port asks for one port in particular. Left out, one is picked.
	Port int `json:"port"`
	// What is known of the worktree, to show with the source. All optional.
	Project  string `json:"project"`
	Worktree string `json:"worktree"`
	Branch   string `json:"branch"`
	Box      string `json:"box"`
}

// OpenProxy gives a source a proxy in front of upstream. Asking again for
// the same upstream (however its address is spelled) and the same label
// gives the proxy there is: one port for each worktree and server, the same
// every time. created says this call opened it.
func (m *Manager) OpenProxy(in Request) (v View, created bool, err error) {
	if in.Kind != "" && in.Kind != "postgres" {
		return v, false, problem("bad_request", "Only Postgres can be proxied.", `Send {"kind":"postgres","upstream":"127.0.0.1:5432"}.`)
	}
	raw := strings.TrimSpace(in.Upstream)
	if raw == "" || strings.ContainsAny(raw, " ,=@") || strings.Contains(raw, "://") {
		return v, false, problem("bad_request", "The upstream is a host and port, as in 127.0.0.1:5432, not a URL.",
			"A URL has a password in it; the proxy needs only the address.")
	}
	upstream, err := localUpstream(raw)
	if err != nil {
		return v, false, err
	}
	if in.Port < 0 || in.Port > 65535 {
		return v, false, problem("bad_request", "The port is not a port.", "Leave it out and one is picked.")
	}
	label := strings.Join(strings.Fields(in.Label), " ")
	if len(label) > maxLabel {
		return v, false, problem("bad_request", "The label is too long.", "It is a service name, as in acme/fix-orders.")
	}
	key := upstreamKey(upstream)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return v, false, problem("shutting_down", "The helper is shutting down.", "")
	}
	for _, px := range m.proxies {
		if upstreamKey(px.Upstream) == key && px.Label == label && (in.Port == 0 || in.Port == px.Port) {
			return m.viewLocked(px, time.Time{}), false, nil
		}
	}
	if len(m.proxies) >= maxProxies {
		return v, false, problem("too_many", fmt.Sprintf("There are already %d proxies.", maxProxies), "Close the ones no worktree uses.")
	}
	px := &proxy{saved: saved{ID: newID(), Upstream: upstream, Label: label}, m: m}
	// A port can be free when it is picked and gone when it is listened
	// on (another program, another helper's tests), so try the next.
	from := m.ports.First
	for attempt := 0; ; attempt++ {
		port := in.Port
		if port == 0 {
			if port, err = m.pickPortLocked(from); err != nil {
				return v, false, err
			}
		}
		px.Port, px.listen = port, listenOn(port)
		m.listenLocked(px)
		if px.p != nil {
			break
		}
		if in.Port != 0 || attempt == 8 {
			return v, false, problem("port_in_use", fmt.Sprintf("Port %d can't be used for the proxy: %s", port, lowerFirst(px.err)),
				"Pick another port, or leave the port out and one is picked.")
		}
		from = port + 1
	}
	m.proxies = append(m.proxies, px)
	if err := m.saveLocked(); err != nil {
		// Unsaved it would be gone after a restart, with an app still
		// pointed at it. Better to say no now.
		m.proxies = m.proxies[:len(m.proxies)-1]
		px.close()
		return v, false, problem("not_saved", "The proxy could not be saved: "+err.Error(), "")
	}
	if m.store != nil && label != "" {
		if err := m.store.NoteWorktrees([]store.Worktree{{Service: label, Project: in.Project, Worktree: in.Worktree, Branch: in.Branch, Box: in.Box}}); err != nil {
			slog.Warn("could not note the worktree of a proxy", "label", label, "err", err)
		}
	}
	return m.viewLocked(px, time.Time{}), true, nil
}

// pickPortLocked finds a port for a new proxy, at or after from: free now,
// and not one another proxy is keeping for later.
func (m *Manager) pickPortLocked(from int) (int, error) {
	if m.ports.First == 0 {
		// Any port: found first and listened on second, so the proxy
		// knows its own address when it has to name itself to the app.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return 0, problem("no_port", "This machine has no free port for a proxy.", "")
		}
		defer ln.Close()
		return ln.Addr().(*net.TCPAddr).Port, nil
	}
	taken := map[int]bool{}
	for _, px := range m.proxies {
		taken[px.Port] = true
	}
	for port := max(from, m.ports.First); port <= m.ports.Last; port++ {
		if !taken[port] && portFree(port) {
			return port, nil
		}
	}
	return 0, problem("no_port", fmt.Sprintf("No port between %d and %d is free for a proxy.", m.ports.First, m.ports.Last),
		"Ask for a port of your own choosing.")
}

// CloseProxy closes one proxy and forgets it. The connections through it
// end. One the helper was started with (--pg) is not closed this way.
func (m *Manager) CloseProxy(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, px := range m.proxies {
		if px.ID != id {
			continue
		}
		if px.declared {
			return problem("bad_request", "This proxy is the helper's own, from its --pg flag.", "Start the helper without the flag.")
		}
		return m.dropLocked(i)
	}
	return problem("not_found", "There is no proxy with that id.", "")
}

func (m *Manager) dropLocked(i int) error {
	px := m.proxies[i]
	m.proxies = append(m.proxies[:i:i], m.proxies[i+1:]...)
	// Under the lock, so the port is free again before anyone asks for it.
	px.close()
	slog.Info("closed postgres proxy", "listen", px.listen, "upstream", px.Upstream, "label", px.Label)
	if err := m.saveLocked(); err != nil {
		return problem("not_saved", "The proxy is closed, but the list of proxies could not be saved: "+err.Error(), "")
	}
	return nil
}

// CloseFor closes every proxy opened for a source: the one of that name and
// those of the names under it. It returns how many it closed.
func (m *Manager) CloseFor(source string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	var errs error
	for i := len(m.proxies) - 1; i >= 0; i-- {
		px := m.proxies[i]
		if px.declared || !Under(px.Label, source) {
			continue
		}
		errs = errors.Join(errs, m.dropLocked(i))
		n++
	}
	return n, errs
}

// Under reports whether a service is the source's, by its name: the
// source's own, or one of the names under it.
func Under(service, source string) bool {
	return source != "" && (service == source || strings.HasPrefix(service, source+store.SubSep))
}

// Proxies lists every proxy, oldest first, with the statements each
// recorded since the time given (the zero time: the last 15 minutes).
func (m *Manager) Proxies(since time.Time) []View {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]View, 0, len(m.proxies))
	for _, px := range m.proxies {
		out = append(out, m.viewLocked(px, since))
	}
	return out
}

func (m *Manager) viewLocked(px *proxy, since time.Time) View {
	v := View{ID: px.ID, Kind: "postgres", Listen: px.listen, Port: px.Port, Upstream: px.Upstream, Label: px.Label,
		Origins: []string{"api"}, State: "listening", Error: px.err}
	switch {
	case px.declared:
		v.Origins = []string{"flag"}
		v.Owner = Owner{Kind: "flag", Label: "the helper's --pg flag"}
	case px.Label == "":
		v.Owner = Owner{Kind: "worktree", Via: "shipyard", Label: "from Shipyard"}
	default:
		var w store.Worktree
		if m.store != nil {
			w = m.store.Worktrees()[px.Label]
		}
		v.Owner = OwnerOf(px.Label, w)
	}
	if px.p == nil {
		v.State = "error"
		return v
	}
	st := px.p.Stats()
	v.ConnectionsOpen, v.ConnectionsTotal, v.UnreadConnections = st.Open, st.Accepted, st.Unread
	v.Statements, v.FailedStatements = px.statements.Load(), px.failed.Load()
	v.WindowStatements = px.recent.since(time.Now(), since)
	if last := max(st.LastConnNs, px.lastNs.Load()); last > 0 {
		v.LastActivityMs = float64(last) / 1e6
	}
	return v
}

// Flush writes what the proxies have recorded and not yet handed on. A
// statement is otherwise stored within a fifth of a second of its answer.
func (m *Manager) Flush() {
	m.mu.Lock()
	recs := make([]*pgproxy.Recorder, 0, len(m.proxies))
	for _, px := range m.proxies {
		if px.rec != nil {
			recs = append(recs, px.rec)
		}
	}
	m.mu.Unlock()
	for _, rec := range recs {
		rec.Flush()
	}
}

// ---- addresses ----

// localUpstream spells an upstream address the proxy's way and insists it
// is on this machine: nothing the helper does leaves the box.
func localUpstream(s string) (string, error) {
	upstream, err := pgproxy.Upstream(s)
	if err != nil {
		return "", problem("bad_request", "The upstream is a host and port, as in 127.0.0.1:5432.", "")
	}
	if strings.HasPrefix(upstream, "/") {
		return upstream, nil
	}
	host, _, _ := net.SplitHostPort(upstream)
	if !LocalHost(host) {
		return "", problem("capture_remote", "The proxy only goes in front of a Postgres on this machine: "+host+" is not.",
			"Run the app against that database directly; its queries then show only if the app reports them itself.")
	}
	return upstream, nil
}

// LocalHost reports whether a host names this machine.
func LocalHost(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// upstreamKey spells a local server's address one way, so that
// localhost:5432 and 127.0.0.1:5432 are found to be the same server.
func upstreamKey(upstream string) string {
	if strings.HasPrefix(upstream, "/") {
		return upstream
	}
	return store.DBServer(upstream, 0)
}

func listenOn(port int) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) }

// answering reports whether something already accepts connections at addr.
// Listening alone doesn't tell: on macOS a loopback listener is allowed
// beside another program's listener on every address, and would then take
// that program's traffic.
func answering(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func portFree(port int) bool {
	addr := listenOn(port)
	if answering(addr) {
		return false
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func newID() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return "p_" + hex.EncodeToString(b)
}

// lastClause is the end of a wrapped error: "address already in use" of
// "listen tcp 127.0.0.1:5433: bind: address already in use".
func lastClause(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 {
		return s[i+2:]
	}
	return s
}

func sentence(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// ---- counting by the minute ----

// windowDefault is how far back the windowed counts look when nobody says.
const windowDefault = 15 * time.Minute

// tally counts things minute by minute over the last day, so "how many in
// the last quarter of an hour" can be answered without asking the store.
type tally struct {
	mu     sync.Mutex
	newest int64 // the minute the last count went into
	counts [24 * 60]int32
}

func (t *tally) add(now time.Time, n int64) {
	if n <= 0 {
		return
	}
	minute := now.Unix() / 60
	t.mu.Lock()
	t.roll(minute)
	t.counts[minute%int64(len(t.counts))] += int32(min(n, 1<<30))
	t.mu.Unlock()
}

// roll clears the minutes that have gone by since the last count.
func (t *tally) roll(minute int64) {
	if minute <= t.newest {
		return
	}
	if minute-t.newest >= int64(len(t.counts)) {
		clear(t.counts[:])
	} else {
		for m := t.newest + 1; m <= minute; m++ {
			t.counts[m%int64(len(t.counts))] = 0
		}
	}
	t.newest = minute
}

// since adds up the minutes from since's to now's. A since of zero means
// the last 15 minutes; further back than a day is a day.
func (t *tally) since(now, since time.Time) int64 {
	if since.IsZero() {
		since = now.Add(-windowDefault)
	}
	minute, from := now.Unix()/60, since.Unix()/60
	from = max(from, minute-int64(len(t.counts))+1)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roll(minute)
	var n int64
	for m := from; m <= minute; m++ {
		n += int64(t.counts[m%int64(len(t.counts))])
	}
	return n
}
