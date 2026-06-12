// Package githooks tests — Phase 1 parity coverage for `yakos git-hooks`.
//
// Design:
//   - No real git binary required; GitRepoRoot is always injected.
//   - No real filesystem side-effects beyond t.TempDir().
//   - SHA256File is injected for deterministic hash values.
//   - Atomic temp-rename is exercised by verifying no .tmp artifacts remain.
//
// Scenarios:
//
//	(a) install: gate installed fresh (no existing hook)
//	(b) install: idempotent when same hash already installed
//	(c) install: error when stale hash and no --force
//	(d) install: --force overwrites stale hash
//	(e) install: error when pre-push exists without hash sibling (not owned)
//	(f) install: --force overwrites non-owned hook
//	(g) install: --promotion-gate creates composed script
//	(h) install: error when gate src missing
//	(i) install: error when promotion-gate src missing (with --promotion-gate)
//	(j) install: no .tmp artifact left behind
//	(k) install: hash sibling written with correct content
//	(l) uninstall: removes hook and sibling
//	(m) uninstall: no-op when hook absent
//	(n) uninstall: error when not YakOS-owned (no sibling)
//	(o) status: NOT INSTALLED when hook absent
//	(p) status: PRESENT but not YakOS-owned
//	(q) status: INSTALLED no drift
//	(r) status: INSTALLED DRIFTED
//	(s) status: composed hash detected (promotion gate)
//	(t) unknown subcommand → error
//	(u) help subcommand → no error
//	(v) PrintHelp contains key phrases
//	(w) buildComposedScript output has shebang
//	(x) Result fields populated correctly for install
//	(y) uninstall Result.Installed=false
//	(z) defaultSHA256File produces consistent hash for same content
package githooks

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

const fakeGateContent = "#!/usr/bin/env bash\n# fake gate\n"
const fakePromotionContent = "#!/usr/bin/env bash\n# fake promotion gate\n"

// buildYakosRoot creates a fake YAKOS_ROOT with the gate script files.
func buildYakosRoot(t *testing.T, withGate, withPromotion bool) string {
	t.Helper()
	root := t.TempDir()
	gateDir := filepath.Join(root, "lib", "hooks", "git")
	if err := os.MkdirAll(gateDir, 0755); err != nil {
		t.Fatalf("mkdir gate dir: %v", err)
	}
	if withGate {
		if err := os.WriteFile(filepath.Join(gateDir, "pre-push-version-gate.sh"),
			[]byte(fakeGateContent), 0755); err != nil {
			t.Fatalf("write gate: %v", err)
		}
	}
	if withPromotion {
		if err := os.WriteFile(filepath.Join(gateDir, "pre-push-promotion-gate.sh"),
			[]byte(fakePromotionContent), 0755); err != nil {
			t.Fatalf("write promotion gate: %v", err)
		}
	}
	return root
}

func newCfg(t *testing.T, sub string) Config {
	t.Helper()
	repoRoot := t.TempDir()
	// Pre-create .git/hooks so hook writes succeed.
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git", "hooks"), 0755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	return Config{
		Subcommand: sub,
		YakosRoot:  buildYakosRoot(t, true, true),
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

func cfgOut(cfg Config) string { return cfg.Writer.(*bytes.Buffer).String() }

func hookPath(cfg Config) string {
	return filepath.Join(cfg.RepoRoot, ".git", "hooks", "pre-push")
}
func hashSiblingPath(cfg Config) string {
	return hookPath(cfg) + ".framework-hash"
}

// ---- scenario (a): install creates fresh hook ------------------------------

func TestGitHooksParity_Install_Fresh(t *testing.T) {
	cfg := newCfg(t, "install")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !res.Installed {
		t.Error("expected Installed=true")
	}
	if !res.Owned {
		t.Error("expected Owned=true")
	}
	if _, err := os.Stat(hookPath(cfg)); err != nil {
		t.Errorf("hook file missing: %v", err)
	}
	if _, err := os.Stat(hashSiblingPath(cfg)); err != nil {
		t.Errorf("hash sibling missing: %v", err)
	}
}

// ---- scenario (b): install idempotent when same hash ----------------------

func TestGitHooksParity_Install_Idempotent(t *testing.T) {
	cfg := newCfg(t, "install")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("first install: %v", err)
	}
	cfg2 := newCfg(t, "install")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.RepoRoot = cfg.RepoRoot

	res, err := Run(cfg2)
	if err != nil {
		t.Fatalf("second install (idempotent): %v", err)
	}
	if !res.Installed {
		t.Error("expected Installed=true on idempotent re-install")
	}
	out := cfgOut(cfg2)
	if !strings.Contains(out, "nothing to do") {
		t.Errorf("expected 'nothing to do'; got %q", out)
	}
}

