package supervisorstream_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
)

// K-112 (b): at the score threshold the Go hook must launch the supervisor
// dispatch like the bash hook does, instead of writing a marker nobody reads.

type recorder struct {
	mu    sync.Mutex
	specs []supervisorstream.LaunchSpec
	err   error
}

func (r *recorder) launch(s supervisorstream.LaunchSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs = append(r.specs, s)
	return r.err
}

func bigEdit(t *testing.T, h *supervisorstream.Hook, env map[string]string, n int) {
	t.Helper()
	big := strings.Repeat("line\n", 25)
	for i := 0; i < n; i++ {
		in := makeInput("Edit", "big.go", big)
		in.Env = env
		if _, err := h.Run(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
}

func logMessages(t *testing.T, work string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(work, "logs", "supervisor-stream.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		msgs = append(msgs, l)
	}
	return msgs
}

func TestLaunchAtThreshold_MatchesBash(t *testing.T) {
	work, proj := t.TempDir(), t.TempDir()
	loosen(t, "min_launch_interval_s: 0\n")
	writeYAML(t, proj, "supervisor:\n  score_every_n_calls: 3\n  model: sonnet\n  runtime: codex\n  agent: watcher\n")
	if err := os.WriteFile(filepath.Join(work, "decisions.md"), []byte("ship the thing\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	h := newHook(work, proj)
	h.Launch = rec.launch
	env := map[string]string{"YAKOS_CLI": "/fake/yakos"}

	bigEdit(t, h, env, 2)
	if len(rec.specs) != 0 {
		t.Fatalf("launched before threshold: %d", len(rec.specs))
	}
	bigEdit(t, h, env, 1)
	if len(rec.specs) != 1 {
		t.Fatalf("launches at threshold = %d, want 1", len(rec.specs))
	}
	sp := rec.specs[0]
	if sp.CLI != "/fake/yakos" {
		t.Errorf("CLI=%q", sp.CLI)
	}
	sw := filepath.ToSlash(work)
	wantTask := "Read " + sw + "/supervisor-buffer.ndjson (the last 50 tool calls; focus on the most recent 3).\n" +
		"Apply the rubric in your persona. Write your finding as a single\n" +
		"JSON line appended to " + sw + "/supervisor-findings.ndjson.\n\n" +
		"Stated intent of the active session: ship the thing"
	want := []string{"dispatch", "watcher", wantTask, "--runtime", "codex", "--model", "sonnet"}
	if strings.Join(sp.Args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("args = %q\nwant   %q", sp.Args, want)
	}
	if sp.StdoutPath != filepath.Join(work, ".supervisor-stdout.log") || sp.StderrPath != filepath.Join(work, ".supervisor-stderr.log") {
		t.Errorf("log paths: %q %q", sp.StdoutPath, sp.StderrPath)
	}
	// The retired marker must not come back.
	if _, err := os.Stat(filepath.Join(work, ".supervisor-dispatch-ready")); err == nil {
		t.Error("dead .supervisor-dispatch-ready marker was written")
	}
	// Log records, in bash order.
	all := strings.Join(logMessages(t, work), "\n")
	iHit := strings.Index(all, "escalation score threshold hit; forking supervisor dispatch (async)")
	iForked := strings.Index(all, "supervisor dispatch forked async (model=sonnet runtime=codex)")
	if iHit < 0 || iForked < iHit {
		t.Errorf("missing/misordered launch log records:\n%s", all)
	}
	if !strings.Contains(all, `"dispatch":"async"`) {
		t.Errorf("forked record lacks dispatch=async:\n%s", all)
	}
	// Second threshold (6th escalation) launches again; 4th and 5th do not.
	// The first run must have finished (the wrapper clears the in-flight
	// state), or the K-117 gate coalesces the second trigger instead.
	finishRuns(t, work)
	bigEdit(t, h, env, 3)
	if len(rec.specs) != 2 {
		t.Errorf("launches after 6 escalations = %d, want 2", len(rec.specs))
	}
}

func TestLaunchDefaultsAndMissingDecisions(t *testing.T) {
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, "supervisor:\n  score_every_n_calls: 1\n")
	rec := &recorder{}
	h := newHook(work, proj)
	h.Launch = rec.launch
	bigEdit(t, h, map[string]string{"YAKOS_CLI": "/fake/yakos"}, 1)
	if len(rec.specs) != 1 {
		t.Fatalf("launches=%d", len(rec.specs))
	}
	a := rec.specs[0].Args
	if a[1] != "supervisor" || a[len(a)-3] != "claude" || a[len(a)-1] != "haiku" {
		t.Errorf("defaults wrong: %q", a)
	}
	if !strings.HasSuffix(a[2], "(decisions.md not found; use the most recent user prompt as intent)") {
		t.Errorf("task tail: %q", a[2])
	}
}

func TestLaunchDecisionsHeadCapped(t *testing.T) {
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, "supervisor:\n  score_every_n_calls: 1\n")
	_ = os.WriteFile(filepath.Join(work, "decisions.md"), []byte(strings.Repeat("x", 3000)), 0o644)
	rec := &recorder{}
	h := newHook(work, proj)
	h.Launch = rec.launch
	bigEdit(t, h, map[string]string{"YAKOS_CLI": "/fake/yakos"}, 1)
	task := rec.specs[0].Args[2]
	intent := task[strings.Index(task, "Stated intent of the active session: ")+len("Stated intent of the active session: "):]
	if n := len(intent); n != 1500 {
		t.Errorf("decisions head = %d bytes, want 1500", n)
	}
}

func TestLaunchNoCLI_WarnsAndDoesNotLaunch(t *testing.T) {
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, "supervisor:\n  score_every_n_calls: 1\n")
	rec := &recorder{}
	h := newHook(work, proj)
	h.Launch = rec.launch
	// Empty PATH dir + no YAKOS_CLI/YAKOS_ROOT: nothing to launch.
	bigEdit(t, h, map[string]string{"PATH": t.TempDir()}, 1)
	if len(rec.specs) != 0 {
		t.Fatalf("launched without a CLI")
	}
	if !strings.Contains(strings.Join(logMessages(t, work), "\n"), "could not locate yakos CLI to fork supervisor") {
		t.Error("missing WARN about the CLI")
	}
}

func TestLaunchFindsCLIViaYakosRootThenPath(t *testing.T) {
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, "supervisor:\n  score_every_n_calls: 1\n")
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "cli"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "cli", "yakos"), []byte("#!/bin/sh\n"), 0o755)
	rec := &recorder{}
	h := newHook(work, proj)
	h.Launch = rec.launch
	bigEdit(t, h, map[string]string{"YAKOS_ROOT": root, "PATH": t.TempDir()}, 1)
	if len(rec.specs) != 1 || rec.specs[0].CLI != filepath.Join(root, "cli", "yakos") {
		t.Fatalf("YAKOS_ROOT lookup: %+v", rec.specs)
	}

	bin := t.TempDir()
	exe := "yakos"
	if runtime.GOOS == "windows" {
		exe = "yakos.exe"
	}
	_ = os.WriteFile(filepath.Join(bin, exe), []byte("#!/bin/sh\n"), 0o755)
	rec2 := &recorder{}
	h2 := newHook(t.TempDir(), proj)
	h2.Launch = rec2.launch
	bigEdit(t, h2, map[string]string{"PATH": bin}, 1)
	if len(rec2.specs) != 1 || rec2.specs[0].CLI != filepath.Join(bin, exe) {
		t.Fatalf("PATH lookup: %+v", rec2.specs)
	}
}

