package supervisorstream

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// gateCall carries one trigger into the launch gate.
type gateCall struct {
	crossed bool // the score threshold was crossed (a launch is on the table)
	high    bool // the trigger is high-risk (sensitive path or risk regex)
	event   map[string]any
	spec    LaunchSpec // dispatch to run; zero when !crossed
	runtime string
	model   string
	agent   string // dispatch agent (budget key); "supervisor" by default
}

// gateAct is the outcome of a gate decision, taken under the lock and turned
// into log records after it is dropped.
type gateAct int

const (
	actCoalesced    gateAct = iota + 1 // a run is in flight: the trigger joins one follow-up
	actRecorded                        // high-risk event below the threshold: recorded only
	actDenied                          // the launch was refused; see gateResult.deny
	actDeferred                        // launch deferred to the end of the interval
	actLaunched                        // wrapper started
	actLaunchFailed                    // spawn failed; the launch was given back
)

// gateResult is everything the report needs from the locked section.
type gateResult struct {
	act      gateAct
	deny     denyReason
	first    bool // the once-per-session flag (cap, ceiling, budget ceiling) was newly set
	kind     string
	delay    int
	staleAge int64
	pending  int
	backoff  int64
	spawnErr error
}

// launchGate is the K-117 gate: coalesce into an in-flight run, cap routine
// launches, defer inside the interval, pause after a session limit. It never
// fails the hook. Bash twin: _ss_gate.
//
// K-128: the critical section (under the shared <counter>.lock, so bash and Go
// hooks serialise) is the run-state read-modify-write plus the wrapper spawn, so
// a launch is claimed and started together and a failed spawn is rolled back
// atomically. Everything else runs outside it: the budget evaluation (it reads
// the dispatch log) before the lock, and every log record, stderr line and
// synthetic finding after it, in the same order as when it all ran under the
// lock. A hook that cannot take the lock within its ceiling journals its
// trigger for the next gate holder instead of dropping it.
//
// The budget read taken before the lock can be stale by the time the lock is held:
// a run (of this session or another) may have started, spent the limit and ended
// while this hook read and waited, and the state then shows no run in flight. The
// read is therefore stamped with the size of the dispatch log (ledgerStamp)
// before it starts, and compared with the stamp under the lock: if the log grew,
// the budget is read again under the lock, as it was before K-128, so the launch
// decision never rests on a read older than the lock.
func (h *Hook) launchGate(out *hooktype.HookOutput, in hooktype.HookInput, cfg *supervisorConfig, logFile string, c gateCall) {
	lim := resolveLimits(cfg, c.model, in.Env)
	if len(lim.ignored) > 0 {
		h.appendLog(out, in, logFile, "WARN", "pass",
			"project supervisor limit would reduce supervision; ignored (only a stricter value is accepted; loosen in ~/.yakos-state/supervisor-policy.yml)",
			map[string]any{"ignored_keys": joinKeys(lim.ignored)})
	}
	if len(lim.invalid) > 0 {
		h.appendLog(out, in, logFile, "WARN", "pass",
			"supervisor limit below its minimum is invalid; using the default",
			map[string]any{"invalid_keys": joinKeys(lim.invalid)})
	}
	key := sessionKey(hookio.SessionID(in))
	counterFile := filepath.Join(h.WorkCurrentDir, ".supervisor-counter")
	statePath := filepath.Join(h.WorkCurrentDir, ".supervisor-run."+key)
	pendingPath := filepath.Join(h.WorkCurrentDir, ".supervisor-pending."+key)
	lockPath := counterFile + ".lock"
	if c.agent == "" {
		c.agent = "supervisor"
	}

	now := h.NowFn().Unix()
	stale := int64(lim.deadline + lim.interval + 60)
	live := func(st runState) bool { return st.hasStart && now-st.start <= stale }
	budgetReady := false
	var budStamp string // the ledger stamp taken just before the last budget read
	readBudget := func() {
		budStamp = ledgerStamp()
		if budgetReadHook != nil {
			budgetReadHook()
		}
		lim.bud = h.evalBudget(c.agent, in)
		budgetReady = true
	}

	var (
		st      runState
		release func()
	)
	for {
		// The budget read is needed only for a launch decision: a crossing with
		// no live run in flight. Peek at the state without the lock (it is
		// replaced by rename, never torn) to find out ...
		if c.crossed && !budgetReady && !live(loadRunState(statePath)) {
			readBudget()
		}
		gatePause(h.WorkCurrentDir, in.Env)
		rel, locked := acquireLockAs(lockPath, "gate", lockBudget)
		if !locked {
			note := "trigger journaled for the next lock holder"
			if !journalGate(statePath, c.high, c.event) {
				note = "could not journal the trigger"
			}
			h.appendLog(out, in, logFile, "WARN", "pass",
				"launch-state lock busy or unremovable; "+note,
				map[string]any{"lock": lockPath})
			return
		}
		// The budget read and the lock wait can take seconds: the decision is
		// taken at the time the lock is held, not at the hook's start.
		now = h.NowFn().Unix()
		st = loadRunState(statePath)
		if c.crossed && !live(st) {
			// ... and re-check under it. If the run ended in between, a launch is on
			// the table after all, so evaluate the budget (outside the lock) and retry.
			if !budgetReady {
				rel()
				readBudget()
				continue
			}
			// The read was taken before the lock. If the dispatch log has grown since
			// (a run started, spent and ended while this hook waited; of this session or
			// another), the read may be stale: read again, here, under the lock.
			if ledgerStamp() != budStamp {
				readBudget()
			}
		}
		release = rel
		break
	}

	res := func() gateResult {
		defer release()
		res := gateResult{}
		if st.hasStart && !live(st) {
			res.staleAge = now - st.start
			st.hasStart = false
		}
		gateHold(in.Env)
		folded := foldGate(statePath, pendingPath, &st)
		if c.high {
			st.high++
		}
		record := func() {
			st.pending++
			appendPending(pendingPath, c.event)
		}
		var prev runState // the state before a launch's own changes, for a failed spawn
		switch {
		case st.hasStart:
			record()
			res.act = actCoalesced
		case !c.crossed:
			record()
			res.act = actRecorded
		default:
			highKind := st.high > 0
			res.kind = "routine"
			if highKind {
				res.kind = "high"
			}
			res.deny = allowLaunch(st, lim, highKind, now)
			res.act = actDenied
			switch res.deny {
			case denyBudget, denyBackoff:
				record()
			case denyBudgetCeiling:
				record()
				if st.budgetlog != 1 {
					st.budgetlog, res.first = 1, true
				}
			case denyCap:
				record()
				if st.caplog != 1 {
					st.caplog, res.first = 1, true
				}
			case denyCeiling:
				record()
				if st.ceillog != 1 {
					st.ceillog, res.first = 1, true
				}
			case denyInterval:
				res.act = actDeferred
				res.delay = lim.interval - int(now-st.last)
				prev = st
				st.launches++
				record()
				st.last, st.start, st.hasStart = now+int64(res.delay), now, true
			default:
				res.act = actLaunched
				prev = st
				if highKind {
					st.hlaunches++
				} else {
					st.launches++
				}
				st.last, st.start, st.hasStart = now, now, true
			}
		}
		// The folded records are removed only once the state they were folded into
		// is on disk.
		if st.save(statePath) == nil {
			removeFiles(folded)
		}
		if res.act == actDeferred || res.act == actLaunched {
			if err := h.startWrapper(c, lim, statePath, lockPath, pendingPath, logFile, res.delay); err != nil {
				// Give the launch back so a failed spawn neither counts nor blocks.
				st = prev
				_ = st.save(statePath)
				res.act, res.spawnErr = actLaunchFailed, err
			}
		}
		res.pending, res.backoff = st.pending, st.backoff
		return res
	}()
	h.reportGate(out, in, logFile, lim, c, key, res)
}

