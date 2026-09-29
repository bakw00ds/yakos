package routing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- defect 3: judge output extraction --------------------------------------

func TestParseJudgeOutput_Table(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantPass  bool
		wantErr   bool
		wantNotes string
		wantScore int
	}{
		{name: "bare object", raw: `{"pass":true,"notes":"ok"}`, wantPass: true, wantNotes: "ok"},
		{name: "bare object with whitespace", raw: "\n  {\"pass\":false}\n", wantPass: false},
		{name: "prose before and after", raw: `Here is my verdict: {"pass":true,"notes":"good"} Thanks!`, wantPass: true, wantNotes: "good"},
		{name: "fenced json block", raw: "Verdict:\n```json\n{\"pass\":true,\"criteria_scores\":[{\"n\":1},{\"n\":2}]}\n```\nDone.", wantPass: true, wantScore: 2},
		{name: "fenced bare block", raw: "```\n{\"pass\":false,\"notes\":\"no\"}\n```", wantPass: false, wantNotes: "no"},
		{name: "braces inside strings", raw: `{"pass":true,"notes":"has } and { inside"}`, wantPass: true, wantNotes: "has } and { inside"},
		{name: "escaped quote in string", raw: `x {"pass":true,"notes":"say \"}\" ok"} y`, wantPass: true, wantNotes: `say "}" ok`},
		{name: "stray brace in prose before object", raw: `I considered {the case} carefully. {"pass":true}`, wantPass: true},
		{name: "unbalanced opener then valid object", raw: `Note { unclosed. {"pass":false,"notes":"n"}`, wantPass: false, wantNotes: "n"},
		{name: "first object lacks pass, second has it", raw: `{"thinking":"x"} then {"pass":true}`, wantPass: true},
		{name: "nested object", raw: `{"pass":true,"criteria_scores":[{"a":{"b":1}}]}`, wantPass: true, wantScore: 1},
		{name: "empty", raw: ``, wantErr: true},
		{name: "prose only", raw: `The agent did fine. Pass.`, wantErr: true},
		{name: "pass is a string", raw: `{"pass":"true"}`, wantErr: true},
		{name: "pass missing", raw: `{"notes":"n"}`, wantErr: true},
		{name: "criteria_scores wrong type", raw: `{"pass":true,"criteria_scores":"x"}`, wantErr: true},
		{name: "truncated object", raw: `{"pass":true,"notes":"cut off`, wantErr: true},
		{name: "array not object", raw: `[{"x":1}]`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseJudgeOutput(tc.raw)
			if tc.wantErr {
				if !got.Unscored() {
					t.Fatalf("expected unscored, got %+v", got)
				}
				if got.Raw != tc.raw {
					t.Errorf("Raw not preserved: got %q want %q", got.Raw, tc.raw)
				}
				if got.Pass {
					t.Errorf("unscored result must not report Pass")
				}
				return
			}
			if got.Unscored() {
				t.Fatalf("unexpected parse failure: %+v", got)
			}
			if got.Pass != tc.wantPass {
				t.Errorf("Pass = %v want %v", got.Pass, tc.wantPass)
			}
			if got.Notes != tc.wantNotes {
				t.Errorf("Notes = %q want %q", got.Notes, tc.wantNotes)
			}
			if len(got.CriteriaScores) != tc.wantScore {
				t.Errorf("criteria_scores len = %d want %d", len(got.CriteriaScores), tc.wantScore)
			}
		})
	}
}

func TestRealJudge_ProseWrappedJSON(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "cli", "lib")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env bash\necho 'Sure thing.'\necho '```json'\necho '{\"pass\":true,\"notes\":\"wrapped\"}'\necho '```'\n"
	if err := os.WriteFile(filepath.Join(dir, "dispatch.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	jr, err := realJudge(root, "code-reviewer", "{}", "")
	if err != nil {
		t.Fatal(err)
	}
	if jr.Unscored() || !jr.Pass || jr.Notes != "wrapped" {
		t.Errorf("prose-wrapped judge JSON not extracted: %+v", jr)
	}
}

// An unparseable judge verdict must be recorded with its raw output and must
// NOT be counted as a failure for the tier.
func TestEval_UnparseableJudge_NotScoredAsFail(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 5)
	cfg.DispatchFn = mockDispatch(0.001, "ok")
	n := 0
	cfg.JudgeFn = func(judgeID, inputJSON, project string) (JudgeResult, error) {
		n++
		if n%2 == 0 {
			return parseJudgeOutput("I could not decide, sorry"), nil
		}
		return JudgeResult{Pass: true}, nil
	}
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	fin := lastRecord(t, cfg.EvalLog, "eval_run_finished")
	rates := fin["tier_pass_rates"].(map[string]interface{})
	// Every scored case passed, so no tier may show a rate below 1 where it
	// was scored at all.
	for tier, v := range rates {
		if r := v.(float64); r != 0 && r != 1 {
			t.Errorf("tier %s pass rate %v: unscored cases leaked into the denominator", tier, r)
		}
	}
	var sawRaw bool
	for _, rec := range readRecords(t, cfg.EvalLog, "eval_case") {
		if rec["scored"] == false {
			sawRaw = true
			if rec["judge_raw"] != "I could not decide, sorry" {
				t.Errorf("raw judge output not recorded: %v", rec["judge_raw"])
			}
			if rec["pass"] != nil {
				t.Errorf("unscored case must have null pass, got %v", rec["pass"])
			}
		}
	}
	if !sawRaw {
		t.Error("no unscored eval_case record found")
	}
}

// ---- helpers ---------------------------------------------------------------

func readRecords(t *testing.T, path, typ string) []map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []map[string]interface{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]interface{}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if typ == "" || rec["type"] == typ {
			out = append(out, rec)
		}
	}
	return out
}

func lastRecord(t *testing.T, path, typ string) map[string]interface{} {
	t.Helper()
	recs := readRecords(t, path, typ)
	if len(recs) == 0 {
		t.Fatalf("no %q record in %s", typ, path)
	}
	return recs[len(recs)-1]
}
