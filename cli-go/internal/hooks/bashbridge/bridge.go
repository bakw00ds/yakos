// Package bashbridge implements the Tier-2 bash-user-hook escape hatch for the
// Phase-3 hook framework (Hybrid Strategy D).
//
// When a lib/hooks-user/<name>.sh file exists the runner calls Apply, which
// executes the script with the same stdin/env contract as today's bash hooks:
//
//   - stdin: JSON-encoded HookInput payload
//   - env:   YAKOS_HOOK_EVENT, YAKOS_HOOK_TOOL, plus the caller's Env map
//   - stdout/stderr: captured and appended to HookOutput
//   - exit code: applied to HookOutput.ExitCode (Tier-2 exit code wins over
//     Tier-0/Tier-1 when non-zero)
//
// On Windows where bash is absent the runner (not this package) handles the
// skip with a one-line diagnostic per Q2. This package only handles execution.
package bashbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// windowsGitBashPaths lists common Git Bash installations on Windows.
// Checked in order; first found wins.
var windowsGitBashPaths = []string{
	`C:\Program Files\Git\bin\bash.exe`,
	`C:\Program Files (x86)\Git\bin\bash.exe`,
	`C:\Git\bin\bash.exe`,
}

// DetectBash locates bash and returns (path, available).
// On Windows it also checks common Git Bash locations.
func DetectBash() (string, bool) {
	if p, err := exec.LookPath("bash"); err == nil {
		return p, true
	}
	if runtime.GOOS == "windows" {
		for _, p := range windowsGitBashPaths {
			if _, err := os.Stat(p); err == nil {
				return p, true
			}
		}
	}
	return "", false
}

// Bridge holds the path to a bash script and the bash binary.
type Bridge struct {
	scriptPath string
	bashPath   string
}

// New creates a Bridge for the given script path using the given bash binary.
// bashPath is the absolute path to bash (from DetectBash).
func New(scriptPath, bashPath string) *Bridge {
	return &Bridge{
		scriptPath: scriptPath,
		bashPath:   bashPath,
	}
}

// Apply executes the bash script and merges its output into HookOutput.
//
// Contract with the bash script (matches today's lib/hooks/*.sh stdin/env):
//   - stdin: JSON object with hook_event, hook_tool, payload, env keys
//   - env:   YAKOS_HOOK_EVENT and YAKOS_HOOK_TOOL set from in; caller's Env merged
//   - exit 0: pass (Tier-2 stdout/stderr appended)
//   - exit 2: block (overrides any previous exit code)
//   - other: treated as soft error (exit code applied)
func (b *Bridge) Apply(ctx context.Context, in hooktype.HookInput, out hooktype.HookOutput) (hooktype.HookOutput, error) {
	// Encode stdin payload.
	stdin, err := buildStdin(in)
	if err != nil {
		return out, fmt.Errorf("bashbridge: encode stdin: %w", err)
	}

	// Build environment.
	env := buildEnv(in)

	cmd := exec.CommandContext(ctx, b.bashPath, b.scriptPath) //nolint:gosec
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = env

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := cmd.Run()

	// Append output regardless of exit code.
	out.Stdout = append(out.Stdout, stdoutBuf.Bytes()...)
	out.Stderr = append(out.Stderr, stderrBuf.Bytes()...)

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return out, fmt.Errorf("bashbridge: exec %s: %w", b.scriptPath, runErr)
		}
	}

	// Tier-2 exit code: non-zero overrides; 0 is ignored (Tier-0 stand).
	if exitCode != 0 {
		out.ExitCode = exitCode
	}

	return out, nil
}

// buildStdin produces the JSON-encoded stdin payload for the bash hook.
func buildStdin(in hooktype.HookInput) ([]byte, error) {
	payload := map[string]any{
		"hook_event": in.Event,
		"hook_tool":  in.Tool,
		"payload":    in.Payload,
		"env":        in.Env,
		"workdir":    in.WorkDir,
	}
	return json.Marshal(payload)
}

// buildEnv assembles the environment slice for the bash hook process.
func buildEnv(in hooktype.HookInput) []string {
	// Start from the current process environment so bash finds its builtins.
	base := os.Environ()

	// Build a set of keys already present so we can de-duplicate.
	seen := map[string]bool{}
	for _, e := range base {
		if idx := strings.IndexByte(e, '='); idx > 0 {
			seen[e[:idx]] = true
		}
	}

	// Always inject these.
	injected := []string{
		"YAKOS_HOOK_EVENT=" + in.Event,
		"YAKOS_HOOK_TOOL=" + in.Tool,
	}
	for _, kv := range injected {
		key := kv[:strings.IndexByte(kv, '=')]
		if !seen[key] {
			base = append(base, kv)
			seen[key] = true
		}
	}

	// Merge caller's Env (override existing keys).
	for k, v := range in.Env {
		kv := k + "=" + v
		// Replace if already present.
		replaced := false
		for i, existing := range base {
			if len(existing) > len(k)+1 && existing[:len(k)+1] == k+"=" {
				base[i] = kv
				replaced = true
				break
			}
		}
		if !replaced {
			base = append(base, kv)
		}
	}

	return base
}
