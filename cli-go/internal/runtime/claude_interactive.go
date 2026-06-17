package runtime

// claude_interactive.go — persistent multi-turn claude process builder.
//
// InteractiveExecCmd constructs the exec.Cmd for a persistent claude session
// that accepts user-turn frames on stdin and streams output frames to stdout
// across multiple turns.  The process stays alive until stdin is closed (EOF).
//
// # Key flag requirements (spike-confirmed)
//
//   - --output-format stream-json REQUIRES --verbose or the CLI errors.
//     This is undocumented by Anthropic; do NOT remove --verbose.
//   - --input-format stream-json enables the bidirectional protocol.
//   - --include-partial-messages streams text/tool/thinking deltas incrementally.
//   - NO -p flag: user input is delivered via stdin as newline-terminated JSON frames.
//   - --permission-mode bypassPermissions: same posture as the one-shot path.
//   - --add-dir <project>: grants the agent access to the project tree.
//   - --exclude-dynamic-system-prompt-sections: PR #31 prompt-cache flag, same
//     as the one-shot and chat dispatch paths.
//
// # User-turn frame schema
//
//	{"type":"user","message":{"role":"user","content":[{"type":"text","text":"<text>"}]}}
//
// Encoded by EncodeUserTurn.  The frame MUST be newline-terminated when written
// to stdin; the CLI uses newline as the frame delimiter.
//
// # Session identity
//
// session_id appears on every output frame from the claude CLI and is used by
// the interactive session manager to route SSE output correctly.  It must NOT
// be passed on the command line (it is derived from the process-level state by
// the CLI).

import (
	"encoding/json"
	"os"
	"os/exec"
)

// InteractiveExecCmd returns a configured exec.Cmd for a persistent multi-turn
// claude session.
//
// The returned cmd has:
//   - Stdin:  nil (caller must call cmd.StdinPipe() to get the write end)
//   - Stdout: nil (caller must call cmd.StdoutPipe() to read frames)
//   - Stderr: nil (caller should attach a buffer or pipe)
//   - Env: inherited from process + YAKOS_MODEL_OVERRIDE when modelOverride != ""
//
// Parameters:
//   - project: absolute path to the project directory (--add-dir)
//   - agentSystemPrompt: agent body injected via --append-system-prompt; empty
//     means no agent system prompt is appended (raw chat mode)
//   - modelOverride: concrete model tier (haiku|sonnet|opus|fable); exported as
//     YAKOS_MODEL_OVERRIDE in the subprocess env.  Empty → adapter default.
//   - effort: reasoning effort level (low|medium|high|xhigh|max); passed as
//     --effort if non-empty.  claude-only; empty → flag omitted.
//
// Callers must NOT use exec.CommandContext here: the interactive session lives
// beyond the HTTP request context.  The caller manages the lifetime via
// Session.Close(), which closes stdin (clean exit) and kills the process group
// on force-close.
func InteractiveExecCmd(project, agentSystemPrompt, modelOverride, effort string) *exec.Cmd {
	args := []string{
		"--print",                          // required for non-interactive mode
		"--input-format", "stream-json",    // bidirectional JSON framing
		"--output-format", "stream-json",   // must pair with --verbose (undocumented requirement)
		"--verbose",                        // REQUIRED: --output-format stream-json fails without it
		"--include-partial-messages",       // stream deltas incrementally
		"--permission-mode", "bypassPermissions",
		"--add-dir", project,
		"--exclude-dynamic-system-prompt-sections", // PR #31 prompt-cache flag
	}

	if agentSystemPrompt != "" {
		args = append(args, "--append-system-prompt", agentSystemPrompt)
	}
	if effort != "" {
		args = append(args, "--effort", effort)
	}

	// Do NOT use exec.CommandContext: the session outlives the originating HTTP
	// request.  Session.Close() manages cleanup via stdin-close + process-group kill.
	cmd := exec.Command("claude", args...) //nolint:gosec
	cmd.Env = buildEnvInteractive(modelOverride)
	if project != "" {
		cmd.Dir = project
	}
	return cmd
}

// EncodeUserTurn encodes text as a newline-terminated user-turn frame in the
// stream-json input format accepted by claude --input-format stream-json.
//
// Wire format (per spike):
//
//	{"type":"user","message":{"role":"user","content":[{"type":"text","text":"<text>"}]}}
//
// The returned bytes always end with '\n' — required by the CLI frame delimiter.
// json.Marshal is used (not manual string interpolation) to correctly escape
// any special characters in text.
func EncodeUserTurn(text string) []byte {
	type contentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type message struct {
		Role    string         `json:"role"`
		Content []contentBlock `json:"content"`
	}
	type userFrame struct {
		Type    string  `json:"type"`
		Message message `json:"message"`
	}
	frame := userFrame{
		Type: "user",
		Message: message{
			Role:    "user",
			Content: []contentBlock{{Type: "text", Text: text}},
		},
	}
	b, _ := json.Marshal(frame) // cannot fail: all fields are valid JSON types
	return append(b, '\n')
}

// buildEnvInteractive constructs the subprocess environment for a persistent
// interactive session.  Inherits the current process env; adds
// YAKOS_MODEL_OVERRIDE when modelOverride is non-empty.
func buildEnvInteractive(modelOverride string) []string {
	base := os.Environ()
	env := make([]string, 0, len(base)+1)
	env = append(env, base...)
	if modelOverride != "" {
		env = append(env, "YAKOS_MODEL_OVERRIDE="+modelOverride)
	}
	return env
}
