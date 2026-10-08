package shaperun

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/registry"
	"github.com/bakw00ds/yakos/internal/runtime"
)

func deps(t *testing.T, failOpen bool) Deps {
	t.Helper()
	wc := t.TempDir()
	return Deps{
		Env:       map[string]string{},
		FailOpen:  failOpen,
		YakosRoot: t.TempDir(),
		Resolve: func(string) (registry.Config, string) {
			return registry.Config{WorkCurrentDir: wc, ProjectDir: t.TempDir(), StateDir: t.TempDir()}, wc
		},
	}
}

func envelope(t *testing.T, shape, tool, path, content string) []byte {
	t.Helper()
	var v any
	if shape == "codex" {
		v = map[string]any{"hook_event_name": "PreToolUse", "tool_name": tool, "cwd": "/w",
			"tool_input": map[string]any{"file_path": path, "content": content}}
	} else {
		v = map[string]any{"workspacePaths": []string{"/w"}, "toolCall": map[string]any{
			"name": tool, "args": map[string]any{"TargetFile": path, "CodeContent": content}}}
	}
	b, _ := json.Marshal(v)
	return b
}

var key = "sk-ant-" + strings.Repeat("a", 93)

func TestRunBlocksSecretWriteInBothShapes(t *testing.T) {
	c := Run(context.Background(), "codex", "secret-scan", envelope(t, "codex", "Write", ".env", "K="+key), deps(t, false))
	if c.ExitCode != 2 || len(c.Stderr) == 0 {
		t.Errorf("codex: %+v", c)
	}
	a := Run(context.Background(), "agy", "secret-scan", envelope(t, "agy", "write_to_file", ".env", "K="+key), deps(t, false))
	if !strings.Contains(string(a.Stdout), `"decision":"deny"`) || a.ExitCode != 0 {
		t.Errorf("agy: %+v", a)
	}
	ok := Run(context.Background(), "agy", "secret-scan", envelope(t, "agy", "write_to_file", "a.txt", "hello"), deps(t, false))
	if !strings.Contains(string(ok.Stdout), `"allow"`) {
		t.Errorf("agy benign: %+v", ok)
	}
}

func TestRunDegradedPosture(t *testing.T) {
	// Fail-closed hook, garbage envelope: deny unless the operator override is set.
	if r := Run(context.Background(), "codex", "secret-scan", []byte("garbage"), deps(t, false)); r.ExitCode != 2 {
		t.Errorf("fail-closed hook passed garbage: %+v", r)
	}
	if r := Run(context.Background(), "codex", "secret-scan", []byte("garbage"), deps(t, true)); r.ExitCode != 0 {
		t.Errorf("FailOpen override ignored: %+v", r)
	}
	// Telemetry hook never blocks on garbage.
	if r := Run(context.Background(), "codex", "supervisor-stream", []byte("garbage"), deps(t, false)); r.ExitCode != 0 {
		t.Errorf("telemetry hook blocked: %+v", r)
	}
	// Unknown hook name allows.
	if r := Run(context.Background(), "codex", "no-such-hook", []byte("{}"), deps(t, false)); r.ExitCode != 0 || Known("no-such-hook") {
		t.Errorf("unknown hook: %+v", r)
	}
	// Non-tool events are not gated.
	ev := []byte(`{"hook_event_name":"SessionStart","cwd":"/w"}`)
	if r := Run(context.Background(), "codex", "secret-scan", ev, deps(t, false)); r.ExitCode != 0 {
		t.Errorf("SessionStart gated: %+v", r)
	}
	// Oversize is undecodable, so the fail-closed hook denies.
	big := []byte(`{"tool_name":"Write","x":"` + strings.Repeat("a", 1<<20) + `"}`)
	if r := Run(context.Background(), "codex", "secret-scan", big, deps(t, false)); r.ExitCode != 2 {
		t.Errorf("oversize passed: %+v", r)
	}
}

func TestRunMultiFilePatchBlocksOnAnyFile(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: ok.txt\n+hi\n*** Add File: .env\n+K=" + key + "\n*** End Patch"
	b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "apply_patch", "cwd": "/w", "tool_input": map[string]any{"input": patch}})
	if r := Run(context.Background(), "codex", "secret-scan", b, deps(t, false)); r.ExitCode != 2 {
		t.Errorf("secret in a patch passed: %+v", r)
	}
}

