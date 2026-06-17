package dispatch

// workdir_test.go: tests for WorkDirOverride plumbing (IDE Phase 3).
//
// These tests verify that Params.WorkDirOverride flows through Service.Run and
// Service.RunStream into the dispatch.Request (and on to the runtime adapter).
// They use the internal setRunFn / withStreamRunFn seams to capture the Request
// without invoking a real runtime.

import (
	"context"
	"testing"

	"github.com/bakw00ds/yakos/internal/runtime"
)

// TestWorkDirOverride_Run verifies that Params.WorkDirOverride flows into the
// Request passed to runFn when using Service.Run.
func TestWorkDirOverride_Run(t *testing.T) {
	var capturedReq Request
	setRunFn(t, func(ctx context.Context, req Request) ([]byte, Result, error) {
		capturedReq = req
		return nil, Result{}, nil
	})

	svc := NewService(ServiceConfig{
		YakosRoot:     "/yakos",
		WorkspaceRoot: "/p",
		OperatorID:    "op",
	})

	const overridePath = "/state/ide-worktrees/wt-sess-abc"
	_, _, err := svc.Run(context.Background(), Params{
		Agent:           "backend",
		Task:            "task",
		Project:         "/p",
		WorkDirOverride: overridePath,
	})
	if err != nil {
		t.Fatalf("Service.Run: unexpected error: %v", err)
	}

	if capturedReq.WorkDirOverride != overridePath {
		t.Errorf("WorkDirOverride in Request: got %q, want %q",
			capturedReq.WorkDirOverride, overridePath)
	}
	// Project must still be the original project path (not overwritten by WorkDirOverride).
	if capturedReq.Project != "/p" {
		t.Errorf("Project must not be changed by WorkDirOverride: got %q, want %q",
			capturedReq.Project, "/p")
	}
}

// TestWorkDirOverride_RunStream verifies that Params.WorkDirOverride flows into
// the Request and ChatDispatchRequest in the streaming path.
func TestWorkDirOverride_RunStream(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)

	var capturedReq Request
	var capturedChatReq runtime.ChatDispatchRequest

	const overridePath = "/state/ide-worktrees/wt-sess-xyz"

	withStreamRunFn(func(
		ctx context.Context,
		req Request,
		adapter runtime.Adapter,
		chatReq runtime.ChatDispatchRequest,
		onChunk func(StreamChunk),
	) (Result, error) {
		capturedReq = req
		capturedChatReq = chatReq
		return Result{ExitCode: 0, ModelChosenBy: "frontmatter", ModelResolved: "sonnet"}, nil
	}, func() {
		svc := NewService(ServiceConfig{
			YakosRoot:     yakosRoot,
			WorkspaceRoot: t.TempDir(),
			OperatorID:    "op",
		})

		_, err := svc.RunStream(context.Background(), Params{
			Agent:           "claude",
			Task:            "hello",
			Project:         t.TempDir(),
			WorkDirOverride: overridePath,
		}, func(StreamChunk) {})
		if err != nil {
			t.Fatalf("RunStream: unexpected error: %v", err)
		}

		if capturedReq.WorkDirOverride != overridePath {
			t.Errorf("WorkDirOverride in Request: got %q, want %q",
				capturedReq.WorkDirOverride, overridePath)
		}
		if capturedChatReq.WorkDirOverride != overridePath {
			t.Errorf("WorkDirOverride in ChatDispatchRequest: got %q, want %q",
				capturedChatReq.WorkDirOverride, overridePath)
		}
	})
}

// TestWorkDirOverride_EmptyWhenNotSet verifies that when WorkDirOverride is not
// set, the Request.WorkDirOverride is empty (no accidental propagation).
func TestWorkDirOverride_EmptyWhenNotSet(t *testing.T) {
	var capturedReq Request
	setRunFn(t, func(ctx context.Context, req Request) ([]byte, Result, error) {
		capturedReq = req
		return nil, Result{}, nil
	})

	svc := NewService(ServiceConfig{
		YakosRoot:     "/yakos",
		WorkspaceRoot: "/p",
		OperatorID:    "op",
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "backend",
		Task:    "task",
		Project: "/p",
		// WorkDirOverride intentionally not set
	})
	if err != nil {
		t.Fatalf("Service.Run: unexpected error: %v", err)
	}

	if capturedReq.WorkDirOverride != "" {
		t.Errorf("WorkDirOverride must be empty when not set; got %q", capturedReq.WorkDirOverride)
	}
}

// TestWorkDirOverride_AdapterHonorsOverride verifies that the adapter-level
// cmd.Dir is set to WorkDirOverride when non-empty, using the ClaudeAdapter
// directly (no dispatch stack needed).
func TestWorkDirOverride_AdapterHonorsOverride(t *testing.T) {
	const overridePath = "/tmp/some-worktree"

	adapter := &runtime.ClaudeAdapter{}
	req := runtime.DispatchRequest{
		Project:         "/real/project",
		AgentName:       "backend",
		AgentJSON:       `{"name":"backend"}`,
		Task:            "do something",
		WorkDirOverride: overridePath,
	}

	cmd := adapter.ExecCmd(context.Background(), req)
	if cmd.Dir != overridePath {
		t.Errorf("cmd.Dir: got %q, want %q (WorkDirOverride must set cmd.Dir)", cmd.Dir, overridePath)
	}
}

// TestWorkDirOverride_AdapterDefault verifies that when WorkDirOverride is
// empty, cmd.Dir is NOT set (zero value / inherits daemon cwd).
func TestWorkDirOverride_AdapterDefault(t *testing.T) {
	adapter := &runtime.ClaudeAdapter{}
	req := runtime.DispatchRequest{
		Project:   "/real/project",
		AgentName: "backend",
		AgentJSON: `{"name":"backend"}`,
		Task:      "do something",
		// WorkDirOverride intentionally empty
	}

	cmd := adapter.ExecCmd(context.Background(), req)
	if cmd.Dir != "" {
		t.Errorf("cmd.Dir must be empty when WorkDirOverride is not set; got %q", cmd.Dir)
	}
}
