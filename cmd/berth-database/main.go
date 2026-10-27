// Command berth-database runs on a box beside its dev servers and lets
// Shipyard's Database tab browse and edit a worktree's own development
// database: its schema, a table's rows, one statement at a time, and edits
// to rows by their primary key. It reads nothing unless the box daemon asks
// with the token only the two of them hold, and writes nothing unless
// writes were turned on for that database in the last quarter of an hour.
//
// internal/database/API.md describes the API. The engines it opens are the
// ones registered here: add a package beside internal/database/postgres and
// a line to engines.
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
	"syscall"
	"time"

	"github.com/cosscom/shipyard/internal/database"
	"github.com/cosscom/shipyard/internal/database/postgres"
	"github.com/cosscom/shipyard/internal/database/server"
	"github.com/cosscom/shipyard/internal/version"
)

// engines is every kind of database this build opens.
func engines() *database.Registry {
	return database.NewRegistry(
		postgres.Engine{},
	)
}

const usage = `berth-database — browse and edit a worktree's own development database

  berth-database serve --dir DIR [--addr 127.0.0.1:4326] [--statement-timeout 10s]
                                          Serve the API, on localhost only
  berth-database version                  Print this build's version

serve keeps what it is told in DIR (BERTH_DATABASE_DIR): the databases of the worktrees it was
attached to (connections.json, with their passwords, readable by you alone) and, while it runs,
where it is listening and the token it asks for (helper.json). It opens only databases on this
machine. Every flag of serve has a variable: BERTH_DATABASE_ADDR, _STATEMENT_TIMEOUT.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		if err := serve(os.Args[2:]); err != nil {
			slog.Error("berth-database stopped", "err", err)
			os.Exit(1)
		}
	case "version", "--version":
		fmt.Println(version.Line("berth-database"))
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "berth-database: unknown command %q\n\n%s", os.Args[1], usage)
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Run(ctx, o)
}

// options reads serve's flags, each falling back to its variable.
func options(args []string, env func(string) string, errout io.Writer) (server.HelperOptions, error) {
	or := func(key, def string) string {
		if v := env(key); v != "" {
			return v
		}
		return def
	}
	fs := flag.NewFlagSet("berth-database serve", flag.ContinueOnError)
	fs.SetOutput(errout)
	dir := fs.String("dir", env("BERTH_DATABASE_DIR"), "data directory (BERTH_DATABASE_DIR); required")
	addr := fs.String("addr", or("BERTH_DATABASE_ADDR", "127.0.0.1:4326"), "loopback address for the API; port 0 takes any free port")
	timeout := fs.String("statement-timeout", or("BERTH_DATABASE_STATEMENT_TIMEOUT", "10s"), "how long a statement may run before the database stops it, as in 10s or 2m")
	if err := fs.Parse(args); err != nil {
		return server.HelperOptions{}, err
	}
	if fs.NArg() > 0 {
		return server.HelperOptions{}, fmt.Errorf("serve takes flags only, not %q", fs.Arg(0))
	}
	o := server.HelperOptions{Dir: *dir, Addr: *addr, Registry: engines()}
	var err error
	if o.Timeout, err = time.ParseDuration(*timeout); err != nil || o.Timeout < time.Second || o.Timeout > time.Hour {
		return o, fmt.Errorf("--statement-timeout: %q is not a time between 1s and 1h", *timeout)
	}
	return o, nil
}