// ---- scenario (c): error when stale hash without --force ------------------

func TestGitHooksParity_Install_StalHash_NoForce_Error(t *testing.T) {
	cfg := newCfg(t, "install")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Overwrite hash sibling with a stale hash.
	if err := os.WriteFile(hashSiblingPath(cfg), []byte("stalehash\n"), 0644); err != nil {
		t.Fatalf("overwrite hash: %v", err)
	}

	cfg2 := newCfg(t, "install")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.RepoRoot = cfg.RepoRoot

	_, err := Run(cfg2)
	if err == nil {
		t.Fatal("expected error for stale hash")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (d): --force overwrites stale hash --------------------------

func TestGitHooksParity_Install_StalHash_Force_Succeeds(t *testing.T) {
	cfg := newCfg(t, "install")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("first install: %v", err)
	}

	if err := os.WriteFile(hashSiblingPath(cfg), []byte("stalehash\n"), 0644); err != nil {
		t.Fatalf("overwrite hash: %v", err)
	}

	cfg2 := newCfg(t, "install")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.RepoRoot = cfg.RepoRoot
	cfg2.Force = true

	res, err := Run(cfg2)
	if err != nil {
		t.Fatalf("install --force: %v", err)
	}
	if !res.Installed {
		t.Error("expected Installed=true after --force")
	}
}

// ---- scenario (e): error when hook exists without hash sibling ------------

func TestGitHooksParity_Install_NotOwned_NoForce_Error(t *testing.T) {
	cfg := newCfg(t, "install")
	// Write a hook without a hash sibling.
	if err := os.WriteFile(hookPath(cfg), []byte("#!/bin/sh\necho hi\n"), 0755); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for non-owned hook")
	}
	if !strings.Contains(err.Error(), "YakOS-owned") || !strings.Contains(err.Error(), "destructive") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (f): --force overwrites non-owned hook ----------------------

func TestGitHooksParity_Install_NotOwned_Force_Succeeds(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Force = true
	if err := os.WriteFile(hookPath(cfg), []byte("#!/bin/sh\necho hi\n"), 0755); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install --force non-owned: %v", err)
	}
	if !res.Installed {
		t.Error("expected Installed=true")
	}
}

// ---- scenario (g): --promotion-gate creates composed script ---------------

func TestGitHooksParity_Install_PromotionGate_Composed(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.WithPromotionGate = true

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install --promotion-gate: %v", err)
	}
	if !res.WithPromotion {
		t.Error("expected WithPromotion=true")
	}

	// Hook file should contain shebang and both gate paths.
	data, _ := os.ReadFile(hookPath(cfg)) //nolint:gosec
	if !strings.Contains(string(data), "composed pre-push") {
		t.Errorf("expected 'composed pre-push' in hook content; got: %q", string(data)[:min(200, len(data))])
	}

	// Hash sibling should be pipe-separated.
	hashData, _ := os.ReadFile(hashSiblingPath(cfg)) //nolint:gosec
	if !strings.Contains(string(hashData), "|") {
		t.Errorf("expected pipe-separated hash in sibling; got: %q", string(hashData))
	}
}

// ---- scenario (h): error when gate src missing ----------------------------

