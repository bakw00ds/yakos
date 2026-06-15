package consoleui

// users_handler.go — Admin user-management API (ADR-0005 §D6, Phase 5).
//
// # Endpoints
//
//	GET  /api/users                — list all users (RoleAdmin, no password hash)
//	POST /api/users                — create user {username,password,role}
//	POST /api/users/role           — set role {username,role}
//	POST /api/users/reset-password — admin password reset {username,newPassword?}
//	POST /api/users/disable        — disable user {username}
//	POST /api/users/enable         — enable user {username}
//	POST /api/users/delete         — delete user {username}
//	POST /api/account/password     — self-service password change (any authenticated user)
//	GET  /api/account              — whoami (any authenticated user)
//
// # Auth
//
// All /api/users/* endpoints require RoleAdmin.  Routes are mounted via
// requireRoleFunc(RoleAdmin, …) in registerRoutes; no re-check is needed inside
// the handler body (enforced at middleware level only).
//
// /api/account/password and /api/account are available to ANY authenticated
// user (not admin-only).  They use requireRoleFunc(RoleRead, …) at the route
// level.
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
// # Self-protection
//
// Any operation that would leave zero non-disabled admins is rejected with 409.
// The check uses userstore.AdminCount and is applied BEFORE mutating state.
//
// # Idempotency
//
// GET endpoints are safe. POST endpoints carry explicit note on idempotency:
// - POST /api/users        — not idempotent (second create → 409 duplicate).
//   Callers wanting idempotency should GET first; no Idempotency-Key header
//   is defined because usernames are the natural idempotency key.
// - POST /api/users/role   — idempotent (setting same role twice is a no-op).
// - POST /api/users/reset-password — idempotent (second reset just re-hashes).
// - POST /api/users/disable / enable — idempotent (double-disable is fine).
// - POST /api/users/delete — idempotent (second delete returns 404).
// - POST /api/account/password — not idempotent; each call bumps session epoch.

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bakw00ds/yakos/internal/authsession"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/userstore"
)

// ---- request DTOs -----------------------------------------------------------
//
// All request bodies are bound through a DTO that explicitly omits privileged
// fields. Privileged fields (session epoch, disabled state, password hash) are
// never bound from caller input — they are set by the store's own logic.

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
// NewPassword is optional: when empty, only the passwordResetReq flag is set
// (the store requires a password, so we generate an error if both are missing).
// In practice callers always provide a temporary password.
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

// ---- GET /api/users + POST /api/users (root dispatcher) --------------------

// handleUsersRoot dispatches GET /api/users → handleListUsers and
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
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	actor := netid.IdentityFrom(r.Context()).OperatorID

	var dto createUserDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"invalid request body"}`)
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
		if strings.Contains(err.Error(), "already exists") {
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
// Sets the role for a user and bumps sessionEpoch to invalidate live sessions.
// Requires RoleAdmin (enforced by route middleware).
func (uh *usersHandlers) handleSetRole(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	actor := netid.IdentityFrom(r.Context()).OperatorID

	var dto setRoleDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"invalid request body"}`)
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

	// Self-protection: refuse if this would remove the last admin.
	// Check: if the target is currently admin and the new role is not admin,
	// and there is only one non-disabled admin, refuse.
	if newRole != netid.RoleAdmin {
		pu, found := uh.userStore.Get(dto.Username)
		if found && pu.Role == netid.RoleAdmin && !pu.Disabled {
			if uh.userStore.AdminCount() <= 1 {
				writeAuthJSON(w, http.StatusConflict,
					`{"error":"cannot demote the last admin; promote another user to admin first"}`)
				return
			}
		}
	}

	if err := uh.userStore.SetRole(dto.Username, newRole); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeAuthJSON(w, http.StatusNotFound, `{"error":"user not found"}`)
			return
		}
		slog.Error("consoleui: set role: store error",
			"actor", actor, "target", dto.Username, "err", err)
		writeAuthJSON(w, http.StatusInternalServerError, `{"error":"internal error"}`)
		return
	}

	// Bump epoch → invalidate live sessions for the target.
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
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"invalid request body"}`)
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
		if strings.Contains(err.Error(), "not found") {
			writeAuthJSON(w, http.StatusNotFound, `{"error":"user not found"}`)
			return
		}
		if strings.Contains(err.Error(), "too short") {
			writeAuthJSON(w, http.StatusBadRequest, `{"error":"`+jsonEscapeString(err.Error())+`"}`)
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
// Disables a user, bumps epoch, and revokes all live sessions.
// Requires RoleAdmin (enforced by route middleware).
func (uh *usersHandlers) handleDisableUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	actor := netid.IdentityFrom(r.Context()).OperatorID

	var dto usernameDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"invalid request body"}`)
		return
	}

	if dto.Username == "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"username required"}`)
		return
	}

	// Self-protection: refuse if this would leave zero active admins.
	pu, found := uh.userStore.Get(dto.Username)
	if found && pu.Role == netid.RoleAdmin && !pu.Disabled {
		if uh.userStore.AdminCount() <= 1 {
			writeAuthJSON(w, http.StatusConflict,
				`{"error":"cannot disable the last admin; promote another user to admin first"}`)
			return
		}
	}

	if err := uh.userStore.Disable(dto.Username); err != nil {
		if strings.Contains(err.Error(), "not found") {
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
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"invalid request body"}`)
		return
	}

	if dto.Username == "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"username required"}`)
		return
	}

	if err := uh.userStore.Enable(dto.Username); err != nil {
		if strings.Contains(err.Error(), "not found") {
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
// Deletes a user and revokes all live sessions.
// Requires RoleAdmin (enforced by route middleware).
func (uh *usersHandlers) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	actor := netid.IdentityFrom(r.Context()).OperatorID

	var dto usernameDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"invalid request body"}`)
		return
	}

	if dto.Username == "" {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"username required"}`)
		return
	}

	// Self-protection: refuse if this would leave zero active admins.
	pu, found := uh.userStore.Get(dto.Username)
	if found && pu.Role == netid.RoleAdmin && !pu.Disabled {
		if uh.userStore.AdminCount() <= 1 {
			writeAuthJSON(w, http.StatusConflict,
				`{"error":"cannot delete the last admin; promote another user to admin first"}`)
			return
		}
	}

	// Revoke live sessions before deleting (deletion makes future lookups fail,
	// but sessions already in-flight would otherwise survive until expiry).
	if uh.authStore != nil {
		uh.authStore.RevokeAllForUser(dto.Username)
	}

	if err := uh.userStore.Delete(dto.Username); err != nil {
		if strings.Contains(err.Error(), "not found") {
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
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeAuthJSON(w, http.StatusBadRequest, `{"error":"invalid request body"}`)
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
		if strings.Contains(err.Error(), "too short") {
			writeAuthJSON(w, http.StatusBadRequest, `{"error":"`+jsonEscapeString(err.Error())+`"}`)
			return
		}
		slog.Error("consoleui: account/password: set password error",
			"username", username, "err", err)
		writeAuthJSON(w, http.StatusInternalServerError, `{"error":"internal error"}`)
		return
	}

	// Invalidate all sessions except the current one would require knowing the
	// current session ID.  Per ADR-0005, epoch bump invalidates ALL sessions
	// including the current; the user is expected to re-authenticate.
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
