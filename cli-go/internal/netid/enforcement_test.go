package netid_test

// enforcement_test.go — Phase 6b tests for the netid package.
//
// Coverage:
//  1. LOW-1: roles.json symlink rejection (Lookup returns RoleRead, not mapped role).
//  2. LOW-1: roles.json group-writable rejection.
//  3. LOW-1: roles.json other-writable rejection.
//  4. LOW-1: roles.json with correct 0600 permissions works normally.
//  5. Identity.Resolved is true when set by the Resolver middleware.
//  6. Identity.Resolved is false for the zero-value Identity (no middleware).

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bakw00ds/yakos/internal/netid"
)

// ---- LOW-1: roles.json file-trust hardening --------------------------------

// TestRoleMapper_Symlink_TreatedAsMissing verifies that a roles.json which is
// a symlink (pointing to a regular file with valid content) is treated as
// missing and Lookup returns RoleRead.  This prevents a symlink-redirect attack
// where an attacker replaces roles.json with a symlink to a path they control.
func TestRoleMapper_Symlink_TreatedAsMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	t.Parallel()

	// Set up two state dirs: one for the real roles.json, one for the symlink.
	realDir := t.TempDir()
	linkDir := t.TempDir()

	// Write a real roles.json with a valid admin mapping.
	writeRolesFile(t, realDir, map[string]string{"alice": "admin"})
	realPath := filepath.Join(realDir, "mtls", "roles.json")

	// Create the link directory structure.
	linkMTLS := filepath.Join(linkDir, "mtls")
	if err := os.MkdirAll(linkMTLS, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	linkPath := filepath.Join(linkMTLS, "roles.json")
	// Create a symlink at linkPath → realPath.
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	m := netid.NewRoleMapper(linkDir)
	// Must return RoleRead (symlink rejected), NOT the mapped admin role.
	got := m.Lookup("alice")
	if got != netid.RoleRead {
		t.Errorf("Lookup via symlink: got %v; want RoleRead (symlink must be rejected)", got)
	}
}

// TestRoleMapper_GroupWritable_TreatedAsMissing verifies that a roles.json with
// group-write permission (mode & 0o020 != 0) is treated as missing.
// This mirrors the 0600/0700 posture enforced by the mtls package.
func TestRoleMapper_GroupWritable_TreatedAsMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not enforced on Windows")
	}
	t.Parallel()

	stateDir := t.TempDir()
	writeRolesFile(t, stateDir, map[string]string{"alice": "admin"})
	rolesPath := filepath.Join(stateDir, "mtls", "roles.json")

	// Make the file group-writable.
	if err := os.Chmod(rolesPath, 0620); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	m := netid.NewRoleMapper(stateDir)
	got := m.Lookup("alice")
	if got != netid.RoleRead {
		t.Errorf("group-writable roles.json: got %v; want RoleRead (group-writable must be rejected)", got)
	}
}

// TestRoleMapper_OtherWritable_TreatedAsMissing verifies that a roles.json with
// other-write permission (mode & 0o002 != 0) is treated as missing.
func TestRoleMapper_OtherWritable_TreatedAsMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not enforced on Windows")
	}
	t.Parallel()

	stateDir := t.TempDir()
	writeRolesFile(t, stateDir, map[string]string{"bob": "dispatch"})
	rolesPath := filepath.Join(stateDir, "mtls", "roles.json")

	// Make the file other-writable.
	if err := os.Chmod(rolesPath, 0602); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	m := netid.NewRoleMapper(stateDir)
	got := m.Lookup("bob")
	if got != netid.RoleRead {
		t.Errorf("other-writable roles.json: got %v; want RoleRead (other-writable must be rejected)", got)
	}
}

