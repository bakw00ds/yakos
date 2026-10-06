package budget

// builtin_tokens_test.go — K-136: the built-in supervisor and librarian budgets carry a
// token limit as well as a dollar limit, so they still trip for an operator on a
// subscription, whose runs cost no dollars and never move limit_usd. Every test runs
// in a temp state directory with no policy file unless it writes one.

import (
	"math"
	"os"
	"runtime"
	"strings"
	"testing"
)

// The built-in token limits are the dollar ceilings converted at the Sonnet reference
// rate and rounded down to a whole million. This pins the arithmetic written in
// policy.go, docs/budgets.md and the CHANGELOG to the numbers in the code.
func TestBuiltinTokenLimits_FollowTheDollarCeilings(t *testing.T) {
	if len(builtinTokenLimits) != len(builtinLimits) {
		t.Fatalf("every built-in dollar limit has a token limit and no other agent does: %v vs %v", builtinTokenLimits, builtinLimits)
	}
	for agent, usd := range builtinLimits {
		got, ok := BuiltinTokenLimit(agent)
		if !ok {
			t.Errorf("%s has a built-in dollar limit and no built-in token limit", agent)
			continue
		}
		wantMillions := math.Floor(usd / builtinTokenRateUSDPerMTok)
		if want := int64(wantMillions) * 1_000_000; got != want {
			t.Errorf("%s: token limit %d, want $%.0f / ($%.0f per 1M tokens) rounded down to a million = %d", agent, got, usd, builtinTokenRateUSDPerMTok, want)
		}
	}
	if v, _ := BuiltinTokenLimit("supervisor"); v != 33_000_000 {
		t.Errorf("supervisor = %d, the documented 33,000,000", v)
	}
	if v, _ := BuiltinTokenLimit("librarian"); v != 13_000_000 {
		t.Errorf("librarian = %d, the documented 13,000,000", v)
	}
	if _, ok := BuiltinTokenLimit("backend"); ok {
		t.Error("an agent with no built-in dollar limit has no built-in token limit")
	}
}

// A subscription-only ledger trips the built-in token limit with no policy file at all:
// no dollar is ever spent, and the supervisor still hits its hard stop. Its dispatch stop
// is 2x, like its dollar stop.
func TestBuiltinTokenLimit_SubscriptionLedgerTripsTheSupervisor(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	appendLog(t, dir, ledgerLine("supervisor", octMid, "subscription", 20_000_000, 3_000_000, 9_000_000, 1_000_000, 0))
	st := mustEval(t, "supervisor", o)
	if st.State != StateHardStop || st.SpentTokens != 33_000_000 || st.SpentUSD != 0 || st.LimitTokens != 33_000_000 || st.Source != "builtin" {
		t.Fatalf("33M subscription tokens must be the supervisor's hard stop with $0 spent: %+v", st)
	}
	if _, err := Enforce("supervisor", o); err != nil {
		t.Fatalf("between 1x and 2x the supervisor still dispatches: %v", err)
	}
	appendLog(t, dir, ledgerLine("supervisor", octMid, "subscription", 33_000_000, 0, 0, 0, 0))
	_, err := Enforce("supervisor", o)
	if !IsRefused(err) {
		t.Fatalf("at 2x the token limit dispatch must refuse, got %v", err)
	}
	if !strings.Contains(err.Error(), "tokens") || strings.Contains(err.Error(), "$") {
		t.Fatalf("the refusal names tokens, not dollars: %v", err)
	}
}

func TestBuiltinTokenLimit_SubscriptionLedgerTripsTheLibrarian(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	appendLog(t, dir, ledgerLine("librarian", octMid, "subscription", 12_999_999, 0, 0, 0, 0))
	if st := mustEval(t, "librarian", o); st.State != StateWarning {
		t.Fatalf("one token under the limit is a warning: %+v", st)
	}
	if _, err := Enforce("librarian", o); err != nil {
		t.Fatal(err)
	}
	appendLog(t, dir, ledgerLine("librarian", octMid, "subscription", 1, 0, 0, 0, 0))
	st := mustEval(t, "librarian", o)
	if st.State != StateHardStop || st.SpentUSD != 0 {
		t.Fatalf("13M subscription tokens must stop the librarian: %+v", st)
	}
	if _, err := Enforce("librarian", o); !IsRefused(err) {
		t.Fatalf("the librarian has no 2x stop, so dispatch refuses at the limit: %v", err)
	}
}

// An API ledger still trips the dollar limit: dollars count for api rows, so $41 of api
// spend stops the librarian while its tokens are far below the token limit, and the
// refusal blames the dollars.
func TestBuiltinLimits_APILedgerTripsTheDollarLimit(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	appendLog(t, dir, ledgerLine("librarian", octMid, "api", 400_000, 100_000, 0, 0, 41))
	st := mustEval(t, "librarian", o)
	if st.State != StateHardStop || st.SpentUSD != 41 || st.SpentTokens != 500_000 {
		t.Fatalf("$41 of api spend stops the librarian: %+v", st)
	}
	_, err := Enforce("librarian", o)
	if !IsRefused(err) || !strings.Contains(err.Error(), "$41.00 of its $40.00") || strings.Contains(err.Error(), "tokens") {
		t.Fatalf("the refusal names the dollar limit that tripped, not the token limit that did not: %v", err)
	}
}