func TestRunPathAllowlistGatesEveryFileOfAPatch(t *testing.T) {
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	pol := `{"lead":{"allow":["**"],"deny":[".env"]}}`
	if err := os.WriteFile(filepath.Join(proj, ".claude", "path-allowlist.json"), []byte(pol), 0o644); err != nil {
		t.Fatal(err)
	}
	d := deps(t, false)
	d.Agent = "lead"
	wc := t.TempDir()
	d.Resolve = func(string) (registry.Config, string) {
		return registry.Config{WorkCurrentDir: wc, ProjectDir: proj, StateDir: t.TempDir()}, wc
	}
	run := func(files ...string) int {
		patch := "*** Begin Patch\n"
		for _, f := range files {
			patch += "*** Add File: " + f + "\n+x\n"
		}
		b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "apply_patch", "cwd": proj, "tool_input": map[string]any{"input": patch + "*** End Patch"}})
		return Run(context.Background(), "codex", "path-allowlist", b, d).ExitCode
	}
	if got := run("ok.txt"); got != 0 {
		t.Fatalf("allowed file blocked: %d", got)
	}
	if got := run("ok.txt", ".env"); got != 2 {
		t.Fatalf("denied second file passed: %d", got)
	}
}

// M1: a call is judged by the DISPATCHED agent's policy, not the lead's.
func TestRunPathAllowlistUsesDispatchedAgent(t *testing.T) {
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// "lead" may write anywhere; "reviewer" may not touch src/.
	pol := `{"lead":{"allow":["**"]},"reviewer":{"deny":["src/**"]}}`
	if err := os.WriteFile(filepath.Join(proj, ".claude", "path-allowlist.json"), []byte(pol), 0o644); err != nil {
		t.Fatal(err)
	}
	wc := t.TempDir()
	mk := func(agent string) Deps {
		d := deps(t, false)
		d.Agent = agent
		d.Resolve = func(string) (registry.Config, string) {
			return registry.Config{WorkCurrentDir: wc, ProjectDir: proj, StateDir: t.TempDir()}, wc
		}
		return d
	}
	b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Write", "cwd": proj,
		"tool_input": map[string]any{"file_path": filepath.Join(proj, "src", "a.go"), "content": "x"}})
	if got := Run(context.Background(), "codex", "path-allowlist", b, mk("reviewer")).ExitCode; got != 2 {
		t.Errorf("reviewer policy not applied: exit %d", got)
	}
	if got := Run(context.Background(), "codex", "path-allowlist", b, mk("lead")).ExitCode; got != 0 {
		t.Errorf("lead policy blocked: exit %d", got)
	}
	// An agent the policy does not know passes, as for Claude.
	if got := Run(context.Background(), "codex", "path-allowlist", b, mk("other")).ExitCode; got != 0 {
		t.Errorf("unlisted agent blocked: exit %d", got)
	}
	// Agent unknown (env did not reach the hook): most restrictive, never the lead.
	for _, bad := range []string{"", "bad name", "a;b", "../x", strings.Repeat("a", 65)} {
		if got := Run(context.Background(), "codex", "path-allowlist", b, mk(bad)).ExitCode; got != 2 {
			t.Errorf("agent %q: exit %d, want 2", bad, got)
		}
	}
	// The endpoint's route: the agent travels in the context.
	if got := Run(hookio.WithAgent(context.Background(), "reviewer"), "codex", "path-allowlist", b, mk("")).ExitCode; got != 2 {
		t.Errorf("context agent ignored: exit %d", got)
	}
	// With no policy file at all nothing is enforced and nothing is blocked.
	if err := os.Remove(filepath.Join(proj, ".claude", "path-allowlist.json")); err != nil {
		t.Fatal(err)
	}
	if got := Run(context.Background(), "codex", "path-allowlist", b, mk("")).ExitCode; got != 0 {
		t.Errorf("no policy file but blocked: exit %d", got)
	}
}

