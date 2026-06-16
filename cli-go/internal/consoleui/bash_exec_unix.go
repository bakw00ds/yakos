//go:build !windows

package consoleui

import (
	"os/exec"
	"syscall"
)

// configureProcAttr makes the child process a process-group leader so that
// the entire group (child + any grandchildren it backgrounds) can be
// terminated atomically when the context deadline fires.
//
// Without Setpgid the backgrounded grandchild inherits the same fd-set as the
// sh wrapper and exec.Cmd.Run() blocks waiting for them to close stdout/stderr,
// defeating the 30-second bashExecTimeout.
func configureProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGKILL to the entire process group rooted at the
// child (negative PID = group kill on POSIX systems).  Called by Cmd.Cancel
// when the context deadline fires.
//
// If the process has not started yet (cmd.Process == nil), this is a no-op.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// -pid targets the process group; Setpgid above ensures the group ID
	// equals the child's PID.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
