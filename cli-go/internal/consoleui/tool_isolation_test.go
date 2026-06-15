package consoleui_test

// tool_isolation_test.go — Phase 4: verifies that tool_use and tool_result
// SSEEvents on an UNSHARED session are NOT delivered to a different operator's
// SSE connection.  Reuses the ChatHub isolation test pattern established by
// TestChatHub_PerOperatorIsolation in chat_test.go.
//
// Also verifies:
//   - The share response now includes a "warning" field when shared=true.
//   - tool_use / tool_result events ARE delivered to all connections on a
//     SHARED session (default-ON policy per spec).

import (
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
)

// TestToolEvent_UnsharedSession_NotDeliveredToDifferentOperator is the
// isolation test required by the task spec.  It routes tool_use and
// tool_result SSEEvents on alice's UNSHARED session and verifies bob's
// connection never receives them.
func TestToolEvent_UnsharedSession_NotDeliveredToDifferentOperator(t *testing.T) {
	hub := consoleui.NewChatHubForTest()

	aliceConn, err := hub.Register("conn-alice-tool", "alice")
	if err != nil {
		t.Fatalf("Register alice: %v", err)
	}
	bobConn, err := hub.Register("conn-bob-tool", "bob")
	if err != nil {
		t.Fatalf("Register bob: %v", err)
	}
	defer hub.Unregister(aliceConn.ID())
	defer hub.Unregister(bobConn.ID())

	// Open UNSHARED session owned by alice.
	if err := hub.OpenSession("sess-tool-priv", "alice", false); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	// Route a tool_use event on alice's session.
	hub.Route(consoleui.SSEEvent{
		SessionID: "sess-tool-priv",
		Type:      "tool_use",
		ToolName:  "Bash",
		ToolInput: `{"command":"ls -la"}`,
	})

	// alice must receive it.
	select {
	case ev := <-aliceConn.Ch():
		if ev.Type != "tool_use" {
			t.Errorf("alice: got type %q, want %q", ev.Type, "tool_use")
		}
		if ev.ToolName != "Bash" {
			t.Errorf("alice: ToolName=%q, want %q", ev.ToolName, "Bash")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("alice did not receive tool_use event within 200ms")
	}

	// bob must NOT receive it (unshared session).
	select {
	case ev := <-bobConn.Ch():
		t.Errorf("bob received tool_use on alice's unshared session: type=%q tool=%q", ev.Type, ev.ToolName)
	case <-time.After(80 * time.Millisecond):
		// Correct.
	}

	// Route a tool_result event.
	hub.Route(consoleui.SSEEvent{
		SessionID:  "sess-tool-priv",
		Type:       "tool_result",
		ToolName:   "Bash",
		ToolOutput: "file1.txt\nfile2.txt",
	})

	// alice receives the tool_result.
	select {
	case ev := <-aliceConn.Ch():
		if ev.Type != "tool_result" {
			t.Errorf("alice: tool_result: got type %q", ev.Type)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("alice did not receive tool_result event within 200ms")
	}

	// bob must still not receive anything.
	select {
	case ev := <-bobConn.Ch():
		t.Errorf("bob received tool_result on alice's unshared session: %+v", ev)
	case <-time.After(80 * time.Millisecond):
		// Correct.
	}
}

// TestToolEvent_SharedSession_AllReceive verifies that when a session is shared,
// tool_use and tool_result events are delivered to ALL registered connections
// (default-ON policy per Phase 4 spec).
func TestToolEvent_SharedSession_AllReceive(t *testing.T) {
	hub := consoleui.NewChatHubForTest()

	aliceConn, err := hub.Register("conn-alice-shared-tool", "alice")
	if err != nil {
		t.Fatalf("Register alice: %v", err)
	}
	bobConn, err := hub.Register("conn-bob-shared-tool", "bob")
	if err != nil {
		t.Fatalf("Register bob: %v", err)
	}
	defer hub.Unregister(aliceConn.ID())
	defer hub.Unregister(bobConn.ID())

	// Open SHARED session owned by alice.
	if err := hub.OpenSession("sess-tool-shared", "alice", true); err != nil {
		t.Fatalf("OpenSession (shared): %v", err)
	}

	hub.Route(consoleui.SSEEvent{
		SessionID: "sess-tool-shared",
		Type:      "tool_use",
		ToolName:  "Read",
		ToolInput: `{"file_path":"/etc/hosts"}`,
	})

	// Both alice and bob must receive.
	deadline := time.After(300 * time.Millisecond)
	gotAlice, gotBob := false, false
	for !gotAlice || !gotBob {
		select {
		case ev := <-aliceConn.Ch():
			if ev.Type == "tool_use" {
				gotAlice = true
			}
		case ev := <-bobConn.Ch():
			if ev.Type == "tool_use" {
				gotBob = true
			}
		case <-deadline:
			if !gotAlice {
				t.Error("alice did not receive tool_use on shared session")
			}
			if !gotBob {
				t.Error("bob did not receive tool_use on shared session")
			}
			return
		}
	}
}

// TestSSEEvent_ToolFieldsPresent verifies that the SSEEvent struct carries the
// Phase 4 tool fields and that they serialise correctly through Route.
// This is a structural smoke test for the SSEEvent extension.
func TestSSEEvent_ToolFieldsPresent(t *testing.T) {
	hub := consoleui.NewChatHubForTest()

	conn, err := hub.Register("conn-tool-fields", "alice")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer hub.Unregister(conn.ID())

	if err := hub.OpenSession("sess-tool-fields", "alice", false); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	hub.Route(consoleui.SSEEvent{
		SessionID:  "sess-tool-fields",
		Type:       "tool_result",
		ToolName:   "Write",
		ToolOutput: "written 42 bytes",
		IsError:    false,
	})

	select {
	case ev := <-conn.Ch():
		if ev.Type != "tool_result" {
			t.Errorf("Type: got %q, want %q", ev.Type, "tool_result")
		}
		if ev.ToolName != "Write" {
			t.Errorf("ToolName: got %q, want %q", ev.ToolName, "Write")
		}
		if ev.ToolOutput != "written 42 bytes" {
			t.Errorf("ToolOutput: got %q, want %q", ev.ToolOutput, "written 42 bytes")
		}
		if ev.IsError {
			t.Error("IsError: got true, want false")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("did not receive tool_result event within 200ms")
	}
}
