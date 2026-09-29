package retro

import (
	"bytes"
	"encoding/json"
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

// readSettings parses ~/.yakos-state/settings.json under home.
func readSettings(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".yakos-state", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("settings.json not valid JSON: %v\n%s", err, data)
	}
	return m
}

func writeSettingsFile(t *testing.T, home, content string) {
	t.Helper()
	p := filepath.Join(home, ".yakos-state", "settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
}

func TestDisable_WritesSettingsFalse(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "disable")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run disable: %v", err)
	}
	if res.AutoDispatch {
		t.Error("expected AutoDispatch=false after disable")
	}
	m := readSettings(t, home)
	retro, _ := m["retro"].(map[string]any)
	if v, ok := retro["auto_dispatch"].(bool); !ok || v {
		t.Errorf("expected .retro.auto_dispatch == false in settings.json; got %v", retro["auto_dispatch"])
	}
	if fileExists(filepath.Join(home, ".yakos-state", "retro-disabled")) {
		t.Error("legacy sentinel must not be (re)created")
	}
	out := testOut(cfg)
	if !strings.Contains(out, "DISABLED") {
		t.Errorf("expected DISABLED in output; got: %q", out)
	}
}

func TestEnable_WritesSettingsTrue_AndClearsLegacySentinel(t *testing.T) {
	home := t.TempDir()
	writeSettingsFile(t, home, `{"retro":{"auto_dispatch":false}}`)
	flagPath := filepath.Join(home, ".yakos-state", "retro-disabled")
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
	retro, _ := readSettings(t, home)["retro"].(map[string]any)
	if v, ok := retro["auto_dispatch"].(bool); !ok || !v {
		t.Errorf("expected .retro.auto_dispatch == true; got %v", retro["auto_dispatch"])
	}
	if fileExists(flagPath) {
		t.Error("expected stale legacy sentinel to be removed after enable")
	}
	out := testOut(cfg)
	if !strings.Contains(out, "ENABLED") {
		t.Errorf("expected ENABLED in output; got: %q", out)
	}
}

func TestDisable_PreservesOtherSettingsKeys(t *testing.T) {
	home := t.TempDir()
	writeSettingsFile(t, home, `{"theme":"dark","big":12345678901234567890,"retro":{"cycle_length":7}}`)
	if _, err := Run(newTestConfig(home, "disable")); err != nil {
		t.Fatalf("disable: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".yakos-state", "settings.json"))
	if !strings.Contains(string(raw), "12345678901234567890") {
		t.Errorf("large number was not preserved verbatim: %s", raw)
	}
	m := readSettings(t, home)
	if m["theme"] != "dark" {
		t.Errorf("theme key lost: %v", m)
	}
	retro, _ := m["retro"].(map[string]any)
	if retro["cycle_length"] != float64(7) {
		t.Errorf("retro.cycle_length lost: %v", retro)
	}
	if retro["auto_dispatch"] != false {
		t.Errorf("auto_dispatch not false: %v", retro)
	}
}

func TestDisable_RefusesToClobberMalformedSettings(t *testing.T) {
	home := t.TempDir()
	writeSettingsFile(t, home, `{not json`)
	if _, err := Run(newTestConfig(home, "disable")); err == nil {
		t.Fatal("expected an error for malformed settings.json")
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".yakos-state", "settings.json"))
	if string(raw) != `{not json` {
		t.Errorf("malformed settings.json was modified: %q", raw)
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

	if _, err := Run(newTestConfig(home, "disable")); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if autoDispatchEnabled(home) {
		t.Fatal("auto-dispatch should be disabled after disable")
	}
	if _, err := Run(newTestConfig(home, "enable")); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !autoDispatchEnabled(home) {
		t.Fatal("auto-dispatch should be enabled after enable")
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
	writeSettingsFile(t, home, `{"retro":{"auto_dispatch":false}}`)

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

// ---- autoDispatchEnabled / fileExists --------------------------------------

func TestAutoDispatchEnabled_Matrix(t *testing.T) {
	cases := []struct {
		name, settings string
		want           bool
	}{
		{"file absent", "", true},
		{"bool false", `{"retro":{"auto_dispatch":false}}`, false},
		{"string false", `{"retro":{"auto_dispatch":"false"}}`, false},
		{"bool true", `{"retro":{"auto_dispatch":true}}`, true},
		{"null", `{"retro":{"auto_dispatch":null}}`, true},
		{"absent key", `{"retro":{}}`, true},
		{"retro not object", `{"retro":"x"}`, true},
		{"malformed", `{nope`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.settings != "" {
				writeSettingsFile(t, home, tc.settings)
			}
			if got := autoDispatchEnabled(home); got != tc.want {
				t.Errorf("autoDispatchEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAutoDispatchEnabled_IgnoresLegacySentinel(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".yakos-state", "retro-disabled")
	_ = os.MkdirAll(filepath.Dir(p), 0755)
	if err := os.WriteFile(p, nil, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !autoDispatchEnabled(home) {
		t.Error("legacy sentinel must not be consulted (no hook reads it)")
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

func TestStatus_LegacySentinel_WarnsOnStderr(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".yakos-state", "retro-disabled")
	_ = os.MkdirAll(filepath.Dir(p), 0755)
	if err := os.WriteFile(p, nil, 0644); err != nil {
		t.Fatal(err)
	}
	cfg := newTestConfig(home, "status")
	var errBuf strings.Builder
	cfg.ErrWriter = &errBuf
	res, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !res.AutoDispatch {
		t.Error("legacy sentinel must not change reported state")
	}
	if !strings.Contains(errBuf.String(), "legacy sentinel") {
		t.Errorf("expected legacy-sentinel note on stderr; got %q", errBuf.String())
	}
	if !fileExists(p) {
		t.Error("status must not delete the sentinel")
	}

	// No sentinel -> no note.
	home2 := t.TempDir()
	cfg2 := newTestConfig(home2, "status")
	var errBuf2 strings.Builder
	cfg2.ErrWriter = &errBuf2
	if _, err := Run(cfg2); err != nil {
		t.Fatal(err)
	}
	if errBuf2.Len() != 0 {
		t.Errorf("unexpected stderr without sentinel: %q", errBuf2.String())
	}
}
