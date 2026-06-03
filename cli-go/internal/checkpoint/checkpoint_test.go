// Package checkpoint — unit tests for internal/checkpoint.
//
// Coverage:
//
//	IsoUTC*              (2)  — format correctness, uniqueness
//	FormatSize*          (4)  — bytes/K/M/G boundary checks
//	ResolveWorkDir*      (3)  — cfg.WorkDir override, YAKOS_WORK_DIR env, canonical
//	ResolveSessionID*    (3)  — direct cfg, env var, unknown fallback
//	CopyFileIfExists*    (2)  — happy path (copies), absent src (no-op)
//	Run_Create*          (3)  — creates dir + files, manifest fields, output
//	Run_List*            (3)  — no checkpoints, single, multiple sorted
//	Run_Restore*         (3)  — happy path, missing id, unknown session
//	Run_Clean*           (3)  — removes old, keeps recent, no checkpoints dir
//	Run_UnknownSubcmd    (1)  — unknown subcommand returns error
//
// Total: ≥ 15 unit tests.
package checkpoint

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

// newCurrentDir creates a temp work/current/ dir and returns its path.
func newCurrentDir(t *testing.T) (workDir, currentDir string) {
	t.Helper()
	workDir = t.TempDir()
	currentDir = filepath.Join(workDir, "current")
	if err := os.MkdirAll(currentDir, 0755); err != nil {
		t.Fatalf("mkdir current: %v", err)
	}
	return workDir, currentDir
}

// seedFile writes content into path (creates parent dirs as needed).
func seedFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newCfg builds a Config with WorkDir set, so no env resolution is needed.
func newCfg(sub, workDir string) Config {
	var out, errOut bytes.Buffer
	return Config{
		Subcommand: sub,
		WorkDir:    workDir,
		Now:        time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
		Writer:     &out,
		ErrWriter:  &errOut,
	}
}

// cfgWithWriters returns a Config and pointers to its output buffers.
func cfgWithWriters(sub, workDir string) (Config, *bytes.Buffer, *bytes.Buffer) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cfg := Config{
		Subcommand: sub,
		WorkDir:    workDir,
		Now:        time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
		Writer:     out,
		ErrWriter:  errOut,
	}
	return cfg, out, errOut
}

// ---- IsoUTC -----------------------------------------------------------------

func TestIsoUTC_Format(t *testing.T) {
	ts := time.Date(2026, 6, 2, 14, 30, 5, 0, time.UTC)
	got := isoUTC(ts)
	want := "2026-06-02T14-30-05"
	if got != want {
		t.Errorf("isoUTC: got %q, want %q", got, want)
	}
}

func TestIsoUTC_NoColons(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := isoUTC(ts)
	if strings.ContainsAny(got, ":") {
		t.Errorf("isoUTC: should not contain colons; got %q", got)
	}
}

// ---- FormatSize -------------------------------------------------------------

func TestFormatSize_Bytes(t *testing.T) {
	got := formatSize(512)
	if got != "512B" {
		t.Errorf("formatSize(512): got %q, want 512B", got)
	}
}

func TestFormatSize_Kilobytes(t *testing.T) {
	got := formatSize(2048)
	if got != "2.0K" {
		t.Errorf("formatSize(2048): got %q, want 2.0K", got)
	}
}

func TestFormatSize_Megabytes(t *testing.T) {
	got := formatSize(3 * 1024 * 1024)
	if got != "3.0M" {
		t.Errorf("formatSize(3MiB): got %q, want 3.0M", got)
	}
}

func TestFormatSize_Gigabytes(t *testing.T) {
	got := formatSize(2 * 1024 * 1024 * 1024)
	if got != "2.0G" {
		t.Errorf("formatSize(2GiB): got %q, want 2.0G", got)
	}
}

// ---- ResolveWorkDir ---------------------------------------------------------

func TestResolveWorkDir_DirectOverride(t *testing.T) {
	cfg := Config{WorkDir: "/custom/work"}
	got := resolveWorkDir(cfg)
	if got != "/custom/work" {
		t.Errorf("resolveWorkDir: got %q, want /custom/work", got)
	}
}

func TestResolveWorkDir_YakosWorkDirEnv(t *testing.T) {
	t.Setenv("YAKOS_WORK_DIR", "/env/work")
	cfg := Config{}
	got := resolveWorkDir(cfg)
	if got != "/env/work" {
		t.Errorf("resolveWorkDir: got %q, want /env/work", got)
	}
}

