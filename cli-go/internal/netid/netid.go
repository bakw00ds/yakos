// Package netid defines the networked-operator identity model for the yakOS
// console.
//
// # Overview
//
// yakOS supports two bind regimes (per ADR-0004):
//
//   - Loopback: bearer-token cooperative labeling.  The Identity is
//     Authenticated=false; Role is admin (preserving today's full access).
//   - Non-loopback (future): mTLS client certificates.  The Identity is
//     Authenticated=true; OperatorID is the cert CN; Role is resolved from
//     the CN→Role mapping file.
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
// mtls/roles.json).  The file is optional; a missing file is tolerated
// (all authenticated certs default to RoleRead).
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
// Role constants below.  Unknown role strings fall back to RoleRead.
// Reloading on every request is safe (file is small; OS page cache keeps I/O
// cheap) and avoids the need for signal-based reload machinery.
//
// # Identity context
//
// ResolveMiddleware inserts an Identity into each request's context.  Use
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
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// ---- Role -------------------------------------------------------------------

// Role represents an operator's privilege level on the yakOS console.
// The roles are ordered; higher-indexed constants represent greater privilege.
type Role int

const (
	// RoleRead permits read-only access (Overview, Cost, Perf, Kanban,
	// shared transcripts).
	RoleRead Role = iota

	// RoleDispatch permits opening chat panes and running agent dispatches.
	RoleDispatch

	// RoleFlowsRun permits triggering and resuming workflows.
	RoleFlowsRun

	// RoleAdmin permits cert/token rotation, bind config, and future
	// client enrollment.
	RoleAdmin
)

// String returns the canonical role string used in config files and logs.
func (r Role) String() string {
	switch r {
	case RoleRead:
		return "read"
	case RoleDispatch:
		return "dispatch"
	case RoleFlowsRun:
		return "flows-run"
	case RoleAdmin:
		return "admin"
	default:
		return "read"
	}
}

// ParseRole converts a role string to a Role constant.
// Unknown strings return RoleRead (least privilege).
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

// Allows reports whether r has at least the privilege level of needed.
// Example: RoleAdmin.Allows(RoleDispatch) == true.
func (r Role) Allows(needed Role) bool {
	return r >= needed
}

// ---- Identity ---------------------------------------------------------------

// Identity represents a resolved operator identity attached to a request.
//
// Authenticated is true only when the identity was established by a verified
// mTLS client certificate.  When false (loopback bearer path), the OperatorID
// is a cooperative label, not a cryptographic guarantee.
//
// This distinction is load-bearing for the dual-regime audit trail described in
// ADR-0004 §Consequences C3: code that reads dispatch logs must not treat
// loopback entries as cryptographically authenticated.
type Identity struct {
	// OperatorID is the operator identifier.  For authenticated identities
	// this is the client certificate CN.  For loopback/bearer identities
	// this is the cooperative label supplied by the caller.
	OperatorID string

	// Role is the resolved privilege level for this identity.
	Role Role

	// Authenticated is true when OperatorID is bound to a verified client
	// certificate.  False for loopback bearer sessions.
	Authenticated bool
}

// ---- Context key ------------------------------------------------------------

// contextKey is the unexported type used as the context key for Identity.
// Using a private type prevents collisions with other packages' context keys.
type contextKey struct{}

// IdentityFrom retrieves the Identity stored in ctx by ResolveMiddleware.
// If no middleware has run, it returns a zero Identity (unauthenticated,
// empty OperatorID, RoleRead).
func IdentityFrom(ctx context.Context) Identity {
	if id, ok := ctx.Value(contextKey{}).(Identity); ok {
		return id
	}
	return Identity{}
}

// withIdentity returns a new context carrying id.
func withIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// ---- Role mapper ------------------------------------------------------------

// RoleMapper resolves a certificate CN to a Role using a JSON mapping file.
// A missing file is tolerated (all CNs default to RoleRead).
//
// The file is re-read on every call to Lookup; since it is small the OS
// page cache makes repeated reads cheap and no signal-based reload is needed.
// Concurrent reads are safe (no mutable shared state beyond the path string).
type RoleMapper struct {
	path string     // absolute path to roles.json
	mu   sync.Mutex // guards the file path; reads use their own local copy
}

