package budget

// status_pct_finite_test.go: the share of a limit used that a status reports stays finite
// (K-136, sec-330's final round of #330). `budget check --json` marshals the status, and
// json.Marshal refuses +Inf: the line comes out empty and the bash supervisor hook, which
// reads that line and nothing else, fails open. A limit that is tiny but not zero divides to
// +Inf (a single run's cost is bounded when it is read, so spend alone cannot).

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

func TestFinitePct(t *testing.T) {
	for _, c := range []struct{ in, want float64 }{
		{0, 0}, {42.5, 42.5}, {100, 100}, {maxPct, maxPct}, {1e300, maxPct}, {math.Inf(1), maxPct}, {math.NaN(), 0},
	} {
		if got := finitePct(c.in); got != c.want {
			t.Errorf("finitePct(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// A positive limit so small that spend divided by it overflows (a trusted-policy 5e-324 is a
// number in the dollar range): the status marshals, whatever the policy does with the value. If
// the limit is applied, the share is the bound and the state is the hard stop.
func TestEvaluatePctIsFiniteForATinyLimit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(PolicyPath(dir), []byte("agents:\n  backend:\n    limit_usd: 5e-324\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	appendLog(t, dir, finished("backend", octMid, 0.5))
	st := mustEval(t, "backend", Options{StateDir: dir, Now: clock(octMid)})
	if math.IsInf(st.Pct, 0) || math.IsNaN(st.Pct) {
		t.Fatalf("the share used must be finite: %+v", st)
	}
	if _, err := json.Marshal(st); err != nil {
		t.Fatalf("the status must marshal (budget check --json prints it): %v", err)
	}
	if st.LimitUSD > 0 && (st.Pct != maxPct || st.State != StateHardStop) {
		t.Errorf("an applied tiny limit is the hard stop at the largest share: %+v", st)
	}
}

// The same for the token share: a limit of one token against more tokens than a run can
// have is a share past the bound, and still a finite number.
func TestEvaluateTokensPctStaysFinite(t *testing.T) {
	dir := t.TempDir()
	setTokenLimit(t, dir, "backend", 1, Monthly)
	appendLog(t, dir, ledgerLine("backend", octMid, "subscription", 1<<40, 0, 0, 0, 0))
	st := mustEval(t, "backend", Options{StateDir: dir, Now: clock(octMid)})
	if st.State != StateHardStop || st.TokensPct != maxPct || st.Pct != maxPct {
		t.Fatalf("2^40 tokens against a limit of 1: %+v", st)
	}
	if _, err := json.Marshal(st); err != nil {
		t.Fatalf("the status must marshal: %v", err)
	}
}
