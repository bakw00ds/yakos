package main

// dispatch_outcome_test.go: `yakos dispatch` (Go) says what a run did (K-135).
// The agent text on stdout does not carry a harness failure message, so a failed
// run must print it to stderr and exit non-zero; text that was cut at a limit
// says so. reportDispatchOutcome is covered directly; the end-to-end cases run
// the built binary against fake runtimes and are skipped when bin/yakos has not
// been built (make build).

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/dispatch"
	rt "github.com/bakw00ds/yakos/internal/runtime"
)

// ---- reportDispatchOutcome ----------------------------------------------------

func TestReportDispatchOutcome(t *testing.T) {
	cases := []struct {
		name     string
		res      dispatch.Result
		wantCode int
		want     []string // substrings of stderr, in order
		absent   []string
	}{
		{
			name:     "clean run is silent",
			res:      dispatch.Result{Runtime: "codex", Parsed: true, Text: "ok"},
			wantCode: 0,
		},
		{
			name:     "harness error with a non-zero exit keeps the exit code",
			res:      dispatch.Result{Runtime: "codex", ExitCode: 1, Error: "model is not supported"},
			wantCode: 1,
			want:     []string{"dispatch: codex reported an error: model is not supported", "dispatch: codex exited with code 1"},
		},
		{
			name:     "harness error but exit 0 still fails the run",
			res:      dispatch.Result{Runtime: "claude", ExitCode: 0, Error: "Credit balance is too low"},
			wantCode: 1,
			want:     []string{"dispatch: claude reported an error: Credit balance is too low"},
			absent:   []string{"exited with code"},
		},
		{
			name:     "runtime exit code is kept",
			res:      dispatch.Result{Runtime: "agy", ExitCode: 3, StderrTail: "AGY_ERROR: {\"code\":429}\n"},
			wantCode: 3,
			want:     []string{"dispatch: agy exited with code 3", "dispatch: agy stderr (last lines):", `AGY_ERROR: {"code":429}`},
		},
		{
			name:     "an exit code the dispatch layer could not read maps to 1",
			res:      dispatch.Result{Runtime: "codex", ExitCode: -1},
			wantCode: 1,
			want:     []string{"dispatch: codex exited with code -1"},
		},
		{
			name:     "no runtime name still reads",
			res:      dispatch.Result{ExitCode: 2},
			wantCode: 2,
			want:     []string{"dispatch: runtime exited with code 2"},
		},
		{
			name:     "text cap",
			res:      dispatch.Result{Runtime: "agy", Truncated: true, TextCapped: true},
			wantCode: 0,
			want:     []string{"dispatch: output truncated at 1 MiB"},
			absent:   []string{"exceeded"},
		},
		{
			name:     "one dropped line",
			res:      dispatch.Result{Runtime: "agy", Truncated: true, LinesDropped: 1},
			wantCode: 0,
			want:     []string{fmt.Sprintf("dispatch: line exceeded %d bytes and was skipped", rt.MaxStreamLineBytes)},
			absent:   []string{"truncated at"},
		},
		{
			name:     "several dropped lines",
			res:      dispatch.Result{Runtime: "agy", Truncated: true, LinesDropped: 3},
			wantCode: 0,
			want:     []string{fmt.Sprintf("dispatch: 3 lines exceeded %d bytes and were skipped", rt.MaxStreamLineBytes)},
		},
		{
			name:     "both limits are named once each",
			res:      dispatch.Result{Runtime: "agy", Truncated: true, TextCapped: true, LinesDropped: 1},
			wantCode: 0,
			want:     []string{"output truncated at 1 MiB", "line exceeded"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			code := reportDispatchOutcome(&buf, c.res)
			if code != c.wantCode {
				t.Errorf("exit code = %d, want %d", code, c.wantCode)
			}
			got := buf.String()
			pos := 0
			for _, w := range c.want {
				i := strings.Index(got[pos:], w)
				if i < 0 {
					t.Errorf("stderr lacks %q (in order):\n%s", w, got)
					break
				}
				pos += i + len(w)
			}
			for _, a := range c.absent {
				if strings.Contains(got, a) {
					t.Errorf("stderr must not contain %q:\n%s", a, got)
				}
			}
			if len(c.want) == 0 && got != "" {
				t.Errorf("expected no stderr output, got:\n%s", got)
			}
		})
	}
}

// ---- the built binary ---------------------------------------------------------

// runDispatchWithStub runs `yakos dispatch` against a fake runtime binary named
// bin whose body is the given shell script, and returns what the process did.
func runDispatchWithStub(t *testing.T, bin, script string) (stdout, stderr string, exit int) {
	t.Helper()
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v (run `make build` first)", goBin, err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	root := t.TempDir()
	agents := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agents, "worker.md"), []byte("---\nid: worker\ndescription: Test agent\n---\n\nTest agent worker.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stubDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stubDir, bin), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	return runGoSplit(t,
		[]string{"dispatch", "worker", "do the thing", "--runtime", bin, "--project", t.TempDir()},
		map[string]string{
			"HOME":               t.TempDir(),
			"YAKOS_ROOT":         root,
			"PATH":               stubDir + string(os.PathListSeparator) + os.Getenv("PATH"),
			"YAKOS_DISPATCH_LOG": t.TempDir(),
		})
}

func fixtureCat(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(repoRoot(t), "tests", "fixtures", "runtime-streams", name)
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	return "cat '" + p + "'"
}

func dataCat(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "out.dat")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return "cat '" + p + "'"
}

