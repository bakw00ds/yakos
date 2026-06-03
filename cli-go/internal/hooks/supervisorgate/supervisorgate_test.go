package supervisorgate_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/supervisorgate"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

func newHook(workDir, projectDir string) *supervisorgate.Hook {
	return &supervisorgate.Hook{
		WorkCurrentDir: workDir,
		ProjectDir:     projectDir,
		NowFn:          fixedNow,
	}
}

func makeInput(tool string, env map[string]string) hooktype.HookInput {
	if env == nil {
		env = map[string]string{}
	}
	return hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    tool,
		Payload: map[string]any{},
		Env:     env,
	}
}

func writeFinding(t *testing.T, findingsFile string, ts, overall, rationale, recommended string) {
	t.Helper()
	rec := map[string]any{
		"ts":                 ts,
		"overall":            overall,
		"rationale":          rationale,
		"recommended_action": recommended,
	}
	data, _ := json.Marshal(rec)
	f, err := os.OpenFile(findingsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open findings: %v", err)
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(data, '\n'))
}

func writeYAML(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(content), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
}

// TestEmergencyBypass passes when YAKOS_SUPERVISOR_DISABLE=1.
func TestEmergencyBypass(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, "2026-01-15T10:00:00Z", "CRITICAL", "bad", "halt")
	h := newHook(tmp, tmp)
	in := makeInput("TeamCreate", map[string]string{
		"YAKOS_SUPERVISOR_DISABLE": "1",
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("emergency bypass should pass, got %d", out.ExitCode)
	}
}

// TestNoFindingsFilePasses passes when no findings file exists.
func TestNoFindingsFilePasses(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp, tmp)
	in := makeInput("TeamCreate", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("no findings file should pass, got %d", out.ExitCode)
	}
}

// TestPassFindingPasses confirms PASS overall exits 0.
func TestPassFindingPasses(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, "2026-01-15T10:00:00Z", "PASS", "all good", "continue")
	h := newHook(tmp, tmp)
	out, err := h.Run(context.Background(), in(t, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("PASS finding should pass, got %d", out.ExitCode)
	}
}

// TestWarnFindingSurfaces confirms WARN overall surfaces to Stderr but doesn't block.
func TestWarnFindingSurfaces(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, "2026-01-15T09:00:00Z", "WARN", "review note", "surface_to_operator")
	h := newHook(tmp, tmp)
	out, err := h.Run(context.Background(), in(t, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("WARN should not block, got %d", out.ExitCode)
	}
	if !strings.Contains(string(out.Stderr), "WARN") {
		t.Fatalf("expected WARN in stderr, got: %s", out.Stderr)
	}
	if !strings.Contains(string(out.Stderr), "review note") {
		t.Fatalf("expected rationale in stderr, got: %s", out.Stderr)
	}
}

// TestCriticalBlocks confirms CRITICAL with block_on_critical=true blocks.
func TestCriticalBlocks(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, "2026-01-15T10:00:00Z", "CRITICAL", "major issue", "halt")
	h := newHook(tmp, tmp)
	out, err := h.Run(context.Background(), in(t, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 2 {
		t.Fatalf("CRITICAL should block (exit 2), got %d", out.ExitCode)
	}
	if !strings.Contains(string(out.Stderr), "CRITICAL") {
		t.Fatalf("expected CRITICAL in block message, got: %s", out.Stderr)
	}
}

// TestCriticalPassiveMode surfaces but doesn't block when block_on_critical=false.
func TestCriticalPassiveMode(t *testing.T) {
	tmp := t.TempDir()
	writeYAML(t, tmp, "supervisor:\n  block_on_critical: false\n")
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, "2026-01-15T10:00:00Z", "CRITICAL", "passive issue", "halt")
	h := newHook(tmp, tmp)
	out, err := h.Run(context.Background(), in(t, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("passive mode should not block, got %d", out.ExitCode)
	}
	if !strings.Contains(string(out.Stderr), "passive mode") {
		t.Fatalf("expected passive mode message, got: %s", out.Stderr)
	}
}

// TestCriticalBypassedPasses when bypass file contains the finding scope.
func TestCriticalBypassedPasses(t *testing.T) {
	tmp := t.TempDir()
	ts := "2026-01-15T10:00:00Z"
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, ts, "CRITICAL", "issue", "halt")
	// Write bypass file.
	bypassContent := "## bypass:supervisor-override-test\n**Hook:** supervisor\n**Scope:** finding=" + ts + "\n"
	if err := os.WriteFile(filepath.Join(tmp, "hook-bypass.md"), []byte(bypassContent), 0644); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, tmp)
	out, err := h.Run(context.Background(), in(t, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("bypassed CRITICAL should pass, got %d", out.ExitCode)
	}
}

