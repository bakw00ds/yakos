package planqualityscore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/planqualityscore"
)

// stubScorer stands in for score-plan.sh. It scores the file it is GIVEN
// from that file's own header lines ("score:", "dissent:", "id:"), so a
// test can tell "scored the just-written file" from "read a stale record".
// It records every invocation (plan path, cost ceiling) to $STUB_CALLS and
// writes its record to $HOME/.yakos-state/plan-quality-log.ndjson, exactly
// where the real scorer writes.
const stubScorer = `#!/usr/bin/env bash
plan="$1"
printf '%s ceiling=%s\n' "$plan" "${YAKOS_PLAN_EVAL_MAX_COST_USD:-}" >> "$STUB_CALLS"
[ -n "${STUB_RC:-}" ] && [ "${STUB_NO_RECORD:-}" = "1" ] && exit "$STUB_RC"
[ "${STUB_NO_RECORD:-}" = "1" ] && exit 0
score="$(sed -n 's/^score: *//p' "$plan" | head -1)"
dissent="$(sed -n 's/^dissent: *//p' "$plan" | head -1)"
id="$(sed -n 's/^id: *//p' "$plan" | head -1)"
mkdir -p "$HOME/.yakos-state"
if [ "${STUB_GARBAGE:-}" = "1" ]; then
  echo 'not json {' >> "$HOME/.yakos-state/plan-quality-log.ndjson"
else
  printf '{"type":"plan_scored","plan_id":"%s","aggregate_score":%s,"threshold":0.75,"verdict":"x","dissent":%s,"panel_size":3}\n' \
    "${id:-plan-x}" "${score:-0}" "${dissent:-false}" >> "$HOME/.yakos-state/plan-quality-log.ndjson"
fi
exit "${STUB_RC:-0}"
`

type env struct {
	root  string // fake framework root holding the stub scorer
	proj  string // project dir (.yakos.yml lives here)
	work  string // work/current
	plan  string // work/current/plan.md
	calls string // STUB_CALLS file
	home  string // HOME for persisted-record copy
}

func newEnv(t *testing.T) *env {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("scorer is a bash script")
	}
	tmp := t.TempDir()
	e := &env{
		root:  filepath.Join(tmp, "fw"),
		proj:  filepath.Join(tmp, "proj"),
		calls: filepath.Join(tmp, "calls.txt"),
		home:  filepath.Join(tmp, "home"),
	}
	e.work = filepath.Join(e.proj, "work", "current")
	e.plan = filepath.Join(e.work, "plan.md")
	script := filepath.Join(e.root, "lib", "skills", "plan-quality-eval", "scripts", "score-plan.sh")
	for _, d := range []string{filepath.Dir(script), e.work, e.home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(script, []byte(stubScorer), 0o755); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) yml(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.proj, ".yakos.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writePlan writes plan.md with the given header and backdates its mtime.
func (e *env) writePlan(t *testing.T, score, extra string, age time.Duration) {
	t.Helper()
	body := "score: " + score + "\n" + extra + "# plan\n"
	if err := os.WriteFile(e.plan, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Add(-age)
	if err := os.Chtimes(e.plan, mt, mt); err != nil {
		t.Fatal(err)
	}
}

func (e *env) hook() *planqualityscore.Hook {
	h := planqualityscore.New(e.work, e.proj)
	h.YakosRoot = e.root
	h.HomeDir = e.home
	return h
}

func (e *env) input(tool, file string, extra map[string]string) hooktype.HookInput {
	env := map[string]string{"STUB_CALLS": e.calls, "HOME": e.home}
	for k, v := range extra {
		env[k] = v
	}
	return hooktype.HookInput{
		Event:   "PostToolUse",
		Tool:    tool,
		Payload: map[string]any{"tool_input": map[string]any{"file_path": file}},
		Env:     env,
	}
}

func (e *env) run(t *testing.T, tool string, extra map[string]string) hooktype.HookOutput {
	t.Helper()
	out, err := e.hook().Run(context.Background(), e.input(tool, e.plan, extra))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("PostToolUse scorer must never block, got exit %d", out.ExitCode)
	}
	return out
}

func (e *env) callCount(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile(e.calls)
	if err != nil {
		return 0
	}
	return len(strings.Split(strings.TrimSpace(string(data)), "\n"))
}

func (e *env) marker(t *testing.T) (map[string]any, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.work, ".plan-blocked"))
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &m); err != nil {
		t.Fatalf("marker not JSON: %v (%s)", err, data)
	}
	return m, true
}

