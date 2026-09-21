package dispatch

// events_test.go — M5 regression: the dispatch-log file and its directory
// must be created with restrictive permissions (0600/0700), not
// world-readable ones (0644/0755). The log holds TaskPreview (the first
// 200 bytes of every dispatched task) plus operator/conversation/session
// IDs. See security-review-2026-09-14.md M5.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAppendEvent_FileModeIs0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode bits don't apply on Windows")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "sub", "dispatch-log.ndjson")

	if err := appendEvent(logPath, []byte(`{"type":"dispatch_started"}`)); err != nil {
		t.Fatalf("appendEvent: %v", err)
	}

	fi, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0600 {
		t.Errorf("dispatch-log file mode = %#o; want 0600", got)
	}
}

func TestAppendEvent_DirModeIs0700(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode bits don't apply on Windows")
	}
	dir := t.TempDir()
	logDir := filepath.Join(dir, "sub")
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	if err := appendEvent(logPath, []byte(`{"type":"dispatch_started"}`)); err != nil {
		t.Fatalf("appendEvent: %v", err)
	}

	fi, err := os.Stat(logDir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0700 {
		t.Errorf("dispatch-log directory mode = %#o; want 0700", got)
	}
}

// TestAppendEvent_RefusesSymlinkedLogPath is the R4 regression: appendEvent
// must not follow a symlink planted at the dispatch-log path — that would
// let a local attacker redirect the daemon's append-only write to an
// arbitrary target file.
func TestAppendEvent_RefusesSymlinkedLogPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics + O_NOFOLLOW are POSIX-specific; see openflags_windows.go")
	}
	dir := t.TempDir()
	logDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(dir, "attacker-target")
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")
	if err := os.Symlink(target, logPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := appendEvent(logPath, []byte(`{"type":"dispatch_started"}`))
	if err == nil {
		t.Fatal("appendEvent through a symlinked log path: want error, got nil (R4 regression: symlink was followed)")
	}
	if _, statErr := os.Stat(target); statErr == nil {
		t.Error("attacker-target was created — appendEvent followed the symlink (R4 regression)")
	}
}
