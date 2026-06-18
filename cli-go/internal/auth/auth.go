// Package auth implements the `yakos auth` subcommand.
//
// It manages credentials for yakOS runtimes (claude, codex, agy, gemini).
//
// Subcommands:
//
//	yakos auth status [<runtime>]      — report cli + auth state for one or all runtimes
//	yakos auth login <runtime>         — print login instructions / exec runtime login flow
//	yakos auth logout <runtime>        — best-effort credential removal
//	yakos auth set-default <runtime>   — persist runtime as yakos start default
//
// yakOS itself never stores or rotates credentials — it hands off to the
// runtime's own login/logout commands and reports state.
//
// OS keychain integration (agy / codex token caching) is provided via
// github.com/zalando/go-keyring, which abstracts macOS Keychain Services,
// Linux secret-service (D-Bus), and Windows DPAPI. Keyring operations
// degrade gracefully when the underlying service is unavailable (e.g.
// headless Linux without dbus).
package auth

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bakw00ds/yakos/internal/claudeauth"
)

// Config holds all parameters for the auth subcommand.
type Config struct {
	// HomeDir is the user home directory ($HOME). Defaults to os.UserHomeDir().
	HomeDir string

	// Subcommand is "status", "login", "logout", "set-default", or "".
	Subcommand string

	// Target is the runtime name positional argument (e.g. "claude", "codex").
	// Empty means "all" for status; required for login/logout/set-default.
	Target string

	// AsDefault, when true for login, also sets the default runtime.
	AsDefault bool

	// All, when true for login, walks every installed runtime.
	All bool

	// Writer receives informational stdout output.
	Writer io.Writer

	// ErrWriter receives diagnostic/error stderr output.
	ErrWriter io.Writer

	// ExecFn is the function used to exec a subprocess (e.g. "codex login").
	// Defaults to DefaultExecFn. Injected in tests to avoid spawning processes.
	ExecFn func(name string, args []string) error

	// KeyringFn is the keyring adapter used to read/write/delete tokens.
	// Defaults to the go-keyring backend. Injected in tests for mocking.
	KeyringFn KeyringBackend
}

// Result summarises the outcome of an auth invocation.
type Result struct {
	// Subcommand that ran.
	Subcommand string

	// Runtime that was targeted (empty = all).
	Runtime string
}

// KnownRuntimes lists all supported runtime identifiers in display order.
var KnownRuntimes = []string{
	"claude",
	"claude-sdk",
	"codex",
	"gemini",
	"agy",
	"antigravity-sdk",
}

// IsKnown returns true when id is in KnownRuntimes.
func IsKnown(id string) bool {
	for _, r := range KnownRuntimes {
		if r == id {
			return true
		}
	}
	return false
}

// Run is the entry-point for `yakos auth`. It dispatches to the appropriate
// subcommand handler.
func Run(cfg Config) (*Result, error) {
	// Apply defaults.
	if cfg.HomeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("auth: cannot resolve home dir: %w", err)
		}
		cfg.HomeDir = home
	}
	if cfg.ExecFn == nil {
		cfg.ExecFn = DefaultExecFn
	}
	if cfg.KeyringFn == nil {
		cfg.KeyringFn = defaultKeyringBackend()
	}
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}

	switch cfg.Subcommand {
	case "", "help":
		PrintHelp(cfg.Writer)
		return &Result{Subcommand: "help"}, nil
	case "status":
		return runStatus(cfg)
	case "login":
		return runLogin(cfg)
	case "logout":
		return runLogout(cfg)
	case "set-default":
		return runSetDefault(cfg)
	default:
		return nil, fmt.Errorf("auth: unknown subcommand %q (try 'yakos auth --help')", cfg.Subcommand)
	}
}

// PrintHelp writes the usage text matching the bash auth.sh usage() output.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos auth <subcommand> [args...]