func TestGitHooksParity_Install_GateSrcMissing_Error(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.YakosRoot = t.TempDir() // no gate script

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing gate src")
	}
	if !strings.Contains(err.Error(), "gate source not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (i): error when promotion src missing (with --promotion-gate) -

func TestGitHooksParity_Install_PromotionSrcMissing_Error(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.WithPromotionGate = true
	// Build root with gate but no promotion gate.
	cfg.YakosRoot = buildYakosRoot(t, true, false)

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing promotion gate src")
	}
	if !strings.Contains(err.Error(), "promotion gate source not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (j): no .tmp artifact left behind --------------------------

func TestGitHooksParity_Install_NoTmpArtifact(t *testing.T) {
	cfg := newCfg(t, "install")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(hookPath(cfg) + ".tmp"); err == nil {
		t.Errorf(".tmp artifact left behind at %s.tmp", hookPath(cfg))
	}
}

// ---- scenario (k): hash sibling contains correct hash ---------------------

func TestGitHooksParity_Install_HashSiblingContent(t *testing.T) {
	cfg := newCfg(t, "install")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}

	data, err := os.ReadFile(hashSiblingPath(cfg)) //nolint:gosec
	if err != nil {
		t.Fatalf("read hash sibling: %v", err)
	}
	hash := strings.TrimSpace(string(data))

	// Verify by computing ourselves.
	gateData, _ := os.ReadFile(filepath.Join(cfg.YakosRoot, GateSrcRelPath)) //nolint:gosec
	expected := fmt.Sprintf("%x", sha256.Sum256(gateData))
	if hash != expected {
		t.Errorf("hash mismatch: got %q; want %q", hash, expected)
	}
}

// ---- scenario (l): uninstall removes hook and sibling ---------------------

func TestGitHooksParity_Uninstall_RemovesFiles(t *testing.T) {
	cfg := newCfg(t, "install")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}

	cfg2 := newCfg(t, "uninstall")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.RepoRoot = cfg.RepoRoot

	res, err := Run(cfg2)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if res.Installed {
		t.Error("expected Installed=false after uninstall")
	}
	if _, err := os.Stat(hookPath(cfg)); err == nil {
		t.Error("hook file still present after uninstall")
	}
	if _, err := os.Stat(hashSiblingPath(cfg)); err == nil {
		t.Error("hash sibling still present after uninstall")
	}
}

// ---- scenario (m): uninstall no-op when hook absent -----------------------

func TestGitHooksParity_Uninstall_NoHook_Noop(t *testing.T) {
	cfg := newCfg(t, "uninstall")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("uninstall (no hook): %v", err)
	}
	if res.Installed {
		t.Error("expected Installed=false")
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "nothing to remove") {
		t.Errorf("expected 'nothing to remove'; got %q", out)
	}
}

// ---- scenario (n): uninstall error when not owned -------------------------

