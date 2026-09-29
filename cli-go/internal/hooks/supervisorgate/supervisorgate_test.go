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
	bypassContent := "# Active hook bypasses\n\n## Active entries\n\n## bypass:supervisor-override-test\n\n**Hook:** supervisor\n**Scope:** finding=" + ts + "\n"
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

// ---- K-87 A-2b: bash parity ---------------------------------------------------

func lastLog(t *testing.T, work string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(work, "logs", "supervisor-gate.ndjson"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestLogRecordShapeMatchesBash(t *testing.T) {
	tmp := t.TempDir()
	writeFinding(t, filepath.Join(tmp, "supervisor-findings.ndjson"), "2026-01-15T10:00:00Z", "CRITICAL", "r", "halt")
	h := newHook(tmp, tmp)
	i := in(t, nil)
	i.Payload = map[string]any{"session_id": "s-1", "agent_type": "yakos:backend"}
	out, _ := h.Run(context.Background(), i)
	if out.ExitCode != 2 {
		t.Fatalf("exit=%d", out.ExitCode)
	}
	rec := lastLog(t, tmp)
	want := map[string]any{
		"hook": "supervisor-gate", "severity": "BLOCK", "decision": "block", "reason": "supervisor CRITICAL; blocking",
		"agent": "backend", "session_id": "s-1", "event": "PreToolUse",
		"finding_ts": "2026-01-15T10:00:00Z", "overall": "CRITICAL", "rationale": "r", "blocked": true,
	}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("log[%s]=%v want %v", k, rec[k], v)
		}
	}
	if _, has := rec["action"]; has {
		t.Error("legacy action field must be gone")
	}
}

func TestBlockMessageIsBashExact(t *testing.T) {
	tmp := t.TempDir()
	ts := "T1"
	writeFinding(t, filepath.Join(tmp, "supervisor-findings.ndjson"), ts, "CRITICAL", "the issue", "halt")
	out, _ := newHook(tmp, tmp).Run(context.Background(), in(t, nil))
	want := "supervisor-gate: supervisor flagged CRITICAL on finding T1:\n" +
		"       the issue\n" +
		"       Recommended action: halt\n" +
		"       To proceed:\n" +
		"         1. Review the finding in work/current/supervisor-findings.ndjson\n" +
		"         2. If the supervisor is wrong, add a bypass entry:\n" +
		"            ## bypass:supervisor-override-T1\n" +
		"            **Hook:** supervisor\n" +
		"            **Scope:** finding=T1\n" +
		"            (plus the standard Hook/Reason/Approved/Created/Expires fields)\n" +
		"         3. Or set supervisor.block_on_critical: false in .yakos.yml\n" +
		"            for passive-mode warnings only.\n" +
		"         4. Emergency bypass for this session only:\n" +
		"            export YAKOS_SUPERVISOR_DISABLE=1\n"
	if string(out.Stderr) != want {
		t.Fatalf("stderr mismatch:\n%q\nwant\n%q", out.Stderr, want)
	}
}

