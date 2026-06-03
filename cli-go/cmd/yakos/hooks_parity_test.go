// hooks_parity_test.go — Phase 1 parity tests for `yakos hooks` (rank 39).
//
// Design notes:
//
//  1. All tests call hooksinstall.Run directly with temp-dir paths.
//
//  2. Decision Q9: hook bodies remain bash. Only config deployment is tested.
//
//  3. Parity is verified behaviourally: output shape, error conditions,
//     filesystem effects (config files written, JSON shape, permissions TOML).
//
// Critical scenarios:
//
//	(a) hooks entry in portedCommands
//	(b) install codex: creates .codex/hooks.json with hook entries
//	(c) install codex: skips missing scripts gracefully
//	(d) install codex: translates path-allowlist.json to codex permissions
//	(e) install gemini: creates .gemini/settings.json with hooks block
//	(f) install agy: creates .agents/hooks.json with hooks block
//	(g) install claude: prints advisory, no file created, no error
//	(h) install: --force skips backup
//	(i) install: backs up existing file without --force
//	(j) status: claude present + counts
//	(k) status: codex absent advisory
//	(l) status: gemini absent advisory
//	(m) status: full present report
//	(n) install: unknown runtime → error
//	(o) install: missing project → error
//	(p) install: missing scripts/hooks → error
//	(q) install codex: idempotent (re-run produces same output)
//	(r) status: project missing → error
//	(s) IsKnownRuntime covers all expected runtimes
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooksinstall"
)

// ---- scenario (a): portedCommands entry -------------------------------------

func TestHooksParity_PortedCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "hooks" {
			return
		}
	}
	t.Error("expected 'hooks' in portedCommands; not found")
}

// ---- helpers ----------------------------------------------------------------

// hooksBuildProject creates a temp project dir with scripts/hooks/ containing
// executable path-allowlist.sh and secret-scan.sh (matching hookPorts table).
func hooksBuildProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	hooksDir := filepath.Join(root, "scripts", "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	for _, name := range []string{"path-allowlist.sh", "secret-scan.sh"} {
		p := filepath.Join(hooksDir, name)
		if err := os.WriteFile(p, []byte("#!/usr/bin/env bash\necho ok\n"), 0755); err != nil {
			t.Fatalf("write hook %s: %v", name, err)
		}
	}
	return root
}

// hooksBuildProjectNoHooks creates a project dir without scripts/hooks/.
func hooksBuildProjectNoHooks(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, ".claude"), 0755)
	return root
}

// hooksBuildCfg returns a Config for the given subcommand and project.
func hooksBuildCfg(sub, runtime, project string) hooksinstall.Config {
	return hooksinstall.Config{
		Subcommand: sub,
		Runtime:    runtime,
		ProjectDir: project,
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
}

// hooksCfgOut extracts Writer output.
func hooksCfgOut(cfg hooksinstall.Config) string { return cfg.Writer.(*bytes.Buffer).String() }

// ---- scenario (b): install codex creates hooks.json -------------------------

func TestHooksParity_InstallCodex_CreatesHooksJSON(t *testing.T) {
	proj := hooksBuildProject(t)
	cfg := hooksBuildCfg("install", "codex", proj)
	res, err := hooksinstall.Run(cfg)
	if err != nil {
		t.Fatalf("install codex: %v", err)
	}
	if res.HooksWritten == 0 {
		t.Error("expected HooksWritten > 0")
	}

	target := filepath.Join(proj, ".codex", "hooks.json")
	data, err := os.ReadFile(target) //nolint:gosec
	if err != nil {
		t.Fatalf("hooks.json missing: %v", err)
	}
	var f struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("hooks.json invalid JSON: %v", err)
	}
	if len(f.Hooks) == 0 {
		t.Error("hooks.json has no entries")
	}
	// At least one entry should reference PreToolUse.
	found := false
	for evt := range f.Hooks {
		if evt == "PreToolUse" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected PreToolUse event in hooks.json")
	}
}

// ---- scenario (c): install codex skips missing scripts ----------------------

func TestHooksParity_InstallCodex_SkipsMissingScripts(t *testing.T) {
	// Project with scripts/hooks/ dir but NO hook scripts.
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, "scripts", "hooks"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := hooksBuildCfg("install", "codex", proj)
	res, err := hooksinstall.Run(cfg)
	if err != nil {
		t.Fatalf("install codex (empty hooks): %v", err)
	}
	if res.HooksWritten != 0 {
		t.Errorf("expected HooksWritten=0 for empty hooks dir; got %d", res.HooksWritten)
	}
	if res.SkippedMissing == 0 {
		t.Error("expected SkippedMissing > 0")
	}
}

// ---- scenario (d): install codex translates path-allowlist.json -------------