// lastLog returns the last record of logs/plan-quality-score.ndjson.
func (e *env) lastLog(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.work, "logs", "plan-quality-score.ndjson"))
	if err != nil {
		t.Fatalf("no hook log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("bad log line: %v", err)
	}
	return m
}

func (e *env) notes(t *testing.T) []string {
	ents, _ := os.ReadDir(filepath.Join(e.work, "notes"))
	var names []string
	for _, en := range ents {
		names = append(names, en.Name())
	}
	return names
}

const old = 30 * time.Second

func TestNonPlanFileIgnored(t *testing.T) {
	e := newEnv(t)
	in := e.input("Write", filepath.Join(e.proj, "api", "handler.go"), nil)
	if out, err := e.hook().Run(context.Background(), in); err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v out=%+v", err, out)
	}
	if e.callCount(t) != 0 {
		t.Fatal("scorer must not run for a non-plan.md write")
	}
	if _, err := os.Stat(filepath.Join(e.work, "logs")); err == nil {
		t.Fatal("non-plan write must be a silent no-op")
	}
}

func TestBelowThresholdWriteBlocksInBlockMode(t *testing.T) {
	e := newEnv(t)
	e.yml(t, "plan_quality:\n  enabled: true\n  mode: block\n  threshold: 0.75\n")
	e.writePlan(t, "0.40", "id: plan-low\n", old)
	e.run(t, "Write", nil)
	m, ok := e.marker(t)
	if !ok {
		t.Fatal(".plan-blocked not written for a below-threshold plan")
	}
	// Marker field types and text match bash (jq --arg: every value a string).
	if m["plan_id"] != "plan-low" || m["aggregate_score"] != "0.40" || m["threshold"] != "0.75" {
		t.Fatalf("marker=%v", m)
	}
	if m["reason"] != "aggregate 0.40 < threshold 0.75; plan quality below bar" {
		t.Fatalf("reason=%v", m["reason"])
	}
	if _, err := time.Parse("2006-01-02T15:04:05Z", m["ts"].(string)); err != nil {
		t.Fatalf("ts=%v", m["ts"])
	}
	if rec := e.lastLog(t); rec["decision"] != "block_next_tool" || rec["severity"] != "WARN" {
		t.Fatalf("log=%v", rec)
	}
	if len(e.notes(t)) != 1 {
		t.Fatalf("notes=%v", e.notes(t))
	}
}

func TestBelowThresholdSurfaceModeWritesNotesNoMarker(t *testing.T) {
	e := newEnv(t)
	e.yml(t, "plan_quality:\n  mode: surface\n  threshold: 0.75\n")
	e.writePlan(t, "0.60", "id: plan-s\n", old)
	e.run(t, "Edit", nil)
	if _, ok := e.marker(t); ok {
		t.Fatal("surface mode must not write .plan-blocked")
	}
	if len(e.notes(t)) != 1 {
		t.Fatalf("notes=%v", e.notes(t))
	}
	if rec := e.lastLog(t); rec["decision"] != "surface_to_operator" {
		t.Fatalf("log=%v", rec)
	}
}

func TestDefaultModeIsSurface(t *testing.T) {
	e := newEnv(t) // no .yakos.yml at all
	e.writePlan(t, "0.10", "", old)
	e.run(t, "Write", nil)
	if _, ok := e.marker(t); ok {
		t.Fatal("default mode must surface, not block")
	}
	if rec := e.lastLog(t); rec["decision"] != "surface_to_operator" || rec["threshold"] != "0.75" {
		t.Fatalf("log=%v", rec)
	}
}

