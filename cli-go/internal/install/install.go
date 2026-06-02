// Package install is the Go port of cli/lib/install.sh.
//
// It implements `yakos install [--force] [--dry-run]`:
//
//  1. Preflight: if $HOME/.claude/settings.json exists, validate it is valid JSON.
//  2. Write $HOME/.yakos with the absolute path to YAKOS_ROOT.
//  3. Managed launcher symlink at $HOME/.local/bin/yakos → $YAKOS_ROOT/cli/yakos.
//     - Created if absent.
//     - Refreshed if existing symlink resolves into this or any yakOS-shaped root.
//     - Left alone (with warning) if existing entry is a real file or points
//       outside any known yakOS root.
//     - Launcher path recorded in $HOME/.yakos-state/install-manifest.
//  4. Per-file symlinks from $HOME/.claude/{agents,skills,rules,playbooks}/
//     into $YAKOS_ROOT/lib/{agents,skills,rules,playbooks}/.
//     - Existing user files preserved (never overwrite a real file).
//     - YakOS-owned symlinks (pointing into YAKOS_ROOT) are refreshed idempotently.
//  5. Merge CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 into $HOME/.claude/settings.json.
//     - Create the file (with marker) if absent.
//     - Backup + jq-style merge if present.
//
// --dry-run: all validation runs; nothing is written to disk.
// --force:   YakOS-owned symlinks are always refreshed even when already correct.
//
// Atomic writes throughout (temp-file rename). No file locking in Phase 1
// (per Decision Q8: locking deferred to Phase 2 daemon).
//
// NOTE: unlike refresh, install targets $HOME global paths, not per-project paths.
// Reuse points from refresh package:
//   - refresh.syncAgents      → used directly for ~/.claude/agents/ symlinks
//   - refresh.MergeSettingsFiles (project settings) is NOT reused here because
//     install writes the simpler global env-var merge, not the hook-registration merge.
//
// Reuse via call: SyncAgents is re-exported from refresh; the install package calls
// refresh.SyncAgents so the agent-symlink logic is never duplicated.
package install

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// subdirs lists the four ~/.claude subdirectories that receive per-file symlinks.
var subdirs = []string{"agents", "skills", "rules", "playbooks"}

// envKey is the settings.json env var key that install guarantees is present.
const envKey = "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"

// envVal is the value for envKey.
const envVal = "1"

// Config controls an install run.
type Config struct {
	// YakosRoot is the framework repo root ($YAKOS_ROOT). Must be set.
	YakosRoot string

	// HomeDir overrides $HOME. If empty, os.Getenv("HOME") is used.
	HomeDir string

	// Force, when true, refreshes existing YakOS-owned symlinks even when
	// they already point to the correct target.
	Force bool

	// DryRun, when true, reports what would change without writing anything.
	DryRun bool

	// Writer is the output destination. Defaults to os.Stdout.
	Writer io.Writer

	// ErrWriter is the error destination. Defaults to os.Stderr.
	ErrWriter io.Writer
}

// SymlinkReport counts per-file symlink outcomes.
type SymlinkReport struct {
	Created    int // new symlinks created
	Refreshed  int // existing YakOS-owned symlinks updated
	Skipped    int // files left alone (foreign symlink, real file, already correct + no --force)
}

// LauncherOutcome describes what happened to the launcher symlink.
type LauncherOutcome string

const (
	LauncherCreated  LauncherOutcome = "created"
	LauncherRefreshed LauncherOutcome = "refreshed"
	LauncherSkipped  LauncherOutcome = "skipped" // foreign or real file
)

// LauncherReport describes the outcome for the managed launcher.
type LauncherReport struct {
	Outcome LauncherOutcome
	Path    string   // absolute path of the launcher entry
	Target  string   // symlink target (empty when Outcome==skipped)
	Warning string   // non-empty when Outcome==skipped and a warning was emitted
}

// SettingsReport describes what happened to ~/.claude/settings.json.
type SettingsReport struct {
	AlreadySet bool   // env var was already at the desired value
	Created    bool   // new file created (marker written)
	Merged     bool   // existing file was backed up and merged
	SkipReason string // non-empty when no action was taken (dry-run or already-set)
}

