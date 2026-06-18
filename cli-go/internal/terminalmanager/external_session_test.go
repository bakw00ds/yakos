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
