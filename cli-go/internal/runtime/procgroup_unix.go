//go:build !windows

package runtime

import (
	"os/exec"
	"syscall"
)

// platformGroupKill makes the child a process-group leader and returns a
// function that SIGKILLs the whole group, so a grandchild that inherited the
// stdout pipe (a harness running "sleep 77 &") dies with its parent.
// Mirrors interactive/procattr_unix.go and consoleui/bash_exec_unix.go.
func platformGroupKill(cmd *exec.Cmd) func() error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative PID targets the group; ESRCH (already gone) is not an error
		// worth surfacing.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return nil
	}
}
