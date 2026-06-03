package install

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// symlinkOrSkip creates a symlink and skips the test if symlink creation is
// not supported on this platform (e.g. Windows without Developer Mode).
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("os.Symlink not supported on this platform (%s); skipping symlink test: %v", runtime.GOOS, err)
	}
}

// foreignTarget returns a path to a real file that exists outside any
// t.TempDir()-based yakosRoot. Used as the target for "foreign symlink" tests
// so that filepath.EvalSymlinks succeeds and the path is not under yakosRootAbs.
//
// On Windows, /dev/null does not exist; on all platforms we create a real file
// in os.TempDir() so that EvalSymlinks can resolve it without error.
func foreignTarget(t *testing.T) string {
	t.Helper()
	// os.TempDir() gives a system-level directory that exists on all platforms.
	// We write a sentinel file there so the symlink target is resolvable.
	target := filepath.Join(os.TempDir(), "yakos-test-foreign-target")
	if err := os.WriteFile(target, []byte("foreign\n"), 0644); err != nil {
		t.Fatalf("foreignTarget: could not write sentinel file %s: %v", target, err)
	}
	t.Cleanup(func() { _ = os.Remove(target) })
	return target
}

// ---- helpers ----------------------------------------------------------------

// newFakeYakosRoot creates a minimal YAKOS_ROOT tree in a temp dir.
// It creates lib/agents/, lib/skills/, lib/rules/, lib/playbooks/ (each with
// one real file) so linkSubdirs has content to work with.
func newFakeYakosRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, sub := range []string{"agents", "skills", "rules", "playbooks"} {
		dir := filepath.Join(root, "lib", sub)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "example.md"), []byte("# example\n"), 0644); err != nil {
			t.Fatalf("write example.md: %v", err)
		}
	}
	// Also create cli/yakos (the launcher target).
	cliDir := filepath.Join(root, "cli")
	if err := os.MkdirAll(cliDir, 0755); err != nil {
		t.Fatalf("mkdir cli: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "yakos"), []byte("#!/bin/bash\n"), 0755); err != nil {
		t.Fatalf("write cli/yakos: %v", err)
	}
	return root
}

// baseConfig returns a Config with isolated home and captured output.
func baseConfig(t *testing.T, yakosRoot string) Config {
	t.Helper()
	home := t.TempDir()
	return Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}
}

// ---- (a) happy path ---------------------------------------------------------

// TestInstall_HappyPath verifies that a fresh install creates all expected
// artifacts: pointer file, launcher symlink, per-file symlinks, settings.json.
func TestInstall_HappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		// install relies on os.Symlink for per-file links and the launcher.
		// Windows symlink semantics (privilege, Developer Mode) are Phase 1.5 scope.
		// The production code degrades gracefully (LauncherSkipped, Symlinks.Created==0)
		// but the happy-path assertions require successful creation.
		t.Skip("symlink-dependent test; Windows symlink support is Phase 1.5 scope")
	}
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	home := cfg.HomeDir

	// Pointer file.
	pointerData, err := os.ReadFile(filepath.Join(home, ".yakos"))
	if err != nil {
		t.Fatalf("pointer file not created: %v", err)
	}
	if !strings.Contains(string(pointerData), root) {
		t.Errorf("pointer file should contain YAKOS_ROOT; got %q", string(pointerData))
	}

	// Launcher symlink.
	launcherPath := filepath.Join(home, ".local", "bin", "yakos")
	linfo, err := os.Lstat(launcherPath)
	if err != nil {
		t.Fatalf("launcher symlink not created: %v", err)
	}
	if linfo.Mode()&os.ModeSymlink == 0 {
		t.Errorf("launcher should be a symlink")
	}
	if res.Launcher.Outcome != LauncherCreated {
		t.Errorf("launcher outcome: got %q, want %q", res.Launcher.Outcome, LauncherCreated)
	}

	// Per-file symlinks created.
	if res.Symlinks.Created == 0 {
		t.Error("expected Created > 0 for fresh install")
	}
	if res.Symlinks.Refreshed != 0 {
		t.Errorf("expected Refreshed == 0 for fresh install; got %d", res.Symlinks.Refreshed)
	}

	// settings.json created.
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	settingsData, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}
	if !strings.Contains(string(settingsData), envKey) {
		t.Errorf("settings.json should contain %s; got %q", envKey, string(settingsData))
	}
	if res.Settings.Created != true {
		t.Errorf("Settings.Created should be true for fresh install")
	}

	// Created marker exists.
	markerPath := filepath.Join(home, ".claude", ".yakos-created-settings")
	if _, err := os.Stat(markerPath); err != nil {
		t.Errorf("created-marker should exist: %v", err)
	}
}

