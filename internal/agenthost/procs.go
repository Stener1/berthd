package agenthost

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// descendants are pid's children, theirs, and so on, deepest last. An
// agent's tools can start processes in groups and sessions of their own,
// which a signal to the agent's group doesn't reach.
func descendants(pid int) []int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=").Output()
	if err != nil {
		return nil
	}
	children := map[int][]int{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		p, err1 := strconv.Atoi(f[0])
		pp, err2 := strconv.Atoi(f[1])
		if err1 == nil && err2 == nil {
			children[pp] = append(children[pp], p)
		}
	}
	var all []int
	queue := []int{pid}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[p] {
			all = append(all, c)
			queue = append(queue, c)
		}
	}
	return all
}

// signalTree sends sig to the process tree under pid (its descendants as
// they are now, then its group, then pid itself).
func signalTree(pid int, sig syscall.Signal) {
	kids := descendants(pid)
	for i := len(kids) - 1; i >= 0; i-- {
		syscall.Kill(kids[i], sig)
	}
	syscall.Kill(-pid, sig)
	syscall.Kill(pid, sig)
}
