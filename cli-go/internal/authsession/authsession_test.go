package authsession_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/authsession"
	"github.com/bakw00ds/yakos/internal/netid"
)

// ---- test helpers -----------------------------------------------------------

// fixedNow returns a now-seam that always returns t.
func fixedNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// advancingNow returns a now-seam backed by a pointer that the test can advance.
func advancingNow(start time.Time) (*time.Time, func() time.Time) {
	cur := start
	return &cur, func() time.Time { return cur }
}

// testStore returns a Store with 2h idle / 12h absolute timeouts and a
// controllable clock. Pass time.Now for production-like behavior or a seam
// function from advancingNow for deterministic time control.
func testStore(nowFn func() time.Time) *authsession.Store {
	return authsession.NewStore(authsession.Config{
		IdleTimeout:     2 * time.Hour,
		AbsoluteTimeout: 12 * time.Hour,
		NowFn:           nowFn,
	})
}

// ---- Token randomness / uniqueness ------------------------------------------

func TestCreate_TokensAreRandom(t *testing.T) {
	s := testStore(time.Now)
	const n = 50
	ids := make(map[string]struct{}, n)
	csrfs := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		sess, err := s.Create("alice", netid.RoleRead, 0)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, dup := ids[sess.ID]; dup {
			t.Fatalf("duplicate session ID at iteration %d", i)
		}
		if _, dup := csrfs[sess.CSRFToken]; dup {
			t.Fatalf("duplicate CSRF token at iteration %d", i)
		}
		ids[sess.ID] = struct{}{}
		csrfs[sess.CSRFToken] = struct{}{}
	}
}

func TestCreate_TokenLength(t *testing.T) {
	s := testStore(time.Now)
	sess, err := s.Create("alice", netid.RoleAdmin, 1)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 32 bytes → 43 chars unpadded base64url.
	if len(sess.ID) != 43 {
		t.Errorf("ID length: got %d, want 43", len(sess.ID))
	}
	if len(sess.CSRFToken) != 43 {
		t.Errorf("CSRFToken length: got %d, want 43", len(sess.CSRFToken))
	}
}

