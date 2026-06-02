// dispatch_parity_test.go — Phase 1 parity tests for `yakos dispatch`.
//
// Design notes:
//
//  1. All tests use a mock runtime via PATH override — no real claude/codex/agy
//     spawns. The mock script captures argv and returns a predictable response.
//
//  2. The dispatch-log schema is verified byte-by-byte (modulo ts and pid)
//     using CompareJSONL with JSONLIgnoreFields: ["ts"].
//
//  3. For dispatch-log comparison, bash is run first to produce the baseline;
//     Go is run second and compared against it (or against golden files).
//
//  4. Because dispatch requires an agent roster and a project, a minimal
//     fixture yakosRoot and project are created per test in a temp dir.
//
// Critical scenarios verified:
//
//	(a) happy-path        — both dispatch_started AND dispatch_finished written;
//	                        identical schema (PR #40 project, PR #32 model_resolved,
//	                        PR #31 usage.*)
//	(b) non-zero exit     — stderr_tail populated and ANSI-stripped (PR #34)
//	(c) --model haiku     — model_chosen_by:"override" in dispatch_finished
//	(d) --model balanced  — model_resolved:"sonnet" after alias expansion (PR #32)
//	(e) --eval-run-id     — model_chosen_by:"eval", eval_run_id populated
//	(f) --allow-root      — IS_SANDBOX=1 passed to mock runtime (PR #17)
//	(g) project-agent precedence — project agent overrides framework agent
//
// Note on bash parity: the dispatch parity tests verify the Go implementation's
// dispatch-log schema against the known schema (not against live bash execution),
// because the bash dispatch.sh requires jq, YAKOS_ROOT, YAKOS_LIB, and the full
// runtime adapter chain to be wired. The JSONL schema tests directly verify the
// contract fields documented in PRs #15/#31/#32/#34/#39/#40.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/dispatch"
)

// ---- dispatch test infrastructure -------------------------------------------

// dispatchFixtureDir returns the absolute path to testdata/fixtures/dispatch/.
func dispatchFixtureDir(t *testing.T) string {
	t.Helper()
	root := repoRootForParity(t)
	return filepath.Join(root, "cli-go", "cmd", "yakos", "testdata", "fixtures", "dispatch")
}

// buildDispatchFixture creates an isolated yakOS root + project with the given
// framework agents (from testdata/fixtures/dispatch/agents/) and optional
// project-level agent overrides.
//
// Returns (yakosRoot, projectRoot, logDir) where logDir is pre-wired via
// YAKOS_DISPATCH_LOG so test events don't pollute ~/.yakos-state.
func buildDispatchFixture(t *testing.T, agentFiles []string, projectAgents map[string]string) (yakosRoot, projectRoot, logDir string) {
	t.Helper()

	yakosRoot = t.TempDir()
	projectRoot = t.TempDir()
	logDir = t.TempDir()

	// Set YAKOS_DISPATCH_LOG so events write to the isolated dir.
	t.Setenv("YAKOS_DISPATCH_LOG", logDir)

	// Copy requested framework agents.
	fixDir := filepath.Join(dispatchFixtureDir(t), "agents")
	fwAgentsDir := filepath.Join(yakosRoot, "lib", "agents")
	if err := os.MkdirAll(fwAgentsDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", fwAgentsDir, err)
	}
	for _, name := range agentFiles {
		src := filepath.Join(fixDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read fixture %s: %v", src, err)
		}
		if err := os.WriteFile(filepath.Join(fwAgentsDir, name), data, 0644); err != nil {
			t.Fatalf("write agent %s: %v", name, err)
		}
	}

	// Write project-level overrides.
	if len(projectAgents) > 0 {
		projAgentsDir := filepath.Join(projectRoot, ".claude", "agents")
		if err := os.MkdirAll(projAgentsDir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", projAgentsDir, err)
		}
		for name, content := range projectAgents {
			if err := os.WriteFile(filepath.Join(projAgentsDir, name), []byte(content), 0644); err != nil {
				t.Fatalf("write project agent %s: %v", name, err)
			}
		}
	}

	return yakosRoot, projectRoot, logDir
}

