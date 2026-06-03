package retro

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

func newTestConfig(homeDir, subcommand string) Config {
	return Config{
		Subcommand: subcommand,
		HomeDir:    homeDir,
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
}

func testOut(cfg Config) string {
	return cfg.Writer.(*bytes.Buffer).String()
}

// makeCurrentDir creates ~/agent-control/<slug>/work/current and registers it
// via cfg.ProjectDir so resolveCurrentDir returns it.
func makeCurrentDir(t *testing.T, home, slug string) string {
	t.Helper()
	cur := filepath.Join(home, "agent-control", slug, "work", "current")
	if err := os.MkdirAll(cur, 0755); err != nil {
		t.Fatalf("mkdir current: %v", err)
	}
	return cur
}

// ---- enable/disable ---------------------------------------------------------

func TestDisable_CreatesFlag(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "disable")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run disable: %v", err)
	}
	if res.AutoDispatch {
		t.Error("expected AutoDispatch=false after disable")
	}
	flagPath := filepath.Join(home, ".yakos-state", "retro-disabled")
	if !fileExists(flagPath) {
		t.Errorf("expected disabled flag at %s", flagPath)
	}
	out := testOut(cfg)
	if !strings.Contains(out, "DISABLED") {
		t.Errorf("expected DISABLED in output; got: %q", out)
	}
}

func TestEnable_RemovesFlag(t *testing.T) {
	home := t.TempDir()
	// Pre-create the flag.
	flagPath := filepath.Join(home, ".yakos-state", "retro-disabled")
	if err := os.MkdirAll(filepath.Dir(flagPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(flagPath, nil, 0644); err != nil {
		t.Fatalf("write flag: %v", err)
	}

	cfg := newTestConfig(home, "enable")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run enable: %v", err)
	}
	if !res.AutoDispatch {
		t.Error("expected AutoDispatch=true after enable")
	}
	if fileExists(flagPath) {
		t.Error("expected disabled flag to be removed after enable")
	}
	out := testOut(cfg)
	if !strings.Contains(out, "ENABLED") {
		t.Errorf("expected ENABLED in output; got: %q", out)
	}
}

func TestEnable_IdempotentWhenAlreadyEnabled(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "enable")
	// No flag exists — enable when already enabled should not error.
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run enable (already enabled): %v", err)
	}
	if !res.AutoDispatch {
		t.Error("expected AutoDispatch=true")
	}
}

func TestDisable_Enable_RoundTrip(t *testing.T) {
	home := t.TempDir()

	cfg := newTestConfig(home, "disable")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("disable: %v", err)
	}
	flagPath := filepath.Join(home, ".yakos-state", "retro-disabled")
	if !fileExists(flagPath) {
		t.Fatal("flag should exist after disable")
	}

	cfg2 := newTestConfig(home, "enable")
	if _, err := Run(cfg2); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if fileExists(flagPath) {
		t.Fatal("flag should be gone after enable")
	}
}

// ---- now (marker) -----------------------------------------------------------

func TestNow_WritesMarker(t *testing.T) {
	home := t.TempDir()
	cur := makeCurrentDir(t, home, "myproj")

	cfg := newTestConfig(home, "now")
	cfg.ProjectDir = cur

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run now: %v", err)
	}
	if !res.MarkerWritten {
		t.Error("expected MarkerWritten=true")
	}
	if !fileExists(res.MarkerPath) {
		t.Errorf("expected marker file at %s", res.MarkerPath)
	}
	out := testOut(cfg)
	if !strings.Contains(out, ".retro-due") {
		t.Errorf("expected .retro-due in output; got: %q", out)
	}
	if !strings.Contains(out, "librarian") {
		t.Errorf("expected librarian mention in output; got: %q", out)
	}
}

func TestNow_Idempotent(t *testing.T) {
	home := t.TempDir()
	cur := makeCurrentDir(t, home, "myproj")

	cfg := newTestConfig(home, "now")
	cfg.ProjectDir = cur

	// Write marker twice — should not error.
	if _, err := Run(cfg); err != nil {
		t.Fatalf("first now: %v", err)
	}
	if _, err := Run(cfg); err != nil {
		t.Fatalf("second now: %v", err)
	}

	markerPath := filepath.Join(cur, ".retro-due")
	if !fileExists(markerPath) {
		t.Error("marker should exist after second now")
	}
}

