// Package githooks implements the Go port of `yakos git-hooks`.
//
// Manages the YakOS git hooks for the current project repository.
//
// Subcommands:
//
//	install                  Install the pre-push version gate.
//	install --promotion-gate Install version-gate + promotion-gate composed
//	                         pre-push (Plan 3 M5 / v0.17+).
//	install --force          Overwrite an existing hook regardless of owner.
//	uninstall                Remove the pre-push hook IFF it is YakOS-owned.
//	status                   Report whether the gate is installed + drift state.
//
// Hook ownership is determined by a `.framework-hash` sibling file adjacent to
// the hook. The file contains the SHA-256 hash of the source gate script(s).
// This allows drift detection between the installed copy and the current
// framework source.
//
// The composed pre-push (--promotion-gate) concatenates the hashes of both
// gate scripts separated by "|" so drift detection catches changes to either.
//
// Atomic write: hook scripts are written to a temp file first, then renamed
// into place (Q8 discipline). The .framework-hash sibling is written after
// the rename to ensure consistency.
//
// Exit-code contract mirrors git-hooks.sh:
//
//	0  success
//	2  user error (bad args)
//	3  not inside a git repo
//	4  file I/O error
package githooks

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GateSrcRelPath is the path to the version gate relative to YAKOS_ROOT.
const GateSrcRelPath = "lib/hooks/git/pre-push-version-gate.sh"

// PromotionGateSrcRelPath is the path to the promotion gate relative to YAKOS_ROOT.
const PromotionGateSrcRelPath = "lib/hooks/git/pre-push-promotion-gate.sh"

// Config controls a git-hooks Run invocation.
type Config struct {
	// Subcommand is one of: install, uninstall, status.
	Subcommand string

	// Force: overwrite existing hook without checking ownership.
	Force bool

	// WithPromotionGate: compose version-gate + promotion-gate.
	WithPromotionGate bool

	// YakosRoot is the framework repo root (YAKOS_ROOT).
	YakosRoot string

	// RepoRoot is the git repository root. When empty, it is discovered via
	// `git rev-parse --show-toplevel` (or GitRepoRoot function if injected).
	RepoRoot string

	// GitRepoRoot is an optional function for discovering the git repo root.
	// Injected by tests; real code falls back to running git.
	GitRepoRoot func() (string, error)

	// SHA256File is an optional function for hashing a file.
	// Injected by tests; real code uses the built-in implementation.
	SHA256File func(path string) (string, error)

	// Writer is the output destination. Defaults to os.Stdout.
	Writer io.Writer

	// ErrWriter is the error output destination. Defaults to os.Stderr.
	ErrWriter io.Writer
}

// Result is the structured outcome of a git-hooks Run invocation.
type Result struct {
	Subcommand string
	// RepoRoot is the resolved git repo root.
	RepoRoot string
	// HookPath is the path to the pre-push hook.
	HookPath string
	// HashSiblingPath is the path to the .framework-hash sibling.
	HashSiblingPath string
	// Installed reports whether the hook is present after the call.
	Installed bool
	// Drifted reports whether the installed hash differs from the source hash
	// (meaningful only when Installed=true and Owned=true).
	Drifted bool
	// Owned reports whether the hook has a .framework-hash sibling.
	Owned bool
	// WithPromotion reflects whether the --promotion-gate flag was active.
	WithPromotion bool
}

// Run executes the git-hooks subcommand described by cfg.
func Run(cfg Config) (*Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}
	sha256fn := cfg.SHA256File
	if sha256fn == nil {
		sha256fn = defaultSHA256File
	}

	switch cfg.Subcommand {
	case "install":
		return runInstall(cfg, sha256fn)
	case "uninstall":
		return runUninstall(cfg, sha256fn)
	case "status":
		return runStatus(cfg, sha256fn)
	case "", "-h", "--help", "help":
		PrintHelp(cfg.Writer)
		return &Result{Subcommand: cfg.Subcommand}, nil
	default:
		return nil, fmt.Errorf("git-hooks: unknown subcommand %q (try --help)", cfg.Subcommand)
	}
}

