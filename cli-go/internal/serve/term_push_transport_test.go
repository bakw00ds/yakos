//go:build !windows

package serve

// term_push_transport_test.go — unit tests for the T2 push transport.
//
// runPushTransport is the ADR-0008 Phase 1 T2 daemon-side receive path:
// start sends length-prefixed 0x00/0x01 frames, the daemon calls
// Manager.PushOutput / Manager.PushExit and fans output to browser subscribers.
//
// Tests use a fake push manager to drive the transport in-process.

import (
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"
)

// fakePushManager is a test double for the push transport's manager calls.
type fakePushManager struct {
	mu sync.Mutex

	pushOutputCalls     []pushCall
	pushExitCalls       []exitCall
	closeExternalCalled bool

	// notFound causes CloseExternal / PushOutput / PushExit to return ErrNotFound.
	notFound bool
}

type pushCall struct {
	sessionID string
	data      []byte
}

type exitCall struct {
	sessionID string
	code      int
}

type fakePushErr struct{ msg string }

func (e *fakePushErr) Error() string { return e.msg }

func (f *fakePushManager) PushOutput(sessionID string, chunk []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notFound {
		return &fakePushErr{"terminalmanager: session not found"}
	}
	cp := make([]byte, len(chunk))
	copy(cp, chunk)
	f.pushOutputCalls = append(f.pushOutputCalls, pushCall{sessionID: sessionID, data: cp})
	return nil
}

func (f *fakePushManager) PushExit(sessionID string, code int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notFound {
		return &fakePushErr{"terminalmanager: session not found"}
	}
	f.pushExitCalls = append(f.pushExitCalls, exitCall{sessionID: sessionID, code: code})
	return nil
}

func (f *fakePushManager) CloseExternal(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeExternalCalled = true
	return nil
}

// runPushTransportFake mirrors runPushTransport but accepts *fakePushManager
// instead of *terminalmanager.Manager so tests don't require a real Manager.
func runPushTransportFake(conn net.Conn, sessionID string, mgr *fakePushManager) {
	defer func() {
		conn.Close()
		_ = mgr.CloseExternal(sessionID)
	}()

	hdr := make([]byte, 2)
	for {
		if err := readFull(conn, hdr); err != nil {
			break
		}
		frameLen := binary.BigEndian.Uint16(hdr)
		if frameLen == 0 {
			continue
		}
		buf := make([]byte, frameLen)
		if err := readFull(conn, buf); err != nil {
			break
		}
		tag := buf[0]
		payload := buf[1:]

		switch tag {
		case 0x00: // PTY output chunk
			if len(payload) > 0 {
				if err := mgr.PushOutput(sessionID, payload); err != nil {
					return
				}
			}
		case 0x01: // exit notification
			code := 0
			if len(payload) >= 4 {
				code = int(binary.BigEndian.Uint32(payload[0:4]))
			}
			_ = mgr.PushExit(sessionID, code)
			return
		}
	}
}

// ---- tests ------------------------------------------------------------------

// TestPushTransport_OutputChunkDelivered verifies that a 0x00 frame from start
// causes PushOutput to be called with the correct payload.
func TestPushTransport_OutputChunkDelivered(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	mgr := &fakePushManager{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPushTransportFake(server, "push-sess-1", mgr)
	}()

	payload := []byte("hello from start PTY")
	if err := writeFrameToConn(client, 0x00, payload); err != nil {
		t.Fatalf("write 0x00 frame: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		mgr.mu.Lock()
		n := len(mgr.pushOutputCalls)
		mgr.mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for PushOutput to be called")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	mgr.mu.Lock()
	call := mgr.pushOutputCalls[0]
	mgr.mu.Unlock()
	if string(call.data) != string(payload) {
		t.Errorf("PushOutput got %q; want %q", call.data, payload)
	}

	client.Close()
	<-done
}

// TestPushTransport_ExitFrameCallsPushExit verifies that a 0x01 frame from start
// causes PushExit to be called with the correct exit code, and the transport exits.
func TestPushTransport_ExitFrameCallsPushExit(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	mgr := &fakePushManager{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPushTransportFake(server, "push-sess-2", mgr)
	}()

	exitPayload := make([]byte, 4)
	binary.BigEndian.PutUint32(exitPayload, 7)
	if err := writeFrameToConn(client, 0x01, exitPayload); err != nil {
		t.Fatalf("write 0x01 frame: %v", err)
	}

	// Transport should exit after the 0x01 frame.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for transport to exit after 0x01 frame")
	}

	mgr.mu.Lock()
	calls := mgr.pushExitCalls
	mgr.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("PushExit called %d times; want 1", len(calls))
	}
	if calls[0].code != 7 {
		t.Errorf("PushExit code = %d; want 7", calls[0].code)
	}
}

// TestPushTransport_MultipleOutputChunks verifies that multiple 0x00 frames
// are delivered in order via PushOutput.
func TestPushTransport_MultipleOutputChunks(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	mgr := &fakePushManager{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPushTransportFake(server, "push-sess-3", mgr)
	}()

	chunks := [][]byte{[]byte("line1\n"), []byte("line2\n"), []byte("line3\n")}
	for _, c := range chunks {
		if err := writeFrameToConn(client, 0x00, c); err != nil {
			t.Fatalf("write frame: %v", err)
		}
	}

	deadline := time.After(2 * time.Second)
	for {
		mgr.mu.Lock()
		n := len(mgr.pushOutputCalls)
		mgr.mu.Unlock()
		if n >= len(chunks) {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out: got %d PushOutput calls; want %d", len(mgr.pushOutputCalls), len(chunks))
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	mgr.mu.Lock()
	got := mgr.pushOutputCalls
	mgr.mu.Unlock()
	for i, c := range chunks {
		if string(got[i].data) != string(c) {
			t.Errorf("chunk %d: got %q; want %q", i, got[i].data, c)
		}
	}

	client.Close()
	<-done
}

// TestPushTransport_ConnDisconnectCallsCloseExternal verifies that when the
// connection drops without a clean 0x01 frame, CloseExternal is called so
// the session is not left dangling in the manager.
func TestPushTransport_ConnDisconnectCallsCloseExternal(t *testing.T) {
	client, server := net.Pipe()

	mgr := &fakePushManager{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPushTransportFake(server, "push-sess-4", mgr)
	}()

	// Close client without sending an exit frame.
	client.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for transport to exit after conn close")
	}

	mgr.mu.Lock()
	closed := mgr.closeExternalCalled
	mgr.mu.Unlock()
	if !closed {
		t.Error("CloseExternal was not called on unclean disconnect")
	}
}
