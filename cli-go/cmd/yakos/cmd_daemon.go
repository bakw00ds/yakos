package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"net"

	"github.com/bakw00ds/yakos/internal/jsonrpc"
)

// errDaemonGone is returned by killDaemonProcess when the target process no
// longer exists (race: died between liveness check and kill attempt).
var errDaemonGone = errors.New("daemon process no longer exists")

// parsePID converts the raw content of a PID file into an integer.
// Returns an error for absent, unreadable, or non-numeric content.
// Shared by daemonAlive (OS-specific files) and readPIDFile.
//
// The PID file format is:
//
//	<pid>\n
//	<version>\n   (written since v0.53.0.1; absent on pre-T2 daemons)
//
// Only the first line is parsed here; callers that need the version use
// readPIDFileVersion.
func parsePID(data []byte) (int, error) {
	firstLine := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)[0]
	return strconv.Atoi(strings.TrimSpace(firstLine))
}

// readPIDFileVersion reads the version string from the second line of a pidfile.
// Returns "" when the file is absent, unreadable, or has only one line
// (pre-T2 daemon that did not write a version line).
func readPIDFileVersion(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return ""
	}
	lines := strings.SplitN(string(data), "\n", 3)
	if len(lines) < 2 {
		return ""
	}
	return strings.TrimSpace(lines[1])
}

// queryDaemonVersion returns the version string reported by the running daemon.
// It tries the yakos.version JSON-RPC method first (requires the socket to be
// reachable); if that fails it falls back to reading the second line of the
// pidfile.  Returns "" when neither source is readable — the caller treats
// this as "version unknown" and should restart.
func queryDaemonVersion(socketPath, pidPath string) string {
	// Primary: JSON-RPC yakos.version call.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if client, err := jsonrpc.DialClient(socketPath); err == nil {
		defer client.Close() //nolint:errcheck
		if raw, err := client.Call(ctx, "yakos.version", nil); err == nil {
			var result struct {
				Version string `json:"version"`
			}
			if err := json.Unmarshal(raw, &result); err == nil && result.Version != "" {
				return result.Version
			}
		}
	}
	// Fallback: second line of pidfile.
	return readPIDFileVersion(pidPath)
}

// shouldRestartDaemon returns true when the running daemon's version differs
// from the current binary's version, indicating a stale daemon that needs to
// be replaced.  An empty runningVersion (unknown, e.g. pre-T2 daemon) always
// triggers a restart — safe default.
func shouldRestartDaemon(runningVersion, currentVersion string) bool {
	if runningVersion == "" {
		return true // unknown → restart (safe default)
	}
	return runningVersion != currentVersion
}

// stopStaleDaemon sends SIGTERM to the process identified by pidPath and waits
// up to 5 seconds for the pidfile and socket to disappear (signs of clean exit).
// If the process does not exit within 5 seconds, stopStaleDaemon logs clearly
// and returns — the caller proceeds with a new spawn attempt regardless.
func stopStaleDaemon(pidPath, socketPath string) {
	pid, err := readPIDFile(pidPath)
	if err != nil || pid <= 0 {
		return
	}
	if err := killDaemonProcess(pid); err != nil {
		if !errors.Is(err, errDaemonGone) {
			fmt.Fprintf(os.Stderr, "start: SIGTERM pid %d: %v\n", pid, err)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "start: sent SIGTERM to stale daemon pid %d\n", pid)

	// Poll until pidfile and socket are gone (clean exit) or 5s elapses.
	const poll = 200 * time.Millisecond
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(poll)
		_, pidErr := os.Stat(pidPath)
		_, sockErr := os.Stat(socketPath)
		if os.IsNotExist(pidErr) && os.IsNotExist(sockErr) {
			return // clean exit confirmed
		}
	}
	fmt.Fprintln(os.Stderr, "start: daemon did not exit after SIGTERM; proceeding with spawn anyway")
}

// spawnDaemonFn is the function used by runStart to launch a detached daemon.
// It defaults to spawnDetachedDaemon and can be overridden in tests to record
// whether spawn was invoked without actually forking a process.
var spawnDaemonFn = spawnDetachedDaemon

// pollSocketFn is the function used by runStart to wait for the daemon's
// JSON-RPC socket to become dial-able.  Overridable in tests.
var pollSocketFn = pollUnixSocket

// pollConsolePort dials addr repeatedly until it succeeds or deadline elapses.
// Returns true when the port accepts a connection within deadline, false otherwise.
func pollConsolePort(addr string, deadline, interval time.Duration) bool {
	start := time.Now()
	for {
		conn, err := net.DialTimeout("tcp", addr, interval)
		if err == nil {
			_ = conn.Close()
			return true
		}
		if time.Since(start) >= deadline {
			return false
		}
		time.Sleep(interval)
	}
}

// pollUnixSocket dials the Unix socket at path repeatedly until it succeeds or
// deadline elapses.  Returns true when the socket is dial-able.
// Used to block until the daemon's JSON-RPC socket is ready before the
// share-terminal pump dials it.
func pollUnixSocket(path string, deadline time.Duration) bool {
	const interval = 100 * time.Millisecond
	started := time.Now()
	for {
		conn, err := net.DialTimeout("unix", path, interval)
		if err == nil {
			_ = conn.Close()
			return true
		}
		if time.Since(started) >= deadline {
			return false
		}
		time.Sleep(interval)
	}
}

// readPIDFile reads a PID from a file.  Returns 0 and a non-nil error when
// the file is absent, unreadable, or contains non-numeric content.
func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return 0, err
	}
	pid, err := parsePID(data)
	if err != nil {
		return 0, fmt.Errorf("malformed pid file %s: %w", path, err)
	}
	return pid, nil
}
