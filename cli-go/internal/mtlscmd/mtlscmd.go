// Package mtlscmd implements the `yakos mtls` CLI subcommand tree.
//
// Subcommands:
//
//	issue-client <name>  Issue a new mTLS client certificate.
//	list-clients         List persisted client CNs with their current roles.
//	show-ca              Print CA cert path and SHA-256 fingerprint.
//	set-role <cn> <role> Assign a role to a certificate CN in roles.json.
//
// All subcommands resolve the state directory the same way the daemon does:
// $HOME/.yakos-state (or $YAKOS_STATE_DIR if set, for test injection).
//
// # Security notes
//
// The hand-off bundle written by issue-client contains a private key.
// Files are created at mode 0600 under the state directory (which is 0700).
// The auto-issued bootstrap key lives under the state dir — operators who
// can read the state dir already have the CA key, so the bootstrap cert adds
// no marginal exposure.
package mtlscmd

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bakw00ds/yakos/internal/mtls"
	"github.com/bakw00ds/yakos/internal/netid"
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

// clientNameRe is the positive allow-list for client certificate names (CNs).
// Accepted characters: ASCII letters, digits, dot, underscore, at-sign, hyphen.
// Maximum length: 64 characters.
//
// "." and ".." are explicitly rejected below even though the regex would
// technically allow them (dot is in the character class).  Path separators
// "/" and "\" are outside the class and therefore rejected by the regex itself.
var clientNameRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)

// ValidateClientName returns an error when name is not a safe client
// certificate CN.  A name is valid if it:
//   - matches [A-Za-z0-9._@-]{1,64}
//   - is not the reserved relative-path tokens "." or ".."
//
// The allow-list is applied at every entry point that turns a name into a
// filesystem path, preventing path-traversal attacks.
func ValidateClientName(name string) error {
	if name == "" {
		return fmt.Errorf("client name must not be empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("client name %q is reserved", name)
	}
	if !clientNameRe.MatchString(name) {
		return fmt.Errorf("client name %q is invalid: must match [A-Za-z0-9._@-]{1,64} and contain no path separators", name)
	}
	return nil
}

// Run dispatches the `yakos mtls <subcmd> [args...]` invocation.
// It writes user-facing output to stdout and progress/errors to stderr.
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
		printMTLSHelp(stdout)
		return nil
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "issue-client":
		return runIssueClient(rest, stdout, stderr, stateDir)
	case "list-clients":
		return runListClients(rest, stdout, stderr, stateDir)
	case "show-ca":
		return runShowCA(rest, stdout, stderr, stateDir)
	case "set-role":
		return runSetRole(rest, stdout, stderr, stateDir)
	default:
		return fmt.Errorf("mtls: unknown subcommand %q (try --help)", sub)
	}
}

// ---- issue-client -----------------------------------------------------------

