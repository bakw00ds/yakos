package status

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- workdir resolution -----------------------------------------------------

func TestResolveWorkDir_Explicit(t *testing.T) {
	env := map[string]string{
		"YAKOS_WORK_DIR": "/tmp/explicit-work",
	}
	got := resolveWorkDir("myproject", "/home/user", env)
	if got != "/tmp/explicit-work" {
		t.Errorf("expected /tmp/explicit-work, got %q", got)
	}
}

func TestResolveWorkDir_Inplace(t *testing.T) {
	env := map[string]string{
		"YAKOS_INPLACE_WORK":  "1",
		"CLAUDE_PROJECT_DIR":  "/repos/myapp",
	}
	got := resolveWorkDir("myproject", "/home/user", env)
	if got != "/repos/myapp/work" {
		t.Errorf("expected /repos/myapp/work, got %q", got)
	}
}

func TestResolveWorkDir_Canonical(t *testing.T) {
	env := map[string]string{}
	got := resolveWorkDir("myproject", "/home/user", env)
	if got != "/home/user/agent-control/myproject/work" {
		t.Errorf("expected /home/user/agent-control/myproject/work, got %q", got)
	}
}

// ---- session age ------------------------------------------------------------

func TestComputeSessionAge_NotStarted(t *testing.T) {
	label, warn := computeSessionAge("/does/not/exist", "/does/not/exist/history.ndjson", time.Now(), "myproj")
	if label != "not started" {
		t.Errorf("expected 'not started', got %q", label)
	}
	if warn != "" {
		t.Errorf("expected no warning, got %q", warn)
	}
}

