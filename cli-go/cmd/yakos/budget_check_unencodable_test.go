package main

// budget_check_unencodable_test.go: `yakos budget check --json` prints a JSON object
// even for a status json cannot encode (K-136, sec-330's final round of #330). The bash
// supervisor hook reads that line and nothing else; an empty line is what it takes for
// "no budget", and the gate then fails open with no decision and no finding.

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/budget"
)

// A status that json cannot encode (a non-finite amount or stop) is a failed read, as a
// panic is: the line is the read_failed object and never empty, and the exit is 0 even
// though the status says hard_stop. Evaluate never returns one (every amount and stop is
// finite by construction), so this goes through reportCheck.
func TestReportCheckAnUnencodableStatusPrintsTheReadFailedObject(t *testing.T) {
	for name, st := range map[string]budget.Status{
		"an infinite dollar stop":  {Agent: "supervisor", State: budget.StateHardStop, LimitUSD: 100, StopUSD: math.Inf(1), SpentUSD: 150},
		"a NaN dollar amount":      {Agent: "supervisor", State: budget.StateHardStop, LimitUSD: math.NaN(), StopUSD: 200},
		"an infinite spend":        {Agent: "supervisor", State: budget.StateHardStop, LimitUSD: 100, StopUSD: 200, SpentUSD: math.Inf(1)},
		"a negative infinite pct":  {Agent: "supervisor", State: budget.StateHardStop, LimitUSD: 100, StopUSD: 200, Pct: math.Inf(-1)},
		"an infinite tokens share": {Agent: "supervisor", State: budget.StateHardStop, LimitTokens: 1, StopTokens: 2, TokensPct: math.Inf(1)},
	} {
		t.Run(name, func(t *testing.T) {
			if !st.Refused() {
				t.Fatal("the case must be a refusing status, or the exit code below proves nothing")
			}
			if _, err := json.Marshal(st); err == nil {
				t.Fatal("the case must be a status json cannot encode")
			}
			var out, errb bytes.Buffer
			if code := reportCheck(&out, &errb, "supervisor", st, nil, true); code != 0 {
				t.Fatalf("an unencodable status fails open like a panic does: exit %d, not the hard stop's", code)
			}
			if got := strings.TrimSpace(out.String()); got != `{"agent":"supervisor","read_failed":true}` {
				t.Fatalf("stdout = %q: never an empty line, always the read_failed object", out.String())
			}
			if strings.Count(out.String(), "\n") != 1 {
				t.Errorf("exactly one line on stdout: %q", out.String())
			}
			if !strings.Contains(errb.String(), "failing open") {
				t.Errorf("stderr = %q", errb.String())
			}
		})
	}

	// A status that encodes is printed as it always was, and a refusal still exits 4.
	st := budget.Status{Agent: "supervisor", State: budget.StateHardStop, LimitUSD: 100, StopUSD: 200, SpentUSD: 150}
	var out, errb bytes.Buffer
	if code := reportCheck(&out, &errb, "supervisor", st, nil, true); code != budget.ExitHardStop {
		t.Fatalf("a refused, encodable status exits %d, got %d", budget.ExitHardStop, code)
	}
	if obj := jsonLine(t, out.String()); obj["state"] != "hard_stop" || obj["read_failed"] != nil {
		t.Errorf("the status itself is printed: %q", out.String())
	}
}