func runIssueClient(args []string, stdout, stderr io.Writer, stateDir string) error {
	name := ""
	outDir := "."
	force := false
	roleName := ""

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--help" || args[i] == "-h":
			printIssueClientHelp(stdout)
			return nil
		case args[i] == "--out":
			i++
			if i >= len(args) {
				return fmt.Errorf("mtls issue-client: --out requires a directory")
			}
			outDir = args[i]
		case strings.HasPrefix(args[i], "--out="):
			outDir = args[i][6:]
		case args[i] == "--force":
			force = true
		case args[i] == "--role":
			i++
			if i >= len(args) {
				return fmt.Errorf("mtls issue-client: --role requires a role name")
			}
			roleName = args[i]
		case strings.HasPrefix(args[i], "--role="):
			roleName = args[i][7:]
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("mtls issue-client: unknown flag %q", args[i])
			}
			if name != "" {
				return fmt.Errorf("mtls issue-client: unexpected argument %q", args[i])
			}
			name = args[i]
		}
	}
	if name == "" {
		return fmt.Errorf("mtls issue-client: missing <name> (try --help)")
	}
	// Validate name before it reaches the filesystem.
	if err := ValidateClientName(name); err != nil {
		return fmt.Errorf("mtls issue-client: invalid name: %w", err)
	}

	// Validate role name if provided.
	if roleName != "" {
		if err := validateRoleStr(roleName); err != nil {
			return fmt.Errorf("mtls issue-client: %w", err)
		}
	}

	// Refuse to overwrite without --force.
	if !force {
		existing, err := mtls.LoadClientCert(stateDir, name)
		if err == nil && existing != nil {
			return fmt.Errorf("mtls issue-client: client cert %q already exists; use --force to overwrite", name)
		}
	}

	caCert, caKey, err := mtls.LoadOrGenerateCA(stateDir)
	if err != nil {
		return fmt.Errorf("mtls issue-client: CA unavailable: %w", err)
	}

	clientCert, err := mtls.IssueClientCert(caCert, caKey, name)
	if err != nil {
		return fmt.Errorf("mtls issue-client: issue cert: %w", err)
	}
	if err := mtls.PersistClientCert(stateDir, name, clientCert); err != nil {
		return fmt.Errorf("mtls issue-client: persist cert: %w", err)
	}

	// Write hand-off bundle.
	if err := os.MkdirAll(outDir, 0700); err != nil {
		return fmt.Errorf("mtls issue-client: mkdir bundle dir: %w", err)
	}
	certFile := filepath.Join(outDir, "client-"+name+".crt")
	keyFile := filepath.Join(outDir, "client-"+name+".key")
	caFile := filepath.Join(outDir, "ca.crt")

	certPEM := pemEncodeCert(clientCert.Certificate[0])
	if err := writeSensitiveFile(certFile, certPEM); err != nil {
		return fmt.Errorf("mtls issue-client: write bundle cert: %w", err)
	}
	keyPEM, err := tlsCertKeyPEM(clientCert)
	if err != nil {
		return fmt.Errorf("mtls issue-client: extract key PEM: %w", err)
	}
	if err := writeSensitiveFile(keyFile, keyPEM); err != nil {
		return fmt.Errorf("mtls issue-client: write bundle key: %w", err)
	}
	// CA cert is public material; 0644 is intentional.
	caCertPEM, err := loadCACertPEM(stateDir)
	if err != nil {
		return fmt.Errorf("mtls issue-client: read CA cert for bundle: %w", err)
	}
	if err := os.WriteFile(caFile, caCertPEM, 0644); err != nil { //nolint:gosec
		return fmt.Errorf("mtls issue-client: write bundle ca.crt: %w", err)
	}

	// Set role if requested.
	if roleName != "" {
		if err := setRoleInFile(stateDir, name, roleName); err != nil {
			return fmt.Errorf("mtls issue-client: set-role: %w", err)
		}
	}

	// Print summary to stdout.
	fmt.Fprintln(stdout, "Issued client certificate")
	fmt.Fprintf(stdout, "  CN (operator_id): %s\n", name)
	fmt.Fprintf(stdout, "  Bundle directory: %s\n", outDir)
	fmt.Fprintln(stdout, "  Files:")
	fmt.Fprintf(stdout, "    %s  (certificate)\n", certFile)
	fmt.Fprintf(stdout, "    %s  (private key, 0600)\n", keyFile)
	fmt.Fprintf(stdout, "    %s  (CA certificate)\n", caFile)
	if roleName != "" {
		fmt.Fprintf(stdout, "  Role assigned: %s\n", roleName)
	} else {
		fmt.Fprintf(stdout, "  Role: read (default — use 'yakos mtls set-role %s <role>' to change)\n", name)
	}
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "To convert to PKCS#12 for browser import:")
	fmt.Fprintf(stdout, "  openssl pkcs12 -export -inkey %s -in %s -certfile %s -out client-%s.p12\n",
		keyFile, certFile, caFile, name)
	fmt.Fprintln(stdout, "")

	// Security warning to stderr (always visible, not paged).
	fmt.Fprintf(stderr, "SECURITY WARNING: The private key at %s is a credential.\n", keyFile)
	fmt.Fprintf(stderr, "  Transmit client-%s.key or client-%s.p12 only over a secure channel\n", name, name)
	fmt.Fprintln(stderr, "  (encrypted email, SSH, password manager, etc.).")
	fmt.Fprintln(stderr, "  Never transmit via unencrypted HTTP, Slack, or email attachment.")

	return nil
}

