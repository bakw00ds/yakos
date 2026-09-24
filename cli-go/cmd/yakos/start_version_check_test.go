// start_version_check_test.go — unit tests for daemon build-id-mismatch
// detection.
//
// These are pure-function tests for the decision logic extracted as helpers:
//
//   - shouldRestartDaemon: given running vs current build id → restart bool
//   - readPIDFileBuildID: extracts second line of a two-line pidfile
//   - queryDaemonBuildID: pidfile fallback path (socket unreachable)
//   - parsePID: handles two-line pidfile format without errors
//
// The RPC primary path of queryDaemonBuildID is exercised by the
// internal/serve package's own TestMethod_Version_BuildIdentity test.
//
// Renamed from *Version to *BuildID (S-6 daemon handshake,
// work/current/reports/s6-structural-plan-2026-09-23.md §4.3): the pidfile's
// second line and the yakos.version RPC's comparison key both moved from
// internal/version.Read's display string to buildinfo.BuildID(), which
// distinguishes two binaries built from different commits at the same
// VERSION file — the exact case that let a stale daemon survive a dev
// rebuild. The comparison logic these tests exercise (string equality,
// empty → restart) is unchanged; only the shape of the compared value is.
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
	if shouldRestartDaemon("0.53.0.0+abc123+deadbeef0000", "0.53.0.0+abc123+deadbeef0000") {
		t.Error("shouldRestartDaemon: expected false for matching build ids")
	}
}

func TestShouldRestartDaemon_VersionMismatch(t *testing.T) {
	t.Parallel()
	if !shouldRestartDaemon("0.50.0.0+aaaaaaaaaaaa+deadbeef0000", "0.53.0.0+bbbbbbbbbbbb+deadbeef0000") {
		t.Error("shouldRestartDaemon: expected true when running build id differs from current")
	}
}

// TestShouldRestartDaemon_SameVersionDifferentCommit is the v0.54 regression
// this rename exists to fix: two binaries built from different commits at
// the same VERSION file must now compare unequal (they didn't when the
// comparison key was internal/version.Read's display string).
func TestShouldRestartDaemon_SameVersionDifferentCommit(t *testing.T) {
	t.Parallel()
	if !shouldRestartDaemon("0.53.0.0+aaaaaaaaaaaa+deadbeef0000", "0.53.0.0+bbbbbbbbbbbb+deadbeef0000") {
		t.Error("shouldRestartDaemon: expected true when only the commit component differs (same VERSION)")
	}
}

func TestShouldRestartDaemon_EmptyRunningVersion(t *testing.T) {
	t.Parallel()
	// Empty running build id = unknown (pre-handshake daemon) → must restart.
	if !shouldRestartDaemon("", "0.53.0.0+abc123+deadbeef0000") {
		t.Error("shouldRestartDaemon: expected true when running build id is empty (unknown)")
	}
}

func TestShouldRestartDaemon_BothEmpty(t *testing.T) {
	t.Parallel()
	// Both empty: dev build with no build-id info; treat as mismatch → restart.
	if !shouldRestartDaemon("", "") {
		t.Error("shouldRestartDaemon: expected true when both build ids are empty")
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

// ---- readPIDFileBuildID ---------------------------------------------------

func TestReadPIDFileBuildID_TwoLineFormat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")
	if err := os.WriteFile(path, []byte("12345\n0.53.0.0+abc123+deadbeef0000\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readPIDFileBuildID(path)
	if got != "0.53.0.0+abc123+deadbeef0000" {
		t.Errorf("readPIDFileBuildID: got %q; want %q", got, "0.53.0.0+abc123+deadbeef0000")
	}
}

func TestReadPIDFileBuildID_OneLineLegacyFormat(t *testing.T) {
	t.Parallel()
	// Pre-handshake pidfile: only a PID, no build-id line.
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")
	if err := os.WriteFile(path, []byte("12345\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readPIDFileBuildID(path)
	// Single line → build id is empty string.
	if got != "" {
		t.Errorf("readPIDFileBuildID one-line: got %q; want empty string", got)
	}
}

func TestReadPIDFileBuildID_AbsentFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got := readPIDFileBuildID(filepath.Join(dir, "nonexistent.pid"))
	if got != "" {
		t.Errorf("readPIDFileBuildID missing file: got %q; want empty string", got)
	}
}

func TestReadPIDFileBuildID_EmptyVersionLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")
	// Two lines but second is blank (edge case: writePIDFile on dev build with no build-id).
	if err := os.WriteFile(path, []byte("12345\n\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readPIDFileBuildID(path)
	if got != "" {
		t.Errorf("readPIDFileBuildID empty build-id line: got %q; want empty string", got)
	}
}

// TestQueryDaemonBuildID_PidfileFallback verifies that queryDaemonBuildID
// falls back to the pidfile's second line when the socket is not reachable.
func TestQueryDaemonBuildID_PidfileFallback(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pidPath := filepath.Join(dir, "daemon.pid")
	sockPath := filepath.Join(dir, "daemon.sock") // non-existent socket

	const wantBuildID = "0.50.0.0+aaaaaaaaaaaa+deadbeef0000"
	if err := os.WriteFile(pidPath, []byte("99999\n"+wantBuildID+"\n"), 0600); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}

	got := queryDaemonBuildID(sockPath, pidPath)
	if got != wantBuildID {
		t.Errorf("queryDaemonBuildID fallback: got %q; want %q", got, wantBuildID)
	}
}

// TestQueryDaemonBuildID_BothUnreachable verifies that queryDaemonBuildID
// returns "" when neither RPC nor pidfile is available.
func TestQueryDaemonBuildID_BothUnreachable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got := queryDaemonBuildID(
		filepath.Join(dir, "nonexistent.sock"),
		filepath.Join(dir, "nonexistent.pid"),
	)
	if got != "" {
		t.Errorf("queryDaemonBuildID both missing: got %q; want empty string", got)
	}
}

// ---- parsePID multi-line format ------------------------------------------

// TestParsePID_TwoLineFormat verifies that parsePID correctly extracts the PID
// from the two-line pidfile format without returning an error.
func TestParsePID_TwoLineFormat(t *testing.T) {
	t.Parallel()
	data := []byte("12345\n0.53.0.0+abc123+deadbeef0000\n")
	pid, err := parsePID(data)
	if err != nil {
		t.Fatalf("parsePID two-line: unexpected error: %v", err)
	}
	if pid != 12345 {
		t.Errorf("parsePID two-line: got %d; want 12345", pid)
	}
}

// TestParsePID_LegacyOneLineFormat verifies backward compatibility: a pidfile
// with only a PID (no build-id line) still parses correctly.
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
