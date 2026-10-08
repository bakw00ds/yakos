package dispatch

// account.go is the dispatch ledger: the ONE place that writes dispatch_started
// and dispatch_finished events (K-136).
//
// Every transport reaches it. Run (the CLI, the REST, JSON-RPC, gRPC and MCP
// facades through Service.Run, and Flows nodes), RunStream (the console chat's
// one-shot turns) and the console's interactive turns (the persistent CLI and
// Agent SDK engines, driven by consoleui) all open an Account and finish it, and
// nothing else in the tree writes these two event types to the dispatch log.
// TestAccount_IsTheOnlyWriterOfDispatchEvents keeps it that way.
//
// Why one writer. Accounting rules are the product here: tokens are the primary
// unit, dollars count only for runs billed per API call, and a subscription run's
// reported cost is kept as an API-equivalent figure and never summed as spend.
// Those rules must hold for every path, so they live in one function
// (writeFinished) rather than being repeated, and drifting, at each call site.
//
// Log hygiene. The event carries only ids and numbers the dispatcher itself
// produced or validated: model ids are bounded identifiers, the session id passes
// runtime.ValidSessionID, the surface is a short lowercase name, and the billing
// mode is a fixed constant read from the PRESENCE of a credential, never its
// value. No token, key, header or environment value is ever copied into an event
// (TestAccount_NoCredentialMaterialInEvents).

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/runtime"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// costSourceHarness marks a dollar figure the harness itself reported. No price
// is ever computed by the dispatcher (the model registry may add one later).
const costSourceHarness = "harness"

// Account is one dispatch's entry in the ledger. Create it with NewAccount, call
// Start when the dispatch begins and Finish when it ends; Finish writes
// dispatch_finished once, whatever happens, and writes dispatch_started first if
// Start was never called. That second form is for a turn whose start is only
// remembered (the console's interactive turns), so a turn that is rejected before
// it runs leaves nothing in the log and a turn that runs always leaves a pair.
//
// An Account is safe for concurrent use.
type Account struct {
	mu       sync.Mutex
	req      Request
	path     string
	started  time.Time
	opened   bool
	finished bool
}

// NewAccount creates the ledger entry for req, begun now. It writes nothing.
func NewAccount(req Request) *Account {
	return newAccountAt(req, dispatchLogPath(), time.Now())
}

// NewAccountAt is NewAccount for a dispatch that began at started, for a caller
// that learns of the dispatch only after it ran.
func NewAccountAt(req Request, started time.Time) *Account {
	return newAccountAt(req, dispatchLogPath(), started)
}

func newAccountAt(req Request, path string, started time.Time) *Account {
	return &Account{req: req, path: path, started: started}
}

// Started is when the dispatch began.
func (a *Account) Started() time.Time { return a.started }

// Start writes dispatch_started, stamped with the begin time. It is a no-op once
// the event has been written.
func (a *Account) Start() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.startLocked()
}

func (a *Account) startLocked() {
	if a.opened {
		return
	}
	a.opened = true
	writeStarted(a.req, a.started, a.path)
}

// Finish writes dispatch_finished for res, stamped now, after dispatch_started
// when that has not been written yet. Only the first call writes; a later one is
// a no-op, so a caller may finish defensively on every exit path. When
// res.DurationS is unset it is filled in from the begin time.
func (a *Account) Finish(res Result) {
	a.FinishAt(res, time.Now())
}

// FinishAt is Finish with an explicit end time.
func (a *Account) FinishAt(res Result, end time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return
	}
	a.finished = true
	a.startLocked()
	if res.DurationS == 0 {
		if d := end.Sub(a.started).Seconds(); d > 0 {
			res.DurationS = d
		}
	}
	writeFinished(a.req, res, end, a.path)
}

// Refuse writes a route_refused event: a sensitive request that no permitted
// runtime could take (K-140). It is the only event of such a dispatch (no
// dispatch_started or dispatch_finished follows), carries a fixed-vocabulary
// reason and no task text, project path or runtime output, and is written once.
func (a *Account) Refuse(class, reason string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished || a.opened {
		return
	}
	a.finished, a.opened = true, true
	ev := refusedEvent{
		Type:           "route_refused",
		Ts:             a.started.UTC().Format(time.RFC3339),
		Agent:          a.req.AgentName,
		RouteClass:     logIdent(class, 64),
		RouteReason:    logIdent(reason, 64),
		OperatorID:     a.req.OperatorID,
		ConversationID: a.req.ConversationID,
		SessionID:      a.req.SessionID,
	}
	if line, err := json.Marshal(ev); err == nil {
		_ = appendEvent(a.path, line)
	}
}

