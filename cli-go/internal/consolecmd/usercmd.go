package consolecmd

// usercmd.go — `yakos console user` subcommand tree.
//
// Subcommands:
//
//	add <name> [--role <role>]  — create a new user (prompts for password, no echo)
//	list                        — list all users
//	set-role <name> <role>      — change a user's role
//	reset-password <name>       — set a new temporary password (prompts, no echo)
//	disable <name>              — disable a user account
//	enable <name>               — re-enable a disabled user
//	delete <name>               — delete a user
//
// # Security notes
//
//   - Password prompts use no-echo input (terminal echo is disabled via
//     readPasswordNoEcho so the password is never visible in the terminal).
//   - Self-protection: operations that would leave zero non-disabled admins
//     are refused with a clear error before touching the store.
//   - All state-mutating operations write an audit-log entry (slog) with actor
//     "cli" + target username + action.  Passwords are NEVER logged.
//
// # Trusted environment
//
// This command MUST run on the daemon host (same file-trust model as
// `yakos mtls`/`yakos console bootstrap-token`).  It manipulates the users.json
// file directly, bypassing HTTP.  It is NOT an authenticated HTTP client — it
// operates as the process owner, which is implicitly trusted.

import (
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/userstore"
)

// defaultRole is the role assigned when --role is not specified.
const defaultRole = "read"

// runUserCmd dispatches `yakos console user <subcmd> [args...]`.
func runUserCmd(args []string, stdout, stderr io.Writer, stateDir string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printUserHelp(stdout)
		return nil
	}
	sub := args[0]
	rest := args[1:]

	usersPath := filepath.Join(stateDir, "users", "users.json")
	uStore, err := userstore.Open(usersPath)
	if err != nil {
		return fmt.Errorf("console user: open user store: %w", err)
	}

	switch sub {
	case "add":
		return runUserAdd(rest, stdout, stderr, uStore)
	case "list":
		return runUserList(rest, stdout, stderr, uStore)
	case "set-role":
		return runUserSetRole(rest, stdout, stderr, uStore)
	case "reset-password":
		return runUserResetPassword(rest, stdout, stderr, uStore)
	case "disable":
		return runUserDisable(rest, stdout, stderr, uStore)
	case "enable":
		return runUserEnable(rest, stdout, stderr, uStore)
	case "delete":
		return runUserDelete(rest, stdout, stderr, uStore)
	default:
		return fmt.Errorf("console user: unknown subcommand %q (try --help)", sub)
	}
}

// ---- add --------------------------------------------------------------------

func runUserAdd(args []string, stdout, stderr io.Writer, uStore *userstore.Store) error {
	// Usage: add <name> [--role <role>]
	for _, a := range args {
		if a == "--help" || a == "-h" {
			printUserAddHelp(stdout)
			return nil
		}
	}

	if len(args) == 0 {
		return fmt.Errorf("console user add: username required (try --help)")
	}

	username := args[0]
	roleStr := defaultRole

	// Parse optional --role flag.
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--role", "-r":
			if i+1 >= len(rest) {
				return fmt.Errorf("console user add: --role requires a value")
			}
			roleStr = rest[i+1]
			i++
		default:
			if strings.HasPrefix(rest[i], "-") {
				return fmt.Errorf("console user add: unknown flag %q (try --help)", rest[i])
			}
		}
	}

	role := netid.ParseRole(roleStr)
	if role.String() != roleStr {
		return fmt.Errorf("console user add: invalid role %q (valid: read, dispatch, flows-run, admin)", roleStr)
	}

	// Prompt for password (no echo).
	fmt.Fprintf(stdout, "Password for %s: ", username)
	password, err := readPasswordNoEcho(stderr)
	fmt.Fprintln(stdout) // newline after prompt
	if err != nil {
		return fmt.Errorf("console user add: read password: %w", err)
	}
	if len(password) < userstore.MinPasswordLen {
		return fmt.Errorf("console user add: password too short: minimum %d characters", userstore.MinPasswordLen)
	}

	// Confirm password.
	fmt.Fprintf(stdout, "Confirm password: ")
	confirm, err := readPasswordNoEcho(stderr)
	fmt.Fprintln(stdout)
	if err != nil {
		return fmt.Errorf("console user add: read confirm password: %w", err)
	}
	if password != confirm {
		return fmt.Errorf("console user add: passwords do not match")
	}

	if err := uStore.Create(username, password, role); err != nil {
		return fmt.Errorf("console user add: %w", err)
	}

	slog.Info("consolecmd: audit: user created",
		"actor", "cli",
		"target", username,
		"action", "create_user",
		"role", roleStr,
	)
	fmt.Fprintf(stdout, "User %q created with role %q.\n", username, roleStr)
	return nil
}

