package main

// dispatch_runtime_cli_test.go: the CLI half of runtime routing (K-132). The
// routing rules are tested in internal/dispatch; these tests run the real
// `yakos dispatch` command (the router re-entered in a subprocess, with no vendor
// CLI on PATH unless a test adds a stub) and pin what only cmd_dispatch.go does:
// reading YAKOS_RUNTIME, parsing --runtime-fallback, and printing the errors.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// routingCLIRoot is a yakOS root with a few agents: plain (no pin), agy-pinned and
// two whose fallback lists name the bash-only claude-sdk.
func routingCLIRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, fm := range map[string]string{
		"plain":      "domain: misc\n",
		"agy-pinned": "domain: misc\nruntime: agy\n",
		// fallback lists that name a bash-only runtime, which --runtime-fallback rejects
		"sdk-fb":   "domain: misc\nruntime-fallback: [claude-sdk, claude]\n",
		"sdk-only": "domain: misc\nruntime-fallback: [claude-sdk]\n",
	} {
		body := "---\nid: " + id + "\n" + fm + "---\n\n## Purpose\n\nCLI routing fixture " + id + ".\n"
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// cliProject is a project directory whose .yakos.yml is yml ("" = none).
func cliProject(t *testing.T, yml string) string {
	t.Helper()
	dir := t.TempDir()
	if yml != "" {
		if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(yml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// fakeClaudeBin returns a directory holding a stub claude that records each run
// in the returned file, and that file.
func fakeClaudeBin(t *testing.T) (binDir, rec string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs")
	}
	binDir = t.TempDir()
	rec = filepath.Join(t.TempDir(), "claude-ran")
	script := "#!/bin/sh\nprintf 'ran\\n' >> '" + rec + "'\n" +
		`printf '%s\n' '{"type":"result","subtype":"success","result":"stub claude answer","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'` + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	return binDir, rec
}

func claudeRan(rec string) bool {
	b, err := os.ReadFile(rec)
	return err == nil && len(b) > 0
}

// dispatchCLI runs `yakos dispatch <args>` on the Go implementation.
func dispatchCLI(t *testing.T, root, project string, env []string, args ...string) (int, string) {
	t.Helper()
	all := append([]string{"dispatch"}, args...)
	all = append(all, "--project", project)
	return runYakos(t, t.TempDir(), append([]string{"YAKOS_IMPL=go", "YAKOS_ROOT=" + root}, env...), all...)
}

// YAKOS_RUNTIME is an ambient default: it picks the runtime for an agent that
// has no pin and a project with no default, and it loses to both. Nothing is
// installed here, so which runtime was tried first shows in the error.
func TestDispatchCLI_YAKOSRuntimeIsADefaultBelowPinsAndProjectConfig(t *testing.T) {
	root := routingCLIRoot(t)

	t.Run("it is used when nothing outranks it", func(t *testing.T) {
		code, out := dispatchCLI(t, root, cliProject(t, ""), []string{"YAKOS_RUNTIME=codex"}, "plain", "do it")
		if code == 0 || !strings.Contains(out, "codex: CLI not found") {
			t.Fatalf("exit %d; want a failure that tried codex first:\n%s", code, out)
		}
		if strings.Contains(out, "claude: CLI not found") {
			t.Errorf("claude was tried although YAKOS_RUNTIME named codex:\n%s", out)
		}
	})
	t.Run("an agent's pin outranks it", func(t *testing.T) {
		_, out := dispatchCLI(t, root, cliProject(t, ""), []string{"YAKOS_RUNTIME=codex"}, "agy-pinned", "do it")
		if !strings.Contains(out, "agy: CLI not found") || strings.Contains(out, "codex: CLI not found") {
			t.Errorf("the pin (agy) must win over YAKOS_RUNTIME (codex):\n%s", out)
		}
	})
	t.Run("the project's default-runtime outranks it", func(t *testing.T) {
		_, out := dispatchCLI(t, root, cliProject(t, "default-runtime: agy\n"), []string{"YAKOS_RUNTIME=codex"}, "plain", "do it")
		if !strings.Contains(out, "agy: CLI not found") || strings.Contains(out, "codex: CLI not found") {
			t.Errorf(".yakos.yml (agy) must win over YAKOS_RUNTIME (codex):\n%s", out)
		}
	})
	t.Run("it is shown as the reason", func(t *testing.T) {
		binDir, _ := fakeClaudeBin(t)
		// claude is installed, codex is not; YAKOS_RUNTIME=claude is honoured.
		_, out := dispatchCLI(t, root, cliProject(t, ""), []string{"YAKOS_RUNTIME=claude", "PATH=" + binDir}, "plain", "do it")
		if !strings.Contains(out, "runtime=claude (by:env)") {
			t.Errorf("the header does not name YAKOS_RUNTIME as the reason:\n%s", out)
		}
	})
	t.Run("a value that is not a runtime id is ignored with a message", func(t *testing.T) {
		_, out := dispatchCLI(t, root, cliProject(t, ""), []string{"YAKOS_RUNTIME=codex;rm -rf"}, "plain", "do it")
		if !strings.Contains(out, "ignoring YAKOS_RUNTIME: not a runtime id") {
			t.Errorf("no message for a malformed YAKOS_RUNTIME:\n%s", out)
		}
		if !strings.Contains(out, "claude: CLI not found") || strings.Contains(out, "codex") {
			t.Errorf("a malformed YAKOS_RUNTIME must fall through to the default (claude):\n%s", out)
		}
	})
}

// A runtime the operator names does not fall back to another vendor; the flag
// that opts in is the CLI's.
func TestDispatchCLI_ExplicitRuntimeFailsFastUnlessTheFlagOptsIn(t *testing.T) {
	root := routingCLIRoot(t)
	project := func() string { return cliProject(t, "default-fallback: [claude]\n") }

	t.Run("--runtime that cannot run fails and names the way out", func(t *testing.T) {
		binDir, rec := fakeClaudeBin(t)
		code, out := dispatchCLI(t, root, project(), []string{"PATH=" + binDir}, "plain", "do it", "--runtime", "codex")
		if code != 1 {
			t.Fatalf("exit %d, want 1:\n%s", code, out)
		}
		for _, want := range []string{
			"dispatch: runtime codex was requested explicitly but cannot run: CLI not found on PATH",
			"Not falling back to claude",
			"dispatch: to allow a fallback for this run, pass --runtime-fallback claude",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output does not contain %q:\n%s", want, out)
			}
		}
		if claudeRan(rec) || strings.Contains(out, "falling back to 'claude'") {
			t.Errorf("it answered from claude although codex was named:\n%s", out)
		}
	})

	t.Run("a runtime name used as the agent is just as explicit", func(t *testing.T) {
		binDir, rec := fakeClaudeBin(t)
		code, out := dispatchCLI(t, root, project(), []string{"PATH=" + binDir}, "codex", "do it")
		if code != 1 || !strings.Contains(out, "runtime codex was requested explicitly") || claudeRan(rec) {
			t.Errorf("exit %d, claude ran=%v:\n%s", code, claudeRan(rec), out)
		}
	})

	t.Run("--runtime-fallback opts in and the fallback is announced", func(t *testing.T) {
		binDir, rec := fakeClaudeBin(t)
		code, out := dispatchCLI(t, root, cliProject(t, ""), []string{"PATH=" + binDir}, "plain", "do it", "--runtime", "codex", "--runtime-fallback", "claude")
		if code != 0 || !claudeRan(rec) {
			t.Fatalf("exit %d, claude ran=%v:\n%s", code, claudeRan(rec), out)
		}
		if !strings.Contains(out, "falling back to 'claude'") || !strings.Contains(out, "stub claude answer") {
			t.Errorf("the fallback was not announced or its answer not printed:\n%s", out)
		}
	})

	t.Run("an agent's pin still falls back without the flag, as bash does", func(t *testing.T) {
		binDir, rec := fakeClaudeBin(t)
		code, out := dispatchCLI(t, root, cliProject(t, "default-fallback: [claude]\n"), []string{"PATH=" + binDir}, "agy-pinned", "do it")
		if code != 0 || !claudeRan(rec) || !strings.Contains(out, "falling back to 'claude'") {
			t.Errorf("exit %d, claude ran=%v:\n%s", code, claudeRan(rec), out)
		}
	})

	t.Run("--runtime-fallback only takes runtimes the dispatcher can run", func(t *testing.T) {
		code, out := dispatchCLI(t, root, cliProject(t, ""), nil, "plain", "do it", "--runtime-fallback", "claude,gemini")
		if code != 1 || !strings.Contains(out, "--runtime-fallback") || !strings.Contains(out, "unknown runtime") {
			t.Errorf("exit %d:\n%s", code, out)
		}
	})
}

// The hint that tells the operator how to opt in only suggests runtimes the flag
// accepts: a fallback list may name claude-sdk, which --runtime-fallback rejects
// as unknown, and a hint that is itself an error is worse than none.
func TestDispatchCLI_OptInHintOnlyNamesRunnableRuntimes(t *testing.T) {
	root := routingCLIRoot(t)
	binDir, _ := fakeClaudeBin(t)

	_, out := dispatchCLI(t, root, cliProject(t, ""), []string{"PATH=" + binDir}, "sdk-fb", "do it", "--runtime", "codex")
	if !strings.Contains(out, "Not falling back to claude-sdk, claude") {
		t.Errorf("the error should still list every unused fallback:\n%s", out)
	}
	if !strings.Contains(out, "pass --runtime-fallback claude\n") {
		t.Errorf("the hint should suggest only claude:\n%s", out)
	}
	if strings.Contains(out, "--runtime-fallback claude-sdk") {
		t.Errorf("the hint suggests a runtime the flag rejects:\n%s", out)
	}

	// With nothing runnable to suggest, the hint says what the flag takes.
	_, out = dispatchCLI(t, root, cliProject(t, ""), []string{"PATH=" + binDir}, "sdk-only", "do it", "--runtime", "codex")
	if !strings.Contains(out, "pass --runtime-fallback <runtime>[,<runtime>]") || strings.Contains(out, "--runtime-fallback claude-sdk") {
		t.Errorf("with no runnable fallback the hint should be generic:\n%s", out)
	}

	// And the hint's own advice works.
	code, out := dispatchCLI(t, root, cliProject(t, ""), []string{"PATH=" + binDir}, "sdk-fb", "do it", "--runtime", "codex", "--runtime-fallback", "claude")
	if code != 0 || !strings.Contains(out, "falling back to 'claude'") {
		t.Errorf("following the hint did not work: exit %d\n%s", code, out)
	}
}

// Errors print one "dispatch:" prefix, whatever package raised them.
func TestDispatchCLI_ErrorsPrintOneDispatchPrefix(t *testing.T) {
	root := routingCLIRoot(t)
	binDir, _ := fakeClaudeBin(t)
	cases := []struct {
		name string
		path string // the PATH the command runs with
		args []string
	}{
		{"an unknown agent", binDir, []string{"no-such-agent", "do it"}},
		{"no runtime available", t.TempDir(), []string{"plain", "do it"}},
		{"an invalid model", binDir, []string{"plain", "do it", "--model", "gpt-x"}},
		{"an unknown runtime", binDir, []string{"plain", "do it", "--runtime", "nope"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := dispatchCLI(t, root, cliProject(t, ""), []string{"PATH=" + tc.path}, tc.args...)
			if code == 0 {
				t.Fatalf("exit 0, want a failure:\n%s", out)
			}
			if strings.Contains(out, "dispatch: dispatch:") {
				t.Errorf("doubled prefix:\n%s", out)
			}
			if !strings.Contains(out, "\ndispatch: ") {
				t.Errorf("the error has no dispatch: prefix:\n%s", out)
			}
		})
	}
}