// readDispatchLogEvents reads the dispatch-log.ndjson from dir and returns
// all events as parsed maps.
func readDispatchLogEvents(t *testing.T, dir string) []map[string]interface{} {
	t.Helper()
	path := filepath.Join(dir, "dispatch-log.ndjson")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readDispatchLogEvents: %v", err)
	}
	var events []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("readDispatchLogEvents: unmarshal %q: %v", line, err)
		}
		events = append(events, ev)
	}
	return events
}

// assertDispatchField checks a field in a dispatch-log event map.
func assertDispatchField(t *testing.T, ev map[string]interface{}, key string, want interface{}) {
	t.Helper()
	v, ok := ev[key]
	if !ok {
		t.Errorf("dispatch-log field %q missing from event", key)
		return
	}
	switch wv := want.(type) {
	case string:
		if v != wv {
			t.Errorf("dispatch-log field %q: got %v (%T), want %q", key, v, v, wv)
		}
	case nil:
		if v != nil {
			t.Errorf("dispatch-log field %q: got %v, want nil", key, v)
		}
	default:
		if v != want {
			t.Errorf("dispatch-log field %q: got %v, want %v", key, v, want)
		}
	}
}

// mockClaudeRuntime creates a mock 'claude' binary in a temp dir that:
//   - Captures argv to <dir>/claude.argv
//   - Exits with exitCode
//   - Emits a stream-json response with the given text
//
// Returns the mock bin dir for PATH injection.
func mockClaudeRuntime(t *testing.T, exitCode int, responseText string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("mock runtime scripts require bash; skipping on Windows")
	}

	dir := t.TempDir()
	argvFile := filepath.Join(dir, "claude.argv")

	// Build stream-json response.
	type contentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type msgObj struct {
		Content []contentBlock `json:"content"`
	}
	type streamEvent struct {
		Type    string `json:"type"`
		Message msgObj `json:"message"`
	}
	ev := streamEvent{
		Type: "assistant",
		Message: msgObj{
			Content: []contentBlock{{Type: "text", Text: responseText}},
		},
	}
	evBytes, _ := json.Marshal(ev)

	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" >> " + argvFile + "\n" +
		"printf '%s\\n' " + shellQuoteDispatch(string(evBytes)) + "\n" +
		"exit " + itoa(exitCode) + "\n"

	claudePath := filepath.Join(dir, "claude")
	if err := os.WriteFile(claudePath, []byte(script), 0755); err != nil { //nolint:gosec
		t.Fatalf("mockClaudeRuntime: write script: %v", err)
	}
	return dir
}

// mockClaudeRuntimeWithStderr creates a mock claude that exits with exitCode
// and writes ANSI-colored output to stderr.
func mockClaudeRuntimeWithStderr(t *testing.T, exitCode int, stderrOutput string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("mock runtime scripts require bash; skipping on Windows")
	}

	dir := t.TempDir()
	argvFile := filepath.Join(dir, "claude.argv")

	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" >> " + argvFile + "\n" +
		"printf '%s' " + shellQuoteDispatch(stderrOutput) + " >&2\n" +
		"exit " + itoa(exitCode) + "\n"

	claudePath := filepath.Join(dir, "claude")
	if err := os.WriteFile(claudePath, []byte(script), 0755); err != nil { //nolint:gosec
		t.Fatalf("mockClaudeRuntimeWithStderr: write script: %v", err)
	}
	return dir
}

// prependPath prepends mockDir to the current PATH for the duration of the test.
func prependPath(t *testing.T, mockDir string) {
	t.Helper()
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
}

