package routing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
		{name: "fenced block beats earlier decoy object", raw: "Format is {\"pass\":false}.\n```json\n{\"pass\":true,\"notes\":\"real\"}\n```", wantPass: true, wantNotes: "real"},
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

// ---- defect 4: default judge never equals the subject ------------------------

func TestResolveJudge_Table(t *testing.T) {
	cases := []struct {
		name, override, domain, subject string
		wantJudge                       string
		wantNote                        bool
	}{
		{"cross-cutting default is architect", "", "cross-cutting", "backend", "architect", false},
		{"architect subject falls back to code-reviewer", "", "cross-cutting", "architect", "code-reviewer", true},
		{"design subject architect falls back", "", "design", "architect", "code-reviewer", true},
		{"code-reviewer subject in backend domain falls back to architect", "", "backend", "code-reviewer", "architect", true},
		{"override wins even if equal (caller refuses)", "architect", "cross-cutting", "architect", "architect", false},
		{"override differing", "opus-judge", "backend", "backend", "opus-judge", false},
		{"unknown domain default", "", "weird", "backend", "code-reviewer", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j, note := resolveJudge(tc.override, tc.domain, tc.subject)
			if j != tc.wantJudge {
				t.Errorf("judge = %q want %q", j, tc.wantJudge)
			}
			if (note != "") != tc.wantNote {
				t.Errorf("note = %q, wantNote=%v", note, tc.wantNote)
			}
			if tc.override == "" && j == tc.subject {
				t.Errorf("default judge equals subject %q", tc.subject)
			}
		})
	}
}

func TestEval_ArchitectSubject_FallsBackAndLogs(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "architect"
	setupEvalAgent(t, cfg, "architect", "opus", "cross-cutting", 5)
	cfg.DispatchFn = mockDispatch(0.001, "ok")
	var gotJudge string
	cfg.JudgeFn = func(judgeID, inputJSON, project string) (JudgeResult, error) {
		gotJudge = judgeID
		return JudgeResult{Pass: true}, nil
	}
	if _, err := Run(cfg); err != nil {
		t.Fatalf("architect as subject must not need --judge: %v", err)
	}
	if gotJudge != "code-reviewer" {
		t.Errorf("judge used = %q want code-reviewer", gotJudge)
	}
	if !strings.Contains(cfgOut(cfg), "fell back to \"code-reviewer\"") {
		t.Errorf("fallback not shown in output: %q", cfgOut(cfg))
	}
	fb := readRecords(t, cfg.EvalLog, "judge_fallback")
	if len(fb) != 1 || fb[0]["judge"] != "code-reviewer" {
		t.Errorf("judge_fallback record missing/wrong: %v", fb)
	}
	started := lastRecord(t, cfg.EvalLog, "eval_run_started")
	if started["judge"] != nil && started["judge"] != "code-reviewer" {
		t.Errorf("eval_run_started judge = %v", started["judge"])
	}
}

func TestEval_ExplicitSelfJudgeStillRefused(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "architect"
	cfg.Judge = "architect"
	setupEvalAgent(t, cfg, "architect", "opus", "cross-cutting", 5)
	if _, err := Run(cfg); err == nil || !strings.Contains(err.Error(), "same agent") {
		t.Fatalf("explicit self judge must be refused, got %v", err)
	}
}

// ---- defect 2: cost telemetry and fail-closed cap ---------------------------

func finishedLine(agent, runID string, dur float64, usage string) string {
	u := ""
	if usage != "" {
		u = `,"usage":` + usage
	}
	return `{"type":"dispatch_finished","agent":"` + agent + `","eval_run_id":"` + runID +
		`","duration_s":` + strings.TrimRight(strings.TrimRight(fmtFloat(dur), "0"), ".") +
		`,"est_input_tokens":11,"est_output_tokens":7` + u + `}`
}

func fmtFloat(f float64) string { return strings.TrimSpace(strings.Replace(jsonNum(f), "e+00", "", 1)) }

func jsonNum(f float64) string { b, _ := json.Marshal(f); return string(b) }