func TestAboveThresholdPasses(t *testing.T) {
	e := newEnv(t)
	e.yml(t, "plan_quality:\n  mode: block\n")
	e.writePlan(t, "0.85", "id: plan-ok\n", old)
	e.run(t, "Write", nil)
	if _, ok := e.marker(t); ok {
		t.Fatal("passing score must not block")
	}
	if rec := e.lastLog(t); rec["decision"] != "pass" || rec["severity"] != "REPORT" {
		t.Fatalf("log=%v", rec)
	}
	if e.callCount(t) != 1 {
		t.Fatalf("calls=%d", e.callCount(t))
	}
}

func TestScoreEqualToThresholdPasses(t *testing.T) {
	e := newEnv(t)
	e.yml(t, "plan_quality:\n  mode: block\n  threshold: 0.75\n")
	e.writePlan(t, "0.75", "", old)
	e.run(t, "Write", nil)
	if _, ok := e.marker(t); ok {
		t.Fatal("aggregate == threshold is a pass (bash: >=)")
	}
}

func TestDissentSurfacesNeverBlocks(t *testing.T) {
	e := newEnv(t)
	e.yml(t, "plan_quality:\n  mode: block\n  threshold: 0.75\n")
	e.writePlan(t, "0.20", "dissent: true\nid: plan-d\n", old) // below threshold AND dissent
	e.run(t, "Write", nil)
	if _, ok := e.marker(t); ok {
		t.Fatal("dissent must never write .plan-blocked")
	}
	if rec := e.lastLog(t); rec["decision"] != "surface_to_operator" {
		t.Fatalf("log=%v", rec)
	}
	if len(e.notes(t)) != 1 {
		t.Fatalf("notes=%v", e.notes(t))
	}
}

// The #293 review finding: Go read the last PERSISTED record instead of
// scoring the file just written. Both symptoms, one test each.
func TestScoresTheWrittenFileNotAStaleRecord_FalseBlock(t *testing.T) {
	e := newEnv(t)
	e.yml(t, "plan_quality:\n  mode: block\n  threshold: 0.75\n")
	// A stale FAILING record is already persisted...
	stale := filepath.Join(e.home, ".yakos-state", "plan-quality-log.ndjson")
	_ = os.MkdirAll(filepath.Dir(stale), 0o755)
	_ = os.WriteFile(stale, []byte(`{"type":"plan_scored","plan_id":"stale","aggregate_score":0.10,"dissent":false}`+"\n"), 0o644)
	// ...but the plan just written is good.
	e.writePlan(t, "0.95", "id: plan-good\n", old)
	e.run(t, "Write", nil)
	if _, ok := e.marker(t); ok {
		t.Fatal("false block: a good plan was judged by a stale failing record")
	}
}

func TestScoresTheWrittenFileNotAStaleRecord_MissedBlock(t *testing.T) {
	e := newEnv(t)
	e.yml(t, "plan_quality:\n  mode: block\n  threshold: 0.75\n")
	stale := filepath.Join(e.home, ".yakos-state", "plan-quality-log.ndjson")
	_ = os.MkdirAll(filepath.Dir(stale), 0o755)
	_ = os.WriteFile(stale, []byte(`{"type":"plan_scored","plan_id":"stale","aggregate_score":0.99,"dissent":false}`+"\n"), 0o644)
	e.writePlan(t, "0.30", "id: plan-bad\n", old)
	e.run(t, "Write", nil)
	m, ok := e.marker(t)
	if !ok || m["plan_id"] != "plan-bad" {
		t.Fatalf("missed block: marker=%v ok=%v", m, ok)
	}
}

