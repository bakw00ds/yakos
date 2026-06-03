package supervisorackgate_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/supervisorackgate"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

func newHook(workDir, projectDir, acksFile string) *supervisorackgate.Hook {
	return &supervisorackgate.Hook{
		WorkCurrentDir: workDir,
		ProjectDir:     projectDir,
		AcksFile:       acksFile,
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

func writeFinding(t *testing.T, findingsFile string, ts, action, rationale string) {
	t.Helper()
	rec := map[string]any{
		"ts":                 ts,
		"overall":            "CRITICAL",
		"recommended_action": action,
		"rationale":          rationale,
	}
	data, _ := json.Marshal(rec)
	f, err := os.OpenFile(findingsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open findings: %v", err)
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(data, '\n'))
}

func writeAck(t *testing.T, acksFile, project, findingID string) {
	t.Helper()
	rec := map[string]any{"project": project, "finding_id": findingID}
	data, _ := json.Marshal(rec)
	f, err := os.OpenFile(acksFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open acks: %v", err)
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(data, '\n'))
}

// TestNonDispatchToolIgnored confirms Edit/Write are ignored.
func TestNonDispatchToolIgnored(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp, tmp, "")
	for _, tool := range []string{"Edit", "Write", "Bash", "Read"} {
		in := makeInput(tool, nil)
		out, err := h.Run(context.Background(), in)
		if err != nil {
			t.Fatalf("tool=%s: unexpected error: %v", tool, err)
		}
		if out.ExitCode != 0 {
			t.Fatalf("tool=%s: expected pass, got %d", tool, out.ExitCode)
		}
	}
}

// TestEmergencyBypass passes when YAKOS_SUPERVISOR_DISABLE=1.
func TestEmergencyBypass(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, "2026-01-15T10:00:00Z", "surface_to_operator", "bad")
	h := newHook(tmp, tmp, "")
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

// TestNoFindingsFilePasses passes when the findings file doesn't exist.
func TestNoFindingsFilePasses(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp, tmp, "")
	in := makeInput("TeamCreate", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("no findings file should pass, got %d", out.ExitCode)
	}
}

// TestContinueActionPasses confirms "continue" findings don't gate.
func TestContinueActionPasses(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, "2026-01-15T10:00:00Z", "continue", "minor note")
	h := newHook(tmp, tmp, "")
	in := makeInput("TeamCreate", map[string]string{
		"YAKOS_PROJECT_NAME": "myproject",
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("continue action should pass, got %d", out.ExitCode)
	}
}