func TestReadDispatchTelemetry_Table(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "dispatch-log.ndjson")
	old := finishedLine("backend", "run-A", 9, `{"total_cost_usd":9.99}`) + "\n"
	body := old +
		finishedLine("backend", "run-B", 3, `{"total_cost_usd":0.5}`) + "\n" + // other run
		finishedLine("other", "run-A", 3, `{"total_cost_usd":0.6}`) + "\n" + // other agent
		`{"type":"dispatch_started","agent":"backend","eval_run_id":"run-A"}` + "\n" +
		"not json\n" +
		finishedLine("backend", "run-A", 2.5, `{"input_tokens":100,"output_tokens":40,"total_cost_usd":0.0123}`) + "\n"
	if err := os.WriteFile(logPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	off := int64(len(old))

	t.Run("matches run+agent after offset", func(t *testing.T) {
		tel, ok := readDispatchTelemetry(logPath, off, "run-A", "backend")
		if !ok || tel.Cost == nil || *tel.Cost != 0.0123 || tel.DurationS != 2.5 || tel.InputTokens != 100 || tel.OutputTokens != 40 {
			t.Fatalf("got %+v ok=%v", tel, ok)
		}
	})
	t.Run("offset excludes earlier record", func(t *testing.T) {
		if _, ok := readDispatchTelemetry(logPath, int64(len(body)), "run-A", "backend"); ok {
			t.Fatal("record before offset must not match")
		}
	})
	t.Run("rotated log read from start", func(t *testing.T) {
		tel, ok := readDispatchTelemetry(logPath, int64(len(body))+1000, "run-B", "backend")
		if !ok || tel.Cost == nil || *tel.Cost != 0.5 {
			t.Fatalf("got %+v ok=%v", tel, ok)
		}
	})
	t.Run("other agent record last does not win", func(t *testing.T) {
		p := filepath.Join(dir, "otheragent.ndjson")
		_ = os.WriteFile(p, []byte(finishedLine("backend", "r", 1, `{"total_cost_usd":0.1}`)+"\n"+finishedLine("other", "r", 1, `{"total_cost_usd":7}`)+"\n"), 0o600)
		tel, ok := readDispatchTelemetry(p, 0, "r", "backend")
		if !ok || tel.Cost == nil || *tel.Cost != 0.1 {
			t.Fatalf("got %+v ok=%v", tel, ok)
		}
	})
	t.Run("no usage means unknown cost, estimated tokens", func(t *testing.T) {
		p := filepath.Join(dir, "nousage.ndjson")
		_ = os.WriteFile(p, []byte(finishedLine("a", "r", 1, "")+"\n"), 0o600)
		tel, ok := readDispatchTelemetry(p, 0, "r", "a")
		if !ok || tel.Cost != nil || tel.InputTokens != 11 || tel.OutputTokens != 7 {
			t.Fatalf("got %+v ok=%v", tel, ok)
		}
	})
	t.Run("usage without total_cost_usd is unknown", func(t *testing.T) {
		p := filepath.Join(dir, "nocost.ndjson")
		_ = os.WriteFile(p, []byte(finishedLine("a", "r", 1, `{"input_tokens":5}`)+"\n"), 0o600)
		tel, ok := readDispatchTelemetry(p, 0, "r", "a")
		if !ok || tel.Cost != nil {
			t.Fatalf("got %+v ok=%v", tel, ok)
		}
	})
	t.Run("explicit zero cost is known", func(t *testing.T) {
		p := filepath.Join(dir, "zero.ndjson")
		_ = os.WriteFile(p, []byte(finishedLine("a", "r", 1, `{"total_cost_usd":0}`)+"\n"), 0o600)
		tel, ok := readDispatchTelemetry(p, 0, "r", "a")
		if !ok || tel.Cost == nil || *tel.Cost != 0 {
			t.Fatalf("got %+v ok=%v", tel, ok)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		if _, ok := readDispatchTelemetry(filepath.Join(dir, "nope"), 0, "r", "a"); ok {
			t.Fatal("expected not found")
		}
	})
	// K-136: a subscription run written by the Go dispatcher has usage cost 0 and
	// keeps the harness's figure as api_equivalent_usd. The eval prices a tier by
	// what the run costs at API rates, so it reads the API-equivalent then, and
	// an api-billed run keeps its own spend.
	t.Run("subscription run is priced at its api equivalent", func(t *testing.T) {
		p := filepath.Join(dir, "subscription.ndjson")
		line := strings.Replace(finishedLine("a", "r", 1, `{"input_tokens":5,"output_tokens":2,"total_cost_usd":0}`),
			`"est_input_tokens"`, `"billing":"subscription","api_equivalent_usd":0.0123,"est_input_tokens"`, 1)
		_ = os.WriteFile(p, []byte(line+"\n"), 0o600)
		tel, ok := readDispatchTelemetry(p, 0, "r", "a")
		if !ok || tel.Cost == nil || *tel.Cost != 0.0123 {
			t.Fatalf("got %+v ok=%v, want cost 0.0123", tel, ok)
		}
	})
	t.Run("api run keeps its spend even with an api equivalent present", func(t *testing.T) {
		p := filepath.Join(dir, "apirun.ndjson")
		line := strings.Replace(finishedLine("a", "r", 1, `{"total_cost_usd":0.5}`),
			`"est_input_tokens"`, `"billing":"api","api_equivalent_usd":9,"est_input_tokens"`, 1)
		_ = os.WriteFile(p, []byte(line+"\n"), 0o600)
		tel, ok := readDispatchTelemetry(p, 0, "r", "a")
		if !ok || tel.Cost == nil || *tel.Cost != 0.5 {
			t.Fatalf("got %+v ok=%v, want the spend 0.5", tel, ok)
		}
	})
}

// writeTelemetryDispatchSh writes a stub dispatch.sh that appends a
// dispatch_finished record to $HOME/.yakos-state/dispatch-log.ndjson, as the
// real dispatch.sh does. usageJSON == "" omits the usage object.
func writeTelemetryDispatchSh(t *testing.T, root, usageJSON string) {
	t.Helper()
	dir := filepath.Join(root, "cli", "lib")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	usage := ""
	if usageJSON != "" {
		usage = `,"usage":` + usageJSON
	}
	script := `#!/usr/bin/env bash
agent="$1"; shift; shift
run=""
while [ "$#" -gt 0 ]; do case "$1" in --eval-run-id) run="$2"; shift;; esac; shift; done
mkdir -p "$HOME/.yakos-state"
printf '{"type":"dispatch_finished","agent":"%s","eval_run_id":"%s","duration_s":4.5,"est_input_tokens":1,"est_output_tokens":2%s}\n' "$agent" "$run" '` + usage + `' >> "$HOME/.yakos-state/dispatch-log.ndjson"
echo subject-output
`
	if err := os.WriteFile(filepath.Join(dir, "dispatch.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRealDispatch_FillsTelemetryFromDispatchLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	writeTelemetryDispatchSh(t, root, `{"input_tokens":123,"output_tokens":45,"total_cost_usd":0.25}`)
	dr, err := realDispatch(root, "backend", "task", "sonnet", "run-9", "")
	if err != nil {
		t.Fatal(err)
	}
	if dr.CostUnknown || dr.Cost != 0.25 || dr.DurationS != 4.5 || dr.InputTokens != 123 || dr.OutputTokens != 45 {
		t.Errorf("telemetry not filled: %+v", dr)
	}
	if !strings.Contains(dr.Stdout, "subject-output") {
		t.Errorf("stdout lost: %q", dr.Stdout)
	}
}

// A record for the same run and agent that predates this call (a retry, or a
// reused run id) must not be attributed to a dispatch that logged nothing.
func TestRealDispatch_StaleLogRecordNotReused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	logDir := filepath.Join(home, ".yakos-state")
	_ = os.MkdirAll(logDir, 0o755)
	_ = os.WriteFile(filepath.Join(logDir, "dispatch-log.ndjson"),
		[]byte(finishedLine("backend", "run-9", 2, `{"total_cost_usd":9.99}`)+"\n"), 0o600)
	root := t.TempDir()
	writeFakeDispatchSh(t, root, "x") // logs nothing
	dr, _ := realDispatch(root, "backend", "task", "sonnet", "run-9", "")
	if !dr.CostUnknown || dr.Cost != 0 {
		t.Errorf("stale record leaked into result: %+v", dr)
	}
}

func TestRealDispatch_NoCostTelemetryIsUnknown(t *testing.T) {
	t.Run("record without usage", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		root := t.TempDir()
		writeTelemetryDispatchSh(t, root, "")
		dr, _ := realDispatch(root, "backend", "task", "sonnet", "run-9", "")
		if !dr.CostUnknown {
			t.Errorf("want CostUnknown, got %+v", dr)
		}
	})
	t.Run("no record at all", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		root := t.TempDir()
		writeFakeDispatchSh(t, root, "x")
		dr, _ := realDispatch(root, "backend", "task", "sonnet", "run-9", "")
		if !dr.CostUnknown || dr.DurationS <= 0 {
			t.Errorf("want CostUnknown with wall-clock duration, got %+v", dr)
		}
	})
}