func TestHooksParity_InstallCodex_TranslatesPathAllowlist(t *testing.T) {
	proj := hooksBuildProject(t)
	// Write a path-allowlist.json.
	claudeDir := filepath.Join(proj, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	al := `{"allow":["src/**","docs/**"],"deny":["secrets/**"]}`
	if err := os.WriteFile(filepath.Join(claudeDir, "path-allowlist.json"), []byte(al), 0644); err != nil {
		t.Fatalf("write path-allowlist.json: %v", err)
	}

	cfg := hooksBuildCfg("install", "codex", proj)
	if _, err := hooksinstall.Run(cfg); err != nil {
		t.Fatalf("install codex: %v", err)
	}

	toml, err := os.ReadFile(filepath.Join(proj, ".codex", "config.toml")) //nolint:gosec
	if err != nil {
		t.Fatalf("config.toml missing: %v", err)
	}
	tomlStr := string(toml)
	if !strings.Contains(tomlStr, "yakos-permissions-start") {
		t.Error("config.toml missing yakos-permissions-start")
	}
	if !strings.Contains(tomlStr, "src/**") {
		t.Error("config.toml missing allow pattern 'src/**'")
	}
	if !strings.Contains(tomlStr, "secrets/**") {
		t.Error("config.toml missing deny pattern 'secrets/**'")
	}
}

// ---- scenario (e): install gemini creates settings.json ---------------------

func TestHooksParity_InstallGemini_CreatesSettingsJSON(t *testing.T) {
	proj := hooksBuildProject(t)
	cfg := hooksBuildCfg("install", "gemini", proj)
	res, err := hooksinstall.Run(cfg)
	if err != nil {
		t.Fatalf("install gemini: %v", err)
	}
	if res.HooksWritten == 0 {
		t.Error("expected HooksWritten > 0")
	}

	target := filepath.Join(proj, ".gemini", "settings.json")
	data, err := os.ReadFile(target) //nolint:gosec
	if err != nil {
		t.Fatalf("settings.json missing: %v", err)
	}
	var f struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("settings.json invalid JSON: %v", err)
	}
	if len(f.Hooks) == 0 {
		t.Error("settings.json has no hooks")
	}
	// gemini uses BeforeTool.
	found := false
	for evt := range f.Hooks {
		if evt == "BeforeTool" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected BeforeTool event in gemini settings.json")
	}
}

// ---- scenario (f): install agy creates hooks.json ---------------------------

func TestHooksParity_InstallAgy_CreatesHooksJSON(t *testing.T) {
	proj := hooksBuildProject(t)
	cfg := hooksBuildCfg("install", "agy", proj)
	res, err := hooksinstall.Run(cfg)
	if err != nil {
		t.Fatalf("install agy: %v", err)
	}
	if res.HooksWritten == 0 {
		t.Error("expected HooksWritten > 0")
	}

	target := filepath.Join(proj, ".agents", "hooks.json")
	data, err := os.ReadFile(target) //nolint:gosec
	if err != nil {
		t.Fatalf("agy hooks.json missing: %v", err)
	}
	var f struct {
		Hooks map[string]interface{} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("agy hooks.json invalid JSON: %v", err)
	}
	if len(f.Hooks) == 0 {
		t.Error("agy hooks.json has no entries")
	}
}

// ---- scenario (g): install claude → advisory, no error ----------------------

func TestHooksParity_InstallClaude_AdvisoryNoError(t *testing.T) {
	proj := hooksBuildProject(t)
	cfg := hooksBuildCfg("install", "claude", proj)
	res, err := hooksinstall.Run(cfg)
	if err != nil {
		t.Fatalf("install claude: %v", err)
	}
	// No config file written for claude.
	if _, err := os.Stat(filepath.Join(proj, ".codex")); err == nil {
		t.Error(".codex dir should not be created for 'install claude'")
	}
	// Advisory in Writer.
	out := hooksCfgOut(cfg)
	if !strings.Contains(out, "source-of-truth") {
		t.Errorf("expected advisory in output; got %q", out)
	}
	_ = res
}

// ---- scenario (h): install --force skips backup -----------------------------

func TestHooksParity_InstallCodex_ForceSkipsBackup(t *testing.T) {
	proj := hooksBuildProject(t)
	// First install.
	cfg := hooksBuildCfg("install", "codex", proj)
	if _, err := hooksinstall.Run(cfg); err != nil {
		t.Fatalf("first install: %v", err)
	}

	target := filepath.Join(proj, ".codex", "hooks.json")
	modTime1, _ := os.Stat(target)

	// Second install with --force: no backup.
	cfg2 := hooksBuildCfg("install", "codex", proj)
	cfg2.Force = true
	if _, err := hooksinstall.Run(cfg2); err != nil {
		t.Fatalf("install --force: %v", err)
	}

	// Verify no .yakos-bak file.
	entries, _ := os.ReadDir(filepath.Dir(target))
	for _, e := range entries {
		if strings.Contains(e.Name(), "yakos-bak") {
			t.Errorf("unexpected backup file: %s", e.Name())
		}
	}
	_ = modTime1
}

