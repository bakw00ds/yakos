package doctor

import (
	"github.com/bakw00ds/yakos/internal/budget"
)

// checkAgentBudgets lists agents whose dollar budget is at the warning
// threshold or in hard_stop (K-119, docs/budgets.md). It stays silent when
// every agent is ok or has no budget, so a default install's output is
// unchanged. A hard stop is a warning, not an error: it is a deliberate pause
// the operator lifts with `yakos budget set` or `yakos budget reset`.
func (r *runner) checkAgentBudgets() {
	dir := r.stateDir()
	pol, perr := budget.LoadPolicy(dir)
	type row struct {
		st  budget.Status
		bad bool
	}
	var rows []row
	projLimits, _ := budget.ProjectLimits(r.cfg.ProjectPath)
	for _, a := range budget.AgentNamesWith(pol, projLimits) {
		st, err := budget.Evaluate(a, budget.Options{StateDir: dir, Project: r.cfg.ProjectPath})
		if err != nil {
			continue
		}
		if st.State == budget.StateWarning || st.State == budget.StateHardStop {
			rows = append(rows, row{st: st})
		}
	}
	if len(rows) == 0 && perr == nil {
		return
	}
	writeln(r, "Agent budgets")
	if perr != nil {
		r.warn(SectionAgentBudgets, "%v", perr)
	}
	for _, x := range rows {
		st := x.st
		switch {
		case st.Agent == "supervisor" && st.State == budget.StateHardStop:
			// K-119 F1: at hard stop the LLM supervision tier is off for the
			// rest of the window, which block_on_critical silently relied on.
			r.err(SectionAgentBudgets, "LLM supervision disabled: supervisor budget exhausted ($%.2f of $%.2f, %s). Local pre-filter and Jev shadow still run. `yakos budget set supervisor <usd>` or `yakos budget reset supervisor` (reason=%s)",
				st.SpentUSD, st.LimitUSD, st.Window, st.Reason)
		case st.Agent == "supervisor":
			r.warn(SectionAgentBudgets, "LLM supervision budget at %.0f%% of $%.2f (%s); at 100%% routine supervisor launches stop (reason=%s)",
				st.Pct, st.LimitUSD, st.Window, st.Reason)
		case st.State == budget.StateHardStop:
			r.warn(SectionAgentBudgets, "%s: hard_stop at $%.2f of $%.2f (%s); new dispatches are refused. `yakos budget set %s <usd>` or `yakos budget reset %s`",
				st.Agent, st.SpentUSD, st.LimitUSD, st.Window, st.Agent, st.Agent)
		default:
			r.warn(SectionAgentBudgets, "%s: warning, %.0f%% of $%.2f (%s) spent", st.Agent, st.Pct, st.LimitUSD, st.Window)
		}
	}
	writeln(r, "")
}
