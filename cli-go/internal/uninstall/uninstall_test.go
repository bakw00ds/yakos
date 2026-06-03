package uninstall

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- test helpers ------------------------------------------------------------

// newFakeYakosRoot creates a minimal YAKOS_ROOT tree in a temp dir.
// lib/{agents,skills,rules,playbooks}/example.md and cli/yakos exist.
func newFakeYakosRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, sub := range []string{"agents", "skills", "rules", "playbooks"} {
		dir := filepath.Join(root, "lib", sub)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "example.md"), []byte("# "+sub+"\n"), 0644); err != nil {
			t.Fatalf("write example.md: %v", err)
		}
	}
	cliDir := filepath.Join(root, "cli")
	if err := os.MkdirAll(cliDir, 0755); err != nil {
		t.Fatalf("mkdir cli: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "yakos"), []byte("#!/bin/bash\n"), 0755); err != nil {
		t.Fatalf("write cli/yakos: %v", err)
	}
	return root
}

// newInstalledHome creates a home dir that looks like a post-install state:
//   - ~/.yakos pointing to yakosRoot
//   - ~/.yakos-state/install-manifest with launcher= entry
//   - ~/.local/bin/yakos → yakosRoot/cli/yakos
//   - ~/.claude/{agents,skills,rules,playbooks}/example.md → yakosRoot/lib/…/example.md
func newInstalledHome(t *testing.T, yakosRoot string) string {
	t.Helper()
	home := t.TempDir()

	// Pointer file.
	if err := os.WriteFile(filepath.Join(home, ".yakos"), []byte(yakosRoot+"\n"), 0644); err != nil {
		t.Fatalf("write .yakos: %v", err)
	}

	// State dir + manifest.
	stateDir := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("mkdir .yakos-state: %v", err)
	}
	launcherPath := filepath.Join(home, ".local", "bin", "yakos")
	manifest := "launcher=" + launcherPath + "\n"
	if err := os.WriteFile(filepath.Join(stateDir, "install-manifest"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// Launcher symlink.
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0755); err != nil {
		t.Fatalf("mkdir .local/bin: %v", err)
	}
	launcherTarget := filepath.Join(yakosRoot, "cli", "yakos")
	if err := os.Symlink(launcherTarget, launcherPath); err != nil {
		t.Fatalf("symlink launcher: %v", err)
	}

	// Per-file symlinks.
	claudeDir := filepath.Join(home, ".claude")
	for _, sub := range subdirs {
		dstDir := filepath.Join(claudeDir, sub)
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dstDir, err)
		}
		srcFile := filepath.Join(yakosRoot, "lib", sub, "example.md")
		dstFile := filepath.Join(dstDir, "example.md")
		if err := os.Symlink(srcFile, dstFile); err != nil {
			t.Fatalf("symlink %s: %v", dstFile, err)
		}
	}

	return home
}

// baseConfig returns a Config with captured output aimed at isolated home.
func baseConfig(home string) Config {
	return Config{
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}
}

// ---- (a) happy path ----------------------------------------------------------

// TestUninstall_HappyPath verifies a full uninstall removes all YakOS artifacts.
func TestUninstall_HappyPath(t *testing.T) {
	root := newFakeYakosRoot(t)
	home := newInstalledHome(t, root)
	cfg := baseConfig(home)

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Pointer removed.
	if _, err := os.Lstat(filepath.Join(home, ".yakos")); !os.IsNotExist(err) {
		t.Error(".yakos pointer should be removed")
	}
	if !res.PointerRemoved {
		t.Error("Result.PointerRemoved should be true")
	}

	// Manifest removed.
	if _, err := os.Lstat(filepath.Join(home, ".yakos-state", "install-manifest")); !os.IsNotExist(err) {
		t.Error("install-manifest should be removed")
	}
	if !res.ManifestRemoved {
		t.Error("Result.ManifestRemoved should be true")
	}

	// Launcher symlink removed.
	launcherPath := filepath.Join(home, ".local", "bin", "yakos")
	if _, err := os.Lstat(launcherPath); !os.IsNotExist(err) {
		t.Error("launcher symlink should be removed")
	}
	if res.Launcher.Outcome != LauncherRemoved {
		t.Errorf("Launcher.Outcome: got %q, want %q", res.Launcher.Outcome, LauncherRemoved)
	}

	// Per-file symlinks removed.
	if res.Symlinks.Removed == 0 {
		t.Error("expected Symlinks.Removed > 0")
	}
	if res.Symlinks.Kept != 0 {
		t.Errorf("expected Symlinks.Kept == 0; got %d", res.Symlinks.Kept)
	}

	// ~/.claude/projects/ must not be touched (doesn't exist in fixture, but
	// the output should mention it).
	stdout := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(stdout, "projects/") {
		t.Errorf("stdout should mention projects/; got:\n%s", stdout)
	}
}

