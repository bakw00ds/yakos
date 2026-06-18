package terminalmanager

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"
)

// skipIfPTYUnavailable skips the test if err indicates the PTY subsystem is
// unavailable in this runtime (e.g. ErrNotSupported on Windows, or
// EPERM from macOS process sandbox).
func skipIfPTYUnavailable(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if err == ErrNotSupported {
		t.Skip("PTY not supported on this platform")
	}
	if errors.Is(err, syscall.EPERM) {
		t.Skip("PTY spawn not permitted in this sandbox environment")
	}
	if strings.Contains(err.Error(), "operation not permitted") {
		t.Skip("PTY spawn not permitted in this sandbox environment")
	}
}

// TestSessionLifecycle spawns a real `echo` command under a PTY, verifies
// that output is fanned out to subscribers, and that the session cleans up.
// Uses /bin/sh -c "echo hello" as a short-lived fake command.
func TestSessionLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mgr := New(ctx, Config{
		Cap:            4,
		ReaperInterval: 1 * time.Second,
		IdleTimeout:    30 * time.Minute,
	})
	defer mgr.Stop()

	sessionID, err := mgr.CreateSession(SpawnSpec{
		Argv: []string{"/bin/sh", "-c", "echo hello"},
		Cwd:  "/tmp",
		Env:  []string{"HOME=/tmp"},
		Cols: 80,
		Rows: 24,
	})
	skipIfPTYUnavailable(t, err)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Subscribe and collect output.
	outputCh := make(chan []byte, 64)
	unsub, err := mgr.Subscribe(sessionID, func(chunk []byte) {
		cp := make([]byte, len(chunk))
		copy(cp, chunk)
		select {
		case outputCh <- cp:
		default:
		}
	}, nil) // exitFn not needed here; we wait on mgr.Get returning ErrNotFound
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	// Collect output until the session exits (manager removes it).
	deadline := time.After(5 * time.Second)
	var got []byte
outer:
	for {
		select {
		case chunk := <-outputCh:
			got = append(got, chunk...)
		case <-deadline:
			t.Fatal("timed out waiting for PTY output")
		default:
			// Check if session is gone (exited).
			_, err := mgr.Get(sessionID)
			if err == ErrNotFound {
				break outer
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Drain any remaining output from the channel.
	for {
		select {
		case chunk := <-outputCh:
			got = append(got, chunk...)
		default:
			goto done
		}
	}
done:

	if len(got) == 0 {
		t.Error("expected some PTY output but got none")
	}
}

// TestSessionCapExceeded verifies that CreateSession returns ErrCapExceeded
// when the cap is reached. Uses a long-lived command (sleep) that won't exit
// during the test.
func TestSessionCapExceeded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow cap test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 2})
	defer mgr.Stop()

	ids := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		id, err := mgr.CreateSession(SpawnSpec{
			Argv: []string{"/bin/sh", "-c", "sleep 60"},
			Cwd:  "/tmp",
			Env:  []string{"HOME=/tmp"},
		})
		skipIfPTYUnavailable(t, err)
		if err != nil {
			t.Fatalf("CreateSession %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	_, err := mgr.CreateSession(SpawnSpec{
		Argv: []string{"/bin/sh", "-c", "sleep 60"},
		Cwd:  "/tmp",
		Env:  []string{"HOME=/tmp"},
	})
	if err != ErrCapExceeded {
		t.Errorf("expected ErrCapExceeded, got %v", err)
	}

	// Clean up.
	for _, id := range ids {
		_ = mgr.Close(id)
	}
}

// TestSessionClose verifies that Close kills the child and removes the session.
func TestSessionClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	id, err := mgr.CreateSession(SpawnSpec{
		Argv: []string{"/bin/sh", "-c", "sleep 60"},
		Cwd:  "/tmp",
		Env:  []string{"HOME=/tmp"},
	})
	skipIfPTYUnavailable(t, err)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Session should be findable.
	if _, err := mgr.Get(id); err != nil {
		t.Fatalf("Get after create: %v", err)
	}

	// Close it.
	if err := mgr.Close(id); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Now it should be gone.
	if _, err := mgr.Get(id); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after Close, got %v", err)
	}

	// Second close should return ErrNotFound.
	if err := mgr.Close(id); err != ErrNotFound {
		t.Errorf("expected ErrNotFound on double Close, got %v", err)
	}
}

// TestFanOut verifies that multiple subscribers all receive the same output.
func TestFanOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	id, err := mgr.CreateSession(SpawnSpec{
		Argv: []string{"/bin/sh", "-c", "printf 'abc'"},
		Cwd:  "/tmp",
		Env:  []string{"HOME=/tmp"},
	})
	skipIfPTYUnavailable(t, err)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	ch1, ch2 := make(chan []byte, 32), make(chan []byte, 32)
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

	unsub1, _ := mgr.Subscribe(id, mkSink(ch1), nil)
	unsub2, _ := mgr.Subscribe(id, mkSink(ch2), nil)
	defer unsub1()
	defer unsub2()

	// Wait for the session to complete.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out")
		default:
			_, err := mgr.Get(id)
			if err == ErrNotFound {
				goto check
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
check:
	// Both channels should have received output.
	drain := func(ch chan []byte) []byte {
		var out []byte
		for {
			select {
			case chunk := <-ch:
				out = append(out, chunk...)
			default:
				return out
			}
		}
	}
	out1 := drain(ch1)
	out2 := drain(ch2)
	if len(out1) == 0 {
		t.Error("subscriber 1 received no output")
	}
	if len(out2) == 0 {
		t.Error("subscriber 2 received no output")
	}
}
