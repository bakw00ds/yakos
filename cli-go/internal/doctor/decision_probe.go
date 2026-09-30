package doctor

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/bakw00ds/yakos/internal/decision"
)

// stateDir mirrors statepath.Dir() but honours the injected env/home so tests
// never touch the real ~/.yakos-state.
func (r *runner) stateDir() string {
	if v := r.env("YAKOS_DISPATCH_LOG"); v != "" {
		return v
	}
	return filepath.Join(r.home, ".yakos-state")
}

// checkDecisionProbe implements --probe-decision (ADR-0009 §3.4). Everything
// here is read-only and local except the single call made when
// ProbeDecisionLive is set. The key is only ever reported as set / not set.
func (r *runner) checkDecisionProbe() {
	sec := SectionDecisionProbe
	writeln(r, "Decision provider probe (ADR-0009)")

	// 1. Kill switch and key (never printed).
	if decision.KillSwitch(r.env) {
		r.warn(sec, "YAKOS_DECISION_DISABLE=1: every decision provider is disabled")
	}
	keySet := r.env(decision.KeyEnv) != ""
	// 2. Config.
	proj := r.cfg.ProjectPath
	if proj == "" {
		if r.cfg.Getwd != nil {
			proj, _ = r.cfg.Getwd()
		} else {
			proj, _ = os.Getwd()
		}
	}
	cfgPath := filepath.Join(proj, ".yakos.yml")
	dc, cerr := decision.LoadConfig(cfgPath)
	if cerr != nil {
		r.err(sec, "%s: decisions config unreadable: %v", cfgPath, cerr)
		dc = decision.DefaultConfig()
	}
	pol, perr := decision.LoadPolicy(decision.StatePaths{Dir: r.stateDir()}.Policy())
	if perr != nil {
		r.err(sec, "%s: unreadable: %v", decision.PolicyFileName, perr)
		pol = decision.DefaultPolicy()
	}
	// A project file cannot enable a provider (ResolveProvider): what runs is
	// the env var or the user-level policy, and a project `provider: none` vetoes the latter.
	name, pwarn := decision.ResolveProvider("", r.env, dc, pol)
	if pwarn != "" {
		r.warn(sec, "%s", pwarn)
	}
	if cerr == nil {
		r.info(sec, "provider configured: %s (model %s)", name, dc.Model)
	}
	dc.Provider = name
	dc = decision.Tighten(dc, pol)
	if keySet {
		r.ok(sec, "%s: set", decision.KeyEnv)
	} else if dc.Provider == decision.ProviderJev {
		r.warn(sec, "%s: not set (provider is jev, so every call will be unavailable)", decision.KeyEnv)
	} else {
		r.info(sec, "%s: not set", decision.KeyEnv)
	}
	if !decision.IsPinnedModel(dc.Model) {
		r.err(sec, "decisions.model %q is an alias or unpinned; pin an exact version such as %s", dc.Model, decision.PinnedModel)
	}

	// 3. Question sets and hashes.
	lib := r.cfg.YakosLib
	if lib == "" && r.yakosRoot != "" {
		lib = filepath.Join(r.yakosRoot, "lib")
	}
	sets := decision.ValidateDir(filepath.Join(lib, "decisions"), decision.StatePaths{Dir: r.stateDir()}.Promotions())
	if len(sets) == 0 {
		r.info(sec, "no question sets under %s", filepath.Join(lib, "decisions"))
	}
	for _, sf := range sets {
		if len(sf.Errs) > 0 {
			for _, e := range sf.Errs {
				r.err(sec, "%s: %v", sf.Path, e)
			}
			continue
		}
		h := sf.Set.Hash
		r.ok(sec, "%s: hash %s, model %s", sf.Set.SchemaID, h[:12], sf.Set.Model)
		if decision.IsPinnedModel(dc.Model) && sf.Set.Model != dc.Model {
			r.warn(sec, "%s pins model %s but decisions.model is %s (a threshold tuned on one model version must not be applied to another)", sf.Set.SchemaID, sf.Set.Model, dc.Model)
		}
	}

	// 4. Breaker and budget state.
	paths := decision.StatePaths{Dir: r.stateDir()}
	br := &decision.Breaker{Path: paths.Breaker()}
	if bs, err := br.Load(); err != nil {
		r.err(sec, "%s: unreadable: %v", paths.Breaker(), err)
	} else if bs.OpenUntil != "" && br.Allow() != nil {
		r.warn(sec, "circuit breaker OPEN until %s after %d consecutive failures (last: %s)", bs.OpenUntil, bs.ConsecutiveFailures, bs.LastFailureClass)
	} else {
		r.ok(sec, "circuit breaker closed (%d consecutive failure(s))", bs.ConsecutiveFailures)
	}
	bud := decision.NewBudget(paths.Budget(), dc.Budget.MaxCallsPerSession, dc.Budget.MaxUSDPerDay)
	if st, err := bud.Load(); err != nil {
		r.err(sec, "%s: unreadable: %v", bud.LedgerPath(), err)
	} else {
		total := 0
		for _, n := range st.Sessions {
			total += n
		}
		r.ok(sec, "budget ledger readable: $%.6f of $%.2f today, %d call(s) across %d session(s)", st.USD, bud.MaxUSDPerDay, total, len(st.Sessions))
	}

	// 5. Live reachability, only on request.
	if !r.cfg.ProbeDecisionLive {
		r.info(sec, "live reachability not checked (pass --live for one minimal call, about $0.000002; it is not counted against the budget)")
		writeln(r, "")
		return
	}
	if !keySet {
		r.err(sec, "--live: %s is not set; cannot probe", decision.KeyEnv)
		writeln(r, "")
		return
	}
	j := &decision.Jev{Getenv: r.env} // no breaker/budget: a probe must not spend or trip them
	req := decision.Request{
		Surface: "doctor-probe", Model: dc.Model,
		State: map[string]any{"probe": "ping"},
		Questions: map[string]decision.Question{
			"ping": {Type: "noul", Instructions: "The state is a connectivity probe."},
		},
		Mode: decision.ModeShadow, Timeout: 10 * time.Second,
	}
	res, err := j.Decide(context.Background(), req)
	if err != nil {
		r.err(sec, "live probe failed: %s", decision.ErrorClass(err))
		writeln(r, "")
		return
	}
	r.ok(sec, "live probe ok: served model %s in %d ms (%d input tokens)", res.Model, res.LatencyMS, res.Usage.InputTokens)
	if res.Model != dc.Model {
		r.warn(sec, "model drift: served %s but pinned %s (aliases move; update decisions.model and the question sets after re-running the eval)", res.Model, dc.Model)
	}
	writeln(r, "")
}

func writeln(r *runner, s string) { _, _ = r.w.Write([]byte(s + "\n")) }
