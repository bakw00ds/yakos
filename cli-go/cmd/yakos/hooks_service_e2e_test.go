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
	bin    string
	env    []string
	work   string
	marker string // created by the planted ./yakos if a hook ever runs it
}

func newSvcEnv(t *testing.T) svcEnv {
	t.Helper()
	bin := hooksImplBinary(t)
	bin, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	if bin, err = filepath.EvalSymlinks(bin); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	proj := filepath.Join(root, "proj")
	for _, d := range []string{home, proj} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// yakos is NOT on PATH, and "." is: the installed absolute path must be what
	// runs, and a ./yakos planted in the harness's cwd must never run.
	env := hooksImplEnv(home, filepath.Join(root, "work"), proj, "PATH=."+string(os.PathListSeparator)+os.Getenv("PATH"))
	return svcEnv{bin: bin, env: env, work: proj, marker: filepath.Join(root, "HIJACKED")}
}

// runHookCommands plays a harness: run every command, return the first that
// blocks according to isBlock.
func (e svcEnv) runCommands(t *testing.T, cmds []string, payload []byte) (stdout string, exit int, ran int) {
	t.Helper()
	for _, c := range cmds {
		cmd := exec.Command("sh", "-c", c) //nolint:gosec
		cmd.Env = e.env
		cmd.Dir = t.TempDir() // agy runs hooks from the .agents dir, not the workspace
		hijack := "#!/bin/sh\ntouch '" + e.marker + "'\nexit 0\n"
		if err := os.WriteFile(filepath.Join(cmd.Dir, "yakos"), []byte(hijack), 0o755); err != nil { //nolint:gosec
			t.Fatal(err)
		}
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
		if _, err := os.Stat(e.marker); err == nil {
			t.Fatalf("a ./yakos planted in the harness cwd was run for %q", c)
		}
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
	path, changed, err := hooksinstall.InstallShape("codex", codexHome, e.bin)
	if err != nil || !changed {
		t.Fatalf("install: %v changed=%v", err, changed)
	}
	raw, _ := os.ReadFile(path)
	cmds := preCommands(t, raw, false)
	if len(cmds) != 3 {
		t.Fatalf("PreToolUse commands = %v", cmds)
	}
	for _, c := range cmds {
		if !strings.Contains(c, ` hook run --shape codex `) || !strings.HasSuffix(c, " "+e.bin) || !filepath.IsAbs(e.bin) {
			t.Errorf("hook command is not the absolute yakos path: %q", c)
		}
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
	path, _, err := hooksinstall.InstallShape("agy", ws, e.bin)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	cmds := preCommands(t, raw, true)
	for _, c := range cmds {
		if !strings.Contains(c, ` hook run --shape agy `) || !strings.HasSuffix(c, " "+e.bin) {
			t.Errorf("hook command is not the absolute yakos path: %q", c)
		}
	}

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

// CLAUDE_PROJECT_DIR is not set by codex or agy: the project is the envelope's
// workspace, and the dispatched agent comes from YAKOS_AGENT_TYPE (M1).
func TestFakeCodexWithoutClaudeProjectDirAppliesDispatchedAgentPolicy(t *testing.T) {
	e := newSvcEnv(t)
	var env []string
	for _, kv := range e.env {
		if !strings.HasPrefix(kv, "CLAUDE_PROJECT_DIR=") && !strings.HasPrefix(kv, "YAKOS_AGENT_TYPE=") {
			env = append(env, kv)
		}
	}
	if err := os.MkdirAll(filepath.Join(e.work, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	pol := `{"lead":{"allow":["**"]},"reviewer":{"deny":["src/**",".env"]}}`
	if err := os.WriteFile(filepath.Join(e.work, ".claude", "path-allowlist.json"), []byte(pol), 0o644); err != nil {
		t.Fatal(err)
	}
	codexHome := t.TempDir()
	path, _, err := hooksinstall.InstallShape("codex", codexHome, e.bin)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	cmds := preCommands(t, raw, false)
	payload, _ := json.Marshal(map[string]any{
		"session_id": "s", "cwd": e.work, "hook_event_name": "PreToolUse",
		"tool_name": "apply_patch", "tool_input": map[string]any{"input": "*** Begin Patch\n*** Add File: src/a.go\n+x\n*** End Patch"},
	})
	for _, tc := range []struct {
		agent string
		exit  int
	}{{"reviewer", 2}, {"lead", 0}, {"", 2}} {
		ee := e
		ee.env = append(append([]string{}, env...), "PWD="+e.work)
		if tc.agent != "" {
			ee.env = append(ee.env, "YAKOS_AGENT_TYPE="+tc.agent)
		}
		out, code, _ := ee.runCommands(t, cmds, payload)
		if code != tc.exit {
			t.Errorf("agent %q: exit %d (want %d) out %q", tc.agent, code, tc.exit, out)
		}
	}
}

// copyBin copies the yakos binary to a fresh path so a test can delete or
// chmod it.
func copyBin(t *testing.T, src string) string {
	t.Helper()
	data, err := os.ReadFile(src) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "yakos")
	if err := os.WriteFile(dst, data, 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	return dst
}

// K-170 (c): the installed hooks.json names an absolute binary, and when that
// binary is missing or not executable the harness's PreToolUse hook REFUSES
// the tool call instead of failing open. Both harnesses, from a hostile cwd.
func TestFakeHarnessesFailClosedWhenInstalledBinaryGone(t *testing.T) {
	for _, harness := range []string{"codex", "agy"} {
		for _, breakIt := range []string{"deleted", "not executable"} {
			e := newSvcEnv(t)
			bin := copyBin(t, e.bin)
			dir := t.TempDir()
			path, _, err := hooksinstall.InstallShape(harness, dir, bin)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(path)
			cmds := preCommands(t, raw, harness == "agy")
			var payload []byte
			if harness == "codex" {
				payload, _ = json.Marshal(map[string]any{"session_id": "s", "cwd": e.work, "hook_event_name": "PreToolUse",
					"tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}})
			} else {
				payload, _ = json.Marshal(map[string]any{"conversationId": "c", "workspacePaths": []string{e.work},
					"toolCall": map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": "ls"}}})
			}
			// Baseline: the installed binary allows a benign call.
			if out, code, _ := e.runCommands(t, cmds, payload); code != 0 || out != "" {
				t.Fatalf("%s: benign call blocked while the binary is installed: exit %d %q", harness, code, out)
			}
			if breakIt == "deleted" {
				err = os.Remove(bin)
			} else {
				err = os.Chmod(bin, 0o644)
			}
			if err != nil {
				t.Fatal(err)
			}
			out, code, ran := e.runCommands(t, cmds, payload)
			switch harness {
			case "codex":
				if code != 2 || !strings.Contains(out, "refusing the tool call") {
					t.Errorf("codex/%s: not refused: exit %d %q", breakIt, code, out)
				}
			default:
				var d struct{ Decision, Reason string }
				if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &d); err != nil || d.Decision != "deny" || d.Reason == "" {
					t.Errorf("agy/%s: want deny JSON, got %q (%v)", breakIt, out, err)
				}
			}
			if ran != 1 {
				t.Errorf("%s/%s: first hook should already refuse, ran %d", harness, breakIt, ran)
			}
		}
	}
}

// K-170 (d): with CLAUDE_PROJECT_DIR unset the project comes from the
// harness's absolute cwd / workspace, never from the hook process's cwd. An
// envelope that names no usable directory is refused by the fail-closed hooks.
func TestShapeWithoutClaudeProjectDirNeverUsesProcessCwd(t *testing.T) {
	e := newSvcEnv(t)
	var env []string
	for _, kv := range e.env {
		if !strings.HasPrefix(kv, "CLAUDE_PROJECT_DIR=") && !strings.HasPrefix(kv, "YAKOS_AGENT_TYPE=") {
			env = append(env, kv)
		}
	}
	e.env = append(env, "YAKOS_AGENT_TYPE=reviewer")
	if err := os.MkdirAll(filepath.Join(e.work, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	pol := `{"reviewer":{"deny":[".env"]}}`
	if err := os.WriteFile(filepath.Join(e.work, ".claude", "path-allowlist.json"), []byte(pol), 0o644); err != nil {
		t.Fatal(err)
	}
	codexWrite := func(cwd any) []byte {
		m := map[string]any{"session_id": "s", "hook_event_name": "PreToolUse", "tool_name": "Write",
			"tool_input": map[string]any{"file_path": ".env", "content": "x"}}
		if cwd != nil {
			m["cwd"] = cwd
		}
		b, _ := json.Marshal(m)
		return b
	}
	agyWrite := func(ws ...string) []byte {
		b, _ := json.Marshal(map[string]any{"conversationId": "c", "workspacePaths": ws,
			"toolCall": map[string]any{"name": "write_to_file", "args": map[string]any{"TargetFile": ".env", "CodeContent": "x"}}})
		return b
	}
	cx := t.TempDir()
	if _, _, err := hooksinstall.InstallShape("codex", cx, e.bin); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(hooksinstall.ShapeTarget("codex", cx))
	codexCmds := preCommands(t, raw, false)
	ws := t.TempDir()
	if _, _, err := hooksinstall.InstallShape("agy", ws, e.bin); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(hooksinstall.ShapeTarget("agy", ws))
	agyCmds := preCommands(t, raw, true)

	for name, tc := range map[string]struct {
		cmds    []string
		payload []byte
		agy     bool
	}{
		"codex absolute cwd, policy applies":     {codexCmds, codexWrite(e.work), false},
		"codex no cwd":                           {codexCmds, codexWrite(nil), false},
		"codex relative cwd":                     {codexCmds, codexWrite("proj"), false},
		"codex empty cwd":                        {codexCmds, codexWrite(""), false},
		"agy absolute workspace, policy applies": {agyCmds, agyWrite(e.work), true},
		"agy no workspace":                       {agyCmds, agyWrite(), true},
		"agy relative workspace":                 {agyCmds, agyWrite("proj"), true},
	} {
		out, code, _ := e.runCommands(t, tc.cmds, tc.payload)
		blocked := code == 2 || strings.Contains(out, `"decision":"deny"`)
		if !blocked {
			t.Errorf("%s: .env write was allowed (exit %d, out %q); the hook process cwd must not stand in for the project", name, code, out)
		}
	}
	// And a benign file in the right project is still allowed, so the denials
	// above are about the project, not a blanket refusal.
	ok, _ := json.Marshal(map[string]any{"session_id": "s", "hook_event_name": "PreToolUse", "tool_name": "Write", "cwd": e.work,
		"tool_input": map[string]any{"file_path": "notes.txt", "content": "x"}})
	if out, code, _ := e.runCommands(t, codexCmds, ok); code != 0 {
		t.Errorf("benign write in the envelope's project blocked: exit %d %q", code, out)
	}
}

// K-170 (b): through the real binary and the installed hooks files, a shell
// command that writes a forbidden file is refused in both harnesses, and a
// harmless one is not.
func TestFakeHarnessesRefuseShellWritesOutsidePolicy(t *testing.T) {
	e := newSvcEnv(t)
	var env []string
	for _, kv := range e.env {
		if !strings.HasPrefix(kv, "CLAUDE_PROJECT_DIR=") && !strings.HasPrefix(kv, "YAKOS_AGENT_TYPE=") {
			env = append(env, kv)
		}
	}
	e.env = append(env, "YAKOS_AGENT_TYPE=reviewer")
	if err := os.MkdirAll(filepath.Join(e.work, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.work, ".claude", "path-allowlist.json"), []byte(`{"reviewer":{"deny":[".env"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cx, ws := t.TempDir(), t.TempDir()
	if _, _, err := hooksinstall.InstallShape("codex", cx, e.bin); err != nil {
		t.Fatal(err)
	}
	if _, _, err := hooksinstall.InstallShape("agy", ws, e.bin); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(hooksinstall.ShapeTarget("codex", cx))
	codexCmds := preCommands(t, raw, false)
	raw, _ = os.ReadFile(hooksinstall.ShapeTarget("agy", ws))
	agyCmds := preCommands(t, raw, true)
	codex := func(cmd string) []byte {
		b, _ := json.Marshal(map[string]any{"session_id": "s", "cwd": e.work, "hook_event_name": "PreToolUse",
			"tool_name": "Bash", "tool_input": map[string]any{"command": cmd}})
		return b
	}
	agy := func(cmd string) []byte {
		b, _ := json.Marshal(map[string]any{"conversationId": "c", "workspacePaths": []string{e.work},
			"toolCall": map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": cmd}}})
		return b
	}
	for _, tc := range []struct {
		cmd  string
		deny bool
	}{
		{"echo K=1 > .env", true},
		{"printf x | tee -a .env", true},
		{"sed -i 's/a/b/' .env", true},
		{"cat <<EOF > .env\nK=1\nEOF", true},
		{"echo x > $(echo .env)", true},
		{"ls -la && echo done > notes.txt", false},
	} {
		for name, run := range map[string]func() (string, int, int){
			"codex": func() (string, int, int) { return e.runCommands(t, codexCmds, codex(tc.cmd)) },
			"agy":   func() (string, int, int) { return e.runCommands(t, agyCmds, agy(tc.cmd)) },
		} {
			out, code, _ := run()
			blocked := code == 2 || strings.Contains(out, `"decision":"deny"`)
			if blocked != tc.deny {
				t.Errorf("%s: %q blocked=%v, want %v (exit %d, out %q)", name, tc.cmd, blocked, tc.deny, code, out)
			}
		}
	}
}