// The cap must trip from costs recovered out of the real dispatch path.
func TestEval_CapTripsFromRealDispatchTelemetry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	cfg.MaxCostUSD = 5
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 5)
	writeTelemetryDispatchSh(t, cfg.YakosRoot, `{"total_cost_usd":3.0}`)
	cfg.JudgeFn = mockJudge(true)
	if _, err := Run(cfg); err == nil {
		t.Fatal("budget-hit run must return an error")
	}
	if len(readRecords(t, cfg.EvalLog, "budget_exceeded")) != 1 {
		t.Error("cap did not trip on real telemetry")
	}
	cases := readRecords(t, cfg.EvalLog, "eval_case")
	if len(cases) != 2 {
		t.Errorf("expected run to stop after 2 dispatches ($3 each, cap $5), got %d", len(cases))
	}
	if c := cases[0]["total_cost_usd"].(float64); c != 3.0 {
		t.Errorf("cost not recorded: %v", c)
	}
}

func TestEval_MissingCostTelemetry_FailsClosed(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 6)
	calls := 0
	cfg.DispatchFn = func(agentID, task, tier, runID, project string) (DispatchResult, error) {
		calls++
		if calls >= 3 {
			return DispatchResult{Stdout: "x", CostUnknown: true}, nil
		}
		return DispatchResult{Stdout: "x", Cost: 0.001, DurationS: 1}, nil
	}
	judged := 0
	cfg.JudgeFn = func(string, string, string) (JudgeResult, error) { judged++; return JudgeResult{Pass: true}, nil }

	res, err := Run(cfg)
	if err == nil || !strings.Contains(err.Error(), "no cost telemetry") {
		t.Fatalf("want fail-closed error, got %v", err)
	}
	if calls != 3 {
		t.Errorf("run must stop at the first unknown-cost dispatch; dispatches=%d", calls)
	}
	if judged != 2 {
		t.Errorf("the unknown-cost dispatch must not be judged/scored; judged=%d", judged)
	}
	if res.CandidateEmitted || res.EvalRunID == "" {
		t.Errorf("partial result wrong: %+v", res)
	}
	if len(readRecords(t, cfg.EvalLog, "budget_unverifiable")) != 1 {
		t.Error("budget_unverifiable not logged")
	}
	fin := lastRecord(t, cfg.EvalLog, "eval_run_finished")
	if fin["partial"] != true || fin["candidate_emitted"] != false {
		t.Errorf("finished record not marked partial: %v", fin)
	}
	if !strings.Contains(cfgOut(cfg), "partial results") {
		t.Errorf("partial results not reported: %q", cfgOut(cfg))
	}
	if _, err := os.Stat(cfg.CandidatesFile); err == nil {
		t.Error("candidate file must not be written for a partial run")
	}
}