// ---- list-clients -----------------------------------------------------------

func runListClients(args []string, stdout, _ io.Writer, stateDir string) error {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Fprintln(stdout, "Usage: yakos mtls list-clients")
			fmt.Fprintln(stdout, "  List persisted client CNs with their current roles.")
			return nil
		}
	}

	clientsDir := filepath.Join(stateDir, "mtls", "clients")

	entries, err := os.ReadDir(clientsDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(stdout, "(no client certs issued yet)")
			return nil
		}
		return fmt.Errorf("mtls list-clients: read clients dir: %w", err)
	}

	mapper := netid.NewRoleMapper(stateDir)

	fmt.Fprintf(stdout, "%-30s  %s\n", "CN", "ROLE")
	fmt.Fprintln(stdout, strings.Repeat("-", 45))
	found := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".crt") {
			continue
		}
		cn := strings.TrimSuffix(e.Name(), ".crt")
		role := mapper.Lookup(cn)
		fmt.Fprintf(stdout, "%-30s  %s\n", cn, role.String())
		found = true
	}
	if !found {
		fmt.Fprintln(stdout, "(no client certs issued yet)")
	}
	return nil
}

// ---- show-ca ----------------------------------------------------------------

func runShowCA(args []string, stdout, _ io.Writer, stateDir string) error {
	showPEM := false
	for _, a := range args {
		switch a {
		case "--pem":
			showPEM = true
		case "--help", "-h":
			fmt.Fprintln(stdout, "Usage: yakos mtls show-ca [--pem]")
			fmt.Fprintln(stdout, "  Print the CA cert path and SHA-256 fingerprint.")
			fmt.Fprintln(stdout, "  --pem  Also dump the CA PEM block.")
			return nil
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("mtls show-ca: unknown flag %q", a)
			}
		}
	}

	certPath := filepath.Join(stateDir, "mtls", "ca.crt")

	data, err := os.ReadFile(certPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("mtls show-ca: no CA cert found at %s (start the daemon first to generate one)", certPath)
		}
		return fmt.Errorf("mtls show-ca: read CA cert: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return fmt.Errorf("mtls show-ca: CA cert PEM is malformed")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("mtls show-ca: parse CA cert: %w", err)
	}

	fp := CertFingerprint(cert)
	fmt.Fprintf(stdout, "CA cert path: %s\n", certPath)
	fmt.Fprintf(stdout, "SHA-256:      %s\n", fp)
	fmt.Fprintf(stdout, "Subject:      %s\n", cert.Subject.CommonName)
	fmt.Fprintf(stdout, "Not before:   %s\n", cert.NotBefore.UTC().Format("2006-01-02T15:04:05Z"))
	fmt.Fprintf(stdout, "Not after:    %s\n", cert.NotAfter.UTC().Format("2006-01-02T15:04:05Z"))
	if showPEM {
		fmt.Fprintln(stdout, "")
		fmt.Fprint(stdout, string(data))
	}
	return nil
}

// ---- set-role ---------------------------------------------------------------