// reportGate writes what the gate decided: the K-117 records and the K-119
// budget notes, after the lock is dropped.
func (h *Hook) reportGate(out *hooktype.HookOutput, in hooktype.HookInput, logFile string, lim limits, c gateCall, key string, res gateResult) {
	if res.staleAge > 0 {
		h.appendLog(out, in, logFile, "WARN", "pass",
			"in-flight supervisor run is older than its deadline; treating it as dead",
			map[string]any{"age_s": res.staleAge})
	}
	switch res.act {
	case actCoalesced:
		h.appendLog(out, in, logFile, "REPORT", "pass",
			"supervisor run already in flight for this session; trigger coalesced into one follow-up",
			map[string]any{"coalesced": true, "pending": res.pending, "session_key": key})
		return
	case actRecorded:
		h.appendLog(out, in, logFile, "REPORT", "pass",
			"high-risk event recorded; the next supervisor run will cover it",
			map[string]any{"high_risk": true, "pending": res.pending})
		return
	}
	// K-128 (S3): a budget that could not be read was treated as off (fail open, as
	// documented). Say so, with the cause, ahead of the decision's own records. A
	// read that did not fail leaves the cause empty. Bash twin: _ss_gate_report.
	if cause := lim.bud.cause; cause != "" {
		h.appendLog(out, in, logFile, "WARN", "pass",
			"supervisor budget unavailable (cause: "+cause+"); failing open: this launch decision is not checked against the dollar budget",
			map[string]any{"agent": c.agent, "budget_reason": reasonBudgetUnavailable, "cause": cause})
	}
	// Budget warning: every launch decision at warning level says so. At
	// hard_stop the deny cases below say it instead (or, for a high-risk
	// launch under the ceiling, the exempt note here).
	if b := lim.bud; b.state == string(budget.StateWarning) {
		h.appendLog(out, in, logFile, "WARN", "pass",
			"supervisor budget at warning level",
			map[string]any{"agent": c.agent, "spent_usd": b.spent, "limit_usd": b.limit, "budget_reason": budget.ReasonWarning})
		out.Stderr = fmt.Appendf(out.Stderr, "supervisor-stream: supervisor budget at %.0f%% ($%.2f of $%.2f); at 100%% routine supervisor runs stop\n", b.spent/b.limit*100, b.spent, b.limit)
	} else if b.hard && res.deny == denyNone {
		h.appendLog(out, in, logFile, "WARN", "pass",
			"supervisor budget exhausted; high-risk launch allowed under the ceiling",
			map[string]any{"agent": c.agent, "spent_usd": b.spent, "limit_usd": b.limit, "ceiling_usd": b.stop, "budget_reason": budget.ReasonExhausted})
		out.Stderr = fmt.Appendf(out.Stderr, "supervisor-stream: supervisor budget exhausted ($%.2f of $%.2f); launching high-risk supervision under the $%.2f ceiling\n", b.spent, b.limit, b.stop)
	}
	findings := filepath.Join(h.WorkCurrentDir, "supervisor-findings.ndjson")
	b := lim.bud
	switch res.act {
	case actDenied:
		switch res.deny {
		case denyBudget:
			h.appendLog(out, in, logFile, "WARN", "pass",
				"supervisor budget exhausted; skipping this routine supervisor launch (high-risk events still launch)",
				map[string]any{"agent": c.agent, "spent_usd": b.spent, "limit_usd": b.limit, "budget_reason": budget.ReasonExhausted, "kind": res.kind})
			out.Stderr = fmt.Appendf(out.Stderr, "supervisor-stream: supervisor budget exhausted ($%.2f of $%.2f); routine supervisor runs are skipped until it is raised, reset, or the month rolls over (yakos budget status)\n", b.spent, b.limit)
		case denyBudgetCeiling:
			if res.first {
				h.appendLog(out, in, logFile, "WARN", "pass",
					"supervisor budget ceiling reached; high-risk launches are no longer supervised",
					map[string]any{"agent": c.agent, "spent_usd": b.spent, "ceiling_usd": b.stop, "budget_reason": budget.ReasonExhausted})
				out.Stderr = fmt.Appendf(out.Stderr, "supervisor-stream: supervisor budget ceiling ($%.2f) reached; high-risk supervisor runs are skipped\n", b.stop)
				writeSynthBudgetFinding(findings, b.stop, h.NowFn())
			}
		case denyBackoff:
			h.appendLog(out, in, logFile, "REPORT", "pass",
				"supervisor launch paused after an account session limit",
				map[string]any{"backoff_until": res.backoff})
		case denyCap:
			if res.first {
				h.appendLog(out, in, logFile, "WARN", "pass",
					"supervisor launch cap reached for this session; skipping further routine launches",
					map[string]any{"capped": true, "cap": lim.cap, "session_key": key})
				out.Stderr = fmt.Appendf(out.Stderr, "supervisor-stream: launch cap (%d) reached for this session; routine supervisor runs are skipped (high-risk events still launch)\n", lim.cap)
			}
		case denyCeiling:
			if res.first {
				h.appendLog(out, in, logFile, "WARN", "pass",
					"high-risk supervisor launch ceiling reached for this session",
					map[string]any{"ceiling": lim.ceil})
				out.Stderr = fmt.Appendf(out.Stderr, "supervisor-stream: high-risk launch ceiling (%d) reached for this session; supervisor runs are skipped\n", lim.ceil)
				writeSynthFinding(findings, lim.ceil, h.NowFn())
			}
		}
	case actDeferred:
		h.appendLog(out, in, logFile, "REPORT", "pass",
			"supervisor launch deferred to the end of the minimum interval",
			map[string]any{"throttled": true, "deferred_s": res.delay, "pending": res.pending})
	case actLaunchFailed:
		launchFailed(out, res.spawnErr)
	case actLaunched:
		h.appendLog(out, in, logFile, "REPORT", "pass",
			fmt.Sprintf("supervisor dispatch forked async (model=%s runtime=%s)", c.model, c.runtime),
			map[string]any{"dispatch": "async", "model": c.model, "runtime": c.runtime, "deadline_s": lim.deadline, "kind": res.kind})
	}
}

