package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/registry"
	"github.com/bakw00ds/yakos/internal/hooks/runner"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// runHookCmd implements `yakos hook` (singular) — the Go-native hook
// dispatch entrypoint (S-6 A-1). Distinct from the existing plural `hooks`
// command (runHooks, cmd_integration.go), which manages runtime-native
// hook *registration* (codex/gemini/agy config), not hook *bodies*.
//
//	yakos hook run <name>     read Claude Code hook JSON on stdin, dispatch
//	                          to the registered Tier-0 hook, exit with its
//	                          code.
//	yakos hook list           print every registered hook name and its
//	                          readiness ("go" = parity-verified, safe for
//	                          `refresh --hooks-impl=go`; "bash-only" = not
//	                          yet).
//	yakos hook mode           print the effective YAKOS_HOOKS mode and why.
func runHookCmd(yakosRoot string, args []string) {
	if len(args) == 0 || isHelpArg(args[0]) {
		printHookHelp(os.Stdout)
		os.Exit(0)
	}

	switch args[0] {
	case "run":
		runHookRun(yakosRoot, args[1:])
	case "list":
		runHookList(args[1:])
	case "mode":
		runHookMode(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "hook: unknown subcommand %q (try --help)\n", args[0])
		os.Exit(1)
	}
}

func printHookHelp(w io.Writer) {
	fmt.Fprint(w, `Usage: yakos hook <subcommand>

Go-native hook dispatch (S-6). Reads Claude Code's hook JSON from stdin and
runs the registered Tier-0 Go hook, per the YAKOS_HOOKS env var (go / bash /
hybrid; default bash — see internal/hooks/runner).

Subcommands:
  run <name>    Read hook JSON on stdin, dispatch, exit with the hook's code.
  list          Print every registered hook name and its readiness.
  mode          Print the effective YAKOS_HOOKS mode and why.

This is the Go-native hook-BODY entrypoint. See 'yakos hooks --help' for
runtime-native hook *registration* (a separate, plural command).
`)
}

// runHookRun is `yakos hook run <name>`.
func runHookRun(yakosRoot string, args []string) {
	if len(args) == 0 || isHelpArg(args[0]) {
		fmt.Fprintln(os.Stderr, "hook run: usage: yakos hook run <name>")
		os.Exit(1)
	}
	name := args[0]

	entry, ok := lookupEntry(name)
	if !ok {
		// Unknown hook: fail open (exit 0) with a one-line diagnostic,
		// matching every bash hook's own posture toward inputs it can't
		// make sense of (no registered hook means nothing to block on).
		fmt.Fprintf(os.Stderr, "yakos hook run: unknown hook %q (try 'yakos hook list')\n", name)
		os.Exit(0)
	}

	data, readErr := io.ReadAll(os.Stdin)
	if readErr != nil {
		failDegraded(name, entry, fmt.Sprintf("reading stdin: %v", readErr))
		return
	}

	in, decodeErr := hookio.DecodeBytes(data)
	if decodeErr != nil {
		failDegraded(name, entry, decodeErr.Error())
		return
	}
	in.Env = snapshotEnv()

	workCurrentDir, projectDir := resolveHookWorkDirs()
	stateDir := statepath.Dir()

	cfg := registry.Config{
		WorkCurrentDir: workCurrentDir,
		ProjectDir:     projectDir,
		StateDir:       stateDir,
	}
	hook, _, ok := registry.Lookup(name, cfg)
	if !ok {
		// Registered in lookupEntry's list moments ago; only reachable if
		// the two lookups ever drift, which they structurally cannot
		// (both read the same registry.entries slice). Kept as a guard.
		fmt.Fprintf(os.Stderr, "yakos hook run: unknown hook %q (try 'yakos hook list')\n", name)
		os.Exit(0)
	}

	hooksDir := filepath.Join(yakosRoot, "lib", "hooks")
	userHooksDir := filepath.Join(projectDir, "lib", "hooks-user")
	r := runner.New(hooksDir, userHooksDir, workCurrentDir, nil, os.Stderr)

	out, runErr := r.Run(context.Background(), hook, in)
	if len(out.Stdout) > 0 {
		_, _ = os.Stdout.Write(out.Stdout)
	}
	if len(out.Stderr) > 0 {
		_, _ = os.Stderr.Write(out.Stderr)
	}
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "yakos hook run %s: %v\n", name, runErr)
	}
	os.Exit(out.ExitCode)
}

