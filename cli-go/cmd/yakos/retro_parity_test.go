// retro_parity_test.go — Phase 1 parity tests for `yakos retro`.
//
// Design notes:
//
//  1. All tests call retro.Run directly with temp-dir paths; no bash runtime
//     or real ~/.yakos-state directory is required.
//
//  2. The bash retro.sh manages the 10-cycle retrospective cadence.
//     Subcommands: now, disable, enable, status, last, history.
//     State (auto-dispatch on/off) is stored in settings.json in bash; the
//     Go port uses a simpler sentinel file at ~/.yakos-state/retro-disabled.
//
//  3. Parity is verified behaviourally (output shape, error conditions,
//     filesystem effects) rather than byte-for-byte.
//
// Critical scenarios:
//
//	(a) disable → flag file created, output says DISABLED
//	(b) enable  → flag file removed, output says ENABLED
//	(c) enable idempotent when already enabled
//	(d) now → .retro-due marker written at work/current/
//	(e) now → error when no active session
//	(f) status no session → advisory + auto-dispatch field
//	(g) status with session → shows cycle + marker state
//	(h) status after disable → shows auto-dispatch=false
//	(i) last → error when no active session
//	(j) last → lists all 5 retro output files
//	(k) history → error when no active session
//	(l) history → advisory when no log
//	(m) history → parses retro_due flags and totals
//	(n) unknown subcommand → error with hint
//	(o) help text contains key phrases
//	(p) retro entry in portedCommands
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/retro"
)

// ---- helpers ----------------------------------------------------------------

