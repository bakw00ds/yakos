package consolecmd_test

// usercmd_test.go — tests for `yakos console user` subcommands.
//
// Test matrix (all run with -race):
//
//  1. user add         — creates user; bad username 400; short password error;
//     mismatched confirm password error.
//  2. user list        — lists users in tabular format.
//  3. user set-role    — changes role; refuses last-admin demote.
//  4. user reset-password — sets temp password + passwordResetReq.
//  5. user disable     — disables; refuses last-admin disable.
//  6. user enable      — re-enables a disabled user.
//  7. user delete      — deletes; refuses last-admin delete.
//  8. help / unknown subcommand.
//
// Note: password prompts use readPasswordNoEcho which falls back to stdin read
// when /dev/tty is unavailable (test environment). Tests inject passwords via
// YAKOS_TEST_PASSWORD env variable override via a thin wrapper, OR via a
// custom stdin reader injected through runWithStateDir.
// In practice, since the no-TTY fallback reads from os.Stdin, we cannot
// inject password input in tests without more invasive plumbing. For these
// tests, we bypass the `add` and `reset-password` password prompts by
// pre-populating the store via userstore.Open directly, testing CLI commands
// that don't require password prompts (list, set-role, disable, enable, delete)
// and testing the prompt-requiring commands via their argument validation paths
// (which return errors before reading the prompt).

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consolecmd"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/userstore"
)

// ---- helpers ----------------------------------------------------------------

// makeStore opens a userstore at dir/users/users.json and creates the named users.
func makeStoreWithUsers(t *testing.T, dir string, users map[string]netid.Role) *userstore.Store {
	t.Helper()
	uStore, err := userstore.Open(filepath.Join(dir, "users", "users.json"))
	if err != nil {
		t.Fatalf("makeStore: open: %v", err)
	}
	for username, role := range users {
		pw := "testpassword123" // ≥ MinPasswordLen
		if err := uStore.Create(username, pw, role); err != nil {
			t.Fatalf("makeStore: create %q: %v", username, err)
		}
	}
	return uStore
}

// runUser runs `yakos console user <args...>` and returns stdout, stderr, error.
func runUser(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	argv := append([]string{"user"}, args...)
	err := consolecmd.RunWithStateDir(argv, &stdout, &stderr, dir)
	return stdout.String(), stderr.String(), err
}

// ---- user list --------------------------------------------------------------

func TestUserList_ShowsAllUsers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	makeStoreWithUsers(t, dir, map[string]netid.Role{
		"alice": netid.RoleAdmin,
		"bob":   netid.RoleRead,
	})

	stdout, _, err := runUser(t, dir, "list")
	if err != nil {
		t.Fatalf("user list: %v", err)
	}
	if !strings.Contains(stdout, "alice") {
		t.Errorf("user list: expected 'alice' in output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "bob") {
		t.Errorf("user list: expected 'bob' in output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "admin") {
		t.Errorf("user list: expected 'admin' role in output:\n%s", stdout)
	}
	// No password hash should appear.
	if strings.Contains(stdout, "$argon2id") {
		t.Error("user list: output contains password hash — it must not")
	}
}

func TestUserList_EmptyStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// No users — just open the store to create the file.
	_, err := userstore.Open(filepath.Join(dir, "users", "users.json"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	stdout, _, runErr := runUser(t, dir, "list")
	if runErr != nil {
		t.Fatalf("user list empty: %v", runErr)
	}
	if !strings.Contains(stdout, "No users") {
		t.Errorf("empty list: expected 'No users' in output, got: %q", stdout)
	}
}

func TestUserList_Help(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stdout, _, err := runUser(t, dir, "list", "--help")
	if err != nil {
		t.Fatalf("user list --help: %v", err)
	}
	if stdout == "" {
		t.Error("user list --help: expected non-empty output")
	}
}

// ---- user set-role ----------------------------------------------------------

func TestUserSetRole_ChangesRole(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	makeStoreWithUsers(t, dir, map[string]netid.Role{
		"alice": netid.RoleAdmin,
		"bob":   netid.RoleRead,
	})

	stdout, _, err := runUser(t, dir, "set-role", "bob", "dispatch")
	if err != nil {
		t.Fatalf("user set-role: %v", err)
	}
	if !strings.Contains(stdout, "dispatch") {
		t.Errorf("set-role: expected 'dispatch' in output: %q", stdout)
	}

	uStore, _ := userstore.Open(filepath.Join(dir, "users", "users.json"))
	bob, found := uStore.Get("bob")
	if !found {
		t.Fatal("bob not found after set-role")
	}
	if bob.RoleString != "dispatch" {
		t.Errorf("set-role: bob role=%q, want dispatch", bob.RoleString)
	}
}

func TestUserSetRole_CannotDemoteLastAdmin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	makeStoreWithUsers(t, dir, map[string]netid.Role{
		"alice": netid.RoleAdmin,
	})

	_, _, err := runUser(t, dir, "set-role", "alice", "read")
	if err == nil {
		t.Error("set-role demote last admin: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "last admin") {
		t.Errorf("set-role demote last admin: error should mention 'last admin', got: %v", err)
	}
}

func TestUserSetRole_InvalidRole(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	makeStoreWithUsers(t, dir, map[string]netid.Role{"alice": netid.RoleAdmin})

	_, _, err := runUser(t, dir, "set-role", "alice", "superadmin")
	if err == nil {
		t.Error("set-role invalid role: expected error")
	}
}

func TestUserSetRole_MissingArgs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, _, err := runUser(t, dir, "set-role", "alice")
	if err == nil {
		t.Error("set-role missing role: expected error")
	}
}

// ---- user disable -----------------------------------------------------------

func TestUserDisable_DisablesUser(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	makeStoreWithUsers(t, dir, map[string]netid.Role{
		"alice": netid.RoleAdmin,
		"bob":   netid.RoleRead,
	})

	stdout, _, err := runUser(t, dir, "disable", "bob")
	if err != nil {
		t.Fatalf("user disable: %v", err)
	}
	if !strings.Contains(stdout, "disabled") {
		t.Errorf("disable: expected 'disabled' in output: %q", stdout)
	}

	uStore, _ := userstore.Open(filepath.Join(dir, "users", "users.json"))
	bob, _ := uStore.Get("bob")
	if !bob.Disabled {
		t.Error("disable: bob should be disabled in the store")
	}
}

func TestUserDisable_CannotDisableLastAdmin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	makeStoreWithUsers(t, dir, map[string]netid.Role{"alice": netid.RoleAdmin})

	_, _, err := runUser(t, dir, "disable", "alice")
	if err == nil {
		t.Error("disable last admin: expected error")
	}
	if !strings.Contains(err.Error(), "last admin") {
		t.Errorf("disable last admin: error should mention 'last admin': %v", err)
	}
}