func TestResolveWorkDir_Canonical(t *testing.T) {
	t.Setenv("YAKOS_WORK_DIR", "")
	t.Setenv("YAKOS_INPLACE_WORK", "")
	t.Setenv("YAKOS_PROJECT_NAME", "myproject")
	cfg := Config{HomeDir: "/home/user"}
	got := resolveWorkDir(cfg)
	want := "/home/user/agent-control/myproject/work"
	if got != want {
		t.Errorf("resolveWorkDir: got %q, want %q", got, want)
	}
}

// ---- ResolveSessionID -------------------------------------------------------

func TestResolveSessionID_DirectCfg(t *testing.T) {
	cfg := Config{SessionID: "test-session-id"}
	got := resolveSessionID(cfg, "/any/dir")
	if got != "test-session-id" {
		t.Errorf("resolveSessionID: got %q, want test-session-id", got)
	}
}

func TestResolveSessionID_EnvVar(t *testing.T) {
	t.Setenv("CLAUDE_SESSION_ID", "env-session-123")
	cfg := Config{}
	got := resolveSessionID(cfg, "/any/dir")
	if got != "env-session-123" {
		t.Errorf("resolveSessionID: got %q, want env-session-123", got)
	}
}

func TestResolveSessionID_UnknownFallback(t *testing.T) {
	t.Setenv("CLAUDE_SESSION_ID", "")
	cfg := Config{}
	got := resolveSessionID(cfg, "/nonexistent/dir/current")
	if got != "unknown" {
		t.Errorf("resolveSessionID: got %q, want unknown", got)
	}
}

// ---- CopyFileIfExists -------------------------------------------------------

func TestCopyFileIfExists_HappyPath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "plan.md")
	dst := filepath.Join(dir, "snap", "plan.md")
	seedFile(t, src, "# plan content")

	if err := copyFileIfExists(src, dst); err != nil {
		t.Fatalf("copyFileIfExists: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile dst: %v", err)
	}
	if string(data) != "# plan content" {
		t.Errorf("content mismatch: got %q", data)
	}
}

func TestCopyFileIfExists_AbsentSrc(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "nonexistent.md")
	dst := filepath.Join(dir, "snap", "nonexistent.md")

	if err := copyFileIfExists(src, dst); err != nil {
		t.Errorf("absent src should be no-op; got: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("dst should not exist when src is absent")
	}
}

// ---- Run_Create -------------------------------------------------------------

func TestRunCreate_CreatesDirectoryAndFiles(t *testing.T) {
	workDir, currentDir := newCurrentDir(t)
	_ = currentDir
	cfg, out, _ := cfgWithWriters("create", workDir)

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run create: %v", err)
	}

	if res.CheckpointID == "" {
		t.Error("CheckpointID should not be empty")
	}
	if res.CheckpointDir == "" {
		t.Error("CheckpointDir should not be empty")
	}

	// Verify expected files exist.
	for _, name := range []string{"summary.md", "token-snapshot.txt", "session-id.txt", "manifest.json"} {
		p := filepath.Join(res.CheckpointDir, name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
		}
	}

	// Verify scratchpad dir was created.
	if _, err := os.Stat(filepath.Join(res.CheckpointDir, "scratchpad")); err != nil {
		t.Error("scratchpad/ should exist")
	}

	// Verify output message.
	if !strings.Contains(out.String(), "checkpoint created") {
		t.Errorf("output should mention 'checkpoint created'; got %q", out.String())
	}
}

func TestRunCreate_ManifestFields(t *testing.T) {
	workDir, currentDir := newCurrentDir(t)
	_ = currentDir
	cfg := newCfg("create", workDir)
	cfg.SessionID = "test-session-abc"
	cfg.Runtime = "claude"
	out := &bytes.Buffer{}
	cfg.Writer = out
	cfg.ErrWriter = &bytes.Buffer{}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run create: %v", err)
	}

	manifestPath := filepath.Join(res.CheckpointDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile manifest.json: %v", err)
	}

	var manifest map[string]string
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Unmarshal manifest.json: %v", err)
	}

	for _, field := range []string{"created", "session_id", "runtime", "by_user"} {
		if manifest[field] == "" {
			t.Errorf("manifest missing field %q", field)
		}
	}
	if manifest["session_id"] != "test-session-abc" {
		t.Errorf("manifest session_id: got %q, want test-session-abc", manifest["session_id"])
	}
	if manifest["runtime"] != "claude" {
		t.Errorf("manifest runtime: got %q, want claude", manifest["runtime"])
	}
}

