// githooks_parity_test.go — Phase 1 parity tests for `yakos git-hooks`.
//
// Design notes:
//
//  1. All tests call githooks.Run directly with temp-dir paths; no real git
//     binary required — GitRepoRoot is always injected.
//
//  2. SHA256File is injected so tests don't rely on specific file content.
//
//  3. Parity is verified behaviourally: output shape, error conditions,
//     filesystem effects.
//
// Critical scenarios:
//
//	(a) git-hooks entry in portedCommands
//	(b) install: hook file created, hash sibling written
//	(c) install: idempotent (same hash)
//	(d) install: --force overwrites stale hook
//	(e) install: error when not owned and no --force
//	(f) install: --promotion-gate creates composed script
//	(g) uninstall: removes hook and sibling
//	(h) uninstall: no-op when hook absent
//	(i) uninstall: error when not owned
//	(j) status: NOT INSTALLED
//	(k) status: PRESENT not owned
//	(l) status: INSTALLED no drift
//	(m) status: INSTALLED DRIFTED
//	(n) unknown subcommand → error
//	(o) help output contains key phrases
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/githooks"
)

// ---- scenario (a): portedCommands entry -------------------------------------

func TestGitHooksParity_PortedCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "git-hooks" {
			return
		}
	}
	t.Error("expected 'git-hooks' in portedCommands; not found")
}

// ---- helpers ----------------------------------------------------------------

const ghFakeGateContent = "#!/usr/bin/env bash\n# fake version gate\n"

func ghBuildYakosRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gateDir := filepath.Join(root, "lib", "hooks", "git")
	if err := os.MkdirAll(gateDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gateDir, "pre-push-version-gate.sh"),
		[]byte(ghFakeGateContent), 0755); err != nil {
		t.Fatalf("write gate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gateDir, "pre-push-promotion-gate.sh"),
		[]byte("#!/usr/bin/env bash\n# fake promotion\n"), 0755); err != nil {
		t.Fatalf("write promotion: %v", err)
	}
	return root
}

func newGHCfg(t *testing.T, sub string) githooks.Config {
	t.Helper()
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git", "hooks"), 0755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	return githooks.Config{
		Subcommand: sub,
		YakosRoot:  ghBuildYakosRoot(t),
		RepoRoot:   repoRoot,
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
		SHA256File: func(path string) (string, error) {
			data, err := os.ReadFile(path) //nolint:gosec
			if err != nil {
				return "", err
			}
			h := sha256.Sum256(data)
			return fmt.Sprintf("%x", h), nil
		},
	}
}

func ghOut(cfg githooks.Config) string { return cfg.Writer.(*bytes.Buffer).String() }

func ghHookPath(cfg githooks.Config) string {
	return filepath.Join(cfg.RepoRoot, ".git", "hooks", "pre-push")
}
func ghHashPath(cfg githooks.Config) string { return ghHookPath(cfg) + ".framework-hash" }

// ---- scenario (b): install creates hook and sibling ------------------------

func TestGitHooksParity_Install_CreatesFiles(t *testing.T) {
	cfg := newGHCfg(t, "install")
	res, err := githooks.Run(cfg)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !res.Installed {
		t.Error("expected Installed=true")
	}
	if _, err := os.Stat(ghHookPath(cfg)); err != nil {
		t.Errorf("hook missing: %v", err)
	}
	if _, err := os.Stat(ghHashPath(cfg)); err != nil {
		t.Errorf("hash sibling missing: %v", err)
	}
}

// ---- scenario (c): install idempotent -------------------------------------

func TestGitHooksParity_Install_Idempotent(t *testing.T) {
	cfg := newGHCfg(t, "install")
	if _, err := githooks.Run(cfg); err != nil {
		t.Fatalf("first install: %v", err)
	}

	cfg2 := newGHCfg(t, "install")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.RepoRoot = cfg.RepoRoot

	res, err := githooks.Run(cfg2)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if !res.Installed {
		t.Error("expected Installed=true (idempotent)")
	}
	out := ghOut(cfg2)
	if !strings.Contains(out, "nothing to do") {
		t.Errorf("expected 'nothing to do'; got %q", out)
	}
}

// ---- scenario (d): --force overwrites stale hook --------------------------

func TestGitHooksParity_Install_Force_OverwritesStale(t *testing.T) {
	cfg := newGHCfg(t, "install")
	if _, err := githooks.Run(cfg); err != nil {
		t.Fatalf("first install: %v", err)
	}
	// Make hash stale.
	if err := os.WriteFile(ghHashPath(cfg), []byte("oldhash\n"), 0644); err != nil {
		t.Fatalf("overwrite hash: %v", err)
	}

	cfg2 := newGHCfg(t, "install")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.RepoRoot = cfg.RepoRoot
	cfg2.Force = true

	res, err := githooks.Run(cfg2)
	if err != nil {
		t.Fatalf("install --force: %v", err)
	}
	if !res.Installed {
		t.Error("expected Installed=true after --force")
	}
}

// ---- scenario (e): error when not owned and no --force --------------------

