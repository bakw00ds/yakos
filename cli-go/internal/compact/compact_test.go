package compact

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- helpers ----------------------------------------------------------------

func newTestCfg(t *testing.T, sub string) Config {
	t.Helper()
	return Config{
		Subcommand: sub,
		HomeDir:    t.TempDir(),
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
		Now:        func() time.Time { return time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC) },
	}
}

func out(cfg Config) string { return cfg.Writer.(*bytes.Buffer).String() }

// ---- compact now ------------------------------------------------------------

// TestNow_PrintsSlashCommandAdvice verifies that `compact now` prints the
// advisory with /compact for Claude Code.
func TestNow_PrintsSlashCommandAdvice(t *testing.T) {
	cfg := newTestCfg(t, "now")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Subcommand != "now" {
		t.Errorf("expected Subcommand='now'; got %q", res.Subcommand)
	}
	o := out(cfg)
	for _, phrase := range []string{"/compact", "Claude Code", "M3.1", "checkpoint"} {
		if !strings.Contains(o, phrase) {
			t.Errorf("output missing phrase %q; got:\n%s", phrase, o)
		}
	}
}

// TestNow_AppendsLogEntry verifies that `compact now` appends a log entry to
// compact-log.ndjson with the expected fields.
func TestNow_AppendsLogEntry(t *testing.T) {
	cfg := newTestCfg(t, "now")
	cfg.Runtime = "claude"
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.LogEntry == "" {
		t.Fatal("expected non-empty LogEntry")
	}

	var rec logRecord
	if err := json.Unmarshal([]byte(res.LogEntry), &rec); err != nil {
		t.Fatalf("parse LogEntry: %v", err)
	}
	if rec.Kind != "manual-recommend" {
		t.Errorf("expected Kind='manual-recommend'; got %q", rec.Kind)
	}
	if rec.Runtime != "claude" {
		t.Errorf("expected Runtime='claude'; got %q", rec.Runtime)
	}
	if rec.TS == "" {
		t.Errorf("expected non-empty TS")
	}

	// Confirm file on disk.
	logPath := filepath.Join(cfg.HomeDir, ".yakos-state", "compact-log.ndjson")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "manual-recommend") {
		t.Errorf("log file does not contain 'manual-recommend'; got:\n%s", string(data))
	}
}

// TestNow_RuntimeDefaultsToEnv verifies that YAKOS_RUNTIME env var is used
// when cfg.Runtime is empty.
func TestNow_RuntimeDefaultsToEnv(t *testing.T) {
	cfg := newTestCfg(t, "now")
	t.Setenv("YAKOS_RUNTIME", "codex")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.LogEntry, "codex") {
		t.Errorf("expected 'codex' in LogEntry; got %q", res.LogEntry)
	}
}

// TestNow_RuntimeDefaultsToClaudeWhenEnvUnset verifies fallback to "claude".
func TestNow_RuntimeDefaultsToClaudeWhenEnvUnset(t *testing.T) {
	cfg := newTestCfg(t, "now")
	// Explicitly unset the env var for this test.
	t.Setenv("YAKOS_RUNTIME", "")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.LogEntry, `"claude"`) {
		t.Errorf("expected 'claude' in LogEntry; got %q", res.LogEntry)
	}
}

// ---- compact threshold (show) -----------------------------------------------

// TestThreshold_ShowDefaults verifies that when settings.json is absent, the
// default thresholds (75/90) are shown.
func TestThreshold_ShowDefaults(t *testing.T) {
	cfg := newTestCfg(t, "threshold")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.NoticeThreshold != 75 {
		t.Errorf("expected NoticeThreshold=75; got %d", res.NoticeThreshold)
	}
	if res.WarningThreshold != 90 {
		t.Errorf("expected WarningThreshold=90; got %d", res.WarningThreshold)
	}
	o := out(cfg)
	if !strings.Contains(o, "notice = 75%") {
		t.Errorf("expected 'notice = 75%%' in output; got: %q", o)
	}
	if !strings.Contains(o, "warning = 90%") {
		t.Errorf("expected 'warning = 90%%' in output; got: %q", o)
	}
}