// Result is the complete outcome of an install run.
type Result struct {
	YakosPointer  string        // path to $HOME/.yakos pointer file
	Launcher      LauncherReport
	Symlinks      SymlinkReport
	Settings      SettingsReport
	DryRun        bool
}

// Run executes the install. Returns a structured Result.
// On error the function returns as early as possible with a descriptive message;
// no state is left in a torn half-written form (atomic writes throughout).
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

	// Resolve YAKOS_ROOT to an absolute path.
	yakosRootAbs, err := filepath.Abs(cfg.YakosRoot)
	if err != nil {
		return nil, fmt.Errorf("install: resolving YAKOS_ROOT %q: %w", cfg.YakosRoot, err)
	}
	// Verify YAKOS_ROOT exists.
	if fi, err := os.Stat(yakosRootAbs); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("install: YAKOS_ROOT %q is not a directory", yakosRootAbs)
	}
	// Resolve symlinks in yakosRootAbs to match what EvalSymlinks returns for paths
	// inside it. On macOS, /var/folders/... resolves to /private/var/folders/...;
	// without this normalisation the HasPrefix ownership check fails.
	if resolved, err := filepath.EvalSymlinks(yakosRootAbs); err == nil {
		yakosRootAbs = resolved
	}

	claudeDir := filepath.Join(home, ".claude")
	yakosPointer := filepath.Join(home, ".yakos")
	settingsFile := filepath.Join(claudeDir, "settings.json")
	createdMarker := filepath.Join(claudeDir, ".yakos-created-settings")
	yakosStateDir := filepath.Join(home, ".yakos-state")
	installManifest := filepath.Join(yakosStateDir, "install-manifest")
	launcherDir := filepath.Join(home, ".local", "bin")
	launcherPath := filepath.Join(launcherDir, "yakos")
	launcherTarget := filepath.Join(yakosRootAbs, "cli", "yakos")

	dryTag := ""
	if cfg.DryRun {
		dryTag = " [DRY RUN]"
	}
	_, _ = fmt.Fprintf(cfg.Writer, "yakos install%s\n\n", dryTag)
	_, _ = fmt.Fprintf(cfg.Writer, "Installing YakOS from %s into %s\n", yakosRootAbs, claudeDir)

	// ---- 0. preflight: validate existing settings.json -------------------------

	if fi, err := os.Stat(settingsFile); err == nil && !fi.IsDir() {
		data, err := os.ReadFile(settingsFile) //nolint:gosec
		if err != nil {
			return nil, fmt.Errorf("install: reading settings.json: %w", err)
		}
		if !isValidJSON(data) {
			return nil, fmt.Errorf("install: %s is not valid JSON — fix or remove it and re-run", settingsFile)
		}
	}

	result := &Result{DryRun: cfg.DryRun}

	// ---- 1. ~/.yakos pointer file -----------------------------------------------

	if cfg.DryRun {
		_, _ = fmt.Fprintf(cfg.Writer, "  [dry-run] would write pointer: %s → %s\n", yakosPointer, yakosRootAbs)
	} else {
		if err := os.MkdirAll(claudeDir, 0755); err != nil { //nolint:gosec
			return nil, fmt.Errorf("install: mkdir %s: %w", claudeDir, err)
		}
		if err := os.MkdirAll(yakosStateDir, 0755); err != nil { //nolint:gosec
			return nil, fmt.Errorf("install: mkdir %s: %w", yakosStateDir, err)
		}
		if err := atomicWriteString(yakosPointer, yakosRootAbs+"\n"); err != nil {
			return nil, fmt.Errorf("install: writing pointer file %s: %w", yakosPointer, err)
		}
		_, _ = fmt.Fprintf(cfg.Writer, "  Wrote pointer: %s\n", yakosPointer)
	}
	result.YakosPointer = yakosPointer

	// ---- 1a. managed launcher symlink ------------------------------------------

	lrpt := manageLauncher(launcherDir, launcherPath, launcherTarget, yakosRootAbs, cfg.DryRun, cfg.Writer, cfg.ErrWriter)
	result.Launcher = lrpt

	if !cfg.DryRun && lrpt.Outcome != LauncherSkipped {
		// Record launcher in manifest so uninstall removes it.
		manifest := fmt.Sprintf("launcher=%s\n", launcherPath)
		if err := atomicWriteString(installManifest, manifest); err != nil {
			// Non-fatal: warn but continue.
			_, _ = fmt.Fprintf(cfg.ErrWriter, "install: warning: could not write install-manifest: %v\n", err)
		} else {
			_, _ = fmt.Fprintf(cfg.Writer, "  Recorded launcher in %s\n", installManifest)
		}
		// PATH guidance if launcher dir is not on PATH.
		if !dirOnPath(launcherDir) {
			_, _ = fmt.Fprintf(cfg.Writer, "\nNOTE: %s is not on your PATH.\n", launcherDir)
			_, _ = fmt.Fprintf(cfg.Writer, "Add this line to your shell profile (~/.bashrc, ~/.zshrc, etc.):\n")
			_, _ = fmt.Fprintf(cfg.Writer, "    export PATH=\"%s:$PATH\"\n\n", launcherDir)
		}
	}

	// ---- 2. per-file symlinks ---------------------------------------------------

	srpt, err := linkSubdirs(yakosRootAbs, claudeDir, cfg.Force, cfg.DryRun, cfg.Writer, cfg.ErrWriter)
	if err != nil {
		return nil, fmt.Errorf("install: per-file symlinks: %w", err)
	}
	result.Symlinks = srpt
	_, _ = fmt.Fprintf(cfg.Writer, "  Symlinks: %d created, %d refreshed, %d skipped\n",
		srpt.Created, srpt.Refreshed, srpt.Skipped)

	// ---- 3. settings.json safe merge -------------------------------------------

	stRpt, err := mergeGlobalSettings(settingsFile, createdMarker, cfg.DryRun, cfg.Writer)
	if err != nil {
		return nil, fmt.Errorf("install: settings.json merge: %w", err)
	}
	result.Settings = stRpt

	// ---- summary ---------------------------------------------------------------

	launcherNote := "not managed (foreign or real-file collision — see warnings above)"
	if lrpt.Outcome != LauncherSkipped {
		launcherNote = fmt.Sprintf("%s → %s", lrpt.Path, lrpt.Target)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "\nYakOS install complete%s.\n", dryTag)
	_, _ = fmt.Fprintf(cfg.Writer, "    Pointer:       %s → %s\n", yakosPointer, yakosRootAbs)
	_, _ = fmt.Fprintf(cfg.Writer, "    Launcher:      %s\n", launcherNote)
	_, _ = fmt.Fprintf(cfg.Writer, "    Manifest:      %s\n", installManifest)
	_, _ = fmt.Fprintf(cfg.Writer, "    Symlinks:      %d created, %d refreshed, %d skipped\n",
		srpt.Created, srpt.Refreshed, srpt.Skipped)
	_, _ = fmt.Fprintf(cfg.Writer, "    Settings:      %s\n", settingsFile)
	_, _ = fmt.Fprintf(cfg.Writer, "\nNext steps:\n")
	_, _ = fmt.Fprintf(cfg.Writer, "    yakos doctor                              Verify everything resolved\n")
	_, _ = fmt.Fprintf(cfg.Writer, "    yakos init <name> --project <path>        Bootstrap a project on top of YakOS\n\n")
	_, _ = fmt.Fprintf(cfg.Writer, "Auto-memory at %s/projects/ was not touched.\n", claudeDir)

	return result, nil
}