// PrintHelp prints the git-hooks help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos git-hooks {install|uninstall|status} [--force]

Manage YakOS git hooks for the current project repo.

  install                  Install the pre-push version gate.
  install --promotion-gate Install version-gate + promotion-gate composed
                           pre-push (Plan 3 M5 / v0.17+). Promotion gate
                           refuses pushes that violate dev→test→prod order
                           when .yakos.yml declares environments.
  uninstall                Remove the pre-push hook IFF it's YakOS-owned
                           (verified by .framework-hash sibling).
  status                   Report whether the gate is installed + drift state.

Hooks honor YAKOS_GATE_DISABLE=1 / YAKOS_PROMOTION_OVERRIDE=1 (bypass + log)
and `+"`"+`git push --no-verify`+"`"+`. See lib/hooks/git/README.md for the contract.
`)
}

// ---- repo root resolution ---------------------------------------------------

func resolveRepoRoot(cfg Config) (string, error) {
	if cfg.RepoRoot != "" {
		return cfg.RepoRoot, nil
	}
	if cfg.GitRepoRoot != nil {
		return cfg.GitRepoRoot()
	}
	return defaultGitRepoRoot()
}

func defaultGitRepoRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("not inside a git repo (run from a project)")
	}
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ---- install ----------------------------------------------------------------

func runInstall(cfg Config, sha256fn func(string) (string, error)) (*Result, error) {
	repoRoot, err := resolveRepoRoot(cfg)
	if err != nil {
		return nil, err
	}

	gateSrc := filepath.Join(cfg.YakosRoot, GateSrcRelPath)
	promotionSrc := filepath.Join(cfg.YakosRoot, PromotionGateSrcRelPath)

	if _, err := os.Stat(gateSrc); err != nil {
		return nil, fmt.Errorf("gate source not found: %s", gateSrc)
	}
	if cfg.WithPromotionGate {
		if _, err := os.Stat(promotionSrc); err != nil {
			return nil, fmt.Errorf("promotion gate source not found: %s", promotionSrc)
		}
	}

	hookDir := filepath.Join(repoRoot, ".git", "hooks")
	if err := os.MkdirAll(hookDir, 0755); err != nil { //nolint:gosec
		return nil, fmt.Errorf("mkdir %s: %w", hookDir, err)
	}

	dst := filepath.Join(hookDir, "pre-push")
	hashSibling := dst + ".framework-hash"

	// Compute source hash.
	var srcHash string
	if cfg.WithPromotionGate {
		h1, err := sha256fn(gateSrc)
		if err != nil {
			return nil, fmt.Errorf("hash gate src: %w", err)
		}
		h2, err := sha256fn(promotionSrc)
		if err != nil {
			return nil, fmt.Errorf("hash promotion src: %w", err)
		}
		srcHash = h1 + "|" + h2
	} else {
		h, err := sha256fn(gateSrc)
		if err != nil {
			return nil, fmt.Errorf("hash gate src: %w", err)
		}
		srcHash = h
	}

	// Check existing hook.
	if _, err := os.Stat(dst); err == nil && !cfg.Force {
		if _, err := os.Stat(hashSibling); err == nil {
			existingHash, _ := readTrimmed(hashSibling)
			if srcHash == existingHash {
				_, _ = fmt.Fprintf(cfg.Writer, "pre-push gate already installed at %s (current version); nothing to do\n", dst)
				return &Result{
					Subcommand:      "install",
					RepoRoot:        repoRoot,
					HookPath:        dst,
					HashSiblingPath: hashSibling,
					Installed:       true,
					Owned:           true,
					Drifted:         false,
					WithPromotion:   cfg.WithPromotionGate,
				}, nil
			}
			return nil, fmt.Errorf("pre-push hook is YakOS-owned but stale or composition changed (rerun with --force to refresh)")
		}
		return nil, fmt.Errorf("pre-push hook exists at %s and isn't YakOS-owned (rerun with --force to overwrite — destructive)", dst)
	}

	// Write hook script (atomic temp-rename).
	if cfg.WithPromotionGate {
		composed := buildComposedScript(gateSrc, promotionSrc)
		if err := atomicWriteFile(dst, []byte(composed), 0755); err != nil {
			return nil, fmt.Errorf("write hook: %w", err)
		}
		_, _ = fmt.Fprintf(cfg.Writer, "pre-push (version-gate + promotion-gate) installed at %s\n", dst)
		_, _ = fmt.Fprintf(cfg.Writer, "  override version-gate:   YAKOS_GATE_DISABLE=1 git push\n")
		_, _ = fmt.Fprintf(cfg.Writer, "  override promotion-gate: YAKOS_PROMOTION_OVERRIDE=1 git push\n")
		_, _ = fmt.Fprintf(cfg.Writer, "  both logged to ~/.yakos-state/gate-log.ndjson\n")
	} else {
		// Copy gate script.
		data, err := os.ReadFile(gateSrc) //nolint:gosec
		if err != nil {
			return nil, fmt.Errorf("read gate src: %w", err)
		}
		if err := atomicWriteFile(dst, data, 0755); err != nil {
			return nil, fmt.Errorf("write hook: %w", err)
		}
		_, _ = fmt.Fprintf(cfg.Writer, "pre-push gate installed at %s\n", dst)
		_, _ = fmt.Fprintf(cfg.Writer, "  to override: YAKOS_GATE_DISABLE=1 git push   (logged to ~/.yakos-state/gate-log.ndjson)\n")
	}
	_, _ = fmt.Fprintf(cfg.Writer, "  to remove:   yakos git-hooks uninstall\n")

	// Write hash sibling (after hook file is in place).
	if err := os.WriteFile(hashSibling, []byte(srcHash+"\n"), 0644); err != nil { //nolint:gosec
		return nil, fmt.Errorf("write hash sibling: %w", err)
	}

	return &Result{
		Subcommand:      "install",
		RepoRoot:        repoRoot,
		HookPath:        dst,
		HashSiblingPath: hashSibling,
		Installed:       true,
		Owned:           true,
		Drifted:         false,
		WithPromotion:   cfg.WithPromotionGate,
	}, nil
}

// buildComposedScript constructs the composed pre-push hook that runs both
// gates, buffering stdin so it can be passed to each gate separately.
func buildComposedScript(gateSrc, promotionSrc string) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
# YakOS composed pre-push: version gate + promotion gate.
# Generated by yakos git-hooks install --promotion-gate.
set -eu
stdin_buf="$(cat)"
rc=0
printf '%%s' "$stdin_buf" | %q "$@" || rc=$?
[ "$rc" -eq 0 ] || exit "$rc"
printf '%%s' "$stdin_buf" | %q "$@" || rc=$?
exit "$rc"
`, gateSrc, promotionSrc)
}

