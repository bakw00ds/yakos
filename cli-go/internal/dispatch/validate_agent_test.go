package dispatch

// validate_agent_test.go — unit tests for ValidateAgentName.
//
// Verifies the three resolution branches:
//  (a) Exact roster match → nil error.
//  (b) Bare known-runtime name (catch-all) → nil error.
//  (c) Genuinely unknown name → non-nil error.
//
// Also exercises the yakosRoot-empty path (only known-runtime check).

import (
	"strings"
	"testing"
)

func TestValidateAgentName_UnknownAgent_Errors(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	err := ValidateAgentName("general", yakosRoot, "")
	if err == nil {
		t.Fatal("ValidateAgentName(unknown): expected error, got nil")
	}
	const wantSub = "unknown agent"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error message %q does not contain %q", err.Error(), wantSub)
	}
}

func TestValidateAgentName_KnownRuntimes_Nil(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	for _, name := range []string{"claude", "codex", "agy", "gemini"} {
		if err := ValidateAgentName(name, yakosRoot, ""); err != nil {
			t.Errorf("ValidateAgentName(%q) with yakosRoot: unexpected error: %v", name, err)
		}
	}
}

func TestValidateAgentName_SpecialistInRoster_Nil(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t) // provides "backend" specialist
	if err := ValidateAgentName("backend", yakosRoot, ""); err != nil {
		t.Errorf("ValidateAgentName(backend): unexpected error: %v", err)
	}
}

func TestValidateAgentName_EmptyYakosRoot_KnownRuntime_Nil(t *testing.T) {
	// When yakosRoot is empty, only the known-runtime check fires.
	for _, name := range []string{"claude", "codex", "agy", "gemini"} {
		if err := ValidateAgentName(name, "", ""); err != nil {
			t.Errorf("ValidateAgentName(%q) empty yakosRoot: unexpected error: %v", name, err)
		}
	}
}

func TestValidateAgentName_EmptyYakosRoot_UnknownAgent_Passthrough(t *testing.T) {
	// When yakosRoot is empty the roster cannot be composed; the validator cannot
	// determine whether an unknown name is genuinely invalid, so it returns nil
	// (pass-through) rather than failing closed.  This prevents misconfigured
	// servers (no YakosRoot) from rejecting every dispatch.
	//
	// The real failure will come from RunStream itself when it tries to compose
	// the roster and resolve the agent — that error surfaces through the goroutine
	// failure path (SSE "error" frame + transcript error turn).
	err := ValidateAgentName("general", "", "")
	if err != nil {
		t.Errorf("ValidateAgentName(unknown, empty yakosRoot): expected nil (passthrough), got: %v", err)
	}
}
