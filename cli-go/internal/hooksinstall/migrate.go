package hooksinstall

// migrate.go — implements `yakos hooks migrate`.
//
// For each lib/hooks/<name>.sh AND <project>/scripts/hooks/<name>.sh that
// differs from the framework baseline (detected by SHA-256 comparison), the
// migrate command generates a lib/hooks/<name>.star stub at the OPERATOR'S
// project directory — never in the framework itself.
//
// Stub format matches the Phase 3 design spec (go-port-phase3-hook-mitigation.md §5):
// a commented scaffold explaining the three tiers and an empty on_event function.
//
// Modes:
//   --dry-run   lists what WOULD be created; no writes.
//   default     actually writes .star stubs.
//
// Detection:
//   - Byte-identical to framework baseline → skip (no operator customization).
//   - .star already exists at the target path → skip (don't overwrite).
//   - Otherwise → scaffold.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MigrateConfig holds the options for a `yakos hooks migrate` invocation.
type MigrateConfig struct {
	// FrameworkHooksDir is the absolute path to lib/hooks/ in the yakOS
	// framework repo. The baseline .sh files live here.
	FrameworkHooksDir string

	// ProjectDir is the absolute path to the operator's project root.
	// Operator-customized hooks are looked up at <ProjectDir>/scripts/hooks/.
	// Generated .star stubs are written to <ProjectDir>/lib/hooks/.
	ProjectDir string

	// DryRun, when true, lists actions without writing any files.
	DryRun bool

	// Writer receives informational output (stub paths, skip reasons).
	Writer io.Writer

	// ErrWriter receives error/warning output.
	ErrWriter io.Writer
}

// MigrateAction describes one hook that migrate would act on.
type MigrateAction struct {
	// HookName is the canonical hook name (e.g. "cycle-counter").
	HookName string

	// SourcePath is the path to the operator's .sh file.
	SourcePath string

	// TargetPath is the path where the .star stub would be written.
	TargetPath string

	// Skipped is true when the action was not taken.
	Skipped bool

	// SkipReason explains why the action was skipped.
	SkipReason string

	// Written is true when the stub was actually written (dry-run = false).
	Written bool
}

// MigrateResult summarises what the migrate run did.
type MigrateResult struct {
	Actions  []MigrateAction
	Written  int
	Skipped  int
	DryRun   bool
}

// RunMigrate executes `yakos hooks migrate` per cfg.
func RunMigrate(cfg MigrateConfig) (MigrateResult, error) {
	if cfg.Writer == nil {
		cfg.Writer = io.Discard
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = io.Discard
	}
	if cfg.FrameworkHooksDir == "" {
		return MigrateResult{}, fmt.Errorf("hooks migrate: FrameworkHooksDir required")
	}
	if cfg.ProjectDir == "" {
		return MigrateResult{}, fmt.Errorf("hooks migrate: ProjectDir required")
	}

	result := MigrateResult{DryRun: cfg.DryRun}

	// Collect framework baseline hooks.
	baselineHooks, err := collectBaselineHooks(cfg.FrameworkHooksDir)
	if err != nil {
		return result, fmt.Errorf("hooks migrate: read framework hooks: %w", err)
	}
	if len(baselineHooks) == 0 {
		fmt.Fprintln(cfg.Writer, "hooks migrate: no .sh files found in framework hooks dir; nothing to do")
		return result, nil
	}

	// Operator's project hooks dir (e.g. <project>/scripts/hooks/).
	projectHooksDir := filepath.Join(cfg.ProjectDir, "scripts", "hooks")
	// Output dir for .star stubs (e.g. <project>/lib/hooks/).
	outputDir := filepath.Join(cfg.ProjectDir, "lib", "hooks")

	if cfg.DryRun {
		fmt.Fprintf(cfg.Writer, "hooks migrate --dry-run: project=%s\n", cfg.ProjectDir)
	} else {
		fmt.Fprintf(cfg.Writer, "hooks migrate: project=%s\n", cfg.ProjectDir)
	}

	for name, baselineSHA := range baselineHooks {
		action := MigrateAction{
			HookName:   name,
			SourcePath: filepath.Join(projectHooksDir, name+".sh"),
			TargetPath: filepath.Join(outputDir, name+".star"),
		}

		// Check 1: does the operator's project have this hook at all?
		operatorSH := filepath.Join(projectHooksDir, name+".sh")
		if _, statErr := os.Stat(operatorSH); os.IsNotExist(statErr) {
			// No operator copy → no customization → skip.
			action.Skipped = true
			action.SkipReason = "no operator copy found at " + operatorSH
			result.Actions = append(result.Actions, action)
			result.Skipped++
			fmt.Fprintf(cfg.Writer, "  skip %-30s — no operator copy\n", name+".sh")
			continue
		}

		// Check 2: is the operator's copy byte-identical to the baseline?
		operatorSHA, shaErr := fileSHA256(operatorSH)
		if shaErr != nil {
			fmt.Fprintf(cfg.ErrWriter, "hooks migrate: sha256(%s): %v\n", operatorSH, shaErr)
			action.Skipped = true
			action.SkipReason = fmt.Sprintf("sha256 error: %v", shaErr)
			result.Actions = append(result.Actions, action)
			result.Skipped++
			continue
		}
		if operatorSHA == baselineSHA {
			action.Skipped = true
			action.SkipReason = "operator copy is byte-identical to framework baseline (no customization)"
			result.Actions = append(result.Actions, action)
			result.Skipped++
			fmt.Fprintf(cfg.Writer, "  skip %-30s — identical to baseline\n", name+".sh")
			continue
		}

		// Check 3: does a .star stub already exist?
		targetPath := filepath.Join(outputDir, name+".star")
		if _, statErr := os.Stat(targetPath); statErr == nil {
			action.Skipped = true
			action.SkipReason = "stub already exists at " + targetPath
			result.Actions = append(result.Actions, action)
			result.Skipped++
			fmt.Fprintf(cfg.Writer, "  skip %-30s — .star already exists\n", name+".star")
			continue
		}

		// Action: write the stub.
		stub := buildStarStub(name)
		if cfg.DryRun {
			fmt.Fprintf(cfg.Writer, "  would create %s\n", targetPath)
			action.Skipped = false
		} else {
			if mkErr := os.MkdirAll(outputDir, 0755); mkErr != nil { //nolint:gosec
				return result, fmt.Errorf("hooks migrate: mkdir %s: %w", outputDir, mkErr)
			}
			if writeErr := os.WriteFile(targetPath, []byte(stub), 0644); writeErr != nil { //nolint:gosec
				fmt.Fprintf(cfg.ErrWriter, "hooks migrate: write %s: %v\n", targetPath, writeErr)
				action.Skipped = true
				action.SkipReason = fmt.Sprintf("write error: %v", writeErr)
				result.Actions = append(result.Actions, action)
				result.Skipped++
				continue
			}
			fmt.Fprintf(cfg.Writer, "  created  %s\n", targetPath)
			action.Written = true
			result.Written++
		}
		result.Actions = append(result.Actions, action)
	}

	if cfg.DryRun {
		fmt.Fprintf(cfg.Writer, "\nhooks migrate --dry-run: would create %d stub(s), skip %d\n",
			result.Written+countWouldCreate(result.Actions), result.Skipped)
	} else {
		fmt.Fprintf(cfg.Writer, "\nhooks migrate: created %d stub(s), skipped %d\n",
			result.Written, result.Skipped)
	}
	return result, nil
}