// TestSurfaceToOperatorBlocks confirms surface_to_operator finding blocks.
func TestSurfaceToOperatorBlocks(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, "2026-01-15T10:00:00Z", "surface_to_operator", "review needed")
	acksFile := filepath.Join(tmp, "acks.ndjson")
	h := newHook(tmp, tmp, acksFile)
	in := makeInput("TeamCreate", map[string]string{
		"YAKOS_PROJECT_NAME": "myproject",
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 2 {
		t.Fatalf("expected block (exit 2), got %d", out.ExitCode)
	}
}

// TestBlockNextToolBlocks confirms block_next_tool action gates.
func TestBlockNextToolBlocks(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, "2026-01-15T09:00:00Z", "block_next_tool", "halt requested")
	acksFile := filepath.Join(tmp, "acks.ndjson")
	h := newHook(tmp, tmp, acksFile)
	in := makeInput("Agent", map[string]string{
		"YAKOS_PROJECT_NAME": "myproject",
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 2 {
		t.Fatalf("expected block, got %d", out.ExitCode)
	}
}

// TestHaltActionBlocks confirms halt action gates.
func TestHaltActionBlocks(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, "2026-01-15T08:00:00Z", "halt", "critical issue")
	acksFile := filepath.Join(tmp, "acks.ndjson")
	h := newHook(tmp, tmp, acksFile)
	in := makeInput("TeamCreate", map[string]string{
		"YAKOS_PROJECT_NAME": "proj",
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 2 {
		t.Fatalf("expected block for halt, got %d", out.ExitCode)
	}
}

// TestAckedFindingPasses confirms acked findings don't block.
func TestAckedFindingPasses(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	ts := "2026-01-15T10:00:00Z"
	writeFinding(t, findingsFile, ts, "surface_to_operator", "needs review")
	acksFile := filepath.Join(tmp, "acks.ndjson")
	// Derive the expected finding ID.
	safeTS := strings.ReplaceAll(ts, ":", "-")
	findingID := fmt.Sprintf("f-%s-myproject-1", safeTS)
	writeAck(t, acksFile, "myproject", findingID)
	h := newHook(tmp, tmp, acksFile)
	in := makeInput("TeamCreate", map[string]string{
		"YAKOS_PROJECT_NAME": "myproject",
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("acked finding should pass, got %d", out.ExitCode)
	}
}

// TestPartialAckBlocks when only some findings are acked.
func TestPartialAckBlocks(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	ts1 := "2026-01-15T08:00:00Z"
	ts2 := "2026-01-15T09:00:00Z"
	writeFinding(t, findingsFile, ts1, "surface_to_operator", "first finding")
	writeFinding(t, findingsFile, ts2, "surface_to_operator", "second finding")
	acksFile := filepath.Join(tmp, "acks.ndjson")
	// Ack only the first finding.
	safeTS1 := strings.ReplaceAll(ts1, ":", "-")
	writeAck(t, acksFile, "proj", fmt.Sprintf("f-%s-proj-1", safeTS1))
	h := newHook(tmp, tmp, acksFile)
	in := makeInput("TeamCreate", map[string]string{
		"YAKOS_PROJECT_NAME": "proj",
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 2 {
		t.Fatalf("partial ack should still block, got %d", out.ExitCode)
	}
}

// TestPerProjectOptOut passes when gate_on_escalation: false.
func TestPerProjectOptOut(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, ".yakos.yml"),
		[]byte("supervise:\n  gate_on_escalation: false\n"), 0644); err != nil {
		t.Fatal(err)
	}
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, findingsFile, "2026-01-15T10:00:00Z", "surface_to_operator", "bad")
	h := newHook(tmp, tmp, "")
	in := makeInput("TeamCreate", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("opt-out should pass, got %d", out.ExitCode)
	}
}

// TestBlockMessageContainsFindingID confirms finding IDs appear in block message.
func TestBlockMessageContainsFindingID(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	ts := "2026-01-15T10:00:00Z"
	writeFinding(t, findingsFile, ts, "halt", "critical failure")
	acksFile := filepath.Join(tmp, "acks.ndjson")
	h := newHook(tmp, tmp, acksFile)
	in := makeInput("TeamCreate", map[string]string{
		"YAKOS_PROJECT_NAME": "testproj",
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out.Stderr), "f-2026-01-15T10") {
		t.Fatalf("expected finding ID in block message, got: %s", out.Stderr)
	}
	if !strings.Contains(string(out.Stderr), "yakos supervise ack") {
		t.Fatalf("expected ack hint in block message, got: %s", out.Stderr)
	}
}

// TestMalformedJSONLineSkipped confirms bad JSON lines are skipped.
func TestMalformedJSONLineSkipped(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	_ = os.WriteFile(findingsFile, []byte("not json at all\n"), 0644)
	h := newHook(tmp, tmp, "")
	in := makeInput("TeamCreate", map[string]string{
		"YAKOS_PROJECT_NAME": "proj",
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Malformed line → no pending findings → pass.
	if out.ExitCode != 0 {
		t.Fatalf("malformed JSON should be skipped, got %d", out.ExitCode)
	}
}

// TestAckForWrongProjectDoesNotCount confirms project-scoped acks.
func TestAckForWrongProjectDoesNotCount(t *testing.T) {
	tmp := t.TempDir()
	findingsFile := filepath.Join(tmp, "supervisor-findings.ndjson")
	ts := "2026-01-15T10:00:00Z"
	writeFinding(t, findingsFile, ts, "surface_to_operator", "finding")
	acksFile := filepath.Join(tmp, "acks.ndjson")
	// Ack for a DIFFERENT project.
	safeTS := strings.ReplaceAll(ts, ":", "-")
	writeAck(t, acksFile, "other-project", fmt.Sprintf("f-%s-proj-1", safeTS))
	h := newHook(tmp, tmp, acksFile)
	in := makeInput("TeamCreate", map[string]string{
		"YAKOS_PROJECT_NAME": "proj",
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 2 {
		t.Fatalf("ack for wrong project should still block, got %d", out.ExitCode)
	}
}
