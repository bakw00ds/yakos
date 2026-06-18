//go:build windows

package main

import (
	"errors"
	"os"
)

// daemonAlive checks whether the process named in pidPath is still running.
// On Windows, os.FindProcess always succeeds (no existence check), so this
// is a best-effort stub that returns false — daemon auto-spawn is not
// supported on Windows; see spawnDetachedDaemon.
func daemonAlive(pidPath string) bool {
	data, err := os.ReadFile(pidPath) //nolint:gosec
	if err != nil {
		return false
	}
	pid, err := parsePID(data)
	if err != nil || pid <= 0 {
		return false
	}
	// os.FindProcess on Windows does not verify the process exists; it always
	// returns a non-nil *Process.  We use it as a best-effort hint: if we
	// cannot even open the handle, the process is definitely gone.
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal(0) is not available on Windows; treat FindProcess success as alive.
	_ = p
	return true
}

// spawnDetachedDaemon is not supported on Windows because Windows has no
// setsid(2) equivalent in the standard library.  The operator should run
// `yakos serve` in a separate terminal window.
func spawnDetachedDaemon(_ []string) error {
	return errors.New(
		"daemon auto-spawn alongside the REPL is not supported on Windows; " +
			"run 'yakos serve' in a separate terminal",
	)
}

// killDaemonProcess sends the equivalent of SIGTERM on Windows.
// Windows does not support UNIX signals; we use os.FindProcess + Process.Kill
// as the closest practical equivalent.
func killDaemonProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return errDaemonGone
	}
	if err := p.Kill(); err != nil {
		return err
	}
	return nil
}