func TestScorerReceivesTheWrittenFilePath(t *testing.T) {
	e := newEnv(t)
	e.writePlan(t, "0.9", "", old)
	e.run(t, "Write", nil)
	data, _ := os.ReadFile(e.calls)
	if !strings.HasPrefix(string(data), e.plan+" ") {
		t.Fatalf("scorer arg = %q, want %q", data, e.plan)
	}
}

// Passing after a failing write does NOT remove the marker: bash never
// does either. Clearing is the gate's and the operator's job.
func TestPassAfterBelowLeavesMarkerLikeBash(t *testing.T) {
	e := newEnv(t)
	e.yml(t, "plan_quality:\n  mode: block\n  threshold: 0.75\n")
	e.writePlan(t, "0.30", "id: p1\n", old)
	e.run(t, "Write", nil)
	if _, ok := e.marker(t); !ok {
		t.Fatal("setup: marker expected")
	}
	e.writePlan(t, "0.90", "id: p2\n", old)
	e.run(t, "Write", nil)
	m, ok := e.marker(t)
	if !ok || m["plan_id"] != "p1" {
		t.Fatalf("scorer must leave the existing marker untouched (bash parity): %v %v", m, ok)
	}
}

func TestDebounce(t *testing.T) {
	cases := []struct {
		name      string
		age       time.Duration
		wantCalls int
	}{
		{"fresh write is skipped", 0, 0},
		{"4s old is skipped", 4 * time.Second, 0},
		{"6s old is scored", 6 * time.Second, 1},
		{"30s old is scored", old, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.writePlan(t, "0.9", "", c.age)
			e.run(t, "Write", nil)
			if got := e.callCount(t); got != c.wantCalls {
				t.Fatalf("calls=%d want %d", got, c.wantCalls)
			}
			if c.wantCalls == 0 {
				rec := e.lastLog(t)
				if rec["decision"] != "pass" || rec["severity"] != "REPORT" ||
					!strings.HasPrefix(rec["reason"].(string), "debounced: plan.md mtime age=") {
					t.Fatalf("debounce log=%v", rec)
				}
			}
		})
	}
}

// A write landing inside the 5 s window of the previous modification is
// skipped, so a burst of writes yields one scoring, not one per write.
func TestBurstOfWritesScoresOnce(t *testing.T) {
	e := newEnv(t)
	e.writePlan(t, "0.9", "id: a\n", old)
	e.run(t, "Write", nil) // scored
	e.writePlan(t, "0.9", "id: b\n", 0)
	e.run(t, "Edit", nil) // mtime fresh: debounced
	e.writePlan(t, "0.9", "id: c\n", time.Second)
	e.run(t, "MultiEdit", nil) // still inside the window
	if got := e.callCount(t); got != 1 {
		t.Fatalf("burst produced %d scorings, want 1", got)
	}
}

// Future mtime (clock skew): negative age does not debounce, like bash.
func TestFutureMtimeDoesNotDebounce(t *testing.T) {
	e := newEnv(t)
	e.writePlan(t, "0.9", "", -time.Hour)
	e.run(t, "Write", nil)
	if e.callCount(t) != 1 {
		t.Fatal("negative age must not debounce")
	}
}

func TestDebounceUsesInjectedClock(t *testing.T) {
	e := newEnv(t)
	e.writePlan(t, "0.9", "", old)
	h := e.hook()
	h.NowFn = func() time.Time { return time.Now().Add(-old) } // "now" is when the file was written
	if _, err := h.Run(context.Background(), e.input("Write", e.plan, nil)); err != nil {
		t.Fatal(err)
	}
	if e.callCount(t) != 0 {
		t.Fatal("injected clock 30s in the past makes the file 'fresh'")
	}
}

func TestEnabledFalseSkipsScoring(t *testing.T) {
	e := newEnv(t)
	e.yml(t, "plan_quality:\n  enabled: false\n")
	e.writePlan(t, "0.1", "", old)
	e.run(t, "Write", nil)
	if e.callCount(t) != 0 {
		t.Fatal("enabled=false must not score")
	}
	if rec := e.lastLog(t); rec["reason"] != "plan_quality.enabled=false; skipping" {
		t.Fatalf("log=%v", rec)
	}
}

