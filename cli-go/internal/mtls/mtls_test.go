package mtls_test

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/mtls"
)

// ---- CA generation tests ----------------------------------------------------

func TestLoadOrGenerateCA_CreatesCA(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cert, key, err := mtls.LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}
	if cert == nil {
		t.Fatal("cert is nil")
	}
	if key == nil {
		t.Fatal("key is nil")
	}
}

func TestLoadOrGenerateCA_IsCA(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cert, _, err := mtls.LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}
	if !cert.IsCA {
		t.Error("generated cert should be a CA")
	}
}

func TestLoadOrGenerateCA_PersistsFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, _, err := mtls.LoadOrGenerateCA(dir); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Files should exist.
	certPath := filepath.Join(dir, "mtls", "ca.crt")
	keyPath := filepath.Join(dir, "mtls", "ca.key")
	if _, err := os.Stat(certPath); err != nil {
		t.Errorf("ca.crt not found: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("ca.key not found: %v", err)
	}
}

func TestLoadOrGenerateCA_LoadsExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cert1, _, err := mtls.LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Second call should return the same cert (same serial).
	cert2, _, err := mtls.LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if cert1.SerialNumber.Cmp(cert2.SerialNumber) != 0 {
		t.Error("second call should load the same certificate")
	}
}

func TestLoadOrGenerateCA_RSA2048(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, key, err := mtls.LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}
	rsaKey, ok := interface{}(key).(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("key is %T; want *rsa.PrivateKey", key)
	}
	if rsaKey.N.BitLen() != 2048 {
		t.Errorf("key bits=%d; want 2048", rsaKey.N.BitLen())
	}
}

func TestLoadOrGenerateCA_10YearValidity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cert, _, err := mtls.LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}
	validity := cert.NotAfter.Sub(cert.NotBefore)
	// Should be ~10 years (±1 day tolerance for test timing).
	tenYears := 10 * 365 * 24 * time.Hour
	if validity < tenYears-24*time.Hour || validity > tenYears+48*time.Hour {
		t.Errorf("CA validity=%v; want ~10 years", validity)
	}
}

// ---- Server certificate tests -----------------------------------------------

func TestIssueServerCert_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caCert, caKey, err := mtls.LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}
	serverCert, err := mtls.IssueServerCert(caCert, caKey, []string{"localhost"})
	if err != nil {
		t.Fatalf("IssueServerCert: %v", err)
	}
	if serverCert == nil {
		t.Fatal("serverCert is nil")
	}
}

func TestIssueServerCert_IncludesLoopback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caCert, caKey, _ := mtls.LoadOrGenerateCA(dir)

	serverCert, err := mtls.IssueServerCert(caCert, caKey, []string{})
	if err != nil {
		t.Fatalf("IssueServerCert: %v", err)
	}
	leaf := serverCert.Leaf
	if leaf == nil {
		leaf = parseLeaf(t, serverCert)
	}
	found127 := false
	for _, ip := range leaf.IPAddresses {
		if ip.Equal(net.ParseIP("127.0.0.1")) {
			found127 = true
		}
	}
	if !found127 {
		t.Error("server cert should always include 127.0.0.1 SAN")
	}
}

func TestIssueServerCert_SignedByCA(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caCert, caKey, _ := mtls.LoadOrGenerateCA(dir)
	serverCert, _ := mtls.IssueServerCert(caCert, caKey, []string{"localhost"})

	pool := mtls.CertPoolFromCert(caCert)
	leaf := serverCert.Leaf
	if leaf == nil {
		leaf = parseLeaf(t, serverCert)
	}
	opts := x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if _, err := leaf.Verify(opts); err != nil {
		t.Errorf("server cert fails CA verification: %v", err)
	}
}

