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
//
// # YAKOS_HOOKS env var
//
// The env var YAKOS_HOOKS controls which tiers fire:
//
//	YAKOS_HOOKS=go      — Tier 0 (Go-native) only; Tier 2 bash is bypassed
//	                       even if a .sh file is present. This is the Phase 3
//	                       operator opt-in path to validate Go parity.
//	YAKOS_HOOKS=bash    — Skip Tier 0 entirely; invoke Tier 2 (bash) only.
//	                       This is the default when unset or empty, preserving
//	                       pre-Phase-3 behaviour during the compat window.
//	YAKOS_HOOKS=hybrid  — Both tiers fire in sequence (audit mode). Any
//	                       artifact divergence is written to
//	                       work/current/logs/hook-parity-divergence.ndjson.
//
// RunEnvMode reads YAKOS_HOOKS from the Runner's EnvLookup field (defaults
// to os.Getenv). Tests inject a no-op or custom lookup to avoid relying on
// real env state.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/bashbridge"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/starlarkbridge"
)

// HooksMode controls which tiers the Runner dispatches to.
type HooksMode string

const (
	// HooksModeGo runs Tier-0 (Go-native) only. YAKOS_HOOKS=go.
	HooksModeGo HooksMode = "go"
	// HooksModeBash skips Tier-0 and runs Tier-2 (bash) only. YAKOS_HOOKS=bash.
	HooksModeBash HooksMode = "bash"
	// HooksModeHybrid runs both tiers and records divergence. YAKOS_HOOKS=hybrid.
	HooksModeHybrid HooksMode = "hybrid"
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

	// EnvLookup is used to read environment variables. Defaults to os.Getenv
	// when nil. Injected for tests so the routing matrix is deterministic.
	EnvLookup func(string) string

	// NowFn is injected for tests to control timestamps on parity-divergence
	// log entries. Defaults to time.Now when nil.
	NowFn func() time.Time

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
		EnvLookup:      os.Getenv,
		NowFn:          time.Now,
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
		EnvLookup:      os.Getenv,
		NowFn:          time.Now,
		bashAvailable:  bashAvailable,
		bashPath:       bashPath,
	}
}

// hooksMode reads YAKOS_HOOKS via EnvLookup and normalises the value.
// Unknown values fall back to HooksModeBash (safe default).
func (r *Runner) hooksMode() HooksMode {
	lookup := r.EnvLookup
	if lookup == nil {
		lookup = os.Getenv
	}
	switch HooksMode(lookup("YAKOS_HOOKS")) {
	case HooksModeGo:
		return HooksModeGo
	case HooksModeHybrid:
		return HooksModeHybrid
	default:
		// "bash", empty string, or any unknown value → bash (safe default).
		return HooksModeBash
	}
}

// Run dispatches a hook according to the YAKOS_HOOKS env var.
//
//	YAKOS_HOOKS=go (or unset)  → Tier 0 → Tier 1 (Starlark) only; Tier 2 bash bypassed
//	YAKOS_HOOKS=bash            → Tier 2 (bash) only; Tier 0 skipped entirely
//	YAKOS_HOOKS=hybrid          → Tier 0 → Tier 2 both fire; divergence logged
//
// Within each mode the intra-tier rules remain:
//   - ExitCode >= 2 from any tier short-circuits the rest.
//   - Tier 1 (Starlark) fires only when a .star file exists (Go and Hybrid modes).
//   - Tier 2 (bash) fires only when a .sh file exists and bash is on PATH.
func (r *Runner) Run(ctx context.Context, h Hook, in HookInput) (HookOutput, error) {
	mode := r.hooksMode()
	switch mode {
	case HooksModeGo:
		return r.runGoOnly(ctx, h, in)
	case HooksModeBash:
		return r.runBashOnly(ctx, h, in)
	case HooksModeHybrid:
		return r.runHybrid(ctx, h, in)
	default:
		return r.runBashOnly(ctx, h, in)
	}
}

// runGoOnly runs Tier 0 (Go-native) then Tier 1 (Starlark if present).
// Tier 2 (bash) is bypassed entirely.
func (r *Runner) runGoOnly(ctx context.Context, h Hook, in HookInput) (HookOutput, error) {
	out, err := h.Run(ctx, in)
	if err != nil {
		return out, fmt.Errorf("hook %s tier-0: %w", h.Name(), err)
	}
	if out.ExitCode >= 2 {
		return out, nil
	}
	return r.applyStarlarkIfPresent(ctx, h.Name(), in, out)
}

// runBashOnly skips Tier 0 and runs only Tier 2 (bash user-hook).
// If no .sh file exists the hook is a no-op (exit 0).
func (r *Runner) runBashOnly(ctx context.Context, h Hook, in HookInput) (HookOutput, error) {
	out := HookOutput{ExitCode: 0}
	shPath := r.shPath(h.Name())
	if _, statErr := os.Stat(shPath); os.IsNotExist(statErr) {
		// No bash hook present — no-op, not an error.
		return out, nil
	}
	if !r.bashAvailable {
		out.Skipped = true
		fmt.Fprintf(r.writer(), "yakos hooks: skipped %s (bash not found on PATH; install Git Bash to enable Tier-2 hooks)\n", shPath)
		return out, nil
	}
	bb := bashbridge.New(shPath, r.bashPath)
	out, err := bb.Apply(ctx, in, out)
	if err != nil {
		return out, fmt.Errorf("hook %s tier-2 (bash-only): %w", h.Name(), err)
	}
	return out, nil
}

