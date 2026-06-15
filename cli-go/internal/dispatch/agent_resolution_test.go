package dispatch

// Tests for generic-runtime agent resolution (the fix for "agent 'claude' not
// found in composed set" when the chat pane defaults to agent="claude").
//
// These tests exercise the resolution path through Service.Run using the
// package-internal setRunFn helper to avoid spawning real runtime processes.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// buildMinimalYakosRoot creates a minimal yakOS root with a single specialist
// agent (backend) in lib/agents/. Used to verify specialist resolution is
// unaffected by the generic-agent fallback.
func buildMinimalYakosRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	agentsDir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", agentsDir, err)
	}
	content := "---\nid: backend\nmodel: sonnet\n---\n\n## Purpose\n\nBackend specialist.\n"
	if err := os.WriteFile(filepath.Join(agentsDir, "backend.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write backend.md: %v", err)
	}
	return root
}

// newResolutionSvc builds a Service with a minimal yakOS root and an injected
// workspace.  Tests replace runFn via setRunFn before calling svc.Run.
func newResolutionSvc(t *testing.T, yakosRoot string) *Service {
	t.Helper()
	return NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: t.TempDir(),
	})
}

// TestGenericAgent_Claude verifies that agent="claude" (the console chat
// default) resolves to the generic claude agent rather than failing with
// "not found in composed set". The key assertion is that the call succeeds;
// runtime resolution to "claude" happens inside the real Run() which the fake
// runFn bypasses — that path is covered by runtime_test.go.
func TestGenericAgent_Claude(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newResolutionSvc(t, yakosRoot)

	var capturedReq Request
	setRunFn(t, func(_ context.Context, r Request) ([]byte, Result, error) {
		capturedReq = r
		return []byte("ok"), Result{ExitCode: 0, ModelChosenBy: "frontmatter", ModelResolved: "sonnet"}, nil
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "claude",
		Task:    "hello",
		Project: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Service.Run with agent=claude: unexpected error: %v", err)
	}
	// The request must carry the correct agent name through to runFn.
	if capturedReq.AgentName != "claude" {
		t.Errorf("captured AgentName = %q, want %q", capturedReq.AgentName, "claude")
	}
}

// TestGenericAgent_Codex verifies that agent="codex" resolves via the generic
// fallback without error. When a Params.Runtime override is given, the
// resolved runtime is passed through to runFn; that is also verified here.
func TestGenericAgent_Codex(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newResolutionSvc(t, yakosRoot)

	var capturedReq Request
	setRunFn(t, func(_ context.Context, r Request) ([]byte, Result, error) {
		capturedReq = r
		return []byte("ok"), Result{ExitCode: 0, ModelChosenBy: "frontmatter", ModelResolved: "sonnet"}, nil
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "codex",
		Task:    "hello",
		Project: t.TempDir(),
		// Explicit runtime override so Service.Run passes it through to runFn.
		Runtime: "codex",
	})
	if err != nil {
		t.Fatalf("Service.Run with agent=codex: unexpected error: %v", err)
	}
	// When Params.Runtime is set, it flows through Service.Run into Request.Runtime.
	if capturedReq.Runtime != "codex" {
		t.Errorf("Runtime = %q, want %q", capturedReq.Runtime, "codex")
	}
}

// TestGenericAgent_Gemini verifies the gemini bare-runtime case.
func TestGenericAgent_Gemini(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newResolutionSvc(t, yakosRoot)

	var capturedReq Request
	setRunFn(t, func(_ context.Context, r Request) ([]byte, Result, error) {
		capturedReq = r
		return []byte("ok"), Result{ExitCode: 0, ModelChosenBy: "frontmatter", ModelResolved: "sonnet"}, nil
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "gemini",
		Task:    "hello",
		Project: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Service.Run with agent=gemini: unexpected error: %v", err)
	}
	if capturedReq.AgentName != "gemini" {
		t.Errorf("AgentName = %q, want %q", capturedReq.AgentName, "gemini")
	}
}

// TestGenericAgent_Agy verifies that "agy" (the fourth known runtime) also
// resolves generically.
func TestGenericAgent_Agy(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newResolutionSvc(t, yakosRoot)

	setRunFn(t, func(_ context.Context, r Request) ([]byte, Result, error) {
		return []byte("ok"), Result{ExitCode: 0, ModelChosenBy: "frontmatter", ModelResolved: "sonnet"}, nil
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "agy",
		Task:    "hello",
		Project: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Service.Run with agent=agy: unexpected error: %v", err)
	}
}

// TestUnknownAgent_StillErrors verifies that a genuinely unknown agent name
// (not a runtime, not in the roster) still returns the "not found" error.
func TestUnknownAgent_StillErrors(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newResolutionSvc(t, yakosRoot)

	// No need to override runFn: the error should surface before Run() is called.
	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "nope",
		Task:    "hello",
		Project: t.TempDir(),
	})
	if err == nil {
		t.Fatal("Service.Run with unknown agent: expected error, got nil")
	}
	const wantSub = "not found in composed set"
	if !containsAny(err.Error(), wantSub) {
		t.Errorf("unexpected error message (want %q in): %v", wantSub, err)
	}
}

// TestSpecialistAgent_StillResolves verifies that a real specialist agent
// (backend, present in the roster) still resolves correctly and the generic
// fallback does not interfere.
func TestSpecialistAgent_StillResolves(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newResolutionSvc(t, yakosRoot)

	var capturedReq Request
	setRunFn(t, func(_ context.Context, r Request) ([]byte, Result, error) {
		capturedReq = r
		return []byte("ok"), Result{ExitCode: 0, ModelChosenBy: "frontmatter", ModelResolved: "sonnet"}, nil
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "backend",
		Task:    "implement GET /v1/users",
		Project: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Service.Run with agent=backend: unexpected error: %v", err)
	}
	if capturedReq.AgentName != "backend" {
		t.Errorf("AgentName = %q, want %q", capturedReq.AgentName, "backend")
	}
}

// containsAny is a simple substring check used for error message assertions.
func containsAny(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
