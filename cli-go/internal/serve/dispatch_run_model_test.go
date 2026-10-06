package serve_test

// dispatch_run_model_test.go: JSON-RPC yakos.dispatch.run passes the model to
// dispatch as given, and dispatch checks it against the runtime that runs
// (K-132). The handler in methods.go validates nothing itself, so a model id
// from a non-Claude catalog must get through it, and a model the runtime cannot
// run must come back as dispatch's own per-runtime error.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// argvRecorder returns a stub script body that writes its arguments, one per
// line, to the returned file and then prints a line of plain text.
func argvRecorder(t *testing.T) (script, argvFile string) {
	t.Helper()
	argvFile = filepath.Join(t.TempDir(), "argv")
	return "printf '%s\\n' \"$@\" > '" + argvFile + "'\necho ok", argvFile
}

func recordedFlag(t *testing.T, argvFile, flag string) (string, bool) {
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

func TestMethod_DispatchRun_PassesAnAgyModelIDThrough(t *testing.T) {
	script, argv := argvRecorder(t)
	client := newDispatchDaemon(t, "agy", script)
	got, _ := callRun(t, client, map[string]string{
		"agent": "worker", "task": "t", "runtime": "agy", "model": "gemini-3.8-flash-high",
	})

	if got["runtime"] != "agy" || got["model_resolved"] != "gemini-3.8-flash-high" {
		t.Errorf("runtime/model_resolved = %v/%v", got["runtime"], got["model_resolved"])
	}
	if id, ok := recordedFlag(t, argv, "--model"); !ok || id != "gemini-3.8-flash-high" {
		t.Errorf("agy was run with --model %q (passed=%v), want gemini-3.8-flash-high", id, ok)
	}
}

func TestMethod_DispatchRun_RefusesAModelTheRuntimeCannotRun(t *testing.T) {
	cases := []struct{ name, rt, model, want string }{
		{"an id that is not a Claude tier on claude", "claude", "gpt-x", `invalid model tier "gpt-x" (must be haiku|sonnet|opus|fable)`},
		{"a Claude tier on agy", "agy", "sonnet", `invalid model "sonnet" for runtime agy`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script, argv := argvRecorder(t)
			client := newDispatchDaemon(t, tc.rt, script)
			_, err := client.Call(context.Background(), "yakos.dispatch.run",
				map[string]string{"agent": "worker", "task": "t", "runtime": tc.rt, "model": tc.model})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if _, statErr := os.Stat(argv); statErr == nil {
				t.Errorf("the %s runtime was spawned despite the refused model", tc.rt)
			}
		})
	}
}

// JSON-RPC: a runtime named in the params is not replaced when it cannot run.
func TestMethod_DispatchRun_ExplicitRuntimeDoesNotFallBack(t *testing.T) {
	script, argv := argvRecorder(t)
	client := newDispatchDaemon(t, "claude", script)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_HOME", t.TempDir())

	_, err := client.Call(context.Background(), "yakos.dispatch.run",
		map[string]string{"agent": "worker", "task": "t", "runtime": "codex"})
	if err == nil || !strings.Contains(err.Error(), "runtime codex was requested explicitly") {
		t.Fatalf("err = %v, want the explicit-runtime refusal", err)
	}
	if _, statErr := os.Stat(argv); statErr == nil {
		t.Error("claude was started although codex was named")
	}
}
