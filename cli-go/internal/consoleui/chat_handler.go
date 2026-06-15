// Package consoleui — chat_handler.go
//
// Phase 3b chat endpoints:
//
//	GET  /api/chat/stream    — per-operator SSE stream (multiplexed by sessionID)
//	POST /api/chat/dispatch  — start a streaming dispatch (returns {sessionId})
//	POST /api/chat/cancel    — cancel an in-flight dispatch
//	GET  /api/chat/transcript — fetch persisted transcript for a conversationId
//
// # Auth
//
// All four endpoints are mounted under the console edge auth
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
package consoleui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/runtime"
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
	hub         *ChatHub
	transcripts *Transcripts
	state       *chatState
	svc         *dispatch.Service          // may be nil (console without dispatch)
	registry    *dispatch.SessionRegistry  // fleet registry; wired by New() after construction
	bus         *wsbus.Bus                 // event bus for fleet.* WS events; wired by New()
	workDir     string                     // used by NewTranscripts
	serverCtx   context.Context            // server-lifetime context; dispatch goroutines derive from this (NOT r.Context())
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
type DispatchRequest struct {
	Runtime        string `json:"runtime"`
	Model          string `json:"model"`
	Agent          string `json:"agent"`
	Task           string `json:"task"`
	SessionID      string `json:"sessionId"`
	OperatorID     string `json:"operatorId"`
	ConversationID string `json:"conversationId"` // optional; empty → new conversation
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

	// --- Validate required string fields ---
	if strings.TrimSpace(req.Agent) == "" {
		http.Error(w, "agent is required", http.StatusBadRequest)
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
			SessionID:   dispReq.SessionID,
			Agent:       dispReq.Agent,
			Runtime:     runtimeName,
			OperatorID:  capturedOperatorID,
			TaskPreview: dispReq.Task, // truncated inside Add to <=120 runes
			StartedAt:   startedAt,
			Status:      dispatch.StatusRunning,
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

		// Accumulate assistant text for a single coalesced assistant turn.
		var assistantBuf strings.Builder

		onChunk := func(chunk dispatch.StreamChunk) {
			ev := SSEEvent{
				SessionID: dispReq.SessionID,
				Type:      chunk.Type,
				Text:      chunk.Text,
				TS:        time.Now().UTC().Format(time.RFC3339Nano),
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
			}
			ch.hub.Route(ev)
		}

		params := dispatch.Params{
			Agent:          dispReq.Agent,
			Task:           dispReq.Task,
			Runtime:        runtimeName,
			Model:          modelName,
			OperatorID:     capturedOperatorID,
			ConversationID: conversationID,
			SessionID:      dispReq.SessionID,
			// Project is intentionally omitted: Service.RunStream pins it to
			// cfg.WorkspaceRoot when empty.  This is the server-side project pin.
			// Pass the resolved identity so dispatch can enforce roles and use cert CN.
			ResolvedIdentity: dispatch.IdentityCarrier{
				Populated: capturedIdentityForGoroutine.Resolved,
				Identity:  capturedIdentityForGoroutine,
			},
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
// The caller must supply operatorId; the transcript reader enforces owner-scoping.
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

	entries, err := ch.transcripts.Read(conversationID, operatorID)
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
		resp.Warning = "Tool output (bash stdout, file contents) will be visible to all session watchers."
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
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