// TestRoleMapper_CorrectPerms_WorksNormally verifies that a roles.json with
// correct restrictive permissions (0600) is read and Lookup returns the mapped role.
func TestRoleMapper_CorrectPerms_WorksNormally(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not enforced on Windows; regular reads tested elsewhere")
	}
	t.Parallel()

	stateDir := t.TempDir()
	// writeRolesFile already creates the file with 0600 permissions.
	writeRolesFile(t, stateDir, map[string]string{
		"alice": "admin",
		"bob":   "dispatch",
	})

	m := netid.NewRoleMapper(stateDir)
	if got := m.Lookup("alice"); got != netid.RoleAdmin {
		t.Errorf("0600 roles.json: alice: got %v; want RoleAdmin", got)
	}
	if got := m.Lookup("bob"); got != netid.RoleDispatch {
		t.Errorf("0600 roles.json: bob: got %v; want RoleDispatch", got)
	}
}

// ---- Identity.Resolved field ------------------------------------------------

// TestIdentityFrom_ZeroValue_ResolvedFalse verifies that the zero-value Identity
// returned when no middleware has run has Resolved==false.
// Phase 3g: zero-value Role is now RoleNone (the unauthenticated sentinel).
// requireRole is safe because it gates on Resolved=false — zero-value identities
// are never blocked by role enforcement.
func TestIdentityFrom_ZeroValue_ResolvedFalse(t *testing.T) {
	t.Parallel()
	// Use a plain request with no identity in context (no middleware ran).
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	id := netid.IdentityFrom(r.Context())
	if id.Resolved {
		t.Error("zero-value Identity.Resolved=true; want false (no middleware ran)")
	}
	// Zero-value Role is RoleNone since Phase 3g (iota=0 is now RoleNone, not RoleRead).
	if id.Role != netid.RoleNone {
		t.Errorf("zero-value Identity.Role=%v; want RoleNone (Phase 3g sentinel, safe because Resolved=false)", id.Role)
	}
}

// TestResolver_Middleware_SetsResolved verifies that when the Resolver middleware
// runs, the resulting Identity has Resolved==true regardless of whether the
// request is authenticated or not.
func TestResolver_Middleware_SetsResolved(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	m := netid.NewRoleMapper(stateDir)
	res := netid.NewResolver(m, func(r *http.Request) string {
		return "local-op"
	}, true /* loopbackTrusted */)

	var capturedID netid.Identity
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = netid.IdentityFrom(r.Context())
	})

	handler := res.Middleware(next)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if !capturedID.Resolved {
		t.Error("Identity.Resolved=false after middleware ran; want true")
	}
	// Loopback (no cert): should be RoleAdmin, Authenticated=false.
	if capturedID.Role != netid.RoleAdmin {
		t.Errorf("loopback Identity.Role=%v; want RoleAdmin", capturedID.Role)
	}
	if capturedID.Authenticated {
		t.Error("loopback Identity.Authenticated=true; want false")
	}
}

// TestResolver_NonLoopback_NoCert_SetsResolved verifies that even a fail-closed
// (RoleNone) resolution from a non-loopback resolver sets Resolved==true.
// Phase 3g: fail-closed identity now carries RoleNone, not RoleRead.
func TestResolver_NonLoopback_NoCert_SetsResolved(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	m := netid.NewRoleMapper(stateDir)
	res := netid.NewResolver(m, nil, false /* loopbackTrusted=false */)

	var capturedID netid.Identity
	handler := res.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = netid.IdentityFrom(r.Context())
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if !capturedID.Resolved {
		t.Error("non-loopback fail-closed Identity.Resolved=false; want true")
	}
	// Phase 3g: unauthenticated networked identities now carry RoleNone.
	if capturedID.Role != netid.RoleNone {
		t.Errorf("non-loopback fail-closed Role=%v; want RoleNone (Phase 3g sentinel)", capturedID.Role)
	}
}

// ---- helpers ----------------------------------------------------------------
// writeRolesFile is defined in netid_test.go (same package netid_test).
// It creates <stateDir>/mtls/roles.json at 0600 permissions.