// ---- defect 1: --tiers and --cases -------------------------------------------

func TestResolveTiers_Table(t *testing.T) {
	cases := []struct {
		name    string
		tiers   []string
		fable   bool
		want    string
		wantErr bool
	}{
		{"default excludes fable", nil, false, "haiku,sonnet,opus", false},
		{"default include fable", nil, true, "haiku,sonnet,opus,fable", false},
		{"explicit subset", []string{"haiku", "sonnet"}, false, "haiku,sonnet", false},
		{"explicit order normalised", []string{"sonnet", "haiku"}, false, "haiku,sonnet", false},
		{"explicit plus include-fable", []string{"haiku"}, true, "haiku,fable", false},
		{"duplicates collapse", []string{"haiku", "haiku"}, false, "haiku", false},
		{"case and space tolerated", []string{" Haiku "}, false, "haiku", false},
		{"unknown tier", []string{"haiku", "gpt"}, false, "", true},
		{"empty entry", []string{"haiku", ""}, false, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveTiers(tc.tiers, tc.fable)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && strings.Join(got, ",") != tc.want {
				t.Errorf("got %v want %s", got, tc.want)
			}
		})
	}
}

func TestEval_OnlyRequestedTiersDispatched(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	cfg.Tiers = []string{"haiku", "sonnet"}
	setupEvalAgent(t, cfg, "backend", "sonnet", "backend", 5)
	seen := map[string]int{}
	cfg.DispatchFn = func(agentID, task, tier, runID, project string) (DispatchResult, error) {
		seen[tier]++
		return DispatchResult{Stdout: "x", Cost: 0.001, DurationS: 1}, nil
	}
	cfg.JudgeFn = mockJudge(true)
	if _, err := Run(cfg); err != nil {
		t.Fatal(err)
	}
	if seen["opus"] != 0 || seen["fable"] != 0 || seen["haiku"] != 5 || seen["sonnet"] != 5 {
		t.Errorf("dispatch counts = %v; want only haiku,sonnet x5", seen)
	}
	out := cfgOut(cfg)
	if strings.Contains(out, "opus") || strings.Contains(out, "fable") {
		t.Errorf("summary mentions excluded tiers: %q", out)
	}
}

func TestEval_DefaultTiersExcludeFable_IncludeFableOptIn(t *testing.T) {
	for _, inc := range []bool{false, true} {
		cfg := newCfg(t)
		cfg.Subcommand = "eval"
		cfg.AgentID = "backend"
		cfg.Judge = "code-reviewer"
		cfg.IncludeFable = inc
		setupEvalAgent(t, cfg, "backend", "opus", "backend", 5)
		fable := 0
		cfg.DispatchFn = func(agentID, task, tier, runID, project string) (DispatchResult, error) {
			if tier == "fable" {
				fable++
			}
			return DispatchResult{Stdout: "x", Cost: 0.001, DurationS: 1}, nil
		}
		cfg.JudgeFn = mockJudge(true)
		if _, err := Run(cfg); err != nil {
			t.Fatal(err)
		}
		if (fable > 0) != inc {
			t.Errorf("includeFable=%v but fable dispatches=%d", inc, fable)
		}
	}
}

