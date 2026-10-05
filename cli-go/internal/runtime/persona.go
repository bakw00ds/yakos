package runtime

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// MaxPersonaBytes caps the agent persona that chat dispatch carries in argv:
// codex passes it as the value of -c developer_instructions=..., agy prepends it
// to the prompt that follows -p. The largest framework agent is about 7 KiB, so
// the cap is generous, and it keeps one argument well under the 128 KiB Linux
// allows for a single argv element (the escaping can make a codex value several
// times longer than the persona). Past that the exec fails with the operating
// system's bare "argument list too long"; this fails first, before argv exists,
// and says what to shorten.
const MaxPersonaBytes = 64 << 10

// ErrPersonaTooLarge is wrapped by the error a chat command reports when the
// persona exceeds MaxPersonaBytes.
var ErrPersonaTooLarge = errors.New("agent persona too large")

// checkPersonaSize returns an error naming the sizes when persona is over the cap.
// It never includes the persona itself.
func checkPersonaSize(persona string) error {
	if len(persona) <= MaxPersonaBytes {
		return nil
	}
	return fmt.Errorf("%w: the agent's system prompt is %d bytes and chat passes it on the command line, which allows at most %d; shorten the agent definition",
		ErrPersonaTooLarge, len(persona), MaxPersonaBytes)
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
