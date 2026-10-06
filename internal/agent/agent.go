// Package agent is the laptop's background process. It holds a connection to
// every paired box, runs saved forwards and the *.localhost proxy, and serves
// a local API that the CLI and the desktop app are clients of.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/sean-brydon/berthd/internal/box"
	"github.com/sean-brydon/berthd/internal/doctor"
	"github.com/sean-brydon/berthd/internal/events"
	"github.com/sean-brydon/berthd/internal/forward"
	"github.com/sean-brydon/berthd/internal/hooks"
	"github.com/sean-brydon/berthd/internal/identity"
	"github.com/sean-brydon/berthd/internal/network"
	"github.com/sean-brydon/berthd/internal/pfredirect"
	"github.com/sean-brydon/berthd/internal/proxy"
	"github.com/sean-brydon/berthd/internal/statefile"
	"github.com/sean-brydon/berthd/internal/trust"
	"github.com/sean-brydon/berthd/internal/wire"
)

const (
	DefaultProxyPort      = 1377
	defaultHealthInterval = 10 * time.Second
	pingTimeout           = 8 * time.Second
	// A tick arriving this much later than scheduled means the laptop slept.
	wakeSkew = 20 * time.Second
)

const (
	StateConnecting = "connecting"
	StateOnline     = "online"
	StateOffline    = "offline"
	StateUntrusted  = "untrusted"
)

type Config struct {
	// Dir is the laptop's state directory, holding identity.pem and boxes.json.
	Dir string
	// Socket is the local API socket. Defaults to Dir/agent.sock.
	Socket string
	// ProxyAddrs are where the *.localhost proxy listens. Defaults to port
	// 1377 on both loopback addresses.
	ProxyAddrs     []string
	HealthInterval time.Duration
	// UIAddr is where the desktop app's API listens; "off" turns it off.
	// Defaults to 127.0.0.1:1378.
	UIAddr string
	// CLI is the berth binary the app's box management runs. Defaults to
	// this executable.
	CLI string
	// SSHDir is the SSH configuration editors use; defaults to ~/.ssh.
	SSHDir string
	// EditorRoots are where editor apps are looked for; defaults to
	// /Applications and ~/Applications.
	EditorRoots []string
	// Run, when set, runs an editor command instead of starting it; tests
	// use it to see what would run.
	Run func(command []string) error
	// UserDir holds hooks, themes, templates and plugins. Defaults to
	// ~/.berth.
	UserDir string
	Log     *log.Logger
	// Now reads the wall clock; tests replace it to simulate sleep.
	Now func() time.Time
	// Networks reaches boxes on other tailnets. Defaults to embedded
	// Tailscale nodes under Dir/networks.
	Networks Networks
	// QueueIdleTimeout is how long a queued prompt waits for a busy agent's
	// turn to end before it is typed anyway (default 30 minutes), and
	// QueueWaitStep how long one wait on the box lasts (default 5 minutes).
	QueueIdleTimeout time.Duration
	QueueWaitStep    time.Duration
	// Berthd is the berthd Use this Mac installs; defaults to the one beside
	// the berth executable or in the app's Contents/Resources (localbox.go).
	// LocalBoxPort is the first port tried for it (default 7445).
	Berthd       string
	LocalBoxPort int
	// Codex is the codex CLI that generates chat backgrounds, and CodexHome
	// where it keeps its images; default to the one on PATH and ~/.codex.
	Codex     string
	CodexHome string
	// Doctor runs `berth doctor`'s checks of this laptop, for the app's
	// Copy diagnostics (GET /v1/doctor); nil answers that there are none.
	Doctor func(ctx context.Context) []doctor.Check
}

// Networks is the set of other tailnets the agent can dial through.
type Networks interface {
	Dial(ctx context.Context, name, addr string) (net.Conn, error)
	Login(ctx context.Context, name string, onURL func(string)) (network.Info, error)
	List(ctx context.Context) []network.Info
	Peers(ctx context.Context, name string) ([]network.Peer, error)
	Close()
}