func TestEval_UnknownTierRefusedBeforeSpend(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	cfg.Tiers = []string{"haiku", "turbo"}
	setupEvalAgent(t, cfg, "backend", "sonnet", "backend", 5)
	called := false
	cfg.DispatchFn = func(agentID, task, tier, runID, project string) (DispatchResult, error) {
		called = true
		return DispatchResult{}, nil
	}
	if _, err := Run(cfg); err == nil || !strings.Contains(err.Error(), "unknown tier") {
		t.Fatalf("want unknown tier error, got %v", err)
	}
	if called {
		t.Error("dispatch must not run for an invalid tier list")
	}
}

// A baseline tier that was not run must never let a candidate through
// (curRate would otherwise read as 0).
func TestEval_BaselineTierNotRun_NoCandidate(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	cfg.Tiers = []string{"haiku"} // current is opus, not run
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 14)
	cfg.DispatchFn = mockDispatch(0.001, "x")
	cfg.JudgeFn = mockJudge(false) // haiku fails everything
	res, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.CandidateEmitted {
		t.Fatal("candidate emitted with no baseline tier run")
	}
	ref := readRecords(t, cfg.EvalLog, "candidate_refused")
	found := false
	for _, r := range ref {
		if r["reason"] == "current_tier_not_run" {
			found = true
		}
	}
	if !found {
		t.Errorf("current_tier_not_run refusal not logged: %v", ref)
	}
}

func TestEval_FableCurrentModel_DefaultTiersRefuse(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	setupEvalAgent(t, cfg, "backend", "fable", "backend", 14)
	cfg.DispatchFn = mockDispatch(0.001, "x")
	cfg.JudgeFn = mockJudge(true)
	res, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.CandidateEmitted {
		t.Fatal("fable-current agent must not get a candidate without running fable")
	}
	cfg2 := cfg
	cfg2.EvalLog = filepath.Join(t.TempDir(), "log.ndjson")
	cfg2.CandidatesFile = filepath.Join(t.TempDir(), "c.ndjson")
	cfg2.Writer = &bytes.Buffer{}
	cfg2.IncludeFable = true
	// Baseline present but weak: fable fails every case, cheaper tiers pass.
	lastTier := ""
	cfg2.DispatchFn = func(agentID, task, tier, runID, project string) (DispatchResult, error) {
		lastTier = tier
		return DispatchResult{Stdout: "x", Cost: 0.001, DurationS: 1}, nil
	}
	cfg2.JudgeFn = func(string, string, string) (JudgeResult, error) {
		return JudgeResult{Pass: lastTier != "fable"}, nil
	}
	res2, err := Run(cfg2)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.CandidateEmitted {
		t.Errorf("with --include-fable the fable baseline exists and a cheaper passing tier qualifies: %+v", res2)
	}
}

func TestSelectCaseFiles_Table(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"01", "02", "03", "alpha"} {
		writeEvalCase(t, dir, id)
	}
	// A case whose case_id differs from its file name.
	data, _ := json.Marshal(map[string]interface{}{"case_id": "weird-id", "task": "t", "expected_outcomes": []string{"x"}, "rubric": map[string]interface{}{}})
	_ = os.WriteFile(filepath.Join(dir, "case-zzz.json"), data, 0o644)

	names := func(files []string) string {
		var b []string
		for _, f := range files {
			b = append(b, strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "case-"), ".json"))
		}
		return strings.Join(b, ",")
	}
	cases := []struct {
		name, spec, want string
		wantErr          bool
	}{
		{"empty is all", "", "01,02,03,alpha,zzz", false},
		{"glob", "case-0*.json", "01,02,03", false},
		{"single id", "02", "02", false},
		{"id list", "03,01", "01,03", false},
		{"full name", "case-alpha.json", "alpha", false},
		{"stem with prefix", "case-alpha", "alpha", false},
		{"case_id field", "weird-id", "zzz", false},
		{"glob plus id", "case-0[12].json,alpha", "01,02,alpha", false},
		{"duplicates collapse", "01,01,case-01.json", "01", false},
		{"no match errors", "01,nope", "", true},
		{"glob no match errors", "zzz-*.json", "", true},
		{"empty entry errors", "01,,02", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectCaseFiles(dir, tc.spec)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if err == nil && names(got) != tc.want {
				t.Errorf("got %s want %s", names(got), tc.want)
			}
		})
	}
}

