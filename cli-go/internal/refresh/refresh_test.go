package refresh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- settings merge unit tests ----------------------------------------------

// TestMergeSettings_InSync verifies that a deployed file identical to template
// produces zero changes and the file is not modified.
func TestMergeSettings_InSync(t *testing.T) {
	tmp := t.TempDir()
	templateFile := filepath.Join(tmp, "template.json")
	deployedFile := filepath.Join(tmp, "settings.json")

	content := `{
  "hooks": {
    "UserPromptSubmit": [
      {
        "hooks": [
          {"type": "command", "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/cycle-counter.sh"}
        ]
      }
    ]
  }
}`
	if err := os.WriteFile(templateFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deployedFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	mtimeBefore := fileMtime(t, deployedFile)
	stats, err := MergeSettingsFiles(templateFile, deployedFile, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.Added != 0 || stats.Removed != 0 {
		t.Errorf("expected 0 changes; got added=%d removed=%d", stats.Added, stats.Removed)
	}
	// File should not have been written (mtime unchanged on same-second systems,
	// or at minimum content should be byte-equal)
	mtimeAfter := fileMtime(t, deployedFile)
	_ = mtimeBefore
	_ = mtimeAfter
	// The no-op path returns early before writing, so content must match.
	got, _ := os.ReadFile(deployedFile) //nolint:gosec
	if string(got) != content {
		t.Errorf("in-sync file was modified; want unchanged content")
	}
}

// TestMergeSettings_AddMissing verifies that hooks present in template but
// absent in deployed are added (Phase B).
func TestMergeSettings_AddMissing(t *testing.T) {
	tmp := t.TempDir()
	templateFile := filepath.Join(tmp, "template.json")
	deployedFile := filepath.Join(tmp, "settings.json")

	templateContent := `{
  "hooks": {
    "UserPromptSubmit": [
      {
        "hooks": [
          {"type": "command", "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/cycle-counter.sh"},
          {"type": "command", "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/retro-dispatch.sh"}
        ]
      }
    ]
  }
}`
	// Deployed only has cycle-counter
	deployedContent := `{
  "hooks": {
    "UserPromptSubmit": [
      {
        "hooks": [
          {"type": "command", "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/cycle-counter.sh"}
        ]
      }
    ]
  }
}`
	if err := os.WriteFile(templateFile, []byte(templateContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deployedFile, []byte(deployedContent), 0644); err != nil {
		t.Fatal(err)
	}

	stats, err := MergeSettingsFiles(templateFile, deployedFile, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.Added != 1 {
		t.Errorf("expected 1 added; got %d", stats.Added)
	}
	if stats.Removed != 0 {
		t.Errorf("expected 0 removed; got %d", stats.Removed)
	}

	// Verify the deployed file now contains retro-dispatch.
	data, _ := os.ReadFile(deployedFile) //nolint:gosec
	if !strings.Contains(string(data), "retro-dispatch.sh") {
		t.Errorf("retro-dispatch.sh not found in merged settings.json")
	}
}

// TestMergeSettings_RemoveSuperseded verifies Phase A: a deployed hook whose
// (event, command) appears in template with a DIFFERENT matcher is removed.
func TestMergeSettings_RemoveSuperseded(t *testing.T) {
	tmp := t.TempDir()
	templateFile := filepath.Join(tmp, "template.json")
	deployedFile := filepath.Join(tmp, "settings.json")

	// Template uses Edit|Write|MultiEdit for path-log
	templateContent := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Edit|Write|MultiEdit",
        "hooks": [
          {"type": "command", "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/path-log.sh"}
        ]
      }
    ]
  }
}`
	// Deployed uses old Edit|Write matcher for path-log
	deployedContent := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [
          {"type": "command", "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/path-log.sh"}
        ]
      }
    ]
  }
}`
	if err := os.WriteFile(templateFile, []byte(templateContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deployedFile, []byte(deployedContent), 0644); err != nil {
		t.Fatal(err)
	}

	stats, err := MergeSettingsFiles(templateFile, deployedFile, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 1 removed (old matcher), 1 added (new matcher + new entry)
	if stats.Removed != 1 {
		t.Errorf("expected 1 removed; got %d", stats.Removed)
	}
	if stats.Added != 1 {
		t.Errorf("expected 1 added; got %d", stats.Added)
	}

	data, _ := os.ReadFile(deployedFile) //nolint:gosec
	if strings.Contains(string(data), `"Edit|Write"`) && !strings.Contains(string(data), `"Edit|Write|MultiEdit"`) {
		t.Errorf("old Edit|Write matcher still present; new Edit|Write|MultiEdit not found")
	}
}

// TestMergeSettings_PreserveProjectLocal verifies Phase C: deployed-only
// hook registrations (e.g. kanban-stop.sh) are never removed.
func TestMergeSettings_PreserveProjectLocal(t *testing.T) {
	tmp := t.TempDir()
	templateFile := filepath.Join(tmp, "template.json")
	deployedFile := filepath.Join(tmp, "settings.json")

	templateContent := `{
  "hooks": {
    "SessionEnd": [
      {
        "hooks": [
          {"type": "command", "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/session-end-check.sh"}
        ]
      }
    ]
  }
}`
	// Deployed has kanban-stop.sh (project-local) in addition to session-end-check.sh
	deployedContent := `{
  "hooks": {
    "SessionEnd": [
      {
        "hooks": [
          {"type": "command", "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/session-end-check.sh"},
          {"type": "command", "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/kanban-stop.sh"}
        ]
      }
    ]
  }
}`
	if err := os.WriteFile(templateFile, []byte(templateContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deployedFile, []byte(deployedContent), 0644); err != nil {
		t.Fatal(err)
	}

	stats, err := MergeSettingsFiles(templateFile, deployedFile, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// kanban-stop.sh is not in template so it must be preserved; no changes.
	if stats.Added != 0 || stats.Removed != 0 {
		t.Errorf("expected 0 changes; got added=%d removed=%d", stats.Added, stats.Removed)
	}

	data, _ := os.ReadFile(deployedFile) //nolint:gosec
	if !strings.Contains(string(data), "kanban-stop.sh") {
		t.Errorf("project-local kanban-stop.sh was removed (must be preserved)")
	}
}

// TestMergeSettings_DropEmptyNonTemplateEvent verifies Phase D: empty event
// keys not present in the template are removed.
func TestMergeSettings_DropEmptyNonTemplateEvent(t *testing.T) {
	tmp := t.TempDir()
	templateFile := filepath.Join(tmp, "template.json")
	deployedFile := filepath.Join(tmp, "settings.json")

	// Template has no "ObsoleteEvent" key.
	templateContent := `{"hooks": {"UserPromptSubmit": [{"hooks": [{"type":"command","command":"cycle-counter.sh"}]}]}}`
	// Deployed has an empty ObsoleteEvent array that Phase A has drained.
	// We simulate this by starting with it already empty.
	deployedContent := `{"hooks": {"UserPromptSubmit": [{"hooks": [{"type":"command","command":"cycle-counter.sh"}]}], "ObsoleteEvent": []}}`

	if err := os.WriteFile(templateFile, []byte(templateContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deployedFile, []byte(deployedContent), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := MergeSettingsFiles(templateFile, deployedFile, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(deployedFile) //nolint:gosec
	if strings.Contains(string(data), "ObsoleteEvent") {
		t.Errorf("empty ObsoleteEvent not dropped by Phase D")
	}
}

// TestMergeSettings_KeepEmptyTemplateEvent verifies that empty event keys
// that ARE present in the template (e.g. "TeammateIdle": []) are preserved.
func TestMergeSettings_KeepEmptyTemplateEvent(t *testing.T) {
	tmp := t.TempDir()
	templateFile := filepath.Join(tmp, "template.json")
	deployedFile := filepath.Join(tmp, "settings.json")

	templateContent := `{"hooks": {"TeammateIdle": []}}`
	deployedContent := `{"hooks": {"TeammateIdle": []}}`

	if err := os.WriteFile(templateFile, []byte(templateContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deployedFile, []byte(deployedContent), 0644); err != nil {
		t.Fatal(err)
	}

	stats, err := MergeSettingsFiles(templateFile, deployedFile, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.Added != 0 || stats.Removed != 0 {
		t.Errorf("TeammateIdle:[] should produce 0 changes; got %+v", stats)
	}

	data, _ := os.ReadFile(deployedFile) //nolint:gosec
	if !strings.Contains(string(data), "TeammateIdle") {
		t.Errorf("TeammateIdle key was dropped (should be preserved as template event)")
	}
}

// TestMergeSettings_DryRunNoWrite verifies that dry-run mode does not write
// the deployed file.
func TestMergeSettings_DryRunNoWrite(t *testing.T) {
	tmp := t.TempDir()
	templateFile := filepath.Join(tmp, "template.json")
	deployedFile := filepath.Join(tmp, "settings.json")

	templateContent := `{"hooks": {"UserPromptSubmit": [{"hooks": [
    {"type":"command","command":"a.sh"},{"type":"command","command":"b.sh"}
  ]}]}}`
	deployedContent := `{"hooks": {"UserPromptSubmit": [{"hooks": [{"type":"command","command":"a.sh"}]}]}}`

	if err := os.WriteFile(templateFile, []byte(templateContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deployedFile, []byte(deployedContent), 0644); err != nil {
		t.Fatal(err)
	}

	var dryRunLog strings.Builder
	stats, err := MergeSettingsFiles(templateFile, deployedFile, true, &dryRunLog)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.Added == 0 {
		t.Errorf("expected stats to show added > 0 even in dry-run")
	}

	// File content must be unchanged.
	got, _ := os.ReadFile(deployedFile) //nolint:gosec
	if string(got) != deployedContent {
		t.Errorf("dry-run modified the file (should not write)")
	}
	if !strings.Contains(dryRunLog.String(), "dry-run") {
		t.Errorf("dry-run log should contain 'dry-run'; got: %s", dryRunLog.String())
	}
}

// TestMergeSettings_IdempotentTwice verifies that running the merge twice
// produces zero changes on the second run.
func TestMergeSettings_IdempotentTwice(t *testing.T) {
	tmp := t.TempDir()
	templateFile := filepath.Join(tmp, "template.json")
	deployedFile := filepath.Join(tmp, "settings.json")

	templateContent := `{
  "hooks": {
    "UserPromptSubmit": [{"hooks": [
      {"type":"command","command":"cycle-counter.sh"},
      {"type":"command","command":"retro-dispatch.sh"}
    ]}]
  }
}`
	deployedContent := `{"hooks": {"UserPromptSubmit": [{"hooks": [{"type":"command","command":"cycle-counter.sh"}]}]}}`

	if err := os.WriteFile(templateFile, []byte(templateContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deployedFile, []byte(deployedContent), 0644); err != nil {
		t.Fatal(err)
	}

	// First run: should add 1.
	stats1, err := MergeSettingsFiles(templateFile, deployedFile, false, nil)
	if err != nil {
		t.Fatalf("first merge error: %v", err)
	}
	if stats1.Added == 0 {
		t.Errorf("first merge: expected added > 0")
	}

	// Second run: should be a no-op.
	stats2, err := MergeSettingsFiles(templateFile, deployedFile, false, nil)
	if err != nil {
		t.Fatalf("second merge error: %v", err)
	}
	if stats2.Added != 0 || stats2.Removed != 0 {
		t.Errorf("second merge should be no-op; got added=%d removed=%d", stats2.Added, stats2.Removed)
	}
}

// TestMergeSettings_AtomicNoTempLeak verifies that no .yakos-refresh-tmp-*
// files are left behind after a successful merge.
func TestMergeSettings_AtomicNoTempLeak(t *testing.T) {
	tmp := t.TempDir()
	templateFile := filepath.Join(tmp, "template.json")
	deployedFile := filepath.Join(tmp, "settings.json")

	templateContent := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"a.sh"},{"type":"command","command":"b.sh"}]}]}}`
	deployedContent := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"a.sh"}]}]}}`

	if err := os.WriteFile(templateFile, []byte(templateContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deployedFile, []byte(deployedContent), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := MergeSettingsFiles(templateFile, deployedFile, false, nil); err != nil {
		t.Fatalf("merge error: %v", err)
	}

	// No temp files should remain.
	entries, _ := os.ReadDir(tmp)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".yakos-refresh-tmp") {
			t.Errorf("temp file leaked: %s", e.Name())
		}
	}
}

// ---- hook sync unit tests ---------------------------------------------------

// TestSyncHooks_NewAndStale creates a fake srcRoot with two hooks and a dstRoot
// with one matching and one stale hook, then verifies counts are correct.
func TestSyncHooks_NewAndStale(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Write two source hooks.
	if err := os.WriteFile(filepath.Join(src, "a.sh"), []byte("#!/bin/bash\necho a\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "b.sh"), []byte("#!/bin/bash\necho b\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// dst has a stale version of a.sh; b.sh is absent.
	if err := os.WriteFile(filepath.Join(dst, "a.sh"), []byte("#!/bin/bash\necho old_a\n"), 0755); err != nil {
		t.Fatal(err)
	}

	var logBuf strings.Builder
	rpt, err := syncHooks(src, dst, false, &logBuf)
	if err != nil {
		t.Fatalf("syncHooks error: %v", err)
	}
	if rpt.New != 1 {
		t.Errorf("expected 1 new; got %d", rpt.New)
	}
	if rpt.Synced != 1 {
		t.Errorf("expected 1 synced; got %d", rpt.Synced)
	}
	if rpt.OK != 0 {
		t.Errorf("expected 0 ok; got %d", rpt.OK)
	}

	// Verify b.sh was copied.
	if _, err := os.Stat(filepath.Join(dst, "b.sh")); err != nil {
		t.Errorf("b.sh was not copied to dst: %v", err)
	}
	// Verify hash sidings exist.
	if _, err := os.Stat(filepath.Join(dst, "a.sh.framework-hash")); err != nil {
		t.Errorf("a.sh.framework-hash not written")
	}
}

// TestSyncHooks_InSync verifies that already-up-to-date hooks produce ok=N,
// new=0, synced=0.
func TestSyncHooks_InSync(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	content := []byte("#!/bin/bash\necho hook\n")

	if err := os.WriteFile(filepath.Join(src, "c.sh"), content, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "c.sh"), content, 0755); err != nil {
		t.Fatal(err)
	}

	rpt, err := syncHooks(src, dst, false, os.Stdout)
	if err != nil {
		t.Fatalf("syncHooks error: %v", err)
	}
	if rpt.OK != 1 || rpt.New != 0 || rpt.Synced != 0 {
		t.Errorf("expected ok=1 new=0 synced=0; got %+v", rpt)
	}
}

// TestSyncHooks_SkipsReadme verifies that README.md and .gitkeep are skipped.
func TestSyncHooks_SkipsReadme(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("# docs\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".gitkeep"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "real-hook.sh"), []byte("#!/bin/bash\n"), 0755); err != nil {
		t.Fatal(err)
	}

	rpt, err := syncHooks(src, dst, false, os.Stdout)
	if err != nil {
		t.Fatalf("syncHooks error: %v", err)
	}
	// Only real-hook.sh should be counted.
	if rpt.New != 1 {
		t.Errorf("expected 1 new (real-hook.sh); got %+v", rpt)
	}
	if _, err := os.Stat(filepath.Join(dst, "README.md")); !os.IsNotExist(err) {
		t.Errorf("README.md should not have been copied")
	}
}

// TestSyncHooks_DryRunNoWrite verifies that dry-run does not copy files.
func TestSyncHooks_DryRunNoWrite(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	if err := os.WriteFile(filepath.Join(src, "d.sh"), []byte("#!/bin/bash\necho d\n"), 0755); err != nil {
		t.Fatal(err)
	}

	var logBuf strings.Builder
	rpt, err := syncHooks(src, dst, true, &logBuf)
	if err != nil {
		t.Fatalf("syncHooks error: %v", err)
	}
	if rpt.New != 1 {
		t.Errorf("expected new=1 in dry-run; got %+v", rpt)
	}
	// File must not have been copied.
	if _, err := os.Stat(filepath.Join(dst, "d.sh")); !os.IsNotExist(err) {
		t.Errorf("dry-run: file was written to dst (should not be)")
	}
	if !strings.Contains(logBuf.String(), "dry-run") {
		t.Errorf("dry-run log missing 'dry-run'; got: %s", logBuf.String())
	}
}

// TestSyncHooks_SubdirCopied verifies that lib/ subdirectory files are copied.
func TestSyncHooks_SubdirCopied(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	subdir := filepath.Join(src, "lib")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "compat.sh"), []byte("#!/bin/bash\n"), 0755); err != nil {
		t.Fatal(err)
	}

	rpt, err := syncHooks(src, dst, false, os.Stdout)
	if err != nil {
		t.Fatalf("syncHooks error: %v", err)
	}
	if rpt.New != 1 {
		t.Errorf("expected 1 new (subdir file); got %+v", rpt)
	}
	if _, err := os.Stat(filepath.Join(dst, "lib", "compat.sh")); err != nil {
		t.Errorf("lib/compat.sh not copied: %v", err)
	}
}

// ---- agent symlink unit tests -----------------------------------------------

// TestSyncAgents_Create verifies that missing symlinks are created.
func TestSyncAgents_Create(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()

	agentsSrc := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agentsSrc, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsSrc, "backend.md"), []byte("# backend\n"), 0644); err != nil {
		t.Fatal(err)
	}
	agentsDst := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(agentsDst, 0755); err != nil {
		t.Fatal(err)
	}

	rpt, err := syncAgents(root, home, false, os.Stdout)
	if err != nil {
		t.Fatalf("syncAgents error: %v", err)
	}
	if rpt.New != 1 || rpt.OK != 0 || rpt.Warns != 0 {
		t.Errorf("expected new=1; got %+v", rpt)
	}
	// Verify it's a symlink pointing to the right place.
	target, err := os.Readlink(filepath.Join(agentsDst, "backend.md"))
	if err != nil {
		t.Fatalf("symlink not created: %v", err)
	}
	if target != filepath.Join(agentsSrc, "backend.md") {
		t.Errorf("symlink target wrong: got %q", target)
	}
}

// TestSyncAgents_AlreadyCorrect verifies that a correctly-pointing symlink
// produces ok=1 and is not touched.
func TestSyncAgents_AlreadyCorrect(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()

	agentsSrc := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agentsSrc, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsSrc, "backend.md"), []byte("# backend\n"), 0644); err != nil {
		t.Fatal(err)
	}
	agentsDst := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(agentsDst, 0755); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(agentsSrc, "backend.md")
	dstPath := filepath.Join(agentsDst, "backend.md")
	if err := os.Symlink(srcPath, dstPath); err != nil {
		t.Fatal(err)
	}

	rpt, err := syncAgents(root, home, false, os.Stdout)
	if err != nil {
		t.Fatalf("syncAgents error: %v", err)
	}
	if rpt.OK != 1 || rpt.New != 0 {
		t.Errorf("expected ok=1; got %+v", rpt)
	}
}

