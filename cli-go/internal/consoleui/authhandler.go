package consoleui

// authhandler.go — POST /login, POST /logout, CSRF middleware, and the
// networked no-credential edge redirect/401.
//
// ADR-0005 Phase 3b.
//
// # Security model
//
// Login (POST /login):
//   - Networked-only (guarded by Config.NetworkedMode).
//   - Per-IP rate limit (fixed-window, in-memory) applied BEFORE the userstore
//     lockout check so that distributed credential-stuffing is blunted at the
//     network layer.  Limits are package consts (loginRateLimitRequests /
//     loginRateLimitWindow).
//   - Session fixation defense: if a valid session cookie is already present,
//     Revoke it before minting a new session.
//   - Secure flag derived from Config.NetworkedMode (true on the networked TLS
//     bind; false on loopback).  This is the enforcement described in LOW-2 of
//     the security review.
//   - Generic 401 body for ALL failure modes (wrong, unknown, disabled, locked).
//     Structured reason is audit-logged server-side only.
//
// Logout (POST /logout):
//   - Reads the session cookie; Revoke(id); sets clear cookies.  Idempotent.
//
// CSRF middleware (requireCSRFForSession):
//   - Applied to all state-mutating methods (POST/PUT/PATCH/DELETE) on the
//     networked bind.
//   - Exemptions by AuthMethod (resolved from context by the resolver middleware):
//       - AuthMethodCert → EXEMPT (client certs are not browser-auto-attached).
//       - AuthMethodNone (loopback bearer / unauthenticated) → EXEMPT/unchanged.
//       - AuthMethodSession → REQUIRE X-CSRF-Token matching session's CSRFToken.
//   - Missing or mismatched CSRF token for a session-auth mutation → 403.
//
// Edge redirect / 401:
//   - Unauthenticated identity on the networked bind.
//   - API/XHR paths (Accept: application/json or prefix /api/ /flows/api/ /v1/)
//     → 401 JSON.
//   - Top-level navigation (other paths) → 302 /login.
//   - Loopback behavior unchanged.

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/authsession"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/userstore"
)

// zeroUsersLoginTarget returns the redirect target for unauthenticated
// navigations: /setup when no users exist yet, /login otherwise.
// uStore may be nil (conservative: returns /login).
func zeroUsersLoginTarget(uStore *userstore.Store) string {
	if uStore != nil && uStore.Count() == 0 {
		return "/setup"
	}
	return "/login"
}

// ---- Rate limiter -----------------------------------------------------------

// loginRateLimitRequests is the maximum number of POST /login attempts allowed
// per IP address within loginRateLimitWindow before a 429 is returned.
const loginRateLimitRequests = 20

// loginRateLimitWindow is the sliding fixed-window period for the per-IP login
// rate limit.
const loginRateLimitWindow = 5 * time.Minute

// ipRateLimiter is a concurrency-safe per-IP fixed-window rate limiter.
// Each IP gets a bucket that resets when the window expires.
// The map is bounded by the number of distinct IPs seen within the window;
// expired entries are cleaned up on each Allow() call (lazy GC).
type ipRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rateBucket
	limit   int
	window  time.Duration
	nowFn   func() time.Time // injectable for tests
}

type rateBucket struct {
	count     int
	windowEnd time.Time
}

func newIPRateLimiter(limit int, window time.Duration) *ipRateLimiter {
	return &ipRateLimiter{
		buckets: make(map[string]*rateBucket),
		limit:   limit,
		window:  window,
		nowFn:   time.Now,
	}
}

// Allow returns true if the request from ip is within the rate limit.
// Increments the counter for ip; resets the window when expired.
// Lazy GC: sweeps all expired buckets on every call (bounded by the number of
// distinct IPs within the window — typically small for a private console).
func (l *ipRateLimiter) Allow(ip string) bool {
	now := l.nowFn()

	l.mu.Lock()
	defer l.mu.Unlock()

	// Lazy GC: remove expired buckets so the map doesn't grow without bound.
	for k, b := range l.buckets {
		if now.After(b.windowEnd) {
			delete(l.buckets, k)
		}
	}

	b, ok := l.buckets[ip]
	if !ok || now.After(b.windowEnd) {
		l.buckets[ip] = &rateBucket{count: 1, windowEnd: now.Add(l.window)}
		return true
	}

	b.count++
	if b.count > l.limit {
		return false
	}
	return true
}