func TestEval_CasesSubsetRunsOnlySelected(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	cfg.CasesGlob = "01,02,03,04,05"
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 8)
	tasks := map[string]bool{}
	cfg.DispatchFn = func(agentID, task, tier, runID, project string) (DispatchResult, error) {
		tasks[task] = true
		return DispatchResult{Stdout: "x", Cost: 0.001, DurationS: 1}, nil
	}
	cfg.JudgeFn = mockJudge(true)
	if _, err := Run(cfg); err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 5 || tasks["test task for 06"] {
		t.Errorf("subset not honoured: %v", tasks)
	}
	if s := lastRecord(t, cfg.EvalLog, "eval_run_started"); s["n_cases"] != float64(5) {
		t.Errorf("n_cases = %v want 5", s["n_cases"])
	}
}

func TestEval_CasesNoMatchErrorsBeforeSpend(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	cfg.CasesGlob = "99"
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 6)
	called := false
	cfg.DispatchFn = func(agentID, task, tier, runID, project string) (DispatchResult, error) {
		called = true
		return DispatchResult{}, nil
	}
	if _, err := Run(cfg); err == nil {
		t.Fatal("expected error for unmatched --cases")
	}
	if called {
		t.Error("dispatched despite unmatched --cases")
	}
}

// ---- defect 5: Wilson lower bound and gate decision in the summary ------------

// tierJudge builds Dispatch/Judge fakes where pass depends on the tier.
func tierJudge(cfg *Config, passes map[string]func(caseNo int) bool, cost map[string]float64) {
	lastTier := ""
	caseNo := map[string]int{}
	cfg.DispatchFn = func(agentID, task, tier, runID, project string) (DispatchResult, error) {
		lastTier = tier
		caseNo[tier]++
		return DispatchResult{Stdout: "x", Cost: cost[tier], DurationS: 1}, nil
	}
	cfg.JudgeFn = func(string, string, string) (JudgeResult, error) {
		return JudgeResult{Pass: passes[lastTier](caseNo[lastTier])}, nil
	}
}

func always(_ bool) func(int) bool { return func(int) bool { return true } }

func TestEval_GateSummary_CIMode_Candidate(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 12) // exactly the 12-case gate
	// opus fails 8/12 (rate .333); haiku and sonnet pass 12/12.
	tierJudge(&cfg, map[string]func(int) bool{
		"haiku": always(true), "sonnet": always(true),
		"opus": func(n int) bool { return n <= 4 },
	}, map[string]float64{"haiku": 0.001, "sonnet": 0.002, "opus": 0.01})
	res, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out := cfgOut(cfg)
	want := WilsonLower(12, 12)
	if !strings.Contains(out, "mode=ci") {
		t.Errorf("12 cases must use the CI gate: %q", out)
	}
	if !strings.Contains(out, "gate decision: CANDIDATE") {
		t.Errorf("gate decision line missing: %q", out)
	}
	if !strings.Contains(out, "ci-lower=") || !strings.Contains(out, fmtPct(want)) {
		t.Errorf("wilson lower %s not printed: %q", fmtPct(want), out)
	}
	if !res.CandidateEmitted || res.Gate.Decision != "candidate" || res.Gate.Baseline != "opus" {
		t.Errorf("result gate wrong: %+v", res.Gate)
	}
	if res.TierCILower["haiku"] != want {
		t.Errorf("Result.TierCILower[haiku] = %v want %v", res.TierCILower["haiku"], want)
	}
	fin := lastRecord(t, cfg.EvalLog, "eval_run_finished")
	g, ok := fin["gate"].(map[string]interface{})
	if !ok || g["decision"] != "candidate" || g["min_cases_for_confidence"] != float64(12) {
		t.Fatalf("gate not in eval_run_finished: %v", fin["gate"])
	}
	rows := g["tiers"].([]interface{})
	if len(rows) == 0 || rows[0].(map[string]interface{})["mode"] != "ci" {
		t.Errorf("gate rows wrong: %v", rows)
	}
}

func TestEval_GateSummary_CIMode_Refused(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 12)
	// Everything passes: haiku lower bound (~0.76) is below opus 1.0 - 0.05.
	tierJudge(&cfg, map[string]func(int) bool{
		"haiku": always(true), "sonnet": always(true), "opus": always(true),
	}, map[string]float64{"haiku": 0.001, "sonnet": 0.002, "opus": 0.01})
	res, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out := cfgOut(cfg)
	if res.CandidateEmitted || !strings.Contains(out, "gate decision: REFUSED") || !strings.Contains(out, "FAIL") {
		t.Errorf("expected REFUSED with FAIL rows: %q", out)
	}
	if res.Gate.Decision != "refused" {
		t.Errorf("Gate.Decision = %q", res.Gate.Decision)
	}
}

