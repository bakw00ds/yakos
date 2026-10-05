package runtime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Golden argv tests for the codex and agy adapters (K-133). Every test starts
// from useEmptyHome so the operator's real ~/.yakos-state (router policy,
// codex-home profile) can never change an expectation.

const codexFramedPrefix = "Delegate this task to subagent named 'backend'. " +
	"Use the agent's discipline and report only the final result.\n\nTask:\n"

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits and exec.Cmd inspection; skipping on Windows")
	}
}

// writeRouterPolicy writes ~/.yakos-state/router-policy.yml under home.
func writeRouterPolicy(t *testing.T, home, body string, mode os.FileMode) string {
	t.Helper()
	dir := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "router-policy.yml")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func assertArgv(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("argv mismatch\n got: %q\nwant: %q", got, want)
	}
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

// ---- codex framed dispatch (ExecCmd) -----------------------------------------

func TestCodexExecCmd_DefaultIsSandboxed(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	project := t.TempDir()
	cmd := (&CodexAdapter{}).ExecCmd(context.Background(), DispatchRequest{
		Project: project, AgentName: "backend", Task: "do the thing",
	})
	assertArgv(t, cmd.Args, []string{
		"codex", "exec", "--json",
		"--sandbox", "workspace-write",
		"-c", `approval_policy="never"`,
		"--", codexFramedPrefix + "do the thing",
	})
	if cmd.Dir != project {
		t.Errorf("cmd.Dir = %q, want the project %q (agent files are looked up from the working directory)", cmd.Dir, project)
	}
	for _, bad := range []string{"--dangerously-bypass-approvals-and-sandbox", "--output-last-message", "--system-prompt", "--add-dir"} {
		if indexOf(cmd.Args, bad) >= 0 {
			t.Errorf("argv must not contain %s: %q", bad, cmd.Args)
		}
	}
}

func TestCodexExecCmd_ModelFlag(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	drops := captureModelDrops(t)
	for _, tc := range []struct {
		model   string
		want    string // "" means -m must be absent
		wantLog bool
	}{
		{"gpt-5.5", "gpt-5.5", false}, // a real catalog id passes through
		{"gpt-5.6-terra", "gpt-5.6-terra", false},
		{"gpt-5.1-codex-max", "gpt-5.1-codex-max", false},
		// A semantic alias has no codex mapping (the catalog's tier semantics are
		// undocumented): it resolves to the harness default, silently.
		{"cheap", "", false},
		{"balanced", "", false},
		{"best", "", false},
		{"reasoning", "", false},
		{"frontier", "", false},
		{"", "", false},
		{"sonnet", "", false}, // the dispatch layer's Claude default is not a codex model
		{"haiku", "", false},
		{"opus", "", false},
		{"fable", "", false},
		{"GPT-5", "", true},     // not a valid id: uppercase
		{"--help", "", true},    // must never become a flag
		{"gpt-5; id", "", true}, // shell metacharacters
		{"a b", "", true},       // whitespace
		{strings.Repeat("a", 65), "", true},
	} {
		drops.Reset()
		cmd := (&CodexAdapter{}).ExecCmd(context.Background(), DispatchRequest{
			Project: t.TempDir(), AgentName: "backend", Task: "t", ModelOverride: tc.model,
		})
		i := indexOf(cmd.Args, "-m")
		if tc.want == "" {
			if i >= 0 {
				t.Errorf("model %q: -m must be absent, got %q", tc.model, cmd.Args)
			}
		} else if i < 0 || i+1 >= len(cmd.Args) || cmd.Args[i+1] != tc.want {
			t.Errorf("model %q: want -m %s in %q", tc.model, tc.want, cmd.Args)
		}
		if (drops.Len() > 0) != tc.wantLog {
			t.Errorf("model %q: drop note = %q, want note=%v", tc.model, drops.String(), tc.wantLog)
		}
	}
}