// B1: the agent a codex/agy chat command exports reaches the hook, so a chat
// write is judged by that agent's policy (and by "lead" for raw chat).
func TestChatEnvAgentDrivesPathPolicy(t *testing.T) {
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	pol := `{"lead":{"allow":["**"]},"backend":{"allow":["src/**"]}}`
	if err := os.WriteFile(filepath.Join(proj, ".claude", "path-allowlist.json"), []byte(pol), 0o644); err != nil {
		t.Fatal(err)
	}
	wc := t.TempDir()
	agentOf := func(env []string) string {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "YAKOS_AGENT_TYPE="); ok {
				return v
			}
		}
		return ""
	}
	write := func(agent, rel string) int {
		d := deps(t, false)
		d.Agent = agent
		d.Resolve = func(string) (registry.Config, string) {
			return registry.Config{WorkCurrentDir: wc, ProjectDir: proj, StateDir: t.TempDir()}, wc
		}
		b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Write", "cwd": proj,
			"tool_input": map[string]any{"file_path": filepath.Join(proj, rel), "content": "x"}})
		return Run(context.Background(), "codex", "path-allowlist", b, d).ExitCode
	}
	for name, envOf := range map[string]func(runtime.ChatDispatchRequest) []string{
		"codex": func(r runtime.ChatDispatchRequest) []string {
			return (&runtime.CodexAdapter{}).ChatExecCmd(context.Background(), r).Env
		},
		"agy": func(r runtime.ChatDispatchRequest) []string {
			return (&runtime.AgyAdapter{}).ChatExecCmd(context.Background(), r).Env
		},
	} {
		be := agentOf(envOf(runtime.ChatDispatchRequest{AgentName: "backend", UserText: "hi", Project: proj}))
		if got := write(be, "src/a.go"); got != 0 {
			t.Errorf("%s: allowed path refused for agent %q: exit %d", name, be, got)
		}
		if got := write(be, "README.md"); got != 2 {
			t.Errorf("%s: disallowed path passed for agent %q: exit %d", name, be, got)
		}
		if got := write(agentOf(envOf(runtime.ChatDispatchRequest{AgentName: "lead", UserText: "hi", Project: proj})), "README.md"); got != 0 {
			t.Errorf("%s: lead chat write refused: exit %d", name, got)
		}
	}
}

// K-170 item 0: a chat pane whose agent is the runtime name (codex, agy,
// claude) has no entry of its own in path-allowlist.json. It must be judged by
// the lead's policy, as a claude chat is, and never pass for lack of a policy.
func TestRuntimeNamedAgentFallsBackToLeadPolicy(t *testing.T) {
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	pol := `{"lead":{"allow":["src/**"]},"agy":{"allow":["docs/**"]}}`
	if err := os.WriteFile(filepath.Join(proj, ".claude", "path-allowlist.json"), []byte(pol), 0o644); err != nil {
		t.Fatal(err)
	}
	wc := t.TempDir()
	write := func(agent, rel string, env map[string]string) int {
		d := deps(t, false)
		d.Agent = agent
		if env != nil {
			d.Env = env
		}
		d.Resolve = func(string) (registry.Config, string) {
			return registry.Config{WorkCurrentDir: wc, ProjectDir: proj, StateDir: t.TempDir()}, wc
		}
		b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Write", "cwd": proj,
			"tool_input": map[string]any{"file_path": filepath.Join(proj, rel), "content": "x"}})
		return Run(context.Background(), "codex", "path-allowlist", b, d).ExitCode
	}
	for _, rt := range []string{"codex", "claude"} {
		if got := write(rt, "README.md", nil); got != 2 {
			t.Errorf("%s chat: write outside the lead allow-list passed (exit %d)", rt, got)
		}
		if got := write(rt, "src/a.go", nil); got != 0 {
			t.Errorf("%s chat: write inside the lead allow-list refused (exit %d)", rt, got)
		}
	}
	// An entry of the runtime's own name still wins over the fallback.
	if got := write("agy", "docs/a.md", nil); got != 0 {
		t.Errorf("agy: own entry not applied (exit %d)", got)
	}
	if got := write("agy", "src/a.go", nil); got != 2 {
		t.Errorf("agy: own entry did not restrict (exit %d)", got)
	}
	// A roster agent with no entry passes, exactly as on claude.
	if got := write("writer", "README.md", nil); got != 0 {
		t.Errorf("roster agent without an entry blocked (exit %d)", got)
	}
	// The harness environment cannot name a fallback for a roster agent.
	if got := write("writer", "README.md", map[string]string{"YAKOS_POLICY_FALLBACK_AGENT": "lead"}); got != 0 {
		t.Errorf("ambient fallback variable changed a roster agent's verdict (exit %d)", got)
	}
}

