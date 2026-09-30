package supervisorstream_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
)

// K-111 P2b: shadow decision call from the supervisor pre-filter.

func shadowEnv(kv ...string) map[string]string {
	env := map[string]string{"YAKOS_CLI": "/fake/yakos"}
	for i := 0; i+1 < len(kv); i += 2 {
		env[kv[i]] = kv[i+1]
	}
	return env
}

func runShadow(t *testing.T, yml string, in hooktype.HookInput, env map[string]string) (*recorder, hooktype.HookOutput) {
	t.Helper()
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, yml)
	if err := os.WriteFile(filepath.Join(work, "decisions.md"), []byte("fix the retry test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	h := newHook(work, proj)
	h.Launch = rec.launch
	in.Env = env
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return rec, out
}

func bashInput(cmd string) hooktype.HookInput {
	return hooktype.HookInput{Tool: "Bash", Payload: map[string]any{
		"session_id": "sess-1", "tool_input": map[string]any{"command": cmd}}}
}

const ssYML = "supervisor:\n  score_every_n_calls: 1000\n"

func TestShadow_ProviderNoneNeverLaunches(t *testing.T) {
	for name, tc := range map[string]struct {
		yml string
		env map[string]string
	}{
		"absent":          {ssYML, shadowEnv()},
		"none in yml":     {ssYML + "decisions:\n  provider: none\n", shadowEnv()},
		"env none":        {ssYML, shadowEnv("YAKOS_DECISION_PROVIDER", "none")},
		"unknown":         {ssYML + "decisions:\n  provider: bogus\n", shadowEnv()},
		"jev without key": {ssYML + "decisions:\n  provider: jev\n", shadowEnv()},
		"kill switch":     {ssYML + "decisions:\n  provider: mock\n", shadowEnv("YAKOS_DECISION_DISABLE", "1")},
		"malformed block": {ssYML + "decisions: [1, 2]\n", shadowEnv()},
	} {
		rec, out := runShadow(t, tc.yml, bashInput("rm -rf /"), tc.env)
		if len(rec.specs) != 0 || out.ExitCode != 0 {
			t.Errorf("%s: launches=%d exit=%d, want none", name, len(rec.specs), out.ExitCode)
		}
	}
}

func TestShadow_MockProviderLaunchesDecideWithLocalVerdict(t *testing.T) {
	rec, out := runShadow(t, ssYML+"decisions:\n  provider: mock\n", bashInput("git push --force origin main"), shadowEnv())
	if out.ExitCode != 0 || out.Stdout != nil && len(out.Stdout) != 0 {
		t.Fatalf("exit=%d stdout=%q: shadow must not change the hook result", out.ExitCode, out.Stdout)
	}
	if len(rec.specs) != 1 {
		t.Fatalf("launches = %d, want 1", len(rec.specs))
	}
	sp := rec.specs[0]
	args := strings.Join(sp.Args, " ")
	for _, want := range []string{"decide supervisor-prefilter --shadow", "--local escalate", "--local-trigger risk-regex", "--session sess-1", "--config "} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q lack %q", args, want)
		}
	}
	var st map[string]any
	if err := json.Unmarshal(sp.Stdin, &st); err != nil {
		t.Fatalf("stdin is not JSON: %v (%q)", err, sp.Stdin)
	}
	if st["tool"] != "Bash" || st["command_or_diff_preview"] != "git push --force origin main" || st["stated_intent"] != "fix the retry test" {
		t.Errorf("state = %v", st)
	}
	if _, has := st["plan_mentions_path"]; has {
		t.Error("plan_mentions_path must be omitted for a call without a file path")
	}
	for k := range st {
		switch k {
		case "tool", "file_path", "command_or_diff_preview", "stated_intent", "plan_mentions_path":
		default:
			t.Errorf("state field %q is not in the question set's state_fields", k)
		}
	}
	if sp.StdoutPath != os.DevNull || sp.StderrPath != os.DevNull {
		t.Error("the shadow child must not write into the work dir")
	}
}

func TestShadow_PassVerdictAndEnvProviderOverridesYML(t *testing.T) {
	rec, _ := runShadow(t, ssYML+"decisions:\n  provider: none\n", bashInput("ls -la"), shadowEnv("YAKOS_DECISION_PROVIDER", "mock"))
	if len(rec.specs) != 1 {
		t.Fatalf("launches = %d", len(rec.specs))
	}
	args := strings.Join(rec.specs[0].Args, " ")
	if !strings.Contains(args, "--local pass") || strings.Contains(args, "--local-trigger") {
		t.Errorf("args = %q", args)
	}
}

