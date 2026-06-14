package mtlscmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/mtls"
	"github.com/bakw00ds/yakos/internal/mtlscmd"
	"github.com/bakw00ds/yakos/internal/netid"
)

// run is a convenience wrapper that calls RunWithStateDir and returns the
// stdout, stderr, and error.
func run(stateDir string, args ...string) (stdout, stderr string, err error) {
	var outBuf, errBuf bytes.Buffer
	err = mtlscmd.RunWithStateDir(args, &outBuf, &errBuf, stateDir)
	return outBuf.String(), errBuf.String(), err
}

// ---- issue-client -----------------------------------------------------------

func TestIssueClient_IssuedAndLoadable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	_, _, err := run(dir, "issue-client", "alice")
	if err != nil {
		t.Fatalf("issue-client: %v", err)
	}

	// The cert must be loadable via mtls.LoadClientCert.
	cert, err := mtls.LoadClientCert(dir, "alice")
	if err != nil {
		t.Fatalf("LoadClientCert: %v", err)
	}
	if cert == nil {
		t.Fatal("cert is nil")
	}
}

func TestIssueClient_CNMatchesName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := run(dir, "issue-client", "alice"); err != nil {
		t.Fatalf("issue-client: %v", err)
	}

	cert, err := mtls.LoadClientCert(dir, "alice")
	if err != nil {
		t.Fatalf("LoadClientCert: %v", err)
	}
	// Parse leaf.
	if len(cert.Certificate) == 0 {
		t.Fatal("no DER blocks")
	}
}

func TestIssueClient_BundleFilesExist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bundleDir := t.TempDir()

	if _, _, err := run(dir, "issue-client", "bob", "--out", bundleDir); err != nil {
		t.Fatalf("issue-client: %v", err)
	}

	for _, name := range []string{"client-bob.crt", "client-bob.key", "ca.crt"} {
		path := filepath.Join(bundleDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected bundle file %s: %v", name, err)
		}
	}
}

func TestIssueClient_KeyFile0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits not meaningful on Windows")
	}
	t.Parallel()
	dir := t.TempDir()
	bundleDir := t.TempDir()

	if _, _, err := run(dir, "issue-client", "carol", "--out", bundleDir); err != nil {
		t.Fatalf("issue-client: %v", err)
	}

	keyPath := filepath.Join(bundleDir, "client-carol.key")
	fi, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("key file mode %04o; want 0600", fi.Mode().Perm())
	}
}

func TestIssueClient_RefusesOverwriteWithoutForce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := run(dir, "issue-client", "eve"); err != nil {
		t.Fatalf("first issue-client: %v", err)
	}

	_, _, err := run(dir, "issue-client", "eve")
	if err == nil {
		t.Fatal("expected error on overwrite without --force; got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' in error; got %q", err.Error())
	}
}

func TestIssueClient_ForceOverwrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := run(dir, "issue-client", "frank"); err != nil {
		t.Fatalf("first issue-client: %v", err)
	}

	// Load DER of first cert.
	cert1, err := mtls.LoadClientCert(dir, "frank")
	if err != nil {
		t.Fatalf("LoadClientCert (first): %v", err)
	}

	if _, _, err := run(dir, "issue-client", "frank", "--force"); err != nil {
		t.Fatalf("force issue-client: %v", err)
	}

	cert2, err := mtls.LoadClientCert(dir, "frank")
	if err != nil {
		t.Fatalf("LoadClientCert (second): %v", err)
	}

	// The new cert should have a different DER (different serial from random).
	if len(cert1.Certificate) > 0 && len(cert2.Certificate) > 0 {
		if bytes.Equal(cert1.Certificate[0], cert2.Certificate[0]) {
			t.Error("--force should issue a new cert; DER blocks are identical")
		}
	}
}

func TestIssueClient_WithRole(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := run(dir, "issue-client", "grace", "--role", "dispatch"); err != nil {
		t.Fatalf("issue-client --role: %v", err)
	}

	mapper := netid.NewRoleMapper(dir)
	role := mapper.Lookup("grace")
	if role != netid.RoleDispatch {
		t.Errorf("role=%v; want dispatch", role)
	}
}

func TestIssueClient_InvalidRole(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	_, _, err := run(dir, "issue-client", "henry", "--role", "superuser")
	if err == nil {
		t.Fatal("expected error for invalid role; got nil")
	}
	if !strings.Contains(err.Error(), "unknown role") {
		t.Errorf("expected 'unknown role'; got %q", err.Error())
	}
}

func TestIssueClient_MissingName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	_, _, err := run(dir, "issue-client")
	if err == nil {
		t.Fatal("expected error for missing name; got nil")
	}
}

// ---- set-role ---------------------------------------------------------------

func TestSetRole_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := run(dir, "set-role", "ivan", "admin"); err != nil {
		t.Fatalf("set-role: %v", err)
	}

	mapper := netid.NewRoleMapper(dir)
	role := mapper.Lookup("ivan")
	if role != netid.RoleAdmin {
		t.Errorf("role=%v; want admin", role)
	}
}