func TestBypassOnlyCountsRealActiveEntries(t *testing.T) {
	tmp := t.TempDir()
	ts := "T2"
	writeFinding(t, filepath.Join(tmp, "supervisor-findings.ndjson"), ts, "CRITICAL", "x", "halt")
	// A bare mention (no "## Active entries" heading) must not bypass.
	if err := os.WriteFile(filepath.Join(tmp, "hook-bypass.md"), []byte("**Hook:** supervisor\n**Scope:** finding=T2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ := newHook(tmp, tmp).Run(context.Background(), in(t, nil)); out.ExitCode != 2 {
		t.Fatalf("informal bypass must not count, exit=%d", out.ExitCode)
	}
	// Hook name must be the literal "supervisor" via substring: "supervisor-gate" also contains it.
	body := "## Active entries\n\n## bypass:x\n\n**Hook:** supervisor-gate\n**Scope:** finding=T2\n"
	if err := os.WriteFile(filepath.Join(tmp, "hook-bypass.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := newHook(tmp, tmp).Run(context.Background(), in(t, nil))
	if out.ExitCode != 0 || lastLog(t, tmp)["bypass"] != true {
		t.Fatalf("exit=%d log=%v", out.ExitCode, lastLog(t, tmp))
	}
}

func TestBlockOnCriticalIsGrepWindowNotYAML(t *testing.T) {
	cases := []struct {
		name string
		yml  string
		exit int
	}{
		{"false", "supervisor:\n  block_on_critical: false\n", 0},
		{"true", "supervisor:\n  block_on_critical: true\n", 2},
		{"trailing comment defeats awk/tr compare", "supervisor:\n  block_on_critical: false # passive\n", 2},
		{"falsey suffix defeats compare", "supervisor:\n  block_on_critical: falsey\n", 2},
		{"first match wins", "supervisor:\n  block_on_critical: true\n  block_on_critical: false\n", 2},
		{"outside the 10-line window", "supervisor:\n" + strings.Repeat("  x: 1\n", 10) + "  block_on_critical: false\n", 2},
		{"inside the 10-line window", "supervisor:\n" + strings.Repeat("  x: 1\n", 9) + "  block_on_critical: false\n", 0},
		{"no supervisor section", "block_on_critical: false\n", 2},
	}
	for _, c := range cases {
		tmp := t.TempDir()
		writeYAML(t, tmp, c.yml)
		writeFinding(t, filepath.Join(tmp, "supervisor-findings.ndjson"), "T", "CRITICAL", "r", "halt")
		out, _ := newHook(tmp, tmp).Run(context.Background(), in(t, nil))
		if out.ExitCode != c.exit {
			t.Errorf("%s: exit=%d want %d", c.name, out.ExitCode, c.exit)
		}
	}
}

func TestEnabledFalseIsGrepWindowNotYAML(t *testing.T) {
	for name, tc := range map[string]struct {
		yml  string
		skip bool
	}{
		"disabled":                 {"supervisor:\n  enabled: false\n", true},
		"trailing comment":         {"supervisor:\n  enabled: false # off\n", false},
		"other section":            {"budget:\n  enabled: false\n", false},
		"trailing whitespace":      {"supervisor:\n  enabled: false   \n", true},
		"nested under other block": {"supervisor:\n  sub:\n    enabled: false\n", true},
	} {
		tmp := t.TempDir()
		writeYAML(t, tmp, tc.yml)
		writeFinding(t, filepath.Join(tmp, "supervisor-findings.ndjson"), "T", "CRITICAL", "r", "halt")
		out, _ := newHook(tmp, tmp).Run(context.Background(), in(t, nil))
		if (out.ExitCode == 0) != tc.skip {
			t.Errorf("%s: exit=%d want skip=%v", name, out.ExitCode, tc.skip)
		}
	}
}

func TestLastPhysicalLineOnly(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "supervisor-findings.ndjson")
	writeFinding(t, f, "T", "CRITICAL", "r", "halt")
	// A trailing blank line is the last physical line: bash's tail -n 1
	// yields "" and the hook has nothing to decide (exit 0), it does NOT
	// walk back to the CRITICAL line above.
	if err := os.WriteFile(f, append(mustRead(t, f), '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ := newHook(tmp, tmp).Run(context.Background(), in(t, nil)); out.ExitCode != 0 {
		t.Fatalf("blank last line must pass, exit=%d", out.ExitCode)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMalformedLastLineWarnsAndPasses(t *testing.T) {
	for name, body := range map[string]string{
		"truncated json": "{\"overall\":\"CRITICAL\"\n",
		"json array":     "[]\n",
		"json number":    "5\n",
		"json string":    "\"x\"\n",
		"json null":      "null\n",
	} {
		tmp := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmp, "supervisor-findings.ndjson"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		out, _ := newHook(tmp, tmp).Run(context.Background(), in(t, nil))
		if out.ExitCode != 0 {
			t.Errorf("%s: exit=%d", name, out.ExitCode)
			continue
		}
		if r := lastLog(t, tmp); r["reason"] != "most-recent finding is not valid JSON; ignoring" || r["severity"] != "WARN" {
			t.Errorf("%s: log=%v", name, r)
		}
	}
}

func TestFieldDefaultsAndJQAlternative(t *testing.T) {
	tmp := t.TempDir()
	// overall false -> jq // -> "PASS"; ts absent -> "unknown".
	if err := os.WriteFile(filepath.Join(tmp, "supervisor-findings.ndjson"), []byte("{\"overall\":false}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := newHook(tmp, tmp).Run(context.Background(), in(t, nil))
	rec := lastLog(t, tmp)
	if out.ExitCode != 0 || rec["reason"] != "supervisor finding: PASS" || rec["finding_ts"] != "unknown" {
		t.Fatalf("exit=%d log=%v", out.ExitCode, rec)
	}
	// numeric overall -> rendered like jq -r -> unknown overall.
	if err := os.WriteFile(filepath.Join(tmp, "supervisor-findings.ndjson"), []byte("{\"overall\":5}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	newHook(tmp, tmp).Run(context.Background(), in(t, nil)) //nolint:errcheck
	if r := lastLog(t, tmp); r["reason"] != "unknown supervisor overall: '5'" {
		t.Fatalf("log=%v", r)
	}
}

func TestWarnMarkerFormat(t *testing.T) {
	tmp := t.TempDir()
	writeFinding(t, filepath.Join(tmp, "supervisor-findings.ndjson"), "T9", "WARN", "careful", "continue")
	newHook(tmp, tmp).Run(context.Background(), in(t, nil)) //nolint:errcheck
	if got := string(mustRead(t, filepath.Join(tmp, ".supervisor-gate-last-surfaced"))); got != "T9\n" {
		t.Fatalf("marker=%q want T9\\n", got)
	}
}