func runSetRole(args []string, stdout, _ io.Writer, stateDir string) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(stdout, "Usage: yakos mtls set-role <cn> <role>")
		fmt.Fprintln(stdout, "  Roles: read, dispatch, flows-run, admin")
		return nil
	}
	if len(args) < 2 {
		return fmt.Errorf("mtls set-role: requires <cn> <role> (try --help)")
	}
	cn := args[0]
	roleStr := args[1]

	if err := validateRoleStr(roleStr); err != nil {
		return fmt.Errorf("mtls set-role: %w", err)
	}

	if err := setRoleInFile(stateDir, cn, roleStr); err != nil {
		return fmt.Errorf("mtls set-role: %w", err)
	}

	rolesPath := filepath.Join(stateDir, "mtls", "roles.json")
	fmt.Fprintln(stdout, "Role set:")
	fmt.Fprintf(stdout, "  CN:   %s\n", cn)
	fmt.Fprintf(stdout, "  Role: %s\n", roleStr)
	fmt.Fprintf(stdout, "  File: %s\n", rolesPath)
	return nil
}

// ---- shared helpers ---------------------------------------------------------

// validateRoleStr returns an error when roleStr is not a valid Role string.
func validateRoleStr(roleStr string) error {
	role := netid.ParseRole(roleStr)
	if role.String() != roleStr {
		return fmt.Errorf("unknown role %q; valid: read, dispatch, flows-run, admin", roleStr)
	}
	return nil
}

func setRoleInFile(stateDir, cn, roleStr string) error {
	if err := validateRoleStr(roleStr); err != nil {
		return err
	}

	rolesPath := filepath.Join(stateDir, "mtls", "roles.json")

	// Read current mapping (tolerate missing file).
	mapping := make(map[string]string)
	data, err := os.ReadFile(rolesPath) //nolint:gosec
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read roles.json: %w", err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &mapping); err != nil {
			return fmt.Errorf("parse roles.json: %w", err)
		}
	}

	mapping[cn] = roleStr

	out, err := json.MarshalIndent(mapping, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal roles.json: %w", err)
	}
	out = append(out, '\n')

	dir := filepath.Dir(rolesPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir for roles.json: %w", err)
	}

	// Atomic temp+rename in the same directory.
	tmp := rolesPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0600); err != nil { //nolint:gosec
		return fmt.Errorf("write roles.json.tmp: %w", err)
	}
	if err := os.Rename(tmp, rolesPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename roles.json: %w", err)
	}
	// Belt-and-suspenders: enforce 0600 in case umask is 0.
	if err := os.Chmod(rolesPath, 0600); err != nil { //nolint:gosec
		return fmt.Errorf("chmod roles.json: %w", err)
	}
	return nil
}

// roleExistsInFile returns true when cn appears as a key in roles.json.
func roleExistsInFile(stateDir, cn string) bool {
	rolesPath := filepath.Join(stateDir, "mtls", "roles.json")
	data, err := os.ReadFile(rolesPath) //nolint:gosec
	if err != nil {
		return false
	}
	var mapping map[string]string
	if err := json.Unmarshal(data, &mapping); err != nil {
		return false
	}
	_, ok := mapping[cn]
	return ok
}

// pemEncodeCert PEM-encodes a DER certificate block.
func pemEncodeCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// tlsCertKeyPEM extracts the RSA private key from a tls.Certificate and
// returns its PKCS#1 PEM encoding.  Never logs key bytes.
func tlsCertKeyPEM(cert *tls.Certificate) ([]byte, error) {
	rsaKey, ok := cert.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("unsupported key type %T; only RSA is supported", cert.PrivateKey)
	}
	der := x509.MarshalPKCS1PrivateKey(rsaKey)
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}), nil
}

// writeSensitiveFile writes data to path at mode 0600 (atomic temp+rename).
// All private key material must use this function.
func writeSensitiveFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil { //nolint:gosec
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// loadCACertPEM reads the CA certificate PEM from stateDir.
func loadCACertPEM(stateDir string) ([]byte, error) {
	certPath := filepath.Join(stateDir, "mtls", "ca.crt")
	return os.ReadFile(certPath) //nolint:gosec
}

// CertFingerprint returns the SHA-256 fingerprint of cert.Raw as
// colon-separated uppercase hex bytes.  Used in show-ca and the
// auto-issue bootstrap banner.
func CertFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// printMTLSHelp prints the top-level `yakos mtls` help.
func printMTLSHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: yakos mtls <subcommand> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Manage mTLS client certificates for the networked console (ADR-0004).")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  issue-client <name>     Issue a new client cert and write a hand-off bundle")
	fmt.Fprintln(w, "  list-clients            List persisted client CNs with their current roles")
	fmt.Fprintln(w, "  show-ca                 Print CA cert path and SHA-256 fingerprint")
	fmt.Fprintln(w, "  set-role <cn> <role>    Assign a role to a cert CN in roles.json")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Roles: read | dispatch | flows-run | admin")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Run  yakos mtls <subcommand> --help  for per-subcommand help.")
}

