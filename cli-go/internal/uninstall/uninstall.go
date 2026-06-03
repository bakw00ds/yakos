// Package uninstall is the Go port of cli/lib/uninstall.sh.
//
// It implements `yakos uninstall [--restore-settings] [--root <path>] [--dry-run]`:
//
//  1. Reads $HOME/.yakos to determine YAKOS_ROOT (or uses --root).
//  2. Removes per-file symlinks under $HOME/.claude/{agents,skills,rules,playbooks}/
//     that resolve into the YakOS root. Dangling symlinks are also removed
//     (treated as YakOS-owned). Foreign symlinks and real files are never touched.
//  3. Removes the managed launcher symlink recorded in
//     $HOME/.yakos-state/install-manifest, only if it resolves into the YakOS
//     root or is a dangling symlink. Foreign binaries are left alone.
//  4. Handles settings.json:
//     - If $HOME/.claude/.yakos-created-settings exists, removes settings.json
//       (we created it during install) and the marker.
//     - If --restore-settings and a yakos backup exists, restores the most
//       recent backup to settings.json.
//     - Otherwise, lists available backups for the operator.
//  5. Removes $HOME/.yakos and $HOME/.yakos-state/install-manifest.
//     Removes $HOME/.yakos-state if it is now empty.
//  6. NEVER removes $HOME/.claude/projects/ (auto-memory).
//
// Partial-uninstall failure handling: log + continue (no rollback).
// Final report enumerates successes and failures.
//
// Stale-pointer robustness: if $HOME/.yakos points to a path that is missing or
// unreadable, a warning is printed and cleanup continues. Dangling symlinks are
// always removed; live symlinks whose target begins with the stale root are also
// removed if resolvable.
//
// --dry-run: all validation runs; nothing is written to disk.
package uninstall

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// subdirs lists the four ~/.claude subdirectories managed by install/uninstall.
var subdirs = []string{"agents", "skills", "rules", "playbooks"}

// Config controls an uninstall run.
type Config struct {
	// HomeDir overrides $HOME. If empty, os.Getenv("HOME") is used.
	HomeDir string

	// ExplicitRoot, when set, overrides ~/.yakos as YAKOS_ROOT. Mirrors --root.
	ExplicitRoot string

	// RestoreSettings, when true, restores the most recent yakos settings
	// backup to settings.json (mirrors --restore-settings).
	RestoreSettings bool

	// DryRun, when true, reports what would change without writing anything.
	DryRun bool

	// Writer is the output destination. Defaults to os.Stdout.
	Writer io.Writer

	// ErrWriter is the error destination. Defaults to os.Stderr.
	ErrWriter io.Writer
}

// SymlinkReport counts per-file symlink outcomes.
type SymlinkReport struct {
	Removed int // symlinks removed (dangling or YakOS-owned)
	Kept    int // symlinks left in place (foreign or real file)
	Errors  int // failures during removal
}

// LauncherOutcome describes what happened to the launcher symlink.
type LauncherOutcome string

const (
	LauncherRemoved LauncherOutcome = "removed"  // removed (dangling or YakOS-owned)
	LauncherKept    LauncherOutcome = "kept"     // foreign or real file; left alone
	LauncherAbsent  LauncherOutcome = "absent"   // not found at recorded path
	LauncherUnknown LauncherOutcome = "unknown"  // no manifest entry
)

// LauncherReport describes the outcome for the managed launcher.
type LauncherReport struct {
	Outcome LauncherOutcome
	Path    string // absolute path of the launcher entry (empty when unknown)
	Note    string // non-empty explanation when Kept or Unknown
}

// SettingsOutcome describes what happened to settings.json.
type SettingsOutcome string

const (
	SettingsRemoved  SettingsOutcome = "removed"  // removed (we created it)
	SettingsRestored SettingsOutcome = "restored" // restored from backup
	SettingsKept     SettingsOutcome = "kept"     // left alone (user-owned)
	SettingsAbsent   SettingsOutcome = "absent"   // no settings.json present
)

// SettingsReport describes what happened to settings.json.
type SettingsReport struct {
	Outcome        SettingsOutcome
	SettingsFile   string   // absolute path to settings.json
	RestoredFrom   string   // non-empty when Outcome==restored
	AvailableBackups []string // sorted list of backup files when Kept
}