func TestRunCreate_CopiesScratchpadFiles(t *testing.T) {
	workDir, currentDir := newCurrentDir(t)
	seedFile(t, filepath.Join(currentDir, "plan.md"), "# My Plan")
	seedFile(t, filepath.Join(currentDir, "kanban.md"), "# Kanban")
	cfg := newCfg("create", workDir)
	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run create: %v", err)
	}

	for _, name := range []string{"plan.md", "kanban.md"} {
		p := filepath.Join(res.CheckpointDir, "scratchpad", name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("scratchpad/%s not copied: %v", name, err)
		}
	}
}

// ---- Run_List ---------------------------------------------------------------

func TestRunList_NoCheckpoints(t *testing.T) {
	workDir, _ := newCurrentDir(t)
	cfg, out, _ := cfgWithWriters("list", workDir)

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run list: %v", err)
	}
	if !strings.Contains(out.String(), "no checkpoints") {
		t.Errorf("list with no checkpoints should say '(no checkpoints)'; got %q", out.String())
	}
}

func TestRunList_SingleCheckpoint(t *testing.T) {
	workDir, currentDir := newCurrentDir(t)
	// Manually create a checkpoint dir.
	cpDir := filepath.Join(currentDir, "checkpoints", "2026-06-01T10-00-00")
	if err := os.MkdirAll(cpDir, 0755); err != nil {
		t.Fatal(err)
	}
	seedFile(t, filepath.Join(cpDir, "summary.md"), "# summary")

	cfg, out, _ := cfgWithWriters("list", workDir)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run list: %v", err)
	}
	if len(res.Checkpoints) != 1 {
		t.Errorf("expected 1 checkpoint; got %d", len(res.Checkpoints))
	}
	if !strings.Contains(out.String(), "2026-06-01T10-00-00") {
		t.Errorf("output should contain checkpoint ID; got %q", out.String())
	}
}

func TestRunList_MultipleSorted(t *testing.T) {
	workDir, currentDir := newCurrentDir(t)
	ids := []string{"2026-06-03T12-00-00", "2026-06-01T08-00-00", "2026-06-02T09-00-00"}
	for _, id := range ids {
		if err := os.MkdirAll(filepath.Join(currentDir, "checkpoints", id), 0755); err != nil {
			t.Fatal(err)
		}
	}

	cfg, out, _ := cfgWithWriters("list", workDir)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run list: %v", err)
	}
	if len(res.Checkpoints) != 3 {
		t.Errorf("expected 3 checkpoints; got %d", len(res.Checkpoints))
	}
	// Output should be in sorted order.
	outStr := out.String()
	idx1 := strings.Index(outStr, "2026-06-01T08-00-00")
	idx2 := strings.Index(outStr, "2026-06-02T09-00-00")
	idx3 := strings.Index(outStr, "2026-06-03T12-00-00")
	if idx1 < 0 || idx2 < 0 || idx3 < 0 {
		t.Errorf("output missing one or more IDs; got %q", outStr)
	}
	if idx1 >= idx2 || idx2 >= idx3 {
		t.Errorf("checkpoints not in sorted order in output: idx1=%d idx2=%d idx3=%d", idx1, idx2, idx3)
	}
}

// ---- Run_Restore ------------------------------------------------------------

func TestRunRestore_HappyPath(t *testing.T) {
	workDir, currentDir := newCurrentDir(t)
	id := "2026-06-01T10-00-00"
	cpDir := filepath.Join(currentDir, "checkpoints", id)
	if err := os.MkdirAll(cpDir, 0755); err != nil {
		t.Fatal(err)
	}
	seedFile(t, filepath.Join(cpDir, "session-id.txt"), "my-session-xyz\n")
	seedFile(t, filepath.Join(cpDir, "summary.md"), "# Summary\nSession state here.")

	cfg, out, _ := cfgWithWriters("restore", workDir)
	cfg.RestoreID = id

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run restore: %v", err)
	}
	outStr := out.String()
	if !strings.Contains(outStr, id) {
		t.Errorf("restore output should mention checkpoint ID; got %q", outStr)
	}
	if !strings.Contains(outStr, "my-session-xyz") {
		t.Errorf("restore output should mention session ID; got %q", outStr)
	}
	if !strings.Contains(outStr, "yakos start") {
		t.Errorf("restore output should mention 'yakos start'; got %q", outStr)
	}
}

