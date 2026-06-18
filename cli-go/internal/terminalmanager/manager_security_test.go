package terminalmanager

// manager_security_test.go — regression tests for ADR-0008 Phase 2 security fixes.
//
// Fix 1 (HIGH): uint16 overflow panic in SendInput.
//
// When len(data) == 65535, uint16(1+65535) == 0 and make([]byte, 2+0) == make([]byte,2);
// then frame[2] panics with an index-out-of-bounds.  The fix adds a maxOwnerFramePayload
// bound check before the frame is constructed, returning an error instead of panicking.
//
// Tests:
//
//   - TestSendInput_OverMaxPayloadRejected: payload > maxOwnerFramePayload returns error.
//   - TestSendInput_ExactMaxPayloadAccepted: payload == maxOwnerFramePayload passes bound check
//     (may fail later for other reasons, e.g. no conn — that is not what we test here).
//   - TestSendInput_BoundaryFuzz: sizes 65530, 65534, 65535, 65536 all return an error
//     and none panic.

import (
	"context"
	"testing"
	"time"
)

// TestSendInput_OverMaxPayloadRejected verifies that a payload one byte over the
// limit is rejected with an error and does not panic.
func TestSendInput_OverMaxPayloadRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "sec-over-max"
	if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
		t.Fatalf("register: %v", err)
	}

	payload := make([]byte, maxOwnerFramePayload+1)
	err := mgr.SendInput(sid, payload)
	if err == nil {
		t.Fatal("SendInput with payload > maxOwnerFramePayload: want error, got nil")
	}
}

// TestSendInput_ExactMaxPayloadAccepted verifies that a payload of exactly
// maxOwnerFramePayload bytes passes the size-bound check.  The call may still
// return an error (e.g. no attach conn set), but that error must NOT be the
// size-bound error, and the call must not panic.
func TestSendInput_ExactMaxPayloadAccepted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "sec-exact-max"
	if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
		t.Fatalf("register: %v", err)
	}

	payload := make([]byte, maxOwnerFramePayload)

	// Must not panic. Error (if any) should be about conn, not about payload size.
	err := mgr.SendInput(sid, payload)
	// If there's an error it should be something other than "payload too large".
	// We verify by checking it's not the size-error string; a nil error is also fine.
	if err != nil {
		errStr := err.Error()
		const sizeErrSubstr = "payload too large"
		if contains(errStr, sizeErrSubstr) {
			t.Errorf("SendInput with exactly maxOwnerFramePayload bytes: got size-bound error %q; want nil or conn error", errStr)
		}
	}
}

// TestSendInput_BoundaryFuzz verifies that payloads at and above the uint16
// overflow boundary all return errors and do not panic.
func TestSendInput_BoundaryFuzz(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := New(ctx, Config{Cap: 4})
	defer mgr.Stop()

	const sid = "sec-fuzz"
	if err := mgr.RegisterExternalSession(sid, "/w", nil); err != nil {
		t.Fatalf("register: %v", err)
	}

	// All these sizes must produce an error (not panic).
	// They are all > maxOwnerFramePayload (32 KB) and near the uint16 boundary.
	for _, size := range []int{65530, 65534, 65535, 65536} {
		size := size
		t.Run("size"+itoa(size), func(t *testing.T) {
			payload := make([]byte, size)
			err := mgr.SendInput(sid, payload)
			if err == nil {
				t.Errorf("SendInput(%d bytes): want error, got nil", size)
			}
		})
	}
}

// contains is a minimal strings.Contains without importing strings.
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// itoa converts a non-negative int to its decimal string representation
// without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