// Result is the complete outcome of an uninstall run.
type Result struct {
	YakosRoot       string        // resolved root (may be empty on stale pointer)
	PointerRemoved  bool          // true when ~/.yakos was removed
	ManifestRemoved bool          // true when ~/.yakos-state/install-manifest was removed
	StateDirRemoved bool          // true when ~/.yakos-state was rmdir'd (empty)
	Symlinks        SymlinkReport
	Launcher        LauncherReport
	Settings        SettingsReport
	DryRun          bool
	Failures        []string // non-fatal failures logged during run
}

// Run executes the uninstall. Returns a structured Result.
// Failures during individual remove steps are logged and continued, not returned
// as errors. An error is returned only for configuration/precondition failures.
func Run(cfg Config) (*Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}

	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}

	claudeDir := filepath.Join(home, ".claude")
	yakosPointer := filepath.Join(home, ".yakos")
	settingsFile := filepath.Join(claudeDir, "settings.json")
	createdMarker := filepath.Join(claudeDir, ".yakos-created-settings")
	yakosStateDir := filepath.Join(home, ".yakos-state")
	installManifest := filepath.Join(yakosStateDir, "install-manifest")

	dryTag := ""
	if cfg.DryRun {
		dryTag = " [DRY RUN]"
	}
	_, _ = fmt.Fprintf(cfg.Writer, "yakos uninstall%s\n\n", dryTag)

	result := &Result{DryRun: cfg.DryRun}

	// Early-out: nothing to do when there is no pointer and no claude dir.
	if cfg.ExplicitRoot == "" {
		pointerExists := fileExists(yakosPointer)
		claudeExists := dirExists(claudeDir)
		if !pointerExists && !claudeExists {
			_, _ = fmt.Fprintf(cfg.Writer, "Nothing to uninstall: no %s and no %s\n",
				yakosPointer, claudeDir)
			return result, nil
		}
	}

	// ---- Resolve the YakOS root -------------------------------------------------

	yakosRoot, pointerStale := resolveRoot(cfg.ExplicitRoot, yakosPointer, cfg.Writer)
	result.YakosRoot = yakosRoot

	// ---- Per-file symlinks ------------------------------------------------------

	srpt := removeSymlinks(yakosRoot, pointerStale, claudeDir, cfg.DryRun, cfg.Writer, cfg.ErrWriter, result)
	result.Symlinks = srpt
	_, _ = fmt.Fprintf(cfg.Writer, "Symlinks: %d removed, %d left in place (not YakOS-owned)\n",
		srpt.Removed, srpt.Kept)

	// ---- Managed launcher symlink -----------------------------------------------

	lrpt := removeLauncher(yakosRoot, installManifest, cfg.DryRun, cfg.Writer, cfg.ErrWriter)
	result.Launcher = lrpt

	// ---- Settings.json ----------------------------------------------------------

	stRpt := handleSettings(settingsFile, createdMarker, claudeDir, cfg.RestoreSettings, cfg.DryRun, cfg.Writer)
	result.Settings = stRpt

	// ---- Pointer and manifest ---------------------------------------------------

	if fileExists(yakosPointer) {
		if cfg.DryRun {
			_, _ = fmt.Fprintf(cfg.Writer, "  [dry-run] would remove pointer: %s\n", yakosPointer)
			result.PointerRemoved = true
		} else {
			if err := os.Remove(yakosPointer); err != nil {
				note := fmt.Sprintf("could not remove pointer %s: %v", yakosPointer, err)
				result.Failures = append(result.Failures, note)
				_, _ = fmt.Fprintf(cfg.ErrWriter, "uninstall: warning: %s\n", note)
			} else {
				_, _ = fmt.Fprintf(cfg.Writer, "Removed %s\n", yakosPointer)
				result.PointerRemoved = true
			}
		}
	}

	if fileExists(installManifest) {
		if cfg.DryRun {
			_, _ = fmt.Fprintf(cfg.Writer, "  [dry-run] would remove manifest: %s\n", installManifest)
			result.ManifestRemoved = true
		} else {
			if err := os.Remove(installManifest); err != nil {
				note := fmt.Sprintf("could not remove manifest %s: %v", installManifest, err)
				result.Failures = append(result.Failures, note)
				_, _ = fmt.Fprintf(cfg.ErrWriter, "uninstall: warning: %s\n", note)
			} else {
				_, _ = fmt.Fprintf(cfg.Writer, "Removed %s\n", installManifest)
				result.ManifestRemoved = true
			}
		}
	}

	// Remove state dir only if now empty.
	if !cfg.DryRun && dirExists(yakosStateDir) {
		if err := os.Remove(yakosStateDir); err == nil {
			result.StateDirRemoved = true
		}
		// Ignore error: dir may not be empty; that is expected.
	}

	// ---- Final report -----------------------------------------------------------

	_, _ = fmt.Fprintf(cfg.Writer, "\nAuto-memory at %s/projects/ left intact (intentional).\n", claudeDir)
	_, _ = fmt.Fprintf(cfg.Writer, "YakOS uninstalled%s.\n", dryTag)

	if len(result.Failures) > 0 {
		_, _ = fmt.Fprintf(cfg.ErrWriter, "\nuninstall: %d non-fatal failure(s) during cleanup:\n", len(result.Failures))
		for _, f := range result.Failures {
			_, _ = fmt.Fprintf(cfg.ErrWriter, "  - %s\n", f)
		}
	}

	return result, nil
}

