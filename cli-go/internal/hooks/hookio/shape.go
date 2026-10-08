package hookio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// Harness shapes accepted by DecodeShape and Respond.
const (
	ShapeCodex = "codex"
	ShapeAgy   = "agy"
)

// MaxShapeBytes bounds one harness envelope. A tool call that carries a larger
// payload (a whole-file write) is refused rather than half-scanned.
const MaxShapeBytes = 1 << 20

// ErrOversize is returned by DecodeShape for an envelope above MaxShapeBytes.
var ErrOversize = errors.New("hookio: envelope exceeds size limit")

// maxReason bounds the deny reason handed back to a harness.
const maxReason = 2000

// IsShape reports whether s names a supported harness shape.
func IsShape(s string) bool { return s == ShapeCodex || s == ShapeAgy }

// DecodeShape maps a codex or agy PreToolUse/PostToolUse envelope onto
// yakOS's hook input shape (Claude Code's: tool_name, tool_input, cwd,
// session_id). It returns one input per file a single tool call touches, so a
// multi-file codex apply_patch is gated file by file; every other call yields
// exactly one input.
//
// Field names follow the K-156 spike (codex 0.154.0, agy 1.3.0). Tool names
// the spike did not exercise (codex apply_patch input layout, agy file tools)
// are mapped best effort and are documented as unverified in
// docs/runtime-matrix.md.
func DecodeShape(shape string, data []byte) ([]hooktype.HookInput, error) {
	if len(data) > MaxShapeBytes {
		return nil, ErrOversize
	}
	switch shape {
	case ShapeCodex:
		return decodeCodex(data)
	case ShapeAgy:
		return decodeAgy(data)
	}
	return nil, fmt.Errorf("hookio: unknown shape %q", shape)
}

func decodeCodex(data []byte) ([]hooktype.HookInput, error) {
	in, err := DecodeBytes(data)
	if err != nil {
		return nil, err
	}
	if in.Tool != "apply_patch" {
		return []hooktype.HookInput{in}, nil
	}
	patch := patchText(in.Payload["tool_input"])
	files := patchFiles(patch)
	if len(files) == 0 {
		// No parsable target: still scan the patch text as a write with no
		// path so secret-scan sees the content.
		files = []string{""}
	}
	out := make([]hooktype.HookInput, 0, len(files))
	for _, f := range files {
		p := make(map[string]any, len(in.Payload))
		for k, v := range in.Payload {
			p[k] = v
		}
		p["tool_name"] = "Write"
		p["tool_input"] = map[string]any{"file_path": f, "content": patch}
		c := in
		c.Tool = "Write"
		c.Payload = p
		out = append(out, c)
	}
	return out, nil
}

// patchText extracts the patch body from an apply_patch tool_input, which
// codex sends either as a bare string or as an object holding it.
func patchText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		for _, k := range []string{"input", "patch", "command"} {
			if s, ok := t[k].(string); ok {
				return s
			}
		}
	}
	return ""
}

// patchFiles lists the target paths named by "*** Add/Update/Delete File:" and
// "*** Move to:" headers, in order, without duplicates.
func patchFiles(patch string) []string {
	var files []string
	seen := map[string]bool{}
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimRight(line, "\r")
		for _, p := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
			if rest, ok := strings.CutPrefix(line, p); ok {
				rest = strings.TrimSpace(rest)
				if rest != "" && !seen[rest] {
					seen[rest] = true
					files = append(files, rest)
				}
			}
		}
	}
	return files
}

// agyToolNames maps agy's step names (lowercased step type) to the yakOS tool
// classes the hooks gate on. Only run_command is verified live (K-156).
var agyToolNames = map[string]string{
	"run_command":                "Bash",
	"write_to_file":              "Write",
	"replace_file_content":       "Edit",
	"multi_replace_file_content": "MultiEdit",
	"edit_file":                  "Edit",
}

var (
	agyPathKeys    = []string{"TargetFile", "AbsolutePath", "FilePath", "Path", "File"}
	agyContentKeys = []string{"CodeContent", "Content", "FileContent"}
	agyNewKeys     = []string{"ReplacementContent", "NewString"}
)

func firstString(m map[string]any, keys []string) (string, bool) {
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			return s, true
		}
	}
	return "", false
}

