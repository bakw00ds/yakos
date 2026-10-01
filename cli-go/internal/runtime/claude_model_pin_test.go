package runtime

import (
	"context"
	"strings"
	"testing"
)

func argAfter(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func hasArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func baseReq() DispatchRequest {
	return DispatchRequest{
		Project:   "/tmp/project",
		AgentName: "backend",
		AgentJSON: `{"backend":{"description":"be","prompt":"p"}}`,
		Task:      "task",
	}
}

// K-116 W1: the outer relay session must be pinned to the resolved tier.
func TestClaudeExecCmd_PinsModel(t *testing.T) {
	a := &ClaudeAdapter{}
	for tier, want := range map[string]string{
		"haiku": "haiku", "sonnet": "sonnet", "opus": "opus", "fable": "claude-fable-5",
	} {
		req := baseReq()
		req.ModelOverride = tier
		cmd := a.ExecCmd(context.Background(), req)
		got, ok := argAfter(cmd.Args, "--model")
		if !ok || got != want {
			t.Errorf("tier %q: --model = %q (present=%v), want %q; argv=%v", tier, got, ok, want, cmd.Args)
		}
	}
}

func TestClaudeExecCmd_NoModelWhenEmpty(t *testing.T) {
	cmd := (&ClaudeAdapter{}).ExecCmd(context.Background(), baseReq())
	if hasArg(cmd.Args, "--model") {
		t.Errorf("--model must be absent when ModelOverride is empty: %v", cmd.Args)
	}
}

// K-116 W5: prefix-trim flags on the framed path.
func TestClaudeExecCmd_PrefixTrimFlags(t *testing.T) {
	cmd := (&ClaudeAdapter{}).ExecCmd(context.Background(), baseReq())
	if v, ok := argAfter(cmd.Args, "--setting-sources"); !ok || v != "project" {
		t.Errorf("--setting-sources project missing: %v", cmd.Args)
	}
	for _, f := range []string{"--strict-mcp-config", "--disable-slash-commands"} {
		if !hasArg(cmd.Args, f) {
			t.Errorf("%s missing: %v", f, cmd.Args)
		}
	}
	// The task must remain the value after -p (last element).
	if cmd.Args[len(cmd.Args)-2] != "-p" || !strings.HasSuffix(cmd.Args[len(cmd.Args)-1], "task") {
		t.Errorf("-p framed prompt must stay last: %v", cmd.Args)
	}
}

// Chat keeps user settings sources; model pinned only when explicit.
func TestClaudeChatExecCmd_ModelPinOnlyWhenExplicit(t *testing.T) {
	a := &ClaudeAdapter{}
	cmd := a.ChatExecCmd(context.Background(), ChatDispatchRequest{
		Project: "/tmp/p", UserText: "hi", ModelOverride: "sonnet"})
	if hasArg(cmd.Args, "--model") {
		t.Errorf("unpinned chat must not pass --model: %v", cmd.Args)
	}
	cmd = a.ChatExecCmd(context.Background(), ChatDispatchRequest{
		Project: "/tmp/p", UserText: "hi", ModelOverride: "haiku", ModelExplicit: true})
	if v, ok := argAfter(cmd.Args, "--model"); !ok || v != "haiku" {
		t.Errorf("explicit chat --model = %q: %v", v, cmd.Args)
	}
	for _, f := range []string{"--setting-sources", "--strict-mcp-config", "--disable-slash-commands"} {
		if hasArg(cmd.Args, f) {
			t.Errorf("chat must not get %s", f)
		}
	}
	if cmd.Args[len(cmd.Args)-2] != "--" {
		t.Errorf("'--' sentinel must stay second-to-last: %v", cmd.Args)
	}
}

// K-117: abstract aliases must never reach --model; unknown names are dropped.
func TestClaudeExecCmd_ModelAliasNeverAbstract(t *testing.T) {
	a := &ClaudeAdapter{}
	for in, want := range map[string]string{"balanced": "sonnet", "cheap": "haiku", "best": "opus", "frontier": "claude-fable-5"} {
		req := baseReq()
		req.AgentName = "supervisor"
		req.ModelOverride = in
		got, _ := argAfter(a.ExecCmd(context.Background(), req).Args, "--model")
		if got != want {
			t.Errorf("%q -> --model %q, want %q", in, got, want)
		}
	}
	req := baseReq()
	req.ModelOverride = "gpt-5"
	if hasArg(a.ExecCmd(context.Background(), req).Args, "--model") {
		t.Error("unknown model name must not be passed through")
	}
	req.AgentName, req.ModelOverride = "supervisor", "haiku"
	if got, _ := argAfter(a.ExecCmd(context.Background(), req).Args, "--model"); got != "haiku" {
		t.Errorf("supervisor haiku -> %q", got)
	}
}
