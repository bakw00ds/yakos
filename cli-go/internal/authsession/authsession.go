// Package authsession implements the server-side session store for the
// yakOS networked console (ADR-0005 Phase 1b).
//
// # Design
//
// Sessions are stored in memory only (v1). The store is concurrency-safe.
// Persistence is a flagged follow-on; the Store interface is designed so
// a persistent backend can be swapped in without changing callers.
//
// # Session lifecycle
//
//   - Created on successful password login (Create).
//   - ID and CSRF token regenerated on login and on any privilege change (Rotate).
//   - Expired lazily on Lookup; the caller may schedule periodic Sweep calls
//     to reclaim memory without waiting for a Lookup.
//   - Revoked immediately on logout (Revoke) or user disable (RevokeAllForUser).
//   - Stale epoch detected via ValidateEpoch: callers pass the current
//     epoch from the user store; sessions whose snapshot epoch lags are invalid.
//
// # Token security
//
// Session IDs and CSRF tokens are 32 bytes from crypto/rand, encoded as
// unpadded base64url (43 characters). The store never accepts a caller-
// supplied ID — all IDs are generated internally (session-fixation defense).
//
// # Cookie conventions
//
// SessionCookie and CSRFCookie are cookie-attribute builders only; they do
// not write anything to an http.ResponseWriter. The caller decides when to
// set/clear cookies.
//
// NOTE (Phase-3 wiring): the handler layer must derive the secure bool from
// the actual bind/TLS state rather than accepting it as a free parameter.
// That enforcement lives in the middleware, not here.
//
// # Epoch-based revocation
//
// This package does not import the user store. The caller (typically the
// auth edge middleware) passes the current user epoch to ValidateEpoch.
// Lookup does NOT validate the epoch automatically — the caller must call
// ValidateEpoch after a successful Lookup if epoch invalidation is required.
// This keeps the package dependency-free and testable in isolation.
//
// NOTE (Phase-3 wiring): the middleware must call ValidateEpoch
// unconditionally after every successful Lookup. Missing this call means
// disabled users can reuse stale sessions until they expire naturally.
//
// # Session cap
//
// Config.MaxSessions limits the number of concurrent live sessions to defend
// against store exhaustion once the HTTP surface is wired. Create performs a
// lazy Sweep of expired sessions before enforcing the cap, so expired sessions
// do not count against it.
//
// # Background goroutines
//
// None. The store never spawns goroutines. Call Sweep() from the daemon's
// maintenance loop (e.g. every 10 minutes) to reap expired sessions.
package authsession

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/dashauth"
	"github.com/bakw00ds/yakos/internal/netid"
)

// cookieNameSession is the cookie name for the session ID.
const cookieNameSession = "yakos_session"

// cookieNameCSRF is the name of the non-HttpOnly double-submit CSRF cookie.
const cookieNameCSRF = "yakos_csrf"

// tokenBytes is the number of random bytes for IDs and CSRF tokens (32 bytes → 43 chars base64url).
const tokenBytes = 32

// defaultMaxSessions is the default cap on concurrent live sessions.
// 0 in Config means unlimited; the default applied by NewStore is this value.
const defaultMaxSessions = 10_000

// Session holds the server-side state for one authenticated browser session.
// Values returned by Lookup and Create are copies — mutating a returned
// *Session has no effect on the store.
type Session struct {
	// ID is the opaque session identifier stored in the session cookie.
	// It is never accepted as input from the caller — the store generates it.
	ID string

	// Username is the authenticated user's username.
	Username string

	// Role is a snapshot of the user's role at session issue time.
	// Stale roles are detected via SessionEpoch + ValidateEpoch.
	Role netid.Role

	// CSRFToken is the double-submit CSRF token. It is placed in a non-HttpOnly
	// cookie so the SPA can read it and send it as X-CSRF-Token on mutations.
	CSRFToken string

	// CreatedAt records when the session was first issued.
	CreatedAt time.Time

	// LastSeen records the last successful Lookup (updated on each valid lookup).
	// Used to enforce the idle timeout window.
	LastSeen time.Time

	// SessionEpoch is a snapshot of the user's epoch at issue time.
	// If the user store's current epoch advances past this value, the session
	// is considered stale. Callers validate via ValidateEpoch.
	SessionEpoch int
}

