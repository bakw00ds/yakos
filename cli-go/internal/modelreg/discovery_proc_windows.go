//go:build windows

package modelreg

import "os/exec"

// isolateProcess does nothing on Windows: there is no process group to kill, so
// the context's cancel kills the program itself (the default) and a descendant it
// started may outlive it. The wait delay still bounds the probe.
func isolateProcess(*exec.Cmd) {}
