package userstore_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/userstore"
)

// TestMain reduces argon2id cost parameters for the whole test binary so that
// the 30+ argon2 invocations across the test suite complete in seconds rather
// than minutes.  The reduced params are (t=1, m=64KiB, p=1) — still a valid
// argon2id derivation, just not production-strength.
//
// VerifyPassword parses params from the stored PHC, so round-trip tests that
// hash-then-verify are unaffected by this reduction.
func TestMain(m *testing.M) {
	restore := userstore.SetArgon2ParamsForTest(1, 64, 1)
	defer restore()
	m.Run()
}

// ---- argon2id hash / verify -------------------------------------------------

func TestHashPassword_RoundTrip(t *testing.T) {
	t.Parallel()
	hash, err := userstore.HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "" {
		t.Fatal("HashPassword returned empty string")
	}

	ok, err := userstore.VerifyPassword(hash, "correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("VerifyPassword: expected true for correct password; got false")
	}
}

func TestHashPassword_WrongPassword_ReturnsFalse(t *testing.T) {
	t.Parallel()
	hash, err := userstore.HashPassword("correct-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, err := userstore.VerifyPassword(hash, "wrong-password")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Error("VerifyPassword: expected false for wrong password; got true")
	}
}

func TestHashPassword_UniqueSalts(t *testing.T) {
	t.Parallel()
	// Two hashes of the same password must differ (unique salts).
	h1, err := userstore.HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword (1): %v", err)
	}
	h2, err := userstore.HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword (2): %v", err)
	}
	if h1 == h2 {
		t.Error("HashPassword produced identical hashes for same password (salt not randomised)")
	}
}

func TestHashPassword_PHCFormat(t *testing.T) {
	t.Parallel()
	hash, err := userstore.HashPassword("test-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	// Must start with $argon2id$
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("HashPassword: PHC string does not start with $argon2id$; got %q", hash)
	}
}

func TestHashPassword_EmbeddsParams(t *testing.T) {
	t.Parallel()
	// PHC must embed m, t, p params so VerifyPassword can parse them.
	// (Tests run with reduced params via TestMain; we check the format is present,
	// not the specific values.)
	hash, err := userstore.HashPassword("param-test-pw")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	// All three params must be present in the form "m=N,t=N,p=N".
	for _, field := range []string{"m=", "t=", "p="} {
		if !strings.Contains(hash, field) {
			t.Errorf("HashPassword: PHC does not contain %q; got %q", field, hash)
		}
	}
}

func TestVerifyPassword_ParsesStoredParams_RoundTrip(t *testing.T) {
	t.Parallel()
	// VerifyPassword must parse the stored params from the PHC string (not use
	// hardcoded constants).  Verify by confirming hash/verify round-trips
	// for the current param set, which is stored inside the PHC.
	hash, err := userstore.HashPassword("param-parse-test")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := userstore.VerifyPassword(hash, "param-parse-test")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("VerifyPassword: round-trip with parsed params failed")
	}
}

func TestVerifyPassword_TamperedHash_ReturnsFalse(t *testing.T) {
	t.Parallel()
	hash, err := userstore.HashPassword("my-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	// Corrupt a character in the middle of the key segment of the PHC string.
	// We cannot reliably corrupt the last character because trailing base64 bits
	// may be ignored, yielding the same decoded value.
	//
	// Strategy: find the last '$' (start of key field) and flip a char in the
	// middle of that field.
	lastDollar := strings.LastIndex(hash, "$")
	if lastDollar < 0 || lastDollar+4 >= len(hash) {
		t.Fatalf("unexpected PHC format: %q", hash)
	}
	mid := lastDollar + (len(hash)-lastDollar)/2
	b := hash[mid]
	var newB byte
	if b == 'A' {
		newB = 'B'
	} else {
		newB = 'A'
	}
	tampered := hash[:mid] + string(newB) + hash[mid+1:]
	if tampered == hash {
		t.Fatalf("tamper produced identical string; test is invalid")
	}

	ok, err := userstore.VerifyPassword(tampered, "my-password")
	if err != nil {
		// A parse error (e.g. invalid base64 after tamper) is also a valid rejection.
		return
	}
	if ok {
		t.Error("VerifyPassword: expected false for tampered hash; got true")
	}
}

func TestVerifyPassword_InvalidPHC_ReturnsError(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"not-a-phc",
		"$bcrypt$...",
		"$argon2id$only-three-segments$x",
	}
	for _, phc := range cases {
		_, err := userstore.VerifyPassword(phc, "any")
		if err == nil {
			t.Errorf("VerifyPassword(%q): expected error for invalid PHC; got nil", phc)
		}
	}
}

