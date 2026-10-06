package budget

// tokens_test.go covers K-136 in the budget package: tokens are the primary unit,
// `limit_tokens` trips exactly like `limit_usd`, and dollars count only for runs
// billed per API call. Every test runs in a temp state directory.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ledgerLine renders a dispatch_finished line as the Go dispatcher writes it after
// K-136: a billing mode, the four token counts and the usage dollar figure.
func ledgerLine(agent, ts, billing string, in, out, cacheRead, cacheCreate int64, usd float64) string {
	b := ""
	if billing != "" {
		b = fmt.Sprintf(`,"billing":%q`, billing)
	}
	return fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":%q,"runtime":"claude","exit_code":0%s,"usage":{"input_tokens":%d,"output_tokens":%d,"cache_read":%d,"cache_creation":%d,"total_cost_usd":%v}}`,
		ts, agent, b, in, out, cacheRead, cacheCreate, usd)
}

func setTokenLimit(t testing.TB, dir, agent string, tokens int64, w Window) {
	t.Helper()
	if err := SetTokenLimit(dir, agent, tokens, w); err != nil {
		t.Fatal(err)
	}
}

// A token limit trips exactly like a dollar limit: ok below the warning
// percentage, warning from it, hard_stop at 100%, and dispatch refuses at the stop.
func TestTokenLimit_TripsLikeUSDLimit(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setTokenLimit(t, dir, "backend", 1000, Monthly)

	appendLog(t, dir, ledgerLine("backend", octMid, "subscription", 500, 100, 100, 99, 0))
	if st := mustEval(t, "backend", o); st.State != StateOK || st.SpentTokens != 799 || st.LimitTokens != 1000 {
		t.Fatalf("799 of 1000 must be ok: %+v", st)
	}
	if _, err := Enforce("backend", o); err != nil {
		t.Fatalf("below the limit dispatch proceeds: %v", err)
	}

	appendLog(t, dir, ledgerLine("backend", octMid, "subscription", 1, 0, 0, 0, 0))
	st := mustEval(t, "backend", o)
	if st.State != StateWarning || st.Refused() || st.Pct != 80 || st.TokensPct != 80 {
		t.Fatalf("800 of 1000 must warn, not stop: %+v", st)
	}
	if _, err := Enforce("backend", o); err != nil {
		t.Fatalf("a warning does not refuse: %v", err)
	}

	appendLog(t, dir, ledgerLine("backend", octMid, "subscription", 200, 0, 0, 0, 0))
	st = mustEval(t, "backend", o)
	if st.State != StateHardStop || !st.Refused() || st.Reason != ReasonExhausted {
		t.Fatalf("1000 of 1000 must be hard_stop: %+v", st)
	}
	_, err := Enforce("backend", o)
	if !IsRefused(err) {
		t.Fatalf("a token hard_stop must refuse a new dispatch, got %v", err)
	}
	for _, want := range []string{`agent "backend"`, "1,000 of its 1,000 tokens", "yakos budget set backend --tokens <n>", "yakos budget reset backend"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "$") {
		t.Errorf("a token-only refusal must not talk about dollars: %q", err.Error())
	}
	if msg := st.Message(); !strings.Contains(msg, "HARD STOP, 1,000 of 1,000 tokens (monthly) spent") {
		t.Errorf("Message() = %q", msg)
	}
}

// limit_usd moves only for runs billed per API call. A subscription or local run
// adds tokens and no dollars, even when its usage cost field is non-zero.
func TestUSDLimit_IgnoresSubscriptionAndLocalRows(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "backend", 5, Monthly)

	appendLog(t, dir,
		ledgerLine("backend", octMid, "subscription", 10, 10, 0, 0, 150), // a legacy-style cost under a subscription must not count
		ledgerLine("backend", octMid, "local", 10, 10, 0, 0, 150),
	)
	st := mustEval(t, "backend", o)
	if st.State != StateOK || st.SpentUSD != 0 {
		t.Fatalf("subscription and local runs are not dollar spend: %+v", st)
	}
	if st.SpentTokens != 40 {
		t.Fatalf("they still count as tokens: %+v", st)
	}

	appendLog(t, dir, ledgerLine("backend", octMid, "api", 10, 10, 0, 0, 6))
	if st := mustEval(t, "backend", o); st.State != StateHardStop || st.SpentUSD != 6 {
		t.Fatalf("an api run counts its dollars: %+v", st)
	}
}

