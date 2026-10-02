package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hookbypass"
	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"github.com/bakw00ds/yakos/internal/hooks/registry"
	"github.com/bakw00ds/yakos/internal/hooks/runner"
	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
	"github.com/bakw00ds/yakos/internal/install"
	"github.com/bakw00ds/yakos/internal/statepath"
	"github.com/bakw00ds/yakos/internal/version"
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
//	                          `refresh --hooks-impl=hybrid`; "go-unverified" =
//	                          a Go twin exists but parity is not verified, so
//	                          hybrid keeps it on bash).
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
	case "supervisor-wrap":
		// Internal: the detached supervisor run wrapper (K-117), started by the
		// supervisor-stream hook. Configured through _SSW_* env; always exits 0.
		os.Exit(supervisorstream.RunWrapper(supervisorstream.WrapperConfigFromEnv(os.Getenv), args[1:], os.Stdout, os.Stderr))
	default:
		fmt.Fprintf(os.Stderr, "hook: unknown subcommand %q (try --help)\n", args[0])
		os.Exit(1)
	}
}

func printHookHelp(w io.Writer) {
	fmt.Fprint(w, `Usage: yakos hook <subcommand>

Go-native hook dispatch (S-6). Reads Claude Code's hook JSON from stdin and
runs the registered Tier-0 Go hook, per --impl if given, else the YAKOS_HOOKS env
var (go / bash / hybrid; default bash — see internal/hooks/runner). --impl
beats the env var; refresh-generated commands always pass --impl go.

Subcommands:
  run [--impl go|bash|hybrid] <name>
                Read hook JSON on stdin, dispatch, exit with the hook's code.
  list          Print every registered hook name and its readiness.
  mode          Print the effective YAKOS_HOOKS mode and why.

This is the Go-native hook-BODY entrypoint. See 'yakos hooks --help' for
runtime-native hook *registration* (a separate, plural command).
`)
}