// copyOf returns a shallow copy of sess as a new heap-allocated Session.
// Used by Lookup and other methods so callers never hold a pointer into
// the live map entry.
func copyOf(sess *Session) *Session {
	cp := *sess
	return &cp
}

// Config controls Store behavior.
type Config struct {
	// IdleTimeout is how long a session may go without activity before it
	// expires. Defaults to 2 hours when zero.
	IdleTimeout time.Duration

	// AbsoluteTimeout is the hard maximum lifetime for any session regardless
	// of activity. Defaults to 12 hours when zero.
	AbsoluteTimeout time.Duration

	// MaxSessions is the maximum number of concurrent live (non-expired)
	// sessions. When Create would exceed this limit it first sweeps expired
	// sessions; if the count still exceeds the cap after the sweep, Create
	// returns an error. 0 means use the default (10 000). Set to -1 to
	// disable the cap (not recommended in production).
	MaxSessions int

	// NowFn is the time source used throughout the store. Defaults to
	// time.Now when nil. Override in tests to control time without sleeping.
	NowFn func() time.Time
}

func (c *Config) idleTimeout() time.Duration {
	if c.IdleTimeout > 0 {
		return c.IdleTimeout
	}
	return 2 * time.Hour
}

func (c *Config) absoluteTimeout() time.Duration {
	if c.AbsoluteTimeout > 0 {
		return c.AbsoluteTimeout
	}
	return 12 * time.Hour
}

func (c *Config) maxSessions() int {
	switch {
	case c.MaxSessions < 0:
		return 0 // unlimited
	case c.MaxSessions == 0:
		return defaultMaxSessions
	default:
		return c.MaxSessions
	}
}

func (c *Config) nowFn() func() time.Time {
	if c.NowFn != nil {
		return c.NowFn
	}
	return time.Now
}

// Store is a concurrency-safe in-memory session store.
//
// Zero value is not usable; construct via NewStore.
type Store struct {
	mu       sync.Mutex
	sessions map[string]*Session // keyed by session ID
	cfg      Config
	now      func() time.Time
}

// NewStore constructs a Store with the given Config.
// Zero Config fields receive their documented defaults.
func NewStore(cfg Config) *Store {
	return &Store{
		sessions: make(map[string]*Session),
		cfg:      cfg,
		now:      cfg.nowFn(),
	}
}

// Create issues a new session for the given user. It generates a random
// session ID and CSRF token; the caller-supplied username, role, and epoch
// are stored as a snapshot.
//
// The caller must never supply the session ID — fixation defense is built
// into the store by making ID generation internal-only.
//
// If Config.MaxSessions is positive and the live session count (after
// sweeping expired entries) would exceed the cap, Create returns an error.
func (s *Store) Create(username string, role netid.Role, epoch int) (*Session, error) {
	// Generate tokens outside the lock — crypto/rand may briefly block.
	id, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("authsession: generate session ID: %w", err)
	}
	csrf, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("authsession: generate CSRF token: %w", err)
	}

	now := s.now()
	sess := &Session{
		ID:           id,
		Username:     username,
		Role:         role,
		CSRFToken:    csrf,
		CreatedAt:    now,
		LastSeen:     now,
		SessionEpoch: epoch,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Enforce the session cap. Sweep expired entries first so they don't
	// count against the limit — a session that has already expired is free
	// capacity even if Sweep hasn't been called recently.
	cap := s.cfg.maxSessions()
	if cap > 0 {
		s.sweepLocked(now)
		if len(s.sessions) >= cap {
			return nil, fmt.Errorf("authsession: session cap (%d) reached", cap)
		}
	}

	s.sessions[id] = sess
	return copyOf(sess), nil
}

// Lookup returns a copy of the session for id if it exists and has not expired.
//
// On a valid lookup, LastSeen is updated on the stored entry (sliding idle
// window). The returned *Session is a snapshot copy; retaining it does not
// race with subsequent store mutations.
//
// Expired sessions are removed from the store on Lookup.
//
// NOTE (Phase-3 wiring): the auth edge middleware MUST call ValidateEpoch
// unconditionally after every successful Lookup to guard against stale
// sessions from disabled or role-changed users.
func (s *Store) Lookup(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[id]
	if !ok {
		return nil, false
	}

	now := s.now()
	if s.isExpiredLocked(sess, now) {
		delete(s.sessions, id)
		return nil, false
	}

	// Slide the idle window on the stored entry.
	sess.LastSeen = now

	// Return a copy so the caller's snapshot is stable under future mutations.
	return copyOf(sess), true
}