// A row from before the billing field keeps counting its dollars, so a dollar
// budget does not reset itself on upgrade; so does a billing value this build does
// not know (the safe direction for a cost guard).
func TestUSDLimit_LegacyAndUnknownBillingStillCount(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "backend", 10, Monthly)
	appendLog(t, dir,
		finished("backend", octMid, 4),                                  // legacy: no billing field
		ledgerLine("backend", octMid, "prepaid-credits", 1, 1, 0, 0, 3), // a mode from the future
	)
	if st := mustEval(t, "backend", o); st.SpentUSD != 7 {
		t.Fatalf("legacy and unknown-billing dollars count: %+v", st)
	}
}

// Tokens are summed for every runtime and billing mode, all four kinds.
func TestTokens_SumEveryKindOfEveryRun(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setTokenLimit(t, dir, "backend", 1_000_000, Monthly)
	appendLog(t, dir,
		ledgerLine("backend", octMid, "subscription", 1, 2, 3, 4, 0),
		ledgerLine("backend", octMid, "api", 10, 20, 30, 40, 0.5),
		ledgerLine("backend", octMid, "local", 100, 200, 300, 400, 0),
		finished("backend", octMid, 1), // no token counts: contributes dollars only
		`{"type":"dispatch_finished","ts":"`+octMid+`","agent":"backend","runtime":"codex","exit_code":0,"billing":"subscription","usage":{"input_tokens":7,"output_tokens":5,"cache_read":9}}`,
	)
	st := mustEval(t, "backend", o)
	if want := int64(1 + 2 + 3 + 4 + 10 + 20 + 30 + 40 + 100 + 200 + 300 + 400 + 7 + 5 + 9); st.SpentTokens != want {
		t.Fatalf("SpentTokens = %d, want %d", st.SpentTokens, want)
	}
	agg := mustAggregate(t, dir)
	if got := agg.tokens("backend", Monthly, WindowKey(Monthly, o.now())); got.Input != 118 || got.Output != 227 || got.CacheRead != 342 || got.CacheCreation != 444 {
		t.Fatalf("per-kind sums = %+v", got)
	}
}

// Cache tokens alone can trip a token limit: the total is every kind.
func TestTokenLimit_CountsCacheTokens(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setTokenLimit(t, dir, "backend", 100, Monthly)
	appendLog(t, dir, ledgerLine("backend", octMid, "subscription", 0, 0, 60, 40, 0))
	if st := mustEval(t, "backend", o); st.State != StateHardStop {
		t.Fatalf("100 cache tokens of 100 must stop: %+v", st)
	}
}

// Either limit stops the agent; the refusal names the one that tripped.
func TestBothLimits_EitherOneTrips(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "backend", 10, Monthly)
	setTokenLimit(t, dir, "backend", 1000, Monthly)

	// Dollars fine, tokens exhausted.
	appendLog(t, dir, ledgerLine("backend", octMid, "api", 1000, 0, 0, 0, 1))
	_, err := Enforce("backend", o)
	if !IsRefused(err) || !strings.Contains(err.Error(), "tokens") || !strings.Contains(err.Error(), "--tokens") {
		t.Fatalf("exhausted tokens must refuse and say so: %v", err)
	}
	if strings.Contains(err.Error(), "$1.00 of its $10.00 monthly budget") {
		t.Fatalf("the dollar limit did not trip and must not be blamed: %v", err)
	}

	// Tokens fine, dollars exhausted.
	dir2 := t.TempDir()
	o2 := Options{StateDir: dir2, Now: clock(octMid)}
	setLimit(t, dir2, "backend", 10, Monthly)
	setTokenLimit(t, dir2, "backend", 1000, Monthly)
	appendLog(t, dir2, ledgerLine("backend", octMid, "api", 5, 5, 0, 0, 11))
	_, err = Enforce("backend", o2)
	if !IsRefused(err) || !strings.Contains(err.Error(), "$11.00 of its $10.00") {
		t.Fatalf("exhausted dollars must refuse and say so: %v", err)
	}

	// Both exhausted: one message that says both.
	appendLog(t, dir2, ledgerLine("backend", octMid, "api", 2000, 0, 0, 0, 0))
	_, err = Enforce("backend", o2)
	if !IsRefused(err) || !strings.Contains(err.Error(), "$11.00 of its $10.00") || !strings.Contains(err.Error(), "2,010 of its 1,000 tokens") {
		t.Fatalf("both exhausted: %v", err)
	}

	// Pct is the binding limit's share.
	dir3 := t.TempDir()
	o3 := Options{StateDir: dir3, Now: clock(octMid)}
	setLimit(t, dir3, "backend", 100, Monthly)
	setTokenLimit(t, dir3, "backend", 1000, Monthly)
	appendLog(t, dir3, ledgerLine("backend", octMid, "api", 850, 0, 0, 0, 10))
	if st := mustEval(t, "backend", o3); st.State != StateWarning || st.Pct != 85 || st.TokensPct != 85 {
		t.Fatalf("tokens at 85%% with dollars at 10%% must warn at 85%%: %+v", st)
	}
}