// TestSupervisorDisabledByYAML passes when supervisor.enabled: false.
func TestSupervisorDisabledByYAML(t *testing.T) {
	tmp := t.TempDir()
	writeYAML(t, tmp, "supervisor:\n  enabled: false\n")
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, "2026-01-15T10:00:00Z", "CRITICAL", "bad", "halt")
	h := newHook(tmp, tmp)
	out, err := h.Run(context.Background(), in(t, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("disabled supervisor should pass, got %d", out.ExitCode)
	}
}

// TestIdempotencyWarnNotResurfaced confirms WARN is not re-emitted for same ts.
func TestIdempotencyWarnNotResurfaced(t *testing.T) {
	tmp := t.TempDir()
	ts := "2026-01-15T09:00:00Z"
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, ts, "WARN", "warn note", "continue")
	h := newHook(tmp, tmp)

	// First run: should surface.
	out1, _ := h.Run(context.Background(), in(t, nil))
	if !strings.Contains(string(out1.Stderr), "warn note") {
		t.Fatalf("first run should surface WARN, got: %s", out1.Stderr)
	}

	// Second run: same ts, should NOT re-surface.
	out2, _ := h.Run(context.Background(), in(t, nil))
	if strings.Contains(string(out2.Stderr), "warn note") {
		t.Fatalf("second run should not re-surface same WARN, got: %s", out2.Stderr)
	}
}

// TestLastFindingUsed confirms only the last line is checked.
func TestLastFindingUsed(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, "2026-01-15T08:00:00Z", "CRITICAL", "earlier critical", "halt")
	writeFinding(t, findingsFile, "2026-01-15T09:00:00Z", "PASS", "later pass", "continue")
	h := newHook(tmp, tmp)
	out, err := h.Run(context.Background(), in(t, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("last finding is PASS; should not block, got %d", out.ExitCode)
	}
}

// TestUnknownOverallPasses passes on unknown overall value.
func TestUnknownOverallPasses(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, "2026-01-15T10:00:00Z", "UNKNOWN_LEVEL", "weird", "continue")
	h := newHook(tmp, tmp)
	out, err := h.Run(context.Background(), in(t, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("unknown overall should pass, got %d", out.ExitCode)
	}
}

// TestBlockMessageContainsHints confirms the block message has bypass instructions.
func TestBlockMessageContainsHints(t *testing.T) {
	tmp := t.TempDir()
	ts := "2026-01-15T10:00:00Z"
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, ts, "CRITICAL", "the issue", "halt")
	h := newHook(tmp, tmp)
	out, _ := h.Run(context.Background(), in(t, nil))
	stderr := string(out.Stderr)
	// The block message should contain bypass instructions (scope format).
	if !strings.Contains(stderr, "bypass") {
		t.Fatalf("expected bypass hint in block message, got: %s", stderr)
	}
	if !strings.Contains(stderr, "YAKOS_SUPERVISOR_DISABLE=1") {
		t.Fatalf("expected emergency bypass hint, got: %s", stderr)
	}
	if !strings.Contains(stderr, ts) {
		t.Fatalf("expected finding ts in block message, got: %s", stderr)
	}
	// Confirm bypass scope format is present (markdown bold: **Scope:** finding=<ts>).
	if !strings.Contains(stderr, "finding="+ts) {
		t.Fatalf("expected bypass scope in block message, got: %s", stderr)
	}
}

// TestEmptyWorkCurrentDirNoOp returns cleanly with empty WorkCurrentDir.
func TestEmptyWorkCurrentDirNoOp(t *testing.T) {
	h := &supervisorgate.Hook{
		WorkCurrentDir: "",
		ProjectDir:     t.TempDir(),
		NowFn:          fixedNow,
	}
	out, err := h.Run(context.Background(), in(t, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("empty workdir should pass, got %d", out.ExitCode)
	}
}

func in(t *testing.T, env map[string]string) hooktype.HookInput {
	t.Helper()
	if env == nil {
		env = map[string]string{}
	}
	return hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    "TeamCreate",
		Payload: map[string]any{},
		Env:     env,
	}
}
