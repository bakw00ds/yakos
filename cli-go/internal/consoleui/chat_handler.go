// Package consoleui — chat_handler.go
//
// Phase 3b chat endpoints:
//
//	GET  /api/chat/stream    — per-operator SSE stream (multiplexed by sessionID)
//	POST /api/chat/dispatch  — start a streaming dispatch (returns {sessionId})
//	POST /api/chat/cancel    — cancel an in-flight dispatch
//	GET  /api/chat/transcript — fetch persisted transcript for a conversationId
//
// Interactive-P1 additions:
//
//	POST /api/chat/send      — deliver a follow-up turn into a running interactive session
//
// # Auth
//
// All endpoints are mounted under the console edge auth
// (requireTokenForNonStatic + RequireLocalHost).  The edge layer enforces the
// Authorization: Bearer <token> header before these handlers are reached.
// No re-check inside handlers — that is middleware's job.
// Query-string tokens are explicitly rejected (handled by edge middleware
// which ignores ?token= queries).
//
// # Per-operator isolation
//
// The SSE stream is self-declared: the caller supplies operatorId in
// the query parameter.  Validation mirrors dispatch.ValidateIdentityField.
// Chunks for a given session are delivered ONLY to the session owner's
// connections (ChatHub.Route enforces this).
//
// The dispatch handler also accepts an operatorId in the request body.
// If the sessionId already exists in the hub and is owned by a different
// operatorId, the handler returns 403 — enforced in ChatHub.OpenSession.
//
// # Project pinning
//
// Project is NEVER accepted from the browser.  It is pinned server-side
// to s.cfg.cfg.WorkspaceRoot (via Service.RunStream's project resolution).
// The DispatchRequest body carries no project field.
//
// # Idempotency
//
// POST /api/chat/dispatch is NOT idempotent by nature (it starts a new LLM
// subprocess).  Callers that wish to retry must generate a new sessionId.
// Re-posting with the same sessionId owned by the same operator is an error
// (409 Conflict): a session may only have one active RunStream at a time.
//
// The endpoint declaration: no Idempotency-Key header required because there
// is no safe replay — retrying with the same sessionId would attempt to open
// a session that is already in flight (409) or already closed (creates a new
// goroutine, risks duplicate transcript entries unless the caller used a fresh
// sessionId).  Documentation: retries MUST use a new sessionId.
//
// POST /api/chat/cancel is idempotent (cancelling an already-cancelled or
// non-existent session is a no-op returning 200).
//
// POST /api/chat/send (interactive path):
//   - Non-idempotent (each call delivers a new turn).
//   - No Idempotency-Key declared: same reasoning as /api/chat/dispatch.
//   - Rate-limit class: inherits the project default.
//   - Owner-scoped: only the session owner may send turns; watchers (RoleRead)
//     may subscribe to the SSE stream but cannot drive the session.
package consoleui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/runtime"
	"github.com/bakw00ds/yakos/internal/worktreemgr"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// chatState is the in-process registry of active RunStream goroutines.
// It maps sessionID → cancel function so pane-close can kill the subprocess.
type chatState struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func newChatState() *chatState {
	return &chatState{cancels: make(map[string]context.CancelFunc)}
}

// add registers a cancel function for a session.  Returns false if the
// sessionID already has an in-flight dispatch (caller returns 409).
func (cs *chatState) add(sessionID string, cancel context.CancelFunc) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if _, exists := cs.cancels[sessionID]; exists {
		return false
	}
	cs.cancels[sessionID] = cancel
	return true
}

// remove deletes the cancel entry when the goroutine exits.
func (cs *chatState) remove(sessionID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.cancels, sessionID)
}

// cancel cancels the in-flight dispatch and removes it.
// No-op if the session is not active.
func (cs *chatState) cancel(sessionID string) {
	cs.mu.Lock()
	fn, ok := cs.cancels[sessionID]
	if ok {
		delete(cs.cancels, sessionID)
	}
	cs.mu.Unlock()
	if ok {
		fn()
	}
}

// chatHandlers holds the dependencies for all chat HTTP handlers.
// Constructed in Server.registerRoutes and kept on the Server struct.
type chatHandlers struct {
	hub           *ChatHub
	transcripts   *Transcripts
	state         *chatState
	svc           *dispatch.Service         // may be nil (console without dispatch)
	registry      *dispatch.SessionRegistry // fleet registry; wired by New() after construction
	bus           *wsbus.Bus                // event bus for fleet.* WS events; wired by New()
	workDir       string                    // used by NewTranscripts
	yakosRoot     string                    // used for up-front agent validation
	workspaceRoot string                    // used as project for up-front agent validation
	serverCtx     context.Context           // server-lifetime context; dispatch goroutines derive from this (NOT r.Context())
	// worktreeMgr is wired by New() when Config.WorktreeManager is non-nil.
	// Used to provision per-session worktrees when WorktreeMode is true in a dispatch request.
	worktreeMgr *worktreemgr.Manager
	// interactiveMgr manages persistent multi-turn claude sessions (Interactive-P1).
	// Nil when interactive mode is not enabled (feature gate).
	// Wired by New() from Config.InteractiveManager.
	interactiveMgr *interactive.Manager

	// interactiveSend is the narrower interface used by handleChatSend for the
	// Send operation.  Normally wired to interactiveMgr (same pointer); tests may
	// substitute a stub to inject specific error returns (e.g. ErrTurnInFlight)
	// without needing a real subprocess.
	// Nil check guards are NOT required here: handleChatSend guards on
	// interactiveMgr != nil first and only reaches interactiveSend when that
	// guard passes, so interactiveSend is always non-nil when used.
	interactiveSend interactiveSender
}

// interactiveSender is the minimal interface covering the Send method consumed
// by handleChatSend.  *interactive.Manager satisfies it; tests can stub it.
type interactiveSender interface {
	Send(conversationID, ownerOperatorID string, frame []byte) error
}

// newChatHandlers is called from registerChatRoutes.
// serverCtx must be a context cancelled when the Server shuts down; it is the
// parent for all dispatch goroutine contexts so they survive the 202 response.
func newChatHandlers(hub *ChatHub, transcripts *Transcripts, svc *dispatch.Service, serverCtx context.Context) *chatHandlers {
	return &chatHandlers{
		hub:         hub,
		transcripts: transcripts,
		state:       newChatState(),
		svc:         svc,
		serverCtx:   serverCtx,
	}
}

