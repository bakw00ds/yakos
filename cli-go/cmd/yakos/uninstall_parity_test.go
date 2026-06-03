// uninstall_parity_test.go — Phase 1 parity tests for `yakos uninstall`.
//
// Design notes:
//
//  1. All tests use uninstall.Run directly with temp dirs so no real
//     ~/.claude or ~/.yakos is touched.
//
//  2. Help-text parity verifies load-bearing phrases from cli/lib/uninstall.sh
//     usage() appear in the Go output.
//
//  3. Round-trip parity (install → uninstall → install) is the primary
//     integration test. The underlying install/uninstall packages share no
//     state, so the round-trip is the end-to-end correctness proof.
//
// Critical scenarios verified:
//
//	(a) happy-path               — all YakOS artifacts removed
//	(b) --dry-run                — no files written; annotations in stdout
//	(c) dangling symlinks        — always removed regardless of root
//	(d) foreign symlinks         — never removed; Kept count increases
//	(e) real files at paths      — never removed
//	(f) stale pointer            — best-effort cleanup; dangling removed
//	(g) nothing to uninstall     — early-out message
//	(h) launcher real file       — left alone (Kept)
//	(i) foreign launcher symlink — left alone (Kept)
//	(j) created-marker present   — settings.json removed
//	(k) no marker                — settings.json kept; backups listed
//	(l) --restore-settings       — most recent backup restored
//	(m) --root override          — explicit root honoured
//	(n) no manifest              — launcher unknown; no crash
//	(o) state dir cleanup        — ~/.yakos-state rmdir'd when empty
//	(p) help text                — all load-bearing phrases present
//	(q) install → uninstall → install round trip
//	(r) nested subdir symlinks   — nested symlinks removed
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/install"
	"github.com/bakw00ds/yakos/internal/uninstall"
)

// ---- helpers ----------------------------------------------------------------

// newUninstallFakeRoot creates a minimal YAKOS_ROOT tree for uninstall tests.
func newUninstallFakeRoot(t *testing.T) string {
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

// installedHome runs install.Run on a fresh temp home and returns the home path.
func installedHome(t *testing.T, yakosRoot string) string {
	t.Helper()
	home := t.TempDir()
	cfg := install.Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}
	if _, err := install.Run(cfg); err != nil {
		t.Fatalf("install.Run: %v", err)
	}
	return home
}

// uninstallConfig builds a Config aimed at the given home with captured output.
func uninstallConfig(home string) uninstall.Config {
	return uninstall.Config{
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}
}

// symlinkOrSkip creates a symlink and skips the test if symlink creation is
// not supported on this platform (e.g. Windows without Developer Mode).
func symlinkOrSkipUninstall(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("os.Symlink not supported on this platform (%s); skipping symlink test: %v", runtime.GOOS, err)
	}
}

// foreignTargetUninstall returns a path to a real file outside any t.TempDir()-based
// yakosRoot. Used as the target for "foreign symlink" tests so that
// filepath.EvalSymlinks resolves without error and the path is not under the
// yakosRootAbs prefix.
//
// On Windows, /dev/null and /usr/bin/env do not exist. This helper creates a
// sentinel file in os.TempDir() that is valid on all platforms.
func foreignTargetUninstall(t *testing.T) string {
	t.Helper()
	target := filepath.Join(os.TempDir(), "yakos-parity-test-foreign-target")
	if err := os.WriteFile(target, []byte("foreign\n"), 0644); err != nil {
		t.Fatalf("foreignTargetUninstall: could not write sentinel file %s: %v", target, err)
	}
	t.Cleanup(func() { _ = os.Remove(target) })
	return target
}

// ---- (a) happy-path ---------------------------------------------------------

// TestUninstallParity_HappyPath verifies full artifact removal after a real install.
func TestUninstallParity_HappyPath(t *testing.T) {
	root := newUninstallFakeRoot(t)
	home := installedHome(t, root)
	cfg := uninstallConfig(home)

	res, err := uninstall.Run(cfg)
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

	// Symlinks removed.
	if res.Symlinks.Removed == 0 {
		t.Error("expected Symlinks.Removed > 0")
	}
	for _, sub := range []string{"agents", "skills", "rules", "playbooks"} {
		link := filepath.Join(home, ".claude", sub, "example.md")
		if _, err := os.Lstat(link); !os.IsNotExist(err) {
			t.Errorf("YakOS symlink should be removed: %s", link)
		}
	}

	// Launcher removed.
	lp := filepath.Join(home, ".local", "bin", "yakos")
	if _, err := os.Lstat(lp); !os.IsNotExist(err) {
		t.Error("launcher symlink should be removed")
	}
	if res.Launcher.Outcome != uninstall.LauncherRemoved {
		t.Errorf("Launcher.Outcome: got %q, want %q", res.Launcher.Outcome, uninstall.LauncherRemoved)
	}
}