func TestEval_GateSummary_ModeBoundary(t *testing.T) {
	for _, tc := range []struct {
		n    int
		mode string
	}{{11, "strict_floor"}, {12, "ci"}} {
		cfg := newCfg(t)
		cfg.Subcommand = "eval"
		cfg.AgentID = "backend"
		cfg.Judge = "code-reviewer"
		setupEvalAgent(t, cfg, "backend", "opus", "backend", tc.n)
		tierJudge(&cfg, map[string]func(int) bool{
			"haiku": always(true), "sonnet": always(true), "opus": always(true),
		}, map[string]float64{"haiku": 0.001, "sonnet": 0.002, "opus": 0.01})
		res, err := Run(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Gate.Rows) == 0 || res.Gate.Rows[0].Mode != tc.mode {
			t.Errorf("n=%d: mode = %+v want %s", tc.n, res.Gate.Rows, tc.mode)
		}
		if !strings.Contains(cfgOut(cfg), "mode="+tc.mode) {
			t.Errorf("n=%d: summary lacks mode=%s: %q", tc.n, tc.mode, cfgOut(cfg))
		}
	}
}

func TestEval_GateSummary_BaselineNotRun(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	cfg.Tiers = []string{"haiku"}
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 12)
	cfg.DispatchFn = mockDispatch(0.001, "x")
	cfg.JudgeFn = mockJudge(true)
	res, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfgOut(cfg), "gate decision: REFUSED (current_tier_not_run") {
		t.Errorf("baseline-not-run reason not surfaced: %q", cfgOut(cfg))
	}
	if res.Gate.Decision != "refused" {
		t.Errorf("decision = %q", res.Gate.Decision)
	}
}

func fmtPct(f float64) string { return strings.TrimSpace(fmt.Sprintf("%5.1f%%", f*100)) }

// ---- review round: truncated verdicts, partial data, paired gate ---------------

func TestParseJudgeOutput_TruncatedNeverScoredFromNested(t *testing.T) {
	cases := []struct{ name, raw string }{
		{"unclosed outer, inner pass true", `{"notes":"x","criteria_scores":[{"id":1,"pass":true}]`},
		{"truncated after inner pass false", `{"pass":true,"criteria_scores":[{"id":1,"pass":false},{"id":2,"no`},
		{"fenced block cut mid-way", "```json\n{\"criteria_scores\":[{\"id\":1,\"pass\":true},{\"id\":2,"},
		{"prose then truncated", `Verdict follows. {"notes": "long", "criteria_scores": [{"pass": true}]`},
		{"spaced opener truncated", "{\n  \"criteria_scores\": [{\"pass\": true}]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseJudgeOutput(tc.raw)
			if !got.Unscored() {
				t.Fatalf("truncated verdict scored: %+v", got)
			}
			if got.Raw != tc.raw {
				t.Errorf("raw not kept")
			}
		})
	}
	// A stray prose brace must still not hide a later valid verdict.
	if got := parseJudgeOutput(`Note { see below. {"pass":true}`); got.Unscored() || !got.Pass {
		t.Errorf("stray brace hid the verdict: %+v", got)
	}
}

// fixed-size partial-data tests use 5 cases (the minimum).
func TestEval_PartialBudgetRun_NoCandidate(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	cfg.MaxCostUSD = 0.05
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 8)
	lastTier := ""
	cfg.DispatchFn = func(agentID, task, tier, runID, project string) (DispatchResult, error) {
		lastTier = tier
		return DispatchResult{Stdout: "x", Cost: 0.01, DurationS: 1}, nil
	}
	// Baseline opus fails, cheaper tiers pass: would promote on partial data.
	cfg.JudgeFn = func(string, string, string) (JudgeResult, error) {
		return JudgeResult{Pass: lastTier != "opus"}, nil
	}
	res, err := Run(cfg)
	if err == nil {
		t.Fatal("partial run must return an error")
	}
	if res.CandidateEmitted {
		t.Error("partial run emitted a candidate")
	}
	if _, serr := os.Stat(cfg.CandidatesFile); serr == nil {
		t.Error("candidates file written for a partial run")
	}
	if fin := lastRecord(t, cfg.EvalLog, "eval_run_finished"); fin["partial"] != true {
		t.Errorf("eval_run_finished must carry partial=true: %v", fin["partial"])
	}
}

func TestEval_CompleteRun_FinishedNotPartial(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 5)
	cfg.DispatchFn = mockDispatch(0.001, "x")
	cfg.JudgeFn = mockJudge(true)
	if _, err := Run(cfg); err != nil {
		t.Fatal(err)
	}
	fin := lastRecord(t, cfg.EvalLog, "eval_run_finished")
	if fin["partial"] != false {
		t.Errorf("partial = %v want false", fin["partial"])
	}
	if _, ok := fin["tier_n_scored"]; !ok {
		t.Error("tier_n_scored missing")
	}
}

