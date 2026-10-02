package dispatch

import (
	"fmt"
	"os"

	"github.com/bakw00ds/yakos/internal/budget"
)

// budgetPreflight enforces the agent's dollar budget (K-119, docs/budgets.md).
// It returns a *budget.RefusedError only for hard_stop. A warning is printed to
// stderr; every other budget failure (unreadable log, untrusted policy file)
// fails open so a cost guard can never take dispatching down.
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
