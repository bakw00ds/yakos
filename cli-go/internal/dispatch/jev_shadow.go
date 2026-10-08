package dispatch

// jev_shadow.go is the K-177 routing shadow: after a dispatch is routed, ask
// Jev which model tier it would suggest and write the answer to the ledger as
// tier_suggested_by_jev. It is a measurement. Nothing here is read by routing:
// the call starts in Account.Start, which runs after routeDispatch has decided,
// and the answer reaches only the dispatch_finished event.
//
// This feature sends task text off the host, so every limit is in this file:
//
//   - Off by default. It runs only when the trusted user policy ($HOME/.yakos-state,
//     never YAKOS_DISPATCH_LOG) sets routing_shadow: true; a project file can only opt out
//     (decision.ResolveRoutingShadow).
//   - The payload is three fields: the agent name, the route class and the first
//     2 KiB of the task text. Never the knowledge block, the agent's prompt, the
//     environment or credentials. A path typed in the task text is part of the
//     task and is sent unless it matches never_paths. The question set
//     allowlists exactly these three fields. The engine redacts the payload
//     again as a backstop; on this path the gate below has already skipped
//     anything that redaction would change, so that second pass never alters a
//     sent payload (the engine's own redaction is tested in internal/decision).
//   - A sensitive task is never sent. The route class is checked first; then the
//     exact sanitized payload and the whole task go through the K-140 scanner; a
//     redaction, a withheld path or a scan that cannot finish counts as
//     sensitive. The ledger records skipped_sensitive.
//   - One attempt, 3 s, no retry. Any failure is recorded as unavailable and
//     nothing else changes.
//   - Off the critical path: the call runs in its own goroutine. The only wait
//     is at ledger-write time, at most jevShadowFinishWait, and only when the
//     dispatch finished before the call did. A late answer is dropped and the
//     row says unavailable; it is not carried to a later row.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/decision"
	"github.com/bakw00ds/yakos/internal/framework"
	"github.com/bakw00ds/yakos/internal/router"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// The values of the jev_shadow ledger field.
const (
	JevShadowOK               = "ok"
	JevShadowSkippedSensitive = "skipped_sensitive"
	JevShadowUnavailable      = "unavailable"
)

// jevShadowTaskBytes is how much of the task text may leave.
const jevShadowTaskBytes = 2048

// The two time bounds are variables so tests can shrink them. Nothing else
// changes them.
var (
	// jevShadowTimeout bounds the whole call.
	jevShadowTimeout = 3 * time.Second
	// jevShadowFinishWait is the longest the ledger write waits for a call that
	// is still in flight.
	jevShadowFinishWait = 150 * time.Millisecond
)

// jevShadowStateDirProduct is what the seam holds in production. It is separate
// so a test can restore it after TestMain blanks the seam.
func jevShadowStateDirProduct() string { return statepath.TrustedDir() }

// Seams tests replace. None is configuration: the defaults are the product.
var (
	// jevShadowStateDir is the TRUSTED home state dir (empty = off). It must not
	// be statepath.Dir: that honours YAKOS_DISPATCH_LOG, which a cloned project
	// can set through a committed .claude/settings.json env block, and the
	// opt-in would then be read from a file the project planted.
	jevShadowStateDir = jevShadowStateDirProduct
	jevShadowGetenv   = os.Getenv
	// jevShadowBaseURL overrides the endpoint (empty: the Jev client's own rules,
	// which accept only https://*.typesafe.ai or loopback).
	jevShadowBaseURL string
	jevShadowHTTP    *http.Client
	jevShadowSet     = loadRoutingTierSet
)

// jevOutcome is what the ledger row records. The zero value records nothing.
type jevOutcome struct {
	Status string
	Tier   string
}

// jevShadow is one call. A nil *jevShadow is the shadow being off.
type jevShadow struct {
	mu   sync.Mutex
	out  jevOutcome
	done chan struct{} // nil when out is already final
}

