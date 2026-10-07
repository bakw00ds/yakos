package runtime

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// MaxPersonaBytes caps the agent persona that chat dispatch carries in argv, as
// it sits there: codex passes it as the value of -c developer_instructions=...,
// TOML-encoded, and agy prepends it as it is to the prompt that follows -p. The
// largest framework agent is about 7 KiB, so the cap is generous, and it keeps
// one argument well under the 128 KiB (131072 bytes) Linux allows for a single
// argv element. Past that limit the exec fails with the operating system's bare
// "argument list too long"; this fails first, before argv exists, and says what
// to shorten.
//
// For codex the cap applies to the persona and again to its encoded form,
// because encoding grows it: a quote, a backslash or a newline takes two bytes
// and any other control character six, so 64 KiB of quotes encodes to 128 KiB
// and 64 KiB of control characters to 384 KiB. agy does no encoding, so its
// persona size is its argv size.
const MaxPersonaBytes = 64 << 10

// ErrPersonaTooLarge is wrapped by the error a chat command reports when the
// persona, or its encoded form, exceeds MaxPersonaBytes.
var ErrPersonaTooLarge = errors.New("agent persona too large")

// ErrInvalidResumeID is what a codex or agy chat command reports when the
// native session id to resume fails ValidSessionID.
var ErrInvalidResumeID = errors.New("invalid native session id for resume")

// checkPersonaSize returns an error naming the size when persona is over the
// cap. It never includes the persona itself.
func checkPersonaSize(persona string) error {
	if len(persona) <= MaxPersonaBytes {
		return nil
	}
	return fmt.Errorf("%w: the agent's system prompt is %d bytes and chat passes it on the command line, which allows at most %d; shorten the agent definition",
		ErrPersonaTooLarge, len(persona), MaxPersonaBytes)
}

// codexPersonaArg returns the value of -c developer_instructions=..., the
// persona TOML-encoded and quoted, or the refusal when the persona or its
// encoded form is over MaxPersonaBytes. The raw size is checked first so an
// enormous persona is refused before it is encoded. The two quotes do not count
// toward the cap: a persona exactly at the cap that needs no escaping is
// accepted.
func codexPersonaArg(persona string) (string, error) {
	if err := checkPersonaSize(persona); err != nil {
		return "", err
	}
	encoded := tomlString(persona)
	if n := len(encoded) - 2; n > MaxPersonaBytes {
		return "", fmt.Errorf("%w: the agent's system prompt is %d bytes, which is %d bytes once escaped for the command line, and chat passes it there, which allows at most %d; shorten the agent definition",
			ErrPersonaTooLarge, len(persona), n, MaxPersonaBytes)
	}
	return encoded, nil
}

// rejectedCmd returns a command that cannot start: Start and Run return err
// instead of executing anything. The ChatExecCmd contract has no error result,
// and the callers already report a Start failure, so this carries the reason to
// them. The command has no arguments, so the rejected persona is never in argv.
// The refusal replaces a "binary not found" error from the PATH lookup: it
// depends only on the request, so it reads the same wherever it runs.
func rejectedCmd(ctx context.Context, binary string, err error) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary)
	cmd.Err = err
	return cmd
}
