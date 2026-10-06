package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sean-brydon/berthd/internal/box"
	"github.com/sean-brydon/berthd/internal/statefile"
)

// secretExec is how a worktree's service gets its secrets: its unit runs
// `berthd secret exec -- PROGRAM...` with the references in its environment
// and their names in BERTH_SECRET_VARS, and this resolves them in memory and
// replaces itself with the program. The unit file on disk only ever holds
// the references. Terminal and agent sessions in a worktree with secrets run
// behind it too, since tmux would otherwise take the values as arguments. A
// reference that cannot be resolved leaves its variable unset, and the box
// hears about it and announces it as a secret.failed event.
func secretExec(b boxHome, args []string) error {
	fs := flag.NewFlagSet("secret exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	socket := fs.String("socket", b.socket(), "the box's local API socket")
	if err := fs.Parse(args); err != nil {
		return err
	}
	argv := fs.Args()
	if len(argv) == 0 {
		return errors.New("usage: berthd secret exec [--socket PATH] -- PROGRAM [ARGS...]")
	}
	refs := map[string]string{}
	for _, k := range strings.Split(os.Getenv(box.SecretVarsEnv), ",") {
		if k = strings.TrimSpace(k); k != "" {
			refs[k] = os.Getenv(k)
			// Never pass a reference on as if it were the value.
			os.Unsetenv(k)
		}
	}
	os.Unsetenv(box.SecretVarsEnv)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	values, failed := b.boxSecrets().ResolveAll(ctx, refs, nil)
	rep := box.SecretReport{Location: os.Getenv("BERTH_LOCATION"), Name: os.Getenv("BERTH_WORKTREE_NAME"), Path: os.Getenv("BERTH_WORKTREE_PATH")}
	for k, v := range values {
		os.Setenv(k, v)
		rep.Results = append(rep.Results, box.SecretResult{Variable: k, Ref: refs[k]})
	}
	for k, err := range failed {
		fmt.Fprintf(os.Stderr, "berthd: %s is not set: %s (%s)\n", k, err, refs[k])
		rep.Results = append(rep.Results, box.SecretResult{Variable: k, Ref: refs[k], Reason: err.Error()})
	}
	if len(rep.Results) > 0 {
		reportSecrets(*socket, rep)
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(bin, argv, os.Environ())
}

// reportSecrets tells the box, if berthd is running, which variables
// resolved and which could not and why, so the app can say so. Never a
// value.
func reportSecrets(socket string, rep box.SecretReport) {
	if _, err := os.Stat(socket); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	box.NewClient(box.NewLocal(socket)).ReportSecrets(ctx, rep)
}

// opSessionFile keeps the session `berthd secret signin` made, for berthd's
// reads of op:// references.
func (b boxHome) opSessionFile() string { return filepath.Join(b.dir, "op-session") }

// boxSecrets is how this box reads secrets, outside berthd serve too.
func (b boxHome) boxSecrets() *box.Secrets {
	host, _ := os.Hostname()
	return &box.Secrets{SessionFile: b.opSessionFile(), Box: host}
}

// secretSignin signs the box's op in to 1Password, in the terminal it runs
// in, and keeps the session for berthd: a team setup's 1Password step runs
// it, and so can the box's user. `op signin` on its own only signs in the
// shell it runs in, which berthd can't see. With --check it only says
// whether berthd can read 1Password now (exit 0) or not (exit 1), asking
// nothing.
func secretSignin(b boxHome, args []string) error {
	fs := flag.NewFlagSet("secret signin", flag.ContinueOnError)
	check := fs.Bool("check", false, "only check whether berthd can read 1Password on this box")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s := b.boxSecrets()
	var opEnv []string
	if dir, err := statefile.UserDir(); err == nil {
		opEnv = box.LoadOpEnv(filepath.Join(dir, "env.json"))
	}
	ctx := context.Background()
	bin, err := s.OpBinary()
	if err != nil {
		fmt.Fprintln(os.Stderr, "op, the 1Password CLI, is not installed on this box. Install it (https://developer.1password.com/docs/cli/get-started/), then try again.")
		return exitCode(127)
	}
	if who, err := s.OpWhoami(ctx, opEnv); err == nil {
		fmt.Printf("1Password: this box is signed in%s.\n", firstLine(who, " as "))
		return nil
	} else if *check {
		fmt.Fprintln(os.Stderr, err)
		return exitCode(1)
	}
	fmt.Println("Sign op in to your 1Password account. If this box has none yet, op asks to add")
	fmt.Println("one: your sign-in address (such as my.1password.com), email, Secret Key and")
	fmt.Println("password. You type them here; Berth never sees them. It keeps only op's session,")
	fmt.Println("readable by you alone, so the box can read the team's shared keys.")
	fmt.Println()
	cmd := exec.Command(bin, "signin")
	cmd.Env = append(os.Environ(), opEnv...)
	cmd.Stdin, cmd.Stderr = os.Stdin, os.Stderr
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "op signin did not finish:", err)
		return exitCode(1)
	}
	vars := box.ParseOpSignin(out.String())
	if len(vars) > 0 {
		if err := box.SaveOpSession(b.opSessionFile(), vars); err != nil {
			return err
		}
	}
	who, err := s.OpWhoami(ctx, opEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "op signed in, but berthd still can't read 1Password:", err)
		return exitCode(1)
	}
	fmt.Printf("\n1Password: signed in%s. berthd reads op:// references with this session; op ends it\n", firstLine(who, " as "))
	fmt.Println("after 30 minutes unused, and then this asks again. A service account never ends:")
	fmt.Println("put OP_SERVICE_ACCOUNT_TOKEN in ~/.berth/env.json instead.")
	return nil
}

func firstLine(s, prefix string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if s == "" {
		return ""
	}
	return prefix + s
}

// exitCode ends berthd with a status, after what was printed.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
