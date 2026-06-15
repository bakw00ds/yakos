package consoleui

// users_handler.go — Admin user-management API (ADR-0005 §D6, Phase 5).
//
// # Endpoints
//
//	GET  /api/users                — list all users (RoleAdmin, no password hash)
//	POST /api/users                — create user {username,password,role}
//	POST /api/users/role           — set role {username,role}
//	POST /api/users/reset-password — admin password reset {username,newPassword}
//	POST /api/users/disable        — disable user {username}
//	POST /api/users/enable         — enable user {username}
//	POST /api/users/delete         — delete user {username}
//	POST /api/account/password     — self-service password change (any authenticated user)
//	GET  /api/account              — whoami (any authenticated user)
//
// # Auth
//
// All /api/users/* endpoints require RoleAdmin.  Routes are mounted via
// requireRoleFunc(RoleAdmin, …) in registerRoutes; role enforcement is at the
// middleware level only — never re-checked in handler bodies.
//
// /api/account/password and /api/account are available to ANY authenticated
// user (RoleRead minimum).
//
// # CSRF
//
// Session-authenticated mutations (POST) go through requireCSRFForSession in
// the middleware stack wired in server.go.  No additional CSRF check is needed
// inside handler bodies.
//
// # Audit
//
// Every state-mutating call writes a slog entry with actor (operatorID),
// target (username), and action.  Passwords are NEVER logged.
//
// # Self-protection (atomic last-admin guard)
//
// SetRoleGuarded, DisableGuarded, and DeleteGuarded in userstore hold the
// store mutex across the last-admin check AND the mutation.  This closes the
// TOCTOU race that would exist if the check (AdminCount) and the mutation were
// separate locking steps.  Handlers map ErrLastAdmin → 409; ErrUserNotFound → 404.
//
// # Idempotency
//
// GET endpoints are safe. POST endpoints:
// - POST /api/users        — not idempotent (second create → 409 duplicate).
//   Username is the natural idempotency key; GET first if needed.
// - POST /api/users/role   — idempotent (setting same role twice is a no-op).
// - POST /api/users/reset-password — idempotent (second reset just re-hashes).
// - POST /api/users/disable / enable — idempotent at the store level.
// - POST /api/users/delete — second delete → 404.
// - POST /api/account/password — not idempotent; each call bumps session epoch.

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/bakw00ds/yakos/internal/authsession"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/userstore"
)

// ---- request DTOs -----------------------------------------------------------
//
// All request bodies are bound through a DTO that explicitly omits privileged
// fields. Privileged fields (session epoch, disabled state, password hash) are
// never bound from caller input — they are set by the store's own logic.
//
// Every decoder uses DisallowUnknownFields() so that client-side typos in field
// names are caught immediately (consistent with other handlers in this package).

// createUserDTO is the wire shape for POST /api/users.
type createUserDTO struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// usernameDTO is the wire shape for operations that take only a username.
// Used by /api/users/disable, /api/users/enable, /api/users/delete.
type usernameDTO struct {
	Username string `json:"username"`
}

// setRoleDTO is the wire shape for POST /api/users/role.
type setRoleDTO struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

// resetPasswordDTO is the wire shape for POST /api/users/reset-password.
type resetPasswordDTO struct {
	Username    string `json:"username"`
	NewPassword string `json:"newPassword"`
}

// changePasswordDTO is the wire shape for POST /api/account/password
// (self-service, non-admin).
type changePasswordDTO struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

// ---- response DTOs ----------------------------------------------------------

// usersListResponseDTO is the response for GET /api/users.
// Each element is a userstore.PublicUser (no password hash).
type usersListResponseDTO struct {
	Users []userstore.PublicUser `json:"users"`
}

// userActionResponseDTO is the generic success response for mutation endpoints.
type userActionResponseDTO struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// accountResponseDTO is the response for GET /api/account.
type accountResponseDTO struct {
	OperatorID string `json:"operatorId"`
	Role       string `json:"role"`
	AuthMethod string `json:"authMethod"`
}

