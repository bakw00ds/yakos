package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/decision"
)

// K-111 P2b: --local records the caller's heuristic verdict beside the answer;
// `decide compare` reads it back.

// recordingProvider captures the request it was handed and answers neutrally.
type recordingProvider struct{ req decision.Request }

func (*recordingProvider) Name() string                    { return decision.ProviderMock }
func (*recordingProvider) Available(context.Context) error { return nil }
func (p *recordingProvider) Decide(_ context.Context, r decision.Request) (*decision.Result, error) {
	p.req = r
	return &decision.Result{Provider: decision.ProviderMock, Model: r.Model, Answers: map[string]decision.Answer{}}, nil
}

func lastLogRecord(t *testing.T, f *decideFixture) decision.Record {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.state, decision.LogFileName))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var r decision.Record
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDecide_LocalVerdictRecordedOnSuccessAndFailure(t *testing.T) {
	f := newDecideFixture(t)
	f.mockFixture(decideAnswers)
	if code, _, _ := f.run(`{"tool":"x"}`, "demo", "--provider", "mock", "--shadow", "--local", "escalate", "--local-trigger", "risk-regex"); code != 0 {
		t.Fatal(code)
	}
	r := lastLogRecord(t, f)
	if r.LocalVerdict != "escalate" || r.LocalTrigger != "risk-regex" || r.Status != "ok" {
		t.Errorf("record = %+v", r)
	}

	// A provider failure still records the local verdict and exits 0 in shadow mode.
	f.mockFixture(`{"error":"timeout"}`)
	code, out, _ := f.run(`{"tool":"x"}`, "demo", "--provider", "mock", "--shadow", "--local", "pass")
	if code != 0 || nullReason(t, out) != "timeout" {
		t.Fatalf("code=%d out=%s", code, out)
	}
	r = lastLogRecord(t, f)
	if r.LocalVerdict != "pass" || r.Status != "timeout" || r.LocalTrigger != "" {
		t.Errorf("failure record = %+v", r)
	}
}

func TestDecide_LocalVerdictIsNeverSentToTheProvider(t *testing.T) {
	f := newDecideFixture(t)
	rp := &recordingProvider{}
	f.provider = rp
	if code, _, _ := f.run(`{"tool":"x"}`, "demo", "--shadow", "--local", "escalate", "--local-trigger", "sensitive-path"); code != 0 {
		t.Fatal(code)
	}
	b, _ := json.Marshal(rp.req)
	if strings.Contains(string(b), "escalate") || strings.Contains(string(b), "sensitive-path") {
		t.Errorf("local verdict leaked into the request: %s", b)
	}
}

func TestDecide_LocalFlagValidationAndTriggerSanitising(t *testing.T) {
	f := newDecideFixture(t)
	f.mockFixture(decideAnswers)
	if code, _, _ := f.run(`{"tool":"x"}`, "demo", "--provider", "mock", "--local", "maybe"); code != 1 {
		t.Errorf("invalid --local must be a usage error, got %d", code)
	}
	if code, _, _ := f.run(`{"tool":"x"}`, "demo", "--provider", "mock", "--shadow", "--local", "escalate",
		"--local-trigger", "risk-regex:rm -rf $(cat ~/.ssh/id_rsa) "+strings.Repeat("z", 100)); code != 0 {
		t.Fatal(code)
	}
	tr := lastLogRecord(t, f).LocalTrigger
	if len(tr) > 40 || strings.ContainsAny(tr, ":$() /~.") {
		t.Errorf("trigger not sanitised: %q", tr)
	}
}

