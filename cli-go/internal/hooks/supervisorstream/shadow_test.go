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

// specState returns the state JSON a launch handed over: the shadow call gets
// it in a private file named by --state-file, never on a pipe.
func specState(t *testing.T, sp supervisorstream.LaunchSpec) []byte {
	t.Helper()
	for i, a := range sp.Args {
		if a == "--state-file" && i+1 < len(sp.Args) {
			b, err := os.ReadFile(sp.Args[i+1])
			if err != nil {
				t.Fatalf("state file: %v", err)
			}
			return b
		}
	}
	t.Fatalf("no --state-file in %v", sp.Args)
	return nil
}

func runShadow(t *testing.T, yml string, in hooktype.HookInput, env map[string]string) (*recorder, hooktype.HookOutput) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
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
		"absent":             {ssYML, shadowEnv()},
		"none in yml":        {ssYML + "decisions:\n  provider: none\n", shadowEnv()},
		"project jev alone":  {ssYML + "decisions:\n  provider: jev\n", shadowEnv("TYPESAFE_API_KEY", "set")},
		"project mock alone": {ssYML + "decisions:\n  provider: mock\n", shadowEnv()},
		"env none":           {ssYML, shadowEnv("YAKOS_DECISION_PROVIDER", "none")},
		"unknown":            {ssYML + "decisions:\n  provider: bogus\n", shadowEnv()},
		"jev without key":    {ssYML, shadowEnv("YAKOS_DECISION_PROVIDER", "jev")},
		"kill switch":        {ssYML, shadowEnv("YAKOS_DECISION_DISABLE", "1", "YAKOS_DECISION_PROVIDER", "mock")},
		"malformed block":    {ssYML + "decisions: [1, 2]\n", shadowEnv()},
	} {
		rec, out := runShadow(t, tc.yml, bashInput("rm -rf /"), tc.env)
		if len(rec.specs) != 0 || out.ExitCode != 0 {
			t.Errorf("%s: launches=%d exit=%d, want none", name, len(rec.specs), out.ExitCode)
		}
	}
}

func TestShadow_MockProviderLaunchesDecideWithLocalVerdict(t *testing.T) {
	rec, out := runShadow(t, ssYML, bashInput("git push --force origin main"), shadowEnv("YAKOS_DECISION_PROVIDER", "mock"))
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
	if err := json.Unmarshal(specState(t, sp), &st); err != nil {
		t.Fatalf("state is not JSON: %v", err)
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
	if rec, _ := runShadow(t, ssYML, bashInput("ls"), shadowEnv("YAKOS_DECISION_PROVIDER", "jev")); len(rec.specs) != 0 {
		t.Error("jev without TYPESAFE_API_KEY must not launch")
	}
	rec, _ := runShadow(t, ssYML, bashInput("ls"), shadowEnv("YAKOS_DECISION_PROVIDER", "jev", "TYPESAFE_API_KEY", "set"))
	if len(rec.specs) != 1 {
		t.Fatalf("jev with a key: launches = %d", len(rec.specs))
	}
	// The key is never forwarded on the command line or stdin.
	if strings.Contains(strings.Join(rec.specs[0].Args, " ")+string(specState(t, rec.specs[0])), "TYPESAFE") {
		t.Error("key material in the launch spec")
	}
}

func TestShadow_LaunchFailureIsSilentFailOpen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, ssYML+"decisions:\n  provider: mock\n")
	rec := &recorder{err: errors.New("exec: no such file")}
	h := newHook(work, proj)
	h.Launch = rec.launch
	in := bashInput("rm -rf /")
	in.Env = shadowEnv("YAKOS_DECISION_PROVIDER", "mock")
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
	rec, out := runShadow(t, ssYML, bashInput("ls"), map[string]string{"PATH": t.TempDir(), "YAKOS_DECISION_PROVIDER": "mock"})
	if len(rec.specs) != 0 || out.ExitCode != 0 {
		t.Errorf("launches=%d exit=%d", len(rec.specs), out.ExitCode)
	}
}

func TestShadow_PreFilterDisabledHasNoLocalVerdictSoNoCall(t *testing.T) {
	rec, _ := runShadow(t, "supervisor:\n  score_every_n_calls: 1000\n  pre_filter:\n    enabled: false\ndecisions:\n  provider: mock\n", bashInput("ls"), shadowEnv("YAKOS_DECISION_PROVIDER", "mock"))
	if len(rec.specs) != 0 {
		t.Errorf("launches = %d, want 0 (no local verdict to compare)", len(rec.specs))
	}
}

func TestShadow_EditStateCarriesPathAndPlanMention(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
		in.Env = shadowEnv("YAKOS_DECISION_PROVIDER", "mock")
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
		_ = json.Unmarshal(specState(t, sp), &st)
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

// End to end through the production launcher: the child gets the state as a
// private file and the hook returns at once, however large the state and
// however slow (or absent) the reader.
func TestShadow_HugeStateNeverBlocksTheHook(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script child")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, ssYML+"decisions:\n  provider: mock\n")
	script := filepath.Join(t.TempDir(), "yakos")
	// A child that never reads anything and does not exit until the test lets it:
	// the hook returning before that is the proof it did not wait for the child, so
	// the proof needs no stopwatch (the old 3 s limit failed on a loaded runner,
	// where building and writing the huge state alone can take that long).
	release := filepath.Join(t.TempDir(), "release")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) }) // a failing test leaves no child waiting
	body := "#!/bin/sh\nwhile [ ! -e '" + release + "' ]; do sleep 0.05; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	h := supervisorstream.New(work, proj)
	h.NowFn = fixedNow
	in := bashInput(strings.Repeat("x", 128*1024) + " git push --force")
	in.Payload["tool_input"].(map[string]any)["file_path"] = "/p/" + strings.Repeat("d/", 100*1024)
	in.Env = map[string]string{"YAKOS_CLI": script, "YAKOS_DECISION_PROVIDER": "mock"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := h.Run(context.Background(), in); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Minute): // a hang guard, not a timing assertion
		t.Fatal("the hook blocked on the shadow launch")
	}
	if _, err := os.Stat(release); err == nil {
		t.Fatal("the release file exists before the hook returned")
	}
	files, _ := filepath.Glob(filepath.Join(home, ".yakos-state", "shadow-state-*.json"))
	if len(files) != 1 {
		t.Fatalf("state files = %v", files)
	}
	fi, _ := os.Stat(files[0])
	if fi.Mode().Perm() != 0o600 || fi.Size() < 64*1024 {
		t.Errorf("state file mode %v size %d", fi.Mode().Perm(), fi.Size())
	}
}