// Gateway writes the gateway_request event (K-151), once. A second call is a
// no-op. The line is built by gatewayLineJSON from bounded identifiers and
// numbers only (gateway_event.go).
func (a *Account) Gateway(ev GatewayEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return
	}
	a.finished, a.opened = true, true
	if b, ok := gatewayLineJSON(ev, a.started, time.Now()); ok {
		_ = appendEvent(a.path, b)
	}
}

// noteRefused writes the route_refused event when err is a RouteRefusedError.
func noteRefused(req Request, err error) {
	if e, ok := AsRouteRefused(err); ok {
		NewAccount(req).Refuse(e.Class, e.Reason)
	}
}

type refusedEvent struct {
	Type           string `json:"type"`
	Ts             string `json:"ts"`
	Agent          string `json:"agent"`
	RouteClass     string `json:"route_class,omitempty"`
	RouteReason    string `json:"route_reason,omitempty"`
	OperatorID     string `json:"operator_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
}

// writeStarted writes a dispatch_started event to the dispatch-log.
// Schema matches PR #40: includes the project field.
// Identity fields (operator_id, conversation_id, session_id) are emitted
// when non-empty; they are omitted on legacy dispatches so legacy readers
// continue to parse the line without error.
//
// Only Account calls it (see the file comment).
func writeStarted(req Request, ts time.Time, logPath string) {
	// Use the canonical Event struct from internal/cost to ensure schema parity.
	ev := cost.Event{
		Type:           "dispatch_started",
		Ts:             ts.UTC().Format(time.RFC3339),
		Agent:          req.AgentName,
		Runtime:        req.Runtime,
		Project:        req.Project,
		TaskPreview:    truncate(req.Task, 200),
		Model:          req.ModelResolved,
		OperatorID:     req.OperatorID,
		ConversationID: req.ConversationID,
		SessionID:      req.SessionID,
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return // not fatal
	}
	_ = appendEvent(logPath, line)
}

// writeFinished writes a dispatch_finished event to the dispatch-log, applying
// the accounting rules (ledgerFields). Only Account calls it.
func writeFinished(req Request, res Result, ts time.Time, logPath string) {
	line, err := json.Marshal(buildFinished(req, res, ts))
	if err != nil {
		return // not fatal
	}
	_ = appendEvent(logPath, line)
}

// buildFinished maps a finished dispatch onto its event.
func buildFinished(req Request, res Result, ts time.Time) finishedEvent {
	ev := finishedEvent{
		Type:            "dispatch_finished",
		Ts:              ts.UTC().Format(time.RFC3339),
		Agent:           req.AgentName,
		Runtime:         req.Runtime,
		Project:         req.Project,
		ExitCode:        res.ExitCode,
		DurationS:       res.DurationS,
		OutputBytes:     res.OutputBytes,
		TaskBytes:       res.TaskBytes,
		EstInputTokens:  res.TaskBytes / 4,
		EstOutputTokens: res.OutputBytes / 4,
		Model:           res.ModelResolved,
		ModelChosenBy:   res.ModelChosenBy,
		ModelResolved:   res.ModelResolved,
		StderrTruncated: res.StderrTrunc,
		// Identity fields: omitempty so legacy readers see no new keys.
		OperatorID:     req.OperatorID,
		ConversationID: req.ConversationID,
		SessionID:      req.SessionID,
		// Routing fields come from the request: it is stamped once by
		// routeDispatch, so every Result constructor stays untouched.
		RuntimeChosenBy: req.RuntimeChosenBy,
		FallbackFrom:    req.FallbackFrom,
	}

	// eval_run_id: null when empty, string when set.
	if res.EvalRunID != "" {
		ev.EvalRunID = res.EvalRunID
	} else {
		ev.EvalRunID = nil
	}

	// stderr_tail: null when empty (success case), string when set.
	if res.StderrTail != "" {
		ev.StderrTail = res.StderrTail
	} else {
		ev.StderrTail = nil
	}

	applyLedger(&ev, req, res)
	return ev
}

// applyLedger fills the K-136 fields of ev and decides what its usage object
// says about dollars. The rules:
//
//   - Tokens are recorded as reported, for every runtime and billing mode, except
//     that a figure the harness reported as negative is recorded as 0: a log
//     sum must never go down because of one hostile or corrupt row.
//   - billing comes from the credentials the harness inherited
//     (runtime.BillingFor), never from the request.
//   - A dollar figure the harness reported (claude's total_cost_usd) is spend only
//     for api billing: it stays in usage.total_cost_usd. For any other billing it
//     moves to api_equivalent_usd and usage.total_cost_usd is written as 0, so a
//     reader that sums usage.total_cost_usd (the budget aggregate, the bash
//     readers) never counts it as spend. cost_source says the figure is the
//     harness's.
//   - A run on a runtime this build does not know (a plugin) has no billing; its
//     row reads like a pre-K-136 row.
//   - Nothing the harness reports can stop the event being written: a cost that
//     is NaN, infinite or negative is recorded as 0 (encoding/json refuses to
//     encode a non-finite number, and the whole finished event would be lost).
//
// The caller's Result and its Usage are not modified.
func applyLedger(ev *finishedEvent, req Request, res Result) {
	runtimeName := res.Runtime
	if runtimeName == "" {
		runtimeName = req.Runtime
	}

	ev.Provider = logIdent(res.Provider, 32)
	if ev.Provider == "" {
		ev.Provider = logIdent(providerForRuntime(runtimeName), 32)
	}
	ev.ModelID = logIdent(res.ModelID, 128)
	if ev.ModelID == "" {
		ev.ModelID = logIdent(res.ModelResolved, 128)
	}
	ev.Billing = runtime.BillingFor(runtimeName)
	if runtimeName == "codex" && runtime.CodexHooksUntrusted() {
		ev.HooksUntrusted = true
	}

	if res.Usage != nil {
		u := *res.Usage
		u.InputTokens = nonNegative(u.InputTokens)
		u.OutputTokens = nonNegative(u.OutputTokens)
		u.CacheRead = nonNegative(u.CacheRead)
		u.CacheCreation = nonNegative(u.CacheCreation)
		u.DurationMs = nonNegative(u.DurationMs)
		usd := finiteUSD(u.TotalCostUSD)
		u.TotalCostUSD = usd
		if usd > 0 {
			ev.CostSource = costSourceHarness
			if !cost.CountsAsSpend(ev.Billing) {
				ev.APIEquivalentUSD = usd
				u.TotalCostUSD = 0
			}
		}
		ev.Usage = &u
	}

	if runtime.ValidSessionID(res.SessionID) {
		ev.NativeSessionID = res.SessionID
	}
	ev.Surface = logSurface(req.Surface)
	ev.RouteRule = logText(req.RouteRule, 128)
	ev.RouteReason = logText(req.RouteReason, 256)
	ev.RouteClass = logIdent(req.RouteClass, 64)
	ev.PolicySHA = logHex(req.PolicySHA, 64)
	if res.ScanFindings > 0 {
		ev.ScanFindings = res.ScanFindings
	}
	ev.CancelReason = logText(res.CancelReason, 96)
	if res.ScanOffReason == "budget" || res.ScanOffReason == "deadline" {
		ev.ScanOffReason = res.ScanOffReason // fixed strings only
	}
}

// nonNegative returns v, or 0 when v is negative.
func nonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// finiteUSD returns v when it is a finite positive dollar figure within reason,
// else 0, so a corrupt or hostile number cannot become spend or an
// API-equivalent, and cannot make the event unencodable.
func finiteUSD(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || v > 1e12 {
		return 0
	}
	return v
}

// logIdent returns s when it is a short identifier fit for a log field (an
// alphanumeric first character, then letters, digits and . _ : / @ + -, at most
// max bytes), else "". It bounds what a harness-reported name can put in the log.
func logIdent(s string, max int) string {
	if s == "" || len(s) > max {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if alnum {
			continue
		}
		if i > 0 && strings.IndexByte("._:/@+-", c) >= 0 {
			continue
		}
		return ""
	}
	return s
}

// logSurface returns s when it is a short lowercase surface name, else "".
func logSurface(s string) string {
	if s == "" || len(s) > 32 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || (i > 0 && c == '-') {
			continue
		}
		return ""
	}
	return s
}

// logHex returns s when it is a hex digest of at most max characters, else "".
func logHex(s string, max int) string {
	if s == "" || len(s) > max {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' {
			continue
		}
		return ""
	}
	return s
}

// logText returns s with control characters removed, cut to at most max bytes on
// a rune boundary. It is for the router's short human-readable reason.
//
// What is removed: the C0 and C1 control characters (so a terminal escape, a
// carriage return or a NEL cannot rewrite a line when the log is tailed), DEL,
// the Unicode format characters (the bidirectional overrides and isolates that
// reorder text on screen, the zero-width characters that hide it, the byte order
// mark), the line and paragraph separators, and invalid UTF-8.
func logText(s string, max int) string {
	if s == "" {
		return ""
	}
	clean := strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
			unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return -1
		}
		return r
	}, s)
	if len(clean) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(clean[cut]) {
			cut--
		}
		clean = clean[:cut]
	}
	return clean
}

// ConfigChange is one write to an owner-only policy file, for the audit trail.
type ConfigChange struct {
	// File is the file's base name; only the three policy files are accepted.
	File string
	// Action is a fixed-vocabulary verb such as "models.enable" or
	// "router.policy.set".
	Action string
	// SHABefore and SHAAfter are the hex SHA-256 of the file's bytes around the
	// write ("" for a file that did not exist).
	SHABefore, SHAAfter string
	// Surface is "cli" or "console".
	Surface string
	// Actor and AuthMethod say who a console write acted as: Actor is
	// "operator-browser" and AuthMethod is how the server authenticated the
	// browser ("session", "cert" or "none" for the loopback bearer token). Both
	// are set by the server from the resolved identity, never from the request.
	// Empty for a CLI write.
	Actor, AuthMethod string
}

// auditFiles are the files a ConfigChange may name: the router policy, the model
// registry overlay, the budget policy and the workflow triggers' schedules file
// (the fixed label "schedules.yml": the real name carries a project slug and a
// path hash). The event carries the base name only, never a path.
var auditFiles = map[string]bool{"router-policy.yml": true, "model-registry.yml": true, "budget-policy.yml": true, "schedules.yml": true}

type configChangedEvent struct {
	Type       string `json:"type"`
	Ts         string `json:"ts"`
	OperatorID string `json:"operator_id"`
	File       string `json:"file"`
	Action     string `json:"action"`
	SHABefore  string `json:"policy_sha_before"`
	SHAAfter   string `json:"policy_sha_after"`
	Surface    string `json:"surface"`
	Actor      string `json:"actor,omitempty"`
	AuthMethod string `json:"auth_method,omitempty"`
}

// ConfigChanged appends a config_changed event: who (the request's OperatorID),
// which policy file, what was done and the file's sha before and after. It is the
// audit line of every policy write (K-153). It refuses to record a change to a
// file that is not one of the three, and returns the error when the log cannot be
// written, so the caller can tell the operator the write went unaudited.
func (a *Account) ConfigChanged(c ConfigChange) error {
	line, err := a.configChangedLine(c)
	if err != nil {
		return err
	}
	return appendEvent(a.path, line)
}

func (a *Account) configChangedLine(c ConfigChange) ([]byte, error) {
	if !auditFiles[c.File] {
		return nil, fmt.Errorf("dispatch: %q is not an auditable policy file", c.File)
	}
	op := logIdent(a.req.OperatorID, 128)
	if op == "" {
		op = "unknown"
	}
	ev := configChangedEvent{
		Type: "config_changed", Ts: a.started.UTC().Format(time.RFC3339), OperatorID: op,
		File: c.File, Action: logIdent(c.Action, 64), SHABefore: logHex(c.SHABefore, 64), SHAAfter: logHex(c.SHAAfter, 64),
		Surface: logSurface(c.Surface), Actor: logIdent(c.Actor, 32), AuthMethod: logSurface(c.AuthMethod),
	}
	return json.Marshal(ev)
}

// ConfigAudit is the home dispatch log opened and flock-held for one policy
// write. Open it BEFORE the write so a write that cannot be audited is refused
// (K-153 sec-356 M2); Record the change through the same descriptor; Close it.
type ConfigAudit struct {
	a *Account
	f *os.File
}

// OpenConfigAudit opens the dispatch log inside stateDir (the trusted
// $HOME/.yakos-state, never the YAKOS_DISPATCH_LOG override: a project can set
// that, and a policy write must not be recordable somewhere the operator does
// not read). It fails when the log cannot be opened.
func OpenConfigAudit(req Request, stateDir string) (*ConfigAudit, error) {
	if stateDir == "" {
		return nil, errors.New("dispatch: no state directory for the audit log")
	}
	path := statepath.DispatchLogIn(stateDir)
	f, err := openLogLocked(path)
	if err != nil {
		return nil, err
	}
	return &ConfigAudit{a: newAccountAt(req, path, time.Now()), f: f}, nil
}

// Record appends the config_changed line through the held descriptor.
func (c *ConfigAudit) Record(ch ConfigChange) error {
	line, err := c.a.configChangedLine(ch)
	if err != nil {
		return err
	}
	return writeLine(c.f, line)
}

// Close releases the lock and the descriptor.
func (c *ConfigAudit) Close() { closeLogLocked(c.f) }
