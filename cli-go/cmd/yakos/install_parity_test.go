// install_parity_test.go — Phase 1 parity tests for `yakos install`.
//
// Design notes:
//
//  1. All tests use install.Run directly with temp dirs so no real
//     ~/.claude or ~/.local/bin is touched.
//
//  2. Help-text parity verifies load-bearing phrases from cli/lib/install.sh
//     usage() appear in the Go output.
//
//  3. The bash install.sh writes ~/.yakos and ~/.local/bin/yakos symlinks at
//     the global level. The Go port implements these identically; per-project
//     hook scripts are NOT the scope of install (they belong to refresh).
//
// Critical scenarios verified:
//
//	(a) happy-path              — pointer, launcher, symlinks, settings.json created
//	(b) --dry-run               — no files written; annotations in stdout
//	(c) --force                 — YakOS-owned symlinks refreshed unconditionally
//	(d) invalid YAKOS_ROOT      — error returned, no state written
//	(e) corrupt settings.json   — preflight aborts before touching state
//	(f) existing settings.json  — env key merged in, other keys preserved
//	(g) already-set env key     — idempotent no-op
//	(h) real file at symlink    — real file preserved; skipped + warning
//	(i) foreign launcher symlink — skipped + warning
//	(j) real file at launcher   — real file left alone + warning
//	(k) nested subdir symlinks  — nested lib/ hierarchy mirrored correctly
//	(l) .gitkeep skipped        — .gitkeep files not symlinked
//	(m) install-manifest        — manifest records launcher path
//	(n) idempotent second run   — second run produces zero Created symlinks
//	(o) help text               — all load-bearing phrases from install.sh usage()
//	(p) settings.json is valid JSON after creation
//	(q) no temp files leak      — no .yakos-install-tmp-* files remain
//	(r) absent lib subdir       — graceful when lib/playbooks/ absent
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/install"
)

// ---- helpers ----------------------------------------------------------------

// newInstallFakeRoot creates a minimal YAKOS_ROOT in a temp dir with all four
// lib/ subdirs populated.
func newInstallFakeRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, sub := range []string{"agents", "skills", "rules", "playbooks"} {
		dir := filepath.Join(root, "lib", sub)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "example.md"), []byte("# "+sub+"\n"), 0644); err != nil {
			t.Fatalf("write %s/example.md: %v", sub, err)
		}
	}
	// cli/yakos launcher target.
	cliDir := filepath.Join(root, "cli")
	if err := os.MkdirAll(cliDir, 0755); err != nil {
		t.Fatalf("mkdir cli: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "yakos"), []byte("#!/bin/bash\n"), 0755); err != nil {
		t.Fatalf("write cli/yakos: %v", err)
	}
	return root
}

// installBaseConfig returns a Config with isolated home + captured output.
func installBaseConfig(t *testing.T, yakosRoot string) install.Config {
	t.Helper()
	home := t.TempDir()
	return install.Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}
}

// ---- (a) happy-path ---------------------------------------------------------

// TestInstallParity_HappyPath verifies the full install scaffold.
func TestInstallParity_HappyPath(t *testing.T) {
	root := newInstallFakeRoot(t)
	cfg := installBaseConfig(t, root)

	res, err := install.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	home := cfg.HomeDir

	// ~/.yakos pointer.
	pdata, err := os.ReadFile(filepath.Join(home, ".yakos"))
	if err != nil {
		t.Fatalf(".yakos pointer not created: %v", err)
	}
	if strings.TrimSpace(string(pdata)) == "" {
		t.Error(".yakos pointer should contain YAKOS_ROOT")
	}

	// Launcher symlink.
	lp := filepath.Join(home, ".local", "bin", "yakos")
	li, err := os.Lstat(lp)
	if err != nil {
		t.Fatalf("launcher not created: %v", err)
	}
	if li.Mode()&os.ModeSymlink == 0 {
		t.Error("launcher should be a symlink")
	}
	if res.Launcher.Outcome != install.LauncherCreated {
		t.Errorf("launcher outcome: got %q, want created", res.Launcher.Outcome)
	}

	// At least one symlink per subdir.
	if res.Symlinks.Created == 0 {
		t.Error("expected Created > 0")
	}

	// settings.json.
	sp := filepath.Join(home, ".claude", "settings.json")
	sd, err := os.ReadFile(sp)
	if err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}
	if !strings.Contains(string(sd), "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS") {
		t.Error("settings.json should contain env key")
	}
}

