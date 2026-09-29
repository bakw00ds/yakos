package dispatch

import (
	"context"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/runtime"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// K-86 item 4: dispatch.started / dispatch.finished carry the agent name and
// project path. They must be published with the dispatching operator as the
// event owner so the WS fan-out can scope them; a plain Bus.Publish stamps an
// empty owner and (before this fix) broadcast them to every operator.

func ownersOfDispatchEvents(bus *wsbus.Bus) (owners []string, topics []string) {
	for _, ev := range bus.History(0) {
		if ev.Topic != wsbus.TopicDispatchStarted && ev.Topic != wsbus.TopicDispatchFinished {
			continue
		}
		topics = append(topics, ev.Topic)
		if ev.Meta == nil {
			owners = append(owners, "<nil-meta>")
			continue
		}
		owners = append(owners, ev.Meta.OwnerOperatorID)
	}
	return
}

func TestRun_DispatchBusEvents_CarryOperatorAsOwner(t *testing.T) {
	logDir := isolatedLogDir(t)
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	svc := NewService(ServiceConfig{YakosRoot: logDir, WorkspaceRoot: logDir, Bus: bus, OperatorID: "svc-default"})

	setRunFn(t, func(ctx context.Context, req Request) ([]byte, Result, error) {
		return []byte("ok"), Result{ExitCode: 0}, nil
	})
	if _, _, err := svc.Run(context.Background(), Params{Agent: "a", Task: "t", Project: logDir, OperatorID: "alice"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	owners, topics := ownersOfDispatchEvents(bus)
	if len(owners) != 2 {
		t.Fatalf("got %d dispatch bus events (%v); want 2", len(owners), topics)
	}
	for i, o := range owners {
		if o != "alice" {
			t.Errorf("SECURITY: %s event owner=%q; want %q (events without an owner broadcast agent+project to every operator)", topics[i], o, "alice")
		}
	}
}

func TestRunStream_DispatchBusEvents_CarryOperatorAsOwner(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "System prompt.")
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	svc := NewService(ServiceConfig{YakosRoot: yakosRoot, WorkspaceRoot: logDir, Bus: bus, OperatorID: "svc-default"})

	withStreamRunFn(func(ctx context.Context, req Request, adapter runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return Result{ExitCode: 0}, nil
	}, func() {
		if _, err := svc.RunStream(context.Background(), Params{Agent: "chat-agent", Task: "t", Project: logDir, OperatorID: "bob"}, func(StreamChunk) {}); err != nil {
			t.Fatalf("RunStream: %v", err)
		}
	})
	owners, topics := ownersOfDispatchEvents(bus)
	if len(owners) != 2 {
		t.Fatalf("got %d dispatch bus events (%v); want 2", len(owners), topics)
	}
	for i, o := range owners {
		if !strings.EqualFold(o, "bob") {
			t.Errorf("SECURITY: %s event owner=%q; want %q", topics[i], o, "bob")
		}
	}
}