func TestEmergencyDisable(t *testing.T) {
	e := newEnv(t)
	e.writePlan(t, "0.1", "", old)
	e.run(t, "Write", map[string]string{"YAKOS_PLAN_QUALITY_DISABLE": "1"})
	if e.callCount(t) != 0 {
		t.Fatal("YAKOS_PLAN_QUALITY_DISABLE=1 must skip scoring")
	}
}

func TestInfraFailuresPassWithWarn(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"exit 2", map[string]string{"STUB_RC": "2"}, "score-plan.sh exited 2 (infra error); no gate action"},
		{"exit 3", map[string]string{"STUB_RC": "3"}, "score-plan.sh exited 3 (infra error); no gate action"},
		{"exit 4", map[string]string{"STUB_RC": "4"}, "score-plan.sh exited 4 (infra error); no gate action"},
		{"no record", map[string]string{"STUB_NO_RECORD": "1"}, "no log record after scoring; no gate action"},
		{"garbage record", map[string]string{"STUB_GARBAGE": "1"}, "invalid log record JSON; no gate action"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.yml(t, "plan_quality:\n  mode: block\n")
			e.writePlan(t, "0.1", "", old)
			out := e.run(t, "Write", c.env)
			if _, ok := e.marker(t); ok {
				t.Fatal("infra error must never write the marker")
			}
			rec := e.lastLog(t)
			if rec["severity"] != "WARN" || rec["decision"] != "pass" || rec["reason"] != c.want {
				t.Fatalf("log=%v", rec)
			}
			if !strings.Contains(string(out.Stderr), "WARN: plan-quality-score:") {
				t.Fatalf("stderr=%q", out.Stderr)
			}
		})
	}
}

func TestExitOneStillReadsTheRecord(t *testing.T) {
	// score-plan.sh exits 1 for fail/dissent; that is a normal verdict.
	e := newEnv(t)
	e.yml(t, "plan_quality:\n  mode: block\n")
	e.writePlan(t, "0.2", "id: p-fail\n", old)
	e.run(t, "Write", map[string]string{"STUB_RC": "1"})
	if _, ok := e.marker(t); !ok {
		t.Fatal("rc=1 with a failing record must still block")
	}
}

func TestScorerMissingPassesWithWarn(t *testing.T) {
	e := newEnv(t)
	e.writePlan(t, "0.1", "", old)
	h := e.hook()
	h.YakosRoot = filepath.Join(e.root, "nope")
	out, err := h.Run(context.Background(), e.input("Write", e.plan, nil))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v out=%+v", err, out)
	}
	rec := e.lastLog(t)
	if rec["severity"] != "WARN" || rec["reason"] != "score-plan.sh not found; skipping scoring" {
		t.Fatalf("log=%v", rec)
	}
}

func TestFrameworkRootResolution(t *testing.T) {
	e := newEnv(t)
	e.writePlan(t, "0.9", "", old)
	// HooksDir/../.. is the root when neither env nor override is set.
	h := planqualityscore.New(e.work, e.proj)
	h.HomeDir = e.home
	h.HooksDir = filepath.Join(e.root, "lib", "hooks")
	if _, err := h.Run(context.Background(), e.input("Write", e.plan, nil)); err != nil {
		t.Fatal(err)
	}
	if e.callCount(t) != 1 {
		t.Fatal("HooksDir/../.. must locate the scorer")
	}
	// $YAKOS_ROOT wins over both.
	h.HooksDir = filepath.Join(e.root, "elsewhere", "hooks")
	if _, err := h.Run(context.Background(), e.input("Write", e.plan, map[string]string{"YAKOS_ROOT": e.root})); err != nil {
		t.Fatal(err)
	}
	if e.callCount(t) != 2 {
		t.Fatal("YAKOS_ROOT must win")
	}
}

