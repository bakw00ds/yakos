package dispatch

// account_test.go covers K-136 in the dispatch package: Account is the only
// writer of dispatch_started and dispatch_finished, every dispatch writes exactly
// one pair, the finished event carries the ledger fields, and the accounting
// rules hold: tokens are recorded for every run, dollars are spend only for api
// billing, and a subscription run's reported cost moves to api_equivalent_usd.
//
// Runs are real: Run is driven end to end against a fake runtime binary on PATH
// replaying a recorded stream, so the real adapter, parser and writer are in the
// loop. Everything happens in temp directories (isolatedLogDir); TestMain starts
// every test with no provider credentials in the environment (a test that wants an
// API-billed run calls apiKeyEnv).

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/runtime"
)

// ---- Account lifecycle --------------------------------------------------------

func TestAccount_StartThenFinishWritesOnePair(t *testing.T) {
	logDir := isolatedLogDir(t)
	req := Request{AgentName: "backend", Runtime: "claude", Project: "/p", Task: "do it", Surface: SurfaceREST}
	acct := newAccountAt(req, filepath.Join(logDir, "dispatch-log.ndjson"), fixedTime)

	if _, err := os.Stat(filepath.Join(logDir, "dispatch-log.ndjson")); err == nil {
		t.Fatal("creating an Account must not write anything")
	}
	acct.Start()
	acct.Start() // a second Start is a no-op
	acct.FinishAt(Result{ExitCode: 0, DurationS: 1.5}, fixedTime.Add(2*time.Second))

	events := readDispatchLog(t, logDir)
	if len(events) != 2 {
		t.Fatalf("want one started and one finished event, got %d: %v", len(events), events)
	}
	assertField(t, events[0], "type", "dispatch_started")
	assertField(t, events[1], "type", "dispatch_finished")
	assertField(t, events[1], "surface", "rest")
	if events[0]["ts"] != "2026-06-02T12:00:00Z" || events[1]["ts"] != "2026-06-02T12:00:02Z" {
		t.Errorf("timestamps = %v / %v", events[0]["ts"], events[1]["ts"])
	}
}

// A turn whose start is only remembered (the console's interactive turns) is
// finished without a Start: the pair is written together, started first and
// stamped with the begin time.
func TestAccount_FinishWithoutStartWritesTheStartedEventFirst(t *testing.T) {
	logDir := isolatedLogDir(t)
	acct := newAccountAt(Request{AgentName: "backend", Runtime: "claude", Project: "/p", Task: "t"},
		filepath.Join(logDir, "dispatch-log.ndjson"), fixedTime)
	acct.FinishAt(Result{ExitCode: 0}, fixedTime.Add(90*time.Second))

	events := readDispatchLog(t, logDir)
	if len(events) != 2 {
		t.Fatalf("want a pair, got %v", events)
	}
	assertField(t, events[0], "type", "dispatch_started")
	assertField(t, events[1], "type", "dispatch_finished")
	if events[0]["ts"] != "2026-06-02T12:00:00Z" || events[1]["ts"] != "2026-06-02T12:01:30Z" {
		t.Errorf("started must carry the begin time: %v / %v", events[0]["ts"], events[1]["ts"])
	}
	// An unset duration is measured from the begin time.
	if d, _ := events[1]["duration_s"].(float64); d != 90 {
		t.Errorf("duration_s = %v, want 90", events[1]["duration_s"])
	}
}

func TestAccount_FinishIsIdempotent(t *testing.T) {
	logDir := isolatedLogDir(t)
	acct := newAccountAt(Request{AgentName: "backend", Runtime: "claude", Project: "/p"},
		filepath.Join(logDir, "dispatch-log.ndjson"), fixedTime)
	acct.Start()
	acct.Finish(Result{ExitCode: 0})
	acct.Finish(Result{ExitCode: 1})
	acct.FinishAt(Result{ExitCode: 2}, fixedTime)
	events := readDispatchLog(t, logDir)
	if len(events) != 2 {
		t.Fatalf("a finished Account writes nothing more: got %d events", len(events))
	}
	if events[1]["exit_code"] != float64(0) {
		t.Errorf("the first Finish wins: %v", events[1]["exit_code"])
	}
}

// A dispatch that was never finished leaves nothing when it was never started.
func TestAccount_UnfinishedAndUnstartedLeavesNothing(t *testing.T) {
	logDir := isolatedLogDir(t)
	_ = newAccountAt(Request{AgentName: "backend"}, filepath.Join(logDir, "dispatch-log.ndjson"), fixedTime)
	if entries, _ := os.ReadDir(logDir); len(entries) != 0 {
		t.Fatalf("nothing may be written: %v", entries)
	}
}