func TestDecideCompare_ReadsLogAndPrintsAgreement(t *testing.T) {
	f := newDecideFixture(t)
	// Use the shipped supervisor-prefilter set so the escalate mapping applies.
	sets := filepath.Join("..", "..", "..", "lib", "decisions")
	real, err := decision.LoadSet(sets, "supervisor-prefilter")
	if err != nil {
		t.Fatal(err)
	}
	c := func(x float64) *float64 { return &x }
	mk := func(local, choice string, conf float64) decision.Record {
		return decision.Record{Type: "decision", Surface: real.Surface, SchemaHash: real.Hash, Mode: "shadow",
			LocalVerdict: local, Status: "ok", CostUSD: 0.00005,
			Answers: map[string]decision.AnswerSummary{
				"risk_class":            {Type: "choice", Choice: choice, Confidence: c(conf)},
				"bypasses_hard_control": {Type: "noul", Noul: c(0.01)},
			}}
	}
	var b bytes.Buffer
	for _, r := range []decision.Record{mk("escalate", "dangerous", 0.9), mk("pass", "benign", 0.9), mk("pass", "dangerous", 0.9), mk("escalate", "benign", 0.9)} {
		line, _ := json.Marshal(r)
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(f.state, decision.LogFileName), b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := decideMain(decideEnv{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }, StateDir: f.state, YakosRoot: t.TempDir(), Home: t.TempDir()},
		[]string{"compare", "supervisor-prefilter", "--sets-dir", sets})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "agreement      50.0%  (2 of 4)") {
		t.Errorf("text output:\n%s", out.String())
	}
	out.Reset()
	code = decideMain(decideEnv{Stdout: &out, Stderr: &errb, Getenv: func(string) string { return "" }, StateDir: f.state, YakosRoot: t.TempDir(), Home: t.TempDir()},
		[]string{"compare", "supervisor-prefilter", "--sets-dir", sets, "--json"})
	var rep decision.CompareReport
	if code != 0 || json.Unmarshal(out.Bytes(), &rep) != nil || rep.Answered != 4 || rep.ShadowOnly != 1 || rep.LocalOnly != 1 {
		t.Fatalf("code=%d json=%s", code, out.String())
	}
}

func TestDecideCompare_UsageErrors(t *testing.T) {
	f := newDecideFixture(t)
	if code, _, _ := f.run("", "compare", "Bad Surface"); code != 1 {
		t.Errorf("bad surface = %d", code)
	}
	if code, _, _ := f.run("", "compare", "no-such-surface"); code != 1 {
		t.Errorf("unknown surface = %d", code)
	}
}