func TestCodexExecCmd_Effort(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	for effort, want := range map[string]string{
		"low": "low", "medium": "medium", "high": "high", "xhigh": "xhigh", "max": "max",
		"": "", "bogus": "", "HIGH": "", "minimal": "", "--evil": "",
	} {
		cmd := (&CodexAdapter{}).ExecCmd(context.Background(), DispatchRequest{
			Project: t.TempDir(), AgentName: "backend", Task: "t", Effort: effort,
		})
		joined := strings.Join(cmd.Args, "\x00")
		wantArg := "model_reasoning_effort=" + strconv.Quote(want)
		if want == "" {
			if strings.Contains(joined, "model_reasoning_effort") {
				t.Errorf("effort %q: no override expected, got %q", effort, cmd.Args)
			}
		} else if !strings.Contains(joined, wantArg) {
			t.Errorf("effort %q: want %s in %q", effort, wantArg, cmd.Args)
		}
	}
}

func TestCodexExecCmd_ResumeUsesExecResumeNotTopLevelResume(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	const thread = "01a10c42-5f96-7bf2-b475-d4ce6e57540f"
	cmd := (&CodexAdapter{}).ExecCmd(context.Background(), DispatchRequest{
		Project: t.TempDir(), AgentName: "backend", Task: "follow up", ConversationID: thread,
		ModelOverride: "gpt-5",
	})
	assertArgv(t, cmd.Args, []string{
		"codex", "exec", "resume", "--json",
		"-m", "gpt-5",
		// `exec resume` accepts no --sandbox flag; the same policy goes in via -c.
		"-c", `sandbox_mode="workspace-write"`,
		"-c", `approval_policy="never"`,
		"--", thread, codexFramedPrefix + "follow up",
	})
	if cmd.Args[1] != "exec" {
		t.Errorf("the top-level `codex resume` is the interactive picker; argv[1] = %q", cmd.Args[1])
	}
	if indexOf(cmd.Args, "--sandbox") >= 0 || indexOf(cmd.Args, "--add-dir") >= 0 {
		t.Errorf("`exec resume` rejects --sandbox and --add-dir: %q", cmd.Args)
	}
}

func TestCodexExecCmd_WorkDirOverrideIsTheWorkspace(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	project, worktree := t.TempDir(), t.TempDir()
	cmd := (&CodexAdapter{}).ExecCmd(context.Background(), DispatchRequest{
		Project: project, WorkDirOverride: worktree, AgentName: "backend", Task: "t",
	})
	if cmd.Dir != worktree {
		t.Errorf("cmd.Dir = %q, want the worktree %q", cmd.Dir, worktree)
	}
	for _, a := range cmd.Args {
		if a == project {
			t.Errorf("the main checkout %q must not be added to the sandbox: %q", project, cmd.Args)
		}
	}
}

// ---- sandbox bypass policy ----------------------------------------------------

func TestCodexExecCmd_BypassOnlyViaTrustedPolicy(t *testing.T) {
	skipOnWindows(t)
	home := useEmptyHome(t)
	notes := captureSandboxNotes(t)
	writeRouterPolicy(t, home, "allow_unsandboxed_runtimes: [codex]\n", 0o600)

	cmd := (&CodexAdapter{}).ExecCmd(context.Background(), DispatchRequest{
		Project: t.TempDir(), AgentName: "backend", Task: "t",
	})
	assertArgv(t, cmd.Args, []string{
		"codex", "exec", "--json",
		"--dangerously-bypass-approvals-and-sandbox",
		"--", codexFramedPrefix + "t",
	})
	if !strings.Contains(notes.String(), "WITHOUT its sandbox") {
		t.Errorf("bypass must print a stderr note, got %q", notes.String())
	}
	// Once per process, not once per dispatch.
	notes.Reset()
	_ = (&CodexAdapter{}).ExecCmd(context.Background(), DispatchRequest{Project: t.TempDir(), AgentName: "backend", Task: "t"})
	if notes.Len() != 0 {
		t.Errorf("the bypass note must print once per process, got %q", notes.String())
	}

	// The resume form takes the same bypass flag.
	cmd = (&CodexAdapter{}).ExecCmd(context.Background(), DispatchRequest{
		Project: t.TempDir(), AgentName: "backend", Task: "t", ConversationID: "thread-1",
	})
	assertArgv(t, cmd.Args, []string{
		"codex", "exec", "resume", "--json",
		"--dangerously-bypass-approvals-and-sandbox",
		"--", "thread-1", codexFramedPrefix + "t",
	})
}