func newRetroConfig(homeDir, subcommand string) retro.Config {
	return retro.Config{
		Subcommand: subcommand,
		HomeDir:    homeDir,
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
}

func retroOut(cfg retro.Config) string {
	return cfg.Writer.(*bytes.Buffer).String()
}

func makeRetroCurrent(t *testing.T, home, slug string) string {
	t.Helper()
	cur := filepath.Join(home, "agent-control", slug, "work", "current")
	if err := os.MkdirAll(cur, 0755); err != nil {
		t.Fatalf("mkdir current: %v", err)
	}
	return cur
}

// ---- scenario (a): disable --------------------------------------------------

func TestRetroParity_Disable_CreatesFlag(t *testing.T) {
	home := t.TempDir()
	cfg := newRetroConfig(home, "disable")
	res, err := retro.Run(cfg)
	if err != nil {
		t.Fatalf("Run disable: %v", err)
	}
	if res.AutoDispatch {
		t.Error("expected AutoDispatch=false after disable")
	}
	flagPath := filepath.Join(home, ".yakos-state", "retro-disabled")
	if _, err := os.Stat(flagPath); err != nil {
		t.Errorf("expected disabled flag at %s; stat: %v", flagPath, err)
	}
	out := retroOut(cfg)
	if !strings.Contains(out, "DISABLED") {
		t.Errorf("expected DISABLED in output; got: %q", out)
	}
}

// ---- scenario (b): enable ---------------------------------------------------

func TestRetroParity_Enable_RemovesFlag(t *testing.T) {
	home := t.TempDir()
	flagPath := filepath.Join(home, ".yakos-state", "retro-disabled")
	if err := os.MkdirAll(filepath.Dir(flagPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(flagPath, nil, 0644); err != nil {
		t.Fatalf("write flag: %v", err)
	}

	cfg := newRetroConfig(home, "enable")
	res, err := retro.Run(cfg)
	if err != nil {
		t.Fatalf("Run enable: %v", err)
	}
	if !res.AutoDispatch {
		t.Error("expected AutoDispatch=true after enable")
	}
	if _, err := os.Stat(flagPath); !os.IsNotExist(err) {
		t.Error("expected flag file to be removed")
	}
	out := retroOut(cfg)
	if !strings.Contains(out, "ENABLED") {
		t.Errorf("expected ENABLED in output; got: %q", out)
	}
}

// ---- scenario (c): enable idempotent ----------------------------------------

func TestRetroParity_Enable_Idempotent(t *testing.T) {
	home := t.TempDir()
	cfg := newRetroConfig(home, "enable")
	if _, err := retro.Run(cfg); err != nil {
		t.Fatalf("Run enable (already enabled): %v", err)
	}
}

// ---- scenario (d): now writes marker ----------------------------------------

func TestRetroParity_Now_WritesMarker(t *testing.T) {
	home := t.TempDir()
	cur := makeRetroCurrent(t, home, "proj")

	cfg := newRetroConfig(home, "now")
	cfg.ProjectDir = cur

	res, err := retro.Run(cfg)
	if err != nil {
		t.Fatalf("Run now: %v", err)
	}
	if !res.MarkerWritten {
		t.Error("expected MarkerWritten=true")
	}
	if _, err := os.Stat(res.MarkerPath); err != nil {
		t.Errorf("expected marker at %s; stat: %v", res.MarkerPath, err)
	}
	if !strings.HasSuffix(res.MarkerPath, ".retro-due") {
		t.Errorf("marker path should end .retro-due; got %q", res.MarkerPath)
	}
	out := retroOut(cfg)
	if !strings.Contains(out, ".retro-due") {
		t.Errorf("expected .retro-due in output; got: %q", out)
	}
}

// ---- scenario (e): now with no session --------------------------------------

func TestRetroParity_Now_NoSession_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newRetroConfig(home, "now")
	// No ProjectDir set, no agent-control entries.
	_, err := retro.Run(cfg)
	if err == nil {
		t.Fatal("expected error for no active session")
	}
	if !strings.Contains(err.Error(), "no active session") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (f): status no session ----------------------------------------

func TestRetroParity_Status_NoSession(t *testing.T) {
	home := t.TempDir()
	cfg := newRetroConfig(home, "status")
	res, err := retro.Run(cfg)
	if err != nil {
		t.Fatalf("Run status: %v", err)
	}
	if res.Subcommand != "status" {
		t.Errorf("expected subcommand=status; got %q", res.Subcommand)
	}
	out := retroOut(cfg)
	if !strings.Contains(out, "none detected") {
		t.Errorf("expected 'none detected'; got: %q", out)
	}
	if !strings.Contains(out, "Auto-dispatch") {
		t.Errorf("expected Auto-dispatch field; got: %q", out)
	}
	if !strings.Contains(out, "Cycle length") {
		t.Errorf("expected Cycle length field; got: %q", out)
	}
}

// ---- scenario (g): status with session --------------------------------------

func TestRetroParity_Status_WithSession(t *testing.T) {
	home := t.TempDir()
	cur := makeRetroCurrent(t, home, "proj")

	if err := os.WriteFile(filepath.Join(cur, ".cycle-count"), []byte("15\n"), 0644); err != nil {
		t.Fatalf("write cycle-count: %v", err)
	}

	cfg := newRetroConfig(home, "status")
	cfg.ProjectDir = cur
	cfg.ProjectName = "proj"

	res, err := retro.Run(cfg)
	if err != nil {
		t.Fatalf("Run status: %v", err)
	}
	if res.CurrentCycle != 15 {
		t.Errorf("expected CurrentCycle=15; got %d", res.CurrentCycle)
	}
	out := retroOut(cfg)
	if !strings.Contains(out, "15") {
		t.Errorf("expected cycle 15 in output; got: %q", out)
	}
	if !strings.Contains(out, "absent") {
		t.Errorf("expected marker absent; got: %q", out)
	}
}

// ---- scenario (h): status after disable -------------------------------------

func TestRetroParity_Status_AfterDisable(t *testing.T) {
	home := t.TempDir()

	// Disable first.
	disableCfg := newRetroConfig(home, "disable")
	if _, err := retro.Run(disableCfg); err != nil {
		t.Fatalf("disable: %v", err)
	}

	// Now check status.
	cfg := newRetroConfig(home, "status")
	res, err := retro.Run(cfg)
	if err != nil {
		t.Fatalf("Run status after disable: %v", err)
	}
	if res.AutoDispatch {
		t.Error("expected AutoDispatch=false in status after disable")
	}
	out := retroOut(cfg)
	if !strings.Contains(out, "false") {
		t.Errorf("expected 'false' for auto-dispatch; got: %q", out)
	}
}

// ---- scenario (i): last no session ------------------------------------------

func TestRetroParity_Last_NoSession_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newRetroConfig(home, "last")
	_, err := retro.Run(cfg)
	if err == nil {
		t.Fatal("expected error for no active session")
	}
	if !strings.Contains(err.Error(), "no active session") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (j): last lists all 5 files -----------------------------------

func TestRetroParity_Last_ListsFiveFiles(t *testing.T) {
	home := t.TempDir()
	cur := makeRetroCurrent(t, home, "proj")

	// Create two of the output files.
	if err := os.WriteFile(filepath.Join(cur, "lessons.md"), []byte("lesson A\n"), 0644); err != nil {
		t.Fatalf("write lessons: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cur, "mistakes.md"), []byte("mistake B\n"), 0644); err != nil {
		t.Fatalf("write mistakes: %v", err)
	}

	cfg := newRetroConfig(home, "last")
	cfg.ProjectDir = cur

	if _, err := retro.Run(cfg); err != nil {
		t.Fatalf("Run last: %v", err)
	}
	out := retroOut(cfg)
	for _, section := range []string{
		"lessons.md", "mistakes.md", "skill-candidates.md",
		"drift-report.md", "soul-proposed-edits.md",
	} {
		if !strings.Contains(out, section) {
			t.Errorf("expected section %q in output; got: %q", section, out)
		}
	}
	if !strings.Contains(out, "lesson A") {
		t.Errorf("expected 'lesson A' in output; got: %q", out)
	}
	if !strings.Contains(out, "(none yet)") {
		t.Errorf("expected '(none yet)' for absent files; got: %q", out)
	}
}

// ---- scenario (k): history no session ---------------------------------------

func TestRetroParity_History_NoSession_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newRetroConfig(home, "history")
	_, err := retro.Run(cfg)
	if err == nil {
		t.Fatal("expected error for no active session")
	}
	if !strings.Contains(err.Error(), "no active session") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (l): history no log -------------------------------------------

func TestRetroParity_History_NoLog(t *testing.T) {
	home := t.TempDir()
	cur := makeRetroCurrent(t, home, "proj")

	cfg := newRetroConfig(home, "history")
	cfg.ProjectDir = cur

	if _, err := retro.Run(cfg); err != nil {
		t.Fatalf("Run history: %v", err)
	}
	out := retroOut(cfg)
	if !strings.Contains(out, "no cycle-counter log") {
		t.Errorf("expected 'no cycle-counter log'; got: %q", out)
	}
}

// ---- scenario (m): history with log -----------------------------------------

func TestRetroParity_History_WithLog(t *testing.T) {
	home := t.TempDir()
	cur := makeRetroCurrent(t, home, "proj")

	logDir := filepath.Join(cur, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	logContent := `{"cycle":10,"retro_due":true}` + "\n" +
		`{"cycle":15,"retro_due":false}` + "\n" +
		`{"cycle":20,"retro_due":true}` + "\n"
	if err := os.WriteFile(filepath.Join(logDir, "cycle-counter.ndjson"), []byte(logContent), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cur, ".cycle-count"), []byte("25\n"), 0644); err != nil {
		t.Fatalf("write cycle-count: %v", err)
	}

	cfg := newRetroConfig(home, "history")
	cfg.ProjectDir = cur

	res, err := retro.Run(cfg)
	if err != nil {
		t.Fatalf("Run history: %v", err)
	}
	if res.CurrentCycle != 25 {
		t.Errorf("expected CurrentCycle=25; got %d", res.CurrentCycle)
	}
	out := retroOut(cfg)
	if !strings.Contains(out, "Retros triggered: 2") {
		t.Errorf("expected 2 retros triggered; got: %q", out)
	}
	if !strings.Contains(out, "Total prompts:    3") {
		t.Errorf("expected 3 total prompts; got: %q", out)
	}
}

// ---- scenario (n): unknown subcommand ---------------------------------------

func TestRetroParity_UnknownSubcommand_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newRetroConfig(home, "bogus")
	_, err := retro.Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "yakos retro help") {
		t.Errorf("expected help hint in error; got: %v", err)
	}
}

// ---- scenario (o): help text ------------------------------------------------

func TestRetroParity_HelpText_KeyPhrases(t *testing.T) {
	var buf bytes.Buffer
	retro.PrintHelp(&buf)
	help := buf.String()

	phrases := []string{
		"yakos retro",
		"now",
		"disable",
		"enable",
		"status",
		"last",
		"history",
		"cycle-counter",
		"librarian",
	}
	for _, p := range phrases {
		if !strings.Contains(help, p) {
			t.Errorf("help text missing phrase %q", p)
		}
	}
}

// ---- scenario (p): portedCommands entry -------------------------------------

func TestRetroParity_PortedCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "retro" {
			return
		}
	}
	t.Error("expected 'retro' in portedCommands; not found")
}
