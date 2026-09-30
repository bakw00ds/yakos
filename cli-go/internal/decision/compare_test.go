package decision

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func prefilterSet() *QuestionSet {
	return &QuestionSet{
		Surface: "supervisor-prefilter", Hash: "hash-current", Model: "jev-1.13.0",
		Thresholds: map[string]ThresholdConfig{
			"risk_class":            {MinConfidence: 0.70},
			"bypasses_hard_control": {MinProbability: 0.80},
		},
	}
}

func ans(choice string, conf, bypass float64) map[string]AnswerSummary {
	return map[string]AnswerSummary{
		"risk_class":            {Type: "choice", Choice: choice, Confidence: fp(conf)},
		"bypasses_hard_control": {Type: "noul", Noul: fp(bypass)},
	}
}

func TestShadowEscalates(t *testing.T) {
	set := prefilterSet()
	cases := []struct {
		name string
		in   map[string]AnswerSummary
		want bool
		ok   bool
	}{
		{"dangerous at threshold", ans("dangerous", 0.70, 0.0), true, true},
		{"dangerous below threshold", ans("dangerous", 0.69, 0.0), false, true},
		{"needs_review never escalates", ans("needs_review", 0.99, 0.0), false, true},
		{"benign", ans("benign", 0.99, 0.0), false, true},
		{"bypass at threshold", ans("benign", 0.9, 0.80), true, true},
		{"bypass below threshold", ans("benign", 0.9, 0.79), false, true},
		{"missing answers", map[string]AnswerSummary{}, false, false},
	}
	for _, c := range cases {
		got, ok := ShadowEscalates(set, c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got (%v,%v) want (%v,%v)", c.name, got, ok, c.want, c.ok)
		}
	}
	if _, ok := ShadowEscalates(&QuestionSet{Surface: "other"}, ans("dangerous", 1, 1)); ok {
		t.Error("a surface without a mapping must report ok=false")
	}
}

func writeLog(t *testing.T, recs ...Record) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "decision-log.ndjson")
	var b strings.Builder
	for _, r := range recs {
		if r.Type == "" {
			r.Type = "decision"
		}
		line, _ := json.Marshal(r)
		b.Write(line)
		b.WriteByte('\n')
	}
	b.WriteString("not json\n")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func rec(local, trigger string, a map[string]AnswerSummary, status string, latency int64) Record {
	r := Record{Surface: "supervisor-prefilter", SchemaHash: "hash-current", Mode: ModeShadow,
		LocalVerdict: local, LocalTrigger: trigger, Answers: a, Status: status, LatencyMS: latency, CostUSD: 0.00005}
	if status != "ok" {
		r.ErrorClass = status
	}
	return r
}

func TestCompare_CountsAndAgreement(t *testing.T) {
	danger, safe := ans("dangerous", 0.9, 0.0), ans("benign", 0.95, 0.0)
	path := writeLog(t,
		rec(LocalEscalate, "risk-regex", danger, "ok", 100), // both escalate
		rec(LocalEscalate, "risk-regex", danger, "ok", 200), // both escalate
		rec(LocalPass, "", safe, "ok", 300),                 // both pass
		rec(LocalPass, "", danger, "ok", 400),               // shadow only
		rec(LocalEscalate, "large-diff", safe, "ok", 500),   // local only
		rec(LocalPass, "", nil, "timeout", 1500),            // fail-open
		rec(LocalPass, "", nil, "http_429", 50),             // fail-open
		rec("", "", danger, "ok", 10),                       // no local verdict: skipped
		Record{Surface: "supervisor-prefilter", SchemaHash: "old-hash", Mode: ModeShadow, LocalVerdict: LocalPass, Status: "ok", Answers: safe},        // other hash
		Record{Surface: "supervisor-prefilter", SchemaHash: "hash-current", Mode: ModePrefilter, LocalVerdict: LocalPass, Status: "ok", Answers: safe}, // not shadow
		Record{Surface: "other", SchemaHash: "hash-current", Mode: ModeShadow, LocalVerdict: LocalPass, Status: "ok", Answers: safe},                   // other surface
		Record{Type: "something-else", Surface: "supervisor-prefilter", SchemaHash: "hash-current", Mode: ModeShadow, LocalVerdict: LocalPass},         // not a decision
	)
	r, err := Compare(path, prefilterSet(), CompareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Records != 7 || r.Answered != 5 || r.OtherHash != 1 || r.NoLocal != 1 {
		t.Fatalf("records=%d answered=%d otherHash=%d noLocal=%d", r.Records, r.Answered, r.OtherHash, r.NoLocal)
	}
	if r.BothEscalate != 2 || r.BothPass != 1 || r.ShadowOnly != 1 || r.LocalOnly != 1 {
		t.Fatalf("matrix %d/%d/%d/%d", r.BothEscalate, r.BothPass, r.ShadowOnly, r.LocalOnly)
	}
	if r.Agreement != 0.6 {
		t.Errorf("agreement = %v, want 0.6", r.Agreement)
	}
	if got := r.LocalEscalationRecall; got < 0.666 || got > 0.667 {
		t.Errorf("local escalation recall = %v, want 2/3", got)
	}
	if r.Errors["timeout"] != 1 || r.Errors["http_429"] != 1 {
		t.Errorf("errors = %v", r.Errors)
	}
	if r.LocalTrigger["risk-regex"] != 2 || r.LocalTrigger["large-diff"] != 1 {
		t.Errorf("triggers = %v", r.LocalTrigger)
	}
	if r.P95LatencyMS != 1500 || r.MeanLatencyMS < 435.7 || r.MeanLatencyMS > 435.8 {
		t.Errorf("latency mean=%v p95=%d", r.MeanLatencyMS, r.P95LatencyMS)
	}
	if r.CostUSD < 0.000349 || r.CostUSD > 0.000351 {
		t.Errorf("cost = %v", r.CostUSD)
	}
	if r.SampleReached {
		t.Error("5 answered decisions must not reach the sample gate")
	}
	var sb strings.Builder
	r.WriteText(&sb)
	for _, want := range []string{"agreement      60.0%", "shadow-only escalate 1", "local-only escalate  1", "timeout=1", "not reached"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("text output lacks %q:\n%s", want, sb.String())
		}
	}
}

