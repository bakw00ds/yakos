//go:build !windows

package consoleui

import (
	"os/exec"
	"syscall"
)

// configureProcAttr makes the child process a process-group leader so that
// the entire group (the sh wrapper + any grandchildren it backgrounds) can
// be reaped atomically by killProcessGroup.
//
// Without Setpgid a backgrounded grandchild ("sleep 60 &") inherits the
// parent's fd-set and keeps stdout/stderr open. exec.Cmd.Wait() then blocks
// waiting for those fds to close, defeating the 30-second bashExecTimeout.
func configureProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGKILL to the entire process group rooted at the
// child (negative PID = group kill on POSIX systems).
//
// It is called unconditionally via defer in handleBash after shCmd.Wait()
// returns, which closes the orphan-leak window: for commands that background
// a grandchild ("sleep 60 &"), sh exits 0 immediately without cancelling the
// 30 s context, so the grandchild would otherwise survive as a live orphan.
//
// If the process has not started yet (cmd.Process == nil), this is a no-op.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// Negative PID targets the process group; Setpgid above ensures the
	// group ID equals the child's PID. ESRCH (already dead) is silently
	// ignored via the blank identifier.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
