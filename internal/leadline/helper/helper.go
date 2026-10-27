// Package helper is berth-leadline put together: the store in its data
// directory, the Postgres proxies recording into it, and the HTTP listener
// that takes OTLP and answers the API. cmd/berth-leadline runs it; tests
// start it on port 0 in a temporary directory.
package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cosscom/shipyard/internal/leadline/api"
	"github.com/cosscom/shipyard/internal/leadline/capture"
	"github.com/cosscom/shipyard/internal/leadline/pgproxy"
	"github.com/cosscom/shipyard/internal/leadline/store"
	"github.com/cosscom/shipyard/internal/version"
)

const (
	// DatabaseFile and StateFile are the names of what the helper keeps in
	// its data directory, beside the proxies' own proxies.json.
	DatabaseFile = "leadline.db"
	// StateFile says where a running helper listens. It is written once
	// the listeners are up and removed when the helper stops, so whoever
	// started the helper on port 0 can find it.
	StateFile = "helper.json"
	// ListenFile remembers where a helper started on port 0 came to
	// listen, and stays when it stops: the next start asks for the same
	// ports first. A dev server is told the helper's address once, when it
	// starts, and keeps sending there across the helper's restarts.
	ListenFile = "listen.json"
)

// listened is what ListenFile holds: the host:port of each listener.
type listened struct {
	API  string `json:"api,omitempty"`
	OTLP string `json:"otlp,omitempty"`
}

// listenAgain listens on addr. For port 0, "any free port", it first asks
// for the port this helper had the last time, which is free again unless
// something else took it since.
func listenAgain(addr, last string) (net.Listener, error) {
	host, port, err := net.SplitHostPort(addr)
	if err == nil && port == "0" && last != "" {
		if lastHost, lastPort, err := net.SplitHostPort(last); err == nil && lastHost == host && lastPort != "0" {
			if ln, err := net.Listen("tcp", last); err == nil {
				return ln, nil
			}
		}
	}
	return net.Listen("tcp", addr)
}

// Options is how to start a helper.
type Options struct {
	// Dir is the data directory. It is made if it is not there, for its
	// owner alone.
	Dir string
	// Addr is where the API and OTLP/HTTP are served: a loopback address,
	// port 0 for any free port.
	Addr string
	// OTLPAddr, when set, is a second loopback address that takes OTLP and
	// answers nothing else: an address to give to apps.
	OTLPAddr string
	// Keep is how much the store holds on to.
	Keep store.Retention
	// Routes are proxies to open at the start, from the --pg flag.
	Routes []pgproxy.Route
	// Ports is where the proxies opened over the API get their ports from.
	Ports capture.Ports
}

// State is what StateFile holds.
type State struct {
	// API is the base URL of the API; it takes OTLP too.
	API string `json:"api"`
	// OTLP are the base URLs that take OTLP/HTTP, the API's own first.
	OTLP    []string `json:"otlp"`
	PID     int      `json:"pid"`
	Version string   `json:"version"`
}

// Helper is a running helper.
type Helper struct {
	dir     string
	store   *store.Store
	proxies *capture.Manager
	servers []*http.Server
	state   State
	cancel  context.CancelFunc
	errs    chan error
	// wroteState says StateFile is this helper's to remove.
	wroteState bool

	closeOnce sync.Once
	closeErr  error
}