// Rotate issues a new session ID and CSRF token for the session identified by
// oldID. The new session carries the same username, role, and epoch. The old
// session is removed.
//
// Used on login (session-fixation defense) and on privilege change.
// Returns an error when oldID is not found or has expired.
//
// Transient-miss window: the lock is intentionally dropped between
// delete(oldID) and insert(newID) so that crypto/rand runs outside the lock.
// During this brief window both IDs are absent from the store. Callers must
// handle a 401 on rotation and retry using the new ID returned in the login
// response. Do not rely on the old ID remaining valid after Rotate returns.
func (s *Store) Rotate(oldID string) (*Session, error) {
	s.mu.Lock()

	old, ok := s.sessions[oldID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("authsession: rotate: session not found")
	}

	now := s.now()
	if s.isExpiredLocked(old, now) {
		delete(s.sessions, oldID)
		s.mu.Unlock()
		return nil, fmt.Errorf("authsession: rotate: session expired")
	}

	// Capture identity fields before releasing the lock.
	username := old.Username
	role := old.Role
	epoch := old.SessionEpoch
	createdAt := old.CreatedAt

	delete(s.sessions, oldID)
	s.mu.Unlock()

	// Generate new tokens outside the lock (crypto/rand may block briefly).
	newID, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("authsession: rotate: generate session ID: %w", err)
	}
	newCSRF, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("authsession: rotate: generate CSRF token: %w", err)
	}

	sess := &Session{
		ID:           newID,
		Username:     username,
		Role:         role,
		CSRFToken:    newCSRF,
		CreatedAt:    createdAt, // preserve original creation time for absolute timeout
		LastSeen:     now,
		SessionEpoch: epoch,
	}

	s.mu.Lock()
	s.sessions[newID] = sess
	s.mu.Unlock()

	return copyOf(sess), nil
}

// Revoke removes the session identified by id from the store.
// It is a no-op when id is not present.
func (s *Store) Revoke(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// RevokeAllForUser removes all sessions for the given username.
// Used on user disable or delete.
func (s *Store) RevokeAllForUser(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		if sess.Username == username {
			delete(s.sessions, id)
		}
	}
}

// InvalidateByEpoch removes all sessions for username whose SessionEpoch is
// less than currentEpoch. Used on password change or role change, where the
// user store increments its epoch counter to invalidate stale sessions.
func (s *Store) InvalidateByEpoch(username string, currentEpoch int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		if sess.Username == username && sess.SessionEpoch < currentEpoch {
			delete(s.sessions, id)
		}
	}
}

// ValidateEpoch reports whether sess is still valid given the user store's
// currentEpoch. Returns false when sess.SessionEpoch < currentEpoch.
//
// The >= direction is intentional: epochs advance monotonically forward.
// A session is stale only when its snapshot (sess.SessionEpoch) is behind
// the current epoch, meaning the user store has been modified since the
// session was issued. Equal epochs (sess.SessionEpoch == currentEpoch) are
// valid; a snapshot ahead of the current epoch can't happen in practice but
// is benign (treated as valid).
//
// This is a pure helper — it does not mutate the store. The caller is
// responsible for revoking the session if ValidateEpoch returns false.
//
// Pattern:
//
//	sess, ok := store.Lookup(id)
//	if !ok { ... 401 ... }
//	currentEpoch := userStore.EpochFor(sess.Username)
//	if !store.ValidateEpoch(sess, currentEpoch) { store.Revoke(sess.ID); ... 401 ... }
func (s *Store) ValidateEpoch(sess *Session, currentEpoch int) bool {
	return sess.SessionEpoch >= currentEpoch
}

// ValidateCSRF reports whether the presented CSRF token matches the one stored
// in sess. Uses constant-time comparison to prevent timing attacks.
// Returns false for empty presented tokens.
func (s *Store) ValidateCSRF(sess *Session, presented string) bool {
	if presented == "" {
		return false
	}
	return dashauth.TokenEqual(sess.CSRFToken, presented)
}