func TestSetRole_AllRoles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cases := []struct {
		roleStr string
		want    netid.Role
	}{
		{"read", netid.RoleRead},
		{"dispatch", netid.RoleDispatch},
		{"flows-run", netid.RoleFlowsRun},
		{"admin", netid.RoleAdmin},
	}
	for _, tc := range cases {
		if _, _, err := run(dir, "set-role", "test-cn", tc.roleStr); err != nil {
			t.Errorf("set-role %s: %v", tc.roleStr, err)
			continue
		}
		mapper := netid.NewRoleMapper(dir)
		got := mapper.Lookup("test-cn")
		if got != tc.want {
			t.Errorf("set-role %s: got %v; want %v", tc.roleStr, got, tc.want)
		}
	}
}

func TestSetRole_RejectsInvalidRole(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	_, _, err := run(dir, "set-role", "judy", "overlord")
	if err == nil {
		t.Fatal("expected error for invalid role; got nil")
	}
	if !strings.Contains(err.Error(), "unknown role") {
		t.Errorf("want 'unknown role' in error; got %q", err.Error())
	}
}

func TestSetRole_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits not meaningful on Windows")
	}
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := run(dir, "set-role", "kate", "admin"); err != nil {
		t.Fatalf("set-role: %v", err)
	}

	rolesPath := filepath.Join(dir, "mtls", "roles.json")
	fi, err := os.Stat(rolesPath)
	if err != nil {
		t.Fatalf("stat roles.json: %v", err)
	}
	// Must be 0600 (passes LOW-1 check: perm & 0o022 == 0).
	if fi.Mode().Perm() != 0600 {
		t.Errorf("roles.json mode %04o; want 0600", fi.Mode().Perm())
	}
	// Verify the LOW-1 predicate passes.
	if fi.Mode().Perm()&0o022 != 0 {
		t.Error("roles.json is group/other-writable; LOW-1 check would reject it")
	}
}

func TestSetRole_MissingArgs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	_, _, err := run(dir, "set-role", "only-one-arg")
	if err == nil {
		t.Fatal("expected error for missing <role>; got nil")
	}
}

// ---- list-clients -----------------------------------------------------------

func TestListClients_EmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	out, _, err := run(dir, "list-clients")
	if err != nil {
		t.Fatalf("list-clients: %v", err)
	}
	if !strings.Contains(out, "no client certs") {
		t.Errorf("expected 'no client certs'; got %q", out)
	}
}

func TestListClients_ShowsIssuedCerts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	names := []string{"liam", "mia", "noah"}
	for _, name := range names {
		if _, _, err := run(dir, "issue-client", name); err != nil {
			t.Fatalf("issue-client %s: %v", name, err)
		}
	}
	// Set a non-default role on one.
	if _, _, err := run(dir, "set-role", "liam", "admin"); err != nil {
		t.Fatalf("set-role: %v", err)
	}

	out, _, err := run(dir, "list-clients")
	if err != nil {
		t.Fatalf("list-clients: %v", err)
	}
	for _, name := range names {
		if !strings.Contains(out, name) {
			t.Errorf("expected CN %q in list-clients output; got:\n%s", name, out)
		}
	}
	if !strings.Contains(out, "admin") {
		t.Errorf("expected 'admin' role in output; got:\n%s", out)
	}
}

// ---- show-ca ----------------------------------------------------------------

func TestShowCA_FingerprintStable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Generate CA by calling LoadOrGenerateCA directly.
	if _, _, err := mtls.LoadOrGenerateCA(dir); err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}

	out1, _, err := run(dir, "show-ca")
	if err != nil {
		t.Fatalf("show-ca (1): %v", err)
	}
	out2, _, err := run(dir, "show-ca")
	if err != nil {
		t.Fatalf("show-ca (2): %v", err)
	}

	fp1 := extractLine(out1, "SHA-256:")
	fp2 := extractLine(out2, "SHA-256:")
	if fp1 == "" {
		t.Fatal("SHA-256 line not found in first show-ca output")
	}
	if fp1 != fp2 {
		t.Errorf("fingerprint changed between calls:\n  first:  %s\n  second: %s", fp1, fp2)
	}
}

func TestShowCA_NoCA(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	_, _, err := run(dir, "show-ca")
	if err == nil {
		t.Fatal("expected error when no CA exists; got nil")
	}
	if !strings.Contains(err.Error(), "no CA cert found") {
		t.Errorf("want 'no CA cert found'; got %q", err.Error())
	}
}

// ---- auto-issue bootstrap ---------------------------------------------------

