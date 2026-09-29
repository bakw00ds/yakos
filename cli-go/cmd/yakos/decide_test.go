package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/decision"
)

const decideTestSet = `schema_id: demo@1
version: 1
surface: demo
model: jev-1.13.0
may_block: false
max_state_bytes: 4096
state_fields: [tool, file_path, preview]
questions:
  risk:
    type: choice
    instructions: Classify the call.
    criteria: {benign: fine, dangerous: destructive}
  scope: {type: noul, instructions: "The call is in scope."}
`

type decideFixture struct {
	t        *testing.T
	sets     string
	state    string
	env      map[string]string
	provider decision.Provider
}

func newDecideFixture(t *testing.T) *decideFixture {
	t.Helper()
	f := &decideFixture{t: t, sets: t.TempDir(), state: t.TempDir(), env: map[string]string{}}
	if err := os.WriteFile(filepath.Join(f.sets, "demo.yaml"), []byte(decideTestSet), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *decideFixture) run(stdin string, args ...string) (int, string, string) {
	f.t.Helper()
	var out, errb bytes.Buffer
	code := decideMain(decideEnv{
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errb,
		Getenv:    func(k string) string { return f.env[k] },
		StateDir:  f.state,
		Provider:  f.provider,
		YakosRoot: f.t.TempDir(), Home: f.t.TempDir(),
	}, append([]string{"--sets-dir", f.sets, "--config", filepath.Join(f.state, "none.yml")}, args...))
	return code, out.String(), errb.String()
}

func (f *decideFixture) mockFixture(answers string) {
	p := filepath.Join(f.t.TempDir(), "fx.json")
	if err := os.WriteFile(p, []byte(answers), 0o600); err != nil {
		f.t.Fatal(err)
	}
	f.env[decision.EnvMock] = p
}

const decideAnswers = `{"answers":{"risk":{"type":"choice","choice":"dangerous","probabilities":{"benign":0.1,"dangerous":0.9},"confidence":0.8},"scope":{"type":"noul","noul":0.2}},"usage":{"input_tokens":50,"output_tokens":0}}`

func nullReason(t *testing.T, out string) string {
	t.Helper()
	var v struct {
		Answer *json.RawMessage `json:"answer"`
		Reason string           `json:"reason"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil || v.Answer != nil {
		t.Fatalf("want {\"answer\":null,\"reason\":...}, got %q (%v)", out, err)
	}
	return v.Reason
}

func TestDecide_MockAnswerPrinted(t *testing.T) {
	f := newDecideFixture(t)
	f.mockFixture(decideAnswers)
	code, out, _ := f.run(`{"tool":"Bash","preview":"rm -rf /"}`, "demo", "--provider", "mock")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	var got struct {
		Answer     map[string]decision.Answer `json:"answer"`
		Surface    string                     `json:"surface"`
		SchemaHash string                     `json:"schema_hash"`
		Provider   string                     `json:"provider"`
		Mode       string                     `json:"mode"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout must be one JSON document: %v\n%s", err, out)
	}
	if got.Answer["risk"].Choice != "dangerous" || *got.Answer["scope"].Noul != 0.2 || got.Provider != "mock" || got.Mode != "prefilter" || len(got.SchemaHash) != 64 {
		t.Errorf("%+v", got)
	}
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Error("output must be a single line")
	}
	if b, _ := os.ReadFile(filepath.Join(f.state, decision.LogFileName)); !strings.Contains(string(b), `"surface":"demo"`) {
		t.Error("decision log not written")
	}
}

func TestDecide_ShadowFlagSetsModeInLog(t *testing.T) {
	f := newDecideFixture(t)
	f.mockFixture(decideAnswers)
	if code, _, _ := f.run(`{"tool":"x"}`, "demo", "--provider", "mock", "--shadow"); code != 0 {
		t.Fatal(code)
	}
	if b, _ := os.ReadFile(filepath.Join(f.state, decision.LogFileName)); !strings.Contains(string(b), `"mode":"shadow"`) {
		t.Errorf("log: %s", b)
	}
}