func TestIssueServerCert_1YearValidity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caCert, caKey, _ := mtls.LoadOrGenerateCA(dir)
	serverCert, _ := mtls.IssueServerCert(caCert, caKey, []string{})
	leaf := serverCert.Leaf
	if leaf == nil {
		leaf = parseLeaf(t, serverCert)
	}
	validity := leaf.NotAfter.Sub(leaf.NotBefore)
	oneYear := 365 * 24 * time.Hour
	if validity < oneYear-24*time.Hour || validity > oneYear+48*time.Hour {
		t.Errorf("server cert validity=%v; want ~1 year", validity)
	}
}

// ---- Client certificate tests -----------------------------------------------

func TestIssueClientCert_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caCert, caKey, _ := mtls.LoadOrGenerateCA(dir)
	clientCert, err := mtls.IssueClientCert(caCert, caKey, "alice")
	if err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}
	if clientCert == nil {
		t.Fatal("clientCert is nil")
	}
}

func TestIssueClientCert_CNMatchesName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caCert, caKey, _ := mtls.LoadOrGenerateCA(dir)
	clientCert, _ := mtls.IssueClientCert(caCert, caKey, "bob-workstation")
	leaf := clientCert.Leaf
	if leaf == nil {
		leaf = parseLeaf(t, clientCert)
	}
	if leaf.Subject.CommonName != "bob-workstation" {
		t.Errorf("CN=%q; want bob-workstation", leaf.Subject.CommonName)
	}
}

func TestIssueClientCert_SignedByCA(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caCert, caKey, _ := mtls.LoadOrGenerateCA(dir)
	clientCert, _ := mtls.IssueClientCert(caCert, caKey, "test-client")

	pool := mtls.CertPoolFromCert(caCert)
	leaf := clientCert.Leaf
	if leaf == nil {
		leaf = parseLeaf(t, clientCert)
	}
	opts := x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if _, err := leaf.Verify(opts); err != nil {
		t.Errorf("client cert fails CA verification: %v", err)
	}
}

// ---- PersistClientCert / LoadClientCert tests -------------------------------

func TestPersistClientCert_WritesFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caCert, caKey, _ := mtls.LoadOrGenerateCA(dir)
	clientCert, _ := mtls.IssueClientCert(caCert, caKey, "persist-test")

	if err := mtls.PersistClientCert(dir, "persist-test", clientCert); err != nil {
		t.Fatalf("PersistClientCert: %v", err)
	}
	certPath := filepath.Join(dir, "mtls", "clients", "persist-test.crt")
	keyPath := filepath.Join(dir, "mtls", "clients", "persist-test.key")
	if _, err := os.Stat(certPath); err != nil {
		t.Errorf("client cert file not found: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("client key file not found: %v", err)
	}
}

func TestLoadClientCert_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caCert, caKey, _ := mtls.LoadOrGenerateCA(dir)
	clientCert, _ := mtls.IssueClientCert(caCert, caKey, "roundtrip-client")

	if err := mtls.PersistClientCert(dir, "roundtrip-client", clientCert); err != nil {
		t.Fatalf("PersistClientCert: %v", err)
	}
	loaded, err := mtls.LoadClientCert(dir, "roundtrip-client")
	if err != nil {
		t.Fatalf("LoadClientCert: %v", err)
	}
	if loaded == nil {
		t.Fatal("loaded cert is nil")
	}
}

// ---- Mutual TLS round-trip tests --------------------------------------------

func TestMutualTLS_ServerClientRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caCert, caKey, err := mtls.LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}

	serverCert, err := mtls.IssueServerCert(caCert, caKey, []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("IssueServerCert: %v", err)
	}

	clientCert, err := mtls.IssueClientCert(caCert, caKey, "test-roundtrip")
	if err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}

	caPool := mtls.CertPoolFromCert(caCert)
	serverTLSCfg := mtls.BuildServerTLSConfig(serverCert, caPool)
	clientTLSCfg := mtls.BuildClientTLSConfig(clientCert, caPool)

	// Start a TLS server on a random port.
	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLSCfg)
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = conn.Close() }()
		// Force handshake.
		tlsConn := conn.(*tls.Conn)
		done <- tlsConn.Handshake()
	}()

	// Client connects.
	conn, err := tls.Dial("tcp", ln.Addr().String(), clientTLSCfg)
	if err != nil {
		t.Fatalf("tls.Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server handshake: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server handshake timeout")
	}
}

