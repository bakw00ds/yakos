package install

// materialize.go — binary-only install support.
//
// When yakos is installed via `curl | sh` without a cloned repo, the binary
// has no on-disk lib/ tree.  This file provides the logic to:
//
//  1. Detect whether the framework lib is already available on disk
//     (repo clone with lib/ or YAKOS_ROOT pointing at one).
//  2. Materialize the embedded lib (via the framework package) to a stable
//     user-owned directory when no on-disk lib/ is available.
//  3. Return the chosen root path so the rest of install.Run can continue.
//
// Materialized lib location:
//
//	~/.local/share/yakos/<version>/
//
// where <version> is the value passed by the caller (from version.Version
// set at build time via -ldflags).  A version stamp file is written so that
// re-materialization happens automatically when the version changes.
//
// The function is idempotent: calling it multiple times with the same version
// is a no-op if the dest directory already exists and contains the expected
// stamp.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/internal/framework"
)

// materializedStampFile is the name of the version-stamp file written inside
// the materialized root directory.
const materializedStampFile = ".yakos-embedded-version"

// MaterializedLibDir returns the canonical path for the materialized embedded
// lib for the given version under the user's home directory.
//
// Format: $HOME/.local/share/yakos/<version>/
//
// When version is empty, "dev" is used so dev builds get a stable slot too.
func MaterializedLibDir(home, version string) string {
	v := strings.TrimSpace(version)
	if v == "" {
		v = "dev"
	}
	return filepath.Join(home, ".local", "share", "yakos", v)
}

// ResolveRoot determines the YakOS framework root to use for this install.
//
// Resolution order (mirrors the spec):
//
//  1. YAKOS_ROOT env var — if set and $YAKOS_ROOT/lib exists, use it.
//     This preserves the dev-with-repo experience: editing lib/ agents
//     is immediately reflected without rebuild.
//
//  2. Caller-supplied repoRoot (derived from exe path) — if it has a lib/
//     directory, use it.  This covers the "binary installed inside a repo
//     clone" case.
//
//  3. Embedded lib — if the binary carries the embedded lib (HasEmbeddedLib()
//     returns true), materialize it to ~/.local/share/yakos/<version>/ and
//     return that path.
//
//  4. No lib found anywhere — return an error with a clear message telling
//     the operator to either set YAKOS_ROOT or use a release binary.
//
// force causes the materialized lib to be re-extracted even if the version
// stamp already matches.  dry-run skips writing to disk and returns the path
// that WOULD be used.
func ResolveRoot(repoRoot, home, version string, force, dryRun bool, w io.Writer) (string, error) {
	// 1. YAKOS_ROOT env override.
	if envRoot := os.Getenv("YAKOS_ROOT"); envRoot != "" {
		abs, err := filepath.Abs(envRoot)
		if err == nil {
			if fi, err := os.Stat(filepath.Join(abs, "lib")); err == nil && fi.IsDir() {
				_, _ = fmt.Fprintf(w, "  Using framework root from YAKOS_ROOT env: %s\n", abs)
				return abs, nil
			}
		}
		// YAKOS_ROOT was set but lib/ doesn't exist there — warn and fall through.
		_, _ = fmt.Fprintf(w, "  WARNING: YAKOS_ROOT=%s but no lib/ directory there; ignoring\n", envRoot)
	}

	// 2. Binary-adjacent repo clone (classic install).
	if repoRoot != "" {
		if fi, err := os.Stat(filepath.Join(repoRoot, "lib")); err == nil && fi.IsDir() {
			_, _ = fmt.Fprintf(w, "  Using framework root from binary location: %s\n", repoRoot)
			return repoRoot, nil
		}
	}

	// 3. Embedded lib.
	if !framework.HasEmbeddedLib() {
		return "", fmt.Errorf("install: no framework lib found on disk (YAKOS_ROOT unset or has no lib/) " +
			"and this binary was built without the embedded framework; " +
			"set YAKOS_ROOT to a yakOS repo clone or install a release binary " +
			"(release binaries carry the embedded lib)")
	}

	// Materialize to ~/.local/share/yakos/<version>/
	destRoot := MaterializedLibDir(home, version)
	_, _ = fmt.Fprintf(w, "  No on-disk repo found; materializing embedded lib to %s\n", destRoot)

	if dryRun {
		_, _ = fmt.Fprintf(w, "  [dry-run] would materialize embedded lib to %s\n", destRoot)
		return destRoot, nil
	}

	if err := materializeEmbeddedLib(destRoot, version, force, w); err != nil {
		return "", fmt.Errorf("install: materializing embedded lib: %w", err)
	}
	return destRoot, nil
}