// The exit contract: 0 (shadow) or 3 (otherwise) on every failure, never 2.
func TestDecide_ExitContract_FailuresNeverExit2(t *testing.T) {
	type tc struct {
		name  string
		setup func(f *decideFixture) []string // extra args
		stdin string
		want  string // reason
	}
	cases := []tc{
		{"provider none", func(f *decideFixture) []string { return []string{"--provider", "none"} }, `{"tool":"x"}`, "disabled"},
		{"default provider is none", func(f *decideFixture) []string { return nil }, `{"tool":"x"}`, "disabled"},
		{"kill switch", func(f *decideFixture) []string {
			f.env["YAKOS_DECISION_DISABLE"] = "1"
			f.mockFixture(decideAnswers)
			return []string{"--provider", "mock"}
		}, `{"tool":"x"}`, "disabled"},
		{"jev without key", func(f *decideFixture) []string { return []string{"--provider", "jev"} }, `{"tool":"x"}`, "no_key"},
		{"invalid state json", func(f *decideFixture) []string { return []string{"--provider", "mock"} }, `{not json`, "bad_request"},
		{"state not object", func(f *decideFixture) []string { return []string{"--provider", "mock"} }, `"prose"`, "bad_request"},
		{"oversize state", func(f *decideFixture) []string { return []string{"--provider", "mock"} }, `{"tool":"` + strings.Repeat("a b ", 3000) + `","preview":"` + strings.Repeat("c d ", 3000) + `","file_path":"` + strings.Repeat("e/", 3000) + `"}`, "oversize"},
		{"mock error fixture", func(f *decideFixture) []string {
			p := filepath.Join(t.TempDir(), "e.json")
			_ = os.WriteFile(p, []byte(`{"error":"timeout"}`), 0o600)
			f.env[decision.EnvMock] = p
			return []string{"--provider", "mock"}
		}, `{"tool":"x"}`, "timeout"},
		{"breaker open", func(f *decideFixture) []string {
			f.env[decision.KeyEnv] = "k"
			until := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
			_ = os.WriteFile(filepath.Join(f.state, decision.BreakerFileName), []byte(`{"consecutive_failures":5,"open_until":"`+until+`"}`), 0o600)
			return []string{"--provider", "jev"}
		}, `{"tool":"x"}`, "breaker_open"},
		{"budget exhausted", func(f *decideFixture) []string {
			f.env[decision.KeyEnv] = "k"
			b := decision.NewBudget(filepath.Join(f.state, decision.BudgetFileName), 1, 1)
			_ = os.WriteFile(b.LedgerPath(), []byte(`{"u":5}`+"\n"), 0o600)
			return []string{"--provider", "jev"}
		}, `{"tool":"x"}`, "budget"},
		{"missing question set", func(f *decideFixture) []string {
			_ = os.Remove(filepath.Join(f.sets, "demo.yaml"))
			return []string{"--provider", "mock"}
		}, `{"tool":"x"}`, "bad_request"},
		{"empty stdin", func(f *decideFixture) []string { return []string{"--provider", "mock"} }, ``, "bad_request"},
	}
	for _, c := range cases {
		for _, shadow := range []bool{false, true} {
			f := newDecideFixture(t)
			extra := c.setup(f)
			args := append([]string{"demo"}, extra...)
			if shadow {
				args = append(args, "--shadow")
			}
			code, out, _ := f.run(c.stdin, args...)
			want := 3
			if shadow {
				want = 0
			}
			if code == 2 {
				t.Fatalf("%s shadow=%v: exit 2 is the hook block code and must never be used", c.name, shadow)
			}
			if code != want {
				t.Errorf("%s shadow=%v: exit %d, want %d\n%s", c.name, shadow, code, want, out)
				continue
			}
			if got := nullReason(t, out); got != c.want {
				t.Errorf("%s shadow=%v: reason %q, want %q", c.name, shadow, got, c.want)
			}
		}
	}
}

func TestDecide_TimeoutAgainstHangingServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer srv.Close()
	for _, shadow := range []bool{false, true} {
		f := newDecideFixture(t)
		f.env[decision.KeyEnv] = "k"
		f.env[decision.BaseURLEnv] = srv.URL
		args := []string{"demo", "--provider", "jev", "--timeout", "100ms"}
		want := 3
		if shadow {
			args = append(args, "--shadow")
			want = 0
		}
		start := time.Now()
		code, out, _ := f.run(`{"tool":"x"}`, args...)
		if code != want || nullReason(t, out) != "timeout" {
			t.Errorf("shadow=%v: code=%d out=%s", shadow, code, out)
		}
		if time.Since(start) > 2*time.Second {
			t.Errorf("deadline not honoured")
		}
	}
}

type panicProvider struct{}

func (panicProvider) Name() string                    { return "panic" }
func (panicProvider) Available(context.Context) error { return nil }
func (panicProvider) Decide(context.Context, decision.Request) (*decision.Result, error) {
	panic("boom")
}