func (h *Hook) startWrapper(c gateCall, lim limits, statePath, lockPath, pendingPath, logFile string, delay int) error {
	spec := c.spec
	spec.Self = h.Self
	spec.State, spec.Lock, spec.Log, spec.Pending = statePath, lockPath, logFile, pendingPath
	spec.DeadlineS, spec.Cap, spec.Ceil, spec.IntervalS, spec.BackoffMin, spec.DelayS = lim.deadline, lim.cap, lim.ceil, lim.interval, lim.backoffMin, delay
	spec.Findings = filepath.Join(h.WorkCurrentDir, "supervisor-findings.ndjson")
	return h.Launch(spec)
}

func joinKeys(k []string) string {
	s := ""
	for i, v := range k {
		if i > 0 {
			s += ","
		}
		s += v
	}
	return s
}

// writeSynthFinding appends a synthetic CRITICAL finding when the high-risk
// ceiling is reached: further high-risk events are no longer supervised, so
// block_on_critical operators must be told. Bash twin: _ss_synth_finding.
func writeSynthFinding(path string, ceiling int, now time.Time) {
	appendSynth(path, now, fmt.Sprintf("High-risk supervisor launch ceiling (%d) reached for this session: further high-risk events are recorded but no longer supervised. Review the session and the pending events file.", ceiling))
}