// materializeEmbeddedLib writes the embedded lib tree to destRoot.
// It is idempotent: if the version stamp matches and force is false, it
// skips the copy and returns nil.
func materializeEmbeddedLib(destRoot, version string, force bool, w io.Writer) error {
	stampPath := filepath.Join(destRoot, materializedStampFile)

	if !force {
		// Check version stamp.
		if stamp, err := os.ReadFile(stampPath); err == nil { //nolint:gosec
			if strings.TrimSpace(string(stamp)) == version {
				_, _ = fmt.Fprintf(w, "  Embedded lib already materialized at %s (version %s)\n", destRoot, version)
				return nil
			}
		}
	}

	_, _ = fmt.Fprintf(w, "  Materializing embedded lib to %s...\n", destRoot)

	if err := os.MkdirAll(destRoot, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir %s: %w", destRoot, err)
	}

	if err := framework.MaterializeLib(destRoot); err != nil {
		return fmt.Errorf("extracting embedded lib: %w", err)
	}

	// Write version stamp.
	v := strings.TrimSpace(version)
	if v == "" {
		v = "dev"
	}
	if err := os.WriteFile(stampPath, []byte(v+"\n"), 0644); err != nil { //nolint:gosec
		// Non-fatal — warn but continue.
		_, _ = fmt.Fprintf(w, "  WARNING: could not write version stamp: %v\n", err)
	}

	_, _ = fmt.Fprintf(w, "  Materialized lib to %s\n", destRoot)
	return nil
}

// MaterializeEmbedded materializes the embedded lib to destRoot when the
// binary carries real framework content (HasEmbeddedLib() is true).
//
// It is idempotent — calling it when the version stamp already matches is a
// no-op.  Returns true when the lib is now present (materialized now or
// already present from a previous run), false when the binary was built
// without the embedded lib (dev build).
//
// When materialization writes new files, a single-line log is written to w:
//
//	"materialized embedded framework lib to <destRoot>"
//
// No log is written when the lib was already present and up-to-date.
//
// This is the public companion to the internal materializeEmbeddedLib used
// by runInstall; runtime commands (start, serve) call this to auto-resolve
// the lib without requiring an explicit `yakos install` step.
func MaterializeEmbedded(destRoot, version string, w io.Writer) bool {
	if !framework.HasEmbeddedLib() {
		return false
	}
	// Detect whether materialization will actually write (stamp mismatch).
	needsWrite := true
	stampPath := filepath.Join(destRoot, materializedStampFile)
	if stamp, err := os.ReadFile(stampPath); err == nil { //nolint:gosec
		v := strings.TrimSpace(version)
		if v == "" {
			v = "dev"
		}
		if strings.TrimSpace(string(stamp)) == v {
			needsWrite = false
		}
	}
	if err := materializeEmbeddedLib(destRoot, version, false, io.Discard); err != nil {
		// Non-fatal: log the failure but don't prevent startup.
		_, _ = fmt.Fprintf(w, "yakos: WARNING: could not materialize embedded lib to %s: %v\n", destRoot, err)
		return false
	}
	if needsWrite {
		_, _ = fmt.Fprintf(w, "materialized embedded framework lib to %s\n", destRoot)
	}
	return true
}

// IsMaterializedRoot returns true when root looks like a materialized
// (not a repo clone) framework root.  The heuristic is: contains lib/ but
// does NOT contain cli/ or .git/.
//
// Used by doctor to accept a materialized root as a valid install pointer.
func IsMaterializedRoot(root string) bool {
	// Must have lib/.
	if _, err := os.Stat(filepath.Join(root, "lib")); err != nil {
		return false
	}
	// Must NOT look like a full repo clone (which has cli/).
	if _, err := os.Stat(filepath.Join(root, "cli")); err == nil {
		return false
	}
	return true
}
