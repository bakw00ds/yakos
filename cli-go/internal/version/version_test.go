package version_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/version"
)

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