// The codex turn failed: the message is on stderr (it used to be in the raw
// JSONL on stdout), nothing is on stdout, and the exit code is the runtime's.
func TestDispatchCLI_FailedRunPrintsTheErrorAndExitsNonZero(t *testing.T) {
	stdout, stderr, exit := runDispatchWithStub(t, "codex", fixtureCat(t, "codex-exec-json-0.154.0-failed.ndjson")+"\nexit 1")
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty (a failed turn has no text)", stdout)
	}
	if !strings.Contains(stderr, "codex reported an error: The 'not-a-real-model-xyz' model is not supported when using Codex with a ChatGPT account.") {
		t.Errorf("stderr lacks the harness error:\n%s", stderr)
	}
	if !strings.Contains(stderr, "codex exited with code 1") {
		t.Errorf("stderr lacks the exit code:\n%s", stderr)
	}
}

// The harness said the turn failed but the process exited 0: dispatch still
// exits 1 and says why.
func TestDispatchCLI_HarnessErrorWithExitZeroExitsOne(t *testing.T) {
	stdout, stderr, exit := runDispatchWithStub(t, "codex", fixtureCat(t, "codex-exec-json-0.154.0-failed.ndjson"))
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
	if stdout != "" || !strings.Contains(stderr, "model is not supported when using Codex") {
		t.Errorf("stdout=%q stderr=%q", stdout, stderr)
	}
	if strings.Contains(stderr, "exited with code") {
		t.Errorf("the runtime exited 0; stderr must not claim otherwise:\n%s", stderr)
	}
}

// Claude's error result behaves the same way.
func TestDispatchCLI_ClaudeErrorResultExitsOne(t *testing.T) {
	_, stderr, exit := runDispatchWithStub(t, "claude", fixtureCat(t, "claude-stream-json-error-SYNTHETIC.ndjson"))
	if exit != 1 || !strings.Contains(stderr, "claude reported an error: Credit balance is too low") {
		t.Errorf("exit=%d stderr=%q", exit, stderr)
	}
}

// A plain-text runtime that exits non-zero keeps its exit code, its partial text
// and its stderr tail.
func TestDispatchCLI_RuntimeExitCodeAndStderrTailAreKept(t *testing.T) {
	stdout, stderr, exit := runDispatchWithStub(t, "agy", "echo partial answer\necho boom >&2\nexit 7")
	if exit != 7 {
		t.Errorf("exit = %d, want 7", exit)
	}
	if stdout != "partial answer\n" {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "agy exited with code 7") || !strings.Contains(stderr, "boom") {
		t.Errorf("stderr lacks the exit code or the stderr tail:\n%s", stderr)
	}
}

// A successful run adds nothing to stderr beyond the banner the CLI always
// prints ("yakos dispatch: agent=..."): no outcome line.
func TestDispatchCLI_SuccessfulRunReportsNoOutcome(t *testing.T) {
	stdout, stderr, exit := runDispatchWithStub(t, "codex", fixtureCat(t, "codex-exec-json-0.154.0-ok.ndjson"))
	if exit != 0 || stdout != "ok\n" {
		t.Errorf("exit=%d stdout=%q", exit, stdout)
	}
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "dispatch: ") {
			t.Errorf("unexpected outcome line on a successful run: %q", line)
		}
	}
}

// Output past the 1 MiB text cap is cut, and the operator is told.
func TestDispatchCLI_NoticeWhenOutputIsCutAtOneMiB(t *testing.T) {
	var data []byte
	for i := 0; i < 1500; i++ { // 1.5 MiB of text in 1 KiB lines
		data = append(data, bytes.Repeat([]byte("a"), 1023)...)
		data = append(data, '\n')
	}
	stdout, stderr, exit := runDispatchWithStub(t, "agy", dataCat(t, data))
	if exit != 0 {
		t.Errorf("exit = %d, want 0 (the run succeeded)", exit)
	}
	if len(stdout) < rt.MaxParsedTextBytes-2 || len(stdout) > rt.MaxParsedTextBytes+2 {
		t.Errorf("stdout is %d bytes, want about %d", len(stdout), rt.MaxParsedTextBytes)
	}
	if strings.Count(stderr, "output truncated at 1 MiB") != 1 {
		t.Errorf("want exactly one truncation notice:\n%s", stderr)
	}
	if strings.Contains(stderr, "exceeded") {
		t.Errorf("no line was skipped, but stderr says so:\n%s", stderr)
	}
}

// A line over the per-line cap is skipped, and the operator is told, whether or
// not anything else was printed.
func TestDispatchCLI_NoticeWhenALineIsSkipped(t *testing.T) {
	long := append(bytes.Repeat([]byte("x"), 3*1024*1024), '\n')
	notice := fmt.Sprintf("line exceeded %d bytes and was skipped", rt.MaxStreamLineBytes)

	// Something else follows: only that is printed.
	stdout, stderr, exit := runDispatchWithStub(t, "agy", dataCat(t, append(append([]byte(nil), long...), []byte("after\n")...)))
	if exit != 0 || stdout != "after\n" {
		t.Errorf("exit=%d stdout=%q", exit, stdout)
	}
	if strings.Count(stderr, notice) != 1 {
		t.Errorf("want exactly one skipped-line notice:\n%s", stderr)
	}

	// The only line is the over-long one: nothing on stdout, but not silent.
	stdout, stderr, exit = runDispatchWithStub(t, "agy", dataCat(t, long))
	if exit != 0 || stdout != "" {
		t.Errorf("exit=%d stdout=%q (want 0 and empty)", exit, stdout)
	}
	if strings.Count(stderr, notice) != 1 {
		t.Errorf("a run whose only line was skipped must say so:\n%s", stderr)
	}
}
