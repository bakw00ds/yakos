package supervisorstream

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// gateCall carries one trigger into the launch gate.
type gateCall struct {
	crossed bool // the score threshold was crossed (a launch is on the table)
	high    bool // the trigger is high-risk (sensitive path or risk regex)
	event   map[string]any
	spec    LaunchSpec // dispatch to run; zero when !crossed
	runtime string
	model   string
}

// launchGate is the K-117 gate: coalesce into an in-flight run, cap routine
// launches, defer inside the interval, pause after a session limit. It runs
// under the counter's mkdir lock so bash and Go hooks serialize, and it never
// fails the hook. Bash twin: _ss_gate.
func (h *Hook) launchGate(out *hooktype.HookOutput, in hooktype.HookInput, cfg *supervisorConfig, logFile string, c gateCall) {
	lim := resolveLimits(cfg, c.model, in.Env)
	if len(lim.ignored) > 0 {
		h.appendLog(out, logFile, "WARN", "pass",
			"project supervisor limit would reduce supervision; ignored (only a stricter value is accepted; loosen in ~/.yakos-state/supervisor-policy.yml)",
			map[string]any{"ignored_keys": joinKeys(lim.ignored)})
	}
	if len(lim.invalid) > 0 {
		h.appendLog(out, logFile, "WARN", "pass",
			"supervisor limit below its minimum is invalid; using the default",
			map[string]any{"invalid_keys": joinKeys(lim.invalid)})
	}
	key := sessionKey(hookio.SessionID(in))
	counterFile := filepath.Join(h.WorkCurrentDir, ".supervisor-counter")
	statePath := filepath.Join(h.WorkCurrentDir, ".supervisor-run."+key)
	pendingPath := filepath.Join(h.WorkCurrentDir, ".supervisor-pending."+key)
	lockPath := counterFile + ".lock"

	release, locked := acquireLock(lockPath)
	if !locked {
		h.appendLog(out, logFile, "WARN", "pass",
			"launch-state lock busy or unremovable; skipping this supervisor launch",
			map[string]any{"lock": lockPath})
		return
	}
	defer release()
	now := h.NowFn().Unix()
	st := loadRunState(statePath)
	if st.hasStart && now-st.start > int64(lim.deadline+lim.interval+60) {
		h.appendLog(out, logFile, "WARN", "pass",
			"in-flight supervisor run is older than its deadline; treating it as dead",
			map[string]any{"age_s": now - st.start})
		st.hasStart = false
	}
	if c.high {
		st.high++
	}
	record := func() {
		st.pending++
		appendPending(pendingPath, c.event)
	}
	if st.hasStart {
		record()
		_ = st.save(statePath)
		h.appendLog(out, logFile, "REPORT", "pass",
			"supervisor run already in flight for this session; trigger coalesced into one follow-up",
			map[string]any{"coalesced": true, "pending": st.pending, "session_key": key})
		return
	}
	if !c.crossed {
		record()
		_ = st.save(statePath)
		h.appendLog(out, logFile, "REPORT", "pass",
			"high-risk event recorded; the next supervisor run will cover it",
			map[string]any{"high_risk": true, "pending": st.pending})
		return
	}
	highKind := st.high > 0
	kind := "routine"
	if highKind {
		kind = "high"
	}
	switch allowLaunch(st, lim, highKind, now) {
	case denyBackoff:
		record()
		_ = st.save(statePath)
		h.appendLog(out, logFile, "REPORT", "pass",
			"supervisor launch paused after an account session limit",
			map[string]any{"backoff_until": st.backoff})
		return
	case denyCap:
		record()
		if st.caplog != 1 {
			st.caplog = 1
			h.appendLog(out, logFile, "WARN", "pass",
				"supervisor launch cap reached for this session; skipping further routine launches",
				map[string]any{"capped": true, "cap": lim.cap, "session_key": key})
			out.Stderr = fmt.Appendf(out.Stderr, "supervisor-stream: launch cap (%d) reached for this session; routine supervisor runs are skipped (high-risk events still launch)\n", lim.cap)
		}
		_ = st.save(statePath)
		return
	case denyCeiling:
		record()
		if st.ceillog != 1 {
			st.ceillog = 1
			h.appendLog(out, logFile, "WARN", "pass",
				"high-risk supervisor launch ceiling reached for this session",
				map[string]any{"ceiling": lim.ceil})
			out.Stderr = fmt.Appendf(out.Stderr, "supervisor-stream: high-risk launch ceiling (%d) reached for this session; supervisor runs are skipped\n", lim.ceil)
			writeSynthFinding(filepath.Join(h.WorkCurrentDir, "supervisor-findings.ndjson"), lim.ceil, h.NowFn())
		}
		_ = st.save(statePath)
		return
	case denyInterval:
		delay := lim.interval - int(now-st.last)
		prev := st
		st.launches++
		record()
		st.last, st.start, st.hasStart = now+int64(delay), now, true
		_ = st.save(statePath)
		if err := h.startWrapper(c, lim, statePath, lockPath, pendingPath, logFile, delay); err != nil {
			st = prev
			_ = st.save(statePath)
			h.appendLog(out, logFile, "WARN", "pass", "supervisor dispatch launch failed",
				map[string]any{"error": err.Error(), "model": c.model, "runtime": c.runtime})
			return
		}
		h.appendLog(out, logFile, "REPORT", "pass",
			"supervisor launch deferred to the end of the minimum interval",
			map[string]any{"throttled": true, "deferred_s": delay, "pending": st.pending})
		return
	}

	prev := st
	if highKind {
		st.hlaunches++
	} else {
		st.launches++
	}
	st.last, st.start, st.hasStart = now, now, true
	_ = st.save(statePath)
	if err := h.startWrapper(c, lim, statePath, lockPath, pendingPath, logFile, 0); err != nil {
		// Give the launch back so a failed spawn neither counts nor blocks.
		st = prev
		_ = st.save(statePath)
		h.appendLog(out, logFile, "WARN", "pass",
			"supervisor dispatch launch failed",
			map[string]any{"error": err.Error(), "model": c.model, "runtime": c.runtime})
		return
	}
	h.appendLog(out, logFile, "REPORT", "pass",
		fmt.Sprintf("supervisor dispatch forked async (model=%s runtime=%s)", c.model, c.runtime),
		map[string]any{"dispatch": "async", "model": c.model, "runtime": c.runtime, "deadline_s": lim.deadline, "kind": kind})
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
	rec := map[string]any{
		"ts": now.UTC().Format(time.RFC3339), "batch_size": 0, "scores": map[string]any{},
		"overall": "CRITICAL", "synthetic": true,
		"rationale":          fmt.Sprintf("High-risk supervisor launch ceiling (%d) reached for this session: further high-risk events are recorded but no longer supervised. Review the session and the pending events file.", ceiling),
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