// ---- the real Run, per runtime --------------------------------------------------

func runWith(t *testing.T, runtimeName string, mutate func(*Request)) (Result, map[string]interface{}, string) {
	t.Helper()
	logDir := isolatedLogDir(t)
	req := Request{AgentName: "unpinned", Task: "t", Project: t.TempDir(), YakosRoot: pinRoot(t), Runtime: runtimeName}
	if mutate != nil {
		mutate(&req)
	}
	_, res, err := Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := readDispatchLog(t, logDir)
	if len(events) != 2 {
		t.Fatalf("one dispatch must write exactly one pair, got %d events: %v", len(events), events)
	}
	assertField(t, events[0], "type", "dispatch_started")
	assertField(t, events[1], "type", "dispatch_finished")
	return res, events[1], logDir
}

func usageOf(t *testing.T, ev map[string]interface{}) map[string]interface{} {
	t.Helper()
	u, ok := ev["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("event has no usage object: %v", ev)
	}
	return u
}

func TestRun_Ledger_ClaudeSubscription(t *testing.T) {
	fakeRuntimeBin(t, "claude", "claude-stream-json-oneshot-SYNTHETIC.ndjson", "", 0)
	res, ev, logDir := runWith(t, "claude", func(r *Request) { r.Surface = SurfaceCLI })

	assertField(t, ev, "provider", "anthropic")
	assertField(t, ev, "model_id", "claude-sonnet-4-5-20250929")
	assertField(t, ev, "billing", "subscription")
	assertField(t, ev, "cost_source", "harness")
	assertField(t, ev, "surface", "cli")
	assertField(t, ev, "native_session_id", "7f3c2a9e-1b4d-4c8e-9a10-0d5e6f7a8b9c")
	// The harness's dollar figure is an API-equivalent, never spend.
	if ev["api_equivalent_usd"] != 0.0123 {
		t.Errorf("api_equivalent_usd = %v, want 0.0123", ev["api_equivalent_usd"])
	}
	u := usageOf(t, ev)
	if u["total_cost_usd"] != float64(0) {
		t.Errorf("usage.total_cost_usd = %v: a subscription run's figure must not be left where readers sum spend", u["total_cost_usd"])
	}
	if u["input_tokens"] != float64(120) || u["output_tokens"] != float64(45) || u["cache_read"] != float64(9000) || u["cache_creation"] != float64(3000) {
		t.Errorf("tokens are recorded for every run: %v", u)
	}
	// The caller's Result is untouched: it still carries what the harness reported.
	if res.Usage == nil || res.Usage.TotalCostUSD != 0.0123 {
		t.Errorf("Result.Usage must keep the harness figure, got %+v", res.Usage)
	}
	// Read back with the canonical reader: tokens yes, spend no.
	got := lastFinished(t, logDir)
	if got.SpendUSD() != 0 || got.Tokens().Total() != 120+45+9000+3000 || got.APIEquivalentUSD != 0.0123 {
		t.Errorf("reader view: spend=%v tokens=%+v api_equivalent=%v", got.SpendUSD(), got.Tokens(), got.APIEquivalentUSD)
	}
}

func TestRun_Ledger_ClaudeAPIBilling(t *testing.T) {
	apiKeyEnv(t, "claude")
	fakeRuntimeBin(t, "claude", "claude-stream-json-oneshot-SYNTHETIC.ndjson", "", 0)
	_, ev, logDir := runWith(t, "claude", nil)

	assertField(t, ev, "billing", "api")
	assertField(t, ev, "cost_source", "harness")
	if _, has := ev["api_equivalent_usd"]; has {
		t.Errorf("an api run's figure is spend, not an API-equivalent: %v", ev["api_equivalent_usd"])
	}
	if u := usageOf(t, ev); u["total_cost_usd"] != 0.0123 {
		t.Errorf("usage.total_cost_usd = %v, want the spend 0.0123", u["total_cost_usd"])
	}
	if got := lastFinished(t, logDir); got.SpendUSD() != 0.0123 {
		t.Errorf("an api row is spend: %v", got.SpendUSD())
	}
}

