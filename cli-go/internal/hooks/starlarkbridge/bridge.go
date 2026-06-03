// Package starlarkbridge implements the Tier-1 Starlark customization layer
// for the Phase-3 hook framework (Hybrid Strategy D).
//
// A .star file in lib/hooks/ may either OVERRIDE or AUGMENT the Go-native
// Tier-0 hook. The mode is selected by the `override` top-level binding:
//
//	override = True   → the script is the primary logic; Tier-0 still runs
//	                    first but the Starlark script's exit code wins.
//	(absent/False)    → AUGMENT mode; the script runs after Tier-0, sees its
//	                    output, and may change the exit code or add artifacts.
//
// Sandbox (Q3): ctx.read_file is restricted to paths under work/current/ plus
// an explicit allowlist provided at construction time. Writes go to
// work/current/hooks/<hook-name>/ via ctx.write_artifact.
//
// Starlark API exposed via the `ctx` global:
//
//	ctx.input          — dict with "event", "tool", "payload", "env", "workdir"
//	ctx.log(msg)       — writes msg to hook Stderr output
//	ctx.set_exit_code(int) — sets the output ExitCode
//	ctx.read_file(path)    — sandbox-restricted file read; returns str
//	ctx.write_artifact(name, bytes) — writes an artifact string
package starlarkbridge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.starlark.net/starlark"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// Bridge holds a compiled Starlark program ready to Apply against hook I/O.
type Bridge struct {
	scriptPath   string
	sandboxPaths []string // dirs under which ctx.read_file is permitted
	isOverride   bool     // true when the script declares `override = True`
	thread       func() *starlark.Thread
	prog         *starlark.Program
}

// New loads and parses the Starlark script at scriptPath.
// sandboxPaths restricts ctx.read_file; at minimum work/current/ must be included.
func New(scriptPath string, sandboxPaths []string) (*Bridge, error) {
	src, err := os.ReadFile(scriptPath) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("starlarkbridge: read %s: %w", scriptPath, err)
	}

	_, prog, err := starlark.SourceProgram(scriptPath, src, isPredeclared)
	if err != nil {
		return nil, fmt.Errorf("starlarkbridge: parse %s: %w", scriptPath, err)
	}

	// Probe for `override = True` by executing the module once in a minimal env.
	isOverride, err := probeOverride(scriptPath, src)
	if err != nil {
		return nil, fmt.Errorf("starlarkbridge: override probe %s: %w", scriptPath, err)
	}

	threadFn := func() *starlark.Thread {
		return &starlark.Thread{Name: filepath.Base(scriptPath)}
	}

	return &Bridge{
		scriptPath:   scriptPath,
		sandboxPaths: sandboxPaths,
		isOverride:   isOverride,
		thread:       threadFn,
		prog:         prog,
	}, nil
}

// Apply runs the Starlark script against the hook I/O and returns the
// (potentially modified) output.
func (b *Bridge) Apply(_ context.Context, in hooktype.HookInput, out hooktype.HookOutput) (hooktype.HookOutput, error) {
	// Mutable state shared between the Starlark built-ins and this function.
	state := &execState{
		exitCode:  out.ExitCode,
		artifacts: copyArtifacts(out.Artifacts),
		stderr:    append([]byte{}, out.Stderr...),
	}

	// Build the ctx object (implements starlark.HasAttrs directly).
	ctxObj := buildCtxObject(in, state, b.sandboxPaths, b.scriptPath)

	// Build the predeclared namespace: only `ctx` is exposed.
	predeclared := starlark.StringDict{
		"ctx": ctxObj,
	}

	thread := b.thread()
	globals, err := b.prog.Init(thread, predeclared)
	if err != nil {
		return out, fmt.Errorf("starlarkbridge: exec %s: %w", b.scriptPath, err)
	}

	// Call on_event(ctx) if it exists.
	if fn, ok := globals["on_event"]; ok {
		if callable, ok := fn.(starlark.Callable); ok {
			if _, err := starlark.Call(thread, callable, starlark.Tuple{ctxObj}, nil); err != nil {
				return out, fmt.Errorf("starlarkbridge: on_event in %s: %w", b.scriptPath, err)
			}
		}
	}

	// Merge state back into output.
	out.ExitCode = state.exitCode
	out.Stderr = state.stderr
	if out.Artifacts == nil {
		out.Artifacts = map[string][]byte{}
	}
	for k, v := range state.artifacts {
		out.Artifacts[k] = v
	}

	return out, nil
}