// countWouldCreate counts actions that would be created in dry-run mode.
func countWouldCreate(actions []MigrateAction) int {
	n := 0
	for _, a := range actions {
		if !a.Skipped {
			n++
		}
	}
	return n
}

// ---- stub generation ---------------------------------------------------------

// stubTemplate is the Starlark scaffold written for each customized hook.
// Placeholder %s is the hook name.
const stubTemplate = `# Migration scaffold for %s.sh
# Operator has customized the bash hook; this scaffold provides an editable
# Starlark equivalent. Decide:
#   - Keep the .sh in lib/hooks-user/ (Tier 2 bash escape — unchanged)
#   - Translate the .sh diff into Starlark below (Tier 1 customization)
# Read docs/go-port-phase3-hook-mitigation.md for the trade-offs.

override = False   # set to True to fully replace the Go-native Tier 0

def on_event(ctx):
    # ctx.input is the hook input (event/tool/payload/env/workdir)
    # ctx.log("msg") writes to stderr
    # ctx.set_exit_code(2) blocks the tool call
    # ctx.read_file(path) reads from sandbox (work/current/ + allow-list)
    # ctx.write_artifact(name, bytes) writes to work/current/hooks/%s/
    pass
`

func buildStarStub(name string) string {
	return fmt.Sprintf(stubTemplate, name, name)
}

// ---- helpers -----------------------------------------------------------------

// collectBaselineHooks returns a map of hook-name → SHA-256 hex string for
// all .sh files in dir.
func collectBaselineHooks(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".sh")
		path := filepath.Join(dir, e.Name())
		sha, err := fileSHA256(path)
		if err != nil {
			return nil, fmt.Errorf("sha256(%s): %w", path, err)
		}
		out[name] = sha
	}
	return out, nil
}

// fileSHA256 returns the lowercase hex SHA-256 of the file at path.
func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// PrintMigrateHelp writes the migrate subcommand usage to w.
func PrintMigrateHelp(w io.Writer) {
	fmt.Fprintln(w, `yakos hooks migrate [--project <dir>] [--dry-run]

Scaffolds Starlark migration stubs for operator-customized bash hooks.

For each lib/hooks/<name>.sh that the operator has modified relative to the
framework baseline, a stub lib/hooks/<name>.star is created in the operator's
project directory. The stub documents the Starlark API and leaves on_event as
a no-op placeholder.

Options:
  --project <dir>   Operator project root (default: current directory).
  --dry-run         List what would be created without writing any files.

Detection logic:
  - Operator copy byte-identical to baseline → skip (no customization).
  - .star file already exists at target path → skip (won't overwrite).
  - No operator copy present → skip.
  - Otherwise → write stub.

Next steps after stub creation:
  1. Review the diff between your .sh and the framework baseline.
  2. Either:
     a. Translate the diff into Starlark inside on_event (Tier 1).
     b. Move your .sh to lib/hooks-user/<name>.sh (Tier 2 bash escape).
  3. Delete the .star stub if you chose option (b).

Read docs/go-port-phase3-hook-mitigation.md for the full trade-off matrix.

Examples:
  yakos hooks migrate --dry-run
  yakos hooks migrate --project ~/code/myapp`)
}
