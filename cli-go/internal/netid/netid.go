// Package netid defines the networked-operator identity model for the yakOS
// console.
//
// # Overview
//
// yakOS supports three bind regimes (per ADR-0005):
//
//   - Loopback: bearer-token cooperative labeling.  The Identity is
//     Authenticated=false; Role is admin (preserving today's full access).
//     AuthMethod is AuthMethodNone.
//   - Networked machines: mTLS client certificates.  The Identity is
//     Authenticated=true; OperatorID is the cert CN; Role is resolved from
//     the CN→Role mapping file.  AuthMethod is AuthMethodCert.
//   - Networked humans: password + session cookie.  The Identity is
//     Authenticated=true; OperatorID and Role are resolved by a caller-
//     supplied SessionLookupFn.  AuthMethod is AuthMethodSession.
//
// This package provides the Role type, the Identity struct, and the resolver
// used by the console edge middleware.  It does NOT attach a listener or start
// a server — that is the caller's responsibility.
//
// # Role ordering
//
// The four roles are ordered by privilege:
//
//	read < dispatch < flows-run < admin
//
// Allows(needed) returns true if the receiver's privilege level is ≥ needed.
//
// # Role-mapping file
//
// The CN→Role mapping is read from
// ~/.yakos-state/mtls/roles.json (or the stateDir-relative path
// mtls/roles.json).  The file is optional; a missing file is tolerated but
// fails closed: every authenticated cert resolves to RoleNone (no access).
//
// When NewRoleMapper is called with an empty stateDir, file I/O is skipped
// entirely and all lookups return RoleNone (fail-closed for misconfiguration).
//
// Format (JSON):
//
//	{
//	  "alice": "admin",
//	  "bob":   "dispatch",
//	  "ci":    "flows-run"
//	}
//
// Keys are certificate Common Names; values are role strings matching the
// Role constants below.  The special key "*" (WildcardCN) grants its role to
// every authenticated cert without an explicit entry, e.g. {"*": "read"}
// restores the pre-2026-09-29 default.  A CN with no entry and no wildcard,
// and any unknown role string, resolves to RoleNone (fail closed).
// Reloading on every request is safe (file is small; OS page cache keeps I/O
// cheap) and avoids the need for signal-based reload machinery.
//
// # Identity context
//
// Resolver.Middleware inserts an Identity into each request's context.  Use
// IdentityFrom(ctx) to retrieve it.  If no middleware has run, IdentityFrom
// returns the zero Identity (Authenticated=false, empty OperatorID, RoleRead).
// Handlers must not panic on the zero value.
//
// # Stability: experimental (Phase 6a foundation)
package netid

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// WildcardCN is the roles.json key meaning "any authenticated client cert
// without an explicit mapping gets this role" (ADR-0005 Amendment 2026-09-29).
// An explicit CN entry always beats the wildcard.
const WildcardCN = "*"

// ---- Role -------------------------------------------------------------------

// Role represents an operator's privilege level on the yakOS console.
// The roles are ordered; higher-indexed constants represent greater privilege.
type Role int

const (
	// RoleNone is the zero-value sentinel assigned to unauthenticated networked
	// identities (certless + sessionless on a non-loopback resolver).  It is
	// strictly below RoleRead: RoleNone.Allows(x) is false for every real role,
	// including RoleRead.
	//
	// RoleNone is intentionally NOT parseable from any config string and must
	// never be stored in roles.json or users.json as a user's role.  It is an
	// internal sentinel for fail-closed, unauthenticated networked identities
	// only — a defense-in-depth property that makes requireRole(RoleRead) reject
	// an unauthenticated identity even if requireAuthOrRedirect is somehow skipped.
	//
	// Loopback behavior is UNCHANGED: the loopback cooperative-bearer path
	// produces RoleAdmin (not RoleNone), preserving full local access.
	RoleNone Role = iota

	// RoleRead permits read-only access (Overview, Cost, Perf, Kanban,
	// shared transcripts).
	RoleRead

	// RoleDispatch permits opening chat panes and running agent dispatches.
	RoleDispatch

	// RoleFlowsRun permits triggering and resuming workflows.
	RoleFlowsRun

	// RoleAdmin permits cert/token rotation, bind config, and future
	// client enrollment.
	RoleAdmin
)

