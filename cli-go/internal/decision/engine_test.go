package decision

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSet(t *testing.T) *QuestionSet {
	t.Helper()
	qs, errs := ParseSet("demo", []byte(goodSet))
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	return qs
}

func TestExecute_LogsRecordNeverRawState(t *testing.T) {
	j, _ := newServer(t, 200)
	lg := NewLogger(filepath.Join(t.TempDir(), "state", "decision-log.ndjson"))
	eng := &Engine{Provider: j, Logger: lg, Egress: EgressConfig{Level: EgressStrict}}
	secret := "SUPERSECRETPREVIEWTEXT-" + fakeAWS
	out := eng.Execute(context.Background(), testSet(t),
		map[string]any{"tool": "Write", "file_path": "a.go", "extra": secret}, ModeShadow, "sess", 0)
	if out.Err != nil {
		t.Fatal(out.Err)
	}
	data, err := os.ReadFile(lg.Path)
	if err != nil {
		t.Fatal(err)
	}
	line := string(data)
	for _, bad := range []string{"SUPERSECRETPREVIEWTEXT", fakeAWS, "Write", "a.go", "classify", "in scope"} {
		if strings.Contains(line, bad) {
			t.Errorf("decision log contains %q: %s", bad, line)
		}
	}
	for _, want := range []string{`"surface":"demo"`, `"schema_id":"demo@1"`, `"schema_hash":"` + testSet(t).Hash, `"mode":"shadow"`,
		`"model":"jev-1.13.0"`, `"latency_ms"`, `"input_tokens":1000`, `"cost_usd"`, `"provider":"jev"`, `"status":"ok"`, `"confidence":0.8`, `"choice":"dangerous"`} {
		if !strings.Contains(line, want) {
			t.Errorf("record lacks %s: %s", want, line)
		}
	}
	if strings.Count(line, "\n") != 1 {
		t.Error("exactly one line per call")
	}
}

func TestExecute_ErrorRecordCarriesClassAndMode(t *testing.T) {
	j, _ := newServer(t, 429)
	lg := NewLogger(filepath.Join(t.TempDir(), "l.ndjson"))
	eng := &Engine{Provider: j, Logger: lg}
	out := eng.Execute(context.Background(), testSet(t), map[string]any{"tool": "x"}, ModePrefilter, "", 0)
	if out.Class != ClassHTTP429 {
		t.Fatalf("class = %q", out.Class)
	}
	data, _ := os.ReadFile(lg.Path)
	if !strings.Contains(string(data), `"provider_error_class":"http_429"`) || !strings.Contains(string(data), `"mode":"prefilter"`) {
		t.Errorf("record: %s", data)
	}
}

func TestExecute_OversizeNeverReachesProvider(t *testing.T) {
	j, rec := newServer(t, 200)
	set := testSet(t)
	set.MaxStateBytes = 64
	eng := &Engine{Provider: j, Egress: EgressConfig{Level: EgressFull}}
	out := eng.Execute(context.Background(), set, map[string]any{"tool": strings.Repeat("a b ", 100)}, ModeShadow, "", 0)
	if out.Class != ClassOversize || rec.calls != 0 {
		t.Fatalf("class=%q calls=%d", out.Class, rec.calls)
	}
}

func TestExecute_LoggerFailureDoesNotChangeOutcome(t *testing.T) {
	j, _ := newServer(t, 200)
	f := filepath.Join(t.TempDir(), "f")
	_ = os.WriteFile(f, []byte("x"), 0o600)
	eng := &Engine{Provider: j, Logger: NewLogger(filepath.Join(f, "sub", "l.ndjson"))}
	if out := eng.Execute(context.Background(), testSet(t), map[string]any{"tool": "x"}, ModeShadow, "", 0); out.Err != nil {
		t.Fatalf("log failure must not fail the decision: %v", out.Err)
	}
}

