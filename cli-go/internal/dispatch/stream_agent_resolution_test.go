package dispatch

// stream_agent_resolution_test.go mirrors agent_resolution_test.go for the
// RunStream / stream.go path.
//
// Before the fix in this PR, stream.go had a DUPLICATE agent-resolution block
// that lacked the generic-runtime fallback added to dispatch.go in PR #203.
// That caused stream.go:214 to error with:
//
//   dispatch: agent "claude" not found in composed set
//
// for any bare runtime name (claude/codex/agy/gemini), breaking the console
// chat REPL.
//
// These tests exercise stream.go's resolution via RunStream using the
// package-level withStreamRunFn seam (same pattern as agent_resolution_test.go
// uses setRunFn for Run).
//
// Test inventory:
//   - TestStream_GenericAgent_Claude  — bare "claude" resolves, no error
//   - TestStream_GenericAgent_Codex   — bare "codex" resolves, no error
//   - TestStream_GenericAgent_Gemini  — bare "gemini" resolves, no error
//   - TestStream_GenericAgent_Agy     — bare "agy" resolves, no error
//   - TestStream_UnknownAgent_StillErrors — unknown name still errors
//   - TestStream_SpecialistAgent_StillResolves — real specialist unaffected
//   - TestResolveAgent_DirectUnit      — unit test of resolveAgent helper
//   - TestResolveRuntime_DirectUnit    — unit test of resolveRuntime helper

import (
	"context"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/runtime"
)

// newStreamResolutionSvc builds a Service pointing at the given yakosRoot.
// Tests inject a fake streamRunFn to avoid spawning real runtime processes.
func newStreamResolutionSvc(t *testing.T, yakosRoot string) *Service {
	t.Helper()
	return NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: t.TempDir(),
	})
}

// runStreamCapture is a helper that installs a capturing fake, calls RunStream,
// and returns the captured Request plus the RunStream error.
func runStreamCapture(ctx context.Context, svc *Service, p Params) (Request, error) {
	var captured Request
	var runErr error

	withStreamRunFn(func(_ context.Context, req Request, _ runtime.Adapter, _ runtime.ChatDispatchRequest, _ func(StreamChunk)) (Result, error) {
		captured = req
		return Result{ExitCode: 0, ModelChosenBy: "frontmatter", ModelResolved: "sonnet"}, nil
	}, func() {
		_, runErr = svc.RunStream(ctx, p, func(_ StreamChunk) {})
	})

	return captured, runErr
}