// A mixed ledger trips whichever limit is reached first, and the refusal says which.
func TestBuiltinLimits_MixedLedgerTripsWhicheverComesFirst(t *testing.T) {
	// Tokens first: a lot of subscription tokens and a little api spend.
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	appendLog(t, dir,
		ledgerLine("librarian", octMid, "api", 500_000, 0, 0, 0, 30),
		ledgerLine("librarian", octMid, "subscription", 11_000_000, 0, 0, 0, 0),
	)
	if st := mustEval(t, "librarian", o); st.State != StateWarning || st.SpentUSD != 30 || st.SpentTokens != 11_500_000 {
		t.Fatalf("neither limit reached yet: %+v", st)
	}
	appendLog(t, dir, ledgerLine("librarian", octMid, "subscription", 1_600_000, 0, 0, 0, 0))
	_, err := Enforce("librarian", o)
	if !IsRefused(err) || !strings.Contains(err.Error(), "tokens") || strings.Contains(err.Error(), "$30.00 of its $40.00") {
		t.Fatalf("tokens reached first ($30 of $40 is still under its limit): %v", err)
	}

	// Dollars first: a little subscription traffic and api spend past the ceiling.
	dir2 := t.TempDir()
	o2 := Options{StateDir: dir2, Now: clock(octMid)}
	appendLog(t, dir2,
		ledgerLine("librarian", octMid, "subscription", 2_000_000, 0, 0, 0, 0),
		ledgerLine("librarian", octMid, "api", 100_000, 0, 0, 0, 40.5),
	)
	_, err = Enforce("librarian", o2)
	if !IsRefused(err) || !strings.Contains(err.Error(), "$40.50 of its $40.00") || strings.Contains(err.Error(), "tokens") {
		t.Fatalf("dollars reached first (2.1M of 13M tokens): %v", err)
	}

	// Both at once: one message that says both.
	appendLog(t, dir2, ledgerLine("librarian", octMid, "subscription", 11_000_000, 0, 0, 0, 0))
	_, err = Enforce("librarian", o2)
	if !IsRefused(err) || !strings.Contains(err.Error(), "$40.50 of its $40.00") || !strings.Contains(err.Error(), "13,100,000 of its 13,000,000 tokens") {
		t.Fatalf("both reached: %v", err)
	}
}

// A pre-K-136 row (no billing field) keeps counting its dollars against the built-in
// dollar limit, and its tokens against the token limit when it reports them.
func TestBuiltinLimits_LegacyRowsKeepCounting(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	appendLog(t, dir, finished("librarian", octMid, 41)) // a bash-written row: dollars, no billing
	if st := mustEval(t, "librarian", o); st.State != StateHardStop || st.SpentUSD != 41 {
		t.Fatalf("a legacy row's dollars still stop the librarian: %+v", st)
	}
}

// The operator's policy overrides a built-in token limit, `limit_tokens: 0` turns it
// off, and an unusable policy file never disables it (as for the dollar limit).
func TestBuiltinTokenLimit_PolicyOverridesAndUntrustedFileDoesNotDisable(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	appendLog(t, dir, ledgerLine("supervisor", octMid, "subscription", 40_000_000, 0, 0, 0, 0))
	if st := mustEval(t, "supervisor", o); st.State != StateHardStop {
		t.Fatalf("40M tokens is past the built-in 33M: %+v", st)
	}

	setTokenLimit(t, dir, "supervisor", 100_000_000, Monthly)
	if st := mustEval(t, "supervisor", o); st.LimitTokens != 100_000_000 || st.State != StateOK {
		t.Fatalf("a policy token limit replaces the built-in one: %+v", st)
	}
	setTokenLimit(t, dir, "supervisor", 0, Monthly)
	if st := mustEval(t, "supervisor", o); st.LimitTokens != 0 || st.State != StateOK {
		t.Fatalf("limit_tokens 0 turns the built-in token limit off; the dollar limit stays: %+v", st)
	}
	setLimit(t, dir, "supervisor", 0, Monthly)
	if st := mustEval(t, "supervisor", o); st.State != StateOff {
		t.Fatalf("both limits 0 is no budget: %+v", st)
	}

	// An untrusted policy file (group and world writable) is ignored whole, so the
	// built-in token limit applies again. A subtest, so that the overrides above
	// still run, and report, where the file modes cannot be made untrusted.
	t.Run("an untrusted policy file does not disable it", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX permission bits: chmod 0666 does not make a file untrusted on Windows")
		}
		if err := os.Chmod(PolicyPath(dir), 0o666); err != nil {
			t.Skipf("cannot make the policy untrusted here: %v", err)
		}
		st, err := Evaluate("supervisor", o)
		if err != nil {
			t.Fatal(err)
		}
		if st.LimitTokens != 33_000_000 || st.State != StateHardStop {
			t.Fatalf("an untrusted policy must not disable the built-in token limit: %+v", st)
		}
	})
}

// An agent with no built-in limits has no token limit unless the operator sets one.
func TestBuiltinTokenLimit_OtherAgentsHaveNone(t *testing.T) {
	dir := t.TempDir()
	appendLog(t, dir, ledgerLine("backend", octMid, "subscription", 900_000_000, 0, 0, 0, 0))
	st := mustEval(t, "backend", Options{StateDir: dir, Now: clock(octMid)})
	if st.State != StateOff || st.LimitTokens != 0 {
		t.Fatalf("%+v", st)
	}
}
