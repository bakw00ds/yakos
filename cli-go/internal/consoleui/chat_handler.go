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
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/router"
	"github.com/bakw00ds/yakos/internal/runtime"
	"github.com/bakw00ds/yakos/internal/worktreemgr"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// chatState is the in-process registry of active RunStream goroutines.
// It maps sessionID → cancel function so pane-close can kill the subprocess.
//
// Each entry carries a generation. A cancel removes the session's entry at once
// (so the pane can send its next turn right away, with the same sessionId), but
// the cancelled turn's goroutine is still unwinding and removes "its" entry when
// it ends. Keyed by sessionId alone, that late remove deleted the NEW turn's
// entry: the new turn could no longer be cancelled, and a third turn on the same
// session was accepted while it ran. A goroutine removes only the generation it
// registered.
type chatState struct {
	mu      sync.Mutex
	cancels map[string]chatEntry
	nextGen uint64
}

type chatEntry struct {
	cancel context.CancelFunc
	gen    uint64
}

func newChatState() *chatState {
	return &chatState{cancels: make(map[string]chatEntry)}
}

// add registers a cancel function for a session and returns the entry's
// generation, which its goroutine hands back to remove.  Returns false if the
// sessionID already has an in-flight dispatch (caller returns 409).
func (cs *chatState) add(sessionID string, cancel context.CancelFunc) (gen uint64, ok bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if _, exists := cs.cancels[sessionID]; exists {
		return 0, false
	}
	cs.nextGen++
	cs.cancels[sessionID] = chatEntry{cancel: cancel, gen: cs.nextGen}
	return cs.nextGen, true
}

// remove deletes the entry registered under gen when its goroutine exits. An
// entry of a later generation (the next turn on the same session) is left alone.
func (cs *chatState) remove(sessionID string, gen uint64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if e, ok := cs.cancels[sessionID]; ok && e.gen == gen {
		delete(cs.cancels, sessionID)
	}
}