// runHookRun is `yakos hook run <name>`.
func runHookRun(yakosRoot string, args []string) {
	if len(args) == 0 || isHelpArg(args[0]) {
		fmt.Fprintln(os.Stderr, "hook run: usage: yakos hook run [--impl go|bash|hybrid] <name>")
		os.Exit(1)
	}
	var override runner.HooksMode
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if args[0] != "--impl" || len(args) < 2 {
			fmt.Fprintf(os.Stderr, "hook run: unknown or incomplete flag %q (usage: yakos hook run [--impl go|bash|hybrid] <name>)\n", args[0])
			os.Exit(2)
		}
		if override != "" {
			fmt.Fprintln(os.Stderr, "hook run: --impl given more than once")
			os.Exit(2)
		}
		switch m := runner.HooksMode(args[1]); m {
		case runner.HooksModeGo, runner.HooksModeBash, runner.HooksModeHybrid:
			override = m
		default:
			fmt.Fprintf(os.Stderr, "hook run: invalid --impl %q (want go, bash, or hybrid)\n", args[1])
			os.Exit(2)
		}
		args = args[2:]
	}
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "hook run: usage: yakos hook run [--impl go|bash|hybrid] <name>")
		os.Exit(1)
	}
	name := args[0]

	entry, ok := lookupEntry(name)
	if !ok && override != "" {
		// An explicit tier request for a hook with no Go implementation must
		// not pass silently: fail closed, consistent with refresh preflight.
		fmt.Fprintf(os.Stderr, "yakos hook run --impl %s: no Go implementation registered for hook %q (see 'yakos hook list'); refusing to fail open\n", override, name)
		os.Exit(2)
	}
	if !ok {
		// Unknown hook: fail open (exit 0) with a one-line diagnostic,
		// matching every bash hook's own posture toward inputs it can't
		// make sense of (no registered hook means nothing to block on).
		fmt.Fprintf(os.Stderr, "yakos hook run: unknown hook %q (try 'yakos hook list')\n", name)
		os.Exit(0)
	}

	workCurrentDir, projectDir := resolveHookWorkDirs()

	data, readErr := io.ReadAll(os.Stdin)
	if readErr != nil {
		failDegraded(name, entry, workCurrentDir, fmt.Sprintf("reading stdin: %v", readErr))
		return
	}

	in, decodeErr := hookio.DecodeBytes(data)
	if decodeErr != nil {
		failDegraded(name, entry, workCurrentDir, degradedReason(decodeErr))
		return
	}
	in.Env = snapshotEnv()

	stateDir := statepath.Dir()

	cfg := registry.Config{
		WorkCurrentDir: workCurrentDir,
		ProjectDir:     projectDir,
		StateDir:       stateDir,
		HooksDir:       resolveHooksDir(yakosRoot),
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
	r.ModeOverride = override
	r.FailClosed = entry.FailClosed

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

// degradedReason maps a hookio decode error to the exact reason text
// lib/hooks/lib/hook-input.sh's hi_init uses, so the stderr message and the
// NDJSON record are byte-identical to bash's.
func degradedReason(err error) string {
	switch {
	case errors.Is(err, hookio.ErrEmptyStdin):
		return "stdin was provided but empty (0 bytes) \u2014 expected a JSON hook payload"
	case errors.Is(err, hookio.ErrNotObject):
		return "stdin parsed as JSON but is not a JSON object (hook payloads are always an object)"
	case errors.Is(err, hookio.ErrNotJSON):
		return "stdin did not parse as valid JSON"
	}
	return err.Error()
}

// failDegraded handles undecodable stdin / read errors for `hook run`,
// mirroring lib/hooks/lib/hook-input.sh's _hi_fail_or_warn EXACTLY —
// including its log record and stderr text:
//
//   - FailClosed hooks, in order:
//     1. YAKOS_HOOKS_FAIL_OPEN=1 -> WARN record + 2 stderr lines, exit 0.
//     2. a hook-bypass.md entry for this hook with **Scope:**
//     degraded-input (EXACT match) -> WARN record + 1 stderr line, exit 0.
//     3. otherwise -> BLOCK record + 6 stderr lines, exit 2.
//   - Telemetry hooks (FailClosed=false) WARN on stderr and pass (exit 0),
//     matching the bash README's "no-block policy for telemetry hooks".
//
// bash continues into the hook body with empty input after a pass-through;
// every fail-closed hook's tool gate exits 0 on empty input, so exiting 0
// here is equivalent (budget-guard alone would go on to count the call).
func failDegraded(name string, entry registry.Entry, workCurrentDir, reason string) {
	logDegraded := func(severity, decision, msg string) {
		// Best effort, like bash's `ho_log ... 2>/dev/null || true`.
		_ = hooklog.Append(workCurrentDir, hooklog.Entry{
			Hook:     name,
			Severity: severity,
			Decision: decision,
			Reason:   msg,
			Agent:    "lead",
		}, time.Now())
	}

	if !entry.FailClosed {
		fmt.Fprintf(os.Stderr, "%s: WARN \u2014 %s. This hook is degraded for this event (jq unavailable or stdin unparseable); treating input as empty.\n", name, reason)
		os.Exit(0)
	}

	if os.Getenv("YAKOS_HOOKS_FAIL_OPEN") == "1" {
		logDegraded("WARN", "pass", fmt.Sprintf("degraded input (%s) but YAKOS_HOOKS_FAIL_OPEN=1 override active", reason))
		fmt.Fprintf(os.Stderr, "%s: WARN \u2014 degraded input (%s), but YAKOS_HOOKS_FAIL_OPEN=1 is set; passing through.\n", name, reason)
		fmt.Fprintf(os.Stderr, "%s: this is an emergency override \u2014 unset it once jq/stdin are fixed.\n", name)
		os.Exit(0)
	}

	if degradedBypassActive(workCurrentDir, name) {
		logDegraded("WARN", "pass", fmt.Sprintf("degraded input (%s) but hook-bypass.md override active (scope: degraded-input)", reason))
		fmt.Fprintf(os.Stderr, "%s: WARN \u2014 degraded input (%s), but a hook-bypass.md entry for '%s' scoped to 'degraded-input' is active; passing through.\n", name, reason, name)
		os.Exit(0)
	}

	logDegraded("BLOCK", "block", "degraded input, failing closed: "+reason)
	fmt.Fprintf(os.Stderr, "%s: BLOCKED \u2014 cannot safely evaluate this tool call (%s).\n", name, reason)
	fmt.Fprintf(os.Stderr, "%s: this hook enforces a security control and refuses to fail open.\n", name)
	fmt.Fprintf(os.Stderr, "%s: fix jq on PATH / the caller's JSON payload, then retry.\n", name)
	fmt.Fprintf(os.Stderr, "%s: emergency overrides: export YAKOS_HOOKS_FAIL_OPEN=1, or add a\n", name)
	fmt.Fprintf(os.Stderr, "%s: work/current/hook-bypass.md entry with **Hook:** %s and\n", name, name)
	fmt.Fprintf(os.Stderr, "%s: **Scope:** degraded-input.\n", name)
	os.Exit(2)
}

// degradedBypassActive mirrors ho_check_bypass_exact "<name>" "degraded-input":
// the operator must have opted in with the literal sentinel scope (a
// substring like api/degraded-input.go does not count, R3-2).
func degradedBypassActive(workCurrentDir, name string) bool {
	if workCurrentDir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(workCurrentDir, "hook-bypass.md")) //nolint:gosec
	if err != nil {
		return false
	}
	return hookbypass.CheckExact(string(data), name, "degraded-input")
}

// resolveHooksDir is the Go analogue of bash's
// HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)" for the framework's hook
// scripts: <yakosRoot>/lib/hooks when it exists (a source checkout, or a
// release layout), else the materialized bare-install copy
// (install.MaterializedLibDir), else <yakosRoot>/lib/hooks anyway. The
// result is symlink-resolved (pwd -P) so it is byte-identical to what a
// bash hook run out of the same directory reports. It never materializes
// anything: this runs on every hook invocation.
func resolveHooksDir(yakosRoot string) string {
	candidates := []string{filepath.Join(yakosRoot, "lib", "hooks")}
	if home := os.Getenv("HOME"); home != "" {
		candidates = append(candidates,
			filepath.Join(install.MaterializedLibDir(home, version.Version), "lib", "hooks"))
	}
	pick := candidates[0]
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			pick = c
			break
		}
	}
	if abs, err := filepath.Abs(pick); err == nil {
		pick = abs
	}
	if resolved, err := filepath.EvalSymlinks(pick); err == nil {
		pick = resolved
	}
	return pick
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
		status := "go-unverified"
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