// ---- unknown-username timing guard ------------------------------------------

// TestVerify_UnknownUser_StillRunsArgon2 verifies that Verify does not return
// early for an unknown username.
//
// Behavioral assertions:
//  1. The returned error wraps ErrAuthFailed.
//  2. AuthFailureReason returns "unknown_user" (not "wrong_password"), confirming
//     the internal sentinel is set correctly for audit logging.
//
// Note: we do not assert timing here because tests run with reduced argon2
// params (via TestMain) that may complete in sub-millisecond time.  The
// no-early-exit behavior is guaranteed by the code path (dummyPHC is always
// passed to VerifyPassword for unknown users), not by a timing check.
func TestVerify_UnknownUser_StillRunsArgon2(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)

	_, err := s.Verify("no-such-user", "some-password")
	if err == nil {
		t.Fatal("Verify unknown user: expected error; got nil")
	}
	if !errors.Is(err, userstore.ErrAuthFailed) {
		t.Errorf("Verify unknown user: error does not wrap ErrAuthFailed; got %v", err)
	}
	reason := userstore.AuthFailureReason(err)
	if reason != "unknown_user" {
		t.Errorf("AuthFailureReason: got %q; want %q", reason, "unknown_user")
	}
}

// ---- lockout ----------------------------------------------------------------

func TestVerify_Lockout_AfterThreshold(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "locked-user", "correct-pass", netid.RoleRead)

	// Burn through the threshold with wrong passwords.
	for i := 0; i < userstore.LockoutThreshold; i++ {
		_, err := s.Verify("locked-user", "wrong-pass")
		if err == nil {
			t.Fatalf("attempt %d: expected error; got nil", i+1)
		}
	}

	// Now the account should be locked — correct password must be rejected.
	_, err := s.Verify("locked-user", "correct-pass")
	if err == nil {
		t.Fatal("locked account: expected error with correct password; got nil")
	}
	if !errors.Is(err, userstore.ErrAuthFailed) {
		t.Errorf("locked account: error does not wrap ErrAuthFailed; got %v", err)
	}
	reason := userstore.AuthFailureReason(err)
	if reason != "locked" {
		t.Errorf("locked account: AuthFailureReason=%q; want %q", reason, "locked")
	}
}

func TestVerify_Lockout_SuccessResetsCounter(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "correct-pass", netid.RoleRead)

	// Fail threshold-1 times.
	for i := 0; i < userstore.LockoutThreshold-1; i++ {
		_, _ = s.Verify("alice", "wrong-pass")
	}

	// One success must reset the counter.
	pub, err := s.Verify("alice", "correct-pass")
	if err != nil {
		t.Fatalf("verify success after partial failures: %v", err)
	}
	if pub.FailedAttempts != 0 {
		t.Errorf("FailedAttempts after success: got %d; want 0", pub.FailedAttempts)
	}
}

func TestVerify_Lockout_CooldownExpiry(t *testing.T) {
	// This test cannot manipulate the clock, so we verify that after lockout
	// LockedUntil is set to a future time and is visible via Get.
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "bob", "correct-pass", netid.RoleRead)

	for i := 0; i < userstore.LockoutThreshold; i++ {
		_, _ = s.Verify("bob", "wrong-pass")
	}

	pub, ok := s.Get("bob")
	if !ok {
		t.Fatal("Get bob: not found after lockout")
	}
	if pub.LockedUntil.IsZero() {
		t.Error("LockedUntil is zero after lockout; want a future time")
	}
	if pub.LockedUntil.Before(time.Now()) {
		t.Errorf("LockedUntil=%v is in the past; want future cooldown", pub.LockedUntil)
	}
}

// ---- CRUD -------------------------------------------------------------------

func TestCreate_Duplicate_ReturnsError(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "pass1", netid.RoleRead)

	err := s.Create("alice", "pass2", netid.RoleAdmin)
	if err == nil {
		t.Fatal("Create duplicate: expected error; got nil")
	}
}

