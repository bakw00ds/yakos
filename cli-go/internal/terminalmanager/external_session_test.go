package terminalmanager

import (
	"context"
	"testing"
	"time"
)

// TestExternalSession_RegisterAndSubscribe verifies that an externally-owned
// session can be registered, subscribed to, and that pushed output is delivered
// to subscribers.
func TestExternalSession_RegisterAndSubscribe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sessionID = "ext-test-001"
	if err := mgr.RegisterExternalSession(sessionID, "/workspace", []string{"claude"}); err != nil {
		t.Fatalf("RegisterExternalSession: %v", err)
	}

	// Session should appear in Get.
	meta, err := mgr.Get(sessionID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if meta.SessionID != sessionID {
		t.Errorf("meta.SessionID = %q; want %q", meta.SessionID, sessionID)
	}

	// Subscribe.
	outputCh := make(chan []byte, 16)
	exitCh := make(chan int, 1)
	unsub, err := mgr.Subscribe(sessionID, func(chunk []byte) {
		cp := make([]byte, len(chunk))
		copy(cp, chunk)
		select {
		case outputCh <- cp:
		default:
		}
	}, func(code int) {
		select {
		case exitCh <- code:
		default:
		}
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	// Push output.
	want := []byte("hello from start")
	if err := mgr.PushOutput(sessionID, want); err != nil {
		t.Fatalf("PushOutput: %v", err)
	}

	select {
	case got := <-outputCh:
		if string(got) != string(want) {
			t.Errorf("output = %q; want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pushed output")
	}

	// Push exit.
	if err := mgr.PushExit(sessionID, 42); err != nil {
		t.Fatalf("PushExit: %v", err)
	}

	select {
	case code := <-exitCh:
		if code != 42 {
			t.Errorf("exit code = %d; want 42", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for exit notification")
	}

	// After PushExit the session should be removed.
	if _, err := mgr.Get(sessionID); err != ErrNotFound {
		t.Errorf("Get after PushExit: want ErrNotFound, got %v", err)
	}
}

// TestExternalSession_FanOut verifies that multiple subscribers all receive the
// same pushed output.
func TestExternalSession_FanOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sessionID = "ext-fanout-001"
	if err := mgr.RegisterExternalSession(sessionID, "/workspace", nil); err != nil {
		t.Fatalf("RegisterExternalSession: %v", err)
	}

	ch1 := make(chan []byte, 16)
	ch2 := make(chan []byte, 16)
	mkSink := func(ch chan []byte) func([]byte) {
		return func(chunk []byte) {
			cp := make([]byte, len(chunk))
			copy(cp, chunk)
			select {
			case ch <- cp:
			default:
			}
		}
	}

	unsub1, _ := mgr.Subscribe(sessionID, mkSink(ch1), nil)
	unsub2, _ := mgr.Subscribe(sessionID, mkSink(ch2), nil)
	defer unsub1()
	defer unsub2()

	payload := []byte("broadcast")
	if err := mgr.PushOutput(sessionID, payload); err != nil {
		t.Fatalf("PushOutput: %v", err)
	}

	for i, ch := range []chan []byte{ch1, ch2} {
		select {
		case got := <-ch:
			if string(got) != string(payload) {
				t.Errorf("subscriber %d: got %q; want %q", i+1, got, payload)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("subscriber %d: timed out waiting for output", i+1)
		}
	}
}

// TestExternalSession_CloseExternal verifies that CloseExternal removes the
// session without sending an exit notification.
func TestExternalSession_CloseExternal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sessionID = "ext-close-001"
	if err := mgr.RegisterExternalSession(sessionID, "/workspace", nil); err != nil {
		t.Fatalf("RegisterExternalSession: %v", err)
	}

	exitCh := make(chan int, 1)
	_, err := mgr.Subscribe(sessionID, nil, func(code int) {
		select {
		case exitCh <- code:
		default:
		}
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := mgr.CloseExternal(sessionID); err != nil {
		t.Fatalf("CloseExternal: %v", err)
	}

	// Session should be gone.
	if _, err := mgr.Get(sessionID); err != ErrNotFound {
		t.Errorf("Get after CloseExternal: want ErrNotFound, got %v", err)
	}

	// No exit notification should have been sent (exitCh should be empty).
	select {
	case code := <-exitCh:
		t.Errorf("unexpected exit notification: code %d", code)
	case <-time.After(100 * time.Millisecond):
		// Expected: no notification on unclean close.
	}
}

// TestExternalSession_CapIncludesExternal verifies that external sessions count
// toward the combined session cap.
func TestExternalSession_CapIncludesExternal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 2})
	defer mgr.Stop()

	if err := mgr.RegisterExternalSession("ext-cap-1", "/w", nil); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := mgr.RegisterExternalSession("ext-cap-2", "/w", nil); err != nil {
		t.Fatalf("second register: %v", err)
	}
	if err := mgr.RegisterExternalSession("ext-cap-3", "/w", nil); err != ErrCapExceeded {
		t.Errorf("third register: want ErrCapExceeded, got %v", err)
	}
}

// TestExternalSession_ListIncludesExternal verifies that List returns
// externally-owned sessions alongside daemon-owned ones.
func TestExternalSession_ListIncludesExternal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	if err := mgr.RegisterExternalSession("list-ext-1", "/w1", []string{"claude"}); err != nil {
		t.Fatalf("RegisterExternalSession: %v", err)
	}

	sessions := mgr.List()
	if len(sessions) != 1 {
		t.Fatalf("List: want 1 session, got %d", len(sessions))
	}
	if sessions[0].SessionID != "list-ext-1" {
		t.Errorf("List: sessionId = %q; want %q", sessions[0].SessionID, "list-ext-1")
	}
}

// ---- scrollback / late-join tests -------------------------------------------

// TestScrollback_LateJoin verifies that a subscriber who calls Subscribe AFTER
// output has been pushed receives all previously-buffered chunks in order.
func TestScrollback_LateJoin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sessionID = "scroll-latejoin-001"
	if err := mgr.RegisterExternalSession(sessionID, "/workspace", nil); err != nil {
		t.Fatalf("RegisterExternalSession: %v", err)
	}

	chunks := [][]byte{
		[]byte("chunk-one"),
		[]byte("chunk-two"),
		[]byte("chunk-three"),
	}
	for _, c := range chunks {
		if err := mgr.PushOutput(sessionID, c); err != nil {
			t.Fatalf("PushOutput: %v", err)
		}
	}

	// Subscribe AFTER all output has been pushed.
	var received [][]byte
	unsub, err := mgr.Subscribe(sessionID, func(chunk []byte) {
		cp := make([]byte, len(chunk))
		copy(cp, chunk)
		received = append(received, cp)
	}, nil)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	if len(received) != len(chunks) {
		t.Fatalf("late-join replay: got %d chunks, want %d", len(received), len(chunks))
	}
	for i, want := range chunks {
		if string(received[i]) != string(want) {
			t.Errorf("chunk[%d] = %q; want %q", i, received[i], want)
		}
	}
}

// TestScrollback_CapEviction verifies that when total buffered bytes exceed
// scrollbackCap, the oldest chunks are dropped and the total stays at or below
// the cap.
func TestScrollback_CapEviction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sessionID = "scroll-cap-001"
	if err := mgr.RegisterExternalSession(sessionID, "/workspace", nil); err != nil {
		t.Fatalf("RegisterExternalSession: %v", err)
	}

	// Push chunks that together exceed scrollbackCap.
	// Use a chunk size that divides evenly so the math is predictable.
	const chunkSize = 128 * 1024 // 128 KB
	const numChunks = 6          // 6 × 128 KB = 768 KB > 512 KB cap
	chunk := make([]byte, chunkSize)
	for i := range chunk {
		chunk[i] = byte(i % 251)
	}
	for i := 0; i < numChunks; i++ {
		payload := make([]byte, chunkSize)
		copy(payload, chunk)
		payload[0] = byte(i) // distinguish chunks by first byte
		if err := mgr.PushOutput(sessionID, payload); err != nil {
			t.Fatalf("PushOutput %d: %v", i, err)
		}
	}

	// Retrieve the internal externalSession to inspect the ring buffer.
	mgr.mu.Lock()
	ext := mgr.externals[sessionID]
	mgr.mu.Unlock()
	if ext == nil {
		t.Fatal("externalSession not found in manager")
	}

	ext.sinksMu.Lock()
	totalBytes := ext.scrollbackBytes
	numRetained := len(ext.scrollback)
	var firstByte byte
	if numRetained > 0 {
		firstByte = ext.scrollback[0][0]
	}
	ext.sinksMu.Unlock()

	if totalBytes > scrollbackCap {
		t.Errorf("scrollbackBytes = %d; want <= %d (cap)", totalBytes, scrollbackCap)
	}
	// With 128 KB chunks and a 512 KB cap, at most 4 chunks fit.
	// The first two (indices 0 and 1) must have been evicted; the oldest
	// retained chunk's first byte should be >= 2.
	if numRetained == 0 {
		t.Fatal("scrollback is empty after pushes")
	}
	expectedOldest := byte(numChunks - numRetained)
	if firstByte != expectedOldest {
		t.Errorf("oldest retained chunk[0][0] = %d; want %d (eviction order)", firstByte, expectedOldest)
	}
}

