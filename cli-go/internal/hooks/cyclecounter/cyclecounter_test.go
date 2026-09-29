package cyclecounter_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/cyclecounter"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/retro"
)

// ---- helpers ----------------------------------------------------------------

func buildHook(t *testing.T) (*cyclecounter.Hook, string) {
	t.Helper()
	tmp := t.TempDir()
	workDir := filepath.Join(tmp, "work", "current")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	h := cyclecounter.New(workDir, "")
	h.NowFn = func() time.Time { return time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC) }
	return h, workDir
}

func runHook(t *testing.T, h *cyclecounter.Hook) hooktype.HookOutput {
	t.Helper()
	out, err := h.Run(context.Background(), hooktype.HookInput{Event: "UserPromptSubmit"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out
}

func readCount(t *testing.T, workDir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workDir, ".cycle-count"))
	if err != nil {
		t.Fatalf("read .cycle-count: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse .cycle-count: %v", err)
	}
	return n
}

func markerExists(workDir string) bool {
	_, err := os.Stat(filepath.Join(workDir, ".retro-due"))
	return err == nil
}

// ---- tests ------------------------------------------------------------------

func TestCycleCounter_Name(t *testing.T) {
	h, _ := buildHook(t)
	if h.Name() != "cycle-counter" {
		t.Errorf("Name() = %q; want 'cycle-counter'", h.Name())
	}
}

func TestCycleCounter_ExitCode0(t *testing.T) {
	h, _ := buildHook(t)
	out := runHook(t, h)
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0; got %d", out.ExitCode)
	}
}

func TestCycleCounter_IncrementsCount(t *testing.T) {
	h, workDir := buildHook(t)
	runHook(t, h)
	if c := readCount(t, workDir); c != 1 {
		t.Errorf("expected count=1; got %d", c)
	}
	runHook(t, h)
	if c := readCount(t, workDir); c != 2 {
		t.Errorf("expected count=2; got %d", c)
	}
}

func TestCycleCounter_StartsAtZeroWithNoFile(t *testing.T) {
	h, workDir := buildHook(t)
	// Ensure .cycle-count does not exist.
	_ = os.Remove(filepath.Join(workDir, ".cycle-count"))
	runHook(t, h)
	if c := readCount(t, workDir); c != 1 {
		t.Errorf("expected count=1 from zero; got %d", c)
	}
}

func TestCycleCounter_CorruptCountResets(t *testing.T) {
	h, workDir := buildHook(t)
	// Write a non-numeric value.
	_ = os.WriteFile(filepath.Join(workDir, ".cycle-count"), []byte("CORRUPT\n"), 0644)
	runHook(t, h)
	if c := readCount(t, workDir); c != 1 {
		t.Errorf("expected count=1 after corrupt reset; got %d", c)
	}
}

func TestCycleCounter_RetroDueAtCycleLength(t *testing.T) {
	h, workDir := buildHook(t)
	h.CycleLength = 5

	// Run 4 times — no marker.
	for i := 0; i < 4; i++ {
		runHook(t, h)
	}
	if markerExists(workDir) {
		t.Error("marker should not exist before cycle length")
	}

	// Run 5th time — marker should appear.
	runHook(t, h)
	if !markerExists(workDir) {
		t.Error("expected .retro-due after cycle length")
	}
}

func TestCycleCounter_RetroNote_InStderr(t *testing.T) {
	h, _ := buildHook(t)
	h.CycleLength = 1 // trigger on every prompt

	out := runHook(t, h)
	if !bytes.Contains(out.Stderr, []byte("retrospective due")) {
		t.Errorf("expected retro note in Stderr; got %q", out.Stderr)
	}
}

func TestCycleCounter_NoMarker_WhenAutoRetroDisabled(t *testing.T) {
	h, workDir := buildHook(t)
	h.CycleLength = 1
	h.AutoRetro = false
	runHook(t, h)
	if markerExists(workDir) {
		t.Error("marker should not exist when AutoRetro=false")
	}
}

func TestCycleCounter_NoMarkerNote_WhenAutoRetroDisabled(t *testing.T) {
	h, _ := buildHook(t)
	h.CycleLength = 1
	h.AutoRetro = false
	out := runHook(t, h)
	if bytes.Contains(out.Stderr, []byte("retrospective due")) {
		t.Error("retro note should not appear when AutoRetro=false")
	}
}

func TestCycleCounter_WritesCycleCountAtomically(t *testing.T) {
	h, workDir := buildHook(t)
	runHook(t, h)
	// Atomic write leaves no .tmp file.
	entries, _ := os.ReadDir(workDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("unexpected .tmp file: %s", e.Name())
		}
	}
}