// IsOverride reports whether the script declared `override = True`.
func (b *Bridge) IsOverride() bool { return b.isOverride }

// ---- internal ---------------------------------------------------------------

// isPredeclared is used by SourceProgram to check which names are predeclared.
func isPredeclared(name string) bool { return name == "ctx" }

// execState is mutable shared state updated by the Starlark built-in functions.
type execState struct {
	exitCode  int
	artifacts map[string][]byte
	stderr    []byte
}

// buildCtxObject constructs the Starlark `ctx` value exposed to the script.
// It returns *ctxObject which implements starlark.HasAttrs so that
// `ctx.log(...)`, `ctx.set_exit_code(...)`, etc. work from within Starlark.
func buildCtxObject(in hooktype.HookInput, state *execState, sandboxPaths []string, scriptPath string) *ctxObject {
	// Build ctx.input as a Starlark dict.
	inputDict := starlark.NewDict(6)
	_ = inputDict.SetKey(starlark.String("event"), starlark.String(in.Event))
	_ = inputDict.SetKey(starlark.String("tool"), starlark.String(in.Tool))
	_ = inputDict.SetKey(starlark.String("workdir"), starlark.String(in.WorkDir))

	// payload sub-dict
	payloadDict := starlark.NewDict(len(in.Payload))
	for k, v := range in.Payload {
		_ = payloadDict.SetKey(starlark.String(k), goToStarlark(v))
	}
	_ = inputDict.SetKey(starlark.String("payload"), payloadDict)

	// env sub-dict
	envDict := starlark.NewDict(len(in.Env))
	for k, v := range in.Env {
		_ = envDict.SetKey(starlark.String(k), starlark.String(v))
	}
	_ = inputDict.SetKey(starlark.String("env"), envDict)

	return &ctxObject{
		input:        inputDict,
		state:        state,
		sandboxPaths: sandboxPaths,
		scriptPath:   scriptPath,
	}
}

// ctxObject implements starlark.Value and starlark.HasAttrs so that `ctx.log(...)` works.
type ctxObject struct {
	input        *starlark.Dict
	state        *execState
	sandboxPaths []string
	scriptPath   string
}

func (c *ctxObject) String() string        { return "<yakos hook ctx>" }
func (c *ctxObject) Type() string          { return "ctx" }
func (c *ctxObject) Freeze()               {}
func (c *ctxObject) Hash() (uint32, error) { return 0, fmt.Errorf("ctx is unhashable") }
func (c *ctxObject) Truth() starlark.Bool  { return starlark.True }