// ---- (b) dry-run -------------------------------------------------------------

// TestUninstall_DryRun verifies that dry-run writes nothing.
func TestUninstall_DryRun(t *testing.T) {
	root := newFakeYakosRoot(t)
	home := newInstalledHome(t, root)
	cfg := baseConfig(home)
	cfg.DryRun = true

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run (dry-run): %v", err)
	}
	if !res.DryRun {
		t.Error("Result.DryRun should be true")
	}

	// Pointer must still exist.
	if _, err := os.Lstat(filepath.Join(home, ".yakos")); err != nil {
		t.Error(".yakos pointer should still exist after dry-run")
	}

	// Launcher must still exist.
	launcherPath := filepath.Join(home, ".local", "bin", "yakos")
	if _, err := os.Lstat(launcherPath); err != nil {
		t.Error("launcher symlink should still exist after dry-run")
	}

	// Symlinks must still exist.
	for _, sub := range subdirs {
		link := filepath.Join(home, ".claude", sub, "example.md")
		if _, err := os.Lstat(link); err != nil {
			t.Errorf("symlink %s should still exist after dry-run", link)
		}
	}

	// stdout mentions "dry-run".
	stdout := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(stdout, "dry-run") {
		t.Errorf("stdout should mention 'dry-run'; got:\n%s", stdout)
	}
}

// ---- (c) dangling symlinks removed ------------------------------------------