func TestCycleCounter_LogEntry_Written(t *testing.T) {
	h, workDir := buildHook(t)
	runHook(t, h)
	logFile := filepath.Join(workDir, "logs", "cycle-counter.ndjson")
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("log file missing: %v", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		t.Error("log file is empty")
	}
}

func TestCycleCounter_LogEntry_ValidJSON(t *testing.T) {
	h, workDir := buildHook(t)
	runHook(t, h)
	logFile := filepath.Join(workDir, "logs", "cycle-counter.ndjson")
	data, _ := os.ReadFile(logFile)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("invalid NDJSON: %v (line: %s)", err, line)
		}
	}
}

func TestCycleCounter_LogEntry_ContainsCycleField(t *testing.T) {
	h, workDir := buildHook(t)
	runHook(t, h)
	logFile := filepath.Join(workDir, "logs", "cycle-counter.ndjson")
	data, _ := os.ReadFile(logFile)
	if !bytes.Contains(data, []byte(`"cycle"`)) {
		t.Error("log entry missing 'cycle' field")
	}
}

func TestCycleCounter_LogEntry_RetroDueTrue_AtCycle(t *testing.T) {
	h, workDir := buildHook(t)
	h.CycleLength = 1
	runHook(t, h)
	logFile := filepath.Join(workDir, "logs", "cycle-counter.ndjson")
	data, _ := os.ReadFile(logFile)
	if !bytes.Contains(data, []byte(`"retro_due":true`)) {
		t.Error("expected retro_due=true in log at cycle length")
	}
}

func TestCycleCounter_LogEntry_RetroDueFalse_BetweenCycles(t *testing.T) {
	h, workDir := buildHook(t)
	h.CycleLength = 5
	runHook(t, h)
	logFile := filepath.Join(workDir, "logs", "cycle-counter.ndjson")
	data, _ := os.ReadFile(logFile)
	if !bytes.Contains(data, []byte(`"retro_due":false`)) {
		t.Error("expected retro_due=false in log between cycles")
	}
}

func TestCycleCounter_LogAppends_MultipleRuns(t *testing.T) {
	h, workDir := buildHook(t)
	runHook(t, h)
	runHook(t, h)
	logFile := filepath.Join(workDir, "logs", "cycle-counter.ndjson")
	data, _ := os.ReadFile(logFile)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineCount := 0
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			lineCount++
		}
	}
	if lineCount != 2 {
		t.Errorf("expected 2 log entries; got %d", lineCount)
	}
}

