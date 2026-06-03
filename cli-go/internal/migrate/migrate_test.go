// migrate_test.go — unit tests for the migrate package.
//
// Scenarios verified:
//
//	(a) status — all known formats listed with version info
//	(b) status — absent sidecar shows legacy "(absent)" label
//	(c) status — present sidecar shows read version
//	(d) up — no-op when all formats up-to-date (empty registry)
//	(e) up — single format restriction accepted
//	(f) up — unknown format returns an error
//	(g) up — dry-run prints would-apply without writing
//	(h) up — real migration applied via registered step (simulated)
//	(i) up — migration sequence applied in order (simulated)
//	(j) up — sidecar written atomically (no .tmp left behind)
//	(k) down — always errors with Phase 1.5 deferral message
//	(l) unknown subcommand — returns descriptive error
//	(m) help text phrases — all load-bearing phrases present
//	(n) readSidecarVersion — missing file returns empty string not error
//	(o) readSidecarVersion — content trimmed correctly
//	(p) atomicWriteSidecar — creates intermediate directories
package migrate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

func newMigrateConfig(t *testing.T) Config {
	t.Helper()
	tmp := t.TempDir()
	return Config{
		WorkDir:   filepath.Join(tmp, "work"),
		MemoryDir: filepath.Join(tmp, "memory"),
		HomeDir:   tmp,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}
}

func runMigrateCmd(t *testing.T, cfg Config) (string, Result, error) {
	t.Helper()
	var out bytes.Buffer
	cfg.Writer = &out
	cfg.ErrWriter = &bytes.Buffer{}
	result, err := Run(cfg)
	return out.String(), result, err
}

// writeSidecar writes a sidecar file at path with the given version string.
func writeSidecar(t *testing.T, path, version string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("writeSidecar mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(version+"\n"), 0644); err != nil {
		t.Fatalf("writeSidecar write: %v", err)
	}
}

// kanbanSidecarPath returns the kanban sidecar path for a given work dir.
func kanbanSidecarPath(workDir string) string {
	return filepath.Join(workDir, "current", ".kanban.schema-version")
}

// withCleanRegistry runs fn with the global registry temporarily emptied and
// restores it after. This isolates tests that call RegisterMigration.
func withCleanRegistry(t *testing.T, fn func()) {
	t.Helper()
	saved := registry
	registry = nil
	defer func() {
		registry = saved
	}()
	fn()
}

// ---- (a) status — all formats listed ----------------------------------------