// ---- GET /api/chat/stream ---------------------------------------------------

// handleChatStream is the per-operator SSE endpoint.
//
// Query parameters:
//   - operatorId (required): the self-asserted operator identity.
//
// Response: text/event-stream, one SSE frame per SSEEvent.
// Frame format:
//
//	data: <JSON>\n\n
//
// Periodic comment-line heartbeats keep the connection alive through proxies.
// The connection is registered in the ChatHub and unregistered on disconnect.
func (ch *chatHandlers) handleChatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// C1 dual-regime operator_id:
	// Authenticated (mTLS cert): cert CN is authoritative; query param silently ignored.
	// Unauthenticated (loopback bearer): cooperative label from query param (unchanged).
	resolvedID := netid.IdentityFrom(r.Context())
	var operatorID string
	if resolvedID.Authenticated {
		// Cert CN wins; no need to validate the query param (it is ignored).
		operatorID = resolvedID.OperatorID
	} else {
		// Cooperative-label path: require operatorId from query param.
		operatorID = r.URL.Query().Get("operatorId")
		if operatorID == "" {
			http.Error(w, "operatorId is required", http.StatusBadRequest)
			return
		}
		if err := dispatch.ValidateIdentityField("operator_id", operatorID); err != nil {
			http.Error(w, "invalid operatorId", http.StatusBadRequest)
			return
		}
	}

	// Require http.Flusher.
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Register this connection in the hub.
	connID := newConnID()
	conn, err := ch.hub.register(connID, operatorID)
	if err != nil {
		if errors.Is(err, errTooManyConns) {
			http.Error(w, "too many SSE connections", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Unregister on exit — this closes conn.closed, signalling this goroutine.
	defer ch.hub.Unregister(connID)

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store") // L2: match other streaming handlers
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx/reverse proxy: disable buffering
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			// Client disconnected or server shutdown.
			return
		case <-conn.closed:
			// Hub was shut down or Unregister called (should not normally happen
			// while we hold the defer, but handle defensively).
			return
		case ev, ok := <-conn.ch:
			if !ok {
				return
			}
			if err := writeSSEEvent(w, ev); err != nil {
				slog.Debug("consoleui: chat SSE write error", "conn", connID, "err", err)
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			// SSE comment-line heartbeat (keep proxies alive).
			_, _ = fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// writeSSEEvent serialises ev as a single SSE data frame.
// Format: "data: <JSON>\n\n"
func writeSSEEvent(w http.ResponseWriter, ev SSEEvent) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}

// ---- POST /api/chat/dispatch ------------------------------------------------

// DispatchRequest is the JSON body for POST /api/chat/dispatch.
//
// Project is intentionally absent — it is pinned server-side.
// SystemPrompt is intentionally absent — the agent name resolves the persona.
// WorktreeMode, when true, provisions a git worktree for this session so the
// agent operates on an isolated copy of the workspace.  The server ignores this
// flag when the workspace is not a git repo (returns 409 with a clear message)
// or when no WorktreeManager is configured (returns 409).
// Default: false (agent operates in the real workspace unchanged).
//
// Effort, when non-empty, passes --effort <level> to the claude CLI so it
// adjusts reasoning intensity.  Valid values: low, medium, high, xhigh, max.
// Empty string (the default) omits the flag entirely.  Invalid values return
// 400.  This field is claude-only; for other runtimes it is accepted in the
// body but silently ignored at the adapter level.
type DispatchRequest struct {
	Runtime        string `json:"runtime"`
	Model          string `json:"model"`
	Agent          string `json:"agent"`
	Task           string `json:"task"`
	SessionID      string `json:"sessionId"`
	OperatorID     string `json:"operatorId"`
	ConversationID string `json:"conversationId"` // optional; empty → new conversation
	// Effort is the reasoning effort level for this dispatch.
	// Valid values: low, medium, high, xhigh, max.  Empty string (default)
	// omits the flag.  Invalid values → 400.  claude-only; other runtimes
	// accept the field without error but do not act on it.
	Effort string `json:"effort"`
	// WorktreeMode opts into diff-review mode for this dispatch.
	// When true, the agent runs inside a per-session git worktree rather than
	// the real workspace.  The caller can then use GET /api/files/diff,
	// POST /api/files/diff/accept, and POST /api/files/diff/reject to review
	// and selectively promote changes.
	// SERVER-SIDE ONLY: the resulting WorkDirOverride is set from the manager
	// path, never from the client body.
	WorktreeMode bool `json:"worktreeMode"`
	// Interactive opts into the persistent multi-turn session regime (Interactive-P1).
	// When true, the first turn starts a long-running claude process keyed by
	// conversationId; subsequent turns are delivered via POST /api/chat/send.
	// When false (default), the existing one-shot RunStream path is used — zero
	// regression on existing clients.
	//
	// Security note: Interactive mode does not accept project/cwd from the client.
	// The project is pinned server-side identically to the one-shot path.
	// --permission-mode bypassPermissions matches the existing chat dispatch posture.
	Interactive bool `json:"interactive"`
}

// DispatchResponse is the JSON body returned by POST /api/chat/dispatch.
type DispatchResponse struct {
	SessionID string `json:"sessionId"`
}

// handleChatDispatch handles POST /api/chat/dispatch.
//
// Validates all fields, opens the session in the hub, launches RunStream in a
// goroutine, and returns {sessionId} immediately (202 Accepted).
//
// Security properties:
//   - Project is pinned server-side (not accepted from body).
//   - Agent system-prompt is resolved server-side via the roster.
//   - A sessionId already owned by a DIFFERENT operatorId → 403.
//   - A sessionId with an active in-flight dispatch → 409.
//   - runtime must be in runtime.Known; model must pass ValidateTier after
//     ResolveAlias; agent name must resolve in the roster (generic 400 on
//     failure — no path/roster leak in error messages).
func (ch *chatHandlers) handleChatDispatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req DispatchRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// --- Validate runtime ---
	runtimeName := req.Runtime
	if runtimeName == "" {
		runtimeName = "claude"
	}
	if !isKnownRuntime(runtimeName) {
		http.Error(w, "invalid runtime", http.StatusBadRequest)
		return
	}

	// --- Validate model ---
	modelName := req.Model
	if modelName != "" {
		modelName = runtime.ResolveAlias(modelName)
		if !runtime.ValidateTier(modelName) {
			http.Error(w, "invalid model", http.StatusBadRequest)
			return
		}
	}

	// --- Validate effort ---
	// Empty string is valid (means "no override — omit the flag").
	// Non-empty must be one of: low, medium, high, xhigh, max.
	if err := dispatch.ValidateEffort(req.Effort); err != nil {
		http.Error(w, "invalid effort: must be one of low, medium, high, xhigh, max", http.StatusBadRequest)
		return
	}

	// --- Validate required string fields ---
	if strings.TrimSpace(req.Agent) == "" {
		http.Error(w, "agent is required", http.StatusBadRequest)
		return
	}

	// --- Validate agent resolves against the roster (or is a known runtime) ---
	// This mirrors the resolution that RunStream performs internally, so an
	// unknown agent name is rejected up front with a clear 400 instead of
	// silently hanging after the 202.
	// Bare runtime names (claude/codex/agy/gemini) are valid catch-alls and are
	// NOT rejected here — only names that resolve to nothing are rejected.
	//
	// When yakosRoot is empty the roster cannot be composed, so we only reject
	// if the agent is also not a known runtime.  This preserves backward
	// compatibility with test servers that omit yakosRoot.
	if err := dispatch.ValidateAgentName(req.Agent, ch.yakosRoot, ch.workspaceRoot); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("unknown agent %q; not in roster and not a known runtime", req.Agent),
		})
		return
	}

	if strings.TrimSpace(req.Task) == "" {
		http.Error(w, "task is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		http.Error(w, "sessionId is required", http.StatusBadRequest)
		return
	}
	// C1 dual-regime operator_id:
	// Authenticated (mTLS cert): cert CN is authoritative; body operatorId silently ignored.
	// Unauthenticated (loopback bearer): require and validate operatorId from body.
	capturedIdentity := netid.IdentityFrom(r.Context())
	var effectiveOperatorID string
	if capturedIdentity.Authenticated {
		// Cert CN wins; body operatorId is ignored.
		effectiveOperatorID = capturedIdentity.OperatorID
	} else {
		// Require and validate from body.
		if strings.TrimSpace(req.OperatorID) == "" {
			http.Error(w, "operatorId is required", http.StatusBadRequest)
			return
		}
		if err := dispatch.ValidateIdentityField("operator_id", req.OperatorID); err != nil {
			http.Error(w, "invalid operatorId", http.StatusBadRequest)
			return
		}
		effectiveOperatorID = req.OperatorID
	}

	// --- Validate identity fields (format; not auth) ---
	// These flow into subprocess argv; validate before any hub or service call.
	if err := dispatch.ValidateIdentityField("session_id", req.SessionID); err != nil {
		http.Error(w, "invalid sessionId", http.StatusBadRequest)
		return
	}
	if req.ConversationID != "" {
		if err := dispatch.ValidateIdentityField("conversation_id", req.ConversationID); err != nil {
			http.Error(w, "invalid conversationId", http.StatusBadRequest)
			return
		}
	}

	// --- Hub: open session (ownership check + global session cap) ---
	// Security priority: ownership 403 must fire even when svc is nil.
	// This enforces per-operator isolation: 403 if session already owned by
	// a different operator.  429 if the global session cap is reached (M3).
	if err := ch.hub.OpenSession(req.SessionID, effectiveOperatorID, false); err != nil {
		if errors.Is(err, errSessionOwnerConflict) {
			http.Error(w, "session owned by different operator", http.StatusForbidden)
			return
		}
		if errors.Is(err, errTooManySessions) {
			http.Error(w, "global session cap reached", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// --- Service availability check (AFTER OpenSession ownership check) ---
	// H1 fix: any early-return after a successful OpenSession must call
	// hub.CloseSession to avoid permanently squatting the sessionId.
	if ch.svc == nil {
		ch.hub.CloseSession(req.SessionID)
		http.Error(w, "dispatch service not configured", http.StatusServiceUnavailable)
		return
	}

	// --- Guard: one active dispatch per session at a time ---
	// B1 fix: derive ctx from the server-lifetime context, NOT r.Context().
	// r.Context() is cancelled when the 202 response returns, which would
	// immediately kill the dispatch goroutine.  The server-lifetime context is
	// only cancelled on Server.Shutdown, keeping the goroutine alive as intended.
	ctx, cancel := context.WithCancel(ch.serverCtx)
	if !ch.state.add(req.SessionID, cancel) {
		cancel()
		// Session already has an active dispatch — close the hub entry we just
		// opened so the sessionId is not permanently squatted (H1 fix).
		ch.hub.CloseSession(req.SessionID)
		// Session already has an active dispatch.
		http.Error(w, "session already has an active dispatch", http.StatusConflict)
		return
	}

	// Use a stable copy for the goroutine.
	// capturedOperatorID is the effective operator ID for this dispatch:
	// cert CN when authenticated; validated body operatorId otherwise.
	capturedOperatorID := effectiveOperatorID
	dispReq := req
	conversationID := dispReq.ConversationID
	if conversationID == "" {
		// Use the session ID as the conversation ID when none is provided.
		// This keeps all turns of one pane under one conversationId.
		conversationID = dispReq.SessionID
	}

	// Bind the conversationID to the session in the hub.
	// SetConversationID rejects binding when conversationID is already claimed
	// by a DIFFERENT operator's session (errConversationOwnerConflict → 403).
	// This closes the binding-poisoning vector: an attacker cannot steal a
	// victim's conversationID by dispatching with it as their own conversationId.
	if err := ch.hub.SetConversationID(dispReq.SessionID, conversationID); err != nil {
		ch.hub.CloseSession(dispReq.SessionID)
		if errors.Is(err, errConversationOwnerConflict) {
			http.Error(w, "conversationId already owned by another operator", http.StatusForbidden)
			return
		}
		slog.Error("consoleui: SetConversationID", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Append the user turn to the transcript.
	_ = ch.transcripts.Append(TranscriptEntry{
		SessionID:      dispReq.SessionID,
		ConversationID: conversationID,
		OperatorID:     capturedOperatorID,
		Role:           RoleUser,
		Text:           dispReq.Task,
		Runtime:        runtimeName,
		Model:          modelName,
	})

	// Capture the resolved identity for the goroutine.
	capturedIdentityForGoroutine := capturedIdentity

	// Fleet registry: record this session before launching the goroutine so
	// the /api/fleet snapshot is immediately accurate.  The entry is removed
	// in the goroutine's deferred cleanup (see defer fleetRemove below).
	//
	// Invariant: TaskPreview is truncated to <=120 chars inside registry.Add.
	// It must NEVER be published in WS fleet.* payloads — only the REST
	// snapshot exposes it, scoped to the caller.
	startedAt := time.Now().UTC()
	if ch.registry != nil {
		ch.registry.Add(dispatch.SessionEntry{
			SessionID:      dispReq.SessionID,
			ConversationID: conversationID,
			Agent:          dispReq.Agent,
			Runtime:        runtimeName,
			OperatorID:     capturedOperatorID,
			TaskPreview:    dispReq.Task, // truncated inside Add to <=120 runes
			StartedAt:      startedAt,
			Status:         dispatch.StatusRunning,
		})
	}

	// Publish fleet.started on the WS bus (metadata-only: no task text).
	// PublishMeta carries OwnerOperatorID + Shared=false in the server-side
	// EventMeta; the WS fan-out filter uses this to deliver the event only to
	// the owning operator's connection.  The meta is NEVER sent to clients.
	if ch.bus != nil {
		ch.bus.PublishMeta(wsbus.TopicFleetStarted, wsbus.FleetStartedPayload{
			SessionID: dispReq.SessionID,
			Agent:     dispReq.Agent,
			TS:        startedAt,
		}, wsbus.EventMeta{
			OwnerOperatorID: capturedOperatorID,
			Shared:          false, // new sessions start unshared
		})
	}

	// --- Worktree mode: provision a per-session git worktree BEFORE goroutine launch.
	// The path is resolved here (in the handler) so the goroutine captures a stable value.
	// WorktreeMode is an opt-in flag from the request body; the resulting
	// WorkDirOverride is ALWAYS set from the manager (server-derived), never from
	// the client body.
	//
	// Fallback to normal mode on error (ErrNotAGitRepo, ErrCapExceeded, nil manager,
	// empty workspaceRoot) — the dispatch proceeds without isolation so the operator
	// still gets a response rather than a hard failure.
	capturedWorktreeOverride := "" // server-derived; captured by the goroutine below
	if dispReq.WorktreeMode {
		if ch.worktreeMgr == nil {
			slog.Warn("consoleui: worktree mode requested but no manager configured; falling back to normal mode",
				"session", dispReq.SessionID)
		} else if ch.workspaceRoot == "" {
			slog.Warn("consoleui: worktree mode requested but workspaceRoot is empty; falling back",
				"session", dispReq.SessionID)
		} else {
			wt, wtErr := ch.worktreeMgr.Ensure(dispReq.SessionID, ch.workspaceRoot)
			if wtErr != nil {
				slog.Warn("consoleui: worktree mode unavailable; falling back to normal mode",
					"session", dispReq.SessionID, "err", wtErr)
			} else {
				capturedWorktreeOverride = wt
				slog.Info("consoleui: worktree mode active",
					"session", dispReq.SessionID, "worktree", wt)
			}
		}
	}
	// capturedMgr is non-nil only when we need to clean up the worktree on session close.
	capturedMgr := ch.worktreeMgr

	// Launch RunStream in a goroutine.  The goroutine owns the cancel function
	// and the hub session for its lifetime.
	//
	// Defer ordering (LIFO — last defer runs first):
	//   1. cancel() — runs first (last registered), kills the ctx so any
	//      in-flight RunStream call exits promptly.
	//   2. state.remove — frees the sessionId slot so a new dispatch can
	//      claim it without racing hub.CloseSession.
	//   3. hub.CloseSession — removes the hub ownership entry last so that
	//      chunks already in flight can still be routed before the entry
	//      disappears.  This order shrinks the re-dispatch race window.
	//   4. fleetRemove — cleanup fleet registry and publish fleet.finished.
	//      registered first; runs last (LIFO) so fleet state outlives the hub
	//      entry, preserving consistency during the short window between hub
	//      close and fleet remove.
	go func() {
		// exitCode captures the RunStream result for fleet.finished.
		// Default 0; overridden to -1 on non-cancel error path.
		exitCode := 0
		exitStatus := dispatch.StatusFinished

		// sharedAtFinish is updated to the session's final shared flag just before
		// RunStream returns (while the hub entry is still open).  The fleet.finished
		// defer reads it so the WS fan-out filter uses the correct visibility.
		sharedAtFinish := ch.hub.IsShared(dispReq.SessionID)

		defer func() {
			// 4. Fleet registry remove + fleet.finished WS event.
			// registered first; runs last (LIFO)
			//
			// hub.CloseSession (defer 3) has already run; sharedAtFinish was
			// captured while the hub entry was open (set immediately after
			// OpenSession and refreshed after RunStream returns below).
			if ch.registry != nil {
				ch.registry.Remove(dispReq.SessionID)
			}
			if ch.bus != nil {
				finishedAt := time.Now().UTC()
				finishedStatus := "finished"
				if exitStatus == dispatch.StatusFailed {
					finishedStatus = "failed"
				}
				ch.bus.PublishMeta(wsbus.TopicFleetFinished, wsbus.FleetFinishedPayload{
					SessionID: dispReq.SessionID,
					Agent:     dispReq.Agent,
					Status:    finishedStatus,
					ExitCode:  exitCode,
					TS:        finishedAt,
				}, wsbus.EventMeta{
					OwnerOperatorID: capturedOperatorID,
					Shared:          sharedAtFinish,
				})
			}
		}()
		defer cancel()                               // 1. stop any pending RunStream
		defer ch.state.remove(dispReq.SessionID)     // 2. release slot
		defer ch.hub.CloseSession(dispReq.SessionID) // 3. remove hub entry
		// 5. Remove the per-session worktree on session close (if any was provisioned).
		// Registered after hub.CloseSession defer; in LIFO order this runs BEFORE
		// hub.CloseSession — acceptable because the worktree path is independent of the
		// hub entry and the diff handler's PathFor check will return (false) once removed.
		if capturedWorktreeOverride != "" && capturedMgr != nil {
			defer func() {
				if err := capturedMgr.Remove(dispReq.SessionID); err != nil {
					slog.Warn("consoleui: worktree remove on session close",
						"session", dispReq.SessionID, "err", err)
				}
			}()
		}

		// Accumulate assistant text for a single coalesced assistant turn.
		var assistantBuf strings.Builder

		onChunk := func(chunk dispatch.StreamChunk) {
			ev := SSEEvent{
				SessionID:      dispReq.SessionID,
				ConversationID: conversationID,
				Type:           chunk.Type,
				Text:           chunk.Text,
				TS:             time.Now().UTC().Format(time.RFC3339Nano),
			}
			switch chunk.Type {
			case "summary":
				// Use pointers so exit_code:0 (success) serialises correctly
				// (omitempty on int zero-value would suppress it).
				code := chunk.ExitCode
				durationS := chunk.DurationS
				totalCostUSD := chunk.TotalCostUSD
				ev.ExitCode = &code
				ev.DurationS = &durationS
				ev.TotalCostUSD = &totalCostUSD
				ev.ModelResolved = chunk.ModelResolved

				// Update fleet registry status from summary exit_code.
				if ch.registry != nil {
					if code == 0 {
						ch.registry.UpdateStatus(dispReq.SessionID, dispatch.StatusFinished)
					} else {
						ch.registry.UpdateStatus(dispReq.SessionID, dispatch.StatusFailed)
					}
				}
				exitCode = code
				if code != 0 {
					exitStatus = dispatch.StatusFailed
				}

				// Append coalesced assistant turn, then summary turn.
				if text := assistantBuf.String(); text != "" {
					_ = ch.transcripts.Append(TranscriptEntry{
						SessionID:      dispReq.SessionID,
						ConversationID: conversationID,
						OperatorID:     capturedOperatorID,
						Role:           RoleAssistant,
						Text:           text,
					})
				}
				_ = ch.transcripts.Append(TranscriptEntry{
					SessionID:      dispReq.SessionID,
					ConversationID: conversationID,
					OperatorID:     capturedOperatorID,
					Role:           RoleSummary,
					ExitCode:       chunk.ExitCode,
					DurationS:      chunk.DurationS,
					TotalCostUSD:   chunk.TotalCostUSD,
					Model:          chunk.ModelResolved,
				})

			case "token":
				assistantBuf.WriteString(chunk.Text)

			case "tool_use":
				// Populate the tool_use SSE fields; transcript persistence is
				// intentionally skipped (tool events are transient UI state, not
				// conversation history — they are already implicitly reflected in
				// the subsequent assistant text).
				ev.ToolName = chunk.ToolName
				ev.ToolInput = chunk.ToolInput

			case "tool_result":
				// ToolOutput is already hard-truncated by emitToolChunk at the
				// dispatch layer (≤ maxToolOutputBytes).  Not persisted to transcript
				// to bound transcript growth; the tool result content is ephemeral UI.
				ev.ToolName = chunk.ToolName
				ev.ToolOutput = chunk.ToolOutput
				ev.IsError = chunk.IsError

			case "thinking":
				// Extended thinking delta.  Owner-scoped (same as token/tool events):
				// ChatHub.Route delivers only to the session owner's connections when
				// the session is not shared.  Not persisted to the transcript —
				// thinking is ephemeral UI state.
				ev.Thinking = chunk.Thinking
				ev.ThinkingTruncated = chunk.ThinkingTruncated
				ev.ThinkingRedacted = chunk.ThinkingRedacted
			}
			ch.hub.Route(ev)
		}

		// Interactive-P1: when interactive=true and an interactiveMgr is configured,
		// route through the persistent session instead of RunStream.
		//
		// This goroutine stays alive (blocking on sess.Closed()) so that the
		// deferred hub.CloseSession only fires when the interactive session itself
		// exits.  This keeps the hub session entry live for the duration, allowing
		// subsequent turns (via /api/chat/send) and SSE routing to work correctly.
		if dispReq.Interactive && ch.interactiveMgr != nil {
			// Resolve the project pin server-side (mirrors the one-shot path).
			project := ch.workspaceRoot
			if capturedWorktreeOverride != "" {
				project = capturedWorktreeOverride
			}

			// Resolve the agent system prompt from the roster (same as RunStream).
			capturedModel := modelName
			capturedEffort := dispReq.Effort
			capturedPrompt := resolveAgentSystemPrompt(ch.yakosRoot, project, dispReq.Agent)

			// onChunk fans out to the hub and accumulates transcript text.
			// Same contract as the one-shot onChunk (called from readLoop goroutine).
			sessParams := interactive.SessionParams{
				ConversationID:  conversationID,
				OwnerOperatorID: capturedOperatorID,
				OnChunk:         onChunk,
				CmdProvider: func() *exec.Cmd {
					return runtime.InteractiveExecCmd(project, capturedPrompt, capturedModel, capturedEffort)
				},
			}

			// Ensure the session exists (create if new, return existing if same owner).
			sess, ensureErr := ch.interactiveMgr.Ensure(conversationID, capturedOperatorID, sessParams)
			if ensureErr != nil {
				exitStatus = dispatch.StatusFailed
				exitCode = -1
				errText := fmt.Sprintf("interactive: start failed: %s", ensureErr.Error())
				_ = ch.transcripts.Append(TranscriptEntry{
					SessionID:      dispReq.SessionID,
					ConversationID: conversationID,
					OperatorID:     capturedOperatorID,
					Role:           RoleError,
					Text:           errText,
				})
				ch.hub.Route(SSEEvent{
					SessionID:      dispReq.SessionID,
					ConversationID: conversationID,
					Type:           "error",
					Text:           errText,
					TS:             time.Now().UTC().Format(time.RFC3339Nano),
				})
				sharedAtFinish = ch.hub.IsShared(dispReq.SessionID)
				return
			}

			// Deliver the first turn.
			frame := runtime.EncodeUserTurn(dispReq.Task)
			if sendErr := ch.interactiveMgr.Send(conversationID, capturedOperatorID, frame); sendErr != nil {
				exitStatus = dispatch.StatusFailed
				exitCode = -1
				errText := fmt.Sprintf("interactive: send failed: %s", sendErr.Error())
				_ = ch.transcripts.Append(TranscriptEntry{
					SessionID:      dispReq.SessionID,
					ConversationID: conversationID,
					OperatorID:     capturedOperatorID,
					Role:           RoleError,
					Text:           errText,
				})
				ch.hub.Route(SSEEvent{
					SessionID:      dispReq.SessionID,
					ConversationID: conversationID,
					Type:           "error",
					Text:           errText,
					TS:             time.Now().UTC().Format(time.RFC3339Nano),
				})
				// Don't return: the session is alive; let it run until closed.
			}

			// Wait for the interactive session to close (crash, idle reap, or cancel).
			// The deferred hub.CloseSession fires when we return, keeping the hub
			// entry live for the entire interactive session lifetime.
			select {
			case <-sess.Closed():
			case <-ctx.Done():
				// Server shutdown or cancel: close the session gracefully.
				ch.interactiveMgr.Close(conversationID)
			}

			sharedAtFinish = ch.hub.IsShared(dispReq.SessionID)
			return
		}

		params := dispatch.Params{
			Agent:          dispReq.Agent,
			Task:           dispReq.Task,
			Runtime:        runtimeName,
			Model:          modelName,
			OperatorID:     capturedOperatorID,
			ConversationID: conversationID,
			SessionID:      dispReq.SessionID,
			// Effort was validated in the handler (ValidateEffort); empty = no flag.
			Effort: dispReq.Effort,
			// Project is intentionally omitted: Service.RunStream pins it to
			// cfg.WorkspaceRoot when empty.  This is the server-side project pin.
			// Pass the resolved identity so dispatch can enforce roles and use cert CN.
			ResolvedIdentity: dispatch.IdentityCarrier{
				Populated: capturedIdentityForGoroutine.Resolved,
				Identity:  capturedIdentityForGoroutine,
			},
			// WorkDirOverride is set server-side from the worktreemgr path when
			// WorktreeMode is true.  It is NEVER derived from the client request body.
			WorkDirOverride: capturedWorktreeOverride,
		}

		if _, err := ch.svc.RunStream(ctx, params, onChunk); err != nil {
			if !errors.Is(err, context.Canceled) {
				slog.Error("consoleui: chat RunStream error",
					"session", dispReq.SessionID,
					"agent", dispReq.Agent,
					"err", err,
				)
				exitStatus = dispatch.StatusFailed
				exitCode = -1

				// Emit an SSE error frame so the client UI shows an error
				// instead of hanging forever.  The error message is kept terse
				// (no internal paths or stack traces) since it is delivered to
				// the browser.  Persist a transcript error turn so the
				// conversation record reflects the failure.
				errText := fmt.Sprintf("dispatch failed: %s", err.Error())
				_ = ch.transcripts.Append(TranscriptEntry{
					SessionID:      dispReq.SessionID,
					ConversationID: conversationID,
					OperatorID:     capturedOperatorID,
					Role:           RoleError,
					Text:           errText,
				})
				ch.hub.Route(SSEEvent{
					SessionID:      dispReq.SessionID,
					ConversationID: conversationID,
					Type:           "error",
					Text:           errText,
					TS:             time.Now().UTC().Format(time.RFC3339Nano),
				})
			}
		}

		// Refresh the shared flag with the final state of the hub entry (before
		// hub.CloseSession fires in defer 3).  This captures any share/unshare
		// that happened during the dispatch lifetime.
		sharedAtFinish = ch.hub.IsShared(dispReq.SessionID)
	}()

	// Respond immediately with the session ID.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(DispatchResponse{SessionID: dispReq.SessionID})
}

// ---- POST /api/chat/cancel --------------------------------------------------

// CancelRequest is the JSON body for POST /api/chat/cancel.
type CancelRequest struct {
	SessionID  string `json:"sessionId"`
	OperatorID string `json:"operatorId"` // C1: required for ownership check
}

// handleChatCancel cancels an in-flight dispatch.
//
// Ownership: the caller must supply the same operatorId that owns the session.
// A different operator attempting to cancel another operator's session receives
// 403.  Cancelling a non-existent or already-finished session is a no-op
// returning 200 (idempotency for the legitimate owner).
func (ch *chatHandlers) handleChatCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		http.Error(w, "sessionId is required", http.StatusBadRequest)
		return
	}
	if err := dispatch.ValidateIdentityField("session_id", req.SessionID); err != nil {
		http.Error(w, "invalid sessionId", http.StatusBadRequest)
		return
	}

	// C1 dual-regime operator_id for cancel ownership check:
	// Authenticated (mTLS cert): cert CN is the identity; body operatorId silently ignored.
	// Unauthenticated (loopback): require and validate operatorId from body.
	cancelID := netid.IdentityFrom(r.Context())
	var effectiveOperatorID string
	if cancelID.Authenticated {
		effectiveOperatorID = cancelID.OperatorID
	} else {
		if req.OperatorID == "" {
			http.Error(w, "operatorId is required", http.StatusBadRequest)
			return
		}
		if err := dispatch.ValidateIdentityField("operator_id", req.OperatorID); err != nil {
			http.Error(w, "invalid operatorId", http.StatusBadRequest)
			return
		}
		effectiveOperatorID = req.OperatorID
	}

	// C1: ownership check — only the session owner may cancel.
	// An unknown session is a no-op (200) for any caller (idempotent).
	// A known session owned by a different operator is 403.
	owner, exists := ch.hub.SessionOwner(req.SessionID)
	if exists && owner != effectiveOperatorID {
		http.Error(w, "forbidden: session owned by different operator", http.StatusForbidden)
		return
	}

	ch.state.cancel(req.SessionID) // no-op if not active
	w.WriteHeader(http.StatusOK)
}

// ---- GET /api/chat/transcript -----------------------------------------------

// handleChatTranscript returns the persisted transcript for a conversationId.
//
// Standard (owner) path: the caller must supply operatorId; the transcript
// reader enforces owner-scoping and returns errTranscriptForbidden (→ 403)
// when the operatorId does not match the conversation owner.
//
// Shared-session watch path: shared-access is determined exclusively by
// hub.IsConversationShared(conversationID) — derived from the CONVERSATION
// being read, not from a caller-supplied sessionId.  This closes the
// confused-deputy IDOR where an attacker could pair a shared sessionId with
// an unrelated victim conversationId to bypass the owner check.
//
// The optional sessionId query parameter is accepted (so the client can send
// it without error) but it does NOT influence the shared-access decision.
// Defense-in-depth: if sessionId is supplied and its hub-tracked conversationID
// differs from the requested conversationId, the request is rejected (403).
//
// Security invariant: hub.IsConversationShared is the sole authority for
// shared-access; callers cannot self-assert it.  errTranscriptForbidden is
// never weakened: a non-owner, non-watcher caller still gets 403.
func (ch *chatHandlers) handleChatTranscript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	conversationID := r.URL.Query().Get("conversationId")
	if conversationID == "" {
		http.Error(w, "conversationId is required", http.StatusBadRequest)
		return
	}
	if err := dispatch.ValidateIdentityField("conversation_id", conversationID); err != nil {
		http.Error(w, "invalid conversationId", http.StatusBadRequest)
		return
	}

	// Optional sessionId — accepted for client compatibility but NOT used to
	// drive the shared-access decision.  Validated when present.
	// Defense-in-depth: if supplied, the hub must record this sessionId as
	// owning conversationID.  A mismatch (shared sessionId paired with a
	// different operatorId's conversationId) is rejected with 403 to prevent
	// confused-deputy IDOR regardless of any future code changes.
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID != "" {
		if err := dispatch.ValidateIdentityField("session_id", sessionID); err != nil {
			http.Error(w, "invalid sessionId", http.StatusBadRequest)
			return
		}
		// If the hub knows this sessionId and it is bound to a DIFFERENT
		// conversationID, reject.  An unregistered session (sessionID not in hub
		// — e.g. already closed) is allowed through; shared-access will still
		// fail because IsConversationShared will return false.
		if hubConvID := ch.hub.ConversationForSession(sessionID); hubConvID != "" && hubConvID != conversationID {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	// C1 dual-regime operator_id for transcript scoping:
	// Authenticated (mTLS cert): cert CN scopes the transcript; query param ignored.
	// Unauthenticated (loopback): require and validate operatorId from query param.
	transcriptID := netid.IdentityFrom(r.Context())
	var operatorID string
	if transcriptID.Authenticated {
		operatorID = transcriptID.OperatorID
	} else {
		operatorID = r.URL.Query().Get("operatorId")
		if operatorID == "" {
			http.Error(w, "operatorId is required", http.StatusBadRequest)
			return
		}
		if err := dispatch.ValidateIdentityField("operator_id", operatorID); err != nil {
			http.Error(w, "invalid operatorId", http.StatusBadRequest)
			return
		}
	}

	// Determine shared-access from the CONVERSATION being read, owner-anchored.
	//
	// Step 1: resolve the transcript's established owner (first user-turn
	// operatorID) WITHOUT performing any auth check.  This is the owner the
	// hub entry must match for shared-access to be granted.
	//
	// Step 2: ask the hub whether a SHARED session that owns conversationID AND
	// is recorded under transcriptOwner is currently open.  The owner-anchor
	// prevents an attacker who poisoned the hub binding (bound their own session
	// to the victim's conversationID) from having their shared session satisfy
	// the lookup: their ownerOperatorID will differ from transcriptOwner.
	//
	// If FirstUserOwner fails (e.g. malformed file) we proceed with "" which
	// disables the owner-anchor in the hub lookup.  That is safe: ReadShared
	// below will apply M1 fail-closed and deny access anyway.
	transcriptOwner, _ := ch.transcripts.FirstUserOwner(conversationID)
	sharedAccess := ch.hub.IsConversationShared(conversationID, transcriptOwner)

	entries, err := ch.transcripts.ReadShared(conversationID, operatorID, sharedAccess)
	if err != nil {
		if errors.Is(err, errTranscriptForbidden) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		slog.Error("consoleui: transcript read error", "conversation", conversationID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []TranscriptEntry{} // return [] not null
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		slog.Error("consoleui: transcript encode error", "err", err)
	}
}

// ---- POST /api/chat/share --------------------------------------------------

// ShareRequest is the JSON body for POST /api/chat/share.
//
// Only the session owner may change the shared flag.  A non-owner attempting
// this receives 403.  A non-existent session receives 404.
type ShareRequest struct {
	SessionID  string `json:"sessionId"`
	OperatorID string `json:"operatorId"`
	Shared     bool   `json:"shared"`
}

// handleChatShare flips the shared flag on a session.
//
// Security: only the session owner (identified by operatorId == session owner)
// may promote or demote a session.  Auth is enforced at the edge
// (requireTokenForNonStatic); no re-check here.
func (ch *chatHandlers) handleChatShare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		http.Error(w, "sessionId is required", http.StatusBadRequest)
		return
	}
	if err := dispatch.ValidateIdentityField("session_id", req.SessionID); err != nil {
		http.Error(w, "invalid sessionId", http.StatusBadRequest)
		return
	}

	// C1 Share-pane confidentiality fix (core of Phase 6b):
	// Authenticated (mTLS cert): cert CN is used for the ownership check;
	// body operatorId is silently ignored.  This closes the C1 vulnerability
	// where operator B could supply operator A's operatorId in the body and
	// flip the shared flag on A's session.
	// Unauthenticated (loopback bearer): require and validate operatorId from body.
	shareID := netid.IdentityFrom(r.Context())
	var effectiveOperatorID string
	if shareID.Authenticated {
		// Cert CN wins; body operatorId cannot override it.
		effectiveOperatorID = shareID.OperatorID
	} else {
		if req.OperatorID == "" {
			http.Error(w, "operatorId is required", http.StatusBadRequest)
			return
		}
		if err := dispatch.ValidateIdentityField("operator_id", req.OperatorID); err != nil {
			http.Error(w, "invalid operatorId", http.StatusBadRequest)
			return
		}
		effectiveOperatorID = req.OperatorID
	}

	// Atomic ownership check + shared-flag update via SetShared.
	//
	// The previous SessionOwner+OpenSession two-step had a TOCTOU window:
	// the dispatch goroutine's deferred CloseSession could fire between the two
	// calls, causing OpenSession to recreate a session entry that nothing ever
	// closes (permanent leak toward maxTotalSessions=256).  SetShared holds the
	// hub mutex across the entire check-and-update, closing that window.
	if err := ch.hub.SetShared(req.SessionID, effectiveOperatorID, req.Shared); err != nil {
		if errors.Is(err, errSessionNotFound) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, errSessionOwnerConflict) {
			http.Error(w, "forbidden: session owned by different operator", http.StatusForbidden)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Phase 4: when promoting to shared, include a safety warning that tool
	// output (bash stdout, file contents) will be visible to ALL watchers.
	// This is a contractual safety mechanic — not optional.
	type shareResponse struct {
		OK      bool   `json:"ok"`
		Warning string `json:"warning,omitempty"`
	}
	resp := shareResponse{OK: true}
	if req.Shared {
		resp.Warning = "Tool output (bash stdout, file contents) and model thinking will be visible to all session watchers."
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// ---- POST /api/chat/send ---------------------------------------------------

// SendRequest is the JSON body for POST /api/chat/send.
//
// Delivers a follow-up user turn into an existing interactive session.
// The session must have been started with interactive:true in
// POST /api/chat/dispatch.
//
// Security:
//   - operatorId is resolved from the cert CN (mTLS) or from the body
//     (cooperative-label path), same as /api/chat/dispatch.
//   - Only the session owner may send turns.  Non-owners receive 403.
//   - sessionId and conversationId are validated by ValidateIdentityField.
//   - text is validated for size (dispatch.MaxTaskBytes; 1 MB); oversized
//     text is rejected with 400 before any stdin write is attempted.
//     Content is not otherwise validated — the claude process handles it.
type SendRequest struct {
	ConversationID string `json:"conversationId"`
	OperatorID     string `json:"operatorId"`
	SessionID      string `json:"sessionId"`
	Text           string `json:"text"`
}

// handleChatSend delivers a follow-up turn to an existing interactive session.
//
// Response codes:
//   - 202 Accepted: turn delivered.
//   - 400 Bad Request: missing/invalid fields.
//   - 403 Forbidden: caller is not the session owner.
//   - 404 Not Found: no live interactive session for this conversationId.
//   - 409 Conflict: a turn is already in flight on this session.
//   - 503 Service Unavailable: interactive mode not configured.
func (ch *chatHandlers) handleChatSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if ch.interactiveMgr == nil {
		http.Error(w, "interactive mode not configured", http.StatusServiceUnavailable)
		return
	}

	var req SendRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.ConversationID) == "" {
		http.Error(w, "conversationId is required", http.StatusBadRequest)
		return
	}
	if err := dispatch.ValidateIdentityField("conversation_id", req.ConversationID); err != nil {
		http.Error(w, "invalid conversationId", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Text) == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}
	if len(req.Text) > dispatch.MaxTaskBytes {
		http.Error(w, fmt.Sprintf("text exceeds maximum size (%d bytes; limit %d)", len(req.Text), dispatch.MaxTaskBytes), http.StatusBadRequest)
		return
	}

	// sessionId is optional but validated when present.
	if req.SessionID != "" {
		if err := dispatch.ValidateIdentityField("session_id", req.SessionID); err != nil {
			http.Error(w, "invalid sessionId", http.StatusBadRequest)
			return
		}
	}

	// C1 dual-regime operator_id (identical to dispatch handler).
	capturedIdentity := netid.IdentityFrom(r.Context())
	var effectiveOperatorID string
	if capturedIdentity.Authenticated {
		effectiveOperatorID = capturedIdentity.OperatorID
	} else {
		if strings.TrimSpace(req.OperatorID) == "" {
			http.Error(w, "operatorId is required", http.StatusBadRequest)
			return
		}
		if err := dispatch.ValidateIdentityField("operator_id", req.OperatorID); err != nil {
			http.Error(w, "invalid operatorId", http.StatusBadRequest)
			return
		}
		effectiveOperatorID = req.OperatorID
	}

	frame := runtime.EncodeUserTurn(req.Text)
	err := ch.interactiveSend.Send(req.ConversationID, effectiveOperatorID, frame)
	if err != nil {
		switch {
		case errors.Is(err, interactive.ErrNoSession):
			http.Error(w, "no live session for this conversationId", http.StatusNotFound)
		case errors.Is(err, interactive.ErrOwnerConflict):
			http.Error(w, "forbidden: session owned by different operator", http.StatusForbidden)
		case errors.Is(err, interactive.ErrTurnInFlight):
			http.Error(w, "turn already in flight on this session", http.StatusConflict)
		default:
			slog.Error("consoleui: interactive send error", "conversationID", req.ConversationID, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	// Append the user turn to the transcript only after successful delivery —
	// orphaned entries (from failed sends) would have no corresponding response.
	_ = ch.transcripts.Append(TranscriptEntry{
		SessionID:      req.SessionID,
		ConversationID: req.ConversationID,
		OperatorID:     effectiveOperatorID,
		Role:           RoleUser,
		Text:           req.Text,
	})

	w.WriteHeader(http.StatusAccepted)
}

// ---- helpers ----------------------------------------------------------------

// isKnownRuntime reports whether name is a registered runtime.
func isKnownRuntime(name string) bool {
	for _, k := range runtime.Known {
		if k == name {
			return true
		}
	}
	return false
}

// resolveAgentSystemPrompt resolves the agent's system prompt body by composing
// the roster.  Returns "" on any error (graceful degradation: the session
// starts without an agent persona rather than failing hard).
//
// Mirrors the agent resolution done inside Service.RunStream.
func resolveAgentSystemPrompt(yakosRoot, project, agentName string) string {
	if yakosRoot == "" || project == "" || agentName == "" {
		return ""
	}
	roster, err := agentscompose.Compose(yakosRoot, project)
	if err != nil {
		slog.Warn("consoleui: interactive: compose roster failed", "err", err)
		return ""
	}
	for _, a := range roster {
		if a.ID == agentName {
			return a.Prompt
		}
	}
	return ""
}