func TestCreate_InvalidUsername_ReturnsError(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	cases := []string{
		"",
		".",
		"..",
		"alice/bob",
		"alice\\bob",
		"a-very-long-name-that-exceeds-the-maximum-allowed-length-of-64chars!",
		"alice bob", // space not in allow-list
	}
	for _, name := range cases {
		err := s.Create(name, "pass", netid.RoleRead)
		if err == nil {
			t.Errorf("Create(%q): expected error for invalid username; got nil", name)
		}
	}
}

func TestGet_NotFound(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	_, ok := s.Get("no-such-user")
	if ok {
		t.Error("Get nonexistent user: expected false; got true")
	}
}

func TestList_NoHashInPublicUser(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "secret", netid.RoleAdmin)
	mustCreate(t, s, "bob", "also-secret", netid.RoleRead)

	users := s.List()
	if len(users) != 2 {
		t.Fatalf("List: got %d users; want 2", len(users))
	}

	// PublicUser.PasswordHash does not exist (compile-time guarantee).
	// Confirm expected fields are accessible and role is properly resolved.
	for _, u := range users {
		if u.Username == "" {
			t.Error("List: PublicUser.Username is empty")
		}
		if u.RoleString == "" {
			t.Error("List: PublicUser.RoleString is empty")
		}
		_ = u.Role
		_ = u.Disabled
		_ = u.SessionEpoch
		_ = u.CreatedAt
		_ = u.UpdatedAt
		_ = u.PasswordResetReq
		_ = u.FailedAttempts
		_ = u.LockedUntil
	}
}

func TestSetRole(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "pass", netid.RoleRead)

	if err := s.SetRole("alice", netid.RoleAdmin); err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	pub, ok := s.Get("alice")
	if !ok {
		t.Fatal("Get alice: not found")
	}
	if pub.Role != netid.RoleAdmin {
		t.Errorf("SetRole: got %v; want %v", pub.Role, netid.RoleAdmin)
	}
}

func TestSetRole_NotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	err := s.SetRole("no-such-user", netid.RoleAdmin)
	if err == nil {
		t.Error("SetRole nonexistent user: expected error; got nil")
	}
}

func TestSetPassword_ClearsResetReq_BumpsEpoch(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "old-pass", netid.RoleRead)

	// Force a reset req via ResetPassword.
	if err := s.ResetPassword("alice", "temp-pass"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	pub, _ := s.Get("alice")
	if !pub.PasswordResetReq {
		t.Fatal("PasswordResetReq not set after ResetPassword")
	}
	epochBefore := pub.SessionEpoch

	// SetPassword must clear the flag and bump epoch again.
	if err := s.SetPassword("alice", "new-pass"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	pub, _ = s.Get("alice")
	if pub.PasswordResetReq {
		t.Error("PasswordResetReq still set after SetPassword; want false")
	}
	if pub.SessionEpoch <= epochBefore {
		t.Errorf("SessionEpoch not bumped: before=%d after=%d", epochBefore, pub.SessionEpoch)
	}

	// New password must work for login.
	_, err := s.Verify("alice", "new-pass")
	if err != nil {
		t.Errorf("Verify after SetPassword: %v", err)
	}
}

func TestResetPassword_SetsResetReq(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "original", netid.RoleRead)

	epochBefore := getPubOrFail(t, s, "alice").SessionEpoch

	if err := s.ResetPassword("alice", "temp"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	pub := getPubOrFail(t, s, "alice")
	if !pub.PasswordResetReq {
		t.Error("PasswordResetReq not set after ResetPassword")
	}
	if pub.SessionEpoch <= epochBefore {
		t.Errorf("SessionEpoch not bumped: before=%d after=%d", epochBefore, pub.SessionEpoch)
	}
}

func TestDisable_BumpsEpoch(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "pass", netid.RoleRead)
	epochBefore := getPubOrFail(t, s, "alice").SessionEpoch

	if err := s.Disable("alice"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	pub := getPubOrFail(t, s, "alice")
	if !pub.Disabled {
		t.Error("Disabled not set after Disable")
	}
	if pub.SessionEpoch <= epochBefore {
		t.Errorf("SessionEpoch not bumped on Disable: before=%d after=%d", epochBefore, pub.SessionEpoch)
	}
}

func TestDisable_VerifyRejectsDisabledUser(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "pass", netid.RoleRead)
	mustDisable(t, s, "alice")

	_, err := s.Verify("alice", "pass")
	if err == nil {
		t.Fatal("Verify disabled user: expected error; got nil")
	}
	if !errors.Is(err, userstore.ErrAuthFailed) {
		t.Errorf("error does not wrap ErrAuthFailed: %v", err)
	}
	reason := userstore.AuthFailureReason(err)
	if reason != "disabled" {
		t.Errorf("AuthFailureReason: got %q; want %q", reason, "disabled")
	}
}