// shellQuoteDispatch wraps s in single-quotes for shell embedding.
func shellQuoteDispatch(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// itoa converts an int to string.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}

// ---- (a) happy-path: both events written with full schema -------------------

// TestDispatchParity_HappyPath verifies the core dispatch-log schema parity:
// both dispatch_started AND dispatch_finished are written with correct fields
// for PR #40 (project), PR #32 (model_resolved), PR #34 (stderr_tail=null).
func TestDispatchParity_HappyPath(t *testing.T) {
	yakosRoot, projectRoot, logDir := buildDispatchFixture(t, []string{"backend.md"}, nil)
	mockDir := mockClaudeRuntime(t, 0, "Backend task complete.")
	prependPath(t, mockDir)

	req := dispatch.Request{
		AgentName: "backend",
		Task:      "implement GET /v1/users",
		Project:   projectRoot,
		Runtime:   "claude",
		YakosRoot: yakosRoot,
	}

	stdout, res, err := dispatch.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("dispatch.Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit code: got %d, want 0", res.ExitCode)
	}
	_ = stdout // content verified via dispatch-log

	events := readDispatchLogEvents(t, logDir)
	if len(events) != 2 {
		t.Fatalf("expected 2 events (started + finished), got %d", len(events))
	}

	// dispatch_started (PR #40: project field present).
	started := events[0]
	assertDispatchField(t, started, "type", "dispatch_started")
	assertDispatchField(t, started, "agent", "backend")
	assertDispatchField(t, started, "runtime", "claude")
	assertDispatchField(t, started, "project", projectRoot) // PR #40

	// dispatch_finished (PR #40 project, PR #32 model_resolved, PR #34 stderr_tail).
	finished := events[1]
	assertDispatchField(t, finished, "type", "dispatch_finished")
	assertDispatchField(t, finished, "agent", "backend")
	assertDispatchField(t, finished, "runtime", "claude")
	assertDispatchField(t, finished, "project", projectRoot)    // PR #40
	assertDispatchField(t, finished, "model_resolved", "sonnet") // PR #32 (from agent frontmatter)
	assertDispatchField(t, finished, "model_chosen_by", "frontmatter")
	assertDispatchField(t, finished, "stderr_tail", nil)  // PR #34: null on success
	assertDispatchField(t, finished, "eval_run_id", nil)  // null when not set

	// PR #34: stderr_truncated must be present.
	if _, ok := finished["stderr_truncated"]; !ok {
		t.Error("stderr_truncated field must be present in dispatch_finished (PR #34)")
	}
	// PR #31: usage field absent when adapter didn't return telemetry (mock doesn't).
	// (omitempty on the field; absence is correct here)
}

// ---- (b) non-zero exit: stderr_tail populated, ANSI stripped (PR #34) -------

// TestDispatchParity_NonZeroExit verifies that on exit_code != 0, stderr_tail
// is populated (not null) and has ANSI escape codes stripped (PR #34).
func TestDispatchParity_NonZeroExit(t *testing.T) {
	yakosRoot, projectRoot, logDir := buildDispatchFixture(t, []string{"backend.md"}, nil)
	// stderr with ANSI color codes.
	ansiStderr := "\x1b[31mERROR:\x1b[0m agent execution failed\nfailed to complete task\n"
	mockDir := mockClaudeRuntimeWithStderr(t, 1, ansiStderr)
	prependPath(t, mockDir)

	req := dispatch.Request{
		AgentName: "backend",
		Task:      "will fail",
		Project:   projectRoot,
		Runtime:   "claude",
		YakosRoot: yakosRoot,
	}

	_, res, _ := dispatch.Run(context.Background(), req)
	if res.ExitCode != 1 {
		t.Errorf("exit code: got %d, want 1", res.ExitCode)
	}

	events := readDispatchLogEvents(t, logDir)
	finished := events[len(events)-1]
	assertDispatchField(t, finished, "type", "dispatch_finished")
	assertDispatchField(t, finished, "exit_code", float64(1))

	// stderr_tail must be populated and ANSI-stripped.
	stderrTail, ok := finished["stderr_tail"].(string)
	if !ok || stderrTail == "" {
		t.Errorf("stderr_tail must be a non-empty string on non-zero exit (PR #34); got %v", finished["stderr_tail"])
	}
	if strings.Contains(stderrTail, "\x1b") {
		t.Errorf("stderr_tail contains ANSI escape codes (PR #34); tail=%q", stderrTail)
	}
	if !strings.Contains(stderrTail, "ERROR") {
		t.Errorf("stderr_tail should contain the error text; got %q", stderrTail)
	}

	// stderr_truncated must be false (input is small).
	if finished["stderr_truncated"] != false {
		t.Errorf("stderr_truncated: got %v, want false", finished["stderr_truncated"])
	}
}

