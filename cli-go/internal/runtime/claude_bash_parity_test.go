package runtime

// K-116 parity: the bash dispatch path (cli/lib/runtimes/claude.sh) and the Go
// ExecCmd must build the same claude argv (modulo the --agents payload and
// framed prompt text) for the same agent and model.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func bashClaudeArgv(t *testing.T, libDir, root, project, agent, model string) ([]string, string) {
	t.Helper()
	bin := t.TempDir()
	out := filepath.Join(t.TempDir(), "argv.txt")
	stub := "#!/bin/sh\n: > '" + out + "'\nfor a in \"$@\"; do printf '%s\\0' \"$a\" >> '" + out + "'; done\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	script := `. "$YAKOS_LIB/runtimes/claude.sh"; yk_rt_claude_dispatch "$1" "$2" "task"`
	cmd := exec.Command("bash", "-c", script, "bash", project, agent)
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"YAKOS_LIB="+libDir, "YAKOS_ROOT="+root, "HOME="+t.TempDir(),
		"YAKOS_MODEL_OVERRIDE="+model, "YAKOS_CONVERSATION_ID=", "YAKOS_ALLOW_ROOT=0",
		"YAKOS_USAGE_OUT="+filepath.Join(t.TempDir(), "usage.json"), "YAKOS_SESSION_OUT=")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if b, err := cmd.Output(); err != nil {
		t.Fatalf("bash dispatch: %v\nstdout=%s\nstderr=%s", err, b, stderr.String())
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("stub claude not run: %v (stderr=%s)", err, stderr.String())
	}
	return strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00"), stderr.String()
}

// stripPayload removes the --agents value and the -p framed text (which are
// formatted differently by jq and Go but carry the same content).
func stripPayload(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--agents":
			i++
			out = append(out, "--agents", "<json>")
		case args[i] == "-p":
			out = append(out, "-p", "<framed>")
			i++
		default:
			out = append(out, args[i])
		}
	}
	return out
}

func TestClaudeBashGoArgvParity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash path")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	libDir, _ := filepath.Abs(filepath.Join("..", "..", "..", "cli", "lib"))
	if _, err := os.Stat(filepath.Join(libDir, "runtimes", "claude.sh")); err != nil {
		t.Skip("cli/lib not present")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "lib", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lib", "agents", "pinned.md"),
		[]byte("---\nid: pinned\nmodel: haiku\n---\n\n## Purpose\n\nTest agent.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()

	cases := []struct{ model, want string }{
		{"haiku", "haiku"}, {"sonnet", "sonnet"}, {"opus", "opus"}, {"fable", "fable"},
		{"balanced", "sonnet"}, {"cheap", "haiku"}, {"frontier", "fable"},
		{"gpt-5", ""}, {"", ""},
	}
	var goLog strings.Builder
	old := modelDropLog
	modelDropLog = &goLog
	defer func() { modelDropLog = old }()

	for _, c := range cases {
		bashArgs, bashErr := bashClaudeArgv(t, libDir, root, project, "pinned", c.model)
		goArgs := (&ClaudeAdapter{}).ExecCmd(context.Background(), DispatchRequest{
			Project: project, AgentName: "pinned", AgentJSON: `{"pinned":{}}`, Task: "task", ModelOverride: c.model,
		}).Args[1:] // drop argv[0]
		b, g := stripPayload(bashArgs), stripPayload(goArgs)
		if strings.Join(b, "\x00") != strings.Join(g, "\x00") {
			t.Errorf("model %q: argv differs\n bash: %v\n   go: %v", c.model, b, g)
		}
		got, _ := argAfter(bashArgs, "--model")
		if got != c.want {
			t.Errorf("model %q: bash --model=%q want %q", c.model, got, c.want)
		}
		if c.model == "gpt-5" && !strings.Contains(bashErr, "gpt-5") {
			t.Errorf("bash should log the dropped model, stderr=%q", bashErr)
		}
	}
}