// TestThreshold_ShowFromSettings verifies that thresholds stored in
// settings.json are read back correctly.
func TestThreshold_ShowFromSettings(t *testing.T) {
	cfg := newTestCfg(t, "threshold")
	settingsPath := filepath.Join(cfg.HomeDir, ".yakos-state", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := `{"context_thresholds": {"notice": 60, "warning": 85}}`
	if err := os.WriteFile(settingsPath, []byte(content), 0644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	res, err := Run(cfg)
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

// ---- compact threshold (set) ------------------------------------------------

// TestThreshold_SetWritesSettings verifies that `compact threshold N` writes
// the notice threshold to settings.json atomically.
func TestThreshold_SetWritesSettings(t *testing.T) {
	cfg := newTestCfg(t, "threshold")
	cfg.ThresholdArg = "80"
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.ThresholdSet {
		t.Errorf("expected ThresholdSet=true")
	}
	if res.NoticeThreshold != 80 {
		t.Errorf("expected NoticeThreshold=80; got %d", res.NoticeThreshold)
	}

	// Confirm settings.json on disk.
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
	if thresholds == nil {
		t.Fatalf("context_thresholds missing from settings")
	}
	if int(thresholds["notice"].(float64)) != 80 {
		t.Errorf("expected notice=80 in settings; got %v", thresholds["notice"])
	}
}

// TestThreshold_SetPreservesExistingKeys verifies that setting the threshold
// preserves other keys already present in settings.json.
func TestThreshold_SetPreservesExistingKeys(t *testing.T) {
	cfg := newTestCfg(t, "threshold")
	settingsPath := filepath.Join(cfg.HomeDir, ".yakos-state", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := `{"some_other_key": "value", "context_thresholds": {"notice": 75}}`
	if err := os.WriteFile(settingsPath, []byte(content), 0644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	cfg.ThresholdArg = "65"
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if !strings.Contains(string(data), "some_other_key") {
		t.Errorf("expected 'some_other_key' preserved; settings:\n%s", string(data))
	}
}

// TestThreshold_SetInvalidNonNumeric verifies that a non-numeric arg errors.
func TestThreshold_SetInvalidNonNumeric(t *testing.T) {
	cfg := newTestCfg(t, "threshold")
	cfg.ThresholdArg = "abc"
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for non-numeric threshold")
	}
	if !strings.Contains(err.Error(), "number") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestThreshold_SetInvalidOutOfRange verifies that 0 and 100 are rejected.
func TestThreshold_SetInvalidOutOfRange(t *testing.T) {
	for _, bad := range []string{"0", "100"} {
		cfg := newTestCfg(t, "threshold")
		cfg.ThresholdArg = bad
		_, err := Run(cfg)
		if err == nil {
			t.Errorf("expected error for threshold=%s", bad)
		}
		if !strings.Contains(err.Error(), "1-99") {
			t.Errorf("expected '1-99' hint; got: %v", err)
		}
	}
}

// TestThreshold_SetEdgeCases verifies the boundary values 1 and 99 are accepted.
func TestThreshold_SetEdgeCases(t *testing.T) {
	for _, good := range []string{"1", "99"} {
		cfg := newTestCfg(t, "threshold")
		cfg.ThresholdArg = good
		_, err := Run(cfg)
		if err != nil {
			t.Errorf("expected no error for threshold=%s; got: %v", good, err)
		}
	}
}

// TestThreshold_OutputContainsSetMessage verifies the confirmation message is
// printed when a threshold is set.
func TestThreshold_OutputContainsSetMessage(t *testing.T) {
	cfg := newTestCfg(t, "threshold")
	cfg.ThresholdArg = "55"
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := out(cfg)
	if !strings.Contains(o, "55") {
		t.Errorf("expected '55' in output; got: %q", o)
	}
}

// ---- compact history --------------------------------------------------------

// TestHistory_EmptyWhenNoLog verifies that history prints the advisory when the
// log file does not exist.
func TestHistory_EmptyWhenNoLog(t *testing.T) {
	cfg := newTestCfg(t, "history")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := out(cfg)
	if !strings.Contains(o, "no compaction history") {
		t.Errorf("expected advisory; got: %q", o)
	}
	if len(res.HistoryLines) != 0 {
		t.Errorf("expected empty HistoryLines; got %v", res.HistoryLines)
	}
}

// TestHistory_ShowsEntries verifies that history formats and prints lines from
// the compact-log.ndjson.
func TestHistory_ShowsEntries(t *testing.T) {
	cfg := newTestCfg(t, "history")
	logPath := filepath.Join(cfg.HomeDir, ".yakos-state", "compact-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	entries := []string{
		`{"ts":"2026-06-01T10:00:00Z","kind":"manual-recommend","pct":"","runtime":"claude","by_user":"alice"}`,
		`{"ts":"2026-06-02T08:00:00Z","kind":"auto","pct":"82","runtime":"codex","by_user":"bob"}`,
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(entries, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.HistoryLines) != 2 {
		t.Fatalf("expected 2 history lines; got %d", len(res.HistoryLines))
	}
	o := out(cfg)
	if !strings.Contains(o, "manual-recommend") {
		t.Errorf("expected 'manual-recommend' in output; got: %q", o)
	}
	if !strings.Contains(o, "codex") {
		t.Errorf("expected 'codex' in output; got: %q", o)
	}
	// Pct "-" for empty pct, "82" for the second entry.
	if !strings.Contains(o, " - ") {
		t.Errorf("expected ' - ' for empty pct; got: %q", o)
	}
	if !strings.Contains(o, "82") {
		t.Errorf("expected '82' in output; got: %q", o)
	}
}

// TestHistory_CapsAt50Lines verifies that only the last 50 entries are shown
// when the log contains more.
func TestHistory_CapsAt50Lines(t *testing.T) {
	cfg := newTestCfg(t, "history")
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

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.HistoryLines) != 50 {
		t.Errorf("expected 50 history lines; got %d", len(res.HistoryLines))
	}
}

// ---- help -------------------------------------------------------------------

// TestHelp_PrintsKeyPhrases verifies that the help text contains expected phrases.
func TestHelp_PrintsKeyPhrases(t *testing.T) {
	var buf bytes.Buffer
	PrintHelp(&buf)
	help := buf.String()
	for _, phrase := range []string{
		"yakos compact",
		"now",
		"threshold",
		"history",
		"/compact",
		"M3.1",
	} {
		if !strings.Contains(help, phrase) {
			t.Errorf("help missing phrase %q; got:\n%s", phrase, help)
		}
	}
}

// TestHelp_Subcommand verifies that calling Run with subcommand "help" returns
// no error.
func TestHelp_Subcommand(t *testing.T) {
	cfg := newTestCfg(t, "help")
	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run help: %v", err)
	}
	o := out(cfg)
	if !strings.Contains(o, "yakos compact") {
		t.Errorf("expected 'yakos compact' in help output; got: %q", o)
	}
}

// ---- unknown subcommand -----------------------------------------------------

// TestUnknown_ReturnsError verifies that an unrecognised subcommand returns an
// error with a hint.
func TestUnknown_ReturnsError(t *testing.T) {
	cfg := newTestCfg(t, "bogus")
	_, err := Run(cfg)
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

// ---- atomicity of settings.json write ---------------------------------------

// TestThreshold_AtomicWrite_NoTmpOnDisk verifies that no .tmp file lingers
// after a successful write.
func TestThreshold_AtomicWrite_NoTmpOnDisk(t *testing.T) {
	cfg := newTestCfg(t, "threshold")
	cfg.ThresholdArg = "70"
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	settingsPath := filepath.Join(cfg.HomeDir, ".yakos-state", "settings.json")
	tmpPath := settingsPath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("expected .tmp file to be gone after atomic rename; got: %v", err)
	}
}

// ---- log record timestamp format --------------------------------------------

// TestNow_LogTimestampIsRFC3339 verifies the ts field is a valid RFC3339 string.
func TestNow_LogTimestampIsRFC3339(t *testing.T) {
	cfg := newTestCfg(t, "now")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var rec logRecord
	if err := json.Unmarshal([]byte(res.LogEntry), &rec); err != nil {
		t.Fatalf("parse LogEntry: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, rec.TS); err != nil {
		t.Errorf("TS %q is not RFC3339: %v", rec.TS, err)
	}
}
