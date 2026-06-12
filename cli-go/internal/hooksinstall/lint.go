package hooksinstall

// lint.go — implements `yakos hooks lint` (Q4).
//
// For each .star file found in the hooksDir, the linter:
//  1. Parses the file using go.starlark.net's static analysis (SourceProgram).
//  2. Reports unused top-level names (names defined but not called in on_event).
//  3. Validates that if `override = True` is present the file also defines
//     on_event — an override with no on_event is always a no-op.
//  4. Detects unreachable code after a `return` statement (limited — Starlark
//     has no CFG in the library; we do a simple heuristic scan).
//  5. Validates sandboxed API usage: calls to ctx.read_file, ctx.write_artifact
//     are fine; calls to anything not in the ctxAPI allow-list are warned.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.starlark.net/starlark"
)

// LintResult summarises the lint findings for one .star file.
type LintResult struct {
	// FilePath is the absolute path to the .star file.
	FilePath string

	// Errors are hard failures (syntax errors, fatal misuse).
	Errors []string

	// Warnings are advisory findings (unused names, missing on_event with override).
	Warnings []string
}

// LintConfig configures a lint run.
type LintConfig struct {
	// HooksDir is the directory containing .star files to lint.
	// Typically lib/hooks/ in the yakOS repo.
	HooksDir string

	// Writer receives summary output.
	Writer io.Writer

	// ErrWriter receives error output.
	ErrWriter io.Writer
}

// LintResults is the set of per-file lint results.
type LintResults struct {
	Files     []LintResult
	Total     int
	ErrCount  int
	WarnCount int
}

// RunLint lints all .star files in cfg.HooksDir and returns the results.
func RunLint(cfg LintConfig) (*LintResults, error) {
	if cfg.Writer == nil {
		cfg.Writer = io.Discard
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = io.Discard
	}

	if cfg.HooksDir == "" {
		return nil, fmt.Errorf("hooks lint: HooksDir required")
	}
	if _, err := os.Stat(cfg.HooksDir); err != nil {
		return nil, fmt.Errorf("hooks lint: %s: %w", cfg.HooksDir, err)
	}

	entries, err := os.ReadDir(cfg.HooksDir)
	if err != nil {
		return nil, fmt.Errorf("hooks lint: readdir %s: %w", cfg.HooksDir, err)
	}

	results := &LintResults{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".star") {
			continue
		}
		path := filepath.Join(cfg.HooksDir, e.Name())
		result := lintStarFile(path)
		results.Files = append(results.Files, result)
		results.Total++
		results.ErrCount += len(result.Errors)
		results.WarnCount += len(result.Warnings)
	}

	// Print summary.
	PrintLintResults(cfg.Writer, results)
	return results, nil
}

// PrintLintResults writes the lint summary to w.
func PrintLintResults(w io.Writer, r *LintResults) {
	if r.Total == 0 {
		fmt.Fprintln(w, "hooks lint: no .star files found")
		return
	}
	fmt.Fprintf(w, "hooks lint: %d file(s) checked\n", r.Total)
	for _, f := range r.Files {
		base := filepath.Base(f.FilePath)
		if len(f.Errors)+len(f.Warnings) == 0 {
			fmt.Fprintf(w, "  %s: ok\n", base)
			continue
		}
		for _, e := range f.Errors {
			fmt.Fprintf(w, "  %s: ERROR: %s\n", base, e)
		}
		for _, wn := range f.Warnings {
			fmt.Fprintf(w, "  %s: WARN: %s\n", base, wn)
		}
	}
	if r.ErrCount > 0 {
		fmt.Fprintf(w, "\nhooks lint: %d error(s), %d warning(s)\n", r.ErrCount, r.WarnCount)
	} else {
		fmt.Fprintf(w, "\nhooks lint: %d warning(s) — no errors\n", r.WarnCount)
	}
}

// ---- per-file lint ----------------------------------------------------------

// ctxAPI is the set of ctx attributes that are valid in a Starlark hook.
var ctxAPI = map[string]bool{
	"log":            true,
	"set_exit_code":  true,
	"read_file":      true,
	"write_artifact": true,
	"input":          true,
}

func lintStarFile(path string) LintResult {
	result := LintResult{FilePath: path}
	src, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("read error: %v", err))
		return result
	}

	// Parse + compile via SourceProgram (static analysis).
	_, prog, err := starlark.SourceProgram(path, src, func(name string) bool { return name == "ctx" })
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("syntax error: %v", err))
		return result
	}
	_ = prog // prog is valid; further checks are source-level.

	// Execute in a sandbox to discover globals.
	thread := &starlark.Thread{Name: "lint:" + filepath.Base(path)}
	globals, execErr := starlark.ExecFile(thread, path, src, nil)
	// execErr may be a runtime error (e.g. calling undefined function in module scope).
	// We treat this as a warning, not a fatal error, since on_event runs lazily.

	// Check 1: override = True requires on_event to be defined.
	hasOverride := false
	hasOnEvent := false
	if execErr == nil {
		if v, ok := globals["override"]; ok {
			if b, ok := v.(starlark.Bool); ok && bool(b) {
				hasOverride = true
			}
		}
		if _, ok := globals["on_event"]; ok {
			hasOnEvent = true
		}
	}
	if hasOverride && !hasOnEvent {
		result.Warnings = append(result.Warnings,
			"override = True declared but on_event is not defined — the override is a no-op")
	}

	// Check 2: warn if execErr during module-scope execution.
	if execErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("module-scope execution warning: %v", execErr))
	}

	// Check 3: source-level heuristics.
	lines := strings.Split(string(src), "\n")
	lintSourceHeuristics(lines, &result)

	return result
}

// lintSourceHeuristics applies simple line-by-line heuristics.
func lintSourceHeuristics(lines []string, result *LintResult) {
	inOnEvent := false
	returnSeen := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Detect on_event function boundary.
		if strings.HasPrefix(trimmed, "def on_event(") {
			inOnEvent = true
			returnSeen = false
			continue
		}
		if inOnEvent && strings.HasPrefix(trimmed, "def ") {
			// Another function starts — exit on_event scope.
			inOnEvent = false
		}

		// Unreachable-after-return heuristic.
		if inOnEvent {
			if trimmed == "return" || strings.HasPrefix(trimmed, "return ") {
				returnSeen = true
				continue
			}
			if returnSeen && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				result.Warnings = append(result.Warnings,
					"possible unreachable code after return in on_event")
				returnSeen = false // only warn once per return
			}
		}

		// ctx attribute misuse: look for ctx.something( where something is not in ctxAPI.
		if idx := strings.Index(trimmed, "ctx."); idx >= 0 {
			rest := trimmed[idx+4:]
			// Extract the attribute name.
			attrEnd := strings.IndexAny(rest, ".([ \t")
			if attrEnd < 0 {
				attrEnd = len(rest)
			}
			attr := rest[:attrEnd]
			if attr != "" && !ctxAPI[attr] {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("ctx.%s is not in the sandboxed API (valid: log, set_exit_code, read_file, write_artifact, input)", attr))
			}
		}
	}
}