func TestLogger_FileMode0600(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d", "l.ndjson")
	if err := NewLogger(p).Append(Record{Type: "decision"}); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 && !isWindows() {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
}

func TestRecordHasNoStateField(t *testing.T) {
	// Structural guard: adding a raw-state-bearing field to Record must be a
	// deliberate, reviewed change that also updates this list.
	allowed := map[string]bool{"type": true, "ts": true, "id": true, "surface": true, "schema_id": true, "schema_hash": true,
		"provider": true, "model": true, "mode": true, "session": true, "state_bytes": true, "redactions": true,
		"latency_ms": true, "input_tokens": true, "output_tokens": true, "cost_usd": true, "status": true,
		"provider_error_class": true, "answers": true}
	b, _ := jsonMarshal(Record{})
	var m map[string]any
	_ = jsonUnmarshal(b, &m)
	full, _ := jsonMarshal(Record{Answers: map[string]AnswerSummary{"a": {}}, ErrorClass: "x", Session: "s"})
	_ = jsonUnmarshal(full, &m)
	for k := range m {
		if !allowed[k] {
			t.Errorf("unexpected log field %q", k)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	if c, err := LoadConfig(filepath.Join(dir, "none.yml")); err != nil || c.Provider != ProviderNone || c.Budget.MaxCallsPerSession != 2000 {
		t.Fatalf("missing file: %+v %v", c, err)
	}
	p := filepath.Join(dir, ".yakos.yml")
	_ = os.WriteFile(p, []byte("yakos: 0.9\nprofile:\n  type: cli-tool\n"), 0o600)
	if c, _ := LoadConfig(p); c.Provider != ProviderNone {
		t.Error("absent block means none")
	}
	_ = os.WriteFile(p, []byte("decisions:\n  provider: jev\n  model: jev-1.13.0\n  budget: {max_calls_per_session: 5}\n  egress: {level: previews, never_paths: [x/*]}\n  surfaces:\n    demo: {mode: shadow, min_confidence: 0.7}\n"), 0o600)
	c, err := LoadConfig(p)
	if err != nil || c.Provider != "jev" || c.Budget.MaxCallsPerSession != 5 || c.Budget.MaxUSDPerDay != 1.0 || c.Egress.Level != "previews" || c.Surfaces["demo"].MinConfidence != 0.7 {
		t.Fatalf("%+v %v", c, err)
	}
	_ = os.WriteFile(p, []byte("decisions: [not a map"), 0o600)
	if c, err := LoadConfig(p); err == nil || c.Provider != ProviderNone {
		t.Error("malformed config must error and default to none")
	}
}

func TestMock_NeutralDefaultNeverTripsThresholds(t *testing.T) {
	m := &Mock{Getenv: func(string) string { return "" }}
	res, err := m.Decide(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if *res.Answers["risk"].Confidence != 0 || res.Answers["risk"].Choice != "benign" || *res.Answers["scope"].Noul != 0.5 {
		t.Errorf("%+v", res.Answers)
	}
	res2, _ := m.Decide(context.Background(), testRequest())
	a, _ := jsonMarshal(res.Answers)
	b, _ := jsonMarshal(res2.Answers)
	if string(a) != string(b) {
		t.Error("mock must be deterministic")
	}
}

func TestMock_FixtureFileDirAndError(t *testing.T) {
	dir := t.TempDir()
	body, _ := jsonMarshal(MockFixture{Answers: okAnswers(), Usage: Usage{InputTokens: 10}})
	_ = os.WriteFile(filepath.Join(dir, "supervisor-prefilter.json"), body, 0o600)
	env := map[string]string{EnvMock: dir}
	m := &Mock{Getenv: func(k string) string { return env[k] }}
	res, err := m.Decide(context.Background(), testRequest())
	if err != nil || res.Answers["risk"].Choice != "dangerous" || res.Provider != "mock" {
		t.Fatalf("dir fixture: %+v %v", res, err)
	}
	file := filepath.Join(t.TempDir(), "f.json")
	_ = os.WriteFile(file, body, 0o600)
	if _, err := (&Mock{Fixture: file}).Decide(context.Background(), testRequest()); err != nil {
		t.Fatalf("file fixture: %v", err)
	}
	efile := filepath.Join(t.TempDir(), "e.json")
	_ = os.WriteFile(efile, []byte(`{"error":"timeout"}`), 0o600)
	if _, err := (&Mock{Fixture: efile}).Decide(context.Background(), testRequest()); ErrorClass(err) != ClassTimeout {
		t.Fatalf("error fixture: %v", err)
	}
	// A fixture missing a question is rejected like a bad vendor response.
	bad := filepath.Join(t.TempDir(), "b.json")
	_ = os.WriteFile(bad, []byte(`{"answers":{}}`), 0o600)
	if _, err := (&Mock{Fixture: bad}).Decide(context.Background(), testRequest()); ErrorClass(err) != ClassMalformed {
		t.Fatalf("incomplete fixture: %v", err)
	}
	if _, err := (&Mock{Fixture: dir}).Decide(context.Background(), Request{Surface: "../x", Model: PinnedModel, Questions: testQuestions()}); err == nil {
		t.Fatal("traversal surface must be rejected")
	}
}

func TestNone_AlwaysDisabled(t *testing.T) {
	n := NewNone()
	if ErrorClass(n.Available(context.Background())) != ClassDisabled {
		t.Fatal("none.Available")
	}
	if _, err := n.Decide(context.Background(), testRequest()); ErrorClass(err) != ClassDisabled {
		t.Fatal("none.Decide")
	}
}

func TestKillSwitch(t *testing.T) {
	if !KillSwitch(func(k string) string { return "1" }) || KillSwitch(func(string) string { return "0" }) || KillSwitch(func(string) string { return "" }) {
		t.Fatal("only YAKOS_DECISION_DISABLE=1 kills")
	}
	m := &Mock{Getenv: func(string) string { return "1" }}
	if _, err := m.Decide(context.Background(), testRequest()); ErrorClass(err) != ClassDisabled {
		t.Fatal("mock must honour the kill switch")
	}
}

func TestErrorClass(t *testing.T) {
	if ErrorClass(nil) != "" || ErrorClass(context.DeadlineExceeded) != ClassTimeout || ErrorClass(os.ErrNotExist) != ClassInternal {
		t.Fatal("ErrorClass mapping")
	}
}