// Attr implements starlark.HasAttrs.
func (c *ctxObject) Attr(name string) (starlark.Value, error) {
	switch name {
	case "input":
		return c.input, nil
	case "log":
		return starlark.NewBuiltin("ctx.log", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			var msg starlark.Value
			if err := starlark.UnpackPositionalArgs("ctx.log", args, nil, 1, &msg); err != nil {
				return nil, err
			}
			line := msg.String()
			// Strip surrounding quotes from string values.
			if s, ok := msg.(starlark.String); ok {
				line = string(s)
			}
			c.state.stderr = append(c.state.stderr, []byte(line+"\n")...)
			return starlark.None, nil
		}), nil
	case "set_exit_code":
		return starlark.NewBuiltin("ctx.set_exit_code", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			var code starlark.Int
			if err := starlark.UnpackPositionalArgs("ctx.set_exit_code", args, nil, 1, &code); err != nil {
				return nil, err
			}
			n, ok := code.Int64()
			if !ok {
				return nil, fmt.Errorf("ctx.set_exit_code: value too large")
			}
			c.state.exitCode = int(n)
			return starlark.None, nil
		}), nil
	case "read_file":
		return starlark.NewBuiltin("ctx.read_file", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			var pathVal starlark.String
			if err := starlark.UnpackPositionalArgs("ctx.read_file", args, nil, 1, &pathVal); err != nil {
				return nil, err
			}
			path := string(pathVal)
			// Resolve relative paths against the first sandbox path (work/current/).
			if !filepath.IsAbs(path) && len(c.sandboxPaths) > 0 {
				path = filepath.Join(c.sandboxPaths[0], path)
			}
			// Sandbox check.
			if !c.inSandbox(path) {
				return nil, fmt.Errorf("ctx.read_file: sandbox violation: %q is outside allowed paths (Q3)", path)
			}
			data, err := os.ReadFile(path) //nolint:gosec
			if err != nil {
				if os.IsNotExist(err) {
					return starlark.String(""), nil
				}
				return nil, fmt.Errorf("ctx.read_file: %w", err)
			}
			return starlark.String(string(data)), nil
		}), nil
	case "write_artifact":
		return starlark.NewBuiltin("ctx.write_artifact", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			var nameVal, dataVal starlark.String
			if err := starlark.UnpackPositionalArgs("ctx.write_artifact", args, nil, 2, &nameVal, &dataVal); err != nil {
				return nil, err
			}
			artifactName := string(nameVal)
			artifactData := []byte(string(dataVal))
			if c.state.artifacts == nil {
				c.state.artifacts = map[string][]byte{}
			}
			c.state.artifacts[artifactName] = artifactData
			return starlark.None, nil
		}), nil
	}
	return nil, nil // attribute not found
}

// AttrNames implements starlark.HasAttrs.
func (c *ctxObject) AttrNames() []string {
	return []string{"input", "log", "set_exit_code", "read_file", "write_artifact"}
}

// inSandbox returns true when path falls under at least one allowed directory.
func (c *ctxObject) inSandbox(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for _, allowed := range c.sandboxPaths {
		allowedAbs, err := filepath.Abs(allowed)
		if err != nil {
			continue
		}
		if abs == allowedAbs || strings.HasPrefix(abs, allowedAbs+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// goToStarlark converts a Go any value to a Starlark value (best-effort).
func goToStarlark(v any) starlark.Value {
	switch t := v.(type) {
	case string:
		return starlark.String(t)
	case bool:
		return starlark.Bool(t)
	case int:
		return starlark.MakeInt(t)
	case int64:
		return starlark.MakeInt64(t)
	case float64:
		return starlark.Float(t)
	case nil:
		return starlark.None
	default:
		return starlark.String(fmt.Sprintf("%v", t))
	}
}

// probeOverride executes the module globals to check for `override = True`.
func probeOverride(scriptPath string, src []byte) (bool, error) {
	thread := &starlark.Thread{Name: "probe"}
	globals, err := starlark.ExecFile(thread, scriptPath, src, nil)
	if err != nil {
		// If execution fails here, the main Apply will also fail with a better message.
		// Return false so we don't hide the real error.
		return false, nil //nolint:nilerr
	}
	if v, ok := globals["override"]; ok {
		if b, ok := v.(starlark.Bool); ok {
			return bool(b), nil
		}
	}
	return false, nil
}

// copyArtifacts returns a shallow copy of the artifacts map.
func copyArtifacts(m map[string][]byte) map[string][]byte {
	if m == nil {
		return map[string][]byte{}
	}
	out := make(map[string][]byte, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