// String returns the canonical role string used in config files and logs.
// RoleNone is an internal sentinel; its string representation is "none" and is
// not accepted by ParseRole (it maps to RoleRead, the least assignable privilege).
func (r Role) String() string {
	switch r {
	case RoleNone:
		return "none"
	case RoleRead:
		return "read"
	case RoleDispatch:
		return "dispatch"
	case RoleFlowsRun:
		return "flows-run"
	case RoleAdmin:
		return "admin"
	default:
		return "none"
	}
}

// ParseRole converts a role string to a Role constant.
// Unknown strings return RoleRead (least assignable privilege).
// "none" is explicitly NOT parseable to RoleNone — RoleNone is an internal
// sentinel for unauthenticated identities and must not be assignable to users.
func ParseRole(s string) Role {
	switch s {
	case "read":
		return RoleRead
	case "dispatch":
		return RoleDispatch
	case "flows-run":
		return RoleFlowsRun
	case "admin":
		return RoleAdmin
	default:
		return RoleRead
	}
}

// ParseRoleStrict is ParseRole without the RoleRead fallback: ok is false for
// any unrecognised string (including "none" and ""), so callers deciding
// authorization can fail closed instead of silently granting read.
func ParseRoleStrict(s string) (Role, bool) {
	switch s {
	case "read":
		return RoleRead, true
	case "dispatch":
		return RoleDispatch, true
	case "flows-run":
		return RoleFlowsRun, true
	case "admin":
		return RoleAdmin, true
	default:
		return RoleNone, false
	}
}

// IsAssignableRole reports whether r is a role that may be assigned to a user
// account.  RoleNone is excluded — it is an internal sentinel for unauthenticated
// networked identities and must never appear in roles.json or users.json.
//
// Callers that accept a netid.Role as user input (Create, SetRole, etc.) should
// call IsAssignableRole before persisting the value.
func IsAssignableRole(r Role) bool {
	switch r {
	case RoleRead, RoleDispatch, RoleFlowsRun, RoleAdmin:
		return true
	default:
		return false
	}
}

// Allows reports whether r has at least the privilege level of needed.
// RoleNone.Allows(any real role) is always false because RoleNone < RoleRead.
// Example: RoleAdmin.Allows(RoleDispatch) == true; RoleNone.Allows(RoleRead) == false.
func (r Role) Allows(needed Role) bool {
	return r >= needed
}

// ---- AuthMethod -------------------------------------------------------------

// AuthMethod identifies which authentication mechanism produced a given
// Identity.  The zero value AuthMethodNone correctly describes both today's
// loopback cooperative-label identities and unauthenticated fail-closed
// identities, so existing struct-literal tests that do not set this field
// continue to compile and pass without change.
type AuthMethod int

const (
	// AuthMethodNone is the zero value.  Used for loopback cooperative-label
	// identities (Authenticated=false) and unauthenticated/fail-closed
	// identities.  Existing callers that do not set this field default to None.
	AuthMethodNone AuthMethod = iota

	// AuthMethodCert indicates the identity was established by a verified mTLS
	// client certificate (VerifiedChains non-empty in r.TLS).
	AuthMethodCert

	// AuthMethodSession indicates the identity was established by a valid
	// server-side session cookie resolved through the injected SessionLookupFn.
	AuthMethodSession
)

// String returns the canonical auth-method string used in audit logs.
func (a AuthMethod) String() string {
	switch a {
	case AuthMethodCert:
		return "cert"
	case AuthMethodSession:
		return "session"
	default:
		return "none"
	}
}

// ---- Identity ---------------------------------------------------------------

