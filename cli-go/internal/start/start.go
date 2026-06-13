// Package start implements the Go port of `yakos start`.
//
// `yakos start` resolves a project by name, selects a runtime, prints a
// preflight banner (including the lead-dispatch-discipline reminder), writes
// audit-log entries to the project's session history and the global launch log,
// materializes agents for the chosen runtime, and then exec's the session CLI.
//
// The package is split into pure domain functions (name inference, banner
// composition, audit-log writing) and the top-level Run function that orchestrates
// them.  The exec step uses syscall.Exec so the Go process is replaced entirely
// by the runtime CLI, matching the bash `exec` behaviour.
//
// Design constraints:
//   - Read-only until the audit-log write (immutable domain layer, mutable I/O
//     layer).
//   - Exec step is behind the ExecFn field in Config so tests can capture the
//     would-be command without spawning a real process.
//   - Kanban web-UI auto-serve is omitted from Phase 1 (rank 41 deferred; the
//     bash path handles it when YAKOS_KANBAN_AUTOSERVE is set).
//   - Workspace hook wiring (the jq-based settings.json merge) is omitted from
//     the Go port; the bash entry-point still runs on exec. Tests that exercise
//     the banner validate all observable outputs.
package start

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// KnownRuntimes is the ordered list of built-in runtime IDs. Mirrors
// YK_RT_KNOWN_BUILTIN in runtime-resolve.sh.
var KnownRuntimes = []string{"claude", "claude-sdk", "codex", "agy", "antigravity-sdk", "gemini"}

// Config controls all inputs to the start command.
type Config struct {
	// Name is the agent-control project name.
	// If empty, InferName is called to derive it from CWD / env.
	Name string

	// YakosRoot is the absolute path to the yakos repo root (used for agent
	// materialization).
	YakosRoot string

	// HomeDir defaults to os.Getenv("HOME").
	HomeDir string

	// Env is the environment map used for resolution.  If nil, os.Getenv is used.
	// Tests inject a synthetic map here so they don't pollute the real environment.
	Env map[string]string

	// Now is the current time for the audit-log timestamp.  Defaults to time.Now().
	Now time.Time

	// --- runtime flags ---

	// Runtime overrides the default runtime selection.
	Runtime string

	// Safe, when true, uses the "safe" (non-bypass) permission mode.
	Safe bool

	// AllowRoot, when true, sets IS_SANDBOX=1 for root-user container dispatch.
	AllowRoot bool

	// NoAgents, when true, skips agent materialization.
	NoAgents bool

	// DryRun prints the would-be exec command and exits without launching.
	DryRun bool

	// PrintAgents prints the composed agent JSON and exits.
	PrintAgents bool

	// --- session passthrough flags ---

	// Continue resumes the most recent claude session (claude-only).
	Continue bool

	// Resume is a session ID to resume (claude / codex).
	Resume string

	// Fork, when true, forks the session (codex fork / claude --fork-session).
	Fork bool

	// IDE attaches to the IDE (claude-only).
	IDE bool

	// Bare enables minimal mode (claude-only).
	Bare bool

	// StrictMCP, when true, passes --strict-mcp-config (claude-only).
	StrictMCP bool

	// Model is the model alias or tier to forward to the runtime.
	Model string

	// Passthrough holds extra flags forwarded verbatim to the runtime CLI.
	Passthrough []string

	// NoREPL, when true, skips the syscall.Exec step (the REPL launch)
	// after printing the preflight banner.  The caller is responsible for
	// starting the web console (typically via runServe).  Alias: --web.
	NoREPL bool

	// ConsoleAddr is the address where the unified console is (or will be)
	// bound.  Defaults to "127.0.0.1:7890" when empty.  Used to compose
	// the Web console URL shown in the preflight banner.
	ConsoleAddr string

	// ConsoleProbeFn, if non-nil, is called to detect whether a console
	// daemon is already listening on addr.  Returns true when reachable.
	// When nil, net.DialTimeout is used with a 200 ms timeout.
	// Tests inject a stub to keep the suite hermetic (no real listener).
	ConsoleProbeFn func(addr string) bool

	// ConsoleToken is the bearer token for the web console URL.  When
	// empty the banner shows the URL without a token fragment (useful in
	// dry-run mode before a state dir exists).
	ConsoleToken string

	// --- I/O ---

	// Writer is where banner output is written.  Defaults to os.Stdout.
	Writer io.Writer

	// ErrWriter is where warnings are written.  Defaults to os.Stderr.
	ErrWriter io.Writer

	// ExecFn, if non-nil, is called instead of syscall.Exec to launch the
	// runtime.  Tests inject a no-op here to capture the command without
	// spawning a real process.
	// Signature: execFn(argv0 string, argv []string, env []string) error
	ExecFn func(argv0 string, argv []string, env []string) error

	// RestoreCwdOnReturn, when true, saves the process cwd before the
	// production-path os.Chdir(controlDir) call and restores it via a deferred
	// call before Run returns.  Default false preserves the historical behaviour
	// (cwd is left as controlDir after exec) which is correct for the CLI exec
	// path where the process is replaced entirely by syscall.Exec anyway.
	//
	// Long-running daemon processes that invoke start.Run repeatedly (e.g. via
	// the dispatch path) must set this to true so each invocation does not
	// permanently pollute the daemon's working directory.
	RestoreCwdOnReturn bool
}

