//go:build !linux && !darwin

package box

import (
	"syscall"
	"time"
)

// Helpers run on Linux and macOS boxes.
func helperPlatformOK() bool { return false }

func helperSysProcAttr() *syscall.SysProcAttr { return nil }

func terminateHelper(pid int, exited <-chan error, timeout time.Duration) {}

func signalProcess(pid int, sig syscall.Signal) {}

func processAlive(pid int) bool { return false }