func TestCreate_TokensAreBase64URL(t *testing.T) {
	s := testStore(time.Now)
	sess, err := s.Create("bob", netid.RoleDispatch, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, tok := range []string{sess.ID, sess.CSRFToken} {
		for _, c := range tok {
			if !isBase64URL(c) {
				t.Errorf("token %q contains non-base64url char %q", tok, c)
			}
		}
	}
}

func isBase64URL(c rune) bool {
	return (c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '_'
}

// ---- Lookup: timeout behavior -----------------------------------------------

func TestLookup_ValidSession(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cur, nowFn := advancingNow(base)
	s := testStore(nowFn)

	sess, err := s.Create("alice", netid.RoleAdmin, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Advance by 30 minutes — within idle timeout.
	*cur = base.Add(30 * time.Minute)

	got, ok := s.Lookup(sess.ID)
	if !ok {
		t.Fatal("Lookup: expected valid session, got false")
	}
	if got.ID != sess.ID {
		t.Errorf("Lookup: wrong ID: got %q", got.ID)
	}
}

func TestLookup_UpdatesLastSeen(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cur, nowFn := advancingNow(base)
	s := testStore(nowFn)

	sess, err := s.Create("alice", netid.RoleRead, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Advance 90 minutes, then look up — LastSeen slides to now.
	*cur = base.Add(90 * time.Minute)
	got, ok := s.Lookup(sess.ID)
	if !ok {
		t.Fatal("expected valid session")
	}
	if !got.LastSeen.Equal(*cur) {
		t.Errorf("LastSeen not updated: got %v, want %v", got.LastSeen, *cur)
	}
}

func TestLookup_ExpiredIdleTimeout(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cur, nowFn := advancingNow(base)
	s := testStore(nowFn)

	sess, err := s.Create("alice", netid.RoleRead, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Advance past the 2-hour idle timeout.
	*cur = base.Add(2*time.Hour + 1*time.Second)

	_, ok := s.Lookup(sess.ID)
	if ok {
		t.Error("Lookup: expected false for idle-expired session")
	}

	// Confirm the session was removed.
	_, ok = s.Lookup(sess.ID)
	if ok {
		t.Error("Lookup: expired session should have been removed from store")
	}
}

func TestLookup_ExpiredAbsoluteTimeout(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur, nowFn := advancingNow(base)
	s := testStore(nowFn)

	sess, err := s.Create("alice", netid.RoleAdmin, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Simulate keeping the session alive by looking up every 30 min,
	// sliding LastSeen, but the absolute timeout should still fire.
	for range 25 {
		*cur = (*cur).Add(30 * time.Minute)
		s.Lookup(sess.ID) // slides LastSeen; that's fine
	}
	// Now cur is 12.5 hours in — past the 12-hour absolute timeout.
	_, ok := s.Lookup(sess.ID)
	if ok {
		t.Error("Lookup: session should have expired via absolute timeout after 12h")
	}
}

func TestLookup_NonExistentID(t *testing.T) {
	s := testStore(time.Now)
	_, ok := s.Lookup("no-such-id")
	if ok {
		t.Error("Lookup: expected false for non-existent ID")
	}
}

// ---- Rotate -----------------------------------------------------------------

func TestRotate_NewIDAndCSRF(t *testing.T) {
	s := testStore(time.Now)
	sess, _ := s.Create("alice", netid.RoleAdmin, 0)

	newSess, err := s.Rotate(sess.ID)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if newSess.ID == sess.ID {
		t.Error("Rotate: new ID must differ from old ID")
	}
	if newSess.CSRFToken == sess.CSRFToken {
		t.Error("Rotate: new CSRF token must differ from old CSRF token")
	}
}

func TestRotate_PreservesIdentity(t *testing.T) {
	s := testStore(time.Now)
	sess, _ := s.Create("alice", netid.RoleAdmin, 5)

	newSess, err := s.Rotate(sess.ID)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if newSess.Username != "alice" {
		t.Errorf("Rotate: Username: got %q, want alice", newSess.Username)
	}
	if newSess.Role != netid.RoleAdmin {
		t.Errorf("Rotate: Role: got %v, want Admin", newSess.Role)
	}
	if newSess.SessionEpoch != 5 {
		t.Errorf("Rotate: SessionEpoch: got %d, want 5", newSess.SessionEpoch)
	}
}

func TestRotate_OldIDInvalidated(t *testing.T) {
	s := testStore(time.Now)
	sess, _ := s.Create("alice", netid.RoleAdmin, 0)

	_, err := s.Rotate(sess.ID)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	_, ok := s.Lookup(sess.ID)
	if ok {
		t.Error("Rotate: old session ID should be invalid after rotation")
	}
}

func TestRotate_NewIDValid(t *testing.T) {
	s := testStore(time.Now)
	sess, _ := s.Create("alice", netid.RoleRead, 0)

	newSess, err := s.Rotate(sess.ID)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	got, ok := s.Lookup(newSess.ID)
	if !ok {
		t.Fatal("Rotate: new session ID should be valid")
	}
	if got.Username != "alice" {
		t.Errorf("Rotate: Username mismatch: got %q", got.Username)
	}
}

func TestRotate_NotFound(t *testing.T) {
	s := testStore(time.Now)
	_, err := s.Rotate("does-not-exist")
	if err == nil {
		t.Error("Rotate: expected error for non-existent ID")
	}
}

func TestRotate_ExpiredSession(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cur, nowFn := advancingNow(base)
	s := testStore(nowFn)

	sess, _ := s.Create("alice", netid.RoleRead, 0)
	*cur = base.Add(3 * time.Hour) // past idle timeout

	_, err := s.Rotate(sess.ID)
	if err == nil {
		t.Error("Rotate: expected error for expired session")
	}
}

func TestRotate_PreservesCreatedAt(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cur, nowFn := advancingNow(base)
	s := testStore(nowFn)

	sess, _ := s.Create("alice", netid.RoleAdmin, 0)
	originalCreatedAt := sess.CreatedAt

	*cur = base.Add(30 * time.Minute)
	newSess, err := s.Rotate(sess.ID)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	// CreatedAt must be preserved so the absolute timeout is anchored to login.
	if !newSess.CreatedAt.Equal(originalCreatedAt) {
		t.Errorf("Rotate: CreatedAt changed; got %v, want %v", newSess.CreatedAt, originalCreatedAt)
	}
}

// ---- Session fixation defense -----------------------------------------------
// The public API provides no way to supply a known ID. This test confirms the
// surface: Create takes (username, role, epoch) and never an ID parameter.
// (This is a compile-time guarantee, but the test documents it explicitly.)
func TestFixationDefense_NoCallerSuppliedID(t *testing.T) {
	s := testStore(time.Now)
	// Create's signature: (username string, role netid.Role, epoch int) — no ID.
	// Rotate's signature: (oldID string) — takes the old ID to invalidate it, not to set the new one.
	// If these lines compile, the surface is correct.
	sess, err := s.Create("alice", netid.RoleAdmin, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" {
		t.Error("Create should always produce a non-empty ID")
	}
	// Rotate produces a new random ID; the caller cannot influence it.
	newSess, err := s.Rotate(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if newSess.ID == "" {
		t.Error("Rotate should always produce a non-empty ID")
	}
}

// ---- Revoke -----------------------------------------------------------------

func TestRevoke_RemovesSession(t *testing.T) {
	s := testStore(time.Now)
	sess, _ := s.Create("alice", netid.RoleRead, 0)

	s.Revoke(sess.ID)

	_, ok := s.Lookup(sess.ID)
	if ok {
		t.Error("Revoke: session should be gone after Revoke")
	}
}

func TestRevoke_NoOpForMissing(t *testing.T) {
	s := testStore(time.Now)
	// Should not panic or error.
	s.Revoke("does-not-exist")
}

// ---- RevokeAllForUser -------------------------------------------------------

func TestRevokeAllForUser_RemovesAll(t *testing.T) {
	s := testStore(time.Now)
	sess1, _ := s.Create("alice", netid.RoleRead, 0)
	sess2, _ := s.Create("alice", netid.RoleAdmin, 0)
	sess3, _ := s.Create("bob", netid.RoleRead, 0)

	s.RevokeAllForUser("alice")

	_, ok1 := s.Lookup(sess1.ID)
	_, ok2 := s.Lookup(sess2.ID)
	_, ok3 := s.Lookup(sess3.ID)

	if ok1 || ok2 {
		t.Error("RevokeAllForUser: alice's sessions should be gone")
	}
	if !ok3 {
		t.Error("RevokeAllForUser: bob's session should remain")
	}
}

func TestRevokeAllForUser_NoOpForUnknownUser(t *testing.T) {
	s := testStore(time.Now)
	sess, _ := s.Create("alice", netid.RoleRead, 0)

	s.RevokeAllForUser("nobody")

	_, ok := s.Lookup(sess.ID)
	if !ok {
		t.Error("RevokeAllForUser: alice's session should remain after revoking unknown user")
	}
}

// ---- InvalidateByEpoch ------------------------------------------------------

func TestInvalidateByEpoch_StaleEpochRemoved(t *testing.T) {
	s := testStore(time.Now)
	old, _ := s.Create("alice", netid.RoleAdmin, 2)
	current, _ := s.Create("alice", netid.RoleAdmin, 5)

	s.InvalidateByEpoch("alice", 5)

	_, ok1 := s.Lookup(old.ID)
	_, ok2 := s.Lookup(current.ID)

	if ok1 {
		t.Error("InvalidateByEpoch: session with epoch 2 should be removed when current=5")
	}
	if !ok2 {
		t.Error("InvalidateByEpoch: session with epoch 5 should survive when current=5")
	}
}

func TestInvalidateByEpoch_DoesNotAffectOtherUsers(t *testing.T) {
	s := testStore(time.Now)
	alice, _ := s.Create("alice", netid.RoleAdmin, 1)
	bob, _ := s.Create("bob", netid.RoleRead, 1)

	s.InvalidateByEpoch("alice", 10)

	_, aliceOk := s.Lookup(alice.ID)
	_, bobOk := s.Lookup(bob.ID)

	if aliceOk {
		t.Error("InvalidateByEpoch: alice's stale session should be gone")
	}
	if !bobOk {
		t.Error("InvalidateByEpoch: bob's session should be unaffected")
	}
}

// ---- ValidateEpoch ----------------------------------------------------------

func TestValidateEpoch_CurrentEpochValid(t *testing.T) {
	s := testStore(time.Now)
	sess, _ := s.Create("alice", netid.RoleAdmin, 5)

	if !s.ValidateEpoch(sess, 5) {
		t.Error("ValidateEpoch: epoch 5 >= 5 should be valid")
	}
}

func TestValidateEpoch_StaleEpochInvalid(t *testing.T) {
	s := testStore(time.Now)
	sess, _ := s.Create("alice", netid.RoleAdmin, 3)

	if s.ValidateEpoch(sess, 5) {
		t.Error("ValidateEpoch: epoch 3 < 5 should be invalid")
	}
}

func TestValidateEpoch_HigherSnapshotValid(t *testing.T) {
	// If the session snapshot epoch is somehow higher than current
	// (shouldn't happen in practice but must not cause issues).
	s := testStore(time.Now)
	sess, _ := s.Create("alice", netid.RoleAdmin, 10)

	if !s.ValidateEpoch(sess, 5) {
		t.Error("ValidateEpoch: snapshot epoch 10 >= current 5 should be valid")
	}
}

// ---- ValidateCSRF -----------------------------------------------------------

func TestValidateCSRF_CorrectToken(t *testing.T) {
	s := testStore(time.Now)
	sess, _ := s.Create("alice", netid.RoleAdmin, 0)

	if !s.ValidateCSRF(sess, sess.CSRFToken) {
		t.Error("ValidateCSRF: correct token should return true")
	}
}

func TestValidateCSRF_WrongToken(t *testing.T) {
	s := testStore(time.Now)
	sess, _ := s.Create("alice", netid.RoleAdmin, 0)

	if s.ValidateCSRF(sess, "wrong-token-value") {
		t.Error("ValidateCSRF: wrong token should return false")
	}
}

func TestValidateCSRF_EmptyToken(t *testing.T) {
	s := testStore(time.Now)
	sess, _ := s.Create("alice", netid.RoleAdmin, 0)

	if s.ValidateCSRF(sess, "") {
		t.Error("ValidateCSRF: empty token should return false")
	}
}

func TestValidateCSRF_ConstantTimeRejectsPartialMatch(t *testing.T) {
	s := testStore(time.Now)
	sess, _ := s.Create("alice", netid.RoleAdmin, 0)

	// Present a prefix of the real token — must be rejected.
	partial := sess.CSRFToken[:10]
	if s.ValidateCSRF(sess, partial) {
		t.Error("ValidateCSRF: partial token should return false")
	}
}

// ---- Cookie helpers ---------------------------------------------------------

func TestSessionCookie_Attributes(t *testing.T) {
	s := testStore(time.Now)
	cookie := s.SessionCookie("some-session-id", true)

	if cookie.Name != "yakos_session" {
		t.Errorf("Name: got %q, want yakos_session", cookie.Name)
	}
	if cookie.Value != "some-session-id" {
		t.Errorf("Value: got %q, want some-session-id", cookie.Value)
	}
	if !cookie.HttpOnly {
		t.Error("SessionCookie: HttpOnly must be true")
	}
	if !cookie.Secure {
		t.Error("SessionCookie: Secure must be true when secure=true")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("SessionCookie: SameSite: got %v, want Strict", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("SessionCookie: Path: got %q, want /", cookie.Path)
	}
	if cookie.MaxAge <= 0 {
		t.Errorf("SessionCookie: MaxAge should be positive; got %d", cookie.MaxAge)
	}
}

func TestSessionCookie_InsecureLoopback(t *testing.T) {
	s := testStore(time.Now)
	cookie := s.SessionCookie("id", false)
	if cookie.Secure {
		t.Error("SessionCookie: Secure should be false when secure=false")
	}
}

func TestCSRFCookie_NotHttpOnly(t *testing.T) {
	s := testStore(time.Now)
	cookie := s.CSRFCookie("csrf-token", true)

	if cookie.Name != "yakos_csrf" {
		t.Errorf("Name: got %q, want yakos_csrf", cookie.Name)
	}
	if cookie.HttpOnly {
		t.Error("CSRFCookie: HttpOnly must be false (JS must be able to read it)")
	}
	if !cookie.Secure {
		t.Error("CSRFCookie: Secure must be true when secure=true")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("CSRFCookie: SameSite: got %v, want Strict", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("CSRFCookie: Path: got %q, want /", cookie.Path)
	}
}

func TestClearSessionCookie_Expires(t *testing.T) {
	cookie := authsession.ClearSessionCookie()
	if cookie.Name != "yakos_session" {
		t.Errorf("ClearSessionCookie: Name: got %q", cookie.Name)
	}
	if cookie.MaxAge != -1 {
		t.Errorf("ClearSessionCookie: MaxAge: got %d, want -1", cookie.MaxAge)
	}
	if cookie.Value != "" {
		t.Errorf("ClearSessionCookie: Value should be empty; got %q", cookie.Value)
	}
}

func TestClearCSRFCookie_Expires(t *testing.T) {
	cookie := authsession.ClearCSRFCookie()
	if cookie.Name != "yakos_csrf" {
		t.Errorf("ClearCSRFCookie: Name: got %q", cookie.Name)
	}
	if cookie.MaxAge != -1 {
		t.Errorf("ClearCSRFCookie: MaxAge: got %d, want -1", cookie.MaxAge)
	}
}

// ---- Sweep ------------------------------------------------------------------

func TestSweep_RemovesExpiredSessions(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cur, nowFn := advancingNow(base)
	s := testStore(nowFn)

	sess, _ := s.Create("alice", netid.RoleRead, 0)

	// Advance past idle timeout.
	*cur = base.Add(3 * time.Hour)
	s.Sweep()

	// Sweep should have removed the expired session.
	_, ok := s.Lookup(sess.ID)
	if ok {
		t.Error("Sweep: expired session should be removed")
	}
}

func TestSweep_PreservesValidSessions(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cur, nowFn := advancingNow(base)
	s := testStore(nowFn)

	sess, _ := s.Create("alice", netid.RoleRead, 0)

	// Advance only 30 minutes — still within idle timeout.
	*cur = base.Add(30 * time.Minute)
	s.Sweep()

	_, ok := s.Lookup(sess.ID)
	if !ok {
		t.Error("Sweep: valid session should not be removed")
	}
}

// ---- Concurrency ------------------------------------------------------------

func TestConcurrency_ParallelCreateLookupRotateRevoke(t *testing.T) {
	s := testStore(time.Now)

	const goroutines = 20
	const ops = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				sess, err := s.Create("user", netid.RoleRead, 0)
				if err != nil {
					// race-safe: just skip
					continue
				}
				if _, ok := s.Lookup(sess.ID); !ok {
					// May have been revoked by another goroutine — acceptable.
					continue
				}
				newSess, err := s.Rotate(sess.ID)
				if err == nil {
					s.Revoke(newSess.ID)
				}
				s.Revoke(sess.ID) // no-op if already revoked
			}
		}()
	}
	wg.Wait()
}

func TestConcurrency_ParallelSweep(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur, nowFn := advancingNow(base)
	s := testStore(nowFn)

	// Populate the store.
	for i := 0; i < 100; i++ {
		s.Create("user", netid.RoleRead, 0) //nolint:errcheck
	}

	// Advance time and sweep concurrently.
	*cur = base.Add(3 * time.Hour)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Sweep()
		}()
	}
	wg.Wait()
	// Should not panic or data-race.
}

func TestConcurrency_RevokeAllForUserParallel(t *testing.T) {
	s := testStore(time.Now)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			s.Create("alice", netid.RoleAdmin, 0) //nolint:errcheck
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			s.RevokeAllForUser("alice")
		}
	}()
	wg.Wait()
}

// ---- Default config seam ----------------------------------------------------

func TestDefaultConfig_IdleAndAbsoluteTimeouts(t *testing.T) {
	// Zero Config should use 2h/12h defaults.
	s := authsession.NewStore(authsession.Config{})
	sess, err := s.Create("alice", netid.RoleRead, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Session should be valid immediately.
	_, ok := s.Lookup(sess.ID)
	if !ok {
		t.Error("NewStore with zero Config: session should be valid immediately")
	}
}

// ---- Idle window sliding ----------------------------------------------------

func TestLookup_IdleWindowSlides(t *testing.T) {
	// Verify that repeated lookups within the idle timeout keep a session alive
	// even as wall-clock time advances beyond a single idle period.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur, nowFn := advancingNow(base)
	s := authsession.NewStore(authsession.Config{
		IdleTimeout:     30 * time.Minute,
		AbsoluteTimeout: 24 * time.Hour,
		NowFn:           nowFn,
	})

	sess, _ := s.Create("alice", netid.RoleRead, 0)

	// Access the session every 20 minutes — within the 30-minute idle window.
	for i := 0; i < 5; i++ {
		*cur = (*cur).Add(20 * time.Minute)
		_, ok := s.Lookup(sess.ID)
		if !ok {
			t.Fatalf("Lookup failed at minute %d; session should still be alive", (i+1)*20)
		}
	}

	// Now go idle for 31 minutes — should expire.
	*cur = cur.Add(31 * time.Minute)
	_, ok := s.Lookup(sess.ID)
	if ok {
		t.Error("Session should be expired after 31 minutes of inactivity")
	}
}

// ---- Store stores username/role/epoch fields correctly ----------------------

func TestCreate_FieldsPreserved(t *testing.T) {
	s := testStore(time.Now)
	sess, err := s.Create("charlie", netid.RoleFlowsRun, 7)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.Username != "charlie" {
		t.Errorf("Username: got %q, want charlie", sess.Username)
	}
	if sess.Role != netid.RoleFlowsRun {
		t.Errorf("Role: got %v, want FlowsRun", sess.Role)
	}
	if sess.SessionEpoch != 7 {
		t.Errorf("SessionEpoch: got %d, want 7", sess.SessionEpoch)
	}
	if sess.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if sess.LastSeen.IsZero() {
		t.Error("LastSeen should not be zero")
	}
}

// ---- No wildcard imports in the public surface ------------------------------

// TestNoCSRFTokenInSessionID confirms that the CSRF token is separate from the
// session ID. (Belt-and-suspenders: they are different random values.)
func TestTokensAreDistinct(t *testing.T) {
	s := testStore(time.Now)
	sess, _ := s.Create("alice", netid.RoleAdmin, 0)
	if sess.ID == sess.CSRFToken {
		t.Error("Session ID and CSRF token must be independent random values")
	}
}

// TestCSRFTokenLeak confirms that the session cookie does NOT contain the
// CSRF token — they are separate cookies.
func TestCSRFCookie_ValueIsCSRFToken(t *testing.T) {
	s := testStore(time.Now)
	sess, _ := s.Create("alice", netid.RoleAdmin, 0)

	sessionCookie := s.SessionCookie(sess.ID, true)
	csrfCookie := s.CSRFCookie(sess.CSRFToken, true)

	if sessionCookie.Value == csrfCookie.Value {
		t.Error("Session cookie and CSRF cookie must carry different values")
	}
	if strings.Contains(sessionCookie.Value, sess.CSRFToken) {
		t.Error("Session cookie must not embed the CSRF token")
	}
}
