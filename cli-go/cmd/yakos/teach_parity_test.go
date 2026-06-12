// teach_parity_test.go — Phase 1 parity tests for `yakos teach`.
//
// Design notes:
//
//  1. All tests call teach.Run directly with temp-dir paths; no bash runtime
//     or real ~/.claude/agents directory is required.
//
//  2. The bash teach.sh appends a lesson bullet to a project agent's
//     ## Lessons learned section. The Go port mirrors the same behaviour:
//     dated bullet, section creation when absent, two-pass splice when
//     present, atomic temp-rename write, backup before edit.
//
//  3. Parity is verified behaviourally (output shape, error conditions,
//     filesystem effects) rather than byte-for-byte because timestamps and
//     temp paths differ per test run.
//
// Critical scenarios:
//
//	(a) happy path — section absent, section created               → new section + bullet
//	(b) happy path — section present (last H2)                     → bullet appended at EOF of section
//	(c) happy path — section present (middle H2)                   → bullet inserted before next H2
//	(d) custom --section                                            → custom heading used
//	(e) --dry-run                                                   → no write; output contains "would append"
//	(f) missing <agent-name>                                        → error
//	(g) missing <lesson-file>                                       → error
//	(h) lesson file not found                                       → error
//	(i) agent file not found                                        → error with bootstrap hint
//	(j) no --project and no inferable project                       → error "--project required"
//	(k) backup file created and contains original content           → verified
//	(l) multi-line lesson indents continuation lines                → "  line" prefix
//	(m) multiple runs accumulate bullets (not deduplicated)         → both bullets present
//	(n) help text contains load-bearing phrases                     → all key phrases present
//	(o) output confirms target file on success                      → "appended lesson to"
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/teach"
)

// ---- helpers ----------------------------------------------------------------

var teachFixedNow = time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

