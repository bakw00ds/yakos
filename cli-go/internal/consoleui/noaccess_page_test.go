package consoleui_test

// K-110: a cert that resolves to RoleNone gets an explanatory 403 page at "/"
// instead of the SPA shell followed by 403 on every data route.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getIndexAsCert(h http.Handler, cn string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if cn != "" {
		req.TLS = certState(cn)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestIndex_RoleNoneCert_Serves403NoAccessPage(t *testing.T) {
	stateDir := t.TempDir()
	writeRoleMap(t, stateDir, map[string]string{"alice": "admin", "bad": "superuser", "denied": "none"})
	h := newCertRoleServer(t, stateDir)

	for _, cn := range []string{"stranger", "bad", "denied"} {
		rr := getIndexAsCert(h, cn)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s: status %d; want 403", cn, rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "yakos mtls set-role "+cn) {
			t.Errorf("%s: page does not name the fix command:\n%s", cn, body)
		}
		if strings.Contains(body, "<script") {
			t.Errorf("%s: page must not be the SPA shell", cn)
		}
		if !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s: Content-Type %q", cn, rr.Header().Get("Content-Type"))
		}
	}
}

func TestIndex_MappedCert_ServesShell(t *testing.T) {
	stateDir := t.TempDir()
	writeRoleMap(t, stateDir, map[string]string{"alice": "read"})
	h := newCertRoleServer(t, stateDir)
	rr := getIndexAsCert(h, "alice")
	if rr.Code != http.StatusOK {
		t.Fatalf("mapped cert: status %d; want 200", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "No access") {
		t.Error("mapped cert must get the shell, not the no-access page")
	}
}

func TestIndex_Loopback_Unchanged(t *testing.T) {
	stateDir := t.TempDir() // no roles.json: any cert would be RoleNone
	h := newCertRoleServer(t, stateDir)
	// certless request through a non-loopback-trusting resolver is
	// unauthenticated, so it is not the "authenticated but unmapped" case.
	if rr := getIndexAsCert(h, ""); rr.Code == http.StatusForbidden && strings.Contains(rr.Body.String(), "No access") {
		t.Error("unauthenticated request must not get the no-access page")
	}
}

func TestIndex_NoAccessPage_EscapesCN(t *testing.T) {
	stateDir := t.TempDir()
	h := newCertRoleServer(t, stateDir)
	rr := getIndexAsCert(h, `<script>alert(1)</script>`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "<script>alert") {
		t.Errorf("CN not escaped:\n%s", rr.Body.String())
	}
}
