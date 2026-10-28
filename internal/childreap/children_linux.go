package childreap

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

// children reads /proc/PID/stat for each process: pid (comm) state ppid …,
// where comm may hold spaces and parentheses.
func children(parent int) []int {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		end := bytes.LastIndexByte(b, ')')
		if end < 0 {
			continue
		}
		f := strings.Fields(string(b[end+1:]))
		if len(f) < 2 {
			continue
		}
		if ppid, err := strconv.Atoi(f[1]); err == nil && ppid == parent {
			out = append(out, pid)
		}
	}
	return out
}