// Identity represents a resolved operator identity attached to a request.
//
// Authenticated is true only when the identity was established by a verified
// mTLS client certificate or a valid session cookie.  When false (loopback
// bearer path), the OperatorID is a cooperative label, not a cryptographic
// guarantee.
//
// AuthMethod records which mechanism produced this identity and is written to
// audit logs so the dispatch NDJSON log can distinguish the three regimes.
//
// This distinction is load-bearing for the audit trail described in
// ADR-0005 §Consequences C3: code that reads dispatch logs must not treat
// loopback entries as cryptographically authenticated.
type Identity struct {
	// OperatorID is the operator identifier.  For cert-authenticated identities
	// this is the client certificate CN.  For session-authenticated identities
	// this is the username resolved from the session store.  For
	// loopback/bearer identities this is the cooperative label supplied by the
	// caller.
	OperatorID string

	// Role is the resolved privilege level for this identity.
	Role Role

	// Authenticated is true when OperatorID is bound to a verified client
	// certificate or a valid server-side session.  False for loopback bearer
	// sessions.
	Authenticated bool

	// Resolved is true when this Identity was stamped by Resolver.Middleware.
	// A zero-value Identity (e.g. in tests that bypass the middleware chain via
	// srv.Handler()) has Resolved=false.  Enforcement middleware MUST check
	// Resolved before applying role gates so that test paths using bare
	// srv.Handler() remain unaffected — preserving the loopback-safe invariant.
	Resolved bool

	// AuthMethod records which authentication mechanism produced this identity.
	// The zero value AuthMethodNone correctly describes loopback and
	// unauthenticated identities, so existing callers that do not set this
	// field default to None without any source change.
	AuthMethod AuthMethod
}

// ---- Context key ------------------------------------------------------------

// contextKey is the unexported type used as the context key for Identity.
// Using a private type prevents collisions with other packages' context keys.
type contextKey struct{}

// IdentityFrom retrieves the Identity stored in ctx by Resolver.Middleware.
// If no middleware has run, it returns a zero Identity (unauthenticated,
// empty OperatorID, RoleNone, Resolved=false).
//
// Enforcement middleware (requireRole) fails CLOSED on an identity with
// Resolved=false (S-2 R17): the zero Identity is not a real principal and is
// refused by every role gate.
func IdentityFrom(ctx context.Context) Identity {
	if id, ok := ctx.Value(contextKey{}).(Identity); ok {
		return id
	}
	return Identity{}
}

// HasIdentity reports whether ANY identity was stored in ctx, including an
// explicit unresolved one. It lets a caller tell "the resolver never ran"
// (nothing stored) from "an identity was stored" without treating the zero
// Identity as a real principal.
func HasIdentity(ctx context.Context) bool {
	_, ok := ctx.Value(contextKey{}).(Identity)
	return ok
}

// withIdentity returns a new context carrying id.
func withIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// WithIdentityForTest returns a new context carrying id.
// This is intended for use in external test packages that need to simulate
// Resolver.Middleware having stamped a specific Identity onto the request
// context (e.g. consoleui enforcement tests).  It is the exported wrapper for
// the unexported withIdentity.
//
// Safe to call from production code but intended only for tests — the name
// makes the test-only intent clear.
func WithIdentityForTest(ctx context.Context, id Identity) context.Context {
	return withIdentity(ctx, id)
}

// ---- Role mapper ------------------------------------------------------------

// RoleMapper resolves a certificate CN to a Role using a JSON mapping file.
// A missing file is tolerated and fails closed (all CNs resolve to RoleNone).
//
// The file is re-read on every call to Lookup; since it is small the OS
// page cache makes repeated reads cheap and no signal-based reload is needed.
// Concurrent reads are safe: path is immutable after construction and
// ReadFile + Unmarshal operate on local variables only.
//
// When stateDir is empty (NewRoleMapper("")) all Lookups return RoleNone
// with no file I/O, preventing CWD-relative path resolution as a footgun.
type RoleMapper struct {
	path   string   // absolute path to roles.json; empty when stateDir was ""
	warned sync.Map // dedup keys for bad-role WARN logs
}

// NewRoleMapper returns a RoleMapper that reads from
// <stateDir>/mtls/roles.json.
//
// If stateDir is empty, no file is ever read and all Lookups return RoleNone.
// Callers should always pass the actual state directory (e.g. ~/.yakos-state)
// to avoid an empty path being silently resolved against the process CWD.
func NewRoleMapper(stateDir string) *RoleMapper {
	if stateDir == "" {
		return &RoleMapper{path: ""}
	}
	return &RoleMapper{
		path: filepath.Join(stateDir, "mtls", "roles.json"),
	}
}

