// Package quickstart is the Go port of cli/lib/quickstart.sh.
//
// It implements `yakos quickstart [flags]`: an onboarding macro that detects
// what has already been done and runs only what is needed, in order:
//
//  1. Install yakOS (skipped when ~/.yakos pointer already points at YAKOS_ROOT).
//  2. Init the project (skipped when ~/agent-control/<name>/ already exists).
//  3. Start the session (always runs last).
//
// Each step delegates to the corresponding package:
//   - install.Run  — internal/install
//   - initialize.Run — internal/initialize
//   - start.Run    — internal/start
//
// Idempotent: re-running quickstart against an already-onboarded project is a
// no-op for install and init, then falls through to start.
//
// Flag forwarding mirrors quickstart.sh:
//
//	--runtime <id>   forwarded to start
//	--multi-dev      forwarded to init (one-time per project)
//	--safe           forwarded to start
//	--allow-root     forwarded to start (and install)
//	--dry-run        forwarded to install, init, and start
//
// Name inference follows the same three-case logic as quickstart.sh:
//
//	A) cwd is ~/agent-control/<name>/… → name is the directory basename
//	B) cwd is inside a tracked project repo → walk agent-control to find owner
//	C) cwd is a git repo not yet bootstrapped → name = basename of git root
//
// If name inference fails, Run returns an error telling the operator to either
// cd into a git repo or run `yakos init` explicitly.
package quickstart

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bakw00ds/yakos/internal/initialize"
	"github.com/bakw00ds/yakos/internal/install"
	"github.com/bakw00ds/yakos/internal/start"
)

// validNameRe mirrors init.sh's name validation: alphanumeric + hyphen + underscore.
var validNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Config controls all inputs to a quickstart run.
type Config struct {
	// YakosRoot is the absolute path to the yakos repo root ($YAKOS_ROOT). Required.
	YakosRoot string

	// HomeDir overrides $HOME. Defaults to os.Getenv("HOME").
	HomeDir string

	// CWD overrides the current working directory for name inference.
	// Defaults to os.Getwd().
	CWD string

	// Runtime is forwarded to start.Run.
	Runtime string

	// MultiDev is forwarded to initialize.Run as an advisory flag.
	MultiDev bool

	// Safe is forwarded to start.Run.
	Safe bool

	// AllowRoot is forwarded to start.Run.
	AllowRoot bool

	// DryRun is forwarded to install.Run, initialize.Run, and start.Run.
	DryRun bool

	// Writer receives progress output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives warning/advisory output (default: os.Stderr).
	ErrWriter io.Writer

	// ExecFn, if non-nil, replaces the real syscall.Exec in start.Run.
	// Tests inject a no-op here to capture the would-be command.
	ExecFn func(argv0 string, argv []string, env []string) error
}

// Result is the aggregate outcome of a quickstart run.
type Result struct {
	// ProjectName is the inferred or detected project name.
	ProjectName string

	// InstallSkipped is true when install was detected as already done.
	InstallSkipped bool

	// InitSkipped is true when init was detected as already done.
	InitSkipped bool

	// DryRun mirrors Config.DryRun.
	DryRun bool
}

