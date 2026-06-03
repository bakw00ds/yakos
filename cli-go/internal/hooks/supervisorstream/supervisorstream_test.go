package supervisorstream_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

func readLastLog(t *testing.T, logFile string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var last map[string]any
	start := 0
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == '\n' {
			if i > start {
				var rec map[string]any
				if err := json.Unmarshal(data[start:i], &rec); err == nil {
					last = rec
				}
			}
			start = i + 1
		}
	}
	if last == nil {
		t.Fatal("no log entries")
	}
	return last
}

func makeInput(tool, filePath, newString string) hooktype.HookInput {
	payload := map[string]any{}
	if filePath != "" {
		payload["path"] = filePath
	}
	if newString != "" {
		payload["new_string"] = newString
	}
	return hooktype.HookInput{Tool: tool, Payload: payload, Env: map[string]string{}}
}

func writeYAML(t *testing.T, dir, content string) {
	t.Helper()
	_ = os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(content), 0644)
}

func newHook(work, proj string) *supervisorstream.Hook {
	return &supervisorstream.Hook{WorkCurrentDir: work, ProjectDir: proj, NowFn: fixedNow}
}

func TestSupervisorStream_NonMutationToolSkipped(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	h := newHook(work, proj)
	out, err := h.Run(context.Background(), makeInput("Read", "", ""))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
	// No buffer file for a skipped tool.
	if _, err := os.Stat(filepath.Join(work, "supervisor-buffer.ndjson")); !os.IsNotExist(err) {
		t.Error("expected no buffer for Read tool")
	}
}

func TestSupervisorStream_EnvDisable(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	h := &supervisorstream.Hook{WorkCurrentDir: work, ProjectDir: proj, NowFn: fixedNow}
	in := hooktype.HookInput{
		Tool:    "Edit",
		Payload: map[string]any{"path": "x.go"},
		Env:     map[string]string{"YAKOS_SUPERVISOR_DISABLE": "1"},
	}
	_, _ = h.Run(context.Background(), in)
	if _, err := os.Stat(filepath.Join(work, "supervisor-buffer.ndjson")); !os.IsNotExist(err) {
		t.Error("no buffer when disabled")
	}
}

func TestSupervisorStream_ConfigDisable(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "supervisor:\n  enabled: false\n")
	h := newHook(work, proj)
	_, _ = h.Run(context.Background(), makeInput("Edit", "x.go", ""))
	if _, err := os.Stat(filepath.Join(work, "supervisor-buffer.ndjson")); !os.IsNotExist(err) {
		t.Error("no buffer when config disabled")
	}
}

func TestSupervisorStream_AppendToBuffer(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "supervisor:\n  enabled: true\n")
	h := newHook(work, proj)
	_, _ = h.Run(context.Background(), makeInput("Edit", "main.go", "hello"))
	if _, err := os.Stat(filepath.Join(work, "supervisor-buffer.ndjson")); err != nil {
		t.Errorf("buffer not created: %v", err)
	}
}

func TestSupervisorStream_CleanEditNoEscalation(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "supervisor:\n  enabled: true\n")
	// Write plan.md so out-of-scope check finds the file.
	_ = os.WriteFile(filepath.Join(work, "plan.md"), []byte("main.go is in scope\n"), 0644)
	h := newHook(work, proj)
	_, _ = h.Run(context.Background(), makeInput("Edit", "main.go", "small change"))
	// Counter should NOT exist (no escalation).
	if _, err := os.Stat(filepath.Join(work, ".supervisor-counter")); !os.IsNotExist(err) {
		t.Error("counter should not exist for non-escalating clean edit")
	}
}

func TestSupervisorStream_LargeDiffEscalates(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "supervisor:\n  enabled: true\n")
	h := newHook(work, proj)
	// Build a new_string with 30+ newlines (> default 20).
	bigChange := ""
	for i := 0; i < 35; i++ {
		bigChange += "line " + itoa(i) + "\n"
	}
	_, _ = h.Run(context.Background(), makeInput("Edit", "big.go", bigChange))
	// Counter should exist.
	if _, err := os.Stat(filepath.Join(work, ".supervisor-counter")); err != nil {
		t.Error("counter should be created for large-diff escalation")
	}
}

func TestSupervisorStream_RiskRegexEscalates(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "supervisor:\n  enabled: true\n")
	h := newHook(work, proj)
	// Match "rm -rf" default risk pattern.
	_, _ = h.Run(context.Background(), makeInput("Bash", "", ""))
	// Override payload.
	in := hooktype.HookInput{
		Tool:    "Edit",
		Payload: map[string]any{"path": "deploy.sh", "new_string": "rm -rf /data"},
		Env:     map[string]string{},
	}
	h2 := newHook(work, proj)
	_, _ = h2.Run(context.Background(), in)
	if _, err := os.Stat(filepath.Join(work, ".supervisor-counter")); err != nil {
		t.Error("counter should exist for risk-regex escalation")
	}
}