// ---- usersHandlers ----------------------------------------------------------

// usersHandlers holds the dependencies for the /api/users and /api/account
// endpoints.  Constructed by newUsersHandlers and wired in registerRoutes.
type usersHandlers struct {
	userStore *userstore.Store
	authStore *authsession.Store
}

func newUsersHandlers(uStore *userstore.Store, aStore *authsession.Store) *usersHandlers {
	return &usersHandlers{
		userStore: uStore,
		authStore: aStore,
	}
}

// decodeJSON decodes r.Body into v, disallowing unknown fields.
// Returns an error message suitable for a 400 response, or "" on success.
func decodeJSON(r *http.Request, v interface{}) string {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return "invalid request body"
	}
	return ""
}

// ---- GET /api/users + POST /api/users (root dispatcher) --------------------

// handleUsersRoot dispatches GET/HEAD /api/users → handleListUsers and
// POST /api/users → handleCreateUser.  A single mux pattern is used to avoid
// duplicate-registration panics in Go's ServeMux.
func (uh *usersHandlers) handleUsersRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		uh.handleListUsers(w, r)
	case http.MethodPost:
		uh.handleCreateUser(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleListUsers handles GET/HEAD /api/users.
// Returns all users as PublicUser (no password hashes).
// Requires RoleAdmin (enforced by route middleware in registerRoutes).
func (uh *usersHandlers) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users := uh.userStore.List()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(usersListResponseDTO{Users: users})
}

// ---- POST /api/users --------------------------------------------------------

// handleCreateUser handles POST /api/users.
// Creates a new user with the given username, password, and role.
// Requires RoleAdmin (enforced by route middleware).
func (uh *usersHandlers) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	actor := netid.IdentityFrom(r.Context()).OperatorID

	var dto createUserDTO
	if msg := decodeJSON(r, &dto); msg != "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"`+jsonEscapeString(msg)+`"}`)
		return
	}

	// Validate username.
	if err := userstore.ValidateUsername(dto.Username); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"`+jsonEscapeString(err.Error())+`"}`)
		return
	}

	// Validate password length.
	if len(dto.Password) < userstore.MinPasswordLen {
		writeAuthJSON(w, http.StatusBadRequest,
			`{"error":"password too short: minimum `+intToString(userstore.MinPasswordLen)+` characters"}`)
		return
	}

	// Validate role.
	role := netid.ParseRole(dto.Role)
	if role.String() != dto.Role {
		writeAuthJSON(w, http.StatusBadRequest,
			`{"error":"invalid role: must be one of read, dispatch, flows-run, admin"}`)
		return
	}

	if err := uh.userStore.Create(dto.Username, dto.Password, role); err != nil {
		if errors.Is(err, userstore.ErrDuplicate) {
			writeAuthJSON(w, http.StatusConflict, `{"error":"username already exists"}`)
			return
		}
		slog.Error("consoleui: create user: store error",
			"actor", actor, "target", dto.Username, "err", err)
		writeAuthJSON(w, http.StatusInternalServerError, `{"error":"internal error"}`)
		return
	}

	slog.Info("consoleui: audit: user created",
		"actor", actor,
		"target", dto.Username,
		"action", "create_user",
		"role", dto.Role,
	)

	writeJSON(w, http.StatusCreated, userActionResponseDTO{OK: true, Message: "user created"})
}

// ---- POST /api/users/role ---------------------------------------------------

// handleSetRole handles POST /api/users/role.
// Sets the role for a user. Uses SetRoleGuarded to atomically enforce the
// last-admin guard (count check + mutation under a single lock).
// Bumps sessionEpoch, then invalidates live sessions.
// Requires RoleAdmin (enforced by route middleware).
func (uh *usersHandlers) handleSetRole(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	actor := netid.IdentityFrom(r.Context()).OperatorID

	var dto setRoleDTO
	if msg := decodeJSON(r, &dto); msg != "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"`+jsonEscapeString(msg)+`"}`)
		return
	}

	if dto.Username == "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"username required"}`)
		return
	}

	newRole := netid.ParseRole(dto.Role)
	if newRole.String() != dto.Role {
		writeAuthJSON(w, http.StatusBadRequest,
			`{"error":"invalid role: must be one of read, dispatch, flows-run, admin"}`)
		return
	}

	// SetRoleGuarded: atomic last-admin check + mutation under store mutex.
	if err := uh.userStore.SetRoleGuarded(dto.Username, newRole); err != nil {
		if errors.Is(err, userstore.ErrLastAdmin) {
			writeAuthJSON(w, http.StatusConflict,
				`{"error":"cannot demote the last admin; promote another user to admin first"}`)
			return
		}
		if errors.Is(err, userstore.ErrUserNotFound) {
			writeAuthJSON(w, http.StatusNotFound, `{"error":"user not found"}`)
			return
		}
		slog.Error("consoleui: set role: store error",
			"actor", actor, "target", dto.Username, "err", err)
		writeAuthJSON(w, http.StatusInternalServerError, `{"error":"internal error"}`)
		return
	}

	// Invalidate live sessions (epoch bumped by SetRoleGuarded).
	if uh.authStore != nil {
		pu, found := uh.userStore.Get(dto.Username)
		if found {
			uh.authStore.InvalidateByEpoch(dto.Username, pu.SessionEpoch)
		}
	}

	slog.Info("consoleui: audit: user role changed",
		"actor", actor,
		"target", dto.Username,
		"action", "set_role",
		"new_role", dto.Role,
	)

	writeJSON(w, http.StatusOK, userActionResponseDTO{OK: true, Message: "role updated"})
}

