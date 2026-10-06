//go:build windows

package modelreg

import "os/exec"

// isolateProcess does nothing on Windows: there is no process group to kill, so
// the context's cancel kills the program itself (the default) and a descendant it
// started may outlive it. The wait delay still bounds the probe.
func isolateProcess(*exec.Cmd) {}

// killProcessGroup does nothing on Windows (see isolateProcess): a helper that
// held the pipes after the command exited is left running there.
func killProcessGroup(*exec.Cmd) {}

// processAlive cannot tell on Windows without a handle to the process, so it says
// yes: a directory is then kept until it is old enough to be a leftover whatever
// its pid says (see discovery_sweep.go).
func processAlive(int) bool { return true }
