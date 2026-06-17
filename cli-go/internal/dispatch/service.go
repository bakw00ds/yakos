package dispatch

import (
	"context"
	"fmt"
	"os/user"
	"regexp"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// defaultMaxConcurrent is the default cap on simultaneous in-flight dispatches
// across ALL transports (gRPC + REST + JSON-RPC + MCP). The cap prevents
// fork-bombing when many transports fire concurrently. It is intentionally
// conservative; operators can raise it via ServiceConfig.
const defaultMaxConcurrent = 8

// identityFieldRe is the allow-list for caller-supplied identity fields
// (OperatorID, ConversationID, SessionID).
//
// Rules:
//   - First character must be alphanumeric (closes the leading-dash argv
//     flag-injection vector: a value starting with '-' would be interpreted
//     as a CLI flag by claude/codex/agy).
//   - Subsequent characters may be alphanumeric, '.', '_', ':', or '-'.
//   - Maximum length is 128 characters.
//
// Validated once in Service.Run so every transport inherits the check.
var identityFieldRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:\-]{0,127}$`)

// reservedOperatorPrefixes are operator-id namespaces that non-MCP transports
// must not claim.  Only the MCP transport layer may stamp "mcp:<agent>".
// "system:" is reserved for future daemon-internal events.
var reservedOperatorPrefixes = []string{"mcp:", "system:"}

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

// IdentityCarrier wraps a netid.Identity with a Populated flag.
// When Populated is false, the Identity field is the zero value and
// Service.Run/RunStream use the cooperative-label (loopback) path unchanged.
// When Populated is true, the identity was resolved by Resolver.Middleware and
// role enforcement and dual-regime operator_id logic apply.
//
// Invariant: Populated MUST equal Identity.Resolved.  Both fields mean "an
// identity was resolved by the edge middleware."  Populated exists here as a
// convenience flag at the dispatch boundary; callers MUST set it to
// Identity.Resolved (never hardcode true or false independently).  A future
// refactor may collapse these into just netid.Identity + checking .Resolved
// directly — the Populated flag is a compatibility shim, not a separate concept.
type IdentityCarrier struct {
	Populated bool
	Identity  netid.Identity
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

	// YakosRoot overrides Config.YakosRoot for this request.
	// If empty, Service.Run uses Config.YakosRoot.
	// At least one of YakosRoot and Config.YakosRoot must be non-empty.
	YakosRoot string

	// Identity — supplied by the transport layer.
	// Empty fields fall back to Config.OperatorID / YAKOS_CONVERSATION_ID env.
	//
	// SECURITY: these fields are SELF-ASSERTED by the caller (cooperative
	// attribution/labeling only).  They MUST NOT be used for authorization
	// decisions.  See validateIdentityField for the format constraint enforced
	// at the facade before any field reaches subprocess argv.
	OperatorID     string
	ConversationID string
	SessionID      string

	// ResolvedIdentity carries the cryptographically resolved identity from the
	// console edge resolver (mTLS path) or remains zero (loopback path).
	// When Populated is true:
	//   - Role enforcement fires (RoleDispatch required).
	//   - If Authenticated is true, the cert CN overrides OperatorID.
	// When Populated is false (loopback / legacy callers), no enforcement runs
	// and cooperative-label OperatorID is used unchanged — preserving
	// today's loopback behavior as an explicit NO-OP.
	ResolvedIdentity IdentityCarrier

	// WorkDirOverride, when non-empty, overrides the subprocess working
	// directory for this dispatch. It is forwarded directly into
	// Request.WorkDirOverride and from there into the runtime adapter's
	// cmd.Dir. This field MUST be set only by trusted server-side code
	// (e.g. the IDE diff handler after validating the path against a
	// Manager-allocated worktree). It must never be derived from a client
	// request body.
	WorkDirOverride string

	// Effort is the reasoning effort level for claude dispatches.
	// Valid values: low, medium, high, xhigh, max.  Empty means no override
	// (omit the --effort flag).  This field MUST be validated by the transport
	// layer (handler) before being placed here — see ValidateEffort.
	// Only the claude adapter acts on this field; other runtimes ignore it.
	Effort string

	// isMCPStamped signals that this Params was built by the MCP transport
	// layer, not by a human-facing transport (gRPC/REST/JSON-RPC/console).
	// Only the MCP transport sets this to true; it is not derivable from the
	// wire by external callers.  Used by Service.Run to allow the "mcp:"
	// prefix in OperatorID without rejecting it as a reserved namespace claim.
	isMCPStamped bool
}

// MCPParams constructs a Params with isMCPStamped=true.  This is the only way
// to set isMCPStamped from outside the package; it is intentionally not a
// public field so non-MCP callers cannot forge the MCP attribution.
func MCPParams(p Params) Params {
	p.isMCPStamped = true
	return p
}

// runFn is the package-level function used by Service.Run to execute a
// dispatch.  It defaults to Run (the real implementation) but can be swapped
// in tests via setRunFn to inject a fake that blocks or fails on demand.
var runFn = func(ctx context.Context, req Request) ([]byte, Result, error) {
	return Run(ctx, req)
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
// YakosRoot precedence:
//  1. p.YakosRoot (per-request override from the caller).
//  2. s.cfg.YakosRoot (daemon-level default set at construction time).
//     At least one must be non-empty; Run returns an error if both are empty.
//
// Concurrency: if the governor cap is reached, Run blocks until a slot is
// available or ctx is cancelled (returns a clear "at capacity" error on cancel).
//
// Sub-dispatch nesting note: a dispatched agent that re-enters the daemon via
// any transport competes for the same semaphore slots as its parent.  Deep
// nesting can exhaust the cap (e.g. cap=8, depth=9 → deadlock-free but the
// 9th slot will block until one of the ancestors returns).  There is no
// deadlock — held slots are released when dispatch.Run returns — but extreme
// nesting will stall.  Raise MaxConcurrent or bound nesting depth at the
// application layer.  Solving nesting at the facade level is deferred to
// Phase 3.
//
// This is the ONLY place that builds a dispatch.Request and calls runFn.
// All transports must go through here.
func (s *Service) Run(ctx context.Context, p Params) (stdout []byte, result Result, err error) {
	// --- Phase 6b: role enforcement (mTLS / Resolved path only) ---
	// When ResolvedIdentity.Populated is true the request came through the
	// console edge resolver; enforce RoleDispatch as the minimum privilege.
	// When Populated is false (loopback / legacy callers), skip enforcement
	// entirely — this preserves the loopback NO-OP invariant.
	if p.ResolvedIdentity.Populated {
		if !p.ResolvedIdentity.Identity.Role.Allows(netid.RoleDispatch) {
			// Generic error returned to caller — role details stay server-side.
			// Logging the specifics here would be useful for incident investigation
			// but is deferred until the audit-log path lands (Phase 6c+).
			return nil, Result{}, fmt.Errorf("dispatch: forbidden: insufficient role")
		}
	}

	// --- Resolve project and yakos root ---
	project := p.Project
	if project == "" {
		project = s.cfg.WorkspaceRoot
	}
	yakosRoot := p.YakosRoot
	if yakosRoot == "" {
		yakosRoot = s.cfg.YakosRoot
	}
	if yakosRoot == "" {
		return nil, Result{}, fmt.Errorf("dispatch: yakos_root is required (set in ServiceConfig or per-request Params.YakosRoot)")
	}

	// --- Task size bound (facade chokepoint) ---
	// Enforced here so all transports inherit the check regardless of their
	// own frame cap.  1 MB ceiling is generous while keeping the boundary well
	// below gRPC's 4 MB frame cap; Phase 3b's SSE/REST path has no frame cap
	// at all, making this check load-bearing for that transport.
	if len(p.Task) > maxTaskBytes {
		return nil, Result{}, fmt.Errorf("dispatch: task exceeds maximum size (%d bytes; limit %d)", len(p.Task), maxTaskBytes)
	}

	// --- Validate and stamp identity ---
	// All three identity fields flow verbatim into subprocess argv
	// (claude --resume <id>, agy --conversation <id>, codex resume <id>).
	// Validate format here once so every transport inherits the protection.
	if err := validateIdentityField("operator_id", p.OperatorID); err != nil {
		return nil, Result{}, err
	}
	if err := validateIdentityField("conversation_id", p.ConversationID); err != nil {
		return nil, Result{}, err
	}
	if err := validateIdentityField("session_id", p.SessionID); err != nil {
		return nil, Result{}, err
	}

	// --- Dual-regime operator_id ---
	// Authenticated (mTLS cert) path: cert CN is authoritative; caller-supplied
	// OperatorID is silently ignored to prevent cross-operator forgery.
	// Unauthenticated (loopback bearer) path: cooperative-label path unchanged.
	var operatorID string
	if p.ResolvedIdentity.Populated && p.ResolvedIdentity.Identity.Authenticated {
		// Cert CN wins; never use caller-supplied OperatorID.
		operatorID = p.ResolvedIdentity.Identity.OperatorID
	} else {
		// Cooperative-label path (loopback or unresolved): existing logic preserved.
		operatorID = p.OperatorID
		if operatorID != "" && !p.isMCPStamped {
			for _, prefix := range reservedOperatorPrefixes {
				if strings.HasPrefix(operatorID, prefix) {
					// Drop to daemon default — not an error, but the claim is ignored.
					operatorID = ""
					break
				}
			}
		}
		if operatorID == "" {
			operatorID = s.opID
		}
	}
	// ConversationID and SessionID are pass-through; env-var fallback for
	// ConversationID happens inside dispatch.Run (preserved for legacy callers).

	req := Request{
		AgentName:       p.Agent,
		Task:            p.Task,
		Project:         project,
		Runtime:         p.Runtime,
		Model:           p.Model,
		YakosRoot:       yakosRoot,
		Timeout:         p.Timeout,
		OperatorID:      operatorID,
		ConversationID:  p.ConversationID,
		SessionID:       p.SessionID,
		WorkDirOverride: p.WorkDirOverride,
		Effort:          p.Effort,
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
	stdout, result, err = runFn(ctx, req)

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

// validEffortLevels is the closed set of --effort values the claude CLI accepts.
// Validated server-side before the value reaches subprocess argv.
var validEffortLevels = map[string]struct{}{
	"low":    {},
	"medium": {},
	"high":   {},
	"xhigh":  {},
	"max":    {},
}

// ValidateEffort checks that effort is either empty (meaning "no override") or
// one of the valid level names.  Returns a descriptive error on invalid input.
// The error message is safe to return to the caller (does not echo the value
// verbatim — value is caller-controlled; a format string would be fine here
// but the name list makes the constraint self-documenting).
func ValidateEffort(effort string) error {
	if effort == "" {
		return nil // empty → omit flag; perfectly valid
	}
	if _, ok := validEffortLevels[effort]; !ok {
		return fmt.Errorf("dispatch: invalid effort %q: must be one of low, medium, high, xhigh, max", effort)
	}
	return nil
}

// ValidateIdentityField is the exported wrapper around validateIdentityField.
// CLI callers (cmd/yakos) use this to validate YAKOS_CONVERSATION_ID and
// similar env vars before passing them to Service.Run.
func ValidateIdentityField(name, value string) error {
	return validateIdentityField(name, value)
}

// validateIdentityField checks that a non-empty identity field value matches
// the allow-list pattern and is not too long.  Returns a generic
// InvalidArgument-style error on failure (no value echo — avoids leaking
// attacker-controlled strings into error messages).
func validateIdentityField(name, value string) error {
	if value == "" {
		return nil // empty is fine; fallback logic handles it above
	}
	if !identityFieldRe.MatchString(value) {
		return fmt.Errorf("dispatch: invalid %s: must match ^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$ (leading char must be alphanumeric, max 128 chars)", name)
	}
	return nil
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