// writeSynthBudgetFinding is the same CRITICAL alert for the dollar-budget
// ceiling, so block_on_critical operators are stopped and told. Bash twin:
// _ss_synth_budget_finding.
func writeSynthBudgetFinding(path string, ceilingUSD float64, now time.Time) {
	appendSynth(path, now, fmt.Sprintf("Supervisor dollar-budget ceiling ($%.2f) reached: further high-risk events are recorded but no longer supervised. Raise or reset the budget (yakos budget status) and review the session and the pending events file.", ceilingUSD))
}

func appendSynth(path string, now time.Time, rationale string) {
	rec := map[string]any{
		"ts": now.UTC().Format(time.RFC3339), "batch_size": 0, "scores": map[string]any{},
		"overall": "CRITICAL", "synthetic": true,
		"rationale":          rationale,
		"recommended_action": "surface_to_operator",
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec
	if err != nil {
		return
	}
	_, _ = f.Write(append(data, '\n'))
	_ = f.Close()
}

// Why a budget read failed (budgetGate.cause), and the budget_reason of the WARN
// that says so. The bash twin has three more causes (timeout, no_output, parse):
// it forks the CLI, this twin evaluates in-process, so only the spend read can
// fail here. Bash twin: _ss_bud_cause.
const (
	budgetCauseReadError    = "read_error"
	reasonBudgetUnavailable = "budget_unavailable"
)

// evalBudget reads the supervisor's dollar budget in-process (no fork). Any
// problem fails open: a zero budgetGate allows the launch, and one whose spend
// could not be read says so in cause (K-128, S3). A budget that is off (a limit
// of 0) is not a failure. The project's agent_budgets can only lower the limit,
// never loosen it (budget.Resolve).
func (h *Hook) evalBudget(agent string, in hooktype.HookInput) budgetGate {
	st, err := budget.Evaluate(agent, budget.Options{Project: h.resolveProjectDir(in), Now: h.NowFn})
	if err != nil {
		return budgetGate{cause: budgetCauseReadError}
	}
	if st.LimitUSD <= 0 {
		return budgetGate{}
	}
	return budgetGate{
		hard:  st.State == budget.StateHardStop,
		over:  st.State == budget.StateHardStop && st.SpentUSD+1e-9 >= st.StopUSD,
		state: string(st.State), spent: st.SpentUSD, limit: st.LimitUSD, stop: st.StopUSD,
	}
}

// ledgerStamp is the freshness token of the spend ledger, the dispatch log the
// budget is computed from: its size and modification time. Every dispatch event,
// spend records included, is appended to it, so the stamp changes whenever spend
// may have been recorded. It is "none" while the log does not exist yet. The bash
// twin stamps with the size alone (_ss_ledger_stamp).
func ledgerStamp() string {
	fi, err := os.Stat(statepath.DispatchLog())
	if err != nil {
		return "none"
	}
	return fmt.Sprintf("%d/%d", fi.Size(), fi.ModTime().UnixNano())
}

// seamEnv reads a test-seam variable from the hook's environment, falling back to
// the process environment.
func seamEnv(env map[string]string, key string) string {
	if v := env[key]; v != "" {
		return v
	}
	return os.Getenv(key)
}

// gateHold is a test seam: with YAKOS_TEST_SEAMS=1 it sleeps
// YAKOS_TEST_GATE_HOLD_MS between the state load and save, so a missing gate
// lock is deterministic. It reads the process environment only (a project
// .yakos.yml cannot set it) and is a no-op otherwise. Bash twin: the seam in
// _ss_gate.
func gateHold(env map[string]string) {
	if seamEnv(env, "YAKOS_TEST_SEAMS") != "1" {
		return
	}
	if ms, ok := parseDecimal(seamEnv(env, "YAKOS_TEST_GATE_HOLD_MS")); ok && ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

// budgetReadHook is a test seam: a test sets it to observe every budget read of the
// launch gate, and whether the gate lock is held at that moment. It is nil in
// production. Bash twin: the fake CLI of the stream suite's (k9) and (k10).
var budgetReadHook func()

// gatePause is a test seam: with YAKOS_TEST_SEAMS=1 and a file
// .supervisor-test-pause in the work directory, a hook about to take the gate
// lock creates .supervisor-test-reached there and waits (20 s at most) for the
// pause file to be removed. A test can then change the world between the budget
// read and the lock without racing the hook. Both names are fixed in the
// directory the hook already writes, never taken from the environment, and the
// seam is a no-op without the environment toggle. Bash twin: _ss_test_pause.
func gatePause(workDir string, env map[string]string) {
	if seamEnv(env, "YAKOS_TEST_SEAMS") != "1" {
		return
	}
	pause := filepath.Join(workDir, ".supervisor-test-pause")
	if _, err := os.Lstat(pause); err != nil {
		return
	}
	if f, err := os.OpenFile(filepath.Join(workDir, ".supervisor-test-reached"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil { //nolint:gosec
		_ = f.Close()
	}
	for i := 0; i < 400; i++ {
		if _, err := os.Lstat(pause); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// launchFailed reports a failed wrapper spawn on stderr only. Bash backgrounds
// the wrapper with `&` and cannot observe a spawn failure, so it never writes a
// "launch failed" record; Go keeps the hook log identical to bash and surfaces
// the error here instead (the launch is already rolled back by the caller).
func launchFailed(out *hooktype.HookOutput, err error) {
	out.Stderr = fmt.Appendf(out.Stderr, "%s: supervisor dispatch launch failed: %v\n", hookName, err)
}