func TestDecide_PanicNeverBecomesExit2(t *testing.T) {
	// Go's uncaught-panic status is 2, the hook block code.
	for _, shadow := range []bool{false, true} {
		f := newDecideFixture(t)
		f.provider = panicProvider{}
		args := []string{"demo"}
		want := 3
		if shadow {
			args = append(args, "--shadow")
			want = 0
		}
		code, out, errs := f.run(`{"tool":"x"}`, args...)
		if code != want {
			t.Errorf("shadow=%v: code=%d, want %d", shadow, code, want)
		}
		if nullReason(t, out) != decision.ClassInternal || !strings.Contains(errs, "internal error") {
			t.Errorf("out=%q err=%q", out, errs)
		}
	}
}

func TestDecide_UsageErrorsExit1(t *testing.T) {
	f := newDecideFixture(t)
	for _, args := range [][]string{
		{}, {"a", "b"}, {"--provider"}, {"demo", "--provider", "openai"}, {"demo", "--timeout", "soon"},
		{"demo", "--timeout", "-5s"}, {"../etc"}, {"Demo"}, {"demo", "--bogus"},
	} {
		if code, _, _ := f.run(`{}`, args...); code != 1 {
			t.Errorf("args %v: exit %d, want 1", args, code)
		}
	}
	if code, out, _ := f.run(``, "--help"); code != 0 || !strings.Contains(out, "never 2") {
		t.Errorf("help: %d %q", code, out)
	}
}

func TestDecide_ProviderPrecedence(t *testing.T) {
	f := newDecideFixture(t)
	f.mockFixture(decideAnswers)
	cfg := filepath.Join(t.TempDir(), ".yakos.yml")
	_ = os.WriteFile(cfg, []byte("decisions:\n  provider: none\n"), 0o600)
	run := func(env map[string]string, args ...string) (int, string) {
		for k, v := range env {
			f.env[k] = v
		}
		var out, errb bytes.Buffer
		code := decideMain(decideEnv{Stdin: strings.NewReader(`{"tool":"x"}`), Stdout: &out, Stderr: &errb,
			Getenv: func(k string) string { return f.env[k] }, StateDir: f.state, YakosRoot: t.TempDir(), Home: t.TempDir()},
			append([]string{"--sets-dir", f.sets, "--config", cfg, "demo"}, args...))
		return code, out.String()
	}
	if code, _ := run(nil); code != 3 {
		t.Errorf("config none: %d", code)
	}
	if code, _ := run(map[string]string{decision.EnvProvider: "mock"}); code != 0 {
		t.Errorf("env overrides config: %d", code)
	}
	if code, _ := run(map[string]string{decision.EnvProvider: "mock"}, "--provider", "none"); code != 3 {
		t.Errorf("flag overrides env: %d", code)
	}
}

func TestDecide_SurfaceOffInConfig(t *testing.T) {
	f := newDecideFixture(t)
	f.mockFixture(decideAnswers)
	cfg := filepath.Join(t.TempDir(), ".yakos.yml")
	_ = os.WriteFile(cfg, []byte("decisions:\n  provider: mock\n  surfaces:\n    demo: {mode: off}\n"), 0o600)
	var out, errb bytes.Buffer
	code := decideMain(decideEnv{Stdin: strings.NewReader(`{}`), Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string { return f.env[k] }, StateDir: f.state, YakosRoot: t.TempDir(), Home: t.TempDir()},
		[]string{"--sets-dir", f.sets, "--config", cfg, "demo", "--shadow"})
	if code != 0 || nullReason(t, out.String()) != "disabled" {
		t.Errorf("%d %s", code, out.String())
	}
}

func TestDecide_SecretsNeverReachLogOrStdout(t *testing.T) {
	f := newDecideFixture(t)
	f.mockFixture(decideAnswers)
	aws := "AKIA" + "IOSFODNN7EXAMPLE"
	code, out, errs := f.run(`{"tool":"Write","preview":"key `+aws+`"}`, "demo", "--provider", "mock")
	if code != 0 {
		t.Fatal(code)
	}
	logb, _ := os.ReadFile(filepath.Join(f.state, decision.LogFileName))
	for _, s := range []string{out, errs, string(logb)} {
		if strings.Contains(s, aws) || strings.Contains(s, "Write") {
			t.Errorf("state leaked: %s", s)
		}
	}
}

