//go:build linux || darwin

package childreap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// The test binary plays berthd across an upgrade: in stage "before" it
// starts two children and leaves them, then execs itself into stage
// "after", which is the new build looking at what it inherited.
const stageEnv = "CHILDREAP_TEST_STAGE"

type report struct {
	Inherited int    `json:"inherited"`
	Reaped    int    `json:"reaped"`
	Left      int    `json:"left"`
	OwnErr    string `json:"ownErr"`
}

func TestMain(m *testing.M) {
	switch os.Getenv(stageEnv) {
	case "before":
		before()
	case "after", "after-no-reap":
		after(os.Getenv(stageEnv) == "after")
	}
	os.Exit(m.Run())
}

func before() {
	// One that has ended by the time of the exec (a tmux client whose
	// terminal went), one that ends after it (a git command in flight).
	for _, script := range []string{"exit 0", "sleep 0.3"} {
		if err := exec.Command("/bin/sh", "-c", script).Start(); err != nil {
			fail(err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	stage := "after"
	if os.Getenv("CHILDREAP_TEST_NO_REAP") != "" {
		stage = "after-no-reap"
	}
	os.Setenv(stageEnv, stage)
	fail(syscall.Exec(os.Args[0], os.Args, os.Environ()))
}

func after(reap bool) {
	pids := Inherited()
	r := report{Inherited: len(pids)}
	// A child of the new build's own, running while Reap does: its exit
	// status stays os/exec's.
	own := exec.Command("/bin/sh", "-c", "sleep 0.2")
	if err := own.Start(); err != nil {
		fail(err)
	}
	if reap {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		r.Reaped = Reap(ctx, pids, 20*time.Millisecond)
		cancel()
	} else {
		time.Sleep(600 * time.Millisecond)
	}
	if err := own.Wait(); err != nil {
		r.OwnErr = err.Error()
	}
	r.Left = len(Inherited())
	json.NewEncoder(os.Stdout).Encode(r)
	os.Exit(0)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}

func runUpgrade(t *testing.T, env ...string) report {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(append(os.Environ(), stageEnv+"=before"), env...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the upgraded process failed: %v (%s)", err, out)
	}
	var r report
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("reading its report %q: %v", out, err)
	}
	return r
}

func TestChildrenFromBeforeAnExecAreReaped(t *testing.T) {
	r := runUpgrade(t)
	if r.Inherited != 2 || r.Reaped != 2 || r.Left != 0 {
		t.Fatalf("inherited %d, reaped %d, left %d; want 2, 2, 0", r.Inherited, r.Reaped, r.Left)
	}
	if r.OwnErr != "" {
		t.Fatalf("the new build's own child lost its exit status: %s", r.OwnErr)
	}
}

func TestWithoutReapThemTheyStayZombies(t *testing.T) {
	r := runUpgrade(t, "CHILDREAP_TEST_NO_REAP=1")
	if r.Inherited != 2 || r.Left != 2 {
		t.Fatalf("inherited %d, left %d; want both left: the test would prove nothing", r.Inherited, r.Left)
	}
}

func TestAPidThatIsNotAChildIsDropped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if n := Reap(ctx, []int{1}, time.Millisecond); n != 0 || ctx.Err() != nil {
		t.Fatalf("Reap of pid 1: reaped %d, ctx %v; want 0 at once", n, ctx.Err())
	}
}