func newTeachConfig(t *testing.T, projectDir, agentName, lessonFile string) teach.Config {
	t.Helper()
	return teach.Config{
		AgentName:  agentName,
		LessonFile: lessonFile,
		ProjectDir: projectDir,
		Now:        teachFixedNow,
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
}

func teachOut(cfg teach.Config) string {
	return cfg.Writer.(*bytes.Buffer).String()
}

// makeTeachAgent creates <dir>/.claude/agents/<name>.md with content.
func makeTeachAgent(t *testing.T, dir, name, content string) string {
	t.Helper()
	agentsDir := filepath.Join(dir, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	path := filepath.Join(agentsDir, name+".md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	return path
}

// makeTeachLesson writes body to a temp lesson.md and returns its path.
func makeTeachLesson(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "lesson.md")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write lesson: %v", err)
	}
	return path
}

// ---- scenario (a): happy path, section absent -------------------------------

func TestTeachParity_HappyPath_SectionAbsent(t *testing.T) {
	dir := t.TempDir()
	lesson := makeTeachLesson(t, dir, "always write the audit-log entry")
	makeTeachAgent(t, dir, "backend", "# backend\n\n## Purpose\n\nDo backend things.\n")

	cfg := newTeachConfig(t, dir, "backend", lesson)
	res, err := teach.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.SectionCreated {
		t.Error("expected SectionCreated=true")
	}

	data, _ := os.ReadFile(res.AgentFile)
	body := string(data)
	if !strings.Contains(body, "## Lessons learned") {
		t.Error("section heading missing")
	}
	if !strings.Contains(body, "always write the audit-log entry") {
		t.Error("lesson text missing")
	}
	if !strings.Contains(body, "2026-06-02") {
		t.Error("date stamp missing")
	}
	// Original content preserved.
	if !strings.Contains(body, "## Purpose") {
		t.Error("original content lost")
	}
}

// ---- scenario (b): happy path, section present (last H2) -------------------

func TestTeachParity_HappyPath_SectionPresent_LastH2(t *testing.T) {
	dir := t.TempDir()
	lesson := makeTeachLesson(t, dir, "new lesson")
	content := "# backend\n\n## Lessons learned\n\n- **2025-01-01** — old lesson\n"
	makeTeachAgent(t, dir, "backend", content)

	cfg := newTeachConfig(t, dir, "backend", lesson)
	res, err := teach.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.SectionCreated {
		t.Error("expected SectionCreated=false")
	}

	data, _ := os.ReadFile(res.AgentFile)
	body := string(data)
	if !strings.Contains(body, "old lesson") {
		t.Error("old lesson not preserved")
	}
	if !strings.Contains(body, "new lesson") {
		t.Error("new lesson missing")
	}
	// New lesson must appear after old lesson.
	if strings.Index(body, "new lesson") < strings.Index(body, "old lesson") {
		t.Error("new lesson appears before old lesson")
	}
}

// ---- scenario (c): section present, middle H2 ------------------------------

func TestTeachParity_SectionPresent_MiddleH2_InsertsBeforeNext(t *testing.T) {
	dir := t.TempDir()
	lesson := makeTeachLesson(t, dir, "inserted lesson")
	content := "# backend\n\n## Lessons learned\n\n- old\n\n## Personality\n\nBe concise.\n"
	makeTeachAgent(t, dir, "backend", content)

	cfg := newTeachConfig(t, dir, "backend", lesson)
	res, err := teach.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, _ := os.ReadFile(res.AgentFile)
	body := string(data)
	lessonIdx := strings.Index(body, "inserted lesson")
	personalityIdx := strings.Index(body, "## Personality")
	if lessonIdx == -1 || personalityIdx == -1 {
		t.Fatalf("lesson or personality section missing; got:\n%s", body)
	}
	if lessonIdx > personalityIdx {
		t.Error("inserted lesson appears after ## Personality (should precede it)")
	}
	if !strings.Contains(body, "Be concise.") {
		t.Error("Personality body content was lost")
	}
}

// ---- scenario (d): custom --section ----------------------------------------

func TestTeachParity_CustomSection(t *testing.T) {
	dir := t.TempDir()
	lesson := makeTeachLesson(t, dir, "gotcha: typed clients drift from the API")
	makeTeachAgent(t, dir, "frontend", "# frontend\n\nbody\n")

	cfg := newTeachConfig(t, dir, "frontend", lesson)
	cfg.Section = "Project gotchas"
	res, err := teach.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Section != "Project gotchas" {
		t.Errorf("expected section='Project gotchas'; got %q", res.Section)
	}

	data, _ := os.ReadFile(res.AgentFile)
	if !strings.Contains(string(data), "## Project gotchas") {
		t.Error("custom section heading missing")
	}
	if !strings.Contains(string(data), "typed clients drift") {
		t.Error("lesson text missing from custom section")
	}
}

// ---- scenario (e): --dry-run -----------------------------------------------

func TestTeachParity_DryRun_NoWrite(t *testing.T) {
	dir := t.TempDir()
	lesson := makeTeachLesson(t, dir, "this must not be written")
	original := "# backend\n\nbody.\n"
	agentPath := makeTeachAgent(t, dir, "backend", original)

	cfg := newTeachConfig(t, dir, "backend", lesson)
	cfg.DryRun = true
	res, err := teach.Run(cfg)
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}

	// File must be unchanged.
	data, _ := os.ReadFile(agentPath)
	if string(data) != original {
		t.Error("dry-run must not modify the agent file")
	}
	// No backup.
	if res.BackupFile != "" {
		t.Errorf("dry-run must not create a backup; got %q", res.BackupFile)
	}
	// Output contains "would append".
	out := teachOut(cfg)
	if !strings.Contains(out, "would append") {
		t.Errorf("expected 'would append' in dry-run output; got: %q", out)
	}
	// Output contains the lesson text.
	if !strings.Contains(out, "this must not be written") {
		t.Errorf("expected lesson body in dry-run output; got: %q", out)
	}
}

// ---- scenario (f): missing <agent-name> ------------------------------------

func TestTeachParity_MissingAgentName_Error(t *testing.T) {
	dir := t.TempDir()
	lesson := makeTeachLesson(t, dir, "lesson")
	cfg := newTeachConfig(t, dir, "", lesson)
	_, err := teach.Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing AgentName")
	}
	if !strings.Contains(err.Error(), "agent-name") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (g): missing <lesson-file> ------------------------------------

func TestTeachParity_MissingLessonFile_Error(t *testing.T) {
	dir := t.TempDir()
	cfg := newTeachConfig(t, dir, "backend", "")
	_, err := teach.Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing LessonFile")
	}
	if !strings.Contains(err.Error(), "lesson-file") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (h): lesson file not found ------------------------------------