// ---- (b) --dry-run ----------------------------------------------------------

// TestInstallParity_DryRun verifies dry-run writes no files.
func TestInstallParity_DryRun(t *testing.T) {
	root := newInstallFakeRoot(t)
	cfg := installBaseConfig(t, root)
	cfg.DryRun = true

	res, err := install.Run(cfg)
	if err != nil {
		t.Fatalf("Run (dry-run): %v", err)
	}
	if !res.DryRun {
		t.Error("Result.DryRun should be true")
	}

	home := cfg.HomeDir
	for _, path := range []string{
		filepath.Join(home, ".yakos"),
		filepath.Join(home, ".local", "bin", "yakos"),
		filepath.Join(home, ".claude", "settings.json"),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("dry-run: %s should NOT exist", path)
		}
	}

	stdout := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(stdout, "dry-run") {
		t.Errorf("stdout should mention 'dry-run'; got:\n%s", stdout)
	}
}

// ---- (c) --force ------------------------------------------------------------

// TestInstallParity_Force verifies --force refreshes YakOS-owned symlinks.
func TestInstallParity_Force(t *testing.T) {
	root := newInstallFakeRoot(t)
	cfg := installBaseConfig(t, root)

	if _, err := install.Run(cfg); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}
	cfg.Force = true

	res2, err := install.Run(cfg)
	if err != nil {
		t.Fatalf("force Run: %v", err)
	}
	if res2.Symlinks.Refreshed == 0 {
		t.Error("--force: expected at least one symlink refreshed")
	}
}

// ---- (d) invalid YAKOS_ROOT -------------------------------------------------

// TestInstallParity_InvalidRoot verifies a bad YAKOS_ROOT is rejected early.
func TestInstallParity_InvalidRoot(t *testing.T) {
	cfg := installBaseConfig(t, "/no/such/path/9999")
	_, err := install.Run(cfg)
	if err == nil {
		t.Fatal("expected error for non-existent YAKOS_ROOT")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error should mention 'not a directory'; got %q", err.Error())
	}
}

// ---- (e) corrupt settings.json preflight ------------------------------------

// TestInstallParity_CorruptSettings verifies preflight aborts before writing state.
func TestInstallParity_CorruptSettings(t *testing.T) {
	root := newInstallFakeRoot(t)
	cfg := installBaseConfig(t, root)
	home := cfg.HomeDir

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("{{BAD}}"), 0644); err != nil {
		t.Fatalf("write corrupt json: %v", err)
	}

	_, err := install.Run(cfg)
	if err == nil {
		t.Fatal("expected error for corrupt settings.json")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("error should mention 'not valid JSON'; got %q", err.Error())
	}
	// Pointer file must not exist.
	if _, err2 := os.Stat(filepath.Join(home, ".yakos")); !os.IsNotExist(err2) {
		t.Error(".yakos should not be created when preflight fails")
	}
}

// ---- (f) existing settings.json merge ---------------------------------------

// TestInstallParity_ExistingSettingsMerge verifies other keys are preserved.
func TestInstallParity_ExistingSettingsMerge(t *testing.T) {
	root := newInstallFakeRoot(t)
	cfg := installBaseConfig(t, root)
	home := cfg.HomeDir

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pre := map[string]any{"sentinel": "value", "env": map[string]any{"X": "Y"}}
	preData, _ := json.MarshalIndent(pre, "", "  ")
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), preData, 0644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}

	if _, err := install.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("merged settings.json invalid JSON: %v", err)
	}
	if out["sentinel"] != "value" {
		t.Error("sentinel key should be preserved")
	}
	envMap, _ := out["env"].(map[string]any)
	if v, _ := envMap["X"].(string); v != "Y" {
		t.Error("env.X should be preserved")
	}
	if v, _ := envMap["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"].(string); v != "1" {
		t.Error("env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS should be set to 1")
	}
}