func decodeAgy(data []byte) ([]hooktype.HookInput, error) {
	if len(data) == 0 {
		return nil, ErrEmptyStdin
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotJSON, err)
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, ErrNotObject
	}
	call, _ := obj["toolCall"].(map[string]any)
	if call == nil {
		return nil, fmt.Errorf("hookio: agy envelope has no toolCall")
	}
	name, _ := call["name"].(string)
	if name == "" {
		return nil, fmt.Errorf("hookio: agy toolCall has no name")
	}
	args, _ := call["args"].(map[string]any)

	// agy sends no event name; only PostToolUse carries "error".
	event := "PreToolUse"
	if _, post := obj["error"]; post {
		event = "PostToolUse"
	}
	tool := name
	if mapped, ok := agyToolNames[name]; ok {
		tool = mapped
	}

	ti := make(map[string]any, len(args)+3)
	for k, v := range args {
		ti[k] = v
	}
	if s, ok := args["CommandLine"].(string); ok {
		ti["command"] = s
	}
	if s, ok := firstString(args, agyPathKeys); ok {
		ti["file_path"] = s
	}
	if s, ok := firstString(args, agyContentKeys); ok {
		ti["content"] = s
	}
	if s, ok := firstString(args, agyNewKeys); ok {
		ti["new_string"] = s
	}

	cwd := ""
	if ws, ok := obj["workspacePaths"].([]any); ok && len(ws) > 0 {
		if s, ok := ws[0].(string); ok && (filepath.IsAbs(s) || strings.HasPrefix(s, "/")) {
			cwd = s
		}
	}
	sid, _ := obj["conversationId"].(string)
	tp, _ := obj["transcriptPath"].(string)
	payload := map[string]any{
		"hook_event_name": event,
		"tool_name":       tool,
		"tool_input":      ti,
		"cwd":             cwd,
		"session_id":      sid,
		"transcript_path": tp,
	}
	return []hooktype.HookInput{{Event: event, Tool: tool, Payload: payload, WorkDir: cwd}}, nil
}

// Response is what `yakos hook run --shape` writes back to the harness.
type Response struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Respond renders a hook verdict in the harness's wire shape. blocked marks a
// deny; reason is shown to the model and bounded.
//
//	codex: deny is exit 2 with the reason on stderr (verified K-156); allow is
//	       exit 0 with no output.
//	agy:   always exit 0; PreToolUse prints {"decision":"deny","reason":...}
//	       or {"decision":"allow"}, PostToolUse prints {}.
func Respond(shape, event string, blocked bool, reason string) Response {
	reason = strings.TrimSpace(reason)
	if len(reason) > maxReason {
		reason = strings.ToValidUTF8(reason[:maxReason], "")
	}
	if blocked && reason == "" {
		reason = "blocked by a yakOS hook"
	}
	if shape == ShapeCodex {
		if blocked {
			return Response{Stderr: []byte(reason + "\n"), ExitCode: 2}
		}
		return Response{}
	}
	if event == "PostToolUse" {
		return Response{Stdout: []byte("{}\n")}
	}
	type decision struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason,omitempty"`
	}
	d := decision{Decision: "allow"}
	if blocked {
		d = decision{Decision: "deny", Reason: reason}
	}
	b, _ := json.Marshal(d)
	return Response{Stdout: append(b, '\n')}
}

type agentCtxKey struct{}

type projectCtxKey struct{}

// WithProject returns ctx carrying the project directory a caller has bound
// the request to (the loopback hooks endpoint binds it to its nonce at issue
// time). shaperun then ignores the process environment and the envelope for
// the project and uses this one.
func WithProject(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, projectCtxKey{}, dir)
}

// ProjectFrom returns the directory WithProject stored, or "".
func ProjectFrom(ctx context.Context) string {
	s, _ := ctx.Value(projectCtxKey{}).(string)
	return s
}

// EnvelopeDirs lists every project directory a codex or agy envelope names:
// codex's cwd, agy's workspacePaths entries. Values are returned exactly as
// sent (possibly relative); an envelope that does not parse names none.
func EnvelopeDirs(shape string, data []byte) []string {
	if len(data) > MaxShapeBytes {
		return nil
	}
	var obj map[string]any
	if json.Unmarshal(data, &obj) != nil {
		return nil
	}
	var out []string
	switch shape {
	case ShapeCodex:
		if s, ok := obj["cwd"].(string); ok && s != "" {
			out = append(out, s)
		}
	case ShapeAgy:
		if ws, ok := obj["workspacePaths"].([]any); ok {
			for _, w := range ws {
				if s, ok := w.(string); ok && s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// WithAgent returns ctx carrying the id of the agent yakOS dispatched, for a
// caller (the loopback hooks endpoint) that cannot pass it through the
// environment.
func WithAgent(ctx context.Context, agent string) context.Context {
	return context.WithValue(ctx, agentCtxKey{}, agent)
}

// AgentFrom returns the agent id WithAgent stored, or "".
func AgentFrom(ctx context.Context) string {
	s, _ := ctx.Value(agentCtxKey{}).(string)
	return s
}

// ValidAgent reports whether s is a plain agent identifier (the same shape
// runtime.withAgentType accepts).
func ValidAgent(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if i > 0 && (r == '.' || r == '_' || r == '-') {
			ok = true
		}
		if !ok {
			return false
		}
	}
	return true
}
