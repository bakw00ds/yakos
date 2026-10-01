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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// Sentinel decode errors. DecodeBytes wraps one of these (errors.Is) so the
// hook-run entrypoint can log the SAME degraded-input reason text bash's
// hi_init does (empty stdin / not JSON / JSON but not an object) instead of
// an implementation-specific error string.
var (
	ErrEmptyStdin = errors.New("hookio: empty stdin")
	ErrNotJSON    = errors.New("hookio: stdin did not parse as JSON")
	ErrNotObject  = errors.New("hookio: stdin is valid JSON but not a JSON object")
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
		return hooktype.HookInput{}, ErrEmptyStdin
	}

	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return hooktype.HookInput{}, fmt.Errorf("%w: %w", ErrNotJSON, err)
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		// Mirrors hi_init's "stdin parsed as JSON but is not a JSON object"
		// check (security review N4/C5 residue, round 2) — an array, a bare
		// string, a number, or null must not silently degrade to an empty
		// object; every hi_* accessor (and every Payload lookup here)
		// assumes an object.
		return hooktype.HookInput{}, ErrNotObject
	}

	// Read the envelope fields by EXACT key from the decoded object, like jq's
	// `.tool_name`. A struct decode matches keys case-insensitively and lets a
	// later "TOOL_NAME" shadow the real "tool_name", so a payload could show
	// the hook a different tool than the one bash evaluates.
	var env claudeHookEnvelope
	for _, f := range []struct {
		key string
		dst *string
	}{{"hook_event_name", &env.HookEventName}, {"tool_name", &env.ToolName}, {"cwd", &env.CWD}} {
		switch v := obj[f.key].(type) {
		case nil:
		case string:
			*f.dst = v
		default:
			return hooktype.HookInput{}, fmt.Errorf("hookio: stdin did not parse as JSON: %s is not a string", f.key)
		}
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

// ToolInputField reads a raw (untyped) field off in.Payload["tool_input"] —
// the Go-side equivalent of jq's `.tool_input.foo` (no type coercion).
// Returns nil if tool_input is absent, not an object, or the field is
// absent.
func ToolInputField(in hooktype.HookInput, key string) any {
	ti := ToolInput(in)
	if ti == nil {
		return nil
	}
	return ti[key]
}

// ToolFilePath is the Go-side equivalent of the bash hi_file_path helper:
// jq's `.tool_input.file_path // .tool_input.notebook_path` rendered as
// `jq -r` does, with command-substitution's trailing-newline stripping.
// Edit/Write/MultiEdit carry the target under tool_input.file_path and
// NotebookEdit under tool_input.notebook_path. It never reads a top-level
// path/file_path: real Claude Code payloads do not carry one.
func ToolFilePath(in hooktype.HookInput) string {
	v := JQAlt(ToolInputField(in, "file_path"), ToolInputField(in, "notebook_path"))
	return strings.TrimRight(JQRawOrJSON(v), "\n")
}

// Nested reads a raw (untyped) field at Payload[outer][inner] — the
// Go-side equivalent of jq's `.outer.inner`. Returns nil if outer is
// absent or not a JSON object, which also matches jq's `//`-chain
// behavior: jq's `//` treats an evaluation error on its left-hand side
// (e.g. indexing a non-object) the same as null/false, so a type
// mismatch here collapsing to nil is the correct analogue, not a bug.
func Nested(payload map[string]any, outer, inner string) any {
	v, ok := payload[outer]
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m[inner]
}

// JQAlt mirrors jq's `//` alternative-operator chain: `a // b // c` in jq
// evaluates left to right and returns the first operand that is not
// jq-falsy (jq's `//` treats only `null` and literal boolean `false` as
// falsy — critically NOT an empty string, `0`, or `[]`). JQAlt reproduces
// that exact falsy set. Passing no values, or only falsy ones, returns
// nil — the Go-side equivalent of a chain terminating in `// empty`.
func JQAlt(vals ...any) any {
	for _, v := range vals {
		if v == nil {
			continue
		}
		if b, ok := v.(bool); ok && !b {
			continue
		}
		return v
	}
	return nil
}

// JQRawOrJSON renders v the way `jq -r` renders a resolved value: a
// string is printed raw, with no surrounding quotes; every other JSON
// type (number, bool, array, object) is printed as jq's default
// (non-`-c`) 2-space-indented pretty JSON. A nil v (the `// empty`
// terminal case) renders as "", matching hi_field's empty-string
// convention for "field absent".
func JQRawOrJSON(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	// jq prints '<', '>' and '&' raw; encoding/json's default HTML escaping
	// would turn "<|im_start|>" into "\u003c|im_start|\u003e" and defeat any
	// literal match downstream. (Known residual differences from jq: object
	// keys come out sorted rather than in input order, and floats use Go's
	// formatting.)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Sprintf("%v", v)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// PayloadField renders a top-level payload field the way bash hi_field does:
// jq's `.key // empty` printed with `jq -r`, then command-substitution's
// trailing-newline stripping. A JSON null/false or an absent key yields "";
// a non-string value (number, array, object) is rendered as jq would, not
// dropped.
func PayloadField(in hooktype.HookInput, key string) string {
	return strings.TrimRight(JQRawOrJSON(JQAlt(in.Payload[key])), "\n")
}

// SessionID is the Go-side equivalent of bash hi_session_id: the payload's
// top-level session_id (jq '.session_id // empty'). It deliberately does NOT
// fall back to $CLAUDE_SESSION_ID: real Claude Code hook stdin always
// carries session_id, and bash hi_session_id never consults the env.
func SessionID(in hooktype.HookInput) string {
	return PayloadField(in, "session_id")
}

// SenderRole is the Go-side equivalent of bash hi_sender_role: the payload's
// agent_type ("lead" when absent or empty), with leading/trailing whitespace
// trimmed (the [:space:] class: space, \t \n \v \f \r) and one leading
// "yakos:" runtime namespace prefix stripped. It deliberately does NOT read
// YAKOS_AGENT_ROLE: bash never does, and the env var is absent on
// subagent hook fires (Phase 1.7).
func SenderRole(in hooktype.HookInput) string {
	raw := PayloadField(in, "agent_type")
	if raw == "" {
		raw = "lead"
	}
	raw = strings.Trim(raw, " \t\n\v\f\r")
	return strings.TrimPrefix(raw, "yakos:")
}