// TestStream_GenericAgent_Claude verifies that agent="claude" (the console chat
// pane default) resolves via the generic-runtime fallback in stream.go and does
// not return "not found in composed set".
func TestStream_GenericAgent_Claude(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newStreamResolutionSvc(t, yakosRoot)

	captured, err := runStreamCapture(context.Background(), svc, Params{
		Agent:   "claude",
		Task:    "hello from chat",
		Project: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("RunStream with agent=claude: unexpected error: %v", err)
	}
	if captured.AgentName != "claude" {
		t.Errorf("AgentName = %q, want %q", captured.AgentName, "claude")
	}
}

// TestStream_GenericAgent_Codex verifies that agent="codex" resolves via the
// generic-runtime fallback in stream.go.
func TestStream_GenericAgent_Codex(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newStreamResolutionSvc(t, yakosRoot)

	captured, err := runStreamCapture(context.Background(), svc, Params{
		Agent:   "codex",
		Task:    "hello",
		Project: t.TempDir(),
		Runtime: "codex", // explicit override — should still resolve
	})
	if err != nil {
		t.Fatalf("RunStream with agent=codex: unexpected error: %v", err)
	}
	if captured.AgentName != "codex" {
		t.Errorf("AgentName = %q, want %q", captured.AgentName, "codex")
	}
	if captured.Runtime != "codex" {
		t.Errorf("Runtime = %q, want %q", captured.Runtime, "codex")
	}
}

// TestStream_GenericAgent_Gemini verifies the gemini bare-runtime case.
func TestStream_GenericAgent_Gemini(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newStreamResolutionSvc(t, yakosRoot)

	captured, err := runStreamCapture(context.Background(), svc, Params{
		Agent:   "gemini",
		Task:    "hello",
		Project: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("RunStream with agent=gemini: unexpected error: %v", err)
	}
	if captured.AgentName != "gemini" {
		t.Errorf("AgentName = %q, want %q", captured.AgentName, "gemini")
	}
}

// TestStream_GenericAgent_Agy verifies the agy bare-runtime case.
func TestStream_GenericAgent_Agy(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newStreamResolutionSvc(t, yakosRoot)

	_, err := runStreamCapture(context.Background(), svc, Params{
		Agent:   "agy",
		Task:    "hello",
		Project: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("RunStream with agent=agy: unexpected error: %v", err)
	}
}

// TestStream_UnknownAgent_StillErrors verifies that a genuinely unknown agent
// (not a runtime, not in the roster) still returns "not found in composed set"
// on the stream path.
func TestStream_UnknownAgent_StillErrors(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newStreamResolutionSvc(t, yakosRoot)

	// No streamRunFn override needed — the error surfaces before streamRunFn runs.
	_, err := svc.RunStream(context.Background(), Params{
		Agent:   "definitely-not-a-real-agent",
		Task:    "hello",
		Project: t.TempDir(),
	}, func(_ StreamChunk) {})
	if err == nil {
		t.Fatal("RunStream with unknown agent: expected error, got nil")
	}
	const wantSub = "not found in composed set"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("unexpected error message (want %q in): %v", wantSub, err)
	}
}

// TestStream_SpecialistAgent_StillResolves verifies that a real specialist agent
// (backend) still resolves correctly on the stream path and the generic
// fallback does not interfere.
func TestStream_SpecialistAgent_StillResolves(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t) // provides "backend" specialist
	svc := newStreamResolutionSvc(t, yakosRoot)

	captured, err := runStreamCapture(context.Background(), svc, Params{
		Agent:   "backend",
		Task:    "implement GET /v1/users",
		Project: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("RunStream with agent=backend: unexpected error: %v", err)
	}
	if captured.AgentName != "backend" {
		t.Errorf("AgentName = %q, want %q", captured.AgentName, "backend")
	}
}

// ---- Unit tests for the shared helper functions --------------------------------

// TestResolveAgent_DirectUnit tests resolveAgent directly, covering all three
// resolution branches without going through Service.RunStream.
func TestResolveAgent_DirectUnit(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	roster, err := agentscompose.Compose(yakosRoot, "")
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	t.Run("specialist found in roster", func(t *testing.T) {
		agent, err := resolveAgent(roster, "backend", yakosRoot, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agent.ID != "backend" {
			t.Errorf("ID = %q, want %q", agent.ID, "backend")
		}
	})

	t.Run("known runtime not in roster returns generic", func(t *testing.T) {
		for _, name := range []string{"claude", "codex", "agy", "gemini"} {
			name := name
			t.Run(name, func(t *testing.T) {
				agent, err := resolveAgent(roster, name, yakosRoot, "")
				if err != nil {
					t.Fatalf("resolveAgent(%q): unexpected error: %v", name, err)
				}
				if agent.ID != name {
					t.Errorf("generic agent ID = %q, want %q", agent.ID, name)
				}
			})
		}
	})

	t.Run("genuinely unknown name errors", func(t *testing.T) {
		_, err := resolveAgent(roster, "not-a-real-agent", yakosRoot, "")
		if err == nil {
			t.Fatal("expected error for unknown agent, got nil")
		}
		if !strings.Contains(err.Error(), "not found in composed set") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// TestResolveRuntime_DirectUnit tests resolveRuntime directly.
func TestResolveRuntime_DirectUnit(t *testing.T) {
	cases := []struct {
		agentName string
		override  string
		want      string
	}{
		// Override always wins.
		{"claude", "codex", "codex"},
		{"backend", "gemini", "gemini"},
		// Known runtime as agent name → use agent name.
		{"claude", "", "claude"},
		{"codex", "", "codex"},
		{"agy", "", "agy"},
		{"gemini", "", "gemini"},
		// Specialist agent, no override → default "claude".
		{"backend", "", "claude"},
		{"frontend", "", "claude"},
	}
	for _, tc := range cases {
		got := resolveRuntime(tc.agentName, tc.override)
		if got != tc.want {
			t.Errorf("resolveRuntime(%q, %q) = %q, want %q", tc.agentName, tc.override, got, tc.want)
		}
	}
}