// ---- Login / logout request DTOs --------------------------------------------

// loginRequestDTO is the wire-shape for POST /login.
// Explicitly omits any privileged fields (role, admin flags, sessionEpoch).
type loginRequestDTO struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginResponseDTO is the 200 response for a successful POST /login.
type loginResponseDTO struct {
	OK                    bool `json:"ok"`
	PasswordResetRequired bool `json:"passwordResetRequired"`
}

// ---- authHandlers -----------------------------------------------------------

// authHandlers holds the dependencies for the auth HTTP handlers.
// Constructed by newAuthHandlers and wired into registerRoutes.
type authHandlers struct {
	authStore   *authsession.Store
	userStore   *userstore.Store
	rateLimiter *ipRateLimiter
	secure      bool // true when the server is on the networked TLS bind
}

func newAuthHandlers(authStore *authsession.Store, uStore *userstore.Store, secure bool) *authHandlers {
	return &authHandlers{
		authStore:   authStore,
		userStore:   uStore,
		rateLimiter: newIPRateLimiter(loginRateLimitRequests, loginRateLimitWindow),
		secure:      secure,
	}
}

// clientIP extracts the real client IP from r.RemoteAddr.
// Does not trust X-Forwarded-For or similar headers — this console is not
// deployed behind a reverse proxy in the v1 model.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr without port (shouldn't happen with net/http, but be safe).
		return r.RemoteAddr
	}
	return host
}

// handleLogin implements POST /login.
// Only wired on the networked bind (enforced in registerAuthRoutes).
func (ah *authHandlers) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Per-IP rate limit — applied before any credential work.
	ip := clientIP(r)
	if !ah.rateLimiter.Allow(ip) {
		slog.Warn("consoleui: login rate limit exceeded", "remote_ip", ip)
		writeAuthJSON(w, http.StatusTooManyRequests, `{"error":"too many requests"}`)
		return
	}

	// Decode the DTO — requireJSONForMutations has already enforced Content-Type.
	var dto loginRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"invalid request body"}`)
		return
	}
	if dto.Username == "" || dto.Password == "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"username and password required"}`)
		return
	}

	// Authenticate. On any error, respond with a GENERIC 401.
	// Server-side: log the specific reason for audit.
	pu, err := ah.userStore.Verify(dto.Username, dto.Password)
	if err != nil {
		reason := userstore.AuthFailureReason(err)
		slog.Warn("consoleui: login failed",
			"remote_ip", ip,
			"username", dto.Username,
			"reason", reason,
			"auth_method", "session",
		)
		writeAuthJSON(w, http.StatusUnauthorized, `{"error":"invalid username or password"}`)
		return
	}

	// Session fixation defense: revoke any existing session cookie.
	if cookie, cookieErr := r.Cookie(sessionCookieName); cookieErr == nil && cookie.Value != "" {
		ah.authStore.Revoke(cookie.Value)
	}

	// Mint a fresh session.
	sess, err := ah.authStore.Create(pu.Username, pu.Role, pu.SessionEpoch)
	if err != nil {
		slog.Error("consoleui: login: create session", "username", pu.Username, "err", err)
		writeAuthJSON(w, http.StatusInternalServerError, `{"error":"internal error"}`)
		return
	}

	// Set both cookies: session (HttpOnly) and CSRF (non-HttpOnly for JS read).
	http.SetCookie(w, ah.authStore.SessionCookie(sess.ID, ah.secure))
	http.SetCookie(w, ah.authStore.CSRFCookie(sess.CSRFToken, ah.secure))

	slog.Info("consoleui: login success",
		"username", pu.Username,
		"remote_ip", ip,
		"auth_method", "session",
	)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	resp := loginResponseDTO{OK: true, PasswordResetRequired: pu.PasswordResetReq}
	_ = json.NewEncoder(w).Encode(resp)
}

