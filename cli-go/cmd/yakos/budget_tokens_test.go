package main

// budget_tokens_test.go — K-136: token limits in the `yakos budget` command. The
// router runs as a subprocess of the test binary against a temp state directory
// (runYakos, budget_e2e_test.go); nothing touches the operator's real state.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
)

func TestParseTokenCount(t *testing.T) {
	good := map[string]int64{
		"0": 0, "5000000": 5_000_000, "500k": 500_000, "500K": 500_000, "1.5m": 1_500_000, "2b": 2_000_000_000,
		" 7 ": 7, "1_000": 1000, "0.5k": 500,
	}
	for in, want := range good {
		if got, err := parseTokenCount(in); err != nil || got != want {
			t.Errorf("parseTokenCount(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "-5", "5x", "nan", "inf", "1e30", "99999999999999999999b", "m"} {
		if got, err := parseTokenCount(bad); err == nil {
			t.Errorf("parseTokenCount(%q) = %d, want an error", bad, got)
		}
	}
}

func TestBudgetSetTokens_RoundTripAndStatus(t *testing.T) {
	state := t.TempDir()

	code, out := runYakos(t, state, nil, "budget", "set", "backend", "--tokens", "5m")
	if code != 0 || !strings.Contains(out, "token budget for backend set to 5000000 tokens (monthly)") {
		t.Fatalf("set --tokens: exit %d: %s", code, out)
	}
	pol, err := budget.LoadPolicy(state)
	if err != nil {
		t.Fatal(err)
	}
	a := pol.Agents["backend"]
	if a.LimitTokens == nil || *a.LimitTokens != 5_000_000 || a.LimitUSD != nil {
		t.Fatalf("a token-only set must not touch the dollar limit: %+v", a)
	}

	// Both at once.
	if code, out = runYakos(t, state, nil, "budget", "set", "backend", "20", "--tokens", "2m", "--window", "lifetime"); code != 0 {
		t.Fatalf("set both: exit %d: %s", code, out)
	}
	pol, _ = budget.LoadPolicy(state)
	a = pol.Agents["backend"]
	if a.LimitUSD == nil || *a.LimitUSD != 20 || a.LimitTokens == nil || *a.LimitTokens != 2_000_000 || a.Window != "lifetime" {
		t.Fatalf("both limits set: %+v", a)
	}

	// status --json carries the token fields.
	code, out = runYakos(t, state, nil, "budget", "status", "--json")
	if code != 0 {
		t.Fatalf("status: exit %d: %s", code, out)
	}
	var rows []budget.Status
	if err := json.Unmarshal([]byte(out[strings.Index(out, "["):]), &rows); err != nil {
		t.Fatalf("status json: %v\n%s", err, out)
	}
	var found bool
	for _, r := range rows {
		if r.Agent == "backend" {
			found = true
			if r.LimitTokens != 2_000_000 || r.LimitUSD != 20 {
				t.Errorf("status row = %+v", r)
			}
		}
	}
	if !found {
		t.Fatalf("backend missing from status: %s", out)
	}

	// 0 turns the token limit off and leaves the dollar limit.
	if code, out = runYakos(t, state, nil, "budget", "set", "backend", "--tokens", "0"); code != 0 || !strings.Contains(out, "token budget for backend turned off") {
		t.Fatalf("--tokens 0: exit %d: %s", code, out)
	}
	pol, _ = budget.LoadPolicy(state)
	if lim := budget.Resolve("backend", pol, nil); lim.Tokens != 0 || lim.USD != 20 {
		t.Fatalf("after --tokens 0: %+v", lim)
	}
}

func TestBudgetSet_UsageErrors(t *testing.T) {
	state := t.TempDir()
	for _, args := range [][]string{
		{"budget", "set", "backend"},                                         // no limit at all
		{"budget", "set"},                                                    // no agent
		{"budget", "set", "backend", "1", "2"},                               // too many positionals
		{"budget", "set", "backend", "--tokens", "lots"},                     // not a count
		{"budget", "set", "backend", "--tokens", "-5"},                       // negative
		{"budget", "set", "backend", "abc"},                                  // not a dollar amount
		{"budget", "set", "backend", "--tokens", "5m", "--window", "weekly"}, // bad window
	} {
		if code, out := runYakos(t, state, nil, args...); code != 1 {
			t.Errorf("%v: exit %d, want 1: %s", args, code, out)
		}
	}
	if pol, _ := budget.LoadPolicy(state); len(pol.Agents) != 0 {
		t.Errorf("a failed set must not write a policy: %+v", pol.Agents)
	}
}

// writeTokenLog appends one subscription dispatch_finished line with the given
// token counts for agent, stamped now.
func writeTokenLog(t *testing.T, state, agent string, in, out int64) {
	t.Helper()
	line := fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":%q,"runtime":"claude","billing":"subscription","usage":{"input_tokens":%d,"output_tokens":%d,"total_cost_usd":0}}`+"\n",
		time.Now().UTC().Format(time.RFC3339), agent, in, out)
	if err := appendFile(filepath.Join(state, "dispatch-log.ndjson"), line); err != nil {
		t.Fatal(err)
	}
}

// `budget check` is the hook-callable pre-flight: exit 4 at a token hard_stop, and
// its machine-readable first line gains the token fields only for an agent that has
// a token limit.
func TestBudgetCheck_TokenLimit(t *testing.T) {
	state := t.TempDir()
	if err := budget.SetTokenLimit(state, "backend", 1000, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	if err := budget.SetLimit(state, "plain", 10, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	writeTokenLog(t, state, "backend", 600, 100)

	code, out := runYakos(t, state, nil, "budget", "check", "backend")
	if code != 0 || !strings.Contains(out, "state=ok") || !strings.Contains(out, "spent_tokens=700 limit_tokens=1000") {
		t.Fatalf("under the limit: exit %d: %s", code, out)
	}
	if code, out = runYakos(t, state, nil, "budget", "check", "plain"); code != 0 || strings.Contains(out, "limit_tokens") {
		t.Fatalf("a dollar-only agent's line is unchanged: exit %d: %s", code, out)
	}

	writeTokenLog(t, state, "backend", 400, 0)
	code, out = runYakos(t, state, nil, "budget", "check", "backend")
	if code != budget.ExitHardStop || !strings.Contains(out, "state=hard_stop") || !strings.Contains(out, "tokens") {
		t.Fatalf("at the token limit: exit %d (want %d): %s", code, budget.ExitHardStop, out)
	}
}

// A subscription agent with only a token limit is refused at dispatch when the limit
// is reached, before the dispatch is handed to the bash implementation.
func TestBudgetGateBlocksBashPassthroughOnATokenLimit(t *testing.T) {
	state := t.TempDir()
	if err := budget.SetTokenLimit(state, "backend", 1000, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	writeTokenLog(t, state, "backend", 1000, 0)
	code, out := runYakos(t, state, []string{"YAKOS_IMPL=bash"}, "dispatch", "backend", "do it", "--project", t.TempDir())
	if code != budget.ExitHardStop || !strings.Contains(out, "dispatch refused") || !strings.Contains(out, "tokens") {
		t.Fatalf("token hard_stop must refuse at the gate: exit %d: %s", code, out)
	}
}

// The status table gains token columns only when there is a token to show; without
// one it is byte-for-byte the dollar table.
func TestBudgetStatusTable_TokenColumnsAppearOnlyWhenNeeded(t *testing.T) {
	state := t.TempDir()
	if err := budget.SetLimit(state, "plain", 10, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	code, out := runYakos(t, state, nil, "budget", "status", "--project", t.TempDir())
	if code != 0 || strings.Join(strings.Fields(strings.SplitN(out, "\n", 2)[0]), " ") != "AGENT STATE SPENT LIMIT USED WINDOW SOURCE" || strings.Contains(out, "TOKEN") {
		t.Fatalf("no tokens: exit %d:\n%s", code, out)
	}

	writeTokenLog(t, state, "plain", 5, 5)
	code, out = runYakos(t, state, nil, "budget", "status", "--project", t.TempDir())
	if code != 0 || !strings.Contains(out, "TOKENS") || !strings.Contains(out, "TOKEN LIMIT") {
		t.Fatalf("with tokens used: exit %d:\n%s", code, out)
	}
	hdr := strings.SplitN(out, "\n", 2)[0]
	if strings.Index(hdr, "TOKENS") > strings.Index(hdr, "SPENT") {
		t.Errorf("tokens come before dollars (tokens are primary): %q", hdr)
	}
	var plainRow string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "plain") {
			plainRow = l
		}
	}
	if !strings.Contains(plainRow, "10") || !strings.Contains(plainRow, "off") {
		t.Errorf("row = %q (10 tokens used, no token limit)", plainRow)
	}
}

func TestBudgetFlagsRegistered_Tokens(t *testing.T) {
	var found bool
	for _, e := range commandRegistry {
		if e.Name != "budget" {
			continue
		}
		for _, s := range e.Specs.Specs {
			found = found || s.Name == "--tokens"
		}
	}
	if !found {
		t.Fatal("--tokens must be registered for yakos budget")
	}
	var help strings.Builder
	printBudgetHelp(&help)
	for _, want := range []string{"--tokens", "limit", "5000000"} {
		if !strings.Contains(help.String(), want) {
			t.Errorf("help lacks %q", want)
		}
	}
	_ = os.Stdout
}
