package runtime

// claude_persona_test.go — the claude chat paths carry the agent persona in argv
// too (--append-system-prompt), so they get the same cap codex and agy chat have
// (MaxPersonaBytes, ErrPersonaTooLarge): a persona over it is refused before any
// argv is built, with an error that says what to shorten, instead of the
// operating system's bare "argument list too long" from a spawn that already
// started.
//
// The refusal tests put an EMPTY directory on PATH. If the cap ever stops
// working, Start fails with "not found" and the test fails, instead of the real
// claude CLI being launched with a 64 KiB prompt.

import (
	"context"
	"errors"
	"os/exec"
	goruntime "runtime"
	"strings"
	"testing"
)

// claudeChatCmd and claudeInteractiveCmd are the two builders that put a persona
// on a claude command line.
func claudeChatCmd(persona string) *exec.Cmd {
	return (&ClaudeAdapter{}).ChatExecCmd(context.Background(),
		ChatDispatchRequest{Project: "", UserText: "hello", AgentSystemPrompt: persona})
}

func claudeInteractiveCmd(persona string) *exec.Cmd {
	return InteractiveExecCmd("", persona, "", "")
}

var claudePersonaBuilders = []struct {
	name  string
	build func(persona string) *exec.Cmd
}{
	{"ChatExecCmd", claudeChatCmd},
	{"InteractiveExecCmd", claudeInteractiveCmd},
}

// isolatePersonaTest keeps these tests off the real claude and the real home.
func isolatePersonaTest(t *testing.T) {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("argv and PATH semantics differ")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
}

func TestClaudePersona_AtTheCapIsAccepted(t *testing.T) {
	isolatePersonaTest(t)
	persona := strings.Repeat("x", MaxPersonaBytes)
	for _, b := range claudePersonaBuilders {
		cmd := b.build(persona)
		// cmd.Err may hold the PATH lookup failure (there is no claude here); it
		// must not be the cap.
		if errors.Is(cmd.Err, ErrPersonaTooLarge) {
			t.Fatalf("%s: a persona of exactly %d bytes must be accepted, got %v", b.name, MaxPersonaBytes, cmd.Err)
		}
		found := false
		for i, a := range cmd.Args {
			if a == "--append-system-prompt" && i+1 < len(cmd.Args) && cmd.Args[i+1] == persona {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: the persona must be passed as --append-system-prompt", b.name)
		}
	}
}

func TestClaudePersona_OverTheCapIsRefusedBeforeArgv(t *testing.T) {
	isolatePersonaTest(t)
	persona := "SECRET-PERSONA-TEXT" + strings.Repeat("x", MaxPersonaBytes)
	for _, b := range claudePersonaBuilders {
		t.Run(b.name, func(t *testing.T) {
			cmd := b.build(persona)
			err := cmd.Start()
			if err == nil {
				_ = cmd.Process.Kill()
				t.Fatal("a persona over the cap must not start a process")
			}
			if !errors.Is(err, ErrPersonaTooLarge) {
				t.Fatalf("want ErrPersonaTooLarge, got %v", err)
			}
			msg := err.Error()
			for _, want := range []string{"agent's system prompt is", "65555", "at most 65536", "shorten the agent definition"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the error should say %q, got %q", want, msg)
				}
			}
			if strings.Contains(msg, "SECRET-PERSONA-TEXT") {
				t.Errorf("the error must not echo the persona: %q", msg)
			}
			for _, a := range cmd.Args {
				if strings.Contains(a, "SECRET-PERSONA-TEXT") {
					t.Errorf("the rejected persona must not be in argv: %q", cmd.Args)
				}
			}
		})
	}
}

func TestClaudePersona_NoPersonaIsNotAffected(t *testing.T) {
	isolatePersonaTest(t)
	for _, b := range claudePersonaBuilders {
		if cmd := b.build(""); errors.Is(cmd.Err, ErrPersonaTooLarge) {
			t.Errorf("%s: no persona must be fine, got %v", b.name, cmd.Err)
		}
	}
}