// ---- list -------------------------------------------------------------------

func runUserList(args []string, stdout, stderr io.Writer, uStore *userstore.Store) error {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Fprintln(stdout, "Usage: yakos console user list")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "List all users in the user store.")
			return nil
		}
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("console user list: unknown flag %q (try --help)", a)
		}
	}

	users := uStore.List()
	if len(users) == 0 {
		fmt.Fprintln(stdout, "No users found.")
		return nil
	}

	// Header.
	fmt.Fprintf(stdout, "%-20s %-12s %-8s %-16s\n", "USERNAME", "ROLE", "DISABLED", "RESET-REQ")
	fmt.Fprintln(stdout, strings.Repeat("-", 60))
	for _, u := range users {
		disabledStr := "no"
		if u.Disabled {
			disabledStr = "yes"
		}
		resetStr := "no"
		if u.PasswordResetReq {
			resetStr = "yes"
		}
		fmt.Fprintf(stdout, "%-20s %-12s %-8s %-16s\n",
			u.Username, u.RoleString, disabledStr, resetStr)
	}
	return nil
}

// ---- set-role ---------------------------------------------------------------

func runUserSetRole(args []string, stdout, stderr io.Writer, uStore *userstore.Store) error {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Fprintln(stdout, "Usage: yakos console user set-role <username> <role>")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Change a user's role.")
			fmt.Fprintln(stdout, "Valid roles: read, dispatch, flows-run, admin")
			return nil
		}
	}

	if len(args) < 2 {
		return fmt.Errorf("console user set-role: username and role required (try --help)")
	}

	username := args[0]
	roleStr := args[1]

	role := netid.ParseRole(roleStr)
	if role.String() != roleStr {
		return fmt.Errorf("console user set-role: invalid role %q (valid: read, dispatch, flows-run, admin)", roleStr)
	}

	// Self-protection: refuse if this would leave zero admins.
	if role != netid.RoleAdmin {
		pu, found := uStore.Get(username)
		if found && pu.Role == netid.RoleAdmin && !pu.Disabled {
			if uStore.AdminCount() <= 1 {
				return fmt.Errorf("console user set-role: cannot demote the last admin; promote another user to admin first")
			}
		}
	}

	if err := uStore.SetRole(username, role); err != nil {
		return fmt.Errorf("console user set-role: %w", err)
	}

	slog.Info("consolecmd: audit: user role changed",
		"actor", "cli",
		"target", username,
		"action", "set_role",
		"new_role", roleStr,
	)
	fmt.Fprintf(stdout, "User %q role set to %q.\n", username, roleStr)
	return nil
}

// ---- reset-password ---------------------------------------------------------

func runUserResetPassword(args []string, stdout, stderr io.Writer, uStore *userstore.Store) error {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Fprintln(stdout, "Usage: yakos console user reset-password <username>")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Set a temporary password for a user.")
			fmt.Fprintln(stdout, "The user will be required to change the password on next login.")
			return nil
		}
	}

	if len(args) == 0 {
		return fmt.Errorf("console user reset-password: username required (try --help)")
	}

	username := args[0]

	fmt.Fprintf(stdout, "New temporary password for %s: ", username)
	password, err := readPasswordNoEcho(stderr)
	fmt.Fprintln(stdout)
	if err != nil {
		return fmt.Errorf("console user reset-password: read password: %w", err)
	}
	if len(password) < userstore.MinPasswordLen {
		return fmt.Errorf("console user reset-password: password too short: minimum %d characters", userstore.MinPasswordLen)
	}

	if err := uStore.ResetPassword(username, password); err != nil {
		return fmt.Errorf("console user reset-password: %w", err)
	}

	slog.Info("consolecmd: audit: password reset",
		"actor", "cli",
		"target", username,
		"action", "reset_password",
	)
	fmt.Fprintf(stdout, "Password reset for %q. User must change it on next login.\n", username)
	return nil
}