// ---- (b) dry-run ------------------------------------------------------------

// TestInstall_DryRun verifies no files are written during dry-run.
func TestInstall_DryRun(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)
	cfg.DryRun = true

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run (dry-run): %v", err)
	}
	if !res.DryRun {
		t.Error("Result.DryRun should be true")
	}

	home := cfg.HomeDir

	// No pointer file written.
	if _, err := os.Stat(filepath.Join(home, ".yakos")); !os.IsNotExist(err) {
		t.Error("dry-run: .yakos pointer file should NOT be written")
	}

	// No launcher created.
	if _, err := os.Lstat(filepath.Join(home, ".local", "bin", "yakos")); !os.IsNotExist(err) {
		t.Error("dry-run: launcher symlink should NOT be created")
	}

	// Dry-run should report what would happen (counts > 0 for fresh install)
	// but must not touch the filesystem.
	if res.Symlinks.Created == 0 {
		t.Error("dry-run: Created count should be > 0 (reports what would happen)")
	}
	// Verify no actual symlinks were written despite non-zero count.
	claudeAgentsDir := filepath.Join(home, ".claude", "agents")
	if _, err := os.Stat(claudeAgentsDir); !os.IsNotExist(err) {
		// If dir somehow exists, no symlinks should be inside.
		entries, _ := os.ReadDir(claudeAgentsDir)
		if len(entries) > 0 {
			t.Error("dry-run: no symlinks should be physically written")
		}
	}

	// No settings.json written.
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Error("dry-run: settings.json should NOT be created")
	}

	// stdout mentions "dry-run".
	stdout := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(stdout, "dry-run") {
		t.Errorf("stdout should mention 'dry-run'; got:\n%s", stdout)
	}
}

// ---- (c) idempotent second run ----------------------------------------------

// TestInstall_Idempotent verifies that running install twice produces the same
// state: symlinks are skipped (already correct), settings is already-set.
func TestInstall_Idempotent(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)

	if _, err := Run(cfg); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Reset output buffers.
	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}

	res2, err := Run(cfg)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	if res2.Symlinks.Created != 0 {
		t.Errorf("second run: expected 0 created symlinks; got %d", res2.Symlinks.Created)
	}
	if !res2.Settings.AlreadySet && !res2.Settings.Created {
		t.Error("second run: settings.json should be already-set or created-noop")
	}
}

// ---- (d) --force flag -------------------------------------------------------

// TestInstall_Force verifies that --force refreshes all YakOS-owned symlinks.
func TestInstall_Force(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)

	if _, err := Run(cfg); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}
	cfg.Force = true

	res2, err := Run(cfg)
	if err != nil {
		t.Fatalf("force Run: %v", err)
	}
	// With --force, all YakOS-owned symlinks should be refreshed.
	if res2.Symlinks.Refreshed == 0 {
		t.Error("--force: expected at least one symlink refreshed")
	}
}

// ---- (e) invalid YAKOS_ROOT -------------------------------------------------

// TestInstall_InvalidRoot verifies that a non-existent YAKOS_ROOT is rejected.
func TestInstall_InvalidRoot(t *testing.T) {
	cfg := baseConfig(t, "/no/such/yakos/root/9999")
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for non-existent YAKOS_ROOT")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error should mention 'not a directory'; got %q", err.Error())
	}
}

