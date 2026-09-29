package doctor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bakw00ds/yakos/internal/decision"
)

const probeSet = `schema_id: probe-demo@1
version: 1
surface: probe-demo
model: jev-1.13.0
may_block: false
max_state_bytes: 1024
state_fields: [tool]
questions:
  q: {type: noul, instructions: "The call is in scope."}
`

// probeFixture builds a lib dir with one question set and a project dir.
func probeFixture(t *testing.T, setBody, cfgBody string) (lib, proj, home string) {
	t.Helper()
	root := t.TempDir()
	lib = filepath.Join(root, "lib")
	writeFile(t, filepath.Join(lib, "decisions", "probe-demo.yaml"), setBody)
	proj = filepath.Join(root, "proj")
	if cfgBody != "" {
		writeFile(t, filepath.Join(proj, ".yakos.yml"), cfgBody)
	} else if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	return lib, proj, makeTmpHome(t)
}

func runProbe(t *testing.T, cfg Config, env map[string]string) (string, *Report) {
	t.Helper()
	var buf bytes.Buffer
	cfg.Writer = &buf
	cfg.LookPath = noLookPath
	cfg.ProbeDecision = true
	cfg.Environ = func(k string) string { return env[k] }
	rep, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return buf.String(), rep
}

func TestDecisionProbe_OffByDefault(t *testing.T) {
	var buf bytes.Buffer
	_, err := Run(Config{HomeDir: makeTmpHome(t), Writer: &buf, LookPath: noLookPath, Environ: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "Decision provider probe") || strings.Contains(buf.String(), "TYPESAFE") {
		t.Fatalf("default report must be unchanged:\n%s", buf.String())
	}
}

func TestDecisionProbe_KeyNeverPrinted_NoNetworkWithoutLive(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&hits, 1) }))
	defer srv.Close()
	lib, proj, home := probeFixture(t, probeSet, "decisions:\n  provider: jev\n  model: jev-1.13.0\n")
	out, rep := runProbe(t, Config{HomeDir: home, YakosLib: lib, Getwd: func() (string, error) { return proj, nil }},
		map[string]string{"TYPESAFE_API_KEY": "sk-SECRETVALUE-123", "TYPESAFE_BASE_URL": srv.URL})
	if strings.Contains(out, "SECRETVALUE") {
		t.Fatalf("key leaked:\n%s", out)
	}
	if !strings.Contains(out, "TYPESAFE_API_KEY: set") {
		t.Errorf("missing key line:\n%s", out)
	}
	if hits != 0 {
		t.Fatal("no network without --live")
	}
	if !strings.Contains(out, "live reachability not checked") || !strings.Contains(out, "probe-demo@1: hash ") {
		t.Errorf("output:\n%s", out)
	}
	if errCount(rep) != 0 {
		t.Errorf("errors = %d\n%s", errCount(rep), out)
	}
}

func TestDecisionProbe_KeyMissingWarnsOnlyWhenJev(t *testing.T) {
	lib, proj, home := probeFixture(t, probeSet, "decisions:\n  provider: jev\n")
	_, rep := runProbe(t, Config{HomeDir: home, YakosLib: lib, Getwd: func() (string, error) { return proj, nil }}, nil)
	if warnCount(rep) == 0 {
		t.Error("jev without a key must warn")
	}
	lib, proj, home = probeFixture(t, probeSet, "")
	_, rep = runProbe(t, Config{HomeDir: home, YakosLib: lib, Getwd: func() (string, error) { return proj, nil }}, nil)
	if warnCount(rep) != 0 || errCount(rep) != 0 {
		t.Error("provider none without a key is fine")
	}
}

func TestDecisionProbe_InvalidSetAndAliasModelAreErrors(t *testing.T) {
	lib, proj, home := probeFixture(t, strings.Replace(probeSet, "jev-1.13.0", "jev-latest", 1), "")
	out, rep := runProbe(t, Config{HomeDir: home, YakosLib: lib, Getwd: func() (string, error) { return proj, nil }}, nil)
	if errCount(rep) == 0 || !strings.Contains(out, "alias or unpinned") {
		t.Fatalf("alias set must error:\n%s", out)
	}
	lib, proj, home = probeFixture(t, probeSet, "decisions:\n  model: jev-latest\n")
	out, rep = runProbe(t, Config{HomeDir: home, YakosLib: lib, Getwd: func() (string, error) { return proj, nil }}, nil)
	if errCount(rep) == 0 || !strings.Contains(out, "decisions.model") {
		t.Fatalf("alias config model must error:\n%s", out)
	}
}

func TestDecisionProbe_ModelMismatchWarns(t *testing.T) {
	lib, proj, home := probeFixture(t, probeSet, "decisions:\n  model: jev-1.14.0\n")
	out, rep := runProbe(t, Config{HomeDir: home, YakosLib: lib, Getwd: func() (string, error) { return proj, nil }}, nil)
	if warnCount(rep) == 0 || !strings.Contains(out, "pins model jev-1.13.0 but decisions.model is jev-1.14.0") {
		t.Fatalf("mismatch must warn:\n%s", out)
	}
}

