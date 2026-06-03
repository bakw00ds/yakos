// Package runner implements the three-tier hook dispatcher for Phase 3.
//
// Tier 0 — Go-native baseline: each hook is a compiled function in the
// yakos binary. Fast (<1 ms), schema-validated, portable.
//
// Tier 1 — Starlark customization: if lib/hooks/<name>.star exists the
// starlarkbridge loads and runs it. A .star file may AUGMENT (default) or
// OVERRIDE Tier 0 by declaring `override = True`.
//
// Tier 2 — Bash user-hooks: if lib/hooks-user/<name>.sh exists AND bash is
// on PATH the bashbridge executes it after Tiers 0+1. On Windows without
// bash the file is skipped and a one-line diagnostic is emitted (Q2).
//
// The dispatch order is fixed: Tier 0 → Tier 1 → Tier 2.
package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bakw00ds/yakos/internal/hooks/bashbridge"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/starlarkbridge"
)

// Re-export the shared types for callers that only import runner.
type HookInput = hooktype.HookInput
type HookOutput = hooktype.HookOutput

// Hook is the interface every Tier-0 Go-native hook implements.
type Hook interface {
	// Name returns the canonical hook name (e.g. "cycle-counter").
	// Used to locate the sibling .star and .sh files.
	Name() string

	// Run executes the hook business logic. Returning a non-nil error with
	// ExitCode 0 in the output is treated as an infrastructure error (not a
	// blocking result). To block a tool call set ExitCode = 2.
	Run(ctx context.Context, in HookInput) (HookOutput, error)
}

// Runner composes all three tiers into a single hook dispatch call.
type Runner struct {
	// HooksDir is the directory that contains optional .star override files.
	// Typically lib/hooks/ relative to the project root.
	HooksDir string

	// UserHooksDir is the directory that contains optional bash user hooks.
	// Typically lib/hooks-user/ relative to the project root.
	UserHooksDir string

	// AllowPaths are additional paths Starlark hooks may read via ctx.read_file.
	// work/current/ is always included.
	AllowPaths []string

	// WorkCurrentDir is the absolute path to work/current/ for this session.
	// Used for Starlark artifact writes and the Q3 sandbox root.
	WorkCurrentDir string

	// Writer receives diagnostic output (Q2 skip notices). Defaults to
	// os.Stderr when nil.
	Writer io.Writer

	bashAvailable bool
	bashPath      string
}

// New builds a Runner and probes for bash availability.
func New(hooksDir, userHooksDir, workCurrentDir string, allowPaths []string, w io.Writer) *Runner {
	if w == nil {
		w = os.Stderr
	}
	bashPath, bashAvailable := bashbridge.DetectBash()
	return &Runner{
		HooksDir:       hooksDir,
		UserHooksDir:   userHooksDir,
		AllowPaths:     allowPaths,
		WorkCurrentDir: workCurrentDir,
		Writer:         w,
		bashAvailable:  bashAvailable,
		bashPath:       bashPath,
	}
}

// NewWithBashPath builds a Runner with an explicit bash path and availability
// flag. Intended for tests that need to simulate Windows-without-bash.
func NewWithBashPath(hooksDir, userHooksDir, workCurrentDir string, allowPaths []string, w io.Writer, bashPath string, bashAvailable bool) *Runner {
	if w == nil {
		w = os.Stderr
	}
	return &Runner{
		HooksDir:       hooksDir,
		UserHooksDir:   userHooksDir,
		AllowPaths:     allowPaths,
		WorkCurrentDir: workCurrentDir,
		Writer:         w,
		bashAvailable:  bashAvailable,
		bashPath:       bashPath,
	}
}

// Run dispatches a hook through all three tiers in order.
//
//	Tier 0 (Go) → Tier 1 (Starlark if .star exists) → Tier 2 (bash if .sh exists + bash available)
func (r *Runner) Run(ctx context.Context, h Hook, in HookInput) (HookOutput, error) {
	// Tier 0 — Go-native baseline.
	out, err := h.Run(ctx, in)
	if err != nil {
		return out, fmt.Errorf("hook %s tier-0: %w", h.Name(), err)
	}
	// Blocking exit codes propagate immediately; no point running Tier 1/2.
	if out.ExitCode >= 2 {
		return out, nil
	}

	// Tier 1 — Starlark override or augment.
	starPath := r.starPath(h.Name())
	if _, statErr := os.Stat(starPath); statErr == nil {
		sandboxPaths := r.buildSandboxPaths()
		bridge, bridgeErr := starlarkbridge.New(starPath, sandboxPaths)
		if bridgeErr != nil {
			return out, fmt.Errorf("hook %s tier-1 init: %w", h.Name(), bridgeErr)
		}
		out, err = bridge.Apply(ctx, in, out)
		if err != nil {
			return out, fmt.Errorf("hook %s tier-1: %w", h.Name(), err)
		}
		if out.ExitCode >= 2 {
			return out, nil
		}
	}

	// Tier 2 — bash user-hook.
	shPath := r.shPath(h.Name())
	if _, statErr := os.Stat(shPath); statErr == nil {
		if !r.bashAvailable {
			// Q2: present-but-skipped with one-line diagnostic.
			out.Skipped = true
			fmt.Fprintf(r.Writer, "yakos hooks: skipped %s (bash not found on PATH; install Git Bash to enable Tier-2 hooks)\n", shPath)
			return out, nil
		}
		bb := bashbridge.New(shPath, r.bashPath)
		out, err = bb.Apply(ctx, in, out)
		if err != nil {
			return out, fmt.Errorf("hook %s tier-2: %w", h.Name(), err)
		}
	}

	return out, nil
}

// BashAvailable reports whether bash was found on PATH at Runner construction time.
func (r *Runner) BashAvailable() bool { return r.bashAvailable }

// ---- helpers -----------------------------------------------------------------

func (r *Runner) starPath(name string) string {
	return filepath.Join(r.HooksDir, name+".star")
}

func (r *Runner) shPath(name string) string {
	return filepath.Join(r.UserHooksDir, name+".sh")
}

func (r *Runner) buildSandboxPaths() []string {
	paths := []string{}
	if r.WorkCurrentDir != "" {
		paths = append(paths, r.WorkCurrentDir)
	}
	paths = append(paths, r.AllowPaths...)
	return paths
}
