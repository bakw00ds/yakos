//go:build !windows

package interactive

import (
	"os/exec"
	"syscall"
)

// configureProcAttr makes the child process a process-group leader so the
// entire group (claude + any grandchildren it spawns for tool calls) can be
// reaped atomically by killPG.
//
// Mirrors the pattern in consoleui/bash_exec_unix.go.
func configureProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killPG sends SIGKILL to the entire process group rooted at the child
// (negative PID = group kill on POSIX).  Mirrors consoleui/bash_exec_unix.go.
//
// Called by Close() after stdin is closed, so grandchildren spawned by the
// claude CLI (bash commands, sub-agents) are also reaped.
func killPG(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// Negative PID targets the process group.  ESRCH (already dead) is silently
	// ignored via the blank identifier.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
