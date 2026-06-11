package version_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/version"
)

// resetVersion restores the package-level Version var to its original value
// after a test that mutates it.  The var is exported specifically so ldflags
// can inject it; tests use the same mechanism to simulate a release build.
func resetVersion(t *testing.T, orig string) {
	t.Helper()
	t.Cleanup(func() { version.Version = orig })
}

func TestRead_ReturnsVersionWithGoSuffix(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1.2.3\n"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := version.Read(dir)
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if !strings.HasSuffix(got, "(go)") {
		t.Errorf("expected suffix (go); got %q", got)
	}
	if !strings.HasPrefix(got, "1.2.3") {
		t.Errorf("expected prefix 1.2.3; got %q", got)
	}
}

func TestRead_StripsTrailingWhitespace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// VERSION file with trailing newline and space — common in editors.
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("0.36.0.0\n"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := version.Read(dir)
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	want := "0.36.0.0 (go)"
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestRead_MissingVersionFile(t *testing.T) {
	t.Parallel()

	// With no VERSION var set and no file, Read must return an error.
	dir := t.TempDir()
	_, err := version.Read(dir)
	if err == nil {
		t.Fatal("expected error for missing VERSION file; got nil")
	}
}

func TestRead_EmptyVersionFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("   \n"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := version.Read(dir)
	if err == nil {
		t.Fatal("expected error for empty VERSION file content; got nil")
	}
}

func TestGoVersion_NonEmpty(t *testing.T) {
	t.Parallel()

	v := version.GoVersion()
	if v == "" {
		t.Error("GoVersion returned empty string")
	}
	if !strings.HasPrefix(v, "go") {
		t.Errorf("expected GoVersion to start with 'go'; got %q", v)
	}
}

// TestRead_CompiledInVersionPreferredOverFile verifies that when the ldflags
// Version variable is set, Read returns that value without reading the VERSION
// file.  This is the Go-only install path.
func TestRead_CompiledInVersionPreferredOverFile(t *testing.T) {
	// Not parallel: mutates package-level var.
	orig := version.Version
	resetVersion(t, orig)

	version.Version = "1.99.0-test"

	// Supply a different version in the file to confirm the var wins.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("0.0.1\n"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := version.Read(dir)
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	want := "1.99.0-test (go)"
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

// TestRead_CompiledInVersionNoFile verifies that when the ldflags Version
// variable is set and NO VERSION file exists, Read succeeds.  This is the
// critical Go-only install scenario: fresh machine, no bash tree.
func TestRead_CompiledInVersionNoFile(t *testing.T) {
	// Not parallel: mutates package-level var.
	orig := version.Version
	resetVersion(t, orig)

	version.Version = "0.38.0.1-goonly"

	dir := t.TempDir() // empty dir — no VERSION file

	got, err := version.Read(dir)
	if err != nil {
		t.Fatalf("Read returned error on Go-only install: %v", err)
	}
	want := "0.38.0.1-goonly (go)"
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

// TestRead_FallsBackToFileWhenVarEmpty confirms the dev-build fallback path:
// when Version is empty (no ldflags), Read reads the VERSION file as before.
func TestRead_FallsBackToFileWhenVarEmpty(t *testing.T) {
	// Not parallel: mutates package-level var.
	orig := version.Version
	resetVersion(t, orig)

	version.Version = "" // simulate dev build

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("0.38.0.0\n"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := version.Read(dir)
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	want := "0.38.0.0 (go)"
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}