func TestMutualTLS_UntrustedClient_Rejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caCert, caKey, _ := mtls.LoadOrGenerateCA(dir)

	// Issue a second CA and a client cert from it (untrusted by the server).
	dir2 := t.TempDir()
	caCert2, caKey2, _ := mtls.LoadOrGenerateCA(dir2)
	untrustedClientCert, _ := mtls.IssueClientCert(caCert2, caKey2, "untrusted")

	serverCert, _ := mtls.IssueServerCert(caCert, caKey, []string{"127.0.0.1"})
	caPool := mtls.CertPoolFromCert(caCert)
	serverTLSCfg := mtls.BuildServerTLSConfig(serverCert, caPool)

	// Client trusts the server cert (signed by caCert) but presents an untrusted client cert.
	clientTLSCfg := mtls.BuildClientTLSConfig(untrustedClientCert, mtls.CertPoolFromCert(caCert))

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLSCfg)
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	// Server: accept, force the handshake to completion (which will reject the client cert).
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.(*tls.Conn).Handshake()
	}()

	// In TLS 1.3 the client-auth alert may arrive after tls.Dial returns.
	// We force a roundtrip (Read) so the alert is propagated to the caller.
	conn, dialErr := tls.Dial("tcp", ln.Addr().String(), clientTLSCfg)
	if dialErr != nil {
		// tls.Dial itself returned an error — the rejection is visible immediately.
		return
	}
	// Connection appeared to succeed; force a read to surface the post-handshake alert.
	buf := make([]byte, 1)
	_, readErr := conn.Read(buf)
	_ = conn.Close()
	if readErr == nil {
		t.Error("expected TLS error for untrusted client cert; got nil on both Dial and Read")
	}
}

// ---- IsNonLoopback tests ----------------------------------------------------

func TestIsNonLoopback_Loopback127(t *testing.T) {
	if mtls.IsNonLoopback("127.0.0.1:7891") {
		t.Error("127.0.0.1:7891 should be loopback")
	}
}

func TestIsNonLoopback_LoopbackIPv6(t *testing.T) {
	if mtls.IsNonLoopback("[::1]:7891") {
		t.Error("::1 should be loopback")
	}
}

func TestIsNonLoopback_Localhost(t *testing.T) {
	if mtls.IsNonLoopback("localhost:7891") {
		t.Error("localhost should be loopback")
	}
}

func TestIsNonLoopback_PublicIP(t *testing.T) {
	if !mtls.IsNonLoopback("192.168.1.100:7891") {
		t.Error("192.168.1.100 should be non-loopback")
	}
}

func TestIsNonLoopback_AllInterfaces(t *testing.T) {
	if !mtls.IsNonLoopback("0.0.0.0:7891") {
		t.Error("0.0.0.0 should be non-loopback (binds all interfaces)")
	}
}

func TestIsNonLoopback_Hostname(t *testing.T) {
	if !mtls.IsNonLoopback("dev-machine:7891") {
		t.Error("unresolved hostname should be treated as non-loopback")
	}
}

// ---- CertPoolFromCert tests -------------------------------------------------

func TestCertPoolFromCert_NotNil(t *testing.T) {
	dir := t.TempDir()
	caCert, _, _ := mtls.LoadOrGenerateCA(dir)
	pool := mtls.CertPoolFromCert(caCert)
	if pool == nil {
		t.Error("CertPoolFromCert returned nil pool")
	}
}

// ---- helpers ----------------------------------------------------------------

// parseLeaf parses the first DER block in tlsCert.Certificate.
func parseLeaf(t *testing.T, tlsCert *tls.Certificate) *x509.Certificate {
	t.Helper()
	if len(tlsCert.Certificate) == 0 {
		t.Fatal("tls.Certificate has no DER blocks")
	}
	cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		t.Fatalf("parseLeaf: %v", err)
	}
	return cert
}