func TestCostCeilingPropagation(t *testing.T) {
	cases := []struct {
		name, yml string
		env       map[string]string
		want      string
	}{
		{"default", "", nil, "ceiling=0.15"},
		{"env", "", map[string]string{"YAKOS_PLAN_EVAL_MAX_COST_USD": "0.5"}, "ceiling=0.5"},
		{"yml beats env", "plan_quality:\n  cost_ceiling_usd: 0.3\n", map[string]string{"YAKOS_PLAN_EVAL_MAX_COST_USD": "0.5"}, "ceiling=0.3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			if c.yml != "" {
				e.yml(t, c.yml)
			}
			e.writePlan(t, "0.9", "", old)
			e.run(t, "Write", c.env)
			data, _ := os.ReadFile(e.calls)
			if !strings.Contains(string(data), c.want) {
				t.Fatalf("calls=%q want %q", data, c.want)
			}
		})
	}
}

func TestScoredRecordIsPersisted(t *testing.T) {
	e := newEnv(t)
	e.writePlan(t, "0.9", "id: persisted\n", old)
	e.run(t, "Write", nil)
	data, err := os.ReadFile(filepath.Join(e.home, ".yakos-state", "plan-quality-log.ndjson"))
	if err != nil || !strings.Contains(string(data), `"plan_id":"persisted"`) {
		t.Fatalf("persisted=%q err=%v", data, err)
	}
}

func TestLogRecordUsesHoLogSchema(t *testing.T) {
	e := newEnv(t)
	e.writePlan(t, "0.9", "", old)
	in := e.input("Write", e.plan, nil)
	in.Payload["agent_type"] = "yakos:planner"
	in.Payload["session_id"] = "sess-1"
	if _, err := e.hook().Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	rec := e.lastLog(t)
	for k, want := range map[string]string{"hook": "plan-quality-score", "agent": "planner", "session_id": "sess-1", "event": "PostToolUse"} {
		if rec[k] != want {
			t.Errorf("%s=%v want %s", k, rec[k], want)
		}
	}
	for _, k := range []string{"decision", "reason", "ts", "plan_id", "aggregate_score", "threshold"} {
		if _, ok := rec[k]; !ok {
			t.Errorf("missing %s in %v", k, rec)
		}
	}
	if _, bad := rec["action"]; bad {
		t.Error("legacy 'action' key must be gone")
	}
}

// Real PostToolUse payloads carry the target only at tool_input.file_path.
func TestRealPayloadShapesWritePlanBlocked(t *testing.T) {
	shapes := map[string]string{
		"Write":     `{"hook_event_name":"PostToolUse","tool_name":"Write","tool_input":{"file_path":%s,"content":"# plan"},"tool_response":{"filePath":%s,"type":"update"}}`,
		"Edit":      `{"hook_event_name":"PostToolUse","tool_name":"Edit","tool_input":{"file_path":%s,"old_string":"a","new_string":"b"},"tool_response":{"filePath":%s}}`,
		"MultiEdit": `{"hook_event_name":"PostToolUse","tool_name":"MultiEdit","tool_input":{"file_path":%s,"edits":[{"old_string":"a","new_string":"b"}]},"tool_response":{"filePath":%s}}`,
	}
	for tool, tmpl := range shapes {
		t.Run(tool, func(t *testing.T) {
			e := newEnv(t)
			e.yml(t, "plan_quality:\n  mode: block\n  threshold: 0.75\n")
			e.writePlan(t, "0.40", "id: plan-real\n", old)
			abs, _ := json.Marshal(e.plan)
			var payload map[string]any
			if err := json.Unmarshal([]byte(strings.ReplaceAll(tmpl, "%s", string(abs))), &payload); err != nil {
				t.Fatal(err)
			}
			in := hooktype.HookInput{Event: "PostToolUse", Tool: tool, Payload: payload,
				Env: map[string]string{"STUB_CALLS": e.calls, "HOME": e.home}}
			if out, err := e.hook().Run(context.Background(), in); err != nil || out.ExitCode != 0 {
				t.Fatalf("err=%v exit=%d", err, out.ExitCode)
			}
			if _, ok := e.marker(t); !ok {
				t.Fatalf(".plan-blocked not written for %s payload", tool)
			}
		})
	}
}

