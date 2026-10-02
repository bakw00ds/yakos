//go:build !windows

package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func fallbackStatePath(t *testing.T) (*runner, string) {
	t.Helper()
	r, _, path := fallbackRunner(t, "")
	return r, path
}

// finishes fails the test when f blocks: reading a FIFO or /dev/zero must not.
func finishes(t *testing.T, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("doctor blocked on a non-regular fallback log")
	}
}

func TestDoctorNeverTrimsThroughASymlink(t *testing.T) {
	r, path := fallbackStatePath(t)
	outside := filepath.Join(t.TempDir(), "unrelated")
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&sb, "%s supervisor-stream rc=%d\n", ago(time.Hour), i)
	}
	if err := os.WriteFile(outside, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	finishes(t, r.checkHookFallback)
	if b, _ := os.ReadFile(outside); string(b) != sb.String() {
		t.Fatal("doctor modified the symlink target")
	}
	if r.report.Warnings != 1 {
		t.Errorf("want a not-a-regular-file warning, got %+v", r.report)
	}
}

func TestDoctorSkipsFIFOAndDevZero(t *testing.T) {
	r, path := fallbackStatePath(t)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	finishes(t, r.checkHookFallback)
	if r.report.Warnings != 1 {
		t.Errorf("fifo: want one warning, got %+v", r.report)
	}
	r2, path2 := fallbackStatePath(t)
	if err := os.Symlink("/dev/zero", path2); err != nil {
		t.Fatal(err)
	}
	finishes(t, r2.checkHookFallback)
	if r2.report.Warnings != 1 {
		t.Errorf("/dev/zero: want one warning, got %+v", r2.report)
	}
}

// The trim replaces the file (temp + rename): the inode changes, so a hard link
// elsewhere keeps the untrimmed data, and no temp file is left behind.
func TestDoctorTrimReplacesTheFileAndCapsReads(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&sb, "%s supervisor-stream rc=%d\n", ago(time.Hour), i)
	}
	r, _, path := fallbackRunner(t, sb.String())
	other := filepath.Join(t.TempDir(), "hardlink")
	if err := os.Link(path, other); err != nil {
		t.Fatal(err)
	}
	r.checkHookFallback()
	if b, _ := os.ReadFile(other); string(b) != sb.String() {
		t.Error("trim wrote in place instead of replacing the file")
	}
	if b, _ := os.ReadFile(path); strings.Count(string(b), "\n") != 200 {
		t.Errorf("want 200 lines after the trim, got %d", strings.Count(string(b), "\n"))
	}
	ents, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".hook-fallback-") {
			t.Errorf("stray temp file %s", e.Name())
		}
	}

	// A file far past the read cap is read from its tail and trimmed.
	big := strings.Repeat("junk line that is not a record\n", 100000) // ~3 MB
	for i := 0; i < 5; i++ {
		big += fmt.Sprintf("%s supervisor-stream rc=%d\n", ago(time.Minute), i)
	}
	r2, buf2, path2 := fallbackRunner(t, big)
	r2.checkHookFallback()
	if !strings.Contains(buf2.String(), "failed 5 time(s)") {
		t.Errorf("tail of an oversized file not read:\n%s", buf2.String())
	}
	if fi, _ := os.Stat(path2); fi.Size() > 20000 {
		t.Errorf("oversized log not trimmed: %d bytes", fi.Size())
	}
}
