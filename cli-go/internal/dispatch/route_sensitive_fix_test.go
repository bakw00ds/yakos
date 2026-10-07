package dispatch

// K-140 fixup tests: the framework roster is never sensitive on its own prose,
// a secret in an agent prompt still is, and RunStream writes route_refused.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	rt "github.com/bakw00ds/yakos/internal/runtime"
)

// Every framework agent's own prompt classifies default. backend.md says "Never
// edit .env*"; scanning that for credential-file names forced every dispatch of
// four agents to claude and broke the runtime adapter fixtures.
func TestSensitive_EveryFrameworkAgentPromptIsDefault(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	roster, err := agentscompose.Compose(root, "")
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(root, "lib", "agents", "*.md"))
	if len(roster) == 0 || len(roster) < len(files)-3 {
		t.Fatalf("roster has %d agents for %d files: wrong root %s?", len(roster), len(files), root)
	}
	seenEnvProse := false
	for i := range roster {
		a := roster[i]
		if strings.Contains(a.Prompt, ".env*") {
			seenEnvProse = true
		}
		if class, why := classifyRequest("", chainInput{}, &a, "implement the task", nil, nil); class != "default" {
			t.Errorf("agent %s classifies %s (%s)", a.ID, class, why)
		}
	}
	if !seenEnvProse {
		t.Error("no roster prompt mentions .env*: the test no longer covers the regression")
	}
}

// The prompt is still scanned for secrets, and the task for names.
func TestSensitive_PromptSecretsStillCountNamesDoNot(t *testing.T) {
	ag := &agentscompose.ComposedAgent{ID: "x", Prompt: "Never edit .env* or ~/.aws/credentials."}
	if class, _ := classifyRequest("", chainInput{}, ag, "go", nil, nil); class != "default" {
		t.Errorf("name prose in a prompt = %s", class)
	}
	ag.Prompt += " Key " + awsKey()
	if class, why := classifyRequest("", chainInput{}, ag, "go", nil, nil); class != "sensitive" || why != "secret-pattern" {
		t.Errorf("secret in a prompt = %s/%s", class, why)
	}
	ag.Prompt = "Never edit .env*."
	if class, why := classifyRequest("", chainInput{}, ag, "cat .env", nil, nil); class != "sensitive" || why != "never-path" {
		t.Errorf("name in the task = %s/%s", class, why)
	}
	if class, _ := classifyRequest("", chainInput{}, ag, "go", []string{"see @.env"}, nil); class != "sensitive" {
		t.Errorf("name in extra = %s", class)
	}
}

// RunStream writes the one route_refused event when no permitted runtime can
// take a sensitive request, as Run does.
func TestRunStream_SensitiveRefusedWritesEvent(t *testing.T) {
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
	svc := newResolutionSvc(t, root)
	called := false
	withStreamRunFn(func(context.Context, Request, rt.Adapter, rt.ChatDispatchRequest, func(StreamChunk)) (Result, error) {
		called = true
		return Result{}, nil
	}, func() {
		task := "use " + awsKey() + " in internal/secret/path"
		_, err := svc.RunStream(context.Background(), Params{Agent: "general-codex", Task: task, Project: t.TempDir(), ConversationID: "c9"}, func(StreamChunk) {})
		if _, ok := AsRouteRefused(err); !ok {
			t.Fatalf("err = %v, want RouteRefusedError", err)
		}
	})
	if called {
		t.Error("a refused request reached a runtime")
	}
	events := readDispatchLog(t, logDir)
	if len(events) != 1 {
		t.Fatalf("events = %v, want exactly one", events)
	}
	assertField(t, events[0], "type", "route_refused")
	assertField(t, events[0], "route_class", "sensitive")
	assertField(t, events[0], "route_reason", "secret-pattern")
	assertField(t, events[0], "conversation_id", "c9")
	raw, _ := os.ReadFile(filepath.Join(logDir, "dispatch-log.ndjson"))
	if strings.Contains(string(raw), awsKey()) || strings.Contains(string(raw), "internal/secret") {
		t.Error("the refusal event carries request text")
	}
}
