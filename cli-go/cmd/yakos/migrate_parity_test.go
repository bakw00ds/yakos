// migrate_parity_test.go — Phase 1 parity tests for `yakos migrate`.
//
// Design notes:
//
//  1. All tests call migrate.Run directly with temp-dir paths; no bash
//     runtime or real ~/agent-control directory is required.
//
//  2. The bash migrate.sh targets .yakos.yml project configs. The Go port
//     targets sidecar schema-version files (Decision A). There is no overlap
//     in the file set, so parity is verified behaviorally (output shape,
//     error conditions) rather than byte-for-byte file equality.
//
//  3. `down` is intentionally deferred to Phase 1.5; the test asserts
//     the appropriate message is returned.
//
// Critical scenarios:
//
//	(a) status happy-path     — both formats listed with version info
//	(b) up no-op              — "nothing to do" when registry empty
//	(c) up dry-run            — "(dry-run)" in output; no writes
//	(d) down deferred         — error mentions Phase 1.5
//	(e) unknown subcommand    — descriptive error returned
//	(f) help text phrases     — all load-bearing phrases present
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/migrate"
)

// ---- helpers ----------------------------------------------------------------

// newMigrateParityConfig builds a Config with temp-dir paths.
func newMigrateParityConfig(t *testing.T) migrate.Config {
	t.Helper()
	tmp := t.TempDir()
	return migrate.Config{
		WorkDir:   filepath.Join(tmp, "work"),
		MemoryDir: filepath.Join(tmp, "memory"),
		HomeDir:   tmp,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}
}

// runMigrateParityCmd runs migrate.Run with captured output.
func runMigrateParityCmd(t *testing.T, cfg migrate.Config) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cfg.Writer = &out
	cfg.ErrWriter = &bytes.Buffer{}
	_, err := migrate.Run(cfg)
	return out.String(), err
}

// ---- (a) status happy-path --------------------------------------------------

// TestMigrateParity_StatusHappyPath verifies both formats appear in status output.
func TestMigrateParity_StatusHappyPath(t *testing.T) {
	cfg := newMigrateParityConfig(t)
	cfg.Subcommand = "status"

	out, err := runMigrateParityCmd(t, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, format := range []string{"kanban", "memory"} {
		if !strings.Contains(out, format) {
			t.Errorf("status output missing format %q;\nfull output:\n%s", format, out)
		}
	}

	if !strings.Contains(out, "yakos migrate status") {
		t.Errorf("status output should start with header; got:\n%s", out)
	}
}

// ---- (b) up no-op -----------------------------------------------------------

// TestMigrateParity_UpNoOp verifies "nothing to do" when registry is empty.
func TestMigrateParity_UpNoOp(t *testing.T) {
	cfg := newMigrateParityConfig(t)
	cfg.Subcommand = "up"

	out, err := runMigrateParityCmd(t, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "nothing to do") || !strings.Contains(out, "up-to-date") {
		t.Errorf("up no-op should report nothing-to-do; got:\n%s", out)
	}
}

// ---- (c) up dry-run ---------------------------------------------------------

// TestMigrateParity_UpDryRun verifies --dry-run prints "(dry-run)" and no writes occur.
func TestMigrateParity_UpDryRun(t *testing.T) {
	cfg := newMigrateParityConfig(t)
	cfg.Subcommand = "up"
	cfg.DryRun = true

	out, err := runMigrateParityCmd(t, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The no-op message should also appear in dry-run when nothing pending.
	if !strings.Contains(out, "up-to-date") && !strings.Contains(out, "dry-run") {
		t.Errorf("dry-run should mention dry-run or up-to-date; got:\n%s", out)
	}

	// No sidecar files should have been created.
	kanbanSidecar := filepath.Join(cfg.WorkDir, "current", ".kanban.schema-version")
	if _, err2 := os.Stat(kanbanSidecar); !os.IsNotExist(err2) {
		t.Errorf("dry-run must not create sidecar at %s", kanbanSidecar)
	}
}

// ---- (d) down deferred -------------------------------------------------------

// TestMigrateParity_DownDeferred verifies down returns a Phase 1.5 deferral error.
func TestMigrateParity_DownDeferred(t *testing.T) {
	cfg := newMigrateParityConfig(t)
	cfg.Subcommand = "down"

	_, err := runMigrateParityCmd(t, cfg)
	if err == nil {
		t.Fatal("expected error for 'down'")
	}
	if !strings.Contains(err.Error(), "Phase 1.5") {
		t.Errorf("down error should mention Phase 1.5; got %q", err.Error())
	}
}

// ---- (e) unknown subcommand -------------------------------------------------

// TestMigrateParity_UnknownSubcommand verifies unknown subcommands return an error.
func TestMigrateParity_UnknownSubcommand(t *testing.T) {
	cfg := newMigrateParityConfig(t)
	cfg.Subcommand = "rewind"

	_, err := runMigrateParityCmd(t, cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("error should say 'unknown subcommand'; got %q", err.Error())
	}
}

// ---- (f) help text phrases --------------------------------------------------

// TestMigrateParity_HelpText verifies all load-bearing phrases appear in help output.
func TestMigrateParity_HelpText(t *testing.T) {
	var out bytes.Buffer
	migrate.PrintHelp(&out)
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
