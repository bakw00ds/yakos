package consolecmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consolecmd"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/userstore"
)

// ---- bootstrap-token --------------------------------------------------------

func TestBootstrapToken_ZeroUsers_PrintsToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	err := consolecmd.RunWithStateDir([]string{"bootstrap-token"}, &stdout, &stderr, dir)
	if err != nil {
		t.Fatalf("bootstrap-token: %v", err)
	}

	tok := strings.TrimSpace(stdout.String())
	if tok == "" {
		t.Fatal("bootstrap-token: expected non-empty token on stdout")
	}
	// Token should be base64url-encoded (no +, /, = padding characters).
	for _, ch := range tok {
		if ch == '+' || ch == '/' || ch == '=' {
			t.Errorf("bootstrap-token: token contains non-base64url character %q", ch)
		}
	}
}

func TestBootstrapToken_ZeroUsers_WritesMarkerFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	if err := consolecmd.RunWithStateDir([]string{"bootstrap-token"}, &stdout, &stderr, dir); err != nil {
		t.Fatalf("bootstrap-token: %v", err)
	}

	markerPath := filepath.Join(dir, "setup-token")
	if _, err := os.Stat(markerPath); os.IsNotExist(err) {
		t.Errorf("marker file should exist at %s after bootstrap-token", markerPath)
	}
}

func TestBootstrapToken_UsersExist_ReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Pre-create a user so Count() > 0.
	uStore, err := userstore.Open(filepath.Join(dir, "users", "users.json"))
	if err != nil {
		t.Fatalf("userstore.Open: %v", err)
	}
	if err := uStore.Create("admin", "adminpassword123", netid.RoleAdmin); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var stdout, stderr bytes.Buffer
	runErr := consolecmd.RunWithStateDir([]string{"bootstrap-token"}, &stdout, &stderr, dir)
	if runErr == nil {
		t.Fatal("bootstrap-token with existing users: expected error, got nil")
	}

	// stdout should be empty (no token printed).
	if tok := strings.TrimSpace(stdout.String()); tok != "" {
		t.Errorf("bootstrap-token with users: unexpected token on stdout: %q", tok)
	}
	// stderr should mention setup is complete.
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "setup") && !strings.Contains(stderrStr, "admin") {
		t.Errorf("bootstrap-token with users: stderr should mention setup/admin, got: %q", stderrStr)
	}
}

func TestBootstrapToken_Help_PrintsHelp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	err := consolecmd.RunWithStateDir([]string{"bootstrap-token", "--help"}, &stdout, &stderr, dir)
	if err != nil {
		t.Fatalf("bootstrap-token --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "bootstrap-token") {
		t.Errorf("--help: expected 'bootstrap-token' in output, got: %q", stdout.String())
	}
}

func TestConsoleHelp_PrintsHelp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	err := consolecmd.RunWithStateDir([]string{"--help"}, &stdout, &stderr, dir)
	if err != nil {
		t.Fatalf("console --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "bootstrap-token") {
		t.Errorf("--help: expected 'bootstrap-token' in output, got: %q", stdout.String())
	}
}

func TestConsoleUnknownSubcommand_ReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	err := consolecmd.RunWithStateDir([]string{"nonexistent"}, &stdout, &stderr, dir)
	if err == nil {
		t.Fatal("unknown subcommand: expected error, got nil")
	}
}