func TestCodexBypass_NotGrantedByOtherRuntimeEntry(t *testing.T) {
	skipOnWindows(t)
	home := useEmptyHome(t)
	writeRouterPolicy(t, home, "allow_unsandboxed_runtimes: [agy]\n", 0o600)
	cmd := (&CodexAdapter{}).ExecCmd(context.Background(), DispatchRequest{Project: t.TempDir(), AgentName: "backend", Task: "t"})
	if indexOf(cmd.Args, "--dangerously-bypass-approvals-and-sandbox") >= 0 {
		t.Errorf("an agy entry must not unsandbox codex: %q", cmd.Args)
	}
	if indexOf(cmd.Args, "--sandbox") < 0 {
		t.Errorf("codex must stay sandboxed: %q", cmd.Args)
	}
}

func TestCodexBypass_RefusedForUntrustedPolicyFiles(t *testing.T) {
	skipOnWindows(t)
	for name, setup := range map[string]func(t *testing.T, home string){
		"group-writable": func(t *testing.T, home string) {
			writeRouterPolicy(t, home, "allow_unsandboxed_runtimes: [codex, agy]\n", 0o660)
		},
		"world-writable": func(t *testing.T, home string) {
			writeRouterPolicy(t, home, "allow_unsandboxed_runtimes: [codex, agy]\n", 0o666)
		},
		"symlink": func(t *testing.T, home string) {
			real := filepath.Join(t.TempDir(), "real.yml")
			if err := os.WriteFile(real, []byte("allow_unsandboxed_runtimes: [codex, agy]\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(home, ".yakos-state")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, filepath.Join(dir, "router-policy.yml")); err != nil {
				t.Skipf("symlink unsupported: %v", err)
			}
		},
		"malformed": func(t *testing.T, home string) {
			writeRouterPolicy(t, home, "allow_unsandboxed_runtimes: [codex\n", 0o600)
		},
	} {
		t.Run(name, func(t *testing.T) {
			home := useEmptyHome(t)
			notes := captureSandboxNotes(t)
			setup(t, home)
			cmd := (&CodexAdapter{}).ExecCmd(context.Background(), DispatchRequest{Project: t.TempDir(), AgentName: "backend", Task: "t"})
			if indexOf(cmd.Args, "--dangerously-bypass-approvals-and-sandbox") >= 0 {
				t.Errorf("an untrusted policy file must not unsandbox codex: %q", cmd.Args)
			}
			if indexOf(cmd.Args, "--sandbox") < 0 {
				t.Errorf("codex must stay sandboxed: %q", cmd.Args)
			}
			if !strings.Contains(notes.String(), "stays sandboxed") {
				t.Errorf("an ignored policy file must be explained on stderr, got %q", notes.String())
			}
			agyCmd := (&AgyAdapter{}).ExecCmd(context.Background(), DispatchRequest{Project: t.TempDir(), AgentName: "backend", Task: "t"})
			if indexOf(agyCmd.Args, "--sandbox") < 0 {
				t.Errorf("agy must stay sandboxed too: %q", agyCmd.Args)
			}
		})
	}
}

// TestBypass_ProjectCannotEnable: a project can ship a .yakos.yml, a policy
// file, and an environment block that relocates the state directory
// (YAKOS_DISPATCH_LOG, K-129). None of it may unsandbox a runtime; only the
// owner-only file in the user's own ~/.yakos-state can.
func TestBypass_ProjectCannotEnable(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".yakos.yml"),
		[]byte("allow_unsandboxed_runtimes: [codex, agy]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(project, "state")
	if err := os.MkdirAll(planted, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planted, "router-policy.yml"),
		[]byte("allow_unsandboxed_runtimes: [codex, agy]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YAKOS_DISPATCH_LOG", planted)

	for _, argv := range [][]string{
		(&CodexAdapter{}).ExecCmd(context.Background(), DispatchRequest{Project: project, AgentName: "backend", Task: "t"}).Args,
		(&CodexAdapter{}).ChatExecCmd(context.Background(), ChatDispatchRequest{Project: project, UserText: "t"}).Args,
	} {
		if indexOf(argv, "--dangerously-bypass-approvals-and-sandbox") >= 0 || indexOf(argv, "--sandbox") < 0 {
			t.Errorf("project-supplied policy unsandboxed codex: %q", argv)
		}
	}
	for _, argv := range [][]string{
		(&AgyAdapter{}).ExecCmd(context.Background(), DispatchRequest{Project: project, AgentName: "backend", Task: "t"}).Args,
		(&AgyAdapter{}).ChatExecCmd(context.Background(), ChatDispatchRequest{Project: project, UserText: "t"}).Args,
	} {
		if indexOf(argv, "--sandbox") < 0 {
			t.Errorf("project-supplied policy unsandboxed agy: %q", argv)
		}
	}
}

// ---- codex chat ---------------------------------------------------------------

func TestCodexChatExecCmd_Default(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	project := t.TempDir()
	cmd := (&CodexAdapter{}).ChatExecCmd(context.Background(), ChatDispatchRequest{
		Project: project, UserText: "say hi",
	})
	assertArgv(t, cmd.Args, []string{
		"codex", "exec", "--json",
		"--sandbox", "workspace-write",
		"-c", `approval_policy="never"`,
		"--", "say hi",
	})
	if cmd.Dir != project {
		t.Errorf("cmd.Dir = %q, want project %q: chat must run where the project's rules and hooks load", cmd.Dir, project)
	}
	if indexOf(cmd.Args, "--system-prompt") >= 0 {
		t.Errorf("codex has no --system-prompt flag: %q", cmd.Args)
	}
}

func TestCodexChatExecCmd_PersonaRidesDeveloperInstructions(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	persona := "You are the lead.\nQuote \"this\" and a backslash \\ and C:\\dir\n\ttabbed\n"
	cmd := (&CodexAdapter{}).ChatExecCmd(context.Background(), ChatDispatchRequest{
		Project: t.TempDir(), UserText: "-- not a flag", AgentSystemPrompt: persona,
		ModelOverride: "gpt-5", Effort: "high",
	})
	assertArgv(t, cmd.Args, []string{
		"codex", "exec", "--json",
		"-m", "gpt-5",
		"-c", `model_reasoning_effort="high"`,
		"--sandbox", "workspace-write",
		"-c", `approval_policy="never"`,
		"-c", "developer_instructions=" + tomlString(persona),
		"--", "-- not a flag",
	})
	// The user text is the last element and only follows the sentinel.
	if cmd.Args[len(cmd.Args)-2] != "--" {
		t.Errorf("the sentinel must immediately precede the user text: %q", cmd.Args)
	}
}

func TestCodexChatExecCmd_BypassViaTrustedPolicyDropsSandboxFlags(t *testing.T) {
	skipOnWindows(t)
	home := useEmptyHome(t)
	writeRouterPolicy(t, home, "allow_unsandboxed_runtimes:\n  - codex\n", 0o600)
	cmd := (&CodexAdapter{}).ChatExecCmd(context.Background(), ChatDispatchRequest{Project: t.TempDir(), UserText: "hi"})
	assertArgv(t, cmd.Args, []string{
		"codex", "exec", "--json", "--dangerously-bypass-approvals-and-sandbox", "--", "hi",
	})
}

func TestCodexChatExecCmd_WorkDirOverride(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	worktree := t.TempDir()
	cmd := (&CodexAdapter{}).ChatExecCmd(context.Background(), ChatDispatchRequest{
		Project: t.TempDir(), WorkDirOverride: worktree, UserText: "hi",
	})
	if cmd.Dir != worktree {
		t.Errorf("cmd.Dir = %q, want worktree %q", cmd.Dir, worktree)
	}
}

// ---- TOML string encoding -----------------------------------------------------

func TestTomlString_RoundTripsAndEscapes(t *testing.T) {
	for _, s := range []string{
		"", "plain", "true", "123", "[array]", "# not a comment",
		"with \"quotes\" and \\ backslash", "multi\nline\r\nand\ttab",
		"\"\"\"triple\"\"\"", "unicode h\u00e9llo \u65e5\u672c\u8a9e \U0001F642",
		"control \x01 \x1f \x7f \x00 end",
	} {
		enc := tomlString(s)
		if !strings.HasPrefix(enc, `"`) || !strings.HasSuffix(enc, `"`) {
			t.Errorf("%q: encoded value must be a quoted string, got %s", s, enc)
		}
		for i := 0; i < len(enc); i++ {
			if c := enc[i]; c < 0x20 || c == 0x7f {
				t.Errorf("%q: raw control byte %#x in %s", s, c, enc)
			}
		}
		// TOML basic-string escapes used here (\", \\, \b \t \n \f \r, \uXXXX)
		// are also valid Go escapes, so strconv.Unquote decodes them.
		got, err := strconv.Unquote(enc)
		if err != nil || got != s {
			t.Errorf("%q: round trip = %q, %v", s, got, err)
		}
	}
	if got := tomlString("\x01\x7f"); got != `"\u0001\u007F"` {
		t.Errorf("control characters must use \\uXXXX escapes, got %s", got)
	}
}

// ---- agy ----------------------------------------------------------------------

func TestAgyExecCmd_Default(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	project := t.TempDir()
	cmd := (&AgyAdapter{}).ExecCmd(context.Background(), DispatchRequest{
		Project: project, AgentName: "security-reviewer", Task: "review auth",
	})
	assertArgv(t, cmd.Args, []string{
		"agy",
		"--add-dir", project,
		"--sandbox",
		"--dangerously-skip-permissions",
		"--output-format", "stream-json",
		"-p", "@yakos-security-reviewer review auth",
	})
	if cmd.Dir != project {
		t.Errorf("cmd.Dir = %q, want project %q", cmd.Dir, project)
	}
}

func TestAgyExecCmd_ModelEffortConversation(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	project := t.TempDir()
	// balanced resolves to gemini-3.8-flash-high. The id carries its own effort,
	// and agy rejects --effort next to it, so --effort is not passed.
	cmd := (&AgyAdapter{}).ExecCmd(context.Background(), DispatchRequest{
		Project: project, AgentName: "backend", Task: "t",
		ModelOverride: "balanced", Effort: "xhigh", ConversationID: "conv-123",
	})
	assertArgv(t, cmd.Args, []string{
		"agy",
		"--add-dir", project,
		"--sandbox",
		"--dangerously-skip-permissions",
		"--model", "gemini-3.8-flash-high",
		"--output-format", "stream-json",
		"--conversation", "conv-123",
		"-p", "@yakos-backend t",
	})
}

func TestAgyExecCmd_ModelResolution(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	captureModelDrops(t)
	for model, want := range map[string]string{
		"gemini-3.8-flash-low":   "gemini-3.8-flash-low", // ids come from `agy models`
		"claude-sonnet-5-5-high": "claude-sonnet-5-5-high",
		"gpt-oss-120b-medium":    "gpt-oss-120b-medium",
		"cheap":                  "gemini-3.8-flash-low",
		"balanced":               "gemini-3.8-flash-high",
		"best":                   "claude-opus-5-5-medium", // Antigravity can front Anthropic models
		"reasoning":              "gemini-3.1-pro-high",
		"frontier":               "claude-opus-5-5-high",
		"sonnet":                 "", // the dispatch layer's Claude default is not an agy model
		"":                       "",
		"--sandbox":              "", // never a flag
		"Gemini 3.1 Pro":         "",
	} {
		cmd := (&AgyAdapter{}).ExecCmd(context.Background(), DispatchRequest{
			Project: t.TempDir(), AgentName: "backend", Task: "t", ModelOverride: model,
		})
		i := indexOf(cmd.Args, "--model")
		switch {
		case want == "" && i >= 0:
			t.Errorf("model %q: --model must be absent, got %q", model, cmd.Args)
		case want != "" && (i < 0 || cmd.Args[i+1] != want):
			t.Errorf("model %q: want --model %s in %q", model, want, cmd.Args)
		}
	}
}

// TestAgyEffortIsOmittedWhenTheModelIDCarriesIt pins the live finding: agy 1.2.17
// answers `--model gemini-3.8-flash-low --effort high` with "invalid model
// selection ... conflicts with --effort=high" (exit 1), while --effort alone,
// with no --model, works. Every id agy lists ends in -low, -medium or -high.
func TestAgyEffortIsOmittedWhenTheModelIDCarriesIt(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	captureModelDrops(t)
	for _, tc := range []struct {
		name, model string
		wantEffort  bool
	}{
		{"suffixed id", "gemini-3.8-flash-low", false},
		{"suffixed id, other family", "claude-opus-5-5-medium", false},
		{"suffixed id, oss", "gpt-oss-120b-medium", false},
		{"alias that resolves to a suffixed id", "best", false},
		{"no model at all", "", true},
		{"Claude tier (dropped, so no model)", "sonnet", true},
		{"an id without a suffix", "some-future-model", true},
	} {
		for _, chat := range []bool{false, true} {
			var args []string
			if chat {
				args = (&AgyAdapter{}).ChatExecCmd(context.Background(), ChatDispatchRequest{
					Project: t.TempDir(), UserText: "hi", ModelOverride: tc.model, Effort: "high",
				}).Args
			} else {
				args = (&AgyAdapter{}).ExecCmd(context.Background(), DispatchRequest{
					Project: t.TempDir(), AgentName: "backend", Task: "t", ModelOverride: tc.model, Effort: "high",
				}).Args
			}
			i := indexOf(args, "--effort")
			if tc.wantEffort && (i < 0 || args[i+1] != "high") {
				t.Errorf("%s (chat=%v): want --effort high in %q", tc.name, chat, args)
			}
			if !tc.wantEffort && i >= 0 {
				t.Errorf("%s (chat=%v): agy rejects --effort next to a suffixed id; got %q", tc.name, chat, args)
			}
		}
	}
}

func TestAgyIDCarriesEffort(t *testing.T) {
	for id, want := range map[string]bool{
		"gemini-3.8-flash-high": true, "gemini-3.7-flash-medium": true, "gemini-3.6-flash-low": true,
		"gemini-3.1-pro-high": true, "gemini-3.1-pro-low": true, "claude-opus-5-5-low": true,
		"claude-sonnet-5-5-medium": true, "gpt-oss-120b-medium": true,
		"": false, "gemini-3.8-flash": false, "high": false, "flash-highest": false, "x-low-y": false,
	} {
		if got := agyIDCarriesEffort(id); got != want {
			t.Errorf("agyIDCarriesEffort(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestAgyEffort(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	for effort, want := range map[string]string{
		"low": "low", "medium": "medium", "high": "high", "xhigh": "xhigh", "max": "max",
		"": "", "minimal": "", "bogus": "", "--evil": "",
	} {
		cmd := (&AgyAdapter{}).ExecCmd(context.Background(), DispatchRequest{
			Project: t.TempDir(), AgentName: "backend", Task: "t", Effort: effort,
		})
		i := indexOf(cmd.Args, "--effort")
		switch {
		case want == "" && i >= 0:
			t.Errorf("effort %q: --effort must be absent, got %q", effort, cmd.Args)
		case want != "" && (i < 0 || cmd.Args[i+1] != want):
			t.Errorf("effort %q: want --effort %s in %q", effort, want, cmd.Args)
		}
	}
}

func TestAgyBypassOnlyViaTrustedPolicyKeepsSkipPermissions(t *testing.T) {
	skipOnWindows(t)
	home := useEmptyHome(t)
	notes := captureSandboxNotes(t)
	writeRouterPolicy(t, home, "allow_unsandboxed_runtimes: [agy]\n", 0o600)
	project := t.TempDir()
	cmd := (&AgyAdapter{}).ExecCmd(context.Background(), DispatchRequest{Project: project, AgentName: "backend", Task: "t"})
	assertArgv(t, cmd.Args, []string{
		"agy",
		"--add-dir", project,
		"--dangerously-skip-permissions", // headless agy has no approval surface
		"--output-format", "stream-json",
		"-p", "@yakos-backend t",
	})
	if !strings.Contains(notes.String(), "agy is running WITHOUT its sandbox") {
		t.Errorf("bypass must print a stderr note, got %q", notes.String())
	}
	// A codex-only entry does not unsandbox agy.
	writeRouterPolicy(t, home, "allow_unsandboxed_runtimes: [codex]\n", 0o600)
	cmd = (&AgyAdapter{}).ExecCmd(context.Background(), DispatchRequest{Project: project, AgentName: "backend", Task: "t"})
	if indexOf(cmd.Args, "--sandbox") < 0 {
		t.Errorf("a codex entry must not unsandbox agy: %q", cmd.Args)
	}
}

func TestAgyChatExecCmd_PersonaPrefixAndStreamJSON(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	project := t.TempDir()
	cmd := (&AgyAdapter{}).ChatExecCmd(context.Background(), ChatDispatchRequest{
		Project: project, UserText: "hello", AgentSystemPrompt: "You are the lead.",
		ModelOverride: "gemini-3.1-pro-high", Effort: "medium",
	})
	assertArgv(t, cmd.Args, []string{
		"agy",
		"--add-dir", project,
		"--sandbox",
		"--dangerously-skip-permissions",
		"--model", "gemini-3.1-pro-high", // carries its own effort: no --effort
		"--output-format", "stream-json",
		"-p", "You are the lead.\n\n---\n\nhello",
	})
	if cmd.Dir != project {
		t.Errorf("cmd.Dir = %q, want project %q", cmd.Dir, project)
	}
}

func TestAgyWorkDirOverrideIsTheWorkspace(t *testing.T) {
	skipOnWindows(t)
	useEmptyHome(t)
	project, worktree := t.TempDir(), t.TempDir()
	for _, cmd := range []*exec.Cmd{
		(&AgyAdapter{}).ExecCmd(context.Background(), DispatchRequest{Project: project, WorkDirOverride: worktree, AgentName: "backend", Task: "t"}),
		(&AgyAdapter{}).ChatExecCmd(context.Background(), ChatDispatchRequest{Project: project, WorkDirOverride: worktree, UserText: "t"}),
	} {
		if cmd.Dir != worktree {
			t.Errorf("cmd.Dir = %q, want worktree %q", cmd.Dir, worktree)
		}
		i := indexOf(cmd.Args, "--add-dir")
		if i < 0 || cmd.Args[i+1] != worktree {
			t.Errorf("--add-dir must be the worktree, not the main checkout: %q", cmd.Args)
		}
	}
}

// ---- alias table --------------------------------------------------------------

// TestHarnessModelAliasesMatchSettingsFile fails when the Go copy of the codex
// and agy alias columns drifts from lib/settings/model-aliases.json (the file
// the bash CLI reads).
func TestHarnessModelAliasesMatchSettingsFile(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "lib", "settings", "model-aliases.json"))
	if err != nil {
		t.Skipf("settings file not reachable from the package dir: %v", err)
	}
	var doc struct {
		Aliases map[string]map[string]string `json:"aliases"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, rt := range []string{"codex", "agy"} {
		for alias, perRuntime := range doc.Aliases {
			want, ok := perRuntime[rt]
			if !ok {
				t.Errorf("settings file has no %s entry for alias %q", rt, alias)
				continue
			}
			if got := harnessModelAliases[rt][alias]; got != want {
				t.Errorf("alias %q on %s: Go table = %q, settings file = %q", alias, rt, got, want)
			}
		}
		if len(harnessModelAliases[rt]) != len(doc.Aliases) {
			t.Errorf("%s: Go table has %d aliases, settings file has %d", rt, len(harnessModelAliases[rt]), len(doc.Aliases))
		}
	}
}

func TestHarnessModelIDResolvesOnlyKnownRuntimes(t *testing.T) {
	drops := captureModelDrops(t)
	if got := HarnessModelID("agy", "balanced"); got != "gemini-3.8-flash-high" {
		t.Errorf("agy balanced = %q", got)
	}
	// A runtime without a table passes a well-formed id through unchanged.
	if got := HarnessModelID("other", "some-model-1"); got != "some-model-1" {
		t.Errorf("unknown runtime passthrough = %q", got)
	}
	if drops.Len() != 0 {
		t.Errorf("unexpected drop note: %q", drops.String())
	}
}

// TestCodexAliasesAreEmptyOnPurpose: every codex alias maps to the empty string
// (harness default). The old gpt-5 / gpt-5-mini / gpt-5-nano / o4-mini ids are
// not in the live ChatGPT-login catalog, and codex rejects an unknown id with
// HTTP 400, so an alias must never turn into a -m the account cannot use.
func TestCodexAliasesAreEmptyOnPurpose(t *testing.T) {
	drops := captureModelDrops(t)
	for _, alias := range []string{"cheap", "balanced", "best", "reasoning", "frontier"} {
		if v, ok := harnessModelAliases["codex"][alias]; !ok || v != "" {
			t.Errorf("codex alias %q = %q (present=%v), want the empty string", alias, v, ok)
		}
		if got := HarnessModelID("codex", alias); got != "" {
			t.Errorf("HarnessModelID(codex, %q) = %q, want no model", alias, got)
		}
	}
	if drops.Len() != 0 {
		t.Errorf("an empty alias is the harness default, not a mistake; got note %q", drops.String())
	}
}
