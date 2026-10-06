package main

// budget_status_columns_test.go: the status table always leads with the TOKENS and TOKEN
// LIMIT columns (K-136, review of #330), even when no agent has a token limit or any token
// data, so a script or an operator reading it sees one layout. With both built-in agents
// turned off and only a dollar-limited agent left, a table that showed the token columns
// only when some agent had token data would drop them: this is the case that distinguishes
// the two.

import (
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/budget"
)

func TestBudgetStatusTable_LeadsWithTokensEvenWhenNoAgentHasTokenData(t *testing.T) {
	state := t.TempDir()
	for _, a := range []string{"supervisor", "librarian"} {
		if err := budget.SetLimit(state, a, 0, budget.Monthly); err != nil {
			t.Fatal(err)
		}
		if err := budget.SetTokenLimit(state, a, 0, budget.Monthly); err != nil {
			t.Fatal(err)
		}
	}
	if err := budget.SetLimit(state, "plain", 10, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	code, out := runYakos(t, state, nil, "budget", "status", "--project", t.TempDir())
	want := "AGENT STATE TOKENS TOKEN LIMIT SPENT LIMIT USED WINDOW SOURCE"
	if got := strings.Join(strings.Fields(strings.SplitN(out, "\n", 2)[0]), " "); code != 0 || got != want {
		t.Fatalf("header = %q (exit %d), want %q:\n%s", got, code, want, out)
	}
}
