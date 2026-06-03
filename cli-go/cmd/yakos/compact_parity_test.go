// compact_parity_test.go — Phase 1 parity tests for `yakos compact`.
//
// Design notes:
//
//  1. All tests call compact.Run directly with temp-dir paths; no bash runtime
//     or real ~/.yakos-state directory is required.
//
//  2. The bash compact.sh manages context-compaction advisories:
//       now        — print the slash command to paste into the session
//       threshold  — show or set notice threshold (default 75%)
//       history    — display the last 50 compaction log entries
//
//  3. Parity is verified behaviourally (output shape, filesystem effects,
//     error conditions) rather than byte-for-byte.
//
// Critical scenarios:
//
//	(a) now: prints /compact advice and M3.1 advisory
//	(b) now: appends manual-recommend entry to compact-log.ndjson
//	(c) threshold show: default values when no settings.json
//	(d) threshold show: reads stored values from settings.json
//	(e) threshold set: writes notice threshold to settings.json atomically
//	(f) threshold set: preserves other keys in settings.json
//	(g) threshold set: rejects non-numeric arg
//	(h) threshold set: rejects out-of-range value
//	(i) history: advisory when log absent
//	(j) history: formats and shows entries from log
//	(k) history: caps output at 50 lines
//	(l) unknown subcommand: error with hint
//	(m) help text contains key phrases
//	(n) compact entry in portedCommands
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/compact"
)

// ---- helpers ----------------------------------------------------------------

func newCompactConfig(t *testing.T, sub string) compact.Config {
	t.Helper()
	return compact.Config{
		Subcommand: sub,
		HomeDir:    t.TempDir(),
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
		Now:        func() time.Time { return time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC) },
	}
}

func compactOut(cfg compact.Config) string    { return cfg.Writer.(*bytes.Buffer).String() }
func compactErrOut(cfg compact.Config) string { return cfg.ErrWriter.(*bytes.Buffer).String() }

// ---- scenario (a): now prints advice ----------------------------------------

func TestCompactParity_Now_PrintsAdvice(t *testing.T) {
	cfg := newCompactConfig(t, "now")
	res, err := compact.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Subcommand != "now" {
		t.Errorf("expected Subcommand='now'; got %q", res.Subcommand)
	}
	o := compactOut(cfg)
	for _, phrase := range []string{"/compact", "Claude Code", "M3.1", "checkpoint"} {
		if !strings.Contains(o, phrase) {
			t.Errorf("output missing phrase %q; got:\n%s", phrase, o)
		}
	}
}

// ---- scenario (b): now appends log entry ------------------------------------

func TestCompactParity_Now_AppendsLog(t *testing.T) {
	cfg := newCompactConfig(t, "now")
	cfg.Runtime = "claude"
	res, err := compact.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.LogEntry == "" {
		t.Fatal("expected non-empty LogEntry")
	}

	// Verify the NDJSON shape.
	var rec map[string]string
	if err := json.Unmarshal([]byte(res.LogEntry), &rec); err != nil {
		t.Fatalf("parse LogEntry JSON: %v", err)
	}
	if rec["kind"] != "manual-recommend" {
		t.Errorf("expected kind='manual-recommend'; got %q", rec["kind"])
	}
	if rec["runtime"] != "claude" {
		t.Errorf("expected runtime='claude'; got %q", rec["runtime"])
	}

	// Confirm file on disk.
	logPath := filepath.Join(cfg.HomeDir, ".yakos-state", "compact-log.ndjson")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read compact log: %v", err)
	}
	if !strings.Contains(string(data), "manual-recommend") {
		t.Errorf("log file missing 'manual-recommend'; got:\n%s", string(data))
	}
}

// ---- scenario (c): threshold show defaults ----------------------------------

func TestCompactParity_Threshold_ShowDefaults(t *testing.T) {
	cfg := newCompactConfig(t, "threshold")
	res, err := compact.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.NoticeThreshold != 75 {
		t.Errorf("expected NoticeThreshold=75; got %d", res.NoticeThreshold)
	}
	if res.WarningThreshold != 90 {
		t.Errorf("expected WarningThreshold=90; got %d", res.WarningThreshold)
	}
	o := compactOut(cfg)
	if !strings.Contains(o, "notice = 75%") {
		t.Errorf("expected 'notice = 75%%' in output; got: %q", o)
	}
	if !strings.Contains(o, "warning = 90%") {
		t.Errorf("expected 'warning = 90%%' in output; got: %q", o)
	}
}

// ---- scenario (d): threshold show reads settings.json -----------------------