Subcommands:
  status [<runtime>]      Report cli + auth state. Defaults to all known runtimes.
  login <runtime>         Run the runtime's login flow:
                          - claude:  prints '/login' instruction + opens claude
                          - codex:   exec 'codex login'
                          - gemini:  prints OAuth / API key options
                          With --as-default, also persist the runtime as the
                          yakos start default.
  logout <runtime>        Best-effort credential removal:
                          - claude:  unset ANTHROPIC_API_KEY hint; remove ~/.claude/auth.json
                          - codex:   exec 'codex logout' if the binary supports it
                          - gemini:  point at gemini's logout flow
  set-default <runtime>   Persist the runtime as yakos start's default
                          (writes ~/.yakos-state/default-runtime).

Examples:
  yakos auth status
  yakos auth login codex --as-default
  yakos auth set-default claude
`)
}

// ---- status -----------------------------------------------------------------

// RuntimeStatus captures the auth + cli state for one runtime.
type RuntimeStatus struct {
	ID          string
	CLIState    string // "OK" | "missing"
	CLIHint     string
	AuthState   string // "OK" | "not configured"
	AuthHint    string
	IsDefault   bool
	AdapterNote string // printed when adapter not shipped yet
	Caps        string
}

// runStatus prints runtime auth state for all or one runtime.
func runStatus(cfg Config) (*Result, error) {
	_, _ = fmt.Fprintf(cfg.Writer, "yakos auth — runtime status\n\n")

	defaultRuntime := readDefaultRuntime(cfg)

	if cfg.Target != "" {
		if !IsKnown(cfg.Target) {
			return nil, fmt.Errorf("auth status: unknown runtime %q", cfg.Target)
		}
		s := checkRuntime(cfg.Target, defaultRuntime, cfg)
		printRuntimeStatus(cfg.Writer, s)
		return &Result{Subcommand: "status", Runtime: cfg.Target}, nil
	}

	for _, id := range KnownRuntimes {
		s := checkRuntime(id, defaultRuntime, cfg)
		printRuntimeStatus(cfg.Writer, s)
	}
	return &Result{Subcommand: "status"}, nil
}

// checkRuntime probes CLI presence and auth state for a runtime.
func checkRuntime(id, defaultRuntime string, cfg Config) RuntimeStatus {
	s := RuntimeStatus{
		ID:        id,
		CLIState:  "missing",
		AuthState: "not configured",
		IsDefault: id == defaultRuntime,
		Caps:      runtimeCaps(id),
	}

	if !cliPresent(id) {
		s.CLIHint = cliInstallHint(id)
		return s
	}
	s.CLIState = "OK"

	if checkAuth(id, cfg) {
		s.AuthState = "OK"
	} else {
		s.AuthHint = fmt.Sprintf("run: yakos auth login %s", id)
	}
	return s
}

// printRuntimeStatus formats one runtime block matching the bash output.
func printRuntimeStatus(w io.Writer, s RuntimeStatus) {
	defaultMarker := ""
	if s.IsDefault {
		defaultMarker = " (default)"
	}
	_, _ = fmt.Fprintf(w, "  %-8s%s\n", s.ID, defaultMarker)

	if s.AdapterNote != "" {
		_, _ = fmt.Fprintf(w, "    cli:    %-15s %s\n", "n/a", "(adapter not yet shipped in this yakOS version)")
		_, _ = fmt.Fprintf(w, "    auth:   %-15s\n", "n/a")
		_, _ = fmt.Fprintf(w, "    caps:   (none — adapter pending)\n")
		_, _ = fmt.Fprintln(w)
		return
	}

	_, _ = fmt.Fprintf(w, "    cli:    %-15s %s\n", s.CLIState, s.CLIHint)
	_, _ = fmt.Fprintf(w, "    auth:   %-15s %s\n", s.AuthState, s.AuthHint)
	_, _ = fmt.Fprintf(w, "    caps:   %s\n", s.Caps)
	_, _ = fmt.Fprintln(w)
}

// ---- login ------------------------------------------------------------------

func runLogin(cfg Config) (*Result, error) {
	if cfg.All {
		return runLoginAll(cfg)
	}

	if cfg.Target == "" {
		return nil, errors.New("auth login: <runtime> required (claude|claude-sdk|codex|gemini|agy|antigravity-sdk), or pass --all")
	}
	if !IsKnown(cfg.Target) {
		return nil, fmt.Errorf("auth login: unknown runtime %q", cfg.Target)
	}

	// SDK adapters delegate their login UX to the underlying CLI.
	targetForLogin := cfg.Target
	switch cfg.Target {
	case "claude-sdk":
		logLine(cfg.ErrWriter, "claude-sdk: bundles Claude Code CLI; using claude auth flow")
		targetForLogin = "claude"
	case "antigravity-sdk":
		logLine(cfg.ErrWriter, "antigravity-sdk: shares Google auth posture; using agy auth flow")
		logLine(cfg.ErrWriter, "antigravity-sdk: SDK additionally reads GEMINI_API_KEY directly")
		targetForLogin = "agy"
	}

	if !cliPresent(targetForLogin) {
		return nil, fmt.Errorf("auth login: %q CLI not on PATH; install it first", cfg.Target)
	}

	if err := printLoginInstructions(cfg.Writer, targetForLogin, cfg.ExecFn); err != nil {
		return nil, err
	}

	if cfg.AsDefault {
		if err := writeDefaultRuntime(cfg, cfg.Target); err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintf(cfg.Writer, "\ndefault runtime set to: %s\n", cfg.Target)
	}

	return &Result{Subcommand: "login", Runtime: cfg.Target}, nil
}

// runLoginAll walks every installed runtime sequentially.
func runLoginAll(cfg Config) (*Result, error) {
	if cfg.Target != "" {
		return nil, errors.New("auth login --all: do not also pass a runtime name")
	}
	_, _ = fmt.Fprintln(cfg.Writer, "yakos auth login --all")
	_, _ = fmt.Fprintln(cfg.Writer, "  walks every installed runtime; uninstalled CLIs are skipped.")
	_, _ = fmt.Fprintln(cfg.Writer)

	nOK, nFail, nSkip := 0, 0, 0
	for _, id := range KnownRuntimes {
		switch id {
		case "claude-sdk", "antigravity-sdk":
			_, _ = fmt.Fprintf(cfg.Writer, "  %s: skip (shares credentials with bundled CLI; covered by sibling)\n", id)
			nSkip++
			continue
		case "gemini":
			_, _ = fmt.Fprintf(cfg.Writer, "  %s: skip (deprecated; use 'yakos auth login agy' instead)\n", id)
			nSkip++
			continue
		}
		if !cliPresent(id) {
			_, _ = fmt.Fprintf(cfg.Writer, "  %s: SKIP (CLI not installed)\n", id)
			nSkip++
			continue
		}
		if checkAuth(id, cfg) {
			_, _ = fmt.Fprintf(cfg.Writer, "  %s: OK (already authed)\n", id)
			nOK++
			continue
		}
		_, _ = fmt.Fprintln(cfg.Writer)
		_, _ = fmt.Fprintf(cfg.Writer, "  --- logging into %s ---\n", id)
		subCfg := cfg
		subCfg.Target = id
		subCfg.All = false
		_, err := runLogin(subCfg)
		if err != nil {
			_, _ = fmt.Fprintf(cfg.Writer, "  %s: FAILED (see output above)\n", id)
			nFail++
		} else {
			_, _ = fmt.Fprintf(cfg.Writer, "  %s: OK\n", id)
			nOK++
		}
	}
	_, _ = fmt.Fprintln(cfg.Writer)
	_, _ = fmt.Fprintf(cfg.Writer, "summary: %d ok, %d skipped, %d failed\n", nOK, nSkip, nFail)
	if nFail > 0 {
		return nil, fmt.Errorf("auth login --all: %d runtime(s) failed", nFail)
	}
	return &Result{Subcommand: "login"}, nil
}

// printLoginInstructions prints per-runtime login guidance or exec's the login command.
func printLoginInstructions(w io.Writer, id string, execFn func(string, []string) error) error {
	switch id {
	case "claude":
		_, _ = fmt.Fprint(w, `claude does not have a non-interactive login command. Two options:

  1. Set the env var:
        export ANTHROPIC_API_KEY="sk-ant-..."

  2. OAuth flow inside a claude session:
        claude   # then type '/login' and follow the browser flow.

