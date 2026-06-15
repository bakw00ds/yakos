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
//   - Accepts {token, username, password} JSON.
//   - Validates: (a) Count()==0; (b) token present, unexpired, correct
//     (constant-time); (c) valid username + password >= MinPasswordLen.
//   - On success: creates first admin, CONSUMES the setup token (zeroes memory
//     + deletes marker file), audit-logs "first admin created" (username +
//     remote_ip, never the token).
//   - Returns 409 when Count()>0 (setup already complete), 403 on bad token,
//     400 on invalid username/password.
//
// Token lifecycle:
//   - SetupToken is passed via consoleui.Config.SetupToken.
//   - When nil (loopback mode, or count>0 at startup), /setup always 404/409.

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bakw00ds/yakos/internal/netid"
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

// setupHandlers holds the dependencies for the /setup HTTP handlers.
type setupHandlers struct {
	setupState *setuptoken.State
	userStore  *userstore.Store
}

func newSetupHandlers(st *setuptoken.State, uStore *userstore.Store) *setupHandlers {
	return &setupHandlers{setupState: st, userStore: uStore}
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
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(setupHTML)
}

// handleSetup serves POST /setup — creates the first admin user.
func (sh *setupHandlers) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ip := clientIP(r)

	// Zero-users guard: setup is only valid when no users exist.
	if sh.userStore == nil || sh.userStore.Count() > 0 {
		slog.Warn("consoleui: POST /setup rejected: setup already complete",
			"remote_ip", ip)
		writeAuthJSON(w, http.StatusConflict, `{"error":"setup already complete"}`)
		return
	}

	// When no setup token state is wired (loopback or no token generated),
	// setup is disabled.
	if sh.setupState == nil {
		writeAuthJSON(w, http.StatusForbidden, `{"error":"setup not available"}`)
		return
	}

	// Decode DTO — requireJSONForMutations has already checked Content-Type.
	var dto setupRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"invalid request body"}`)
		return
	}

	// Validate token first (constant-time, expiry, single-use guard).
	if dto.Token == "" || !sh.setupState.Validate(dto.Token) {
		slog.Warn("consoleui: POST /setup: invalid or expired setup token",
			"remote_ip", ip)
		writeAuthJSON(w, http.StatusForbidden, `{"error":"invalid or expired setup token"}`)
		return
	}

	// Validate username.
	if dto.Username == "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"username required"}`)
		return
	}

	// Validate password length (before hashing).
	if len(dto.Password) < userstore.MinPasswordLen {
		msg := `{"error":"password too short: minimum ` + intToString(userstore.MinPasswordLen) + ` characters"}`
		writeAuthJSON(w, http.StatusBadRequest, msg)
		return
	}

	// Create the first admin user.
	if err := sh.userStore.Create(dto.Username, dto.Password, netid.RoleAdmin); err != nil {
		// Check if it's a duplicate-username error (shouldn't happen since
		// Count()==0 at the top, but defend against a very narrow race).
		slog.Error("consoleui: POST /setup: create user failed",
			"remote_ip", ip,
			"username", dto.Username,
			"err", err,
		)
		// Distinguish validation errors (400) from other errors (500).
		// userstore.Create returns validation errors with messages like
		// "userstore: create: username ... is invalid" or "password too short".
		errMsg := err.Error()
		if containsValidationKeyword(errMsg) {
			writeAuthJSON(w, http.StatusBadRequest, `{"error":"`+jsonEscapeString(errMsg)+`"}`)
			return
		}
		writeAuthJSON(w, http.StatusInternalServerError, `{"error":"internal error"}`)
		return
	}

	// Consume the token: single-use guarantee.
	sh.setupState.Consume()

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