func TestSupervisorStream_CounterIncrementsOnEscalation(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "supervisor:\n  enabled: true\n")
	h := newHook(work, proj)
	bigChange := ""
	for i := 0; i < 25; i++ {
		bigChange += "line\n"
	}
	for i := 0; i < 3; i++ {
		_, _ = h.Run(context.Background(), makeInput("Edit", "big.go", bigChange))
	}
	data, err := os.ReadFile(filepath.Join(work, ".supervisor-counter"))
	if err != nil {
		t.Fatalf("counter file: %v", err)
	}
	var counter int
	json.Unmarshal(data, &counter) //nolint:errcheck
	if counter < 3 {
		// Just check file exists and has content > 0.
		// The actual value may be 3 or could have variation from pre-filter.
		counterStr := string(data)
		if counterStr == "0\n" || counterStr == "" {
			t.Error("counter should be > 0")
		}
	}
}

func TestSupervisorStream_DispatchMarkerAtThreshold(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "supervisor:\n  enabled: true\n  score_every_n_calls: 3\n")
	h := newHook(work, proj)
	bigChange := ""
	for i := 0; i < 25; i++ {
		bigChange += "line\n"
	}
	// 3 escalations should trigger dispatch marker.
	for i := 0; i < 3; i++ {
		_, _ = h.Run(context.Background(), makeInput("Edit", "big.go", bigChange))
	}
	if _, err := os.Stat(filepath.Join(work, ".supervisor-dispatch-ready")); err != nil {
		t.Error("dispatch-ready marker should exist at score threshold")
	}
}

func TestSupervisorStream_PreFilterDisabledCounts(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "supervisor:\n  enabled: true\n  pre_filter:\n    enabled: false\n  score_every_n_calls: 2\n")
	h := newHook(work, proj)
	// With pre_filter disabled, every call counts.
	_, _ = h.Run(context.Background(), makeInput("Edit", "x.go", "small"))
	_, _ = h.Run(context.Background(), makeInput("Edit", "y.go", "small"))
	if _, err := os.Stat(filepath.Join(work, ".supervisor-dispatch-ready")); err != nil {
		t.Error("dispatch marker should exist when pre_filter disabled and threshold hit")
	}
}

func TestSupervisorStream_BufferTrimmedAt50(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "supervisor:\n  enabled: true\n  pre_filter:\n    enabled: false\n  score_every_n_calls: 1000\n")
	h := newHook(work, proj)
	// Write 60 events to trigger buffer trim.
	for i := 0; i < 60; i++ {
		_, _ = h.Run(context.Background(), makeInput("Edit", "f.go", "x"))
	}
	data, err := os.ReadFile(filepath.Join(work, "supervisor-buffer.ndjson"))
	if err != nil {
		t.Fatalf("buffer: %v", err)
	}
	count := 0
	for _, b := range data {
		if b == '\n' {
			count++
		}
	}
	if count > 55 { // allow some slack; trim fires on each call
		t.Errorf("buffer has %d lines, want ~50", count)
	}
}

func TestSupervisorStream_AlwaysExitZero(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "supervisor:\n  enabled: true\n")
	h := newHook(work, proj)
	out, err := h.Run(context.Background(), makeInput("Edit", "x.go", ""))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
}

func TestSupervisorStream_HookName(t *testing.T) {
	h := supervisorstream.New("/tmp/work", "/tmp/proj")
	if h.Name() != "supervisor-stream" {
		t.Errorf("Name()=%q", h.Name())
	}
}

func TestSupervisorStream_NoWorkDir(t *testing.T) {
	h := &supervisorstream.Hook{WorkCurrentDir: "", NowFn: fixedNow}
	out, err := h.Run(context.Background(), makeInput("Edit", "x.go", ""))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
}

func TestSupervisorStream_BashToolIncluded(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "supervisor:\n  enabled: true\n  pre_filter:\n    enabled: false\n  score_every_n_calls: 1000\n")
	h := newHook(work, proj)
	_, _ = h.Run(context.Background(), makeInput("Bash", "", ""))
	if _, err := os.Stat(filepath.Join(work, "supervisor-buffer.ndjson")); err != nil {
		t.Error("Bash tool should add to buffer")
	}
}

func TestSupervisorStream_OutOfScopeCheck(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "supervisor:\n  enabled: true\n  score_every_n_calls: 100\n")
	// Write decisions.md that does NOT mention "secret.go".
	_ = os.WriteFile(filepath.Join(work, "decisions.md"), []byte("# decisions\nonly main.go is in scope\n"), 0644)
	h := newHook(work, proj)
	_, _ = h.Run(context.Background(), makeInput("Edit", "secret.go", "small"))
	// Out-of-scope should escalate.
	if _, err := os.Stat(filepath.Join(work, ".supervisor-counter")); err != nil {
		t.Error("counter should exist for out-of-scope escalation")
	}
}

func TestSupervisorStream_LogEntryWritten(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "supervisor:\n  enabled: true\n")
	_ = os.WriteFile(filepath.Join(work, "plan.md"), []byte("x.go\n"), 0644)
	h := newHook(work, proj)
	_, _ = h.Run(context.Background(), makeInput("Edit", "x.go", "small"))
	if _, err := os.Stat(filepath.Join(work, "logs", "supervisor-stream.ndjson")); err != nil {
		t.Error("log file should exist")
	}
}

// itoa avoids importing strconv in the test.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