// Start opens the data directory and starts listening. Close stops it.
func Start(o Options) (*Helper, error) {
	if o.Dir == "" {
		return nil, errors.New("no data directory: give --dir or set BERTH_LEADLINE_DIR")
	}
	// Loopback or nothing: what the helper holds is SQL and logs, it has
	// no password, and the proxies speak plaintext.
	for _, addr := range []string{o.Addr, o.OTLPAddr} {
		if addr == "" {
			continue
		}
		if err := Loopback(addr); err != nil {
			return nil, err
		}
	}
	if o.Addr == "" {
		return nil, errors.New("no address to listen on: give --addr")
	}
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(o.Dir, DatabaseFile))
	if err != nil {
		return nil, err
	}
	h := &Helper{dir: o.Dir, store: st, errs: make(chan error, 2)}
	fail := func(err error) (*Helper, error) {
		h.Close()
		return nil, err
	}
	st.Retain(o.Keep)

	// The proxies wake the API's event streams, and the API lists the
	// proxies: each is made knowing the other.
	var made atomic.Pointer[api.Server]
	notify := func() {
		if srv := made.Load(); srv != nil {
			srv.Notify()
		}
	}
	if h.proxies, err = capture.Open(capture.Options{Dir: o.Dir, Store: st, Notify: notify, Ports: o.Ports}); err != nil {
		return fail(err)
	}
	srv := api.New(st, h.proxies)
	made.Store(srv)
	if err := h.proxies.Declare(o.Routes); err != nil {
		return fail(err)
	}

	// Every request's context ends with ctx. An event stream never ends by
	// itself, so this is what lets the servers shut down at once.
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	base := func(net.Listener) context.Context { return ctx }
	var last, now listened
	if raw, err := os.ReadFile(filepath.Join(o.Dir, ListenFile)); err == nil {
		json.Unmarshal(raw, &last)
	}
	listen := func(addr, last string, handler http.Handler) (url, at string, err error) {
		ln, err := listenAgain(addr, last)
		if err != nil {
			return "", "", err
		}
		server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, BaseContext: base}
		h.servers = append(h.servers, server)
		go func() { h.errs <- server.Serve(ln) }()
		return "http://" + ln.Addr().String(), ln.Addr().String(), nil
	}
	if h.state.API, now.API, err = listen(o.Addr, last.API, srv.Handler()); err != nil {
		return fail(err)
	}
	h.state.OTLP = []string{h.state.API}
	if o.OTLPAddr != "" {
		var url string
		if url, now.OTLP, err = listen(o.OTLPAddr, last.OTLP, srv.OTLPHandler()); err != nil {
			return fail(fmt.Errorf("the OTLP address: %w", err))
		}
		h.state.OTLP = append(h.state.OTLP, url)
	}
	// Not being able to remember the ports is no reason not to start.
	if raw, err := json.Marshal(now); err == nil && now != last {
		if err := os.WriteFile(filepath.Join(o.Dir, ListenFile), append(raw, '\n'), 0o600); err != nil {
			slog.Warn("could not remember where the helper listens", "err", err)
		}
	}
	srv.SetOTLP(h.state.OTLP...)
	h.state.PID, h.state.Version = os.Getpid(), version.Version
	if err := h.writeState(); err != nil {
		return fail(err)
	}
	slog.Info("listening", "api", h.state.API, "otlp", h.state.OTLP, "dir", o.Dir)
	return h, nil
}

// Loopback checks that an address to listen on is on this machine only.
func Loopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("listen address %q is not on this machine only; use 127.0.0.1", addr)
	}
	return nil
}

func (h *Helper) writeState() error {
	raw, err := json.MarshalIndent(h.state, "", "  ")
	if err != nil {
		return err
	}
	file := filepath.Join(h.dir, StateFile)
	if err := os.WriteFile(file+".tmp", append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(file+".tmp", file); err != nil {
		return err
	}
	h.wroteState = true
	return nil
}

// State says where the helper listens.
func (h *Helper) State() State { return h.state }

// Flush has everything taken in so far written: what the proxies recorded
// and what the store was handed. A read through the API after it sees all
// of it.
func (h *Helper) Flush() {
	h.proxies.Flush()
	h.store.Flush()
}

// Failed gives the error of a listener that stopped by itself.
func (h *Helper) Failed() <-chan error { return h.errs }

// Close stops listening, ends the proxies' connections, writes what was
// still waiting and closes the database. Closing twice is closing once.
func (h *Helper) Close() error {
	h.closeOnce.Do(func() { h.closeErr = h.close() })
	return h.closeErr
}

func (h *Helper) close() error {
	if h.cancel != nil {
		h.cancel()
	}
	// The streams have ended with the context. What is left is a request
	// in the middle of being answered, which gets a moment and no more.
	shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	for _, s := range h.servers {
		if err := s.Shutdown(shutdown); err != nil {
			s.Close()
		}
	}
	h.servers = nil
	if h.proxies != nil {
		h.proxies.Close()
	}
	// Only the file this helper wrote: one that failed to start must not
	// take away the word of another that is running.
	if h.wroteState {
		os.Remove(filepath.Join(h.dir, StateFile))
	}
	return h.store.Close()
}

// Run starts a helper and keeps it until ctx ends or a listener fails.
func Run(ctx context.Context, o Options) error {
	h, err := Start(o)
	if err != nil {
		return err
	}
	var failed error
	select {
	case <-ctx.Done():
	case err := <-h.Failed():
		if !errors.Is(err, http.ErrServerClosed) {
			failed = err
		}
	}
	return errors.Join(failed, h.Close())
}
