package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cosscom/shipyard/internal/database"
	"github.com/cosscom/shipyard/internal/version"
)

const (
	// StateFile says where a running helper listens and the token it asks
	// for. It is written once the helper listens, for its owner alone, and
	// removed when the helper stops: the box daemon reads both from it.
	StateFile = "helper.json"
	// ListenFile remembers where a helper started on port 0 came to
	// listen, and stays when it stops: the next start asks for the same
	// port first.
	ListenFile = "listen.json"
)

// HelperOptions is how to start a helper.
type HelperOptions struct {
	// Dir is the data directory. It is made if it is not there, for its
	// owner alone.
	Dir string
	// Addr is where the API is served: a loopback address, port 0 for any
	// free port.
	Addr string
	// Registry is the engines to open databases with.
	Registry *database.Registry
	// Timeout stops a statement; zero is database.DefaultTimeout.
	Timeout time.Duration
}

// State is what StateFile holds.
type State struct {
	// API is the base URL of the API.
	API string `json:"api"`
	// Token is what every route but /api/health asks for, as
	// "Authorization: Bearer <token>". It is new at every start.
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

// Helper is a running helper.
type Helper struct {
	dir    string
	srv    *Server
	http   *http.Server
	state  State
	cancel context.CancelFunc
	errs   chan error
	// wroteState says StateFile is this helper's to remove.
	wroteState bool

	closeOnce sync.Once
}

// Start opens the data directory and starts listening. Close stops it.
func Start(o HelperOptions) (*Helper, error) {
	if o.Dir == "" {
		return nil, errors.New("no data directory: give --dir or set BERTH_DATABASE_DIR")
	}
	if o.Addr == "" {
		return nil, errors.New("no address to listen on: give --addr")
	}
	// Loopback or nothing: the token is sent in the clear.
	if err := Loopback(o.Addr); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return nil, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	h := &Helper{dir: o.Dir, errs: make(chan error, 1)}
	h.state.Token = hex.EncodeToString(raw)
	var err error
	if h.srv, err = New(Options{Registry: o.Registry, Token: h.state.Token, Dir: o.Dir, Timeout: o.Timeout}); err != nil {
		return nil, err
	}

	var last struct {
		API string `json:"api,omitempty"`
	}
	listenFile := filepath.Join(o.Dir, ListenFile)
	if raw, err := os.ReadFile(listenFile); err == nil {
		json.Unmarshal(raw, &last)
	}
	ln, err := listenAgain(o.Addr, last.API)
	if err != nil {
		h.srv.Close()
		return nil, err
	}
	// Every request's context ends with ctx, so a statement in flight
	// ends when the helper is told to stop.
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.http = &http.Server{Handler: h.srv.Handler(), ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { h.errs <- h.http.Serve(ln) }()

	at := ln.Addr().String()
	// Not being able to remember the port is no reason not to start.
	if at != last.API {
		if raw, err := json.Marshal(map[string]string{"api": at}); err == nil {
			if err := writePrivate(listenFile, append(raw, '\n')); err != nil {
				slog.Warn("could not remember where the helper listens", "kind", fmt.Sprintf("%T", err))
			}
		}
	}
	h.state.API, h.state.PID, h.state.Version = "http://"+at, os.Getpid(), version.Version
	doc, err := json.MarshalIndent(h.state, "", "  ")
	if err == nil {
		err = writePrivate(filepath.Join(o.Dir, StateFile), append(doc, '\n'))
	}
	if err != nil {
		h.Close()
		return nil, err
	}
	h.wroteState = true
	slog.Info("listening", "api", h.state.API, "dir", o.Dir, "engines", o.Registry.Names())
	return h, nil
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

// State says where the helper listens, and its token.
func (h *Helper) State() State { return h.state }

// Failed gives the error of a listener that stopped by itself.
func (h *Helper) Failed() <-chan error { return h.errs }

// Close stops listening and ends every connection to every database.
// Closing twice is closing once.
func (h *Helper) Close() error {
	h.closeOnce.Do(func() {
		if h.cancel != nil {
			h.cancel()
		}
		if h.http != nil {
			shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
			defer done()
			if err := h.http.Shutdown(shutdown); err != nil {
				h.http.Close()
			}
		}
		h.srv.Close()
		// Only the file this helper wrote: one that failed to start must
		// not take away the word of another that is running.
		if h.wroteState {
			os.Remove(filepath.Join(h.dir, StateFile))
		}
	})
	return nil
}

// Run starts a helper and keeps it until ctx ends or its listener fails.
func Run(ctx context.Context, o HelperOptions) error {
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
