// Package childreap waits for the children a process inherited from the
// program it was before an exec.
//
// berthd upgrades itself by exec'ing the new build in its own process. The
// process id stays, so whatever the old build had started (a terminal's
// tmux client, a git command, a helper) is still its child, but the new
// build holds no exec.Cmd for it and would never wait for it: each one
// stays a zombie once it ends. Inherited lists them before the new build
// starts anything of its own, and Reap waits for exactly those, never for
// any child (wait4(-1) would take exit statuses that os/exec is waiting
// for).
package childreap

import (
	"context"
	"os"
	"slices"
	"time"
)

// Inherited is this process's children right now. Call it before the
// process starts any of its own: then each one was started by the program
// this process was before it exec'd.
func Inherited() []int {
	return children(os.Getpid())
}

// Reap waits for each of pids to end, looking every interval, and returns
// how many it waited for once all have or ctx ends. A pid that is no
// longer this process's child is dropped.
func Reap(ctx context.Context, pids []int, every time.Duration) int {
	left := slices.Clone(pids)
	reaped := 0
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		left = slices.DeleteFunc(left, func(pid int) bool {
			done, gone := wait(pid)
			if done {
				reaped++
			}
			return done || gone
		})
		if len(left) == 0 {
			return reaped
		}
		select {
		case <-ctx.Done():
			return reaped
		case <-t.C:
		}
	}
}