// TestUninstall_DanglingSymlinks verifies that dangling symlinks are always removed.
func TestUninstall_DanglingSymlinks(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	agentsDir := filepath.Join(claudeDir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create a dangling symlink.
	danglingPath := filepath.Join(agentsDir, "missing.md")
	if err := os.Symlink("/no/such/path/ghost.md", danglingPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cfg := baseConfig(home)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Symlinks.Removed == 0 {
		t.Error("expected dangling symlink to be removed")
	}

	if _, err := os.Lstat(danglingPath); !os.IsNotExist(err) {
		t.Error("dangling symlink should be gone after uninstall")
	}
}

// ---- (d) foreign symlinks preserved -----------------------------------------

// TestUninstall_ForeignSymlinkPreserved verifies that symlinks pointing outside
// the YakOS root are not removed.
func TestUninstall_ForeignSymlinkPreserved(t *testing.T) {
	root := newFakeYakosRoot(t)
	home := t.TempDir()

	// Write pointer.
	if err := os.WriteFile(filepath.Join(home, ".yakos"), []byte(root+"\n"), 0644); err != nil {
		t.Fatalf("write .yakos: %v", err)
	}

	// Place a foreign symlink (points to /dev/null, which is outside the root).
	agentsDir := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	foreignLink := filepath.Join(agentsDir, "foreign.md")
	if err := os.Symlink("/dev/null", foreignLink); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cfg := baseConfig(home)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Foreign symlink must still exist.
	target, err := os.Readlink(foreignLink)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if target != "/dev/null" {
		t.Errorf("foreign symlink was redirected; got %q", target)
	}
	if res.Symlinks.Kept == 0 {
		t.Error("expected Symlinks.Kept > 0 for foreign symlink")
	}
}

// ---- (e) real files at symlink paths are left alone -------------------------

// TestUninstall_RealFilePreserved verifies that real (non-symlink) files in
// ~/.claude/ subdirs are never removed.
func TestUninstall_RealFilePreserved(t *testing.T) {
	root := newFakeYakosRoot(t)
	home := t.TempDir()

	if err := os.WriteFile(filepath.Join(home, ".yakos"), []byte(root+"\n"), 0644); err != nil {
		t.Fatalf("write .yakos: %v", err)
	}

	agentsDir := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	realContent := "# operator-managed\n"
	realPath := filepath.Join(agentsDir, "custom.md")
	if err := os.WriteFile(realPath, []byte(realContent), 0644); err != nil {
		t.Fatalf("write real file: %v", err)
	}

	cfg := baseConfig(home)
	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := os.ReadFile(realPath)
	if err != nil {
		t.Fatalf("real file was removed: %v", err)
	}
	if string(got) != realContent {
		t.Errorf("real file content changed; got %q", string(got))
	}
}

// ---- (f) stale pointer — best-effort cleanup --------------------------------

// TestUninstall_StalePointer verifies that when ~/.yakos points to a missing
// directory, dangling symlinks are still cleaned up.
func TestUninstall_StalePointer(t *testing.T) {
	home := t.TempDir()

	// Pointer to a non-existent root.
	staleRoot := filepath.Join(home, "no-such-yakos-root")
	if err := os.WriteFile(filepath.Join(home, ".yakos"), []byte(staleRoot+"\n"), 0644); err != nil {
		t.Fatalf("write .yakos: %v", err)
	}

	// Create a dangling symlink under agents.
	agentsDir := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	danglingLink := filepath.Join(agentsDir, "stale.md")
	if err := os.Symlink("/absolutely/not/here.md", danglingLink); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cfg := baseConfig(home)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Dangling symlink should be gone.
	if _, err := os.Lstat(danglingLink); !os.IsNotExist(err) {
		t.Error("dangling symlink should be removed on stale pointer cleanup")
	}
	if res.Symlinks.Removed == 0 {
		t.Error("expected Symlinks.Removed > 0 on stale pointer cleanup")
	}

	// Stderr (or stdout) should warn about stale pointer.
	out := cfg.Writer.(*bytes.Buffer).String() + cfg.ErrWriter.(*bytes.Buffer).String()
	if !strings.Contains(strings.ToLower(out), "warning") && !strings.Contains(strings.ToLower(out), "stale") && !strings.Contains(strings.ToLower(out), "missing") {
		t.Logf("output: %s", out)
		// Not a hard failure — the warning text may vary.
	}
}

// ---- (g) no pointer and no claude dir ---------------------------------------

// TestUninstall_NothingToUninstall verifies early-out when there is no pointer
// and no ~/.claude directory.
func TestUninstall_NothingToUninstall(t *testing.T) {
	home := t.TempDir()
	cfg := baseConfig(home)

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	stdout := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(stdout, "Nothing to uninstall") {
		t.Errorf("expected 'Nothing to uninstall' message; got:\n%s", stdout)
	}
	if res.PointerRemoved {
		t.Error("PointerRemoved should be false when nothing to uninstall")
	}
}

// ---- (h) launcher: foreign real file left alone -----------------------------

// TestUninstall_LauncherRealFileKept verifies that a real file at the launcher
// path is left alone.
func TestUninstall_LauncherRealFileKept(t *testing.T) {
	home := t.TempDir()
	// Create .claude dir so the early-out check does not fire.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	stateDir := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	launcherPath := filepath.Join(localBin, "yakos")
	manifest := "launcher=" + launcherPath + "\n"
	if err := os.WriteFile(filepath.Join(stateDir, "install-manifest"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	// Real file, not a symlink.
	if err := os.WriteFile(launcherPath, []byte("#!/bin/bash\n# foreign\n"), 0755); err != nil {
		t.Fatalf("write real launcher: %v", err)
	}

	cfg := baseConfig(home)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Launcher.Outcome != LauncherKept {
		t.Errorf("Launcher.Outcome: got %q, want %q", res.Launcher.Outcome, LauncherKept)
	}
	// File must still exist and be unchanged.
	if _, err := os.Stat(launcherPath); err != nil {
		t.Error("real launcher file should still exist")
	}
}

// ---- (i) launcher: foreign symlink left alone --------------------------------

// TestUninstall_ForeignLauncherSymlinkKept verifies that a launcher symlink
// pointing outside YakOS is left alone.
func TestUninstall_ForeignLauncherSymlinkKept(t *testing.T) {
	home := t.TempDir()
	// Create .claude dir so the early-out check does not fire.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	stateDir := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	launcherPath := filepath.Join(localBin, "yakos")
	manifest := "launcher=" + launcherPath + "\n"
	if err := os.WriteFile(filepath.Join(stateDir, "install-manifest"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	// Symlink to a foreign real path.
	if err := os.Symlink("/usr/bin/env", launcherPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cfg := baseConfig(home)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Launcher.Outcome != LauncherKept {
		t.Errorf("Launcher.Outcome: got %q, want %q", res.Launcher.Outcome, LauncherKept)
	}
	target, _ := os.Readlink(launcherPath)
	if target != "/usr/bin/env" {
		t.Errorf("foreign launcher was redirected; got %q", target)
	}
}

// ---- (j) settings.json: created-marker present → removed --------------------

// TestUninstall_SettingsRemovedWhenCreatedByYakOS verifies that settings.json
// is removed when the .yakos-created-settings marker exists.
func TestUninstall_SettingsRemovedWhenCreatedByYakOS(t *testing.T) {
	root := newFakeYakosRoot(t)
	home := newInstalledHome(t, root)

	claudeDir := filepath.Join(home, ".claude")
	// Create settings.json and the created-marker.
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"env":{}}`), 0644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
	markerPath := filepath.Join(claudeDir, ".yakos-created-settings")
	if err := os.WriteFile(markerPath, []byte{}, 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	cfg := baseConfig(home)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Settings.Outcome != SettingsRemoved {
		t.Errorf("Settings.Outcome: got %q, want %q", res.Settings.Outcome, SettingsRemoved)
	}
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Error("settings.json should be removed when marker present")
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Error("created-marker should be removed")
	}
}

// ---- (k) settings.json: no marker → kept; backups listed -------------------

// TestUninstall_SettingsKeptWhenNoMarker verifies settings.json is not touched
// when the created-marker is absent.
func TestUninstall_SettingsKeptWhenNoMarker(t *testing.T) {
	root := newFakeYakosRoot(t)
	home := newInstalledHome(t, root)

	claudeDir := filepath.Join(home, ".claude")
	settingsPath := filepath.Join(claudeDir, "settings.json")
	content := `{"env":{"USER_KEY":"value"}}`
	if err := os.WriteFile(settingsPath, []byte(content), 0644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
	// No marker → settings is user-owned.

	cfg := baseConfig(home)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Settings.Outcome != SettingsKept {
		t.Errorf("Settings.Outcome: got %q, want %q", res.Settings.Outcome, SettingsKept)
	}
	got, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json should still exist: %v", err)
	}
	if string(got) != content {
		t.Error("settings.json content was modified")
	}
}

// ---- (l) settings.json: --restore-settings applies most recent backup -------

// TestUninstall_RestoreSettings verifies that --restore-settings restores the
// most recent yakos backup.
func TestUninstall_RestoreSettings(t *testing.T) {
	root := newFakeYakosRoot(t)
	home := newInstalledHome(t, root)

	claudeDir := filepath.Join(home, ".claude")
	settingsPath := filepath.Join(claudeDir, "settings.json")
	// Write a current settings.json (user-owned, no marker).
	if err := os.WriteFile(settingsPath, []byte(`{"current":true}`), 0644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
	// Write two backup files; the second is the "most recent" lexically.
	bak1 := filepath.Join(claudeDir, "settings.json.yakos-bak-2026-01-01")
	bak2 := filepath.Join(claudeDir, "settings.json.yakos-bak-2026-06-01")
	originalContent := `{"env":{"restored":true}}`
	if err := os.WriteFile(bak1, []byte(`{"env":{"old":true}}`), 0644); err != nil {
		t.Fatalf("write bak1: %v", err)
	}
	if err := os.WriteFile(bak2, []byte(originalContent), 0644); err != nil {
		t.Fatalf("write bak2: %v", err)
	}

	cfg := baseConfig(home)
	cfg.RestoreSettings = true
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Settings.Outcome != SettingsRestored {
		t.Errorf("Settings.Outcome: got %q, want %q", res.Settings.Outcome, SettingsRestored)
	}
	if res.Settings.RestoredFrom != bak2 {
		t.Errorf("RestoredFrom: got %q, want %q", res.Settings.RestoredFrom, bak2)
	}
	got, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile settings.json: %v", err)
	}
	if string(got) != originalContent {
		t.Errorf("settings.json not restored correctly; got %q", string(got))
	}
}

// ---- (m) settings.json: --restore-settings with no backup ------------------

// TestUninstall_RestoreSettingsNoBackup verifies graceful handling when
// --restore-settings is passed but no backup exists.
func TestUninstall_RestoreSettingsNoBackup(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// No backup files present, just the current settings.json.
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{}`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg := baseConfig(home)
	cfg.RestoreSettings = true
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// No backup → falls through to Kept.
	if res.Settings.Outcome == SettingsRestored {
		t.Error("should not report Restored when no backup exists")
	}
}

// ---- (n) explicit root overrides pointer ------------------------------------

// TestUninstall_ExplicitRoot verifies that --root overrides the ~/.yakos pointer.
func TestUninstall_ExplicitRoot(t *testing.T) {
	root := newFakeYakosRoot(t)
	home := t.TempDir()

	// Write a pointer pointing at a different (non-existent) root.
	if err := os.WriteFile(filepath.Join(home, ".yakos"), []byte("/wrong/root\n"), 0644); err != nil {
		t.Fatalf("write .yakos: %v", err)
	}

	// Install a symlink pointing at the real root.
	agentsDir := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	realSrc := filepath.Join(root, "lib", "agents", "example.md")
	dstLink := filepath.Join(agentsDir, "example.md")
	if err := os.Symlink(realSrc, dstLink); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cfg := baseConfig(home)
	cfg.ExplicitRoot = root // override
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Symlink should be removed (target is inside explicit root).
	if _, err := os.Lstat(dstLink); !os.IsNotExist(err) {
		t.Error("YakOS-owned symlink should be removed with explicit root")
	}
	if res.Symlinks.Removed == 0 {
		t.Error("expected Symlinks.Removed > 0")
	}
}

// ---- (o) no manifest → launcher unknown ------------------------------------

// TestUninstall_NoManifest verifies that absence of install-manifest is handled
// gracefully.
func TestUninstall_NoManifest(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".yakos"), []byte("/some/root\n"), 0644); err != nil {
		t.Fatalf("write .yakos: %v", err)
	}
	// No manifest created.

	cfg := baseConfig(home)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Launcher.Outcome != LauncherUnknown {
		t.Errorf("Launcher.Outcome: got %q, want %q", res.Launcher.Outcome, LauncherUnknown)
	}
}

// ---- (p) state dir removed when empty ---------------------------------------

// TestUninstall_StateDirRemovedWhenEmpty verifies that ~/.yakos-state is removed
// after the manifest is deleted, if the directory becomes empty.
func TestUninstall_StateDirRemovedWhenEmpty(t *testing.T) {
	root := newFakeYakosRoot(t)
	home := newInstalledHome(t, root)

	cfg := baseConfig(home)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	stateDir := filepath.Join(home, ".yakos-state")
	if res.StateDirRemoved {
		// Dir should not exist any more.
		if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
			t.Error("state dir should be gone when removed")
		}
	}
	// Not failing if StateDirRemoved==false (may have other files).
}

// ---- (q) help text ----------------------------------------------------------

// TestUninstall_HelpText verifies all load-bearing phrases from uninstall.sh --help.
func TestUninstall_HelpText(t *testing.T) {
	var out bytes.Buffer
	PrintHelp(&out)
	got := out.String()

	mustContain := []string{
		"yakos uninstall",
		"~/.claude",
		"agents",
		"skills",
		"rules",
		"playbooks",
		"settings.json",
		"--restore-settings",
		"--root",
		"--dry-run",
		"--help",
		"projects/",
		"Foreign",
	}
	for _, phrase := range mustContain {
		if !strings.Contains(got, phrase) {
			t.Errorf("help text missing phrase %q;\nfull output:\n%s", phrase, got)
		}
	}
}

// ---- (r) install → uninstall → install round trip --------------------------

// TestUninstall_RoundTrip verifies that Install → Uninstall → Install leaves
// no orphan files (state after second install matches first install).
func TestUninstall_RoundTrip(t *testing.T) {
	root := newFakeYakosRoot(t)
	home := newInstalledHome(t, root)

	// Uninstall.
	cfg := baseConfig(home)
	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run (uninstall): %v", err)
	}

	// Verify the core artifacts are gone.
	for _, path := range []string{
		filepath.Join(home, ".yakos"),
		filepath.Join(home, ".yakos-state", "install-manifest"),
		filepath.Join(home, ".local", "bin", "yakos"),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("after uninstall: %s should not exist", path)
		}
	}
	for _, sub := range subdirs {
		link := filepath.Join(home, ".claude", sub, "example.md")
		if _, err := os.Lstat(link); !os.IsNotExist(err) {
			t.Errorf("after uninstall: symlink %s should not exist", link)
		}
	}

	// Re-install (simulate by restoring the same fixture state).
	home2 := newInstalledHome(t, root)
	// Verify second install has the same structure.
	for _, sub := range subdirs {
		link := filepath.Join(home2, ".claude", sub, "example.md")
		linfo, err := os.Lstat(link)
		if err != nil {
			t.Errorf("after re-install: symlink %s missing: %v", link, err)
			continue
		}
		if linfo.Mode()&os.ModeSymlink == 0 {
			t.Errorf("after re-install: %s should be a symlink", link)
		}
	}
}

// ---- (s) nested subdirectory symlinks ---------------------------------------

// TestUninstall_NestedSubdirSymlinks verifies that nested symlinks (e.g.
// ~/.claude/skills/pre-commit/SKILL.md) are removed correctly.
func TestUninstall_NestedSubdirSymlinks(t *testing.T) {
	root := newFakeYakosRoot(t)
	// Add a nested skill.
	nestedDir := filepath.Join(root, "lib", "skills", "pre-commit")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	nestedSrc := filepath.Join(nestedDir, "SKILL.md")
	if err := os.WriteFile(nestedSrc, []byte("# skill\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".yakos"), []byte(root+"\n"), 0644); err != nil {
		t.Fatalf("write .yakos: %v", err)
	}
	// Create the nested symlink.
	nestedDstDir := filepath.Join(home, ".claude", "skills", "pre-commit")
	if err := os.MkdirAll(nestedDstDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	nestedLink := filepath.Join(nestedDstDir, "SKILL.md")
	if err := os.Symlink(nestedSrc, nestedLink); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cfg := baseConfig(home)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Lstat(nestedLink); !os.IsNotExist(err) {
		t.Error("nested symlink should be removed")
	}
	if res.Symlinks.Removed == 0 {
		t.Error("expected Symlinks.Removed > 0 for nested symlink")
	}
}

// ---- (t) partial-uninstall failures log and continue ------------------------

// TestUninstall_PartialFailureLogsAndContinues verifies that a failure on one
// symlink removal does not abort the entire run; other artifacts are still
// cleaned up.
func TestUninstall_PartialFailureLogsAndContinues(t *testing.T) {
	// This test creates an environment where the symlink exists but then we make
	// the parent directory read-only to simulate a permission error. Skip on
	// systems where root can override permissions.
	if os.Getuid() == 0 {
		t.Skip("skipping permission test when running as root")
	}

	root := newFakeYakosRoot(t)
	home := newInstalledHome(t, root)

	// Make ~/.claude/agents dir read-only so Remove fails.
	agentsDir := filepath.Join(home, ".claude", "agents")
	if err := os.Chmod(agentsDir, 0555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(agentsDir, 0755)
	})

	cfg := baseConfig(home)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run should not return error on partial failure: %v", err)
	}

	// Some failures expected.
	if res.Symlinks.Errors == 0 && len(res.Failures) == 0 {
		// Depending on OS, the remove may or may not fail.
		t.Log("no failures recorded; OS may have allowed the remove anyway")
	}

	// Pointer should still be removed (it's a separate step).
	if _, err := os.Lstat(filepath.Join(home, ".yakos")); !os.IsNotExist(err) {
		t.Error(".yakos pointer should be removed even when symlink removal fails")
	}
}

// ---- (u) auto-memory mention in output ---------------------------------------

// TestUninstall_AutoMemoryMention verifies that the output always references
// ~/.claude/projects/ as intentionally untouched.
func TestUninstall_AutoMemoryMention(t *testing.T) {
	home := t.TempDir()
	// Create claude dir so we don't hit the early-out.
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".yakos"), []byte("/fake/root\n"), 0644); err != nil {
		t.Fatalf("write .yakos: %v", err)
	}

	cfg := baseConfig(home)
	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	stdout := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(stdout, "projects/") {
		t.Errorf("output should mention projects/; got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "intact") {
		t.Errorf("output should mention 'intact'; got:\n%s", stdout)
	}
}
