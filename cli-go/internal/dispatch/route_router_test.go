package dispatch

// Tests for the router at the routeDispatch chokepoint (K-139 slice a): rule
// ids, pins, sticky, cooldown, project tightening, the no-policy differential and
// the ledger row. They drive the real routing step and the real Run with fake
// CLIs on PATH, as agent_resolution_test.go does.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/router"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
	rt "github.com/bakw00ds/yakos/internal/runtime"
)

// setPolicy installs body as the router policy (a 0600 file in a temp state dir)
// and returns the directory. It also resets the cooldown and sticky tables.
func setPolicy(t *testing.T, body string) string {
	t.Helper()
	resetRouterState(t)
	dir := t.TempDir()
	p := routerpolicy.Path(dir)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	routerPolicyDir = func() string { return dir }
	return dir
}

// pinOf reads the pin the current policy file and project would see.
func pinOf(project, conversation, agent string) (router.Pin, bool) {
	return routerSticky.Get(conversation, agent, project, routerpolicy.FileSHA(routerPolicyDir()))
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

// tablePolicy has one rule per id the dispatch layer can exercise. R5 lists a
// tag, which no v1 caller supplies, so it must never match here.
const tablePolicy = `
rules:
  - match: {class: chat}
    action: {runtime: codex}
  - match: {domain: code-review}
    action: {runtime: codex, model: gpt-5.5, fallbacks: [claude]}
  - match: {agent: bare}
    action: {runtime: agy, model: balanced}
  - match: {task_bytes_gt: 1000}
    action: {model: haiku}
  - match: {tags: [bulk]}
    action: {runtime: agy}
  - match: {agent: gpt-claude}
    action: {fallbacks: [codex]}
`

func TestRoute_RuleIDsTable(t *testing.T) {
	captureRouteLog(t)
	root := routingRoot(t)
	project := projectWithYML(t, "")
	cases := []struct {
		name      string
		agent     string
		mut       func(*routeInput)
		probeFail string // runtime whose probe fails
		wantRule  string
		wantRT    string
		wantModel string
		wantChain string
	}{
		{"R1 class", "plain", func(in *routeInput) { in.Class = "chat" }, "", "R1", "codex", "", "codex"},
		{"R2 domain with model and fallbacks", "reviewer", nil, "", "R2", "codex", "gpt-5.5", "codex claude"},
		{"R2 falls back inside its own list", "reviewer", nil, "codex", "R2", "claude", "sonnet", "codex claude"},
		{"R3 agent with an alias model", "bare", nil, "", "R3", "agy", "agy-balanced-x", "agy"},
		{"R4 size sets a model on the default runtime", "gpt-claude", func(in *routeInput) { in.TaskBytes = 1001 }, "", "R4", "claude", "haiku", "claude"},
		{"R4 equal size does not match", "plain", func(in *routeInput) { in.TaskBytes = 1000 }, "", "R0", "claude", "sonnet", "claude"},
		{"R5 tags never match in v1", "plain", nil, "", "R0", "claude", "sonnet", "claude"},
		{"R6 fallbacks only", "gpt-claude", nil, "claude", "R6", "codex", "gpt-5", "claude codex"},
		{"no rule matches", "plain", nil, "", "R0", "claude", "sonnet", "claude"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setPolicy(t, tablePolicy)
			withProbe(t, func(name string) probeResult {
				if name == c.probeFail {
					return probeResult{Reason: "not signed in"}
				}
				return probeResult{OK: true}
			})
			got, err := route(t, root, project, c.agent, c.mut)
			if err != nil {
				t.Fatal(err)
			}
			d := got.Decision
			if d.RuleID != c.wantRule || d.Runtime != c.wantRT || d.ModelID != c.wantModel || strings.Join(d.Chain, " ") != c.wantChain {
				t.Errorf("rule %q runtime %q model %q chain %v, want %q %q %q [%s] (reason: %s)",
					d.RuleID, d.Runtime, d.ModelID, d.Chain, c.wantRule, c.wantRT, c.wantModel, c.wantChain, d.Reason)
			}
			if got.Runtime != d.Runtime || got.Model != d.ModelID {
				t.Errorf("the decision must equal what routing returns: %+v vs %s/%s", d, got.Runtime, got.Model)
			}
			if d.RouteClass == "" || len(d.PolicySHA) != 64 || d.Reason == "" || d.Provider == "" {
				t.Errorf("incomplete decision: %+v", d)
			}
		})
	}
}

