// Package consolecmd implements the `yakos console` CLI subcommand tree.
//
// Subcommands:
//
//	bootstrap-token   Regenerate and print a fresh first-admin setup token.
//
// All subcommands resolve the state directory the same way the daemon does:
// $HOME/.yakos-state (or $YAKOS_STATE_DIR if set, for test injection).
//
// # Security notes
//
// bootstrap-token regenerates the setup token only when zero users exist.
// It refuses with a clear message once an admin exists — at that point the
// operator should use the Users panel or `yakos console user add` (future
// phase).  The token is printed to stdout only; callers should transmit it
// only over a trusted channel (e.g. SSH session to the daemon host).
//
// The state directory is trusted (0700); the marker file is 0600.
// This command MUST run on the daemon host (same file-trust model as
// `yakos mtls`).
package consolecmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bakw00ds/yakos/internal/setuptoken"
	"github.com/bakw00ds/yakos/internal/userstore"
)

// DefaultStateDir returns the default state directory for yakOS, consistent
// with how serve.go resolves it ($HOME/.yakos-state).
// Tests may set YAKOS_STATE_DIR to override.
func DefaultStateDir() string {
	if d := os.Getenv("YAKOS_STATE_DIR"); d != "" {
		return d
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}
	return filepath.Join(home, ".yakos-state")
}

// Run dispatches the `yakos console <subcmd> [args...]` invocation.
// It writes user-facing output to stdout and errors/warnings to stderr.
// On error it returns a non-nil error; the caller calls os.Exit(1).
//
// The state directory is resolved via DefaultStateDir().
func Run(args []string, stdout, stderr io.Writer) error {
	return RunWithStateDir(args, stdout, stderr, DefaultStateDir())
}

// RunWithStateDir is like Run but accepts an explicit stateDir.
// Used by tests to inject a temporary directory without touching env vars.
func RunWithStateDir(args []string, stdout, stderr io.Writer, stateDir string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printConsoleHelp(stdout)
		return nil
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "bootstrap-token":
		return runBootstrapToken(rest, stdout, stderr, stateDir)
	default:
		return fmt.Errorf("console: unknown subcommand %q (try --help)", sub)
	}
}

// ---- bootstrap-token --------------------------------------------------------

func runBootstrapToken(args []string, stdout, stderr io.Writer, stateDir string) error {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			printBootstrapTokenHelp(stdout)
			return nil
		}
		if a[0] == '-' {
			return fmt.Errorf("console bootstrap-token: unknown flag %q (try --help)", a)
		}
	}

	// Open the user store to check whether an admin already exists.
	usersPath := filepath.Join(stateDir, "users", "users.json")
	uStore, err := userstore.Open(usersPath)
	if err != nil {
		return fmt.Errorf("console bootstrap-token: open user store: %w", err)
	}

	if uStore.Count() > 0 {
		fmt.Fprintln(stderr, "error: an admin account already exists; setup is complete.")
		fmt.Fprintln(stderr, "  To manage users: yakos console user add|list|set-role (coming soon)")
		fmt.Fprintln(stderr, "  To use the admin Users panel in the web console.")
		return fmt.Errorf("console bootstrap-token: setup already complete")
	}

	// Generate a fresh setup token (always generates new, does not reuse).
	tokenFilePath := filepath.Join(stateDir, "setup-token")
	st := setuptoken.New(tokenFilePath, nil)
	tok, err := st.Generate()
	if err != nil {
		return fmt.Errorf("console bootstrap-token: generate token: %w", err)
	}

	fmt.Fprintln(stdout, tok)
	fmt.Fprintln(stderr, "Setup token generated (expires in 30 minutes).")
	fmt.Fprintln(stderr, "Use it at: https://<your-console-host>/setup?token=<token>")
	fmt.Fprintln(stderr, "SECURITY: transmit this token only over a secure channel (e.g. SSH).")

	return nil
}

// ---- helpers ----------------------------------------------------------------

func printConsoleHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: yakos console <subcommand> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Manage the yakOS networked console (ADR-0005).")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  bootstrap-token   Regenerate the first-admin setup token (zero-users only)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Run  yakos console <subcommand> --help  for per-subcommand help.")
}

func printBootstrapTokenHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: yakos console bootstrap-token")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Regenerate and print a fresh first-admin setup token.")
	fmt.Fprintln(w, "Only available when no admin account exists yet (zero-users state).")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "The token is printed to stdout. It expires in 30 minutes and is single-use.")
	fmt.Fprintln(w, "Use it at: https://<your-console-host>/setup?token=<token>")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "SECURITY: transmit this token only over a secure channel (e.g. SSH).")
	fmt.Fprintln(w, "  Never share via unencrypted HTTP, Slack, or email attachment.")
}