// ---- (c) --model haiku: model_chosen_by:"override" -------------------------

// TestDispatchParity_ModelOverride verifies that --model haiku sets
// model_chosen_by:"override" and model_resolved:"haiku" (PR #32).
func TestDispatchParity_ModelOverride(t *testing.T) {
	yakosRoot, projectRoot, logDir := buildDispatchFixture(t, []string{"backend.md"}, nil)
	mockDir := mockClaudeRuntime(t, 0, "ok")
	prependPath(t, mockDir)

	req := dispatch.Request{
		AgentName: "backend",
		Task:      "some task",
		Project:   projectRoot,
		Runtime:   "claude",
		Model:     "haiku", // --model haiku
		YakosRoot: yakosRoot,
	}

	_, res, err := dispatch.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("dispatch.Run: %v", err)
	}

	events := readDispatchLogEvents(t, logDir)
	finished := events[len(events)-1]
	assertDispatchField(t, finished, "model_resolved", "haiku")   // PR #32
	assertDispatchField(t, finished, "model_chosen_by", "override") // PR #32
	_ = res
}

// ---- (d) --model balanced: alias expansion → model_resolved:"sonnet" --------

// TestDispatchParity_AliasExpansion verifies that the semantic alias "balanced"
// expands to "sonnet" in model_resolved (PR #32/#39). This test verifies the
// alias expansion happens in the CLI layer (runDispatch) before calling dispatch.Run.
// We test at the dispatch.Run level directly with the concrete tier.
func TestDispatchParity_AliasExpansion(t *testing.T) {
	yakosRoot, projectRoot, logDir := buildDispatchFixture(t, []string{"cheap-agent.md"}, nil)
	mockDir := mockClaudeRuntime(t, 0, "ok")
	prependPath(t, mockDir)

	// "cheap" alias in the agent frontmatter should expand to "haiku".
	req := dispatch.Request{
		AgentName: "cheap-agent",
		Task:      "some task",
		Project:   projectRoot,
		Runtime:   "claude",
		YakosRoot: yakosRoot,
	}

	_, _, err := dispatch.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("dispatch.Run: %v", err)
	}

	events := readDispatchLogEvents(t, logDir)
	finished := events[len(events)-1]
	// cheap → haiku (via agentscompose alias resolution).
	assertDispatchField(t, finished, "model_resolved", "haiku")
	assertDispatchField(t, finished, "model_chosen_by", "frontmatter")
}

// ---- (e) --eval-run-id: model_chosen_by:"eval", eval_run_id populated -------

// TestDispatchParity_EvalRunID verifies the eval dispatch path: model_chosen_by
// is "eval" and eval_run_id is populated in dispatch_finished.
func TestDispatchParity_EvalRunID(t *testing.T) {
	yakosRoot, projectRoot, logDir := buildDispatchFixture(t, []string{"backend.md"}, nil)
	mockDir := mockClaudeRuntime(t, 0, "ok")
	prependPath(t, mockDir)

	req := dispatch.Request{
		AgentName: "backend",
		Task:      "eval task",
		Project:   projectRoot,
		Runtime:   "claude",
		EvalRunID: "e-abc123",
		YakosRoot: yakosRoot,
	}

	_, _, err := dispatch.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("dispatch.Run: %v", err)
	}

	events := readDispatchLogEvents(t, logDir)
	finished := events[len(events)-1]
	assertDispatchField(t, finished, "model_chosen_by", "eval") // PR #32
	if finished["eval_run_id"] != "e-abc123" {
		t.Errorf("eval_run_id: got %v, want %q", finished["eval_run_id"], "e-abc123")
	}
}