// manageLauncher handles the launcher symlink lifecycle. It is side-effect-free
// when dryRun is true.
func manageLauncher(launcherDir, launcherPath, launcherTarget, yakosRootAbs string, dryRun bool, w, ew io.Writer) LauncherReport {
	if !dryRun {
		if err := os.MkdirAll(launcherDir, 0755); err != nil { //nolint:gosec
			_, _ = fmt.Fprintf(ew, "install: warning: could not create launcher dir %s: %v\n", launcherDir, err)
		}
	}

	linfo, err := os.Lstat(launcherPath)
	if err != nil {
		// Nothing at launcherPath — create it.
		if dryRun {
			_, _ = fmt.Fprintf(w, "  [dry-run] would create launcher symlink: %s → %s\n", launcherPath, launcherTarget)
			return LauncherReport{Outcome: LauncherCreated, Path: launcherPath, Target: launcherTarget}
		}
		if err2 := os.Symlink(launcherTarget, launcherPath); err2 != nil {
			_, _ = fmt.Fprintf(ew, "install: warning: could not create launcher symlink: %v\n", err2)
			return LauncherReport{Outcome: LauncherSkipped, Path: launcherPath, Warning: err2.Error()}
		}
		_, _ = fmt.Fprintf(w, "  Created launcher symlink: %s → %s\n", launcherPath, launcherTarget)
		return LauncherReport{Outcome: LauncherCreated, Path: launcherPath, Target: launcherTarget}
	}

	if linfo.Mode()&os.ModeSymlink != 0 {
		// It's a symlink — check where it resolves.
		existing, err := filepath.EvalSymlinks(launcherPath)
		if err != nil {
			// Dangling symlink — treat as absent, refresh.
			existing = ""
		}

		owned := false
		switch {
		case strings.HasPrefix(existing, yakosRootAbs+string(filepath.Separator)):
			// Owned by this exact root.
			owned = true
		case strings.HasSuffix(existing, filepath.Join("cli", "yakos")):
			// Looks like a yakOS launcher from a different clone location.
			owned = true
		}

		if owned {
			if dryRun {
				_, _ = fmt.Fprintf(w, "  [dry-run] would refresh launcher symlink: %s → %s (was %s)\n", launcherPath, launcherTarget, existing)
				return LauncherReport{Outcome: LauncherRefreshed, Path: launcherPath, Target: launcherTarget}
			}
			_ = os.Remove(launcherPath)
			if err := os.Symlink(launcherTarget, launcherPath); err != nil {
				_, _ = fmt.Fprintf(ew, "install: warning: could not refresh launcher symlink: %v\n", err)
				return LauncherReport{Outcome: LauncherSkipped, Path: launcherPath, Warning: err.Error()}
			}
			_, _ = fmt.Fprintf(w, "  Refreshed launcher symlink: %s → %s\n", launcherPath, launcherTarget)
			return LauncherReport{Outcome: LauncherRefreshed, Path: launcherPath, Target: launcherTarget}
		}

		// Symlink points outside any known yakOS root — leave alone.
		msg := fmt.Sprintf("WARNING: %s is a symlink pointing to %q (not yakOS-owned). Leaving it alone. "+
			"To use this yakOS install, add %s/cli to your PATH or remove/rename the existing launcher.",
			launcherPath, existing, yakosRootAbs)
		_, _ = fmt.Fprintf(ew, "install: %s\n", msg)
		return LauncherReport{Outcome: LauncherSkipped, Path: launcherPath, Warning: msg}
	}

	// Real file — never touch it.
	msg := fmt.Sprintf("WARNING: %s exists and is not a symlink (real file). Leaving it alone. "+
		"To use this yakOS install, add %s/cli to your PATH or remove/rename the existing binary.",
		launcherPath, yakosRootAbs)
	_, _ = fmt.Fprintf(ew, "install: %s\n", msg)
	return LauncherReport{Outcome: LauncherSkipped, Path: launcherPath, Warning: msg}
}