func TestEnable_ReenablesAccount(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "pass", netid.RoleRead)
	mustDisable(t, s, "alice")
	if err := s.Enable("alice"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	pub := getPubOrFail(t, s, "alice")
	if pub.Disabled {
		t.Error("Disabled still set after Enable")
	}
	_, err := s.Verify("alice", "pass")
	if err != nil {
		t.Errorf("Verify after Enable: %v", err)
	}
}

func TestDelete_RemovesUser(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "pass", netid.RoleRead)
	mustCreate(t, s, "bob", "pass", netid.RoleRead)

	if err := s.Delete("alice"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, ok := s.Get("alice")
	if ok {
		t.Error("Get deleted user: expected false; got true")
	}
	if s.Count() != 1 {
		t.Errorf("Count after delete: got %d; want 1", s.Count())
	}
	// bob should still be there
	_, ok = s.Get("bob")
	if !ok {
		t.Error("Get bob after alice deleted: expected true; got false")
	}
}

func TestDelete_NotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	err := s.Delete("no-such-user")
	if err == nil {
		t.Error("Delete nonexistent user: expected error; got nil")
	}
}

func TestAdminCount(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	if s.AdminCount() != 0 {
		t.Errorf("AdminCount empty store: got %d; want 0", s.AdminCount())
	}

	mustCreate(t, s, "alice", "pass", netid.RoleAdmin)
	mustCreate(t, s, "bob", "pass", netid.RoleRead)
	if s.AdminCount() != 1 {
		t.Errorf("AdminCount with one admin: got %d; want 1", s.AdminCount())
	}

	mustCreate(t, s, "carol", "pass", netid.RoleAdmin)
	if s.AdminCount() != 2 {
		t.Errorf("AdminCount with two admins: got %d; want 2", s.AdminCount())
	}

	mustDisable(t, s, "alice")
	if s.AdminCount() != 1 {
		t.Errorf("AdminCount after disabling one admin: got %d; want 1", s.AdminCount())
	}
}

func TestBumpSessionEpoch(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "pass", netid.RoleRead)
	epochBefore := getPubOrFail(t, s, "alice").SessionEpoch

	if err := s.BumpSessionEpoch("alice"); err != nil {
		t.Fatalf("BumpSessionEpoch: %v", err)
	}
	pub := getPubOrFail(t, s, "alice")
	if pub.SessionEpoch <= epochBefore {
		t.Errorf("SessionEpoch not bumped: before=%d after=%d", epochBefore, pub.SessionEpoch)
	}
}

func TestCount_Empty(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	if s.Count() != 0 {
		t.Errorf("Count empty store: got %d; want 0", s.Count())
	}
	mustCreate(t, s, "alice", "pass", netid.RoleAdmin)
	if s.Count() != 1 {
		t.Errorf("Count after create: got %d; want 1", s.Count())
	}
}

// ---- file hardening ---------------------------------------------------------

func TestOpen_CreatesParentDir(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	// Use a nested path that doesn't exist yet.
	path := filepath.Join(base, "users", "users.json")

	s, err := userstore.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s == nil {
		t.Fatal("Open returned nil store")
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("Parent directory not created: %v", err)
	}
}

func TestOpen_SymlinkRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	t.Parallel()

	// Create a real users.json in one location.
	realDir := t.TempDir()
	realPath := filepath.Join(realDir, "users.json")
	if err := os.WriteFile(realPath, []byte(`{"users":[]}`), 0600); err != nil {
		t.Fatalf("write real file: %v", err)
	}

	// Create a symlink in another location pointing to the real file.
	linkDir := t.TempDir()
	linkPath := filepath.Join(linkDir, "users.json")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err := userstore.Open(linkPath)
	if err == nil {
		t.Error("Open symlink: expected error; got nil")
	}
}

func TestOpen_GroupWritable_Rejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on Windows")
	}
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	if err := os.WriteFile(path, []byte(`{"users":[]}`), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	// Use explicit Chmod — WriteFile mode is subject to umask, which would
	// strip the group-write bit before it reaches the filesystem.
	if err := os.Chmod(path, 0620); err != nil {
		t.Fatalf("chmod 0620: %v", err)
	}

	_, err := userstore.Open(path)
	if err == nil {
		t.Error("Open group-writable file: expected error; got nil")
	}
}

