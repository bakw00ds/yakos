package shaperun

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/registry"
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