func TestCompactParity_Threshold_ShowReadsSettings(t *testing.T) {
	cfg := newCompactConfig(t, "threshold")
	settingsPath := filepath.Join(cfg.HomeDir, ".yakos-state", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"context_thresholds":{"notice":60,"warning":85}}`), 0644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	res, err := compact.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.NoticeThreshold != 60 {
		t.Errorf("expected NoticeThreshold=60; got %d", res.NoticeThreshold)
	}
	if res.WarningThreshold != 85 {
		t.Errorf("expected WarningThreshold=85; got %d", res.WarningThreshold)
	}
}

// ---- scenario (e): threshold set writes atomically --------------------------

func TestCompactParity_Threshold_SetWritesAtomically(t *testing.T) {
	cfg := newCompactConfig(t, "threshold")
	cfg.ThresholdArg = "80"
	res, err := compact.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.ThresholdSet {
		t.Errorf("expected ThresholdSet=true")
	}
	if res.NoticeThreshold != 80 {
		t.Errorf("expected NoticeThreshold=80; got %d", res.NoticeThreshold)
	}

	settingsPath := filepath.Join(cfg.HomeDir, ".yakos-state", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	thresholds, _ := m["context_thresholds"].(map[string]interface{})
	if int(thresholds["notice"].(float64)) != 80 {
		t.Errorf("expected notice=80 in settings; got %v", thresholds["notice"])
	}

	// No stray .tmp file.
	if _, err := os.Stat(settingsPath + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("stray .tmp file should not exist after atomic write")
	}
}

// ---- scenario (f): threshold set preserves other keys -----------------------

func TestCompactParity_Threshold_SetPreservesKeys(t *testing.T) {
	cfg := newCompactConfig(t, "threshold")
	settingsPath := filepath.Join(cfg.HomeDir, ".yakos-state", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"other_key":"preserved","context_thresholds":{"notice":75}}`), 0644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	cfg.ThresholdArg = "65"
	if _, err := compact.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if !strings.Contains(string(data), "other_key") {
		t.Errorf("expected 'other_key' preserved; settings:\n%s", string(data))
	}
}

// ---- scenario (g): threshold set rejects non-numeric -----------------------

func TestCompactParity_Threshold_RejectsNonNumeric(t *testing.T) {
	cfg := newCompactConfig(t, "threshold")
	cfg.ThresholdArg = "notanumber"
	_, err := compact.Run(cfg)
	if err == nil {
		t.Fatal("expected error for non-numeric threshold")
	}
	if !strings.Contains(err.Error(), "number") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (h): threshold set rejects out of range -----------------------

func TestCompactParity_Threshold_RejectsOutOfRange(t *testing.T) {
	for _, bad := range []string{"0", "100"} {
		cfg := newCompactConfig(t, "threshold")
		cfg.ThresholdArg = bad
		_, err := compact.Run(cfg)
		if err == nil {
			t.Errorf("expected error for threshold=%s", bad)
		}
		if !strings.Contains(err.Error(), "1-99") {
			t.Errorf("expected '1-99' in error for threshold=%s; got: %v", bad, err)
		}
	}
}

// ---- scenario (i): history advisory when log absent -------------------------

func TestCompactParity_History_AdvisoryWhenAbsent(t *testing.T) {
	cfg := newCompactConfig(t, "history")
	res, err := compact.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := compactOut(cfg)
	if !strings.Contains(o, "no compaction history") {
		t.Errorf("expected advisory; got: %q", o)
	}
	if len(res.HistoryLines) != 0 {
		t.Errorf("expected empty HistoryLines; got %v", res.HistoryLines)
	}
}

// ---- scenario (j): history formats entries ---------------------------------

func TestCompactParity_History_FormatsEntries(t *testing.T) {
	cfg := newCompactConfig(t, "history")
	logPath := filepath.Join(cfg.HomeDir, ".yakos-state", "compact-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	entries := strings.Join([]string{
		`{"ts":"2026-06-01T10:00:00Z","kind":"manual-recommend","pct":"","runtime":"claude","by_user":"alice"}`,
		`{"ts":"2026-06-02T08:00:00Z","kind":"auto","pct":"82","runtime":"codex","by_user":"bob"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(entries), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	res, err := compact.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.HistoryLines) != 2 {
		t.Fatalf("expected 2 history lines; got %d", len(res.HistoryLines))
	}
	o := compactOut(cfg)
	if !strings.Contains(o, "manual-recommend") {
		t.Errorf("expected 'manual-recommend' in output; got: %q", o)
	}
	// Empty pct should render as "-".
	if !strings.Contains(o, " - ") {
		t.Errorf("expected ' - ' for empty pct; got: %q", o)
	}
	if !strings.Contains(o, "82") {
		t.Errorf("expected '82' in output; got: %q", o)
	}
	// Ignore unused variable.
	_ = compactErrOut(cfg)
}

// ---- scenario (k): history caps at 50 lines ---------------------------------

func TestCompactParity_History_CapsAt50(t *testing.T) {
	cfg := newCompactConfig(t, "history")
	logPath := filepath.Join(cfg.HomeDir, ".yakos-state", "compact-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var sb strings.Builder
	for i := 0; i < 60; i++ {
		sb.WriteString(`{"ts":"2026-06-01T00:00:00Z","kind":"auto","pct":"","runtime":"claude","by_user":"u"}`)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(logPath, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	res, err := compact.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.HistoryLines) != 50 {
		t.Errorf("expected 50 history lines; got %d", len(res.HistoryLines))
	}
}

// ---- scenario (l): unknown subcommand error ---------------------------------

func TestCompactParity_UnknownSubcommand_Error(t *testing.T) {
	cfg := newCompactConfig(t, "bogus")
	_, err := compact.Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "yakos compact help") {
		t.Errorf("expected help hint; got: %v", err)
	}
}

// ---- scenario (m): help text key phrases ------------------------------------

func TestCompactParity_HelpText_KeyPhrases(t *testing.T) {
	var buf bytes.Buffer
	compact.PrintHelp(&buf)
	help := buf.String()

	phrases := []string{
		"yakos compact",
		"now",
		"threshold",
		"history",
		"/compact",
		"M3.1",
	}
	for _, p := range phrases {
		if !strings.Contains(help, p) {
			t.Errorf("help text missing phrase %q; got:\n%s", p, help)
		}
	}
}

// ---- scenario (n): portedCommands entry -------------------------------------

func TestCompactParity_PortedCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "compact" {
			return
		}
	}
	t.Error("expected 'compact' in portedCommands; not found")
}