// resolveRoot returns (yakosRootAbs, pointerStale).
// pointerStale is true when the root cannot be verified as a readable directory.
func resolveRoot(explicitRoot, yakosPointer string, w io.Writer) (string, bool) {
	if explicitRoot != "" {
		resolved, err := filepath.EvalSymlinks(explicitRoot)
		if err != nil {
			// Use as-is; caller treats absence as best-effort.
			resolved = explicitRoot
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			resolved = explicitRoot
		}
		_, _ = fmt.Fprintf(w, "Using explicit root: %s\n", resolved)
		return resolved, false
	}

	if !fileExists(yakosPointer) {
		_, _ = fmt.Fprintf(w,
			"No %s pointer and no --root flag; proceeding with best-effort cleanup (dangling symlinks only)\n",
			yakosPointer)
		return "", true
	}

	data, err := os.ReadFile(yakosPointer) //nolint:gosec
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		_, _ = fmt.Fprintf(w,
			"WARNING: %s exists but is empty or unreadable; proceeding with best-effort cleanup\n",
			yakosPointer)
		return "", true
	}

	root := strings.TrimSpace(string(data))
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		_, _ = fmt.Fprintf(w,
			"WARNING: pointer targets %q which is missing or unreadable — cleaning what we can\n",
			root)
		return root, true
	}
	// Normalise (macOS symlinks under /var → /private/var, etc.).
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return root, false
}

// removeSymlinks processes all four subdirs, removing YakOS-owned and dangling
// symlinks. Returns a SymlinkReport.
func removeSymlinks(yakosRoot string, pointerStale bool, claudeDir string,
	dryRun bool, w, ew io.Writer, result *Result) SymlinkReport {

	var total SymlinkReport
	for _, sub := range subdirs {
		rpt := removeSymlinksIn(sub, yakosRoot, pointerStale, claudeDir, dryRun, w, ew)
		total.Removed += rpt.Removed
		total.Kept += rpt.Kept
		total.Errors += rpt.Errors
		result.Failures = append(result.Failures, rpt.errors...)
	}
	return total
}

// symlinkSubReport is the internal (unexported) report used within one subdir.
type symlinkSubReport struct {
	Removed int
	Kept    int
	Errors  int
	errors  []string
}

