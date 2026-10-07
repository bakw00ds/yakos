package dispatch

// Tests for the sensitive route class (K-140) at the routing chokepoint, the
// pre-check seam, the explain seam and the ledger. Secret-shaped fixtures are
// assembled from parts.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/router"
	rt "github.com/bakw00ds/yakos/internal/runtime"
)

func awsKey() string  { return "AKIA" + "IOSFODNN7EXAMPLE" }
func ghToken() string { return "ghp" + "_" + strings.Repeat("a1B2", 9) }
func pemHead() string { return "-----BEGIN RSA PRIV" + "ATE KEY-----" }

func upProbe(t *testing.T) {
	t.Helper()
	withProbe(t, func(string) probeResult { return probeResult{OK: true} })
}

// Every item of the egress corpus forces the primary runtime, even for an agent
// pinned to codex, an explicit --runtime and a policy rule that names agy.
func TestSensitive_EgressCorpusForcesPrimary(t *testing.T) {
	captureRouteLog(t)
	upProbe(t)
	root := routingRoot(t)
	project := projectWithYML(t, "router:\n  never_paths: [\"internal/billing/*\"]\n")
	tasks := map[string]string{
		"aws":           "deploy with " + awsKey(),
		"github":        "push using " + ghToken(),
		"pem":           "key follows " + pemHead(),
		".env path":     "summarise the .env file",
		"project path":  "audit internal/billing/ledger.go",
		"id_rsa":        "use ~/.ssh/id_rsa",
		"aws creds":     "look in ~/.aws/credentials",
		"nested secret": "x\n\n   see secrets/prod/key.txt   ",
	}
	for name, task := range tasks {
		for _, c := range []struct {
			agent, override string
		}{{"general-codex", ""}, {"plain", "codex"}, {"general-agy", ""}} {
			setPolicy(t, "rules:\n  - match: {agent: plain}\n    action: {runtime: agy}\n")
			got, err := route(t, root, project, c.agent, func(in *routeInput) {
				in.Task, in.RuntimeOverride = task, c.override
			})
			if err != nil {
				t.Fatalf("%s/%s: %v", name, c.agent, err)
			}
			d := got.Decision
			if got.Runtime != "claude" || d.RouteClass != router.ClassSensitive || strings.Join(d.Chain, " ") != "claude" {
				t.Errorf("%s/%s: runtime %q class %q chain %v", name, c.agent, got.Runtime, d.RouteClass, d.Chain)
			}
			if !strings.Contains(d.Reason, "sensitive -> primary only") {
				t.Errorf("%s/%s: reason %q", name, c.agent, d.Reason)
			}
			for _, leak := range []string{awsKey(), ghToken(), "billing", ".ssh"} {
				if strings.Contains(d.Reason, leak) {
					t.Errorf("%s/%s: reason leaks %q", name, c.agent, leak)
				}
			}
		}
	}
}

// The same agents with a clean task keep their pins: the class changes nothing
// for a request that is not sensitive.
func TestSensitive_CleanTaskRoutesAsBefore(t *testing.T) {
	captureRouteLog(t)
	upProbe(t)
	setPolicy(t, "")
	got, err := route(t, routingRoot(t), projectWithYML(t, ""), "general-codex", func(in *routeInput) { in.Task = "write a haiku" })
	if err != nil || got.Runtime != "codex" || got.Decision.RouteClass != "default" {
		t.Fatalf("got %+v err %v", got, err)
	}
}

// A project cannot remove a default pattern: an empty list, a negation and an
// unrelated glob leave `.env` sensitive.
func TestSensitive_HostileProjectCannotRemoveDefaults(t *testing.T) {
	captureRouteLog(t)
	upProbe(t)
	root := routingRoot(t)
	for _, yml := range []string{
		"router:\n  never_paths: []\n",
		"router:\n  never_paths: [\"!.env*\", \"nothing\"]\n",
		"router:\n  never_paths: not-a-list\n",
		"decisions:\n  egress:\n    never_paths: []\n",
	} {
		setPolicy(t, "")
		got, err := route(t, root, projectWithYML(t, yml), "general-codex", func(in *routeInput) { in.Task = "read .env" })
		if err != nil || got.Runtime != "claude" || got.Decision.RouteClass != "sensitive" {
			t.Errorf("%q: runtime %q class %q err %v", yml, got.Runtime, got.Decision.RouteClass, err)
		}
	}
}

