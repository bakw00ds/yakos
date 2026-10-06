package main

// budget_floor_test.go: the one-cent floor on a dollar limit through the CLI (K-136, spec
// item 7 of #330). A positive limit below it is out of range: `budget set` refuses it and
// writes nothing. The floor itself is accepted.

import (
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/budget"
)

func TestBudgetSet_RefusesALimitBelowTheFloor(t *testing.T) {
	state := t.TempDir()
	code, out := runYakos(t, state, nil, "budget", "set", "backend", "0.009")
	if code != 1 || !strings.Contains(out, "0.01") {
		t.Fatalf("a limit of $0.009 must be refused (exit 1) and name the floor: exit %d: %s", code, out)
	}
	if pol, _ := budget.LoadPolicy(state); len(pol.Agents) != 0 {
		t.Fatalf("a refused set must not write a policy: %+v", pol.Agents)
	}

	// the floor itself is a limit
	if code, out = runYakos(t, state, nil, "budget", "set", "backend", "0.01"); code != 0 {
		t.Fatalf("exactly one cent is accepted: exit %d: %s", code, out)
	}
	pol, _ := budget.LoadPolicy(state)
	if lim := pol.Agents["backend"].LimitUSD; lim == nil || *lim != 0.01 {
		t.Fatalf("the policy should hold a limit of 0.01: %+v", pol.Agents)
	}
}