// A leftover state file (child never ran) is swept on a later call.
func TestShadow_StaleStateFilesAreSwept(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".yakos-state")
	_ = os.MkdirAll(dir, 0o700)
	stale := filepath.Join(dir, "shadow-state-old.json")
	_ = os.WriteFile(stale, []byte("{}"), 0o600)
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(stale, old, old)
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, ssYML+"decisions:\n  provider: mock\n")
	rec := &recorder{}
	h := newHook(work, proj)
	h.Launch = rec.launch
	in := bashInput("ls")
	in.Env = shadowEnv("YAKOS_DECISION_PROVIDER", "mock")
	if _, err := h.Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("stale state file was not swept")
	}
}

// A failed launch leaves no state file behind.
func TestShadow_FailedLaunchRemovesStateFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, ssYML+"decisions:\n  provider: mock\n")
	rec := &recorder{err: errors.New("boom")}
	h := newHook(work, proj)
	h.Launch = rec.launch
	in := bashInput("ls")
	in.Env = shadowEnv("YAKOS_DECISION_PROVIDER", "mock")
	_, _ = h.Run(context.Background(), in)
	if files, _ := filepath.Glob(filepath.Join(home, ".yakos-state", "shadow-state-*.json")); len(files) != 0 {
		t.Errorf("leftover %v", files)
	}
}

// The user-level policy file is what enables a provider without an env var; a
// project `provider: none` vetoes it, and the env var still wins over the veto.
func TestShadow_UserPolicyEnablesAndProjectNoneVetoes(t *testing.T) {
	run := func(yml string, env map[string]string) int {
		t.Helper()
		home := t.TempDir()
		t.Setenv("HOME", home)
		_ = os.MkdirAll(filepath.Join(home, ".yakos-state"), 0o700)
		_ = os.WriteFile(filepath.Join(home, ".yakos-state", "decision-policy.yml"), []byte("provider: mock\n"), 0o600)
		work, proj := t.TempDir(), t.TempDir()
		writeYAML(t, proj, yml)
		rec := &recorder{}
		h := newHook(work, proj)
		h.Launch = rec.launch
		in := bashInput("ls")
		in.Env = env
		if _, err := h.Run(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		return len(rec.specs)
	}
	if n := run(ssYML, shadowEnv()); n != 1 {
		t.Errorf("user policy provider: launches = %d, want 1", n)
	}
	if n := run(ssYML+"decisions:\n  provider: none\n", shadowEnv()); n != 0 {
		t.Errorf("project none must veto the policy switch: launches = %d", n)
	}
	if n := run(ssYML+"decisions:\n  provider: none\n", shadowEnv("YAKOS_DECISION_PROVIDER", "mock")); n != 1 {
		t.Errorf("env still wins over the project veto: launches = %d", n)
	}
}

// A policy file that others can write must not enable egress.
func TestShadow_UntrustedPolicyFileDoesNotEnable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not modelled on windows")
	}
	for name, setup := range map[string]func(path string){
		"world-writable": func(p string) { _ = os.Chmod(p, 0o666) },
		"group-writable": func(p string) { _ = os.Chmod(p, 0o660) },
		"symlink": func(p string) {
			real := p + ".real"
			_ = os.Rename(p, real)
			_ = os.Symlink(real, p)
		},
	} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		_ = os.MkdirAll(filepath.Join(home, ".yakos-state"), 0o700)
		pol := filepath.Join(home, ".yakos-state", "decision-policy.yml")
		_ = os.WriteFile(pol, []byte("provider: mock\n"), 0o600)
		setup(pol)
		work, proj := t.TempDir(), t.TempDir()
		writeYAML(t, proj, ssYML)
		rec := &recorder{}
		h := newHook(work, proj)
		h.Launch = rec.launch
		in := bashInput("ls")
		in.Env = shadowEnv()
		if _, err := h.Run(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		if len(rec.specs) != 0 {
			t.Errorf("%s policy file enabled the provider", name)
		}
	}
}

// With the surface off in the project config the hook never writes a state file.
func TestShadow_SurfaceOffWritesNoStateFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	rec, _ := runShadowWithHome(t, ssYML+"decisions:\n  surfaces:\n    supervisor-prefilter: {mode: off}\n", bashInput("mysql -pRAWSECRET77 db"), shadowEnv("YAKOS_DECISION_PROVIDER", "mock"))
	if len(rec.specs) != 0 {
		t.Errorf("launches = %d, want 0", len(rec.specs))
	}
	if files, _ := filepath.Glob(filepath.Join(os.Getenv("HOME"), ".yakos-state", "shadow-state-*.json")); len(files) != 0 {
		t.Errorf("raw state file left behind: %v", files)
	}
}

func runShadowWithHome(t *testing.T, yml string, in hooktype.HookInput, env map[string]string) (*recorder, hooktype.HookOutput) {
	t.Helper()
	return runShadow(t, yml, in, env)
}