// Banner holds all fields computed during the preflight phase.  It is
// returned from Run so callers can inspect the result (useful in tests).
type Banner struct {
	// Project is the resolved project name.
	Project string

	// ProjectRepo is the absolute path to the project repo.
	ProjectRepo string

	// ControlDir is the absolute path to ~/agent-control/<project>.
	ControlDir string

	// Runtime is the selected runtime ID.
	Runtime string

	// Capabilities is the runtime capabilities string reported to the banner.
	Capabilities string

	// CLIOk is true when the runtime CLI binary was found on PATH.
	CLIOk bool

	// AuthOk is true when runtime auth appears to be configured.
	AuthOk bool

	// PermMode is "bypass" or "safe".
	PermMode string

	// AgentCount is the number of agents composed (0 when --no-agents).
	AgentCount int

	// ModeFlags is a space-separated list of enabled mode flags for the banner.
	ModeFlags string

	// DryRunCmd is populated only when DryRun==true: the command that would be
	// exec'd, formatted as a shell-safe string.
	DryRunCmd string

	// AuditEvent is the JSON-serialised session_launched event.
	AuditEvent string

	// WebConsoleURL is the fully-composed URL for the unified web console,
	// including the token fragment.  Empty when no token was available.
	WebConsoleURL string

	// WebConsoleRunning is true when the probe detected an active console
	// daemon on the console address at banner time.
	WebConsoleRunning bool
}