func TestTeachParity_LessonFileNotFound_Error(t *testing.T) {
	dir := t.TempDir()
	cfg := newTeachConfig(t, dir, "backend", filepath.Join(dir, "ghost.md"))
	_, err := teach.Run(cfg)
	if err == nil {
		t.Fatal("expected error for absent lesson file")
	}
	if !strings.Contains(err.Error(), "lesson file not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (i): agent file not found ------------------------------------

func TestTeachParity_AgentFileNotFound_Error(t *testing.T) {
	dir := t.TempDir()
	lesson := makeTeachLesson(t, dir, "lesson")
	// Agent file intentionally absent.
	cfg := newTeachConfig(t, dir, "does-not-exist", lesson)
	_, err := teach.Run(cfg)
	if err == nil {
		t.Fatal("expected error for absent agent file")
	}
	if !strings.Contains(err.Error(), "project agent not found") {
		t.Errorf("unexpected error: %v", err)
	}
	// Bootstrap hint present.
	if !strings.Contains(err.Error(), "yakos agent new") {
		t.Errorf("expected bootstrap hint in error; got: %v", err)
	}
}

// ---- scenario (j): no project, cannot infer --------------------------------

func TestTeachParity_NoProject_CannotInfer_Error(t *testing.T) {
	dir := t.TempDir()
	lesson := makeTeachLesson(t, dir, "lesson")
	cfg := teach.Config{
		AgentName:  "backend",
		LessonFile: lesson,
		ProjectDir: "",  // not supplied
		HomeDir:    dir, // no agent-control entries → inference fails
		Now:        teachFixedNow,
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
	_, err := teach.Run(cfg)
	if err == nil {
		t.Fatal("expected error when project cannot be inferred")
	}
	if !strings.Contains(err.Error(), "--project required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (k): backup file created -------------------------------------

func TestTeachParity_BackupCreatedAndContainsOriginal(t *testing.T) {
	dir := t.TempDir()
	lesson := makeTeachLesson(t, dir, "new lesson")
	original := "# agent\n\noriginal content\n"
	makeTeachAgent(t, dir, "agent", original)

	cfg := newTeachConfig(t, dir, "agent", lesson)
	res, err := teach.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	backupData, err := os.ReadFile(res.BackupFile)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backupData) != original {
		t.Error("backup does not contain the original content")
	}
}

// ---- scenario (l): multi-line lesson indentation ---------------------------

func TestTeachParity_MultiLineLesson_ContinuationIndented(t *testing.T) {
	dir := t.TempDir()
	body := "always write audit-log\nincluding actor, target, action\nno exceptions"
	lesson := makeTeachLesson(t, dir, body)
	makeTeachAgent(t, dir, "backend", "# backend\n\nbody.\n")

	cfg := newTeachConfig(t, dir, "backend", lesson)
	res, err := teach.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, _ := os.ReadFile(res.AgentFile)
	body2 := string(data)
	if !strings.Contains(body2, "always write audit-log") {
		t.Error("first line missing")
	}
	if !strings.Contains(body2, "  including actor") {
		t.Error("continuation line not indented by 2 spaces")
	}
	if !strings.Contains(body2, "  no exceptions") {
		t.Error("last continuation line not indented")
	}
}

// ---- scenario (m): multiple runs accumulate bullets ------------------------

func TestTeachParity_MultipleRuns_AccumulateBullets(t *testing.T) {
	dir := t.TempDir()
	lesson1 := makeTeachLesson(t, dir, "first lesson")
	lesson2 := filepath.Join(dir, "second.md")
	if err := os.WriteFile(lesson2, []byte("second lesson"), 0644); err != nil {
		t.Fatal(err)
	}
	makeTeachAgent(t, dir, "backend", "# backend\n\nbody.\n")

	cfg1 := newTeachConfig(t, dir, "backend", lesson1)
	if _, err := teach.Run(cfg1); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	cfg2 := newTeachConfig(t, dir, "backend", lesson2)
	if _, err := teach.Run(cfg2); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	agentPath := filepath.Join(dir, ".claude", "agents", "backend.md")
	data, _ := os.ReadFile(agentPath)
	body := string(data)
	if !strings.Contains(body, "first lesson") {
		t.Error("first lesson missing after second run")
	}
	if !strings.Contains(body, "second lesson") {
		t.Error("second lesson missing")
	}
}

// ---- scenario (n): help text contains load-bearing phrases -----------------

func TestTeachParity_HelpText_ContainsKeyPhrases(t *testing.T) {
	var buf bytes.Buffer
	teach.PrintHelp(&buf)
	help := buf.String()

	phrases := []string{
		"yakos teach",
		"<agent-name>",
		"<lesson-file>",
		"--project",
		"--section",
		"--dry-run",
		"Lessons learned",
		"yakos agent new",
	}
	for _, p := range phrases {
		if !strings.Contains(help, p) {
			t.Errorf("help text missing phrase %q", p)
		}
	}
}

// ---- scenario (o): output confirms target file on success ------------------

func TestTeachParity_OutputConfirmsTargetFile(t *testing.T) {
	dir := t.TempDir()
	lesson := makeTeachLesson(t, dir, "a lesson")
	makeTeachAgent(t, dir, "backend", "# backend\n\nbody.\n")

	cfg := newTeachConfig(t, dir, "backend", lesson)
	if _, err := teach.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := teachOut(cfg)
	if !strings.Contains(out, "appended lesson to") {
		t.Errorf("expected 'appended lesson to' in output; got: %q", out)
	}
	if !strings.Contains(out, "backup:") {
		t.Errorf("expected backup path in output; got: %q", out)
	}
}
