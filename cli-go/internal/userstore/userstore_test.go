package userstore_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/userstore"
)

// TestMain reduces argon2id cost parameters for the whole test binary so that
// the many argon2 invocations across the test suite complete in seconds rather
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
	hash, err := userstore.HashPassword("correct-password-long-enough")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, err := userstore.VerifyPassword(hash, "wrong-password-long-enough")
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
	h1, err := userstore.HashPassword("same-password-long")
	if err != nil {
		t.Fatalf("HashPassword (1): %v", err)
	}
	h2, err := userstore.HashPassword("same-password-long")
	if err != nil {
		t.Fatalf("HashPassword (2): %v", err)
	}
	if h1 == h2 {
		t.Error("HashPassword produced identical hashes for same password (salt not randomised)")
	}
}

func TestHashPassword_PHCFormat(t *testing.T) {
	t.Parallel()
	hash, err := userstore.HashPassword("test-password-xyz")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("HashPassword: PHC string does not start with $argon2id$; got %q", hash)
	}
}

func TestHashPassword_EmbeddsParams(t *testing.T) {
	t.Parallel()
	// PHC must embed m, t, p params so VerifyPassword can parse them.
	// (Tests run with reduced params via TestMain; we check the format is present,
	// not the specific values.)
	hash, err := userstore.HashPassword("param-test-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	for _, field := range []string{"m=", "t=", "p="} {
		if !strings.Contains(hash, field) {
			t.Errorf("HashPassword: PHC does not contain %q; got %q", field, hash)
		}
	}
}

func TestVerifyPassword_ParsesStoredParams_RoundTrip(t *testing.T) {
	t.Parallel()
	// VerifyPassword must parse the stored params from the PHC string (not use
	// hardcoded constants).
	hash, err := userstore.HashPassword("param-parse-test-pw")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := userstore.VerifyPassword(hash, "param-parse-test-pw")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("VerifyPassword: round-trip with parsed params failed")
	}
}

func TestVerifyPassword_TamperedHash_ReturnsFalse(t *testing.T) {
	t.Parallel()
	hash, err := userstore.HashPassword("my-long-password-xyz")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	// Corrupt a character in the middle of the key segment of the PHC string.
	// Avoid the last character — trailing base64 bits may be absorbed silently.
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

	ok, err := userstore.VerifyPassword(tampered, "my-long-password-xyz")
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

// TestVerifyPassword_CorruptCostParams verifies that a PHC string with negative
// or out-of-range cost parameters is rejected before argon2 is called, preventing
// an OOM-kill from a corrupt m=-1 (which would cast to ~4 TiB).
func TestVerifyPassword_CorruptCostParams(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		phc  string
	}{
		{
			name: "negative_m",
			// m=-1 would cast to 4294967295 KiB ≈ 4 TiB
			phc: "$argon2id$v=19$m=-1,t=3,p=4$c29tZXNhbHRYWA$aGFzaGtleVhYWFhYWFhYWFhYWFhYWFhYWA",
		},
		{
			name: "zero_t",
			phc:  "$argon2id$v=19$m=65536,t=0,p=4$c29tZXNhbHRYWA$aGFzaGtleVhYWFhYWFhYWFhYWFhYWFhYWA",
		},
		{
			name: "zero_p",
			phc:  "$argon2id$v=19$m=65536,t=3,p=0$c29tZXNhbHRYWA$aGFzaGtleVhYWFhYWFhYWFhYWFhYWFhYWA",
		},
		{
			name: "memory_exceeds_4gib",
			// 1<<22 + 1 KiB exceeds the 4 GiB ceiling
			phc: fmt.Sprintf("$argon2id$v=19$m=%d,t=3,p=4$c29tZXNhbHRYWA$aGFzaGtleVhYWFhYWFhYWFhYWFhYWFhYWA", (1<<22)+1),
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := userstore.VerifyPassword(tc.phc, "any-password")
			if err == nil {
				t.Errorf("VerifyPassword with %s: expected error; got nil", tc.name)
			}
		})
	}
}