// Run executes the start command and returns the composed banner.
//
// When DryRun is false and ExecFn is nil, Run calls syscall.Exec to replace the
// current process with the runtime CLI.  It only returns when exec itself fails
// (in which case an error is returned) or when DryRun/PrintAgents mode is active.
func Run(cfg Config) (*Banner, error) {
	env := cfg.Env
	if env == nil {
		env = osEnvMap()
	}

	home := cfg.HomeDir
	if home == "" {
		home = envGet(env, "HOME")
	}
	if home == "" {
		home = "/tmp"
	}

	now := cfg.Now
	if now.IsZero() {
		now = time.Now()
	}

	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}
	ew := cfg.ErrWriter
	if ew == nil {
		ew = os.Stderr
	}

	// ---- name resolution -------------------------------------------------------

	name := cfg.Name
	if name == "" {
		var err error
		name, err = inferName(home, env)
		if err != nil {
			return nil, fmt.Errorf("start: %w", err)
		}
	}
	if name == "" {
		return nil, fmt.Errorf("start: cannot infer project name from cwd; pass <name> explicitly")
	}
	if err := validateName(name); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	controlDir := filepath.Join(home, "agent-control", name)
	if _, err := os.Stat(controlDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("start: project %q not bootstrapped — run 'yakos init %s --project <path>' first", name, name)
	}

	projectPathFile := filepath.Join(controlDir, ".project-path")
	projectRepo, err := readFirstLine(projectPathFile)
	if err != nil {
		return nil, fmt.Errorf("start: %s missing — re-run 'yakos init' to repair", projectPathFile)
	}
	projectRepo = strings.TrimSpace(projectRepo)
	if _, err := os.Stat(projectRepo); os.IsNotExist(err) {
		return nil, fmt.Errorf("start: project repo not found at %s", projectRepo)
	}

	// ---- runtime selection -----------------------------------------------------

	runtime := cfg.Runtime
	if runtime == "" {
		runtime = resolveDefaultRuntime(home, env)
	}
	if !isKnownRuntime(runtime) {
		return nil, fmt.Errorf("start: unknown runtime %q (known: %s)", runtime, strings.Join(KnownRuntimes, " "))
	}

	caps := runtimeCapabilities(runtime)
	cliOk := checkRuntimeCLI(runtime)
	authOk := true
	if cliOk {
		authOk = checkRuntimeAuth(runtime, env)
	}

	// Warn on auth not configured even if not bailing.
	if !authOk && !cfg.DryRun && !cfg.PrintAgents {
		_, _ = fmt.Fprintf(ew, "WARN: %q auth not detected; the runtime may prompt or fail. Run 'yakos auth login %s' to fix.\n", runtime, runtime)
	}

	// Skip PATH check when a test has injected ExecFn, when --no-repl is set
	// (no REPL will be exec'd), or when in dry-run / print-agents mode.
	if !cfg.DryRun && !cfg.PrintAgents && !cfg.NoREPL && cfg.ExecFn == nil {
		if !cliOk {
			return nil, fmt.Errorf("start: %q CLI not on PATH. Install it, then retry. (--dry-run works without the CLI installed.)", runtime)
		}
	}

	permMode := "bypass"
	if cfg.Safe {
		permMode = "safe"
	}

	// ---- agent count -----------------------------------------------------------

	agentCount := 0
	if !cfg.NoAgents {
		agentCount = countAgents(cfg.YakosRoot, projectRepo, runtime)
	}

	// ---- print-agents mode (early exit) ----------------------------------------

	if cfg.PrintAgents {
		if err := printAgents(cfg.YakosRoot, projectRepo, runtime, w); err != nil {
			return nil, fmt.Errorf("start: print-agents: %w", err)
		}
		return &Banner{
			Project:      name,
			ProjectRepo:  projectRepo,
			ControlDir:   controlDir,
			Runtime:      runtime,
			Capabilities: caps,
			CLIOk:        cliOk,
			AuthOk:       authOk,
			PermMode:     permMode,
			AgentCount:   agentCount,
		}, nil
	}

	// ---- mode flags label ------------------------------------------------------

	modeFlags := buildModeFlags(cfg)

	// ---- web console URL (best-effort probe) -----------------------------------

	consoleAddr := cfg.ConsoleAddr
	if consoleAddr == "" {
		consoleAddr = "127.0.0.1:7890"
	}
	probeFn := cfg.ConsoleProbeFn
	if probeFn == nil {
		probeFn = defaultConsoleProbe
	}
	consoleRunning := probeFn(consoleAddr)
	consoleURL := buildConsoleURL(consoleAddr, cfg.ConsoleToken)

	// ---- preflight banner ------------------------------------------------------

	printBanner(w, name, projectRepo, controlDir, runtime, caps, cliOk, authOk, permMode, cfg.AllowRoot, agentCount, cfg.NoAgents, modeFlags, consoleURL, consoleRunning, cfg.NoREPL)

	// ---- soft-degrade warnings --------------------------------------------------

	if cfg.Continue && runtime != "claude" {
		_, _ = fmt.Fprintf(ew, "NOTE: %q does not support --continue (claude-specific); flag will be ignored.\n", runtime)
	}
	if cfg.IDE && runtime != "claude" {
		_, _ = fmt.Fprintf(ew, "NOTE: --ide is claude-specific; ignored for %s.\n", runtime)
	}
	if cfg.Bare && runtime != "claude" {
		_, _ = fmt.Fprintf(ew, "NOTE: --bare is claude-specific; ignored for %s.\n", runtime)
	}
	if cfg.StrictMCP && runtime != "claude" {
		_, _ = fmt.Fprintf(ew, "NOTE: --strict-mcp is claude-specific; ignored for %s.\n", runtime)
	}

	// ---- assemble extra flags ---------------------------------------------------

	extraFlags := buildExtraFlags(cfg, projectRepo)

	// ---- dry-run exit ----------------------------------------------------------

	var dryRunCmd string
	if cfg.DryRun {
		if cfg.NoREPL {
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, "Dry run — --no-repl: would start web console daemon (yakos serve) at "+consoleAddr)
			_, _ = fmt.Fprintln(w, "  No REPL will be launched.")
		} else {
			dryRunCmd = formatDryRunCmd(runtime, projectRepo, permMode, agentCount, cfg, extraFlags)
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, "Dry run — would exec via runtime '"+runtime+"':")
			_, _ = fmt.Fprintln(w, dryRunCmd)
		}
		return &Banner{
			Project:           name,
			ProjectRepo:       projectRepo,
			ControlDir:        controlDir,
			Runtime:           runtime,
			Capabilities:      caps,
			CLIOk:             cliOk,
			AuthOk:            authOk,
			PermMode:          permMode,
			AgentCount:        agentCount,
			ModeFlags:         modeFlags,
			DryRunCmd:         dryRunCmd,
			WebConsoleURL:     consoleURL,
			WebConsoleRunning: consoleRunning,
		}, nil
	}

	// ---- materialize agents ----------------------------------------------------

	if !cfg.NoAgents {
		if err := materializeAgents(cfg.YakosRoot, projectRepo, runtime, ew); err != nil {
			_, _ = fmt.Fprintf(ew, "WARN: agent materialization for runtime %q returned non-zero: %v\n", runtime, err)
		}
	}

	// ---- audit trail -----------------------------------------------------------

	ts := now.UTC().Format(time.RFC3339)
	auditEvent, _ := buildAuditEvent(ts, name, projectRepo, runtime, permMode, agentCount)

	sessionHistory := filepath.Join(controlDir, "work", "current", ".session-started-history.ndjson")
	appendAuditLine(sessionHistory, auditEvent)

	stateDir := filepath.Join(home, ".yakos-state")
	_ = os.MkdirAll(stateDir, 0755) //nolint:gosec
	launchLog := filepath.Join(stateDir, "launch-log.ndjson")
	appendAuditLine(launchLog, auditEvent)

	// ---- exec runtime ----------------------------------------------------------

	// Build argv for exec: the runtime binary + all its flags.
	argv0, argv, execEnv, err := buildExecArgs(runtime, projectRepo, permMode, agentCount, cfg, extraFlags, env)
	if err != nil {
		return nil, fmt.Errorf("start: could not resolve runtime binary %q: %w", runtime, err)
	}

	banner := &Banner{
		Project:           name,
		ProjectRepo:       projectRepo,
		ControlDir:        controlDir,
		Runtime:           runtime,
		Capabilities:      caps,
		CLIOk:             cliOk,
		AuthOk:            authOk,
		PermMode:          permMode,
		AgentCount:        agentCount,
		ModeFlags:         modeFlags,
		DryRunCmd:         dryRunCmd,
		AuditEvent:        auditEvent,
		WebConsoleURL:     consoleURL,
		WebConsoleRunning: consoleRunning,
	}

	// ---- no-repl mode: skip exec, return for caller to start serve ----------

	if cfg.NoREPL {
		return banner, nil
	}

	execFn := cfg.ExecFn
	if execFn == nil {
		// Production path: cd to the control directory then exec.
		// The chdir is deferred to just before exec so that test code injecting
		// ExecFn is not affected by a cwd side-effect (tests use t.TempDir() paths
		// and would leak cwd state across parallel subtests if chdir ran earlier).
		if cfg.RestoreCwdOnReturn {
			// Long-running callers (daemons) ask us to restore cwd on return so
			// the daemon's working directory is not permanently polluted.
			if origCwd, err := os.Getwd(); err == nil {
				defer func() { _ = os.Chdir(origCwd) }()
			}
		}
		if err := os.Chdir(controlDir); err != nil {
			_, _ = fmt.Fprintf(ew, "WARN: start: could not cd to %s: %v\n", controlDir, err)
		}
		execFn = defaultExec
	}
	if err := execFn(argv0, argv, execEnv); err != nil {
		return banner, fmt.Errorf("start: exec %s: %w", argv0, err)
	}
	return banner, nil
}