// cancel cancels the in-flight dispatch and removes it.
// No-op if the session is not active.
func (cs *chatState) cancel(sessionID string) {
	cs.mu.Lock()
	e, ok := cs.cancels[sessionID]
	if ok {
		delete(cs.cancels, sessionID)
	}
	cs.mu.Unlock()
	if ok {
		e.cancel()
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

	// sdkEngineFactory is the factory for SDKEngine instances (P2c).
	// Non-nil only when --console-structured-questions is passed AND node+bundle
	// are available.  Nil → 503 for structuredQuestions:true requests.
	sdkEngineFactory *interactive.SDKEngineFactory

	// pendingQuestions holds the per-conversation pending AskUserQuestion state
	// for forged-answer prevention.  Always non-nil after New().
	pendingQuestions *pendingQuestionStore

	// turns gives every turn of an interactive session its dispatch-log event
	// pair (K-136); see chat_account.go.  Always non-nil.
	turns *turnLedger

	// loopbackHost is true when the console runs on the loopback trust path
	// (not networked); loopbackOwnerID is the host operator's identity. Only
	// that identity may read the host's soul text (K-149 F1).
	loopbackHost    bool
	loopbackOwnerID string
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
		turns:       newTurnLedger(),
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

	// StructuredQuestions opts into the SDK engine (P2c) instead of the CLI
	// engine for this interactive session.  Requires interactive:true; ignored
	// when interactive is false.
	//
	// When true: routes through Manager.EnsureSDK using the SDKEngineFactory.
	// The factory must be non-nil (requires --console-structured-questions +
	// node ≥18 + sidecar bundle); otherwise returns 503.
	//
	// When false (default): uses the existing CLI engine (Session) path.  Zero
	// regression on all existing clients.
	StructuredQuestions bool `json:"structuredQuestions"`

	// OverrideRuntime and OverrideModel are the one-turn override an "@codex:gpt-5"
	// message prefix becomes (parsed client-side, K-148). They beat the pane's
	// runtime and model selects for this turn only.
	OverrideRuntime string `json:"overrideRuntime,omitempty"`
	OverrideModel   string `json:"overrideModel,omitempty"`
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
//   - runtime must be in runtime.Known, or empty/"auto" to resolve from the
//     agent's pin; the model must be valid for the runtime the request
//     resolves to (K-132); agent name must resolve in the roster (generic 400
//     on failure — no path/roster leak in error messages).
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

	// --- Per-turn override (K-148): an @prefix beats the pane's selects ---
	routePinned, ovErr := applyRouteOverride(&req)
	if ovErr != nil {
		http.Error(w, "invalid override", http.StatusBadRequest)
		return
	}
	overrideRuntime := ""
	if routePinned == "override" {
		overrideRuntime = req.Runtime
	}

	// --- Validate runtime ---
	// An empty runtime (or "auto") asks the dispatcher to resolve the runtime
	// from the agent's frontmatter pin and the project config. It is passed
	// through as "" instead of being forced to claude (K-127: the pane could
	// never reach a pinned agent's runtime). An explicit runtime must be known.
	requestedRuntime := strings.TrimSpace(req.Runtime)
	if requestedRuntime == "auto" {
		requestedRuntime = ""
	}
	if requestedRuntime != "" && !isKnownRuntime(requestedRuntime) {
		http.Error(w, "invalid runtime", http.StatusBadRequest)
		return
	}
	requestedModel := strings.TrimSpace(req.Model)

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
	// Bare runtime names (claude/codex/agy) are valid catch-alls and are
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

	// --- Resolve the runtime, then validate the model against it (K-132) ---
	// A model id means something only to the runtime that runs it, and with an
	// auto pane that runtime is known only once the agent's pin is read.
	// PreferredRuntime names the runtime this request is headed for without
	// touching the machine (no CLI or sign-in probe), so the model can be
	// checked and a bad one rejected with 400 before the 202. RunStream makes
	// the real choice, probe and fallbacks included, from the same inputs.
	pref, prefErr := dispatch.PreferredRuntime(dispatch.RouteQuery{
		YakosRoot: ch.yakosRoot,
		Project:   ch.workspaceRoot,
		Agent:     req.Agent,
		Override:  requestedRuntime,
		Task:      req.Task, // the sensitive class (K-140) reads it, as RunStream will
	})
	if prefErr != nil {
		http.Error(w, "invalid runtime", http.StatusBadRequest)
		return
	}
	runtimeName := pref.Runtime
	modelName := requestedModel
	// An interactive pane is a long-lived engine, so the router decides once, at
	// its first turn (K-148): the decision picks the engine's runtime and, when a
	// rule supplied it, the model. Later turns ride /api/chat/send.
	var interactiveRoute *routeView
	// interactiveRefusal is an Explain error: the router could not (or, for a
	// sensitive task, would not) place this first turn. The turn is refused the
	// way a one-shot turn is (an error frame after the 202) and no engine starts
	// on a runtime nobody decided (sec-347 F2).
	var interactiveRefusal error
	if req.Interactive && ch.svc != nil {
		interactiveRoute = &routeView{Runtime: runtimeName, Pinned: routePinned}
		d, exErr := dispatch.Explain(r.Context(), dispatch.ExplainQuery{
			YakosRoot: ch.yakosRoot, Project: ch.workspaceRoot, Agent: req.Agent,
			Runtime: requestedRuntime, Model: requestedModel,
			TaskBytes: int64(len(req.Task)), ConversationID: req.ConversationID,
			Task: req.Task, // the sensitive class (K-140) reads it, as RunStream will
		})
		if exErr != nil && sdkPaneToleratesRouteError(req.StructuredQuestions, runtimeName, exErr) {
			// The SDK engine is a sidecar on ANTHROPIC_API_KEY and never runs the
			// claude CLI, so "CLI not found" says nothing about it. Let the engine
			// gate (K-137) answer with its own operator-facing message. A sensitive
			// task never lands here: its every router error is a RouteRefusedError.
			exErr = nil
		}
		if exErr != nil {
			interactiveRefusal = exErr
		} else if isKnownRuntime(d.Runtime) {
			runtimeName = d.Runtime
			if modelName == "" && d.RuleID != router.RuleDefault {
				modelName, requestedModel = d.ModelID, d.ModelID
			}
			interactiveRoute = routeViewFrom(&dispatch.RouteInfo{
				Runtime: d.Runtime, Provider: d.Provider, Model: d.ModelID, RuleID: d.RuleID,
				Reason: d.Reason, Class: d.RouteClass, FallbackFrom: d.FallbackFrom,
			}, routePinned)
			noteRefusedOverride(interactiveRoute, routePinned, overrideRuntime)
		}
	}
	if modelName != "" {
		resolved, ok := dispatch.CheckModelOverride(runtimeName, modelName)
		if !ok {
			http.Error(w, "invalid model", http.StatusBadRequest)
			return
		}
		modelName = resolved
	}
	// The persistent claude engines (CLI and SDK) are claude processes. A codex
	// or agy pane gets a ResumeEngine instead, so the toggle is accepted; only
	// the SDK engine's structured questions stay claude-only.
	if req.Interactive && req.StructuredQuestions && runtimeName != "claude" {
		http.Error(w, "structured questions are only available for the claude runtime", http.StatusBadRequest)
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
	// Authenticated (mTLS cert / session): cert CN or username is authoritative;
	//   body operatorId is silently ignored.
	// Unauthenticated (loopback bearer): the resolver stamps a stable OS-derived
	//   ID via callerLabelFn (capturedIdentity.OperatorID); prefer that over the
	//   client body token so the owner identity is stable across browser restarts,
	//   port changes, and localStorage clears.  If the resolver provided no label
	//   (pre-fix test servers, empty stateDir), fall back to the body token.
	capturedIdentity := netid.IdentityFrom(r.Context())
	var effectiveOperatorID string
	if capturedIdentity.Authenticated {
		// Cert CN / session username wins; body operatorId is ignored.
		effectiveOperatorID = capturedIdentity.OperatorID
	} else if capturedIdentity.OperatorID != "" {
		// Loopback path with stable server-derived label: use it directly.
		// No body validation needed — the server stamped this value, not the client.
		effectiveOperatorID = capturedIdentity.OperatorID
	} else {
		// Legacy path: no resolver label; require and validate from body.
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

	// --- A conversation belongs to the operator who started it ---
	// The transcript's first user turn names that operator (the same anchor the
	// transcript and share endpoints use). Dispatching into someone else's
	// conversation would append to their transcript and, on claude, resume their
	// native session, which carries everything they said and every tool result
	// (sec-324 F1). The hub's own check only covers a turn that is still running;
	// this one holds after it has ended and after a restart, and after the owner
	// unshared a conversation a watcher still has the id of. A conversation with
	// no transcript yet is new and nobody's.
	//
	// The gate fails closed. A transcript that exists but cannot be read (a
	// permissions problem, a damaged or replaced file) leaves the owner unknown,
	// and passing would let anyone in, with only the stored-session owner check
	// still standing between them and a resume. Only "there is no transcript"
	// means a new conversation. It is a server-side fault, not a verdict about
	// the caller, so the answer is 500, and the reason is logged once here.
	{
		convForOwner := req.ConversationID
		if convForOwner == "" {
			convForOwner = req.SessionID
		}
		owner, ownerErr := ch.transcripts.FirstUserOwner(convForOwner)
		if ownerErr != nil {
			slog.Error("consoleui: cannot establish the conversation owner; refusing the dispatch",
				"conversation", convForOwner, "err", ownerErr)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if owner != "" && owner != effectiveOperatorID {
			http.Error(w, "forbidden: conversation owned by different operator", http.StatusForbidden)
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
	stateGen, registered := ch.state.add(req.SessionID, cancel)
	if !registered {
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

	// A conversation's live engine decides what kind of pane it is. A dispatch
	// that would build the other kind (claude into a codex or agy ResumeEngine,
	// or the reverse) is refused instead of being delivered to the wrong engine.
	if dispReq.Interactive && ch.interactiveMgr != nil {
		live, resume := ch.interactiveMgr.LiveEngineKind(conversationID, capturedOperatorID)
		// codex and agy are both ResumeEngines: the engine's own runtime must be
		// the one the chip names, not just the same kind (K-148).
		liveRT := ch.interactiveMgr.LiveResumeRuntime(conversationID, capturedOperatorID)
		if live && (resume != (runtimeName != "claude") || (liveRT != "" && liveRT != runtimeName)) {
			cancel()
			ch.state.remove(dispReq.SessionID, stateGen)
			ch.hub.CloseSession(dispReq.SessionID)
			http.Error(w, "conversation is pinned to a different runtime; start a new conversation to switch", http.StatusConflict)
			return
		}
	}

	// Handoff (K-148): an operator-initiated runtime switch carries a bounded,
	// scanned digest of the earlier turns at the tail of this turn's task. The
	// transcript and the fleet keep the operator's own words; only the turn
	// delivered to the runtime grows. Planned before the user turn is appended so
	// the digest holds earlier turns only.
	handoffDigest, handoffInfo := ch.planHandoff(conversationID, capturedOperatorID, runtimeName,
		routePinned != "router", len(req.Task))

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

	// The turn handed to the runtime: the operator's task, plus the handoff digest
	// when this is the first turn after a runtime switch (see planHandoff).
	dispReq.Task += handoffDigest

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
		// outcome captures how the dispatch ended, for fleet.finished: finished with
		// exit code 0 by default, failed with -1 on a non-cancel error path, and the
		// exit code of each turn's summary. The chunk callback writes it from the
		// engine's goroutine, so it has a lock (see dispatchOutcome).
		outcome := newDispatchOutcome()

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
				status, exitCode := outcome.get()
				if status == dispatch.StatusFailed {
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
		defer cancel()                                     // 1. stop any pending RunStream
		defer ch.state.remove(dispReq.SessionID, stateGen) // 2. release slot
		defer ch.hub.CloseSession(dispReq.SessionID)       // 3. remove hub entry
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

		// An interactive session's turns are accounted here, by turnLedger: the
		// session outlives any single call, so the dispatch layer cannot open and
		// finish an Account around a turn. A one-shot turn is accounted inside
		// Service.RunStream and must not be counted a second time.
		//
		// A codex or agy pane (resumePane) is the exception: its ResumeEngine runs
		// each turn through RunStream, which writes the Account pair itself.
		resumePane := dispReq.Interactive && ch.interactiveMgr != nil && runtimeName != "claude"
		interactiveTurns := dispReq.Interactive && ch.interactiveMgr != nil && !resumePane

		// routedRuntime is the runtime the router sent a one-shot turn to, for
		// forgetting a dead native session of that runtime afterwards.
		var routedRuntime atomic.Value
		onChunk := func(chunk dispatch.StreamChunk) {
			ev := SSEEvent{
				SessionID:      dispReq.SessionID,
				ConversationID: conversationID,
				Type:           chunk.Type,
				Text:           chunk.Text,
				TS:             time.Now().UTC().Format(time.RFC3339Nano),
			}
			switch chunk.Type {
			case "route":
				// The router's decision, first chunk of a one-shot turn (K-148).
				// Persisted and sent by emitRoute, with the handoff notice when
				// this turn follows a runtime switch.
				if chunk.Route != nil {
					routedRuntime.Store(chunk.Route.Runtime)
					ch.emitRoute(dispReq.SessionID, conversationID, capturedOperatorID,
						noteRefusedOverride(routeViewFrom(chunk.Route, routePinned), routePinned, overrideRuntime), handoffInfo)
				}
				return

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
				ev.RuntimeResolved = chunk.RuntimeResolved

				// Update fleet registry status from summary exit_code.
				if ch.registry != nil {
					if code == 0 {
						ch.registry.UpdateStatus(dispReq.SessionID, dispatch.StatusFinished)
					} else {
						ch.registry.UpdateStatus(dispReq.SessionID, dispatch.StatusFailed)
					}
				}
				outcome.summary(code)
				if interactiveTurns {
					ch.turns.finish(conversationID, chunk)
				}

				// Continuity (K-132): remember claude's own session id so the
				// next one-shot turn of this conversation can --resume it. Not
				// in worktree mode: a per-turn worktree is a different working
				// directory each time, and claude files sessions by directory.
				// Not for an interactive session either (K-136): its process still
				// holds that session, and a one-shot --resume of the same id while
				// it is open would have two claude processes writing one session.
				// Every runtime keeps its own session id (K-148); a codex or agy pane
				// stores its ids through its ResumeEngine instead.
				if chunk.NativeSessionID != "" && isKnownRuntime(chunk.RuntimeResolved) && capturedWorktreeOverride == "" && !interactiveTurns && !resumePane {
					if err := ch.transcripts.SetNativeSession(conversationID, chunk.RuntimeResolved, chunk.NativeSessionID, capturedOperatorID); err != nil {
						slog.Warn("consoleui: store native session id", "conversation", conversationID, "err", err)
					}
				}

				// Append coalesced assistant turn, then summary turn. The buffer
				// holds one turn's text: an interactive session emits a summary for
				// every turn, so it is emptied here, or each stored assistant entry
				// after the first would repeat all the turns before it (K-136; a
				// one-shot dispatch has a single summary, which hid this).
				if text := assistantBuf.String(); text != "" {
					_ = ch.transcripts.Append(TranscriptEntry{
						SessionID:      dispReq.SessionID,
						ConversationID: conversationID,
						OperatorID:     capturedOperatorID,
						Role:           RoleAssistant,
						Text:           text,
					})
				}
				assistantBuf.Reset()
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
				if interactiveTurns {
					ch.turns.noteOutput(conversationID, len(chunk.Text))
				}

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

			case "ask_user_question":
				// Record the pending question server-side for forged-answer prevention
				// (P2c).  The pending-question store is keyed by conversationID so the
				// /api/chat/answer handler can validate toolUseID and option labels before
				// forwarding the answer to the engine.
				//
				// ask_user_question SSE is OWNER-PRIVATE: ChatHub.Route already enforces
				// this via the ev.Type == "ask_user_question" carve-out, even on shared
				// sessions.  We set the SSE fields here for the SSE delivery.
				ev.AskToolUseID = chunk.AskToolUseID
				ev.AskQuestionsJSON = chunk.AskQuestionsJSON
				if ch.pendingQuestions != nil && chunk.AskToolUseID != "" {
					ch.pendingQuestions.Set(conversationID, chunk.AskToolUseID, chunk.AskQuestionsJSON)
				}
			}
			ch.hub.Route(ev)
		}

		// An interactive pane's route is decided above, once, and sent here before
		// its engine starts; a one-shot turn gets its route chunk from RunStream.
		if dispReq.Interactive && interactiveRefusal != nil {
			if refused, ok := dispatch.AsRouteRefused(interactiveRefusal); ok {
				dispatch.NewAccount(dispatch.Request{AgentName: dispReq.Agent, Project: ch.workspaceRoot,
					OperatorID: capturedOperatorID, ConversationID: conversationID, SessionID: dispReq.SessionID,
					Surface: dispatch.SurfaceConsoleChat}).
					Refuse(refused.Class, refused.Reason)
			}
			outcome.fail(-1)
			ch.failInteractiveStart(dispReq.SessionID, conversationID, capturedOperatorID, interactiveRefusal)
			sharedAtFinish = ch.hub.IsShared(dispReq.SessionID)
			return
		}
		if dispReq.Interactive && ch.interactiveMgr != nil {
			ch.emitRoute(dispReq.SessionID, conversationID, capturedOperatorID, interactiveRoute, handoffInfo)
		}

		// K-147: a codex or agy pane keeps its context across turns through a
		// ResumeEngine (one RunStream per turn, the harness's own session id
		// threaded from turn to turn). No turnLedger: RunStream accounts each turn.
		if resumePane {
			ch.runResumePane(ctx, resumePaneArgs{
				dispReq:          dispReq,
				conversationID:   conversationID,
				operatorID:       capturedOperatorID,
				runtimeName:      runtimeName,
				model:            requestedModel,
				identity:         capturedIdentityForGoroutine,
				worktreeOverride: capturedWorktreeOverride,
				onChunk:          onChunk,
				fail:             func() { outcome.fail(-1) },
			})
			sharedAtFinish = ch.hub.IsShared(dispReq.SessionID)
			return
		}

		// Interactive-P2c: when interactive=true AND structuredQuestions=true,
		// route through the SDK engine (EnsureSDK).
		//
		// Gate: sdkEngineFactory must be non-nil (--console-structured-questions +
		// node + bundle present).  If nil → 503 immediately.
		if dispReq.Interactive && dispReq.StructuredQuestions {
			if ch.sdkEngineFactory == nil {
				outcome.fail(-1)
				errText := "structured questions require --console-structured-questions (and node ≥18 + sidecar bundle)"
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

			// Budget pre-flight (K-136): the refusal a one-shot turn gets, before the
			// sidecar is started or a turn is delivered to a live one.
			if perr := ch.interactivePreflight(conversationID, dispReq.Agent); perr != nil {
				outcome.fail(-1)
				ch.failInteractiveStart(dispReq.SessionID, conversationID, capturedOperatorID, perr)
				sharedAtFinish = ch.hub.IsShared(dispReq.SessionID)
				return
			}

			sdkParams := interactive.SDKEngineParams{
				ConversationID:  conversationID,
				OwnerOperatorID: capturedOperatorID,
				OnChunk:         onChunk,
				YakosRoot:       ch.yakosRoot,
			}

			// Ensure the SDK session exists (create if new, return existing if same owner).
			sess, ensureErr := ch.interactiveMgr.EnsureSDK(conversationID, capturedOperatorID, sdkParams, *ch.sdkEngineFactory)
			if ensureErr != nil {
				outcome.fail(-1)
				errText := fmt.Sprintf("interactive: SDK start failed: %s", ensureErr.Error())
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

			// Deliver the first turn. The ledger entry is opened before the frame
			// is written, so a fast engine cannot answer before the turn is known,
			// and dropped if the frame is not delivered (K-136).
			ch.turns.register(conversationID, ch.interactiveTurnTemplate(dispReq.Agent, modelName, conversationID, capturedOperatorID, dispReq.SessionID))
			turn := ch.turns.begin(conversationID, dispReq.Task, dispReq.SessionID, capturedOperatorID)
			frame := runtime.EncodeUserTurn(dispReq.Task)
			if sendErr := ch.sendFrame(conversationID, capturedOperatorID, frame); sendErr != nil {
				ch.turns.drop(conversationID, turn)
				outcome.fail(-1)
				errText := fmt.Sprintf("interactive: SDK send failed: %s", sendErr.Error())
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

			// Wait for the SDK session to close (crash, idle reap, or cancel).
			select {
			case <-sess.Closed():
			case <-ctx.Done():
				ch.interactiveMgr.Close(conversationID)
			}
			// A turn still running when the session ended is finished as failed.
			ch.turns.closeConversation(conversationID)

			// Clear any stale pending question on session close.
			if ch.pendingQuestions != nil {
				ch.pendingQuestions.Clear(conversationID)
			}
			sharedAtFinish = ch.hub.IsShared(dispReq.SessionID)
			return
		}

		// Interactive-P1: when interactive=true (without structuredQuestions) and an
		// interactiveMgr is configured, route through the persistent CLI session
		// instead of RunStream.
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

			// Budget pre-flight (K-136): the refusal a one-shot turn gets, before the
			// claude process is started or a turn is delivered to a live one.
			if perr := ch.interactivePreflight(conversationID, dispReq.Agent); perr != nil {
				outcome.fail(-1)
				ch.failInteractiveStart(dispReq.SessionID, conversationID, capturedOperatorID, perr)
				sharedAtFinish = ch.hub.IsShared(dispReq.SessionID)
				return
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
				outcome.fail(-1)
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

			// Deliver the first turn (ledger entry opened before the write, dropped
			// if it fails; see the SDK branch above).
			ch.turns.register(conversationID, ch.interactiveTurnTemplate(dispReq.Agent, capturedModel, conversationID, capturedOperatorID, dispReq.SessionID))
			turn := ch.turns.begin(conversationID, dispReq.Task, dispReq.SessionID, capturedOperatorID)
			frame := runtime.EncodeUserTurn(dispReq.Task)
			if sendErr := ch.sendFrame(conversationID, capturedOperatorID, frame); sendErr != nil {
				ch.turns.drop(conversationID, turn)
				outcome.fail(-1)
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
			// A turn still running when the session ended is finished as failed.
			ch.turns.closeConversation(conversationID)

			sharedAtFinish = ch.hub.IsShared(dispReq.SessionID)
			return
		}

		// Continuity (K-132): a follow-up one-shot turn on claude resumes the
		// conversation's native session, so it remembers the previous turn.
		// Every runtime has its own session id (K-148): hand over all of them and
		// RunStream resumes the one it routes to, so a conversation moved to codex
		// and back finds its claude session again.
		var nativeIDs map[string]string
		if capturedWorktreeOverride == "" {
			nativeIDs = map[string]string{}
			for _, rt := range runtime.Known {
				if id := ch.transcripts.NativeSession(conversationID, rt, capturedOperatorID); id != "" {
					nativeIDs[rt] = id
				}
			}
		}

		// Knowledge pack and skill tail (K-149): non-claude runtimes only; claude
		// loads the rules natively and its argv stays as it was.
		knowledgeBlock, dispatchTask := ch.nonClaudeTurn(runtimeName, conversationID, capturedOperatorID, dispReq.Agent, dispReq.Task)

		params := dispatch.Params{
			Agent:     dispReq.Agent,
			Task:      dispatchTask,
			Knowledge: knowledgeBlock,
			// The request's own runtime ("" for auto) and model, not the values
			// resolved above for validation: the dispatcher resolves both again
			// against the runtime it actually picks, so an alias follows a
			// fallback and an auto pane lands where the agent's pin says.
			Runtime:        requestedRuntime,
			Model:          requestedModel,
			NativeSessions: nativeIDs,
			EmitRoute:      true,
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
			Surface:         dispatch.SurfaceConsoleChat,
		}

		res, err := ch.svc.RunStream(ctx, params, onChunk)
		// A saved session can disappear (claude prunes old ones, or the project
		// moved). The failed resume leaves the stored id pointing at nothing, so
		// forget it and let the next turn start a fresh session rather than fail
		// the same way forever. See forgetDeadResume for how a dead session is
		// recognised.
		if ctx.Err() == nil {
			rt, _ := routedRuntime.Load().(string)
			if rt == "" {
				rt = res.Runtime
			}
			if nativeIDs[rt] != "" {
				ch.forgetDeadResume(conversationID, capturedOperatorID, rt, res)
			}
		}
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				slog.Error("consoleui: chat RunStream error",
					"session", dispReq.SessionID,
					"agent", dispReq.Agent,
					"err", err,
				)
				outcome.fail(-1)

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

// resumeFailureLimit is how many turns in a row may fail while resuming a stored
// session before the id is forgotten whatever the failure said.
const resumeFailureLimit = 2

// resumeTargetGone reports whether text (the CLI's stderr tail) says the
// conversation or session it was asked to resume does not exist. claude 2.1.289
// prints "No conversation found with session ID: <id>"; the match is on the
// meaning, a "conversation" or "session" that is "not found", "unknown", gone or
// missing, so a reworded message still counts. A false positive costs the
// remembered context of one conversation, and only on a turn that already failed.
func resumeTargetGone(text string) bool {
	t := strings.ToLower(text)
	if !strings.Contains(t, "conversation") && !strings.Contains(t, "session") {
		return false
	}
	for _, marker := range []string{
		"not found", "no conversation", "no session", "no such", "does not exist",
		"doesn't exist", "unknown", "expired", "could not find", "couldn't find",
		"cannot find", "can't find", "not exist", "no longer",
	} {
		if strings.Contains(t, marker) {
			return true
		}
	}
	return false
}

// forgetDeadResume is called after a turn that resumed the conversation's stored
// claude session. A failed turn (non-zero exit) is counted; the stored id is
// forgotten when the failure says the session is gone, or when
// resumeFailureLimit turns in a row have failed, so a CLI that rewords its
// message cannot leave a dead id failing every follow-up. One failure that does
// not look like a missing session (a rate limit, a network error) keeps the id.
// A successful turn stores its own id, which resets the count.
func (ch *chatHandlers) forgetDeadResume(conversationID, operatorID, rt string, res dispatch.Result) {
	if res.ExitCode == 0 {
		return
	}
	n, err := ch.transcripts.NoteResumeFailure(conversationID, rt, operatorID)
	if err != nil {
		slog.Warn("consoleui: count resume failure", "conversation", conversationID, "err", err)
		return
	}
	if !resumeTargetGone(res.StderrTail) && n < resumeFailureLimit {
		return
	}
	if clrErr := ch.transcripts.ClearNativeSession(conversationID, rt, operatorID); clrErr != nil {
		slog.Warn("consoleui: clear native session id", "conversation", conversationID, "err", clrErr)
	}
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
	// Authenticated (mTLS cert / session): cert CN or username wins; body operatorId ignored.
	// Unauthenticated loopback with stable label: resolver-stamped ID wins; body ignored.
	// Legacy (no resolver label): require and validate operatorId from body.
	cancelID := netid.IdentityFrom(r.Context())
	var effectiveOperatorID string
	if cancelID.Authenticated {
		effectiveOperatorID = cancelID.OperatorID
	} else if cancelID.OperatorID != "" {
		// Loopback path: stable server-derived label; no body validation needed.
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
	// Authenticated (mTLS cert / session): cert CN or username scopes the
	//   transcript; query param ignored.
	// Unauthenticated loopback with stable label: resolver-stamped ID wins;
	//   query param ignored.
	// Legacy (no resolver label): require and validate operatorId from query param.
	transcriptID := netid.IdentityFrom(r.Context())
	var operatorID string
	if transcriptID.Authenticated {
		operatorID = transcriptID.OperatorID
	} else if transcriptID.OperatorID != "" {
		// Loopback path with stable server-derived label.
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
// conversationId (primary key, required): the stable conversation identifier.
// shared (required): the desired shared state — explicit set rather than
//
//	blind toggle avoids races when the client retries.
//
// operatorId: required on the loopback bearer path; ignored when cert CN is
//
//	available (C1 dual-regime, same as /api/chat/send and /api/chat/answer).
//
// The share state is keyed on conversationId and persists across session
// boundaries: a pane may be shared or unshared whether or not a live session
// is currently active.
//
// Idempotency: the endpoint is idempotent — POSTing the same {conversationId,
// shared} twice returns 200 both times with no side-effect on the second call.
type ShareRequest struct {
	ConversationID string `json:"conversationId"`
	OperatorID     string `json:"operatorId"`
	Shared         bool   `json:"shared"`
}

// handleChatShare sets the shared flag on a conversation.
//
// Unlike the old session-keyed SetShared, this endpoint operates on the stable
// conversationId so it works whether or not a live session is currently active.
//
// Response codes:
//   - 200 OK:      {"shared": bool, "warning": string (omitempty)}
//   - 400 Bad Request: missing/invalid fields or unknown JSON fields.
//   - 403 Forbidden:  caller is not the conversation owner.
//   - 405 Method Not Allowed: non-POST request.
//
// Note: 404 is NOT returned for "no live session" — the whole point of this
// endpoint is that sharing works when idle.  403 is returned when the caller
// is not the owner (consistent with /api/chat/answer's non-owner response,
// using 403 rather than 404 because conversationId existence is not secret for
// the authenticated caller who owns the conversation).
func (ch *chatHandlers) handleChatShare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ShareRequest
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

	// C1 Share-pane confidentiality fix:
	// Authenticated (mTLS cert): cert CN is used for the ownership check;
	// body operatorId is silently ignored.
	// Unauthenticated (loopback bearer): require and validate operatorId from body.
	shareID := netid.IdentityFrom(r.Context())
	var effectiveOperatorID string
	if shareID.Authenticated {
		// Cert CN wins; body operatorId cannot override it.
		effectiveOperatorID = shareID.OperatorID
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

	// Ownership verification for conversations with no hub-established entry
	// (hub restarted, or this is the very first share call for this conversation):
	// consult the transcript's first user-turn to determine the true owner.
	// If the transcript exists and its owner does not match effectiveOperatorID,
	// reject 403 before writing to the hub.  If there is no transcript (a new
	// conversation not yet dispatched) or it has no user turn, we allow the call:
	// SetConversationShared will record effectiveOperatorID as the owner and
	// subsequent calls will enforce it.
	//
	// A transcript that exists but cannot be read fails closed, like the dispatch
	// gate: passing would let any operator claim a conversation they do not own
	// as the owner of its share state. 500, and the reason is logged once.
	if _, _, hasEntry := ch.hub.GetConversationShared(req.ConversationID); !hasEntry {
		transcriptOwner, ownerErr := ch.transcripts.FirstUserOwner(req.ConversationID)
		if ownerErr != nil {
			slog.Error("consoleui: cannot establish the conversation owner; refusing the share call",
				"conversation", req.ConversationID, "err", ownerErr)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if transcriptOwner != "" && transcriptOwner != effectiveOperatorID {
			http.Error(w, "forbidden: conversation owned by different operator", http.StatusForbidden)
			return
		}
	}

	// Atomic ownership check + conversation-level shared-flag update.
	// SetConversationShared:
	//   - Establishes ownership on first call (shared=true only; unshare is a no-op
	//     when no entry exists, since the entry was already deleted or never created).
	//   - Rejects errConversationNotOwned (→ 403) on operator mismatch.
	//   - Rejects errTooManySharedConversations (→ 429) when the map cap is full.
	//   - Propagates the flag into any currently-open sessions for this conversation.
	if err := ch.hub.SetConversationShared(req.ConversationID, effectiveOperatorID, req.Shared); err != nil {
		if errors.Is(err, errConversationNotOwned) {
			http.Error(w, "forbidden: conversation owned by different operator", http.StatusForbidden)
			return
		}
		if errors.Is(err, errTooManySharedConversations) {
			http.Error(w, "too many shared conversations", http.StatusTooManyRequests)
			return
		}
		slog.Error("consoleui: SetConversationShared", "conversationID", req.ConversationID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// When promoting to shared, include a safety warning that tool output (bash
	// stdout, file contents) and model thinking will be visible to ALL watchers.
	// This is a contractual safety mechanic — not optional.
	type shareResponse struct {
		Shared  bool   `json:"shared"`
		Warning string `json:"warning,omitempty"`
	}
	resp := shareResponse{Shared: req.Shared}
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
//   - 429 Too Many Requests: the session's agent is at its budget hard stop
//     (K-136); the body is the one-shot turn's refusal text. Only the session's
//     owner gets it; another operator gets the 403 above.
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

	// C1 dual-regime operator_id (same logic as dispatch handler — see that
	// comment for the full rationale on the three-branch pattern).
	capturedIdentity := netid.IdentityFrom(r.Context())
	var effectiveOperatorID string
	if capturedIdentity.Authenticated {
		effectiveOperatorID = capturedIdentity.OperatorID
	} else if capturedIdentity.OperatorID != "" {
		// Loopback path: stable server-derived label; no body validation needed.
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

	// Budget pre-flight (K-136): every follow-up turn is a launch, so an agent in
	// hard_stop is refused here as a one-shot turn is, keyed on the agent the
	// session is pinned to. Only the session's owner is checked: anyone else falls
	// through to the engine's own refusal (403) and learns nothing about the
	// owner's agent or budget. The refusal is a 429 whose body the pane shows, and
	// an error turn in the transcript, like the one-shot path's.
	// A ResumeEngine pane is neither pre-flighted nor ledgered here: its RunStream
	// turn runs the budget check and writes the Account pair itself.
	ownTurns := ch.interactiveMgr.AccountsOwnTurns(req.ConversationID, effectiveOperatorID)
	if agent, owner, ok := ch.turns.sessionOf(req.ConversationID); ok && !ownTurns && owner == effectiveOperatorID {
		if perr := dispatch.PreflightBudget(agent, ch.workspaceRoot); perr != nil {
			slog.Warn("consoleui: interactive chat send refused", "conversation", req.ConversationID, "agent", agent, "err", perr)
			errText := "dispatch failed: " + perr.Error()
			_ = ch.transcripts.Append(TranscriptEntry{
				SessionID:      req.SessionID,
				ConversationID: req.ConversationID,
				OperatorID:     effectiveOperatorID,
				Role:           RoleError,
				Text:           errText,
			})
			http.Error(w, errText, http.StatusTooManyRequests)
			return
		}
	}

	// The turn's ledger entry is opened before the frame is written and dropped
	// when the engine refuses it, so a refused send (404/403/409/500) leaves no
	// event and a delivered one always finishes as a pair (K-136).
	var turn *pendingTurn
	if !ownTurns {
		turn = ch.turns.begin(req.ConversationID, req.Text, req.SessionID, effectiveOperatorID)
	}
	frame := runtime.EncodeUserTurn(req.Text)
	err := ch.interactiveSend.Send(req.ConversationID, effectiveOperatorID, frame)
	if err != nil {
		if !ownTurns {
			ch.turns.drop(req.ConversationID, turn)
		}
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

// ---- POST /api/chat/answer -------------------------------------------------

// AnswerRequest is the JSON body for POST /api/chat/answer.
//
// Delivers the operator's answer to an AskUserQuestion tool call (P2c).
// The caller must be the conversation OWNER (403 for non-owners).
//
// Security notes:
//   - operatorId is resolved from the cert CN (mTLS) or from the body
//     (cooperative-label path), identical to /api/chat/send.
//   - toolUseId MUST match the session's currently-pending question (404 on
//     mismatch; single-use: a second answer returns 409).
//   - answers keys must be declared option labels/keys from the original
//     ask_user_question (unknown options → 400).
//   - multiSelect is enforced: at most 1 answer for single-select questions.
//   - response and answers are size-bounded to dispatch.MaxTaskBytes.
//   - questionsJSON is NOT accepted from the client; the server-recorded value
//     from pendingQuestionStore is always used (forged-answer prevention).
//
// Idempotency-Key: not declared — each answer is one-time-use by construction
// (the pending entry is cleared on acceptance; retrying returns 409).
type AnswerRequest struct {
	ConversationID  string            `json:"conversationId"`
	OperatorID      string            `json:"operatorId"`
	SessionID       string            `json:"sessionId,omitempty"`
	ToolUseID       string            `json:"toolUseId"`
	Answers         map[string]string `json:"answers"` // question text → chosen option label
	Response        string            `json:"response,omitempty"`
	AnnotationsJSON string            `json:"annotations,omitempty"`
	// QuestionsJSON is intentionally absent: the server always uses the value
	// recorded by pendingQuestionStore when the ask_user_question SSE was emitted.
	// Clients cannot substitute this value.
}

// handleChatAnswer delivers the operator's answer to an AskUserQuestion tool call.
//
// Response codes:
//   - 202 Accepted: answer delivered.
//   - 400 Bad Request: missing/invalid fields, unknown option, multiSelect violation.
//   - 403 Forbidden: caller is not the conversation owner.
//   - 404 Not Found: no live session OR toolUseId does not match pending question
//     (404 for mismatch so we don't leak pending state).
//   - 409 Conflict: question already answered (single-use enforcement).
//   - 501 Not Implemented: engine does not support AnswerQuestion (CLI engine).
//   - 503 Service Unavailable: interactive mode not configured.
func (ch *chatHandlers) handleChatAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if ch.interactiveMgr == nil {
		http.Error(w, "interactive mode not configured", http.StatusServiceUnavailable)
		return
	}

	// R2: cap request body before decoding to prevent memory exhaustion.
	var req AnswerRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, int64(dispatch.MaxTaskBytes)+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// --- Required field validation ---
	if strings.TrimSpace(req.ConversationID) == "" {
		http.Error(w, "conversationId is required", http.StatusBadRequest)
		return
	}
	if err := dispatch.ValidateIdentityField("conversation_id", req.ConversationID); err != nil {
		http.Error(w, "invalid conversationId", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.ToolUseID) == "" {
		http.Error(w, "toolUseId is required", http.StatusBadRequest)
		return
	}

	// sessionId is optional but validated when present.
	if req.SessionID != "" {
		if err := dispatch.ValidateIdentityField("session_id", req.SessionID); err != nil {
			http.Error(w, "invalid sessionId", http.StatusBadRequest)
			return
		}
	}

	// R2: Size-bound all client-supplied answer fields forwarded to the sidecar.
	if len(req.Response) > dispatch.MaxTaskBytes {
		http.Error(w, "request body too large", http.StatusBadRequest)
		return
	}
	if len(req.AnnotationsJSON) > dispatch.MaxTaskBytes {
		http.Error(w, "request body too large", http.StatusBadRequest)
		return
	}
	var answersTotal int
	for _, v := range req.Answers {
		answersTotal += len(v)
	}
	if answersTotal > dispatch.MaxTaskBytes {
		http.Error(w, "request body too large", http.StatusBadRequest)
		return
	}

	// C1 dual-regime operator_id (same logic as dispatch handler — see that
	// comment for the full rationale on the three-branch pattern).
	capturedIdentity := netid.IdentityFrom(r.Context())
	var effectiveOperatorID string
	if capturedIdentity.Authenticated {
		effectiveOperatorID = capturedIdentity.OperatorID
	} else if capturedIdentity.OperatorID != "" {
		// Loopback path: stable server-derived label; no body validation needed.
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

	// R3: Ownership check BEFORE consuming the pending entry.
	//
	// A non-owner attempt must not burn the legitimate owner's pending question.
	// Check that a live session exists and that effectiveOperatorID is its owner
	// before calling ValidateAndConsume (which marks the question consumed).
	exists, owned := ch.interactiveMgr.IsOwner(req.ConversationID, effectiveOperatorID)
	if !exists {
		http.Error(w, "no live session for this conversationId", http.StatusNotFound)
		return
	}
	if !owned {
		http.Error(w, "forbidden: session owned by different operator", http.StatusForbidden)
		return
	}

	// --- Forged-answer prevention: validate toolUseID and single-use ---
	// ValidateAndConsume checks:
	//   1. A pending question exists for this conversationID.
	//   2. toolUseID matches exactly (mismatch → errPendingNoPendingQuestion → 404).
	//   3. Single-use: entry already consumed → errPendingAlreadyConsumed → 409.
	// Option values are NOT restricted to declared menu labels — custom/free-text
	// answers are a first-class feature (add-your-own path) and must not be rejected.
	// On success the pending entry is marked consumed and questionsJSON is returned.
	// questionsJSON is ALWAYS from server-recorded state; the client cannot supply it.
	if ch.pendingQuestions == nil {
		// Safety: pendingQuestions is always set by New(); this guard is defensive.
		http.Error(w, "answer service not configured", http.StatusServiceUnavailable)
		return
	}
	questionsJSON, pqErr := ch.pendingQuestions.ValidateAndConsume(req.ConversationID, req.ToolUseID, req.Answers)
	if pqErr != nil {
		switch {
		case errors.Is(pqErr, errPendingNoPendingQuestion):
			// 404: no matching pending question.  We intentionally do not distinguish
			// "no question at all" from "wrong toolUseId" to avoid leaking state.
			// Note: option-value enumeration is NOT enforced here — custom/free-text
			// answers are a first-class feature (add-your-own path).
			http.Error(w, "not found", http.StatusNotFound)
		case errors.Is(pqErr, errPendingAlreadyConsumed):
			http.Error(w, "question already answered", http.StatusConflict)
		case errors.Is(pqErr, errPendingMultiSelectViolation):
			http.Error(w, pqErr.Error(), http.StatusBadRequest)
		default:
			slog.Error("consoleui: pending question validate", "err", pqErr)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	// --- Forward answer to engine ---
	// questionsJSON is the server-recorded value from pendingQuestionStore; never
	// overridden by anything from the client request (R1).
	answer := interactive.QuestionAnswer{
		Answers:         req.Answers,
		Response:        req.Response,
		AnnotationsJSON: req.AnnotationsJSON,
		QuestionsJSON:   questionsJSON,
	}

	if err := ch.interactiveMgr.AnswerQuestion(req.ConversationID, effectiveOperatorID, req.ToolUseID, answer); err != nil {
		switch {
		case errors.Is(err, interactive.ErrNoSession):
			http.Error(w, "no live session for this conversationId", http.StatusNotFound)
		case errors.Is(err, interactive.ErrOwnerConflict):
			http.Error(w, "forbidden: session owned by different operator", http.StatusForbidden)
		case errors.Is(err, interactive.ErrAnswerUnsupported):
			http.Error(w, "engine does not support structured questions (CLI engine; use structuredQuestions:true)", http.StatusNotImplemented)
		default:
			slog.Error("consoleui: chat answer error",
				"conversationID", req.ConversationID,
				"toolUseID", req.ToolUseID,
				"err", err,
			)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	// Append the question + answer to the transcript so the conversation record
	// reflects it.  The answer is recorded as a "question_answer" role entry.
	answerJSON, _ := json.Marshal(req.Answers)
	_ = ch.transcripts.Append(TranscriptEntry{
		SessionID:      req.SessionID,
		ConversationID: req.ConversationID,
		OperatorID:     effectiveOperatorID,
		Role:           RoleQuestionAnswer,
		Text:           string(answerJSON),
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

// sdkPaneToleratesRouteError reports whether a router error may be left to the
// SDK engine's own start gate: the pane is a structured-questions (SDK) pane on
// claude, the error is the claude runtime being unavailable on this machine, and
// it is not a sensitive refusal or a project disable.
func sdkPaneToleratesRouteError(structured bool, runtimeName string, err error) bool {
	if !structured || runtimeName != "claude" {
		return false
	}
	if _, refused := dispatch.AsRouteRefused(err); refused {
		return false
	}
	var ex *dispatch.ExplicitRuntimeError
	if !errors.As(err, &ex) {
		return false
	}
	return ex.Runtime == "claude" && ex.Reason != dispatch.DisabledByProjectReason
}
