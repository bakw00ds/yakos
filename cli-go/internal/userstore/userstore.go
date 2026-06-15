// Package userstore implements the persistent, file-backed user store for the
// yakOS hybrid-auth program (ADR-0005, Phase 1a).
//
// # Overview
//
// Users are stored in <dir>/users.json (permissions 0600; parent 0700).
// Passwords are hashed with argon2id and stored in PHC string format so that
// parameters are self-describing and can be raised in future releases without
// a migration.
//
// # File-trust hardening
//
// The same hardening applied to roles.json in netid.RoleMapper is applied
// here: Open refuses to use a users.json that is a symlink or that has
// group- or other-writable permission bits (Unix only; Windows stub always
// permits).  See perm_unix.go / perm_windows.go.
//
// # Lockout
//
// After LockoutThreshold consecutive failed login attempts the account is
// locked for LockoutCooldown.  Verify always runs the full argon2id
// derivation — even for unknown usernames — to prevent timing-based
// username enumeration.
//
// # Concurrency
//
// All public methods on Store are safe for concurrent use.  A sync.Mutex
// serialises every mutation; reads hold the lock for consistency.
//
// # Stability: experimental (Phase 1a — no HTTP wiring)
package userstore

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/bakw00ds/yakos/internal/netid"
)

// usernameRe is the same allow-list as mtlscmd.clientNameRe.
// Accepted characters: ASCII letters, digits, dot, underscore, at-sign, hyphen.
// Maximum length: 64 characters.  "." and ".." are rejected separately below.
var usernameRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)

// ---- Password policy --------------------------------------------------------

// MinPasswordLen is the minimum acceptable password length enforced by Create,
// SetPassword, and ResetPassword.  12 characters is the documented operator-
// account floor; adjust the constant and call a config layer in a later phase.
const MinPasswordLen = 12

// ---- Lockout params ---------------------------------------------------------

const (
	// LockoutThreshold is the number of consecutive failed attempts that
	// triggers an account lockout.
	LockoutThreshold = 10

	// LockoutCooldown is how long an account remains locked after reaching
	// the failure threshold.
	LockoutCooldown = 15 * time.Minute
)

// ---- argon2id params --------------------------------------------------------
// These are the locked parameters from ADR-0005.

const (
	// argon2SaltLen and argon2KeyLen are always fixed; only the cost params below
	// are overridable for testing.
	argon2SaltLen = 16
	argon2KeyLen  = 32
)

// argon2id cost parameters.  Production values are the ADR-0005 locked params.
// Tests may call SetArgon2ParamsForTest to reduce these for speed; the PHC
// string stores the actual params used, so verification always uses whatever
// params are embedded in the stored hash — no migration needed.
var (
	activeArgon2Time    uint32 = 3
	activeArgon2Memory  uint32 = 64 * 1024 // 64 MiB
	activeArgon2Threads uint8  = 4
)

// SetArgon2ParamsForTest replaces the package-level argon2id cost parameters
// and regenerates the dummy PHC used for unknown-username timing.
//
// ONLY for use in tests.  Call t.Cleanup to restore the original values:
//
//	restore := userstore.SetArgon2ParamsForTest(1, 64, 1)
//	t.Cleanup(restore)
//
// Using reduced parameters in tests does not affect verification of real
// hashes because VerifyPassword parses parameters from the stored PHC string.
func SetArgon2ParamsForTest(time, memory uint32, threads uint8) func() {
	oldTime := activeArgon2Time
	oldMem := activeArgon2Memory
	oldThreads := activeArgon2Threads

	activeArgon2Time = time
	activeArgon2Memory = memory
	activeArgon2Threads = threads

	// Regenerate the dummy PHC with the new params so Verify still works.
	var err error
	dummyPHC, err = HashPassword("yakos-dummy-password-for-unknown-user-timing")
	if err != nil {
		panic(fmt.Sprintf("userstore: SetArgon2ParamsForTest: regenerate dummy PHC: %v", err))
	}

	return func() {
		activeArgon2Time = oldTime
		activeArgon2Memory = oldMem
		activeArgon2Threads = oldThreads
		// Restore original dummy PHC; panic on failure (same posture as init).
		var restoreErr error
		dummyPHC, restoreErr = HashPassword("yakos-dummy-password-for-unknown-user-timing")
		if restoreErr != nil {
			panic(fmt.Sprintf("userstore: SetArgon2ParamsForTest restore: regenerate dummy PHC: %v", restoreErr))
		}
	}
}

