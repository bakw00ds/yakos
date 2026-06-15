// Package mtls implements certificate generation and mutual TLS configuration
// for cross-machine yakOS daemon connections.
//
// # Architecture
//
// A self-signed CA certificate is generated on first daemon start and persisted
// at ~/.yakos-state/mtls/ca.{crt,key} (mode 0600).  The daemon issues server
// and client certificates signed by this CA.
//
// Loopback addresses (127.0.0.1, ::1) continue to use the bearer-token model.
// Non-loopback listeners REQUIRE mutual TLS; the daemon refuses to bind them
// without a valid CA and server certificate.
//
// # Certificate issuance
//
//   - CA: 10-year validity, RSA-2048, self-signed.
//   - Server cert: 1-year validity, signed by CA, SubjectAltNames = provided hostnames.
//   - Client cert: 1-year validity, signed by CA, CN = clientName.
//
// # Cross-platform notes
//
// File permissions are set to 0600 on Unix.  Windows ACL hardening is a
// Phase 1.5 follow-up (#3); on Windows the files are created with the default
// process ACL (operator must restrict manually).
//
// # Stability: experimental
package mtls

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/bakw00ds/yakos/internal/winsec"
)

// caKeyBits is the RSA key size for all certificates.
const caKeyBits = 2048

// certDir returns the mtls subdirectory of stateDir.
func certDir(stateDir string) string {
	return filepath.Join(stateDir, "mtls")
}

// caFiles returns the CA cert and key paths.
func caFiles(stateDir string) (certPath, keyPath string) {
	dir := certDir(stateDir)
	return filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
}

// clientDir returns the client certificate directory.
func clientDir(stateDir string) string {
	return filepath.Join(stateDir, "mtls", "clients")
}

// LoadOrGenerateCA loads the CA certificate and key from stateDir.
// If absent, a new self-signed CA is generated and persisted.
//
// Files are created at mode 0600.
//
// Errors:
//   - returns a wrapped error on I/O or crypto failure
func LoadOrGenerateCA(stateDir string) (*x509.Certificate, *rsa.PrivateKey, error) {
	certPath, keyPath := caFiles(stateDir)

	// Try to load existing.
	cert, key, err := loadCertKey(certPath, keyPath)
	if err == nil {
		return cert, key, nil
	}
	if !os.IsNotExist(err) {
		// File exists but is corrupt.
		return nil, nil, fmt.Errorf("mtls: load CA: %w", err)
	}

	// Generate new CA.
	return generateCA(stateDir, certPath, keyPath)
}