// ---- POST /api/users/reset-password -----------------------------------------

// handleResetPassword handles POST /api/users/reset-password.
// Sets a temporary password and marks the account as requiring a password change.
// Requires RoleAdmin (enforced by route middleware).
func (uh *usersHandlers) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	actor := netid.IdentityFrom(r.Context()).OperatorID

	var dto resetPasswordDTO
	if msg := decodeJSON(r, &dto); msg != "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"`+jsonEscapeString(msg)+`"}`)
		return
	}

	if dto.Username == "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"username required"}`)
		return
	}

	if dto.NewPassword == "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"newPassword required"}`)
		return
	}

	if len(dto.NewPassword) < userstore.MinPasswordLen {
		writeAuthJSON(w, http.StatusBadRequest,
			`{"error":"password too short: minimum `+intToString(userstore.MinPasswordLen)+` characters"}`)
		return
	}

	if err := uh.userStore.ResetPassword(dto.Username, dto.NewPassword); err != nil {
		if errors.Is(err, userstore.ErrUserNotFound) {
			writeAuthJSON(w, http.StatusNotFound, `{"error":"user not found"}`)
			return
		}
		slog.Error("consoleui: reset password: store error",
			"actor", actor, "target", dto.Username, "err", err)
		writeAuthJSON(w, http.StatusInternalServerError, `{"error":"internal error"}`)
		return
	}

	// Invalidate live sessions (epoch bumped by ResetPassword).
	if uh.authStore != nil {
		pu, found := uh.userStore.Get(dto.Username)
		if found {
			uh.authStore.InvalidateByEpoch(dto.Username, pu.SessionEpoch)
		}
	}

	// Audit: log actor, target, action — NEVER the password.
	slog.Info("consoleui: audit: password reset by admin",
		"actor", actor,
		"target", dto.Username,
		"action", "reset_password",
	)

	writeJSON(w, http.StatusOK, userActionResponseDTO{OK: true, Message: "password reset; user must change on next login"})
}

// ---- POST /api/users/disable ------------------------------------------------