func (c *Config) defaults() {
	if c.Socket == "" {
		c.Socket = filepath.Join(c.Dir, "agent.sock")
	}
	if c.ProxyAddrs == nil {
		port := strconv.Itoa(DefaultProxyPort)
		c.ProxyAddrs = []string{net.JoinHostPort("127.0.0.1", port), net.JoinHostPort("::1", port)}
	}
	if c.UIAddr == "" {
		c.UIAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(DefaultUIPort))
	}
	if c.UserDir == "" {
		if dir, err := statefile.UserDir(); err == nil {
			c.UserDir = dir
		} else {
			c.UserDir = filepath.Join(c.Dir, "user")
		}
	}
	if c.HealthInterval == 0 {
		c.HealthInterval = defaultHealthInterval
	}
	if c.Log == nil {
		c.Log = log.New(os.Stderr, "", log.LstdFlags)
	}
	if c.Networks == nil {
		c.Networks = &network.Manager{Dir: filepath.Join(c.Dir, "networks"), Log: c.Log}
	}
	if c.Now == nil {
		// Round(0) drops the monotonic reading: on macOS the monotonic clock
		// stops during sleep, so only wall-clock time reveals that it happened.
		c.Now = func() time.Time { return time.Now().Round(0) }
	}
}

type BoxStatus struct {
	Name        string    `json:"name"`
	Address     string    `json:"address"`
	Network     string    `json:"network,omitempty"`
	Fingerprint string    `json:"fingerprint"`
	State       string    `json:"state"`
	Error       string    `json:"error,omitempty"`
	LatencyMs   int64     `json:"latency_ms,omitempty"`
	Since       time.Time `json:"since"`
	// Local marks a box on this computer itself (Use this Mac).
	Local bool `json:"local,omitempty"`
}

