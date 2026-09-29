package netid_test

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bakw00ds/yakos/internal/netid"
)

// S-2 (RoleMapper/SAN item; R8 and K8 in s2/k82 reviews): a verified client
// certificate with no Subject CN (SAN-only, which modern CAs commonly issue)
// used to resolve to Identity{OperatorID: "", Authenticated: true}. Every such
// certificate then collapsed into one shared anonymous principal, and an
// authenticated identity with an empty ID reads as "unowned" to owner checks.

func sanOnlyState() *tls.ConnectionState {
	cert := &x509.Certificate{Subject: pkix.Name{CommonName: ""}, DNSNames: []string{"machine.example"}}
	return &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{cert}}}
}

func TestCNFromTLS_EmptyCN_ReturnsFalse(t *testing.T) {
	t.Parallel()
	cn, ok := netid.CNFromTLS(sanOnlyState())
	if ok || cn != "" {
		t.Errorf("CNFromTLS(SAN-only cert) = (%q, %v); want (\"\", false)", cn, ok)
	}
}

func TestResolver_SANOnlyCert_NotAnAuthenticatedIdentity(t *testing.T) {
	t.Parallel()
	m := netid.NewRoleMapper(t.TempDir())
	res := netid.NewResolver(m, func(*http.Request) string { return "" }, false)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.TLS = sanOnlyState()

	id := res.Resolve(r)
	if id.Authenticated {
		t.Error("SAN-only client cert produced an Authenticated identity")
	}
	if id.AuthMethod == netid.AuthMethodCert {
		t.Error("SAN-only client cert resolved with AuthMethodCert")
	}
	if id.Role.Allows(netid.RoleRead) {
		t.Errorf("SAN-only client cert on a networked resolver got role %v; want fail-closed RoleNone", id.Role)
	}
	if id.OperatorID != "" {
		t.Errorf("OperatorID=%q; want empty", id.OperatorID)
	}
}
