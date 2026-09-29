package planqualitygate_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/planqualitygate"
)

func newHook(workDir, projectDir string) *planqualitygate.Hook {
	return &planqualitygate.Hook{
		WorkCurrentDir: workDir,
		ProjectDir:     projectDir,
		NowFn:          func() time.Time { return time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC) },
	}
}

func input(tool string, env map[string]string) hooktype.HookInput {
	if env == nil {
		env = map[string]string{}
	}
	return hooktype.HookInput{Event: "PreToolUse", Tool: tool, Payload: map[string]any{}, Env: env}
}

func writeMarker(t *testing.T, dir, planID, reason string) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"plan_id": planID, "reason": reason})
	if err := os.WriteFile(filepath.Join(dir, ".plan-blocked"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, h *planqualitygate.Hook, in hooktype.HookInput) hooktype.HookOutput {
	t.Helper()
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return out
}

func TestName(t *testing.T) {
	if got := newHook("", "").Name(); got != "plan-quality-gate" {
		t.Fatalf("Name()=%q", got)
	}
}

func TestMarkerBlocksDispatchTools(t *testing.T) {
	for _, tool := range []string{"TeamCreate", "Agent"} {
		tmp := t.TempDir()
		writeMarker(t, tmp, "plan-bad", "aggregate 0.5 < threshold 0.75")
		out := run(t, newHook(tmp, tmp), input(tool, nil))
		if out.ExitCode != 2 {
			t.Fatalf("%s: want block, got exit %d", tool, out.ExitCode)
		}
		s := string(out.Stderr)
		if !strings.HasPrefix(s, "plan-quality-gate: ") {
			t.Fatalf("%s: stderr should carry ho_block's hook prefix: %s", tool, s)
		}
		if !strings.Contains(s, "plan-bad") || !strings.Contains(s, "yakos plan score override plan-bad") {
			t.Fatalf("%s: stderr lacks plan id / override hint: %s", tool, s)
		}
	}
}

func TestNoMarkerPasses(t *testing.T) {
	tmp := t.TempDir()
	if out := run(t, newHook(tmp, tmp), input("Agent", nil)); out.ExitCode != 0 {
		t.Fatalf("want pass, got %d", out.ExitCode)
	}
}

func TestMissingWorkCurrentDirPasses(t *testing.T) {
	tmp := t.TempDir()
	if out := run(t, newHook(filepath.Join(tmp, "absent", "current"), tmp), input("Agent", nil)); out.ExitCode != 0 {
		t.Fatalf("absent work/current (searchable parent) must pass, got %d", out.ExitCode)
	}
}

// Tool-name prefix / case variants must not be gated.
func TestOnlyExactToolNamesGated(t *testing.T) {
	tmp := t.TempDir()
	writeMarker(t, tmp, "p", "bad")
	for _, tool := range []string{"AgentX", "TeamCreateFoo", "agent", "XAgent", "Bash", "Edit"} {
		if out := run(t, newHook(tmp, tmp), input(tool, nil)); out.ExitCode != 0 {
			t.Fatalf("tool %q must not be gated, got exit %d", tool, out.ExitCode)
		}
	}
}

func TestEmptyToolNameFailsClosed(t *testing.T) {
	tmp := t.TempDir()
	out := run(t, newHook(tmp, tmp), input("", nil))
	if out.ExitCode != 2 || !strings.Contains(string(out.Stderr), "BLOCKED") {
		t.Fatalf("want exit 2 with BLOCKED, got %d %q", out.ExitCode, out.Stderr)
	}
}

func TestEmptyWorkDirFailsClosed(t *testing.T) {
	out := run(t, newHook("", ""), input("Agent", nil))
	if out.ExitCode != 2 {
		t.Fatalf("unresolvable work dir must block, got %d", out.ExitCode)
	}
}

func TestDisableEnvPasses(t *testing.T) {
	tmp := t.TempDir()
	writeMarker(t, tmp, "p", "bad")
	out := run(t, newHook(tmp, tmp), input("Agent", map[string]string{"YAKOS_PLAN_QUALITY_DISABLE": "1"}))
	if out.ExitCode != 0 {
		t.Fatalf("want pass, got %d", out.ExitCode)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "logs", "plan-quality-gate.ndjson"))
	if err != nil || !strings.Contains(string(data), "gate bypassed") {
		t.Fatalf("bypass must leave a WARN record: %v %s", err, data)
	}
}