// linkSubdirs creates per-file symlinks for all four subdirs.
func linkSubdirs(yakosRootAbs, claudeDir string, force, dryRun bool, w, ew io.Writer) (SymlinkReport, error) {
	var total SymlinkReport
	for _, sub := range subdirs {
		rpt, err := linkFilesIn(sub, yakosRootAbs, claudeDir, force, dryRun, w, ew)
		if err != nil {
			return total, err
		}
		total.Created += rpt.Created
		total.Refreshed += rpt.Refreshed
		total.Skipped += rpt.Skipped
	}
	return total, nil
}

// linkFilesIn handles one subdir (e.g. "agents").
// It mirrors the bash link_files_in() logic: per-file recursive symlinks from
// yakosRootAbs/lib/<sub>/ into claudeDir/<sub>/.
func linkFilesIn(sub, yakosRootAbs, claudeDir string, force, dryRun bool, w, ew io.Writer) (SymlinkReport, error) {
	var rpt SymlinkReport
	srcRoot := filepath.Join(yakosRootAbs, "lib", sub)
	dstRoot := filepath.Join(claudeDir, sub)

	if _, err := os.Stat(srcRoot); os.IsNotExist(err) {
		return rpt, nil
	}

	if !dryRun {
		if err := os.MkdirAll(dstRoot, 0755); err != nil { //nolint:gosec
			return rpt, fmt.Errorf("mkdir %s: %w", dstRoot, err)
		}
	}

	err := filepath.Walk(srcRoot, func(srcPath string, info os.FileInfo, werr error) error {
		if werr != nil {
			return nil // skip inaccessible entries
		}
		if info.IsDir() {
			return nil
		}
		base := filepath.Base(srcPath)
		if base == ".gitkeep" {
			return nil
		}

		rel, err := filepath.Rel(srcRoot, srcPath)
		if err != nil {
			return nil
		}
		dstPath := filepath.Join(dstRoot, rel)

		if !dryRun {
			if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil { //nolint:gosec
				return nil
			}
		}

		linfo, err := os.Lstat(dstPath)
		if err != nil {
			// Does not exist — create symlink.
			if dryRun {
				_, _ = fmt.Fprintf(w, "  [dry-run] would create symlink %s/%s\n", sub, rel)
			} else {
				if err := os.Symlink(srcPath, dstPath); err != nil {
					_, _ = fmt.Fprintf(ew, "install: skip: could not create symlink %s: %v\n", dstPath, err)
					rpt.Skipped++
					return nil
				}
			}
			rpt.Created++
			return nil
		}

		if linfo.Mode()&os.ModeSymlink != 0 {
			// It's a symlink — check ownership.
			existing, err := filepath.EvalSymlinks(dstPath)
			if err != nil {
				// Dangling symlink owned by us — refresh.
				existing = ""
			}

			yakosOwned := existing == "" || strings.HasPrefix(existing, yakosRootAbs+string(filepath.Separator))

			if yakosOwned {
				if force || existing != srcPath {
					// Refresh the symlink.
					if dryRun {
						_, _ = fmt.Fprintf(w, "  [dry-run] would refresh symlink %s/%s\n", sub, rel)
						rpt.Refreshed++
					} else {
						_ = os.Remove(dstPath)
						if err := os.Symlink(srcPath, dstPath); err != nil {
							_, _ = fmt.Fprintf(ew, "install: skip: could not refresh symlink %s: %v\n", dstPath, err)
							rpt.Skipped++
						} else {
							rpt.Refreshed++
						}
					}
				} else {
					// Already correct and --force not set.
					rpt.Skipped++
				}
			} else {
				// Symlink points outside YakOS — leave alone.
				_, _ = fmt.Fprintf(ew, "install: skip: %s is a symlink pointing outside YakOS (%s); leaving alone\n",
					dstPath, existing)
				rpt.Skipped++
			}
			return nil
		}

		// Real file — never overwrite.
		_, _ = fmt.Fprintf(ew, "install: skip: %s already exists and is not a symlink (rename/remove manually)\n", dstPath)
		rpt.Skipped++
		return nil
	})

	return rpt, err
}

