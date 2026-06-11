package metrics

// install_hook.go implements `yakos metrics install-hook` and
// `yakos metrics uninstall-hook`.
//
// # Design invariants
//
//  1. Never clobber an existing user-written hook.  Any pre-existing content is
//     preserved exactly; only the managed block is inserted (on install) or
//     removed (on uninstall).
//
//  2. The managed block is delimited by two sentinel comments:
//
//     # >>> yakos metrics >>>
//     … managed content …
//     # <<< yakos metrics <<<
//
//     Re-running install is idempotent: the block is replaced in-place; it is
//     never appended a second time.
//
//  3. --force on install replaces the entire hook file with a new managed-only
//     hook (shebang + block).  Use this to reset a hook that has diverged.
//
//  4. The hook command runs
//
//     yakos metrics collect --trigger git-hook --skip-analyzers
//
//     followed by "|| true" so the hook ALWAYS exits 0.  This means:
//     - The collect is fast (only [E] collectors, no [T] tool invocations).
//     - A collect error never blocks the commit/push.
//     - The binary path comes from os.Executable() so the running yakos binary
//       is used (no PATH ambiguity).  YAKOS_IMPL=go is propagated so the
//       metrics code path is always reached even when the default is bash.
//
//  5. Supported hooks: post-commit (default), pre-push.
//
//  6. The hook file is created with mode 0755 (executable) if it does not
//     already exist.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	hookBlockOpen  = "# >>> yakos metrics >>>"
	hookBlockClose = "# <<< yakos metrics <<<"
	hookShebang    = "#!/bin/sh"
)

// InstallHookConfig holds options for install-hook / uninstall-hook.
type InstallHookConfig struct {
	// HookName is the git hook to manage: "post-commit" (default) or "pre-push".
	HookName string

	// Force, when true, overwrites the entire hook file ignoring existing content.
	Force bool

	// ProjectDir is the absolute path to the project root whose .git dir is used.
	// When empty ResolveProjectDir is called.
	ProjectDir string

	// YakosBin is the path to the yakos binary that the hook will invoke.
	// Defaults to os.Executable().
	YakosBin string

	// Writer receives informational output.
	Writer io.Writer

	// ErrWriter receives warning/advisory output.
	ErrWriter io.Writer
}

// InstallHookResult summarises what install-hook / uninstall-hook did.
type InstallHookResult struct {
	// HookFile is the absolute path to the hook file.
	HookFile string

	// Installed is true when install-hook added or updated the managed block.
	Installed bool

	// Uninstalled is true when uninstall-hook removed the managed block.
	Uninstalled bool

	// AlreadyAbsent is true when uninstall-hook found no managed block to remove.
	AlreadyAbsent bool
}

