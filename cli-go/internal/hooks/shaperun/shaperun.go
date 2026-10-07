// Package shaperun runs one registered Go hook against a codex or agy envelope
// and returns the harness's answer. It is the shared core of
// `yakos hook run --shape` and the loopback hooks endpoint (K-145); neither
// caller exits the process or reads process-global state here, so the daemon
// can serve it concurrently.
//
// Fail posture: an envelope that cannot be decoded is denied for a fail-closed
// hook (Deps.FailOpen is the operator's emergency override, the
// YAKOS_HOOKS_FAIL_OPEN=1 of the Claude path) and allowed for a telemetry hook.
// An unknown hook name is allowed; hooksinstall only emits registered names.
package shaperun

import (
	"context"
	"io"
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/registry"
	"github.com/bakw00ds/yakos/internal/hooks/runner"
)

// Deps carries what differs between the CLI and the daemon.
type Deps struct {
	// Env is the environment snapshot the hooks read (HookInput.Env).
	Env map[string]string
	// FailOpen is the operator's emergency override for undecodable envelopes.
	FailOpen bool
	// YakosRoot locates lib/hooks for the runner (unused in Go mode).
	YakosRoot string
	// Resolve returns the hook Config and work/current dir for the workspace the
	// envelope names ("" when it names none).
	Resolve func(workDir string) (cfg registry.Config, workCurrentDir string)
}

// Known reports whether name is a registered hook.
func Known(name string) bool {
	_, ok := lookup(name)
	return ok
}

func lookup(name string) (registry.Entry, bool) {
	for _, e := range registry.All() {
		if e.Name == name {
			return e, true
		}
	}
	return registry.Entry{}, false
}

// Run executes hook name on one envelope.
func Run(ctx context.Context, shape, name string, data []byte, d Deps) hookio.Response {
	entry, ok := lookup(name)
	if !ok {
		return hookio.Respond(shape, "PreToolUse", false, "")
	}
	degraded := func(why string) hookio.Response {
		if entry.FailClosed && !d.FailOpen {
			return hookio.Respond(shape, "PreToolUse", true, name+": "+why+"; refusing to fail open")
		}
		return hookio.Respond(shape, "PreToolUse", false, "")
	}
	ins, err := hookio.DecodeShape(shape, data)
	if err != nil || len(ins) == 0 {
		return degraded("cannot decode " + shape + " envelope")
	}
	// A tool envelope with no event name is treated as PreToolUse (fail
	// closed); any other named event is not one yakOS gates.
	for i := range ins {
		if ins[i].Event == "" {
			ins[i].Event = "PreToolUse"
		}
	}
	event := ins[0].Event
	if event != "PreToolUse" && event != "PostToolUse" {
		return hookio.Respond(shape, "PreToolUse", false, "")
	}

	cfg, workCurrentDir := d.Resolve(ins[0].WorkDir)
	hook, _, found := registry.Lookup(name, cfg)
	if !found {
		return hookio.Respond(shape, event, false, "")
	}
	r := runner.New(filepath.Join(d.YakosRoot, "lib", "hooks"), filepath.Join(cfg.ProjectDir, "lib", "hooks-user"), workCurrentDir, nil, io.Discard)
	r.ModeOverride = runner.HooksModeGo
	r.FailClosed = entry.FailClosed

	// The hooks find the project's policy files through CLAUDE_PROJECT_DIR. A
	// harness does not set it, and agy runs hooks from its .agents directory, so
	// without this path-allowlist would look in the wrong place and pass.
	env := make(map[string]string, len(d.Env)+1)
	for k, v := range d.Env {
		env[k] = v
	}
	if env["CLAUDE_PROJECT_DIR"] == "" {
		env["CLAUDE_PROJECT_DIR"] = cfg.ProjectDir
	}

	for _, in := range ins {
		in.Env = env
		out, runErr := r.Run(ctx, hook, in)
		if out.ExitCode == 2 {
			return hookio.Respond(shape, event, true, blockReason(name, out))
		}
		if runErr != nil && entry.FailClosed {
			return hookio.Respond(shape, event, true, "yakOS hook "+name+" failed: "+runErr.Error())
		}
	}
	return hookio.Respond(shape, event, false, "")
}

// blockReason is the hook's own stderr text, falling back to its stdout.
func blockReason(name string, out hooktype.HookOutput) string {
	if s := strings.TrimSpace(string(out.Stderr)); s != "" {
		return s
	}
	if s := strings.TrimSpace(string(out.Stdout)); s != "" {
		return s
	}
	return "blocked by yakOS hook " + name
}
