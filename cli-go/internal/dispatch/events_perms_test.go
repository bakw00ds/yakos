package dispatch

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// S-2 R12 / N5: appendEvent's MkdirAll(0700)/O_CREATE(0600) modes are no-ops
// on existing paths, so a pre-existing 0755 directory or a pre-created 0666
// log kept its permissive mode and stayed readable by other local users.

func TestAppendEvent_TightensExistingStateDirAndLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	dir := filepath.Join(t.TempDir(), ".yakos-state")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "dispatch-log.ndjson")
	if err := os.WriteFile(path, []byte("{\"type\":\"old\"}\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}

	if err := appendEvent(path, []byte(`{"type":"new"}`)); err != nil {
		t.Fatalf("appendEvent: %v", err)
	}

	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("state dir mode=%o; want 700", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("log mode=%o; want 600 (task previews + operator IDs were world-readable)", fi.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if string(data) != "{\"type\":\"old\"}\n{\"type\":\"new\"}\n" {
		t.Errorf("log content=%q; existing lines must be preserved and the new one appended", data)
	}
}

// An operator-chosen override directory (YAKOS_DISPATCH_LOG) is not ours to
// chmod, but the log file inside it is.
func TestAppendEvent_OverrideDirUntouchedFileTightened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	dir := filepath.Join(t.TempDir(), "shared-log-dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "dispatch-log.ndjson")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendEvent(path, []byte(`{}`)); err != nil {
		t.Fatalf("appendEvent: %v", err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o755 {
		t.Errorf("override dir mode=%o; want it left at 755", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("log mode=%o; want 600", fi.Mode().Perm())
	}
}

// A symlinked default-named state dir is refused, and nothing is written
// through it.
func TestAppendEvent_RefusesSymlinkedStateDir(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "victim")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, ".yakos-state")
	if err := os.Symlink(target, dir); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := appendEvent(filepath.Join(dir, "dispatch-log.ndjson"), []byte(`{}`)); err == nil {
		t.Error("appendEvent wrote through a symlinked state dir")
	}
	if _, err := os.Stat(filepath.Join(target, "dispatch-log.ndjson")); err == nil {
		t.Error("log created inside the symlink target")
	}
}

// appendEvent keys its "this is the yakOS state dir" decision on the base
// name; keep it in step with statepath's default.
func TestStateDirName_MatchesStatepath(t *testing.T) {
	t.Setenv("YAKOS_DISPATCH_LOG", "")
	t.Setenv("HOME", t.TempDir())
	if got := filepath.Base(statepath.Dir()); got != stateDirName {
		t.Fatalf("statepath default dir base=%q; dispatch.stateDirName=%q", got, stateDirName)
	}
}