// Lookup returns the Role for the given certificate CN.
//
// Fail-closed posture (ADR-0005 Amendment 2026-09-29): a CN that is not in
// the mapping resolves to RoleNone.  Resolution order:
//
//  1. an explicit entry for the CN wins (including over the wildcard);
//  2. otherwise the wildcard entry WildcardCN ("*") applies, if present;
//  3. otherwise RoleNone.
//
// The role string "none" is an explicit deny (beats the wildcard, no log).
// Any other string ParseRoleStrict does not recognise (typo, "") resolves to
// RoleNone and logs one WARN per (key, value) naming the key ("*" for the
// wildcard, else the CN).
// A missing, empty, symlinked, unsafe-permission or malformed file resolves
// to RoleNone for every CN.  When the mapper was constructed with an empty
// stateDir, Lookup always returns RoleNone without any file I/O.
//
// LOW-1 file-trust hardening: before reading the file, Lookup calls
// os.Lstat to check:
//   - Symlinks: if the path is a symlink it is treated as missing.
//   - Permission bits (Unix only): see roles_perm_unix.go / roles_perm_windows.go.
//     On Unix, group/other-writable files are treated as missing.
//     On Windows, Unix permission bits are not meaningful; the check is
//     skipped (the 0700 parent directory created by the mtls package
//     provides the trust boundary).
func (m *RoleMapper) Lookup(cn string) Role {
	mapping, ok := m.load()
	if !ok {
		return RoleNone
	}
	key := cn
	roleStr, found := mapping[key]
	if !found {
		key = WildcardCN
		roleStr, found = mapping[key]
		if !found {
			return RoleNone
		}
	}
	// "none" is the explicit-deny value: it beats the wildcard and is not a
	// typo, so it does not warn.
	if roleStr == "none" {
		return RoleNone
	}
	r, valid := ParseRoleStrict(roleStr)
	if !valid {
		// Name the key the bad value lives under ("*" for the wildcard), not
		// the requesting CN, so one bad entry logs once however many certs hit it.
		m.warnBadRole(key, roleStr)
		return RoleNone
	}
	return r
}

// load reads and validates the mapping file.  ok is false when the file is
// unusable (no path, missing, symlink, unsafe perms, unreadable, malformed).
func (m *RoleMapper) load() (map[string]string, bool) {
	if m.path == "" {
		return nil, false
	}
	fi, err := os.Lstat(m.path)
	if err != nil {
		return nil, false // missing: expected before roles are configured
	}
	// From here the file exists but may be unusable. That locks out every
	// cert (including the bootstrap admin), so say why, once per file version.
	if fi.Mode()&os.ModeSymlink != 0 {
		m.warnUnusable(fi, "is a symlink")
		return nil, false
	}
	if !rolesFilePermOK(fi) {
		m.warnUnusable(fi, "is group/other-writable")
		return nil, false
	}
	data, err := os.ReadFile(m.path) //nolint:gosec
	if err != nil {
		m.warnUnusable(fi, "is unreadable: "+err.Error())
		return nil, false
	}
	var mapping map[string]string
	if err := json.Unmarshal(data, &mapping); err != nil {
		m.warnUnusable(fi, "is not valid JSON: "+err.Error())
		return nil, false
	}
	return mapping, true
}

// warnUnusable logs one WARN per (reason, file mtime) so per-request loads do
// not flood, yet each new bad version of the file is reported.
func (m *RoleMapper) warnUnusable(fi os.FileInfo, reason string) {
	if _, dup := m.warned.LoadOrStore("file\x00"+reason+"\x00"+fi.ModTime().String(), struct{}{}); dup {
		return
	}
	slog.Warn("netid: role map is unusable; every client cert resolves to no access",
		"file", m.path, "reason", reason)
}

// warnBadRole logs once per (CN, value) so per-request Lookups do not flood.
func (m *RoleMapper) warnBadRole(cn, roleStr string) {
	if _, dup := m.warned.LoadOrStore(cn+"\x00"+roleStr, struct{}{}); dup {
		return
	}
	slog.Warn("netid: roles.json has an unrecognised role; treating CN as no access",
		"cn", cn, "role", roleStr,
		"valid_roles", "read, dispatch, flows-run, admin")
}