After either, run 'yakos auth status claude' to verify.
`)
	case "codex":
		_, _ = fmt.Fprintln(w, "Launching 'codex login'...")
		return execFn("codex", []string{"login"})
	case "gemini":
		_, _ = fmt.Fprint(w, `gemini-cli supports three auth paths:

  1. OAuth (free tier, recommended for personal use):
        gemini   # interactive auth on first launch

  2. API key (Google AI Studio):
        export GEMINI_API_KEY="..."
        # Or persist via gemini's settings.

  3. Vertex AI (enterprise):
        gcloud auth application-default login
        export GOOGLE_GENAI_USE_VERTEXAI=true

After configuring one, run 'yakos auth status gemini' to verify.

NOTE: Gemini CLI stops serving requests 2026-06-18. Migrate to agy:
      yakos auth login agy --as-default
`)
	case "agy":
		_, _ = fmt.Fprint(w, `agy (Antigravity CLI) uses Google OAuth via system keyring on first
run. yakOS doesn't drive the OAuth flow — agy does, interactively.

  1. Run agy once interactively:
        agy
        # Browser opens; complete Google OAuth.
        # Close the TUI when done (the credentials are now cached).

  2. (Optional) headless / SSH / CI:
        export ANTIGRAVITY_API_KEY="..."