// runHybrid runs both Tier 0 (Go) and Tier 2 (bash) in sequence and writes
// any artifact/exit-code divergence to
// work/current/logs/hook-parity-divergence.ndjson.
func (r *Runner) runHybrid(ctx context.Context, h Hook, in HookInput) (HookOutput, error) {
	// Tier 0 path (Go + Starlark).
	goOut, goErr := r.runGoOnly(ctx, h, in)
	if goErr != nil {
		// If Go fails we still run bash so operators can verify bash side works.
		fmt.Fprintf(r.writer(), "yakos hooks hybrid: tier-0 error for %s: %v\n", h.Name(), goErr)
	}

	// Tier 2 path (bash).
	bashOut, bashErr := r.runBashOnly(ctx, h, in)

	// Record divergence if output differs.
	if diverged(goOut, bashOut) || (goErr != nil) != (bashErr != nil) {
		r.logParityDivergence(h.Name(), in, goOut, goErr, bashOut, bashErr)
	}

	// Hybrid returns the Go output (Tier 0 is authoritative in go-mode direction).
	if goErr != nil {
		// Fall back to bash output if Go failed.
		return bashOut, bashErr
	}
	return goOut, goErr
}

// applyStarlarkIfPresent runs the Starlark bridge if a .star file exists.
func (r *Runner) applyStarlarkIfPresent(ctx context.Context, name string, in HookInput, out HookOutput) (HookOutput, error) {
	starPath := r.starPath(name)
	if _, statErr := os.Stat(starPath); statErr != nil {
		return out, nil
	}
	sandboxPaths := r.buildSandboxPaths()
	bridge, bridgeErr := starlarkbridge.New(starPath, sandboxPaths)
	if bridgeErr != nil {
		return out, fmt.Errorf("hook %s tier-1 init: %w", name, bridgeErr)
	}
	out, err := bridge.Apply(ctx, in, out)
	if err != nil {
		return out, fmt.Errorf("hook %s tier-1: %w", name, err)
	}
	return out, nil
}

// applyBashTier2 executes the Tier-2 bash user-hook if present and bash is available.
// It is used by the legacy runGoThenBash path (kept for the original Run semantics
// when both tiers are expected in sequence).
func (r *Runner) applyBashTier2(ctx context.Context, name string, in HookInput, out HookOutput) (HookOutput, error) {
	shPath := r.shPath(name)
	if _, statErr := os.Stat(shPath); os.IsNotExist(statErr) {
		return out, nil
	}
	if !r.bashAvailable {
		out.Skipped = true
		fmt.Fprintf(r.writer(), "yakos hooks: skipped %s (bash not found on PATH; install Git Bash to enable Tier-2 hooks)\n", shPath)
		return out, nil
	}
	bb := bashbridge.New(shPath, r.bashPath)
	out, err := bb.Apply(ctx, in, out)
	if err != nil {
		return out, fmt.Errorf("hook %s tier-2: %w", name, err)
	}
	return out, nil
}

// diverged reports whether two HookOutputs differ in exit code or artifacts.
func diverged(a, b HookOutput) bool {
	if a.ExitCode != b.ExitCode {
		return true
	}
	if len(a.Artifacts) != len(b.Artifacts) {
		return true
	}
	for k, av := range a.Artifacts {
		bv, ok := b.Artifacts[k]
		if !ok {
			return true
		}
		if string(av) != string(bv) {
			return true
		}
	}
	return false
}

// logParityDivergence appends a parity-divergence record to the NDJSON log.
func (r *Runner) logParityDivergence(hookName string, in HookInput, goOut HookOutput, goErr error, bashOut HookOutput, bashErr error) {
	if r.WorkCurrentDir == "" {
		return
	}
	nowFn := r.NowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	logDir := filepath.Join(r.WorkCurrentDir, "logs")
	_ = os.MkdirAll(logDir, 0755) //nolint:gosec
	logFile := filepath.Join(logDir, "hook-parity-divergence.ndjson")

	goErrStr := ""
	if goErr != nil {
		goErrStr = goErr.Error()
	}
	bashErrStr := ""
	if bashErr != nil {
		bashErrStr = bashErr.Error()
	}

	entry := map[string]any{
		"ts":              nowFn().UTC().Format(time.RFC3339),
		"hook":            hookName,
		"event":           in.Event,
		"tool":            in.Tool,
		"go_exit_code":    goOut.ExitCode,
		"bash_exit_code":  bashOut.ExitCode,
		"go_error":        goErrStr,
		"bash_error":      bashErrStr,
		"artifacts_match": !diverged(goOut, bashOut),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		return
	}
	defer f.Close() //nolint:errcheck
	_, _ = f.Write(data)
}

// writer returns the configured writer or os.Stderr.
func (r *Runner) writer() io.Writer {
	if r.Writer != nil {
		return r.Writer
	}
	return os.Stderr
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
