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
	"github.com/bakw00ds/yakos/internal/runtime"
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
	svc         *dispatch.Service // may be nil (console without dispatch)
	workDir     string            // used by NewTranscripts
}

// newChatHandlers is called from registerChatRoutes.
func newChatHandlers(hub *ChatHub, transcripts *Transcripts, svc *dispatch.Service) *chatHandlers {
	return &chatHandlers{
		hub:         hub,
		transcripts: transcripts,
		state:       newChatState(),
		svc:         svc,
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

	// Validate operatorId from query (SSE — browser uses fetch; SW injects
	// Authorization header; but operatorId is attribution, not auth).
	operatorID := r.URL.Query().Get("operatorId")
	if err := dispatch.ValidateIdentityField("operator_id", operatorID); err != nil {
		http.Error(w, "invalid operatorId", http.StatusBadRequest)
		return
	}
	if operatorID == "" {
		http.Error(w, "operatorId is required", http.StatusBadRequest)
		return
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
	w.Header().Set("Cache-Control", "no-cache")
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
	if strings.TrimSpace(req.OperatorID) == "" {
		http.Error(w, "operatorId is required", http.StatusBadRequest)
		return
	}

	// --- Validate identity fields (format; not auth) ---
	// These flow into subprocess argv; validate before any hub or service call.
	if err := dispatch.ValidateIdentityField("session_id", req.SessionID); err != nil {
		http.Error(w, "invalid sessionId", http.StatusBadRequest)
		return
	}
	if err := dispatch.ValidateIdentityField("operator_id", req.OperatorID); err != nil {
		http.Error(w, "invalid operatorId", http.StatusBadRequest)
		return
	}
	if req.ConversationID != "" {
		if err := dispatch.ValidateIdentityField("conversation_id", req.ConversationID); err != nil {
			http.Error(w, "invalid conversationId", http.StatusBadRequest)
			return
		}
	}

	// --- Hub: open session (ownership check, must happen before svc check) ---
	// This enforces per-operator isolation: 403 if session already owned by
	// a different operator.  This check runs regardless of whether the dispatch
	// service is configured.
	if err := ch.hub.OpenSession(req.SessionID, req.OperatorID, false); err != nil {
		if errors.Is(err, errSessionOwnerConflict) {
			http.Error(w, "session owned by different operator", http.StatusForbidden)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// --- Service availability check ---
	if ch.svc == nil {
		http.Error(w, "dispatch service not configured", http.StatusServiceUnavailable)
		return
	}

	// --- Guard: one active dispatch per session at a time ---
	ctx, cancel := context.WithCancel(r.Context())
	if !ch.state.add(req.SessionID, cancel) {
		cancel()
		// Session already has an active dispatch.
		http.Error(w, "session already has an active dispatch", http.StatusConflict)
		return
	}

	// Use a stable copy for the goroutine.
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
		OperatorID:     dispReq.OperatorID,
		Role:           RoleUser,
		Text:           dispReq.Task,
		Runtime:        runtimeName,
		Model:          modelName,
	})

	// Launch RunStream in a goroutine.  The goroutine owns the cancel function
	// and the hub session for its lifetime.
	go func() {
		defer cancel()
		defer ch.state.remove(dispReq.SessionID)
		defer ch.hub.CloseSession(dispReq.SessionID)

		// Accumulate assistant text for a single coalesced assistant turn.
		var assistantBuf strings.Builder

		onChunk := func(chunk dispatch.StreamChunk) {
			ev := SSEEvent{
				SessionID: dispReq.SessionID,
				Type:      chunk.Type,
				Text:      chunk.Text,
				TS:        time.Now().UTC().Format(time.RFC3339Nano),
			}
			if chunk.Type == "summary" {
				ev.ExitCode = chunk.ExitCode
				ev.DurationS = chunk.DurationS
				ev.TotalCostUSD = chunk.TotalCostUSD
				ev.ModelResolved = chunk.ModelResolved

				// Append coalesced assistant turn, then summary turn.
				if text := assistantBuf.String(); text != "" {
					_ = ch.transcripts.Append(TranscriptEntry{
						SessionID:      dispReq.SessionID,
						ConversationID: conversationID,
						OperatorID:     dispReq.OperatorID,
						Role:           RoleAssistant,
						Text:           text,
					})
				}
				_ = ch.transcripts.Append(TranscriptEntry{
					SessionID:      dispReq.SessionID,
					ConversationID: conversationID,
					OperatorID:     dispReq.OperatorID,
					Role:           RoleSummary,
					ExitCode:       chunk.ExitCode,
					DurationS:      chunk.DurationS,
					TotalCostUSD:   chunk.TotalCostUSD,
					Model:          chunk.ModelResolved,
				})
			} else if chunk.Type == "token" {
				assistantBuf.WriteString(chunk.Text)
			}
			ch.hub.Route(ev)
		}

		params := dispatch.Params{
			Agent:          dispReq.Agent,
			Task:           dispReq.Task,
			Runtime:        runtimeName,
			Model:          modelName,
			OperatorID:     dispReq.OperatorID,
			ConversationID: conversationID,
			SessionID:      dispReq.SessionID,
			// Project is intentionally omitted: Service.RunStream pins it to
			// cfg.WorkspaceRoot when empty.  This is the server-side project pin.
		}

		if _, err := ch.svc.RunStream(ctx, params, onChunk); err != nil {
			if !errors.Is(err, context.Canceled) {
				slog.Error("consoleui: chat RunStream error",
					"session", dispReq.SessionID,
					"agent", dispReq.Agent,
					"err", err,
				)
			}
		}
	}()

	// Respond immediately with the session ID.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(DispatchResponse{SessionID: dispReq.SessionID})
}

// ---- POST /api/chat/cancel --------------------------------------------------

// CancelRequest is the JSON body for POST /api/chat/cancel.
type CancelRequest struct {
	SessionID string `json:"sessionId"`
}

// handleChatCancel cancels an in-flight dispatch.
// Idempotent: cancelling a non-existent or already-finished session is a no-op.
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
	operatorID := r.URL.Query().Get("operatorId")

	if conversationID == "" {
		http.Error(w, "conversationId is required", http.StatusBadRequest)
		return
	}
	if err := dispatch.ValidateIdentityField("conversation_id", conversationID); err != nil {
		http.Error(w, "invalid conversationId", http.StatusBadRequest)
		return
	}
	if operatorID == "" {
		http.Error(w, "operatorId is required", http.StatusBadRequest)
		return
	}
	if err := dispatch.ValidateIdentityField("operator_id", operatorID); err != nil {
		http.Error(w, "invalid operatorId", http.StatusBadRequest)
		return
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