func TestNow_NoActiveSession_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "now")
	// No ProjectDir, no agent-control entries.
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error when no session detected")
	}
	if !strings.Contains(err.Error(), "no active session") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNow_MarkerPathContainsRetrodue(t *testing.T) {
	home := t.TempDir()
	cur := makeCurrentDir(t, home, "proj")
	cfg := newTestConfig(home, "now")
	cfg.ProjectDir = cur

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run now: %v", err)
	}
	if !strings.HasSuffix(res.MarkerPath, ".retro-due") {
		t.Errorf("marker path should end in .retro-due; got %q", res.MarkerPath)
	}
}

// ---- status -----------------------------------------------------------------

func TestStatus_NoSession(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "status")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run status: %v", err)
	}
	if res.Subcommand != "status" {
		t.Errorf("expected subcommand=status; got %q", res.Subcommand)
	}
	out := testOut(cfg)
	if !strings.Contains(out, "none detected") {
		t.Errorf("expected 'none detected' for no-session status; got: %q", out)
	}
	if !strings.Contains(out, "Auto-dispatch") {
		t.Errorf("expected Auto-dispatch field; got: %q", out)
	}
}

func TestStatus_WithSession_ShowsCycleAndMarker(t *testing.T) {
	home := t.TempDir()
	cur := makeCurrentDir(t, home, "myproj")

	// Write a cycle count.
	if err := os.WriteFile(filepath.Join(cur, ".cycle-count"), []byte("7\n"), 0644); err != nil {
		t.Fatalf("write cycle-count: %v", err)
	}

	cfg := newTestConfig(home, "status")
	cfg.ProjectDir = cur
	cfg.ProjectName = "myproj"

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run status: %v", err)
	}
	if res.CurrentCycle != 7 {
		t.Errorf("expected CurrentCycle=7; got %d", res.CurrentCycle)
	}
	out := testOut(cfg)
	if !strings.Contains(out, "7") {
		t.Errorf("expected cycle count 7 in output; got: %q", out)
	}
	if !strings.Contains(out, "absent") {
		t.Errorf("expected marker absent; got: %q", out)
	}
}

func TestStatus_WithMarkerPresent(t *testing.T) {
	home := t.TempDir()
	cur := makeCurrentDir(t, home, "myproj")

	// Place the marker.
	if err := os.WriteFile(filepath.Join(cur, ".retro-due"), nil, 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	cfg := newTestConfig(home, "status")
	cfg.ProjectDir = cur
	cfg.ProjectName = "myproj"

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run status: %v", err)
	}
	if !res.MarkerPresent {
		t.Error("expected MarkerPresent=true")
	}
	out := testOut(cfg)
	if !strings.Contains(out, "PRESENT") {
		t.Errorf("expected PRESENT in output; got: %q", out)
	}
}

func TestStatus_Disabled_ShowsFalse(t *testing.T) {
	home := t.TempDir()
	flagPath := filepath.Join(home, ".yakos-state", "retro-disabled")
	if err := os.MkdirAll(filepath.Dir(flagPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(flagPath, nil, 0644); err != nil {
		t.Fatalf("write flag: %v", err)
	}

	cfg := newTestConfig(home, "status")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run status: %v", err)
	}
	if res.AutoDispatch {
		t.Error("expected AutoDispatch=false when disabled")
	}
	out := testOut(cfg)
	if !strings.Contains(out, "false") {
		t.Errorf("expected 'false' for disabled auto-dispatch in output; got: %q", out)
	}
}

// ---- last -------------------------------------------------------------------

func TestLast_NoSession_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "last")
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error when no session detected")
	}
	if !strings.Contains(err.Error(), "no active session") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLast_ShowsAllOutputFiles(t *testing.T) {
	home := t.TempDir()
	cur := makeCurrentDir(t, home, "myproj")

	// Create one of the output files.
	if err := os.WriteFile(filepath.Join(cur, "lessons.md"), []byte("lesson content\n"), 0644); err != nil {
		t.Fatalf("write lessons: %v", err)
	}

	cfg := newTestConfig(home, "last")
	cfg.ProjectDir = cur

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run last: %v", err)
	}
	out := testOut(cfg)
	// All five sections should be present.
	for _, section := range []string{"lessons.md", "mistakes.md", "skill-candidates.md", "drift-report.md", "soul-proposed-edits.md"} {
		if !strings.Contains(out, section) {
			t.Errorf("expected section %q in output; got: %q", section, out)
		}
	}
	if !strings.Contains(out, "lesson content") {
		t.Errorf("expected file content in output; got: %q", out)
	}
	if !strings.Contains(out, "(none yet)") {
		t.Errorf("expected '(none yet)' for absent files; got: %q", out)
	}
}