After either, run 'yakos auth status agy' to verify.
Credentials live in your keychain + ~/.gemini/antigravity-cli/.
`)
	default:
		return fmt.Errorf("auth login: no login flow defined for %q", id)
	}
	return nil
}

// ---- logout -----------------------------------------------------------------

func runLogout(cfg Config) (*Result, error) {
	if cfg.Target == "" {
		return nil, errors.New("auth logout: <runtime> required")
	}
	if !IsKnown(cfg.Target) {
		return nil, fmt.Errorf("auth logout: unknown runtime %q", cfg.Target)
	}

	var err error
	switch cfg.Target {
	case "claude", "claude-sdk":
		err = logoutClaude(cfg)
	case "codex":
		err = logoutCodex(cfg)
	case "gemini":
		err = logoutGemini(cfg)
	case "agy", "antigravity-sdk":
		err = logoutAgy(cfg)
	}
	if err != nil {
		return nil, err
	}
	return &Result{Subcommand: "logout", Runtime: cfg.Target}, nil
}

func logoutClaude(cfg Config) error {
	if cfg.Target == "claude-sdk" {
		logLine(cfg.ErrWriter, "claude-sdk: shares credentials with claude (bundled CLI); routing logout to claude")
	}
	authFile := filepath.Join(cfg.HomeDir, ".claude", "auth.json")
	if _, err := os.Stat(authFile); err == nil {
		if err2 := os.Remove(authFile); err2 != nil {
			return fmt.Errorf("auth logout claude: remove auth.json: %w", err2)
		}
		_, _ = fmt.Fprintln(cfg.Writer, "removed ~/.claude/auth.json")
	} else {
		_, _ = fmt.Fprintln(cfg.Writer, "no claude auth.json found")
	}
	_, _ = fmt.Fprintln(cfg.Writer, "if ANTHROPIC_API_KEY is set in your shell rc, unset it manually.")
	return nil
}

func logoutCodex(cfg Config) error {
	// Try exec 'codex logout' if present.
	if cliPresent("codex") {
		if err := cfg.ExecFn("codex", []string{"logout"}); err != nil {
			logLine(cfg.ErrWriter, "codex logout returned non-zero (may not be supported in this version)")
		}
	}
	// Remove auth.json from CODEX_HOME (default: ~/.codex).
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(cfg.HomeDir, ".codex")
	}
	authFile := filepath.Join(codexHome, "auth.json")
	if _, err := os.Stat(authFile); err == nil {
		if err2 := os.Remove(authFile); err2 != nil {
			return fmt.Errorf("auth logout codex: remove auth.json: %w", err2)
		}
		_, _ = fmt.Fprintf(cfg.Writer, "removed %s\n", authFile)
	}
	return nil
}

func logoutGemini(cfg Config) error {
	_, _ = fmt.Fprint(cfg.Writer, `gemini-cli stores OAuth state in its own config dir. To clear:
    rm -rf ~/.gemini/auth.json    # if present
    unset GEMINI_API_KEY
    unset GOOGLE_GENAI_USE_VERTEXAI
