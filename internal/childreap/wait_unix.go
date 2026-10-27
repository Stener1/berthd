//go:build unix

package childreap

import (
	"errors"
	"syscall"
)

// wait collects pid's exit status if it has ended (done), or says it is
// not this process's child (gone). A pid stays this process's until it is
// waited for, so the system can't give it to another process meanwhile.
func wait(pid int) (done, gone bool) {
	var ws syscall.WaitStatus
	got, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
	switch {
	case errors.Is(err, syscall.EINTR):
		return false, false
	case err != nil:
		return false, true
	}
	return got == pid, false
}
