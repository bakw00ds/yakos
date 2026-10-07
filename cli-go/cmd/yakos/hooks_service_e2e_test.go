package main

// hooks_service_e2e_test.go — K-145: fake codex and fake agy harnesses that do
// what the real ones do with an installed hooks.json: find the PreToolUse
// commands, run each through `sh -c` with the tool-call envelope on stdin, and
// honour the answer (codex: exit 2; agy: {"decision":"deny"}). The installed
// file and the real bin/yakos are used, so this proves the whole chain, not
// the shape of the command. Requires make build; skips like the other
// binary-driven tests.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooksinstall"
)

type svcEnv struct {
	bin  string
	env  []string
	work string
}

func newSvcEnv(t *testing.T) svcEnv {
	t.Helper()
	bin := hooksImplBinary(t)
	root := t.TempDir()
	pathDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(pathDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(pathDir, "yakos")); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	proj := filepath.Join(root, "proj")
	for _, d := range []string{home, proj} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	env := hooksImplEnv(home, filepath.Join(root, "work"), proj, "PATH="+pathDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return svcEnv{bin: bin, env: env, work: proj}
}

// runHookCommands plays a harness: run every command, return the first that
// blocks according to isBlock.
func (e svcEnv) runCommands(t *testing.T, cmds []string, payload []byte) (stdout string, exit int, ran int) {
	t.Helper()
	for _, c := range cmds {
		cmd := exec.Command("sh", "-c", c) //nolint:gosec
		cmd.Env = e.env
		cmd.Dir = t.TempDir() // agy runs hooks from the .agents dir, not the workspace
		cmd.Stdin = bytes.NewReader(payload)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("run %q: %v", c, err)
		}
		ran++
		if code == 2 || strings.Contains(out.String(), `"decision":"deny"`) {
			return out.String(), code, ran
		}
	}
	return "", 0, ran
}

func preCommands(t *testing.T, raw []byte, agy bool) []string {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if agy {
		if err := json.Unmarshal(doc["yakos"], &doc); err != nil {
			t.Fatal(err)
		}
	} else if err := json.Unmarshal(doc["hooks"], &doc); err != nil {
		t.Fatal(err)
	}
	var groups []struct {
		Hooks []struct{ Command string } `json:"hooks"`
	}
	if err := json.Unmarshal(doc["PreToolUse"], &groups); err != nil {
		t.Fatal(err)
	}
	var cmds []string
	for _, g := range groups {
		for _, h := range g.Hooks {
			cmds = append(cmds, h.Command)
		}
	}
	return cmds
}

func secretKey() string { return "sk-ant-" + strings.Repeat("a", 93) }

func TestFakeCodexBlocksEnvWriteViaHooksJSON(t *testing.T) {
	e := newSvcEnv(t)
	codexHome := t.TempDir()
	path, changed, err := hooksinstall.InstallShape("codex", codexHome, "")
	if err != nil || !changed {
		t.Fatalf("install: %v changed=%v", err, changed)
	}
	raw, _ := os.ReadFile(path)
	cmds := preCommands(t, raw, false)
	if len(cmds) != 3 {
		t.Fatalf("PreToolUse commands = %v", cmds)
	}

	patch := "*** Begin Patch\n*** Add File: .env\n+ANTHROPIC_API_KEY=" + secretKey() + "\n*** End Patch"
	payload, _ := json.Marshal(map[string]any{
		"session_id": "s", "cwd": e.work, "hook_event_name": "PreToolUse",
		"tool_name": "apply_patch", "tool_input": map[string]any{"input": patch},
	})
	out, code, _ := e.runCommands(t, cmds, payload)
	if code != 2 || out == "" {
		t.Fatalf("secret write not blocked: exit %d out %q", code, out)
	}

	ok, _ := json.Marshal(map[string]any{
		"session_id": "s", "cwd": e.work, "hook_event_name": "PreToolUse",
		"tool_name": "apply_patch", "tool_input": map[string]any{"input": "*** Begin Patch\n*** Add File: notes.txt\n+hello\n*** End Patch"},
	})
	if out, code, ran := e.runCommands(t, cmds, ok); code != 0 || ran != 3 {
		t.Fatalf("benign write blocked or short-circuited: exit %d ran %d out %q", code, ran, out)
	}
}

func TestFakeAgyBlocksEnvWriteViaHooksJSON(t *testing.T) {
	e := newSvcEnv(t)
	ws := t.TempDir()
	path, _, err := hooksinstall.InstallShape("agy", ws, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	cmds := preCommands(t, raw, true)

	payload, _ := json.Marshal(map[string]any{
		"conversationId": "c", "workspacePaths": []string{e.work},
		"toolCall": map[string]any{"name": "write_to_file", "args": map[string]any{
			"TargetFile": filepath.Join(e.work, ".env"), "CodeContent": "K=" + secretKey()}},
	})
	out, _, _ := e.runCommands(t, cmds, payload)
	var d struct{ Decision, Reason string }
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &d); err != nil || d.Decision != "deny" || d.Reason == "" {
		t.Fatalf("want deny JSON with a reason, got %q (%v)", out, err)
	}

	ok, _ := json.Marshal(map[string]any{
		"conversationId": "c", "workspacePaths": []string{e.work},
		"toolCall": map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": "ls"}},
	})
	if out, _, ran := e.runCommands(t, cmds, ok); out != "" || ran != 3 {
		t.Fatalf("benign command blocked: ran %d out %q", ran, out)
	}
}

func TestShapeUndecodableEnvelopeFailsClosed(t *testing.T) {
	e := newSvcEnv(t)
	for _, tc := range []struct {
		shape, want string
		exit        int
	}{{"codex", "", 2}, {"agy", `"decision":"deny"`, 0}} {
		cmd := exec.Command(e.bin, "hook", "run", "--shape", tc.shape, "secret-scan") //nolint:gosec
		cmd.Env = e.env
		cmd.Stdin = strings.NewReader("not json")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		if code != tc.exit || !strings.Contains(out.String(), tc.want) {
			t.Errorf("%s: exit %d out %q, want exit %d containing %q", tc.shape, code, out.String(), tc.exit, tc.want)
		}
	}
}

func TestShapeFlagValidation(t *testing.T) {
	e := newSvcEnv(t)
	for _, args := range [][]string{
		{"hook", "run", "--shape", "bogus", "secret-scan"},
		{"hook", "run", "--shape", "codex", "--impl", "go", "secret-scan"},
	} {
		cmd := exec.Command(e.bin, args...) //nolint:gosec
		cmd.Env = e.env
		if err := cmd.Run(); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