func TestGitHooksParity_Install_NotOwned_Error(t *testing.T) {
	cfg := newGHCfg(t, "install")
	if err := os.WriteFile(ghHookPath(cfg), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	_, err := githooks.Run(cfg)
	if err == nil {
		t.Fatal("expected error for non-owned hook")
	}
	if !strings.Contains(err.Error(), "YakOS-owned") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (f): --promotion-gate creates composed script ----------------

func TestGitHooksParity_Install_PromotionGate(t *testing.T) {
	cfg := newGHCfg(t, "install")
	cfg.WithPromotionGate = true

	res, err := githooks.Run(cfg)
	if err != nil {
		t.Fatalf("install promotion: %v", err)
	}
	if !res.WithPromotion {
		t.Error("expected WithPromotion=true")
	}

	data, _ := os.ReadFile(ghHookPath(cfg)) //nolint:gosec
	if !strings.Contains(string(data), "composed pre-push") {
		t.Errorf("expected composed script content; got: %q", string(data)[:100])
	}
}

// ---- scenario (g): uninstall removes files --------------------------------

func TestGitHooksParity_Uninstall_RemovesFiles(t *testing.T) {
	cfg := newGHCfg(t, "install")
	if _, err := githooks.Run(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}

	cfg2 := newGHCfg(t, "uninstall")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.RepoRoot = cfg.RepoRoot

	res, err := githooks.Run(cfg2)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if res.Installed {
		t.Error("expected Installed=false")
	}
	if _, err := os.Stat(ghHookPath(cfg)); err == nil {
		t.Error("hook still present")
	}
}

// ---- scenario (h): uninstall no-op when absent ----------------------------

func TestGitHooksParity_Uninstall_NoHook_Noop(t *testing.T) {
	cfg := newGHCfg(t, "uninstall")
	res, err := githooks.Run(cfg)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if res.Installed {
		t.Error("expected Installed=false")
	}
	out := ghOut(cfg)
	if !strings.Contains(out, "nothing to remove") {
		t.Errorf("expected 'nothing to remove'; got %q", out)
	}
}

// ---- scenario (i): uninstall error when not owned -------------------------

func TestGitHooksParity_Uninstall_NotOwned_Error(t *testing.T) {
	cfg := newGHCfg(t, "uninstall")
	if err := os.WriteFile(ghHookPath(cfg), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	_, err := githooks.Run(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "YakOS-owned") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (j): status NOT INSTALLED -----------------------------------

func TestGitHooksParity_Status_NotInstalled(t *testing.T) {
	cfg := newGHCfg(t, "status")
	res, err := githooks.Run(cfg)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if res.Installed {
		t.Error("expected Installed=false")
	}
	out := ghOut(cfg)
	if !strings.Contains(out, "NOT INSTALLED") {
		t.Errorf("expected NOT INSTALLED; got %q", out)
	}
}

// ---- scenario (k): status PRESENT not owned -------------------------------

func TestGitHooksParity_Status_NotOwned(t *testing.T) {
	cfg := newGHCfg(t, "status")
	if err := os.WriteFile(ghHookPath(cfg), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	res, err := githooks.Run(cfg)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !res.Installed {
		t.Error("expected Installed=true")
	}
	if res.Owned {
		t.Error("expected Owned=false")
	}
	out := ghOut(cfg)
	if !strings.Contains(out, "not YakOS-owned") {
		t.Errorf("expected 'not YakOS-owned'; got %q", out)
	}
}

// ---- scenario (l): status INSTALLED no drift ------------------------------

func TestGitHooksParity_Status_NoDrift(t *testing.T) {
	cfg := newGHCfg(t, "install")
	if _, err := githooks.Run(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}

	cfg2 := newGHCfg(t, "status")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.RepoRoot = cfg.RepoRoot

	res, err := githooks.Run(cfg2)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if res.Drifted {
		t.Error("expected Drifted=false")
	}
	out := ghOut(cfg2)
	if !strings.Contains(out, "no drift") {
		t.Errorf("expected 'no drift'; got %q", out)
	}
}

// ---- scenario (m): status INSTALLED DRIFTED --------------------------------

func TestGitHooksParity_Status_Drifted(t *testing.T) {
	cfg := newGHCfg(t, "install")
	if _, err := githooks.Run(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := os.WriteFile(ghHashPath(cfg), []byte("wronghash\n"), 0644); err != nil {
		t.Fatalf("overwrite hash: %v", err)
	}

	cfg2 := newGHCfg(t, "status")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.RepoRoot = cfg.RepoRoot

	res, err := githooks.Run(cfg2)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !res.Drifted {
		t.Error("expected Drifted=true")
	}
	out := ghOut(cfg2)
	if !strings.Contains(out, "DRIFTED") {
		t.Errorf("expected DRIFTED; got %q", out)
	}
}

// ---- scenario (n): unknown subcommand → error ------------------------------

func TestGitHooksParity_UnknownSubcommand_Error(t *testing.T) {
	cfg := newGHCfg(t, "bogus")
	_, err := githooks.Run(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (o): help key phrases ----------------------------------------

func TestGitHooksParity_Help_KeyPhrases(t *testing.T) {
	var buf bytes.Buffer
	githooks.PrintHelp(&buf)
	help := buf.String()
	for _, phrase := range []string{
		"install",
		"uninstall",
		"status",
		"--force",
		"--promotion-gate",
		"pre-push",
	} {
		if !strings.Contains(help, phrase) {
			t.Errorf("help missing phrase %q", phrase)
		}
	}
}