func TestDecisionProbe_BudgetAndBreakerState(t *testing.T) {
	lib, proj, home := probeFixture(t, probeSet, "")
	sd := filepath.Join(home, ".yakos-state")
	ledger := decision.NewBudget(filepath.Join(sd, "decision-budget.json"), 1, 1).LedgerPath()
	writeFile(t, ledger, "not json\n")
	out, rep := runProbe(t, Config{HomeDir: home, YakosLib: lib, Getwd: func() (string, error) { return proj, nil }}, nil)
	if errCount(rep) == 0 || !strings.Contains(out, "unreadable") {
		t.Fatalf("corrupt budget must error:\n%s", out)
	}
	os.Remove(ledger)
	writeFile(t, filepath.Join(sd, "decision-breaker.json"), `{"consecutive_failures":5,"open_until":"2999-01-01T00:00:00Z","last_failure_class":"timeout"}`)
	out, rep = runProbe(t, Config{HomeDir: home, YakosLib: lib, Getwd: func() (string, error) { return proj, nil }}, nil)
	if warnCount(rep) == 0 || !strings.Contains(out, "circuit breaker OPEN") {
		t.Fatalf("open breaker must warn:\n%s", out)
	}
	if !strings.Contains(out, "budget ledger readable") {
		t.Errorf("missing budget line:\n%s", out)
	}
}

func liveServer(t *testing.T, model string, status int) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("Authorization") != "Bearer live-key" {
			w.WriteHeader(401)
			return
		}
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		half := 0.5
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   model,
			"answers": map[string]any{"ping": map[string]any{"type": "noul", "noul": half}},
			"usage":   map[string]any{"input_tokens": 12, "output_tokens": 0},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestDecisionProbe_Live(t *testing.T) {
	lib, proj, home := probeFixture(t, probeSet, "decisions:\n  provider: jev\n")
	srv, hits := liveServer(t, "jev-1.13.0", 200)
	env := map[string]string{"TYPESAFE_API_KEY": "live-key", "TYPESAFE_BASE_URL": srv.URL}
	out, rep := runProbe(t, Config{HomeDir: home, YakosLib: lib, Getwd: func() (string, error) { return proj, nil }, ProbeDecisionLive: true}, env)
	if *hits != 1 || errCount(rep) != 0 || !strings.Contains(out, "live probe ok: served model jev-1.13.0") {
		t.Fatalf("hits=%d errors=%d\n%s", *hits, errCount(rep), out)
	}
	if strings.Contains(out, "live-key") {
		t.Fatal("key leaked")
	}

	// Drift: the server serves a different version than pinned.
	srv2, _ := liveServer(t, "jev-1.14.0", 200)
	env["TYPESAFE_BASE_URL"] = srv2.URL
	out, rep = runProbe(t, Config{HomeDir: home, YakosLib: lib, Getwd: func() (string, error) { return proj, nil }, ProbeDecisionLive: true}, env)
	if warnCount(rep) == 0 || !strings.Contains(out, "model drift: served jev-1.14.0 but pinned jev-1.13.0") {
		t.Fatalf("drift must warn:\n%s", out)
	}

	// Server error is an error finding, with the class only.
	srv4, _ := liveServer(t, "", 529)
	env["TYPESAFE_BASE_URL"] = srv4.URL
	out, rep = runProbe(t, Config{HomeDir: home, YakosLib: lib, Getwd: func() (string, error) { return proj, nil }, ProbeDecisionLive: true}, env)
	if errCount(rep) == 0 || !strings.Contains(out, "live probe failed: http_529") {
		t.Fatalf("failure must error:\n%s", out)
	}
}

func TestDecisionProbe_LiveWithoutKeyIsErrorAndNoNetwork(t *testing.T) {
	lib, proj, home := probeFixture(t, probeSet, "")
	srv, hits := liveServer(t, "jev-1.13.0", 200)
	out, rep := runProbe(t, Config{HomeDir: home, YakosLib: lib, Getwd: func() (string, error) { return proj, nil }, ProbeDecisionLive: true},
		map[string]string{"TYPESAFE_BASE_URL": srv.URL})
	if *hits != 0 || errCount(rep) == 0 || !strings.Contains(out, "cannot probe") {
		t.Fatalf("hits=%d\n%s", *hits, out)
	}
}

// errCount / warnCount count only the decision-probe section: the rest of the
// report depends on the host (bash/git/jq on PATH) and is not under test.
func errCount(r *Report) int  { return countSev(r, SeverityErr) }
func warnCount(r *Report) int { return countSev(r, SeverityWarn) }
func countSev(r *Report, sev Severity) int {
	n := 0
	for _, f := range r.Findings {
		if f.Section == SectionDecisionProbe && f.Severity == sev {
			n++
		}
	}
	return n
}

func TestDecisionProbe_ProjectCapsAreClampedByPolicy(t *testing.T) {
	lib, proj, home := probeFixture(t, probeSet, "decisions:\n  budget: {max_usd_per_day: 500}\n")
	out, _ := runProbe(t, Config{HomeDir: home, YakosLib: lib, Getwd: func() (string, error) { return proj, nil }}, nil)
	if !strings.Contains(out, "of $1.00 today") {
		t.Fatalf("a project must not raise the daily cap above the default ceiling:\n%s", out)
	}
	writeFile(t, filepath.Join(home, ".yakos-state", "decision-policy.yml"), "budget: {max_usd_per_day: 600}\n")
	out, _ = runProbe(t, Config{HomeDir: home, YakosLib: lib, Getwd: func() (string, error) { return proj, nil }}, nil)
	if !strings.Contains(out, "of $500.00 today") {
		t.Fatalf("the user-level policy raises the ceiling; the project value then applies:\n%s", out)
	}
}