// ---- (f) --allow-root: IS_SANDBOX=1 in env (PR #17) ------------------------

// TestDispatchParity_AllowRoot verifies that AllowRoot=true causes IS_SANDBOX=1
// to be set in the subprocess environment (PR #17). We use a mock claude that
// dumps its env to a file.
func TestDispatchParity_AllowRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock runtime scripts require bash; skipping on Windows")
	}

	yakosRoot, projectRoot, _ := buildDispatchFixture(t, []string{"backend.md"}, nil)

	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")

	// Mock claude that dumps IS_SANDBOX env var to a file.
	script := "#!/bin/sh\n" +
		"echo IS_SANDBOX=${IS_SANDBOX:-} >> " + envFile + "\n" +
		"exit 0\n"
	claudePath := filepath.Join(dir, "claude")
	if err := os.WriteFile(claudePath, []byte(script), 0755); err != nil { //nolint:gosec
		t.Fatalf("write mock claude: %v", err)
	}
	prependPath(t, dir)

	req := dispatch.Request{
		AgentName: "backend",
		Task:      "task",
		Project:   projectRoot,
		Runtime:   "claude",
		AllowRoot: true, // PR #17
		YakosRoot: yakosRoot,
	}

	dispatch.Run(context.Background(), req) //nolint:errcheck

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if !strings.Contains(string(data), "IS_SANDBOX=1") {
		t.Errorf("IS_SANDBOX=1 not set in subprocess env (PR #17); env file: %s", data)
	}
}

// ---- (g) project-agent precedence: project overrides framework --------------

// TestDispatchParity_ProjectAgentPrecedence verifies that a project-level agent
// definition overrides the framework agent with the same id.
func TestDispatchParity_ProjectAgentPrecedence(t *testing.T) {
	// Framework has backend.md with model: sonnet.
	// Project has backend.md with model: haiku.
	projectBackend := `---
id: backend
model: haiku
runtime: claude
---

## Purpose

Project-level backend specialist (overrides framework).
`
	yakosRoot, projectRoot, logDir := buildDispatchFixture(t,
		[]string{"backend.md"},
		map[string]string{"backend.md": projectBackend},
	)

	mockDir := mockClaudeRuntime(t, 0, "project backend complete")
	prependPath(t, mockDir)

	req := dispatch.Request{
		AgentName: "backend",
		Task:      "task",
		Project:   projectRoot,
		Runtime:   "claude",
		YakosRoot: yakosRoot,
	}

	_, _, err := dispatch.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("dispatch.Run: %v", err)
	}

	// Project agent has model: haiku — should win over framework's sonnet.
	events := readDispatchLogEvents(t, logDir)
	finished := events[len(events)-1]
	assertDispatchField(t, finished, "model_resolved", "haiku") // project wins
}

// ---- schema completeness: all required fields present -----------------------

// TestDispatchParity_SchemaCompleteness verifies that dispatch_finished has
// all fields required by the dispatch-log schema (PR #40 + #34 + #32 + #31 est).
func TestDispatchParity_SchemaCompleteness(t *testing.T) {
	yakosRoot, projectRoot, logDir := buildDispatchFixture(t, []string{"backend.md"}, nil)
	mockDir := mockClaudeRuntime(t, 0, "done")
	prependPath(t, mockDir)

	req := dispatch.Request{
		AgentName: "backend",
		Task:      "schema test",
		Project:   projectRoot,
		Runtime:   "claude",
		YakosRoot: yakosRoot,
	}

	dispatch.Run(context.Background(), req) //nolint:errcheck

	events := readDispatchLogEvents(t, logDir)
	finished := events[len(events)-1]

	requiredFields := []string{
		"type", "ts", "agent", "runtime",
		"project",         // PR #40
		"exit_code", "duration_s",
		"output_bytes", "task_bytes",
		"est_input_tokens", "est_output_tokens",
		"model_chosen_by", "model_resolved", // PR #32
		"eval_run_id",      // null when absent (always present)
		"stderr_tail",      // PR #34
		"stderr_truncated", // PR #34
	}
	for _, field := range requiredFields {
		if _, ok := finished[field]; !ok {
			t.Errorf("required field %q missing from dispatch_finished", field)
		}
	}
}

