//go:build windows

package doctor

import "os/exec"

// configureProcAttr is a no-op on Windows: it has no POSIX process-group
// concept. killProcessGroup below terminates the direct child only.
// Mirrors internal/interactive/procattr_windows.go and
// internal/consoleui/bash_exec_windows.go's identical pattern.
func configureProcAttr(_ *exec.Cmd) {}

// killProcessGroup kills the direct child process on Windows. A
// backgrounded grandchild (rare for a single `gh`/`git` invocation) may
// survive as an orphan — a known, documented limitation identical to the
// interactive/consoleui subprocess handling on Windows.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