func TestComputeSessionAge_SecondsAgo(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().UTC().Add(-30 * time.Second).Format(time.RFC3339)
	f := filepath.Join(dir, ".session-started")
	if err := os.WriteFile(f, []byte(started+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	label, warn := computeSessionAge(f, dir+"/history.ndjson", time.Now(), "myproj")
	if !strings.Contains(label, "s ago") {
		t.Errorf("expected 's ago' in label, got %q", label)
	}
	if warn != "" {
		t.Errorf("expected no warning for <4h session, got %q", warn)
	}
}

func TestComputeSessionAge_MinutesAgo(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().UTC().Add(-90 * time.Second).Format(time.RFC3339)
	f := filepath.Join(dir, ".session-started")
	if err := os.WriteFile(f, []byte(started+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	label, _ := computeSessionAge(f, dir+"/history.ndjson", time.Now(), "myproj")
	if !strings.Contains(label, "m ago") {
		t.Errorf("expected 'm ago' in label, got %q", label)
	}
}

func TestComputeSessionAge_HoursAgo(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().UTC().Add(-2*time.Hour - 5*time.Minute).Format(time.RFC3339)
	f := filepath.Join(dir, ".session-started")
	if err := os.WriteFile(f, []byte(started+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	label, _ := computeSessionAge(f, dir+"/history.ndjson", time.Now(), "myproj")
	if !strings.Contains(label, "h ") || !strings.Contains(label, "m ago") {
		t.Errorf("expected 'Xh Ym ago' in label, got %q", label)
	}
}

func TestComputeSessionAge_Over4h_Warning(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().UTC().Add(-5 * time.Hour).Format(time.RFC3339)
	f := filepath.Join(dir, ".session-started")
	if err := os.WriteFile(f, []byte(started+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, warn := computeSessionAge(f, dir+"/history.ndjson", time.Now(), "myproj")
	if !strings.Contains(warn, "session age >4h") {
		t.Errorf("expected >4h warning, got %q", warn)
	}
}

func TestComputeSessionAge_HistoryFallback(t *testing.T) {
	dir := t.TempDir()
	ts := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	historyContent := `{"event":"team_created","ts":"` + ts + `"}` + "\n"
	histFile := filepath.Join(dir, ".session-started-history.ndjson")
	if err := os.WriteFile(histFile, []byte(historyContent), 0644); err != nil {
		t.Fatal(err)
	}
	label, _ := computeSessionAge(dir+"/.session-started", histFile, time.Now(), "myproj")
	if !strings.Contains(label, "m ago") {
		t.Errorf("expected 'm ago' from history fallback, got %q", label)
	}
}

// ---- file age ---------------------------------------------------------------

func TestFileAgeLabel_Absent(t *testing.T) {
	got := fileAgeLabel("/does/not/exist.md", time.Now())
	if got != "absent" {
		t.Errorf("expected 'absent', got %q", got)
	}
}

func TestFileAgeLabel_Recent(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(f, []byte("# plan"), 0644); err != nil {
		t.Fatal(err)
	}
	got := fileAgeLabel(f, time.Now().Add(10*time.Second))
	// File was just created; age should be ≤10s.
	// On coarse-resolution clocks the age might be "0s ago" — accept either.
	if got == "absent" {
		t.Errorf("expected an age string, got %q", got)
	}
}

// ---- hook log parsing -------------------------------------------------------

func TestCollectHooks_NoLogsDir(t *testing.T) {
	hooks := collectHooks("/does/not/exist/logs")
	if len(hooks) != 1 || hooks[0].Name != "(no hook logs yet)" {
		t.Errorf("expected placeholder entry; got %v", hooks)
	}
}

func TestCollectHooks_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	hooks := collectHooks(dir)
	if len(hooks) != 1 || hooks[0].Name != "(no hook logs yet)" {
		t.Errorf("expected placeholder for empty dir; got %v", hooks)
	}
}

func TestCollectHooks_WithLog(t *testing.T) {
	dir := t.TempDir()
	content := `{"decision":"pass","ts":"2026-06-01T10:00:00Z"}` + "\n" +
		`{"decision":"pass","ts":"2026-06-01T10:01:00Z"}` + "\n" +
		`{"decision":"block","ts":"2026-06-01T10:02:00Z"}` + "\n"
	logFile := filepath.Join(dir, "secret-scan.ndjson")
	if err := os.WriteFile(logFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	hooks := collectHooks(dir)
	if len(hooks) != 1 {
		t.Fatalf("expected 1 hook entry; got %d", len(hooks))
	}
	h := hooks[0]
	if h.Name != "secret-scan" {
		t.Errorf("hook name: want 'secret-scan', got %q", h.Name)
	}
	if !strings.Contains(h.Summary, "2 pass") {
		t.Errorf("summary missing '2 pass'; got %q", h.Summary)
	}
	if !strings.Contains(h.Summary, "1 block") {
		t.Errorf("summary missing '1 block'; got %q", h.Summary)
	}
	if h.LastTS != "2026-06-01T10:02:00Z" {
		t.Errorf("lastTS: want '2026-06-01T10:02:00Z', got %q", h.LastTS)
	}
}

func TestCollectHooks_SortedOrder(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"zzz.ndjson", "aaa.ndjson", "mmm.ndjson"} {
		content := `{"decision":"pass","ts":"2026-06-01T10:00:00Z"}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	hooks := collectHooks(dir)
	if len(hooks) != 3 {
		t.Fatalf("expected 3 hooks; got %d", len(hooks))
	}
	if hooks[0].Name != "aaa" || hooks[1].Name != "mmm" || hooks[2].Name != "zzz" {
		t.Errorf("hooks not sorted: %v %v %v", hooks[0].Name, hooks[1].Name, hooks[2].Name)
	}
}

// ---- bypass counting --------------------------------------------------------

func TestCountBypasses_NoFile(t *testing.T) {
	n := countBypasses("/does/not/exist/hook-bypass.md")
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
}

func TestCountBypasses_WithActive(t *testing.T) {
	content := `# hook-bypass.md

## Format documentation
## bypass: example (NOT counted — in doc section)

## Active entries
## bypass: secret-scan-2026-06-01
## bypass: another-bypass
`
	dir := t.TempDir()
	f := filepath.Join(dir, "hook-bypass.md")
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	n := countBypasses(f)
	if n != 2 {
		t.Errorf("expected 2 active bypasses, got %d", n)
	}
}

func TestCountBypasses_NoActiveSection(t *testing.T) {
	content := `# hook-bypass.md
## bypass: orphan-not-counted
`
	dir := t.TempDir()
	f := filepath.Join(dir, "hook-bypass.md")
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	n := countBypasses(f)
	if n != 0 {
		t.Errorf("expected 0 (no 'Active entries' section), got %d", n)
	}
}

// ---- mailbox ----------------------------------------------------------------

func TestCountLines_NoFile(t *testing.T) {
	n := countLines("/does/not/exist/messages.ndjson")
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
}

func TestCountLines_ThreeLines(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "messages.ndjson")
	content := `{"type":"message"}` + "\n" +
		`{"type":"message"}` + "\n" +
		`{"type":"message"}` + "\n"
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	n := countLines(f)
	if n != 3 {
		t.Errorf("expected 3, got %d", n)
	}
}

// ---- memory label -----------------------------------------------------------

func TestComputeMemoryLabel_None(t *testing.T) {
	dir := t.TempDir()
	label := computeMemoryLabel(dir)
	if label != "not configured" {
		t.Errorf("expected 'not configured', got %q", label)
	}
}

func TestComputeMemoryLabel_TwoProjects(t *testing.T) {
	dir := t.TempDir()
	for _, proj := range []string{"proj-a", "proj-b"} {
		p := filepath.Join(dir, ".claude", "projects", proj)
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "MEMORY.md"), []byte("# mem"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	label := computeMemoryLabel(dir)
	if !strings.Contains(label, "2 MEMORY.md") {
		t.Errorf("expected '2 MEMORY.md file(s)...', got %q", label)
	}
}

// ---- Format -----------------------------------------------------------------

func TestFormat_EmptyReport(t *testing.T) {
	rpt := &Report{
		Project:      "testproj",
		ControlDir:   "/home/user/agent-control/testproj",
		SessionAge:   "not started",
		ScratchSize:  "(empty)",
		ArchiveSize:  "(empty)",
		PlanAge:      "absent",
		ContractsAge: "absent",
		DecisionsAge: "absent",
		Hooks:        []HookEntry{{Name: "(no hook logs yet)"}},
		MemoryLabel:  "not configured",
	}

	var buf bytes.Buffer
	if err := Format(&buf, rpt); err != nil {
		t.Fatalf("Format error: %v", err)
	}

	out := buf.String()
	// Must start with blank line.
	if !strings.HasPrefix(out, "\n") {
		t.Errorf("output should start with blank line; got %q", out[:min(len(out), 20)])
	}
	// Must contain project name.
	if !strings.Contains(out, "Project: testproj") {
		t.Errorf("missing 'Project: testproj' in output:\n%s", out)
	}
	// Must end with blank line.
	if !strings.HasSuffix(out, "\n\n") {
		t.Errorf("output should end with blank line; got %q", out[max(0, len(out)-10):])
	}
}

func TestFormat_WithWarnings(t *testing.T) {
	rpt := &Report{
		Project:      "testproj",
		ControlDir:   "/home/user/agent-control/testproj",
		SessionAge:   "started 2026-06-01T00:00:00Z (5h 0m ago)",
		SessionWarn:  "session age >4h. consider 'yakos team restart testproj' for context hygiene.",
		ScratchSize:  "(empty)",
		ScratchWarn:  "scratchpad >100MB. archive recommended.",
		ArchiveSize:  "(empty)",
		PlanAge:      "absent",
		ContractsAge: "absent",
		DecisionsAge: "absent",
		Hooks:        []HookEntry{{Name: "(no hook logs yet)"}},
		MemoryLabel:  "not configured",
	}

	var buf bytes.Buffer
	if err := Format(&buf, rpt); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "⚠ session age >4h") {
		t.Errorf("missing session warning; output:\n%s", out)
	}
	if !strings.Contains(out, "⚠ scratchpad >100MB") {
		t.Errorf("missing scratch warning; output:\n%s", out)
	}
}

func TestFormat_WithHooks(t *testing.T) {
	rpt := &Report{
		Project:      "testproj",
		ControlDir:   "/home/user/agent-control/testproj",
		SessionAge:   "not started",
		ScratchSize:  "(empty)",
		ArchiveSize:  "(empty)",
		PlanAge:      "absent",
		ContractsAge: "absent",
		DecisionsAge: "absent",
		Hooks: []HookEntry{
			{Name: "secret-scan", Summary: "2 pass, 1 block", LastTS: "2026-06-01T10:00:00Z"},
		},
		MemoryLabel: "not configured",
	}

	var buf bytes.Buffer
	if err := Format(&buf, rpt); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "    secret-scan: 2 pass, 1 block (last 2026-06-01T10:00:00Z)") {
		t.Errorf("missing hook line; output:\n%s", out)
	}
}

func TestFormat_DecisionsWarn(t *testing.T) {
	rpt := &Report{
		Project:       "testproj",
		ControlDir:    "/home/user/agent-control/testproj",
		SessionAge:    "not started",
		ScratchSize:   "(empty)",
		ArchiveSize:   "(empty)",
		PlanAge:       "absent",
		ContractsAge:  "absent",
		DecisionsAge:  "3h ago",
		DecisionsWarn: " ⚠",
		Hooks:         []HookEntry{{Name: "(no hook logs yet)"}},
		MemoryLabel:   "not configured",
	}

	var buf bytes.Buffer
	if err := Format(&buf, rpt); err != nil {
		t.Fatalf("Format error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "  Decisions:   3h ago ⚠") {
		t.Errorf("missing decisions warning; output:\n%s", out)
	}
}

// ---- helpers ----------------------------------------------------------------

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
