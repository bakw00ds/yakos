package budget

// read_failed_tokens_test.go: read_failed does not depend on a dollar limit (K-136,
// rev-327's gate review of #330). The CLI's own word that it could not read the spend
// is what the bash supervisor hook trusts, ahead of every limit. A token-only budget
// (limit_usd 0 and a token limit) is what a subscription operator has, so a read_failed
// that was only set for a dollar limit would leave "ok, nothing spent" there and the
// gate would launch with no warning.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEvaluateReadFailedForATokenOnlyBudget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory where the log belongs is not a read error on windows")
	}
	for name, setup := range map[string]func(t *testing.T, dir string){
		// an operator's own token limit with the dollar limit explicitly off
		"an operator entry with limit_usd 0": func(t *testing.T, dir string) {
			setLimit(t, dir, "backend", 0, Monthly)
			setTokenLimit(t, dir, "backend", 1000, Monthly)
		},
		// a subscription operator who turned the supervisor's dollar limit off keeps its built-in token limit
		"the supervisor with its dollars off": func(t *testing.T, dir string) {
			setLimit(t, dir, "supervisor", 0, Monthly)
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			setup(t, dir)
			agent := "backend"
			if strings.Contains(name, "supervisor") {
				agent = "supervisor"
			}
			if err := os.Mkdir(filepath.Join(dir, logFileName), 0o700); err != nil {
				t.Fatal(err)
			}
			st, err := Evaluate(agent, Options{StateDir: dir, Now: clock(octMid)})
			if st.LimitUSD != 0 || st.LimitTokens <= 0 {
				t.Fatalf("the setup is not a token-only budget: %+v", st)
			}
			if err == nil || !st.ReadFailed || st.State != StateOK || st.SpentTokens != 0 {
				t.Fatalf("an unreadable log under a token-only budget must say read_failed (it fails open, so the state stays ok): err=%v status=%+v", err, st)
			}
			if b, _ := json.Marshal(st); !strings.Contains(string(b), `"read_failed":true`) {
				t.Errorf("read_failed is not in the JSON: %s", b)
			}
		})
	}

	// A read that works carries no read_failed, for the same token-only budget.
	good := t.TempDir()
	setLimit(t, good, "backend", 0, Monthly)
	setTokenLimit(t, good, "backend", 1000, Monthly)
	st := mustEval(t, "backend", Options{StateDir: good, Now: clock(octMid)})
	if b, _ := json.Marshal(st); st.ReadFailed || strings.Contains(string(b), "read_failed") {
		t.Errorf("a good read of a token-only budget carries read_failed: %s", b)
	}
}
