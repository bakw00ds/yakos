package dispatch

import (
	"fmt"
	"os"

	"github.com/bakw00ds/yakos/internal/budget"
)

// budgetPreflight enforces the agent's budget, in tokens, dollars or both (K-119,
// K-136, docs/budgets.md).
// It returns a *budget.RefusedError only for hard_stop. A warning is printed to
// stderr; every other budget failure (unreadable log, untrusted policy file)
// fails open so a cost guard can never take dispatching down.
//
// Every path that launches an agent runs it before it forks anything: Run,
// RunStream, and (K-136) the console's interactive chat turns through
// PreflightBudget.
func budgetPreflight(req Request) error {
	st, err := budget.Enforce(req.AgentName, budget.Options{Project: req.Project})
	if budget.IsRefused(err) {
		return err
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "yakos budget: %v (continuing)\n", err)
	}
	for _, w := range st.Warnings {
		fmt.Fprintf(os.Stderr, "yakos budget: %s\n", w)
	}
	if st.State == budget.StateWarning {
		fmt.Fprintf(os.Stderr, "yakos budget: %s\n", st.Message())
	}
	return nil
}

// PreflightBudget is the budget pre-flight for a launch that does not go through
// Run or RunStream: the console's interactive chat sessions, which keep one agent
// process alive across turns. It refuses a NEW session or turn for an agent in
// hard_stop with the same *budget.RefusedError a one-shot dispatch returns, and
// otherwise behaves exactly like budgetPreflight (warnings go to stderr, any other
// budget problem fails open). project is the project whose .yakos.yml may lower
// the agent's limit.
func PreflightBudget(agentName, project string) error {
	return budgetPreflight(Request{AgentName: agentName, Project: project})
}