// removeSymlinksIn removes YakOS-owned and dangling symlinks in claudeDir/sub.
func removeSymlinksIn(sub, yakosRoot string, pointerStale bool, claudeDir string,
	dryRun bool, w, ew io.Writer) symlinkSubReport {

	var rpt symlinkSubReport
	dstRoot := filepath.Join(claudeDir, sub)

	if !dirExists(dstRoot) {
		return rpt
	}

	// Walk looking for symlinks (including nested subdirs from install).
	err := filepath.Walk(dstRoot, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return nil // skip inaccessible entries
		}
		// We want symlinks; Walk follows them by default — use Lstat instead.
		linfo, lerr := os.Lstat(path)
		if lerr != nil {
			return nil
		}
		if linfo.Mode()&os.ModeSymlink == 0 {
			return nil // not a symlink; skip
		}

		rawTarget, _ := os.Readlink(path)

		// Resolve to canonical path (nil if dangling).
		resolved, resolveErr := filepath.EvalSymlinks(path)
		dangling := resolveErr != nil || !pathExists(path)

		// Case 1: dangling symlink — always remove (YakOS-owned by assumption).
		if dangling {
			if dryRun {
				_, _ = fmt.Fprintf(w, "  [dry-run] would remove dangling symlink: %s (was: %s)\n",
					path, rawTarget)
				rpt.Removed++
			} else {
				if err2 := os.Remove(path); err2 != nil {
					note := fmt.Sprintf("could not remove dangling symlink %s: %v", path, err2)
					rpt.errors = append(rpt.errors, note)
					_, _ = fmt.Fprintf(ew, "uninstall: warning: %s\n", note)
					rpt.Errors++
				} else {
					_, _ = fmt.Fprintf(w, "Removed dangling symlink: %s (was: %s)\n", path, rawTarget)
					rpt.Removed++
				}
			}
			return nil
		}

		// Case 2: live symlink.
		remove := false
		if yakosRoot != "" {
			rootPrefix := yakosRoot + string(filepath.Separator)
			switch {
			case strings.HasPrefix(resolved, rootPrefix):
				// Resolves into the known YakOS root — remove it.
				remove = true
			case pointerStale:
				// Stale pointer with live symlink to unknown target — be conservative.
				remove = false
			default:
				// Live symlink pointing somewhere else — foreign; keep it.
				remove = false
			}
		}
		// No known root and not dangling: conservative keep.

		if remove {
			if dryRun {
				_, _ = fmt.Fprintf(w, "  [dry-run] would remove symlink: %s → %s\n", path, resolved)
				rpt.Removed++
			} else {
				if err2 := os.Remove(path); err2 != nil {
					note := fmt.Sprintf("could not remove symlink %s: %v", path, err2)
					rpt.errors = append(rpt.errors, note)
					_, _ = fmt.Fprintf(ew, "uninstall: warning: %s\n", note)
					rpt.Errors++
				} else {
					rpt.Removed++
				}
			}
		} else {
			rpt.Kept++
		}
		return nil
	})

	if err != nil {
		note := fmt.Sprintf("walk error in %s: %v", dstRoot, err)
		rpt.errors = append(rpt.errors, note)
		_, _ = fmt.Fprintf(ew, "uninstall: warning: %s\n", note)
	}

	return rpt
}