// IssueServerCert creates a server TLS certificate signed by the given CA.
// The certificate includes SubjectAltNames for each hostname/IP in hostnames.
//
// Errors:
//   - returns error on key generation or signing failure
func IssueServerCert(caCert *x509.Certificate, caKey *rsa.PrivateKey, hostnames []string) (*tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, caKeyBits)
	if err != nil {
		return nil, fmt.Errorf("mtls: server cert: generate key: %w", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: newSerial(),
		Subject:      pkix.Name{CommonName: "yakos-server"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hostnames {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	// Always include loopback so tests work.
	tmpl.IPAddresses = appendIfMissing(tmpl.IPAddresses, net.ParseIP("127.0.0.1"), net.ParseIP("::1"))

	return signLeafCert(tmpl, caCert, key, caKey)
}

// IssueClientCert creates a client TLS certificate signed by the given CA.
// The certificate CN is set to clientName.
//
// Errors:
//   - returns error on key generation or signing failure
func IssueClientCert(caCert *x509.Certificate, caKey *rsa.PrivateKey, clientName string) (*tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, caKeyBits)
	if err != nil {
		return nil, fmt.Errorf("mtls: client cert: generate key: %w", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: newSerial(),
		Subject:      pkix.Name{CommonName: clientName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	return signLeafCert(tmpl, caCert, key, caKey)
}

// PersistClientCert writes a client cert and key to stateDir/mtls/clients/<name>.{crt,key}.
// Files are created at mode 0600.
//
// clientName must not be empty, must not be "." or "..", and must not contain
// "/" or "\" (path separators).  These checks are defense-in-depth: callers
// (internal/mtlscmd) apply the full positive allow-list before reaching here.
//
// Errors:
//   - returns wrapped error on invalid name or I/O failure
func PersistClientCert(stateDir, clientName string, cert *tls.Certificate) error {
	if err := validateClientNameBasic(clientName); err != nil {
		return fmt.Errorf("mtls: PersistClientCert: %w", err)
	}
	dir := clientDir(stateDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mtls: mkdir clients: %w", err)
	}
	certPath := filepath.Join(dir, clientName+".crt")
	keyPath := filepath.Join(dir, clientName+".key")
	return writeTLSCertKey(cert, certPath, keyPath)
}

// validateClientNameBasic is a defense-in-depth path-traversal guard inside
// the mtls package.  It rejects the most dangerous inputs: empty name, ".",
// "..", and names containing "/" or "\" (path separators on any OS).
//
// The full positive allow-list (^[A-Za-z0-9._@-]{1,64}$) is enforced by the
// mtlscmd layer before this function is reached; this check catches any caller
// that bypasses mtlscmd.ValidateClientName.
func validateClientNameBasic(name string) error {
	if name == "" {
		return fmt.Errorf("client name must not be empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("client name %q is a reserved path component", name)
	}
	for _, ch := range name {
		if ch == '/' || ch == '\\' {
			return fmt.Errorf("client name %q contains a path separator", name)
		}
	}
	return nil
}

// LoadClientCert loads a previously-persisted client cert from stateDir.
//
// Errors:
//   - returns error if the cert files are absent or corrupt
func LoadClientCert(stateDir, clientName string) (*tls.Certificate, error) {
	dir := clientDir(stateDir)
	certPath := filepath.Join(dir, clientName+".crt")
	keyPath := filepath.Join(dir, clientName+".key")

	_, _, err := loadCertKey(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("mtls: load client cert %q: %w", clientName, err)
	}
	tlsCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("mtls: load client tls cert %q: %w", clientName, err)
	}
	return &tlsCert, nil
}

// BuildServerTLSConfig builds a *tls.Config for the daemon's strict mTLS
// listeners (machine-to-machine paths).  It REQUIRES and verifies client
// certificates signed by caPool — a connection without a client certificate
// is rejected at the TLS handshake.
//
// Use BuildServerTLSConfigHybrid for the console networked listener where
// password+session users (who present no client cert) must also be admitted.
//
// The serverCert is presented to connecting clients.
func BuildServerTLSConfig(serverCert *tls.Certificate, caPool *x509.CertPool) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{*serverCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}
}

// BuildServerTLSConfigHybrid builds a *tls.Config for the console networked
// listener under ADR-0005 hybrid auth.  It uses VerifyClientCertIfGiven
// instead of RequireAndVerifyClientCert so that:
//
//   - A client that presents a certificate MUST have it verified against
//     caPool — an untrusted cert still causes a handshake failure.
//   - A client that presents NO certificate completes the handshake; the
//     TLS stack leaves r.TLS.VerifiedChains empty, and the HTTP-layer
//     identity resolver falls through to the session-cookie or fail-closed
//     path (ADR-0005 §Resolution precedence).
//
// All other parameters (ClientCAs, MinVersion, ciphers) are identical to
// BuildServerTLSConfig — only the REQUIREMENT to present a cert is relaxed.
//
// This function is deliberately separate from BuildServerTLSConfig so that
// strict machine-to-machine callers are not silently changed.  Call sites
// that must keep the strict requirement (e.g. future gRPC mTLS) continue to
// use BuildServerTLSConfig.
func BuildServerTLSConfigHybrid(serverCert *tls.Certificate, caPool *x509.CertPool) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{*serverCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.VerifyClientCertIfGiven,
		MinVersion:   tls.VersionTLS12,
	}
}

// BuildClientTLSConfig builds a *tls.Config for operator clients connecting to
// a daemon over mTLS.  The clientCert is presented to the server; caPool is
// used to verify the server's certificate.
func BuildClientTLSConfig(clientCert *tls.Certificate, caPool *x509.CertPool) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{*clientCert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS12,
	}
}

// CertPoolFromCert creates an *x509.CertPool containing the given certificate.
func CertPoolFromCert(cert *x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return pool
}

// IsNonLoopback returns true when addr is not a loopback address.
// Loopback: 127.0.0.1, ::1, or hostname "localhost".
// mTLS is REQUIRED for non-loopback daemon listeners.
func IsNonLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// addr may not have a port (e.g. in tests).
		host = addr
	}
	if host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true // unresolved hostname → assume non-loopback
	}
	return !ip.IsLoopback()
}