func TestTopLevelPathIgnored(t *testing.T) {
	e := newEnv(t)
	e.yml(t, "plan_quality:\n  mode: block\n")
	e.writePlan(t, "0.1", "", old)
	in := hooktype.HookInput{Event: "PostToolUse", Tool: "Write",
		Payload: map[string]any{"path": e.plan, "file_path": e.plan},
		Env:     map[string]string{"STUB_CALLS": e.calls}}
	if _, err := e.hook().Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if e.callCount(t) != 0 {
		t.Fatal("top-level path must not trigger scoring (bash ignores it)")
	}
}

// plan_id is model-written; it must not escape work/current/notes.
func TestUnsafePlanIDWritesNoNotesOutsideNotesDir(t *testing.T) {
	for _, id := range []string{"../../../../ESCAPED", "a/b", "/abs/x", ".hidden", "..", `x\u0000y`} {
		for _, dissent := range []string{"false", "true"} {
			e := newEnv(t)
			e.yml(t, "plan_quality:\n  mode: block\n  threshold: 0.75\n")
			e.writePlan(t, "0.40", fmt.Sprintf("id: %s\ndissent: %s\n", id, dissent), old)
			e.run(t, "Write", nil)
			_ = filepath.Walk(e.proj, func(path string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() && strings.Contains(path, "ESCAPED") {
					t.Errorf("id %q: escaped write %s", id, path)
				}
				return nil
			})
			if n := e.notes(t); len(n) != 0 {
				t.Errorf("id %q dissent=%s: notes written for unsafe id: %v", id, dissent, n)
			}
		}
	}
}

func TestSafePlanIDStillWritesNotes(t *testing.T) {
	e := newEnv(t)
	e.writePlan(t, "0.40", "id: plan-2026.09_x\n", old)
	e.run(t, "Write", nil)
	if n := e.notes(t); len(n) != 1 || n[0] != "plan-quality-plan-2026.09_x.md" {
		t.Fatalf("notes=%v", n)
	}
}

func TestPreToolUseNeverGates(t *testing.T) {
	e := newEnv(t)
	_ = os.WriteFile(filepath.Join(e.work, ".plan-blocked"), []byte(`{"plan_id":"p","reason":"bad"}`), 0o644)
	for _, tool := range []string{"Agent", "TeamCreate"} {
		in := hooktype.HookInput{Event: "PreToolUse", Tool: tool, Payload: map[string]any{}, Env: map[string]string{}}
		out, err := e.hook().Run(context.Background(), in)
		if err != nil || out.ExitCode != 0 {
			t.Fatalf("scorer must never block PreToolUse %s: err=%v exit=%d", tool, err, out.ExitCode)
		}
	}
}

func TestUnknownEventNoOp(t *testing.T) {
	e := newEnv(t)
	in := hooktype.HookInput{Event: "UserPromptSubmit", Payload: map[string]any{}, Env: map[string]string{}}
	if out, err := e.hook().Run(context.Background(), in); err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v out=%+v", err, out)
	}
}

func TestName(t *testing.T) {
	if got := planqualityscore.New(t.TempDir(), "").Name(); got != "plan-quality-score" {
		t.Fatalf("Name()=%q", got)
	}
}

// A YAML type error must not silently turn mode: block into surface (bash's
// awk reads the block leniently and would block). Fail closed to block.
func TestUnparseableConfigFailsClosedToBlock(t *testing.T) {
	e := newEnv(t)
	e.yml(t, "plan_quality:\n  mode: block\n  enabled: notabool\n")
	e.writePlan(t, "0.10", "id: p-bad-cfg\n", old)
	e.run(t, "Write", nil)
	if _, ok := e.marker(t); !ok {
		t.Fatal("unparseable plan_quality config must fail closed to block")
	}
}