func TestMigrate_StatusListsAllFormats(t *testing.T) {
	cfg := newMigrateConfig(t)
	cfg.Subcommand = "status"

	out, _, err := runMigrateCmd(t, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, name := range []string{"kanban", "memory"} {
		if !strings.Contains(out, name) {
			t.Errorf("status output missing format %q;\nfull output:\n%s", name, out)
		}
	}
}

// ---- (b) status — absent sidecar shows legacy label -------------------------

func TestMigrate_StatusAbsentSidecarLabel(t *testing.T) {
	cfg := newMigrateConfig(t)
	cfg.Subcommand = "status"
	// Do not create the sidecar files.

	out, _, err := runMigrateCmd(t, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "absent") && !strings.Contains(out, "up-to-date") {
		t.Errorf("status should note absent sidecar as up-to-date or absent; got:\n%s", out)
	}
}

// ---- (c) status — present sidecar shows version ----------------------------

func TestMigrate_StatusPresentSidecarShowsVersion(t *testing.T) {
	cfg := newMigrateConfig(t)
	cfg.Subcommand = "status"

	// Write a kanban sidecar with version "1".
	writeSidecar(t, kanbanSidecarPath(cfg.WorkDir), "1")

	out, _, err := runMigrateCmd(t, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "1") {
		t.Errorf("status should show version '1' for kanban; got:\n%s", out)
	}
}

// ---- (d) up — no-op when all up-to-date ------------------------------------

func TestMigrate_UpNoPendingMigrations(t *testing.T) {
	withCleanRegistry(t, func() {
		cfg := newMigrateConfig(t)
		cfg.Subcommand = "up"

		out, result, err := runMigrateCmd(t, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Applied != 0 {
			t.Errorf("expected 0 applied; got %d", result.Applied)
		}
		if !strings.Contains(out, "up-to-date") || !strings.Contains(out, "nothing to do") {
			t.Errorf("up should report nothing-to-do; got:\n%s", out)
		}
	})
}

// ---- (e) up — single format restriction accepted ----------------------------

func TestMigrate_UpSingleFormatRestriction(t *testing.T) {
	withCleanRegistry(t, func() {
		cfg := newMigrateConfig(t)
		cfg.Subcommand = "up"
		cfg.Format = "kanban"

		out, result, err := runMigrateCmd(t, cfg)
		if err != nil {
			t.Fatalf("unexpected error for known format: %v", err)
		}
		if result.Applied != 0 {
			t.Errorf("expected 0 applied; got %d", result.Applied)
		}
		// Only kanban should appear; memory should be absent.
		if strings.Contains(out, "memory") {
			t.Errorf("single-format up should only mention kanban, not memory; got:\n%s", out)
		}
	})
}

// ---- (f) up — unknown format returns error ----------------------------------

func TestMigrate_UpUnknownFormatError(t *testing.T) {
	cfg := newMigrateConfig(t)
	cfg.Subcommand = "up"
	cfg.Format = "nonexistent-format"

	_, _, err := runMigrateCmd(t, cfg)
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
	if !strings.Contains(err.Error(), "unknown format") {
		t.Errorf("error should mention 'unknown format'; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "nonexistent-format") {
		t.Errorf("error should mention the format name; got %q", err.Error())
	}
}

// ---- (g) up — dry-run prints would-apply without writing --------------------

func TestMigrate_UpDryRunPrintsWithoutWriting(t *testing.T) {
	withCleanRegistry(t, func() {
		// Register a migration so there is something to dry-run.
		RegisterMigration("kanban", "0", "1", func(p string) (string, error) {
			return "1\n", nil
		})

		cfg := newMigrateConfig(t)
		cfg.Subcommand = "up"
		cfg.DryRun = true

		// Seed a kanban sidecar at version "0" so the migration applies.
		writeSidecar(t, kanbanSidecarPath(cfg.WorkDir), "0")

		out, result, err := runMigrateCmd(t, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should report would-apply.
		if !strings.Contains(out, "dry-run") {
			t.Errorf("dry-run output should mention 'dry-run'; got:\n%s", out)
		}

		// Applied count still increments (it counts would-be-applied).
		if result.Applied != 1 {
			t.Errorf("dry-run should count 1 would-apply; got %d", result.Applied)
		}

		// Sidecar must remain at version "0" (no write).
		data, err2 := os.ReadFile(kanbanSidecarPath(cfg.WorkDir))
		if err2 != nil {
			t.Fatalf("sidecar read-back failed: %v", err2)
		}
		if strings.TrimSpace(string(data)) != "0" {
			t.Errorf("dry-run must not write; sidecar should still be '0'; got %q", string(data))
		}
	})
}

// ---- (h) up — real migration applied via registered step --------------------

func TestMigrate_UpAppliesSingleRegisteredMigration(t *testing.T) {
	withCleanRegistry(t, func() {
		RegisterMigration("kanban", "0", "1", func(p string) (string, error) {
			return "1\n", nil
		})

		cfg := newMigrateConfig(t)
		cfg.Subcommand = "up"
		cfg.Format = "kanban"

		// Seed kanban sidecar at "0".
		writeSidecar(t, kanbanSidecarPath(cfg.WorkDir), "0")

		out, result, err := runMigrateCmd(t, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Applied != 1 {
			t.Errorf("expected 1 applied; got %d", result.Applied)
		}
		if !strings.Contains(out, "applied") {
			t.Errorf("output should say 'applied'; got:\n%s", out)
		}

		// Sidecar must now be at version "1".
		data, err2 := os.ReadFile(kanbanSidecarPath(cfg.WorkDir))
		if err2 != nil {
			t.Fatalf("sidecar read-back failed: %v", err2)
		}
		if strings.TrimSpace(string(data)) != "1" {
			t.Errorf("sidecar should be '1' after migration; got %q", string(data))
		}
	})
}

// ---- (i) up — migration sequence applied in order --------------------------

func TestMigrate_UpAppliesMigrationSequence(t *testing.T) {
	withCleanRegistry(t, func() {
		applied := []string{}

		RegisterMigration("kanban", "0", "1", func(p string) (string, error) {
			applied = append(applied, "0→1")
			return "1\n", nil
		})
		RegisterMigration("kanban", "1", "2", func(p string) (string, error) {
			applied = append(applied, "1→2")
			return "2\n", nil
		})

		cfg := newMigrateConfig(t)
		cfg.Subcommand = "up"
		cfg.Format = "kanban"

		// Seed kanban sidecar at "0" so both steps run.
		writeSidecar(t, kanbanSidecarPath(cfg.WorkDir), "0")

		_, result, err := runMigrateCmd(t, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Applied != 2 {
			t.Errorf("expected 2 applied; got %d", result.Applied)
		}
		if len(applied) != 2 || applied[0] != "0→1" || applied[1] != "1→2" {
			t.Errorf("migration sequence wrong; got %v", applied)
		}

		// Sidecar should be at "2".
		data, _ := os.ReadFile(kanbanSidecarPath(cfg.WorkDir))
		if strings.TrimSpace(string(data)) != "2" {
			t.Errorf("sidecar should be '2' after sequence; got %q", string(data))
		}
	})
}

// ---- (j) up — sidecar written atomically ------------------------------------

func TestMigrate_UpNoStrandedTmpFile(t *testing.T) {
	withCleanRegistry(t, func() {
		RegisterMigration("kanban", "0", "1", func(p string) (string, error) {
			return "1\n", nil
		})

		cfg := newMigrateConfig(t)
		cfg.Subcommand = "up"
		cfg.Format = "kanban"
		writeSidecar(t, kanbanSidecarPath(cfg.WorkDir), "0")

		if _, _, err := runMigrateCmd(t, cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// No .tmp file should remain.
		tmpPath := kanbanSidecarPath(cfg.WorkDir) + ".tmp"
		if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
			t.Errorf("stranded .tmp file found at %s", tmpPath)
		}
	})
}

// ---- (k) down — always errors -----------------------------------------------

func TestMigrate_DownAlwaysErrors(t *testing.T) {
	cfg := newMigrateConfig(t)
	cfg.Subcommand = "down"

	_, _, err := runMigrateCmd(t, cfg)
	if err == nil {
		t.Fatal("expected error for 'down'")
	}
	if !strings.Contains(err.Error(), "Phase 1.5") {
		t.Errorf("error should mention Phase 1.5 deferral; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "rollback") {
		t.Errorf("error should mention rollback; got %q", err.Error())
	}
}

// TestMigrate_DownWithFormatAlsoErrors verifies `down <format>` is also deferred.
func TestMigrate_DownWithFormatAlsoErrors(t *testing.T) {
	cfg := newMigrateConfig(t)
	cfg.Subcommand = "down"
	cfg.Format = "kanban"

	_, _, err := runMigrateCmd(t, cfg)
	if err == nil {
		t.Fatal("expected error for 'down kanban'")
	}
	if !strings.Contains(err.Error(), "Phase 1.5") {
		t.Errorf("down kanban: error should mention Phase 1.5; got %q", err.Error())
	}
}

// ---- (l) unknown subcommand -------------------------------------------------

func TestMigrate_UnknownSubcommand(t *testing.T) {
	cfg := newMigrateConfig(t)
	cfg.Subcommand = "sideways"

	_, _, err := runMigrateCmd(t, cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("error should say 'unknown subcommand'; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "sideways") {
		t.Errorf("error should mention the subcommand name; got %q", err.Error())
	}
}

// ---- (m) help text phrases --------------------------------------------------

func TestMigrate_HelpTextPhrases(t *testing.T) {
	var out bytes.Buffer
	PrintHelp(&out)
	got := out.String()

	mustContain := []string{
		"yakos migrate",
		"status",
		"up",
		"down",
		"Phase 1.5",
		"kanban",
		"memory",
		"--dry-run",
		"Examples:",
	}
	for _, phrase := range mustContain {
		if !strings.Contains(got, phrase) {
			t.Errorf("help missing phrase %q;\nfull output:\n%s", phrase, got)
		}
	}
}

// ---- (n) readSidecarVersion — missing file ----------------------------------

func TestMigrate_ReadSidecarVersionMissingFile(t *testing.T) {
	v, err := readSidecarVersion(filepath.Join(t.TempDir(), "nonexistent.schema-version"))
	if err != nil {
		t.Fatalf("missing file should not error; got: %v", err)
	}
	if v != "" {
		t.Errorf("missing file should return empty version; got %q", v)
	}
}

// ---- (o) readSidecarVersion — content trimmed correctly ---------------------

func TestMigrate_ReadSidecarVersionTrimmed(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".schema-version")
	if err := os.WriteFile(p, []byte("  memory/v1  \n"), 0644); err != nil {
		t.Fatal(err)
	}
	v, err := readSidecarVersion(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "memory/v1" {
		t.Errorf("expected trimmed version 'memory/v1'; got %q", v)
	}
}

// ---- (p) atomicWriteSidecar — creates intermediate directories --------------

func TestMigrate_AtomicWriteSidecarCreatesDir(t *testing.T) {
	base := t.TempDir()
	deep := filepath.Join(base, "a", "b", "c", ".schema-version")

	if err := atomicWriteSidecar(deep, "test/v1\n"); err != nil {
		t.Fatalf("atomicWriteSidecar should create dirs: %v", err)
	}

	data, err := os.ReadFile(deep)
	if err != nil {
		t.Fatalf("read-back failed: %v", err)
	}
	if string(data) != "test/v1\n" {
		t.Errorf("content mismatch; got %q", string(data))
	}
}

// ---- RegisterMigration panic on duplicate -----------------------------------

func TestMigrate_RegisterMigrationPanicOnDuplicate(t *testing.T) {
	withCleanRegistry(t, func() {
		RegisterMigration("kanban", "0", "1", func(p string) (string, error) {
			return "1\n", nil
		})

		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic for duplicate registration; did not panic")
			}
		}()

		// This should panic.
		RegisterMigration("kanban", "0", "2", func(p string) (string, error) {
			return "2\n", nil
		})
	})
}

// ---- status reports registry count ------------------------------------------

func TestMigrate_StatusReportsRegistryCount(t *testing.T) {
	withCleanRegistry(t, func() {
		RegisterMigration("kanban", "0", "1", func(p string) (string, error) {
			return "1\n", nil
		})

		cfg := newMigrateConfig(t)
		cfg.Subcommand = "status"

		out, _, err := runMigrateCmd(t, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(out, "1 registered migration") {
			t.Errorf("status should report 1 migration; got:\n%s", out)
		}
	})
}

// ---- up memory format -------------------------------------------------------

func TestMigrate_UpMemoryFormatAccepted(t *testing.T) {
	withCleanRegistry(t, func() {
		cfg := newMigrateConfig(t)
		cfg.Subcommand = "up"
		cfg.Format = "memory"

		out, result, err := runMigrateCmd(t, cfg)
		if err != nil {
			t.Fatalf("unexpected error for memory format: %v", err)
		}
		if result.Applied != 0 {
			t.Errorf("expected 0 applied; got %d", result.Applied)
		}
		// Only memory should appear; kanban should be absent.
		if strings.Contains(out, "kanban") {
			t.Errorf("memory-only up should not mention kanban; got:\n%s", out)
		}
	})
}

// ---- migration step error propagation ----------------------------------------

func TestMigrate_UpMigrationStepErrorPropagates(t *testing.T) {
	withCleanRegistry(t, func() {
		RegisterMigration("kanban", "0", "1", func(p string) (string, error) {
			return "", fmt.Errorf("simulated migration failure")
		})

		cfg := newMigrateConfig(t)
		cfg.Subcommand = "up"
		cfg.Format = "kanban"
		writeSidecar(t, kanbanSidecarPath(cfg.WorkDir), "0")

		_, _, err := runMigrateCmd(t, cfg)
		if err == nil {
			t.Fatal("expected error from failing migration step")
		}
		if !strings.Contains(err.Error(), "simulated migration failure") {
			t.Errorf("error should include step message; got %q", err.Error())
		}
	})
}

// ---- status with pending migration indicator --------------------------------

func TestMigrate_StatusShowsPendingMigration(t *testing.T) {
	withCleanRegistry(t, func() {
		RegisterMigration("kanban", "0", "1", func(p string) (string, error) {
			return "1\n", nil
		})

		cfg := newMigrateConfig(t)
		cfg.Subcommand = "status"

		// Seed sidecar at "0" to show a pending migration.
		writeSidecar(t, kanbanSidecarPath(cfg.WorkDir), "0")

		out, _, err := runMigrateCmd(t, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, "pending migration") {
			t.Errorf("status should show 'pending migration' when version behind; got:\n%s", out)
		}
	})
}