// RunInstallHook executes `yakos metrics install-hook`.
func RunInstallHook(cfg InstallHookConfig) (InstallHookResult, error) {
	w := cfg.Writer
	if w == nil {
		w = io.Discard
	}
	ew := cfg.ErrWriter
	if ew == nil {
		ew = io.Discard
	}

	hookName := cfg.HookName
	if hookName == "" {
		hookName = "post-commit"
	}
	if err := validateHookName(hookName); err != nil {
		return InstallHookResult{}, err
	}

	projectDir := ResolveProjectDir(cfg.ProjectDir)
	if projectDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return InstallHookResult{}, fmt.Errorf("metrics install-hook: cannot determine project dir")
		}
		projectDir = cwd
		_, _ = fmt.Fprintf(ew, "metrics install-hook: no project dir resolved; using cwd %s\n", projectDir)
	}

	gitDir := findGitDir(projectDir)
	if gitDir == "" {
		return InstallHookResult{}, fmt.Errorf("metrics install-hook: .git directory not found under %s", projectDir)
	}

	hookFile := filepath.Join(gitDir, "hooks", hookName)

	yakosBin := cfg.YakosBin
	if yakosBin == "" {
		var err error
		yakosBin, err = os.Executable()
		if err != nil {
			return InstallHookResult{}, fmt.Errorf("metrics install-hook: resolve executable: %w", err)
		}
	}

	// Build the managed block content.
	managedBlock := buildManagedBlock(yakosBin)

	var result InstallHookResult
	result.HookFile = hookFile

	// Read existing hook content.
	existing, readErr := os.ReadFile(hookFile) //nolint:gosec
	exists := readErr == nil

	var newContent string

	if cfg.Force {
		// --force: replace file entirely with shebang + managed block.
		newContent = hookShebang + "\n\n" + managedBlock + "\n"
	} else if !exists {
		// File does not exist: create with shebang + managed block.
		newContent = hookShebang + "\n\n" + managedBlock + "\n"
	} else {
		// File exists: insert or replace the managed block.
		newContent = insertOrReplaceManagedBlock(string(existing), managedBlock)
	}

	// Ensure hooks directory exists.
	hooksDir := filepath.Dir(hookFile)
	if err := os.MkdirAll(hooksDir, 0755); err != nil { //nolint:gosec
		return InstallHookResult{}, fmt.Errorf("metrics install-hook: mkdir %s: %w", hooksDir, err)
	}

	if err := os.WriteFile(hookFile, []byte(newContent), 0755); err != nil { //nolint:gosec
		return InstallHookResult{}, fmt.Errorf("metrics install-hook: write %s: %w", hookFile, err)
	}

	result.Installed = true

	_, _ = fmt.Fprintf(w, "metrics install-hook: installed %s hook at %s\n", hookName, hookFile)
	_, _ = fmt.Fprintf(w, "  runs: YAKOS_IMPL=go %s metrics collect --trigger git-hook --skip-analyzers || true\n", yakosBin)
	if !exists || cfg.Force {
		_, _ = fmt.Fprintf(w, "  hook file: created\n")
	} else {
		_, _ = fmt.Fprintf(w, "  hook file: managed block updated (existing content preserved)\n")
	}
	_, _ = fmt.Fprintf(w, "  idempotent: re-run to update; existing user content is never removed\n")

	return result, nil
}

// RunUninstallHook executes `yakos metrics uninstall-hook`.
func RunUninstallHook(cfg InstallHookConfig) (InstallHookResult, error) {
	w := cfg.Writer
	if w == nil {
		w = io.Discard
	}
	ew := cfg.ErrWriter
	if ew == nil {
		ew = io.Discard
	}

	hookName := cfg.HookName
	if hookName == "" {
		hookName = "post-commit"
	}
	if err := validateHookName(hookName); err != nil {
		return InstallHookResult{}, err
	}

	projectDir := ResolveProjectDir(cfg.ProjectDir)
	if projectDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return InstallHookResult{}, fmt.Errorf("metrics uninstall-hook: cannot determine project dir")
		}
		projectDir = cwd
		_, _ = fmt.Fprintf(ew, "metrics uninstall-hook: no project dir resolved; using cwd %s\n", projectDir)
	}

	gitDir := findGitDir(projectDir)
	if gitDir == "" {
		return InstallHookResult{}, fmt.Errorf("metrics uninstall-hook: .git directory not found under %s", projectDir)
	}

	hookFile := filepath.Join(gitDir, "hooks", hookName)

	var result InstallHookResult
	result.HookFile = hookFile

	existing, err := os.ReadFile(hookFile) //nolint:gosec
	if os.IsNotExist(err) {
		// Hook file does not exist at all — friendly no-op.
		_, _ = fmt.Fprintf(w, "metrics uninstall-hook: %s hook not found at %s (nothing to do)\n", hookName, hookFile)
		result.AlreadyAbsent = true
		return result, nil
	}
	if err != nil {
		return InstallHookResult{}, fmt.Errorf("metrics uninstall-hook: read %s: %w", hookFile, err)
	}

	content := string(existing)
	if !containsManagedBlock(content) {
		// No managed block present — friendly no-op.
		_, _ = fmt.Fprintf(w, "metrics uninstall-hook: no managed block found in %s (nothing to do)\n", hookFile)
		result.AlreadyAbsent = true
		return result, nil
	}

	// Remove the managed block.
	stripped := removeManagedBlock(content)

	// If the remaining content is empty or only a shebang, remove the file.
	if isShebangOnly(stripped) {
		if err := os.Remove(hookFile); err != nil {
			return InstallHookResult{}, fmt.Errorf("metrics uninstall-hook: remove %s: %w", hookFile, err)
		}
		_, _ = fmt.Fprintf(w, "metrics uninstall-hook: removed %s (file was managed-only)\n", hookFile)
	} else {
		if err := os.WriteFile(hookFile, []byte(stripped), 0755); err != nil { //nolint:gosec
			return InstallHookResult{}, fmt.Errorf("metrics uninstall-hook: write %s: %w", hookFile, err)
		}
		_, _ = fmt.Fprintf(w, "metrics uninstall-hook: removed managed block from %s (user content preserved)\n", hookFile)
	}

	result.Uninstalled = true
	return result, nil
}

