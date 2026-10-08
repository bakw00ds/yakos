package doctor

import (
	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/projfile"
)

// checkAgentBudgets lists agents whose budget, in tokens or in dollars, is at the
// warning threshold or in hard_stop (K-119, K-136, docs/budgets.md). It stays
// silent when every agent is ok or has no budget, so a default install's output
// is unchanged. A hard stop is a warning, not an error: it is a deliberate pause
// the operator lifts with `yakos budget set` or `yakos budget reset`.
//
// Tokens are the primary unit, so an agent with a token limit is described in
// tokens first, says which limit it reached, and is pointed at the matching flag
// (`--tokens <n>` for a token stop, `<usd>` for a dollar stop). A dollar-only agent
// reads as it always did.
//
// The section reads the project's .yakos.yml (agent_budgets: and supervisor: agent:,
// see budgetProject) and nothing else from it: every line it prints names an agent
// that passed budget.ValidateAgent and gives limit numbers. It prints no path and no
// text taken from the project file, and it never prints budget.Status.Warnings, which
// carry the YAML error of a malformed project file (the raw file text).
func (r *runner) checkAgentBudgets() {
	dir := r.stateDir()
	project := r.budgetProject()
	pol, perr := budget.LoadPolicy(dir)
	type row struct {
		st  budget.Status
		bad bool
	}
	var rows []row
	// The agent a project names as its supervisor is budgeted at the stricter of its
	// own limit and the supervisor's (budget.Evaluate builds that one limit) under
	// whatever name it gives it, so it gets the supervisor's wording too.
	supervisors := map[string]bool{"supervisor": true}
	for _, n := range budget.ProjectSupervisorAgents(project) {
		supervisors[n] = true
	}
	for _, a := range budget.AgentNamesForProject(pol, project) {
		st, err := budget.Evaluate(a, budget.Options{StateDir: dir, Project: project})
		if err != nil {
			continue
		}
		if st.State == budget.StateWarning || st.State == budget.StateHardStop {
			rows = append(rows, row{st: st})
		}
	}
	// A refused project file (a symlink, not a regular file, over the size cap,
	// unreadable) is read as absent, so none of its project settings apply. The operator
	// would not otherwise learn that their limits are not applied. The line is fixed text keyed off a structured answer; it names no path and carries
	// nothing from the file.
	refused := budget.ProjectRefused(project)
	if len(rows) == 0 && perr == nil && !refused {
		return
	}
	writeln(r, "Agent budgets")
	if perr != nil {
		r.warn(SectionAgentBudgets, "%v", perr)
	}
	if refused {
		r.warn(SectionAgentBudgets, ".yakos.yml was not read (a symlink, not a regular file, over %d bytes, or unreadable): every project setting in it is ignored (budget, models, injection_scan, decisions, supervisor-gate), so the built-in defaults apply", projfile.MaxBytes)
	}
	for _, x := range rows {
		st := x.st
		tokens := st.LimitTokens > 0
		switch {
		case supervisors[st.Agent] && st.State == budget.StateHardStop:
			// K-119 F1: at hard stop the LLM supervision tier is off for the
			// rest of the window, which block_on_critical silently relied on.
			if tokens {
				r.err(SectionAgentBudgets, "LLM supervision disabled: %s budget exhausted (%s reached: %s, %s). Local pre-filter and Jev shadow still run. %s or `yakos budget reset %s` (reason=%s)",
					st.Agent, st.ReachedDesc(), st.Usage(), st.Window, st.RaiseHint(), st.Agent, st.Reason)
			} else {
				r.err(SectionAgentBudgets, "LLM supervision disabled: %s budget exhausted ($%.2f of $%.2f, %s). Local pre-filter and Jev shadow still run. `yakos budget set %s <usd>` or `yakos budget reset %s` (reason=%s)",
					st.Agent, st.SpentUSD, st.LimitUSD, st.Window, st.Agent, st.Agent, st.Reason)
			}
		case supervisors[st.Agent]:
			label := "LLM supervision budget"
			if st.Agent != "supervisor" {
				label += " (" + st.Agent + ")"
			}
			if tokens {
				r.warn(SectionAgentBudgets, "%s at %.0f%% (%s, %s); at 100%% routine supervisor launches stop (reason=%s)",
					label, st.Pct, st.Usage(), st.Window, st.Reason)
			} else {
				r.warn(SectionAgentBudgets, "%s at %.0f%% of $%.2f (%s); at 100%% routine supervisor launches stop (reason=%s)",
					label, st.Pct, st.LimitUSD, st.Window, st.Reason)
			}
		case st.State == budget.StateHardStop && tokens:
			r.warn(SectionAgentBudgets, "%s: hard_stop, %s reached (%s, %s); new dispatches are refused. %s or `yakos budget reset %s`",
				st.Agent, st.ReachedDesc(), st.Usage(), st.Window, st.RaiseHint(), st.Agent)
		case st.State == budget.StateHardStop:
			r.warn(SectionAgentBudgets, "%s: hard_stop at $%.2f of $%.2f (%s); new dispatches are refused. `yakos budget set %s <usd>` or `yakos budget reset %s`",
				st.Agent, st.SpentUSD, st.LimitUSD, st.Window, st.Agent, st.Agent)
		case tokens:
			r.warn(SectionAgentBudgets, "%s: warning, %.0f%% (%s, %s) spent", st.Agent, st.Pct, st.Usage(), st.Window)
		default:
			r.warn(SectionAgentBudgets, "%s: warning, %.0f%% of $%.2f (%s) spent", st.Agent, st.Pct, st.LimitUSD, st.Window)
		}
	}
	writeln(r, "")
}

// budgetProject is the project directory the budgets section reads: BudgetProject,
// else ProjectPath, else none (a library caller that names no project gets the
// built-in and policy budgets only; the entry point supplies the working directory).
// It is a place to read one file from, never a place to derive a state path from.
func (r *runner) budgetProject() string {
	if r.cfg.BudgetProject != "" {
		return r.cfg.BudgetProject
	}
	return r.cfg.ProjectPath
}
