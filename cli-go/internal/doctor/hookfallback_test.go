package doctor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fallbackRunner(t *testing.T, content string) (*runner, *bytes.Buffer, string) {
	t.Helper()
	home := t.TempDir()
	state := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "hook-fallback.log")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	return &runner{cfg: Config{Writer: &buf}, w: &buf, home: home, env: func(string) string { return "" }, report: &Report{}}, &buf, path
}

func ago(d time.Duration) string { return time.Now().UTC().Add(-d).Format(time.RFC3339) }

func TestDoctorWarnsOnRecentHookFallback(t *testing.T) {
	log := fmt.Sprintf("%s supervisor-stream rc=1\n%s supervisor-stream rc=2\n%s supervisor-stream rc=137\n",
		ago(30*24*time.Hour), ago(2*time.Hour), ago(time.Minute))
	r, buf, _ := fallbackRunner(t, log)
	r.checkHookFallback()
	out := buf.String()
	if !strings.Contains(out, "supervisor-stream: the Go hook failed 2 time(s) in the last 7 days (last rc=137") {
		t.Fatalf("want count 2 (the 30-day-old entry excluded) and last rc 137:\n%s", out)
	}
	if r.report.Warnings != 1 || r.report.Errors != 0 {
		t.Fatalf("want exactly one warning: %+v", r.report)
	}
}

func TestDoctorHookFallbackSilentWhenAbsentOrOld(t *testing.T) {
	for name, content := range map[string]string{
		"absent":  "",
		"old":     fmt.Sprintf("%s supervisor-stream rc=1\n", ago(10*24*time.Hour)),
		"garbage": "not a record\n\n",
	} {
		r, buf, _ := fallbackRunner(t, content)
		r.checkHookFallback()
		if buf.Len() != 0 || r.report.Warnings != 0 {
			t.Errorf("%s: want silence, got %q %+v", name, buf.String(), r.report)
		}
	}
}

func TestDoctorTrimsHookFallbackLogToLast200Lines(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 450; i++ {
		fmt.Fprintf(&sb, "%s supervisor-stream rc=%d\n", ago(time.Hour), i)
	}
	r, _, path := fallbackRunner(t, sb.String())
	r.checkHookFallback()
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 200 || !strings.HasSuffix(lines[199], "rc=449") || !strings.HasSuffix(lines[0], "rc=250") {
		t.Fatalf("want the last 200 lines, got %d (%s .. %s)", len(lines), lines[0], lines[len(lines)-1])
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("trim changed the mode to %v", fi.Mode().Perm())
	}
}
