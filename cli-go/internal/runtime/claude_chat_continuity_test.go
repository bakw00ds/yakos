package runtime

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// argAfter and hasArg live in claude_model_pin_test.go.

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

// The one-shot chat subprocess used to inherit the daemon's cwd, so project
// hooks and CLAUDE.md differed from a terminal session and a saved session
// could not be found again. It now runs in the project.
func TestChatExecCmd_RunsInProjectDir(t *testing.T) {
	a := &ClaudeAdapter{}
	project := t.TempDir()
	worktree := t.TempDir()

	cmd := a.ChatExecCmd(context.Background(), ChatDispatchRequest{Project: project, UserText: "hi"})
	if cmd.Dir != project {
		t.Errorf("Dir = %q, want the project %q when there is no worktree override", cmd.Dir, project)
	}

	cmd = a.ChatExecCmd(context.Background(), ChatDispatchRequest{Project: project, UserText: "hi", WorkDirOverride: worktree})
	if cmd.Dir != worktree {
		t.Errorf("Dir = %q, want the worktree override %q", cmd.Dir, worktree)
	}

	cmd = a.ChatExecCmd(context.Background(), ChatDispatchRequest{UserText: "hi"})
	if cmd.Dir != "" {
		t.Errorf("Dir = %q, want it left unset when there is no project", cmd.Dir)
	}
}

// A follow-up turn passes --resume with the previous turn's session id.
func TestChatExecCmd_ResumeSessionID(t *testing.T) {
	a := &ClaudeAdapter{}
	const id = "3f1c2d4e-5a6b-4c7d-8e9f-0a1b2c3d4e5f"
	args := a.ChatExecCmd(context.Background(), ChatDispatchRequest{
		Project: "/p", UserText: "and then?", ResumeSessionID: id,
	}).Args

	got, ok := argAfter(args, "--resume")
	if !ok || got != id {
		t.Fatalf("--resume = %q ok=%v, want %q; args=%v", got, ok, id, args)
	}
	// The user text is still the last argv element, after the '--' sentinel,
	// so nothing about resume lets it be read as a flag.
	if args[len(args)-1] != "and then?" || args[len(args)-2] != "--" {
		t.Errorf("tail of argv must stay `-- <text>`; args=%v", args)
	}
	if indexOf(args, "--resume") > indexOf(args, "--") {
		t.Errorf("--resume must come before the '--' sentinel; args=%v", args)
	}
}

// First turns, and anything that is not a well-formed id, start a fresh session.
func TestChatExecCmd_NoResumeWhenAbsentOrInvalid(t *testing.T) {
	a := &ClaudeAdapter{}
	for _, id := range []string{
		"",
		"-resume-me",       // would parse as a flag
		"two words",        // not a single argv-safe token
		"id;rm -rf /",      // shell metacharacters
		"../../etc/passwd", // path characters
		"abc\ndef",         // newline
		strings.Repeat("a", 129),
	} {
		args := a.ChatExecCmd(context.Background(), ChatDispatchRequest{
			Project: "/p", UserText: "hi", ResumeSessionID: id,
		}).Args
		if _, ok := argAfter(args, "--resume"); ok {
			t.Errorf("ResumeSessionID %q must not reach argv; args=%v", id, args)
		}
	}
}

// The persona must be byte-identical turn to turn so the prompt cache keeps
// hitting (rule:cache-stability): resuming must not change the system prompt
// arguments.
func TestChatExecCmd_ResumeDoesNotChangePersonaArgs(t *testing.T) {
	a := &ClaudeAdapter{}
	base := ChatDispatchRequest{Project: "/p", UserText: "x", AgentSystemPrompt: "You are the backend agent."}
	first := a.ChatExecCmd(context.Background(), base).Args
	resumed := base
	resumed.ResumeSessionID = "abc-123"
	second := a.ChatExecCmd(context.Background(), resumed).Args

	p1, _ := argAfter(first, "--append-system-prompt")
	p2, _ := argAfter(second, "--append-system-prompt")
	if p1 == "" || p1 != p2 {
		t.Errorf("--append-system-prompt changed between first turn and resumed turn: %q vs %q", p1, p2)
	}
}