type ForwardStatus struct {
	Forward
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

type ProxyStatus struct {
	Port int `json:"port"`
	// URLPort is the port URLs should name: 80 once the port 80 redirect is
	// installed, so http://3000.devl.localhost/ needs no port at all.
	URLPort int    `json:"url_port"`
	Error   string `json:"error,omitempty"`
}

type Status struct {
	Boxes    []BoxStatus     `json:"boxes"`
	Forwards []ForwardStatus `json:"forwards"`
	Routes   []Route         `json:"routes"`
	Proxy    ProxyStatus     `json:"proxy"`
}

type Agent struct {
	seqs     *seqStore
	cfg      Config
	hooks    *hooks.Runner
	id       *identity.Identity
	boxes    *trust.Store
	forwards forwardStore
	routes   routeStore
	bus      events.Bus
	proxy    *proxy.Proxy
	proxySt  ProxyStatus
	queue    *promptQueue
	local    localBox
	// imageGenBusy lets one chat background generate at a time.
	imageGenBusy sync.Mutex
	// outdated remembers which boxes run an older berthd (outdated.go).
	outdated outdatedCache
	// selfNames are the boxes' own names, which the proxy accepts too
	// (aliases.go).
	selfNames selfNames
	// startedAs is the program this agent runs, and work the long work
	// under way that a restart waits for (restart.go).
	startedAs startedAs
	work      workSet

	// ctx lives as long as the agent; forwards added through the API run under
	// it rather than under the request that created them.
	ctx context.Context

	mu      sync.Mutex
	svc     map[string]serviceCache
	clients map[string]*boxState
	running map[string]*runningForward
	wake    chan struct{}
}

type boxState struct {
	peer   trust.Peer
	client *wire.Client
	status BoxStatus
	// stopRelay ends the goroutine relaying this box's events, if running.
	stopRelay context.CancelFunc
}

type runningForward struct {
	fwd    Forward
	cancel context.CancelFunc
	state  string
	err    string
}

// ErrAlreadyRunning means another agent owns this state directory.
var ErrAlreadyRunning = errors.New("another berth agent is already running")

// Run serves until ctx is cancelled or a client asks the agent to stop.
func Run(ctx context.Context, cfg Config) error {
	cfg.defaults()
	if len(cfg.Socket) > 100 {
		return fmt.Errorf("agent socket path %s is too long for a Unix socket; set a shorter BERTH_HOME", cfg.Socket)
	}
	unlock, err := lockAgent(cfg.Dir)
	if err != nil {
		return err
	}
	defer unlock()
	id, err := identity.LoadOrCreate(filepath.Join(cfg.Dir, "identity.pem"))
	if err != nil {
		return err
	}
	a := &Agent{
		cfg:      cfg,
		id:       id,
		boxes:    trust.NewStore(filepath.Join(cfg.Dir, "boxes.json")),
		forwards: forwardStore{path: filepath.Join(cfg.Dir, "forwards.json")},
		routes:   routeStore{path: filepath.Join(cfg.Dir, "routes.json")},
		clients:  map[string]*boxState{},
		seqs:     newSeqStore(cfg.Dir),
		running:  map[string]*runningForward{},
		wake:     make(chan struct{}, 1),
	}
	a.startedAs = readStartedAs(cfg.Now())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.ctx = ctx
	a.queue = newPromptQueue(ctx, filepath.Join(cfg.Dir, "queue.json"), agentBoxes{a}, a.publish, cfg.Now, cfg.Log.Printf, cfg.QueueIdleTimeout, cfg.QueueWaitStep)

	// Holding the agent lock means any socket file left here is stale.
	os.Remove(cfg.Socket)
	apiLn, err := net.Listen("unix", cfg.Socket)
	if err != nil {
		return err
	}
	defer os.Remove(cfg.Socket)
	if err := os.Chmod(cfg.Socket, 0o600); err != nil {
		apiLn.Close()
		return err
	}
	a.startProxy(ctx)
	a.sync()
	a.startSavedForwards(ctx)
	go a.healthLoop(ctx)
	go a.keepLocalBoxCurrent(ctx)
	go a.watchTeamUpdates(ctx)
	a.hooks = &hooks.Runner{Path: filepath.Join(cfg.UserDir, "hooks.json"), PluginsDir: filepath.Join(cfg.UserDir, "plugins"), Log: cfg.Log}
	go a.hooks.Run(ctx, &a.bus)

	handler := a.api(cancel)
	a.startUI(ctx, handler)
	api := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	stop := context.AfterFunc(ctx, func() { api.Close() })
	defer stop()
	a.publish(Event{Type: EventAgentStarted})
	a.cfg.Log.Printf("berth agent running; API %s, proxy port %d", cfg.Socket, a.proxySt.Port)
	err = api.Serve(apiLn)
	a.shutdown()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// lockAgent makes the agent a singleton per state directory. Losing the race
// is reported to the caller, which exits cleanly so a supervisor does not
// restart it against the winner forever.
func lockAgent(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "agent.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrAlreadyRunning
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

func (a *Agent) publish(e Event) {
	e.Time = a.cfg.Now()
	a.bus.Publish(e)
}

func (a *Agent) startProxy(ctx context.Context) {
	a.proxy = &proxy.Proxy{
		Dialer: func(box string) (proxy.DialFunc, bool) {
			c, ok := a.client(box)
			if !ok {
				return nil, false
			}
			return c.DialPort, true
		},
		Index:    http.HandlerFunc(a.serveIndex),
		Route:    a.route,
		Worktree: a.worktree,
		BoxAlias: a.boxAlias,
	}
	srv := &http.Server{Handler: a.proxy, ReadHeaderTimeout: 30 * time.Second}
	context.AfterFunc(ctx, func() { srv.Close() })
	var errs []string
	for _, addr := range a.cfg.ProxyAddrs {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if a.proxySt.Port == 0 {
			a.proxySt.Port = ln.Addr().(*net.TCPAddr).Port
		}
		go srv.Serve(ln)
	}
	// The IPv4 address is the one that must work; IPv6 is best effort.
	if a.proxySt.Port == 0 && len(errs) > 0 {
		a.proxySt.Error = "proxy could not listen: " + errs[0]
		a.cfg.Log.Print(a.proxySt.Error)
	}
}

func (a *Agent) runCtx() context.Context { return a.ctx }

// dialerFor returns how to reach a box on the named network; nil means this
// machine's own network.
func (a *Agent) dialerFor(name string) wire.DialFunc {
	if name == "" {
		return nil
	}
	return func(ctx context.Context, _, addr string) (net.Conn, error) {
		return a.cfg.Networks.Dial(ctx, name, addr)
	}
}

func (a *Agent) client(box string) (*wire.Client, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.clients[box]
	if !ok {
		return nil, false
	}
	return st.client, true
}

// sync brings the set of box clients in line with the trust store, which the
// CLI may have changed by pairing or forgetting a box.
func (a *Agent) sync() {
	peers, err := a.boxes.List()
	if err != nil {
		a.cfg.Log.Printf("reading paired boxes: %v", err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := map[string]bool{}
	for _, p := range peers {
		seen[p.Name] = true
		if st, ok := a.clients[p.Name]; ok && st.peer == p {
			continue
		}
		if st, ok := a.clients[p.Name]; ok {
			st.close()
		}
		a.clients[p.Name] = &boxState{
			peer:   p,
			client: wire.NewClientVia(a.id, p, a.dialerFor(p.Network)),
			status: BoxStatus{Name: p.Name, Address: p.Address, Network: p.Network, Fingerprint: p.Fingerprint.String(), State: StateConnecting, Since: a.cfg.Now()},
		}
	}
	for name, st := range a.clients {
		if !seen[name] {
			st.close()
			delete(a.clients, name)
		}
	}
}

func (st *boxState) close() {
	if st.stopRelay != nil {
		st.stopRelay()
	}
	st.client.Reset()
}

// relay republishes a box's events on the agent's bus under the laptop's name
// for the box, so hooks and the app see one stream for every box. The stream
// reconnects until the box is removed.
func (a *Agent) relay(ctx context.Context, name string, c *wire.Client) {
	bc := box.NewClient(c)
	// The last event seen: a reconnect (after sleep, say) asks the box's
	// journal for what it missed, up to the box's replay limit. It is kept
	// on disk, so a restart of the agent catches up too.
	fp := c.Box().Fingerprint.String()
	last := a.seqs.get(name, fp)
	save := time.NewTicker(5 * time.Second)
	defer save.Stop()
	defer a.seqs.save()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-save.C:
				a.seqs.save()
			}
		}
	}()
	for ctx.Err() == nil {
		first := true
		bc.EventsSince(ctx, last, func(e events.Event) {
			if e.Seq > 0 {
				if e.Seq <= last && !(first && last > 0) {
					return
				}
				// A first event at or below the saved Seq means the box's
				// journal started over (a reinstall): take it from there.
				last = e.Seq
				a.seqs.set(name, fp, last)
			}
			first = false
			e.Box = name
			a.bus.Publish(e)
		})
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (a *Agent) healthLoop(ctx context.Context) {
	interval := a.cfg.HealthInterval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	last := a.cfg.Now()
	a.checkAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-a.wake:
		}
		now := a.cfg.Now()
		if now.Sub(last) > interval+wakeSkew {
			a.cfg.Log.Printf("clock jumped %s; the laptop slept, reconnecting every box", now.Sub(last).Round(time.Second))
			a.resetAll()
		}
		last = now
		a.checkAll(ctx)
	}
}

// checkSoon asks the health loop to run now instead of at its next tick.
func (a *Agent) checkSoon() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

func (a *Agent) resetAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for name, st := range a.clients {
		st.client.Reset()
		a.proxy.ResetBox(name)
	}
}

func (a *Agent) checkAll(ctx context.Context) {
	a.sync()
	a.retryFailedForwards(ctx)
	a.mu.Lock()
	boxes := make(map[string]*boxState, len(a.clients))
	for name, st := range a.clients {
		boxes[name] = st
	}
	a.mu.Unlock()
	var wg sync.WaitGroup
	for name, st := range boxes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.check(ctx, name, st)
		}()
	}
	wg.Wait()
	// Prompts queued while a box was away go once it answers again.
	a.queue.kickAll()
}