func TestCycleCounter_EmptyWorkDir_NoOp(t *testing.T) {
	h := cyclecounter.New("", "")
	out, err := h.Run(context.Background(), hooktype.HookInput{Event: "UserPromptSubmit"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0 for no-op; got %d", out.ExitCode)
	}
}

func TestCycleCounter_NonexistentWorkDir_NoOp(t *testing.T) {
	h := cyclecounter.New("/nonexistent/path/99999", "")
	out, err := h.Run(context.Background(), hooktype.HookInput{Event: "UserPromptSubmit"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0 for missing workdir; got %d", out.ExitCode)
	}
}

func TestCycleCounter_MultipleRetros_AtEveryMultiple(t *testing.T) {
	h, workDir := buildHook(t)
	h.CycleLength = 3

	// Cycle 3 → marker.
	for i := 0; i < 3; i++ {
		runHook(t, h)
	}
	if !markerExists(workDir) {
		t.Error("expected .retro-due at cycle 3")
	}

	// Remove marker (simulate lead clearing it).
	_ = os.Remove(filepath.Join(workDir, ".retro-due"))

	// Cycles 4-5 → no marker.
	runHook(t, h)
	runHook(t, h)
	if markerExists(workDir) {
		t.Error("unexpected .retro-due at cycle 5")
	}

	// Cycle 6 → marker again.
	runHook(t, h)
	if !markerExists(workDir) {
		t.Error("expected .retro-due at cycle 6")
	}
}

func TestCycleCounter_CustomCycleLength(t *testing.T) {
	h, workDir := buildHook(t)
	h.CycleLength = 7

	for i := 0; i < 6; i++ {
		runHook(t, h)
	}
	if markerExists(workDir) {
		t.Error("marker should not appear before cycle 7")
	}
	runHook(t, h)
	if !markerExists(workDir) {
		t.Error("expected .retro-due at cycle 7")
	}
}

func TestCycleCounter_LogDir_CreatedIfMissing(t *testing.T) {
	h, workDir := buildHook(t)
	// Remove logs/ dir if it exists.
	_ = os.RemoveAll(filepath.Join(workDir, "logs"))
	runHook(t, h)
	logFile := filepath.Join(workDir, "logs", "cycle-counter.ndjson")
	if _, err := os.Stat(logFile); err != nil {
		t.Errorf("log file not created: %v", err)
	}
}

func TestCycleCounter_DefaultCycleLengthIs10(t *testing.T) {
	if cyclecounter.DefaultCycleLength != 10 {
		t.Errorf("DefaultCycleLength = %d; want 10", cyclecounter.DefaultCycleLength)
	}
}

func TestCycleCounter_ReturnsNilError(t *testing.T) {
	h, _ := buildHook(t)
	_, err := h.Run(context.Background(), hooktype.HookInput{})
	if err != nil {
		t.Errorf("expected nil error; got %v", err)
	}
}

func TestCycleCounter_New_HasAutoRetroTrue(t *testing.T) {
	h := cyclecounter.New("/tmp", "")
	if !h.AutoRetro {
		t.Error("expected AutoRetro=true by default")
	}
}

func TestCycleCounter_New_HasDefaultCycleLength(t *testing.T) {
	h := cyclecounter.New("/tmp", "")
	if h.CycleLength != cyclecounter.DefaultCycleLength {
		t.Errorf("expected CycleLength=%d; got %d", cyclecounter.DefaultCycleLength, h.CycleLength)
	}
}

func TestCycleCounter_LogEntry_ContainsTsField(t *testing.T) {
	h, workDir := buildHook(t)
	runHook(t, h)
	logFile := filepath.Join(workDir, "logs", "cycle-counter.ndjson")
	data, _ := os.ReadFile(logFile)
	if !bytes.Contains(data, []byte(`"ts"`)) {
		t.Error("log entry missing 'ts' field")
	}
}

func TestCycleCounter_LogEntry_ContainsCycleLengthField(t *testing.T) {
	h, workDir := buildHook(t)
	runHook(t, h)
	logFile := filepath.Join(workDir, "logs", "cycle-counter.ndjson")
	data, _ := os.ReadFile(logFile)
	if !bytes.Contains(data, []byte(`"cycle_length"`)) {
		t.Error("log entry missing 'cycle_length' field")
	}
}

// ---- settings.json overrides (S-6 A-2a round 2, finding 4) -------------------
//
// Sandbox StateDir (never the real $HOME/.yakos-state) throughout, per the
// review's instruction to test with a sandbox HOME rather than touching
// the operator's real settings.

func writeSettings(t *testing.T, stateDir, content string) {
	t.Helper()
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("mkdir stateDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "settings.json"), []byte(content), 0644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
}

func TestCycleCounter_SettingsOverride_CustomCycleLength(t *testing.T) {
	tmp := t.TempDir()
	workDir := filepath.Join(tmp, "work", "current")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("mkdir workDir: %v", err)
	}
	stateDir := filepath.Join(tmp, "state")
	writeSettings(t, stateDir, `{"retro":{"cycle_length":5}}`)

	h := cyclecounter.New(workDir, stateDir)
	h.NowFn = func() time.Time { return time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC) }

	for i := 0; i < 4; i++ {
		runHook(t, h)
	}
	if markerExists(workDir) {
		t.Error("marker should not exist before the settings-overridden cycle length (5)")
	}
	runHook(t, h)
	if !markerExists(workDir) {
		t.Error("expected .retro-due at cycle 5 (settings.json override)")
	}
}

func TestCycleCounter_SettingsOverride_InvalidCycleLengthKeepsDefault(t *testing.T) {
	tmp := t.TempDir()
	workDir := filepath.Join(tmp, "work", "current")
	_ = os.MkdirAll(workDir, 0755)
	stateDir := filepath.Join(tmp, "state")
	writeSettings(t, stateDir, `{"retro":{"cycle_length":"abc"}}`)

	h := cyclecounter.New(workDir, stateDir)
	h.NowFn = func() time.Time { return time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC) }

	for i := 0; i < 9; i++ {
		runHook(t, h)
	}
	if markerExists(workDir) {
		t.Error("marker should not exist before the DEFAULT cycle length (10) when override is invalid")
	}
	runHook(t, h)
	if !markerExists(workDir) {
		t.Error("expected .retro-due at cycle 10 (default; invalid override ignored)")
	}
}