// K-170 (d, e): a project bound by the caller (the endpoint's nonce) wins over
// the envelope's cwd and over the daemon's own CLAUDE_PROJECT_DIR.
func TestBoundProjectOutranksEnvelopeAndEnvironment(t *testing.T) {
	bound, decoy := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(bound, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bound, ".claude", "path-allowlist.json"), []byte(`{"lead":{"deny":[".env"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	wc := t.TempDir()
	var resolved string
	d := deps(t, false)
	d.Agent = "lead"
	d.Env = map[string]string{"CLAUDE_PROJECT_DIR": decoy}
	d.Resolve = func(workDir string) (registry.Config, string) {
		resolved = workDir
		return registry.Config{WorkCurrentDir: wc, ProjectDir: workDir, StateDir: t.TempDir()}, wc
	}
	b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Write", "cwd": decoy,
		"tool_input": map[string]any{"file_path": ".env", "content": "x"}})
	if got := Run(hookio.WithProject(context.Background(), bound), "codex", "path-allowlist", b, d).ExitCode; got != 2 {
		t.Errorf("bound project's policy not applied: exit %d", got)
	}
	if resolved != bound {
		t.Errorf("hooks resolved the envelope's directory, not the bound project")
	}
	// Control: without a bound project the envelope's directory is the project.
	if got := Run(context.Background(), "codex", "path-allowlist", b, d).ExitCode; got != 0 {
		t.Errorf("control: unbound call blocked: exit %d", got)
	}
}

// K-170 (d): no trusted source for the project means a fail-closed hook refuses
// a PreToolUse call; telemetry and PostToolUse still pass.
func TestUnknownProjectFailsClosed(t *testing.T) {
	d := deps(t, false)
	d.Resolve = func(string) (registry.Config, string) { return registry.Config{}, "" }
	for _, shape := range []string{"codex", "agy"} {
		var env []byte
		if shape == "codex" {
			env = envelope(t, "codex", "Write", "a.txt", "hello")
		} else {
			env = envelope(t, "agy", "write_to_file", "a.txt", "hello")
		}
		r := Run(context.Background(), shape, "secret-scan", env, d)
		blocked := r.ExitCode == 2 || strings.Contains(string(r.Stdout), `"deny"`)
		if !blocked {
			t.Errorf("%s: fail-closed hook allowed a call with no known project: %+v", shape, r)
		}
		if strings.Contains(string(r.Stderr)+string(r.Stdout), "/w") {
			t.Errorf("%s: reason leaks a path: %+v", shape, r)
		}
	}
	// FailOpen is the operator's emergency override.
	d.FailOpen = true
	if r := Run(context.Background(), "codex", "secret-scan", envelope(t, "codex", "Write", "a.txt", "hello"), d); r.ExitCode != 0 {
		t.Errorf("YAKOS_HOOKS_FAIL_OPEN ignored: %+v", r)
	}
	// With CLAUDE_PROJECT_DIR set the project is known even if Resolve says "".
	d.FailOpen = false
	d.Env = map[string]string{"CLAUDE_PROJECT_DIR": t.TempDir()}
	if r := Run(context.Background(), "codex", "secret-scan", envelope(t, "codex", "Write", "a.txt", "hello"), d); r.ExitCode == 2 {
		t.Errorf("project from CLAUDE_PROJECT_DIR refused: %+v", r)
	}
}

// shellDeps builds Deps for a project that holds the given policy.
func shellDeps(t *testing.T, agent, policy string) Deps {
	t.Helper()
	proj := t.TempDir()
	if policy != "" {
		if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proj, ".claude", "path-allowlist.json"), []byte(policy), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wc := t.TempDir()
	d := deps(t, false)
	d.Agent = agent
	d.Resolve = func(string) (registry.Config, string) {
		return registry.Config{WorkCurrentDir: wc, ProjectDir: proj, StateDir: t.TempDir()}, wc
	}
	return d
}

func shellEnvelope(shape, cmd string, argv []string) []byte {
	var v any
	var cmdv any = cmd
	if argv != nil {
		cmdv = argv
	}
	if shape == "codex" {
		v = map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": cmdv}}
	} else {
		v = map[string]any{"toolCall": map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": cmd}}}
	}
	b, _ := json.Marshal(v)
	return b
}

func denied(r hookio.Response) bool {
	return r.ExitCode == 2 || strings.Contains(string(r.Stdout), `"decision":"deny"`)
}

// K-170 (b): a shell command that writes a file is refused by path-allowlist
// exactly as the Write tool is, in both harness shapes.
func TestShellWritesGatedByPathAllowlist(t *testing.T) {
	const policy = `{"reviewer":{"deny":[".env","secrets/**"]},"backend":{"allow":["src/**"]}}`
	cases := []struct {
		agent, cmd string
		deny       bool
	}{
		{"reviewer", "echo hi > .env", true},
		{"reviewer", "echo hi >> .env", true},
		{"reviewer", "echo hi | tee .env", true},
		{"reviewer", "cp notes.txt .env", true},
		{"reviewer", "mv a .env", true},
		{"reviewer", "sed -i 's/a/b/' .env", true},
		{"reviewer", "dd if=a of=.env", true},
		{"reviewer", "python3 -c \"open('.env','w').write('x')\"", true},
		{"reviewer", "cat <<EOF > .env\nK=1\nEOF", true},
		{"reviewer", "echo $(echo hi > .env)", true},
		{"reviewer", "bash -c 'echo hi > secrets/a.key'", true},
		{"reviewer", "cd secrets && echo hi > a.key", true},
		{"reviewer", "F=.env; echo hi > $F", true},
		{"reviewer", "echo hi > $OUT", true}, // deny-only policy: the dynamic rule is what refuses it
		{"reviewer", "echo hi | tee $(mktemp)", true},
		{"reviewer", "bash -c \"$CMD\"", true},
		{"reviewer", "echo hi > notes.txt", false},
		{"reviewer", "ls -la && git status", false},
		{"reviewer", "cat .env", false},
		{"backend", "echo hi > src/a.go", false},
		{"backend", "sed -i 's/a/b/' src/a.go src/b.go", false},
		{"backend", "cd src && echo hi > a.go", false},
		{"backend", "cp a src/b.go", false},
		{"backend", "echo hi > README.md", true},
		{"backend", "tee README.md", true},
		{"backend", "sed -i 's/a/b/' README.md", true},
		{"backend", "cp a ../outside", true},
		{"backend", "cd .. && echo hi > a", true},
		{"backend", "echo hi > /tmp/scratch", true},
		{"backend", "echo hi > $OUT", true},         // undecidable target under a policy
		{"backend", "echo hi > $(mktemp)", true},    // undecidable target under a policy
		{"backend", "echo 'echo x' | sh", true},     // script read from stdin
		{"backend", "cd src; echo hi > a.go", true}, // a failed cd would leave the shell elsewhere
		{"backend", "echo $(( $(echo hi > README.md; echo 1) + 1 ))", true},
		{"reviewer", "cd .claude && echo '{}' > path-allowlist.json", false}, // .claude/path-allowlist.json is not in this deny list
		{"reviewer", "echo hi > .en?", true},
		{"reviewer", "echo hi > $\".env\"", true},
		{"reviewer", "echo hi >! .env", true},
		{"reviewer", "rm .env", true},
		{"reviewer", "gofmt -w .env", true},
		{"backend", "echo hi > /dev/null", false},
		{"backend", "go test ./... 2>&1 | tail -5", false},
		{"nobody-listed", "echo hi > .env", false}, // no policy for this agent: as on claude
		{"nobody-listed", "echo hi > $OUT", false},
	}
	for _, shape := range []string{"codex", "agy"} {
		for _, c := range cases {
			d := shellDeps(t, c.agent, policy)
			r := Run(context.Background(), shape, "path-allowlist", shellEnvelope(shape, c.cmd, nil), d)
			if denied(r) != c.deny {
				t.Errorf("%s/%s: %q denied=%v, want %v (%+v)", shape, c.agent, c.cmd, denied(r), c.deny, r)
			}
		}
	}
	// codex sends an argv array for some shell tools.
	d := shellDeps(t, "reviewer", policy)
	if r := Run(context.Background(), "codex", "path-allowlist", shellEnvelope("codex", "", []string{"bash", "-lc", "echo hi > .env"}), d); !denied(r) {
		t.Errorf("argv-form command not gated: %+v", r)
	}
	// With no policy file nothing is enforced, dynamic targets included.
	d = shellDeps(t, "backend", "")
	if r := Run(context.Background(), "codex", "path-allowlist", shellEnvelope("codex", "echo hi > $OUT; echo x > .env", nil), d); denied(r) {
		t.Errorf("blocked with no policy file: %+v", r)
	}
	// A refusal names the problem, not the command.
	d = shellDeps(t, "backend", policy)
	r := Run(context.Background(), "codex", "path-allowlist", shellEnvelope("codex", "echo hi > README.md", nil), d)
	if !strings.Contains(string(r.Stderr), "README.md") || !strings.Contains(string(r.Stderr), "allow-list") {
		t.Errorf("reason does not name the path and the rule: %q", r.Stderr)
	}
}

// A PostToolUse shell envelope is not decoded for writes (nothing to prevent).
func TestShellWritesNotDecodedAfterTheFact(t *testing.T) {
	d := shellDeps(t, "reviewer", `{"reviewer":{"deny":[".env"]}}`)
	b, _ := json.Marshal(map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Bash", "cwd": "/w",
		"tool_input": map[string]any{"command": "echo hi > .env"}, "tool_response": map[string]any{"stdout": ""}})
	if r := Run(context.Background(), "codex", "path-allowlist", b, d); denied(r) {
		t.Errorf("PostToolUse blocked: %+v", r)
	}
}

// K-170 (b): secret-scan sees a secret written by a shell command.
func TestShellWritesGatedBySecretScan(t *testing.T) {
	for _, shape := range []string{"codex", "agy"} {
		d := shellDeps(t, "lead", "")
		bad := "echo ANTHROPIC_API_KEY=" + key + " > .env"
		if r := Run(context.Background(), shape, "secret-scan", shellEnvelope(shape, bad, nil), d); !denied(r) {
			t.Errorf("%s: a secret written by echo was not blocked: %+v", shape, r)
		}
		if r := Run(context.Background(), shape, "secret-scan", shellEnvelope(shape, "echo hello > notes.txt", nil), d); denied(r) {
			t.Errorf("%s: a benign write blocked: %+v", shape, r)
		}
		// Not a write: nothing to prevent, as before.
		if r := Run(context.Background(), shape, "secret-scan", shellEnvelope(shape, "echo "+key, nil), d); denied(r) {
			t.Errorf("%s: a command that writes nothing was blocked: %+v", shape, r)
		}
	}
}

// One synthesized Write per decoded target, carrying the command as content.
func TestShellWriteInputsOnePerTarget(t *testing.T) {
	ins, err := hookio.DecodeShape("codex", shellEnvelope("codex", "echo a > f; echo b > g", nil))
	if err != nil {
		t.Fatal(err)
	}
	extra := hookio.ShellWriteInputs(ins)
	if len(extra) != 2 || extra[0].Tool != "Write" || extra[1].Tool != "Write" {
		t.Fatalf("synthesized inputs: %+v", extra)
	}
	if hookio.ToolFilePath(extra[0]) != "f" || hookio.ToolFilePath(extra[1]) != "g" {
		t.Errorf("targets: %q %q", hookio.ToolFilePath(extra[0]), hookio.ToolFilePath(extra[1]))
	}
	if len(hookio.ShellWriteInputs([]hooktype.HookInput{{Tool: "Read", Event: "PreToolUse"}})) != 0 {
		t.Error("a non-shell tool produced writes")
	}
}

// Relative patch paths are relative to the envelope's cwd, which may be a
// subdirectory of the project: "src/evil.go" from P/other lands in
// P/other/src/evil.go, outside an allow-list of src/**.
func TestRelativePathsAreJudgedFromTheEnvelopeCwd(t *testing.T) {
	proj, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".claude", "path-allowlist.json"), []byte(`{"backend":{"allow":["src/**"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	wc := t.TempDir()
	run := func(cwd, tool string, input map[string]any) int {
		d := deps(t, false)
		d.Agent = "backend"
		d.Resolve = func(string) (registry.Config, string) {
			return registry.Config{WorkCurrentDir: wc, ProjectDir: proj, StateDir: t.TempDir()}, wc
		}
		b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": tool, "cwd": cwd, "tool_input": input})
		return Run(hookio.WithProject(context.Background(), proj), "codex", "path-allowlist", b, d).ExitCode
	}
	patch := map[string]any{"input": "*** Begin Patch\n*** Add File: src/evil.go\n+x\n*** End Patch"}
	if got := run(filepath.Join(proj, "other"), "apply_patch", patch); got != 2 {
		t.Errorf("patch from a subdirectory cwd: exit %d, want 2", got)
	}
	if got := run(proj, "apply_patch", patch); got != 0 {
		t.Errorf("patch from the project root: exit %d, want 0", got)
	}
	// From the allowed subdirectory the same relative path is fine.
	if got := run(filepath.Join(proj, "src"), "Write", map[string]any{"file_path": "a.go", "content": "x"}); got != 0 {
		t.Errorf("write inside src from cwd src: exit %d, want 0", got)
	}
	if got := run(filepath.Join(proj, "src"), "Write", map[string]any{"file_path": "../README.md", "content": "x"}); got != 2 {
		t.Errorf("traversal from cwd src: exit %d, want 2", got)
	}
	if got := run(filepath.Join(proj, "other"), "Write", map[string]any{"file_path": "src/evil.go", "content": "x"}); got != 2 {
		t.Errorf("write from a subdirectory cwd: exit %d, want 2", got)
	}
}

// A shell redirect is relative to the envelope's cwd as well.
func TestShellWriteFromSubdirectoryCwd(t *testing.T) {
	d := shellDeps(t, "backend", `{"backend":{"allow":["src/**"]}}`)
	cfg, _ := d.Resolve("")
	ctx := hookio.WithProject(context.Background(), cfg.ProjectDir)
	mk := func(cwd string) []byte {
		b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "cwd": cwd,
			"tool_input": map[string]any{"command": "echo x > src/evil.go"}})
		return b
	}
	if r := Run(ctx, "codex", "path-allowlist", mk(filepath.Join(cfg.ProjectDir, "other")), d); !denied(r) {
		t.Errorf("redirect from a subdirectory cwd allowed: %+v", r)
	}
	if r := Run(ctx, "codex", "path-allowlist", mk(cfg.ProjectDir), d); denied(r) {
		t.Errorf("redirect from the project root blocked: %+v", r)
	}
}

// Probe (K-170 b): the policy file and the agy hooks file are ordinary project
// paths. An allow-list that covers them lets a shell redirect rewrite them, as
// the Write tool could; listing them in deny stops it. The codex profile
// hooks.json lives outside the project and is refused as absolute.
func TestShellWritesToPolicyAndHooksFiles(t *testing.T) {
	targets := []string{".claude/path-allowlist.json", ".agents/hooks.json"}
	for _, tc := range []struct {
		name, policy string
		deny         bool
	}{
		{"allow everything", `{"backend":{"allow":["**"]}}`, false},
		{"allow src only", `{"backend":{"allow":["src/**"]}}`, true},
		{"deny listed", `{"backend":{"allow":["**"],"deny":[".claude/**",".agents/**"]}}`, true},
	} {
		for _, tgt := range targets {
			d := shellDeps(t, "backend", tc.policy)
			r := Run(context.Background(), "codex", "path-allowlist", shellEnvelope("codex", "echo '{}' > "+tgt, nil), d)
			if denied(r) != tc.deny {
				t.Errorf("%s: write to %s denied=%v, want %v", tc.name, tgt, denied(r), tc.deny)
			}
		}
	}
	d := shellDeps(t, "backend", `{"backend":{"allow":["**"]}}`)
	r := Run(context.Background(), "codex", "path-allowlist", shellEnvelope("codex", "echo '{}' > /home/u/.yakos-state/codex-home/hooks.json", nil), d)
	if !denied(r) {
		t.Errorf("absolute hooks.json outside the project allowed: %+v", r)
	}
}

// A command field the decoder cannot read is refused under a policy.
func TestUnreadableShellCommandRefusedUnderPolicy(t *testing.T) {
	d := shellDeps(t, "backend", `{"backend":{"deny":[".env"]}}`)
	for name, cmd := range map[string]any{"object": map[string]any{"x": 1}, "number": 5, "argv with a number": []any{"tee", ".env", 1}} {
		b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": cmd}})
		if r := Run(context.Background(), "codex", "path-allowlist", b, d); !denied(r) {
			t.Errorf("%s: not refused: %+v", name, r)
		}
	}
	// and a glob target is, too
	for _, c := range []string{"echo K=1 > .en?", "tee .en*", "echo x > $'\\x2e\\x65\\x6e\\x76'"} {
		if r := Run(context.Background(), "codex", "path-allowlist", shellEnvelope("codex", c, nil), d); !denied(r) {
			t.Errorf("%q not refused: %+v", c, r)
		}
	}
}