// StartupSummary inspects the mapping file once and returns a single
// human-readable line for the daemon startup log, plus whether it is a
// warning.  It is used only for networked daemons.
//
//   - no usable file, or file with zero entries: warn=true, states that no
//     client certificate is authorized and how to fix it;
//   - otherwise: warn=false, a short summary (entry count, wildcard role).
func (m *RoleMapper) StartupSummary() (msg string, warn bool) {
	mapping, ok := m.load()
	if !ok || len(mapping) == 0 {
		where := m.path
		if where == "" {
			where = "<stateDir>/mtls/roles.json"
		}
		return "no client certificate is authorized: role map " + where +
			" is missing, empty or untrusted, so every CA-signed cert resolves to no access (RoleNone). " +
			"Fix: run `yakos mtls set-role <cn> <role>`, or `yakos mtls set-role '*' read` to grant read to any authenticated cert", true
	}
	if w, has := mapping[WildcardCN]; has {
		return "role map loaded: " + itoa(len(mapping)) + " entries; wildcard \"*\" grants \"" + w +
			"\" to every authenticated cert without an explicit mapping", false
	}
	return "role map loaded: " + itoa(len(mapping)) + " entries; unmapped certs get no access", false
}

func itoa(n int) string { return strconv.Itoa(n) }

// ---- Client-cert CN extraction ---------------------------------------------

// CNFromRequest extracts the client certificate Common Name from the
// TLS connection state embedded in r, if present and verified.
//
// Returns ("", false) when:
//   - the connection has no TLS state (plain HTTP or test),
//   - no client certificate was presented, or
//   - the certificate chain is empty.
//
// Note: by the time a request reaches an HTTP handler over a TLS listener,
// any client cert that was presented has already been cryptographically
// verified by the TLS stack (VerifyClientCertIfGiven verifies if given;
// RequireAndVerifyClientCert verifies and requires one).  This function only
// extracts the CN from VerifiedChains; it does not re-verify.  When no
// client cert was presented, VerifiedChains is empty and this returns ("", false).
func CNFromRequest(r *http.Request) (cn string, ok bool) {
	return CNFromTLS(r.TLS)
}

// OpenAICompatOwner is the owner label every conversation made through the
// OpenAI-compatible endpoint carries (K-150). It is reserved: no user, client
// certificate name or certificate CN may take it, or that principal would read
// and resume the endpoint's conversations.
const OpenAICompatOwner = "openai-compat"

// IsReservedOwner reports whether name is an owner label no operator may hold.
// The match ignores case and surrounding space.
func IsReservedOwner(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), OpenAICompatOwner)
}

// CNFromTLS extracts the client certificate CN from a TLS connection state.
// Returns ("", false) if cs is nil, contains no verified peer certificates, or
// the leaf certificate has an empty Subject CN (SAN-only).
func CNFromTLS(cs *tls.ConnectionState) (cn string, ok bool) {
	if cs == nil {
		return "", false
	}
	if len(cs.VerifiedChains) == 0 || len(cs.VerifiedChains[0]) == 0 {
		return "", false
	}
	cn = cs.VerifiedChains[0][0].Subject.CommonName
	if IsReservedOwner(cn) {
		// A certificate whose CN is a reserved owner label must not become that
		// principal: the label owns transcripts the OpenAI-compatible endpoint
		// made. Treated like a CN-less certificate (K-174).
		return "", false
	}
	if cn == "" {
		// A CN-less (SAN-only) certificate has no operator identity. Treating
		// it as ("", true) would merge every such certificate into one shared
		// anonymous principal and hand owner checks an authenticated-but-empty
		// ID. Fall through to the fail-closed / session branches instead.
		return "", false
	}
	return cn, true
}

// ---- Session lookup injection -----------------------------------------------

// SessionLookupFn is an optional function injected into a Resolver that
// resolves a request to an (operatorID, role) pair using a server-side
// session store.  It is called only when no verified mTLS client certificate
// is present and the resolver is not in loopback-trusted mode.
//
// Implementations must be safe for concurrent use from multiple goroutines.
// If the session is absent, expired, or invalid, ok must be false.
//
// The netid package does not import any session or user-store package.
// Decoupling is maintained by injection: the caller (the auth edge layer)
// supplies the concrete lookup and netid remains a leaf package.
type SessionLookupFn func(r *http.Request) (operatorID string, role Role, ok bool)

// ---- Identity resolution middleware ----------------------------------------