// TestCycleCounter_SettingsOverride_AutoDispatchMatrix pins K-89: an explicit
// boolean false (what `yakos retro disable` writes) must disable auto-retro.
// jq's `//` used to swallow it; bash and Go now share
// `null/absent -> true, otherwise the value itself` semantics.
func TestCycleCounter_SettingsOverride_AutoDispatchMatrix(t *testing.T) {
	cases := []struct {
		name       string
		settings   string
		wantMarker bool
	}{
		{"bool false disables", `{"retro":{"auto_dispatch":false,"cycle_length":1}}`, false},
		{"string false disables", `{"retro":{"auto_dispatch":"false","cycle_length":1}}`, false},
		{"bool true enables", `{"retro":{"auto_dispatch":true,"cycle_length":1}}`, true},
		{"string true enables", `{"retro":{"auto_dispatch":"true","cycle_length":1}}`, true},
		{"absent key enables", `{"retro":{"cycle_length":1}}`, true},
		{"null enables", `{"retro":{"auto_dispatch":null,"cycle_length":1}}`, true},
		{"retro not an object enables", `{"retro":"oops"}`, true},
		{"empty object enables", `{}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			workDir := filepath.Join(tmp, "work", "current")
			_ = os.MkdirAll(workDir, 0755)
			stateDir := filepath.Join(tmp, "state")
			writeSettings(t, stateDir, tc.settings)

			h := cyclecounter.New(workDir, stateDir)
			h.NowFn = func() time.Time { return time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC) }
			h.CycleLength = 1

			runHook(t, h)
			if got := markerExists(workDir); got != tc.wantMarker {
				t.Errorf("marker exists = %v, want %v (settings %s)", got, tc.wantMarker, tc.settings)
			}
		})
	}
}

func TestCycleCounter_SettingsOverride_AutoDispatchStringFalseDisables(t *testing.T) {
	// A literal JSON string "false" still disables (jq -r renders it as
	// the bare word false); kept alongside the boolean case.
	tmp := t.TempDir()
	workDir := filepath.Join(tmp, "work", "current")
	_ = os.MkdirAll(workDir, 0755)
	stateDir := filepath.Join(tmp, "state")
	writeSettings(t, stateDir, `{"retro":{"auto_dispatch":"false","cycle_length":1}}`)

	h := cyclecounter.New(workDir, stateDir)
	h.NowFn = func() time.Time { return time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC) }

	runHook(t, h)
	if markerExists(workDir) {
		t.Error("expected no .retro-due: auto_dispatch as a JSON STRING \"false\" does disable")
	}
}

func TestCycleCounter_SettingsOverride_MissingFileKeepsConstructedDefaults(t *testing.T) {
	tmp := t.TempDir()
	workDir := filepath.Join(tmp, "work", "current")
	_ = os.MkdirAll(workDir, 0755)
	stateDir := filepath.Join(tmp, "state") // no settings.json written

	h := cyclecounter.New(workDir, stateDir)
	h.NowFn = func() time.Time { return time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC) }
	h.CycleLength = 2 // explicit test override, must survive an absent settings file

	runHook(t, h)
	if markerExists(workDir) {
		t.Error("marker should not exist at cycle 1 of 2")
	}
	runHook(t, h)
	if !markerExists(workDir) {
		t.Error("expected .retro-due at cycle 2 (constructed CycleLength, no settings.json present)")
	}
}

// TestCycleCounter_HonorsGoRetroDisable is the K-89 end-to-end check: the
// state written by `yakos retro disable` (internal/retro) must be exactly
// what the hook reads, and `enable` must restore it.
func TestCycleCounter_HonorsGoRetroDisable(t *testing.T) {
	tmp := t.TempDir()
	workDir := filepath.Join(tmp, "work", "current")
	_ = os.MkdirAll(workDir, 0755)
	stateDir := filepath.Join(tmp, ".yakos-state")

	var sink strings.Builder
	retroCfg := func(sub string) retro.Config {
		return retro.Config{Subcommand: sub, HomeDir: tmp, Writer: &sink, ErrWriter: &sink}
	}

	newHook := func() *cyclecounter.Hook {
		h := cyclecounter.New(workDir, stateDir)
		h.NowFn = func() time.Time { return time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC) }
		h.CycleLength = 1
		return h
	}

	if _, err := retro.Run(retroCfg("disable")); err != nil {
		t.Fatalf("retro disable: %v", err)
	}
	runHook(t, newHook())
	if markerExists(workDir) {
		t.Fatal("marker written after `yakos retro disable`; hook ignored the CLI's state")
	}

	if _, err := retro.Run(retroCfg("enable")); err != nil {
		t.Fatalf("retro enable: %v", err)
	}
	runHook(t, newHook())
	if !markerExists(workDir) {
		t.Fatal("marker not written after `yakos retro enable`")
	}
}