func TestRun_Ledger_CodexAndAgyReportTokensAndNoDollars(t *testing.T) {
	fakeRuntimeBin(t, "codex", "codex-exec-json-0.154.0-ok.ndjson", "", 0)
	_, ev, _ := runWith(t, "codex", nil)
	assertField(t, ev, "provider", "openai")
	assertField(t, ev, "billing", "subscription")
	assertField(t, ev, "native_session_id", "01a10c3a-338e-71f3-a8b8-6aef08430c40")
	u := usageOf(t, ev)
	if u["input_tokens"] != float64(7707) || u["cache_read"] != float64(7424) || u["output_tokens"] != float64(5) {
		t.Errorf("codex tokens: %v", u)
	}
	for _, k := range []string{"cost_source", "api_equivalent_usd"} {
		if _, has := ev[k]; has {
			t.Errorf("codex reports no dollar figure, so %s must be absent: %v", k, ev[k])
		}
	}
	if u["total_cost_usd"] != float64(0) {
		t.Errorf("no dollars for codex: %v", u["total_cost_usd"])
	}

	fakeRuntimeBin(t, "agy", "agy-stream-json-1.2.17-ok.ndjson", "", 0)
	_, ev, _ = runWith(t, "agy", nil)
	assertField(t, ev, "provider", "google")
	assertField(t, ev, "billing", "subscription")
	if u := usageOf(t, ev); u["input_tokens"] != float64(12863) {
		t.Errorf("agy tokens: %v", u)
	}
	if _, has := ev["api_equivalent_usd"]; has {
		t.Error("agy reports no dollar figure")
	}
}

// Billing is read from the credentials the harness inherits: an API key for its
// provider means api, none means subscription, and a key for another provider
// changes nothing.
func TestRun_Ledger_BillingFollowsTheHarnessCredentials(t *testing.T) {
	cases := []struct {
		runtime, env, want string
	}{
		{"codex", "", "subscription"},
		{"codex", "OPENAI_API_KEY", "api"},
		{"codex", "ANTHROPIC_API_KEY", "subscription"}, // another provider's key is not codex's
		{"agy", "GEMINI_API_KEY", "api"},
		{"agy", "OPENAI_API_KEY", "subscription"},
	}
	for _, c := range cases {
		t.Run(c.runtime+"/"+c.env, func(t *testing.T) {
			if c.env != "" {
				t.Setenv(c.env, "fake-key-for-billing-detection")
			}
			fixture := "codex-exec-json-0.154.0-ok.ndjson"
			if c.runtime == "agy" {
				fixture = "agy-stream-json-1.2.17-ok.ndjson"
			}
			fakeRuntimeBin(t, c.runtime, fixture, "", 0)
			_, ev, _ := runWith(t, c.runtime, nil)
			assertField(t, ev, "billing", c.want)
		})
	}
}

// A plugin runtime has no known billing: its row reads like a pre-K-136 row.
func TestBuildFinished_UnknownRuntimeHasNoBilling(t *testing.T) {
	ev := buildFinished(Request{AgentName: "a", Runtime: "myplugin", Project: "/p"},
		Result{Usage: &cost.Usage{InputTokens: 5, TotalCostUSD: 1.5}}, fixedTime)
	if ev.Billing != "" || ev.CostSource != "harness" || ev.APIEquivalentUSD != 0 || ev.Usage.TotalCostUSD != 1.5 {
		t.Fatalf("an unknown runtime keeps its figure as spend: %+v", ev)
	}
}

// No routing fields until the router lands: they are absent, not empty.
func TestRun_Ledger_RoutingFieldsAreAbsentUntilTheRouterLands(t *testing.T) {
	fakeRuntimeBin(t, "claude", "claude-stream-json-oneshot-SYNTHETIC.ndjson", "", 0)
	_, ev, _ := runWith(t, "claude", nil)
	for _, k := range []string{"route_rule", "route_reason", "route_class", "policy_sha"} {
		if _, has := ev[k]; has {
			t.Errorf("%s must be absent: %v", k, ev[k])
		}
	}
	// A request that carries them has them written (the router's hand-off).
	fakeRuntimeBin(t, "claude", "claude-stream-json-oneshot-SYNTHETIC.ndjson", "", 0)
	_, ev, _ = runWith(t, "claude", func(r *Request) {
		r.RouteRule, r.RouteReason, r.RouteClass, r.PolicySHA = "R3", "class chat matched", "chat", "ab12cd34"
	})
	assertField(t, ev, "route_rule", "R3")
	assertField(t, ev, "route_reason", "class chat matched")
	assertField(t, ev, "route_class", "chat")
	assertField(t, ev, "policy_sha", "ab12cd34")
}

