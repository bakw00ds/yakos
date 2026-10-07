package dispatch

import (
	"context"
	"testing"

	"github.com/bakw00ds/yakos/internal/runtime"
)

// routeChunks runs RunStream with a stubbed executor that emits one token, and
// returns the chunk types in the order onChunk saw them.
func routeChunks(t *testing.T, p Params) (types []string, route *RouteInfo) {
	t.Helper()
	logDir := isolatedLogDir(t)
	svc := NewService(ServiceConfig{
		YakosRoot:     buildFakeRoster(t, "chat-agent", "You are a helpful assistant."),
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})
	p.Agent, p.Task, p.Project = "chat-agent", "go on", logDir
	withStreamRunFn(func(ctx context.Context, req Request, a runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		onChunk(StreamChunk{Type: "token", Text: "x"})
		return Result{}, nil
	}, func() {
		if _, err := svc.RunStream(context.Background(), p, func(c StreamChunk) {
			types = append(types, c.Type)
			if c.Type == "route" {
				route = c.Route
			}
		}); err != nil {
			t.Fatal(err)
		}
	})
	return types, route
}

func TestRunStream_EmitRouteIsFirstChunk(t *testing.T) {
	types, route := routeChunks(t, Params{Runtime: "codex", EmitRoute: true})
	if len(types) != 2 || types[0] != "route" || types[1] != "token" {
		t.Fatalf("chunks = %v, want [route token]", types)
	}
	if route == nil || route.Runtime != "codex" || route.RuleID == "" || route.Reason == "" || route.Provider != "openai" {
		t.Fatalf("route = %+v", route)
	}
}

// A transport that forwards every chunk type (gRPC) must not get a frame it
// never asked for.
func TestRunStream_NoRouteChunkUnlessAsked(t *testing.T) {
	types, route := routeChunks(t, Params{Runtime: "codex"})
	if route != nil || len(types) != 1 || types[0] != "token" {
		t.Fatalf("chunks = %v route=%+v, want [token] and no route", types, route)
	}
}
