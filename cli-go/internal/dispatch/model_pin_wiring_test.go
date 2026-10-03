package dispatch

// K-116 wiring tests: the model pin must flow from override / agent
// frontmatter through Run (framed) and RunStream (chat) into the real claude
// argv. Run is exercised end to end with a fake `claude` on PATH that records
// its argv; RunStream through the streamRunFn seam plus the real adapter.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	rt "github.com/bakw00ds/yakos/internal/runtime"
)

func pinRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	agents := map[string]string{
		"pinned":     "model: haiku\n",
		"unpinned":   "",
		"aliased":    "model: balanced\n",
		"foreign":    "model: gpt-5\n",
		"fableagent": "model: fable\n",
		"supervisor": "model: haiku\n",
	}
	for name, fm := range agents {
		body := "---\nid: " + name + "\n" + fm + "---\n\n## Purpose\n\nTest agent " + name + ".\n"
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func modelArg(args []string) (string, bool) {
	for i, a := range args {
		if a == "--model" && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// fakeClaude puts a `claude` stub first on PATH that writes its argv (one per
// line) to the returned file.
func fakeClaude(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	bin := t.TempDir()
	out := filepath.Join(t.TempDir(), "argv.txt")
	script := "#!/bin/sh\n: > '" + out + "'\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> '" + out + "'; done\necho '{\"type\":\"result\",\"result\":\"ok\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YAKOS_ROOT", "")
	return out
}

func framedArgv(t *testing.T, root, agent, override string) []string {
	t.Helper()
	out := fakeClaude(t)
	_, res, err := Run(context.Background(), Request{
		AgentName: agent, Task: "t", Project: t.TempDir(), YakosRoot: root, Model: override,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = res
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("fake claude not invoked: %v", err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// Precedence override > agent pin > default, framed path.
func TestRun_ModelPinPrecedence(t *testing.T) {
	root := pinRoot(t)
	cases := []struct{ agent, override, want string }{
		{"pinned", "", "haiku"},        // agent pin
		{"pinned", "opus", "opus"},     // override beats pin
		{"unpinned", "", "sonnet"},     // default
		{"unpinned", "haiku", "haiku"}, // override beats default
		{"aliased", "", "sonnet"},      // balanced never abstract
		{"fableagent", "", "fable"},    // bare alias, not a pinned id
		{"foreign", "", "sonnet"},      // foreign model dropped -> default
	}
	for _, c := range cases {
		argv := framedArgv(t, root, c.agent, c.override)
		got, ok := modelArg(argv)
		if !ok || got != c.want {
			t.Errorf("%s/%q: --model=%q ok=%v want %q", c.agent, c.override, got, ok, c.want)
		}
		joined := strings.Join(argv, " ")
		for _, f := range []string{"--setting-sources", "--strict-mcp-config", "--disable-slash-commands"} {
			if !strings.Contains(joined, f) {
				t.Errorf("%s: %s missing", c.agent, f)
			}
		}
	}
}

// Chat path: ModelExplicit wiring. Pinned agent or override => --model;
// unpinned or foreign => none. Mutating stream.go's ModelExplicit to false
// fails the pinned/override rows.
func TestRunStream_ModelPinWiring(t *testing.T) {
	root := pinRoot(t)
	svc := NewService(ServiceConfig{YakosRoot: root, WorkspaceRoot: t.TempDir()})
	cases := []struct {
		agent, override string
		want            string // "" = no --model
	}{
		{"pinned", "", "haiku"},
		{"pinned", "opus", "opus"},
		{"unpinned", "", ""},
		{"unpinned", "haiku", "haiku"},
		{"aliased", "", "sonnet"},
		{"fableagent", "", "fable"},
		{"foreign", "", ""},
	}
	for _, c := range cases {
		var chat rt.ChatDispatchRequest
		withStreamRunFn(func(_ context.Context, _ Request, _ rt.Adapter, cr rt.ChatDispatchRequest, _ func(StreamChunk)) (Result, error) {
			chat = cr
			return Result{}, nil
		}, func() {
			if _, err := svc.RunStream(context.Background(), Params{Agent: c.agent, Task: "hi", Project: t.TempDir(), Model: c.override}, func(StreamChunk) {}); err != nil {
				t.Fatalf("%s: %v", c.agent, err)
			}
		})
		args := (&rt.ClaudeAdapter{}).ChatExecCmd(context.Background(), chat).Args
		got, ok := modelArg(args)
		if c.want == "" && ok {
			t.Errorf("%s/%q: unexpected --model %q", c.agent, c.override, got)
		}
		if c.want != "" && (!ok || got != c.want) {
			t.Errorf("%s/%q: --model=%q ok=%v want %q", c.agent, c.override, got, ok, c.want)
		}
	}
}

func opusSupervisorRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nid: supervisor\nmodel: opus\n---\n\n## Purpose\n\nSupervisor.\n"
	if err := os.WriteFile(filepath.Join(dir, "supervisor.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// K-119 + K-116: a supervisor PINNED to opus is clamped to the built-in
// sonnet ceiling AFTER pin resolution (the clamp must actually fire).
func TestRun_OpusPinnedSupervisorClampedToSonnet(t *testing.T) {
	argv := framedArgv(t, opusSupervisorRoot(t), "supervisor", "")
	if got, ok := modelArg(argv); !ok || got != "sonnet" {
		t.Fatalf("framed: --model=%q ok=%v want sonnet; argv=%v", got, ok, argv)
	}
}

func TestRunStream_OpusPinnedSupervisorClampedToSonnet(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc := NewService(ServiceConfig{YakosRoot: opusSupervisorRoot(t), WorkspaceRoot: t.TempDir()})
	var chat rt.ChatDispatchRequest
	withStreamRunFn(func(_ context.Context, _ Request, _ rt.Adapter, cr rt.ChatDispatchRequest, _ func(StreamChunk)) (Result, error) {
		chat = cr
		return Result{}, nil
	}, func() {
		if _, err := svc.RunStream(context.Background(), Params{Agent: "supervisor", Task: "hi", Project: t.TempDir()}, func(StreamChunk) {}); err != nil {
			t.Fatal(err)
		}
	})
	got, ok := modelArg((&rt.ClaudeAdapter{}).ChatExecCmd(context.Background(), chat).Args)
	if !ok || got != "sonnet" {
		t.Fatalf("chat: --model=%q ok=%v want sonnet", got, ok)
	}
}