func TestOptOutClearsMarkerAndPasses(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, ".yakos.yml"), []byte("plan_quality:\n  enabled: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeMarker(t, tmp, "p", "bad")
	if out := run(t, newHook(tmp, tmp), input("TeamCreate", nil)); out.ExitCode != 0 {
		t.Fatalf("want pass, got %d", out.ExitCode)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".plan-blocked")); err == nil {
		t.Fatal("marker should be cleared")
	}
}

// A directory (or dangling symlink) named .plan-blocked is still a marker.
func TestNonRegularMarkerBlocks(t *testing.T) {
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, ".plan-blocked"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := run(t, newHook(tmp, tmp), input("Agent", nil))
	if out.ExitCode != 2 || !strings.Contains(string(out.Stderr), "not a regular file") {
		t.Fatalf("directory marker: want block w/ reason, got %d %q", out.ExitCode, out.Stderr)
	}

	tmp2 := t.TempDir()
	if err := os.Symlink(filepath.Join(tmp2, "nope"), filepath.Join(tmp2, ".plan-blocked")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if out := run(t, newHook(tmp2, tmp2), input("Agent", nil)); out.ExitCode != 2 {
		t.Fatalf("dangling symlink marker: want block, got %d", out.ExitCode)
	}
}

func TestPlainTextAndEmptyMarkersBlock(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, ".plan-blocked"), []byte("plain reason\nmore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := run(t, newHook(tmp, tmp), input("Agent", nil))
	if out.ExitCode != 2 || !strings.Contains(string(out.Stderr), "plain reason") {
		t.Fatalf("plain-text marker: got %d %q", out.ExitCode, out.Stderr)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".plan-blocked"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if out := run(t, newHook(tmp, tmp), input("Agent", nil)); out.ExitCode != 2 {
		t.Fatalf("empty marker: want block, got %d", out.ExitCode)
	}
}

func TestPathWithSpaces(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "a dir with spaces", "work current")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	if out := run(t, newHook(tmp, tmp), input("Agent", nil)); out.ExitCode != 0 {
		t.Fatalf("absent: want pass, got %d", out.ExitCode)
	}
	writeMarker(t, tmp, "p-sp", "bad")
	if out := run(t, newHook(tmp, tmp), input("Agent", nil)); out.ExitCode != 2 {
		t.Fatalf("present: want block, got %d", out.ExitCode)
	}
}

func TestUnreadableDirFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits not enforced here")
	}
	base := t.TempDir()
	cur := filepath.Join(base, "work", "current")
	if err := os.MkdirAll(cur, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMarker(t, cur, "p", "bad")
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(base, "work"), 0o755); _ = os.Chmod(cur, 0o755) })

	// Parent unsearchable: marker cannot be told apart from absent.
	if err := os.Chmod(filepath.Join(base, "work"), 0); err != nil {
		t.Fatal(err)
	}
	if out := run(t, newHook(cur, base), input("Agent", nil)); out.ExitCode != 2 {
		t.Fatalf("unsearchable parent: want block, got %d", out.ExitCode)
	}
	_ = os.Chmod(filepath.Join(base, "work"), 0o755)

	// Directory searchable but not readable and marker absent.
	_ = os.Remove(filepath.Join(cur, ".plan-blocked"))
	if err := os.Chmod(cur, 0o300); err != nil {
		t.Fatal(err)
	}
	if out := run(t, newHook(cur, base), input("Agent", nil)); out.ExitCode != 2 {
		t.Fatalf("unreadable work/current: want block, got %d", out.ExitCode)
	}
}

func TestUnwritableLogDoesNotChangeDecision(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits not enforced here")
	}
	tmp := t.TempDir()
	logs := filepath.Join(tmp, "logs")
	if err := os.Mkdir(logs, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(logs, 0o755) })
	if out := run(t, newHook(tmp, tmp), input("Agent", nil)); out.ExitCode != 0 {
		t.Fatalf("absent marker + unwritable log: want pass, got %d", out.ExitCode)
	}
	writeMarker(t, tmp, "p", "bad")
	if out := run(t, newHook(tmp, tmp), input("Agent", nil)); out.ExitCode != 2 {
		t.Fatalf("marker + unwritable log: want block, got %d", out.ExitCode)
	}
}