func TestDecide_ConsumeStateFileDeletesOnlyTheHooksOwnFilesAfterReading(t *testing.T) {
	f := newDecideFixture(t)
	f.mockFixture(decideAnswers)
	mk := func(dir, name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(`{"tool":"x"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	gone := func(p string) bool { _, err := os.Lstat(p); return err != nil }
	args := func(p string, extra ...string) []string {
		return append([]string{"demo", "--provider", "mock", "--shadow", "--state-file", p, "--consume-state-file"}, extra...)
	}

	// Own file in the state dir: deleted after a successful read, even if the provider then fails.
	p := mk(f.state, "shadow-state-abc.json")
	if code, _, _ := f.run("", args(p)...); code != 0 || !gone(p) {
		t.Errorf("own state file must be consumed (code %d)", code)
	}
	f.mockFixture(`{"error":"timeout"}`)
	p = mk(f.state, "shadow-state-def.json")
	f.run("", args(p)...)
	if !gone(p) {
		t.Error("state file survived a provider failure")
	}
	f.mockFixture(decideAnswers)

	// Outside the state dir, wrong name, symlink, usage error: nothing deleted.
	other := t.TempDir()
	cases := map[string]string{
		"outside dir": mk(other, "shadow-state-x.json"),
		"wrong name":  mk(f.state, "victim.txt"),
	}
	target := mk(other, "target.json")
	link := filepath.Join(f.state, "shadow-state-link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	cases["symlink"] = link
	for name, path := range cases {
		f.run("", args(path)...)
		if gone(path) {
			t.Errorf("%s: %s was deleted", name, path)
		}
	}
	if gone(target) {
		t.Error("symlink target deleted")
	}
	// Every exit after the usage checks removes the hook's raw state file.
	bad := filepath.Join(f.state, "shadow-state-bad.json")
	_ = os.WriteFile(bad, []byte("not json"), 0o600)
	f.run("", args(bad)...)
	if !gone(bad) {
		t.Error("an unreadable state file must still be removed")
	}
	for name, run := range map[string]func(p string){
		"kill switch": func(p string) {
			f.env["YAKOS_DECISION_DISABLE"] = "1"
			f.run("", args(p)...)
			delete(f.env, "YAKOS_DECISION_DISABLE")
		},
		"surface off": func(p string) {
			cfg := filepath.Join(t.TempDir(), ".yakos.yml")
			_ = os.WriteFile(cfg, []byte("decisions:\n  surfaces:\n    demo: {mode: off}\n"), 0o600)
			f.run("", args(p, "--config", cfg)...)
		},
		"config error": func(p string) {
			cfg := filepath.Join(t.TempDir(), ".yakos.yml")
			_ = os.WriteFile(cfg, []byte("decisions: [unclosed\n"), 0o600)
			f.run("", args(p, "--config", cfg)...)
		},
		"question set error": func(p string) { f.run("", args(p, "--sets-dir", t.TempDir())...) },
	} {
		p := mk(f.state, "shadow-state-early-"+strings.ReplaceAll(name, " ", "-")+".json")
		run(p)
		if !gone(p) {
			t.Errorf("%s: the raw state file was left behind", name)
		}
	}
	p = mk(f.state, "shadow-state-usage.json")
	if code, _, _ := f.run("", args(p, "--local", "maybe")...); code != 1 || gone(p) {
		t.Errorf("usage error must not delete (code %d)", code)
	}
	// Without the flag nothing is deleted.
	p = mk(f.state, "shadow-state-keep.json")
	f.run("", "demo", "--provider", "mock", "--shadow", "--state-file", p)
	if gone(p) {
		t.Error("--state-file alone must not delete the file")
	}
}

func TestDecide_TagIsRecorded(t *testing.T) {
	f := newDecideFixture(t)
	f.mockFixture(decideAnswers)
	f.run(`{"tool":"x"}`, "demo", "--provider", "mock", "--shadow", "--tag", "smoke")
	if lastLogRecord(t, f).Tag != "smoke" {
		t.Error("tag not recorded")
	}
}

// A project .yakos.yml must not be able to start egress on its own.
func TestDecide_ProjectFileCannotEnableAProvider(t *testing.T) {
	f := newDecideFixture(t)
	f.mockFixture(decideAnswers)
	cfg := filepath.Join(t.TempDir(), ".yakos.yml")
	_ = os.WriteFile(cfg, []byte("decisions:\n  provider: mock\n"), 0o600)
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := decideMain(decideEnv{Stdin: strings.NewReader(`{"tool":"x"}`), Stdout: &out, Stderr: &errb,
			Getenv: func(k string) string { return f.env[k] }, StateDir: f.state, YakosRoot: t.TempDir(), Home: t.TempDir()},
			append([]string{"--sets-dir", f.sets, "--config", cfg, "demo", "--shadow"}, args...))
		return code, out.String(), errb.String()
	}
	code, out, errs := run()
	if code != 0 || nullReason(t, out) != "disabled" || !strings.Contains(errs, "cannot enable a provider") {
		t.Fatalf("project-only provider must resolve to none with a warning: %d %q %q", code, out, errs)
	}
	// The user-level policy file turns it on.
	_ = os.WriteFile(filepath.Join(f.state, decision.PolicyFileName), []byte("provider: mock\n"), 0o600)
	if code, out, _ := run(); code != 0 || strings.Contains(out, `"answer":null`) {
		t.Errorf("user policy provider must enable it: %d %q", code, out)
	}
	// An explicit provider: none in the project vetoes the user-level policy.
	_ = os.WriteFile(cfg, []byte("decisions:\n  provider: none\n"), 0o600)
	if _, out, _ := run(); nullReason(t, out) != "disabled" {
		t.Errorf("project none must veto the policy switch: %q", out)
	}
	// The environment is an explicit per-shell choice and still wins.
	f.env[decision.EnvProvider] = "mock"
	if _, out, _ := run(); strings.Contains(out, `"answer":null`) {
		t.Errorf("env must override the project veto: %q", out)
	}
}