// TestSyncAgents_RealFileWarn verifies that a real file (not symlink) produces
// a warning and is not overwritten.
func TestSyncAgents_RealFileWarn(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()

	agentsSrc := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agentsSrc, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsSrc, "backend.md"), []byte("# framework backend\n"), 0644); err != nil {
		t.Fatal(err)
	}
	agentsDst := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(agentsDst, 0755); err != nil {
		t.Fatal(err)
	}
	// Write a real file (operator override)
	realContent := "# operator backend\n"
	if err := os.WriteFile(filepath.Join(agentsDst, "backend.md"), []byte(realContent), 0644); err != nil {
		t.Fatal(err)
	}

	var logBuf strings.Builder
	rpt, err := syncAgents(root, home, false, &logBuf)
	if err != nil {
		t.Fatalf("syncAgents error: %v", err)
	}
	if rpt.Warns != 1 {
		t.Errorf("expected warns=1; got %+v", rpt)
	}
	// Real file must be unchanged.
	data, _ := os.ReadFile(filepath.Join(agentsDst, "backend.md")) //nolint:gosec
	if string(data) != realContent {
		t.Errorf("real file was overwritten (should be preserved)")
	}
	if !strings.Contains(logBuf.String(), "warn") {
		t.Errorf("warn log not emitted for real file; got: %s", logBuf.String())
	}
}

