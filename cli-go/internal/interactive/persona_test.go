package interactive_test

// persona_test.go — an interactive claude session whose agent persona is over the
// cap (runtime.MaxPersonaBytes) fails to start with the cap's error, before any
// process exists. The persona is an argv element (--append-system-prompt) of the
// session's claude command.

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
	"github.com/bakw00ds/yakos/internal/runtime"
)

func TestSessionStart_RefusesAnOversizedPersonaBeforeSpawn(t *testing.T) {
	// An empty PATH: if the cap stopped working the start would fail with "not
	// found" and the error assertion below would fail, never run a real claude.
	t.Setenv("PATH", t.TempDir())
	persona := strings.Repeat("x", runtime.MaxPersonaBytes+1)
	spawned := false
	s := interactive.NewSession(interactive.SessionParams{
		ConversationID:  "conv-persona",
		OwnerOperatorID: "alice",
		OnChunk:         func(dispatch.StreamChunk) {},
		CmdProvider: func() *exec.Cmd {
			spawned = true // the provider is called, as in production
			return runtime.InteractiveExecCmd("", persona, "", "")
		},
	})
	err := s.Start(context.Background())
	if !spawned {
		t.Fatal("the command provider was never asked for a command")
	}
	if !errors.Is(err, runtime.ErrPersonaTooLarge) {
		t.Fatalf("Start = %v, want an error wrapping ErrPersonaTooLarge", err)
	}
	if !strings.Contains(err.Error(), "shorten the agent definition") {
		t.Errorf("the error does not say what to do: %v", err)
	}
	if strings.Contains(err.Error(), "xxxxxxxxxx") {
		t.Errorf("the error echoes the persona: %.200s", err)
	}
	if s.IsClosed() {
		// A session that never started has nothing to close; it must not be
		// reported as a crashed one.
		t.Error("a session that failed to start was marked closed")
	}
}
