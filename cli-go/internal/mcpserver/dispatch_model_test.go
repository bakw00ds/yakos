package mcpserver_test

// dispatch_model_test.go: the yakos.dispatch tool takes the model values
// dispatch itself accepts (K-132): a semantic alias, a Claude tier on claude, or
// a model id from the chosen runtime's own catalog. The handler does not
// validate against the schema (the schema test is in dispatch_text_test.go); the
// per-runtime rule lives in dispatch, so these tests drive real calls through a
// real dispatch.Service and a fake runtime binary that records its argv.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/runtime"
)

// recordArgv rewrites the stub dispatchCfgWithFake installed for bin so that it
// also writes its arguments, one per line, to the file it returns. dispatchCfgWithFake
// put the stub first on PATH, so LookPath finds it and not a real install.
func recordArgv(t *testing.T, bin string) string {
	t.Helper()
	stub, err := exec.LookPath(bin)
	if err != nil {
		t.Fatalf("stub %s not on PATH: %v", bin, err)
	}
	orig, err := os.ReadFile(stub)
	if err != nil {
		t.Fatal(err)
	}
	argv := filepath.Join(t.TempDir(), "argv")
	script := strings.Replace(string(orig), "\n", "\nprintf '%s\\n' \"$@\" > '"+argv+"'\n", 1)
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	return argv
}

// recordedFlagValue returns the argv element after flag in the recorded file,
// and whether the flag was passed at all.
func recordedFlagValue(t *testing.T, argvFile, flag string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the runtime was never run: %v", err)
	}
	args := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// A model id from agy's own catalog is accepted and reaches the agy command line
// unchanged. Before, dispatch refused everything but the four Claude tiers and
// the schema enumerated them.
func TestDispatchTool_AcceptsAnAgyModelID(t *testing.T) {
	cfg := dispatchCfgWithFake(t, "agy", "ok")
	argv := recordArgv(t, "agy")
	got, _ := callDispatch(t, cfg, map[string]interface{}{
		"agent": "worker", "task": "t", "runtime": "agy", "model": "gemini-3.8-flash-high",
	})

	if got["runtime"] != "agy" || got["model_resolved"] != "gemini-3.8-flash-high" {
		t.Errorf("runtime/model_resolved = %v/%v", got["runtime"], got["model_resolved"])
	}
	if id, ok := recordedFlagValue(t, argv, "--model"); !ok || id != "gemini-3.8-flash-high" {
		t.Errorf("agy was run with --model %q (passed=%v), want gemini-3.8-flash-high", id, ok)
	}
}

// An alias is portable: on agy it becomes agy's own model id.
func TestDispatchTool_AliasBecomesTheRuntimesModel(t *testing.T) {
	want, ok := runtime.AliasModelFor("agy", "balanced")
	if !ok {
		t.Skip("the alias table maps balanced to nothing on agy")
	}
	cfg := dispatchCfgWithFake(t, "agy", "ok")
	argv := recordArgv(t, "agy")
	got, _ := callDispatch(t, cfg, map[string]interface{}{
		"agent": "worker", "task": "t", "runtime": "agy", "model": "balanced",
	})

	if got["model_resolved"] != want {
		t.Errorf("model_resolved = %v, want agy's %q", got["model_resolved"], want)
	}
	if id, _ := recordedFlagValue(t, argv, "--model"); id != want {
		t.Errorf("agy was run with --model %q, want %q", id, want)
	}
}

// A model the chosen runtime cannot run is refused with that runtime's own
// message, and nothing is spawned.
func TestDispatchTool_RefusesAModelTheRuntimeCannotRun(t *testing.T) {
	cases := []struct {
		name, rt, model string
		want            []string
	}{
		{"an id that is not a Claude tier on claude", "claude", "gpt-x",
			[]string{`invalid model tier "gpt-x"`, "haiku|sonnet|opus|fable"}},
		{"a Claude tier on agy", "agy", "sonnet",
			[]string{`invalid model "sonnet" for runtime agy`, "alias"}},
		{"a Claude tier on codex", "codex", "opus",
			[]string{`invalid model "opus" for runtime codex`, "alias"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := dispatchCfgWithFake(t, tc.rt, "should never run")
			argv := recordArgv(t, tc.rt)
			resp := findByID(t, session(t, cfg, callReq(1, "yakos.dispatch", map[string]interface{}{
				"agent": "worker", "task": "t", "runtime": tc.rt, "model": tc.model,
			})), 1)
			if !isToolError(resp) {
				t.Fatalf("%s %s was accepted: %s", tc.rt, tc.model, resultText(resp))
			}
			msg := resultText(resp)
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("error %q does not contain %q", msg, w)
				}
			}
			if _, err := os.Stat(argv); err == nil {
				t.Errorf("the %s runtime was spawned despite the refused model", tc.rt)
			}
		})
	}
}

// A runtime the caller names is not replaced by another vendor's when it cannot
// run, even though the project lists claude as a fallback (K-132). codex cannot
// run here whatever the machine: no key, an empty home and CODEX_HOME hold no
// login, and it may not be installed at all (the reason differs, the refusal not).
func TestDispatchTool_ExplicitRuntimeDoesNotFallBack(t *testing.T) {
	cfg := dispatchCfgWithFake(t, "claude", claudeStream("answered by claude")...)
	argv := recordArgv(t, "claude")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(cfg.WorkspaceRoot, ".yakos.yml"), []byte("default-fallback: [claude]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := findByID(t, session(t, cfg, callReq(1, "yakos.dispatch", map[string]interface{}{
		"agent": "worker", "task": "t", "runtime": "codex",
	})), 1)
	if !isToolError(resp) {
		t.Fatalf("a signed-out codex was answered by another runtime: %s", resultText(resp))
	}
	msg := resultText(resp)
	for _, want := range []string{"runtime codex was requested explicitly", "Not falling back to claude"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
	if _, err := os.Stat(argv); err == nil {
		t.Error("claude was started although codex was named")
	}
}