// ---- scenario (i): install backs up existing file without --force -----------

func TestHooksParity_InstallCodex_BacksUpWithoutForce(t *testing.T) {
	proj := hooksBuildProject(t)
	// First install.
	cfg := hooksBuildCfg("install", "codex", proj)
	if _, err := hooksinstall.Run(cfg); err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Second install without --force: should create backup.
	cfg2 := hooksBuildCfg("install", "codex", proj)
	if _, err := hooksinstall.Run(cfg2); err != nil {
		t.Fatalf("second install: %v", err)
	}

	// Look for .yakos-bak file.
	target := filepath.Join(proj, ".codex", "hooks.json")
	entries, _ := os.ReadDir(filepath.Dir(target))
	found := false
	for _, e := range entries {
		if strings.Contains(e.Name(), "yakos-bak") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected backup file after second install without --force")
	}
}

// ---- scenario (j): status claude present + counts ---------------------------

func TestHooksParity_Status_ClaudePresent(t *testing.T) {
	proj := t.TempDir()
	// Write a minimal .claude/settings.json with hooks.
	claudeDir := filepath.Join(proj, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settings := `{"hooks":{"PreToolUse":[{"matcher":".*","command":"/scripts/hooks/secret-scan.sh"}]}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(settings), 0644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}

	var buf bytes.Buffer
	cfg := hooksinstall.Config{
		Subcommand: "status",
		ProjectDir: proj,
		Writer:     &buf,
		ErrWriter:  &bytes.Buffer{},
	}
	if _, err := hooksinstall.Run(cfg); err != nil {
		t.Fatalf("status: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "settings.json present") {
		t.Errorf("expected 'settings.json present' for claude; got %q", out)
	}
	if !strings.Contains(out, "1 hook entries") {
		t.Errorf("expected '1 hook entries'; got %q", out)
	}
}

// ---- scenario (k): status codex absent advisory -----------------------------

func TestHooksParity_Status_CodexAbsent(t *testing.T) {
	proj := t.TempDir()
	var buf bytes.Buffer
	cfg := hooksinstall.Config{
		Subcommand: "status",
		ProjectDir: proj,
		Writer:     &buf,
		ErrWriter:  &bytes.Buffer{},
	}
	if _, err := hooksinstall.Run(cfg); err != nil {
		t.Fatalf("status: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "hooks.json absent") {
		t.Errorf("expected 'hooks.json absent' advisory; got %q", out)
	}
}

// ---- scenario (l): status gemini absent advisory ----------------------------

func TestHooksParity_Status_GeminiAbsent(t *testing.T) {
	proj := t.TempDir()
	var buf bytes.Buffer
	cfg := hooksinstall.Config{
		Subcommand: "status",
		ProjectDir: proj,
		Writer:     &buf,
		ErrWriter:  &bytes.Buffer{},
	}
	if _, err := hooksinstall.Run(cfg); err != nil {
		t.Fatalf("status: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "settings.json absent") {
		t.Errorf("expected 'settings.json absent' advisory for gemini; got %q", out)
	}
}

// ---- scenario (m): status full present report --------------------------------

func TestHooksParity_Status_FullPresentReport(t *testing.T) {
	proj := hooksBuildProject(t)

	// Install into both runtimes.
	for _, rt := range []string{"codex", "gemini"} {
		cfg := hooksBuildCfg("install", rt, proj)
		if _, err := hooksinstall.Run(cfg); err != nil {
			t.Fatalf("install %s: %v", rt, err)
		}
	}

	// Also seed .claude/settings.json.
	claudeDir := filepath.Join(proj, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	settings := `{"hooks":{"PreToolUse":[{"matcher":".*","command":"foo"}]}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(settings), 0644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}

	var buf bytes.Buffer
	cfg := hooksinstall.Config{
		Subcommand: "status",
		ProjectDir: proj,
		Writer:     &buf,
		ErrWriter:  &bytes.Buffer{},
	}
	if _, err := hooksinstall.Run(cfg); err != nil {
		t.Fatalf("status: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"claude:", "codex:", "gemini:", "present"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q; got:\n%s", want, out)
		}
	}
}

// ---- scenario (n): install unknown runtime → error --------------------------

func TestHooksParity_Install_UnknownRuntime_Error(t *testing.T) {
	proj := hooksBuildProject(t)
	cfg := hooksBuildCfg("install", "bogus", proj)
	_, err := hooksinstall.Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown runtime")
	}
	if !strings.Contains(err.Error(), "unknown runtime") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (o): install missing project → error --------------------------

