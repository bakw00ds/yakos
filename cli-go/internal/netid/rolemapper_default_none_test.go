package netid_test

// K-98 (ADR-0005 Amendment 2026-09-29): the RoleMapper fails closed. A CN with
// no mapping resolves to RoleNone; "*" is the explicit opt-in for the old
// "any authenticated cert gets a role" behaviour.

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/netid"
)

func TestRoleMapper_Wildcard(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		mapping map[string]string
		cn      string
		want    netid.Role
	}{
		{"unmapped, no wildcard -> none", map[string]string{"alice": "admin"}, "stranger", netid.RoleNone},
		{"wildcard read restores read", map[string]string{"*": "read"}, "stranger", netid.RoleRead},
		{"wildcard dispatch", map[string]string{"*": "dispatch"}, "stranger", netid.RoleDispatch},
		{"explicit beats wildcard (higher)", map[string]string{"*": "read", "alice": "admin"}, "alice", netid.RoleAdmin},
		{"explicit beats wildcard (lower)", map[string]string{"*": "admin", "bob": "read"}, "bob", netid.RoleRead},
		{"unmapped with wildcard+others", map[string]string{"*": "read", "alice": "admin"}, "carol", netid.RoleRead},
		{"unknown role string -> none", map[string]string{"dan": "superuser"}, "dan", netid.RoleNone},
		{"role string none -> none", map[string]string{"dan": "none"}, "dan", netid.RoleNone},
		{"empty role string -> none", map[string]string{"dan": ""}, "dan", netid.RoleNone},
		{"bad explicit does not fall back to wildcard", map[string]string{"*": "admin", "dan": "superuser"}, "dan", netid.RoleNone},
		{"bad wildcard -> none", map[string]string{"*": "everything"}, "anyone", netid.RoleNone},
		{"empty map -> none", map[string]string{}, "alice", netid.RoleNone},
		{"wildcard is not a glob", map[string]string{"al*": "admin"}, "alice", netid.RoleNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeRolesFile(t, dir, tc.mapping)
			if got := netid.NewRoleMapper(dir).Lookup(tc.cn); got != tc.want {
				t.Errorf("Lookup(%q)=%v; want %v", tc.cn, got, tc.want)
			}
		})
	}
}

func TestRoleMapper_UnknownRole_WarnsNamingCN_Once(t *testing.T) {
	// Not parallel: swaps the process-wide slog default.
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(old) })

	dir := t.TempDir()
	writeRolesFile(t, dir, map[string]string{"mallory": "superuser"})
	m := netid.NewRoleMapper(dir)
	for i := 0; i < 3; i++ {
		if got := m.Lookup("mallory"); got != netid.RoleNone {
			t.Fatalf("Lookup=%v; want none", got)
		}
	}
	out := buf.String()
	if !strings.Contains(out, "WARN") || !strings.Contains(out, "mallory") || !strings.Contains(out, "superuser") {
		t.Errorf("want WARN naming CN and value; got %q", out)
	}
	if n := strings.Count(out, "unrecognised role"); n != 1 {
		t.Errorf("WARN emitted %d times; want exactly once per (CN,value)", n)
	}
}

func TestParseRoleStrict(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]netid.Role{"read": netid.RoleRead, "dispatch": netid.RoleDispatch, "flows-run": netid.RoleFlowsRun, "admin": netid.RoleAdmin} {
		if got, ok := netid.ParseRoleStrict(s); !ok || got != want {
			t.Errorf("ParseRoleStrict(%q)=%v,%v", s, got, ok)
		}
	}
	for _, s := range []string{"", "none", "ADMIN", "root", "*"} {
		if got, ok := netid.ParseRoleStrict(s); ok || got != netid.RoleNone {
			t.Errorf("ParseRoleStrict(%q)=%v,%v; want none,false", s, got, ok)
		}
	}
}

func TestRoleMapper_StartupSummary(t *testing.T) {
	t.Parallel()
	// missing file
	msg, warn := netid.NewRoleMapper(t.TempDir()).StartupSummary()
	if !warn || !strings.Contains(msg, "no client certificate is authorized") || !strings.Contains(msg, `"*": "read"`) {
		t.Errorf("missing file: warn=%v msg=%q", warn, msg)
	}
	// empty stateDir
	if _, warn := netid.NewRoleMapper("").StartupSummary(); !warn {
		t.Error("empty stateDir: want warn")
	}
	// empty map
	dir := t.TempDir()
	writeRolesFile(t, dir, map[string]string{})
	if _, warn := netid.NewRoleMapper(dir).StartupSummary(); !warn {
		t.Error("empty map: want warn")
	}
	// populated
	dir2 := t.TempDir()
	writeRolesFile(t, dir2, map[string]string{"alice": "admin"})
	if msg, warn := netid.NewRoleMapper(dir2).StartupSummary(); warn || !strings.Contains(msg, "unmapped certs get no access") {
		t.Errorf("populated: warn=%v msg=%q", warn, msg)
	}
	// wildcard
	dir3 := t.TempDir()
	writeRolesFile(t, dir3, map[string]string{"*": "read"})
	if msg, warn := netid.NewRoleMapper(dir3).StartupSummary(); warn || !strings.Contains(msg, "wildcard") {
		t.Errorf("wildcard: warn=%v msg=%q", warn, msg)
	}
}

// Loopback owner is unaffected: the resolver's loopback branch never consults
// the mapper.
func TestResolver_Loopback_UnaffectedByEmptyRoleMap(t *testing.T) {
	t.Parallel()
	res := netid.NewResolver(netid.NewRoleMapper(t.TempDir()), func(*http.Request) string { return "owner" }, true)
	id := res.Resolve(httptest.NewRequest(http.MethodGet, "/", nil))
	if id.Role != netid.RoleAdmin || id.OperatorID != "owner" {
		t.Errorf("loopback: role=%v op=%q; want admin/owner", id.Role, id.OperatorID)
	}
}

func TestResolver_Cert_WildcardAndExplicit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRolesFile(t, dir, map[string]string{"*": "read", "ops": "admin"})
	res := netid.NewResolver(netid.NewRoleMapper(dir), nil, false)
	for cn, want := range map[string]netid.Role{"ops": netid.RoleAdmin, "someone": netid.RoleRead} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.TLS = performMTLSHandshake(t, cn)
		if got := res.Resolve(r).Role; got != want {
			t.Errorf("cert %q: role=%v; want %v", cn, got, want)
		}
	}
}
