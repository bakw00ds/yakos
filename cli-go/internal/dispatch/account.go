package dispatch

// account.go is the dispatch ledger: the ONE place that writes dispatch_started
// and dispatch_finished events (K-136).
//
// Every transport reaches it. Run (the CLI, the REST, JSON-RPC, gRPC and MCP
// facades through Service.Run, and Flows nodes), RunStream (the console chat's
// one-shot turns) and the console's interactive turns (the persistent CLI and
// Agent SDK engines, driven by consoleui) all open an Account and finish it, and
// nothing else in the tree writes these two event types to the dispatch log.
// account_single_writer_test.go keeps it that way.
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
	"math"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/runtime"
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