func TestCompare_MissingLogIsEmptyNotError(t *testing.T) {
	r, err := Compare(filepath.Join(t.TempDir(), "nope.ndjson"), prefilterSet(), CompareOptions{})
	if err != nil || r.Records != 0 || r.Agreement != 0 {
		t.Fatalf("got %+v, %v", r, err)
	}
	var sb strings.Builder
	r.WriteText(&sb)
	if !strings.Contains(sb.String(), "n/a") {
		t.Errorf("empty report must say n/a:\n%s", sb.String())
	}
}

func TestCompare_SampleGate(t *testing.T) {
	var recs []Record
	for i := 0; i < PromotionMinSample; i++ {
		recs = append(recs, rec(LocalPass, "", ans("benign", 0.9, 0), "ok", 10))
	}
	r, err := Compare(writeLog(t, recs...), prefilterSet(), CompareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.SampleReached || r.Agreement != 1 {
		t.Errorf("reached=%v agreement=%v", r.SampleReached, r.Agreement)
	}
}

// The engine copies the local verdict into the log record and never to the provider.
func TestExecute_RecordsLocalVerdict(t *testing.T) {
	lg := NewLogger(filepath.Join(t.TempDir(), "l.ndjson"))
	eng := &Engine{Provider: &Mock{Fixture: writeMockFixture(t, `{"answers":{"risk":{"type":"choice","choice":"benign","confidence":0.9},"scope":{"type":"noul","noul":0.5},"sev":{"type":"score","score":1,"confidence":0.5}}}`)},
		Logger: lg, LocalVerdict: LocalEscalate, LocalTrigger: "risk-regex"}
	if out := eng.Execute(context.Background(), testSet(t), map[string]any{"tool": "x"}, ModeShadow, "s", 0); out.Err != nil {
		t.Fatal(out.Err)
	}
	b, _ := os.ReadFile(lg.Path)
	if !strings.Contains(string(b), `"local_verdict":"escalate"`) || !strings.Contains(string(b), `"local_trigger":"risk-regex"`) {
		t.Errorf("record lacks local verdict: %s", b)
	}
	// Without a local verdict the fields are absent (log lines unchanged for other callers).
	lg2 := NewLogger(filepath.Join(t.TempDir(), "l2.ndjson"))
	eng2 := &Engine{Provider: eng.Provider, Logger: lg2}
	eng2.Execute(context.Background(), testSet(t), map[string]any{"tool": "x"}, ModeShadow, "s", 0)
	b2, _ := os.ReadFile(lg2.Path)
	if strings.Contains(string(b2), "local_") {
		t.Errorf("local fields must be omitted when unset: %s", b2)
	}
}

func writeMockFixture(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fx.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// hangProvider blocks until its context is cancelled.
type hangProvider struct{}

func (hangProvider) Name() string                    { return ProviderMock }
func (hangProvider) Available(context.Context) error { return nil }
func (hangProvider) Decide(ctx context.Context, _ Request) (*Result, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// A shadow call can never outlive its deadline, whatever the provider does.
func TestExecute_EnforcesDeadlineOnAnyProvider(t *testing.T) {
	eng := &Engine{Provider: hangProvider{}}
	done := make(chan Outcome, 1)
	go func() {
		done <- eng.Execute(context.Background(), testSet(t), map[string]any{"tool": "x"}, ModeShadow, "s", 50*time.Millisecond)
	}()
	select {
	case out := <-done:
		if out.Class != ClassTimeout {
			t.Errorf("class = %q, want timeout", out.Class)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Execute did not return: the provider deadline is not enforced")
	}
}

// Evidence must be about real traffic on the real provider: mock dry runs and
// tagged smoke calls never inflate agreement or the 200-decision gate.
func TestCompare_SkipsMockAndTaggedAndFilters(t *testing.T) {
	safe := ans("benign", 0.95, 0)
	mk := func(provider, tag, session, ts string) Record {
		r := rec(LocalPass, "", safe, "ok", 10)
		r.Provider, r.Tag, r.Session, r.TS = provider, tag, session, ts
		return r
	}
	path := writeLog(t,
		mk("jev", "", "real", "2026-09-30T12:00:00Z"),
		mk("jev", "", "real", "2026-09-01T12:00:00Z"),
		mk("mock", "", "real", "2026-09-30T12:00:00Z"),
		mk("jev", "smoke", "p2b-smoke", "2026-09-30T12:00:00Z"),
		mk("jev", "", "p2b-smoke", "2026-09-30T12:00:00Z"),
	)
	r, _ := Compare(path, prefilterSet(), CompareOptions{})
	if r.Answered != 3 || r.MockSkipped != 1 || r.TaggedSkipped != 1 {
		t.Errorf("default: answered=%d mock=%d tagged=%d", r.Answered, r.MockSkipped, r.TaggedSkipped)
	}
	r, _ = Compare(path, prefilterSet(), CompareOptions{IncludeMock: true, IncludeTagged: true})
	if r.Answered != 5 {
		t.Errorf("include: answered=%d", r.Answered)
	}
	r, _ = Compare(path, prefilterSet(), CompareOptions{ExcludeSessions: []string{"p2b-smoke"}})
	if r.Answered != 2 || r.Filtered != 1 {
		t.Errorf("exclude-session: answered=%d filtered=%d", r.Answered, r.Filtered)
	}
	r, _ = Compare(path, prefilterSet(), CompareOptions{Session: "real"})
	if r.Answered != 2 {
		t.Errorf("session: answered=%d", r.Answered)
	}
	since, _ := time.Parse(time.RFC3339, "2026-09-15T00:00:00Z")
	r, _ = Compare(path, prefilterSet(), CompareOptions{Since: since})
	if r.Answered != 2 || r.Filtered != 1 {
		t.Errorf("since: answered=%d filtered=%d", r.Answered, r.Filtered)
	}
}

// ignoreProvider never looks at its context.
type ignoreProvider struct{ release chan struct{} }

func (ignoreProvider) Name() string                    { return ProviderMock }
func (ignoreProvider) Available(context.Context) error { return nil }
func (p ignoreProvider) Decide(context.Context, Request) (*Result, error) {
	<-p.release
	return nil, nil
}

func TestExecute_DeadlineHoldsForAProviderThatIgnoresItsContext(t *testing.T) {
	rel := make(chan struct{})
	defer close(rel)
	eng := &Engine{Provider: ignoreProvider{rel}}
	done := make(chan Outcome, 1)
	go func() {
		done <- eng.Execute(context.Background(), testSet(t), map[string]any{"tool": "x"}, ModeShadow, "s", 50*time.Millisecond)
	}()
	select {
	case out := <-done:
		if out.Class != ClassTimeout {
			t.Errorf("class = %q", out.Class)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Execute waited on a provider that ignores its context")
	}
}

type panicProvider struct{}

func (panicProvider) Name() string                    { return ProviderMock }
func (panicProvider) Available(context.Context) error { return nil }
func (panicProvider) Decide(context.Context, Request) (*Result, error) {
	panic("boom")
}

func TestExecute_ProviderPanicIsAnInternalError(t *testing.T) {
	out := (&Engine{Provider: panicProvider{}}).Execute(context.Background(), testSet(t), map[string]any{"tool": "x"}, ModeShadow, "s", time.Second)
	if out.Class != ClassInternal {
		t.Errorf("class = %q", out.Class)
	}
}

// An untrusted policy file (symlink, group/world writable) is ignored whole.
func TestLoadPolicy_IgnoresUntrustedFiles(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yml")
	_ = os.WriteFile(good, []byte("provider: mock\n"), 0o600)
	if p, err := LoadPolicy(good); err != nil || p.Provider != "mock" {
		t.Fatalf("trusted file: %+v %v", p, err)
	}
	for name, mk := range map[string]func() string{
		"world-writable": func() string {
			p := filepath.Join(dir, "ww.yml")
			_ = os.WriteFile(p, []byte("provider: mock\n"), 0o600)
			_ = os.Chmod(p, 0o666)
			return p
		},
		"group-writable": func() string {
			p := filepath.Join(dir, "gw.yml")
			_ = os.WriteFile(p, []byte("provider: mock\n"), 0o600)
			_ = os.Chmod(p, 0o620)
			return p
		},
		"symlink": func() string {
			p := filepath.Join(dir, "link.yml")
			_ = os.Symlink(good, p)
			return p
		},
	} {
		if runtime.GOOS == "windows" {
			t.Skip("mode bits are not modelled on windows")
		}
		p, err := LoadPolicy(mk())
		if !errors.Is(err, ErrUntrustedPolicy) || p.Provider != "" {
			t.Errorf("%s: provider=%q err=%v, want ignored", name, p.Provider, err)
		}
	}
}
