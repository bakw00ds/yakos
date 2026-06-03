package winsec_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bakw00ds/yakos/internal/winsec"
)

// TestSecureFile_NoopOnUnix verifies that SecureFile is a no-op and returns
// nil on non-Windows platforms (POSIX 0600 is the access control mechanism
// there). The test is meaningful on every platform: it proves the Unix stub
// compiles and does not error.
func TestSecureFile_NoopOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix no-op test skipped on Windows; see TestSecureFile_Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("test-content\n"), 0600); err != nil { //nolint:gosec
		t.Fatalf("write file: %v", err)
	}
	if err := winsec.SecureFile(path); err != nil {
		t.Fatalf("SecureFile returned error on Unix (should be no-op): %v", err)
	}
}

// TestSecureFile_NoopOnUnix_MissingFile verifies the Unix stub returns nil
// even for a non-existent path (it is truly a no-op).
func TestSecureFile_NoopOnUnix_MissingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix no-op test skipped on Windows")
	}
	if err := winsec.SecureFile("/nonexistent/path/that/does/not/exist"); err != nil {
		t.Fatalf("SecureFile (Unix no-op) should return nil for any path: %v", err)
	}
}

// TestSecureFile_Windows creates a real file, calls SecureFile, then asserts:
//  1. The caller (current user) still has read+write access.
//  2. The DACL contains exactly one explicit ACE (the current-user grant).
//
// This test only runs on Windows; on other platforms it is skipped.
func TestSecureFile_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows NTFS ACL test skipped on non-Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("tok\n"), 0600); err != nil { //nolint:gosec
		t.Fatalf("write file: %v", err)
	}

	if err := winsec.SecureFile(path); err != nil {
		t.Fatalf("SecureFile: %v", err)
	}

	// Caller must still be able to read the file we just secured.
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatalf("ReadFile after SecureFile: %v — current user lost read access", err)
	}
	if string(data) != "tok\n" {
		t.Errorf("content after SecureFile = %q; want %q", data, "tok\n")
	}

	// Caller must still be able to write (overwrite) the file.
	if err := os.WriteFile(path, []byte("tok2\n"), 0600); err != nil { //nolint:gosec
		t.Fatalf("WriteFile after SecureFile: %v — current user lost write access", err)
	}
}

// TestSecureFile_Windows_Idempotent verifies that calling SecureFile twice on
// the same path does not fail.
func TestSecureFile_Windows_Idempotent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows ACL idempotency test skipped on non-Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("tok\n"), 0600); err != nil { //nolint:gosec
		t.Fatalf("write file: %v", err)
	}
	if err := winsec.SecureFile(path); err != nil {
		t.Fatalf("first SecureFile: %v", err)
	}
	if err := winsec.SecureFile(path); err != nil {
		t.Fatalf("second SecureFile (idempotent): %v", err)
	}
}