// Resolver resolves an Identity for each request and stores it in the context.
//
// Resolution rules (per ADR-0005) — single decision point, evaluated in order:
//  1. Verified mTLS client cert (r.TLS.VerifiedChains non-empty) →
//     Identity{OperatorID: CN, Role: mapped-or-wildcard-or-RoleNone, Authenticated: true,
//     AuthMethod: AuthMethodCert}.
//     Cert beats session deliberately: a machine presenting a cert must never
//     be silently downgraded to a stray browser session.
//  2. No cert + sessionLookupFn != nil + !loopbackTrusted → call the fn; on
//     ok: Identity{OperatorID: operatorID, Role: role, Authenticated: true,
//     AuthMethod: AuthMethodSession}.
//  3. No cert + loopbackTrusted → today's cooperative bearer-token behavior
//     (unchanged): Identity{OperatorID: cooperativeLabel, Role: RoleAdmin,
//     Authenticated: false, AuthMethod: AuthMethodNone}.
//  4. Else (networked, no cert, no valid session) → fail-closed:
//     Identity{OperatorID: "", Role: RoleNone, Authenticated: false,
//     AuthMethod: AuthMethodNone}.
//     RoleNone (< RoleRead) ensures requireRole(RoleRead) rejects this
//     identity even if requireAuthOrRedirect is somehow bypassed (Phase 3g
//     defense-in-depth).  The primary gate is still requireAuthOrRedirect.
//
// The loopbackTrusted flag is a per-resolver trust decision made at
// construction time by the caller who knows which listener this resolver
// serves.  It is defense-in-depth at the HTTP layer: the resolver fails closed
// (certless+sessionless → RoleNone / Authenticated=false, Phase 3g) regardless
// of the TLS configuration.  After ADR-0005 Phase 3f the console networked
// listener uses VerifyClientCertIfGiven; the resolver's fail-closed behavior is
// the gate that prevents an unauthenticated certless request from gaining access.
//
// The session branch is guarded by !loopbackTrusted so the loopback regime
// is byte-for-byte unchanged regardless of whether a sessionLookupFn is set.
//
// The cooperativeLabel is the OperatorID extracted by callerLabelFn, which
// today comes from the daemon-level mintOperatorID.  It is supplied by the
// caller to avoid a circular import between netid and dispatch.
type Resolver struct {
	mapper          *RoleMapper
	callerLabelFn   func(*http.Request) string
	loopbackTrusted bool
	sessionLookupFn SessionLookupFn // nil means no session path (today's default)
}

// NewResolver constructs an identity Resolver without a session lookup
// function.  This is the existing constructor; all existing call sites
// continue to work unchanged.  The resolver behaves identically to before:
// cert path uses AuthMethodCert, loopback and unauthenticated paths use
// AuthMethodNone.
//
//   - mapper resolves CN→Role for authenticated (mTLS) requests.
//   - callerLabelFn extracts the cooperative OperatorID label from a request
//     for unauthenticated (loopback bearer) requests.  May be nil or return "".
//   - loopbackTrusted controls the no-cert fallback.  Set to true for the
//     loopback listener (certless → RoleAdmin, Authenticated=false, preserving
//     today's behavior).  Set to false for any non-loopback listener (certless
//     → RoleNone, Authenticated=false; fail-closed, Phase 3g).
func NewResolver(mapper *RoleMapper, callerLabelFn func(*http.Request) string, loopbackTrusted bool) *Resolver {
	return &Resolver{
		mapper:          mapper,
		callerLabelFn:   callerLabelFn,
		loopbackTrusted: loopbackTrusted,
	}
}

// NewResolverWithSession constructs an identity Resolver with an injected
// session lookup function for the password+session-cookie auth regime
// (ADR-0005 Phase 2).
//
// The sessionLookupFn is called only when:
//   - no verified mTLS client certificate is present, AND
//   - loopbackTrusted is false (the loopback path bypasses session lookup).
//
// If sessionLookupFn is nil this constructor is identical to NewResolver.
// Callers that do not yet have a session store should use NewResolver instead.
//
// Parameters are the same as NewResolver with the addition of sessionLookupFn.
func NewResolverWithSession(
	mapper *RoleMapper,
	callerLabelFn func(*http.Request) string,
	loopbackTrusted bool,
	sessionLookupFn SessionLookupFn,
) *Resolver {
	return &Resolver{
		mapper:          mapper,
		callerLabelFn:   callerLabelFn,
		loopbackTrusted: loopbackTrusted,
		sessionLookupFn: sessionLookupFn,
	}
}