// TestSyncAgents_SkipsReadme verifies README.md is not symlinked.
func TestSyncAgents_SkipsReadme(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()

	agentsSrc := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agentsSrc, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsSrc, "README.md"), []byte("# docs\n"), 0644); err != nil {
		t.Fatal(err)
	}
	agentsDst := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(agentsDst, 0755); err != nil {
		t.Fatal(err)
	}

	rpt, err := syncAgents(root, home, false, os.Stdout)
	if err != nil {
		t.Fatalf("syncAgents error: %v", err)
	}
	if rpt.New != 0 {
		t.Errorf("README.md should be skipped; got new=%d", rpt.New)
	}
	if _, err := os.Stat(filepath.Join(agentsDst, "README.md")); !os.IsNotExist(err) {
		t.Errorf("README.md was symlinked (should be skipped)")
	}
}

// TestSyncAgents_DryRunNoSymlink verifies dry-run does not create symlinks.
func TestSyncAgents_DryRunNoSymlink(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()

	agentsSrc := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agentsSrc, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsSrc, "backend.md"), []byte("# backend\n"), 0644); err != nil {
		t.Fatal(err)
	}
	agentsDst := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(agentsDst, 0755); err != nil {
		t.Fatal(err)
	}

	var logBuf strings.Builder
	rpt, err := syncAgents(root, home, true, &logBuf)
	if err != nil {
		t.Fatalf("syncAgents error: %v", err)
	}
	if rpt.New != 1 {
		t.Errorf("dry-run should still count new; got %+v", rpt)
	}
	// Symlink must not have been created.
	if _, err := os.Lstat(filepath.Join(agentsDst, "backend.md")); !os.IsNotExist(err) {
		t.Errorf("dry-run: symlink was created (should not be)")
	}
}

