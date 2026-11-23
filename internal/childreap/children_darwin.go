package childreap

import (
	"os"

	"golang.org/x/sys/unix"
)

// children asks sysctl for this user's processes, without starting one
// (ps) that would be a child of its own.
func children(parent int) []int {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return nil
	}
	var out []int
	for _, k := range all {
		if int(k.Eproc.Ppid) == parent && k.Proc.P_pid > 0 {
			out = append(out, int(k.Proc.P_pid))
		}
	}
	return out
}