func TestGitHooksParity_Uninstall_NotOwned_Error(t *testing.T) {
	cfg := newCfg(t, "uninstall")
	// Write hook without sibling.
	if err := os.WriteFile(hookPath(cfg), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for non-owned hook")
	}
	if !strings.Contains(err.Error(), "YakOS-owned") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (o): status NOT INSTALLED -----------------------------------

func TestGitHooksParity_Status_NotInstalled(t *testing.T) {
	cfg := newCfg(t, "status")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if res.Installed {
		t.Error("expected Installed=false")
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "NOT INSTALLED") {
		t.Errorf("expected NOT INSTALLED; got %q", out)
	}
}

// ---- scenario (p): status PRESENT but not owned --------------------------

func TestGitHooksParity_Status_PresentNotOwned(t *testing.T) {
	cfg := newCfg(t, "status")
	if err := os.WriteFile(hookPath(cfg), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !res.Installed {
		t.Error("expected Installed=true")
	}
	if res.Owned {
		t.Error("expected Owned=false (no sibling)")
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "not YakOS-owned") {
		t.Errorf("expected 'not YakOS-owned'; got %q", out)
	}
}

// ---- scenario (q): status INSTALLED no drift --------------------------------

func TestGitHooksParity_Status_InstalledNoDrift(t *testing.T) {
	cfg := newCfg(t, "install")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}

	cfg2 := newCfg(t, "status")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.RepoRoot = cfg.RepoRoot

	res, err := Run(cfg2)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !res.Installed {
		t.Error("expected Installed=true")
	}
	if res.Drifted {
		t.Error("expected Drifted=false (same hash)")
	}
	out := cfgOut(cfg2)
	if !strings.Contains(out, "no drift") {
		t.Errorf("expected 'no drift'; got %q", out)
	}
}

// ---- scenario (r): status INSTALLED DRIFTED --------------------------------

func TestGitHooksParity_Status_InstalledDrifted(t *testing.T) {
	cfg := newCfg(t, "install")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Overwrite hash sibling with wrong hash.
	if err := os.WriteFile(hashSiblingPath(cfg), []byte("wronghash\n"), 0644); err != nil {
		t.Fatalf("overwrite hash: %v", err)
	}

	cfg2 := newCfg(t, "status")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.RepoRoot = cfg.RepoRoot

	res, err := Run(cfg2)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !res.Drifted {
		t.Error("expected Drifted=true")
	}
	out := cfgOut(cfg2)
	if !strings.Contains(out, "DRIFTED") {
		t.Errorf("expected DRIFTED; got %q", out)
	}
	if !strings.Contains(out, "--force") {
		t.Errorf("expected --force hint; got %q", out)
	}
}

// ---- scenario (s): status handles composed (pipe) hash -------------------

func TestGitHooksParity_Status_ComposedHash(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.WithPromotionGate = true
	if _, err := Run(cfg); err != nil {
		t.Fatalf("install promotion: %v", err)
	}

	cfg2 := newCfg(t, "status")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.RepoRoot = cfg.RepoRoot

	res, err := Run(cfg2)
	if err != nil {
		t.Fatalf("status composed: %v", err)
	}
	// Composed hash contains the gate portion; status compares gate hash only.
	// Since the gate script hasn't changed, Drifted should be false.
	if res.Drifted {
		t.Error("expected Drifted=false for composed (gate portion matches)")
	}
}

// ---- scenario (t): unknown subcommand → error -----------------------------

func TestGitHooksParity_UnknownSubcommand_Error(t *testing.T) {
	cfg := newCfg(t, "bogus")
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (u): help subcommand ----------------------------------------

func TestGitHooksParity_Help_NoError(t *testing.T) {
	cfg := newCfg(t, "help")
	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	out := cfgOut(cfg)
	if len(out) == 0 {
		t.Error("expected non-empty help output")
	}
}

// ---- scenario (v): PrintHelp contains key phrases -------------------------

func TestGitHooksParity_PrintHelp_Phrases(t *testing.T) {
	var buf bytes.Buffer
	PrintHelp(&buf)
	help := buf.String()
	for _, phrase := range []string{
		"install",
		"uninstall",
		"status",
		"--force",
		"--promotion-gate",
		"pre-push",
		"YAKOS_GATE_DISABLE",
	} {
		if !strings.Contains(help, phrase) {
			t.Errorf("help missing phrase %q", phrase)
		}
	}
}

// ---- scenario (w): buildComposedScript has shebang ------------------------

func TestGitHooksParity_BuildComposedScript_HasShebang(t *testing.T) {
	script := buildComposedScript("/gate/src.sh", "/promotion/src.sh")
	if !strings.HasPrefix(script, "#!/usr/bin/env bash") {
		t.Errorf("composed script missing shebang; got: %q", script[:min(80, len(script))])
	}
	if !strings.Contains(script, "composed pre-push") {
		t.Errorf("expected 'composed pre-push' comment; got: %q", script[:min(200, len(script))])
	}
}

// ---- scenario (x): Result fields correct for install ----------------------

func TestGitHooksParity_Install_ResultFields(t *testing.T) {
	cfg := newCfg(t, "install")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.Subcommand != "install" {
		t.Errorf("expected Subcommand=install; got %q", res.Subcommand)
	}
	if res.RepoRoot != cfg.RepoRoot {
		t.Errorf("expected RepoRoot=%s; got %s", cfg.RepoRoot, res.RepoRoot)
	}
	if res.HookPath == "" {
		t.Error("expected non-empty HookPath")
	}
	if res.HashSiblingPath == "" {
		t.Error("expected non-empty HashSiblingPath")
	}
}

// ---- scenario (y): uninstall Result.Installed=false ----------------------

func TestGitHooksParity_Uninstall_ResultInstalled(t *testing.T) {
	cfg := newCfg(t, "install")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}

	cfg2 := newCfg(t, "uninstall")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.RepoRoot = cfg.RepoRoot

	res, err := Run(cfg2)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if res.Installed {
		t.Error("expected Installed=false after uninstall")
	}
}

// ---- scenario (z): defaultSHA256File consistent hash ----------------------

func TestGitHooksParity_DefaultSHA256File_Consistent(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "hashtest")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	if _, err := io.WriteString(f, "hello world"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = f.Close()

	h1, err := defaultSHA256File(f.Name())
	if err != nil {
		t.Fatalf("hash1: %v", err)
	}
	h2, err := defaultSHA256File(f.Name())
	if err != nil {
		t.Fatalf("hash2: %v", err)
	}
	if h1 != h2 {
		t.Errorf("expected consistent hash; got %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex hash; got %d chars: %q", len(h1), h1)
	}
}

// ---- min helper -------------------------------------------------------------

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