// TestVerify_CorruptPHC_WrapsErrAuthFailed verifies that when a stored
// password hash is corrupt (unparseable), Verify wraps ErrAuthFailed so that
// callers get a consistent error type, and AuthFailureReason returns "" (this
// is an infrastructure failure, not a user-facing auth reason).
func TestVerify_CorruptPHC_WrapsErrAuthFailed(t *testing.T) {
	t.Parallel()
	// Open a store and inject a corrupt hash directly into users.json, then
	// re-open so the store loads it, then call Verify.
	base := t.TempDir()
	path := filepath.Join(base, "users.json")

	// Write a users.json with a corrupt passwordHash for "alice".
	corruptJSON := `{
  "users": [
    {
      "username": "alice",
      "passwordHash": "not-a-valid-phc-string",
      "role": "read",
      "disabled": false,
      "passwordResetReq": false,
      "createdAt": "2026-01-01T00:00:00Z",
      "updatedAt": "2026-01-01T00:00:00Z",
      "failedAttempts": 0,
      "sessionEpoch": 0
    }
  ]
}`
	if err := os.WriteFile(path, []byte(corruptJSON), 0600); err != nil {
		t.Fatalf("write corrupt users.json: %v", err)
	}

	s, err := userstore.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	_, verifyErr := s.Verify("alice", "any-password-here")
	if verifyErr == nil {
		t.Fatal("Verify with corrupt PHC: expected error; got nil")
	}
	if !errors.Is(verifyErr, userstore.ErrAuthFailed) {
		t.Errorf("Verify with corrupt PHC: error does not wrap ErrAuthFailed; got %v", verifyErr)
	}
	// Infrastructure failure must NOT be attributed to a user action.
	reason := userstore.AuthFailureReason(verifyErr)
	if reason != "" {
		t.Errorf("AuthFailureReason for corrupt PHC: got %q; want \"\" (infra failure, not user action)", reason)
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
// params (via TestMain) that may complete in sub-millisecond time.
func TestVerify_UnknownUser_StillRunsArgon2(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)

	_, err := s.Verify("no-such-user", "some-long-password")
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
	mustCreate(t, s, "locked-user", "correct-long-pass!", netid.RoleRead)

	// Burn through the threshold with wrong passwords.
	for i := 0; i < userstore.LockoutThreshold; i++ {
		_, err := s.Verify("locked-user", "wrong-long-password")
		if err == nil {
			t.Fatalf("attempt %d: expected error; got nil", i+1)
		}
	}

	// Now the account should be locked — correct password must be rejected.
	_, err := s.Verify("locked-user", "correct-long-pass!")
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
	mustCreate(t, s, "alice", "correct-long-pass!", netid.RoleRead)

	// Fail threshold-1 times.
	for i := 0; i < userstore.LockoutThreshold-1; i++ {
		_, _ = s.Verify("alice", "wrong-long-password!")
	}

	// One success must reset the counter.
	pub, err := s.Verify("alice", "correct-long-pass!")
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
	mustCreate(t, s, "bob", "correct-long-pass!", netid.RoleRead)

	for i := 0; i < userstore.LockoutThreshold; i++ {
		_, _ = s.Verify("bob", "wrong-long-password!")
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
	mustCreate(t, s, "alice", "pass1-long-enough", netid.RoleRead)

	err := s.Create("alice", "pass2-long-enough", netid.RoleAdmin)
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
		err := s.Create(name, "password-long-enough", netid.RoleRead)
		if err == nil {
			t.Errorf("Create(%q): expected error for invalid username; got nil", name)
		}
	}
}

// ---- password length floor --------------------------------------------------

func TestCreate_ShortPassword_ReturnsError(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	// Passwords shorter than MinPasswordLen must be rejected.
	short := strings.Repeat("x", userstore.MinPasswordLen-1)
	err := s.Create("alice", short, netid.RoleRead)
	if err == nil {
		t.Errorf("Create with %d-char password: expected error (min=%d); got nil",
			len(short), userstore.MinPasswordLen)
	}
}

func TestCreate_MinLengthPassword_Accepted(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	exactly := strings.Repeat("x", userstore.MinPasswordLen)
	if err := s.Create("alice", exactly, netid.RoleRead); err != nil {
		t.Errorf("Create with exactly MinPasswordLen chars: unexpected error: %v", err)
	}
}

func TestSetPassword_ShortPassword_ReturnsError(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "long-enough-pass!", netid.RoleRead)

	short := strings.Repeat("y", userstore.MinPasswordLen-1)
	err := s.SetPassword("alice", short)
	if err == nil {
		t.Errorf("SetPassword with %d-char password: expected error (min=%d); got nil",
			len(short), userstore.MinPasswordLen)
	}
}