// Every panic must surface as exit 2. A nil Env map is fine; force a panic via a
// nil NowFn-independent path by passing a nil hook receiver field misuse.
func TestPanicFailsClosed(t *testing.T) {
	var h *planqualitygate.Hook // nil receiver: h.WorkCurrentDir panics
	out, err := h.Run(context.Background(), input("Agent", nil))
	if out.ExitCode != 2 {
		t.Fatalf("panic must fail closed (exit 2), got %d", out.ExitCode)
	}
	if err == nil {
		t.Fatal("panic should also be reported as an error")
	}
}

// Racing marker create/remove (what `yakos plan score override` does) must only
// ever yield pass or block, never an error or another exit code.
func TestConcurrentOverrideAndGate(t *testing.T) {
	tmp := t.TempDir()
	proto, _ := json.Marshal(map[string]any{"plan_id": "p", "reason": "bad"})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		marker := filepath.Join(tmp, ".plan-blocked")
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.WriteFile(marker+".tmp", proto, 0o644)
			_ = os.Rename(marker+".tmp", marker)
			_ = os.Remove(marker)
		}
	}()
	h := newHook(tmp, tmp)
	for i := 0; i < 300; i++ {
		out, err := h.Run(context.Background(), input("Agent", nil))
		if err != nil || (out.ExitCode != 0 && out.ExitCode != 2) {
			close(stop)
			wg.Wait()
			t.Fatalf("iteration %d: exit=%d err=%v", i, out.ExitCode, err)
		}
	}
	close(stop)
	wg.Wait()
}

// The gate is event-agnostic (bash parity): a stale PostToolUse registration
// delivering Edit is a no-op; delivering Agent is still gated.
func TestEventAgnostic(t *testing.T) {
	tmp := t.TempDir()
	writeMarker(t, tmp, "p", "bad")
	in := input("Edit", nil)
	in.Event = "PostToolUse"
	if out := run(t, newHook(tmp, tmp), in); out.ExitCode != 0 {
		t.Fatalf("stale PostToolUse Edit registration must be a no-op, got %d", out.ExitCode)
	}
}

// work/current being a regular file (ENOTDIR on the marker lookup) means the
// marker cannot be inspected: fail closed, do not read it as "absent".
func TestWorkCurrentIsFileFailsClosed(t *testing.T) {
	f := filepath.Join(t.TempDir(), "current")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := run(t, newHook(f, ""), input("Agent", nil)); out.ExitCode != 2 {
		t.Fatalf("want block, got %d", out.ExitCode)
	}
}

// The block record uses bash ho_log's schema (decision/reason/plan_id), so a
// log consumer sees the same shape from either implementation.
func TestBlockLogRecordSchema(t *testing.T) {
	tmp := t.TempDir()
	writeMarker(t, tmp, "plan-log", "aggregate 0.4 < threshold 0.75")
	run(t, newHook(tmp, tmp), input("Agent", nil))
	data, err := os.ReadFile(filepath.Join(tmp, "logs", "plan-quality-gate.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &rec); err != nil {
		t.Fatalf("log record is not JSON: %v: %s", err, data)
	}
	for k, want := range map[string]any{"hook": "plan-quality-gate", "severity": "BLOCK", "decision": "block", "plan_id": "plan-log", "agent": "lead"} {
		if rec[k] != want {
			t.Errorf("record[%q]=%v want %v", k, rec[k], want)
		}
	}
}

// enabled:false under a different top-level section must not disable the gate.
func TestOptOutScopedToPlanQualitySection(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, ".yakos.yml"), []byte("plan_quality:\n  mode: block\nother:\n  enabled: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeMarker(t, tmp, "p", "bad")
	if out := run(t, newHook(tmp, tmp), input("Agent", nil)); out.ExitCode != 2 {
		t.Fatalf("want block, got %d", out.ExitCode)
	}
}
