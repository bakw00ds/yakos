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
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
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