// The agent's own prompt and the extra texts (knowledge block, digests, upstream
// flow output) are scanned, not only the task.
func TestSensitive_AgentPromptAndExtraAreScanned(t *testing.T) {
	captureRouteLog(t)
	upProbe(t)
	root := routingRoot(t)
	body := "---\nid: leaky\nruntime: codex\n---\n\n## Purpose\n\nDeploys with " + awsKey() + ".\n"
	if err := os.WriteFile(filepath.Join(root, "lib", "agents", "leaky.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	setPolicy(t, "")
	got, err := route(t, root, projectWithYML(t, ""), "leaky", func(in *routeInput) { in.Task = "go" })
	if err != nil || got.Runtime != "claude" || got.Decision.RouteClass != "sensitive" {
		t.Fatalf("prompt: runtime %q class %q err %v", got.Runtime, got.Decision.RouteClass, err)
	}
	for _, extra := range [][]string{{"fine", "upstream said " + ghToken()}, {"digest mentions ~/.aws/credentials"}} {
		got, err := route(t, root, projectWithYML(t, ""), "general-codex", func(in *routeInput) { in.Task, in.Extra = "go", extra })
		if err != nil || got.Runtime != "claude" || got.Decision.RouteClass != "sensitive" {
			t.Errorf("extra %q: runtime %q class %q err %v", extra, got.Runtime, got.Decision.RouteClass, err)
		}
	}
}

// The flows seam: an upstream output holding a key makes the downstream node's
// request sensitive; a clean one does not.
func TestClassifyFlowOutput(t *testing.T) {
	p := t.TempDir()
	if !ClassifyFlowOutput(p, "clean", "step 1 printed "+awsKey()) {
		t.Error("upstream key not detected")
	}
	if ClassifyFlowOutput(p, "clean", "also clean") || ClassifyFlowOutput(p) {
		t.Error("clean output flagged")
	}
}

// A scan that cannot finish fails closed to the primary with a warning.
func TestSensitive_OversizeScanFailsClosedWithWarning(t *testing.T) {
	log := captureRouteLog(t)
	upProbe(t)
	setPolicy(t, "")
	big := strings.Repeat("a", 17<<20)
	got, err := route(t, routingRoot(t), projectWithYML(t, ""), "general-codex", func(in *routeInput) { in.Task = big })
	if err != nil || got.Runtime != "claude" || got.Decision.RouteClass != "sensitive" {
		t.Fatalf("runtime %q class %q err %v", got.Runtime, got.Decision.RouteClass, err)
	}
	if !strings.Contains(log.String(), "classified sensitive (scan-oversize)") {
		t.Errorf("no warning: %q", log.String())
	}
}

// When even the primary is unavailable the request is refused, never sent to
// another runtime, and the refusal is the only ledger event.
func TestSensitive_PrimaryDownIsRefusedWithAnEvent(t *testing.T) {
	captureRouteLog(t)
	root := routingRoot(t)
	withProbe(t, func(name string) probeResult {
		if name == "claude" {
			return probeResult{Reason: "not signed in"}
		}
		return probeResult{OK: true}
	})
	logDir := isolatedLogDir(t)
	setPolicy(t, "")
	task := "use " + awsKey() + " in internal/secret/path"
	_, _, err := Run(context.Background(), Request{AgentName: "general-codex", Task: task, Project: t.TempDir(), YakosRoot: root, ConversationID: "c1"})
	if _, ok := AsRouteRefused(err); !ok {
		t.Fatalf("err = %v, want RouteRefusedError", err)
	}
	if strings.Contains(err.Error(), awsKey()) {
		t.Error("the error carries the secret")
	}
	events := readDispatchLog(t, logDir)
	if len(events) != 1 {
		t.Fatalf("events = %v, want exactly one", events)
	}
	ev := events[0]
	assertField(t, ev, "type", "route_refused")
	assertField(t, ev, "route_class", "sensitive")
	assertField(t, ev, "route_reason", "secret-pattern")
	assertField(t, ev, "conversation_id", "c1")
	raw, _ := os.ReadFile(filepath.Join(logDir, "dispatch-log.ndjson"))
	if strings.Contains(string(raw), awsKey()) || strings.Contains(string(raw), "internal/secret") {
		t.Error("the refusal event carries request text")
	}
}

// The project disabling claude refuses a sensitive request too.
func TestSensitive_PrimaryDisabledByProjectIsRefused(t *testing.T) {
	captureRouteLog(t)
	upProbe(t)
	setPolicy(t, "")
	_, err := route(t, routingRoot(t), projectWithYML(t, "router:\n  disable_runtimes: [claude]\n"), "plain", func(in *routeInput) { in.Task = "read .env" })
	if _, ok := AsRouteRefused(err); !ok {
		t.Fatalf("err = %v", err)
	}
}

// Run writes the class to the ledger and execs only the primary.
func TestRun_SensitiveLedgerRowAndOnlyPrimaryExecutes(t *testing.T) {
	root := routingRoot(t)
	rec := fakeCLIs(t)
	logDir := isolatedLogDir(t)
	setPolicy(t, "")
	captureRouteLog(t)
	if _, _, err := Run(context.Background(), Request{AgentName: "general-codex", Task: "see " + ghToken(), Project: t.TempDir(), YakosRoot: root}); err != nil {
		t.Fatal(err)
	}
	if got := invoked(t, rec); strings.Join(got, ",") != "claude" {
		t.Fatalf("executed %v, want only claude", got)
	}
	events := readDispatchLog(t, logDir)
	fin := events[len(events)-1]
	assertField(t, fin, "route_class", "sensitive")
	assertField(t, fin, "runtime", "claude")
	if r, _ := fin["route_reason"].(string); !strings.Contains(r, "sensitive -> primary only (secret-pattern)") {
		t.Errorf("route_reason = %q", r)
	}
}

func TestRunStream_SensitiveRoutesToPrimary(t *testing.T) {
	captureRouteLog(t)
	upProbe(t)
	root := routingRoot(t)
	svc := newResolutionSvc(t, root)
	setPolicy(t, "")
	var req Request
	withStreamRunFn(func(_ context.Context, r Request, _ rt.Adapter, _ rt.ChatDispatchRequest, _ func(StreamChunk)) (Result, error) {
		req = r
		return Result{}, nil
	}, func() {
		for _, p := range []Params{
			{Agent: "general-codex", Task: "go", Project: t.TempDir(), ScanExtra: []string{"prior turn: " + awsKey()}},
			{Agent: "general-codex", Task: "use " + ghToken(), Project: t.TempDir()},
		} {
			req = Request{}
			if _, err := svc.RunStream(context.Background(), p, func(StreamChunk) {}); err != nil {
				t.Fatal(err)
			}
			if req.Runtime != "claude" || req.RouteClass != "sensitive" {
				t.Errorf("stream request = runtime %q class %q", req.Runtime, req.RouteClass)
			}
		}
	})
}

// The pre-check and Run agree: PreferredRuntime with the same task names the
// runtime routing picks, and ResolveRuntime too.
func TestSensitive_PreCheckAgreesWithRun(t *testing.T) {
	captureRouteLog(t)
	upProbe(t)
	root := routingRoot(t)
	project := projectWithYML(t, "")
	setPolicy(t, "")
	for _, task := range []string{"hello", "cat .env", "key " + awsKey()} {
		q := RouteQuery{YakosRoot: root, Project: project, Agent: "general-codex", Task: task}
		pre, err := PreferredRuntime(q)
		if err != nil {
			t.Fatal(err)
		}
		res, err := ResolveRuntime(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		run, err := route(t, root, project, "general-codex", func(in *routeInput) { in.Task = task })
		if err != nil {
			t.Fatal(err)
		}
		if pre.Runtime != run.Runtime || res.Runtime != run.Runtime {
			t.Errorf("%q: pre-check %q, resolve %q, run %q", task, pre.Runtime, res.Runtime, run.Runtime)
		}
	}
}

// Explain with --class sensitive (and with a sensitive task) shows the restriction.
func TestSensitive_ExplainShowsPrimaryOnly(t *testing.T) {
	captureRouteLog(t)
	upProbe(t)
	root := routingRoot(t)
	setPolicy(t, "")
	for _, q := range []ExplainQuery{
		{YakosRoot: root, Project: projectWithYML(t, ""), Agent: "general-codex", Class: "sensitive"},
		{YakosRoot: root, Project: projectWithYML(t, ""), Agent: "general-codex", Task: "read .env"},
	} {
		d, err := Explain(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		if d.Runtime != "claude" || d.RouteClass != "sensitive" || strings.Join(d.Chain, " ") != "claude" || !strings.Contains(d.Reason, "sensitive -> primary only") {
			t.Errorf("explain = %+v", d)
		}
	}
}

// Only claude, and runtimes whose every catalog model is billing=local, may take
// a sensitive request. No shipped harness is local today.
func TestSensitive_EligibleRuntimes(t *testing.T) {
	for name, want := range map[string]bool{"claude": true, "codex": false, "agy": false, "": false, "claude-sdk": false} {
		if got := sensitiveEligible(name); got != want {
			t.Errorf("sensitiveEligible(%q) = %v", name, got)
		}
	}
	got := restrictSensitive([]candidate{{"codex", "x"}, {"agy", "y"}})
	if len(got) != 1 || got[0].name != "claude" {
		t.Errorf("an empty chain must fall closed to claude: %v", got)
	}
}
