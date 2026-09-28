//go:build !windows

package doctor

import (
	"os/exec"
	"syscall"
)

// configureProcAttr makes the child a process-group leader so the entire
// group (e.g. gh/git plus any credential-helper or browser child it spawns)
// can be reaped atomically by killProcessGroup on timeout.
//
// Mirrors internal/interactive/procattr_unix.go and
// internal/consoleui/bash_exec_unix.go's identical pattern.
func configureProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGKILL to the entire process group rooted at the
// child (negative PID = group kill on POSIX). Called from cmd.Cancel on a
// runCommandWithTimeout deadline, so a hung `gh`/`git` — and anything it
// spawned — is actually terminated rather than left orphaned.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// ESRCH (already dead) is expected and silently ignored.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
