package dispatch

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/runtime"
)

// RunStream's Result carries the router's record (K-142), like Run's.
func TestExecWithStreamingResultCarriesRouteFields(t *testing.T) {
	isolatedLogDir(t)
	a := &scriptAdapter{"codex", `printf '%s\n' '{"type":"item.completed","item":{"id":"a","type":"agent_message","text":"hi"}}'`}
	res, err := execWithStreaming(context.Background(),
		Request{AgentName: "chat-agent", Task: "t", Project: t.TempDir(), Runtime: "codex", ModelResolved: "m", ModelChosenBy: "frontmatter",
			RouteRule: "rule-1", RouteReason: "because", RouteClass: "cheap", PolicySHA: "abc123"},
		a, runtime.ChatDispatchRequest{UserText: "t"}, func(StreamChunk) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.RouteRule != "rule-1" || res.RouteReason != "because" || res.RouteClass != "cheap" || res.PolicySHA != "abc123" {
		t.Errorf("route fields lost: %+v", res)
	}
}

// A structured stream with an unparseable line logs a count, never the line.
func TestStreamSkippedLinesWarn(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	script := `printf '%s\n' '{"type":"turn.started"}' '{"type":"item.completed","item":{"id":"a","type":"agent_message","text":"hi"}}' '{"type":"turn.compl' 'SECRET-garbage'`
	if _, err := streamScript(t, &scriptAdapter{"codex", script}, func(StreamChunk) {}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "skipped unparseable output lines") || !strings.Contains(out, "skipped=2") || !strings.Contains(out, "runtime=codex") {
		t.Errorf("warning missing or wrong: %q", out)
	}
	if strings.Contains(out, "SECRET-garbage") {
		t.Error("a skipped line must never be echoed")
	}
}