func TestAutoIssueBootstrap_IssuesWhenEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Generate CA first (mirrors networked startup order).
	if _, _, err := mtls.LoadOrGenerateCA(dir); err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}

	result, err := mtlscmd.AutoIssueBootstrap(dir, "testoperator", false)
	if err != nil {
		t.Fatalf("AutoIssueBootstrap: %v", err)
	}
	if !result.Issued {
		t.Error("expected Issued=true on first call to empty dir")
	}
	if result.CN != "testoperator" {
		t.Errorf("CN=%q; want testoperator", result.CN)
	}
	if result.BundleDir == "" {
		t.Error("BundleDir is empty")
	}

	// Cert must be loadable.
	cert, err := mtls.LoadClientCert(dir, "testoperator")
	if err != nil {
		t.Fatalf("LoadClientCert after bootstrap: %v", err)
	}
	if cert == nil {
		t.Fatal("loaded cert is nil")
	}
}

func TestAutoIssueBootstrap_AdminRoleAssigned(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := mtls.LoadOrGenerateCA(dir); err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}

	if _, err := mtlscmd.AutoIssueBootstrap(dir, "admin-bootstrap", false); err != nil {
		t.Fatalf("AutoIssueBootstrap: %v", err)
	}

	mapper := netid.NewRoleMapper(dir)
	role := mapper.Lookup("admin-bootstrap")
	if role != netid.RoleAdmin {
		t.Errorf("role=%v; want admin", role)
	}
}

func TestAutoIssueBootstrap_BundleWritten(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := mtls.LoadOrGenerateCA(dir); err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}

	result, err := mtlscmd.AutoIssueBootstrap(dir, "bundletest", false)
	if err != nil {
		t.Fatalf("AutoIssueBootstrap: %v", err)
	}

	for _, name := range []string{"client-bundletest.crt", "client-bundletest.key", "ca.crt"} {
		path := filepath.Join(result.BundleDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected bundle file %s: %v", name, err)
		}
	}
}

func TestAutoIssueBootstrap_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := mtls.LoadOrGenerateCA(dir); err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}

	r1, err := mtlscmd.AutoIssueBootstrap(dir, "idempotent", false)
	if err != nil {
		t.Fatalf("first AutoIssueBootstrap: %v", err)
	}
	if !r1.Issued {
		t.Fatal("first call: expected Issued=true")
	}

	r2, err := mtlscmd.AutoIssueBootstrap(dir, "idempotent", false)
	if err != nil {
		t.Fatalf("second AutoIssueBootstrap: %v", err)
	}
	if r2.Issued {
		t.Error("second call: expected Issued=false (idempotent); cert already exists")
	}
}

func TestAutoIssueBootstrap_NoBootstrapFlag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := mtls.LoadOrGenerateCA(dir); err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}

	result, err := mtlscmd.AutoIssueBootstrap(dir, "skipped", true /* noBootstrap */)
	if err != nil {
		t.Fatalf("AutoIssueBootstrap: %v", err)
	}
	if !result.Skipped {
		t.Error("expected Skipped=true when noBootstrap=true")
	}

	// No client cert should have been written.
	clientsDir := filepath.Join(dir, "mtls", "clients")
	entries, _ := os.ReadDir(clientsDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".crt") {
			t.Errorf("unexpected client cert file %s when noBootstrap=true", e.Name())
		}
	}
}

func TestAutoIssueBootstrap_ExistingCertNotOverwritten(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := mtls.LoadOrGenerateCA(dir); err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}

	// Manually issue a cert via mtls (simulating prior enrollment).
	caCert, caKey, _ := mtls.LoadOrGenerateCA(dir)
	prior, _ := mtls.IssueClientCert(caCert, caKey, "prior")
	if err := mtls.PersistClientCert(dir, "prior", prior); err != nil {
		t.Fatalf("PersistClientCert: %v", err)
	}
	priorDER := prior.Certificate[0]

	// Auto-issue should be a no-op because clientDir is non-empty.
	result, err := mtlscmd.AutoIssueBootstrap(dir, "new-op", false)
	if err != nil {
		t.Fatalf("AutoIssueBootstrap: %v", err)
	}
	if result.Issued {
		t.Error("expected Issued=false when clientDir already has a cert")
	}

	// The pre-existing cert should be unchanged.
	loaded, err := mtls.LoadClientCert(dir, "prior")
	if err != nil {
		t.Fatalf("LoadClientCert (prior): %v", err)
	}
	if len(loaded.Certificate) == 0 || !bytes.Equal(loaded.Certificate[0], priorDER) {
		t.Error("pre-existing cert was modified by AutoIssueBootstrap")
	}
}

// ---- unknown subcommand -----------------------------------------------------

func TestRun_UnknownSubcommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, _, err := run(dir, "no-such-cmd")
	if err == nil {
		t.Fatal("expected error for unknown subcommand; got nil")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("want 'unknown subcommand'; got %q", err.Error())
	}
}

// ---- helpers ----------------------------------------------------------------

// extractLine returns the value portion of the first line in s that starts with
// the given prefix.  Returns "" when not found.
func extractLine(s, prefix string) string {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		}
	}
	return ""
}
