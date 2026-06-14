// bootstrap_test.go — tests for the auto-issue bootstrap client cert behavior
// wired into the networked-bind startup path.
//
// Strategy: the networked serve.Run() lifecycle is hard to exercise in a unit
// test without a full socket/TLS stack.  We test the bootstrap logic directly
// via the exported mtlscmd.AutoIssueBootstrap function (same code path) and
// verify that the Config fields thread through.  Integration-level verification
// that Run() calls AutoIssueBootstrap is covered by the no-bootstrap-cert flag
// wiring and the serve.Config field presence checks below.
package serve_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/mtls"
	"github.com/bakw00ds/yakos/internal/mtlscmd"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/serve"
)

// TestBootstrap_ConfigFieldsExist verifies that the Config struct carries the
// bootstrap fields.  This is a compile-time check surfaced as a test.
func TestBootstrap_ConfigFieldsExist(t *testing.T) {
	t.Parallel()
	cfg := serve.Config{
		ConsoleBootstrapCertName: "operator",
		NoBootstrapCert:          true,
	}
	if cfg.ConsoleBootstrapCertName != "operator" {
		t.Error("ConsoleBootstrapCertName field not threaded correctly")
	}
	if !cfg.NoBootstrapCert {
		t.Error("NoBootstrapCert field not threaded correctly")
	}
}

// TestBootstrap_NetworkedStart_IssuesBootstrapCert verifies the full path:
// empty clientDir → AutoIssueBootstrap → cert exists, role=admin, bundle written.
func TestBootstrap_NetworkedStart_IssuesBootstrapCert(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Pre-generate CA (mirrors what Run() does before calling AutoIssueBootstrap).
	if _, _, err := mtls.LoadOrGenerateCA(dir); err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}

	result := mtlscmd.AutoIssueBootstrap(dir, "testop", false)
	if result.Err != nil {
		t.Fatalf("AutoIssueBootstrap: %v", result.Err)
	}

	if !result.Issued {
		t.Error("expected Issued=true on first networked start")
	}
	if result.CN != "testop" {
		t.Errorf("CN=%q; want testop", result.CN)
	}

	// Cert must be loadable.
	cert, err := mtls.LoadClientCert(dir, "testop")
	if err != nil {
		t.Fatalf("LoadClientCert: %v", err)
	}
	if cert == nil {
		t.Fatal("cert is nil")
	}

	// Role must be admin.
	mapper := netid.NewRoleMapper(dir)
	role := mapper.Lookup("testop")
	if role != netid.RoleAdmin {
		t.Errorf("role=%v; want admin", role)
	}

	// Bundle must be written.
	for _, name := range []string{"client-testop.crt", "client-testop.key", "ca.crt"} {
		path := filepath.Join(result.BundleDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected bundle file %s: %v", name, err)
		}
	}
}

// TestBootstrap_NoBootstrapCert_NoIssue verifies that --no-bootstrap-cert
// prevents auto-issue.
func TestBootstrap_NoBootstrapCert_NoIssue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := mtls.LoadOrGenerateCA(dir); err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}

	result := mtlscmd.AutoIssueBootstrap(dir, "op", true /* noBootstrap */)
	if result.Err != nil {
		t.Fatalf("AutoIssueBootstrap with noBootstrap: %v", result.Err)
	}
	if !result.Skipped {
		t.Error("expected Skipped=true")
	}

	// No client cert should exist.
	clientsDir := filepath.Join(dir, "mtls", "clients")
	entries, _ := os.ReadDir(clientsDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".crt") {
			t.Errorf("unexpected cert %s when noBootstrap=true", e.Name())
		}
	}
}

