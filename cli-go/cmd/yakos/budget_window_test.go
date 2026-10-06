package main

// budget_window_test.go: the set subcommand keeps an agent's current window unless
// --window is given (K-136, security review of #330). The dollar and token limits
// share one window, so adding a token limit used to turn a lifetime dollar cap
// into a monthly one without a word.

import (
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/budget"
)

func TestBudgetSet_WithoutWindowKeepsALifetimeWindow(t *testing.T) {
	state := t.TempDir()
	window := func() string {
		pol, err := budget.LoadPolicy(state)
		if err != nil {
			t.Fatal(err)
		}
		return pol.Agents["backend"].Window
	}

	if code, out := runYakos(t, state, nil, "budget", "set", "backend", "20", "--window", "lifetime"); code != 0 || !strings.Contains(out, "(lifetime)") {
		t.Fatalf("lifetime set: exit %d: %s", code, out)
	}

	// A token limit with no --window: the window stays lifetime, and the output says so.
	code, out := runYakos(t, state, nil, "budget", "set", "backend", "--tokens", "2m")
	if code != 0 || !strings.Contains(out, "token budget for backend set to 2000000 tokens (lifetime)") {
		t.Fatalf("set --tokens: exit %d: %s", code, out)
	}
	if w := window(); w != "lifetime" {
		t.Fatalf("set --tokens turned a lifetime window into %q", w)
	}

	// A dollar limit with no --window keeps it too.
	code, out = runYakos(t, state, nil, "budget", "set", "backend", "30")
	if code != 0 || !strings.Contains(out, "budget for backend set to $30.00 (lifetime)") {
		t.Fatalf("set 30: exit %d: %s", code, out)
	}
	if w := window(); w != "lifetime" {
		t.Fatalf("a plain set turned a lifetime window into %q", w)
	}

	// Both limits and no --window: the same.
	if code, out = runYakos(t, state, nil, "budget", "set", "backend", "40", "--tokens", "3m"); code != 0 {
		t.Fatalf("set both: exit %d: %s", code, out)
	}
	if w := window(); w != "lifetime" {
		t.Fatalf("set <usd> --tokens turned a lifetime window into %q", w)
	}

	// --window still changes it.
	if code, out = runYakos(t, state, nil, "budget", "set", "backend", "--tokens", "3m", "--window", "monthly"); code != 0 || !strings.Contains(out, "(monthly)") {
		t.Fatalf("explicit window: exit %d: %s", code, out)
	}
	if w := window(); w != "monthly" {
		t.Fatalf("--window monthly was ignored: %q", w)
	}
}

// An agent that has no entry is monthly, as it always was.
func TestBudgetSet_WithoutWindowOnANewAgentIsMonthly(t *testing.T) {
	state := t.TempDir()
	if code, out := runYakos(t, state, nil, "budget", "set", "frontend", "--tokens", "1m"); code != 0 || !strings.Contains(out, "(monthly)") {
		t.Fatalf("exit %d: %s", code, out)
	}
	pol, _ := budget.LoadPolicy(state)
	if w := pol.Agents["frontend"].Window; w != "monthly" {
		t.Fatalf("window = %q", w)
	}
}
