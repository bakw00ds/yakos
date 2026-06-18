package terminalmanager

// phase2_test.go — unit tests for ADR-0008 Phase 2 back-channel methods:
// SetAttachConn, SendInput, SendResize, and externalSession.sendToOwner.
//
// Tests:
//  1. SetAttachConn on unknown session returns ErrNotFound.
//  2. SendInput on unknown session returns ErrNotFound (owner-scoping: never
//     routes to daemon-owned entries).
//  3. SendResize on unknown session returns ErrNotFound.
//  4. SendInput writes a correct 0x10 frame to the attach conn.
//  5. SendResize writes a correct 0x11 frame to the attach conn.
//  6. Cross-session isolation: SendInput("A", ...) does NOT reach session B's conn.
//  7. SetAttachConn(nil) clears the conn; subsequent SendInput returns error.
//  8. sendToOwner frame format: length-prefix + tag + payload.

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// readFrameFromConn reads a single length-prefixed frame from conn.
// Layout: [ 2-byte BE total length ] [ tag byte ] [ payload ].
// Returns tag and payload.
func readFrameFromConn(t *testing.T, conn net.Conn) (tag byte, payload []byte) {
	t.Helper()
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		t.Fatalf("readFrameFromConn: read header: %v", err)
	}
	frameLen := binary.BigEndian.Uint16(hdr)
	if frameLen == 0 {
		return 0, nil
	}
	buf := make([]byte, frameLen)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("readFrameFromConn: read body: %v", err)
	}
	return buf[0], buf[1:]
}

// ---- SetAttachConn -----------------------------------------------------------

// TestSetAttachConn_UnknownSession verifies ErrNotFound on unknown sessionId.
func TestSetAttachConn_UnknownSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	err := mgr.SetAttachConn("nonexistent", c1)
	if err != ErrNotFound {
		t.Errorf("SetAttachConn on unknown session: want ErrNotFound, got %v", err)
	}
}

// TestSetAttachConn_RecordsAndClears verifies that SetAttachConn records the
// conn and that setting nil clears it so SendInput returns an error.
func TestSetAttachConn_RecordsAndClears(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "conn-lifecycle"
	if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
		t.Fatalf("register: %v", err)
	}

	daemonConn, startConn := net.Pipe()
	defer daemonConn.Close()
	defer startConn.Close()

	// Set conn.
	if err := mgr.SetAttachConn(sid, daemonConn); err != nil {
		t.Fatalf("SetAttachConn: %v", err)
	}

	// SendInput should reach startConn.
	done := make(chan struct{})
	go func() {
		defer close(done)
		tag, payload := readFrameFromConn(t, startConn)
		if tag != 0x10 {
			t.Errorf("tag = 0x%02x; want 0x10", tag)
		}
		if string(payload) != "hi" {
			t.Errorf("payload = %q; want %q", payload, "hi")
		}
	}()

	if err := mgr.SendInput(sid, []byte("hi")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for frame on startConn")
	}

	// Clear conn.
	if err := mgr.SetAttachConn(sid, nil); err != nil {
		t.Fatalf("SetAttachConn(nil): %v", err)
	}

	// SendInput should now fail with an error (conn is nil).
	if err := mgr.SendInput(sid, []byte("hi")); err == nil {
		t.Error("SendInput after SetAttachConn(nil): want error, got nil")
	}
}

// ---- SendInput owner-scoping ------------------------------------------------

// TestSendInput_OnlyRoutesToExternals verifies that SendInput returns
// ErrNotFound for daemon-owned sessions (entries), not just missing sessions.
// This is the owner-scoping safety property: browser input must never reach
// a daemon-owned PTY.
func TestSendInput_OnlyRoutesToExternals(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	// Inject a fake daemon-owned session directly into entries (bypassing
	// CreateSession, which requires a real PTY).
	const sid = "daemon-owned-fake"
	mgr.mu.Lock()
	mgr.entries[sid] = &session{id: sid}
	mgr.mu.Unlock()
	defer func() {
		mgr.mu.Lock()
		delete(mgr.entries, sid)
		mgr.mu.Unlock()
	}()

	// SendInput must NOT route to a daemon-owned session.
	err := mgr.SendInput(sid, []byte("x"))
	if err != ErrNotFound {
		t.Errorf("SendInput on daemon-owned session: want ErrNotFound, got %v", err)
	}
}