// failDegraded handles undecodable stdin / read errors for `hook run`,
// mirroring lib/hooks/lib/hook-input.sh's HOOK_FAIL_CLOSED contract:
//   - FailClosed hooks exit 2 (block) unless YAKOS_HOOKS_FAIL_OPEN=1 is set,
//     in which case they WARN and pass (exit 0).
//   - Telemetry hooks (FailClosed=false) always WARN and pass (exit 0),
//     matching the bash README's "no-block policy for telemetry hooks".
func failDegraded(name string, entry registry.Entry, reason string) {
	if entry.FailClosed && os.Getenv("YAKOS_HOOKS_FAIL_OPEN") != "1" {
		fmt.Fprintf(os.Stderr, "%s: BLOCKED — cannot safely evaluate this tool call (%s).\n", name, reason)
		fmt.Fprintf(os.Stderr, "%s: this hook enforces a security control and refuses to fail open.\n", name)
		fmt.Fprintf(os.Stderr, "%s: emergency override: export YAKOS_HOOKS_FAIL_OPEN=1\n", name)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "%s: WARN — degraded input (%s); treating as a no-op pass.\n", name, reason)
	os.Exit(0)
}

// lookupEntry finds name in the registry without constructing a Hook —
// used to decide fail-open/fail-closed posture before stdin is even known
// to be decodable.
func lookupEntry(name string) (registry.Entry, bool) {
	for _, e := range registry.All() {
		if e.Name == name {
			return e, true
		}
	}
	return registry.Entry{}, false
}

// runHookList is `yakos hook list`.
func runHookList(args []string) {
	if len(args) > 0 && isHelpArg(args[0]) {
		fmt.Fprintln(os.Stdout, "Usage: yakos hook list")
		os.Exit(0)
	}
	for _, e := range registry.All() {
		status := "bash-only"
		if e.GoReady {
			status = "go"
		}
		fmt.Printf("%-24s %s\n", e.Name, status)
	}
}

// runHookMode is `yakos hook mode`.
func runHookMode(args []string) {
	if len(args) > 0 && isHelpArg(args[0]) {
		fmt.Fprintln(os.Stdout, "Usage: yakos hook mode")
		os.Exit(0)
	}
	raw := os.Getenv("YAKOS_HOOKS")
	var mode, why string
	switch raw {
	case "go":
		mode, why = "go", "YAKOS_HOOKS=go"
	case "hybrid":
		mode, why = "hybrid", "YAKOS_HOOKS=hybrid"
	case "bash":
		mode, why = "bash", "YAKOS_HOOKS=bash"
	case "":
		mode, why = "bash", "YAKOS_HOOKS unset (safe default)"
	default:
		mode, why = "bash", fmt.Sprintf("YAKOS_HOOKS=%q is unrecognized (safe default)", raw)
	}
	fmt.Printf("%s (%s)\n", mode, why)
}

// snapshotEnv copies os.Environ() into a map[string]string, mirroring how
// bash hooks read $VAR directly rather than through stdin JSON. Hook
// business logic (e.g. senderRole fallbacks, budget-guard's
// YAKOS_BUDGET_DISABLE) reads specific keys out of HookInput.Env.
func snapshotEnv() map[string]string {
	environ := os.Environ()
	out := make(map[string]string, len(environ))
	for _, kv := range environ {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	return out
}

// resolveHookWorkDirs mirrors lib/hooks/lib/paths.sh's yakos_work_dir /
// yakos_current_dir and $CLAUDE_PROJECT_DIR resolution, duplicated here in
// the same spirit as internal/status.resolveWorkDir and
// internal/checkpoint.resolveWorkDir (each hook-adjacent caller keeps its
// own small copy rather than sharing one package — consistent with the
// existing pattern in this codebase).
func resolveHookWorkDirs() (workCurrentDir, projectDir string) {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}
	projectDir = os.Getenv("CLAUDE_PROJECT_DIR")
	if projectDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			projectDir = cwd
		}
	}

	// 1. $YAKOS_WORK_DIR — absolute override.
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
		return filepath.Join(v, "current"), projectDir
	}
	// 2. $YAKOS_INPLACE_WORK=1 + $CLAUDE_PROJECT_DIR — in-repo work/.
	if os.Getenv("YAKOS_INPLACE_WORK") == "1" && projectDir != "" {
		return filepath.Join(projectDir, "work", "current"), projectDir
	}
	// 3. $HOME/agent-control/<name>/work — canonical.
	name := resolveHookProjectName(projectDir)
	return filepath.Join(home, "agent-control", name, "work", "current"), projectDir
}

// resolveHookProjectName mirrors paths.sh's yakos_project_name function.
func resolveHookProjectName(projectDir string) string {
	if v := os.Getenv("YAKOS_PROJECT_NAME"); v != "" {
		return v
	}
	if projectDir != "" {
		return filepath.Base(projectDir)
	}
	return "unknown"
}
