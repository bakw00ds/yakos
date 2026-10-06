package supervisorstream

import "testing"

// K-136: the three rules that pick the unit a budget message names, at their
// thresholds. The bash twin computes the same three from the same numbers in its
// jq filter (the uw, uh and uc flags); tests/run-supervisor-budget-test.sh covers
// each through the real CLI.

func TestBudgetGateUnitRules(t *testing.T) {
	for _, c := range []struct {
		name             string
		b                budgetGate
		warn, hard, over bool // tokens named in a warning, a hard-stop message, a ceiling message
	}{
		{"dollars only", budgetGate{limit: 100, stop: 200, spent: 500}, false, false, false},
		{"a token limit that is not set never wins", budgetGate{limit: 100, stop: 200, spent: 1, tokSpent: 1 << 40}, false, false, false},
		{"tokens only, below their limit", budgetGate{tokLimit: 1000, tokStop: 2000, tokSpent: 999}, true, false, false},
		{"tokens only, at their limit", budgetGate{tokLimit: 1000, tokStop: 2000, tokSpent: 1000}, true, true, false},
		{"tokens only, one under the stop", budgetGate{tokLimit: 1000, tokStop: 2000, tokSpent: 1999}, true, true, false},
		{"tokens only, at the stop", budgetGate{tokLimit: 1000, tokStop: 2000, tokSpent: 2000}, true, true, true},
		{"both, tokens the larger share", budgetGate{limit: 100, stop: 200, spent: 85, tokLimit: 1000, tokStop: 2000, tokSpent: 900}, true, false, false},
		{"both, dollars the larger share", budgetGate{limit: 100, stop: 200, spent: 90, tokLimit: 1000, tokStop: 2000, tokSpent: 850}, false, false, false},
		{"both, a tie in shares", budgetGate{limit: 100, stop: 200, spent: 85, tokLimit: 1000, tokStop: 2000, tokSpent: 850}, true, false, false},
		{"both, tokens reached, dollars far past", budgetGate{limit: 100, stop: 200, spent: 900, tokLimit: 1000, tokStop: 2000, tokSpent: 1000}, false, true, false},
		{"both, only dollars over their stop", budgetGate{limit: 100, stop: 200, spent: 250, tokLimit: 1000, tokStop: 2000, tokSpent: 1500}, false, true, false},
		{"both, only tokens over their stop", budgetGate{limit: 100, stop: 200, spent: 150, tokLimit: 1000, tokStop: 2000, tokSpent: 2500}, true, true, true},
	} {
		if got := c.b.tokensWarn(); got != c.warn {
			t.Errorf("%s: tokensWarn = %v, want %v", c.name, got, c.warn)
		}
		if got := c.b.tokensHard(); got != c.hard {
			t.Errorf("%s: tokensHard = %v, want %v", c.name, got, c.hard)
		}
		if got := c.b.tokensOver(); got != c.over {
			t.Errorf("%s: tokensOver = %v, want %v", c.name, got, c.over)
		}
	}
}
