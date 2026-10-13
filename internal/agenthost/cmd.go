package agenthost

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Main is `berthd agent-host --dir DIR [--socket S] [--cwd W] -- COMMAND...`,
// run by berthd as a tmux session's program: it hosts the agent until it
// ends, and returns its exit code. Closing the session (SIGHUP) or SIGTERM
// stops the agent first.
func Main(args []string, stdout io.Writer) (int, error) {
	fs := flag.NewFlagSet("agent-host", flag.ContinueOnError)
	dir := fs.String("dir", "", "the conversation's folder (its log, and socket)")
	sock := fs.String("socket", "", "where to listen (default DIR/"+SocketName+")")
	cwd := fs.String("cwd", "", "where the agent runs (default here)")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if *dir == "" || fs.NArg() == 0 {
		return 2, errors.New("usage: berthd agent-host --dir DIR [--socket S] [--cwd W] -- COMMAND...")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	defer stop()
	return Run(ctx, Options{Dir: *dir, Socket: *sock, WorkDir: *cwd, Command: fs.Args(), Render: stdout, Env: os.Environ()})
}
