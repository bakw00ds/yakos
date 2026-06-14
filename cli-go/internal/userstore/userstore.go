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
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/bakw00ds/yakos/internal/netid"
)

// usernameRe is the same allow-list as mtlscmd.clientNameRe.
// Accepted characters: ASCII letters, digits, dot, underscore, at-sign, hyphen.
// Maximum length: 64 characters.  "." and ".." are rejected separately below.
var usernameRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)

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
		// Restore original dummy PHC.
		dummyPHC, _ = HashPassword("yakos-dummy-password-for-unknown-user-timing")
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

// internal sentinel errors (not exported — used only for structured audit logging)
var (
	errUnknownUser   = errors.New("userstore: unknown username")
	errWrongPassword = errors.New("userstore: wrong password")
	errUserDisabled  = errors.New("userstore: account disabled")
	errUserLocked    = errors.New("userstore: account locked")
	errDuplicate     = errors.New("userstore: username already exists")
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
	path string
	mu   sync.Mutex
	data storeFile
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
		s := &Store{path: path, data: storeFile{Users: []user{}}}
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

	return &Store{path: path, data: sf}, nil
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
// Returns an error when the username already exists or is invalid.
func (s *Store) Create(username, password string, role netid.Role) error {
	if err := validateUsername(username); err != nil {
		return fmt.Errorf("userstore: create: %w", err)
	}

	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("userstore: create: hash password: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.findLocked(username); ok {
		return fmt.Errorf("userstore: create %q: %w", username, errDuplicate)
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
		return PublicUser{}, fmt.Errorf("userstore: verify: %w", err)
	}
	if !ok {
		// Increment failure counter; lock if threshold reached.
		u.FailedAttempts++
		if u.FailedAttempts >= LockoutThreshold {
			u.LockedUntil = time.Now().UTC().Add(LockoutCooldown)
		}
		u.UpdatedAt = time.Now().UTC()
		s.updateLocked(u)
		if err := s.persist(); err != nil {
			// Best-effort — log but don't hide the auth failure.
			_ = err
		}
		return PublicUser{}, fmt.Errorf("%w: %w", ErrAuthFailed, errWrongPassword)
	}

	// Success: reset failure state.
	u.FailedAttempts = 0
	u.LockedUntil = time.Time{}
	u.UpdatedAt = time.Now().UTC()
	s.updateLocked(u)
	if err := s.persist(); err != nil {
		// Best-effort on success path — return the user anyway.
		_ = err
	}

	return toPublic(u), nil
}

// SetRole sets the role for username.  Returns an error if the user does not exist.
func (s *Store) SetRole(username string, role netid.Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.findLocked(username)
	if !ok {
		return fmt.Errorf("userstore: set-role: user %q not found", username)
	}
	u.Role = role.String()
	u.UpdatedAt = time.Now().UTC()
	s.updateLocked(u)
	return s.persist()
}

// SetPassword sets a new password for username, clears PasswordResetReq, and
// bumps SessionEpoch (invalidating all live sessions for this user).
func (s *Store) SetPassword(username, newPassword string) error {
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
func (s *Store) ResetPassword(username, tempPassword string) error {
	hash, err := HashPassword(tempPassword)
	if err != nil {
		return fmt.Errorf("userstore: reset-password: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.findLocked(username)
	if !ok {
		return fmt.Errorf("userstore: reset-password: user %q not found", username)
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
		return fmt.Errorf("userstore: enable: user %q not found", username)
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
func parsePHC(phc string) (m, t uint32, p uint8, salt, key []byte, err error) {
	var version int
	var mI, tI, pI int

	// We need to extract the salt and key sections by splitting on '$'.
	// Format segments (split by '$', first element is empty because phc starts with '$'):
	// [0]="" [1]="argon2id" [2]="v=19" [3]="m=65536,t=3,p=4" [4]="<salt>" [5]="<key>"
	parts := splitPHC(phc)
	if len(parts) != 6 || parts[1] != "argon2id" {
		err = fmt.Errorf("not a valid argon2id PHC string")
		return
	}

	if _, scanErr := fmt.Sscanf(parts[2], "v=%d", &version); scanErr != nil {
		err = fmt.Errorf("parse version: %w", scanErr)
		return
	}
	if version != argon2.Version {
		err = fmt.Errorf("unsupported argon2id version %d; expected %d", version, argon2.Version)
		return
	}

	if _, scanErr := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mI, &tI, &pI); scanErr != nil {
		err = fmt.Errorf("parse params: %w", scanErr)
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

// splitPHC splits a PHC string on '$', preserving the leading empty segment
// that results from the leading '$' character.
func splitPHC(s string) []string {
	// Walk the string and split on '$'.
	out := make([]string, 0, 7)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '$' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// ---- username validation ----------------------------------------------------

// validateUsername reuses the same allow-list as mtlscmd.ValidateClientName:
// [A-Za-z0-9._@-]{1,64}, with "." and ".." explicitly rejected.
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
