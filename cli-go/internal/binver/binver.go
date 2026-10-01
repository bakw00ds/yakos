// Package binver reads and compares the version of a yakos binary on disk.
//
// A generated `yakos hook run --impl go <name>` command is only meaningful to
// a binary that knows `--impl` (#292, first released in 0.60.0.0). An older
// binary reads "--impl" as an unknown hook name, prints a message and exits 0,
// so a gate pinned to it would silently stop enforcing.
package binver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MinHookRun is the first release whose `hook run` understands `--impl`.
const MinHookRun = "0.60.0.0"

var versionRe = regexp.MustCompile(`\b(\d+)\.(\d+)\.(\d+)\.(\d+)\b`)

// Parse extracts the first a.b.c.d version from s (the output of
// `yakos --version`, for example "0.60.1.0 (go)").
func Parse(s string) ([4]int, bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return [4]int{}, false
	}
	var v [4]int
	for i := 0; i < 4; i++ {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

// AtLeast reports whether version string have is >= min. An unparseable have
// is not at least anything.
func AtLeast(have, min string) bool {
	h, ok := Parse(have)
	if !ok {
		return false
	}
	m, ok := Parse(min)
	if !ok {
		return false
	}
	for i := 0; i < 4; i++ {
		if h[i] != m[i] {
			return h[i] > m[i]
		}
	}
	return true
}

// Probe runs `<bin> --version` and returns its trimmed first output line.
// Any failure (not runnable, timeout, no output) is an error.
func Probe(bin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output() //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", bin, err)
	}
	for i, c := range out {
		if c == '\n' {
			out = out[:i]
			break
		}
	}
	if len(out) == 0 {
		return "", fmt.Errorf("%s --version printed nothing", bin)
	}
	return string(out), nil
}

// probeHook is a hook name no binary registers. A binary that understands
// `--impl` answers `hook run --impl go <probeHook>` with exit 2 and a "no Go
// implementation" reason; an older one reads "--impl" as the hook name and
// exits 0.
const probeHook = "yakos-capability-probe"

// SupportsHookRun reports whether bin understands `hook run --impl`. It asks
// the binary instead of trusting `--version`, which a development build or a
// bare install may not be able to print. detail explains a negative answer.
func SupportsHookRun(bin string) (ok bool, detail string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "hook", "run", "--impl", "go", probeHook) //nolint:gosec
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		code = ee.ExitCode()
	default:
		return false, fmt.Sprintf("cannot run it: %v", err)
	}
	if code == 2 && strings.Contains(stderr.String(), "no Go implementation") {
		return true, ""
	}
	if v, verr := Probe(bin); verr == nil {
		return false, fmt.Sprintf("version %s; probe exit %d", v, code)
	}
	return false, fmt.Sprintf("probe exit %d", code)
}