func TestDecide_KeyNeverPrinted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	f := newDecideFixture(t)
	f.env[decision.KeyEnv] = "sk-VERYSECRETKEY"
	f.env[decision.BaseURLEnv] = srv.URL
	code, out, errs := f.run(`{"tool":"x"}`, "demo", "--provider", "jev")
	if code != 3 || nullReason(t, out) != "http_401" {
		t.Fatalf("%d %s", code, out)
	}
	logb, _ := os.ReadFile(filepath.Join(f.state, decision.LogFileName))
	for _, s := range []string{out, errs, string(logb)} {
		if strings.Contains(s, "VERYSECRETKEY") {
			t.Errorf("key leaked: %s", s)
		}
	}
}

func TestDecide_StateFile(t *testing.T) {
	f := newDecideFixture(t)
	f.mockFixture(decideAnswers)
	p := filepath.Join(t.TempDir(), "s.json")
	_ = os.WriteFile(p, []byte(`{"tool":"x"}`), 0o600)
	if code, out, _ := f.run(``, "demo", "--provider", "mock", "--state-file", p); code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	if code, _, _ := f.run(``, "demo", "--provider", "mock", "--state-file", filepath.Join(t.TempDir(), "missing")); code != 3 {
		t.Fatal("missing state file is a decide failure (3)")
	}
}

// `yakos decide` has no bash equivalent, so it must reach the Go router under
// every YAKOS_IMPL setting (a bash "unknown command" would break the 0/3 exit
// contract that hooks rely on).
func TestIsDecideForceGo(t *testing.T) {
	for args, want := range map[string]bool{"decide": true, "decide x --shadow": true, "doctor": false, "hook": false, "": false} {
		var a []string
		if args != "" {
			a = strings.Fields(args)
		}
		if got := isDecideForceGo(a); got != want {
			t.Errorf("isDecideForceGo(%q) = %v, want %v", args, got, want)
		}
	}
}

func TestDecide_OversizeStdinIsFailureNotHang(t *testing.T) {
	f := newDecideFixture(t)
	f.mockFixture(decideAnswers)
	// Trailing whitespace keeps a truncated read valid JSON, so only the
	// explicit size check can reject it.
	big := `{"tool":"x"}` + strings.Repeat(" ", decideMaxStdin+10)
	code, out, errs := f.run(big, "demo", "--provider", "mock")
	if code != 3 || nullReason(t, out) != "bad_request" || !strings.Contains(errs, "exceeds") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errs)
	}
}

// End to end through the real binary with YAKOS_IMPL UNSET: on a checkout that
// still carries the bash tree, shadow-mode routing would hand `decide` to bash
// ("unknown command"), breaking the 0/3 contract. Skips when the binary is not
// built (make build).
func TestDecide_BinaryReachesGoRouterWithImplUnset(t *testing.T) {
	bin := resolveGoBinary()
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("binary not built: %v", err)
	}
	for _, impl := range []string{"", "bash"} {
		cmd := exec.Command(bin, "decide", "supervisor-prefilter", "--provider", "none", "--shadow") //nolint:gosec
		cmd.Stdin = strings.NewReader(`{"tool":"Bash"}`)
		var env []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "YAKOS_IMPL=") && !strings.HasPrefix(kv, "YAKOS_ROOT=") && !strings.HasPrefix(kv, "YAKOS_LIB=") && !strings.HasPrefix(kv, "HOME=") {
				env = append(env, kv)
			}
		}
		env = append(env, "HOME="+t.TempDir(), "YAKOS_DECISION_DISABLE=", "YAKOS_DISPATCH_LOG="+t.TempDir())
		if impl != "" {
			env = append(env, "YAKOS_IMPL="+impl)
		}
		cmd.Env = env
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		if err != nil {
			t.Fatalf("impl=%q: %v\nstdout=%s\nstderr=%s", impl, err, out.String(), errb.String())
		}
		if nullReason(t, out.String()) != "disabled" {
			t.Errorf("impl=%q: stdout=%s", impl, out.String())
		}
	}
}

// Review F7: shadow no longer blocks for 10 s by default.
func TestDecide_ShadowDefaultDeadlineIsCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(8 * time.Second):
		}
	}))
	defer srv.Close()
	f := newDecideFixture(t)
	f.env[decision.KeyEnv] = "k"
	f.env[decision.BaseURLEnv] = srv.URL
	start := time.Now()
	code, out, _ := f.run(`{"tool":"x"}`, "demo", "--provider", "jev", "--shadow")
	if code != 0 || nullReason(t, out) != "timeout" {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if d := time.Since(start); d > 2500*time.Millisecond {
		t.Errorf("shadow default blocked for %v (want ~1.5 s)", d)
	}
}