// TestSyncHooks_LegacyFallback verifies that when the srcRoot has no top-level
// *.sh files but legacy/<name>.sh real files exist (the materialized-embed
// case), syncHooks copies them flat to dstRoot/<name>.sh.
func TestSyncHooks_LegacyFallback(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Simulate a materialized install: only legacy/ has real files; no
	// top-level symlinks exist (go:embed does not follow them).
	legacyDir := filepath.Join(src, "legacy")
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatal(err)
	}
	cycleContent := []byte("#!/usr/bin/env bash\n# cycle-counter\necho hi\n")
	budgetContent := []byte("#!/usr/bin/env bash\n# budget-guard\necho budget\n")
	if err := os.WriteFile(filepath.Join(legacyDir, "cycle-counter.sh"), cycleContent, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "budget-guard.sh"), budgetContent, 0755); err != nil {
		t.Fatal(err)
	}

	rpt, err := syncHooks(src, dst, false, os.Stdout)
	if err != nil {
		t.Fatalf("syncHooks error: %v", err)
	}
	if rpt.New != 2 {
		t.Errorf("expected new=2 from legacy fallback; got %+v", rpt)
	}

	// Verify files land at the flat dst path (not dst/legacy/).
	for name, want := range map[string][]byte{
		"cycle-counter.sh": cycleContent,
		"budget-guard.sh":  budgetContent,
	} {
		dstPath := filepath.Join(dst, name)
		data, err := os.ReadFile(dstPath) //nolint:gosec
		if err != nil {
			t.Errorf("%s: not found in dst: %v", name, err)
			continue
		}
		if string(data) != string(want) {
			t.Errorf("%s: content mismatch", name)
		}
		// Must be executable.
		info, _ := os.Stat(dstPath)
		if info.Mode()&0111 == 0 {
			t.Errorf("%s: not executable (mode %v)", name, info.Mode())
		}
		// legacy/ subdir must NOT have been created in dst.
		if _, err := os.Stat(filepath.Join(dst, "legacy")); !os.IsNotExist(err) {
			t.Errorf("legacy/ subdir was created in dst (should not exist)")
		}
	}
}