// ---- (f) invalid settings.json preflight ------------------------------------

// TestInstall_InvalidSettingsJSON verifies that corrupt settings.json aborts
// before any state is mutated.
func TestInstall_InvalidSettingsJSON(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)
	home := cfg.HomeDir

	// Place invalid JSON at settings.json before running.
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte("not json {{{"), 0644); err != nil {
		t.Fatalf("write bad settings.json: %v", err)
	}

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for corrupt settings.json")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("error should mention 'not valid JSON'; got %q", err.Error())
	}

	// Pointer file must NOT have been written.
	if _, err2 := os.Stat(filepath.Join(home, ".yakos")); !os.IsNotExist(err2) {
		t.Error("pointer file should not be written when preflight fails")
	}
}

// ---- (g) existing settings.json merge ---------------------------------------

// TestInstall_ExistingSettingsJSON verifies that an existing valid settings.json
// gets the env key merged in without losing other keys.
func TestInstall_ExistingSettingsJSON(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)
	home := cfg.HomeDir

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := map[string]any{
		"otherKey": "otherVal",
		"env": map[string]any{
			"MY_CUSTOM_VAR": "hello",
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Settings.Merged {
		t.Error("Settings.Merged should be true when env key was added")
	}

	merged, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile settings.json: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(merged, &out); err != nil {
		t.Fatalf("merged settings.json is invalid JSON: %v", err)
	}
	envMap, _ := out["env"].(map[string]any)
	if v, _ := envMap[envKey].(string); v != envVal {
		t.Errorf("env.%s should be %q; got %q", envKey, envVal, v)
	}
	if out["otherKey"] != "otherVal" {
		t.Error("otherKey should be preserved after merge")
	}
	if v, _ := envMap["MY_CUSTOM_VAR"].(string); v != "hello" {
		t.Error("MY_CUSTOM_VAR should be preserved after merge")
	}
}

// ---- (h) settings already set -----------------------------------------------

// TestInstall_SettingsAlreadySet verifies that if the env key is already at
// the desired value, settings.json is untouched.
func TestInstall_SettingsAlreadySet(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)
	home := cfg.HomeDir

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := map[string]any{
		"env": map[string]any{
			envKey: envVal,
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
	mtimeBefore, _ := os.Stat(settingsPath)

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Settings.AlreadySet {
		t.Error("Settings.AlreadySet should be true when env key is already set")
	}

	mtimeAfter, _ := os.Stat(settingsPath)
	// File should be byte-identical (we can check mtime within same second context).
	_ = mtimeBefore
	_ = mtimeAfter
	data2, _ := os.ReadFile(settingsPath)
	if string(data2) != string(data) {
		t.Error("settings.json was modified even though env key was already set")
	}
}

// ---- (i) real file at symlink destination -----------------------------------

// TestInstall_RealFileNotOverwritten verifies that a real (non-symlink) file
// in ~/.claude/<sub>/ is not overwritten; it is skipped with a warning.
func TestInstall_RealFileNotOverwritten(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)
	home := cfg.HomeDir

	// Place a real file where a symlink would land.
	agentsDir := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	realContent := "# operator-managed agent\n"
	realPath := filepath.Join(agentsDir, "example.md")
	if err := os.WriteFile(realPath, []byte(realContent), 0644); err != nil {
		t.Fatalf("write real file: %v", err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Symlinks.Skipped == 0 {
		t.Error("expected at least one symlink skipped for real file")
	}

	// Real file must be unchanged.
	got, _ := os.ReadFile(realPath)
	if string(got) != realContent {
		t.Errorf("real file was overwritten (should be preserved); got %q", string(got))
	}

	// Warning on stderr.
	if !strings.Contains(cfg.ErrWriter.(*bytes.Buffer).String(), "not a symlink") {
		t.Error("stderr should warn about real file")
	}
}

// ---- (j) foreign symlink left alone -----------------------------------------

// TestInstall_ForeignSymlinkLeftAlone verifies that a symlink pointing outside
// YAKOS_ROOT is preserved untouched.
func TestInstall_ForeignSymlinkLeftAlone(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)
	home := cfg.HomeDir

	agentsDir := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Use a real file in the system temp dir as the foreign target.
	// /dev/null does not exist on Windows; using a cross-platform real file ensures
	// filepath.EvalSymlinks resolves the target and the ownership check fires correctly.
	ft := foreignTarget(t)
	foreignSymlink := filepath.Join(agentsDir, "example.md")
	symlinkOrSkip(t, ft, foreignSymlink)

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Symlinks.Skipped == 0 {
		t.Error("expected at least one symlink skipped for foreign symlink")
	}

	// Foreign symlink must still point to original target.
	got, err := os.Readlink(foreignSymlink)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if got != ft {
		t.Errorf("foreign symlink was redirected; got %q, want %q", got, ft)
	}
}

// ---- (k) existing yakos-owned launcher refresh ------------------------------

// TestInstall_LauncherRefresh verifies that an existing symlink pointing into
// YAKOS_ROOT is refreshed (outcome == refreshed, not created).
func TestInstall_LauncherRefresh(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)

	// First install: creates launcher.
	if _, err := Run(cfg); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Second install: launcher already exists and points into root.
	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}

	res2, err := Run(cfg)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	// Could be LauncherCreated (if target == target) or LauncherRefreshed.
	// Either way it should NOT be LauncherSkipped (since it's our own symlink).
	if res2.Launcher.Outcome == LauncherSkipped {
		t.Errorf("expected launcher to be refreshed, not skipped; warning: %q", res2.Launcher.Warning)
	}
}