// ---- uninstall --------------------------------------------------------------

func runUninstall(cfg Config, _ func(string) (string, error)) (*Result, error) {
	repoRoot, err := resolveRepoRoot(cfg)
	if err != nil {
		return nil, err
	}

	dst := filepath.Join(repoRoot, ".git", "hooks", "pre-push")
	hashSibling := dst + ".framework-hash"

	if _, err := os.Stat(dst); os.IsNotExist(err) {
		_, _ = fmt.Fprintf(cfg.Writer, "pre-push hook not present at %s; nothing to remove\n", dst)
		return &Result{
			Subcommand:      "uninstall",
			RepoRoot:        repoRoot,
			HookPath:        dst,
			HashSiblingPath: hashSibling,
			Installed:       false,
		}, nil
	}

	if _, err := os.Stat(hashSibling); err != nil {
		return nil, fmt.Errorf("pre-push hook at %s isn't YakOS-owned (no .framework-hash); refusing to remove", dst)
	}

	if err := os.Remove(dst); err != nil {
		return nil, fmt.Errorf("remove hook: %w", err)
	}
	if err := os.Remove(hashSibling); err != nil {
		// Non-fatal: hook is already removed.
		_, _ = fmt.Fprintf(cfg.ErrWriter, "git-hooks: could not remove hash sibling: %v\n", err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "pre-push gate removed from %s\n", dst)

	return &Result{
		Subcommand:      "uninstall",
		RepoRoot:        repoRoot,
		HookPath:        dst,
		HashSiblingPath: hashSibling,
		Installed:       false,
		Owned:           false,
	}, nil
}

// ---- status -----------------------------------------------------------------

func runStatus(cfg Config, sha256fn func(string) (string, error)) (*Result, error) {
	repoRoot, err := resolveRepoRoot(cfg)
	if err != nil {
		return nil, err
	}

	dst := filepath.Join(repoRoot, ".git", "hooks", "pre-push")
	hashSibling := dst + ".framework-hash"

	res := &Result{
		Subcommand:      "status",
		RepoRoot:        repoRoot,
		HookPath:        dst,
		HashSiblingPath: hashSibling,
	}

	if _, err := os.Stat(dst); os.IsNotExist(err) {
		_, _ = fmt.Fprintf(cfg.Writer, "pre-push gate: NOT INSTALLED\n")
		_, _ = fmt.Fprintf(cfg.Writer, "  install: yakos git-hooks install\n")
		return res, nil
	}
	res.Installed = true

	if _, err := os.Stat(hashSibling); err != nil {
		_, _ = fmt.Fprintf(cfg.Writer, "pre-push gate: PRESENT but not YakOS-owned (no .framework-hash sibling)\n")
		return res, nil
	}
	res.Owned = true

	// Drift check.
	existingHash, err := readTrimmed(hashSibling)
	if err != nil {
		_, _ = fmt.Fprintf(cfg.Writer, "pre-push gate: PRESENT (could not read .framework-hash: %v)\n", err)
		return res, nil
	}

	// For the status check, compute the version-gate source hash.
	// If the installed hash contains "|", it's a composed hash.
	gateSrc := filepath.Join(cfg.YakosRoot, GateSrcRelPath)
	currentHash := ""
	if _, err := os.Stat(gateSrc); err == nil {
		currentHash, _ = sha256fn(gateSrc)
	}

	// For composed hooks, compare only the version-gate portion (first segment).
	installedHashPart := existingHash
	if idx := strings.Index(existingHash, "|"); idx >= 0 {
		installedHashPart = existingHash[:idx]
	}

	if currentHash != "" && currentHash != installedHashPart {
		res.Drifted = true
		_, _ = fmt.Fprintf(cfg.Writer, "pre-push gate: INSTALLED but DRIFTED from framework\n")
		_, _ = fmt.Fprintf(cfg.Writer, "  framework hash: %s\n", currentHash)
		_, _ = fmt.Fprintf(cfg.Writer, "  installed hash: %s\n", existingHash)
		_, _ = fmt.Fprintf(cfg.Writer, "  to refresh: yakos git-hooks install --force\n")
	} else {
		_, _ = fmt.Fprintf(cfg.Writer, "pre-push gate: INSTALLED (current version, no drift)\n")
	}

	return res, nil
}

// ---- helpers ----------------------------------------------------------------

// defaultSHA256File computes the hex-encoded SHA-256 of a file.
func defaultSHA256File(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// atomicWriteFile writes data to path with the given perm, using temp-rename.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Chmod(tmp, perm); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s → %s: %w", tmp, path, err)
	}
	_ = dir // suppress unused warning
	return nil
}

// readTrimmed reads a file and returns its contents trimmed of whitespace.
func readTrimmed(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