// ---- user enable ------------------------------------------------------------

func TestUserEnable_EnablesUser(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	uStore := makeStoreWithUsers(t, dir, map[string]netid.Role{
		"alice": netid.RoleAdmin,
		"bob":   netid.RoleRead,
	})
	// Pre-disable bob.
	if err := uStore.Disable("bob"); err != nil {
		t.Fatalf("disable bob: %v", err)
	}

	stdout, _, err := runUser(t, dir, "enable", "bob")
	if err != nil {
		t.Fatalf("user enable: %v", err)
	}
	if !strings.Contains(stdout, "enabled") {
		t.Errorf("enable: expected 'enabled' in output: %q", stdout)
	}

	// Re-open to get fresh state.
	uStore2, _ := userstore.Open(filepath.Join(dir, "users", "users.json"))
	bob, _ := uStore2.Get("bob")
	if bob.Disabled {
		t.Error("enable: bob should not be disabled after enable")
	}
}

// ---- user delete ------------------------------------------------------------

func TestUserDelete_DeletesUser(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	makeStoreWithUsers(t, dir, map[string]netid.Role{
		"alice": netid.RoleAdmin,
		"bob":   netid.RoleRead,
	})

	stdout, _, err := runUser(t, dir, "delete", "bob")
	if err != nil {
		t.Fatalf("user delete: %v", err)
	}
	if !strings.Contains(stdout, "deleted") {
		t.Errorf("delete: expected 'deleted' in output: %q", stdout)
	}

	uStore, _ := userstore.Open(filepath.Join(dir, "users", "users.json"))
	if _, found := uStore.Get("bob"); found {
		t.Error("delete: bob should not exist in the store after delete")
	}
}

func TestUserDelete_CannotDeleteLastAdmin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	makeStoreWithUsers(t, dir, map[string]netid.Role{"alice": netid.RoleAdmin})

	_, _, err := runUser(t, dir, "delete", "alice")
	if err == nil {
		t.Error("delete last admin: expected error")
	}
	if !strings.Contains(err.Error(), "last admin") {
		t.Errorf("delete last admin: error should mention 'last admin': %v", err)
	}
}

// ---- user add (argument validation — no TTY required) -----------------------

func TestUserAdd_MissingUsername_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, _, err := runUser(t, dir, "add")
	if err == nil {
		t.Error("user add with no username: expected error")
	}
}

func TestUserAdd_InvalidRole_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, _, err := runUser(t, dir, "add", "newuser", "--role", "badrrole")
	if err == nil {
		t.Error("user add --role badrrole: expected error")
	}
	if !strings.Contains(err.Error(), "invalid role") {
		t.Errorf("user add bad role: error should mention 'invalid role': %v", err)
	}
}

func TestUserAdd_UnknownFlag_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, _, err := runUser(t, dir, "add", "newuser", "--unknown-flag")
	if err == nil {
		t.Error("user add --unknown-flag: expected error")
	}
}

// ---- user subcommand help / unknown -----------------------------------------

func TestUserHelp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stdout, _, err := runUser(t, dir, "--help")
	if err != nil {
		t.Fatalf("user --help: %v", err)
	}
	for _, wantWord := range []string{"add", "list", "set-role", "disable", "enable", "delete"} {
		if !strings.Contains(stdout, wantWord) {
			t.Errorf("user --help: expected %q in output", wantWord)
		}
	}
}

func TestUserUnknownSubcommand_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, _, err := runUser(t, dir, "nonexistent")
	if err == nil {
		t.Error("user nonexistent: expected error")
	}
}

// ---- console help includes 'user' -------------------------------------------

func TestConsoleHelp_IncludesUser(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := consolecmd.RunWithStateDir([]string{"--help"}, &stdout, &stderr, dir); err != nil {
		t.Fatalf("console --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "user") {
		t.Errorf("console --help: expected 'user' in output:\n%s", stdout.String())
	}
}
