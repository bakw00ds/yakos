package runtime

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// The persona travels in argv on the chat path (codex -c developer_instructions,
// agy -p prefix). A cap with a clear error replaces the operating system's bare
// "argument list too long".

// personaEchoMarker is plain text that no escaping changes, so it shows up in a
// refusal or in argv whether the persona is echoed as given or as encoded.
const personaEchoMarker = "SECRET-PERSONA-TEXT"

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
	persona := personaEchoMarker + strings.Repeat("x", MaxPersonaBytes)
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
			if strings.Contains(msg, personaEchoMarker) {
				t.Errorf("the error must not echo the persona: %q", msg)
			}
			// Plain text does not grow when escaped, so this is the raw refusal.
			if strings.Contains(msg, "once escaped") {
				t.Errorf("a persona over the raw cap is refused as such, not for its escaped size: %q", msg)
			}
			for _, a := range cmd.Args {
				if strings.Contains(a, personaEchoMarker) {
					t.Errorf("the rejected persona must not be in argv: %q", cmd.Args)
				}
			}
		})
	}
}

// linuxArgStrlen is the longest single argv element Linux accepts (MAX_ARG_STRLEN,
// 32 pages). An exec with a longer one fails with a bare "argument list too long".
const linuxArgStrlen = 131072

// encodedGrowth lists characters codex's TOML encoding of -c developer_instructions
// makes longer, and how many bytes each takes once written: a quote, a backslash
// and a newline become two-character escapes, any other control character
// \u00XX.
var encodedGrowth = []struct {
	name, unit   string
	encodedBytes int
}{
	{"quote-heavy", `"`, 2},
	{"backslash-heavy", `\`, 2},
	{"newline-heavy", "\n", 2},
	{"control-heavy", "\x01", 6},
	{"delete-heavy", "\x7f", 6},
}

// TestChatPersona_CodexEncodedLengthIsCapped: a persona under the raw cap can still
// be over the argument limit once it is encoded. 64 KiB of quotes is 128 KiB of
// argument, 64 KiB of control characters is 384 KiB, and the exec failed with the
// operating system's bare error instead of the clear one. The encoded form is now
// held to the same cap, before argv is built.
func TestChatPersona_CodexEncodedLengthIsCapped(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	// No codex on PATH: if the check ever stops working, Start fails with "not
	// found" and the test fails, instead of launching a real harness.
	t.Setenv("PATH", t.TempDir())
	for _, g := range encodedGrowth {
		t.Run(g.name, func(t *testing.T) {
			// The table must describe the real encoding, or the boundaries below mean nothing.
			if got := len(tomlString(g.unit)) - 2; got != g.encodedBytes {
				t.Fatalf("%q encodes to %d bytes, the table says %d", g.unit, got, g.encodedBytes)
			}
			fits := MaxPersonaBytes / g.encodedBytes // encodes to at most the cap

			// At the boundary: accepted, in argv, and under the Linux limit.
			atCap := strings.Repeat(g.unit, fits)
			cmd := chatCmdFor("codex", atCap)
			if errors.Is(cmd.Err, ErrPersonaTooLarge) {
				t.Fatalf("a persona that encodes to %d bytes is within the cap, got %v", len(tomlString(atCap))-2, cmd.Err)
			}
			inArgv := false
			for _, a := range cmd.Args {
				if len(a) >= linuxArgStrlen {
					t.Errorf("an accepted persona produced an argument of %d bytes, past the Linux limit", len(a))
				}
				if strings.HasPrefix(a, "developer_instructions=") {
					inArgv = true
				}
			}
			if !inArgv {
				t.Errorf("the accepted persona must be in argv: %d arguments", len(cmd.Args))
			}

			// One character over: far under the raw cap, over the cap once encoded.
			over := strings.Repeat(g.unit, fits+1)
			if len(over) > MaxPersonaBytes {
				t.Fatalf("the persona must be under the raw cap to prove the encoded check, it is %d bytes", len(over))
			}
			cmd = chatCmdFor("codex", over)
			err := cmd.Start()
			if err == nil {
				_ = cmd.Process.Kill()
				t.Fatal("a persona that encodes past the cap must not start a process")
			}
			if !errors.Is(err, ErrPersonaTooLarge) {
				t.Fatalf("want ErrPersonaTooLarge, got %v", err)
			}
			encoded := len(tomlString(over)) - 2
			for _, want := range []string{
				"agent's system prompt is", strconv.Itoa(len(over)) + " bytes", strconv.Itoa(encoded) + " bytes once escaped",
				"at most 65536", "shorten the agent definition",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error should say %q, got %q", want, err.Error())
				}
			}
			if len(cmd.Args) != 1 {
				t.Errorf("the refused persona must not reach argv, got %d arguments", len(cmd.Args))
			}

			// The refusal never echoes the persona, as given or as encoded, in the
			// error or in argv. A marker at both ends of a persona that is refused for
			// its encoded size must come back in neither.
			probe := personaEchoMarker + strings.Repeat(g.unit, fits) + personaEchoMarker
			if len(probe) > MaxPersonaBytes {
				t.Fatalf("the probe must be under the raw cap to prove the encoded check, it is %d bytes", len(probe))
			}
			cmd = chatCmdFor("codex", probe)
			err = cmd.Start()
			if err == nil {
				_ = cmd.Process.Kill()
				t.Fatal("a persona that encodes past the cap must not start a process")
			}
			if !errors.Is(err, ErrPersonaTooLarge) || !strings.Contains(err.Error(), "once escaped") {
				t.Fatalf("want the encoded-size refusal, got %v", err)
			}
			if strings.Contains(err.Error(), personaEchoMarker) {
				t.Errorf("the encoded-size refusal must not echo the persona: %.200q", err.Error())
			}
			for _, a := range cmd.Args {
				if strings.Contains(a, personaEchoMarker) {
					t.Errorf("the refused persona must not be in argv: %.200q", cmd.Args)
				}
			}
		})
	}
}

// TestChatPersona_RealisticMarkdownPersonaIsAccepted: ordinary agent text, with
// quotes, backslashes and a newline on every line, is far from the cap.
func TestChatPersona_RealisticMarkdownPersonaIsAccepted(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	t.Setenv("PATH", t.TempDir())
	persona := strings.Repeat("## Rules\n\n- Quote \"carefully\" and keep C:\\paths intact.\n- Run `go test`.\n\n", 400)
	if len(persona) > MaxPersonaBytes {
		t.Fatalf("the sample persona is %d bytes, over the raw cap", len(persona))
	}
	if cmd := chatCmdFor("codex", persona); errors.Is(cmd.Err, ErrPersonaTooLarge) {
		t.Errorf("an ordinary persona of %d bytes must be accepted, got %v", len(persona), cmd.Err)
	}
}

// TestChatPersona_AgyIsHeldToItsRawSize: agy writes the persona into argv as it
// is, so what grows codex's argument does not grow agy's.
func TestChatPersona_AgyIsHeldToItsRawSize(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	t.Setenv("PATH", t.TempDir())
	for _, g := range encodedGrowth {
		persona := strings.Repeat(g.unit, MaxPersonaBytes) // 64 KiB raw: at the cap
		cmd := chatCmdFor("agy", persona)
		if errors.Is(cmd.Err, ErrPersonaTooLarge) {
			t.Errorf("%s: agy does no escaping, so a raw persona at the cap is accepted, got %v", g.name, cmd.Err)
		}
		for _, a := range cmd.Args {
			if len(a) >= linuxArgStrlen {
				t.Errorf("%s: agy argument of %d bytes passes the Linux limit", g.name, len(a))
			}
		}
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