// handleDisableUser handles POST /api/users/disable.
// Uses DisableGuarded to atomically enforce the last-admin guard.
// Bumps epoch and revokes all live sessions.
// Requires RoleAdmin (enforced by route middleware).
func (uh *usersHandlers) handleDisableUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	actor := netid.IdentityFrom(r.Context()).OperatorID

	var dto usernameDTO
	if msg := decodeJSON(r, &dto); msg != "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"`+jsonEscapeString(msg)+`"}`)
		return
	}

	if dto.Username == "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"username required"}`)
		return
	}

	// DisableGuarded: atomic last-admin check + mutation under store mutex.
	if err := uh.userStore.DisableGuarded(dto.Username); err != nil {
		if errors.Is(err, userstore.ErrLastAdmin) {
			writeAuthJSON(w, http.StatusConflict,
				`{"error":"cannot disable the last admin; promote another user to admin first"}`)
			return
		}
		if errors.Is(err, userstore.ErrUserNotFound) {
			writeAuthJSON(w, http.StatusNotFound, `{"error":"user not found"}`)
			return
		}
		slog.Error("consoleui: disable user: store error",
			"actor", actor, "target", dto.Username, "err", err)
		writeAuthJSON(w, http.StatusInternalServerError, `{"error":"internal error"}`)
		return
	}

	// Purge all live sessions for the disabled user.
	if uh.authStore != nil {
		uh.authStore.RevokeAllForUser(dto.Username)
	}

	slog.Info("consoleui: audit: user disabled",
		"actor", actor,
		"target", dto.Username,
		"action", "disable_user",
	)

	writeJSON(w, http.StatusOK, userActionResponseDTO{OK: true, Message: "user disabled"})
}

// ---- POST /api/users/enable -------------------------------------------------

// handleEnableUser handles POST /api/users/enable.
// Re-enables a disabled user.
// Requires RoleAdmin (enforced by route middleware).
func (uh *usersHandlers) handleEnableUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	actor := netid.IdentityFrom(r.Context()).OperatorID

	var dto usernameDTO
	if msg := decodeJSON(r, &dto); msg != "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"`+jsonEscapeString(msg)+`"}`)
		return
	}

	if dto.Username == "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"username required"}`)
		return
	}

	if err := uh.userStore.Enable(dto.Username); err != nil {
		if errors.Is(err, userstore.ErrUserNotFound) {
			writeAuthJSON(w, http.StatusNotFound, `{"error":"user not found"}`)
			return
		}
		slog.Error("consoleui: enable user: store error",
			"actor", actor, "target", dto.Username, "err", err)
		writeAuthJSON(w, http.StatusInternalServerError, `{"error":"internal error"}`)
		return
	}

	slog.Info("consoleui: audit: user enabled",
		"actor", actor,
		"target", dto.Username,
		"action", "enable_user",
	)

	writeJSON(w, http.StatusOK, userActionResponseDTO{OK: true, Message: "user enabled"})
}

// ---- POST /api/users/delete -------------------------------------------------

// handleDeleteUser handles POST /api/users/delete.
// Uses DeleteGuarded to atomically enforce the last-admin guard.
// Purges live sessions before deletion.
// Requires RoleAdmin (enforced by route middleware).
func (uh *usersHandlers) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	actor := netid.IdentityFrom(r.Context()).OperatorID

	var dto usernameDTO
	if msg := decodeJSON(r, &dto); msg != "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"`+jsonEscapeString(msg)+`"}`)
		return
	}

	if dto.Username == "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"username required"}`)
		return
	}

	// Revoke live sessions before deleting (deletion makes future store lookups
	// return not-found, but sessions already in-flight would otherwise survive
	// until their idle/absolute timeout).
	if uh.authStore != nil {
		uh.authStore.RevokeAllForUser(dto.Username)
	}

	// DeleteGuarded: atomic last-admin check + deletion under store mutex.
	if err := uh.userStore.DeleteGuarded(dto.Username); err != nil {
		if errors.Is(err, userstore.ErrLastAdmin) {
			writeAuthJSON(w, http.StatusConflict,
				`{"error":"cannot delete the last admin; promote another user to admin first"}`)
			return
		}
		if errors.Is(err, userstore.ErrUserNotFound) {
			writeAuthJSON(w, http.StatusNotFound, `{"error":"user not found"}`)
			return
		}
		slog.Error("consoleui: delete user: store error",
			"actor", actor, "target", dto.Username, "err", err)
		writeAuthJSON(w, http.StatusInternalServerError, `{"error":"internal error"}`)
		return
	}

	slog.Info("consoleui: audit: user deleted",
		"actor", actor,
		"target", dto.Username,
		"action", "delete_user",
	)

	writeJSON(w, http.StatusOK, userActionResponseDTO{OK: true, Message: "user deleted"})
}

