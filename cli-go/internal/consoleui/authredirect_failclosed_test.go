package consoleui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bakw00ds/yakos/internal/netid"
)

// K-86 review (LOW): requireAuthOrRedirect passed an UNRESOLVED identity
// straight through. It must fail closed like the R17 role gates.
func TestRequireAuthOrRedirect_UnresolvedIdentity_FailsClosed(t *testing.T) {
	called := false
	h := requireAuthOrRedirect(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	for name, ctxID := range map[string]*netid.Identity{"absent": nil, "explicit zero": {}} {
		called = false
		r := httptest.NewRequest(http.MethodGet, "/api/board", nil)
		if ctxID != nil {
			r = r.WithContext(netid.WithIdentityForTest(r.Context(), *ctxID))
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		if called {
			t.Errorf("%s: unresolved identity reached the protected handler", name)
		}
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s: status=%d; want 401", name, rr.Code)
		}
	}

	// A resolved authenticated identity still passes.
	called = false
	r := httptest.NewRequest(http.MethodGet, "/api/board", nil)
	r = r.WithContext(netid.WithIdentityForTest(r.Context(), netid.Identity{OperatorID: "a", Resolved: true, Authenticated: true}))
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !called {
		t.Error("authenticated identity was blocked")
	}
}
