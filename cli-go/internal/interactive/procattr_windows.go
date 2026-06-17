//go:build windows

package interactive

import "os/exec"

// configureProcAttr is a no-op on Windows.
// Windows does not expose POSIX process groups.  killPG below terminates the
// direct child only.  Mirrors consoleui/bash_exec_windows.go.
func configureProcAttr(_ *exec.Cmd) {}

// killPG kills the direct child process on Windows.
// Backgrounded grandchildren may survive as orphans — a known limitation,
// identical to the bash pass-through behaviour on Windows.
func killPG(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