// ---- private helpers --------------------------------------------------------

// generateCA creates a new self-signed CA and persists it.
func generateCA(stateDir, certPath, keyPath string) (*x509.Certificate, *rsa.PrivateKey, error) {
	if err := os.MkdirAll(certDir(stateDir), 0700); err != nil {
		return nil, nil, fmt.Errorf("mtls: mkdir: %w", err)
	}

	key, err := rsa.GenerateKey(rand.Reader, caKeyBits)
	if err != nil {
		return nil, nil, fmt.Errorf("mtls: generate CA key: %w", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          newSerial(),
		Subject:               pkix.Name{CommonName: "yakos-ca", Organization: []string{"yakOS"}},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("mtls: sign CA: %w", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("mtls: parse CA cert: %w", err)
	}

	// Persist cert.
	if err := writeFile(certPath, pemEncodeCert(der)); err != nil {
		return nil, nil, fmt.Errorf("mtls: write CA cert: %w", err)
	}
	// Persist key.
	keyPEM := pemEncodeKey(key)
	if err := writeFile(keyPath, keyPEM); err != nil {
		return nil, nil, fmt.Errorf("mtls: write CA key: %w", err)
	}

	return cert, key, nil
}

// signLeafCert creates a leaf certificate from tmpl using the CA.
func signLeafCert(tmpl, caCert *x509.Certificate, leafKey *rsa.PrivateKey, caKey *rsa.PrivateKey) (*tls.Certificate, error) {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("mtls: sign cert (%s): %w", tmpl.Subject.CommonName, err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("mtls: parse signed cert: %w", err)
	}
	return &tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  leafKey,
		Leaf:        leaf,
	}, nil
}

// newSerial generates a random 128-bit serial number.
func newSerial() *big.Int {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic("mtls: generate serial: " + err.Error())
	}
	return serial
}

// pemEncodeCert PEM-encodes a DER certificate block.
func pemEncodeCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// pemEncodeKey PEM-encodes an RSA private key.
func pemEncodeKey(key *rsa.PrivateKey) []byte {
	der := x509.MarshalPKCS1PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
}

// writeFile writes data to path atomically (temp+rename) at mode 0600, then
// applies Windows NTFS ACL hardening via winsec.SecureFile (no-op on Unix).
func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil { //nolint:gosec
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Apply Windows NTFS ACL hardening (no-op on non-Windows; 0600 above
	// is the POSIX guard on Unix/macOS).
	return winsec.SecureFile(path)
}

// writeTLSCertKey writes a tls.Certificate's cert and private key to certPath/keyPath.
func writeTLSCertKey(cert *tls.Certificate, certPath, keyPath string) error {
	if len(cert.Certificate) == 0 {
		return fmt.Errorf("mtls: no DER blocks in certificate")
	}
	certPEM := pemEncodeCert(cert.Certificate[0])
	if err := writeFile(certPath, certPEM); err != nil {
		return fmt.Errorf("mtls: write cert %s: %w", certPath, err)
	}
	key, ok := cert.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		return fmt.Errorf("mtls: unsupported key type %T", cert.PrivateKey)
	}
	keyPEM := pemEncodeKey(key)
	if err := writeFile(keyPath, keyPEM); err != nil {
		return fmt.Errorf("mtls: write key %s: %w", keyPath, err)
	}
	return nil
}

// loadCertKey loads a PEM-encoded cert and key from disk.
func loadCertKey(certPath, keyPath string) (*x509.Certificate, *rsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(certPath) //nolint:gosec
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(keyPath) //nolint:gosec
	if err != nil {
		return nil, nil, err
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("mtls: decode cert PEM: no block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("mtls: parse cert: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("mtls: decode key PEM: no block")
	}
	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("mtls: parse key: %w", err)
	}

	return cert, key, nil
}

// appendIfMissing appends IPs to the slice only if not already present.
func appendIfMissing(existing []net.IP, ips ...net.IP) []net.IP {
	for _, ip := range ips {
		found := false
		for _, ex := range existing {
			if ex.Equal(ip) {
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, ip)
		}
	}
	return existing
}