// handleLogout implements POST /logout.
// Idempotent: no session present → still 200 + clear cookies.
func (ah *authHandlers) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ip := clientIP(r)
	username := ""

	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		// Look up the session for audit logging; Revoke regardless.
		if sess, found := ah.authStore.Lookup(cookie.Value); found {
			username = sess.Username
		}
		ah.authStore.Revoke(cookie.Value)
	}

	http.SetCookie(w, ah.authStore.ClearSessionCookie(ah.secure))
	http.SetCookie(w, ah.authStore.ClearCSRFCookie(ah.secure))

	slog.Info("consoleui: logout",
		"username", username,
		"remote_ip", ip,
		"auth_method", "session",
	)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}` + "\n"))
}

// sessionCookieName is the authoritative cookie name for the session ID.
// This package-level const is used by authhandler.go so that we have a
// single definition within consoleui rather than repeating the string literal.
//
// NOTE: the value must equal authsession.cookieNameSession (unexported).
// A cross-package test in authhandler_test.go asserts this invariant using
// the authsession.CookieNameSession test export.
const sessionCookieName = "yakos_session"

// ---- CSRF middleware --------------------------------------------------------

// requireCSRFForSession is middleware that enforces the double-submit CSRF
// check for session-authenticated mutations on the networked bind.
//
// Application:
//   - Only active on the networked bind (cfg.NetworkedMode=true); the loopback
//     path does not use sessions and is unchanged.
//   - Only fires for state-mutating methods (POST/PUT/PATCH/DELETE).
//   - Checks AuthMethod from the resolved identity on the request context:
//   - AuthMethodSession → require X-CSRF-Token matching session's CSRFToken.
//   - AuthMethodCert    → EXEMPT (not browser-auto-attached; no CSRF vector).
//   - AuthMethodNone    → EXEMPT (unauthenticated; handled by edge auth).
//   - Missing or mismatched token → 403.
//   - Correct token → pass through to next.
//
// This middleware composes with (does not replace) the existing layers:
//   - SameSite=Strict on the session cookie (first layer).
//   - requireJSONForMutations Content-Type gate (second layer — CORS preflight).
//   - This double-submit header check (third layer).
func requireCSRFForSession(authStore *authsession.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only check mutations.
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		// POST /login and POST /setup are unauthenticated auth endpoints;
		// exempt from CSRF.  (requireAuthOrRedirect lets them through before
		// this middleware, but the resolver may still set an unauthenticated
		// identity, so we guard here too for defense-in-depth.)
		if r.URL.Path == "/login" || r.URL.Path == "/setup" {
			next.ServeHTTP(w, r)
			return
		}

		id := netid.IdentityFrom(r.Context())

		// Only enforce when the identity has been resolved AND the auth method
		// is session-based.  Cert and loopback/unauthenticated are exempt.
		if !id.Resolved || id.AuthMethod != netid.AuthMethodSession {
			next.ServeHTTP(w, r)
			return
		}

		// Read the session cookie to look up the stored CSRF token.
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			// No session cookie on a session-auth identity is anomalous;
			// fail closed.
			slog.Warn("consoleui: CSRF check: session auth but no session cookie",
				"method", r.Method, "path", r.URL.Path)
			writeAuthJSON(w, http.StatusForbidden, `{"error":"invalid CSRF token"}`)
			return
		}

		sess, found := authStore.Lookup(cookie.Value)
		if !found {
			// Session expired or revoked between identity resolution and CSRF check.
			writeAuthJSON(w, http.StatusForbidden, `{"error":"invalid CSRF token"}`)
			return
		}

		presented := r.Header.Get("X-CSRF-Token")
		if !authStore.ValidateCSRF(sess, presented) {
			slog.Warn("consoleui: CSRF token mismatch",
				"method", r.Method,
				"path", r.URL.Path,
				"username", sess.Username,
				"auth_method", "session",
			)
			writeAuthJSON(w, http.StatusForbidden, `{"error":"invalid CSRF token"}`)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// ---- Edge: unauthenticated request handling ---------------------------------

// requireAuthOrRedirect wraps next with networked-edge auth enforcement:
//   - Authenticated identities pass through unchanged.
//   - Static assets and the login/setup pages pass through (exempt via
//     isStaticAsset — those pages must be reachable unauthenticated).
//   - API/XHR paths (Accept: application/json or /api/ /flows/api/ /v1/ prefixes)
//     → 401 JSON.
//   - Top-level navigations → 302 redirect to /setup (when zero users) or
//     /login (after first admin is created).
//
// Only wired on the networked bind (NetworkedMode=true).
// Loopback path is unchanged.
//
// uStore is used to determine the redirect target: /setup when Count()==0,
// /login otherwise.  May be nil (conservative: redirects to /login).
func requireAuthOrRedirect(uStore *userstore.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /login, /login.js, /login.css, /setup, /setup.js and other static
		// assets are always accessible without authentication.  /logout is
		// intentionally NOT exempt: it requires an authenticated session cookie
		// (the session lookup in handleLogout handles the no-session case gracefully).
		if isStaticAsset(r) {
			next.ServeHTTP(w, r)
			return
		}

		id := netid.IdentityFrom(r.Context())

		// If identity is not yet resolved (zero-value — e.g. in tests that call
		// srv.Handler() directly without resolver middleware), pass through so
		// loopback and unit-test callers are not broken.
		if !id.Resolved {
			next.ServeHTTP(w, r)
			return
		}

		// Authenticated: pass through regardless of method.
		if id.Authenticated {
			next.ServeHTTP(w, r)
			return
		}

		// Unauthenticated on the networked bind.
		// Distinguish between API/XHR requests and top-level navigations.
		if isAPIRequest(r) {
			writeAuthJSON(w, http.StatusUnauthorized, `{"error":"authentication required"}`)
			return
		}

		// Top-level navigation: redirect to /setup (zero users) or /login.
		http.Redirect(w, r, zeroUsersLoginTarget(uStore), http.StatusFound)
	})
}

// isLoginPath reports whether the request is for /login (any method),
// /login.js (GET/HEAD), /login.css (GET/HEAD), /setup (any method), or
// /setup.js (GET/HEAD).  These paths are always accessible without
// authentication:
//   - GET /login: serves the login form page.
//   - POST /login: the authentication endpoint itself.
//   - GET /login.js: the login form script.
//   - GET /login.css: the login form stylesheet.
//   - GET /setup: serves the first-admin setup page.
//   - POST /setup: the first-admin creation endpoint.
//   - GET /setup.js: the setup form script.
//
// /logout is intentionally excluded: it is a session-auth endpoint and the
// handler deals gracefully with a missing session (idempotent 200 + clear cookies).
func isLoginPath(r *http.Request) bool {
	if r.URL.Path == "/login" || r.URL.Path == "/setup" {
		return true // any method (GET page, POST auth)
	}
	if (r.URL.Path == "/login.js" || r.URL.Path == "/login.css" || r.URL.Path == "/setup.js") &&
		(r.Method == http.MethodGet || r.Method == http.MethodHead) {
		return true
	}
	return false
}

// isAPIRequest reports whether r looks like an API/XHR request that should
// receive a 401 JSON response rather than an HTML redirect.
//
// Heuristics (in order):
//  1. Accept header contains "application/json".
//  2. Path is under /api/, /flows/api/, /v1/ (API paths per registerRoutes).
func isAPIRequest(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") {
		return true
	}
	p := r.URL.Path
	return strings.HasPrefix(p, "/api/") ||
		strings.HasPrefix(p, "/flows/api/") ||
		strings.HasPrefix(p, "/v1/")
}

// ---- helpers ----------------------------------------------------------------

// writeAuthJSON writes a JSON body with the given status code.
// Sets Content-Type and Cache-Control.
func writeAuthJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body + "\n"))
}
