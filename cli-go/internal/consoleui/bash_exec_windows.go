//go:build windows

package consoleui

import (
	"os/exec"
)

// configureProcAttr is a no-op on Windows.
//
// Windows does not expose POSIX process groups.  The best-effort kill in
// killProcessGroup (below) terminates the direct child only; backgrounded
// grandchildren may outlive the deadline.  This is a known limitation of
// the Windows build: operators running yakOS on Windows with commands that
// background sub-processes should expect those processes to survive past
// the 30-second exec timeout.
func configureProcAttr(_ *exec.Cmd) {}

// killProcessGroup kills the direct child process on Windows.
//
// Called unconditionally via defer in handleBash after shCmd.Wait() returns.
// On Windows there is no POSIX-style process-group kill; backgrounded
// grandchildren ("cmd &") may survive as orphans after this call. Proper
// group reaping would require a Windows Job Object (elevated privileges,
// significantly more complexity) — that is out of scope. This best-effort
// kill covers the common case: the sh wrapper itself.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
