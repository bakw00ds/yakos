//go:build !windows

package consoleui_test

import "syscall"

// pidAlive reports whether a process with this pid still exists.
func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }
