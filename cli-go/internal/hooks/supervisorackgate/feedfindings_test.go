package supervisorackgate_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
)

// seedFeedFindings writes n distinct critical detect-and-report findings the way
// the dispatch event-scan feed does (K-146) and returns the work/current dir.
func seedFeedFindings(t *testing.T, n int) string {
	t.Helper()
	wc := filepath.Join(t.TempDir(), "current")
	if err := os.MkdirAll(wc, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		f := supervisorstream.FeedFinding{
			Runtime: "codex", Kind: "tool_result", Severity: "critical",
			Labels: []string{fmt.Sprintf("ignore-previous-instructions-%d", i)}, Session: "s1",
		}
		if err := supervisorstream.ReportFeedFinding(wc, f, time.Date(2026, 10, 7, 1, 0, i, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	return wc
}

// K-146 F1: model and tool output must not control the lead's ack gate. A run
// that produced 16 critical feed findings leaves the Go gate open.
func TestFeedFindingsNeverGate_Go(t *testing.T) {
	wc := seedFeedFindings(t, 16)
	h := newHook(wc, filepath.Dir(wc), filepath.Join(t.TempDir(), "acks.ndjson"))
	for _, tool := range []string{"Agent", "TeamCreate"} {
		out, err := h.Run(context.Background(), makeInput(tool, map[string]string{"YAKOS_PROJECT_NAME": "p"}))
		if err != nil {
			t.Fatal(err)
		}
		if out.ExitCode != 0 {
			t.Fatalf("%s: feed findings gated the lead: exit %d, stderr %q", tool, out.ExitCode, out.Stderr)
		}
	}
}

func runBashGate(t *testing.T, wc string) (int, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash hook")
	}
	bash := "/bin/bash"
	if _, err := os.Stat(bash); err != nil {
		t.Skip("no /bin/bash")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("no jq")
	}
	_, self, _, _ := runtime.Caller(0)
	hook := filepath.Join(filepath.Dir(self), "..", "..", "..", "..", "lib", "hooks", "supervisor-ack-gate.sh")
	if _, err := os.Stat(hook); err != nil {
		t.Skip("hook not found")
	}
	home := t.TempDir()
	cmd := exec.Command(bash, hook)
	cmd.Stdin = strings.NewReader(`{"tool_name":"Agent","tool_input":{}}`)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + home,
		"YAKOS_WORK_DIR=" + filepath.Dir(wc), "YAKOS_PROJECT_NAME=p",
		"CLAUDE_PROJECT_DIR=" + t.TempDir(),
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, stderr.String()
}

// The legacy bash gate (run under the system bash, 3.2 on macOS) agrees, and a
// genuine surface_to_operator line in the same file still blocks it (control).
func TestFeedFindingsNeverGate_Bash(t *testing.T) {
	wc := seedFeedFindings(t, 16)
	if code, se := runBashGate(t, wc); code != 0 {
		t.Fatalf("bash gate blocked on feed findings: exit %d: %s", code, se)
	}
	writeFinding(t, filepath.Join(wc, "supervisor-findings.ndjson"), "2026-10-07T02:00:00Z", "surface_to_operator", "real escalation")
	if code, _ := runBashGate(t, wc); code != 2 {
		t.Fatalf("control: bash gate must block a real escalation, exit %d", code)
	}
}
