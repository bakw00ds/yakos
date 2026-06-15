package consoleui

// setuphandler.go — GET /setup, POST /setup, GET /setup.js.
//
// ADR-0005 Phase 3c: one-time first-admin bootstrap via setup token.
//
// # Security model
//
// GET /setup:
//   - Auth-exempt + token-exempt (same as /login).
//   - Returns 404 (redirects to /login) when userStore.Count() > 0: setup is over.
//
// POST /setup:
//   - Per-IP rate limit (same limiter as /login) applied before any credential work.
//   - Accepts {token, username, password} JSON.
//   - Token is validated and consumed atomically via setupToken.ValidateAndConsume:
//     exactly one concurrent winner; losers get 403 even with the same token.
//   - On success: creates first admin via userStore.CreateFirstAdmin, which asserts
//     len(users)==0 atomically with the insert (second defence-in-depth layer).
//   - Audit-logs "first admin created" (username + remote_ip, never the token).
//   - Returns 409 when Count()>0 (setup already complete), 403 on bad token,
//     400 on invalid username/password.
//
// Token lifecycle:
//   - SetupToken is passed via consoleui.Config.SetupToken.
//   - When nil (loopback mode, or count>0 at startup), /setup always 404/409.

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bakw00ds/yakos/internal/setuptoken"
	"github.com/bakw00ds/yakos/internal/userstore"
)

// setupRequestDTO is the wire shape for POST /setup.
// Explicitly omits privileged fields (role is always admin for the first user).
type setupRequestDTO struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// setupResponseDTO is the 200 response for a successful POST /setup.
type setupResponseDTO struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// setupRateLimitRequests / setupRateLimitWindow match the /login rate-limit
// configuration.  The /setup endpoint triggers argon2 and must not be an
// unbounded surface even though it is single-use: a malicious operator could
// race it with many goroutines before the token is consumed, and argon2 is
// expensive even when every attempt 403s after ValidateAndConsume.
const (
	setupRateLimitRequests = loginRateLimitRequests
	setupRateLimitWindow   = loginRateLimitWindow
)

// setupHandlers holds the dependencies for the /setup HTTP handlers.
type setupHandlers struct {
	setupState  *setuptoken.State
	userStore   *userstore.Store
	rateLimiter *ipRateLimiter
}

func newSetupHandlers(st *setuptoken.State, uStore *userstore.Store) *setupHandlers {
	return &setupHandlers{
		setupState:  st,
		userStore:   uStore,
		rateLimiter: newIPRateLimiter(setupRateLimitRequests, setupRateLimitWindow),
	}
}

// handleSetupPage serves GET /setup — the first-admin creation page.
// Returns 404 (and redirects to /login) when users already exist.
func (sh *setupHandlers) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// If users already exist, setup is complete: 404 and redirect to login.
	if sh.userStore != nil && sh.userStore.Count() > 0 {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	// CSP mirrors login page: script-src 'self' (no inline scripts), form-action 'self'.
	w.Header().Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"connect-src 'self'",
		"font-src 'self'",
		"img-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"object-src 'none'",
		"form-action 'self'",
	}, "; "))
	// Referrer-Policy: no-referrer prevents the browser from leaking the
	// setup URL (which may still contain ?token= in some operator flows)
	// to third-party origins via the Referer request header.
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(setupHTML)
}

