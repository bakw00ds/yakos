package consoleui_test

// models_page_test.go: the Models & Providers tab's read endpoints (K-153):
// role gating, no write path, response hygiene (no path, no secret value, no-store),
// DNS-rebinding refusal on the Host header, the overview's content, the bounded
// probe cache and eval reader, and the explain playground.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/auth"
	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/router"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

var modelsEndpoints = []string{"/api/models/overview", "/api/models/explain?agent=backend", "/api/router/policy"}

// Secret values planted in the environment and in the state files. None may ever
// appear in a response.
var modelsSentinels = []string{
	"SENTINEL-OPENAI-KEY", "SENTINEL-ANTHROPIC-KEY", "SENTINEL-GEMINI-KEY", "SENTINEL-ANTIGRAVITY-KEY",
	"SENTINEL-OAUTH-TOKEN", "SENTINEL-OVERLAY-KEY", "SENTINEL-POLICY-KEY", "SENTINEL-TASK-TEXT", "SENTINEL-EVAL-NOTE",
}

type modelsFx struct {
	home, ledger, workspace, root string
	srv                           *consoleui.Server
	tok                           string
	handler                       http.Handler
}

func (f modelsFx) state() string { return filepath.Join(f.home, ".yakos-state") }

func (f modelsFx) writeState(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(f.state(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.state(), name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f modelsFx) ledgerPath() string { return filepath.Join(f.ledger, "dispatch-log.ndjson") }

// newModelsFx builds a loopback console whose home, ledger, PATH and credentials
// are all scratch. id is the identity every request is served as.
func newModelsFx(t *testing.T, id netid.Identity) modelsFx {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("POSIX paths and shell stubs")
	}
	f := modelsFx{home: t.TempDir(), ledger: t.TempDir(), workspace: t.TempDir(), root: t.TempDir()}
	t.Setenv("HOME", f.home)
	t.Setenv("YAKOS_DISPATCH_LOG", f.ledger)
	t.Setenv("YAKOS_ROOT", "")
	t.Setenv("YAKOS_LIB", "")
	bin := t.TempDir()
	for _, cli := range []string{"codex", "agy"} {
		if err := os.WriteFile(filepath.Join(bin, cli), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("OPENAI_API_KEY", modelsSentinels[0])
	t.Setenv("ANTHROPIC_API_KEY", modelsSentinels[1])
	t.Setenv("GEMINI_API_KEY", modelsSentinels[2])
	t.Setenv("ANTIGRAVITY_API_KEY", modelsSentinels[3])
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", modelsSentinels[4])

	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	f.tok = tok
	f.srv = consoleui.MustNew(t, consoleui.Config{
		Token: tok, KanbanBoardPath: t.TempDir() + "/kanban.md", KanbanProject: "test",
		MetricsProjectDir: t.TempDir(), PerfWorkDir: t.TempDir(), Bus: bus, WorkDir: t.TempDir(),
		WorkspaceRoot: f.workspace, YakosRoot: f.root,
	})
	f.handler = consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(injectIdentityMiddleware(id, f.srv.HandlerForTest())))
	return f
}

var readerID = netid.Identity{OperatorID: "carol", Role: netid.RoleRead, Authenticated: true, Resolved: true}

func (f modelsFx) do(method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:7890"+path, strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+f.tok)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

func (f modelsFx) get(path string) *httptest.ResponseRecorder { return f.do(http.MethodGet, path, nil) }

func (f modelsFx) overview(t *testing.T) map[string]any {
	t.Helper()
	rr := f.get("/api/models/overview")
	if rr.Code != 200 {
		t.Fatalf("overview: %d %s", rr.Code, rr.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func seedModelsState(t *testing.T, f modelsFx) {
	t.Helper()
	f.writeState(t, "model-registry.yml", "models:\n  gpt-5.5:\n    enabled: false\naliases:\n  balanced:\n    codex: gpt-5.6-terra\nx_secret: SENTINEL-OVERLAY-KEY\n")
	f.writeState(t, "router-policy.yml", "x_secret: SENTINEL-POLICY-KEY\nallow_unsandboxed_runtimes: [codex]\nhooks_endpoint: true\n"+
		"rules:\n  - {match: {agent: backend}, action: {runtime: codex, model: gpt-5.6-terra}, override_pins: true}\n  - {match: {class: chat}, action: {runtime: claude, model: sonnet}}\n")
	ev := func(m map[string]any) string { b, _ := json.Marshal(m); return string(b) + "\n" }
	log := ev(map[string]any{"type": "eval_run_finished", "ts": "2026-10-01T10:00:00Z", "run_id": "run-old", "agent": "backend",
		"tier_pass_rates": map[string]float64{"haiku": 0.5, "sonnet": 0.9, "ti\x1b[31mer": 0.1, strings.Repeat("k", 65): 0.2}, "tier_mean_costs": map[string]float64{"haiku": 0.01, "bad\u202ekey": 3},
		"candidate_emitted": true, "candidate_tier": "sonnet", "note": "SENTINEL-EVAL-NOTE"}) +
		"not json at all SENTINEL-EVAL-NOTE\n" +
		ev(map[string]any{"type": "eval_run_finished", "ts": "2026-10-02T10:00:00Z", "run_id": "run-\x1b[31mevil", "agent": "backend"}) +
		ev(map[string]any{"type": "eval_run_finished", "ts": "2026-10-03T10:00:00Z", "run_id": "run-new", "agent": "reviewer", "partial": true}) +
		ev(map[string]any{"type": "dispatch_finished", "ts": time.Now().UTC().Format(time.RFC3339), "agent": "supervisor", "runtime": "claude",
			"project": f.workspace, "task_preview": "SENTINEL-TASK-TEXT", "usage": map[string]any{"input_tokens": 1000, "output_tokens": 500, "total_cost_usd": 1.25}, "billing": "api"})
	if err := os.WriteFile(f.ledgerPath(), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixedProbe(ctx context.Context, h string) auth.ProbeResult {
	switch h {
	case "claude":
		return auth.ProbeResult{CLIPresent: true, Authed: true}
	case "codex":
		return auth.ProbeResult{CLIPresent: true, Authed: false, AuthHint: "run: codex login, or set OPENAI_API_KEY"}
	}
	return auth.ProbeResult{CLIPresent: false, CLIHint: "install agy"}
}

// explainOK stands in for dispatch.Explain, which needs a roster and CLIs.
func explainOK(t *testing.T) {
	t.Helper()
	restore := consoleui.SetModelsExplainForTest(func(context.Context, dispatch.ExplainQuery) (router.RouteDecision, error) {
		return router.RouteDecision{Runtime: "claude", RuleID: "R0", Chain: []string{"claude"}}, nil
	})
	t.Cleanup(restore)
}

func TestModelsPage_RoleGating(t *testing.T) {
	explainOK(t)
	for name, tc := range map[string]struct {
		id   netid.Identity
		want int
	}{
		"read":                {readerID, 200},
		"dispatch":            {netid.Identity{OperatorID: "bob", Role: netid.RoleDispatch, Authenticated: true, Resolved: true}, 200},
		"admin":               {netid.Identity{OperatorID: "alice", Role: netid.RoleAdmin, Authenticated: true, Resolved: true}, 200},
		"authenticated, none": {netid.Identity{OperatorID: "dave", Role: netid.RoleNone, Authenticated: true, Resolved: true}, 403},
		"unresolved":          {netid.Identity{OperatorID: "x", Role: netid.RoleAdmin, Resolved: false}, 403},
	} {
		t.Run(name, func(t *testing.T) {
			f := newModelsFx(t, tc.id)
			for _, ep := range modelsEndpoints {
				if rr := f.get(ep); rr.Code != tc.want {
					t.Errorf("GET %s as %s = %d, want %d (%s)", ep, name, rr.Code, tc.want, rr.Body.String())
				}
			}
		})
	}
	t.Run("no bearer token", func(t *testing.T) {
		f := newModelsFx(t, readerID)
		for _, ep := range modelsEndpoints {
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7890"+ep, nil)
			rr := httptest.NewRecorder()
			f.handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("GET %s without a token = %d, want 401", ep, rr.Code)
			}
		}
	})
}

// This slice has no browser write path: every mutating method is refused and
// nothing on disk changes.
func TestModelsPage_HasNoWritePath(t *testing.T) {
	f := newModelsFx(t, netid.Identity{OperatorID: "alice", Role: netid.RoleAdmin, Authenticated: true, Resolved: true})
	seedModelsState(t, f)
	policyBefore, _ := os.ReadFile(filepath.Join(f.state(), "router-policy.yml"))
	overlayBefore, _ := os.ReadFile(filepath.Join(f.state(), "model-registry.yml"))
	paths := append([]string{"/api/models", "/api/models/enable", "/api/models/gpt-5.5"}, modelsEndpoints...)
	for _, ep := range paths {
		for _, m := range []string{http.MethodPut, http.MethodPost, http.MethodPatch, http.MethodDelete} {
			rr := f.do(m, ep, map[string]string{"Content-Type": "application/json", "X-CSRF-Token": "anything"})
			if rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 405 or 404", m, ep, rr.Code)
			}
		}
	}
	policyAfter, _ := os.ReadFile(filepath.Join(f.state(), "router-policy.yml"))
	overlayAfter, _ := os.ReadFile(filepath.Join(f.state(), "model-registry.yml"))
	if string(policyBefore) != string(policyAfter) || string(overlayBefore) != string(overlayAfter) {
		t.Error("a refused write changed a policy file")
	}
	if ov := f.overview(t); ov["writes_enabled"] != false {
		t.Errorf("writes_enabled = %v; this build has no browser writes", ov["writes_enabled"])
	}
}

func TestModelsPage_ResponseHeaders(t *testing.T) {
	f := newModelsFx(t, readerID)
	restore := consoleui.SetModelsExplainForTest(func(context.Context, dispatch.ExplainQuery) (router.RouteDecision, error) {
		return router.RouteDecision{Runtime: "claude", RuleID: "R0"}, nil
	})
	defer restore()
	consoleui.SetModelsHooksForTest(f.srv, consoleui.ModelsHooks{Probe: fixedProbe})
	for _, ep := range append(modelsEndpoints, "/api/models/explain?agent=nope%00", "/api/models/explain") {
		rr := f.get(ep)
		h := rr.Header()
		if h.Get("Cache-Control") != "no-store" || h.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(h.Get("Content-Type"), "application/json") {
			t.Errorf("GET %s (%d) headers = %v", ep, rr.Code, h)
		}
	}
}

// Sentinel fuzz: secret values in the environment and the state files, a project
// path in the ledger, and hostile query values must never come back in any
// response of any /api/models* or /api/router/policy endpoint.
func TestModelsPage_SentinelFuzz(t *testing.T) {
	f := newModelsFx(t, readerID)
	seedModelsState(t, f)
	restore := consoleui.SetModelsExplainForTest(func(_ context.Context, q dispatch.ExplainQuery) (router.RouteDecision, error) {
		switch q.Agent {
		case "boom":
			return router.RouteDecision{}, fmt.Errorf("open %s/agents/x: SENTINEL-TASK-TEXT %s", f.root, f.home)
		case "ghost":
			return router.RouteDecision{}, fmt.Errorf("agent %q not found in composed set under %s", q.Agent, f.root)
		}
		return router.RouteDecision{Runtime: "codex", ModelID: "gpt-5.6-terra", Provider: "openai", RuleID: "R1", Chain: []string{"codex", "claude"}, Reason: "pin"}, nil
	})
	defer restore()
	paths := []string{
		"/api/models", "/api/models/overview", "/api/router/policy",
		"/api/models/explain?agent=backend", "/api/models/explain?agent=boom", "/api/models/explain?agent=ghost",
		"/api/models/explain?agent=a&class=" + modelsSentinels[4],
		"/api/models/explain?agent=a&task_bytes=" + modelsSentinels[2], "/api/models/explain?agent=" + f.home,
		"/api/models/explain?agent=%00%1b[31m", "/api/models/explain",
	}
	forbidden := append([]string{f.home, f.ledger, f.workspace, f.root}, modelsSentinels...)
	for _, ep := range paths {
		rr := f.get(ep)
		body := rr.Body.String()
		for _, bad := range forbidden {
			// A response may only repeat an input the request itself carried, and
			// none of these endpoints does.
			if strings.Contains(body, bad) {
				t.Errorf("GET %s leaked %q:\n%s", ep, bad, body)
			}
		}
		if strings.Contains(body, "\x1b") {
			t.Errorf("GET %s carries a terminal escape", ep)
		}
	}
	// And the overview really did read the planted state, so the checks above bite.
	ov := f.overview(t)
	if len(ov["evals"].([]any)) == 0 || len(ov["budgets"].([]any)) == 0 {
		t.Fatalf("fixture not read: %v", ov)
	}
}

func TestModelsPage_DNSRebindingAndHostChecks(t *testing.T) {
	f := newModelsFx(t, readerID)
	full := f.srv.FullHandler()
	do := func(host string) int {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/models/overview", nil)
		req.Host = host
		req.Header.Set("Authorization", "Bearer "+f.tok)
		rr := httptest.NewRecorder()
		full.ServeHTTP(rr, req)
		return rr.Code
	}
	for _, host := range []string{"evil.example.com", "evil.example.com:7890", "127.0.0.1.evil.example.com:7890", "attacker.test:7890", "127.0.0.1:9999", "localhost.evil.com:7890", "0.0.0.0:7890", ""} {
		if code := do(host); code != http.StatusForbidden {
			t.Errorf("Host %q = %d, want 403", host, code)
		}
	}
	for _, host := range []string{"127.0.0.1:7890", "localhost:7890"} {
		if code := do(host); code != http.StatusOK {
			t.Errorf("Host %q = %d, want 200", host, code)
		}
	}
	// A rebinding page that guessed the token is still stopped by the Host check
	// for the other two endpoints.
	for _, ep := range modelsEndpoints {
		req := httptest.NewRequest(http.MethodGet, "http://evil.example.com:7890"+ep, nil)
		req.Header.Set("Authorization", "Bearer "+f.tok)
		rr := httptest.NewRecorder()
		full.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("rebinding GET %s = %d", ep, rr.Code)
		}
	}
}

func TestModelsPage_OverviewContent(t *testing.T) {
	f := newModelsFx(t, readerID)
	seedModelsState(t, f)
	consoleui.SetModelsHooksForTest(f.srv, consoleui.ModelsHooks{
		Probe: fixedProbe,
		Cooling: func(project, rt string) (bool, time.Duration) {
			return rt == "codex", 41 * time.Second
		},
	})
	ov := f.overview(t)

	provs := ov["providers"].([]any)
	if len(provs) != 3 {
		t.Fatalf("providers = %v", provs)
	}
	byH := map[string]map[string]any{}
	for _, p := range provs {
		m := p.(map[string]any)
		byH[m["harness"].(string)] = m
	}
	if byH["claude"]["signed_in"] != true || byH["codex"]["signed_in"] != false || byH["agy"]["installed"] != false {
		t.Errorf("providers = %v", byH)
	}
	if byH["codex"]["cooling"] != true || byH["codex"]["cooldown_seconds"].(float64) < 41 || byH["claude"]["cooling"] != false {
		t.Errorf("cooldown = %v", byH)
	}
	if !strings.Contains(byH["codex"]["hint"].(string), "OPENAI_API_KEY") || strings.Contains(byH["codex"]["hint"].(string), "SENTINEL") {
		t.Errorf("hint = %v", byH["codex"]["hint"])
	}

	enabled := map[string]bool{}
	for _, m := range ov["models"].([]any) {
		e := m.(map[string]any)
		if e["harness"] == "codex" {
			enabled[e["id"].(string)] = e["enabled"].(bool)
		}
	}
	if enabled["gpt-5.5"] || !enabled["gpt-5.6-terra"] {
		t.Errorf("the overlay's disable was not applied: %v", enabled)
	}
	var balanced map[string]any
	for _, a := range ov["aliases"].([]any) {
		if a.(map[string]any)["alias"] == "balanced" {
			balanced = a.(map[string]any)["by"].(map[string]any)
		}
	}
	if balanced["codex"] != "gpt-5.6-terra" || balanced["claude"] == nil {
		t.Errorf("balanced alias = %v", balanced)
	}

	rt := ov["router"].(map[string]any)
	if len(rt["sha"].(string)) != 64 || len(rt["rules"].([]any)) != 2 || len(rt["pins"].([]any)) != 1 || rt["hooks_endpoint"] != true {
		t.Errorf("router = %v", rt)
	}
	if got := f.get("/api/router/policy").Body.String(); !strings.Contains(got, rt["sha"].(string)) {
		t.Errorf("/api/router/policy disagrees with the overview: %s", got)
	}
	if ov["sensitive"].(map[string]any)["never_path_patterns"].(float64) == 0 {
		t.Errorf("sensitive = %v", ov["sensitive"])
	}

	var sup map[string]any
	for _, b := range ov["budgets"].([]any) {
		if b.(map[string]any)["agent"] == "supervisor" {
			sup = b.(map[string]any)
		}
	}
	if sup == nil || sup["spent_tokens"].(float64) != 1500 || sup["spent_usd"].(float64) != 1.25 || sup["limit_tokens"].(float64) == 0 {
		t.Errorf("supervisor budget = %v", sup)
	}

	evals := ov["evals"].([]any)
	if len(evals) != 2 {
		t.Fatalf("evals = %v; want the two with a clean run id (newest first)", evals)
	}
	if evals[0].(map[string]any)["run_id"] != "run-new" || evals[1].(map[string]any)["candidate_tier"] != "sonnet" {
		t.Errorf("evals = %v", evals)
	}
	old := evals[1].(map[string]any)
	rates, _ := old["tier_pass_rates"].(map[string]any)
	costs, _ := old["tier_mean_costs"].(map[string]any)
	if len(rates) != 2 || rates["haiku"] != 0.5 || rates["sonnet"] != 0.9 || len(costs) != 1 || costs["haiku"] != 0.01 {
		t.Errorf("tier maps must keep only identifier keys: rates=%v costs=%v", rates, costs)
	}
}

func TestModelsPage_ProbeResultsAreCached(t *testing.T) {
	f := newModelsFx(t, readerID)
	var calls atomic.Int32
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	consoleui.SetModelsHooksForTest(f.srv, consoleui.ModelsHooks{
		Probe: func(ctx context.Context, h string) auth.ProbeResult {
			calls.Add(1)
			if _, ok := ctx.Deadline(); !ok {
				t.Error("probe ran without a deadline")
			}
			return auth.ProbeResult{CLIPresent: true, Authed: true}
		},
		Now: func() time.Time { return now },
	})
	f.overview(t)
	f.overview(t)
	if calls.Load() != 3 {
		t.Fatalf("%d probes for two overviews, want 3 (one per harness, cached)", calls.Load())
	}
	now = now.Add(time.Minute)
	f.overview(t)
	if calls.Load() != 6 {
		t.Errorf("%d probes after the cache expired, want 6", calls.Load())
	}
}

func TestModelsPage_EvalReaderIsBounded(t *testing.T) {
	line := func(i int) string {
		return fmt.Sprintf(`{"type":"eval_run_finished","ts":"2026-10-01T00:00:00Z","run_id":"run-%03d","agent":"backend"}`+"\n", i)
	}
	pad := strings.Repeat("{\"type\":\"dispatch_started\",\"pad\":\""+strings.Repeat("x", 900)+"\"}\n", consoleui.ModelsEvalTailBytes/900+50)
	write := func(f modelsFx, body string) {
		t.Helper()
		if err := os.WriteFile(f.ledgerPath(), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("at most the newest few", func(t *testing.T) {
		f := newModelsFx(t, readerID)
		var b strings.Builder
		for i := 0; i < consoleui.ModelsMaxEvals+5; i++ {
			b.WriteString(line(i))
		}
		write(f, b.String())
		evals := f.overview(t)["evals"].([]any)
		if len(evals) != consoleui.ModelsMaxEvals {
			t.Fatalf("%d evals, want %d", len(evals), consoleui.ModelsMaxEvals)
		}
		if evals[0].(map[string]any)["run_id"] != fmt.Sprintf("run-%03d", consoleui.ModelsMaxEvals+4) {
			t.Errorf("newest = %v", evals[0])
		}
	})
	t.Run("only the tail of the log is read", func(t *testing.T) {
		f := newModelsFx(t, readerID)
		var b strings.Builder
		for i := 100; i < 120; i++ {
			b.WriteString(line(i)) // far before the window
		}
		b.WriteString(pad)
		for i := 0; i < 3; i++ {
			b.WriteString(line(i))
		}
		write(f, b.String())
		if evals := f.overview(t)["evals"].([]any); len(evals) != 3 {
			t.Errorf("%d evals, want the 3 in the tail window", len(evals))
		}
	})
	t.Run("a symlinked or missing log is no results", func(t *testing.T) {
		f := newModelsFx(t, readerID)
		if evals := f.overview(t)["evals"].([]any); len(evals) != 0 {
			t.Errorf("evals from no log: %v", evals)
		}
		if err := os.Symlink(filepath.Join(f.home, "elsewhere"), f.ledgerPath()); err != nil {
			t.Fatal(err)
		}
		if evals := f.overview(t)["evals"].([]any); len(evals) != 0 {
			t.Errorf("evals from a symlinked log: %v", evals)
		}
	})
}

func TestModelsPage_ExplainRoundTripAndRefusals(t *testing.T) {
	f := newModelsFx(t, readerID)
	seedModelsState(t, f)
	var got dispatch.ExplainQuery
	restore := consoleui.SetModelsExplainForTest(func(_ context.Context, q dispatch.ExplainQuery) (router.RouteDecision, error) {
		got = q
		switch q.Agent {
		case "ghost":
			return router.RouteDecision{}, errors.New(`agent "ghost" not found in composed set`)
		case "stuck":
			return router.RouteDecision{}, errors.New("no runtime can run")
		}
		return router.RouteDecision{Runtime: "codex", ModelID: "gpt-5.6-terra", Provider: "openai", RuleID: "R1",
			Chain: []string{"codex", "claude"}, Reason: "pin", PolicySHA: "abc", Skipped: []router.Skip{{Runtime: "agy", Cooling: true}}}, nil
	})
	defer restore()

	rr := f.get("/api/models/explain?agent=backend&class=chat&task_bytes=2048")
	if rr.Code != 200 {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	var x map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &x); err != nil {
		t.Fatal(err)
	}
	if x["runtime"] != "codex" || x["model"] != "gpt-5.6-terra" || x["rule"] != "R1" || x["agent"] != "backend" {
		t.Errorf("explain = %v", x)
	}
	if got.Agent != "backend" || got.Class != "chat" || got.TaskBytes != 2048 || got.Project != f.workspace || got.YakosRoot != f.root || got.ConversationID != "" || got.Task != "" {
		t.Errorf("query = %+v; a playground run must be a bare dry run in the workspace", got)
	}
	for q, want := range map[string]int{
		"agent=ghost":                       404,
		"agent=stuck":                       422,
		"agent=":                            400,
		"":                                  400,
		"agent=../etc/passwd":               400,
		"agent=a&class=bad%20class":         400,
		"agent=a&task_bytes=-1":             400,
		"agent=a&task_bytes=abc":            400,
		"agent=a&task_bytes=99999999999999": 400,
		"agent=" + strings.Repeat("a", 129): 400,
	} {
		if rr := f.get("/api/models/explain?" + q); rr.Code != want {
			t.Errorf("explain?%s = %d, want %d (%s)", q, rr.Code, want, rr.Body.String())
		}
	}
	if rr := f.get("/api/models/explain?agent=a&class=nonexistent"); rr.Code != 400 {
		t.Errorf("a class no rule or built-in names is refused: %d", rr.Code)
	}
	if rr := f.get("/api/models/explain?agent=backend&class=sensitive"); rr.Code != 200 || got.Class != "sensitive" {
		t.Errorf("class=sensitive must reach the dry run: %d class=%q %s", rr.Code, got.Class, rr.Body.String())
	}
}

func TestModelsPage_ExplainNeedsAWorkspace(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("POSIX paths")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	srv := consoleui.MustNew(t, consoleui.Config{Token: "t", KanbanBoardPath: t.TempDir() + "/k.md", KanbanProject: "x",
		MetricsProjectDir: t.TempDir(), PerfWorkDir: t.TempDir(), Bus: bus, WorkDir: t.TempDir()})
	h := injectIdentityMiddleware(readerID, srv.HandlerForTest())
	req := httptest.NewRequest(http.MethodGet, "/api/models/explain?agent=backend", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("no workspace = %d, want 503", rr.Code)
	}
}

func TestModelsPage_StaticAssetAndWiring(t *testing.T) {
	f := newModelsFx(t, readerID)
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7890/models.js", nil)
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req) // no token: the script is a static asset
	if rr.Code != 200 || !strings.HasPrefix(rr.Header().Get("Content-Type"), "application/javascript") || !strings.Contains(rr.Body.String(), "YakModels") {
		t.Errorf("/models.js = %d %q", rr.Code, rr.Header().Get("Content-Type"))
	}
	if rr.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Error("missing CORP header")
	}
	index, _ := os.ReadFile("dist/index.html")
	app, _ := os.ReadFile("dist/app.js")
	if !strings.Contains(string(index), `src="/models.js"`) || !strings.Contains(string(app), "id: 'models'") || !strings.Contains(string(app), `id="panel-models"`) {
		t.Error("the tab is not wired into index.html and app.js")
	}
}

// An untrusted router-policy.yml (world-writable, or a symlink) is read as no
// policy: the empty view, the fixed warning and no path or file text anywhere.
func TestModelsPage_UntrustedPolicyIsEmptyViewWithFixedWarning(t *testing.T) {
	const wantWarning = "router policy ignored: the file must be a regular file you own that is not group or world writable (chmod 600); no routing rules are applied"
	body := "x_secret: SENTINEL-POLICY-KEY\nhooks_endpoint: true\nrules:\n  - {match: {agent: backend}, action: {runtime: codex}}\n"
	cases := map[string]func(t *testing.T, f modelsFx){
		"world-writable": func(t *testing.T, f modelsFx) {
			f.writeState(t, "router-policy.yml", body)
			if err := os.Chmod(filepath.Join(f.state(), "router-policy.yml"), 0o666); err != nil { //nolint:gosec
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, f modelsFx) {
			target := filepath.Join(f.home, "elsewhere-policy.yml")
			if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(f.state(), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(f.state(), "router-policy.yml")); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			f := newModelsFx(t, readerID)
			plant(t, f)
			for _, path := range []string{"/api/router/policy", "/api/models/overview"} {
				rr := f.get(path)
				if rr.Code != 200 {
					t.Fatalf("%s: %d %s", path, rr.Code, rr.Body.String())
				}
				raw := rr.Body.String()
				for _, leak := range []string{f.home, f.state(), "router-policy.yml", "elsewhere-policy", "SENTINEL-POLICY-KEY", "0666"} {
					if strings.Contains(raw, leak) {
						t.Errorf("%s leaks %q: %s", path, leak, raw)
					}
				}
				var m map[string]any
				if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
					t.Fatal(err)
				}
				view := m
				if path == "/api/models/overview" {
					view = m["router"].(map[string]any)
				}
				if view["present"] != false || view["sha"] != "" || len(view["rules"].([]any)) != 0 || view["hooks_endpoint"] != false {
					t.Errorf("%s: want the empty view, got %v", path, view)
				}
				ws, _ := view["warnings"].([]any)
				if len(ws) != 1 || ws[0] != wantWarning {
					t.Errorf("%s: warnings = %v, want exactly the fixed text", path, ws)
				}
			}
		})
	}
}