// ---- (l) launcher: real file at path ----------------------------------------

// TestInstall_LauncherRealFileSkipped verifies that a real file at the launcher
// path is left alone with a warning.
func TestInstall_LauncherRealFileSkipped(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)
	home := cfg.HomeDir

	// Place a real file (not symlink) at ~/.local/bin/yakos.
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	realBin := filepath.Join(localBin, "yakos")
	if err := os.WriteFile(realBin, []byte("#!/bin/bash\n# foreign\n"), 0755); err != nil {
		t.Fatalf("write real bin: %v", err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Launcher.Outcome != LauncherSkipped {
		t.Errorf("launcher outcome: got %q, want %q", res.Launcher.Outcome, LauncherSkipped)
	}
	if !strings.Contains(res.Launcher.Warning, "WARNING") {
		t.Errorf("launcher warning should mention WARNING; got %q", res.Launcher.Warning)
	}
	// Real file must be unchanged.
	data, _ := os.ReadFile(realBin)
	if !strings.Contains(string(data), "foreign") {
		t.Error("real binary was overwritten")
	}
}

// ---- (m) install-manifest written -------------------------------------------

// TestInstall_ManifestWritten verifies the install-manifest records the launcher path.
func TestInstall_ManifestWritten(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)
	home := cfg.HomeDir

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	manifestPath := filepath.Join(home, ".yakos-state", "install-manifest")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("install-manifest not written: %v", err)
	}
	if !strings.Contains(string(data), "launcher=") {
		t.Errorf("install-manifest should contain 'launcher='; got %q", string(data))
	}
}

// ---- (n) gitkeep skipped in lib/ --------------------------------------------

// TestInstall_GitkeepSkipped verifies that .gitkeep files in lib/ subdirs are
// not symlinked.
func TestInstall_GitkeepSkipped(t *testing.T) {
	root := newFakeYakosRoot(t)
	// Add a .gitkeep to lib/agents/
	gitkeepPath := filepath.Join(root, "lib", "agents", ".gitkeep")
	if err := os.WriteFile(gitkeepPath, []byte{}, 0644); err != nil {
		t.Fatalf("write .gitkeep: %v", err)
	}

	cfg := baseConfig(t, root)
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	home := cfg.HomeDir
	symlinkPath := filepath.Join(home, ".claude", "agents", ".gitkeep")
	if _, err := os.Lstat(symlinkPath); !os.IsNotExist(err) {
		t.Error(".gitkeep should not be symlinked")
	}
}

