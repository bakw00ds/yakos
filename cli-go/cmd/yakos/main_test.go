package main

import (
	"os"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/version"
)

// repoRoot walks up from the current working directory to find the repo root
// (the directory containing a "VERSION" regular file).
//
// Note: the IsDir check is required because macOS case-insensitive filesystems
// match "VERSION" against a "version" subdirectory, which would produce a
// false positive when walking through cli-go/internal/.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	for {
		fi, err := os.Stat(dir + "/VERSION")
		if err == nil && !fi.IsDir() {
			return dir
		}
		parent := dir[:strings.LastIndex(dir, "/")]
		if parent == "" || parent == dir {
			t.Fatal("could not find repo root (no VERSION regular file in any parent dir)")
		}
		dir = parent
	}
}

// TestVersionRead verifies that the version package produces the expected
// "(go)" suffix when pointed at the actual repo root VERSION file.
// This is a smoke test for the version pipeline used by --version.
func TestVersionRead_RepoRoot(t *testing.T) {
	root := repoRoot(t)
	got, err := version.Read(root)
	if err != nil {
		t.Fatalf("version.Read: %v", err)
	}
	if !strings.HasSuffix(got, " (go)") {
		t.Errorf("version string should end with ' (go)'; got %q", got)
	}
	// The version from the file should not be empty before the suffix.
	bare := strings.TrimSuffix(got, " (go)")
	if bare == "" {
		t.Errorf("bare version (without go suffix) is empty in %q", got)
	}
}

// TestPortedCommandsEmpty confirms the bootstrap invariant: no subcommands
// are ported yet. This test fails intentionally when the first real
// subcommand is added so the developer is prompted to update both
// portedCommands AND this test.
func TestPortedCommandsEmpty(t *testing.T) {
	if len(portedCommands) != 0 {
		t.Errorf(
			"expected 0 ported commands in bootstrap phase; got %d — "+
				"update this test when the first subcommand is ported",
			len(portedCommands),
		)
	}
}

// TestPortedCommandStruct verifies the portedCommand struct has the expected
// fields — catches accidental field renames.
func TestPortedCommandStruct(t *testing.T) {
	pc := portedCommand{
		Name:  "example",
		Since: "1.0.0",
		Notes: "test",
	}
	if pc.Name != "example" {
		t.Errorf("Name field broken; got %q", pc.Name)
	}
	if pc.Since != "1.0.0" {
		t.Errorf("Since field broken; got %q", pc.Since)
	}
	if pc.Notes != "test" {
		t.Errorf("Notes field broken; got %q", pc.Notes)
	}
}