// Run executes the quickstart sequence. It returns an error on any step failure.
// On success the start.Run call replaces the process (unless DryRun or ExecFn is set).
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

	cwd := cfg.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("quickstart: could not determine cwd: %w", err)
		}
	}
	// Resolve symlinks for canonical comparison (mirrors `cd "$PWD" && pwd -P`).
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}

	_, _ = fmt.Fprintln(cfg.Writer, "yakos quickstart")
	_, _ = fmt.Fprintf(cfg.Writer, "  cwd:        %s\n", cwd)
	_, _ = fmt.Fprintf(cfg.Writer, "  yakos root: %s\n", cfg.YakosRoot)
	_, _ = fmt.Fprintln(cfg.Writer)

	res := &Result{DryRun: cfg.DryRun}

	// ---- step 1: install --------------------------------------------------------

	yakosPointer := filepath.Join(home, ".yakos")
	needsInstall, installReason := checkNeedsInstall(yakosPointer, cfg.YakosRoot)

	if needsInstall {
		_, _ = fmt.Fprintf(cfg.Writer, "step 1/3: install yakOS — %s\n", installReason)
		if cfg.DryRun {
			_, _ = fmt.Fprintf(cfg.Writer, "  (dry-run) would run: yakos install\n")
		} else {
			instCfg := install.Config{
				YakosRoot: cfg.YakosRoot,
				HomeDir:   home,
				DryRun:    false,
				Writer:    cfg.Writer,
				ErrWriter: cfg.ErrWriter,
			}
			if _, err := install.Run(instCfg); err != nil {
				return nil, fmt.Errorf("quickstart: install step: %w", err)
			}
		}
	} else {
		_, _ = fmt.Fprintf(cfg.Writer, "step 1/3: install — already done (pointer at %s)\n", yakosPointer)
		res.InstallSkipped = true
	}
	_, _ = fmt.Fprintln(cfg.Writer)

	// ---- step 2: detect project + init ------------------------------------------

	acRoot := filepath.Join(home, "agent-control")
	name, projectAbs, needsInit, initReason := detectProject(cwd, acRoot)
	if name == "" {
		return nil, fmt.Errorf("quickstart: cwd (%s) is not a git repo and not a known yakos project.\n"+
			"  Either:\n"+
			"    - cd into a git repo and re-run 'yakos quickstart', OR\n"+
			"    - run 'yakos init <name> --project <repo-path>' explicitly", cwd)
	}
	if !validNameRe.MatchString(name) {
		return nil, fmt.Errorf("quickstart: inferred project name %q has invalid characters.\n"+
			"  Run 'yakos init <name> --project %s' explicitly with a valid name", name, projectAbs)
	}

	res.ProjectName = name

	if needsInit {
		_, _ = fmt.Fprintf(cfg.Writer, "step 2/3: init project %q — %s\n", name, initReason)
		if cfg.DryRun {
			_, _ = fmt.Fprintf(cfg.Writer, "  (dry-run) would run: yakos init %s --project %s\n", name, projectAbs)
		} else {
			initCfg := initialize.Config{
				Name:        name,
				ProjectPath: projectAbs,
				MultiDev:    cfg.MultiDev,
				DryRun:      false,
				HomeDir:     home,
				Writer:      cfg.Writer,
				ErrWriter:   cfg.ErrWriter,
			}
			if _, err := initialize.Run(initCfg); err != nil {
				return nil, fmt.Errorf("quickstart: init step: %w", err)
			}
		}
	} else {
		_, _ = fmt.Fprintf(cfg.Writer, "step 2/3: project %q — already bootstrapped\n", name)
		res.InitSkipped = true
	}
	_, _ = fmt.Fprintln(cfg.Writer)

	// ---- step 3: start ----------------------------------------------------------

	_, _ = fmt.Fprintf(cfg.Writer, "step 3/3: start session for %q\n", name)

	// When dry-run + init was also deferred (project not yet bootstrapped),
	// start.Run would fail because the control dir does not exist yet.
	// Instead of failing, print what would have been exec'd and return.
	if cfg.DryRun && needsInit {
		_, _ = fmt.Fprintf(cfg.Writer, "  (dry-run) would run: yakos start %s", name)
		if cfg.Runtime != "" {
			_, _ = fmt.Fprintf(cfg.Writer, " --runtime %s", cfg.Runtime)
		}
		if cfg.Safe {
			_, _ = fmt.Fprint(cfg.Writer, " --safe")
		}
		if cfg.AllowRoot {
			_, _ = fmt.Fprint(cfg.Writer, " --allow-root")
		}
		_, _ = fmt.Fprintln(cfg.Writer)
		return res, nil
	}

	startCfg := start.Config{
		Name:      name,
		YakosRoot: cfg.YakosRoot,
		HomeDir:   home,
		Runtime:   cfg.Runtime,
		Safe:      cfg.Safe,
		AllowRoot: cfg.AllowRoot,
		DryRun:    cfg.DryRun,
		NoAgents:  false,
		Writer:    cfg.Writer,
		ErrWriter: cfg.ErrWriter,
		ExecFn:    cfg.ExecFn,
	}

	if _, err := start.Run(startCfg); err != nil {
		return nil, fmt.Errorf("quickstart: start step: %w", err)
	}

	return res, nil
}

// ---- helpers ------------------------------------------------------------------

// checkNeedsInstall returns true (and a reason string) when the ~/.yakos pointer
// is absent or points at a different yakOS root than cfg.YakosRoot.
// Mirrors quickstart.sh's needs_install check.
func checkNeedsInstall(pointerPath, yakosRoot string) (bool, string) {
	data, err := os.ReadFile(pointerPath) //nolint:gosec
	if err != nil {
		return true, pointerPath + " does not exist"
	}
	current := strings.TrimSpace(string(data))
	if current == "" {
		return true, pointerPath + " is empty"
	}
	// Resolve both sides so comparison works across symlinks.
	wantAbs, _ := filepath.Abs(yakosRoot)
	if resolved, err := filepath.EvalSymlinks(wantAbs); err == nil {
		wantAbs = resolved
	}
	if current != wantAbs {
		return true, fmt.Sprintf("%s points to a different yakOS install (%s)", pointerPath, current)
	}
	return false, ""
}