// ---- hygiene ---------------------------------------------------------------------

// Billing detection reads whether a credential is present, never what it is:
// no credential value, and no other environment value, may reach any event.
func TestAccount_NoCredentialMaterialInEvents(t *testing.T) {
	secrets := map[string]string{
		"ANTHROPIC_API_KEY":     "sk-ant-api03-SENTINEL-KEY-MATERIAL-0001",
		"ANTHROPIC_AUTH_TOKEN":  "sk-ant-oat01-SENTINEL-OAUTH-TOKEN-0002",
		"OPENAI_API_KEY":        "sk-proj-SENTINEL-OPENAI-0003",
		"GEMINI_API_KEY":        "AIzaSENTINELGEMINI0004",
		"AWS_SECRET_ACCESS_KEY": "SENTINEL-AWS-SECRET-0005",
		"GH_TOKEN":              "ghp_SENTINELGITHUBTOKEN0006",
	}
	for k, v := range secrets {
		t.Setenv(k, v)
	}
	fakeRuntimeBin(t, "claude", "claude-stream-json-oneshot-SYNTHETIC.ndjson", "", 0)
	_, ev, logDir := runWith(t, "claude", func(r *Request) { r.Surface = SurfaceMCP })
	assertField(t, ev, "billing", "api") // the key was seen...

	fakeRuntimeBin(t, "codex", "codex-exec-json-0.154.0-ok.ndjson", "", 0)
	runKeepingTheLog(t, "codex")
	fakeRuntimeBin(t, "agy", "agy-stream-json-1.2.17-ok.ndjson", "", 0)
	runKeepingTheLog(t, "agy")

	data, err := os.ReadFile(filepath.Join(logDir, "dispatch-log.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range secrets {
		if strings.Contains(string(data), v) || strings.Contains(string(data), strings.ToLower(v)) {
			t.Errorf("the value of %s reached the dispatch log", k)
		}
	}
	// ...and so was the fact: nothing but fixed names and numbers came from it.
	if strings.Contains(string(data), "SENTINEL") {
		t.Errorf("sentinel text in the log:\n%s", data)
	}
}

// What a harness reports about itself is bounded before it is logged.
func TestBuildFinished_DropsOrBoundsHostileValues(t *testing.T) {
	long := strings.Repeat("a", 300)
	req := Request{
		AgentName: "a", Runtime: "claude", Project: "/p",
		Surface:     "Evil Surface\n",
		RouteRule:   "R" + strings.Repeat("x", 500),
		RouteReason: "line one\nline two\x00\x1b[31m red",
		RouteClass:  "not an ident!",
		PolicySHA:   "not-hex",
	}
	res := Result{
		Runtime: "claude", Provider: "anthropic\nforged: yes",
		ModelID: "model\nid", ModelResolved: "sonnet",
		SessionID: "-rf; --flag",
		Usage:     &cost.Usage{InputTokens: 1, TotalCostUSD: math.Inf(1)},
	}
	ev := buildFinished(req, res, fixedTime)
	if ev.Provider != "anthropic" { // the hostile name is dropped, the runtime's own provider stands
		t.Errorf("provider = %q", ev.Provider)
	}
	if ev.ModelID != "sonnet" {
		t.Errorf("an unusable model id falls back to the resolved model: %q", ev.ModelID)
	}
	if ev.NativeSessionID != "" || ev.Surface != "" || ev.RouteClass != "" || ev.PolicySHA != "" {
		t.Errorf("hostile identifiers must be dropped: %+v", ev)
	}
	if len(ev.RouteRule) > 128 || strings.ContainsAny(ev.RouteReason, "\n\x00\x1b") {
		t.Errorf("free text is cleaned and bounded: rule len %d, reason %q", len(ev.RouteRule), ev.RouteReason)
	}
	// A non-finite dollar figure is neither spend nor an API-equivalent.
	if ev.APIEquivalentUSD != 0 || ev.CostSource != "" {
		t.Errorf("an infinite cost must not be recorded: %+v", ev)
	}
	if got := logIdent(long, 128); got != "" {
		t.Errorf("an over-long identifier is dropped: %q", got)
	}
	if got := logText("héllo wörld", 3); !strings.HasPrefix("héllo wörld", got) || len(got) > 3 {
		t.Errorf("logText must cut on a rune boundary: %q", got)
	}
}

// ---- the event schema, over mixed legacy and new lines ----------------------------

// allowedFinishedKeys is every key a dispatch_finished event may carry. Adding a
// key to the writer without adding it here (and to cost.Event for the ledger
// ones) is a schema change this test makes you notice.
var allowedFinishedKeys = []string{
	"type", "ts", "agent", "runtime", "project", "exit_code", "duration_s", "output_bytes", "task_bytes",
	"est_input_tokens", "est_output_tokens", "model", "model_chosen_by", "model_resolved", "eval_run_id",
	"stderr_tail", "stderr_truncated", "usage", "operator_id", "conversation_id", "session_id",
	"runtime_chosen_by", "fallback_from",
	// K-136 ledger fields:
	"provider", "model_id", "billing", "cost_source", "api_equivalent_usd", "route_rule", "route_reason",
	"route_class", "policy_sha", "surface", "native_session_id",
}

var legacyRequiredKeys = []string{
	"type", "ts", "agent", "runtime", "project", "exit_code", "duration_s", "output_bytes", "task_bytes",
	"est_input_tokens", "est_output_tokens", "model_chosen_by", "model_resolved", "eval_run_id", "stderr_tail", "stderr_truncated",
}

var ledgerKeys = []string{
	"provider", "model_id", "billing", "cost_source", "api_equivalent_usd", "route_rule", "route_reason",
	"fallback_from", "route_class", "policy_sha", "surface", "native_session_id",
}

func TestFinishedEventSchema_MixedLegacyAndNewLines(t *testing.T) {
	logDir := isolatedLogDir(t)
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	// A bash-written row, the oldest shape, then rows from this writer.
	legacy := legacyRow("legacy-claude", "claude", `{"input_tokens":120,"output_tokens":45,"cache_read":9000,"cache_creation":3000,"duration_ms":4321,"total_cost_usd":0.0123}`)
	if err := os.WriteFile(logPath, []byte(legacy+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeRuntimeBin(t, "claude", "claude-stream-json-oneshot-SYNTHETIC.ndjson", "", 0)
	runKeepingTheLog(t, "claude")
	fakeRuntimeBin(t, "codex", "codex-exec-json-0.154.0-ok.ndjson", "", 0)
	runKeepingTheLog(t, "codex")
	fakeRuntimeBin(t, "agy", "", "just prose\n", 0)
	runKeepingTheLog(t, "agy")

	allowed := map[string]bool{}
	for _, k := range allowedFinishedKeys {
		allowed[k] = true
	}
	events := readDispatchLog(t, logDir)
	finished := 0
	for i, ev := range events {
		if ev["type"] != "dispatch_finished" {
			continue
		}
		finished++
		for k := range ev {
			if !allowed[k] {
				t.Errorf("event %d carries key %q, which is not in the schema", i, k)
			}
		}
		for _, k := range legacyRequiredKeys {
			if _, ok := ev[k]; !ok {
				t.Errorf("event %d lacks the legacy key %q", i, k)
			}
		}
		isLegacy := i == 0
		for _, k := range ledgerKeys {
			if _, has := ev[k]; has && isLegacy {
				t.Errorf("the bash-written row must not have the ledger key %q", k)
			}
		}
		if !isLegacy {
			for _, k := range []string{"provider", "billing"} {
				if _, has := ev[k]; !has {
					t.Errorf("event %d (a Go row) lacks the ledger key %q", i, k)
				}
			}
		}
	}
	if finished != 4 {
		t.Fatalf("want 4 finished rows (1 legacy, 3 new), got %d", finished)
	}

	// Every ledger key the writer can emit is a field of the canonical reader, so a
	// reader built on cost.Event sees all of them.
	tags := map[string]bool{}
	rt := reflect.TypeOf(cost.Event{})
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		tags[tag] = true
	}
	for _, k := range ledgerKeys {
		if !tags[k] {
			t.Errorf("cost.Event has no field for the ledger key %q", k)
		}
	}

	// All four rows read together through the real reader, legacy and new, with
	// the spend rule applied per row.
	files, _ := cost.LogFiles(logDir)
	var rows []cost.Event
	for ev := range cost.StreamFiles(files, "") {
		rows = append(rows, ev)
	}
	if len(rows) != 4 {
		t.Fatalf("reader saw %d rows", len(rows))
	}
	var spend float64
	var tokens int64
	for _, r := range rows {
		spend += r.SpendUSD()
		tokens += r.Tokens().Total()
	}
	if spend != 0.0123 { // only the legacy row; the Go claude row was a subscription run
		t.Errorf("total spend = %v, want only the legacy row's 0.0123", spend)
	}
	if want := int64(120+45+9000+3000) * 2; tokens < want { // claude twice (bash + go), plus codex
		t.Errorf("total tokens = %d, want at least %d", tokens, want)
	}
}

// ---- Account is the only writer ----------------------------------------------------

// TestAccount_IsTheOnlyWriterOfDispatchEvents is the tripwire for K-136's central
// rule: no code outside account.go builds or writes a dispatch_started or
// dispatch_finished event for the dispatch log. It reads the source, so a new
// writer fails here and has to be sent through Account.
func TestAccount_IsTheOnlyWriterOfDispatchEvents(t *testing.T) {
	root := filepath.Join("..", "..") // cli-go
	// The workflow engine keeps a per-run bookkeeping file (node-dispatch.ndjson,
	// for resume and orphan detection) under the same two type names. It is not
	// the dispatch log; the node's real dispatch is logged by Account through
	// Service.Run.
	eventTypeAllowed := map[string]string{
		filepath.Join("internal", "dispatch", "account.go"): "the ledger",
		filepath.Join("internal", "workflow", "engine.go"):  "per-run node bookkeeping, not the dispatch log",
	}
	var violations []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == "testdata" || n == "node_modules" || n == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		file, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return nil // a file that does not parse is the build's problem
		}
		inDispatch := strings.HasPrefix(rel, filepath.Join("internal", "dispatch")+string(filepath.Separator))
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.KeyValueExpr:
				// A struct literal that sets Type to a dispatch event type.
				key, ok1 := x.Key.(*ast.Ident)
				val, ok2 := x.Value.(*ast.BasicLit)
				if ok1 && ok2 && key.Name == "Type" && (val.Value == `"dispatch_started"` || val.Value == `"dispatch_finished"`) {
					if _, ok := eventTypeAllowed[rel]; !ok {
						violations = append(violations, rel+": builds a "+val.Value+" event")
					}
				}
			case *ast.CallExpr:
				id, ok := x.Fun.(*ast.Ident)
				if !ok || !inDispatch {
					return true
				}
				switch id.Name {
				case "writeStarted", "writeFinished":
					if filepath.Base(rel) != "account.go" {
						violations = append(violations, rel+": calls "+id.Name+" (only Account may)")
					}
				case "appendEvent":
					if b := filepath.Base(rel); b != "account.go" && b != "events.go" {
						violations = append(violations, rel+": calls appendEvent")
					}
				}
			case *ast.FuncDecl:
				if inDispatch && (x.Name.Name == "writeStarted" || x.Name.Name == "writeFinished") && filepath.Base(rel) != "account.go" {
					violations = append(violations, rel+": declares "+x.Name.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// ---- Service plumbing --------------------------------------------------------------

// Every transport stamps its own surface on Params; Service.Run carries it onto the
// Request that Run and then Account see.
func TestService_RunCarriesTheSurface(t *testing.T) {
	var got []string
	orig := runFn
	runFn = func(_ context.Context, req Request) ([]byte, Result, error) {
		got = append(got, req.Surface)
		return nil, Result{}, nil
	}
	defer func() { runFn = orig }()

	svc := NewService(ServiceConfig{YakosRoot: t.TempDir(), WorkspaceRoot: t.TempDir(), OperatorID: "op"})
	for _, s := range []string{SurfaceREST, SurfaceJSONRPC, SurfaceGRPC, SurfaceFlows, ""} {
		if _, _, err := svc.Run(context.Background(), Params{Agent: "a", Task: "t", Surface: s}); err != nil {
			t.Fatal(err)
		}
	}
	// MCPParams is the only way to build MCP-originated Params, and it stamps the surface.
	if _, _, err := svc.Run(context.Background(), MCPParams(Params{Agent: "a", Task: "t", Surface: SurfaceREST})); err != nil {
		t.Fatal(err)
	}
	want := []string{"rest", "jsonrpc", "grpc", "flows", "", "mcp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("surfaces seen by Run = %v, want %v", got, want)
	}
}

// ---- budget pre-flight on both paths -------------------------------------------------

func writeFinishedLine(t *testing.T, dir, line string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, "dispatch-log.ndjson"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// RunStream is refused for an agent in hard_stop exactly like Run: before any
// process is forked and before any event is written (K-136 closed the gap that let
// a streamed chat turn spend past a hard stop).
func TestRunStream_RefusesAnAgentInHardStop(t *testing.T) {
	state := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "p")
	if err := budget.SetLimit(state, "chat-agent", 5, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"dispatch_finished","ts":"` + time.Now().UTC().Format(time.RFC3339) + `","agent":"chat-agent","usage":{"total_cost_usd":5.5}}`
	writeFinishedLine(t, state, line)
	before, _ := os.ReadFile(filepath.Join(state, "dispatch-log.ndjson"))

	svc := NewService(ServiceConfig{YakosRoot: yakosRoot, WorkspaceRoot: state, OperatorID: "op"})
	ran := false
	withStreamRunFn(func(context.Context, Request, runtime.Adapter, runtime.ChatDispatchRequest, func(StreamChunk)) (Result, error) {
		ran = true
		return Result{}, nil
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{Agent: "chat-agent", Task: "t", Project: state}, func(StreamChunk) {})
		if !budget.IsRefused(err) {
			t.Fatalf("want a budget refusal, got %v", err)
		}
	})
	if ran {
		t.Fatal("a refused streamed dispatch must not execute")
	}
	after, _ := os.ReadFile(filepath.Join(state, "dispatch-log.ndjson"))
	if string(after) != string(before) {
		t.Fatalf("a refused dispatch must not touch the log:\n%s", after)
	}
}

// The same hard stop, reached through TOKENS on a subscription agent: a
// subscription run costs no dollars, so only a token limit can stop it.
func TestRunAndRunStream_RefuseWhenTheTokenLimitIsReached(t *testing.T) {
	state := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "p")
	if err := budget.SetTokenLimit(state, "chat-agent", 1000, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"dispatch_finished","ts":"` + time.Now().UTC().Format(time.RFC3339) +
		`","agent":"chat-agent","billing":"subscription","usage":{"input_tokens":600,"output_tokens":300,"cache_read":100,"total_cost_usd":0}}`
	writeFinishedLine(t, state, line)

	_, _, err := Run(context.Background(), Request{AgentName: "chat-agent", Task: "t", Project: t.TempDir(), YakosRoot: yakosRoot})
	if !budget.IsRefused(err) || !strings.Contains(err.Error(), "tokens") {
		t.Fatalf("Run: want a token refusal, got %v", err)
	}
	svc := NewService(ServiceConfig{YakosRoot: yakosRoot, WorkspaceRoot: state, OperatorID: "op"})
	withStreamRunFn(func(context.Context, Request, runtime.Adapter, runtime.ChatDispatchRequest, func(StreamChunk)) (Result, error) {
		t.Fatal("a refused streamed dispatch must not execute")
		return Result{}, nil
	}, func() {
		if _, err := svc.RunStream(context.Background(), Params{Agent: "chat-agent", Task: "t", Project: state}, func(StreamChunk) {}); !budget.IsRefused(err) {
			t.Fatalf("RunStream: want a token refusal, got %v", err)
		}
	})
}

// A subscription agent with only a DOLLAR limit is never stopped by it, however
// many subscription runs it makes: those runs cost no dollars.
func TestDollarLimitDoesNotStopASubscriptionAgent(t *testing.T) {
	state := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "p")
	if err := budget.SetLimit(state, "chat-agent", 1, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		writeFinishedLine(t, state, `{"type":"dispatch_finished","ts":"`+time.Now().UTC().Format(time.RFC3339)+
			`","agent":"chat-agent","billing":"subscription","usage":{"input_tokens":9,"total_cost_usd":0}}`)
	}
	st, err := budget.Enforce("chat-agent", budget.Options{StateDir: state})
	if err != nil || st.State != budget.StateOK || st.SpentUSD != 0 {
		t.Fatalf("subscription runs are not dollar spend: %+v, %v", st, err)
	}
	_ = yakosRoot
}

// ---- RunStream's ledger -------------------------------------------------------------

func streamLinesWithModelAndSession() []string {
	return []string{
		`{"type":"system","subtype":"init","session_id":"ses_FIXTURE_0001","model":"claude-sonnet-4-5-20250929","cwd":"/p"}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`,
		`{"type":"result","subtype":"success","result":"hello","is_error":false,"duration_ms":1200,"session_id":"ses_FIXTURE_0001","total_cost_usd":0.5,"usage":{"input_tokens":42,"output_tokens":3,"cache_read_input_tokens":900,"cache_creation_input_tokens":60}}`,
	}
}

func runStreamOnce(t *testing.T, surface string) (map[string]interface{}, cost.Event) {
	t.Helper()
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "p")
	svc := NewService(ServiceConfig{YakosRoot: yakosRoot, WorkspaceRoot: logDir, OperatorID: "op"})
	fake := &fakeClaudeStreamAdapter{lines: streamLinesWithModelAndSession()}
	withStreamRunFn(func(ctx context.Context, req Request, _ runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return execWithStreaming(ctx, req, fake, chatReq, onChunk)
	}, func() {
		if _, err := svc.RunStream(context.Background(), Params{Agent: "chat-agent", Task: "t", Project: logDir, Surface: surface}, func(StreamChunk) {}); err != nil {
			t.Fatalf("RunStream: %v", err)
		}
	})
	events := readDispatchLog(t, logDir)
	if len(events) != 2 {
		t.Fatalf("one streamed dispatch writes one pair, got %d events", len(events))
	}
	return events[1], lastFinished(t, logDir)
}

func TestRunStream_Ledger_SubscriptionTurn(t *testing.T) {
	ev, row := runStreamOnce(t, SurfaceConsoleChat)
	assertField(t, ev, "surface", "console-chat")
	assertField(t, ev, "provider", "anthropic")
	assertField(t, ev, "model_id", "claude-sonnet-4-5-20250929") // the concrete id from the stream, not the alias
	assertField(t, ev, "billing", "subscription")
	assertField(t, ev, "native_session_id", "ses_FIXTURE_0001")
	assertField(t, ev, "cost_source", "harness")
	if ev["api_equivalent_usd"] != 0.5 {
		t.Errorf("api_equivalent_usd = %v", ev["api_equivalent_usd"])
	}
	// Cache tokens are reported on the streamed path too: they are most of a claude turn.
	if tok := row.Tokens(); tok.Input != 42 || tok.Output != 3 || tok.CacheRead != 900 || tok.CacheCreation != 60 {
		t.Errorf("streamed tokens = %+v", tok)
	}
	if row.SpendUSD() != 0 {
		t.Errorf("a subscription turn is not spend: %v", row.SpendUSD())
	}
}

func TestRunStream_Ledger_APITurnCountsItsDollars(t *testing.T) {
	apiKeyEnv(t, "claude")
	ev, row := runStreamOnce(t, SurfaceConsoleChat)
	assertField(t, ev, "billing", "api")
	if row.SpendUSD() != 0.5 {
		t.Errorf("an api streamed turn is spend: %v", row.SpendUSD())
	}
}

// ---- helpers --------------------------------------------------------------------------

func lastFinished(t *testing.T, logDir string) cost.Event {
	t.Helper()
	files, err := cost.LogFiles(logDir)
	if err != nil {
		t.Fatal(err)
	}
	var last cost.Event
	n := 0
	for ev := range cost.StreamFiles(files, "") {
		last = ev
		n++
	}
	if n == 0 {
		t.Fatal("no dispatch_finished rows in the log")
	}
	return last
}

// ---- every transport stamps its surface --------------------------------------------

// TestEveryTransportStampsASurface keeps the surface field honest as transports are
// added: every dispatch.Params or dispatch.Request literal outside the dispatch
// package and the tests must set Surface, or its events carry none. (The MCP tool
// builds its Params inside MCPParams, which stamps the surface itself; the Flows
// handler's literal feeds a test-only run function and never reaches Account.)
func TestEveryTransportStampsASurface(t *testing.T) {
	root := filepath.Join("..", "..") // cli-go
	exempt := map[string]string{
		filepath.Join("internal", "mcpserver", "tools.go"):         "wrapped in dispatch.MCPParams, which stamps the MCP surface",
		filepath.Join("internal", "consoleui", "flows_handler.go"): "builds Params for a test-only run function",
	}
	var missing []string
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == "testdata" || n == "node_modules" || n == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if strings.HasPrefix(rel, filepath.Join("internal", "dispatch")+string(filepath.Separator)) {
			return nil
		}
		file, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || (pkg.Name != "dispatch" && pkg.Name != "internaldispatch") || (sel.Sel.Name != "Params" && sel.Sel.Name != "Request") {
				return true
			}
			// The public pkg/dispatch has its own Request type, with no Surface.
			if rel == filepath.Join("pkg", "dispatch", "dispatch.go") && pkg.Name == "dispatch" {
				return true
			}
			if _, ok := exempt[rel]; ok {
				return true
			}
			checked++
			for _, el := range lit.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Surface" {
						return true
					}
				}
			}
			missing = append(missing, rel)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 6 {
		t.Fatalf("the scan found only %d transport literals; it has stopped finding them", checked)
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("%s builds a dispatch Params/Request without a Surface: its events would carry no surface", m)
	}
}