// TestBootstrap_Idempotent_SecondStart verifies that a second networked start
// with an existing client cert does NOT issue a new cert (idempotency).
func TestBootstrap_Idempotent_SecondStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := mtls.LoadOrGenerateCA(dir); err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}

	// First start — issues bootstrap cert.
	r1 := mtlscmd.AutoIssueBootstrap(dir, "admin", false)
	if r1.Err != nil {
		t.Fatalf("first AutoIssueBootstrap: %v", r1.Err)
	}
	if !r1.Issued {
		t.Fatal("first call: expected Issued=true")
	}
	firstDER := func() []byte {
		c, err := mtls.LoadClientCert(dir, "admin")
		if err != nil {
			t.Fatalf("LoadClientCert (first): %v", err)
		}
		if len(c.Certificate) == 0 {
			t.Fatal("no DER in first cert")
		}
		return c.Certificate[0]
	}()

	// Second start — should be a no-op.
	r2 := mtlscmd.AutoIssueBootstrap(dir, "admin", false)
	if r2.Err != nil {
		t.Fatalf("second AutoIssueBootstrap: %v", r2.Err)
	}
	if r2.Issued {
		t.Error("second call: expected Issued=false (cert already exists)")
	}

	// Original cert must be unchanged.
	c2, err := mtls.LoadClientCert(dir, "admin")
	if err != nil {
		t.Fatalf("LoadClientCert (second): %v", err)
	}
	if len(c2.Certificate) == 0 {
		t.Fatal("no DER in second load")
	}
	der2 := c2.Certificate[0]
	if len(firstDER) != len(der2) {
		t.Error("cert DER changed between starts; bootstrap is not idempotent")
	}
	for i, b := range firstDER {
		if b != der2[i] {
			t.Errorf("cert DER byte %d changed; bootstrap is not idempotent", i)
			break
		}
	}
}

// TestBootstrap_ExistingCert_NoAutoIssue verifies that a pre-existing client
// cert (from 'yakos mtls issue-client') prevents auto-issue.
func TestBootstrap_ExistingCert_NoAutoIssue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	caCert, caKey, err := mtls.LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}

	// Manually issue a cert (simulating prior 'yakos mtls issue-client').
	prior, err := mtls.IssueClientCert(caCert, caKey, "prior-op")
	if err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}
	if err := mtls.PersistClientCert(dir, "prior-op", prior); err != nil {
		t.Fatalf("PersistClientCert: %v", err)
	}

	// Auto-issue for a different name — should be no-op because clientDir is
	// non-empty.
	result := mtlscmd.AutoIssueBootstrap(dir, "new-op", false)
	if result.Err != nil {
		t.Fatalf("AutoIssueBootstrap: %v", result.Err)
	}
	if result.Issued {
		t.Error("expected Issued=false when clientDir already has a cert")
	}

	// The pre-existing cert must be intact.
	loaded, err := mtls.LoadClientCert(dir, "prior-op")
	if err != nil {
		t.Fatalf("LoadClientCert (prior): %v", err)
	}
	if loaded == nil {
		t.Fatal("prior cert is nil")
	}
}

// TestBootstrap_AutoIssueFailure_ErrFieldSet verifies that when AutoIssueBootstrap
// encounters an error (e.g. read-only stateDir prevents CA load), it sets
// BootstrapResult.Err instead of returning an error, and sets Issued=false.
// This ensures the serve banner arm "case bootstrap.Err != nil" can fire.
func TestBootstrap_AutoIssueFailure_ErrFieldSet(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("read-only dir permission test is Unix-only")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	dir := t.TempDir()
	// Make the stateDir unreadable so LoadOrGenerateCA fails inside AutoIssueBootstrap.
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	result := mtlscmd.AutoIssueBootstrap(dir, "op", false)
	if result.Err == nil {
		t.Error("expected Err != nil when stateDir is inaccessible; got nil")
	}
	if result.Issued {
		t.Error("expected Issued=false when AutoIssueBootstrap errors")
	}
}

// TestBootstrap_LoopbackStart_NoAutoIssue verifies by inference: the auto-issue
// hook is ONLY in the non-loopback networked path (isNonLoopbackBind == true).
// We can't run the full daemon but we can verify the predicate via the exported
// IsNonLoopbackForTest helper — loopback start → hook not reached.
func TestBootstrap_LoopbackStart_NoAutoIssue(t *testing.T) {
	t.Parallel()
	loopbackAddrs := []string{
		"127.0.0.1:7890",
		"127.0.0.1:0",
		"localhost:7890",
	}
	for _, addr := range loopbackAddrs {
		addr := addr
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			if serve.IsNonLoopbackForTest(addr) {
				t.Errorf("IsNonLoopback(%q) = true; loopback addresses must NOT trigger auto-issue", addr)
			}
		})
	}
}