// TestScrollback_Mixed verifies that a pre-subscribed listener (subscriber A)
// receives only live output, while a late-joining listener (subscriber B) receives
// replay bytes followed by the live continuation.
func TestScrollback_Mixed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sessionID = "scroll-mixed-001"
	if err := mgr.RegisterExternalSession(sessionID, "/workspace", nil); err != nil {
		t.Fatalf("RegisterExternalSession: %v", err)
	}

	chA := make(chan []byte, 32)
	unsubA, err := mgr.Subscribe(sessionID, func(chunk []byte) {
		cp := make([]byte, len(chunk))
		copy(cp, chunk)
		select {
		case chA <- cp:
		default:
		}
	}, nil)
	if err != nil {
		t.Fatalf("Subscribe A: %v", err)
	}
	defer unsubA()

	// Push two chunks before B subscribes.
	for _, payload := range [][]byte{[]byte("before-1"), []byte("before-2")} {
		if err := mgr.PushOutput(sessionID, payload); err != nil {
			t.Fatalf("PushOutput: %v", err)
		}
	}

	// Drain A's channel to confirm it received the live chunks.
	for _, want := range []string{"before-1", "before-2"} {
		select {
		case got := <-chA:
			if string(got) != want {
				t.Errorf("A got %q; want %q", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("A timed out waiting for %q", want)
		}
	}

	// Late-joining subscriber B.
	var bReceived []string
	unsubB, err := mgr.Subscribe(sessionID, func(chunk []byte) {
		bReceived = append(bReceived, string(chunk))
	}, nil)
	if err != nil {
		t.Fatalf("Subscribe B: %v", err)
	}
	defer unsubB()

	// B should have replayed the two pre-join chunks synchronously in subscribe.
	if len(bReceived) != 2 {
		t.Fatalf("B replay: got %d chunks, want 2", len(bReceived))
	}
	if bReceived[0] != "before-1" || bReceived[1] != "before-2" {
		t.Errorf("B replay order: %v", bReceived)
	}

	// Push a third chunk; both A and B should receive it.
	if err := mgr.PushOutput(sessionID, []byte("after-1")); err != nil {
		t.Fatalf("PushOutput after-1: %v", err)
	}
	select {
	case got := <-chA:
		if string(got) != "after-1" {
			t.Errorf("A live: got %q; want %q", got, "after-1")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("A timed out waiting for after-1")
	}
	// B's outputFn is synchronous (called inline by pushOutput), so bReceived
	// is updated before PushOutput returns.
	if len(bReceived) != 3 || bReceived[2] != "after-1" {
		t.Errorf("B live: want [before-1 before-2 after-1], got %v", bReceived)
	}
}

// TestScrollback_NoLossAtJoin is a sequential lock-ordering proof that a chunk
// pushed concurrently with Subscribe is neither lost nor double-delivered.
//
// Two interleavings are exercised explicitly:
//  1. pushOutput acquires sinksMu first → chunk lands in scrollback; subscribe
//     replays it; the new sink is not yet in s.sinks during the live fan-out so
//     there is no double delivery.
//  2. subscribe acquires sinksMu first → replay completes; sink is added; the
//     subsequent pushOutput delivers the chunk to the new sink via live fan-out
//     only (it is not in the scrollback yet during the replay).
func TestScrollback_NoLossAtJoin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	// Interleaving 1: push THEN subscribe.
	t.Run("push-then-subscribe", func(t *testing.T) {
		const sid = "no-loss-push-first"
		if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := mgr.PushOutput(sid, []byte("early")); err != nil {
			t.Fatalf("push: %v", err)
		}
		var got []string
		unsub, _ := mgr.Subscribe(sid, func(c []byte) { got = append(got, string(c)) }, nil)
		defer unsub()
		if len(got) != 1 || got[0] != "early" {
			t.Errorf("push-then-subscribe: got %v, want [early]", got)
		}
	})

	// Interleaving 2: subscribe THEN push.
	t.Run("subscribe-then-push", func(t *testing.T) {
		const sid = "no-loss-sub-first"
		if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
			t.Fatalf("register: %v", err)
		}
		ch := make(chan string, 4)
		unsub, _ := mgr.Subscribe(sid, func(c []byte) {
			cp := make([]byte, len(c))
			copy(cp, c)
			ch <- string(cp)
		}, nil)
		defer unsub()
		if err := mgr.PushOutput(sid, []byte("late")); err != nil {
			t.Fatalf("push: %v", err)
		}
		select {
		case got := <-ch:
			if got != "late" {
				t.Errorf("subscribe-then-push: got %q, want %q", got, "late")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("subscribe-then-push: timed out waiting for chunk")
		}
	})
}

// TestDaemonNeverSpawnsInExternalPath is a structural proof that the
// externally-owned session path does NOT call newSession (the daemon-owned PTY
// spawn).  We verify this by registering an external session and confirming that
// the daemon's entries map (daemon-owned PTY sessions) remains empty.
func TestDaemonNeverSpawnsInExternalPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	if err := mgr.RegisterExternalSession("no-spawn", "/workspace", []string{"claude"}); err != nil {
		t.Fatalf("RegisterExternalSession: %v", err)
	}

	mgr.mu.Lock()
	daemonEntries := len(mgr.entries)
	externalEntries := len(mgr.externals)
	mgr.mu.Unlock()

	if daemonEntries != 0 {
		t.Errorf("daemon-owned session count = %d; want 0 — daemon must not fork/exec in the external path", daemonEntries)
	}
	if externalEntries != 1 {
		t.Errorf("external session count = %d; want 1", externalEntries)
	}
}
