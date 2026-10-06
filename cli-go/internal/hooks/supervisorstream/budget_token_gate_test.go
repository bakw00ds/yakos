package supervisorstream_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
)

// K-136: the supervisor launch gate decides over both budget units. A token-only
// budget (no dollar limit, which is what a subscription operator has) refuses
// routine launches at its hard stop and blocks high-risk ones at 2x its tokens,
// with a CRITICAL finding, exactly as a dollar budget does; "off" needs both
// limits gone. Bash twin: tests/run-supervisor-budget-test.sh (11).

// tokenBudget gives the supervisor the two limits (0 turns a limit off, a built-in
// one included) and records one run that spent usd dollars and tokens tokens in
// the hook's month. The row has no billing field, so it counts like a legacy row:
// both units. It returns the hook under test and its work directory.
func tokenBudget(t *testing.T, usdLimit float64, tokLimit int64, usd float64, tokens int64) (hookUnderTest, string) {
	t.Helper()
	h, rec, work, env := gateHook(t, "min_launch_interval_s: 0\n", "")
	dir := os.Getenv("YAKOS_DISPATCH_LOG")
	if err := budget.SetLimit(dir, "supervisor", usdLimit, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	if err := budget.SetTokenLimit(dir, "supervisor", tokLimit, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	if usd > 0 || tokens > 0 {
		line := fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":"supervisor","usage":{"input_tokens":%d,"output_tokens":0,"total_cost_usd":%v}}`+"\n",
			fixedNow().UTC().Format(time.RFC3339), tokens, usd)
		if err := os.WriteFile(filepath.Join(dir, "dispatch-log.ndjson"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return hookUnderTest{h: h, rec: rec, env: env}, work
}

// recordWith returns the one hook-log record whose line contains sub.
func recordWith(t *testing.T, work, sub string) string {
	t.Helper()
	var got []string
	for _, rec := range logMessages(t, work) {
		if strings.Contains(rec, sub) {
			got = append(got, rec)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want exactly one record containing %q, got %d:\n%s", sub, len(got), allLogs(t, work))
	}
	return got[0]
}

// A token-only budget past its limit: a routine launch is refused, with one WARN
// naming tokens (no dollar field in it), one stderr line, exit 0 and no launch.
func TestTokenOnlyBudgetRoutineRefusedAtHardStop(t *testing.T) {
	b, work := tokenBudget(t, 0, 1000, 0, 1500)
	out := bigEditOut(t, b)
	if len(b.rec.specs) != 0 {
		t.Fatalf("a routine launch at a token hard stop must be refused, got %d launches", len(b.rec.specs))
	}
	if out.ExitCode != 0 {
		t.Fatalf("the hook must exit 0, got %d", out.ExitCode)
	}
	rec := recordWith(t, work, "skipping this routine")
	for _, want := range []string{`"severity":"WARN"`, `"spent_tokens":1500,"limit_tokens":1000,"budget_reason":"budget_exhausted","kind":"routine"`} {
		if !strings.Contains(rec, want) {
			t.Errorf("the record lacks %s: %s", want, rec)
		}
	}
	if strings.Contains(rec, "_usd") {
		t.Errorf("a token record carries no dollar field: %s", rec)
	}
	if strings.Contains(allLogs(t, work), "forked async") {
		t.Error("a refused launch must not log forked async")
	}
	want := "supervisor-stream: supervisor budget exhausted (1500 of 1000 tokens); routine supervisor runs are skipped until it is raised, reset, or the month rolls over (yakos budget status)\n"
	if string(out.Stderr) != want {
		t.Errorf("stderr = %q, want %q", out.Stderr, want)
	}
}

// Past the limit and under 2x it, a high-risk launch runs and says it is exempt; at
// the stop itself (>=) nothing launches and ONE token CRITICAL is written.
func TestTokenOnlyBudgetHighRiskExemptUnderTheCeilingThenCritical(t *testing.T) {
	b, work := tokenBudget(t, 0, 1000, 0, 1999) // one token under the 2x stop
	out := riskEdit(t, b.h, b.env)
	if out.ExitCode != 0 || len(b.rec.specs) != 1 {
		t.Fatalf("a high-risk launch under the token ceiling must run: exit %d launches %d", out.ExitCode, len(b.rec.specs))
	}
	rec := recordWith(t, work, "high-risk launch allowed under the ceiling")
	if want := `"spent_tokens":1999,"limit_tokens":1000,"ceiling_tokens":2000,"budget_reason":"budget_exhausted"`; !strings.Contains(rec, want) || strings.Contains(rec, "_usd") {
		t.Errorf("the exempt note should be a token record with %s: %s", want, rec)
	}
	if want := "supervisor-stream: supervisor budget exhausted (1999 of 1000 tokens); launching high-risk supervision under the 2000 token ceiling\n"; string(out.Stderr) != want {
		t.Errorf("stderr = %q, want %q", out.Stderr, want)
	}
	if findings(t, work) != "" {
		t.Error("no CRITICAL finding below the ceiling")
	}

	b2, work2 := tokenBudget(t, 0, 1000, 0, 2000) // at the 2x stop
	out = riskEdit(t, b2.h, b2.env)
	if out.ExitCode != 0 || len(b2.rec.specs) != 0 {
		t.Fatalf("at the token ceiling nothing launches: exit %d launches %d", out.ExitCode, len(b2.rec.specs))
	}
	f := findings(t, work2)
	for _, want := range []string{`"overall":"CRITICAL"`, `"synthetic":true`, `"recommended_action":"surface_to_operator"`,
		"Supervisor token-budget ceiling (2000 tokens) reached: further high-risk events are recorded but no longer supervised."} {
		if !strings.Contains(f, want) {
			t.Errorf("the finding lacks %s: %s", want, f)
		}
	}
	if strings.Contains(f, "dollar") {
		t.Errorf("a token ceiling is not a dollar one: %s", f)
	}
	rec = recordWith(t, work2, "supervisor budget ceiling reached")
	if want := `"spent_tokens":2000,"ceiling_tokens":2000,"budget_reason":"budget_exhausted"`; !strings.Contains(rec, want) || strings.Contains(rec, "_usd") {
		t.Errorf("the ceiling note should be a token record with %s: %s", want, rec)
	}
	if want := "supervisor-stream: supervisor budget ceiling (2000 tokens) reached; high-risk supervisor runs are skipped\n"; string(out.Stderr) != want {
		t.Errorf("stderr = %q, want %q", out.Stderr, want)
	}
	riskEdit(t, b2.h, b2.env) // once per session: a second event adds no second finding
	if n := strings.Count(findings(t, work2), "\n"); n != 1 {
		t.Errorf("%d findings, want 1", n)
	}
}

// At the warning level a token-only budget says so in tokens.
func TestTokenBudgetWarningNamesTokens(t *testing.T) {
	b, work := tokenBudget(t, 0, 1000, 0, 850)
	out := bigEditOut(t, b)
	if out.ExitCode != 0 || len(b.rec.specs) != 1 {
		t.Fatalf("warning must still launch: exit %d launches %d", out.ExitCode, len(b.rec.specs))
	}
	rec := recordWith(t, work, "supervisor budget at warning level")
	if want := `"spent_tokens":850,"limit_tokens":1000,"budget_reason":"budget_warning"`; !strings.Contains(rec, want) || strings.Contains(rec, "_usd") {
		t.Errorf("want a token record with %s: %s", want, rec)
	}
	if want := "supervisor-stream: supervisor budget at 85% (850 of 1000 tokens); at 100% routine supervisor runs stop\n"; string(out.Stderr) != want {
		t.Errorf("stderr = %q, want %q", out.Stderr, want)
	}
}

// "Off" needs both limits gone: with neither a dollar nor a token limit, a vast
// spend of both is not a budget event; nothing is logged and the launch runs.
func TestBudgetOffOnBothUnitsStaysOff(t *testing.T) {
	b, work := tokenBudget(t, 0, 0, 5000, 90_000_000_000)
	out := bigEditOut(t, b)
	if out.ExitCode != 0 || len(b.rec.specs) != 1 {
		t.Fatalf("a budget off on both units must not stop a launch: exit %d launches %d", out.ExitCode, len(b.rec.specs))
	}
	if logs := allLogs(t, work); strings.Contains(logs, "budget") || strings.Contains(string(out.Stderr), "budget") {
		t.Errorf("nothing budget-related may be logged when the budget is off:\n%s\nstderr %q", logs, out.Stderr)
	}
	// A high-risk event is as free: no ceiling, no finding.
	out = riskEdit(t, b.h, b.env)
	if out.ExitCode != 0 || findings(t, work) != "" {
		t.Errorf("a budget off on both units has no ceiling: exit %d finding %q", out.ExitCode, findings(t, work))
	}
}

// Turning off only the dollar limit leaves the supervisor's built-in token limit on
// (33,000,000 a month), and the gate enforces it: this is the subscription
// operator's backstop, and a dollar limit of 0 must not read as "no budget".
func TestDollarLimitOffLeavesTheTokenLimitGated(t *testing.T) {
	h, rec, work, env := gateHook(t, "min_launch_interval_s: 0\n", "")
	dir := os.Getenv("YAKOS_DISPATCH_LOG")
	if err := budget.SetLimit(dir, "supervisor", 0, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":"supervisor","billing":"subscription","usage":{"input_tokens":34000000,"output_tokens":0,"total_cost_usd":0}}`+"\n",
		fixedNow().UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(dir, "dispatch-log.ndjson"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	b := hookUnderTest{h: h, rec: rec, env: env}
	out := bigEditOut(t, b)
	if len(rec.specs) != 0 || out.ExitCode != 0 {
		t.Fatalf("34M tokens against the built-in 33M must refuse a routine launch: exit %d launches %d", out.ExitCode, len(rec.specs))
	}
	r := recordWith(t, work, "skipping this routine")
	if want := `"spent_tokens":34000000,"limit_tokens":33000000,"budget_reason":"budget_exhausted","kind":"routine"`; !strings.Contains(r, want) {
		t.Errorf("want %s: %s", want, r)
	}
}

// Dollar behaviour is unchanged when a token limit is configured but has not been
// reached: the message names dollars, field for field as before, with no token field.
func TestDollarMessagesUnchangedWhileATokenLimitIsPresent(t *testing.T) {
	b, work := tokenBudget(t, 100, 33_000_000, 100, 10)
	out := bigEditOut(t, b)
	if len(b.rec.specs) != 0 {
		t.Fatalf("launched at a dollar hard stop: %d", len(b.rec.specs))
	}
	rec := recordWith(t, work, "skipping this routine")
	if want := `"spent_usd":100,"limit_usd":100,"budget_reason":"budget_exhausted","kind":"routine"`; !strings.Contains(rec, want) || strings.Contains(rec, "_tokens") {
		t.Errorf("a dollar record is the one it always was, with no token field (%s): %s", want, rec)
	}
	if want := "supervisor-stream: supervisor budget exhausted ($100.00 of $100.00); routine supervisor runs are skipped until it is raised, reset, or the month rolls over (yakos budget status)\n"; string(out.Stderr) != want {
		t.Errorf("stderr = %q, want %q", out.Stderr, want)
	}
	// And a dollar CRITICAL names dollars.
	b2, work2 := tokenBudget(t, 100, 33_000_000, 250, 10)
	riskEdit(t, b2.h, b2.env)
	if f := findings(t, work2); !strings.Contains(f, "Supervisor dollar-budget ceiling ($200.00) reached") {
		t.Errorf("the dollar ceiling finding: %q", f)
	}
}

// With both limits set, the one that tripped is named; with both reached, tokens
// come first even when the dollars have the larger share.
func TestBothLimitsNameTheLimitThatTripped(t *testing.T) {
	b, work := tokenBudget(t, 100, 1000, 10, 1500) // tokens reached, dollars at 10%
	bigEditOut(t, b)
	rec := recordWith(t, work, "skipping this routine")
	if want := `"spent_tokens":1500,"limit_tokens":1000,"budget_reason":"budget_exhausted","kind":"routine"`; !strings.Contains(rec, want) || strings.Contains(rec, "_usd") {
		t.Errorf("only the token limit is reached, so the record names tokens (%s): %s", want, rec)
	}

	b2, work2 := tokenBudget(t, 100, 1000, 150, 10) // dollars reached, tokens at 1%
	bigEditOut(t, b2)
	rec = recordWith(t, work2, "skipping this routine")
	if want := `"spent_usd":150,"limit_usd":100,"budget_reason":"budget_exhausted","kind":"routine"`; !strings.Contains(rec, want) || strings.Contains(rec, "_tokens") {
		t.Errorf("only the dollar limit is reached, so the record names dollars (%s): %s", want, rec)
	}

	b3, work3 := tokenBudget(t, 100, 1000, 180, 1500) // both reached; dollars at 180%, tokens at 150%
	bigEditOut(t, b3)
	rec = recordWith(t, work3, "skipping this routine")
	if want := `"spent_tokens":1500,"limit_tokens":1000,"budget_reason":"budget_exhausted","kind":"routine"`; !strings.Contains(rec, want) || strings.Contains(rec, "_usd") {
		t.Errorf("both reached: tokens first, whatever the shares (%s): %s", want, rec)
	}
}

// At the warning level the unit with the larger share of its limit is named, and a
// tie goes to tokens.
func TestWarningNamesTheUnitWithTheLargerShare(t *testing.T) {
	for _, c := range []struct {
		name   string
		usd    float64
		tokens int64
		tok    bool
	}{
		{"tokens have the larger share", 85, 900, true},
		{"dollars have the larger share", 90, 850, false},
		{"a tie goes to tokens", 85, 850, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, work := tokenBudget(t, 100, 1000, c.usd, c.tokens)
			out := bigEditOut(t, b)
			rec := recordWith(t, work, "supervisor budget at warning level")
			if c.tok {
				if !strings.Contains(rec, `"spent_tokens":`) || strings.Contains(rec, "_usd") || !strings.Contains(string(out.Stderr), "tokens)") {
					t.Errorf("want tokens named: %s / %q", rec, out.Stderr)
				}
			} else if !strings.Contains(rec, `"spent_usd":`) || strings.Contains(rec, "_tokens") || !strings.Contains(string(out.Stderr), "($90.00 of $100.00)") {
				t.Errorf("want dollars named: %s / %q", rec, out.Stderr)
			}
		})
	}
}

// The 2x ceiling note and its CRITICAL name the limit that is past its own stop,
// not merely the one that reached its limit: dollars at 2.5x and tokens at 1.5x
// make the dollar ceiling the one reached, while the routine refusal of that same
// state names tokens (tokens first, they reached their limit).
func TestCeilingNamesTheLimitThatIsOverItsStop(t *testing.T) {
	b, work := tokenBudget(t, 100, 1000, 250, 1500)
	riskEdit(t, b.h, b.env)
	if len(b.rec.specs) != 0 {
		t.Fatalf("past the dollar ceiling nothing launches: %d", len(b.rec.specs))
	}
	rec := recordWith(t, work, "supervisor budget ceiling reached")
	if want := `"spent_usd":250,"ceiling_usd":200,"budget_reason":"budget_exhausted"`; !strings.Contains(rec, want) || strings.Contains(rec, "_tokens") {
		t.Errorf("the dollar ceiling is the one reached (%s): %s", want, rec)
	}
	if f := findings(t, work); !strings.Contains(f, "Supervisor dollar-budget ceiling ($200.00) reached") {
		t.Errorf("finding %q", f)
	}

	b2, work2 := tokenBudget(t, 100, 1000, 250, 1500)
	bigEditOut(t, b2)
	if rec := recordWith(t, work2, "skipping this routine"); !strings.Contains(rec, `"spent_tokens":1500,"limit_tokens":1000`) {
		t.Errorf("the routine refusal names tokens: %s", rec)
	}

	b3, work3 := tokenBudget(t, 100, 1000, 150, 2500) // tokens past their stop, dollars only at their limit
	riskEdit(t, b3.h, b3.env)
	rec = recordWith(t, work3, "supervisor budget ceiling reached")
	if want := `"spent_tokens":2500,"ceiling_tokens":2000,"budget_reason":"budget_exhausted"`; !strings.Contains(rec, want) || strings.Contains(rec, "_usd") {
		t.Errorf("the token ceiling is the one reached (%s): %s", want, rec)
	}
	if f := findings(t, work3); !strings.Contains(f, "Supervisor token-budget ceiling (2000 tokens) reached") {
		t.Errorf("finding %q", f)
	}
}

// A spend log that cannot be read is a failed read, and a zero dollar limit does not
// turn it into "off": a token-only budget still says so, once, as unavailable.
func TestTokenOnlyBudgetUnreadableSpendStillWarns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("there is no portable way to make the spend log unreadable on windows: a directory where " +
			"it belongs opens fine there and a chmod 000 file stays readable")
	}
	b, work := tokenBudget(t, 0, 1000, 0, 0)
	if err := os.Mkdir(filepath.Join(os.Getenv("YAKOS_DISPATCH_LOG"), "dispatch-log.ndjson"), 0o700); err != nil {
		t.Fatal(err)
	}
	out := bigEditOut(t, b)
	if out.ExitCode != 0 || len(b.rec.specs) != 1 {
		t.Fatalf("an unreadable spend log must fail open: exit %d launches %d", out.ExitCode, len(b.rec.specs))
	}
	rec := recordWith(t, work, `"budget_reason":"budget_unavailable"`)
	for _, want := range []string{`"cause":"read_error"`, "(cause: read_error); failing open: this launch decision is not checked against the budget"} {
		if !strings.Contains(rec, want) {
			t.Errorf("the WARN lacks %s: %s", want, rec)
		}
	}
	if strings.Contains(rec, "dollar") {
		t.Errorf("the WARN covers both units now: %s", rec)
	}
}