// printIssueClientHelp prints help for `yakos mtls issue-client`.
func printIssueClientHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: yakos mtls issue-client <name> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Issue a new mTLS client certificate and write a portable hand-off bundle.")
	fmt.Fprintln(w, "The certificate CN is set to <name> (used as operator_id in audit logs).")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --out <dir>     Directory for the hand-off bundle (default: cwd)")
	fmt.Fprintln(w, "  --force         Overwrite an existing cert (dangerous: revokes the old cert)")
	fmt.Fprintln(w, "  --role <role>   Also set the role in roles.json (read|dispatch|flows-run|admin)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Output files:")
	fmt.Fprintln(w, "  client-<name>.crt  Certificate (PEM, 0600)")
	fmt.Fprintln(w, "  client-<name>.key  Private key (PEM, 0600) — keep secret")
	fmt.Fprintln(w, "  ca.crt             CA certificate (PEM, 0644)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "PKCS#12 conversion (for browser import):")
	fmt.Fprintln(w, "  openssl pkcs12 -export -inkey client-<name>.key -in client-<name>.crt \\")
	fmt.Fprintln(w, "    -certfile ca.crt -out client-<name>.p12")
}

// OSUsername returns the current OS username, or "admin" if it cannot be
// determined.  Used for the auto-issued bootstrap cert CN.
func OSUsername() string {
	u, err := user.Current()
	if err != nil || u.Username == "" {
		return "admin"
	}
	name := u.Username
	// Strip domain prefix on Windows (DOMAIN\user → user).
	if idx := strings.LastIndex(name, "\\"); idx >= 0 {
		name = name[idx+1:]
	}
	if name == "" {
		return "admin"
	}
	return name
}

// BootstrapResult summarizes what happened during auto-issue.
type BootstrapResult struct {
	// CN is the certificate common name used (or that would have been used).
	CN string
	// BundleDir is the directory where the hand-off bundle was written.
	// Empty when Issued is false.
	BundleDir string
	// Issued is true when a new cert was auto-issued; false when one already existed.
	Issued bool
	// Skipped is true when auto-issue was disabled (noBootstrap=true).
	Skipped bool
	// Err is non-nil when auto-issue was attempted but failed.
	// The banner must display this error rather than claiming success or skip.
	Err error
}

