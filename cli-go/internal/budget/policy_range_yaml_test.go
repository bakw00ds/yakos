package budget

// policy_range_yaml_test.go: every out-of-range value in the trusted policy FILE, read
// through the YAML decoder, for both built-in agents (K-136, security review of #330). An
// out-of-range value never switches a built-in limit off and never yields an infinite
// limit or stop: it is ignored with a warning and the built-in stays. Explicit 0 still
// switches a unit off. The values the spec lists: limit_tokens negative, not a whole
// number or above 2^50; limit_usd negative, NaN, +Inf, -Inf or above 1e9. yaml.v3 decodes
// 1500000.5 into an int64 as 1500000 without an error, so the file is read through a node.

import (
	"os"
	"testing"
)

func evalFromYAML(t *testing.T, agent, entry string) Status {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(PolicyPath(dir), []byte("agents:\n  "+agent+":\n    "+entry+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Evaluate(agent, Options{StateDir: dir, Now: clock(octMid)})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestPolicyFile_OutOfRangeValuesKeepTheBuiltin(t *testing.T) {
	builtin := map[string]struct {
		usd float64
		tok int64
	}{"supervisor": {100, 33_000_000}, "librarian": {40, 13_000_000}}
	for agent, b := range builtin {
		for _, bad := range []string{
			"limit_tokens: -5", "limit_tokens: -1", "limit_tokens: 1500000.5", "limit_tokens: 0.5",
			"limit_tokens: 1125899906842625", "limit_tokens: 99999999999999999", "limit_tokens: 1.5e15",
			"limit_tokens: 99999999999999999999999", "limit_tokens: 18446744073709551615", "limit_tokens: .inf", "limit_tokens: -.inf", "limit_tokens: .nan",
			`limit_tokens: "5m"`, "limit_tokens: 5m", "limit_tokens: true", "limit_tokens: [1, 2]",
		} {
			st := evalFromYAML(t, agent, bad)
			if st.LimitTokens != b.tok || st.LimitUSD != b.usd || st.Source != "builtin" {
				t.Errorf("%s %s: the built-in must stay on: %+v", agent, bad, st)
			}
			if !hasWarning(st, "limit_tokens for "+agent+" ignored") {
				t.Errorf("%s %s: the ignored value is reported: %v", agent, bad, st.Warnings)
			}
		}
		for _, bad := range []string{
			"limit_usd: -5", "limit_usd: -0.01", "limit_usd: .nan", "limit_usd: .inf", "limit_usd: -.inf",
			"limit_usd: 1000000001", "limit_usd: 1e10", "limit_usd: 1e308",
		} {
			st := evalFromYAML(t, agent, bad)
			if st.LimitUSD != b.usd || st.LimitTokens != b.tok || st.Source != "builtin" {
				t.Errorf("%s %s: the built-in must stay on: %+v", agent, bad, st)
			}
			if st.StopUSD != b.usd*map[string]float64{"supervisor": 2, "librarian": 1}[agent] {
				t.Errorf("%s %s: the stop must stay finite and the built-in's: %v", agent, bad, st.StopUSD)
			}
			if !hasWarning(st, "limit_usd for "+agent+" ignored") {
				t.Errorf("%s %s: the ignored value is reported: %v", agent, bad, st.Warnings)
			}
		}
		// A value that is not a number at all fails the whole file closed: the built-ins apply.
		for _, bad := range []string{`limit_usd: "5"`, "limit_usd: five", "limit_usd: [5]"} {
			st := evalFromYAML(t, agent, bad)
			if st.LimitUSD != b.usd || st.LimitTokens != b.tok || len(st.Warnings) == 0 {
				t.Errorf("%s %s: an unreadable policy leaves the built-ins on, with a warning: %+v", agent, bad, st)
			}
		}
		// Explicit 0 still switches that unit off, on purpose, with no warning.
		if st := evalFromYAML(t, agent, "limit_tokens: 0"); st.LimitTokens != 0 || st.LimitUSD != b.usd || len(st.Warnings) != 0 {
			t.Errorf("%s: limit_tokens 0 switches the token limit off: %+v", agent, st)
		}
		if st := evalFromYAML(t, agent, "limit_usd: 0"); st.LimitUSD != 0 || st.LimitTokens != b.tok || len(st.Warnings) != 0 {
			t.Errorf("%s: limit_usd 0 switches the dollar limit off: %+v", agent, st)
		}
		// The edges that are accepted.
		for _, ok := range []struct {
			entry string
			usd   float64
			tok   int64
		}{
			{"limit_tokens: 50000000", b.usd, 50_000_000},
			{"limit_tokens: 1125899906842624", b.usd, maxTokenLimit},
			{"limit_tokens: 1e6", b.usd, 1_000_000},
			{"limit_tokens: 2.0e6", b.usd, 2_000_000},
			{"limit_usd: 1000000000", 1e9, b.tok},
			{"limit_usd: 0.5", 0.5, b.tok},
		} {
			st := evalFromYAML(t, agent, ok.entry)
			if st.LimitUSD != ok.usd || st.LimitTokens != ok.tok || len(st.Warnings) != 0 {
				t.Errorf("%s %s should apply as written: %+v", agent, ok.entry, st)
			}
		}
	}
}

// Another agent's entry survives beside a bad one, and the stop of a limit at the bound
// is finite (2e9 for the supervisor's factor of 2, not an overflow).
func TestPolicyFile_ABadValueDoesNotCostTheRestOfTheFile(t *testing.T) {
	dir := t.TempDir()
	body := "agents:\n  supervisor:\n    limit_tokens: 1500000.5\n    limit_usd: 1000000000\n  backend:\n    limit_usd: 50\n"
	if err := os.WriteFile(PolicyPath(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	o := Options{StateDir: dir, Now: clock(octMid)}
	if st := mustEval(t, "supervisor", o); st.LimitTokens != 33_000_000 || st.LimitUSD != 1e9 || st.StopUSD != 2e9 || !hasWarning(st, "not a whole number of tokens") {
		t.Errorf("%+v", st)
	}
	if st := mustEval(t, "backend", o); st.LimitUSD != 50 || len(st.Warnings) != 0 {
		t.Errorf("%+v", st)
	}
}

// set refuses what the resolver would ignore, so a value it writes is a value that applies.
func TestSetLimit_RefusesAboveTheBound(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []float64{maxLimitUSD + 1, 1e10, 1e308} {
		if err := SetLimit(dir, "backend", bad, Monthly); err == nil {
			t.Errorf("SetLimit accepted $%v", bad)
		}
	}
	if err := SetLimit(dir, "backend", maxLimitUSD, Monthly); err != nil {
		t.Errorf("the bound itself is allowed: %v", err)
	}
	if st := mustEval(t, "backend", Options{StateDir: dir, Now: clock(octMid)}); st.LimitUSD != maxLimitUSD || len(st.Warnings) != 0 {
		t.Errorf("%+v", st)
	}
}