// removeLauncher removes the managed launcher symlink if it is YakOS-owned.
func removeLauncher(yakosRoot, installManifest string, dryRun bool, w, ew io.Writer) LauncherReport {
	// Read launcher path from manifest.
	launcherPath := readLauncherFromManifest(installManifest)
	if launcherPath == "" {
		return LauncherReport{Outcome: LauncherUnknown, Note: "No launcher path in manifest; skipping launcher removal"}
	}

	linfo, err := os.Lstat(launcherPath)
	if err != nil {
		// Does not exist.
		_, _ = fmt.Fprintf(w, "Launcher at %s does not exist; nothing to remove\n", launcherPath)
		return LauncherReport{Outcome: LauncherAbsent, Path: launcherPath}
	}

	if linfo.Mode()&os.ModeSymlink == 0 {
		// Real file — never remove.
		note := fmt.Sprintf("launcher at %s is not a symlink (real file); leaving alone", launcherPath)
		_, _ = fmt.Fprintf(w, "Launcher at %s is not a symlink (real file); leaving alone\n", launcherPath)
		return LauncherReport{Outcome: LauncherKept, Path: launcherPath, Note: note}
	}

	resolved, resolveErr := filepath.EvalSymlinks(launcherPath)
	dangling := resolveErr != nil || !pathExists(launcherPath)

	if dangling {
		// Dangling symlink — safe to remove (in the manifest so it is ours).
		if dryRun {
			_, _ = fmt.Fprintf(w, "  [dry-run] would remove dangling launcher symlink: %s\n", launcherPath)
		} else {
			if err2 := os.Remove(launcherPath); err2 != nil {
				note := fmt.Sprintf("could not remove dangling launcher symlink %s: %v", launcherPath, err2)
				_, _ = fmt.Fprintf(ew, "uninstall: warning: %s\n", note)
				return LauncherReport{Outcome: LauncherKept, Path: launcherPath, Note: note}
			}
			_, _ = fmt.Fprintf(w, "Removed dangling launcher symlink: %s\n", launcherPath)
		}
		return LauncherReport{Outcome: LauncherRemoved, Path: launcherPath}
	}

	// Live symlink: remove only if it resolves into the YakOS root we are
	// uninstalling, or matches a generic yakOS launcher shape.
	doRemove := false
	if yakosRoot != "" {
		rootPrefix := yakosRoot + string(filepath.Separator)
		switch {
		case strings.HasPrefix(resolved, rootPrefix):
			doRemove = true
		case strings.HasSuffix(resolved, filepath.Join("cli", "yakos")):
			// YakOS-shaped launcher from a different clone — still ours per manifest.
			doRemove = true
		}
	} else {
		// No known root — trust the manifest record; shape-match.
		if strings.HasSuffix(resolved, filepath.Join("cli", "yakos")) {
			doRemove = true
		}
	}

	if doRemove {
		if dryRun {
			_, _ = fmt.Fprintf(w, "  [dry-run] would remove managed launcher symlink: %s → %s\n",
				launcherPath, resolved)
		} else {
			if err2 := os.Remove(launcherPath); err2 != nil {
				note := fmt.Sprintf("could not remove launcher symlink %s: %v", launcherPath, err2)
				_, _ = fmt.Fprintf(ew, "uninstall: warning: %s\n", note)
				return LauncherReport{Outcome: LauncherKept, Path: launcherPath, Note: note}
			}
			_, _ = fmt.Fprintf(w, "Removed managed launcher symlink: %s\n", launcherPath)
		}
		return LauncherReport{Outcome: LauncherRemoved, Path: launcherPath}
	}

	// Foreign launcher — leave alone.
	note := fmt.Sprintf("launcher %s → %s is not YakOS-owned; leaving alone", launcherPath, resolved)
	_, _ = fmt.Fprintf(w, "Launcher %s → %s is not YakOS-owned; leaving alone\n", launcherPath, resolved)
	return LauncherReport{Outcome: LauncherKept, Path: launcherPath, Note: note}
}

// handleSettings manages settings.json and the created-marker.
func handleSettings(settingsFile, createdMarker, claudeDir string,
	restoreSettings, dryRun bool, w io.Writer) SettingsReport {

	// If we created settings.json (marker present), remove it.
	if fileExists(createdMarker) {
		if fileExists(settingsFile) {
			if dryRun {
				_, _ = fmt.Fprintf(w, "  [dry-run] would remove settings.json (we created it): %s\n", settingsFile)
			} else {
				if err := os.Remove(settingsFile); err == nil {
					_, _ = fmt.Fprintf(w, "Removed %s (we created it during install)\n", settingsFile)
				}
			}
		}
		if !dryRun {
			_ = os.Remove(createdMarker)
		}
		return SettingsReport{
			Outcome:      SettingsRemoved,
			SettingsFile: settingsFile,
		}
	}

	// If --restore-settings, find the most recent backup and restore.
	if restoreSettings {
		backups := findSettingsBackups(claudeDir)
		if len(backups) > 0 {
			latest := backups[len(backups)-1]
			if dryRun {
				_, _ = fmt.Fprintf(w, "  [dry-run] would restore settings.json from %s\n", latest)
			} else {
				data, err := os.ReadFile(latest) //nolint:gosec
				if err == nil {
					if err2 := atomicWrite(settingsFile, data); err2 == nil {
						_, _ = fmt.Fprintf(w, "Restored %s from %s\n", settingsFile, latest)
					}
				}
			}
			return SettingsReport{
				Outcome:      SettingsRestored,
				SettingsFile: settingsFile,
				RestoredFrom: latest,
			}
		}
		_, _ = fmt.Fprintf(w,
			"--restore-settings requested but no settings.json.yakos-bak-* found in %s\n", claudeDir)
		return SettingsReport{Outcome: SettingsKept, SettingsFile: settingsFile}
	}

	// List available backups for the operator.
	backups := findSettingsBackups(claudeDir)
	if len(backups) > 0 {
		_, _ = fmt.Fprintf(w,
			"YakOS settings backups available (use --restore-settings to apply the most recent):\n")
		for _, b := range backups {
			_, _ = fmt.Fprintf(w, "  %s\n", b)
		}
	}

	if !fileExists(settingsFile) {
		return SettingsReport{Outcome: SettingsAbsent, SettingsFile: settingsFile}
	}
	return SettingsReport{
		Outcome:          SettingsKept,
		SettingsFile:     settingsFile,
		AvailableBackups: backups,
	}
}

