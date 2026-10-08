//go:build unix

package supervisorstream_test

// feedreport_fifo_unix_test.go: K-171. The findings write opens its file after an
// Lstat; in in-place work mode the model can write to the directory, so an entry
// it swaps for a FIFO between the two used to block the open for good (O_WRONLY
// on a FIFO waits for a reader). The open is O_NONBLOCK now, so it fails at once,
// and the opened descriptor is checked to be a regular file. The pending file's
// read-back is bounded the same way, by a documented cap.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
)

// reportWithin runs ReportFeedFinding and fails the test if it has not returned in
// d. A stuck call is released by opening the FIFO for reading, then the test fails.
func reportWithin(t *testing.T, d time.Duration, wc string, release string) error {
	t.Helper()
	f := supervisorstream.FeedFinding{Runtime: "codex", Kind: "text", Severity: "warn", Labels: []string{"x"}, Session: "s1"}
	done := make(chan error, 1)
	go func() { done <- supervisorstream.ReportFeedFinding(wc, f, time.Now()) }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		if release != "" {
			if r, err := os.OpenFile(release, os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
				defer func() { _ = r.Close() }()
				<-done
			}
		}
		t.Fatalf("ReportFeedFinding still blocked after %v: a swapped-in FIFO hung the open", d)
		return nil
	}
}

// A FIFO swapped in for the findings file after the Lstat saw nothing there.
func TestReportFeedFinding_FifoSwappedInAfterCheckDoesNotHang(t *testing.T) {
	wc := t.TempDir()
	fifo := filepath.Join(wc, "supervisor-findings.ndjson")
	restore := supervisorstream.SetBeforeOpenHookForTest(func(path string) {
		if path == fifo {
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Errorf("mkfifo: %v", err)
			}
		}
	})
	defer restore()
	err := reportWithin(t, 5*time.Second, wc, fifo)
	if err == nil {
		t.Fatal("a FIFO in the findings path was accepted")
	}
	if fi, lerr := os.Lstat(fifo); lerr != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Errorf("the FIFO was replaced or removed: %v", lerr)
	}
}

// A regular pending file swapped for a FIFO after the Lstat saw the regular one.
func TestReportFeedFinding_PendingSwappedForFifoDoesNotHang(t *testing.T) {
	wc := t.TempDir()
	pend := filepath.Join(wc, ".supervisor-pending.s1")
	if err := os.WriteFile(pend, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := supervisorstream.SetBeforeOpenHookForTest(func(path string) {
		if path == pend {
			if err := os.Remove(path); err != nil {
				t.Errorf("remove: %v", err)
			}
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Errorf("mkfifo: %v", err)
			}
		}
	})
	defer restore()
	if err := reportWithin(t, 5*time.Second, wc, pend); err == nil {
		t.Fatal("a FIFO swapped in for the pending file was accepted")
	}
}

// A static FIFO is still refused at once.
func TestReportFeedFinding_StaticFifoRefused(t *testing.T) {
	wc := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(wc, ".supervisor-pending.s1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reportWithin(t, 5*time.Second, wc, ""); !errors.Is(err, os.ErrInvalid) {
		t.Errorf("want ErrInvalid, got %v", err)
	}
}