// ---- POST /api/account/password (self-service) ------------------------------

// handleChangePassword handles POST /api/account/password.
// Allows any authenticated user to change their OWN password.
// Requires RoleRead (any authenticated user); enforced by route middleware.
// The requesting user's identity comes from the resolved netid.Identity.
func (uh *usersHandlers) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := netid.IdentityFrom(r.Context())
	// On the networked path the identity is always resolved by the middleware
	// stack.  Defense-in-depth: refuse if the identity is not authenticated.
	if !id.Authenticated {
		writeAuthJSON(w, http.StatusUnauthorized, `{"error":"authentication required"}`)
		return
	}
	username := id.OperatorID

	var dto changePasswordDTO
	if msg := decodeJSON(r, &dto); msg != "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"`+jsonEscapeString(msg)+`"}`)
		return
	}

	if dto.OldPassword == "" || dto.NewPassword == "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"oldPassword and newPassword required"}`)
		return
	}

	if len(dto.NewPassword) < userstore.MinPasswordLen {
		writeAuthJSON(w, http.StatusBadRequest,
			`{"error":"newPassword too short: minimum `+intToString(userstore.MinPasswordLen)+` characters"}`)
		return
	}

	// Verify the old password. Verify handles all failure modes (wrong, disabled,
	// locked) and returns ErrAuthFailed for all of them.
	if _, err := uh.userStore.Verify(username, dto.OldPassword); err != nil {
		// Audit server-side; generic response to client (no distinguishing info).
		slog.Warn("consoleui: account/password: old password verification failed",
			"username", username,
			"reason", userstore.AuthFailureReason(err),
		)
		writeAuthJSON(w, http.StatusUnauthorized, `{"error":"invalid current password"}`)
		return
	}

	// SetPassword hashes new password, clears passwordResetReq, bumps epoch.
	if err := uh.userStore.SetPassword(username, dto.NewPassword); err != nil {
		slog.Error("consoleui: account/password: set password error",
			"username", username, "err", err)
		writeAuthJSON(w, http.StatusInternalServerError, `{"error":"internal error"}`)
		return
	}

	// Per ADR-0005, epoch bump invalidates ALL sessions including the current;
	// the user is expected to re-authenticate after a password change.
	if uh.authStore != nil {
		pu, found := uh.userStore.Get(username)
		if found {
			uh.authStore.InvalidateByEpoch(username, pu.SessionEpoch)
		}
	}

	// Audit: actor + action, no passwords.
	slog.Info("consoleui: audit: password changed (self-service)",
		"actor", username,
		"target", username,
		"action", "change_own_password",
	)

	writeJSON(w, http.StatusOK, userActionResponseDTO{OK: true, Message: "password changed"})
}

// ---- GET /api/account (whoami) ----------------------------------------------

// handleAccount handles GET /api/account.
// Returns the authenticated user's operatorId, role, and authMethod.
// Available to any authenticated user (RoleRead).
func (uh *usersHandlers) handleAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := netid.IdentityFrom(r.Context())
	if !id.Authenticated {
		writeAuthJSON(w, http.StatusUnauthorized, `{"error":"authentication required"}`)
		return
	}

	resp := accountResponseDTO{
		OperatorID: id.OperatorID,
		Role:       id.Role.String(),
		AuthMethod: id.AuthMethod.String(),
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// ---- helpers ----------------------------------------------------------------

// writeJSON writes a JSON response with the given status code and value.
// Distinct from writeAuthJSON: accepts any JSON-marshallable value.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
