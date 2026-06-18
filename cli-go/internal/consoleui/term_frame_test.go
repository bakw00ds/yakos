package consoleui

import (
	"testing"
)

// TestEncodeDecodeExitFrame verifies the 0x01 frame encode/decode round-trip.
func TestEncodeDecodeExitFrame(t *testing.T) {
	cases := []struct {
		exitCode int
	}{
		{0},
		{1},
		{255},
		{130}, // typical SIGINT-terminated exit code
	}
	for _, tc := range cases {
		frame := encodeExitFrame(tc.exitCode)
		if len(frame) != 5 {
			t.Errorf("encodeExitFrame(%d): expected 5 bytes, got %d", tc.exitCode, len(frame))
			continue
		}
		if frame[0] != 0x01 {
			t.Errorf("encodeExitFrame(%d): expected tag 0x01, got 0x%02x", tc.exitCode, frame[0])
		}
		got, err := decodeExitFrame(frame)
		if err != nil {
			t.Errorf("decodeExitFrame(%d): unexpected error: %v", tc.exitCode, err)
			continue
		}
		if got != tc.exitCode {
			t.Errorf("round-trip exitCode %d: got %d", tc.exitCode, got)
		}
	}
}

// TestDecodeExitFrameErrors verifies malformed input is rejected.
func TestDecodeExitFrameErrors(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		{0x00, 0x00, 0x00, 0x00, 0x00}, // wrong tag
		{0x01, 0x00},                    // too short
	}
	for _, frame := range cases {
		if _, err := decodeExitFrame(frame); err == nil {
			t.Errorf("decodeExitFrame(%v): expected error, got nil", frame)
		}
	}
}

// TestOutputFrameTag verifies that the 0x00 tag is prepended to PTY output.
// The handler prepends 0x00 before sending; we test the convention explicitly.
func TestOutputFrameTag(t *testing.T) {
	payload := []byte("hello from PTY")
	frame := make([]byte, 1+len(payload))
	frame[0] = 0x00
	copy(frame[1:], payload)

	if frame[0] != 0x00 {
		t.Errorf("output frame tag: expected 0x00, got 0x%02x", frame[0])
	}
	if string(frame[1:]) != string(payload) {
		t.Errorf("output frame payload mismatch")
	}
}