// A token-only agent has no dollar limit: LimitUSD stays 0, and dollars never stop it.
func TestTokenOnlyAgent_DollarsNeverStopIt(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setTokenLimit(t, dir, "backend", 1000, Monthly)
	appendLog(t, dir, ledgerLine("backend", octMid, "api", 1, 1, 0, 0, 9999))
	st := mustEval(t, "backend", o)
	if st.State != StateOK || st.LimitUSD != 0 || st.SpentUSD != 9999 {
		t.Fatalf("%+v", st)
	}
	if _, err := Enforce("backend", o); err != nil {
		t.Fatal(err)
	}
}

// Tokens are reported even when only a dollar limit is set (tokens are primary).
func TestSpentTokensReportedWithoutATokenLimit(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "backend", 50, Monthly)
	appendLog(t, dir, ledgerLine("backend", octMid, "subscription", 300, 200, 0, 0, 0))
	st := mustEval(t, "backend", o)
	if st.SpentTokens != 500 || st.LimitTokens != 0 || st.TokensPct != 0 || st.State != StateOK {
		t.Fatalf("%+v", st)
	}
	if got := st.Message(); strings.Contains(got, "token") {
		t.Errorf("a dollar-only agent's message is unchanged: %q", got)
	}
}

// `yakos budget reset` baselines tokens as well as dollars.
func TestTokenLimit_ResetStartsTheWindowOver(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setTokenLimit(t, dir, "backend", 1000, Monthly)
	appendLog(t, dir, ledgerLine("backend", octMid, "subscription", 1200, 0, 0, 0, 0))
	if st := mustEval(t, "backend", o); st.State != StateHardStop {
		t.Fatalf("%+v", st)
	}
	if _, err := Reset("backend", o); err != nil {
		t.Fatal(err)
	}
	st := mustEval(t, "backend", o)
	if st.State != StateOK || st.SpentTokens != 0 {
		t.Fatalf("after a reset the spent tokens start over: %+v", st)
	}
	appendLog(t, dir, ledgerLine("backend", octMid, "subscription", 300, 0, 0, 0, 0))
	if st := mustEval(t, "backend", o); st.SpentTokens != 300 {
		t.Fatalf("tokens after the reset count: %+v", st)
	}
}

// The supervisor's dispatch stop is 2x its limit; a token limit scales the same way.
func TestTokenLimit_StopFactorScalesTokens(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setTokenLimit(t, dir, "supervisor", 1000, Monthly)
	appendLog(t, dir, ledgerLine("supervisor", octMid, "subscription", 1500, 0, 0, 0, 0))
	st := mustEval(t, "supervisor", o)
	if st.State != StateHardStop || st.StopTokens != 2000 {
		t.Fatalf("%+v", st)
	}
	if _, err := Enforce("supervisor", o); err != nil {
		t.Fatalf("between 1x and 2x the supervisor still dispatches: %v", err)
	}
	appendLog(t, dir, ledgerLine("supervisor", octMid, "subscription", 600, 0, 0, 0, 0))
	if _, err := Enforce("supervisor", o); !IsRefused(err) {
		t.Fatalf("past 2x the supervisor is refused, got %v", err)
	}
}

