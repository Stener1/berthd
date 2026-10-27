// Command berth-leadline runs on a box beside its dev servers and records
// what their code did: every database query, grouped by shape and checked
// for N+1s, the traces of their requests, and their logs. It takes
// OpenTelemetry over OTLP/HTTP, records Postgres statements through a proxy,
// keeps both in one SQLite file, and answers a JSON API on localhost.
//
// It is Leadline's collector (github.com/sean-brydon/leadline) as a helper
// of Shipyard's. internal/leadline/API.md describes the API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/cosscom/shipyard/internal/leadline/capture"
	"github.com/cosscom/shipyard/internal/leadline/helper"
	"github.com/cosscom/shipyard/internal/leadline/pgproxy"
	"github.com/cosscom/shipyard/internal/leadline/store"
	"github.com/cosscom/shipyard/internal/version"
)

const usage = `berth-leadline — records what the code on this box did in development

  berth-leadline serve --dir DIR [--addr 127.0.0.1:4325] [--otlp ADDR] [--retain 72h] [--max-size 2GB]
                       [--pg LISTEN=UPSTREAM]... [--proxy-ports 25432-25531]
                                          Take OTLP/HTTP and serve the API, on localhost only
  berth-leadline version                  Print this build's version

serve keeps everything in DIR (BERTH_LEADLINE_DIR): leadline.db, the proxies it was asked for
(proxies.json), and where it is listening while it runs (helper.json). Nothing leaves this machine.
Every flag of serve has a variable: BERTH_LEADLINE_ADDR, _OTLP_ADDR, _RETAIN, _MAX_SIZE, _PG, _PROXY_PORTS.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		if err := serve(os.Args[2:]); err != nil {
			slog.Error("berth-leadline stopped", "err", err)
			os.Exit(1)
		}
	case "version", "--version":
		fmt.Println(version.Line("berth-leadline"))
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "berth-leadline: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

func serve(args []string) error {
	o, err := options(args, os.Getenv, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	// The signals are taken first and given back last, after the database
	// has closed. Until then a second Ctrl-C is only noted: let through, it
	// would kill the helper in the middle of its last write.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		<-signals
		cancel()
		said := false
		for range signals {
			if !said {
				slog.Info("already stopping: writing what is left and closing the database")
				said = true
			}
		}
	}()
	return helper.Run(ctx, o)
}

// options reads serve's flags, each falling back to its variable.
func options(args []string, env func(string) string, errout io.Writer) (helper.Options, error) {
	or := func(key, def string) string {
		if v := env(key); v != "" {
			return v
		}
		return def
	}
	var routes pgproxy.Routes
	fs := flag.NewFlagSet("berth-leadline serve", flag.ContinueOnError)
	fs.SetOutput(errout)
	dir := fs.String("dir", env("BERTH_LEADLINE_DIR"), "data directory (BERTH_LEADLINE_DIR); required")
	addr := fs.String("addr", or("BERTH_LEADLINE_ADDR", "127.0.0.1:4325"), "loopback address for the API and OTLP/HTTP; port 0 takes any free port")
	otlp := fs.String("otlp", env("BERTH_LEADLINE_OTLP_ADDR"), "a second loopback address that takes OTLP/HTTP and nothing else")
	retain := fs.String("retain", or("BERTH_LEADLINE_RETAIN", "72h"), "how long to keep data, as in 72h or 7d; 0 keeps it forever")
	maxSize := fs.String("max-size", or("BERTH_LEADLINE_MAX_SIZE", "2GB"), "drop the oldest data early once the database is larger than this; 0 is no cap")
	ports := fs.String("proxy-ports", or("BERTH_LEADLINE_PROXY_PORTS", ""), "the ports proxies are given, as in 25432-25531")
	fs.Var(&routes, "pg", "record a Postgres's queries by proxying it: listen=upstream, as in 127.0.0.1:5433=127.0.0.1:5432; repeatable, or BERTH_LEADLINE_PG with commas between")
	if err := fs.Parse(args); err != nil {
		return helper.Options{}, err
	}
	if fs.NArg() > 0 {
		return helper.Options{}, fmt.Errorf("serve takes flags only, not %q", fs.Arg(0))
	}
	o := helper.Options{Dir: *dir, Addr: *addr, OTLPAddr: *otlp, Ports: capture.DefaultPorts}
	if o.OTLPAddr == "off" {
		o.OTLPAddr = ""
	}
	var err error
	if o.Keep.MaxAge, err = store.ParseAge(*retain); err != nil {
		return o, errors.New("--retain: " + err.Error())
	}
	if o.Keep.MaxBytes, err = store.ParseSize(*maxSize); err != nil {
		return o, errors.New("--max-size: " + err.Error())
	}
	if *ports != "" {
		if o.Ports, err = portRange(*ports); err != nil {
			return o, errors.New("--proxy-ports: " + err.Error())
		}
	}
	if o.Routes, err = pgproxy.RoutesOr(routes, env("BERTH_LEADLINE_PG")); err != nil {
		return o, err
	}
	return o, nil
}

// portRange reads "first-last".
func portRange(s string) (capture.Ports, error) {
	first, last, ok := strings.Cut(s, "-")
	a, errA := strconv.Atoi(strings.TrimSpace(first))
	b, errB := strconv.Atoi(strings.TrimSpace(last))
	if !ok || errA != nil || errB != nil || a < 1024 || b > 65535 || a > b {
		return capture.Ports{}, fmt.Errorf("%q is not a range of ports like 25432-25531", s)
	}
	return capture.Ports{First: a, Last: b}, nil
}