// TestSendInput_UnknownSession returns ErrNotFound.
func TestSendInput_UnknownSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	err := mgr.SendInput("gone", []byte("x"))
	if err != ErrNotFound {
		t.Errorf("SendInput on missing session: want ErrNotFound, got %v", err)
	}
}

// TestSendResize_UnknownSession returns ErrNotFound.
func TestSendResize_UnknownSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	err := mgr.SendResize("gone", 80, 24)
	if err != ErrNotFound {
		t.Errorf("SendResize on missing session: want ErrNotFound, got %v", err)
	}
}

// ---- Frame format proofs ----------------------------------------------------

// TestSendInput_FrameFormat verifies the exact wire format of a 0x10 frame.
// Layout: [2-byte BE total len][0x10][payload bytes]
func TestSendInput_FrameFormat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "input-frame-fmt"
	if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
		t.Fatalf("register: %v", err)
	}

	daemonConn, startConn := net.Pipe()
	defer daemonConn.Close()
	defer startConn.Close()
	if err := mgr.SetAttachConn(sid, daemonConn); err != nil {
		t.Fatalf("SetAttachConn: %v", err)
	}

	keystroke := []byte("echo hi\n")
	done := make(chan struct{})
	go func() {
		defer close(done)
		tag, payload := readFrameFromConn(t, startConn)
		if tag != 0x10 {
			t.Errorf("frame tag = 0x%02x; want 0x10", tag)
		}
		if string(payload) != string(keystroke) {
			t.Errorf("frame payload = %q; want %q", payload, keystroke)
		}
	}()

	if err := mgr.SendInput(sid, keystroke); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
}

// TestSendResize_FrameFormat verifies the exact wire format of a 0x11 frame.
// Layout: [2-byte BE total len=5][0x11][cols uint16 BE][rows uint16 BE]
func TestSendResize_FrameFormat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "resize-frame-fmt"
	if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
		t.Fatalf("register: %v", err)
	}

	daemonConn, startConn := net.Pipe()
	defer daemonConn.Close()
	defer startConn.Close()
	if err := mgr.SetAttachConn(sid, daemonConn); err != nil {
		t.Fatalf("SetAttachConn: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		tag, payload := readFrameFromConn(t, startConn)
		if tag != 0x11 {
			t.Errorf("frame tag = 0x%02x; want 0x11", tag)
		}
		if len(payload) < 4 {
			t.Errorf("resize payload too short: %d bytes", len(payload))
			return
		}
		cols := binary.BigEndian.Uint16(payload[0:2])
		rows := binary.BigEndian.Uint16(payload[2:4])
		if cols != 120 || rows != 40 {
			t.Errorf("resize: cols=%d rows=%d; want cols=120 rows=40", cols, rows)
		}
	}()

	if err := mgr.SendResize(sid, 120, 40); err != nil {
		t.Fatalf("SendResize: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
}

// TestSendInput_CrossSessionIsolation verifies that SendInput("session-A", data)
// does NOT reach session B's attach conn.
func TestSendInput_CrossSessionIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sidA = "cross-iso-A"
	const sidB = "cross-iso-B"
	for _, sid := range []string{sidA, sidB} {
		if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
			t.Fatalf("register %s: %v", sid, err)
		}
	}

	daemonConnA, startConnA := net.Pipe()
	daemonConnB, startConnB := net.Pipe()
	defer daemonConnA.Close()
	defer startConnA.Close()
	defer daemonConnB.Close()
	defer startConnB.Close()

	if err := mgr.SetAttachConn(sidA, daemonConnA); err != nil {
		t.Fatalf("SetAttachConn A: %v", err)
	}
	if err := mgr.SetAttachConn(sidB, daemonConnB); err != nil {
		t.Fatalf("SetAttachConn B: %v", err)
	}

	// Send to A only.
	receivedByB := make(chan struct{}, 1)
	go func() {
		// Set a short read deadline on B's start conn; if anything arrives, record it.
		startConnB.SetReadDeadline(time.Now().Add(200 * time.Millisecond)) //nolint:errcheck
		buf := make([]byte, 128)
		n, _ := startConnB.Read(buf)
		if n > 0 {
			receivedByB <- struct{}{}
		}
	}()

	// Drain A's output (expected).
	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		readFrameFromConn(t, startConnA)
	}()

	if err := mgr.SendInput(sidA, []byte("secret")); err != nil {
		t.Fatalf("SendInput A: %v", err)
	}

	select {
	case <-doneA:
		// Good: A received the frame.
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for frame on session A's conn")
	}

	// B must NOT have received anything.
	select {
	case <-receivedByB:
		t.Error("cross-session isolation violated: session B received input sent to session A")
	case <-time.After(300 * time.Millisecond):
		// Good: nothing arrived on B.
	}
}