// PrintHelp writes the help text for `yakos uninstall` to w. The text mirrors
// the bash uninstall.sh --help output exactly (load-bearing phrases).
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos uninstall — remove YakOS-managed symlinks and pointer

Removes per-file symlinks under ~/.claude/{agents,skills,rules,playbooks}/
that point into the YakOS repo. Files YakOS didn't create are left alone.

Removes the managed launcher symlink at the path recorded in
~/.yakos-state/install-manifest (only if it still resolves into the yakOS
root being uninstalled — foreign binaries and foreign symlinks are never
touched).

Stale-pointer robustness: if ~/.yakos points at a path that is missing or
unreadable (moved install, root-owned clone), a warning is printed and
cleanup proceeds for what is locally accessible. Dangling symlinks under
~/.claude/{agents,skills,rules,playbooks}/ are removed as yakOS-owned.

Auto-memory at ~/.claude/projects/ is NEVER touched, even with any flag.

Usage: yakos uninstall [--restore-settings] [--root <path>] [--dry-run]

Options:
    --restore-settings   If a YakOS settings.json backup exists, restore
                         from the most recent one. Default: leave
                         settings.json alone, list backups.
    --root <path>        Use <path> as YAKOS_ROOT instead of reading
                         ~/.yakos. Useful for cleaning a leftover install
                         at an alternate location (e.g. a prior root-owned
                         install). Combine with 'sudo' if the path is not
                         accessible as the current user.
    --dry-run            Report what would change without writing anything.
    --help, -h           Print this help

Removes:
    YakOS-owned symlinks under ~/.claude/{agents,skills,rules,playbooks}/
    Dangling symlinks under the above directories (treated as yakOS-owned)
    ~/.claude/settings.json    (only if YakOS created it; marker file present)
    ~/.yakos                   (the pointer file)
    ~/.yakos-state/install-manifest (the launcher record)
    The managed launcher symlink recorded in the manifest (if yakOS-owned)

Never removes:
    ~/.claude/projects/        (auto-memory; always preserved)
    Files or symlinks that don't point into the YakOS repo
    Foreign yakos binaries or symlinks at the launcher path
`)
}

// ---- helpers -----------------------------------------------------------------

// readLauncherFromManifest reads the launcher path from install-manifest.
// Returns "" when the manifest is absent or lacks a launcher= entry.
func readLauncherFromManifest(manifestPath string) string {
	data, err := os.ReadFile(manifestPath) //nolint:gosec
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "launcher=") {
			return strings.TrimPrefix(line, "launcher=")
		}
	}
	return ""
}

// findSettingsBackups returns sorted list of settings.json.yakos-bak-* files.
func findSettingsBackups(claudeDir string) []string {
	entries, err := os.ReadDir(claudeDir)
	if err != nil {
		return nil
	}
	var backups []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "settings.json.yakos-bak-") {
			backups = append(backups, filepath.Join(claudeDir, e.Name()))
		}
	}
	sort.Strings(backups)
	return backups
}

// atomicWrite writes data to path atomically via temp file + os.Rename.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".yakos-uninstall-tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		if writeErr != nil {
			return fmt.Errorf("writing temp file: %w", writeErr)
		}
		return fmt.Errorf("closing temp file: %w", closeErr)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming %s → %s: %w", tmpPath, path, err)
	}
	return nil
}

// fileExists returns true when path exists and is not a directory.
// It uses Lstat so it does not follow symlinks (for pointer file, marker, etc.).
func fileExists(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && !fi.IsDir()
}

// dirExists returns true when path exists and is a directory.
func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// pathExists returns true when path exists (follows symlinks — for checking
// whether a symlink target is reachable).
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