// The pending file is read back through a cap. 120 lines of 20 KiB are under the
// 150-line trim threshold but over the cap, so only the cap can shrink them.
func TestAppendPending_ReadIsCappedAndKeepsNewest(t *testing.T) {
	wc := t.TempDir()
	pend := filepath.Join(wc, ".supervisor-pending.s1")
	var b strings.Builder
	for i := 0; i < 120; i++ {
		b.WriteString(`{"pad":"`)
		b.WriteString(strings.Repeat("a", 20<<10))
		b.WriteString("\"}\n")
	}
	if err := os.WriteFile(pend, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	f := supervisorstream.FeedFinding{Runtime: "codex", Kind: "text", Severity: "warn", Labels: []string{"x"}, Session: "s1"}
	if err := supervisorstream.ReportFeedFinding(wc, f, time.Now()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(pend)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > (1<<20)+4096 {
		t.Errorf("pending file kept %d bytes; the cap is 1 MiB", len(raw))
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > 100 {
		t.Errorf("kept %d lines, want at most 100", len(lines))
	}
	if !strings.Contains(lines[len(lines)-1], "event-scan:text") {
		t.Errorf("the newest record was not kept: %.80s", lines[len(lines)-1])
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "{") || !strings.HasSuffix(l, "}") {
			t.Fatalf("a partial line survived the cut: %.40s...%.20s", l, l[max(0, len(l)-20):])
		}
	}
	if fi, _ := os.Lstat(pend); fi == nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
		t.Errorf("pending file is not a 0600 regular file: %v", fi)
	}
}

// One giant line with no newline does not stay on disk, and the call still succeeds.
func TestAppendPending_GiantLineIsCut(t *testing.T) {
	wc := t.TempDir()
	pend := filepath.Join(wc, ".supervisor-pending.s1")
	if err := os.WriteFile(pend, []byte(strings.Repeat("z", 3<<20)), 0o600); err != nil {
		t.Fatal(err)
	}
	f := supervisorstream.FeedFinding{Runtime: "codex", Kind: "text", Severity: "warn", Labels: []string{"x"}, Session: "s1"}
	if err := supervisorstream.ReportFeedFinding(wc, f, time.Now()); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(pend); err != nil || fi.Size() > 4096 {
		t.Errorf("giant line kept: %v %v", fi, err)
	}
}

// The write open only gets as far as the regular-file check when a reader is on
// the other end of the FIFO: with none, O_WRONLY|O_NONBLOCK fails with ENXIO
// first. So attach a non-blocking reader to the swapped-in FIFO, which lets the
// open succeed, and require the call to refuse it and write nothing.
func TestReportFeedFinding_FifoWithAReaderIsRefusedByTheRegularFileCheck(t *testing.T) {
	wc := t.TempDir()
	fifo := filepath.Join(wc, "supervisor-findings.ndjson")
	var reader *os.File
	restore := supervisorstream.SetBeforeOpenHookForTest(func(path string) {
		if path != fifo {
			return
		}
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Errorf("mkfifo: %v", err)
			return
		}
		r, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Errorf("reader open: %v", err)
			return
		}
		reader = r
	})
	defer restore()
	err := reportWithin(t, 5*time.Second, wc, fifo)
	if reader == nil {
		t.Fatal("the reader was not attached")
	}
	defer func() { _ = reader.Close() }()
	if !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid from the regular-file check", err)
	}
	buf := make([]byte, 4096)
	if n, _ := reader.Read(buf); n != 0 {
		t.Errorf("%d bytes were written to the FIFO: %q", n, buf[:n])
	}
}

// A findings file that is also named somewhere else is not appended to: the write
// would reach the other name's file too.
func TestReportFeedFinding_HardLinkedFileIsRefused(t *testing.T) {
	wc, other := t.TempDir(), t.TempDir()
	victim := filepath.Join(other, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"supervisor-findings.ndjson", ".supervisor-pending.s1"} {
		link := filepath.Join(wc, name)
		if err := os.Link(victim, link); err != nil {
			t.Skipf("cannot create a hard link here: %v", err)
		}
		if err := reportWithin(t, 5*time.Second, wc, ""); !errors.Is(err, os.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
		if b, _ := os.ReadFile(victim); string(b) != "keep\n" {
			t.Errorf("%s: the other name's file was written: %q", name, b)
		}
		if fi, _ := os.Stat(victim); fi.Mode().Perm() != 0o644 {
			t.Errorf("%s: the other name's file was chmodded to %v", name, fi.Mode().Perm())
		}
		_ = os.Remove(link)
	}
}