// TestSyncHooks_LegacyDeduplicatesTopLevel verifies that when the srcRoot has
// BOTH a top-level a.sh AND legacy/a.sh (as on a real repo install with
// symlinks resolved), only one copy is made (no double-counting).
func TestSyncHooks_LegacyDeduplicatesTopLevel(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	content := []byte("#!/usr/bin/env bash\necho hook\n")

	// Top-level real file (simulates resolved symlink on disk).
	if err := os.WriteFile(filepath.Join(src, "a.sh"), content, 0755); err != nil {
		t.Fatal(err)
	}
	// legacy/a.sh exists too (same content — would produce ok if counted, not new).
	legacyDir := filepath.Join(src, "legacy")
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "a.sh"), content, 0755); err != nil {
		t.Fatal(err)
	}
	// legacy/b.sh exists with no top-level counterpart.
	bContent := []byte("#!/usr/bin/env bash\necho b\n")
	if err := os.WriteFile(filepath.Join(legacyDir, "b.sh"), bContent, 0755); err != nil {
		t.Fatal(err)
	}

	rpt, err := syncHooks(src, dst, false, os.Stdout)
	if err != nil {
		t.Fatalf("syncHooks error: %v", err)
	}
	// a.sh from top level → new=1; b.sh from legacy (no top-level) → new=1; total new=2.
	totalNew := rpt.New + rpt.Synced + rpt.OK
	if totalNew != 2 {
		t.Errorf("expected 2 total operations (a.sh + b.sh); got new=%d synced=%d ok=%d",
			rpt.New, rpt.Synced, rpt.OK)
	}
	// Specifically: a.sh must appear exactly once.
	if _, err := os.Stat(filepath.Join(dst, "a.sh")); err != nil {
		t.Errorf("a.sh not found in dst: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "b.sh")); err != nil {
		t.Errorf("b.sh (from legacy fallback) not found in dst: %v", err)
	}
	// legacy/ subdir must NOT exist in dst.
	if _, err := os.Stat(filepath.Join(dst, "legacy")); !os.IsNotExist(err) {
		t.Errorf("legacy/ subdir was created in dst (should not exist)")
	}
}

// ---- helpers ----------------------------------------------------------------

func fileMtime(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.ModTime().UnixNano()
}
