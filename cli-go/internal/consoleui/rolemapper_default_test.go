package consoleui_test

// K-98: with the RoleMapper failing closed, a CA-signed cert whose CN is not
// in roles.json gets 403 on every RoleRead route; "*" restores read.

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

func writeRoleMap(t *testing.T, stateDir string, m map[string]string) {
	t.Helper()
	dir := filepath.Join(stateDir, "mtls")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "roles.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func certState(cn string) *tls.ConnectionState {
	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: cn}}
	return &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf}}}
}

// roleReadRoutes are the RoleRead routes named in the K-86 review.
var roleReadRoutes = []struct{ name, path string }{
	{"kanban board", "/kanban/api/board"},
	{"flows list", "/flows/api/workflows"},
	{"flows get", "/flows/api/workflow?name=x"},
	{"cost", "/cost/"},
	{"perf", "/perf/"},
	{"presence", "/api/presence"},
	{"chat transcript", "/api/chat/transcript?conversation_id=x"},
}

func newCertRoleServer(t *testing.T, stateDir string) http.Handler {
	t.Helper()
	tk, err := consoleui.LoadOrCreateToken(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tk,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           t.TempDir(),
		StateDir:          stateDir,
	})
	res := netid.NewResolver(netid.NewRoleMapper(stateDir), nil, false)
	return res.Middleware(srv.HandlerForTest())
}

func getAsCert(h http.Handler, cn, path string) int {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.TLS = certState(cn)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}

func TestRoleMapperDefault_UnmappedCert_Forbidden_OnEveryRoleReadRoute(t *testing.T) {
	stateDir := t.TempDir()
	writeRoleMap(t, stateDir, map[string]string{"alice": "admin"})
	h := newCertRoleServer(t, stateDir)
	for _, rt := range roleReadRoutes {
		if got := getAsCert(h, "stranger", rt.path); got != http.StatusForbidden {
			t.Errorf("%s: unmapped cert got %d; want 403", rt.name, got)
		}
		if got := getAsCert(h, "alice", rt.path); got == http.StatusForbidden {
			t.Errorf("%s: mapped cert got 403; want allowed", rt.name)
		}
	}
}

func TestRoleMapperDefault_EmptyOrMissingMap_Forbidden(t *testing.T) {
	for name, setup := range map[string]func(string){
		"missing": func(string) {},
		"empty":   func(d string) { writeRoleMap(t, d, map[string]string{}) },
	} {
		stateDir := t.TempDir()
		setup(stateDir)
		h := newCertRoleServer(t, stateDir)
		for _, rt := range roleReadRoutes {
			if got := getAsCert(h, "anyone", rt.path); got != http.StatusForbidden {
				t.Errorf("%s map, %s: got %d; want 403", name, rt.name, got)
			}
		}
	}
}

func TestRoleMapperDefault_WildcardOptIn_RestoresRead_ButNotDispatch(t *testing.T) {
	stateDir := t.TempDir()
	writeRoleMap(t, stateDir, map[string]string{"*": "read"})
	h := newCertRoleServer(t, stateDir)
	for _, rt := range roleReadRoutes {
		if got := getAsCert(h, "stranger", rt.path); got == http.StatusForbidden {
			t.Errorf("%s: wildcard read cert got 403; want allowed", rt.name)
		}
	}
	// read must not reach a RoleDispatch route.
	req := httptest.NewRequest(http.MethodPost, "/flows/api/cancel?id=x", nil)
	req.TLS = certState("stranger")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("wildcard-read cert on RoleFlowsRun route: got %d; want 403", rr.Code)
	}
}

func TestRoleMapperDefault_ExplicitMappingBeatsWildcard(t *testing.T) {
	stateDir := t.TempDir()
	writeRoleMap(t, stateDir, map[string]string{"*": "read", "bad": "superuser"})
	h := newCertRoleServer(t, stateDir)
	// Unknown role string -> RoleNone, even though a wildcard exists.
	if got := getAsCert(h, "bad", "/api/presence"); got != http.StatusForbidden {
		t.Errorf("unknown-role CN: got %d; want 403", got)
	}
}

func TestRoleMapperDefault_LoopbackUnaffected(t *testing.T) {
	stateDir := t.TempDir() // no roles.json at all
	tk, err := consoleui.LoadOrCreateToken(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	srv := consoleui.MustNew(t, consoleui.Config{
		Token: tk, KanbanBoardPath: t.TempDir() + "/kanban.md", KanbanProject: "t",
		MetricsProjectDir: t.TempDir(), PerfWorkDir: t.TempDir(), Bus: bus,
		WorkDir: t.TempDir(), StateDir: stateDir,
	})
	h := netid.NewResolver(netid.NewRoleMapper(stateDir), func(*http.Request) string { return "owner" }, true).Middleware(srv.HandlerForTest())
	for _, rt := range roleReadRoutes {
		req := httptest.NewRequest(http.MethodGet, rt.path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code == http.StatusForbidden {
			t.Errorf("%s: loopback owner got 403", rt.name)
		}
	}
}
