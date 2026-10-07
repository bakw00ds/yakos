package dispatch

import (
	"context"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/runtime"
)

func TestKnowledgePersona(t *testing.T) {
	p := Params{Knowledge: "BLOCK"}
	cases := []struct {
		name    string
		rt      string
		p       Params
		resumed bool
		want    string
	}{
		{"claude never swapped", "claude", p, false, "BODY"},
		{"claude resumed never swapped", "claude", p, true, "BODY"},
		{"codex gets block", "codex", p, false, "BLOCK"},
		{"codex gets block on resume", "codex", p, true, "BLOCK"},
		{"agy first turn", "agy", p, false, "BLOCK"},
		{"agy resumed native session", "agy", p, true, ""},
		{"no knowledge keeps body", "codex", Params{}, false, "BODY"},
		{"unknown runtime keeps body", "other", p, false, "BODY"},
	}
	for _, c := range cases {
		if got := knowledgePersona(c.rt, c.p, "BODY", c.resumed); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// A claude turn with a knowledge block still carries the agent body, byte for
// byte, in AgentSystemPrompt: the claude argv does not change.
func TestRunStreamClaudeIgnoresKnowledge(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "You are a helpful assistant.")
	svc := NewService(ServiceConfig{YakosRoot: yakosRoot, WorkspaceRoot: logDir, OperatorID: "test-op"})
	var got string
	withStreamRunFn(func(ctx context.Context, req Request, a runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		got = chatReq.AgentSystemPrompt
		return Result{}, nil
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{
			Agent: "chat-agent", Task: "hi", Project: logDir, Runtime: "claude", Knowledge: "KNOWLEDGE-BLOCK",
		}, func(StreamChunk) {})
		if err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(got, "KNOWLEDGE-BLOCK") || !strings.Contains(got, "helpful assistant") {
		t.Fatalf("claude persona changed: %q", got)
	}
}