// Count returns the number of sessions currently in the store, including
// sessions that have expired but not yet been swept. For a live (non-expired)
// count, call Sweep first.
func (s *Store) Count() int {
	s.mu.Lock()
	n := len(s.sessions)
	s.mu.Unlock()
	return n
}

// ActiveForUser returns the number of live (non-expired) sessions for the
// given username. Expired sessions are not counted but are not removed from
// the store as a side effect — call Sweep separately if reclamation is needed.
//
// The admin Users panel uses this to show how many active sessions exist
// before disabling a user account.
func (s *Store) ActiveForUser(username string) int {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, sess := range s.sessions {
		if sess.Username == username && !s.isExpiredLocked(sess, now) {
			count++
		}
	}
	return count
}

// Sweep removes all expired sessions from the store. It is designed to be
// called from a periodic maintenance goroutine in the daemon — the store
// itself never spawns goroutines.
//
// Typical call site:
//
//	ticker := time.NewTicker(10 * time.Minute)
//	defer ticker.Stop()
//	for range ticker.C {
//	    store.Sweep()
//	}
func (s *Store) Sweep() {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
}

// sweepLocked removes expired sessions. Must be called with s.mu held.
func (s *Store) sweepLocked(now time.Time) {
	for id, sess := range s.sessions {
		if s.isExpiredLocked(sess, now) {
			delete(s.sessions, id)
		}
	}
}

// isExpiredLocked returns true if sess has exceeded either the idle or absolute
// timeout. Must be called with s.mu held.
func (s *Store) isExpiredLocked(sess *Session, now time.Time) bool {
	idle := now.Sub(sess.LastSeen) > s.cfg.idleTimeout()
	absolute := now.Sub(sess.CreatedAt) > s.cfg.absoluteTimeout()
	return idle || absolute
}

// ---- Cookie helpers ---------------------------------------------------------
//
// These methods return *http.Cookie values with the correct security attributes
// per ADR-0005. They do not write to any ResponseWriter — that is the
// handler's responsibility.
//
// The secure parameter must be true when the server is bound to a non-loopback
// address (HTTPS). Pass false only for loopback development servers.
//
// NOTE (Phase-3 wiring): the handler layer must derive secure from the actual
// bind/TLS state (Config.NetworkedMode or similar), not accept it as a free
// caller parameter. That enforcement belongs in the middleware, not here.

// SessionCookie returns an HttpOnly session cookie with the correct security
// attributes. MaxAge is set to the store's AbsoluteTimeout.
func (s *Store) SessionCookie(id string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     cookieNameSession,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(s.cfg.absoluteTimeout().Seconds()),
	}
}

// CSRFCookie returns the non-HttpOnly double-submit CSRF cookie. HttpOnly is
// intentionally false so the SPA can read the token and inject it as
// X-CSRF-Token on mutations.
func (s *Store) CSRFCookie(token string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     cookieNameCSRF,
		Value:    token,
		Path:     "/",
		HttpOnly: false, // must be readable by JS for the double-submit pattern
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(s.cfg.absoluteTimeout().Seconds()),
	}
}

// ClearSessionCookie returns an expired Set-Cookie that instructs the browser
// to delete the session cookie. Mirrors SessionCookie's security attributes
// (HttpOnly, Secure, SameSite=Strict, Path=/) so the browser matches the
// correct cookie for deletion. Used on logout.
func (s *Store) ClearSessionCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     cookieNameSession,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	}
}

// ClearCSRFCookie returns an expired Set-Cookie that instructs the browser
// to delete the CSRF cookie. Mirrors CSRFCookie's security attributes
// (HttpOnly=false, Secure, SameSite=Strict, Path=/) so the browser matches
// the correct cookie for deletion. Used on logout alongside ClearSessionCookie.
func (s *Store) ClearCSRFCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     cookieNameCSRF,
		Value:    "",
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	}
}

// ---- Internal helpers -------------------------------------------------------

// generateToken returns a 32-byte crypto/rand token encoded as unpadded base64url.
// The returned string is 43 characters long.
func generateToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
