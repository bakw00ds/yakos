package consoleui

// sessionauth.go — builds the netid.SessionLookupFn that connects the
// authsession and userstore packages to the netid.Resolver for the
// networked-human auth regime (ADR-0005 Phase 3a).
//
// Why here (not a separate authglue package):
//
//	consoleui already imports netid and is the only caller of this glue.
//	authsession and userstore are leaf packages; neither imports consoleui,
//	so there is no import cycle.  A dedicated authglue package would add a
//	layer with a single function and a single caller — not worth the indirection.
//
// The returned function is safe for concurrent use: both stores are
// concurrency-safe and the function holds no mutable state of its own.

import (
	"net/http"

	"github.com/bakw00ds/yakos/internal/authsession"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/userstore"
)

// buildSessionLookupFn returns a netid.SessionLookupFn that resolves an
// incoming *http.Request to an (operatorID, role, ok) triple using the
// provided authsession and userstore stores.
//
// Resolution steps per ADR-0005 §Resolution precedence and the Phase-3
// NOTE in authsession about mandatory epoch validation:
//
//  1. Read the session cookie (cookieNameSession).  No cookie → (_, _, false).
//  2. authStore.Lookup(cookieValue) — not found or expired → false.
//  3. userStore.Get(sess.Username) — user not found → false.
//     Disabled users → false (defense-in-depth; Disable also revokes sessions
//     via RevokeAllForUser, but we check again in case of a race or a future
//     persistent-store path).
//  4. authStore.ValidateEpoch(sess, pu.SessionEpoch) — stale epoch (role
//     changed, password reset, or account disabled since session was issued)
//     → revoke the session and return false.
//  5. Success → (sess.Username, pu.Role, true).
//
// Security: OperatorID is always taken from the server-side session/user
// store — never from a request header or query parameter.  Any spoofed
// ?operatorId= or X-Operator-Id header is silently ignored.
func buildSessionLookupFn(authStore *authsession.Store, userStore *userstore.Store) netid.SessionLookupFn {
	return func(r *http.Request) (operatorID string, role netid.Role, ok bool) {
		// Step 1: read the session cookie.
		// The cookie name "yakos_session" is the ADR-0005-locked constant defined
		// in authsession.cookieNameSession (unexported).  We use the string literal
		// here to avoid adding an exported accessor to authsession; if the name
		// ever changes, authsession.SessionCookie / ClearSessionCookie and this
		// constant must all move together (they are the only three sites).
		cookie, err := r.Cookie("yakos_session")
		if err != nil {
			// http.ErrNoCookie or any parse error — not authenticated.
			return "", 0, false
		}
		cookieValue := cookie.Value
		if cookieValue == "" {
			return "", 0, false
		}

		// Step 2: look up the session in the store.
		// Lookup returns a copy; it also removes expired sessions and slides
		// the idle window on the stored entry (Lookup doc: "LastSeen is updated
		// on the stored entry").
		sess, found := authStore.Lookup(cookieValue)
		if !found {
			return "", 0, false
		}

		// Step 3: look up the user.
		pu, found := userStore.Get(sess.Username)
		if !found {
			// User was deleted after the session was issued.
			return "", 0, false
		}
		if pu.Disabled {
			// Defense-in-depth: Disable() also calls RevokeAllForUser, but the
			// session might still be present in a future persistent-store path or
			// in a narrow race window.  Fail closed here regardless.
			return "", 0, false
		}

		// Step 4: epoch check (mandatory per authsession Phase-3 NOTE).
		// A session whose SessionEpoch lags behind the user store's current epoch
		// was issued before a role change, password reset, or account disable.
		// Revoke it immediately so it cannot be replayed.
		if !authStore.ValidateEpoch(sess, pu.SessionEpoch) {
			authStore.Revoke(sess.ID)
			return "", 0, false
		}

		// Step 5: authenticated.
		// OperatorID is the username from the server-side store — never from any
		// client-supplied header or query parameter (LOW-1 requirement from the
		// security review, referenced in netid.SessionLookupFn doc).
		return sess.Username, pu.Role, true
	}
}