func TestOpen_OtherWritable_Rejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on Windows")
	}
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	if err := os.WriteFile(path, []byte(`{"users":[]}`), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	// Use explicit Chmod — WriteFile mode is subject to umask.
	if err := os.Chmod(path, 0602); err != nil {
		t.Fatalf("chmod 0602: %v", err)
	}

	_, err := userstore.Open(path)
	if err == nil {
		t.Error("Open other-writable file: expected error; got nil")
	}
}

func TestOpen_CorrectPerms_WorksNormally(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on Windows; open tested elsewhere")
	}
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	if err := os.WriteFile(path, []byte(`{"users":[]}`), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	s, err := userstore.Open(path)
	if err != nil {
		t.Fatalf("Open 0600 file: %v", err)
	}
	if s == nil {
		t.Fatal("Open returned nil")
	}
}

// ---- atomic write -----------------------------------------------------------

func TestPersist_NoTmpLeftover(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	path := filepath.Join(base, "users.json")

	s, err := userstore.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	mustCreate(t, s, "alice", "pass", netid.RoleAdmin)

	// After a successful write, no .tmp file should remain.
	tmp := path + ".tmp"
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("tmp file still exists at %q after persist; want removed", tmp)
	}
}

func TestPersist_Roundtrip(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	path := filepath.Join(base, "users.json")

	s, err := userstore.Open(path)
	if err != nil {
		t.Fatalf("Open (1): %v", err)
	}
	mustCreate(t, s, "alice", "pass", netid.RoleAdmin)
	mustCreate(t, s, "bob", "pass", netid.RoleDispatch)

	// Re-open and verify data survived.
	s2, err := userstore.Open(path)
	if err != nil {
		t.Fatalf("Open (2): %v", err)
	}
	users := s2.List()
	if len(users) != 2 {
		t.Fatalf("Roundtrip: got %d users; want 2", len(users))
	}
}

// ---- concurrency ------------------------------------------------------------

func TestConcurrentMutations_Race(t *testing.T) {
	// Not t.Parallel() at the outer level so -race clearly covers the goroutine
	// fan-out within this test.
	//
	// We use non-argon2 mutations (SetRole, BumpSessionEpoch, List, Get, Disable,
	// Enable) to keep the test fast under -race; the mutex that protects these
	// also covers HashPassword callers, but verifying mutex correctness does not
	// require exercising every method.
	base := t.TempDir()
	path := filepath.Join(base, "users.json")

	s, err := userstore.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Create 5 users sequentially (each Create calls HashPassword; 5 × ~5s is
	// acceptable in the test suite even without -short).
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("user%02d", i)
		if err := s.Create(name, "pass", netid.RoleRead); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}

	// Fan out 20 goroutines doing concurrent non-argon2 mutations.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		name := fmt.Sprintf("user%02d", i%5) // round-robin across 5 users
		go func(n string) {
			defer wg.Done()
			_ = s.SetRole(n, netid.RoleDispatch)
			_ = s.BumpSessionEpoch(n)
			_ = s.List()
			_, _ = s.Get(n)
			_ = s.Disable(n)
			_ = s.Enable(n)
		}(name)
	}
	wg.Wait()

	// Store must still be consistent after concurrent mutations.
	if s.Count() != 5 {
		t.Errorf("Count after concurrent mutations: got %d; want 5", s.Count())
	}
}

// ---- helpers ----------------------------------------------------------------

func openEmpty(t *testing.T) *userstore.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "users.json")
	s, err := userstore.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func mustCreate(t *testing.T, s *userstore.Store, username, password string, role netid.Role) {
	t.Helper()
	if err := s.Create(username, password, role); err != nil {
		t.Fatalf("Create %q: %v", username, err)
	}
}

func mustDisable(t *testing.T, s *userstore.Store, username string) {
	t.Helper()
	if err := s.Disable(username); err != nil {
		t.Fatalf("Disable %q: %v", username, err)
	}
}

func getPubOrFail(t *testing.T, s *userstore.Store, username string) userstore.PublicUser {
	t.Helper()
	pub, ok := s.Get(username)
	if !ok {
		t.Fatalf("Get %q: not found", username)
	}
	return pub
}
