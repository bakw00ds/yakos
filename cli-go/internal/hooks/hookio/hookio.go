// Package hookio translates between the two hook JSON shapes yakOS deals
// with and hooktype.HookInput:
//
//   - Decode / DecodeBytes read Claude Code's native hook stdin JSON — the
//     shape every bash hook under lib/hooks/*.sh consumes via
//     lib/hooks/lib/hook-input.sh (hi_init/hi_field/...), and the shape the
//     58 fixtures under tests/fixtures/hooks/*.json use:
//     {hook_event_name, tool_name, tool_input, cwd, session_id,
//     transcript_path, permission_mode, agent_type, ...}.
//
//   - DecodeGoShape reads the Go-native fixture shape used by the 63
//     fixtures under .github/fixtures/hooks/**/*.json:
//     {event, tool, payload, env, work_dir}. Since hooktype.HookInput now
//     carries matching JSON tags, this is a plain json.Unmarshal.
//
// Before this package existed there was no code path that turned a real
// Claude Code hook invocation into a hooktype.HookInput (S-6 structural
// plan §1.2) — `yakos hook run <name>` (cmd/yakos/cmd_hook.go) is the first
// production caller of Decode.
package hookio

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// claudeHookEnvelope mirrors the top-level fields Claude Code always (or
// almost always) sends on a hook's stdin. Every other field — tool_input,
// agent_type, session_id, transcript_path, permission_mode, tool_use_id,
// and any event-specific payload — is preserved verbatim in Payload rather
// than being individually typed here, exactly as bash's hi_field/jq
// accessors read straight from the raw JSON rather than a fixed struct.
type claudeHookEnvelope struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	CWD           string `json:"cwd"`
}

// Decode reads Claude Code's native hook JSON from r and returns a
// hooktype.HookInput.
//
//	hook_event_name -> Event
//	tool_name       -> Tool
//	cwd             -> WorkDir
//	(the whole decoded object) -> Payload
//
// Env is left nil: process environment is not part of Claude Code's hook
// stdin payload. Callers that need Env (e.g. `yakos hook run`) populate it
// separately from the OS environment, mirroring how bash hooks read $VAR
// directly (YAKOS_AGENT_ROLE, YAKOS_HOOKS_FAIL_OPEN, ...) rather than
// through stdin.
func Decode(r io.Reader) (hooktype.HookInput, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return hooktype.HookInput{}, fmt.Errorf("hookio: read stdin: %w", err)
	}
	return DecodeBytes(data)
}

// DecodeBytes is Decode over an already-read byte slice. Split out so
// callers that already have the bytes (e.g. the parity harness, which
// needs to feed the same fixture bytes to both bash and Go) don't need an
// io.Reader wrapper.
func DecodeBytes(data []byte) (hooktype.HookInput, error) {
	if len(data) == 0 {
		return hooktype.HookInput{}, fmt.Errorf("hookio: empty stdin")
	}

	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return hooktype.HookInput{}, fmt.Errorf("hookio: stdin did not parse as JSON: %w", err)
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		// Mirrors hi_init's "stdin parsed as JSON but is not a JSON object"
		// check (security review N4/C5 residue, round 2) — an array, a bare
		// string, a number, or null must not silently degrade to an empty
		// object; every hi_* accessor (and every Payload lookup here)
		// assumes an object.
		return hooktype.HookInput{}, fmt.Errorf("hookio: stdin is valid JSON but not a JSON object")
	}

	var env claudeHookEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		// Unreachable in practice (the first Unmarshal above already
		// succeeded into `any`), kept for defense in depth.
		return hooktype.HookInput{}, fmt.Errorf("hookio: stdin did not parse as JSON: %w", err)
	}

	return hooktype.HookInput{
		Event:   env.HookEventName,
		Tool:    env.ToolName,
		Payload: obj,
		WorkDir: env.CWD,
	}, nil
}

// DecodeGoShape reads the .github/fixtures/hooks/**/*.json shape
// ({event, tool, payload, env, work_dir}) into a HookInput. Kept as a named
// entrypoint — rather than asking callers to know hooktype.HookInput is now
// tag-compatible with that shape — so the mapping stays discoverable and
// swappable if the two shapes ever diverge again.
func DecodeGoShape(r io.Reader) (hooktype.HookInput, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return hooktype.HookInput{}, fmt.Errorf("hookio: read stdin: %w", err)
	}
	if len(data) == 0 {
		return hooktype.HookInput{}, fmt.Errorf("hookio: empty stdin")
	}
	var in hooktype.HookInput
	if err := json.Unmarshal(data, &in); err != nil {
		return hooktype.HookInput{}, fmt.Errorf("hookio: invalid go-shape JSON: %w", err)
	}
	return in, nil
}

// PayloadString reads a string field directly off a HookInput's Payload
// (the top level of the Claude Code hook JSON object) — the Go-side
// equivalent of hi_field '.foo'. Returns "" if the key is absent or not a
// string.
func PayloadString(in hooktype.HookInput, key string) string {
	v, ok := in.Payload[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// ToolInput returns in.Payload["tool_input"] as a map, or nil if absent or
// not an object — the Go-side equivalent of jq's `.tool_input`.
func ToolInput(in hooktype.HookInput) map[string]any {
	v, ok := in.Payload["tool_input"]
	if !ok {
		return nil
	}
	m, _ := v.(map[string]any)
	return m
}

// ToolInputString reads a string field off in.Payload["tool_input"] — the
// Go-side equivalent of hi_field '.tool_input.foo'. Returns "" if
// tool_input is absent, not an object, or the field is absent/not a
// string.
func ToolInputString(in hooktype.HookInput, key string) string {
	ti := ToolInput(in)
	if ti == nil {
		return ""
	}
	v, ok := ti[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}