// collect returns the outcome for the ledger, waiting at most jevShadowFinishWait
// for a call still in flight. Safe on nil.
func (s *jevShadow) collect() jevOutcome {
	if s == nil {
		return jevOutcome{}
	}
	if s.done != nil {
		t := time.NewTimer(jevShadowFinishWait)
		select {
		case <-s.done:
		case <-t.C:
		}
		t.Stop()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.out.Status == "" {
		return jevOutcome{Status: JevShadowUnavailable} // still in flight: late, dropped
	}
	return s.out
}

func (s *jevShadow) set(o jevOutcome) {
	s.mu.Lock()
	s.out = o
	s.mu.Unlock()
}

// startJevShadow begins the shadow for a routed request, or returns nil when the
// user has not opted in. It never blocks on the network and never panics out.
func startJevShadow(req Request) (s *jevShadow) {
	defer func() {
		if recover() != nil {
			s = nil
		}
	}()
	stateDir := jevShadowStateDir()
	if stateDir == "" || req.Task == "" {
		return nil
	}
	cfg := decision.ResolveRoutingShadow(stateDir, req.Project, jevShadowGetenv)
	if !cfg.Enabled {
		return nil
	}
	s = &jevShadow{}
	if req.RouteClass == router.ClassSensitive {
		s.out = jevOutcome{Status: JevShadowSkippedSensitive}
		return s
	}
	s.done = make(chan struct{})
	// The request is copied: the goroutine must not see later edits.
	go func(req Request) {
		defer close(s.done)
		defer func() {
			if recover() != nil {
				s.set(jevOutcome{Status: JevShadowUnavailable})
			}
		}()
		s.set(runJevShadow(req, stateDir, cfg.Config))
	}(req)
	return s
}

// jevShadowPayload is the state handed to the decision engine: exactly the
// question set's state_fields, and nothing else.
func jevShadowPayload(req Request) map[string]any {
	return map[string]any{
		"agent":        req.AgentName,
		"route_class":  req.RouteClass,
		"task_preview": cutUTF8(req.Task, jevShadowTaskBytes),
	}
}

// cutUTF8 returns at most n bytes of s, cut at a character boundary.
func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func runJevShadow(req Request, stateDir string, cfg decision.Config) jevOutcome {
	set, err := jevShadowSet(req.YakosRoot)
	if err != nil {
		return jevOutcome{Status: JevShadowUnavailable}
	}
	payload := jevShadowPayload(req)

	// Gate: the exact payload that would leave. Sanitize is the engine's own
	// first step, so the bytes scanned here are the bytes sent.
	san, stats, serr := decision.Sanitize(payload, decision.SanitizeOptions{
		Level:         cfg.Egress.Level,
		NeverPaths:    cfg.Egress.NeverPaths,
		AllowedFields: set.StateFields,
		MaxBytes:      set.MaxStateBytes,
	})
	if serr != nil {
		return jevOutcome{Status: JevShadowUnavailable}
	}
	if stats.Redactions > 0 || stats.Withheld {
		return jevOutcome{Status: JevShadowSkippedSensitive}
	}
	sent, merr := json.Marshal(san)
	if merr != nil {
		return jevOutcome{Status: JevShadowUnavailable}
	}
	// The scanner fails closed (timeout, oversize, error all give a reason), and
	// the whole task is scanned too: a secret straddling the 2 KiB cut is cut in
	// half in the payload and would not match there.
	if router.SensitiveReason(router.Input{Material: []string{string(sent), req.Task}, NeverPaths: cfg.Egress.NeverPaths}) != "" {
		return jevOutcome{Status: JevShadowSkippedSensitive}
	}

	paths := decision.StatePaths{Dir: stateDir}
	prov := &decision.Jev{
		BaseURL: jevShadowBaseURL,
		HTTP:    jevShadowHTTP,
		Getenv:  jevShadowGetenv,
		NoRetry: true,
		// Their own breaker and budget files: shadow failures or volume must not
		// open the supervisor pre-filter's breaker or spend its budget.
		Breaker: decision.NewBreaker(paths.ShadowBreaker()),
		Budget:  decision.NewBudget(paths.ShadowBudget(), cfg.Budget.MaxCallsPerSession, cfg.Budget.MaxUSDPerDay),
	}
	eng := &decision.Engine{Provider: prov, Logger: decision.NewLogger(paths.Log()), Egress: cfg.Egress, Tag: "routing-shadow"}
	session := req.SessionID
	if session == "" {
		session = req.ConversationID
	}
	if session == "" {
		session = "default"
	}
	out := eng.Execute(context.Background(), set, payload, decision.ModeShadow, session, jevShadowTimeout)
	if out.Err != nil || out.Result == nil {
		return jevOutcome{Status: JevShadowUnavailable}
	}
	ans, ok := out.Result.Answers["tier"]
	if !ok || ans.Type != "choice" || !validTier(ans.Choice) {
		return jevOutcome{Status: JevShadowUnavailable}
	}
	return jevOutcome{Status: JevShadowOK, Tier: ans.Choice}
}

// validTier is the closed set of values tier_suggested_by_jev can take.
func validTier(t string) bool { return t == "haiku" || t == "sonnet" || t == "opus" }

// loadRoutingTierSet loads the reviewed question set. The copy embedded in the
// binary wins, so a project cannot swap the question; a source checkout without
// the embedded lib falls back to <root>/lib/decisions.
func loadRoutingTierSet(yakosRoot string) (*decision.QuestionSet, error) {
	if data, err := framework.LibFS().ReadFile("embedded/decisions/" + decision.RoutingShadowSurface + ".yaml"); err == nil {
		qs, errs := decision.ParseSet(decision.RoutingShadowSurface, data)
		if len(errs) == 0 {
			return qs, nil
		}
	}
	if yakosRoot == "" {
		return nil, errors.New("no framework root")
	}
	return decision.LoadSet(filepath.Join(yakosRoot, "lib", "decisions"), decision.RoutingShadowSurface)
}
