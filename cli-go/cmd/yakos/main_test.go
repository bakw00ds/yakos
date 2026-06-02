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

// TestPortedCommandsCount confirms that the ported command count matches the
// expected number.  Update this test each time a new subcommand is ported.
//
// Current ported commands: validate (rank 2), cost (rank 3), status (rank 4),
// doctor (rank 5), refresh (rank 6), kanban (rank 7), dispatch (rank 8),
// team (rank 9), archive (rank 10), init (rank 11), install (rank 12).
func TestPortedCommandsCount(t *testing.T) {
	const want = 11
	if len(portedCommands) != want {
		t.Errorf(
			"expected %d ported command(s); got %d — "+
				"update portedCommands in main.go and this test count together",
			want, len(portedCommands),
		)
	}
}

// TestPortedCommandsNames verifies each ported command entry is well-formed.
func TestPortedCommandsNames(t *testing.T) {
	for i, cmd := range portedCommands {
		if cmd.Name == "" {
			t.Errorf("portedCommands[%d].Name is empty", i)
		}
		if cmd.Since == "" {
			t.Errorf("portedCommands[%d].Since is empty", i)
		}
	}
}

// TestValidateCommandEntry asserts that "validate" is in the ported list.
func TestValidateCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "validate" {
			return
		}
	}
	t.Error("expected 'validate' in portedCommands; not found")
}

// TestCostCommandEntry asserts that "cost" is in the ported list.
func TestCostCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "cost" {
			return
		}
	}
	t.Error("expected 'cost' in portedCommands; not found")
}

// TestStatusCommandEntry asserts that "status" is in the ported list.
func TestStatusCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "status" {
			return
		}
	}
	t.Error("expected 'status' in portedCommands; not found")
}

// TestDoctorCommandEntry asserts that "doctor" is in the ported list.
func TestDoctorCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "doctor" {
			return
		}
	}
	t.Error("expected 'doctor' in portedCommands; not found")
}

// TestRefreshCommandEntry asserts that "refresh" is in the ported list.
func TestRefreshCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "refresh" {
			return
		}
	}
	t.Error("expected 'refresh' in portedCommands; not found")
}

// TestKanbanCommandEntry asserts that "kanban" is in the ported list.
func TestKanbanCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "kanban" {
			return
		}
	}
	t.Error("expected 'kanban' in portedCommands; not found")
}

// TestDispatchCommandEntry asserts that "dispatch" is in the ported list.
func TestDispatchCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "dispatch" {
			return
		}
	}
	t.Error("expected 'dispatch' in portedCommands; not found")
}

// TestTeamCommandEntry asserts that "team" is in the ported list.
func TestTeamCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "team" {
			return
		}
	}
	t.Error("expected 'team' in portedCommands; not found")
}

// TestArchiveCommandEntry asserts that "archive" is in the ported list.
func TestArchiveCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "archive" {
			return
		}
	}
	t.Error("expected 'archive' in portedCommands; not found")
}

// TestInitCommandEntry asserts that "init" is in the ported list.
func TestInitCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "init" {
			return
		}
	}
	t.Error("expected 'init' in portedCommands; not found")
}

// TestInstallCommandEntry asserts that "install" is in the ported list.
func TestInstallCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "install" {
			return
		}
	}
	t.Error("expected 'install' in portedCommands; not found")
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
