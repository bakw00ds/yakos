package main

import (
	"bytes"
	"context"
	"os/exec"
)

// policyRuntimeVersion runs `<id> --version` and returns its first 512 bytes of
// stdout, "" when the CLI is absent or fails. The output is only ever parsed for
// a version number (runtime.ParseVersion); it is bounded because the binary is
// not ours. No state directory or login is read by `--version`.
func policyRuntimeVersion(ctx context.Context, id string) string {
	path, err := exec.LookPath(id)
	if err != nil {
		return ""
	}
	var out limitedBuffer
	cmd := exec.CommandContext(ctx, path, "--version") //nolint:gosec // id is a fixed harness name chosen by the caller
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return out.String()
}

// limitedBuffer keeps the first 512 bytes written and drops the rest.
type limitedBuffer struct{ b bytes.Buffer }

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := 512 - l.b.Len(); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		l.b.Write(p[:room])
	}
	return len(p), nil
}

func (l *limitedBuffer) String() string { return l.b.String() }