func (a *Agent) check(ctx context.Context, name string, st *boxState) {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	start := time.Now()
	_, err := st.client.Ping(ctx)
	latency := time.Since(start)
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	state := StateOnline
	switch {
	case errors.Is(err, wire.ErrUntrusted):
		state = StateUntrusted
	case err != nil:
		state = StateOffline
	}
	a.mu.Lock()
	if a.clients[name] != st {
		a.mu.Unlock()
		return
	}
	prev := st.status.State
	if prev != state {
		st.status.Since = a.cfg.Now()
	}
	st.status.State = state
	st.status.Error = ""
	st.status.LatencyMs = 0
	if err != nil {
		st.status.Error = err.Error()
	} else {
		st.status.LatencyMs = latency.Milliseconds()
	}
	a.mu.Unlock()

	if err != nil {
		// Whatever connection we had is suspect; the next attempt dials fresh.
		st.client.Reset()
		a.proxy.ResetBox(name)
	}
	if prev == state {
		return
	}
	if state == StateOnline {
		a.mu.Lock()
		if st.stopRelay == nil && a.clients[name] == st {
			relayCtx, stop := context.WithCancel(a.ctx)
			st.stopRelay = stop
			go a.relay(relayCtx, name, st.client)
		}
		a.mu.Unlock()
		go a.learnSelfName(a.ctx, name, st.client)
	}
	switch state {
	case StateOnline:
		a.proxy.ResetBox(name)
		a.publish(Event{Type: EventBoxConnected, Box: name})
		a.queue.kick(name)
	case StateUntrusted:
		a.publish(Event{Type: EventBoxUntrusted, Box: name, Error: err.Error()})
	case StateOffline:
		if prev != StateConnecting {
			a.publish(Event{Type: EventBoxDisconnected, Box: name, Error: err.Error()})
		}
	}
}

func (a *Agent) dialer(box string) forward.DialFunc {
	return func(ctx context.Context, port int) (net.Conn, error) {
		c, ok := a.client(box)
		if !ok {
			return nil, fmt.Errorf("box %s is not paired", box)
		}
		return c.DialPort(ctx, port)
	}
}