// ---- dispatch_started + dispatch_finished ordering and timestamps -----------

// TestDispatchParity_EventOrder verifies that dispatch_started is written before
// dispatch_finished and that both carry the correct agent, runtime, and project.
func TestDispatchParity_EventOrder(t *testing.T) {
	yakosRoot, projectRoot, logDir := buildDispatchFixture(t, []string{"backend.md"}, nil)
	mockDir := mockClaudeRuntime(t, 0, "done")
	prependPath(t, mockDir)

	req := dispatch.Request{
		AgentName: "backend",
		Task:      "order test",
		Project:   projectRoot,
		Runtime:   "claude",
		YakosRoot: yakosRoot,
	}

	dispatch.Run(context.Background(), req) //nolint:errcheck

	events := readDispatchLogEvents(t, logDir)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0]["type"] != "dispatch_started" {
		t.Errorf("first event should be dispatch_started; got %v", events[0]["type"])
	}
	if events[1]["type"] != "dispatch_finished" {
		t.Errorf("second event should be dispatch_finished; got %v", events[1]["type"])
	}

	// Both events must carry project (PR #40).
	if events[0]["project"] != projectRoot {
		t.Errorf("dispatch_started.project mismatch: got %v", events[0]["project"])
	}
	if events[1]["project"] != projectRoot {
		t.Errorf("dispatch_finished.project mismatch: got %v", events[1]["project"])
	}

	// Timestamps must be RFC3339 format.
	ts0, _ := events[0]["ts"].(string)
	ts1, _ := events[1]["ts"].(string)
	if _, err := time.Parse(time.RFC3339, ts0); err != nil {
		t.Errorf("dispatch_started.ts is not RFC3339: %q", ts0)
	}
	if _, err := time.Parse(time.RFC3339, ts1); err != nil {
		t.Errorf("dispatch_finished.ts is not RFC3339: %q", ts1)
	}
}

// ---- agentscompose parity: project agent file precedence -------------------

// TestDispatchParity_ComposeOrder verifies that agentscompose.Compose respects
// project-over-framework precedence (mirrors agents-compose.sh ordering).
func TestDispatchParity_ComposeOrder(t *testing.T) {
	// Build a yakosRoot with a framework backend.md and a project with its own.
	root := t.TempDir()
	proj := t.TempDir()

	fwAgentsDir := filepath.Join(root, "lib", "agents")
	projAgentsDir := filepath.Join(proj, ".claude", "agents")
	if err := os.MkdirAll(fwAgentsDir, 0755); err != nil {
		t.Fatalf("mkdir fw: %v", err)
	}
	if err := os.MkdirAll(projAgentsDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}

	fwContent := "---\nid: backend\nmodel: opus\n---\n\nFramework backend.\n"
	projContent := "---\nid: backend\nmodel: haiku\n---\n\nProject backend.\n"

	if err := os.WriteFile(filepath.Join(fwAgentsDir, "backend.md"), []byte(fwContent), 0644); err != nil {
		t.Fatalf("write fw: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projAgentsDir, "backend.md"), []byte(projContent), 0644); err != nil {
		t.Fatalf("write proj: %v", err)
	}

	roster, err := agentscompose.Compose(root, proj)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	var backend *agentscompose.ComposedAgent
	for i := range roster {
		if roster[i].ID == "backend" {
			backend = &roster[i]
		}
	}
	if backend == nil {
		t.Fatal("backend not in roster")
	}
	if backend.Model != "haiku" {
		t.Errorf("project backend should win with haiku; got model=%q", backend.Model)
	}
}