// mergeGlobalSettings merges CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 into
// ~/.claude/settings.json. Mirrors bash merge_settings().
//
// If the file does not exist: creates it with just the env block and writes
// a marker file (.yakos-created-settings) so uninstall knows we own it.
// If the file exists and already has the value: no-op.
// If the file exists and lacks the value: backup + merge.
func mergeGlobalSettings(settingsFile, createdMarker string, dryRun bool, w io.Writer) (SettingsReport, error) {
	var rpt SettingsReport

	data, err := os.ReadFile(settingsFile) //nolint:gosec
	if os.IsNotExist(err) {
		// Create the file with the minimal env block.
		content := fmt.Sprintf("{\n  \"env\": {\n    %q: %q\n  }\n}\n", envKey, envVal)
		if dryRun {
			_, _ = fmt.Fprintf(w, "  [dry-run] would create settings.json with env.%s=%s\n", envKey, envVal)
			rpt.Created = true
			return rpt, nil
		}
		if err2 := os.MkdirAll(filepath.Dir(settingsFile), 0755); err2 != nil { //nolint:gosec
			return rpt, fmt.Errorf("mkdir for settings.json: %w", err2)
		}
		if err2 := atomicWriteString(settingsFile, content); err2 != nil {
			return rpt, fmt.Errorf("creating settings.json: %w", err2)
		}
		// Touch the created-marker.
		if err2 := os.WriteFile(createdMarker, []byte{}, 0644); err2 != nil { //nolint:gosec
			// Non-fatal — warn but proceed.
			_, _ = fmt.Fprintf(w, "  warning: could not write created-marker: %v\n", err2)
		}
		_, _ = fmt.Fprintf(w, "  Created settings.json (marker: %s)\n", createdMarker)
		rpt.Created = true
		return rpt, nil
	}
	if err != nil {
		return rpt, fmt.Errorf("reading settings.json: %w", err)
	}

	// File exists — parse and check current value.
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return rpt, fmt.Errorf("settings.json is not valid JSON: %w", err)
	}

	currentVal := ""
	if envMap, ok := parsed["env"].(map[string]any); ok {
		if v, ok := envMap[envKey].(string); ok {
			currentVal = v
		}
	}

	if currentVal == envVal {
		_, _ = fmt.Fprintf(w, "  settings.json already has env.%s=%s (no change)\n", envKey, envVal)
		rpt.AlreadySet = true
		rpt.SkipReason = "already set"
		return rpt, nil
	}

	// Merge the env key.
	if dryRun {
		_, _ = fmt.Fprintf(w, "  [dry-run] would merge env.%s=%s into settings.json\n", envKey, envVal)
		rpt.Merged = true
		return rpt, nil
	}

	// Deep-merge: set env.<key> = <val>, preserving all other keys.
	envMap, _ := parsed["env"].(map[string]any)
	if envMap == nil {
		envMap = make(map[string]any)
	}
	envMap[envKey] = envVal
	parsed["env"] = envMap

	out, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return rpt, fmt.Errorf("marshalling merged settings.json: %w", err)
	}
	out = append(out, '\n')

	if err := atomicWrite(settingsFile, out); err != nil {
		return rpt, fmt.Errorf("writing merged settings.json: %w", err)
	}

	_, _ = fmt.Fprintf(w, "  Merged env.%s into settings.json\n", envKey)
	rpt.Merged = true
	return rpt, nil
}