// AutoIssueBootstrap issues a bootstrap client cert if none exists yet.
//
// Called from the networked-bind startup path in serve.go, after CA and server
// cert are established.  The function is idempotent: if any client cert already
// exists in clientDir, it does nothing and returns Issued=false.
//
// The function never returns a non-nil error.  Failures are captured in
// BootstrapResult.Err so the caller (the startup banner) can display the
// failure truthfully without a separate error-handling branch that could leave
// the result in an ambiguous state.
//
// Security note: the auto-issued key is written at 0600 under
// stateDir/mtls/bootstrap/.  Anyone with read access to stateDir already holds
// the CA key and can issue arbitrary client certs; the bootstrap cert does not
// add marginal exposure beyond what holding the CA key already implies.
func AutoIssueBootstrap(stateDir, certName string, noBootstrap bool) BootstrapResult {
	if noBootstrap {
		return BootstrapResult{CN: certName, Skipped: true}
	}

	if certName == "" {
		certName = OSUsername()
	}

	// Sanitise the CN after the OSUsername fallback: if the OS-derived name is
	// not a valid client name (e.g. contains path separators on some platforms),
	// fall back to "admin".
	if err := ValidateClientName(certName); err != nil {
		certName = "admin"
	}

	// Check whether ANY client cert already exists under clientDir.
	clientsDir := filepath.Join(stateDir, "mtls", "clients")
	entries, err := os.ReadDir(clientsDir)
	if err != nil && !os.IsNotExist(err) {
		return BootstrapResult{
			CN:  certName,
			Err: fmt.Errorf("mtlscmd: read clients dir: %w", err),
		}
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".crt") {
			// At least one client cert already exists — do not auto-issue or overwrite.
			return BootstrapResult{CN: certName, Issued: false}
		}
	}

	// No client cert exists. Load/generate CA and issue bootstrap cert.
	caCert, caKey, err := mtls.LoadOrGenerateCA(stateDir)
	if err != nil {
		return BootstrapResult{
			CN:  certName,
			Err: fmt.Errorf("mtlscmd: bootstrap: CA unavailable: %w", err),
		}
	}

	clientCert, err := mtls.IssueClientCert(caCert, caKey, certName)
	if err != nil {
		return BootstrapResult{
			CN:  certName,
			Err: fmt.Errorf("mtlscmd: bootstrap: issue cert: %w", err),
		}
	}
	if err := mtls.PersistClientCert(stateDir, certName, clientCert); err != nil {
		return BootstrapResult{
			CN:  certName,
			Err: fmt.Errorf("mtlscmd: bootstrap: persist cert: %w", err),
		}
	}

	// Write hand-off bundle to stateDir/mtls/bootstrap/.
	bundleDir := filepath.Join(stateDir, "mtls", "bootstrap")
	if err := os.MkdirAll(bundleDir, 0700); err != nil {
		return BootstrapResult{
			CN:  certName,
			Err: fmt.Errorf("mtlscmd: bootstrap: mkdir bundle: %w", err),
		}
	}

	certFile := filepath.Join(bundleDir, "client-"+certName+".crt")
	keyFile := filepath.Join(bundleDir, "client-"+certName+".key")
	caFile := filepath.Join(bundleDir, "ca.crt")

	certPEM := pemEncodeCert(clientCert.Certificate[0])
	if err := writeSensitiveFile(certFile, certPEM); err != nil {
		return BootstrapResult{
			CN:  certName,
			Err: fmt.Errorf("mtlscmd: bootstrap: write cert: %w", err),
		}
	}
	keyPEMBytes, err := tlsCertKeyPEM(clientCert)
	if err != nil {
		return BootstrapResult{
			CN:  certName,
			Err: fmt.Errorf("mtlscmd: bootstrap: extract key: %w", err),
		}
	}
	if err := writeSensitiveFile(keyFile, keyPEMBytes); err != nil {
		return BootstrapResult{
			CN:  certName,
			Err: fmt.Errorf("mtlscmd: bootstrap: write key: %w", err),
		}
	}
	caCertPEM, err := loadCACertPEM(stateDir)
	if err != nil {
		return BootstrapResult{
			CN:  certName,
			Err: fmt.Errorf("mtlscmd: bootstrap: read CA PEM: %w", err),
		}
	}
	if err := os.WriteFile(caFile, caCertPEM, 0644); err != nil { //nolint:gosec
		return BootstrapResult{
			CN:  certName,
			Err: fmt.Errorf("mtlscmd: bootstrap: write ca.crt: %w", err),
		}
	}

	// Assign admin role in roles.json if the CN has no existing entry.
	// This is the bootstrapping operator; they need admin to enroll other operators.
	if !roleExistsInFile(stateDir, certName) {
		if err := setRoleInFile(stateDir, certName, "admin"); err != nil {
			return BootstrapResult{
				CN:  certName,
				Err: fmt.Errorf("mtlscmd: bootstrap: set admin role: %w", err),
			}
		}
	}

	return BootstrapResult{
		CN:        certName,
		BundleDir: bundleDir,
		Issued:    true,
	}
}