// ---- Concurrent-writer integrity (Fix 1 — sendMu) ---------------------------

// TestSendInput_ConcurrentWritersNoInterleave verifies that N concurrent
// SendInput callers on the same session do not interleave length-prefixed
// frames on the wire.
//
// Without sendMu in sendToOwner, concurrent conn.Write calls can split and
// interleave the bytes of separate frames, producing a garbled byte stream
// that readDaemonFrames cannot parse correctly.  With sendMu, each
// Write call is serialized so every received frame is complete and
// well-formed.
//
// Run with -race to also catch the data race that exists without the mutex.
//
// To demonstrate the failure mode: remove the s.sendMu.Lock/Unlock pair
// wrapping conn.Write in externalSession.sendToOwner and re-run with -race.
// The race detector will flag a concurrent write, and the frame parser below
// will observe partial/interleaved frames.
func TestSendInput_ConcurrentWritersNoInterleave(t *testing.T) {
	const (
		numWriters    = 10
		payloadPerMsg = 128 // large enough that interleaving is detectable
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "concurrent-writers"
	if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
		t.Fatalf("register: %v", err)
	}

	daemonConn, startConn := net.Pipe()
	defer daemonConn.Close()
	defer startConn.Close()

	if err := mgr.SetAttachConn(sid, daemonConn); err != nil {
		t.Fatalf("SetAttachConn: %v", err)
	}

	// Reader goroutine: reads length-prefixed frames from startConn using the
	// same readFull + length-prefix parsing that readDaemonFrames uses in
	// start/pump_unix.go.  Records each frame's tag and payload.
	type receivedFrame struct {
		tag     byte
		payload []byte
	}
	received := make([]receivedFrame, 0, numWriters)
	readErrs := make([]error, 0)
	var readerMu sync.Mutex
	readerDone := make(chan struct{})

	go func() {
		defer close(readerDone)
		hdr := make([]byte, 2)
		for {
			if _, err := io.ReadFull(startConn, hdr); err != nil {
				// EOF after all writers finish and daemonConn is closed.
				return
			}
			frameLen := binary.BigEndian.Uint16(hdr)
			if frameLen == 0 {
				continue
			}
			buf := make([]byte, frameLen)
			if _, err := io.ReadFull(startConn, buf); err != nil {
				readerMu.Lock()
				readErrs = append(readErrs, fmt.Errorf("read body: %w", err))
				readerMu.Unlock()
				return
			}
			readerMu.Lock()
			received = append(received, receivedFrame{tag: buf[0], payload: buf[1:]})
			readerMu.Unlock()
		}
	}()

	// Build distinct payloads — each goroutine uses a repeated single byte so
	// interleaving between frames from different goroutines is detectable: a
	// payload for writer i is all 0x41+i bytes.
	var wg sync.WaitGroup
	for i := 0; i < numWriters; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := bytes.Repeat([]byte{byte(0x41 + i)}, payloadPerMsg)
			if err := mgr.SendInput(sid, payload); err != nil {
				t.Errorf("SendInput goroutine %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	// Close daemonConn to signal EOF to the reader goroutine.
	_ = daemonConn.Close()

	select {
	case <-readerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for reader goroutine to finish")
	}

	readerMu.Lock()
	defer readerMu.Unlock()

	// All frames must have been received without parse errors.
	if len(readErrs) > 0 {
		t.Errorf("frame parse errors (interleaving detected): %v", readErrs)
	}

	// Every frame must be a 0x10 keystroke frame with a pure payload:
	// all bytes in the payload must be the same value (no interleaving).
	if len(received) != numWriters {
		t.Errorf("received %d frames; want %d", len(received), numWriters)
	}
	for i, f := range received {
		if f.tag != 0x10 {
			t.Errorf("frame %d: tag = 0x%02x; want 0x10", i, f.tag)
			continue
		}
		if len(f.payload) != payloadPerMsg {
			t.Errorf("frame %d: payload len = %d; want %d (frame truncated — interleaving?)", i, len(f.payload), payloadPerMsg)
			continue
		}
		// Verify all payload bytes are identical (a mixed payload means interleaving).
		first := f.payload[0]
		for j, b := range f.payload {
			if b != first {
				t.Errorf("frame %d: payload byte %d = 0x%02x; want 0x%02x (interleaved payload)", i, j, b, first)
				break
			}
		}
	}
}