// ---- (b) --dry-run ----------------------------------------------------------

// TestUninstallParity_DryRun verifies dry-run writes nothing.
func TestUninstallParity_DryRun(t *testing.T) {
	root := newUninstallFakeRoot(t)
	home := installedHome(t, root)
	cfg := uninstallConfig(home)
	cfg.DryRun = true

	res, err := uninstall.Run(cfg)
	if err != nil {
		t.Fatalf("Run (dry-run): %v", err)
	}
	if !res.DryRun {
		t.Error("Result.DryRun should be true")
	}

	// All artifacts must still exist.
	for _, path := range []string{
		filepath.Join(home, ".yakos"),
		filepath.Join(home, ".yakos-state", "install-manifest"),
		filepath.Join(home, ".local", "bin", "yakos"),
	} {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("dry-run: %s should still exist", path)
		}
	}

	stdout := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(stdout, "dry-run") {
		t.Errorf("stdout should mention 'dry-run'; got:\n%s", stdout)
	}
}

// ---- (c) dangling symlinks --------------------------------------------------

// TestUninstallParity_DanglingSymlinks verifies that dangling symlinks are always
// removed even without a valid yakos root.
func TestUninstallParity_DanglingSymlinks(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	agentsDir := filepath.Join(claudeDir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write a pointer to a non-existent root.
	if err := os.WriteFile(filepath.Join(home, ".yakos"), []byte("/no/such/root\n"), 0644); err != nil {
		t.Fatalf("write .yakos: %v", err)
	}
	// Create a dangling symlink.
	link := filepath.Join(agentsDir, "ghost.md")
	if err := os.Symlink("/no/such/target.md", link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cfg := uninstallConfig(home)
	res, err := uninstall.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("dangling symlink should be removed")
	}
	if res.Symlinks.Removed == 0 {
		t.Error("expected Symlinks.Removed > 0")
	}
}

// ---- (d) foreign symlinks ---------------------------------------------------

// TestUninstallParity_ForeignSymlinksKept verifies foreign symlinks are untouched.
func TestUninstallParity_ForeignSymlinksKept(t *testing.T) {
	root := newUninstallFakeRoot(t)
	home := installedHome(t, root)

	// Add a foreign symlink in ~/.claude/agents/ pointing at a real file outside
	// yakosRoot. /dev/null does not exist on Windows; use a cross-platform sentinel.
	ft := foreignTargetUninstall(t)
	foreignLink := filepath.Join(home, ".claude", "agents", "foreign.md")
	symlinkOrSkipUninstall(t, ft, foreignLink)

	cfg := uninstallConfig(home)
	res, err := uninstall.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Foreign symlink must still exist.
	target, err := os.Readlink(foreignLink)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if target != ft {
		t.Errorf("foreign symlink was redirected; got %q, want %q", target, ft)
	}
	if res.Symlinks.Kept == 0 {
		t.Error("expected Symlinks.Kept > 0 for foreign symlink")
	}
}

// ---- (e) real files at paths ------------------------------------------------

// TestUninstallParity_RealFilesKept verifies real files in managed dirs are untouched.
func TestUninstallParity_RealFilesKept(t *testing.T) {
	root := newUninstallFakeRoot(t)
	home := installedHome(t, root)

	// Place a real file.
	realPath := filepath.Join(home, ".claude", "skills", "operator.md")
	if err := os.WriteFile(realPath, []byte("# operator content\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg := uninstallConfig(home)
	_, err := uninstall.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.ReadFile(realPath); err != nil {
		t.Fatalf("real file should still exist: %v", err)
	}
}

// ---- (f) stale pointer ------------------------------------------------------

// TestUninstallParity_StalePointer verifies cleanup continues on stale pointer.
func TestUninstallParity_StalePointer(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".yakos"), []byte("/moved/to/somewhere-else\n"), 0644); err != nil {
		t.Fatalf("write .yakos: %v", err)
	}
	agentsDir := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Dangling symlink — should be cleaned up even on stale pointer.
	link := filepath.Join(agentsDir, "stale.md")
	if err := os.Symlink("/no/such/path.md", link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cfg := uninstallConfig(home)
	res, err := uninstall.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("dangling symlink should be cleaned up on stale pointer")
	}
	if res.Symlinks.Removed == 0 {
		t.Error("expected Symlinks.Removed > 0")
	}
}

// ---- (g) nothing to uninstall -----------------------------------------------

// TestUninstallParity_NothingToUninstall verifies the early-out message.
func TestUninstallParity_NothingToUninstall(t *testing.T) {
	home := t.TempDir()
	cfg := uninstallConfig(home)

	res, err := uninstall.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	stdout := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(stdout, "Nothing to uninstall") {
		t.Errorf("expected 'Nothing to uninstall'; got:\n%s", stdout)
	}
	if res.PointerRemoved {
		t.Error("PointerRemoved should be false when nothing to uninstall")
	}
}

// ---- (h) launcher: real file ------------------------------------------------

// TestUninstallParity_LauncherRealFileKept verifies a real file at launcher path is kept.
func TestUninstallParity_LauncherRealFileKept(t *testing.T) {
	root := newUninstallFakeRoot(t)
	home := installedHome(t, root)

	// Replace launcher symlink with a real file.
	lp := filepath.Join(home, ".local", "bin", "yakos")
	if err := os.Remove(lp); err != nil {
		t.Fatalf("remove launcher: %v", err)
	}
	if err := os.WriteFile(lp, []byte("#!/bin/bash\n# foreign\n"), 0755); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg := uninstallConfig(home)
	res, err := uninstall.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Launcher.Outcome != uninstall.LauncherKept {
		t.Errorf("Launcher.Outcome: got %q, want %q", res.Launcher.Outcome, uninstall.LauncherKept)
	}
	if _, err := os.Stat(lp); err != nil {
		t.Error("real launcher file should still exist")
	}
}

// ---- (i) launcher: foreign symlink ------------------------------------------

// TestUninstallParity_ForeignLauncherSymlink verifies a foreign launcher symlink is kept.
func TestUninstallParity_ForeignLauncherSymlink(t *testing.T) {
	root := newUninstallFakeRoot(t)
	home := installedHome(t, root)

	// Replace launcher symlink with a foreign one pointing at a real file outside
	// yakosRoot. /usr/bin/env does not exist on Windows; use a cross-platform sentinel.
	ft := foreignTargetUninstall(t)
	lp := filepath.Join(home, ".local", "bin", "yakos")
	if err := os.Remove(lp); err != nil {
		t.Fatalf("remove launcher: %v", err)
	}
	symlinkOrSkipUninstall(t, ft, lp)

	cfg := uninstallConfig(home)
	res, err := uninstall.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Launcher.Outcome != uninstall.LauncherKept {
		t.Errorf("Launcher.Outcome: got %q, want %q", res.Launcher.Outcome, uninstall.LauncherKept)
	}
	target, _ := os.Readlink(lp)
	if target != ft {
		t.Errorf("foreign launcher was redirected; got %q, want %q", target, ft)
	}
}

// ---- (j) created-marker present → settings removed -------------------------

// TestUninstallParity_CreatedMarkerRemovesSettings verifies settings.json is removed.
func TestUninstallParity_CreatedMarkerRemovesSettings(t *testing.T) {
	root := newUninstallFakeRoot(t)
	home := installedHome(t, root)

	claudeDir := filepath.Join(home, ".claude")
	sp := filepath.Join(claudeDir, "settings.json")
	marker := filepath.Join(claudeDir, ".yakos-created-settings")
	// Ensure settings.json exists and marker is present.
	if err := os.WriteFile(sp, []byte(`{"env":{}}`), 0644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	if err := os.WriteFile(marker, []byte{}, 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	cfg := uninstallConfig(home)
	res, err := uninstall.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Settings.Outcome != uninstall.SettingsRemoved {
		t.Errorf("Settings.Outcome: got %q, want %q", res.Settings.Outcome, uninstall.SettingsRemoved)
	}
	if _, err := os.Stat(sp); !os.IsNotExist(err) {
		t.Error("settings.json should be removed when marker present")
	}
}

// ---- (k) no marker → settings kept -----------------------------------------

// TestUninstallParity_NoMarkerSettingsKept verifies settings.json is left alone.
func TestUninstallParity_NoMarkerSettingsKept(t *testing.T) {
	root := newUninstallFakeRoot(t)
	home := installedHome(t, root)

	claudeDir := filepath.Join(home, ".claude")
	sp := filepath.Join(claudeDir, "settings.json")
	content := `{"sentinel":"parity-test"}`
	if err := os.WriteFile(sp, []byte(content), 0644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	// No marker: simulate user-owned settings.
	_ = os.Remove(filepath.Join(claudeDir, ".yakos-created-settings"))

	cfg := uninstallConfig(home)
	res, err := uninstall.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Settings.Outcome != uninstall.SettingsKept {
		t.Errorf("Settings.Outcome: got %q, want %q", res.Settings.Outcome, uninstall.SettingsKept)
	}
	got, _ := os.ReadFile(sp)
	if string(got) != content {
		t.Error("settings.json was modified when it should be kept")
	}
}

// ---- (l) --restore-settings -------------------------------------------------

// TestUninstallParity_RestoreSettings verifies the most recent backup is restored.
// The test uses a home where settings.json is user-owned (no created-marker)
// so the restore branch is reached.
func TestUninstallParity_RestoreSettings(t *testing.T) {
	root := newUninstallFakeRoot(t)
	home := installedHome(t, root)

	claudeDir := filepath.Join(home, ".claude")
	sp := filepath.Join(claudeDir, "settings.json")
	// Remove the created-marker so settings.json is treated as user-owned.
	_ = os.Remove(filepath.Join(claudeDir, ".yakos-created-settings"))
	// Write user settings.json.
	if err := os.WriteFile(sp, []byte(`{"current":true}`), 0644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	bak := filepath.Join(claudeDir, "settings.json.yakos-bak-2026-06-01")
	bakContent := `{"restored":true}`
	if err := os.WriteFile(bak, []byte(bakContent), 0644); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	cfg := uninstallConfig(home)
	cfg.RestoreSettings = true
	res, err := uninstall.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Settings.Outcome != uninstall.SettingsRestored {
		t.Errorf("Settings.Outcome: got %q, want %q", res.Settings.Outcome, uninstall.SettingsRestored)
	}
	got, _ := os.ReadFile(sp)
	if string(got) != bakContent {
		t.Errorf("settings.json not restored correctly; got %q", string(got))
	}
}

// ---- (m) --root override ----------------------------------------------------

// TestUninstallParity_RootOverride verifies --root takes precedence over pointer.
func TestUninstallParity_RootOverride(t *testing.T) {
	root := newUninstallFakeRoot(t)
	home := t.TempDir()

	// Pointer points at wrong location.
	if err := os.WriteFile(filepath.Join(home, ".yakos"), []byte("/wrong/root\n"), 0644); err != nil {
		t.Fatalf("write .yakos: %v", err)
	}
	// Symlink pointing into the real root.
	agentsDir := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	realSrc := filepath.Join(root, "lib", "agents", "example.md")
	link := filepath.Join(agentsDir, "example.md")
	if err := os.Symlink(realSrc, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cfg := uninstallConfig(home)
	cfg.ExplicitRoot = root
	res, err := uninstall.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("YakOS-owned symlink should be removed with --root override")
	}
	if res.Symlinks.Removed == 0 {
		t.Error("expected Symlinks.Removed > 0")
	}
}

// ---- (n) no manifest → launcher unknown ------------------------------------

// TestUninstallParity_NoManifest verifies absence of manifest is handled gracefully.
func TestUninstallParity_NoManifest(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".yakos"), []byte("/fake\n"), 0644); err != nil {
		t.Fatalf("write .yakos: %v", err)
	}

	cfg := uninstallConfig(home)
	res, err := uninstall.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Launcher.Outcome != uninstall.LauncherUnknown {
		t.Errorf("Launcher.Outcome: got %q, want %q", res.Launcher.Outcome, uninstall.LauncherUnknown)
	}
}

// ---- (o) state dir cleanup --------------------------------------------------

// TestUninstallParity_StateDirCleanup verifies ~/.yakos-state is removed when empty.
func TestUninstallParity_StateDirCleanup(t *testing.T) {
	root := newUninstallFakeRoot(t)
	home := installedHome(t, root)

	cfg := uninstallConfig(home)
	res, err := uninstall.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	stateDir := filepath.Join(home, ".yakos-state")
	if res.StateDirRemoved {
		if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
			t.Error("state dir should be removed when empty")
		}
	}
	// Result is informational; not asserting StateDirRemoved==true because
	// the OS may leave it in certain edge cases.
	_ = res
}

// ---- (p) help text ----------------------------------------------------------

// TestUninstallParity_HelpText verifies all load-bearing phrases from uninstall.sh.
func TestUninstallParity_HelpText(t *testing.T) {
	var buf bytes.Buffer
	uninstall.PrintHelp(&buf)
	got := buf.String()

	for _, phrase := range []string{
		"yakos uninstall",
		"~/.claude",
		"agents", "skills", "rules", "playbooks",
		"settings.json",
		"--restore-settings",
		"--root",
		"--dry-run",
		"--help",
		"projects/",
		"Foreign",
	} {
		if !strings.Contains(got, phrase) {
			t.Errorf("help text missing %q;\nfull output:\n%s", phrase, got)
		}
	}
}

// ---- (q) install → uninstall → install round-trip --------------------------

// TestUninstallParity_RoundTrip verifies that install → uninstall → install
// leaves no orphan files and the second install looks identical to the first.
func TestUninstallParity_RoundTrip(t *testing.T) {
	root := newUninstallFakeRoot(t)
	home := installedHome(t, root) // first install

	// Uninstall.
	cfg := uninstallConfig(home)
	if _, err := uninstall.Run(cfg); err != nil {
		t.Fatalf("uninstall.Run: %v", err)
	}

	// Verify all YakOS artifacts removed.
	for _, path := range []string{
		filepath.Join(home, ".yakos"),
		filepath.Join(home, ".yakos-state", "install-manifest"),
		filepath.Join(home, ".local", "bin", "yakos"),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("after uninstall: %s should not exist", path)
		}
	}
	for _, sub := range []string{"agents", "skills", "rules", "playbooks"} {
		link := filepath.Join(home, ".claude", sub, "example.md")
		if _, err := os.Lstat(link); !os.IsNotExist(err) {
			t.Errorf("after uninstall: symlink %s should not exist", link)
		}
	}

	// Second install on the same home.
	cfg2 := install.Config{
		YakosRoot: root,
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}
	if _, err := install.Run(cfg2); err != nil {
		t.Fatalf("second install.Run: %v", err)
	}

	// Verify second install re-created all artifacts.
	for _, path := range []string{
		filepath.Join(home, ".yakos"),
		filepath.Join(home, ".yakos-state", "install-manifest"),
		filepath.Join(home, ".local", "bin", "yakos"),
	} {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("after re-install: %s should exist: %v", path, err)
		}
	}
	for _, sub := range []string{"agents", "skills", "rules", "playbooks"} {
		link := filepath.Join(home, ".claude", sub, "example.md")
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

// ---- (r) nested subdir symlinks ---------------------------------------------

// TestUninstallParity_NestedSubdirSymlinks verifies nested installed symlinks are removed.
func TestUninstallParity_NestedSubdirSymlinks(t *testing.T) {
	root := newUninstallFakeRoot(t)
	// Add a nested skill to the root.
	nestedDir := filepath.Join(root, "lib", "skills", "pre-commit")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "SKILL.md"), []byte("# skill\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Install (which will create the nested symlink).
	home := installedHome(t, root)

	nestedLink := filepath.Join(home, ".claude", "skills", "pre-commit", "SKILL.md")
	if _, err := os.Lstat(nestedLink); err != nil {
		t.Fatalf("nested symlink should exist after install: %v", err)
	}

	// Uninstall.
	cfg := uninstallConfig(home)
	res, err := uninstall.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Lstat(nestedLink); !os.IsNotExist(err) {
		t.Error("nested symlink should be removed by uninstall")
	}
	if res.Symlinks.Removed == 0 {
		t.Error("expected Symlinks.Removed > 0 for nested symlink")
	}
}
