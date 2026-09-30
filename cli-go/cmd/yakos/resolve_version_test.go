package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bakw00ds/yakos/internal/buildinfo"
	"github.com/bakw00ds/yakos/internal/version"
)

// TestResolveCurrentVersion covers the empty `current:` line bug: release
// builds inject buildinfo.Version, not version.Version.
func TestResolveCurrentVersion(t *testing.T) {
	origB, origV := buildinfo.Version, version.Version
	defer func() { buildinfo.Version, version.Version = origB, origV }()

	root := t.TempDir()

	buildinfo.Version, version.Version = "0.60.1.0", ""
	if got := resolveCurrentVersion(root); got != "0.60.1.0" {
		t.Errorf("buildinfo path: got %q", got)
	}

	buildinfo.Version, version.Version = "", ""
	if got := resolveCurrentVersion(root); got != "unknown" {
		t.Errorf("no source: got %q, want unknown", got)
	}

	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("9.9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveCurrentVersion(root); got != "9.9.9.9" {
		t.Errorf("file path: got %q", got)
	}
}