// detectProject inspects cwd against the agent-control tree and git to figure
// out the project name, project repo path, whether init is needed, and a reason.
//
// Returns ("", "", false, "") when cwd cannot be mapped to any project.
// The caller should treat an empty name as an error.
//
// Mirrors the three-case detection in quickstart.sh (step 2/3):
//   - Case A: cwd is inside ~/agent-control/<name>/
//   - Case B: cwd is inside a repo tracked by an existing project-path file
//   - Case C: cwd is an untracked git repo → infer name = git root basename
//
// All path comparisons use filepath.EvalSymlinks-resolved versions so that
// macOS /var → /private/var and similar symlink indirections don't cause
// false mismatches.
func detectProject(cwd, acRoot string) (name, projectAbs string, needsInit bool, reason string) {
	// Resolve cwd symlinks for consistent comparison.
	cwdReal := cwd
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwdReal = resolved
	}

	// Resolve acRoot symlinks too.
	acRootReal := acRoot
	if resolved, err := filepath.EvalSymlinks(acRoot); err == nil {
		acRootReal = resolved
	}

	// Case A: cwd is inside ~/agent-control/<name>/
	if strings.HasPrefix(cwdReal, acRootReal+string(filepath.Separator)) ||
		strings.HasPrefix(cwdReal, acRoot+string(filepath.Separator)) {
		// Use the raw acRoot for path construction (matches filesystem).
		base := cwdReal
		baseRoot := acRootReal
		if !strings.HasPrefix(base, baseRoot+string(filepath.Separator)) {
			base = cwdReal
			baseRoot = acRoot
		}
		rest := base[len(baseRoot)+1:]
		n := rest
		if idx := strings.IndexByte(rest, filepath.Separator); idx > 0 {
			n = rest[:idx]
		}
		ppFile := filepath.Join(acRoot, n, ".project-path")
		if data, err := os.ReadFile(ppFile); err == nil { //nolint:gosec
			proj := strings.TrimSpace(string(data))
			return n, proj, false, ""
		}
		// .project-path missing — treat as needing repair (tell operator to re-init).
		return n, "", false, ""
	}

	// Case B: scan existing project-path files.
	entries, err := os.ReadDir(acRoot)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			ppFile := filepath.Join(acRoot, e.Name(), ".project-path")
			data, err := os.ReadFile(ppFile) //nolint:gosec
			if err != nil {
				continue
			}
			proj := strings.TrimSpace(string(data))
			if proj == "" {
				continue
			}
			// Resolve both sides for comparison.
			real := proj
			if resolved, err := filepath.EvalSymlinks(proj); err == nil {
				real = resolved
			}
			if cwdReal == real || strings.HasPrefix(cwdReal, real+string(filepath.Separator)) {
				return e.Name(), real, false, ""
			}
		}
	}

	// Case C: cwd is a git repo not yet bootstrapped.
	gitRoot := gitToplevel(cwd)
	if gitRoot == "" {
		return "", "", false, ""
	}
	// Resolve git root symlinks.
	gitRootReal := gitRoot
	if resolved, err := filepath.EvalSymlinks(gitRoot); err == nil {
		gitRootReal = resolved
	}
	n := filepath.Base(gitRootReal)
	controlDir := filepath.Join(acRoot, n)
	if _, err := os.Stat(controlDir); err == nil {
		// ~/agent-control/<name>/ exists but we didn't find it in the scan above —
		// treat as already bootstrapped.
		return n, gitRootReal, false, ""
	}
	return n, gitRootReal, true, fmt.Sprintf("cwd is git repo %q but %s does not exist", gitRootReal, controlDir)
}

// gitToplevel returns the git repository root containing path, or "".
func gitToplevel(path string) string {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel") //nolint:gosec
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// PrintHelp writes the --help text for `yakos quickstart`, mirroring
// the bash quickstart.sh usage() output.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos quickstart [--runtime <id>] [--multi-dev] [--safe] [--dry-run] [--allow-root]

One command to go from "fresh clone" to "session started against the cwd".

Detects what's been done and runs only what's needed:
  1. yakOS not installed → 'yakos install'
  2. cwd is a git repo not yet bootstrapped → 'yakos init'
  3. project already bootstrapped → 'yakos start'

Idempotent; safe to re-run.

Options:
  --runtime <id>  Forward runtime selection to 'yakos start'.
  --multi-dev     Forward --multi-dev to 'yakos init' (advisory in Go port Phase 1).
  --safe          Forward --safe to 'yakos start'.
  --allow-root    Forward --allow-root to 'yakos start'; container/root bypass mode.
  --dry-run       Print what each step would do; no writes, no exec.
  --help, -h      Print this help.

Examples:
  cd ~/code/myapp
  yakos quickstart                  # install if needed, init, start
  yakos quickstart --multi-dev      # bootstrap with co-pilot mode
  yakos quickstart --runtime codex  # start with codex instead of claude
  yakos quickstart --allow-root     # container/root bypass mode
`)
}
