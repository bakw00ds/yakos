package refresh

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// TestMergeSettings_ReplacesAbsolutePathFormWithTemplateForm is the K-91
// follow-up regression test for a live duplicate-registration incident
// (2026-09-28): `yakos refresh --project <path>` against a settings.json
// whose hook commands used an absolute checkout path
// (/Users/tw/github/yakOS/scripts/hooks/<name>.sh) instead of the
// template's ${CLAUDE_PROJECT_DIR}/scripts/hooks/<name>.sh macro form
// added the template-form registration ALONGSIDE the existing absolute-form
// one for every hook, instead of replacing it — every hook fired twice.
// The merge must recognize both spellings as the SAME hook (by canonical
// name, i.e. script basename) and end up with exactly one registration per
// (event, hook), in the template's own form.
//
// Mutation test: revert canonicalHookName-based matching in Phase A/B back
// to raw command-string matching (the pre-fix behavior) and this test
// fails — both the absolute and template forms end up in the merged file,
// and Added/Removed no longer balance.
func TestMergeSettings_ReplacesAbsolutePathFormWithTemplateForm(t *testing.T) {
	tmp := t.TempDir()
	templateFile := filepath.Join(tmp, "template.json")
	deployedFile := filepath.Join(tmp, "settings.json")

	templateContent := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "*",
        "hooks": [
          {"type": "command", "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/supervisor-gate.sh"},
          {"type": "command", "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/secret-scan.sh"}
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [
          {"type": "command", "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/cycle-counter.sh"}
        ]
      }
    ]
  }
}`
	// Deployed has the SAME three hooks, same matchers, but registered via
	// an absolute checkout path instead of the ${CLAUDE_PROJECT_DIR} macro
	// — exactly the live-incident shape.
	deployedContent := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "*",
        "hooks": [
          {"type": "command", "command": "/Users/tw/github/yakOS/scripts/hooks/supervisor-gate.sh"},
          {"type": "command", "command": "/Users/tw/github/yakOS/scripts/hooks/secret-scan.sh"}
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [
          {"type": "command", "command": "/Users/tw/github/yakOS/scripts/hooks/cycle-counter.sh"}
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
	// All 3 absolute-form registrations replaced: 3 removed, 3 added.
	if stats.Removed != 3 {
		t.Errorf("expected 3 removed (absolute-form registrations superseded); got %d", stats.Removed)
	}
	if stats.Added != 3 {
		t.Errorf("expected 3 added (template-form registrations); got %d", stats.Added)
	}

	data, err := os.ReadFile(deployedFile) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}

	var merged map[string]any
	if err := json.Unmarshal(data, &merged); err != nil {
		t.Fatalf("merged settings.json is not valid JSON: %v\n%s", err, data)
	}

	// The absolute-path form must be gone entirely.
	if strings.Contains(string(data), "/Users/tw/github/yakOS/scripts/hooks/") {
		t.Errorf("absolute-path form still present in merged settings.json (duplicate registration not removed):\n%s", data)
	}

	// Exactly one registration per hook — count occurrences of each command
	// string (which, post-merge, can only be the template form).
	for _, name := range []string{"supervisor-gate.sh", "secret-scan.sh", "cycle-counter.sh"} {
		want := "${CLAUDE_PROJECT_DIR}/scripts/hooks/" + name
		count := strings.Count(string(data), want)
		if count != 1 {
			t.Errorf("hook %s: expected exactly 1 registration in template form; found %d\n%s", name, count, data)
		}
	}

	// Re-running the merge must now be a no-op (idempotent on the replaced form).
	stats2, err := MergeSettingsFiles(templateFile, deployedFile, false, nil)
	if err != nil {
		t.Fatalf("second merge error: %v", err)
	}
	if stats2.Added != 0 || stats2.Removed != 0 {
		t.Errorf("second merge should be a no-op; got added=%d removed=%d", stats2.Added, stats2.Removed)
	}
}

// TestCanonicalHookName verifies path-prefix-independent hook identity
// extraction used by the settings merge (see performMerge's
// templateDesired comment).
func TestCanonicalHookName(t *testing.T) {
	cases := map[string]string{
		"${CLAUDE_PROJECT_DIR}/scripts/hooks/secret-scan.sh":  "secret-scan.sh",
		"/Users/tw/github/yakOS/scripts/hooks/secret-scan.sh": "secret-scan.sh",
		"scripts/hooks/secret-scan.sh":                        "secret-scan.sh",
		"secret-scan.sh":                                      "secret-scan.sh",
		"${CLAUDE_PROJECT_DIR}/scripts/hooks/per-domain/x.sh": "per-domain/x.sh",
		"/Users/tw/github/yakOS/scripts/hooks/lib/x.sh":       "lib/x.sh",
		"/opt/myscripts/hooks/y.sh":                           "y.sh",
		"":                                                    "",
		"  ":                                                  "",
	}
	for cmd, want := range cases {
		if got := canonicalHookName(cmd); got != want {
			t.Errorf("canonicalHookName(%q) = %q; want %q", cmd, got, want)
		}
	}
}

// TestMergeSettings_SameBasenameDifferentSubdirsStayDistinct is the K-94
// regression test: two hooks that share a basename but live in different
// subdirectories under scripts/hooks/ are DIFFERENT hooks and must each be
// registered (and each replaced from an absolute-path form) independently.
//
// Mutation test: revert canonicalHookName to basename-only keying and the
// two hooks collapse into one merge key, so the second is never added
// (Added == 1, not 2) and the absolute-form replacement leaves one behind.
func TestMergeSettings_SameBasenameDifferentSubdirsStayDistinct(t *testing.T) {
	tmp := t.TempDir()
	templateFile := filepath.Join(tmp, "template.json")
	deployedFile := filepath.Join(tmp, "settings.json")

	template := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "*",
        "hooks": [
          {"type": "command", "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/a/check.sh"},
          {"type": "command", "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/b/check.sh"}
        ]
      }
    ]
  }
}`
	// Case 1: nothing deployed yet — both must be added.
	if err := os.WriteFile(templateFile, []byte(template), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deployedFile, []byte(`{"hooks": {}}`), 0644); err != nil {
		t.Fatal(err)
	}
	stats, err := MergeSettingsFiles(templateFile, deployedFile, false, nil)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if stats.Added != 2 || stats.Removed != 0 {
		t.Errorf("fresh merge: want added=2 removed=0; got added=%d removed=%d", stats.Added, stats.Removed)
	}
	data, _ := os.ReadFile(deployedFile) //nolint:gosec
	for _, want := range []string{"scripts/hooks/a/check.sh", "scripts/hooks/b/check.sh"} {
		if got := strings.Count(string(data), want); got != 1 {
			t.Errorf("%s: want exactly 1 registration; got %d\n%s", want, got, data)
		}
	}

	// Case 2: both deployed under absolute-path prefixes — each is replaced
	// by its own template form, none lost or duplicated.
	abs := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "*",
        "hooks": [
          {"type": "command", "command": "/Users/x/repo/scripts/hooks/a/check.sh"},
          {"type": "command", "command": "/Users/x/repo/scripts/hooks/b/check.sh"}
        ]
      }
    ]
  }
}`
	if err := os.WriteFile(deployedFile, []byte(abs), 0644); err != nil {
		t.Fatal(err)
	}
	stats, err = MergeSettingsFiles(templateFile, deployedFile, false, nil)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if stats.Added != 2 || stats.Removed != 2 {
		t.Errorf("absolute-form merge: want added=2 removed=2; got added=%d removed=%d", stats.Added, stats.Removed)
	}
	data, _ = os.ReadFile(deployedFile) //nolint:gosec
	if strings.Contains(string(data), "/Users/x/repo") {
		t.Errorf("absolute-form registrations not replaced:\n%s", data)
	}
	for _, want := range []string{"${CLAUDE_PROJECT_DIR}/scripts/hooks/a/check.sh", "${CLAUDE_PROJECT_DIR}/scripts/hooks/b/check.sh"} {
		if got := strings.Count(string(data), want); got != 1 {
			t.Errorf("%s: want exactly 1 registration; got %d\n%s", want, got, data)
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

// ---- resolveAgentsSourceRoot / worktree-target agent symlink tests --------
//
// Regression coverage for the live incident (2026-09-28): an install/
// refresh run using a worktree's binary re-pointed the GLOBAL
// ~/.claude/agents symlinks at that worktree; the worktree was later
// removed, leaving every project's agent symlinks dangling machine-wide.

// TestResolveAgentsSourceRoot_NonGitDirUnchanged verifies that a plain
// (non-git) directory — the materialized-embedded-install case — passes
// through unchanged: there is no worktree to redirect away from.
func TestResolveAgentsSourceRoot_NonGitDirUnchanged(t *testing.T) {
	root := t.TempDir()
	got, err := resolveAgentsSourceRoot(root, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != root {
		t.Errorf("expected unchanged root %q; got %q", root, got)
	}
}

// TestResolveAgentsSourceRoot_MainCheckoutUnchanged verifies that the main
// checkout of a git repo (not a worktree) passes through unchanged.
func TestResolveAgentsSourceRoot_MainCheckoutUnchanged(t *testing.T) {
	if _, err := runGit("", "--version"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	initGitRepoWithCommit(t, root)

	got, err := resolveAgentsSourceRoot(root, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolvePath(t, root) != resolvePath(t, got) {
		t.Errorf("expected unchanged root %q; got %q", root, got)
	}
}

// TestResolveAgentsSourceRoot_WorktreeRedirectsToCanonical verifies that a
// git worktree of the framework repo is redirected to the canonical (main)
// checkout when the canonical checkout has a usable lib/agents/.
func TestResolveAgentsSourceRoot_WorktreeRedirectsToCanonical(t *testing.T) {
	if _, err := runGit("", "--version"); err != nil {
		t.Skip("git not available")
	}
	main := t.TempDir()
	initGitRepoWithCommit(t, main)
	if err := os.MkdirAll(filepath.Join(main, "lib", "agents"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(main, "lib", "agents", "backend.md"), []byte("# backend\n"), 0644); err != nil {
		t.Fatal(err)
	}

	wtParent := t.TempDir()
	worktree := filepath.Join(wtParent, "wt")
	if out, err := runGit(main, "worktree", "add", "-q", worktree, "-b", "wt-branch"); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	var logBuf strings.Builder
	got, err := resolveAgentsSourceRoot(worktree, &logBuf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolvePath(t, got) != resolvePath(t, main) {
		t.Errorf("expected redirect to canonical checkout %q; got %q", main, got)
	}
	if !strings.Contains(logBuf.String(), "worktree") {
		t.Errorf("expected an info log line mentioning the worktree redirect; got: %s", logBuf.String())
	}
}

// TestResolveAgentsSourceRoot_WorktreeWithoutUsableCanonicalRefuses
// verifies that when the canonical checkout can't be confirmed usable (no
// lib/agents/ there), resolveAgentsSourceRoot refuses with a clear error
// instead of silently falling back to the worktree path — the exact
// behavior the incident needs closed.
func TestResolveAgentsSourceRoot_WorktreeWithoutUsableCanonicalRefuses(t *testing.T) {
	if _, err := runGit("", "--version"); err != nil {
		t.Skip("git not available")
	}
	main := t.TempDir()
	initGitRepoWithCommit(t, main)
	// Deliberately do NOT create lib/agents/ under main.

	wtParent := t.TempDir()
	worktree := filepath.Join(wtParent, "wt")
	if out, err := runGit(main, "worktree", "add", "-q", worktree, "-b", "wt-branch"); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	_, err := resolveAgentsSourceRoot(worktree, io.Discard)
	if err == nil {
		t.Fatal("expected an error when the canonical checkout has no usable lib/agents/; got nil")
	}
	if !strings.Contains(err.Error(), "worktree") {
		t.Errorf("error should explain the worktree refusal; got: %v", err)
	}
}

// TestSyncAgents_WorktreeRootTargetsCanonical is the end-to-end regression
// test: syncAgents, given a worktree as yakosRoot, must symlink
// ~/.claude/agents/*.md into the CANONICAL checkout's lib/agents/, never
// into the worktree's own lib/agents/ — even though the worktree also has
// a (different) agents directory.
//
// Mutation test: make resolveAgentsSourceRoot always return yakosRoot
// unchanged (skip the worktree redirect) and this test fails, because the
// created symlink then points into the worktree's lib/agents/ instead of
// the canonical checkout's.
func TestSyncAgents_WorktreeRootTargetsCanonical(t *testing.T) {
	if _, err := runGit("", "--version"); err != nil {
		t.Skip("git not available")
	}
	main := t.TempDir()
	initGitRepoWithCommit(t, main)
	mainAgents := filepath.Join(main, "lib", "agents")
	if err := os.MkdirAll(mainAgents, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainAgents, "backend.md"), []byte("# canonical backend\n"), 0644); err != nil {
		t.Fatal(err)
	}

	wtParent := t.TempDir()
	worktree := filepath.Join(wtParent, "wt")
	if out, err := runGit(main, "worktree", "add", "-q", worktree, "-b", "wt-branch2"); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	// The worktree ALSO has a lib/agents/ (its own working tree content,
	// possibly diverged from main) — this is what a naive, unredirected
	// syncAgents would incorrectly symlink into.
	wtAgents := filepath.Join(worktree, "lib", "agents")
	if err := os.MkdirAll(wtAgents, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtAgents, "backend.md"), []byte("# worktree backend\n"), 0644); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	rpt, err := syncAgents(worktree, home, false, io.Discard)
	if err != nil {
		t.Fatalf("syncAgents error: %v", err)
	}
	if rpt.New != 1 {
		t.Errorf("expected new=1; got %+v", rpt)
	}

	target, err := os.Readlink(filepath.Join(home, ".claude", "agents", "backend.md"))
	if err != nil {
		t.Fatalf("symlink not created: %v", err)
	}
	targetResolved := resolvePath(t, target)
	wantResolved := resolvePath(t, filepath.Join(mainAgents, "backend.md"))
	notWantResolved := resolvePath(t, filepath.Join(wtAgents, "backend.md"))
	if targetResolved != wantResolved {
		t.Errorf("symlink target = %q; want canonical checkout's %q (not the worktree's %q)", target, wantResolved, notWantResolved)
	}
}

// resolvePath returns p with all symlinks evaluated (so macOS's
// /tmp → /private/tmp aliasing, and similar OS-level path canonicalization,
// don't produce false mismatches when comparing paths derived two
// different ways — e.g. one via t.TempDir() directly, the other via a
// `git rev-parse` round trip). Falls back to filepath.Abs if the path
// doesn't exist yet (EvalSymlinks requires the path to exist).
func resolvePath(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		abs, aerr := filepath.Abs(p)
		if aerr != nil {
			t.Fatalf("resolvePath(%q): %v (abs fallback also failed: %v)", p, err, aerr)
		}
		return abs
	}
	return resolved
}

// initGitRepoWithCommit initializes a minimal, real git repo at dir with
// one commit, so `git worktree add` and `git rev-parse
// --git-dir/--git-common-dir` all work against it.
func initGitRepoWithCommit(t *testing.T, dir string) {
	t.Helper()
	if out, err := runGit(dir, "init", "-q"); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := runGit(dir, "config", "user.email", "test@example.com"); err != nil {
		t.Fatalf("git config email: %v\n%s", out, err)
	}
	if out, err := runGit(dir, "config", "user.name", "test"); err != nil {
		t.Fatalf("git config name: %v\n%s", out, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("root\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := runGit(dir, "add", "README.md"); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := runGit(dir, "commit", "-q", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
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
		// Must be executable. Windows has no POSIX exec-bit concept: Go's
		// os.Stat on Windows always reports plain -rw-rw-rw- for a regular
		// file regardless of the mode passed to WriteFile/Chmod (confirmed
		// on windows-latest CI: "cycle-counter.sh: not executable (mode
		// -rw-rw-rw-)"), so this assertion is structurally unsatisfiable
		// there and isn't testing syncHooks's own behavior on that
		// platform.
		info, _ := os.Stat(dstPath)
		if runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
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

// TestSyncHooks_LegacyNotShadowedBySameBasenameInSubdir is the K-94
// regression test for pass-1/pass-2 deduplication: a helper at lib/x.sh is
// a different destination than the flat legacy/x.sh, so it must not
// suppress it.
//
// Mutation test: key processedRels by basename again and legacy/x.sh is
// skipped (only lib/x.sh is deployed).
func TestSyncHooks_LegacyNotShadowedBySameBasenameInSubdir(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	write("lib/x.sh", "#!/usr/bin/env bash\necho helper\n")
	write("legacy/x.sh", "#!/usr/bin/env bash\necho legacy\n")

	rpt, err := syncHooks(src, dst, false, os.Stdout)
	if err != nil {
		t.Fatalf("syncHooks: %v", err)
	}
	if rpt.New != 2 {
		t.Errorf("want new=2 (lib/x.sh + flat x.sh); got new=%d synced=%d ok=%d", rpt.New, rpt.Synced, rpt.OK)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "x.sh")); !strings.Contains(string(got), "legacy") { //nolint:gosec
		t.Errorf("flat x.sh should come from legacy/x.sh; got %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "lib", "x.sh")); !strings.Contains(string(got), "helper") { //nolint:gosec
		t.Errorf("lib/x.sh should be the helper; got %q", got)
	}
}

// ---- CollectProjects self-sweep exclusion (K-91a) --------------------------

// writeYakosWiredSettings writes a minimal .claude/settings.json under dir
// that satisfies CollectProjects' "looks yakos-wired" scan criterion (must
// contain the literal substring "scripts/hooks/").
func writeYakosWiredSettings(t *testing.T, dir string) {
	t.Helper()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := `{"hooks": {"PreToolUse": [{"hooks": [{"command": "scripts/hooks/path-allowlist.sh"}]}]}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestCollectProjectsExcluding_ExcludesFrameworkRoot is the K-91a regression
// test: a fake HOME containing both a consumer project and the framework's
// own repo (both yakos-wired under ~/github/) must, when yakosRoot is
// passed, collect only the consumer — never the framework repo itself. This
// is the exact self-sweep mechanism from
// work/current/reports/scripts-hooks-drift-diag-2026-09-23.md: the
// framework's own .claude/settings.json legitimately contains
// "scripts/hooks/", so without exclusion it is indistinguishable from any
// consumer project.
//
// Mutation test: revert the isFrameworkSelf check (e.g. make add() always
// append) and this test fails, because the framework root then appears in
// the result alongside the consumer.
func TestCollectProjectsExcluding_ExcludesFrameworkRoot(t *testing.T) {
	home := t.TempDir()
	ghRoot := filepath.Join(home, "github")

	consumer := filepath.Join(ghRoot, "consumer-project")
	framework := filepath.Join(ghRoot, "yakOS")
	if err := os.MkdirAll(consumer, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(framework, 0755); err != nil {
		t.Fatal(err)
	}
	writeYakosWiredSettings(t, consumer)
	writeYakosWiredSettings(t, framework)

	got := CollectProjectsExcluding(home, framework)

	if len(got) != 1 || got[0] != consumer {
		t.Fatalf("expected exactly [%s]; got %v", consumer, got)
	}
	for _, p := range got {
		if p == framework {
			t.Fatalf("framework root %s leaked into CollectProjectsExcluding result: %v", framework, got)
		}
	}
}

// TestCollectProjectsExcluding_EmptyYakosRootDisablesExclusion verifies the
// documented opt-out: yakosRoot="" must reproduce the historical (pre-K-91a)
// behavior of including every yakos-wired project, since some callers
// legitimately have no resolved YakosRoot yet.
func TestCollectProjectsExcluding_EmptyYakosRootDisablesExclusion(t *testing.T) {
	home := t.TempDir()
	ghRoot := filepath.Join(home, "github")

	a := filepath.Join(ghRoot, "proj-a")
	b := filepath.Join(ghRoot, "proj-b")
	if err := os.MkdirAll(a, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(b, 0755); err != nil {
		t.Fatal(err)
	}
	writeYakosWiredSettings(t, a)
	writeYakosWiredSettings(t, b)

	got := CollectProjectsExcluding(home, "")
	if len(got) != 2 {
		t.Fatalf("expected 2 projects with exclusion disabled; got %v", got)
	}
}

// TestCollectProjects_UsesEnvYakosRoot verifies that the exported
// CollectProjects (the function every existing caller, including the
// bash-parity `yakos refresh --all` command, already calls) self-excludes
// using $YAKOS_ROOT from the environment — the reproducible trigger the
// incident diagnosis identified — with zero call-site changes required.
func TestCollectProjects_UsesEnvYakosRoot(t *testing.T) {
	home := t.TempDir()
	ghRoot := filepath.Join(home, "github")

	consumer := filepath.Join(ghRoot, "consumer-project")
	framework := filepath.Join(ghRoot, "yakOS")
	if err := os.MkdirAll(consumer, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(framework, 0755); err != nil {
		t.Fatal(err)
	}
	writeYakosWiredSettings(t, consumer)
	writeYakosWiredSettings(t, framework)

	t.Setenv("YAKOS_ROOT", framework)

	got := CollectProjects(home)
	if len(got) != 1 || got[0] != consumer {
		t.Fatalf("expected exactly [%s] with YAKOS_ROOT=%s; got %v", consumer, framework, got)
	}
}

// TestCollectProjectsExcluding_ExcludesWorktree verifies the worktree half
// of K-91a: a git worktree of the framework repo, deployed under
// ~/github/<name> as its own yakos-wired-looking project, must also be
// excluded — it is not a distinct consumer project, it's the same repo
// checked out twice. Exercises the git-common-dir comparison path in
// isFrameworkSelf (the os.SameFile-on-toplevel check alone would not catch
// this, since a worktree's toplevel is a different directory from the main
// checkout's).
func TestCollectProjectsExcluding_ExcludesWorktree(t *testing.T) {
	if _, err := runGit("", "--version"); err != nil {
		t.Skip("git not available")
	}

	home := t.TempDir()
	ghRoot := filepath.Join(home, "github")
	if err := os.MkdirAll(ghRoot, 0755); err != nil {
		t.Fatal(err)
	}

	framework := filepath.Join(ghRoot, "yakOS")
	if err := os.MkdirAll(framework, 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := runGit(framework, "init", "-q"); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := runGit(framework, "config", "user.email", "test@example.com"); err != nil {
		t.Fatalf("git config email: %v\n%s", out, err)
	}
	if out, err := runGit(framework, "config", "user.name", "test"); err != nil {
		t.Fatalf("git config name: %v\n%s", out, err)
	}
	if err := os.WriteFile(filepath.Join(framework, "README.md"), []byte("root\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := runGit(framework, "add", "README.md"); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := runGit(framework, "commit", "-q", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	worktree := filepath.Join(ghRoot, "yakOS-wt-test")
	if out, err := runGit(framework, "worktree", "add", "-q", worktree, "-b", "wt-branch"); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	consumer := filepath.Join(ghRoot, "consumer-project")
	if err := os.MkdirAll(consumer, 0755); err != nil {
		t.Fatal(err)
	}

	writeYakosWiredSettings(t, framework)
	writeYakosWiredSettings(t, worktree)
	writeYakosWiredSettings(t, consumer)

	got := CollectProjectsExcluding(home, framework)

	if len(got) != 1 || got[0] != consumer {
		t.Fatalf("expected exactly [%s] (framework root AND its worktree excluded); got %v", consumer, got)
	}
}

// TestRun_RefusesApplyAgainstYakosRootUnderTest is the K-91b regression
// test: Run must refuse apply=true (DryRun:false) whenever a ProjectPath is
// YakosRoot itself, while running under `go test` (testing.Testing() is
// always true in this test binary). This is the safety net for the
// footgun shape diagnosed in
// work/current/reports/scripts-hooks-drift-diag-2026-09-23.md §4 — a test
// that hardcodes the real repo checkout as a write target.
//
// Mutation test: comment out the `!cfg.DryRun && testing.Testing()` guard
// block in Run and this test fails, because Run then proceeds to actually
// write scripts/hooks/* into the "project" (== YakosRoot) directory instead
// of returning an error.
func TestRun_RefusesApplyAgainstYakosRootUnderTest(t *testing.T) {
	yakosRoot := t.TempDir()
	// Minimal framework layout so, if the guard failed to fire, Run's write
	// path would actually have something to copy (making a guard failure
	// observable as a real file write, not just a silent no-op).
	hooksSrc := filepath.Join(yakosRoot, "lib", "hooks")
	if err := os.MkdirAll(hooksSrc, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooksSrc, "example.sh"), []byte("#!/usr/bin/env bash\n"), 0755); err != nil {
		t.Fatal(err)
	}
	settingsDir := filepath.Join(yakosRoot, "lib", "settings")
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.template.json"), []byte(`{"hooks":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Run(Config{
		YakosRoot:    yakosRoot,
		ProjectPaths: []string{yakosRoot}, // the footgun: project == YakosRoot
		DryRun:       false,               // apply=true
		Writer:       io.Discard,
		ErrWriter:    io.Discard,
	})
	if err == nil {
		t.Fatal("expected Run to refuse apply=true against a ProjectPath equal to YakosRoot under go test; got nil error")
	}
	if !strings.Contains(err.Error(), "refusing apply=true") {
		t.Errorf("error should explain the refusal; got: %v", err)
	}

	// Confirm nothing was actually written into the "project" — the hooks
	// dir must not have gained a scripts/hooks mirror.
	if _, statErr := os.Stat(filepath.Join(yakosRoot, "scripts", "hooks")); !os.IsNotExist(statErr) {
		t.Errorf("scripts/hooks was written into YakosRoot despite the refusal (statErr=%v)", statErr)
	}
}

// TestRun_AllowsApplyAgainstYakosRootWhenDryRun verifies the guard is
// specific to apply=true — a dry-run against a ProjectPath equal to
// YakosRoot (as several intentionally-safe existing tests do, e.g.
// internal/serve's TestMethod_RefreshRun_OmittedApplyDoesNotWrite-style
// patterns) must not be refused.
func TestRun_AllowsApplyAgainstYakosRootWhenDryRun(t *testing.T) {
	yakosRoot := t.TempDir()
	hooksSrc := filepath.Join(yakosRoot, "lib", "hooks")
	if err := os.MkdirAll(hooksSrc, 0755); err != nil {
		t.Fatal(err)
	}
	settingsDir := filepath.Join(yakosRoot, "lib", "settings")
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.template.json"), []byte(`{"hooks":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Run(Config{
		YakosRoot:    yakosRoot,
		ProjectPaths: []string{yakosRoot},
		DryRun:       true,
		Writer:       io.Discard,
		ErrWriter:    io.Discard,
	})
	if err != nil {
		t.Fatalf("dry-run against YakosRoot should not be refused; got: %v", err)
	}
}

// runGit runs git with args in dir (or the current directory, for
// version-probing calls where dir is "") and returns combined output.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
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