// All-but-one case unscored must not yield a candidate at n=1.
func TestEval_MostlyUnscored_NoCandidate(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 5)
	lastTier := ""
	seen := map[string]int{}
	cfg.DispatchFn = func(agentID, task, tier, runID, project string) (DispatchResult, error) {
		lastTier = tier
		seen[tier]++
		return DispatchResult{Stdout: "x", Cost: 0.001, DurationS: 1}, nil
	}
	cfg.JudgeFn = func(string, string, string) (JudgeResult, error) {
		if seen[lastTier] > 1 { // only each tier's first case is scored
			return parseJudgeOutput("no verdict"), nil
		}
		return JudgeResult{Pass: lastTier != "opus"}, nil // baseline fails, cheap passes
	}
	res, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.CandidateEmitted {
		t.Fatalf("candidate emitted at n=1: %+v", res.Gate)
	}
	found := false
	for _, r := range readRecords(t, cfg.EvalLog, "candidate_refused") {
		if r["reason"] == "insufficient_scored_cases" {
			found = true
		}
	}
	if !found {
		t.Error("insufficient_scored_cases refusal not logged")
	}
}

// The gate compares candidate and baseline on the cases both scored: a
// baseline unscored on its failing cases must not look better or worse.
func TestEval_GatePairedOnCommonCases(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 6)
	lastTier := ""
	n := map[string]int{}
	cfg.DispatchFn = func(agentID, task, tier, runID, project string) (DispatchResult, error) {
		lastTier = tier
		n[tier]++
		return DispatchResult{Stdout: "x", Cost: 0.001, DurationS: 1}, nil
	}
	cfg.JudgeFn = func(string, string, string) (JudgeResult, error) {
		switch lastTier {
		case "opus": // scores only cases 1-4
			if n["opus"] > 4 {
				return parseJudgeOutput("?"), nil
			}
			return JudgeResult{Pass: true}, nil
		default:
			return JudgeResult{Pass: true}, nil
		}
	}
	res, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Gate.Rows {
		if r.NScored != 4 {
			t.Errorf("row %s NScored=%d want 4 (paired with baseline)", r.Tier, r.NScored)
		}
		if r.Mode != "insufficient_n" {
			t.Errorf("row %s mode=%s want insufficient_n (4 < 5)", r.Tier, r.Mode)
		}
	}
	if res.CandidateEmitted {
		t.Error("candidate emitted from 4 paired cases")
	}
}

func TestEval_DispatchError_UnscoredNotJudged(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 5)
	calls := 0
	cfg.DispatchFn = func(agentID, task, tier, runID, project string) (DispatchResult, error) {
		calls++
		if calls == 2 {
			return DispatchResult{}, errors.New("boom")
		}
		return DispatchResult{Stdout: "x", Cost: 0.001, DurationS: 1}, nil
	}
	judged := 0
	cfg.JudgeFn = func(string, string, string) (JudgeResult, error) { judged++; return JudgeResult{Pass: true}, nil }
	if _, err := Run(cfg); err != nil {
		t.Fatal(err)
	}
	if judged != 14 {
		t.Errorf("judged=%d want 14 (the failed dispatch must not be judged)", judged)
	}
	var unscored int
	for _, r := range readRecords(t, cfg.EvalLog, "eval_case") {
		if r["scored"] == false && strings.Contains(r["judge_error"].(string), "subject dispatch failed") {
			unscored++
		}
	}
	if unscored != 1 {
		t.Errorf("unscored dispatch-failure records = %d want 1", unscored)
	}
}

func TestEval_UnloadableCase_FailsLoudly(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 5)
	bad := filepath.Join(cfg.YakosRoot, "lib", "agents", "backend", "eval", "case-zz.json")
	_ = os.WriteFile(bad, []byte("{not json"), 0o644)
	called := false
	cfg.DispatchFn = func(agentID, task, tier, runID, project string) (DispatchResult, error) {
		called = true
		return DispatchResult{}, nil
	}
	if _, err := Run(cfg); err == nil || !strings.Contains(err.Error(), "case-zz.json") {
		t.Fatalf("want error naming the bad case, got %v", err)
	}
	if called {
		t.Error("dispatched despite an unloadable case")
	}
}

func TestEval_MinCasesCountsLoadedCases(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	setupEvalAgent(t, cfg, "backend", "opus", "backend", 4)
	_ = os.WriteFile(filepath.Join(cfg.YakosRoot, "lib", "agents", "backend", "eval", "case-zz.json"), []byte("{bad"), 0o644)
	if _, err := Run(cfg); err == nil {
		t.Fatal("4 loadable + 1 broken must not satisfy the 5-case minimum")
	}
}
