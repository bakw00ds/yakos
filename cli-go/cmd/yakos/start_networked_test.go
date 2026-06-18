// start_networked_test.go — unit tests for the networked-console helpers
// introduced to fix two regressions in `yakos start` v0.46.0.0:
//
//  1. A non-loopback --console-bind never set `networked = true`, so the
//     banner remained http:// and forwarding logic never fired.
//  2. --networked (or a non-loopback --console-bind) in interactive mode
//     silently came up loopback instead of failing with guidance.
//
// Both helpers are pure functions; no binary required.
package main

import (
	"strings"
	"testing"
)

// ---- networkedFromFlags -------------------------------------------------------

// TestNetworkedFromFlags_ExplicitFlag verifies that --networked sets networked.
func TestNetworkedFromFlags_ExplicitFlag(t *testing.T) {
	if !networkedFromFlags(true, "") {
		t.Error("networkedFromFlags(true, \"\") should be true")
	}
}

// TestNetworkedFromFlags_WildcardBind verifies that 0.0.0.0:<port> implies networked.
func TestNetworkedFromFlags_WildcardBind(t *testing.T) {
	if !networkedFromFlags(false, "0.0.0.0:7890") {
		t.Error("networkedFromFlags(false, \"0.0.0.0:7890\") should be true")
	}
}

// TestNetworkedFromFlags_RealIPBind verifies that a real IP bind implies networked.
func TestNetworkedFromFlags_RealIPBind(t *testing.T) {
	if !networkedFromFlags(false, "192.168.1.50:7890") {
		t.Error("networkedFromFlags(false, \"192.168.1.50:7890\") should be true")
	}
}

// TestNetworkedFromFlags_LoopbackBind verifies that 127.0.0.1 does NOT imply networked.
func TestNetworkedFromFlags_LoopbackBind(t *testing.T) {
	if networkedFromFlags(false, "127.0.0.1:7890") {
		t.Error("networkedFromFlags(false, \"127.0.0.1:7890\") should be false")
	}
}

// TestNetworkedFromFlags_LocalhostBind verifies that localhost does NOT imply networked.
func TestNetworkedFromFlags_LocalhostBind(t *testing.T) {
	if networkedFromFlags(false, "localhost:7890") {
		t.Error("networkedFromFlags(false, \"localhost:7890\") should be false")
	}
}

// TestNetworkedFromFlags_EmptyBind verifies that an empty bind does NOT imply networked.
func TestNetworkedFromFlags_EmptyBind(t *testing.T) {
	if networkedFromFlags(false, "") {
		t.Error("networkedFromFlags(false, \"\") should be false")
	}
}

// TestNetworkedFromFlags_ExplicitFlagWithLoopbackBind verifies that --networked
// wins even when --console-bind is loopback (explicit flag beats the bind addr).
func TestNetworkedFromFlags_ExplicitFlagWithLoopbackBind(t *testing.T) {
	if !networkedFromFlags(true, "127.0.0.1:7890") {
		t.Error("networkedFromFlags(true, \"127.0.0.1:7890\") should be true")
	}
}

// ---- validateNetworkedStartMode ----------------------------------------------

// TestValidateNetworkedStartMode_NetworkedNoREPL verifies no error when
// networked=true and noREPL=true (valid combination).
func TestValidateNetworkedStartMode_NetworkedNoREPL(t *testing.T) {
	if err := validateNetworkedStartMode(true, true); err != nil {
		t.Errorf("validateNetworkedStartMode(true, true) should be nil; got %v", err)
	}
}

// TestValidateNetworkedStartMode_NotNetworked verifies no error when not networked.
func TestValidateNetworkedStartMode_NotNetworked(t *testing.T) {
	if err := validateNetworkedStartMode(false, false); err != nil {
		t.Errorf("validateNetworkedStartMode(false, false) should be nil; got %v", err)
	}
}

// TestValidateNetworkedStartMode_NetworkedREPL verifies that
// networked=true and noREPL=false returns an error with guidance.
func TestValidateNetworkedStartMode_NetworkedREPL(t *testing.T) {
	err := validateNetworkedStartMode(true, false)
	if err == nil {
		t.Fatal("validateNetworkedStartMode(true, false) should return an error")
	}
	msg := err.Error()
	for _, want := range []string{
		"--no-repl",
		"--web",
		"Interactive REPL mode replaces the process",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q; got:\n%s", want, msg)
		}
	}
}