func TestHooksParity_Install_MissingProject_Error(t *testing.T) {
	cfg := hooksBuildCfg("install", "codex", "/nonexistent/path/12345")
	_, err := hooksinstall.Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing project dir")
	}
}

// ---- scenario (p): install missing scripts/hooks → error --------------------

func TestHooksParity_Install_MissingHooksDir_Error(t *testing.T) {
	proj := hooksBuildProjectNoHooks(t)
	cfg := hooksBuildCfg("install", "codex", proj)
	_, err := hooksinstall.Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing scripts/hooks dir")
	}
	if !strings.Contains(err.Error(), filepath.Join("scripts", "hooks")) {
		t.Errorf("expected error to mention %s; got: %v", filepath.Join("scripts", "hooks"), err)
	}
}

// ---- scenario (q): install codex idempotent --------------------------------

func TestHooksParity_InstallCodex_Idempotent(t *testing.T) {
	proj := hooksBuildProject(t)

	cfg1 := hooksBuildCfg("install", "codex", proj)
	cfg1.Force = true // avoid backup on second run
	res1, err := hooksinstall.Run(cfg1)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}

	cfg2 := hooksBuildCfg("install", "codex", proj)
	cfg2.Force = true
	res2, err := hooksinstall.Run(cfg2)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}

	if res1.HooksWritten != res2.HooksWritten {
		t.Errorf("idempotent: HooksWritten changed: %d → %d", res1.HooksWritten, res2.HooksWritten)
	}
}

// ---- scenario (r): status project missing → error ---------------------------

func TestHooksParity_Status_MissingProject_Error(t *testing.T) {
	cfg := hooksinstall.Config{
		Subcommand: "status",
		ProjectDir: "", // empty = should error
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
	_, err := hooksinstall.Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing project in status")
	}
}

// ---- scenario (s): IsKnownRuntime covers expected runtimes ------------------

func TestHooksParity_IsKnownRuntime(t *testing.T) {
	for _, rt := range []string{"claude", "codex", "gemini", "agy"} {
		if !hooksinstall.IsKnownRuntime(rt) {
			t.Errorf("expected %q to be a known runtime", rt)
		}
	}
	if hooksinstall.IsKnownRuntime("bogus") {
		t.Error("expected 'bogus' to be unknown")
	}
}

// ---- hooks lint subcommand --------------------------------------------------

func TestHooksParity_Lint_ValidStarFile_NoErrors(t *testing.T) {
	tmp := t.TempDir()
	star := filepath.Join(tmp, "cycle-counter.star")
	if err := os.WriteFile(star, []byte("def on_event(ctx):\n    ctx.log(\"ok\")\n"), 0644); err != nil {
		t.Fatalf("write star: %v", err)
	}
	cfg := hooksinstall.LintConfig{
		HooksDir:  tmp,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}
	r, err := hooksinstall.RunLint(cfg)
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	if r.ErrCount != 0 {
		t.Errorf("expected 0 errors; got %d", r.ErrCount)
	}
	if r.Total != 1 {
		t.Errorf("expected 1 file; got %d", r.Total)
	}
}

func TestHooksParity_Lint_SyntaxError_Reported(t *testing.T) {
	tmp := t.TempDir()
	star := filepath.Join(tmp, "bad.star")
	if err := os.WriteFile(star, []byte("def on_event(ctx bad {{"), 0644); err != nil {
		t.Fatalf("write star: %v", err)
	}
	cfg := hooksinstall.LintConfig{
		HooksDir:  tmp,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}
	r, err := hooksinstall.RunLint(cfg)
	if err != nil {
		t.Fatalf("RunLint: %v", err)
	}
	if r.ErrCount == 0 {
		t.Error("expected error for syntax error")
	}
}

func TestHooksParity_Lint_MissingDir_Error(t *testing.T) {
	cfg := hooksinstall.LintConfig{
		HooksDir:  "/nonexistent/hooks/dir",
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}
	_, err := hooksinstall.RunLint(cfg)
	if err == nil {
		t.Fatal("expected error for missing hooks dir")
	}
}

func TestHooksParity_Lint_PrintHelp_MentionsLint(t *testing.T) {
	var buf bytes.Buffer
	hooksinstall.PrintHelp(&buf)
	out := buf.String()
	if !strings.Contains(out, "lint") {
		t.Errorf("PrintHelp should mention 'lint' subcommand; got %q", out)
	}
}

// ---- kanban serve portedCommands entry ----------------------------------------

func TestKanbanServeParity_PortedCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "kanban serve" {
			return
		}
	}
	t.Error("expected 'kanban serve' in portedCommands; not found")
}