`)
	return nil
}

func logoutAgy(cfg Config) error {
	if cfg.Target == "antigravity-sdk" {
		logLine(cfg.ErrWriter, "antigravity-sdk: shares Google credential surface with agy; routing logout to agy")
		logLine(cfg.ErrWriter, "antigravity-sdk: SDK also reads GEMINI_API_KEY directly — unset it separately")
	}
	_, _ = fmt.Fprint(cfg.Writer, `agy stores OAuth state in your system keychain plus
~/.gemini/antigravity-cli/. To fully sign out:
    1. Remove keychain entry (macOS: Keychain Access → search "antigravity"
       or "gemini"; Linux: secret-tool clear; Windows: Credential Manager)
    2. rm -rf ~/.gemini/antigravity-cli/conversations
    3. unset ANTIGRAVITY_API_KEY
    4. unset GEMINI_API_KEY

Or simpler: re-run 'agy' interactively to reauthenticate as a different
account; the keychain entry gets overwritten.
`)
	return nil
}

// ---- set-default ------------------------------------------------------------

func runSetDefault(cfg Config) (*Result, error) {
	if cfg.Target == "" {
		return nil, errors.New("auth set-default: <runtime> required")
	}
	if !IsKnown(cfg.Target) {
		return nil, fmt.Errorf("auth set-default: unknown runtime %q", cfg.Target)
	}
	if err := writeDefaultRuntime(cfg, cfg.Target); err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(cfg.Writer, "default runtime set to: %s (~/.yakos-state/default-runtime)\n", cfg.Target)
	return &Result{Subcommand: "set-default", Runtime: cfg.Target}, nil
}

// ---- default-runtime persistence --------------------------------------------

// readDefaultRuntime reads the current default runtime from
// ~/.yakos-state/default-runtime. Returns "" on any error.
func readDefaultRuntime(cfg Config) string {
	path := defaultRuntimePath(cfg.HomeDir)
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return ""
	}
	return trimNewline(string(data))
}

// writeDefaultRuntime writes the runtime name to ~/.yakos-state/default-runtime.
func writeDefaultRuntime(cfg Config, runtime string) error {
	stateDir := filepath.Join(cfg.HomeDir, ".yakos-state")
	if err := os.MkdirAll(stateDir, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("auth set-default: mkdir %s: %w", stateDir, err)
	}
	path := filepath.Join(stateDir, "default-runtime")
	if err := os.WriteFile(path, []byte(runtime+"\n"), 0644); err != nil { //nolint:gosec
		return fmt.Errorf("auth set-default: write %s: %w", path, err)
	}
	return nil
}

func defaultRuntimePath(homeDir string) string {
	return filepath.Join(homeDir, ".yakos-state", "default-runtime")
}

// ---- runtime probe helpers --------------------------------------------------

// cliPresent returns true when the CLI binary for id is on PATH.
func cliPresent(id string) bool {
	bin := cliName(id)
	if bin == "" {
		return false
	}
	return commandOnPath(bin)
}

// cliName maps a runtime id to its executable name.
func cliName(id string) string {
	switch id {
	case "claude", "claude-sdk":
		return "claude"
	case "codex":
		return "codex"
	case "gemini":
		return "gemini"
	case "agy", "antigravity-sdk":
		return "agy"
	}
	return ""
}

// cliInstallHint returns the install instruction for a missing CLI.
func cliInstallHint(id string) string {
	switch id {
	case "claude":
		return "install: https://docs.claude.com/en/docs/claude-code"
	case "claude-sdk":
		return "install: pip install claude-agent-sdk (python 3.10+)"
	case "codex":
		return "install: npm install -g @openai/codex"
	case "gemini":
		return "install: npm install -g @google/gemini-cli (DEPRECATED 2026-06-18; use agy)"
	case "agy":
		return "install: curl -fsSL https://antigravity.google/cli/install.sh | bash"
	case "antigravity-sdk":
		return "install: pip install google-antigravity (bundles compiled binary)"
	}
	return ""
}

// checkAuth returns true when auth appears to be configured for the runtime.
// For runtimes that store credentials in files (claude, codex), checks the
// flat-file path. For runtimes that use the OS keychain (agy), queries the
// keyring backend provided in cfg. For env-var-based runtimes (gemini) checks
// the known env var.
func checkAuth(id string, cfg Config) bool {
	switch id {
	case "claude", "claude-sdk":
		// Delegate to claudeauth.IsAuthed for the full detection order:
		// ANTHROPIC_API_KEY → ~/.claude/auth.json → ~/.claude.json oauthAccount
		// → ~/.claude/credentials.json. See internal/claudeauth for details.
		return claudeauth.IsAuthed(cfg.HomeDir, os.Getenv("ANTHROPIC_API_KEY"))

	case "codex":
		// Auth is OPENAI_API_KEY env var or ~/.codex/auth.json.
		if os.Getenv("OPENAI_API_KEY") != "" {
			return true
		}
		codexHome := os.Getenv("CODEX_HOME")
		if codexHome == "" {
			codexHome = filepath.Join(cfg.HomeDir, ".codex")
		}
		authFile := filepath.Join(codexHome, "auth.json")
		_, err := os.Stat(authFile)
		return err == nil

	case "gemini":
		// GEMINI_API_KEY or GOOGLE_GENAI_USE_VERTEXAI configures gemini.
		return os.Getenv("GEMINI_API_KEY") != "" || os.Getenv("GOOGLE_GENAI_USE_VERTEXAI") != ""

	case "agy", "antigravity-sdk":
		// ANTIGRAVITY_API_KEY or GEMINI_API_KEY covers headless mode.
		if os.Getenv("ANTIGRAVITY_API_KEY") != "" || os.Getenv("GEMINI_API_KEY") != "" {
			return true
		}
		// Check OS keychain via injected backend. Degrade gracefully on error.
		if cfg.KeyringFn != nil {
			tok, err := cfg.KeyringFn.Get(KeyringService, id)
			if err == nil && tok != "" {
				return true
			}
		}
		// Fall back to ~/.gemini/antigravity-cli/ presence.
		aDir := filepath.Join(cfg.HomeDir, ".gemini", "antigravity-cli")
		_, err := os.Stat(aDir)
		return err == nil
	}
	return false
}

// runtimeCaps returns the capabilities string for a runtime.
func runtimeCaps(id string) string {
	switch id {
	case "claude":
		return "agent-dispatch multi-turn stream-json"
	case "claude-sdk":
		return "agent-dispatch multi-turn stream-json (python)"
	case "codex":
		return "agent-dispatch multi-turn"
	case "gemini":
		return "agent-dispatch (deprecated 2026-06-18)"
	case "agy":
		return "agent-dispatch multi-turn mcp"
	case "antigravity-sdk":
		return "agent-dispatch (python)"
	}
	return ""
}

// ---- exec helpers -----------------------------------------------------------

// DefaultExecFn runs name with args using os/exec, forwarding stdin/stdout/stderr.
func DefaultExecFn(name string, args []string) error {
	return defaultExecImpl(name, args)
}

// ---- misc helpers -----------------------------------------------------------

func logLine(w io.Writer, msg string) {
	_, _ = fmt.Fprintln(w, msg)
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