// dummyPHC is a pre-computed PHC hash for a random password, used by Verify
// to perform a constant-time argon2id derivation when the username is unknown.
// This prevents timing-based username enumeration.  It is initialised in
// init() so the derivation happens at package-load time, not on the first
// request (which could be observable as a cold-start latency spike).
var dummyPHC string

func init() {
	var err error
	dummyPHC, err = HashPassword("yakos-dummy-password-for-unknown-user-timing")
	if err != nil {
		// If argon2 fails during init, userstore is unusable.
		panic(fmt.Sprintf("userstore: failed to initialise dummy PHC: %v", err))
	}
}

// ---- Sentinel errors --------------------------------------------------------

// These sentinel values are used internally for audit-log discrimination.
// The public Verify method returns only ErrAuthFailed for every failure mode
// so that no distinguishing information reaches the caller.

// ErrAuthFailed is the generic authentication error returned by Verify for
// every failure mode (unknown user, wrong password, disabled, locked).
// Callers must return "invalid username or password" to the end-user without
// qualification.
var ErrAuthFailed = errors.New("invalid username or password")

// ErrNotFirstUser is returned by CreateFirstAdmin when the store already
// contains at least one user.  The caller (POST /setup handler) translates
// this to a 409 Conflict response so the client knows setup is already done.
//
// The check is performed under the store mutex, making the zero-users assertion
// atomic with the insert — closing the TOCTOU window that exists when a caller
// does Count()==0 and Create in separate calls.
var ErrNotFirstUser = errors.New("userstore: admin already exists; use CreateFirstAdmin only on a zero-users store")

// ErrLastAdmin is returned by the guarded mutation methods (SetRoleGuarded,
// DisableGuarded, DeleteGuarded) when the operation would leave zero
// non-disabled admins.  Callers map this to 409 Conflict (HTTP) or a clear
// error message (CLI).
//
// Using errors.Is(err, ErrLastAdmin) is the canonical check; do NOT use
// strings.Contains on the error message.
var ErrLastAdmin = errors.New("userstore: operation would remove the last admin")

// ErrUserNotFound is returned by the guarded mutation methods when the target
// username does not exist in the store.  Callers map this to 404 Not Found
// (HTTP) or a clear error message (CLI).
//
// Using errors.Is(err, ErrUserNotFound) is the canonical check.
var ErrUserNotFound = errors.New("userstore: user not found")

// ErrDuplicate is returned by Create when the username already exists.
// Exported so callers can use errors.Is instead of string matching.
var ErrDuplicate = errors.New("userstore: username already exists")

// internal sentinel errors (not exported — used only for structured audit logging)
var (
	errUnknownUser   = errors.New("userstore: unknown username")
	errWrongPassword = errors.New("userstore: wrong password")
	errUserDisabled  = errors.New("userstore: account disabled")
	errUserLocked    = errors.New("userstore: account locked")
)

// AuthFailureReason extracts an internal reason code from a Verify error for
// structured audit logging.  It returns a short token suitable for log fields:
// "unknown_user", "wrong_password", "disabled", "locked".
// Returns "" when err is not one of the internal failure sentinels.
//
// Callers MUST NOT surface this reason to the end-user.  It is for audit
// logs only.
func AuthFailureReason(err error) string {
	switch {
	case errors.Is(err, errUnknownUser):
		return "unknown_user"
	case errors.Is(err, errWrongPassword):
		return "wrong_password"
	case errors.Is(err, errUserDisabled):
		return "disabled"
	case errors.Is(err, errUserLocked):
		return "locked"
	default:
		return ""
	}
}

// ---- User record ------------------------------------------------------------

