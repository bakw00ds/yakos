package dispatch

import (
	"context"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/runtime"
)

// resumeSeen runs RunStream and returns the chat request the seam received.
func resumeSeen(t *testing.T, p Params) (runtime.ChatDispatchRequest, error) {
	t.Helper()
	logDir := isolatedLogDir(t)
	svc := NewService(ServiceConfig{
		YakosRoot:     buildFakeRoster(t, "chat-agent", "You are a helpful assistant."),
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})
	p.Agent, p.Task, p.Project = "chat-agent", "go on", logDir
	var seen runtime.ChatDispatchRequest
	var runErr error
	withStreamRunFn(func(ctx context.Context, req Request, a runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		seen = chatReq
		return Result{}, nil
	}, func() {
		_, runErr = svc.RunStream(context.Background(), p, func(StreamChunk) {})
	})
	return seen, runErr
}

func TestRunStream_ResumePicksIDForResolvedRuntime(t *testing.T) {
	sessions := map[string]string{"claude": "sess-C", "codex": "thread-X", "agy": "conv-Y"}
	for _, rt := range []string{"claude", "codex", "agy"} {
		seen, err := resumeSeen(t, Params{Runtime: rt, NativeSessions: sessions})
		if err != nil {
			t.Fatalf("%s: %v", rt, err)
		}
		if seen.ResumeSessionID != sessions[rt] || seen.ResumeRuntime != rt {
			t.Errorf("%s: resume = %q/%q, want %q/%q", rt, seen.ResumeSessionID, seen.ResumeRuntime, sessions[rt], rt)
		}
	}
}

func TestRunStream_ResumeLegacyClaudeOnly(t *testing.T) {
	seen, err := resumeSeen(t, Params{Runtime: "claude", ResumeSessionID: "sess-C"})
	if err != nil || seen.ResumeSessionID != "sess-C" {
		t.Fatalf("claude legacy id lost: %q err=%v", seen.ResumeSessionID, err)
	}
	// The legacy field is claude's: another runtime must not receive it.
	seen, err = resumeSeen(t, Params{Runtime: "codex", ResumeSessionID: "sess-C"})
	if err != nil || seen.ResumeSessionID != "" {
		t.Errorf("codex got claude's id %q err=%v", seen.ResumeSessionID, err)
	}
	// Another runtime's entry is not used either.
	seen, _ = resumeSeen(t, Params{Runtime: "agy", NativeSessions: map[string]string{"codex": "thread-X"}})
	if seen.ResumeSessionID != "" {
		t.Errorf("agy got codex's id %q", seen.ResumeSessionID)
	}
}

func TestRunStream_NativeSessionsRefusesBadEntries(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"flag as id":      {"codex": "--dangerously-bypass"},
		"path":            {"agy": "../../etc/passwd"},
		"unknown runtime": {"gemini": "sess-A"},
		"too long":        {"codex": strings.Repeat("a", 129)},
	} {
		if _, err := resumeSeen(t, Params{Runtime: "codex", NativeSessions: m}); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