func (a *Agent) startSavedForwards(ctx context.Context) {
	saved, err := a.forwards.list()
	if err != nil {
		a.cfg.Log.Printf("reading saved forwards: %v", err)
		return
	}
	for _, f := range saved {
		lns, err := forward.Listen(f.Local)
		a.run(ctx, f, lns, err)
	}
}

// run starts f on listeners that are already open, or records why it could
// not start; failed forwards are retried on every health check.
func (a *Agent) run(ctx context.Context, f Forward, lns []net.Listener, listenErr error) {
	rf := &runningForward{fwd: f, state: "listening"}
	if listenErr != nil {
		rf.state, rf.err = "failed", listenErr.Error()
		a.mu.Lock()
		a.running[f.ID] = rf
		a.mu.Unlock()
		a.publish(Event{Type: EventForwardFailed, Box: f.Box, Data: forwardData(f), Error: rf.err})
		return
	}
	fctx, cancel := context.WithCancel(ctx)
	rf.cancel = cancel
	a.mu.Lock()
	if old := a.running[f.ID]; old != nil && old.cancel != nil {
		old.cancel()
	}
	a.running[f.ID] = rf
	a.mu.Unlock()
	for _, ln := range lns {
		go forward.Serve(fctx, ln, f.Remote, a.dialer(f.Box), func(err error) {
			a.cfg.Log.Printf("forward %d → %s:%d: %v", f.Local, f.Box, f.Remote, err)
		})
	}
	a.publish(Event{Type: EventForwardStarted, Box: f.Box, Data: forwardData(f)})
}

func (a *Agent) retryFailedForwards(ctx context.Context) {
	a.mu.Lock()
	var failed []Forward
	for _, rf := range a.running {
		if rf.state == "failed" {
			failed = append(failed, rf.fwd)
		}
	}
	a.mu.Unlock()
	for _, f := range failed {
		if lns, err := forward.Listen(f.Local); err == nil {
			a.run(ctx, f, lns, nil)
		}
	}
}

func (a *Agent) addForward(ctx context.Context, box string, local, remote int, pin string) (Forward, error) {
	if local < 1 || local > 65535 || remote < 1 || remote > 65535 {
		return Forward{}, errors.New("ports must be between 1 and 65535")
	}
	if _, ok := a.client(box); !ok {
		return Forward{}, fmt.Errorf("no paired box named %q", box)
	}
	lns, err := forward.Listen(local)
	if err != nil {
		return Forward{}, err
	}
	f, err := a.forwards.add(Forward{Box: box, Local: local, Remote: remote, Pin: pin})
	if err != nil {
		for _, ln := range lns {
			ln.Close()
		}
		return Forward{}, err
	}
	a.run(ctx, f, lns, nil)
	return f, nil
}

func (a *Agent) removeForward(id string) (Forward, error) {
	f, err := a.forwards.remove(id)
	if err != nil {
		return Forward{}, err
	}
	a.mu.Lock()
	if rf := a.running[id]; rf != nil {
		if rf.cancel != nil {
			rf.cancel()
		}
		delete(a.running, id)
	}
	a.mu.Unlock()
	a.publish(Event{Type: EventForwardRemoved, Box: f.Box, Data: forwardData(f)})
	return f, nil
}

func (a *Agent) status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := Status{Boxes: []BoxStatus{}, Forwards: []ForwardStatus{}, Routes: []Route{}, Proxy: a.proxySt}
	if routes, err := a.routes.list(); err == nil && routes != nil {
		s.Routes = routes
	}
	s.Proxy.URLPort = s.Proxy.Port
	if runtime.GOOS == "darwin" && s.Proxy.Port != 0 && pfredirect.Installed(s.Proxy.Port) {
		s.Proxy.URLPort = 80
	}
	for _, st := range a.clients {
		b := st.status
		b.Local = a.isLocal(st.peer)
		s.Boxes = append(s.Boxes, b)
	}
	for _, rf := range a.running {
		s.Forwards = append(s.Forwards, ForwardStatus{Forward: rf.fwd, State: rf.state, Error: rf.err})
	}
	sort.Slice(s.Boxes, func(i, j int) bool { return s.Boxes[i].Name < s.Boxes[j].Name })
	sort.Slice(s.Forwards, func(i, j int) bool { return s.Forwards[i].Local < s.Forwards[j].Local })
	return s
}

func (a *Agent) shutdown() {
	defer a.cfg.Networks.Close()
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, rf := range a.running {
		if rf.cancel != nil {
			rf.cancel()
		}
	}
	for _, st := range a.clients {
		st.close()
	}
}