// user is the internal (full) representation of a user, including the
// password hash.  It is never serialised directly to a caller-facing API.
type user struct {
	Username         string    `json:"username"`
	PasswordHash     string    `json:"passwordHash"`
	Role             string    `json:"role"`
	Disabled         bool      `json:"disabled"`
	PasswordResetReq bool      `json:"passwordResetReq"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	FailedAttempts   int       `json:"failedAttempts"`
	LockedUntil      time.Time `json:"lockedUntil,omitempty"`
	SessionEpoch     int       `json:"sessionEpoch"`
}

// PublicUser is a caller-facing view of a user record with the password hash
// omitted.  This is the shape returned by Get and List.
type PublicUser struct {
	Username         string     `json:"username"`
	Role             netid.Role `json:"-"`
	RoleString       string     `json:"role"`
	Disabled         bool       `json:"disabled"`
	PasswordResetReq bool       `json:"passwordResetReq"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	FailedAttempts   int        `json:"failedAttempts"`
	LockedUntil      time.Time  `json:"lockedUntil,omitempty"`
	SessionEpoch     int        `json:"sessionEpoch"`
}

func toPublic(u user) PublicUser {
	return PublicUser{
		Username:         u.Username,
		Role:             netid.ParseRole(u.Role),
		RoleString:       u.Role,
		Disabled:         u.Disabled,
		PasswordResetReq: u.PasswordResetReq,
		CreatedAt:        u.CreatedAt,
		UpdatedAt:        u.UpdatedAt,
		FailedAttempts:   u.FailedAttempts,
		LockedUntil:      u.LockedUntil,
		SessionEpoch:     u.SessionEpoch,
	}
}

// ---- on-disk schema ---------------------------------------------------------

type storeFile struct {
	Users []user `json:"users"`
}

// ---- Store ------------------------------------------------------------------

// Store is a file-backed user store.  Obtain one with Open.
// All methods are safe for concurrent use.
type Store struct {
	path   string
	mu     sync.Mutex
	data   storeFile
	logger func(msg string) // optional; called on non-fatal internal errors
}

// Open loads (or initialises) the user store at path.
//
// File-trust hardening: Open refuses to use a users.json that is a symlink or
// is group/world-writable (mirrors netid.RoleMapper hardening from ADR-0005).
// The parent directory and file are created at 0700/0600 respectively if
// absent (MkdirAll ensures no "save without parent dir" failure class).
//
// The path argument must be the full file path (e.g. ~/.yakos-state/users/users.json).
func Open(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("userstore: mkdir %q: %w", dir, err)
	}

	// File-trust: check before reading. Tolerate missing file (fresh install).
	fi, err := os.Lstat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("userstore: lstat %q: %w", path, err)
		}
		// File does not exist — create an empty store and persist it.
		s := &Store{path: path, data: storeFile{Users: []user{}}, logger: func(string) {}}
		if err := s.persist(); err != nil {
			return nil, fmt.Errorf("userstore: create %q: %w", path, err)
		}
		return s, nil
	}

	// Reject symlinks.
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("userstore: %q is a symlink; refusing to use it", path)
	}

	// Reject unsafe permissions (Unix only; Windows stub always returns true).
	if !filePermOK(fi) {
		return nil, fmt.Errorf("userstore: %q has unsafe permissions (must not be group- or world-writable)", path)
	}

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("userstore: read %q: %w", path, err)
	}

	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("userstore: parse %q: %w", path, err)
	}
	if sf.Users == nil {
		sf.Users = []user{}
	}

	return &Store{path: path, data: sf, logger: func(string) {}}, nil
}

// SetLogger attaches a log function to the store.  It is called with a
// diagnostic message when a non-fatal internal error occurs — specifically
// when persist() fails after an in-memory mutation (lockout state, success
// counter reset).  The in-memory state is always updated; the logger makes
// the durability gap observable so operators can act on it.
//
// Phase 3 will inject the daemon's structured logger here.  Until then the
// default no-op keeps existing callers unchanged.
//
// fn must be safe for concurrent use; it is called while s.mu is held.
func (s *Store) SetLogger(fn func(msg string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fn == nil {
		fn = func(string) {}
	}
	s.logger = fn
}

// Count returns the number of users in the store.
// Zero users means setup-token mode is active.
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data.Users)
}