func TestDecide_ForeignBaseURLNeverReceivesTheKey(t *testing.T) {
	f := newDecideFixture(t)
	f.env[decision.KeyEnv] = "sk-SECRET"
	f.env[decision.BaseURLEnv] = "https://collector.evil.example"
	code, out, errs := f.run(`{"tool":"x"}`, "demo", "--provider", "jev")
	if code != 3 || nullReason(t, out) != "bad_request" || !strings.Contains(errs, "not allowed") || strings.Contains(errs+out, "sk-SECRET") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errs)
	}
}

// Review F6: a project's .yakos.yml cannot loosen egress or raise caps.
func TestDecide_ProjectConfigCannotLoosenPolicy(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		half := 0.5
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "jev-1.13.0",
			"answers": map[string]any{
				"risk":  map[string]any{"type": "choice", "choice": "benign", "probabilities": map[string]float64{"benign": 1}, "confidence": 0.9},
				"scope": map[string]any{"type": "noul", "noul": half}},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 0}})
	}))
	defer srv.Close()
	f := newDecideFixture(t)
	f.env[decision.KeyEnv] = "k"
	f.env[decision.BaseURLEnv] = srv.URL
	cfg := filepath.Join(t.TempDir(), ".yakos.yml")
	_ = os.WriteFile(cfg, []byte("decisions:\n  provider: jev\n  budget: {max_calls_per_session: 999999, max_usd_per_day: 999}\n  egress: {level: full}\n"), 0o600)
	long := strings.Repeat("a b ", 2000) // 8000 bytes: cut to 2 KiB under strict, kept under full
	var out, errb bytes.Buffer
	code := decideMain(decideEnv{Stdin: strings.NewReader(`{"tool":"Write","preview":"` + long + `"}`), Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string { return f.env[k] }, StateDir: f.state, YakosRoot: t.TempDir(), Home: t.TempDir()},
		[]string{"--sets-dir", f.sets, "--config", cfg, "demo"})
	if code != 0 {
		t.Fatalf("code=%d %s %s", code, out.String(), errb.String())
	}
	if len(body) > 4000 || !strings.Contains(string(body), "truncated") {
		t.Errorf("project egress: full must be clamped to strict (body %d bytes)", len(body))
	}
	// The user-level policy may loosen it.
	_ = os.WriteFile(filepath.Join(f.state, decision.PolicyFileName), []byte("egress: {level: full}\n"), 0o600)
	out.Reset()
	code = decideMain(decideEnv{Stdin: strings.NewReader(`{"tool":"Write","preview":"` + long + `"}`), Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string { return f.env[k] }, StateDir: f.state, YakosRoot: t.TempDir(), Home: t.TempDir()},
		[]string{"--sets-dir", f.sets, "--config", cfg, "demo"})
	if code != 3 { // 8000-byte preview exceeds the demo set's 4096 cap: oversize proves it was NOT truncated
		t.Fatalf("with the user policy allowing full the long preview must reach the size cap: code=%d %s", code, out.String())
	}
}

func TestDecide_Promote(t *testing.T) {
	f := newDecideFixture(t)
	report := filepath.Join(t.TempDir(), "eval.md")
	_ = os.WriteFile(report, []byte("precision 0.9\n"), 0o600)
	code, out, errs := f.run(``, "promote", "demo", "--report", report)
	if code != 0 {
		t.Fatalf("code=%d %s %s", code, out, errs)
	}
	var p decision.Promotion
	if err := json.Unmarshal([]byte(out), &p); err != nil || p.Surface != "demo" || p.RecordSHA256 == "" {
		t.Fatalf("%v %s", err, out)
	}
	set, _ := decision.LoadSet(f.sets, "demo")
	if !decision.HasPromotion(decision.StatePaths{Dir: f.state}.Promotions(), "demo", set.Hash) {
		t.Fatal("the recorded promotion must verify")
	}
	for _, args := range [][]string{{"promote", "demo"}, {"promote", "../x", "--report", report}, {"promote", "demo", "--report", filepath.Join(t.TempDir(), "missing")}, {"promote", "nosuch", "--report", report}} {
		if c, _, _ := f.run(``, args...); c != 1 {
			t.Errorf("%v: exit %d, want 1 (never 2)", args, c)
		}
	}
}