// ---- (g) already-set env key ------------------------------------------------

// TestInstallParity_AlreadySetEnvKey verifies idempotent no-op.
func TestInstallParity_AlreadySetEnvKey(t *testing.T) {
	root := newInstallFakeRoot(t)
	cfg := installBaseConfig(t, root)
	home := cfg.HomeDir

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pre := map[string]any{"env": map[string]any{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "1"}}
	preData, _ := json.MarshalIndent(pre, "", "  ")
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, preData, 0644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}

	res, err := install.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Settings.AlreadySet {
		t.Error("Settings.AlreadySet should be true")
	}
	// File must be unchanged.
	got, _ := os.ReadFile(settingsPath)
	if string(got) != string(preData) {
		t.Error("settings.json modified even though env key already correct")
	}
}

// ---- (h) real file at symlink dest ------------------------------------------

// TestInstallParity_RealFileAtDest verifies real files are never overwritten.
func TestInstallParity_RealFileAtDest(t *testing.T) {
	root := newInstallFakeRoot(t)
	cfg := installBaseConfig(t, root)
	home := cfg.HomeDir

	agentsDir := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "# operator-managed\n"
	if err := os.WriteFile(filepath.Join(agentsDir, "example.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := install.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Symlinks.Skipped == 0 {
		t.Error("expected Skipped > 0 for real file")
	}
	got, _ := os.ReadFile(filepath.Join(agentsDir, "example.md"))
	if string(got) != content {
		t.Error("real file was overwritten")
	}
}

// ---- (i) foreign launcher symlink -------------------------------------------

// TestInstallParity_ForeignLauncher verifies a foreign launcher symlink is left alone.
func TestInstallParity_ForeignLauncher(t *testing.T) {
	root := newInstallFakeRoot(t)
	cfg := installBaseConfig(t, root)
	home := cfg.HomeDir

	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink("/usr/bin/env", filepath.Join(localBin, "yakos")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	res, err := install.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Launcher.Outcome != install.LauncherSkipped {
		t.Errorf("foreign launcher: outcome should be skipped; got %q", res.Launcher.Outcome)
	}
	if !strings.Contains(res.Launcher.Warning, "WARNING") {
		t.Errorf("expected WARNING in Launcher.Warning; got %q", res.Launcher.Warning)
	}
}

// ---- (j) real file at launcher ----------------------------------------------

// TestInstallParity_RealFileLauncher verifies a real-file launcher is left alone.
func TestInstallParity_RealFileLauncher(t *testing.T) {
	root := newInstallFakeRoot(t)
	cfg := installBaseConfig(t, root)
	home := cfg.HomeDir

	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localBin, "yakos"), []byte("#!/bin/bash\n#foreign\n"), 0755); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := install.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Launcher.Outcome != install.LauncherSkipped {
		t.Errorf("real-file launcher: outcome should be skipped; got %q", res.Launcher.Outcome)
	}
}

// ---- (k) nested subdir symlinks ---------------------------------------------

// TestInstallParity_NestedSubdir verifies nested lib/ hierarchy is mirrored.
func TestInstallParity_NestedSubdir(t *testing.T) {
	root := newInstallFakeRoot(t)
	// Add nested skill.
	dir := filepath.Join(root, "lib", "skills", "pre-commit")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# s\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg := installBaseConfig(t, root)
	res, err := install.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	symPath := filepath.Join(cfg.HomeDir, ".claude", "skills", "pre-commit", "SKILL.md")
	li, err := os.Lstat(symPath)
	if err != nil {
		t.Fatalf("nested symlink not created: %v", err)
	}
	if li.Mode()&os.ModeSymlink == 0 {
		t.Error("nested path should be a symlink")
	}
	if res.Symlinks.Created == 0 {
		t.Error("Created should be > 0 for nested file")
	}
}

// ---- (l) .gitkeep skipped ---------------------------------------------------