func TestResetPassword_ShortPassword_ReturnsError(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "long-enough-pass!", netid.RoleRead)

	short := strings.Repeat("z", userstore.MinPasswordLen-1)
	err := s.ResetPassword("alice", short)
	if err == nil {
		t.Errorf("ResetPassword with %d-char password: expected error (min=%d); got nil",
			len(short), userstore.MinPasswordLen)
	}
}

// ---- continued CRUD ---------------------------------------------------------

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
	mustCreate(t, s, "alice", "alice-secret-pass!", netid.RoleAdmin)
	mustCreate(t, s, "bob", "bob-also-secret-pw", netid.RoleRead)

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

func TestSetRole_BumpsEpoch(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "alice-pass-long!!", netid.RoleRead)
	epochBefore := getPubOrFail(t, s, "alice").SessionEpoch

	if err := s.SetRole("alice", netid.RoleAdmin); err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	pub := getPubOrFail(t, s, "alice")
	if pub.Role != netid.RoleAdmin {
		t.Errorf("SetRole: role got %v; want %v", pub.Role, netid.RoleAdmin)
	}
	if pub.SessionEpoch <= epochBefore {
		t.Errorf("SetRole: SessionEpoch not bumped: before=%d after=%d (role change must invalidate sessions)", epochBefore, pub.SessionEpoch)
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
	mustCreate(t, s, "alice", "old-pass-is-fine!", netid.RoleRead)

	// Force a reset req via ResetPassword.
	if err := s.ResetPassword("alice", "temp-pass-is-ok!"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	pub, _ := s.Get("alice")
	if !pub.PasswordResetReq {
		t.Fatal("PasswordResetReq not set after ResetPassword")
	}
	epochBefore := pub.SessionEpoch

	// SetPassword must clear the flag and bump epoch again.
	if err := s.SetPassword("alice", "new-pass-is-fine!"); err != nil {
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
	_, err := s.Verify("alice", "new-pass-is-fine!")
	if err != nil {
		t.Errorf("Verify after SetPassword: %v", err)
	}
}

func TestResetPassword_SetsResetReq(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "original-password!", netid.RoleRead)

	epochBefore := getPubOrFail(t, s, "alice").SessionEpoch

	if err := s.ResetPassword("alice", "temp-password-ok!"); err != nil {
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
	mustCreate(t, s, "alice", "alice-pass-long!!", netid.RoleRead)
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
	mustCreate(t, s, "alice", "alice-pass-long!!", netid.RoleRead)
	mustDisable(t, s, "alice")

	_, err := s.Verify("alice", "alice-pass-long!!")
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
	mustCreate(t, s, "alice", "alice-pass-long!!", netid.RoleRead)
	mustDisable(t, s, "alice")
	if err := s.Enable("alice"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	pub := getPubOrFail(t, s, "alice")
	if pub.Disabled {
		t.Error("Disabled still set after Enable")
	}
	_, err := s.Verify("alice", "alice-pass-long!!")
	if err != nil {
		t.Errorf("Verify after Enable: %v", err)
	}
}

func TestDelete_RemovesUser(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "alice", "alice-pass-long!!", netid.RoleRead)
	mustCreate(t, s, "bob", "bob-pass-long-ok!", netid.RoleRead)

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

	mustCreate(t, s, "alice", "alice-pass-long!!", netid.RoleAdmin)
	mustCreate(t, s, "bob", "bob-pass-long-ok!", netid.RoleRead)
	if s.AdminCount() != 1 {
		t.Errorf("AdminCount with one admin: got %d; want 1", s.AdminCount())
	}

	mustCreate(t, s, "carol", "carol-pass-long!!", netid.RoleAdmin)
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
	mustCreate(t, s, "alice", "alice-pass-long!!", netid.RoleRead)
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
	mustCreate(t, s, "alice", "alice-pass-long!!", netid.RoleAdmin)
	if s.Count() != 1 {
		t.Errorf("Count after create: got %d; want 1", s.Count())
	}
}

// ---- SetLogger --------------------------------------------------------------

func TestSetLogger_CalledOnPersistFailure(t *testing.T) {
	t.Parallel()
	// This test exercises the logger path by setting up a store on a read-only
	// directory so persist fails, then checking the logger is called with a
	// non-empty message during Verify (lockout state persistence).
	if runtime.GOOS == "windows" {
		t.Skip("read-only directory chmod not reliable on Windows")
	}

	base := t.TempDir()
	path := filepath.Join(base, "users.json")
	s, err := userstore.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	mustCreate(t, s, "alice", "alice-pass-long!!", netid.RoleRead)

	var logged []string
	s.SetLogger(func(msg string) {
		logged = append(logged, msg)
	})

	// Make the parent directory read-only so persist() will fail.
	if err := os.Chmod(base, 0500); err != nil {
		t.Fatalf("chmod base dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(base, 0700) })

	// A wrong-password attempt should trigger a persist failure for lockout state.
	_, _ = s.Verify("alice", "wrong-password-here!")

	if len(logged) == 0 {
		t.Error("SetLogger: expected logger to be called on persist failure; got no messages")
	}
}

// ---- file hardening ---------------------------------------------------------

func TestOpen_CreatesParentDir(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
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

	realDir := t.TempDir()
	realPath := filepath.Join(realDir, "users.json")
	if err := os.WriteFile(realPath, []byte(`{"users":[]}`), 0600); err != nil {
		t.Fatalf("write real file: %v", err)
	}

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
	mustCreate(t, s, "alice", "alice-pass-long!!", netid.RoleAdmin)

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
	mustCreate(t, s, "alice", "alice-pass-long!!", netid.RoleAdmin)
	mustCreate(t, s, "bob", "bob-pass-long-ok!", netid.RoleDispatch)

	s2, err := userstore.Open(path)
	if err != nil {
		t.Fatalf("Open (2): %v", err)
	}
	users := s2.List()
	if len(users) != 2 {
		t.Fatalf("Roundtrip: got %d users; want 2", len(users))
	}
}

// ---- CreateFirstAdmin -------------------------------------------------------

func TestCreateFirstAdmin_ZeroUsers_CreatesAdmin(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	if err := s.CreateFirstAdmin("admin", "securepassword1!"); err != nil {
		t.Fatalf("CreateFirstAdmin: %v", err)
	}
	if s.Count() != 1 {
		t.Errorf("Count after CreateFirstAdmin: got %d; want 1", s.Count())
	}
	pub, ok := s.Get("admin")
	if !ok {
		t.Fatal("Get('admin'): not found after CreateFirstAdmin")
	}
	if pub.Role != netid.RoleAdmin {
		t.Errorf("first user role = %q; want admin", pub.Role)
	}
}

func TestCreateFirstAdmin_UsersExist_ReturnsErrNotFirstUser(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	mustCreate(t, s, "existing", "existing-pass-1!", netid.RoleAdmin)

	err := s.CreateFirstAdmin("second", "securepassword1!")
	if err == nil {
		t.Fatal("CreateFirstAdmin with existing user: expected error; got nil")
	}
	if !errors.Is(err, userstore.ErrNotFirstUser) {
		t.Errorf("CreateFirstAdmin error = %v; want ErrNotFirstUser", err)
	}
	// Original user must still be the only one.
	if s.Count() != 1 {
		t.Errorf("Count after rejected CreateFirstAdmin: got %d; want 1", s.Count())
	}
}

func TestCreateFirstAdmin_InvalidUsername_ReturnsError(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	err := s.CreateFirstAdmin("invalid/user!", "securepassword1!")
	if err == nil {
		t.Fatal("CreateFirstAdmin invalid username: expected error; got nil")
	}
	if s.Count() != 0 {
		t.Error("Count should remain 0 after validation failure")
	}
}

func TestCreateFirstAdmin_ShortPassword_ReturnsError(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	err := s.CreateFirstAdmin("admin", "short")
	if err == nil {
		t.Fatal("CreateFirstAdmin short password: expected error; got nil")
	}
	if s.Count() != 0 {
		t.Error("Count should remain 0 after password validation failure")
	}
}

func TestCreateFirstAdmin_ConcurrentRace_ExactlyOneSucceeds(t *testing.T) {
	// Not Parallel: goroutine fan-out test; isolated for -race clarity.
	const goroutines = 32
	s := openEmpty(t)

	var (
		successes int32
		start     = make(chan struct{})
	)
	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			<-start
			username := fmt.Sprintf("admin%d", idx)
			err := s.CreateFirstAdmin(username, "securepassword1!")
			if err == nil {
				atomic.AddInt32(&successes, 1)
			}
			done <- struct{}{}
		}(i)
	}
	close(start)
	for i := 0; i < goroutines; i++ {
		<-done
	}
	if got := atomic.LoadInt32(&successes); got != 1 {
		t.Errorf("concurrent CreateFirstAdmin: want exactly 1 success, got %d", got)
	}
	if s.Count() != 1 {
		t.Errorf("Count after concurrent CreateFirstAdmin: got %d; want 1", s.Count())
	}
}

// ---- concurrency ------------------------------------------------------------

func TestConcurrentMutations_Race(t *testing.T) {
	// Not t.Parallel() at the outer level so -race clearly covers the goroutine
	// fan-out within this test.
	base := t.TempDir()
	path := filepath.Join(base, "users.json")

	s, err := userstore.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("user%02d", i)
		if err := s.Create(name, "password-long-enough!", netid.RoleRead); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		name := fmt.Sprintf("user%02d", i%5)
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

// mustCreate uses a password that is guaranteed to meet MinPasswordLen.
func mustCreate(t *testing.T, s *userstore.Store, username, password string, role netid.Role) {
	t.Helper()
	if len(password) < userstore.MinPasswordLen {
		t.Fatalf("mustCreate: password %q is shorter than MinPasswordLen=%d; fix the test", password, userstore.MinPasswordLen)
	}
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

// ---- Phase 3g: RoleNone must be rejected as a user-assignable role ----------

// TestCreate_RoleNone_Rejected verifies that Create returns an error when
// RoleNone is supplied as the role argument.  RoleNone is an internal sentinel
// for unauthenticated networked identities and must never be persisted as a
// user's role (ADR-0005 Phase 3g).
func TestCreate_RoleNone_Rejected(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	err := s.Create("alice", "correct-horse-battery-staple", netid.RoleNone)
	if err == nil {
		t.Fatal("Create with RoleNone: want error; got nil (RoleNone must be rejected)")
	}
	// The user must not have been created.
	_, ok := s.Get("alice")
	if ok {
		t.Error("Create with RoleNone: user was created despite error; want no user")
	}
}

// TestCreate_AllAssignableRoles_Accepted verifies that Create succeeds for all
// four real user-assignable roles.  Phase 3g must not accidentally break
// legitimate user creation.
func TestCreate_AllAssignableRoles_Accepted(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		role netid.Role
	}{
		{"read", netid.RoleRead},
		{"dispatch", netid.RoleDispatch},
		{"flows-run", netid.RoleFlowsRun},
		{"admin", netid.RoleAdmin},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := openEmpty(t)
			err := s.Create("user-"+tc.name, "correct-horse-battery-staple", tc.role)
			if err != nil {
				t.Errorf("Create with role %q: unexpected error: %v", tc.name, err)
			}
		})
	}
}

// TestSetRole_RoleNone_Rejected verifies that SetRole returns an error when
// RoleNone is supplied.  Even a privileged admin must not be able to degrade
// a user to the internal unauthenticated sentinel.
func TestSetRole_RoleNone_Rejected(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	// Create a legitimate user first.
	if err := s.Create("bob", "correct-horse-battery-staple", netid.RoleRead); err != nil {
		t.Fatalf("Create: %v", err)
	}
	err := s.SetRole("bob", netid.RoleNone)
	if err == nil {
		t.Fatal("SetRole with RoleNone: want error; got nil (RoleNone must be rejected)")
	}
	// The user's role must not have changed.
	pub, ok := s.Get("bob")
	if !ok {
		t.Fatal("Get after SetRole(RoleNone): user not found")
	}
	if pub.Role == netid.RoleNone {
		t.Error("SetRole with RoleNone: user role was updated to RoleNone despite error")
	}
	if pub.Role != netid.RoleRead {
		t.Errorf("SetRole with RoleNone: user role changed to %v; want RoleRead (original)", pub.Role)
	}
}
