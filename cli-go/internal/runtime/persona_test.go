package runtime

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// The persona travels in argv on the chat path (codex -c developer_instructions,
// agy -p prefix). A cap with a clear error replaces the operating system's bare
// "argument list too long".

func chatCmdFor(runtimeName string, persona string) *exec.Cmd {
	req := ChatDispatchRequest{Project: "", UserText: "hello", AgentSystemPrompt: persona}
	if runtimeName == "codex" {
		return (&CodexAdapter{}).ChatExecCmd(context.Background(), req)
	}
	return (&AgyAdapter{}).ChatExecCmd(context.Background(), req)
}

func TestChatPersona_AtTheCapIsAccepted(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	persona := strings.Repeat("x", MaxPersonaBytes)
	for _, rt := range []string{"codex", "agy"} {
		cmd := chatCmdFor(rt, persona)
		if cmd.Err != nil && errors.Is(cmd.Err, ErrPersonaTooLarge) {
			t.Fatalf("%s: a persona of exactly %d bytes must be accepted, got %v", rt, MaxPersonaBytes, cmd.Err)
		}
		found := false
		for _, a := range cmd.Args {
			if strings.Contains(a, persona) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: the persona must be in argv", rt)
		}
	}
}

func TestChatPersona_OverTheCapIsRefusedBeforeArgv(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	// No codex or agy can be found: if the cap ever stops working, Start fails
	// with "not found" and this test fails, instead of launching a real harness.
	t.Setenv("PATH", t.TempDir())
	persona := "SECRET-PERSONA-TEXT" + strings.Repeat("x", MaxPersonaBytes)
	for _, rt := range []string{"codex", "agy"} {
		t.Run(rt, func(t *testing.T) {
			cmd := chatCmdFor(rt, persona)
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

func TestChatPersona_NoPersonaIsNotAffected(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	for _, rt := range []string{"codex", "agy"} {
		if cmd := chatCmdFor(rt, ""); cmd.Err != nil && errors.Is(cmd.Err, ErrPersonaTooLarge) {
			t.Errorf("%s: no persona must be fine, got %v", rt, cmd.Err)
		}
	}
}