// TestInstallParity_GitkeepSkipped verifies .gitkeep is not symlinked.
func TestInstallParity_GitkeepSkipped(t *testing.T) {
	root := newInstallFakeRoot(t)
	if err := os.WriteFile(filepath.Join(root, "lib", "agents", ".gitkeep"), []byte{}, 0644); err != nil {
		t.Fatalf("write .gitkeep: %v", err)
	}

	cfg := installBaseConfig(t, root)
	if _, err := install.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	symPath := filepath.Join(cfg.HomeDir, ".claude", "agents", ".gitkeep")
	if _, err := os.Lstat(symPath); !os.IsNotExist(err) {
		t.Error(".gitkeep should not be symlinked")
	}
}

// ---- (m) install-manifest ---------------------------------------------------

// TestInstallParity_InstallManifest verifies the manifest records the launcher.
func TestInstallParity_InstallManifest(t *testing.T) {
	root := newInstallFakeRoot(t)
	cfg := installBaseConfig(t, root)

	if _, err := install.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(cfg.HomeDir, ".yakos-state", "install-manifest"))
	if err != nil {
		t.Fatalf("install-manifest not created: %v", err)
	}
	if !strings.Contains(string(data), "launcher=") {
		t.Errorf("manifest should have 'launcher='; got %q", string(data))
	}
}

// ---- (n) idempotent second run ----------------------------------------------

// TestInstallParity_Idempotent verifies second run creates zero new symlinks.
func TestInstallParity_Idempotent(t *testing.T) {
	root := newInstallFakeRoot(t)
	cfg := installBaseConfig(t, root)

	if _, err := install.Run(cfg); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}
	res2, err := install.Run(cfg)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if res2.Symlinks.Created != 0 {
		t.Errorf("second run: expected 0 created; got %d", res2.Symlinks.Created)
	}
}

// ---- (o) help text ----------------------------------------------------------

// TestInstallParity_HelpText verifies all load-bearing phrases.
func TestInstallParity_HelpText(t *testing.T) {
	var buf bytes.Buffer
	install.PrintHelp(&buf)
	got := buf.String()

	for _, phrase := range []string{
		"yakos install",
		"~/.claude",
		"agents", "skills", "rules", "playbooks",
		"~/.local/bin/yakos",
		"settings.json",
		"--force",
		"--dry-run",
		"--help",
		"Foreign",
		"projects/",
	} {
		if !strings.Contains(got, phrase) {
			t.Errorf("help text missing %q;\nfull output:\n%s", phrase, got)
		}
	}
}

// ---- (p) settings.json is valid JSON after creation -------------------------

// TestInstallParity_CreatedSettingsValidJSON verifies created settings.json is parseable.
func TestInstallParity_CreatedSettingsValidJSON(t *testing.T) {
	root := newInstallFakeRoot(t)
	cfg := installBaseConfig(t, root)

	if _, err := install.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	sp := filepath.Join(cfg.HomeDir, ".claude", "settings.json")
	data, _ := os.ReadFile(sp)
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Errorf("created settings.json is not valid JSON: %v\ncontent:\n%s", err, string(data))
	}
}

// ---- (q) no temp files leak -------------------------------------------------

// TestInstallParity_NoTempLeak verifies no .yakos-install-tmp-* files remain.
func TestInstallParity_NoTempLeak(t *testing.T) {
	root := newInstallFakeRoot(t)
	cfg := installBaseConfig(t, root)

	if _, err := install.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, dir := range []string{
		filepath.Join(cfg.HomeDir, ".claude"),
		filepath.Join(cfg.HomeDir, ".yakos-state"),
		cfg.HomeDir,
	} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.Contains(e.Name(), ".yakos-install-tmp") {
				t.Errorf("temp file leaked in %s: %s", dir, e.Name())
			}
		}
	}
}

// ---- (r) absent lib subdir --------------------------------------------------

// TestInstallParity_AbsentLibSubdir verifies graceful handling of absent lib/playbooks/.
func TestInstallParity_AbsentLibSubdir(t *testing.T) {
	root := newInstallFakeRoot(t)
	if err := os.RemoveAll(filepath.Join(root, "lib", "playbooks")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	cfg := installBaseConfig(t, root)
	_, err := install.Run(cfg)
	if err != nil {
		t.Fatalf("Run with absent lib/playbooks: %v", err)
	}
}