func TestRunRestore_MissingID(t *testing.T) {
	workDir, _ := newCurrentDir(t)
	cfg := newCfg("restore", workDir)
	cfg.RestoreID = "2026-01-01T00-00-00"
	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing checkpoint ID")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found'; got %q", err.Error())
	}
}

func TestRunRestore_UnknownSession(t *testing.T) {
	workDir, currentDir := newCurrentDir(t)
	id := "2026-06-01T11-00-00"
	cpDir := filepath.Join(currentDir, "checkpoints", id)
	if err := os.MkdirAll(cpDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Write "unknown" as the session ID — should cause an error.
	seedFile(t, filepath.Join(cpDir, "session-id.txt"), "unknown\n")

	cfg := newCfg("restore", workDir)
	cfg.RestoreID = id
	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error when session-id is 'unknown'")
	}
	if !strings.Contains(err.Error(), "no session-id") {
		t.Errorf("error should mention 'no session-id'; got %q", err.Error())
	}
}

// ---- Run_Clean --------------------------------------------------------------

func TestRunClean_RemovesOldCheckpoints(t *testing.T) {
	workDir, currentDir := newCurrentDir(t)
	cpDir := filepath.Join(currentDir, "checkpoints", "2024-01-01T00-00-00")
	if err := os.MkdirAll(cpDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Make the dir appear old (> 30 days) by setting its mtime far in the past.
	oldTime := time.Now().Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(cpDir, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	cfg, out, _ := cfgWithWriters("clean", workDir)
	cfg.CleanAgeDays = 30

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run clean: %v", err)
	}
	if res.RemovedCount != 1 {
		t.Errorf("expected 1 removed; got %d (output: %s)", res.RemovedCount, out.String())
	}
	if _, err := os.Stat(cpDir); !os.IsNotExist(err) {
		t.Error("old checkpoint dir should have been removed")
	}
}

func TestRunClean_KeepsRecentCheckpoints(t *testing.T) {
	workDir, currentDir := newCurrentDir(t)
	cpDir := filepath.Join(currentDir, "checkpoints", "2026-06-01T00-00-00")
	if err := os.MkdirAll(cpDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Recent (1 day ago): should NOT be removed.
	recentTime := time.Now().Add(-1 * 24 * time.Hour)
	if err := os.Chtimes(cpDir, recentTime, recentTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	cfg, _, _ := cfgWithWriters("clean", workDir)
	cfg.CleanAgeDays = 30

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run clean: %v", err)
	}
	if res.RemovedCount != 0 {
		t.Errorf("expected 0 removed for recent checkpoint; got %d", res.RemovedCount)
	}
	if _, err := os.Stat(cpDir); err != nil {
		t.Error("recent checkpoint should NOT have been removed")
	}
}

func TestRunClean_NoCheckpointsDir(t *testing.T) {
	workDir, _ := newCurrentDir(t)
	cfg, out, _ := cfgWithWriters("clean", workDir)

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run clean with no checkpoints dir: %v", err)
	}
	if res.RemovedCount != 0 {
		t.Errorf("expected 0 removed; got %d", res.RemovedCount)
	}
	if !strings.Contains(out.String(), "removed 0") {
		t.Errorf("output should say 'removed 0'; got %q", out.String())
	}
}

// ---- Run_UnknownSubcommand --------------------------------------------------

func TestRunUnknownSubcommand(t *testing.T) {
	workDir, _ := newCurrentDir(t)
	cfg := newCfg("frobnicate", workDir)
	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("error should mention 'unknown subcommand'; got %q", err.Error())
	}
}

// ---- Run_RestoreMissingRestoreID --------------------------------------------

func TestRunRestore_MissingRestoreIDArg(t *testing.T) {
	workDir, _ := newCurrentDir(t)
	cfg := newCfg("restore", workDir)
	// RestoreID left empty.
	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error when RestoreID is empty")
	}
	if !strings.Contains(err.Error(), "missing <id>") {
		t.Errorf("error should mention 'missing <id>'; got %q", err.Error())
	}
}