// A rule's model reaches the model choice as "policy", and the reason names the rule.
func TestRoute_RuleModelIsRecordedAsPolicy(t *testing.T) {
	setPolicy(t, tablePolicy)
	got, err := route(t, routingRoot(t), projectWithYML(t, ""), "reviewer", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelChosenBy != "policy" || !got.ModelExplicit || got.RuntimeChosenBy != RuntimeByPolicy {
		t.Errorf("by %q/%q explicit=%v", got.RuntimeChosenBy, got.ModelChosenBy, got.ModelExplicit)
	}
	if want := "rule R2 matched [domain=code-review]: runtime=codex model=gpt-5.5 fallbacks=[claude]"; got.Decision.Reason != want {
		t.Errorf("reason = %q, want %q", got.Decision.Reason, want)
	}
}

// Frontmatter pins outrank a rule unless it sets override_pins; an explicit
// --runtime or --model outranks both.
func TestRoute_OverridePinsBothWays(t *testing.T) {
	captureRouteLog(t)
	root := routingRoot(t)
	project := projectWithYML(t, "")
	rule := func(pins string) string {
		return "rules:\n  - match: {agent: general-codex}\n    action: {runtime: agy, model: gemini-3.8-pro}\n" + pins
	}
	t.Run("without override_pins the pin stands", func(t *testing.T) {
		setPolicy(t, rule(""))
		got, err := route(t, root, project, "general-codex", nil)
		if err != nil {
			t.Fatal(err)
		}
		d := got.Decision
		if d.Runtime != "codex" || d.ModelID != "gpt-5.5" || d.RuleID != "R0" || got.RuntimeChosenBy != RuntimeByFrontmatter {
			t.Errorf("got %+v by %s", d, got.RuntimeChosenBy)
		}
		if !strings.Contains(d.Reason, "rule R1 matched but a pin outranks") {
			t.Errorf("the reason should say the rule was outranked: %q", d.Reason)
		}
	})
	t.Run("with override_pins the rule wins", func(t *testing.T) {
		setPolicy(t, rule("    override_pins: true\n"))
		got, err := route(t, root, project, "general-codex", nil)
		if err != nil {
			t.Fatal(err)
		}
		d := got.Decision
		if d.Runtime != "agy" || d.ModelID != "gemini-3.8-pro" || d.RuleID != "R1" || got.RuntimeChosenBy != RuntimeByPolicy {
			t.Errorf("got %+v by %s", d, got.RuntimeChosenBy)
		}
	})
	t.Run("explicit runtime and model beat override_pins", func(t *testing.T) {
		setPolicy(t, rule("    override_pins: true\n"))
		got, err := route(t, root, project, "general-codex", func(in *routeInput) {
			in.RuntimeOverride, in.ModelOverride = "codex", "gpt-5.1"
		})
		if err != nil {
			t.Fatal(err)
		}
		if d := got.Decision; d.Runtime != "codex" || d.ModelID != "gpt-5.1" || d.RuleID != "R0" || got.ModelChosenBy != "override" {
			t.Errorf("got %+v model by %s", d, got.ModelChosenBy)
		}
	})
	t.Run("an explicit model beats a rule model even with override_pins", func(t *testing.T) {
		setPolicy(t, "rules:\n  - match: {agent: general-codex}\n    action: {model: gpt-5.9}\n    override_pins: true\n")
		got, err := route(t, root, project, "general-codex", func(in *routeInput) { in.ModelOverride = "gpt-5.1" })
		if err != nil || got.Model != "gpt-5.1" || got.ModelChosenBy != "override" || got.Decision.RuleID != "R0" {
			t.Errorf("got %+v %v", got.Decision, err)
		}
	})
	t.Run("a rule model on a pinned-model agent needs override_pins", func(t *testing.T) {
		// general-codex pins runtime and model; a rule that sets only a model
		// leaves the pinned model alone unless it overrides pins.
		setPolicy(t, "rules:\n  - match: {agent: general-codex}\n    action: {model: gpt-5.9}\n")
		got, _ := route(t, root, project, "general-codex", nil)
		if got.Model != "gpt-5.5" || got.Decision.RuleID != "R0" {
			t.Errorf("the pinned model must stand: %+v", got.Decision)
		}
		setPolicy(t, "rules:\n  - match: {agent: general-codex}\n    action: {model: gpt-5.9}\n    override_pins: true\n")
		got, _ = route(t, root, project, "general-codex", nil)
		if got.Model != "gpt-5.9" || got.Decision.RuleID != "R1" {
			t.Errorf("override_pins must replace the pinned model: %+v", got.Decision)
		}
	})
}

// A rule's model belongs to the runtime the rule named: when the chain falls to
// another runtime, the model is not carried along (agy would accept the id).
func TestRoute_RuleModelStaysWithItsRuntime(t *testing.T) {
	captureRouteLog(t)
	setPolicy(t, "rules:\n  - match: {agent: bare}\n    action: {runtime: codex, model: gpt-5.5, fallbacks: [agy]}\n")
	withProbe(t, func(name string) probeResult { return probeResult{OK: name != "codex", Reason: "not signed in"} })
	got, err := route(t, routingRoot(t), projectWithYML(t, ""), "bare", nil)
	if err != nil || got.Runtime != "agy" || got.Model != "" || got.FallbackFrom != "codex" {
		t.Fatalf("got %+v %v", got, err)
	}
}

// A rule's model that the runtime cannot take is dropped, never passed through:
// a Claude tier never reaches codex, a foreign id never reaches Claude Code.
func TestRoute_RuleModelNeverCrossesVendors(t *testing.T) {
	logbuf := captureRouteLog(t)
	root := routingRoot(t)
	setPolicy(t, "rules:\n  - match: {agent: bare}\n    action: {model: gpt-5.5}\n  - match: {agent: codex-bare}\n    action: {model: opus}\n")
	got, err := route(t, root, projectWithYML(t, ""), "bare", nil)
	if err != nil || got.Runtime != "claude" || got.Model != "sonnet" {
		t.Fatalf("a foreign model must not reach claude: %+v %v", got, err)
	}
	got, err = route(t, root, projectWithYML(t, ""), "codex-bare", nil)
	if err != nil || got.Runtime != "codex" || got.Model != "" {
		t.Fatalf("a Claude tier must not reach codex: %+v %v", got, err)
	}
	if n := strings.Count(logbuf.String(), "does not fit runtime"); n != 2 {
		t.Errorf("each dropped model is announced once, got %d: %q", n, logbuf.String())
	}
}

// An untrusted policy file leaves routing exactly as it was and warns without a path.
func TestRoute_UntrustedPolicyIsIgnored(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("the group/world-writable file-mode trust check has no Windows semantics")
	}
	logbuf := captureRouteLog(t)
	dir := setPolicy(t, tablePolicy)
	if err := os.Chmod(routerpolicy.Path(dir), 0o666); err != nil {
		t.Fatal(err)
	}
	got, err := route(t, routingRoot(t), projectWithYML(t, ""), "reviewer", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != "claude" || got.Decision.RuleID != "R0" || got.Decision.PolicySHA != "" {
		t.Errorf("an untrusted policy must change nothing: %+v", got.Decision)
	}
	if out := logbuf.String(); !strings.Contains(out, "router policy ignored") || strings.Contains(out, dir) {
		t.Errorf("want a path-free warning, got %q", out)
	}
}

// ---- sticky ------------------------------------------------------------------

func TestRoute_StickyKeepsAConversationWhereItStarted(t *testing.T) {
	captureRouteLog(t)
	root := routingRoot(t)
	project := projectWithYML(t, "")
	setPolicy(t, "rules:\n  - match: {class: chat}\n    action: {runtime: codex, model: gpt-5.5}\n")
	first, err := route(t, root, project, "plain", func(in *routeInput) { in.Class, in.ConversationID = "chat", "conv-1" })
	if err != nil || first.Runtime != "codex" || first.Decision.RuleID != "R1" {
		t.Fatalf("first turn: %+v %v", first, err)
	}
	// The next turn would not match the rule (class default), but the
	// conversation stays on codex with the same model, and says why.
	next, err := route(t, root, project, "plain", func(in *routeInput) { in.ConversationID = "conv-1" })
	if err != nil {
		t.Fatal(err)
	}
	if next.Runtime != "codex" || next.Model != "gpt-5.5" || next.RuntimeChosenBy != RuntimeBySticky || next.ModelChosenBy != "sticky" {
		t.Errorf("second turn: %s/%s by %s/%s", next.Runtime, next.Model, next.RuntimeChosenBy, next.ModelChosenBy)
	}
	if d := next.Decision; d.RuleID != "R1" || !strings.HasPrefix(d.Reason, "sticky:") {
		t.Errorf("sticky decision: %+v", d)
	}
	// Another conversation is routed afresh.
	other, _ := route(t, root, project, "plain", func(in *routeInput) { in.ConversationID = "conv-2" })
	if other.Runtime != "claude" {
		t.Errorf("another conversation must not inherit the pin: %s", other.Runtime)
	}
	// An explicit runtime is the operator moving the conversation.
	moved, err := route(t, root, project, "plain", func(in *routeInput) { in.ConversationID, in.RuntimeOverride = "conv-1", "claude" })
	if err != nil || moved.Runtime != "claude" || moved.RuntimeChosenBy != RuntimeByOverride {
		t.Errorf("an explicit runtime must win over the pin: %+v %v", moved, err)
	}
}

// The router never moves a conversation by itself, not even when its runtime is gone.
func TestRoute_StickyRuntimeDownIsAnErrorNotASwitch(t *testing.T) {
	captureRouteLog(t)
	root := routingRoot(t)
	project := projectWithYML(t, "")
	setPolicy(t, "rules:\n  - match: {agent: plain}\n    action: {runtime: codex, fallbacks: [claude]}\n")
	if _, err := route(t, root, project, "plain", func(in *routeInput) { in.ConversationID = "c" }); err != nil {
		t.Fatal(err)
	}
	withProbe(t, func(name string) probeResult {
		if name == "codex" {
			return probeResult{Reason: "not signed in"}
		}
		return probeResult{OK: true}
	})
	_, err := route(t, root, project, "plain", func(in *routeInput) { in.ConversationID = "c" })
	if ex, ok := AsExplicitRuntimeError(err); !ok || ex.Runtime != "codex" {
		t.Fatalf("want an ExplicitRuntimeError for codex, got %v", err)
	}
}

// Without a policy rule the router stays out: nothing is pinned, so a
// conversation resolves by the P0a chain every turn, as before.
func TestRoute_NoStickyWithoutPolicy(t *testing.T) {
	captureRouteLog(t)
	resetRouterState(t)
	project := projectWithYML(t, "")
	if _, err := route(t, routingRoot(t), project, "plain", func(in *routeInput) { in.ConversationID = "c" }); err != nil {
		t.Fatal(err)
	}
	if _, ok := pinOf(project, "c", "plain"); ok {
		t.Fatal("a conversation must not be pinned when no rule is in force")
	}
}

// ---- cooldown ----------------------------------------------------------------

func TestRoute_CooldownSkipsAFailingRuntime(t *testing.T) {
	captureRouteLog(t)
	root := routingRoot(t)
	project := projectWithYML(t, "")
	setPolicy(t, "# a policy file with no rules still engages the cooldown\n")
	clk := &fakeClock{t: time.Unix(5_000, 0)}
	seedCooldownForTest(t, project, "agy", clk.now)
	got, err := route(t, root, project, "pinned-fb", nil) // agy, then codex, claude
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != "codex" || got.FallbackFrom != "agy" || got.RuntimeChosenBy != RuntimeByFallback {
		t.Errorf("a cooling agy must be skipped: %s by %s from %q", got.Runtime, got.RuntimeChosenBy, got.FallbackFrom)
	}
	if !strings.Contains(got.Decision.Reason, "fell back from agy") {
		t.Errorf("reason = %q", got.Decision.Reason)
	}
	clk.t = clk.t.Add(60 * time.Second)
	got, _ = route(t, root, project, "pinned-fb", nil)
	if got.Runtime != "agy" {
		t.Errorf("after 60 s agy is back: %s", got.Runtime)
	}
}

func TestRoute_CooldownIsAPreferenceNotABan(t *testing.T) {
	captureRouteLog(t)
	root := routingRoot(t)
	setPolicy(t, "# cooldown needs a policy file\n")
	project := projectWithYML(t, "")
	failThrice(project, "claude")
	// "plain" has nowhere else to go: it still runs on claude.
	got, err := route(t, root, project, "plain", nil)
	if err != nil || got.Runtime != "claude" {
		t.Fatalf("the only candidate must still run: %+v %v", got, err)
	}
	// An explicit choice never goes through the cooldown, even with somewhere to go.
	routerCooldown.of(project).Success("claude")
	failThrice(project, "agy")
	got, err = route(t, root, project, "pinned-fb", func(in *routeInput) { in.RuntimeOverride, in.RuntimeFallbackOptIn = "agy", []string{"claude"} })
	if err != nil || got.Runtime != "agy" || got.RuntimeChosenBy != RuntimeByOverride {
		t.Fatalf("an explicit runtime ignores the cooldown: %+v %v", got, err)
	}
}

func TestNoteRun_CountsFailuresNotCancelsAndSuccessResets(t *testing.T) {
	resetRouterState(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 5; i++ {
		noteRun(cancelled, "/p", "codex", 1, errors.New("killed"))
	}
	if cool, _ := routerCooldown.of("/p").Cooling("codex"); cool {
		t.Fatal("a run cut short by its context says nothing about the runtime")
	}
	noteRun(context.Background(), "/p", "codex", 0, errors.New("exec: not found"))
	noteRun(context.Background(), "/p", "codex", 2, nil)
	noteRun(context.Background(), "/p", "codex", 0, nil) // success
	noteRun(context.Background(), "/p", "codex", 1, nil)
	if cool, _ := routerCooldown.of("/p").Cooling("codex"); cool {
		t.Fatal("a success must reset the count")
	}
	noteRun(context.Background(), "/p", "codex", 1, nil)
	noteRun(context.Background(), "/p", "codex", 1, nil)
	if cool, _ := routerCooldown.of("/p").Cooling("codex"); !cool {
		t.Fatal("three failures in a row must cool")
	}
}

// ---- the project can only tighten -------------------------------------------

func TestRoute_ProjectCanOnlyDisable(t *testing.T) {
	captureRouteLog(t)
	root := routingRoot(t)
	t.Run("a disabled runtime is skipped and a rule falls back", func(t *testing.T) {
		setPolicy(t, tablePolicy)
		got, err := route(t, root, projectWithYML(t, "router:\n  disable_runtimes: [codex]\n"), "reviewer", nil)
		if err != nil || got.Runtime != "claude" || got.FallbackFrom != "codex" {
			t.Fatalf("got %+v %v", got, err)
		}
	})
	t.Run("a pin to a disabled runtime fails closed", func(t *testing.T) {
		resetRouterState(t)
		_, err := route(t, root, projectWithYML(t, "router:\n  disable_runtimes: [codex]\n"), "general-codex", nil)
		if err == nil || !strings.Contains(err.Error(), "disabled by this project") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an explicit runtime that is disabled is an explicit error", func(t *testing.T) {
		resetRouterState(t)
		_, err := route(t, root, projectWithYML(t, "router:\n  disable_runtimes: [agy]\n"), "plain", func(in *routeInput) { in.RuntimeOverride = "agy" })
		if ex, ok := AsExplicitRuntimeError(err); !ok || !strings.Contains(ex.Reason, "disabled by this project") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a disabled model is refused by id", func(t *testing.T) {
		resetRouterState(t)
		_, err := route(t, root, projectWithYML(t, "router:\n  disable_models: [gpt-5.5]\n"), "general-codex", nil)
		if err == nil || !strings.Contains(err.Error(), `"gpt-5.5"`) || !strings.Contains(err.Error(), "disable_models") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("nothing in the project can add or enable", func(t *testing.T) {
		resetRouterState(t)
		yml := "router:\n  enable_runtimes: [agy]\n  rules: [{action: {runtime: agy}}]\n  providers: [x]\n"
		got, err := route(t, root, projectWithYML(t, yml), "plain", nil)
		if err != nil || got.Runtime != "claude" || got.Decision.RuleID != "R0" {
			t.Fatalf("a project file must not route: %+v %v", got, err)
		}
	})
}

// ---- the no-policy differential (behavior neutrality) -----------------------

// ---- ledger -------------------------------------------------------------------

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// A policy rule sending domain code-review to codex shows in the ledger row.
func TestRun_LedgerCarriesTheRouteOfAPolicyRule(t *testing.T) {
	root := routingRoot(t)
	rec := fakeCLIs(t)
	logDir := isolatedLogDir(t)
	setPolicy(t, "rules:\n  - match: {class: chat}\n    action: {runtime: agy}\n  - match: {domain: code-review}\n    action: {runtime: codex}\n")
	captureRouteLog(t)
	if _, _, err := Run(context.Background(), Request{AgentName: "reviewer", Task: "hi", Project: t.TempDir(), YakosRoot: root}); err != nil {
		t.Fatal(err)
	}
	if got := invoked(t, rec); strings.Join(got, ",") != "codex" {
		t.Fatalf("executed %v, want codex", got)
	}
	events := readDispatchLog(t, logDir)
	fin := events[len(events)-1]
	assertField(t, fin, "route_rule", "R2")
	assertField(t, fin, "route_class", "default")
	assertField(t, fin, "runtime", "codex")
	assertField(t, fin, "runtime_chosen_by", "policy")
	if r, _ := fin["route_reason"].(string); !strings.HasPrefix(r, "rule R2 matched [domain=code-review]") {
		t.Errorf("route_reason = %q", r)
	}
	if s, _ := fin["policy_sha"].(string); !hex64.MatchString(s) {
		t.Errorf("policy_sha = %q", fin["policy_sha"])
	}
}

// The streaming path stamps the same fields: both transports share the chokepoint.
func TestRunStream_RequestCarriesTheRoute(t *testing.T) {
	captureRouteLog(t)
	root := routingRoot(t)
	svc := newResolutionSvc(t, root)
	setPolicy(t, "rules:\n  - match: {domain: code-review}\n    action: {runtime: codex, model: gpt-5.5}\n")
	var req Request
	var chat rt.ChatDispatchRequest
	withStreamRunFn(func(_ context.Context, r Request, _ rt.Adapter, cr rt.ChatDispatchRequest, _ func(StreamChunk)) (Result, error) {
		req, chat = r, cr
		return Result{}, nil
	}, func() {
		if _, err := svc.RunStream(context.Background(), Params{Agent: "reviewer", Task: "hi", Project: t.TempDir()}, func(StreamChunk) {}); err != nil {
			t.Fatal(err)
		}
	})
	if req.RouteRule != "R1" || req.RouteClass != "default" || !hex64.MatchString(req.PolicySHA) || !strings.HasPrefix(req.RouteReason, "rule R1") || req.Runtime != "codex" {
		t.Errorf("stream request routing = %+v", req)
	}
	if chat.ModelOverride != "gpt-5.5" || !chat.ModelExplicit {
		t.Errorf("chat model %q explicit=%v", chat.ModelOverride, chat.ModelExplicit)
	}
}

// general-codex still runs codex with -m through the router, and a rule can move
// an unpinned agent to codex with its own -m.
func TestRun_PolicyRuleExecsCodexWithTheRuleModel(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("shell stubs")
	}
	root := routingRoot(t)
	bin := t.TempDir()
	rec := filepath.Join(t.TempDir(), "argv.txt")
	for _, name := range []string{"claude", "codex", "agy"} {
		body := "#!/bin/sh\nprintf '" + name + " %s\\n' \"$*\" >> '" + rec + "'\necho ok\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	isolatedLogDir(t)
	setPolicy(t, "rules:\n  - match: {agent: plain}\n    action: {runtime: codex, model: gpt-5.4}\n")
	captureRouteLog(t)
	for _, agent := range []string{"general-codex", "plain"} {
		want := map[string]string{"general-codex": "gpt-5.5", "plain": "gpt-5.4"}[agent]
		_ = os.Remove(rec)
		if _, _, err := Run(context.Background(), Request{AgentName: agent, Task: "hi", Project: t.TempDir(), YakosRoot: root}); err != nil {
			t.Fatalf("%s: %v", agent, err)
		}
		b, _ := os.ReadFile(rec)
		line := string(b)
		if !strings.HasPrefix(line, "codex ") || !strings.Contains(line, "-m "+want) || strings.Contains(line, "sonnet") {
			t.Errorf("%s: argv %q, want codex with -m %s", agent, line, want)
		}
	}
}

// ---- explain seam, queries -----------------------------------------------------

func TestExplain_DecidesWithoutPinningOrLogging(t *testing.T) {
	captureRouteLog(t)
	root := routingRoot(t)
	logDir := isolatedLogDir(t)
	setPolicy(t, "rules:\n  - match: {class: chat}\n    action: {runtime: codex}\n")
	project := projectWithYML(t, "")
	d, err := Explain(context.Background(), ExplainQuery{YakosRoot: root, Project: project, Agent: "plain", Class: "chat", ConversationID: "c"})
	if err != nil || d.RuleID != "R1" || d.Runtime != "codex" || d.RouteClass != "chat" {
		t.Fatalf("got %+v %v", d, err)
	}
	if _, ok := pinOf(project, "c", "plain"); ok {
		t.Error("explaining must not pin a conversation")
	}
	if entries, _ := os.ReadDir(logDir); len(entries) != 0 {
		t.Error("explaining must write no ledger event")
	}
}

// What the validation queries report is what Run will pick.
func TestPreferredAndResolveRuntime_FollowTheRules(t *testing.T) {
	captureRouteLog(t)
	root := routingRoot(t)
	setPolicy(t, "rules:\n  - match: {domain: code-review}\n    action: {runtime: agy}\n")
	q := RouteQuery{YakosRoot: root, Project: projectWithYML(t, ""), Agent: "reviewer"}
	if c, err := PreferredRuntime(q); err != nil || c.Runtime != "agy" || c.ChosenBy != RuntimeByPolicy {
		t.Errorf("PreferredRuntime = %+v %v", c, err)
	}
	if c, err := ResolveRuntime(context.Background(), q); err != nil || c.Runtime != "agy" {
		t.Errorf("ResolveRuntime = %+v %v", c, err)
	}
}

// Run hands the router the size of the task and the conversation: a size rule
// matches through Run, and the conversation is pinned after the first turn.
func TestRun_TaskSizeAndConversationReachTheRouter(t *testing.T) {
	root := routingRoot(t)
	rec := fakeCLIs(t)
	logDir := isolatedLogDir(t)
	setPolicy(t, "rules:\n  - match: {task_bytes_gt: 5}\n    action: {runtime: agy}\n")
	captureRouteLog(t)
	project := t.TempDir()
	if _, _, err := Run(context.Background(), Request{AgentName: "bare", Task: "0123456789", Project: project, YakosRoot: root, ConversationID: "conv-run"}); err != nil {
		t.Fatal(err)
	}
	if got := invoked(t, rec); strings.Join(got, ",") != "agy" {
		t.Fatalf("executed %v, want agy by the size rule", got)
	}
	events := readDispatchLog(t, logDir)
	assertField(t, events[len(events)-1], "route_rule", "R1")
	if p, ok := pinOf(project, "conv-run", "bare"); !ok || p.Runtime != "agy" || p.RuleID != "R1" {
		t.Errorf("pin = %+v %v", p, ok)
	}
}

// Run feeds every finished run to the cooldown: three failing runs of a runtime
// cool it, through the real Run.
func TestRun_RunFeedsTheCooldown(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("shell stubs")
	}
	root := routingRoot(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho boom >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	isolatedLogDir(t)
	captureRouteLog(t)
	project := t.TempDir()
	for i := 0; i < 3; i++ {
		if cool, _ := routerCooldown.of(project).Cooling("codex"); cool {
			t.Fatalf("cooling after only %d failures", i)
		}
		_, res, err := Run(context.Background(), Request{AgentName: "codex-bare", Task: "hi", Project: project, YakosRoot: root})
		if err != nil || res.ExitCode != 3 {
			t.Fatalf("run %d: exit %d err %v", i, res.ExitCode, err)
		}
	}
	if cool, _ := routerCooldown.of(project).Cooling("codex"); !cool {
		t.Fatal("three failed runs must cool codex")
	}
}