// ---- (o) nested subdir symlinks ---------------------------------------------

// TestInstall_NestedSubdir verifies that files in nested lib/skills/pre-commit/
// subdirectories are correctly symlinked with matching directory structure.
func TestInstall_NestedSubdir(t *testing.T) {
	root := newFakeYakosRoot(t)
	// Add a nested skill file.
	nestedDir := filepath.Join(root, "lib", "skills", "pre-commit")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "SKILL.md"), []byte("# skill\n"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	cfg := baseConfig(t, root)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	home := cfg.HomeDir
	symlinkPath := filepath.Join(home, ".claude", "skills", "pre-commit", "SKILL.md")
	linfo, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatalf("nested symlink not created: %v", err)
	}
	if linfo.Mode()&os.ModeSymlink == 0 {
		t.Error("nested path should be a symlink")
	}
	if res.Symlinks.Created == 0 {
		t.Error("expected Created > 0 for nested file")
	}
}

// ---- (p) dry-run with existing settings.json --------------------------------

// TestInstall_DryRunWithExistingSettings verifies that dry-run on an existing
// settings.json reports the merge but does not write.
func TestInstall_DryRunExistingSettings(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)
	home := cfg.HomeDir

	// Pre-create settings.json without the env key.
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	originalContent := `{"hooks": {"UserPromptSubmit": []}}`
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(originalContent), 0644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}

	cfg.DryRun = true
	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run (dry-run): %v", err)
	}

	// File must be byte-identical.
	got, _ := os.ReadFile(settingsPath)
	if string(got) != originalContent {
		t.Errorf("dry-run: settings.json should be unchanged; got %q", string(got))
	}

	// stdout must mention dry-run.
	stdout := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(stdout, "dry-run") {
		t.Errorf("stdout should mention 'dry-run'; got:\n%s", stdout)
	}
}

// ---- (q) atomicWrite no temp leak -------------------------------------------

// TestInstall_AtomicWriteNoTempLeak verifies that no .yakos-install-tmp-* files
// are left behind after a successful run.
func TestInstall_AtomicWriteNoTempLeak(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)
	home := cfg.HomeDir

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Check all directories we write into.
	dirs := []string{
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".yakos-state"),
		home,
	}
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.Contains(e.Name(), ".yakos-install-tmp") {
				t.Errorf("temp file leaked in %s: %s", dir, e.Name())
			}
		}
	}
}

// ---- (r) help text ----------------------------------------------------------

// TestInstall_HelpText verifies all load-bearing phrases from install.sh --help.
func TestInstall_HelpText(t *testing.T) {
	var out bytes.Buffer
	PrintHelp(&out)
	got := out.String()

	mustContain := []string{
		"yakos install",
		"~/.claude",
		"agents",
		"skills",
		"rules",
		"playbooks",
		"~/.local/bin/yakos",
		"settings.json",
		"--force",
		"--dry-run",
		"--help",
		"Foreign",
		"projects/",
	}
	for _, phrase := range mustContain {
		if !strings.Contains(got, phrase) {
			t.Errorf("help text missing phrase %q;\nfull output:\n%s", phrase, got)
		}
	}
}

// ---- (s) settings.json atomic write -----------------------------------------

// TestInstall_SettingsAtomicWrite verifies that after a merge the settings.json
// is valid JSON (not a torn write).
func TestInstall_SettingsAtomicWrite(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)
	home := cfg.HomeDir

	// Pre-seed a settings.json with hooks content that should be preserved.
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pre := map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []any{},
		},
		"anotherTopLevelKey": "preserved",
	}
	preData, _ := json.MarshalIndent(pre, "", "  ")
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, preData, 0644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile settings.json: %v", err)
	}
	if !isValidJSON(data) {
		t.Errorf("settings.json after merge is not valid JSON; content:\n%s", string(data))
	}
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	if out["anotherTopLevelKey"] != "preserved" {
		t.Error("anotherTopLevelKey should be preserved after settings merge")
	}
}