// ---- disable ----------------------------------------------------------------

func runUserDisable(args []string, stdout, stderr io.Writer, uStore *userstore.Store) error {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Fprintln(stdout, "Usage: yakos console user disable <username>")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Disable a user account. The user will be unable to log in.")
			return nil
		}
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("console user disable: unknown flag %q (try --help)", a)
		}
	}

	if len(args) == 0 {
		return fmt.Errorf("console user disable: username required (try --help)")
	}

	username := args[0]

	// Self-protection.
	pu, found := uStore.Get(username)
	if found && pu.Role == netid.RoleAdmin && !pu.Disabled {
		if uStore.AdminCount() <= 1 {
			return fmt.Errorf("console user disable: cannot disable the last admin; promote another user to admin first")
		}
	}

	if err := uStore.Disable(username); err != nil {
		return fmt.Errorf("console user disable: %w", err)
	}

	slog.Info("consolecmd: audit: user disabled",
		"actor", "cli",
		"target", username,
		"action", "disable_user",
	)
	fmt.Fprintf(stdout, "User %q disabled.\n", username)
	return nil
}

// ---- enable -----------------------------------------------------------------

func runUserEnable(args []string, stdout, stderr io.Writer, uStore *userstore.Store) error {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Fprintln(stdout, "Usage: yakos console user enable <username>")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Re-enable a disabled user account.")
			return nil
		}
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("console user enable: unknown flag %q (try --help)", a)
		}
	}

	if len(args) == 0 {
		return fmt.Errorf("console user enable: username required (try --help)")
	}

	username := args[0]

	if err := uStore.Enable(username); err != nil {
		return fmt.Errorf("console user enable: %w", err)
	}

	slog.Info("consolecmd: audit: user enabled",
		"actor", "cli",
		"target", username,
		"action", "enable_user",
	)
	fmt.Fprintf(stdout, "User %q enabled.\n", username)
	return nil
}

// ---- delete -----------------------------------------------------------------

func runUserDelete(args []string, stdout, stderr io.Writer, uStore *userstore.Store) error {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Fprintln(stdout, "Usage: yakos console user delete <username>")
			fmt.Fprintln(stdout, "")
			fmt.Fprintln(stdout, "Permanently delete a user. This action cannot be undone.")
			return nil
		}
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("console user delete: unknown flag %q (try --help)", a)
		}
	}

	if len(args) == 0 {
		return fmt.Errorf("console user delete: username required (try --help)")
	}

	username := args[0]

	// Self-protection.
	pu, found := uStore.Get(username)
	if found && pu.Role == netid.RoleAdmin && !pu.Disabled {
		if uStore.AdminCount() <= 1 {
			return fmt.Errorf("console user delete: cannot delete the last admin; promote another user to admin first")
		}
	}

	if err := uStore.Delete(username); err != nil {
		return fmt.Errorf("console user delete: %w", err)
	}

	slog.Info("consolecmd: audit: user deleted",
		"actor", "cli",
		"target", username,
		"action", "delete_user",
	)
	fmt.Fprintf(stdout, "User %q deleted.\n", username)
	return nil
}

// ---- help -------------------------------------------------------------------

func printUserHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: yakos console user <subcommand> [args]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Manage users in the yakOS console user store.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  add <name> [--role <role>]  Create a new user (prompts for password)")
	fmt.Fprintln(w, "  list                         List all users")
	fmt.Fprintln(w, "  set-role <name> <role>       Change a user's role")
	fmt.Fprintln(w, "  reset-password <name>        Set a temporary password")
	fmt.Fprintln(w, "  disable <name>               Disable a user account")
	fmt.Fprintln(w, "  enable <name>                Re-enable a disabled user")
	fmt.Fprintln(w, "  delete <name>                Permanently delete a user")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Valid roles: read, dispatch, flows-run, admin")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "SECURITY: self-protection guards refuse to remove the last admin.")
	fmt.Fprintln(w, "Run  yakos console user <subcommand> --help  for per-subcommand help.")
}

func printUserAddHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: yakos console user add <username> [--role <role>]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Create a new user. Prompts for a password (no echo).")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --role <role>   Role to assign (default: read)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Valid roles: read, dispatch, flows-run, admin")
}
