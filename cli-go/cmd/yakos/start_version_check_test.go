// start_version_check_test.go — unit tests for daemon version-mismatch detection.
//
// These are pure-function tests for the decision logic extracted as helpers:
//
//   - shouldRestartDaemon: given running vs current version → restart bool
//   - readPIDFileVersion: extracts second line of a two-line pidfile
//   - queryDaemonVersion: pidfile fallback path (socket unreachable)
//   - parsePID: handles two-line pidfile format without errors
//
// The RPC primary path of queryDaemonVersion is exercised by the
// internal/serve package's own TestMethod_Version test.
//
// These tests do not fork processes, spawn daemons, or touch real sockets.
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ---- shouldRestartDaemon -------------------------------------------------

func TestShouldRestartDaemon_VersionMatch(t *testing.T) {
	t.Parallel()
	if shouldRestartDaemon("0.53.0.0 (go)", "0.53.0.0 (go)") {
		t.Error("shouldRestartDaemon: expected false for matching versions")
	}
}

func TestShouldRestartDaemon_VersionMismatch(t *testing.T) {
	t.Parallel()
	if !shouldRestartDaemon("0.50.0.0 (go)", "0.53.0.0 (go)") {
		t.Error("shouldRestartDaemon: expected true when running version differs from current")
	}
}

func TestShouldRestartDaemon_EmptyRunningVersion(t *testing.T) {
	t.Parallel()
	// Empty running version = unknown (pre-T2 daemon) → must restart.
	if !shouldRestartDaemon("", "0.53.0.0 (go)") {
		t.Error("shouldRestartDaemon: expected true when running version is empty (unknown)")
	}
}

func TestShouldRestartDaemon_BothEmpty(t *testing.T) {
	t.Parallel()
	// Both empty: dev build with no version info; treat as mismatch → restart.
	if !shouldRestartDaemon("", "") {
		t.Error("shouldRestartDaemon: expected true when both versions are empty")
	}
}

func TestShouldRestartDaemon_SameEmptyVersions(t *testing.T) {
	t.Parallel()
	// "" vs "": restart because empty running = unknown.
	got := shouldRestartDaemon("", "")
	if !got {
		t.Error("shouldRestartDaemon('', ''): expected true (empty running = unknown)")
	}
}

// ---- readPIDFileVersion ---------------------------------------------------

func TestReadPIDFileVersion_TwoLineFormat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")
	if err := os.WriteFile(path, []byte("12345\n0.53.0.0 (go)\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readPIDFileVersion(path)
	if got != "0.53.0.0 (go)" {
		t.Errorf("readPIDFileVersion: got %q; want %q", got, "0.53.0.0 (go)")
	}
}

func TestReadPIDFileVersion_OneLineLegacyFormat(t *testing.T) {
	t.Parallel()
	// Pre-T2 pidfile: only a PID, no version line.
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")
	if err := os.WriteFile(path, []byte("12345\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readPIDFileVersion(path)
	// Single line → version is empty string.
	if got != "" {
		t.Errorf("readPIDFileVersion one-line: got %q; want empty string", got)
	}
}

func TestReadPIDFileVersion_AbsentFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got := readPIDFileVersion(filepath.Join(dir, "nonexistent.pid"))
	if got != "" {
		t.Errorf("readPIDFileVersion missing file: got %q; want empty string", got)
	}
}

func TestReadPIDFileVersion_EmptyVersionLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")
	// Two lines but second is blank (edge case: writePIDFile on dev build with no version).
	if err := os.WriteFile(path, []byte("12345\n\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readPIDFileVersion(path)
	if got != "" {
		t.Errorf("readPIDFileVersion empty version line: got %q; want empty string", got)
	}
}

// TestQueryDaemonVersion_PidfileFallback verifies that queryDaemonVersion falls
// back to the pidfile's second line when the socket is not reachable.
func TestQueryDaemonVersion_PidfileFallback(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pidPath := filepath.Join(dir, "daemon.pid")
	sockPath := filepath.Join(dir, "daemon.sock") // non-existent socket

	const wantVer = "0.50.0.0 (go)"
	if err := os.WriteFile(pidPath, []byte("99999\n"+wantVer+"\n"), 0600); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}

	got := queryDaemonVersion(sockPath, pidPath)
	if got != wantVer {
		t.Errorf("queryDaemonVersion fallback: got %q; want %q", got, wantVer)
	}
}

// TestQueryDaemonVersion_BothUnreachable verifies that queryDaemonVersion
// returns "" when neither RPC nor pidfile is available.
func TestQueryDaemonVersion_BothUnreachable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got := queryDaemonVersion(
		filepath.Join(dir, "nonexistent.sock"),
		filepath.Join(dir, "nonexistent.pid"),
	)
	if got != "" {
		t.Errorf("queryDaemonVersion both missing: got %q; want empty string", got)
	}
}

// ---- parsePID multi-line format ------------------------------------------

// TestParsePID_TwoLineFormat verifies that parsePID correctly extracts the PID
// from the new two-line pidfile format without returning an error.
func TestParsePID_TwoLineFormat(t *testing.T) {
	t.Parallel()
	data := []byte("12345\n0.53.0.0 (go)\n")
	pid, err := parsePID(data)
	if err != nil {
		t.Fatalf("parsePID two-line: unexpected error: %v", err)
	}
	if pid != 12345 {
		t.Errorf("parsePID two-line: got %d; want 12345", pid)
	}
}

// TestParsePID_LegacyOneLineFormat verifies backward compatibility: a pidfile
// with only a PID (no version line) still parses correctly.
func TestParsePID_LegacyOneLineFormat(t *testing.T) {
	t.Parallel()
	data := []byte("99999\n")
	pid, err := parsePID(data)
	if err != nil {
		t.Fatalf("parsePID legacy: unexpected error: %v", err)
	}
	if pid != 99999 {
		t.Errorf("parsePID legacy: got %d; want 99999", pid)
	}
}