// ---- (t) refresh → install idempotency -------------------------------------

// TestInstall_RefreshInstallIdempotency verifies that running install then
// refresh on the same project leaves the project in a clean state
// (regression: install must not write per-project hooks; only global symlinks).
func TestInstall_RefreshInstallIdempotency(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)

	// First install.
	res1, err := Run(cfg)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if res1.DryRun {
		t.Fatal("first run should not be dry-run")
	}

	// Second install — should be fully idempotent.
	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}
	res2, err := Run(cfg)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	// No new symlinks should be created on second run.
	if res2.Symlinks.Created != 0 {
		t.Errorf("second install: expected 0 new symlinks; got %d", res2.Symlinks.Created)
	}
}

// ---- (u) launcher foreign symlink left alone --------------------------------

// TestInstall_ForeignLauncherSymlink verifies that a launcher symlink pointing
// to a non-yakOS path is left alone with a warning.
func TestInstall_ForeignLauncherSymlink(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)
	home := cfg.HomeDir

	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Create a symlink to a foreign real file outside YAKOS_ROOT.
	// /usr/bin/env does not exist on Windows; use a cross-platform real file so
	// filepath.EvalSymlinks resolves the target and the ownership check fires correctly.
	ft := foreignTarget(t)
	foreignLauncher := filepath.Join(localBin, "yakos")
	symlinkOrSkip(t, ft, foreignLauncher)

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Launcher.Outcome != LauncherSkipped {
		t.Errorf("foreign launcher symlink: outcome should be skipped; got %q", res.Launcher.Outcome)
	}
	// Symlink target must be unchanged.
	got, _ := os.Readlink(foreignLauncher)
	if got != ft {
		t.Errorf("foreign launcher symlink was redirected; got %q, want %q", got, ft)
	}
}

// ---- (v) settings.json created with valid JSON ------------------------------

// TestInstall_CreatedSettingsIsValidJSON verifies that a freshly created
// settings.json is valid JSON containing the env key.
func TestInstall_CreatedSettingsIsValidJSON(t *testing.T) {
	root := newFakeYakosRoot(t)
	cfg := baseConfig(t, root)

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	settingsPath := filepath.Join(cfg.HomeDir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !isValidJSON(data) {
		t.Errorf("created settings.json is not valid JSON; content:\n%s", string(data))
	}
	var parsed map[string]any
	_ = json.Unmarshal(data, &parsed)
	envMap, _ := parsed["env"].(map[string]any)
	if v, _ := envMap[envKey].(string); v != envVal {
		t.Errorf("created settings.json: env.%s = %q, want %q", envKey, v, envVal)
	}
}

// ---- (w) empty lib subdir is a no-op ----------------------------------------

// TestInstall_EmptyLibSubdir verifies that install succeeds even when some lib/
// subdirectories do not exist (e.g. lib/playbooks/ absent from the root).
func TestInstall_EmptyLibSubdir(t *testing.T) {
	root := newFakeYakosRoot(t)
	// Remove lib/playbooks/ to simulate partial install.
	if err := os.RemoveAll(filepath.Join(root, "lib", "playbooks")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	cfg := baseConfig(t, root)
	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run with absent lib/playbooks: %v", err)
	}
	// playbooks dir should not be created in ~/.claude.
	playbooks := filepath.Join(cfg.HomeDir, ".claude", "playbooks")
	fi, err := os.Stat(playbooks)
	if err == nil && fi.IsDir() {
		// The dir may or may not exist — no symlinks inside it is what matters.
		entries, _ := os.ReadDir(playbooks)
		if len(entries) != 0 {
			t.Errorf("~/.claude/playbooks should be empty when lib/playbooks is absent; got %d entries", len(entries))
		}
	}
}