// ---- history ----------------------------------------------------------------

func TestHistory_NoSession_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "history")
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error when no session detected")
	}
	if !strings.Contains(err.Error(), "no active session") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHistory_NoLog(t *testing.T) {
	home := t.TempDir()
	cur := makeCurrentDir(t, home, "myproj")
	cfg := newTestConfig(home, "history")
	cfg.ProjectDir = cur

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run history (no log): %v", err)
	}
	out := testOut(cfg)
	if !strings.Contains(out, "no cycle-counter log") {
		t.Errorf("expected 'no cycle-counter log'; got: %q", out)
	}
}

func TestHistory_WithLog(t *testing.T) {
	home := t.TempDir()
	cur := makeCurrentDir(t, home, "myproj")

	// Create log dir and file.
	logDir := filepath.Join(cur, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	logContent := `{"cycle":10,"retro_due":true}` + "\n" +
		`{"cycle":11,"retro_due":false}` + "\n" +
		`{"cycle":20,"retro_due":true}` + "\n"
	if err := os.WriteFile(filepath.Join(logDir, "cycle-counter.ndjson"), []byte(logContent), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cur, ".cycle-count"), []byte("22\n"), 0644); err != nil {
		t.Fatalf("write cycle-count: %v", err)
	}

	cfg := newTestConfig(home, "history")
	cfg.ProjectDir = cur

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run history: %v", err)
	}
	if res.CurrentCycle != 22 {
		t.Errorf("expected CurrentCycle=22; got %d", res.CurrentCycle)
	}
	out := testOut(cfg)
	if !strings.Contains(out, "Retros triggered: 2") {
		t.Errorf("expected 2 retros triggered; got: %q", out)
	}
	if !strings.Contains(out, "Total prompts:    3") {
		t.Errorf("expected 3 total prompts; got: %q", out)
	}
	if !strings.Contains(out, "Current cycle:    22") {
		t.Errorf("expected current cycle 22; got: %q", out)
	}
}

// ---- unknown subcommand -----------------------------------------------------

func TestUnknownSubcommand_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "bogus")
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- PrintHelp --------------------------------------------------------------

func TestPrintHelp_ContainsKeyPhrases(t *testing.T) {
	var buf bytes.Buffer
	PrintHelp(&buf)
	help := buf.String()

	phrases := []string{
		"yakos retro",
		"now",
		"disable",
		"enable",
		"status",
		"last",
		"history",
		"librarian",
		"cycle-counter",
	}
	for _, p := range phrases {
		if !strings.Contains(help, p) {
			t.Errorf("help text missing phrase %q", p)
		}
	}
}

// ---- atomicTouch ------------------------------------------------------------

func TestAtomicTouch_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sentinel")
	if err := atomicTouch(path); err != nil {
		t.Fatalf("atomicTouch: %v", err)
	}
	if !fileExists(path) {
		t.Error("expected sentinel file to exist")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Size() != 0 {
		t.Errorf("expected empty file; got %d bytes", fi.Size())
	}
}

func TestAtomicTouch_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "sentinel")
	if err := atomicTouch(path); err != nil {
		t.Fatalf("atomicTouch nested: %v", err)
	}
	if !fileExists(path) {
		t.Error("expected nested sentinel file to exist")
	}
}

// ---- isDisabled / fileExists ------------------------------------------------

func TestIsDisabled_FlagAbsent(t *testing.T) {
	dir := t.TempDir()
	if isDisabled(filepath.Join(dir, "retro-disabled")) {
		t.Error("expected isDisabled=false when flag absent")
	}
}

func TestIsDisabled_FlagPresent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "retro-disabled")
	if err := os.WriteFile(p, nil, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !isDisabled(p) {
		t.Error("expected isDisabled=true when flag present")
	}
}

// ---- readCycleCount ---------------------------------------------------------

func TestReadCycleCount_Missing(t *testing.T) {
	dir := t.TempDir()
	n, _ := readCycleCount(dir)
	if n != 0 {
		t.Errorf("expected 0 for missing file; got %d", n)
	}
}

func TestReadCycleCount_ValidValue(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".cycle-count"), []byte("42\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	n, _ := readCycleCount(dir)
	if n != 42 {
		t.Errorf("expected 42; got %d", n)
	}
}

func TestReadCycleCount_Garbage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".cycle-count"), []byte("not-a-number\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	n, _ := readCycleCount(dir)
	if n != 0 {
		t.Errorf("expected 0 for garbage; got %d", n)
	}
}