// ---- internal helpers -------------------------------------------------------

// validateHookName returns an error if name is not a supported hook.
func validateHookName(name string) error {
	switch name {
	case "post-commit", "pre-push":
		return nil
	default:
		return fmt.Errorf("metrics: unsupported hook %q (use post-commit or pre-push)", name)
	}
}

// findGitDir walks up from projectDir looking for a .git directory.
// Returns the path to the .git directory, or "" if none found.
func findGitDir(projectDir string) string {
	dir := projectDir
	for {
		candidate := filepath.Join(dir, ".git")
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// buildManagedBlock returns the hook block content (open sentinel, commands,
// close sentinel) — not including the surrounding newlines.
func buildManagedBlock(yakosBin string) string {
	// The command is backgrounded and silenced via "|| true" so that:
	//  - It never blocks the commit/push (exits 0 regardless).
	//  - A failing collect does not surface as a hook error.
	// YAKOS_IMPL=go is explicit so the Go binary routes correctly regardless of
	// the shell environment.
	var sb strings.Builder
	sb.WriteString(hookBlockOpen + "\n")
	sb.WriteString("# yakos metrics: auto-collect fast [E] metrics on " + "each git operation.\n")
	sb.WriteString("# This hook is managed by 'yakos metrics install-hook'.\n")
	sb.WriteString("# Edit with: yakos metrics install-hook --force\n")
	sb.WriteString("# Remove with: yakos metrics uninstall-hook\n")
	sb.WriteString("YAKOS_IMPL=go " + yakosBin + " metrics collect --trigger git-hook --skip-analyzers || true\n")
	sb.WriteString(hookBlockClose)
	return sb.String()
}

// insertOrReplaceManagedBlock either replaces an existing managed block in
// content or appends one if none exists.
func insertOrReplaceManagedBlock(content, managedBlock string) string {
	if containsManagedBlock(content) {
		return replaceManagedBlock(content, managedBlock)
	}
	// Append block, ensuring there's a newline separator.
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + "\n" + managedBlock + "\n"
}

// containsManagedBlock reports whether content includes the managed block sentinels.
func containsManagedBlock(content string) bool {
	return strings.Contains(content, hookBlockOpen) && strings.Contains(content, hookBlockClose)
}

// replaceManagedBlock replaces the content between (and including) the block
// sentinels with the new managedBlock.
func replaceManagedBlock(content, managedBlock string) string {
	lines := strings.Split(content, "\n")
	var out []string
	insideBlock := false
	for _, line := range lines {
		if strings.TrimSpace(line) == hookBlockOpen {
			insideBlock = true
			// Emit the new block here.
			out = append(out, strings.Split(managedBlock, "\n")...)
			continue
		}
		if strings.TrimSpace(line) == hookBlockClose {
			insideBlock = false
			continue
		}
		if !insideBlock {
			out = append(out, line)
		}
	}
	result := strings.Join(out, "\n")
	// Normalise trailing newline.
	result = strings.TrimRight(result, "\n") + "\n"
	return result
}

// removeManagedBlock strips the managed block (open sentinel through close
// sentinel inclusive) from content.
func removeManagedBlock(content string) string {
	lines := strings.Split(content, "\n")
	var out []string
	insideBlock := false
	for _, line := range lines {
		if strings.TrimSpace(line) == hookBlockOpen {
			insideBlock = true
			continue
		}
		if strings.TrimSpace(line) == hookBlockClose {
			insideBlock = false
			continue
		}
		if !insideBlock {
			out = append(out, line)
		}
	}
	result := strings.Join(out, "\n")
	// Normalise to a clean trailing newline.
	result = strings.TrimRight(result, "\n")
	if result != "" {
		result += "\n"
	}
	return result
}

// isShebangOnly returns true if content is empty or contains only whitespace
// and an optional shebang line — i.e. no meaningful user content remains.
func isShebangOnly(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true
	}
	lines := strings.Split(trimmed, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "#!") {
			continue
		}
		// There is a non-empty, non-shebang line.
		return false
	}
	return true
}
