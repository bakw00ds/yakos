//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// daemonAlive reads the PID file at pidPath and returns true when the process
// with that PID is currently running.  Uses kill(pid, 0) — no signal sent,
// only existence checked.  Returns false for absent, stale, or malformed files.
func daemonAlive(pidPath string) bool {
	data, err := os.ReadFile(pidPath) //nolint:gosec
	if err != nil {
		return false
	}
	pid, err := parsePID(data)
	if err != nil || pid <= 0 {
		return false
	}
	// nil = process running; EPERM = running but no permission to signal.
	err = syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// spawnDetachedDaemon starts `yakos serve <serveArgs>` as a detached child
// process (new session, not waited on).  The child inherits the parent's
// stdout/stderr so the setup token and banner URL appear on the terminal.
// YAKOS_IMPL=go is forced in the child environment so the Go binary handles
// the serve subcommand regardless of the parent's YAKOS_IMPL setting.
func spawnDetachedDaemon(serveArgs []string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	argv := append([]string{"serve"}, serveArgs...)
	cmd := exec.Command(exe, argv...) //nolint:gosec
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Build child env: inherit everything, then force YAKOS_IMPL=go so the
	// child's serve subcommand routes through the Go implementation.
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, "YAKOS_IMPL=") {
			filtered = append(filtered, kv)
		}
	}
	filtered = append(filtered, "YAKOS_IMPL=go")
	cmd.Env = filtered
	return cmd.Start()
}

// killDaemonProcess sends SIGTERM to the process with the given PID.
// Returns nil on success, a sentinel errDaemonGone when the process no longer
// exists, or a wrapped error for unexpected failures.
func killDaemonProcess(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return errDaemonGone
		}
		return err
	}
	return nil
}
