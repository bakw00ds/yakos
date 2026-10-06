package main

// Scratch evidence from sec-330's final review of #330, adopted as a test (spec item 7): a
// tiny positive limit_usd made the percentage overflow, and `budget check --json` lost its
// line. The setup is sec-330's, unchanged: $0.50 of api spend by the supervisor and a policy
// limit_usd of 5e-324. The original only logged what came out; this asserts it, runs in
// process through budgetCheck (the function the CLI's main calls, reading stdout alone as the
// bash hook does) as spec item 8 asks, and keeps one real-binary case for the argv and
// exit-code contract.
//
// 5e-324 is below the one-cent floor, so the limit is ignored with a warning and the
// supervisor keeps its built-in $100: the line is one object, the state is ok, the exit is 0.

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
)

func tinyLimitState(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	line := fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":"supervisor","runtime":"claude","billing":"api","usage":{"input_tokens":10,"output_tokens":0,"total_cost_usd":0.5}}`+"\n", time.Now().UTC().Format(time.RFC3339))
	if err := appendFile(state+"/dispatch-log.ndjson", line); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(budget.PolicyPath(state), []byte("agents:\n  supervisor:\n    limit_usd: 5e-324\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestSec330h_TinyDollarLimitJSON(t *testing.T) {
	state := tinyLimitState(t)
	var out, errb bytes.Buffer
	code := budgetCheck(&out, &errb, "supervisor", budget.Options{StateDir: state}, true)
	obj, found := firstJSONObject(out.String())
	if !found || !usableStatusLine(obj) {
		t.Fatalf("no usable JSON line on stdout (the bash gate would fail open): exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
	if code != 0 || obj["state"] != "ok" || obj["limit_usd"] != 100.0 {
		t.Errorf("the tiny limit is ignored and the built-in $100 stays: exit %d, %v", code, obj)
	}
	if !strings.Contains(errb.String(), "limit_usd for supervisor ignored") {
		t.Errorf("the ignored value must be reported: %q", errb.String())
	}
}

func TestSec330h_TinyDollarLimitJSONThroughTheBinary(t *testing.T) {
	code, out := runYakos(t, tinyLimitState(t), nil, "budget", "check", "supervisor", "--json")
	obj, found := firstJSONObject(out)
	if !found || !usableStatusLine(obj) {
		t.Fatalf("no usable JSON line (the bash gate would fail open): exit %d: %q", code, out)
	}
	if code != 0 || obj["state"] != "ok" {
		t.Errorf("exit %d, %v", code, obj)
	}
}