func TestLaunchFailureIsLoggedNotFatal(t *testing.T) {
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, "supervisor:\n  score_every_n_calls: 1\n")
	rec := &recorder{err: os.ErrPermission}
	h := newHook(work, proj)
	h.Launch = rec.launch
	in := makeInput("Edit", "big.go", strings.Repeat("line\n", 25))
	in.Env = map[string]string{"YAKOS_CLI": "/fake/yakos"}
	out, err := h.Run(context.Background(), in)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
	if !strings.Contains(strings.Join(logMessages(t, work), "\n"), "supervisor dispatch launch failed") {
		t.Error("launch failure not logged")
	}
}

func TestNilLaunchNeverSpawns(t *testing.T) {
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, "supervisor:\n  score_every_n_calls: 1\n")
	h := newHook(work, proj) // struct literal: Launch is nil
	bigEdit(t, h, map[string]string{"YAKOS_CLI": "/fake/yakos"}, 1)
	if !strings.Contains(strings.Join(logMessages(t, work), "\n"), "could not locate yakos CLI") {
		t.Error("nil launcher should warn, not spawn")
	}
}

// End to end with the production launcher and a fake dispatcher script that
// records its argv and env-independent launch. Unix only (shell script).
func TestProductionLauncherRunsFakeDispatcher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script dispatcher")
	}
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, "supervisor:\n  score_every_n_calls: 1\n  model: haiku\n")
	rec := filepath.Join(t.TempDir(), "argv.txt")
	script := filepath.Join(t.TempDir(), "yakos")
	body := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"ARG:$a\" >> " + rec + "; done\necho out-line\necho err-line >&2\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	h := supervisorstream.New(work, proj)
	h.NowFn = fixedNow
	// Under `go test` the running executable is the test binary, not yakos, so
	// the wrapper re-exec is off here: this runs the unwrapped fallback. The
	// wrapper itself is covered by TestWrapper* and the coalesce script.
	h.Self = ""
	in := hooktype.HookInput{
		Event: "PostToolUse", Tool: "Edit",
		Payload: map[string]any{"tool_input": map[string]any{"file_path": "big.go", "new_string": strings.Repeat("line\n", 25)}},
		Env:     map[string]string{"YAKOS_CLI": script},
	}
	if _, err := h.Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, _ := os.ReadFile(rec)
		if strings.Contains(string(data), "ARG:haiku") {
			if !strings.Contains(string(data), "ARG:dispatch\nARG:supervisor\n") {
				t.Fatalf("argv: %s", data)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake dispatcher never ran; argv file: %q", data)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, f := range []struct{ name, want string }{{".supervisor-stdout.log", "out-line"}, {".supervisor-stderr.log", "err-line"}} {
		for {
			data, _ := os.ReadFile(filepath.Join(work, f.name))
			if strings.Contains(string(data), f.want) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never received %q", f.name, f.want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// finishRuns simulates the detached wrapper completing: it clears the
// in-flight marker of every session state file (the wrapper does the same).
func finishRuns(t *testing.T, work string) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(work, ".supervisor-run.*"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if strings.HasPrefix(l, "start=") {
				l = "start="
			}
			out = append(out, l)
		}
		if err := os.WriteFile(f, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