// The cache from before K-136 (version 2, dollars only) cannot answer a token
// question: it is rebuilt from the log.
func TestAggregate_OldCacheVersionIsRebuilt(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setTokenLimit(t, dir, "backend", 1000, Monthly)
	appendLog(t, dir, ledgerLine("backend", octMid, "subscription", 400, 0, 0, 0, 0))
	old := fmt.Sprintf(`{"version":2,"zone":%q,"offset":0,"head_len":0,"head":"","agents":{"backend":{"lifetime":0,"monthly":{}}}}`, zoneFingerprint())
	if err := os.WriteFile(filepath.Join(dir, aggregateFileName), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := mustEval(t, "backend", o); st.SpentTokens != 400 {
		t.Fatalf("an old cache must be rebuilt from the log: %+v", st)
	}
}

// A line that spells a count oddly still counts its other fields, and a hostile
// number never moves a total.
func TestScan_ToleratesOddTokenSpellings(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setTokenLimit(t, dir, "backend", 100000, Monthly)
	lines := []string{
		// float spelling and a negative count: 1500 counts, -5 does not.
		`{"type":"dispatch_finished","ts":"` + octMid + `","agent":"backend","billing":"subscription","usage":{"input_tokens":1.5e3,"output_tokens":-5}}`,
		// beyond any real run: the huge count is dropped, the small one kept.
		`{"type":"dispatch_finished","ts":"` + octMid + `","agent":"backend","billing":"subscription","usage":{"input_tokens":1e30,"output_tokens":7}}`,
		// a usage object with no tokens and no dollars is nothing.
		`{"type":"dispatch_finished","ts":"` + octMid + `","agent":"backend","billing":"subscription","usage":{}}`,
		`{"type":"dispatch_finished","ts":"` + octMid + `","agent":"backend","billing":"subscription"}`,
	}
	appendLog(t, dir, lines...)
	if st := mustEval(t, "backend", o); st.SpentTokens != 1507 {
		t.Fatalf("SpentTokens = %d, want 1507: %+v", st.SpentTokens, st)
	}
}

// Policy: SetTokenLimit writes limit_tokens next to limit_usd, validates its input
// and 0 turns the limit off; the file stays owner-only.
func TestSetTokenLimit_PolicyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	setLimit(t, dir, "backend", 20, Lifetime)
	setTokenLimit(t, dir, "backend", 2_500_000, Lifetime)
	pol, err := LoadPolicy(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := pol.Agents["backend"]
	if a.LimitUSD == nil || *a.LimitUSD != 20 || a.LimitTokens == nil || *a.LimitTokens != 2_500_000 || a.Window != "lifetime" {
		t.Fatalf("policy entry = %+v", a)
	}
	data, err := os.ReadFile(PolicyPath(dir))
	if err != nil || !strings.Contains(string(data), "limit_tokens: 2500000") {
		t.Fatalf("policy file: %q (%v)", data, err)
	}
	if lim := Resolve("backend", pol, nil); lim.Tokens != 2_500_000 || lim.USD != 20 || lim.Window != Lifetime || lim.Source != "policy" {
		t.Fatalf("Resolve = %+v", lim)
	}

	for _, bad := range []int64{-1, maxTokenLimit + 1} {
		if err := SetTokenLimit(dir, "backend", bad, Monthly); err == nil {
			t.Errorf("SetTokenLimit(%d) must fail", bad)
		}
	}
	if err := SetTokenLimit(dir, "bad agent!", 5, Monthly); err == nil {
		t.Error("an invalid agent name must fail")
	}
	if err := SetTokenLimit(dir, "backend", 5, Window("weekly")); err == nil {
		t.Error("an invalid window must fail")
	}

	setTokenLimit(t, dir, "backend", 0, Lifetime)
	pol, _ = LoadPolicy(dir)
	if lim := Resolve("backend", pol, nil); lim.Tokens != 0 || lim.USD != 20 {
		t.Fatalf("0 turns the token limit off and leaves the dollar limit: %+v", lim)
	}
}

// A token-only entry still reports its source, and a project cannot add a token
// limit (it has no field for one).
func TestResolve_TokenOnlyEntryHasASource(t *testing.T) {
	zero := int64(500)
	lim := Resolve("backend", Policy{Agents: map[string]AgentLimit{"backend": {LimitTokens: &zero}}}, nil)
	if lim.Tokens != 500 || lim.USD != 0 || lim.Source != "policy" {
		t.Fatalf("%+v", lim)
	}
	lim = Resolve("backend", Policy{Default: AgentLimit{LimitTokens: &zero}}, nil)
	if lim.Tokens != 500 || lim.Source != "policy-default" {
		t.Fatalf("%+v", lim)
	}
	neg := int64(-5)
	if lim = Resolve("backend", Policy{Agents: map[string]AgentLimit{"backend": {LimitTokens: &neg}}}, nil); lim.Tokens != 0 {
		t.Fatalf("a negative limit is off: %+v", lim)
	}
}

func TestCommas(t *testing.T) {
	for in, want := range map[int64]string{0: "0", 7: "7", 999: "999", 1000: "1,000", 1234567: "1,234,567", -1234: "-1,234"} {
		if got := commas(in); got != want {
			t.Errorf("commas(%d) = %q, want %q", in, got, want)
		}
	}
}

func mustAggregate(t testing.TB, dir string) *aggregate {
	t.Helper()
	agg, err := refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	return agg
}
