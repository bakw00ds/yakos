package dispatch

import (
	"context"
	"fmt"
	"os/user"
	"time"

	"github.com/bakw00ds/yakos/internal/wsbus"
)

// defaultMaxConcurrent is the default cap on simultaneous in-flight dispatches
// across ALL transports (gRPC + REST + JSON-RPC + MCP). The cap prevents
// fork-bombing when many transports fire concurrently. It is intentionally
// conservative; operators can raise it via ServiceConfig.
const defaultMaxConcurrent = 8

// ServiceConfig holds tunable parameters for Service.
type ServiceConfig struct {
	// WorkspaceRoot is the default project path when a request omits Project.
	WorkspaceRoot string

	// YakosRoot is the yakOS framework root (required for agent composition).
	YakosRoot string

	// Bus, when non-nil, receives dispatch lifecycle events for the WS event
	// stream.  Callers that do not need the bus may leave this nil.
	Bus *wsbus.Bus

	// MaxConcurrent is the maximum number of simultaneous in-flight dispatches.
	// 0 means use defaultMaxConcurrent (8).
	MaxConcurrent int

	// OperatorID is stamped on every dispatch that does not supply its own
	// OperatorID. Typically derived from the OS user at daemon startup.
	// Empty is valid (legacy / headless callers).
	OperatorID string
}

// Params is the transport-agnostic input to Service.Run.
// Each transport (gRPC, REST, JSON-RPC, MCP) maps its own request shape
// onto this struct before calling the facade.
type Params struct {
	Agent   string
	Task    string
	Project string // empty → Config.WorkspaceRoot
	Runtime string
	Model   string
	Timeout int

	// Identity — supplied by the transport layer.
	// Empty fields fall back to Config.OperatorID / YAKOS_CONVERSATION_ID env.
	OperatorID     string
	ConversationID string
	SessionID      string
}

// Service is the single chokepoint for all dispatch invocations. It:
//   - Stamps identity (OperatorID/ConversationID/SessionID) onto each Request.
//   - Enforces a global concurrency governor so gRPC/REST/JSON-RPC/MCP cannot
//     collectively exhaust the host by forking unbounded concurrent dispatches.
//   - Publishes lifecycle events onto the WS bus when configured.
//   - Delegates to dispatch.Run for the actual execution.
//
// Callers must construct Service via NewService; the zero value is not valid.
type Service struct {
	cfg  ServiceConfig
	sem  chan struct{} // bounded semaphore (cap == MaxConcurrent)
	opID string        // resolved daemon-level operator ID
}

// NewService constructs a Service.  cfg.YakosRoot must be non-empty for
// dispatches that require agent composition.
func NewService(cfg ServiceConfig) *Service {
	cap := cfg.MaxConcurrent
	if cap <= 0 {
		cap = defaultMaxConcurrent
	}
	s := &Service{cfg: cfg, sem: make(chan struct{}, cap)}
	// Pre-fill the semaphore: N tokens = N slots available.
	for i := 0; i < cap; i++ {
		s.sem <- struct{}{}
	}
	s.opID = cfg.OperatorID
	if s.opID == "" {
		s.opID = mintOperatorID()
	}
	return s
}

// Run executes a dispatch through the facade.
//
// Identity precedence:
//  1. p.OperatorID / p.ConversationID / p.SessionID (per-request; transport sets these).
//  2. Service.opID (daemon-level default derived from OS user at startup).
//  3. YAKOS_CONVERSATION_ID env var (legacy bash/CLI fallback inside dispatch.Run).
//
// Concurrency: if the governor cap is reached, Run blocks until a slot is
// available or ctx is cancelled (returns a clear "at capacity" error on cancel).
//
// This is the ONLY place that builds a dispatch.Request and calls dispatch.Run.
// All transports must go through here.
func (s *Service) Run(ctx context.Context, p Params) (stdout []byte, result Result, err error) {
	// --- Resolve project and yakos root ---
	project := p.Project
	if project == "" {
		project = s.cfg.WorkspaceRoot
	}
	yakosRoot := s.cfg.YakosRoot

	// --- Stamp identity ---
	operatorID := p.OperatorID
	if operatorID == "" {
		operatorID = s.opID
	}
	// ConversationID and SessionID are pass-through; env-var fallback for
	// ConversationID happens inside dispatch.Run (preserved for legacy callers).

	req := Request{
		AgentName:      p.Agent,
		Task:           p.Task,
		Project:        project,
		Runtime:        p.Runtime,
		Model:          p.Model,
		YakosRoot:      yakosRoot,
		Timeout:        p.Timeout,
		OperatorID:     operatorID,
		ConversationID: p.ConversationID,
		SessionID:      p.SessionID,
	}

	// --- Acquire governor slot ---
	select {
	case <-s.sem:
		// Acquired a slot; release it when done.
		defer func() { s.sem <- struct{}{} }()
	case <-ctx.Done():
		return nil, Result{}, fmt.Errorf("dispatch: service at capacity, request cancelled: %w", ctx.Err())
	}

	// --- Bus: dispatch started ---
	if s.cfg.Bus != nil {
		s.cfg.Bus.Publish(wsbus.TopicDispatchStarted, wsbus.DispatchStartedPayload{
			Agent:   p.Agent,
			Project: project,
			TS:      time.Now().UTC(),
		})
	}

	// --- Execute ---
	stdout, result, err = Run(ctx, req)

	// --- Bus: dispatch finished ---
	if s.cfg.Bus != nil {
		exitCode := result.ExitCode
		if err != nil {
			exitCode = -1
		}
		s.cfg.Bus.Publish(wsbus.TopicDispatchFinished, wsbus.DispatchFinishedPayload{
			Agent:    p.Agent,
			Project:  project,
			ExitCode: exitCode,
			TS:       time.Now().UTC(),
		})
	}

	return stdout, result, err
}

// mintOperatorID returns a best-effort operator identifier derived from the
// OS user. This is a Phase 2 minimal mint; the Phase 2.5 rich presence UI
// will allow operators to customise their display name / color.
// Returns an empty string if the OS user cannot be determined (non-fatal).
func mintOperatorID() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	if u.Username != "" {
		return u.Username
	}
	return u.Uid
}