// ---- name inference -----------------------------------------------------------

// nameRE validates that a project name contains only alphanumerics, dashes, and underscores.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validateName checks that the project name matches the allowed character set.
func validateName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("<name> must be alphanumeric (with - or _ allowed): got %q", name)
	}
	return nil
}

// inferName mirrors the bash name-inference logic in start.sh.
// It checks: (1) cwd under ~/agent-control/<name>; (2) any project-path file
// whose resolved path matches cwd; (3) YAKOS_PROJECT_NAME env.
func inferName(home string, env map[string]string) (string, error) {
	// Prefer explicit env.
	if n := envGet(env, "YAKOS_PROJECT_NAME"); n != "" {
		return n, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", nil
	}

	acRoot := filepath.Join(home, "agent-control")

	// Case 1: cwd is inside ~/agent-control/<name>.
	if strings.HasPrefix(cwd, acRoot+string(os.PathSeparator)) {
		rest := cwd[len(acRoot)+1:]
		if idx := strings.IndexByte(rest, os.PathSeparator); idx > 0 {
			return rest[:idx], nil
		}
		if rest != "" {
			return rest, nil
		}
	}

	// Case 2: scan all project-path files.
	entries, err := os.ReadDir(acRoot)
	if err != nil {
		return "", nil
	}
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
		real, err := filepath.EvalSymlinks(proj)
		if err != nil {
			real = proj
		}
		if cwd == real || strings.HasPrefix(cwd, real+string(os.PathSeparator)) {
			return e.Name(), nil
		}
	}

	return "", nil
}

// ---- runtime resolution -------------------------------------------------------

// resolveDefaultRuntime checks: (1) YAKOS_RUNTIME env; (2) ~/.yakos-state/default-runtime;
// (3) fallback "claude".
func resolveDefaultRuntime(home string, env map[string]string) string {
	if v := envGet(env, "YAKOS_RUNTIME"); v != "" {
		return v
	}
	drFile := filepath.Join(home, ".yakos-state", "default-runtime")
	if data, err := os.ReadFile(drFile); err == nil {
		if rt := strings.TrimSpace(string(data)); rt != "" {
			return rt
		}
	}
	return "claude"
}

// isKnownRuntime returns true for any built-in runtime ID.
func isKnownRuntime(rt string) bool {
	for _, k := range KnownRuntimes {
		if k == rt {
			return true
		}
	}
	return false
}

// runtimeCapabilities returns a short capabilities string for the banner.
// Mirrors the yk_rt_capabilities bash function for each adapter.
func runtimeCapabilities(rt string) string {
	switch rt {
	case "claude", "claude-sdk":
		return "interactive,headless,fork-headless,resume"
	case "codex":
		return "interactive,headless"
	case "agy", "antigravity-sdk":
		return "interactive,headless,resume"
	case "gemini":
		return "interactive,headless"
	default:
		return "unknown"
	}
}

