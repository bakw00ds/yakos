package main

// budget_check_readfailed_tokens_test.go: what `yakos budget check --json` prints when the
// spend cannot be read under a token-only budget (K-136, rev-327's gate review of #330).
// The bash supervisor hook reads that one line and nothing else: it must carry read_failed
// whatever the limits are, a dollar limit or not.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
)

// jsonLine returns the first line of out that is a JSON object, decoded.
func jsonLine(t *testing.T, out string) map[string]any {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "{") {
			var obj map[string]any
			if err := json.Unmarshal([]byte(l), &obj); err != nil {
				t.Fatalf("a JSON line that does not parse: %q: %v", l, err)
			}
			return obj
		}
	}
	t.Fatalf("no JSON line in %q", out)
	return nil
}

// tokenOnlyUnreadableState is a state directory with a token-only budget for agent and a
// directory where the spend log belongs, so the spend cannot be read.
func tokenOnlyUnreadableState(t *testing.T, agent string) string {
	t.Helper()
	state := t.TempDir()
	if err := budget.SetLimit(state, agent, 0, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	if agent != "supervisor" {
		// the supervisor keeps its built-in token limit when its dollar limit is turned off
		if err := budget.SetTokenLimit(state, agent, 1000, budget.Monthly); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(state, "dispatch-log.ndjson"), 0o700); err != nil {
		t.Fatal(err)
	}
	return state
}

// In process: an unreadable spend under a token-only budget fails open (exit 0) and says
// so in the JSON, as it does under a dollar budget.
func TestBudgetCheckSaysItCouldNotReadTheSpendOfATokenOnlyBudget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory where the log belongs is not a read error on windows")
	}
	for _, agent := range []string{"backend", "supervisor"} {
		state := tokenOnlyUnreadableState(t, agent)
		o := budget.Options{StateDir: state, Now: func() time.Time { return time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC) }}
		var out, errb bytes.Buffer
		if code := budgetCheck(&out, &errb, agent, o, true); code != 0 {
			t.Fatalf("%s: an unreadable spend log fails open: exit %d", agent, code)
		}
		if !strings.Contains(out.String(), `"read_failed":true`) || !strings.Contains(errb.String(), "failing open") {
			t.Fatalf("%s: stdout=%q stderr=%q", agent, out.String(), errb.String())
		}
		obj := jsonLine(t, out.String())
		if lim, ok := obj["limit_usd"].(float64); !ok || lim != 0 {
			t.Errorf("%s: the budget under test is token-only (limit_usd 0): %v", agent, obj["limit_usd"])
		}
	}
}

// Through the CLI's own main, the line a hook reads: the same, with a parseable object
// and the numbers the bash jq needs.
func TestBudgetCheckCLIJSONSaysReadFailedForATokenOnlyBudget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory where the log belongs is not a read error on windows")
	}
	for _, agent := range []string{"backend", "supervisor"} {
		state := tokenOnlyUnreadableState(t, agent)
		code, out := runYakos(t, state, nil, "budget", "check", agent, "--json")
		if code != 0 {
			t.Fatalf("%s: exit %d, an unreadable spend log fails open: %q", agent, code, out)
		}
		obj := jsonLine(t, out)
		if obj["read_failed"] != true {
			t.Errorf("%s: read_failed is not true in %q", agent, out)
		}
		if _, ok := obj["limit_usd"].(float64); !ok {
			t.Errorf("%s: limit_usd is not a number in %q", agent, out)
		}
		if s, ok := obj["state"].(string); !ok || s != "ok" {
			t.Errorf("%s: state = %v, want the string ok (a failed read says nothing was spent) in %q", agent, obj["state"], out)
		}
	}
}