// handleSetup serves POST /setup — creates the first admin user.
//
// Defence-in-depth against TOCTOU (concurrent multi-admin amplification):
//
//  1. setupState.ValidateAndConsume holds setupToken.mu for the entire
//     check-and-zero: exactly one goroutine wins; losers get 403.
//  2. userStore.CreateFirstAdmin asserts len(users)==0 under userStore.mu
//     atomically with the insert: even if two callers somehow both passed
//     ValidateAndConsume (which is impossible), only one create succeeds.
func (sh *setupHandlers) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ip := clientIP(r)

	// Per-IP rate limit — applied before any credential work (argon2 is expensive).
	if !sh.rateLimiter.Allow(ip) {
		slog.Warn("consoleui: setup rate limit exceeded", "remote_ip", ip)
		writeAuthJSON(w, http.StatusTooManyRequests, `{"error":"too many requests"}`)
		return
	}

	// When no setup token state is wired (loopback or no token generated),
	// setup is disabled.
	if sh.setupState == nil {
		writeAuthJSON(w, http.StatusForbidden, `{"error":"setup not available"}`)
		return
	}

	// Quick count check before expensive decoding (not a security gate —
	// CreateFirstAdmin re-checks atomically below).
	if sh.userStore == nil || sh.userStore.Count() > 0 {
		slog.Warn("consoleui: POST /setup rejected: setup already complete",
			"remote_ip", ip)
		writeAuthJSON(w, http.StatusConflict, `{"error":"setup already complete"}`)
		return
	}

	// Decode DTO — requireJSONForMutations has already checked Content-Type.
	var dto setupRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"invalid request body"}`)
		return
	}

	// Validate username and password length BEFORE consuming the token.
	//
	// Security requirement: any input that would cause CreateFirstAdmin to fail
	// with a validation error MUST be rejected here, before the one-time token
	// is consumed.  If we consumed first and then discovered a bad username, the
	// token would be burned with zero admins created — a remote attacker could
	// POST malformed-username requests to repeatedly lock out setup.
	//
	// userstore.ValidateUsername is the single source of truth for username
	// rules (same function called by CreateFirstAdmin → validateUsername inside
	// the store).  Using the exported wrapper here prevents the two layers from
	// drifting.
	if err := userstore.ValidateUsername(dto.Username); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"`+jsonEscapeString(err.Error())+`"}`)
		return
	}
	if len(dto.Password) < userstore.MinPasswordLen {
		msg := `{"error":"password too short: minimum ` + intToString(userstore.MinPasswordLen) + ` characters"}`
		writeAuthJSON(w, http.StatusBadRequest, msg)
		return
	}

	// Atomically validate AND consume the token under a single lock.
	// This is the primary TOCTOU defence: exactly one concurrent caller wins.
	// Losers (wrong token, expired token, already-consumed token) all get 403.
	if dto.Token == "" || !sh.setupState.ValidateAndConsume(dto.Token) {
		slog.Warn("consoleui: POST /setup: invalid or expired setup token",
			"remote_ip", ip)
		writeAuthJSON(w, http.StatusForbidden, `{"error":"invalid or expired setup token"}`)
		return
	}

	// Create the first admin user.  CreateFirstAdmin asserts len(users)==0
	// under the userStore mutex atomically with the insert (second defence layer).
	if err := sh.userStore.CreateFirstAdmin(dto.Username, dto.Password); err != nil {
		slog.Error("consoleui: POST /setup: create first admin failed",
			"remote_ip", ip,
			"username", dto.Username,
			"err", err,
		)
		// ErrNotFirstUser → 409 (setup already complete, even though we passed the
		// quick Count() check above — this means a race completed between the check
		// and the lock).
		if errors.Is(err, userstore.ErrNotFirstUser) {
			writeAuthJSON(w, http.StatusConflict, `{"error":"setup already complete"}`)
			return
		}
		// Validation errors (invalid username, password too short) → 400.
		errMsg := err.Error()
		if containsValidationKeyword(errMsg) {
			writeAuthJSON(w, http.StatusBadRequest, `{"error":"`+jsonEscapeString(errMsg)+`"}`)
			return
		}
		writeAuthJSON(w, http.StatusInternalServerError, `{"error":"internal error"}`)
		return
	}

	// Audit log: username + remote_ip ONLY. Never log the token.
	slog.Info("consoleui: first admin created via setup token",
		"username", dto.Username,
		"remote_ip", ip,
	)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	resp := setupResponseDTO{OK: true, Message: "admin account created; please sign in"}
	_ = json.NewEncoder(w).Encode(resp)
}

// handleSetupJS serves GET /setup.js — the setup form JS.
// Token-exempt (like login.js).
func (s *Server) handleSetupJS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(setupJS)
}

// ---- helpers ----------------------------------------------------------------

// containsValidationKeyword returns true when errMsg looks like a validation
// error from userstore.Create (username invalid, password too short, duplicate).
// Used to decide between 400 and 500.
func containsValidationKeyword(errMsg string) bool {
	for _, kw := range []string{
		"invalid", "too short", "reserved", "already exists", "empty",
	} {
		if strings.Contains(errMsg, kw) {
			return true
		}
	}
	return false
}

// intToString converts an int to its decimal string representation without
// importing strconv (it's used only for the error message).
func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}

// jsonEscapeString escapes a string for safe embedding in a JSON value.
// Only handles the characters that can appear in userstore error messages
// (no control characters, just quotes and backslashes).
func jsonEscapeString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