// checkRuntimeCLI returns true when the runtime's CLI binary is on PATH.
func checkRuntimeCLI(rt string) bool {
	binary := runtimeBinary(rt)
	_, err := exec.LookPath(binary)
	return err == nil
}

// checkRuntimeAuth returns true when auth appears to be configured for the runtime.
func checkRuntimeAuth(rt string, env map[string]string) bool {
	switch rt {
	case "claude", "claude-sdk":
		// ANTHROPIC_API_KEY env or ~/.claude/credentials.json
		if envGet(env, "ANTHROPIC_API_KEY") != "" {
			return true
		}
		home := envGet(env, "HOME")
		if home == "" {
			return false
		}
		_, err := os.Stat(filepath.Join(home, ".claude", "credentials.json"))
		return err == nil
	case "codex":
		return envGet(env, "OPENAI_API_KEY") != ""
	case "agy", "antigravity-sdk":
		return envGet(env, "GEMINI_API_KEY") != "" || envGet(env, "GOOGLE_API_KEY") != ""
	case "gemini":
		return envGet(env, "GEMINI_API_KEY") != "" || envGet(env, "GOOGLE_API_KEY") != ""
	default:
		return false
	}
}

// runtimeBinary returns the CLI binary name for a runtime ID.
func runtimeBinary(rt string) string {
	switch rt {
	case "claude", "claude-sdk":
		return "claude"
	case "codex":
		return "codex"
	case "agy", "antigravity-sdk", "gemini":
		return "agy"
	default:
		return rt
	}
}

// ---- agent count / materialization -------------------------------------------

// countAgents counts the number of composed agents for the project.
// Returns 0 when the yakos root or project path is not available.
func countAgents(yakosRoot, projectRepo, runtime string) int {
	if yakosRoot == "" || projectRepo == "" {
		return 0
	}
	// Use the agents-compose bash script to count agents.
	// Mirrors: yk_agents_compose "$YAKOS_ROOT" "$PROJECT_REPO" | jq 'length'
	composeScript := filepath.Join(yakosRoot, "cli", "lib", "agents-compose.sh")
	if _, err := os.Stat(composeScript); os.IsNotExist(err) {
		return 0
	}
	cmd := exec.Command("bash", "-c",
		fmt.Sprintf(`. %q && . %q && yk_agents_compose %q %q 2>/dev/null | jq -e 'length' 2>/dev/null || echo 0`,
			filepath.Join(yakosRoot, "cli", "lib", "compat.sh"),
			composeScript,
			yakosRoot,
			projectRepo,
		),
	)
	cmd.Env = append(os.Environ(),
		"YAKOS_ROOT="+yakosRoot,
		"YAKOS_LIB="+filepath.Join(yakosRoot, "cli", "lib"),
	)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	n := 0
	_, _ = fmt.Sscan(strings.TrimSpace(string(out)), &n)
	return n
}