func TestShadow_JevNeedsKeyAndQuotedProviderIsRead(t *testing.T) {
	yml := ssYML + "decisions:\n  provider: \"jev\"   # shadow only\n"
	if rec, _ := runShadow(t, yml, bashInput("ls"), shadowEnv()); len(rec.specs) != 0 {
		t.Error("jev without TYPESAFE_API_KEY must not launch")
	}
	rec, _ := runShadow(t, yml, bashInput("ls"), shadowEnv("TYPESAFE_API_KEY", "set"))
	if len(rec.specs) != 1 {
		t.Fatalf("jev with a key: launches = %d", len(rec.specs))
	}
	// The key is never forwarded on the command line or stdin.
	if strings.Contains(strings.Join(rec.specs[0].Args, " ")+string(rec.specs[0].Stdin), "TYPESAFE") {
		t.Error("key material in the launch spec")
	}
}

func TestShadow_LaunchFailureIsSilentFailOpen(t *testing.T) {
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, ssYML+"decisions:\n  provider: mock\n")
	rec := &recorder{err: errors.New("exec: no such file")}
	h := newHook(work, proj)
	h.Launch = rec.launch
	in := bashInput("rm -rf /")
	in.Env = shadowEnv()
	out, err := h.Run(context.Background(), in)
	if err != nil || out.ExitCode != 0 || len(out.Stderr) != 0 {
		t.Fatalf("err=%v exit=%d stderr=%q", err, out.ExitCode, out.Stderr)
	}
	if len(rec.specs) != 1 {
		t.Fatalf("launch was not attempted")
	}
	// Escalation bookkeeping is untouched by the failed shadow launch.
	if b, _ := os.ReadFile(filepath.Join(work, ".supervisor-counter")); strings.TrimSpace(string(b)) != "1" {
		t.Errorf("counter = %q, want 1", b)
	}
}

func TestShadow_NoCLIMeansNoLaunch(t *testing.T) {
	rec, out := runShadow(t, ssYML+"decisions:\n  provider: mock\n", bashInput("ls"), map[string]string{"PATH": t.TempDir()})
	if len(rec.specs) != 0 || out.ExitCode != 0 {
		t.Errorf("launches=%d exit=%d", len(rec.specs), out.ExitCode)
	}
}

func TestShadow_PreFilterDisabledHasNoLocalVerdictSoNoCall(t *testing.T) {
	rec, _ := runShadow(t, "supervisor:\n  score_every_n_calls: 1000\n  pre_filter:\n    enabled: false\ndecisions:\n  provider: mock\n", bashInput("ls"), shadowEnv())
	if len(rec.specs) != 0 {
		t.Errorf("launches = %d, want 0 (no local verdict to compare)", len(rec.specs))
	}
}

func TestShadow_EditStateCarriesPathAndPlanMention(t *testing.T) {
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, ssYML+"decisions:\n  provider: mock\n")
	if err := os.WriteFile(filepath.Join(work, "plan.md"), []byte("touch api.go only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	h := newHook(work, proj)
	h.Launch = rec.launch
	for _, f := range []string{"api.go", "other.go"} {
		in := makeInput("Edit", f, "x := 1")
		in.Env = shadowEnv()
		if _, err := h.Run(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	if len(rec.specs) != 2 {
		t.Fatalf("launches = %d", len(rec.specs))
	}
	want := []bool{true, false}
	for i, sp := range rec.specs {
		var st map[string]any
		_ = json.Unmarshal(sp.Stdin, &st)
		if st["plan_mentions_path"] != want[i] || st["command_or_diff_preview"] != "x := 1" || st["file_path"] == nil {
			t.Errorf("state %d = %v", i, st)
		}
	}
	// out-of-scope escalates: the trigger kind is passed, without the file name.
	if a := strings.Join(rec.specs[1].Args, " "); !strings.Contains(a, "--local escalate --local-trigger out-of-scope") || strings.Contains(a, "other.go") {
		t.Errorf("args = %q", a)
	}
}

// A malformed decisions: block must not cost the hook its supervisor: config.
func TestShadow_MalformedDecisionsBlockKeepsSupervisorConfig(t *testing.T) {
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, "supervisor:\n  enabled: false\ndecisions: [1, 2]\n")
	h := newHook(work, proj)
	in := makeInput("Edit", "a.go", "x")
	in.Env = map[string]string{}
	if _, err := h.Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "supervisor-buffer.ndjson")); err == nil {
		t.Error("supervisor.enabled: false must still disable the hook")
	}
}

// End to end through the production launcher: the child really receives the state on stdin.
func TestShadow_ProductionLauncherFeedsStateOnStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script child")
	}
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, ssYML+"decisions:\n  provider: mock\n")
	out := filepath.Join(t.TempDir(), "stdin.txt")
	script := filepath.Join(t.TempDir(), "yakos")
	body := "#!/bin/sh\ncat > " + out + "\nprintf '%s ' \"$@\" > " + out + ".args\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	h := supervisorstream.New(work, proj)
	h.NowFn = fixedNow
	in := bashInput("git push --force")
	in.Env = map[string]string{"YAKOS_CLI": script}
	if _, err := h.Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, _ := os.ReadFile(out)
		a, _ := os.ReadFile(out + ".args")
		if strings.Contains(string(b), "git push --force") && strings.Contains(string(a), "--shadow") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("child never got the state; stdin=%q args=%q", b, a)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