// Resolve returns the Identity for r.
// Every returned Identity has Resolved=true; callers that need to distinguish
// "middleware ran" from "zero-value / no middleware" can check this field.
//
// Resolution order (ADR-0005 — single decision point):
//  1. Verified client cert → AuthMethodCert.  Cert beats session.
//  2. Valid session + !loopbackTrusted → AuthMethodSession.
//  3. loopbackTrusted, no credential → loopback cooperative bearer (AuthMethodNone).
//  4. Networked, no cert, no valid session → fail-closed (RoleNone, AuthMethodNone).
//     Phase 3g: RoleNone ensures requireRole(RoleRead) rejects this identity
//     even if requireAuthOrRedirect is somehow bypassed.
func (res *Resolver) Resolve(r *http.Request) Identity {
	// Step 1: verified mTLS client certificate — highest precedence.
	// A machine presenting a cert must never be silently downgraded to a
	// stray browser session, so cert is checked before session unconditionally.
	if cn, ok := CNFromRequest(r); ok {
		return Identity{
			OperatorID:    cn,
			Role:          res.mapper.Lookup(cn),
			Authenticated: true,
			Resolved:      true,
			AuthMethod:    AuthMethodCert,
		}
	}

	// No verified client certificate present.

	// Step 2: session cookie — only for non-loopback resolvers.
	// The !loopbackTrusted guard ensures the loopback path is byte-for-byte
	// unchanged regardless of whether a sessionLookupFn is injected.
	if res.sessionLookupFn != nil && !res.loopbackTrusted {
		if operatorID, role, ok := res.sessionLookupFn(r); ok {
			return Identity{
				OperatorID:    operatorID,
				Role:          role,
				Authenticated: true,
				Resolved:      true,
				AuthMethod:    AuthMethodSession,
			}
		}
		// Session lookup returned ok=false: fail-closed with RoleNone.
		// RoleNone.Allows(RoleRead) == false so requireRole(RoleRead) rejects
		// this identity even if requireAuthOrRedirect is somehow skipped (defense-
		// in-depth; ADR-0005 Phase 3g).  RoleRead is NOT used here because
		// RoleRead.Allows(RoleRead) == true, which would silently grant read
		// access to unauthenticated networked requests that bypass the edge.
		// We do NOT fall through to the loopback path because loopbackTrusted
		// is false; the loopback path is never reached in this branch.
		return Identity{
			OperatorID:    "",
			Role:          RoleNone,
			Authenticated: false,
			Resolved:      true,
			AuthMethod:    AuthMethodNone,
		}
	}

	// Step 3: loopback-trusted, no credential.
	// Preserves today's full-access loopback bearer behavior unchanged.
	if res.loopbackTrusted {
		label := ""
		if res.callerLabelFn != nil {
			label = res.callerLabelFn(r)
		}
		return Identity{
			OperatorID:    label,
			Role:          RoleAdmin,
			Authenticated: false,
			Resolved:      true,
			AuthMethod:    AuthMethodNone,
		}
	}

	// Step 4: networked listener, no cert, no session → fail-closed.
	// Never grant admin on a certless request to a non-loopback listener.
	//
	// RoleNone is used here (not RoleRead) so that requireRole(RoleRead)
	// REJECTS this identity even if requireAuthOrRedirect is somehow bypassed
	// (ADR-0005 Phase 3g defense-in-depth).  RoleNone.Allows(RoleRead) == false.
	return Identity{
		OperatorID:    "",
		Role:          RoleNone,
		Authenticated: false,
		Resolved:      true,
		AuthMethod:    AuthMethodNone,
	}
}

// Middleware returns an http.Handler that resolves an Identity for each
// request and stores it in the request context before calling next.
//
// Downstream handlers retrieve the identity with IdentityFrom(r.Context()).
// Nothing is gated or rejected — this middleware is purely additive.
// Enforcement (role checks, operator_id override) is the responsibility of
// later middleware and the dispatch facade (next PR per ADR-0005).
func (res *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := res.Resolve(r)
		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), id)))
	})
}