// printAgents writes the composed agent JSON to w.
func printAgents(yakosRoot, projectRepo, runtime string, w io.Writer) error {
	if yakosRoot == "" {
		return fmt.Errorf("YAKOS_ROOT is not set; cannot compose agents")
	}
	composeScript := filepath.Join(yakosRoot, "cli", "lib", "agents-compose.sh")
	cmd := exec.Command("bash", "-c",
		fmt.Sprintf(`. %q && . %q && yk_agents_compose %q %q`,
			filepath.Join(yakosRoot, "cli", "lib", "compat.sh"),
			composeScript,
			yakosRoot,
			projectRepo,
		),
	)
	cmd.Env = append(os.Environ(),
		"YAKOS_ROOT="+yakosRoot,
		"YAKOS_LIB="+filepath.Join(yakosRoot, "cli", "lib"),
	)
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// materializeAgents runs yk_rt_materialize_agents for the chosen runtime.
func materializeAgents(yakosRoot, projectRepo, runtime string, ew io.Writer) error {
	if yakosRoot == "" {
		return nil
	}
	rtScript := filepath.Join(yakosRoot, "cli", "lib", "runtimes", runtime+".sh")
	if _, err := os.Stat(rtScript); os.IsNotExist(err) {
		// Runtime adapter not found; skip silently (matches bash's || true).
		return nil
	}
	cmd := exec.Command("bash", "-c",
		fmt.Sprintf(
			`. %q && . %q && . %q && yk_rt_load %q && yk_rt_materialize_agents %q %q`,
			filepath.Join(yakosRoot, "cli", "lib", "compat.sh"),
			filepath.Join(yakosRoot, "cli", "lib", "agents-compose.sh"),
			filepath.Join(yakosRoot, "cli", "lib", "runtime-resolve.sh"),
			runtime,
			yakosRoot,
			projectRepo,
		),
	)
	cmd.Env = append(os.Environ(),
		"YAKOS_ROOT="+yakosRoot,
		"YAKOS_LIB="+filepath.Join(yakosRoot, "cli", "lib"),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = ew
	return cmd.Run()
}

// ---- banner output -----------------------------------------------------------

// printBanner writes the preflight banner to w.  The output is structurally
// identical to bash start.sh's print_banner() function.
func printBanner(w io.Writer, name, projectRepo, controlDir, runtime, caps string,
	cliOk, authOk bool, permMode string, allowRoot bool, agentCount int, noAgents bool,
	modeFlags, consoleURL string, consoleRunning, noREPL bool) {

	cliStr := "OK"
	if !cliOk {
		cliStr = "NOT FOUND (--dry-run only)"
	}

	authStr := "OK"
	if !authOk {
		authStr = fmt.Sprintf("NOT CONFIGURED (run: yakos auth login %s)", runtime)
	}

	permStr := "bypassPermissions"
	if permMode == "safe" {
		permStr = "default"
	}
	if allowRoot {
		permStr += " (allow-root)"
	}

	agentStr := fmt.Sprintf("%d registered", agentCount)
	if noAgents {
		agentStr += " (--no-agents: suppressed)"
	}

	// Compose the web console line.  When the daemon is already listening we
	// say "(running)"; when it isn't, we give the operator a hint.
	var consoleLine string
	if consoleURL != "" {
		if consoleRunning {
			consoleLine = "  web console:    (running) " + consoleURL
		} else if noREPL {
			// --no-repl: serve will start momentarily — hint is not needed.
			consoleLine = "  web console:    " + consoleURL + "  (starting...)"
		} else {
			consoleLine = "  web console:    " + consoleURL + "  (run 'yakos serve' or 'yakos start --no-repl' to start)"
		}
	}

	lines := []string{
		"yakos start — preflight",
		"  project:        " + name,
		"  repo:           " + projectRepo,
		"  control dir:    " + controlDir,
		"  runtime:        " + runtime + " (" + caps + ")",
		"  cli:            " + cliStr,
		"  auth:           " + authStr,
		"  permission:     " + permStr,
		"  agents:         " + agentStr,
		"  mode flags:     " + modeFlags,
	}
	if consoleLine != "" {
		lines = append(lines, consoleLine)
	}
	lines = append(lines,
		"",
		"  Lead discipline (rule:lead-dispatch-discipline):",
		"    lead = decompose / integrate / supervise. specialists = parallel.",
		"    sequential only when the next task depends on the previous.",
	)

	for _, l := range lines {
		_, _ = fmt.Fprintln(w, l)
	}
}

// buildConsoleURL returns the http URL for the unified console.
// If token is empty, the fragment is omitted.
func buildConsoleURL(addr, token string) string {
	if addr == "" {
		addr = "127.0.0.1:7890"
	}
	u := "http://" + addr + "/"
	if token != "" {
		u += "#token=" + token
	}
	return u
}

// defaultConsoleProbe returns true when a TCP connection to addr succeeds
// within 200 ms, indicating the console daemon is already running.
func defaultConsoleProbe(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// buildModeFlags assembles the mode-flags label shown in the banner.
// Mirrors the inline printf logic in start.sh's print_banner().
func buildModeFlags(cfg Config) string {
	var parts []string
	if cfg.Bare {
		parts = append(parts, "bare")
	}
	if cfg.IDE {
		parts = append(parts, "ide")
	}
	if cfg.Continue {
		parts = append(parts, "continue")
	}
	if cfg.Resume != "" {
		parts = append(parts, "resume="+cfg.Resume)
	}
	if cfg.Fork {
		parts = append(parts, "fork")
	}
	if cfg.Model != "" {
		parts = append(parts, "model="+cfg.Model)
	}
	return strings.Join(parts, " ")
}

// ---- dry-run command formatting ----------------------------------------------

// formatDryRunCmd formats the dry-run command output matching start.sh's dry-run block.
func formatDryRunCmd(runtime, projectRepo, permMode string, agentCount int, cfg Config, extraFlags []string) string {
	var sb strings.Builder

	bypassFlag := "bypassPermissions"
	if permMode == "safe" {
		bypassFlag = "default"
	}

	switch runtime {
	case "claude", "claude-sdk":
		if cfg.AllowRoot {
			sb.WriteString("  IS_SANDBOX=1 ")
		} else {
			sb.WriteString("  ")
		}
		fmt.Fprintf(&sb, "claude --add-dir %s --permission-mode %s",
			shellescape(projectRepo), bypassFlag)
		if agentCount > 0 && !cfg.NoAgents {
			fmt.Fprintf(&sb, " --agents '<%d agents JSON>'", agentCount)
		}
		mcpConfig := filepath.Join(projectRepo, ".mcp.json")
		if _, err := os.Stat(mcpConfig); err == nil {
			fmt.Fprintf(&sb, " --mcp-config %s", shellescape(mcpConfig))
		}

	case "codex":
		fmt.Fprintf(&sb, "  codex --add-dir %s", shellescape(projectRepo))
		if permMode != "safe" {
			sb.WriteString(" --dangerously-bypass-approvals-and-sandbox")
		}
		if agentCount > 0 && !cfg.NoAgents {
			fmt.Fprintf(&sb, "  # + %d agents staged at %s/.codex/agents/yakos-*.toml",
				agentCount, projectRepo)
		}

	case "agy", "antigravity-sdk":
		fmt.Fprintf(&sb, "  agy --include-directories %s", shellescape(projectRepo))
		if permMode != "safe" {
			sb.WriteString(" --approval-mode=yolo")
		}
		if agentCount > 0 && !cfg.NoAgents {
			fmt.Fprintf(&sb, "  # + %d agents staged at %s/.agy/agents/yakos-*.md",
				agentCount, projectRepo)
		}

	case "gemini":
		fmt.Fprintf(&sb, "  gemini --include-directories %s", shellescape(projectRepo))
		if permMode != "safe" {
			sb.WriteString(" --approval-mode=yolo")
		}
		if agentCount > 0 && !cfg.NoAgents {
			fmt.Fprintf(&sb, "  # + %d agents staged at %s/.gemini/agents/yakos-*.md",
				agentCount, projectRepo)
		}

	default:
		fmt.Fprintf(&sb, "  %s", runtime)
	}

	for _, f := range extraFlags {
		sb.WriteString(" " + shellescape(f))
	}

	return sb.String()
}

// ---- extra flags assembly ---------------------------------------------------

// buildExtraFlags assembles the runtime-specific extra flag list.
// Mirrors start.sh's EXTRA_FLAGS assembly block.
func buildExtraFlags(cfg Config, projectRepo string) []string {
	var flags []string
	switch cfg.Runtime {
	case "claude", "claude-sdk":
		if cfg.Model != "" {
			flags = append(flags, "--model", cfg.Model)
		}
		if cfg.Bare {
			flags = append(flags, "--bare")
		}
		if cfg.IDE {
			flags = append(flags, "--ide")
		}
		if cfg.Continue {
			flags = append(flags, "--continue")
		}
		if cfg.Resume != "" {
			flags = append(flags, "--resume", cfg.Resume)
		}
		if cfg.Fork {
			flags = append(flags, "--fork-session")
		}
		if cfg.StrictMCP {
			if _, err := os.Stat(filepath.Join(projectRepo, ".mcp.json")); err == nil {
				flags = append(flags, "--strict-mcp-config")
			}
		}
	case "codex":
		if cfg.Resume != "" {
			flags = append(flags, "resume", cfg.Resume)
		}
	case "agy", "antigravity-sdk", "gemini":
		if cfg.Resume != "" {
			flags = append(flags, "--resume", cfg.Resume)
		}
		if cfg.Model != "" {
			flags = append(flags, "--model", cfg.Model)
		}
	}
	flags = append(flags, cfg.Passthrough...)
	return flags
}

// ---- exec args assembly -----------------------------------------------------

// buildExecArgs constructs the argv0, argv, and env for the exec call.
// Returns an error if the runtime binary cannot be found.
func buildExecArgs(runtime, projectRepo, permMode string, agentCount int, cfg Config, extraFlags []string, env map[string]string) (string, []string, []string, error) {
	binary := runtimeBinary(runtime)
	var argv0 string
	if cfg.ExecFn != nil {
		// Test injected ExecFn — skip PATH resolution; use the bare binary name
		// as argv0 so tests can assert on it.
		argv0 = binary
	} else {
		resolved, err := exec.LookPath(binary)
		if err != nil {
			return "", nil, nil, fmt.Errorf("binary %q not on PATH: %w", binary, err)
		}
		argv0 = resolved
	}

	var argv []string
	bypassFlag := "bypassPermissions"
	if permMode == "safe" {
		bypassFlag = "default"
	}

	switch runtime {
	case "claude", "claude-sdk":
		argv = append(argv, binary)
		argv = append(argv, "--add-dir", projectRepo)
		argv = append(argv, "--permission-mode", bypassFlag)
		// Agents would be injected here via --agents JSON (materialized already).
	case "codex":
		argv = append(argv, binary)
		argv = append(argv, "--add-dir", projectRepo)
		if permMode != "safe" {
			argv = append(argv, "--dangerously-bypass-approvals-and-sandbox")
		}
	case "agy", "antigravity-sdk":
		argv = append(argv, binary)
		argv = append(argv, "--include-directories", projectRepo)
		if permMode != "safe" {
			argv = append(argv, "--approval-mode=yolo")
		}
	case "gemini":
		argv = append(argv, binary)
		argv = append(argv, "--include-directories", projectRepo)
		if permMode != "safe" {
			argv = append(argv, "--approval-mode=yolo")
		}
	default:
		argv = append(argv, binary)
	}

	argv = append(argv, extraFlags...)

	// Build environment for exec.
	execEnv := buildExecEnv(env, cfg.AllowRoot)

	return argv0, argv, execEnv, nil
}

// buildExecEnv constructs the environment slice for exec.
// When AllowRoot is true, IS_SANDBOX=1 is injected (mirrors bash's ALLOW_ROOT export).
func buildExecEnv(env map[string]string, allowRoot bool) []string {
	var result []string
	for k, v := range env {
		result = append(result, k+"="+v)
	}
	if allowRoot {
		// Inject IS_SANDBOX=1 to signal disposable container context.
		result = append(result, "IS_SANDBOX=1")
	}
	return result
}

// ---- audit trail ------------------------------------------------------------

// buildAuditEvent constructs the session_launched JSON event.
func buildAuditEvent(ts, name, repo, runtime, permMode string, agentCount int) (string, error) {
	type auditEvent struct {
		Type           string `json:"type"`
		TS             string `json:"ts"`
		Project        string `json:"project"`
		Repo           string `json:"repo"`
		Runtime        string `json:"runtime"`
		PermissionMode string `json:"permission_mode"`
		AgentCount     int    `json:"agent_count"`
	}
	ev := auditEvent{
		Type:           "session_launched",
		TS:             ts,
		Project:        name,
		Repo:           repo,
		Runtime:        runtime,
		PermissionMode: permMode,
		AgentCount:     agentCount,
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// appendAuditLine appends line+\n to path, creating the file if needed.
// Missing parent directories are not created — mirrors bash's || true pattern.
func appendAuditLine(path, line string) {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(f, line)
	_ = f.Close()
}

// ---- exec wrapper -----------------------------------------------------------

// defaultExec replaces the current process with argv0/argv using syscall.Exec.
// On success it never returns (the process image is replaced).
func defaultExec(argv0 string, argv []string, env []string) error {
	// Import is guarded by build tags in exec_unix.go / exec_windows.go.
	return execSyscall(argv0, argv, env)
}

// ---- help output ------------------------------------------------------------

// PrintHelp writes the help text for `yakos start` to w, matching the
// bash start.sh usage() output.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos start [<name>] [flags] — launch a session for a yakos project.

Resolves the project repo from ~/agent-control/<name>/.project-path,
loads the chosen runtime adapter, materializes agents in the right
format, and exec's the session. <name> is inferred from the cwd if
not supplied.

Runtime selection:
    --runtime <id>        claude (default) | codex | gemini | agy
                          Falls back to YAKOS_RUNTIME env or
                          ~/.yakos-state/default-runtime.

Permission mode:
    --safe                Prompts on (claude: --permission-mode default;
                          codex: default sandbox; gemini: default).
    (default)             bypass — claude bypassPermissions / codex
                          --dangerously-bypass-approvals-and-sandbox /
                          gemini --approval-mode=yolo.
    --allow-root          Opt-in: allow bypass mode when running as root
                          (e.g. inside a container). Has no effect with --safe.

Agent injection:
    --no-agents           Skip materialization (debug; agents won't bind).

Session passthroughs (forwarded to the runtime CLI when supported):
    --continue, -c        claude only — continue most recent.
    --resume <id>         claude / codex (codex resume <id>).
    --fork-session        claude / codex (codex fork).
    --ide                 claude only — auto-attach to IDE.
    --bare                claude only — minimal mode.
    --strict-mcp          claude only — pass --strict-mcp-config.
    --model <alias>       Forward to runtime if it supports a model flag.

Web console:
    --no-repl, --web      Skip the REPL; run preflight then bring up the web
                          console daemon (yakos serve) instead. Blocks in the
                          foreground like 'yakos serve'. Combines preflight +
                          web UI without launching an interactive session.
                          --no-repl --dry-run prints the serve intent without
                          binding.

Inspection:
    --dry-run             Print what would be exec'd; exit 0.
    --print-agents        Print the composed agent JSON; exit 0.
    --                    End of yakos flags; rest passed to runtime CLI.

Examples:
    yakos start                       # auto-detect, claude (default)
    yakos start myapp
    yakos start myapp --runtime codex
    yakos start myapp --runtime gemini --safe
    yakos start myapp --dry-run
    yakos start myapp --allow-root    # container/root bypass mode
    yakos start myapp --no-repl       # web console only, no REPL
    yakos start myapp --web           # same as --no-repl
`)
}

// ---- helpers ----------------------------------------------------------------

// readFirstLine reads and returns the first line of a file.
func readFirstLine(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return "", err
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	return line, nil
}

// shellescape returns a shell-safe quoted version of s.
// This is used only for dry-run display, not for actual exec args.
func shellescape(s string) string {
	if !strings.ContainsAny(s, " \t\n'\"\\()[]{}$`|;&<>*?!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// osEnvMap builds a map from os.Environ().
func osEnvMap() map[string]string {
	env := os.Environ()
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			m[kv[:idx]] = kv[idx+1:]
		}
	}
	return m
}

// envGet retrieves a value from an env map, or "" if absent.
func envGet(env map[string]string, key string) string {
	if env == nil {
		return os.Getenv(key)
	}
	return env[key]
}
