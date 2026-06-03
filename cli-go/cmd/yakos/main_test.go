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
// team (rank 9), archive (rank 10), init (rank 11), install (rank 12),
// uninstall (rank 13), start (rank 14), update (rank 15), quickstart (rank 16),
// auth (rank 17), memory (rank 18), agent (rank 19), session (rank 20),
// migrate (rank 21), plugin (rank 22), teach (rank 23), soul (rank 24),
// retro (rank 25), skill (rank 26), compact (rank 27), checkpoint (rank 28),
// env (rank 29), standards (rank 30), peer (rank 31),
// mcp (rank 32), completion (rank 33), git-hooks (rank 38),
// supervise (rank 34), plan score (rank 35), work close (rank 37),
// model-routing (rank 36).
func TestPortedCommandsCount(t *testing.T) {
	const want = 37
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

// TestUninstallCommandEntry asserts that "uninstall" is in the ported list.
func TestUninstallCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "uninstall" {
			return
		}
	}
	t.Error("expected 'uninstall' in portedCommands; not found")
}

// TestStartCommandEntry asserts that "start" is in the ported list.
func TestStartCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "start" {
			return
		}
	}
	t.Error("expected 'start' in portedCommands; not found")
}

// TestUpdateCommandEntry asserts that "update" is in the ported list.
func TestUpdateCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "update" {
			return
		}
	}
	t.Error("expected 'update' in portedCommands; not found")
}

// TestQuickstartCommandEntry asserts that "quickstart" is in the ported list.
func TestQuickstartCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "quickstart" {
			return
		}
	}
	t.Error("expected 'quickstart' in portedCommands; not found")
}

// TestMemoryCommandEntry asserts that "memory" is in the ported list.
func TestMemoryCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "memory" {
			return
		}
	}
	t.Error("expected 'memory' in portedCommands; not found")
}

// TestAgentCommandEntry asserts that "agent" is in the ported list.
func TestAgentCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "agent" {
			return
		}
	}
	t.Error("expected 'agent' in portedCommands; not found")
}

// TestSessionCommandEntry asserts that "session" is in the ported list.
func TestSessionCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "session" {
			return
		}
	}
	t.Error("expected 'session' in portedCommands; not found")
}

// TestMigrateCommandEntry asserts that "migrate" is in the ported list.
func TestMigrateCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "migrate" {
			return
		}
	}
	t.Error("expected 'migrate' in portedCommands; not found")
}

// TestPluginCommandEntry asserts that "plugin" is in the ported list.
func TestPluginCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "plugin" {
			return
		}
	}
	t.Error("expected 'plugin' in portedCommands; not found")
}

// TestTeachCommandEntry asserts that "teach" is in the ported list.
func TestTeachCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "teach" {
			return
		}
	}
	t.Error("expected 'teach' in portedCommands; not found")
}

// TestSoulCommandEntry asserts that "soul" is in the ported list.
func TestSoulCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "soul" {
			return
		}
	}
	t.Error("expected 'soul' in portedCommands; not found")
}

// TestRetroCommandEntry asserts that "retro" is in the ported list.
func TestRetroCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "retro" {
			return
		}
	}
	t.Error("expected 'retro' in portedCommands; not found")
}

// TestSkillCommandEntry asserts that "skill" is in the ported list.
func TestSkillCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "skill" {
			return
		}
	}
	t.Error("expected 'skill' in portedCommands; not found")
}

// TestCompactCommandEntry asserts that "compact" is in the ported list.
func TestCompactCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "compact" {
			return
		}
	}
	t.Error("expected 'compact' in portedCommands; not found")
}

// TestCheckpointCommandEntry asserts that "checkpoint" is in the ported list.
func TestCheckpointCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "checkpoint" {
			return
		}
	}
	t.Error("expected 'checkpoint' in portedCommands; not found")
}

// TestSupervisesCommandEntry asserts that "supervise" is in the ported list.
func TestSupervisesCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "supervise" {
			return
		}
	}
	t.Error("expected 'supervise' in portedCommands; not found")
}

// TestPlanScoreCommandEntry asserts that "plan score" is in the ported list.
func TestPlanScoreCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "plan score" {
			return
		}
	}
	t.Error("expected 'plan score' in portedCommands; not found")
}

// TestWorkCloseCommandEntry asserts that "work close" is in the ported list.
func TestWorkCloseCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "work close" {
			return
		}
	}
	t.Error("expected 'work close' in portedCommands; not found")
}

// TestModelRoutingCommandEntry asserts that "model-routing" is in the ported list.
func TestModelRoutingCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "model-routing" {
			return
		}
	}
	t.Error("expected 'model-routing' in portedCommands; not found")
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