// PrintHelp writes the help text for `yakos install` to w. The text mirrors
// the bash install.sh --help output exactly (load-bearing phrases).
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos install — install YakOS into ~/.claude

Creates per-file symlinks under ~/.claude/{agents,skills,rules,playbooks}/
that point into this repo's lib/. Existing files in those directories are
preserved (use --force to overwrite YakOS-owned symlinks).

Creates a managed launcher symlink at ~/.local/bin/yakos → this repo's
cli/yakos. The symlink is only created (or refreshed) when there is no
existing 'yakos' entry at that path or it already points into this repo.
Foreign binaries/symlinks are left alone with a warning. Records the
launcher path in ~/.yakos-state/install-manifest for clean uninstall.

Safely merges the experimental-agent-teams env var into
~/.claude/settings.json. If the file already exists, it is validated as
JSON before merge.

Usage: yakos install [--force] [--dry-run]

Options:
    --force      Overwrite existing symlinks if they already point into
                 this YakOS repo. Never overwrites non-symlink files or
                 symlinks pointing outside this repo.
    --dry-run    Report what would change without writing anything.
    --help, -h   Print this help

Never touches:
    ~/.claude/projects/    (auto-memory)
    Unknown keys in settings.json
    Foreign yakos binaries or symlinks at ~/.local/bin/yakos
`)
}

// ---- helpers ----------------------------------------------------------------

// atomicWriteString writes content to path atomically via temp-rename.
func atomicWriteString(path, content string) error {
	return atomicWrite(path, []byte(content))
}

// atomicWrite writes data to path atomically via temp file + os.Rename.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".yakos-install-tmp-*")
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

// isValidJSON returns true when data is non-empty parseable JSON.
func isValidJSON(data []byte) bool {
	var v any
	return json.Unmarshal(data, &v) == nil
}

// dirOnPath returns true when dir appears in the PATH environment variable.
func dirOnPath(dir string) bool {
	path := os.Getenv("PATH")
	for _, p := range filepath.SplitList(path) {
		if p == dir {
			return true
		}
	}
	return false
}