// NewRoleMapper returns a RoleMapper that reads from
// <stateDir>/mtls/roles.json.
func NewRoleMapper(stateDir string) *RoleMapper {
	return &RoleMapper{
		path: filepath.Join(stateDir, "mtls", "roles.json"),
	}
}

// Lookup returns the Role for the given certificate CN.
// If the CN is not in the mapping, or the file is absent or unparseable,
// Lookup returns RoleRead (least privilege; fail-closed).
func (m *RoleMapper) Lookup(cn string) Role {
	m.mu.Lock()
	path := m.path
	m.mu.Unlock()

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		// Missing file is expected before the operator configures roles.
		return RoleRead
	}
	var mapping map[string]string
	if err := json.Unmarshal(data, &mapping); err != nil {
		// Malformed file → fail closed.
		return RoleRead
	}
	if roleStr, ok := mapping[cn]; ok {
		return ParseRole(roleStr)
	}
	return RoleRead
}

// ---- Client-cert CN extraction ---------------------------------------------

// CNFromRequest extracts the client certificate Common Name from the
// TLS connection state embedded in r, if present and verified.
//
// Returns ("", false) when:
//   - the connection has no TLS state (plain HTTP or test),
//   - no client certificate was presented, or
//   - the certificate chain is empty.
//
// Note: by the time a request reaches an HTTP handler over a TLS listener
// configured with tls.RequireAndVerifyClientCert, the client cert has already
// been cryptographically verified by the TLS stack.  This function only
// extracts the CN; it does not re-verify.
func CNFromRequest(r *http.Request) (cn string, ok bool) {
	return CNFromTLS(r.TLS)
}

// CNFromTLS extracts the client certificate CN from a TLS connection state.
// Returns ("", false) if cs is nil or contains no verified peer certificates.
func CNFromTLS(cs *tls.ConnectionState) (cn string, ok bool) {
	if cs == nil {
		return "", false
	}
	if len(cs.VerifiedChains) == 0 || len(cs.VerifiedChains[0]) == 0 {
		return "", false
	}
	return cs.VerifiedChains[0][0].Subject.CommonName, true
}

// ---- Identity resolution middleware ----------------------------------------

// Resolver resolves an Identity for each request and stores it in the context.
//
// Resolution rules (per ADR-0004):
//   - If the request has a verified TLS client cert (r.TLS.VerifiedChains
//     non-empty) → Identity{OperatorID: CN, Role: mapped-or-RoleRead,
//     Authenticated: true}.
//   - Otherwise (loopback bearer path, today's only path) →
//     Identity{OperatorID: cooperativeLabel, Role: RoleAdmin,
//     Authenticated: false}.
//
// The cooperativeLabel is the OperatorID extracted by the callerLabel function,
// which today comes from the daemon-level mintOperatorID.  It is supplied by
// the caller to avoid a circular import between netid and dispatch.
type Resolver struct {
	mapper        *RoleMapper
	callerLabelFn func(*http.Request) string
}

// NewResolver constructs an identity Resolver.
//
//   - mapper resolves CN→Role for authenticated (mTLS) requests.
//   - callerLabelFn extracts the cooperative OperatorID label from a request
//     for unauthenticated (loopback bearer) requests.  May return "".
func NewResolver(mapper *RoleMapper, callerLabelFn func(*http.Request) string) *Resolver {
	return &Resolver{
		mapper:        mapper,
		callerLabelFn: callerLabelFn,
	}
}

// Resolve returns the Identity for r.
func (res *Resolver) Resolve(r *http.Request) Identity {
	if cn, ok := CNFromRequest(r); ok {
		return Identity{
			OperatorID:    cn,
			Role:          res.mapper.Lookup(cn),
			Authenticated: true,
		}
	}
	// Loopback / bearer path: cooperative label, full admin access.
	label := ""
	if res.callerLabelFn != nil {
		label = res.callerLabelFn(r)
	}
	return Identity{
		OperatorID:    label,
		Role:          RoleAdmin,
		Authenticated: false,
	}
}

// Middleware returns an http.Handler that resolves an Identity for each
// request and stores it in the request context before calling next.
//
// Downstream handlers retrieve the identity with IdentityFrom(r.Context()).
// Nothing is gated or rejected — this middleware is purely additive.
// Enforcement (role checks, operator_id override) is the responsibility of
// later middleware and the dispatch facade (next PR per ADR-0004).
func (res *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := res.Resolve(r)
		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), id)))
	})
}