// AdminCount returns the number of non-disabled admin users.
// Callers use this to guard against removing the last admin.
func (s *Store) AdminCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.adminCountLocked()
}

func (s *Store) adminCountLocked() int {
	n := 0
	for _, u := range s.data.Users {
		if u.Role == netid.RoleAdmin.String() && !u.Disabled {
			n++
		}
	}
	return n
}

// Create creates a new user with the given username, password, and role.
// Returns an error when the username already exists, is invalid, or the
// password is shorter than MinPasswordLen.
func (s *Store) Create(username, password string, role netid.Role) error {
	if err := validateUsername(username); err != nil {
		return fmt.Errorf("userstore: create: %w", err)
	}
	if err := validatePassword(password); err != nil {
		return fmt.Errorf("userstore: create: %w", err)
	}

	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("userstore: create: hash password: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.findLocked(username); ok {
		return fmt.Errorf("userstore: create %q: %w", username, ErrDuplicate)
	}

	now := time.Now().UTC()
	s.data.Users = append(s.data.Users, user{
		Username:     username,
		PasswordHash: hash,
		Role:         role.String(),
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	return s.persist()
}

// CreateFirstAdmin creates the first admin user atomically.  Under the store
// mutex it asserts len(users)==0 first — returning ErrNotFirstUser if any
// user already exists — then creates the admin.  This makes the zero-users
// invariant atomic with the insert, closing the TOCTOU window that would exist
// if a caller did Count()==0 and Create in separate locking steps.
//
// Callers MUST use CreateFirstAdmin (not Create) for the /setup flow.
// Subsequent user creation uses the ordinary Create method.
func (s *Store) CreateFirstAdmin(username, password string) error {
	if err := validateUsername(username); err != nil {
		return fmt.Errorf("userstore: create-first-admin: %w", err)
	}
	if err := validatePassword(password); err != nil {
		return fmt.Errorf("userstore: create-first-admin: %w", err)
	}

	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("userstore: create-first-admin: hash password: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Atomically assert zero users under the same lock as the insert.
	if len(s.data.Users) > 0 {
		return ErrNotFirstUser
	}

	now := time.Now().UTC()
	s.data.Users = append(s.data.Users, user{
		Username:     username,
		PasswordHash: hash,
		Role:         netid.RoleAdmin.String(),
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	return s.persist()
}

// Get returns the PublicUser for username, or (zero, false) if not found.
func (s *Store) Get(username string) (PublicUser, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.findLocked(username)
	if !ok {
		return PublicUser{}, false
	}
	return toPublic(u), true
}

// List returns all users as PublicUser (no password hashes).
// The returned slice is a copy; mutations do not affect the store.
func (s *Store) List() []PublicUser {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]PublicUser, len(s.data.Users))
	for i, u := range s.data.Users {
		out[i] = toPublic(u)
	}
	return out
}

// Verify is the login primitive.  It checks disabled/lockout, then verifies
// the password.  On success it resets failedAttempts.  On failure it
// increments failedAttempts and applies lockout after the threshold.
//
// Timing protection: argon2id is always run — even for unknown usernames —
// so that response latency does not reveal whether the username exists.
//
// Return value: on any failure the returned error wraps ErrAuthFailed.
// Structured failure reason for audit logging is available via AuthFailureReason.
func (s *Store) Verify(username, password string) (PublicUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, known := s.findLocked(username)

	if !known {
		// Run the full argon2id derivation against the dummy hash to prevent
		// username enumeration via timing.
		_, _ = VerifyPassword(dummyPHC, password)
		return PublicUser{}, fmt.Errorf("%w: %w", ErrAuthFailed, errUnknownUser)
	}

	if u.Disabled {
		// Still run argon2 to preserve timing uniformity.
		_, _ = VerifyPassword(dummyPHC, password)
		return PublicUser{}, fmt.Errorf("%w: %w", ErrAuthFailed, errUserDisabled)
	}

	if !u.LockedUntil.IsZero() && time.Now().UTC().Before(u.LockedUntil) {
		// Still run argon2 to preserve timing uniformity.
		_, _ = VerifyPassword(dummyPHC, password)
		return PublicUser{}, fmt.Errorf("%w: %w", ErrAuthFailed, errUserLocked)
	}

	ok, err := VerifyPassword(u.PasswordHash, password)
	if err != nil {
		// Corrupt or unparseable stored PHC.  This is an infrastructure failure,
		// not a user-facing auth reason, so AuthFailureReason returns "".
		// Wrapping ErrAuthFailed ensures the caller still gets a consistent
		// error type and cannot accidentally expose the raw internal message.
		return PublicUser{}, fmt.Errorf("%w: internal error verifying password: %w", ErrAuthFailed, err)
	}
	if !ok {
		// Increment failure counter; lock if threshold reached.
		u.FailedAttempts++
		if u.FailedAttempts >= LockoutThreshold {
			u.LockedUntil = time.Now().UTC().Add(LockoutCooldown)
		}
		u.UpdatedAt = time.Now().UTC()
		s.updateLocked(u)
		if persistErr := s.persist(); persistErr != nil {
			// In-memory state is updated (live process stays protected).
			// Log so operators can detect the durability gap; do not hide
			// the auth failure from the caller.
			s.logger(fmt.Sprintf("userstore: failed to persist lockout state for %q: %v", u.Username, persistErr))
		}
		return PublicUser{}, fmt.Errorf("%w: %w", ErrAuthFailed, errWrongPassword)
	}

	// Success: reset failure state.
	u.FailedAttempts = 0
	u.LockedUntil = time.Time{}
	u.UpdatedAt = time.Now().UTC()
	s.updateLocked(u)
	if persistErr := s.persist(); persistErr != nil {
		// In-memory reset is applied; the live session is valid.
		// Log the durability gap so it is observable.
		s.logger(fmt.Sprintf("userstore: failed to persist login-success state for %q: %v", u.Username, persistErr))
	}

	return toPublic(u), nil
}

// SetRole sets the role for username and bumps SessionEpoch, atomically
// invalidating all live sessions for this user (per ADR-0005: a role change
// must take effect immediately; sessions must re-resolve the new role).
//
// Returns an error if the user does not exist.
func (s *Store) SetRole(username string, role netid.Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.findLocked(username)
	if !ok {
		return fmt.Errorf("userstore: set-role: user %q not found", username)
	}
	u.Role = role.String()
	u.SessionEpoch++
	u.UpdatedAt = time.Now().UTC()
	s.updateLocked(u)
	return s.persist()
}

// SetPassword sets a new password for username, clears PasswordResetReq, and
// bumps SessionEpoch (invalidating all live sessions for this user).
// Returns an error when the new password is shorter than MinPasswordLen.
func (s *Store) SetPassword(username, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return fmt.Errorf("userstore: set-password: %w", err)
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("userstore: set-password: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.findLocked(username)
	if !ok {
		return fmt.Errorf("userstore: set-password: user %q not found", username)
	}
	u.PasswordHash = hash
	u.PasswordResetReq = false
	u.SessionEpoch++
	u.UpdatedAt = time.Now().UTC()
	s.updateLocked(u)
	return s.persist()
}

// ResetPassword sets a new (typically admin-generated) password and marks the
// account as requiring a password change on next login.  Bumps SessionEpoch.
// Returns an error when the new password is shorter than MinPasswordLen.
func (s *Store) ResetPassword(username, tempPassword string) error {
	if err := validatePassword(tempPassword); err != nil {
		return fmt.Errorf("userstore: reset-password: %w", err)
	}
	hash, err := HashPassword(tempPassword)
	if err != nil {
		return fmt.Errorf("userstore: reset-password: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.findLocked(username)
	if !ok {
		return fmt.Errorf("userstore: reset-password: user %q: %w", username, ErrUserNotFound)
	}
	u.PasswordHash = hash
	u.PasswordResetReq = true
	u.SessionEpoch++
	u.UpdatedAt = time.Now().UTC()
	s.updateLocked(u)
	return s.persist()
}

// Disable disables a user account and bumps SessionEpoch, immediately
// invalidating all live sessions for this user.
func (s *Store) Disable(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.findLocked(username)
	if !ok {
		return fmt.Errorf("userstore: disable: user %q not found", username)
	}
	u.Disabled = true
	u.SessionEpoch++
	u.UpdatedAt = time.Now().UTC()
	s.updateLocked(u)
	return s.persist()
}

// Enable re-enables a disabled user account.
func (s *Store) Enable(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.findLocked(username)
	if !ok {
		return fmt.Errorf("userstore: enable: user %q: %w", username, ErrUserNotFound)
	}
	u.Disabled = false
	u.UpdatedAt = time.Now().UTC()
	s.updateLocked(u)
	return s.persist()
}

// Delete removes a user from the store.
func (s *Store) Delete(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.indexLocked(username)
	if idx < 0 {
		return fmt.Errorf("userstore: delete: user %q not found", username)
	}
	s.data.Users = append(s.data.Users[:idx], s.data.Users[idx+1:]...)
	return s.persist()
}

// BumpSessionEpoch increments the SessionEpoch for a user, invalidating all
// live sessions.  Callers may use this after a privilege change.
func (s *Store) BumpSessionEpoch(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.findLocked(username)
	if !ok {
		return fmt.Errorf("userstore: bump-session-epoch: user %q not found", username)
	}
	u.SessionEpoch++
	u.UpdatedAt = time.Now().UTC()
	s.updateLocked(u)
	return s.persist()
}

// ---- Guarded admin-safe mutation methods ------------------------------------
//
// SetRoleGuarded, DisableGuarded, and DeleteGuarded are atomic alternatives to
// SetRole, Disable, and Delete that incorporate the last-admin self-protection
// check under the SAME mutex lock as the mutation.  This closes the TOCTOU
// race that exists when callers call AdminCount() and then mutate in separate
// locking steps: two concurrent goroutines can both observe AdminCount()==2,
// both proceed, and together reduce the admin count to zero.
//
// Each guarded method:
//  1. Acquires s.mu.
//  2. Looks up the target user (returns ErrUserNotFound if absent).
//  3. Evaluates whether the op would drop admin count to zero via adminCountLocked.
//     Returns ErrLastAdmin (without mutating) if so.
//  4. Performs the mutation under the same lock.
//  5. Persists and returns.
//
// Callers MUST use errors.Is(err, ErrLastAdmin) and errors.Is(err, ErrUserNotFound)
// to distinguish these sentinels from other errors.
//
// Cross-process note: these guards are in-process only (single sync.Mutex).
// `yakos console user` CLI accesses users.json directly with its own Store
// instance — a separate OS process from the daemon.  The in-process guard
// prevents races within a single process but does not protect against
// concurrent CLI + daemon mutations.  Operators should avoid running CLI
// user-management commands while the daemon is serving admin API requests.
// A future enhancement could use file-locking across processes.

// SetRoleGuarded sets the role for username, bumps SessionEpoch, and returns
// ErrLastAdmin if the operation would demote the last non-disabled admin.
// Atomic: the last-admin check and the mutation share a single lock.
func (s *Store) SetRoleGuarded(username string, role netid.Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.findLocked(username)
	if !ok {
		return fmt.Errorf("userstore: set-role: user %q: %w", username, ErrUserNotFound)
	}

	// Self-protection: if demoting an active admin and they are the last one.
	if role != netid.RoleAdmin && u.Role == netid.RoleAdmin.String() && !u.Disabled {
		if s.adminCountLocked() <= 1 {
			return fmt.Errorf("userstore: set-role: user %q: %w", username, ErrLastAdmin)
		}
	}

	u.Role = role.String()
	u.SessionEpoch++
	u.UpdatedAt = time.Now().UTC()
	s.updateLocked(u)
	return s.persist()
}

// DisableGuarded disables a user account, bumps SessionEpoch, and returns
// ErrLastAdmin if the operation would disable the last non-disabled admin.
// Atomic: the last-admin check and the mutation share a single lock.
func (s *Store) DisableGuarded(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.findLocked(username)
	if !ok {
		return fmt.Errorf("userstore: disable: user %q: %w", username, ErrUserNotFound)
	}

	// Self-protection: if disabling an active admin and they are the last one.
	if u.Role == netid.RoleAdmin.String() && !u.Disabled {
		if s.adminCountLocked() <= 1 {
			return fmt.Errorf("userstore: disable: user %q: %w", username, ErrLastAdmin)
		}
	}

	u.Disabled = true
	u.SessionEpoch++
	u.UpdatedAt = time.Now().UTC()
	s.updateLocked(u)
	return s.persist()
}

// DeleteGuarded deletes a user and returns ErrLastAdmin if the operation would
// remove the last non-disabled admin.
// Atomic: the last-admin check and the deletion share a single lock.
func (s *Store) DeleteGuarded(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.indexLocked(username)
	if idx < 0 {
		return fmt.Errorf("userstore: delete: user %q: %w", username, ErrUserNotFound)
	}

	u := s.data.Users[idx]

	// Self-protection: if deleting an active admin and they are the last one.
	if u.Role == netid.RoleAdmin.String() && !u.Disabled {
		if s.adminCountLocked() <= 1 {
			return fmt.Errorf("userstore: delete: user %q: %w", username, ErrLastAdmin)
		}
	}

	s.data.Users = append(s.data.Users[:idx], s.data.Users[idx+1:]...)
	return s.persist()
}

// ---- internal helpers -------------------------------------------------------

// findLocked returns a copy of the user record for username.
// Caller must hold s.mu.
func (s *Store) findLocked(username string) (user, bool) {
	for _, u := range s.data.Users {
		if u.Username == username {
			return u, true
		}
	}
	return user{}, false
}

// indexLocked returns the slice index of username, or -1 if not found.
// Caller must hold s.mu.
func (s *Store) indexLocked(username string) int {
	for i, u := range s.data.Users {
		if u.Username == username {
			return i
		}
	}
	return -1
}

// updateLocked replaces the in-memory user record for u.Username.
// Caller must hold s.mu.
func (s *Store) updateLocked(u user) {
	for i, existing := range s.data.Users {
		if existing.Username == u.Username {
			s.data.Users[i] = u
			return
		}
	}
}

// persist writes s.data atomically to s.path at mode 0600.
// Caller must hold s.mu (or be in a constructor path before the Store is shared).
func (s *Store) persist() error {
	out, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("userstore: marshal: %w", err)
	}
	out = append(out, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("userstore: mkdir %q: %w", dir, err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0600); err != nil { //nolint:gosec
		return fmt.Errorf("userstore: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("userstore: rename: %w", err)
	}
	// Belt-and-suspenders: enforce 0600 in case umask is 0.
	if err := os.Chmod(s.path, 0600); err != nil { //nolint:gosec
		return fmt.Errorf("userstore: chmod: %w", err)
	}
	return nil
}

// ---- argon2id ---------------------------------------------------------------

// HashPassword derives an argon2id hash for pw and encodes it as a PHC string:
//
//	$argon2id$v=19$m=65536,t=3,p=4$<base64-salt>$<base64-hash>
//
// A fresh 16-byte random salt is generated for each call.
// The cost parameters are taken from the package-level active* variables
// (production defaults: t=3, m=64MiB, p=4; overridable in tests via
// SetArgon2ParamsForTest).
func HashPassword(pw string) (string, error) {
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("userstore: generate salt: %w", err)
	}
	t := activeArgon2Time
	m := activeArgon2Memory
	p := activeArgon2Threads
	key := argon2.IDKey([]byte(pw), salt, t, m, p, argon2KeyLen)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Key := base64.RawStdEncoding.EncodeToString(key)

	phc := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, m, t, p, b64Salt, b64Key)
	return phc, nil
}

// VerifyPassword verifies pw against a PHC-encoded argon2id hash.
// The parameters (m, t, p) are parsed from the stored PHC string so that
// older records created under previous parameters continue to verify correctly
// as parameters are raised over time.
//
// Uses subtle.ConstantTimeCompare for the final key comparison.
func VerifyPassword(phc, pw string) (bool, error) {
	m, t, p, salt, key, err := parsePHC(phc)
	if err != nil {
		return false, fmt.Errorf("userstore: parse PHC: %w", err)
	}

	derived := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(key)))
	if subtle.ConstantTimeCompare(derived, key) != 1 {
		return false, nil
	}
	return true, nil
}

// parsePHC parses an argon2id PHC string and returns its components.
// Expected format: $argon2id$v=19$m=<m>,t=<t>,p=<p>$<b64salt>$<b64key>
//
// Security: cost parameters are bounds-checked before being passed to argon2.
// A corrupt stored value with m=-1 would otherwise be cast to ~4 TiB and
// OOM-kill the process on the next login attempt for that account.
func parsePHC(phc string) (m, t uint32, p uint8, salt, key []byte, err error) {
	// base64.RawStdEncoding never emits '$', so strings.Split is safe here.
	// Format (6 segments when split on '$', first is empty due to leading '$'):
	// [0]="" [1]="argon2id" [2]="v=19" [3]="m=65536,t=3,p=4" [4]="<salt>" [5]="<key>"
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		err = fmt.Errorf("not a valid argon2id PHC string")
		return
	}

	var version int
	if _, scanErr := fmt.Sscanf(parts[2], "v=%d", &version); scanErr != nil {
		err = fmt.Errorf("parse version: %w", scanErr)
		return
	}
	if version != argon2.Version {
		err = fmt.Errorf("unsupported argon2id version %d; expected %d", version, argon2.Version)
		return
	}

	var mI, tI, pI int
	if _, scanErr := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mI, &tI, &pI); scanErr != nil {
		err = fmt.Errorf("parse params: %w", scanErr)
		return
	}
	// Bounds-check before converting to unsigned types.  Negative values
	// from a corrupt file would wrap to enormous uint32/uint8 values and
	// either OOM-kill the process (memory) or silently reduce cost (time/threads).
	// Upper bound on memory: 4 GiB (1<<22 KiB) is a sane operational ceiling.
	if mI <= 0 || tI <= 0 || pI <= 0 || pI > 255 {
		err = fmt.Errorf("PHC params out of range: m=%d, t=%d, p=%d (all must be positive; p ≤ 255)", mI, tI, pI)
		return
	}
	const maxMemoryKiB = 1 << 22 // 4 GiB
	if mI > maxMemoryKiB {
		err = fmt.Errorf("PHC memory param %d KiB exceeds maximum %d KiB (4 GiB)", mI, maxMemoryKiB)
		return
	}
	m = uint32(mI)
	t = uint32(tI)
	p = uint8(pI)

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		err = fmt.Errorf("decode salt: %w", err)
		return
	}
	key, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		err = fmt.Errorf("decode key: %w", err)
		return
	}
	return
}

