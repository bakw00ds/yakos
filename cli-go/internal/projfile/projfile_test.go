package projfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeProject(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// within fails the test if fn does not return by the deadline, so a regression
// that blocks or reads without end reports instead of hanging the run.
func within(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("read did not return within %s", d)
	}
}

func setHook(t *testing.T, h *func(string), fn func(string)) {
	t.Helper()
	old := *h
	*h = fn
	t.Cleanup(func() { *h = old })
}

func refusedWith(t *testing.T, err error, want string) {
	t.Helper()
	if !IsRefused(err) || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want a refusal containing %q", err, want)
	}
}

func TestReadRegularFile(t *testing.T) {
	data, err := Read(writeProject(t, "a: 1\n"))
	if err != nil || string(data) != "a: 1\n" {
		t.Fatalf("%q %v", data, err)
	}
}

func TestReadMissingIsNotExistAndNotRefused(t *testing.T) {
	_, err := Read(t.TempDir())
	if !errors.Is(err, os.ErrNotExist) || IsRefused(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadOversized(t *testing.T) {
	_, err := Read(writeProject(t, "#"+strings.Repeat("x", MaxBytes)))
	refusedWith(t, err, "larger than")
	// Exactly at the cap is still read.
	if _, err := Read(writeProject(t, "#"+strings.Repeat("x", MaxBytes-1))); err != nil {
		t.Fatalf("file at the cap refused: %v", err)
	}
}

// A file that is small when vetted and grows after the open is cut off at the
// cap, not read to the end.
func TestReadFileGrowingAfterOpenIsRefused(t *testing.T) {
	dir := writeProject(t, "a: 1\n")
	setHook(t, &afterOpen, func(path string) {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = f.Close() }()
		_, _ = f.Write([]byte(strings.Repeat("x", MaxBytes+10)))
	})
	_, err := Read(dir)
	refusedWith(t, err, "larger than")
}

type endless struct{ n int }

func (e *endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	e.n += len(p)
	return len(p), nil
}

// The read stops at the cap however much the source has: without the limit this
// never returns.
func TestReadBoundedStopsAtTheCap(t *testing.T) {
	src := &endless{}
	var err error
	within(t, 5*time.Second, func() { _, err = readBounded(src) })
	refusedWith(t, err, "larger than")
	if src.n > MaxBytes+1+64*1024 {
		t.Fatalf("read %d bytes past a %d byte cap", src.n, MaxBytes)
	}
}

func TestReadSymlinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	real := writeProject(t, "a: 1\n")
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(real, Name), filepath.Join(dir, Name)); err != nil {
		t.Fatal(err)
	}
	_, err := Read(dir)
	refusedWith(t, err, "symlink")
}

func TestReadDeviceSymlinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/zero")
	}
	dir := t.TempDir()
	if err := os.Symlink("/dev/zero", filepath.Join(dir, Name)); err != nil {
		t.Fatal(err)
	}
	var err error
	within(t, 5*time.Second, func() { _, err = Read(dir) })
	refusedWith(t, err, "symlink")
}

func TestReadDirectoryRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, Name), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := Read(dir)
	refusedWith(t, err, "not a regular file")
}

// L1: an I/O error names the cause, never the project path.
func TestErrorsCarryNoPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes differ on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root reads a mode 000 file")
	}
	dir := writeProject(t, "a: 1\n")
	if err := os.Chmod(filepath.Join(dir, Name), 0); err != nil {
		t.Fatal(err)
	}
	_, err := Read(dir)
	refusedWith(t, err, "permission denied")
	if strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), string(filepath.Separator)) {
		t.Fatalf("error carries a path: %v", err)
	}
	// The project path is a file: Lstat fails with ENOTDIR.
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Read(file)
	if err == nil || strings.Contains(err.Error(), file) || strings.Contains(err.Error(), string(filepath.Separator)) {
		t.Fatalf("lstat error carries a path: %v", err)
	}
}

func TestReadSwappedAfterLstat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs symlinks and Unix open flags")
	}
	dir := writeProject(t, "a: 1\n")
	target := writeProject(t, "a: 999\n")
	setHook(t, &afterLstat, func(path string) {
		_ = os.Remove(path)
		_ = os.Symlink(filepath.Join(target, Name), path)
	})
	if _, err := Read(dir); err == nil {
		t.Fatal("file swapped for a symlink after the Lstat was read")
	}
}

// A different regular file takes the place of the inspected one between the
// Lstat and the open: only the identity check can tell.
func TestReadReplacedAfterLstat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("rename over an open path differs on Windows")
	}
	dir := writeProject(t, "a: 1\n")
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, []byte("a: 999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setHook(t, &afterLstat, func(path string) { _ = os.Rename(other, path) })
	_, err := Read(dir)
	refusedWith(t, err, "changed")
}