// Interactive sessions used to pass the model only as YAKOS_MODEL_OVERRIDE,
// which the claude CLI does not read, so the model picker did nothing.
func TestInteractiveExecCmd_PassesModelFlag(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"sonnet", "sonnet"},
		{"opus", "opus"},
		{"balanced", "sonnet"}, // alias expands exactly as on the other claude paths
		{"best", "opus"},
	}
	for _, c := range cases {
		cmd := InteractiveExecCmd("/p", "persona", c.in, "")
		got, ok := argAfter(cmd.Args, "--model")
		if !ok || got != c.want {
			t.Errorf("model %q: --model = %q ok=%v, want %q; args=%v", c.in, got, ok, c.want, cmd.Args)
		}
		// The env var stays for the hooks that read it.
		found := false
		for _, e := range cmd.Env {
			if e == "YAKOS_MODEL_OVERRIDE="+c.in {
				found = true
			}
		}
		if !found {
			t.Errorf("model %q: YAKOS_MODEL_OVERRIDE missing from env", c.in)
		}
	}
}

func TestInteractiveExecCmd_NoModelFlagWhenUnsetOrForeign(t *testing.T) {
	var drop bytes.Buffer
	old := modelDropLog
	modelDropLog = &drop
	defer func() { modelDropLog = old }()

	if _, ok := argAfter(InteractiveExecCmd("/p", "", "", "").Args, "--model"); ok {
		t.Error("empty model must not add --model")
	}
	if drop.Len() != 0 {
		t.Errorf("empty model must not log: %q", drop.String())
	}

	// A non-Claude id means nothing to claude; it is dropped, loudly.
	if _, ok := argAfter(InteractiveExecCmd("/p", "", "gpt-5", "").Args, "--model"); ok {
		t.Error("a non-claude model must not reach --model")
	}
	if !strings.Contains(drop.String(), "gpt-5") {
		t.Errorf("dropping a foreign model must be logged, got %q", drop.String())
	}
}

func TestResultSessionID(t *testing.T) {
	const id = "9b2f6c1e-1111-4222-8333-444455556666"
	cases := []struct {
		name, line, want string
	}{
		{"result line", `{"type":"result","subtype":"success","session_id":"` + id + `","result":"ok","total_cost_usd":0.001}`, id},
		{"explicit is_error false", `{"type":"result","subtype":"success","is_error":false,"session_id":"` + id + `"}`, id},
		// Recorded from claude 2.1.289: resuming an unknown id still stamps the
		// result with that id. It must not be taken for a live session.
		{"failed resume echoes the bad id", `{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"` + id + `","total_cost_usd":0}`, ""},
		{"result without an id", `{"type":"result","subtype":"success","result":"ok"}`, ""},
		{"other line types are ignored", `{"type":"assistant","session_id":"` + id + `"}`, ""},
		{"system init is ignored", `{"type":"system","subtype":"init","session_id":"` + id + `"}`, ""},
		{"id with a space", `{"type":"result","session_id":"two words"}`, ""},
		{"id that would read as a flag", `{"type":"result","session_id":"--model"}`, ""},
		{"id with shell metacharacters", `{"type":"result","session_id":"a;b"}`, ""},
		{"empty id", `{"type":"result","session_id":""}`, ""},
		{"non-string id", `{"type":"result","session_id":12345}`, ""},
		{"not json", `not json at all`, ""},
		{"empty", ``, ""},
	}
	for _, c := range cases {
		if got := ResultSessionID([]byte(c.line)); got != c.want {
			t.Errorf("%s: ResultSessionID = %q, want %q", c.name, got, c.want)
		}
	}
}