// ---- username validation ----------------------------------------------------

// ValidateUsername is the exported username validator.  It enforces the same
// allow-list as mtlscmd.ValidateClientName: [A-Za-z0-9._@-]{1,64}, with "."
// and ".." explicitly rejected.
//
// Exported so that callers outside the package (e.g. the /setup handler) can
// pre-validate a username before performing irreversible operations (such as
// consuming a one-time setup token).  This ensures both layers enforce exactly
// the same rule with no drift.
func ValidateUsername(name string) error {
	return validateUsername(name)
}

// validateUsername is the internal implementation shared by Create,
// CreateFirstAdmin, and the exported ValidateUsername.
func validateUsername(name string) error {
	if name == "" {
		return fmt.Errorf("username must not be empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("username %q is reserved", name)
	}
	if !usernameRe.MatchString(name) {
		return fmt.Errorf("username %q is invalid: must match [A-Za-z0-9._@-]{1,64} and contain no path separators", name)
	}
	return nil
}

// ---- password validation ----------------------------------------------------

// validatePassword rejects passwords shorter than MinPasswordLen.
// Called by Create, SetPassword, and ResetPassword before hashing.
func validatePassword(pw string) error {
	if len(pw) < MinPasswordLen {
		return fmt.Errorf("password too short: minimum %d characters", MinPasswordLen)
	}
	return nil
}
