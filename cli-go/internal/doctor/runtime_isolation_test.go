package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func runIsolation(t *testing.T, home string, found map[string]string, env map[string]string) (string, *Report) {
	t.Helper()
	var buf bytes.Buffer
	cfg := Config{
		Writer:    &buf,
		ErrWriter: &buf,
		HomeDir:   home,
		LookPath:  singleLookPath(found),
		Environ:   func(k string) string { return env[k] },
	}
	r := &runner{cfg: cfg, w: &buf, home: home, env: cfg.Environ, report: &Report{}}
	r.checkRuntimeIsolation()
	return buf.String(), r.report
}

func writePolicy(t *testing.T, home, body string, mode os.FileMode) {
	t.Helper()
	dir := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "router-policy.yml")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeIsolation_SilentWithoutCodexOrPolicy(t *testing.T) {
	out, rep := runIsolation(t, t.TempDir(), nil, nil)
	if out != "" || len(rep.Findings) != 0 {
		t.Fatalf("expected no output on a machine with no codex and no policy, got %q", out)
	}
}

func TestRuntimeIsolation_HintsAtTheYakosProfileWhenSharingCodex(t *testing.T) {
	home := t.TempDir()
	out, rep := runIsolation(t, home, map[string]string{"codex": "/usr/bin/codex"}, nil)
	if !strings.Contains(out, "Runtime isolation") ||
		!strings.Contains(out, "yakos auth login codex") ||
		!strings.Contains(out, filepath.Join(home, ".yakos-state", "codex-home")) {
		t.Errorf("hint missing or incomplete:\n%s", out)
	}
	if rep.Warnings != 0 || rep.Errors != 0 {
		t.Errorf("the hint is informational, got %d warnings %d errors", rep.Warnings, rep.Errors)
	}
}

func TestRuntimeIsolation_ReportsTheProfileOnceItHoldsALogin(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".yakos-state", "codex-home")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ := runIsolation(t, home, map[string]string{"codex": "/usr/bin/codex"}, nil)
	if !strings.Contains(out, "[ok]") || !strings.Contains(out, "yakOS-owned profile") || strings.Contains(out, "yakos auth login") {
		t.Errorf("want an ok line naming the profile and no hint:\n%s", out)
	}
}

func TestRuntimeIsolation_NoHintWhenAPIKeyAuthenticatesCodex(t *testing.T) {
	out, _ := runIsolation(t, t.TempDir(), map[string]string{"codex": "/usr/bin/codex"}, map[string]string{"OPENAI_API_KEY": "sk-x"})
	if out != "" {
		t.Errorf("an API key has no shared auth.json to isolate; got %q", out)
	}
}

func TestRuntimeIsolation_WarnsWhenPolicyUnsandboxesAHarness(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	home := t.TempDir()
	writePolicy(t, home, "allow_unsandboxed_runtimes: [codex, agy, claude]\n", 0o600)
	out, rep := runIsolation(t, home, nil, nil)
	if !strings.Contains(out, "codex, agy run WITHOUT their sandbox") || rep.Warnings != 1 {
		t.Errorf("want one warning naming codex and agy (claude is not a sandboxed harness), got %d:\n%s", rep.Warnings, out)
	}
}

func TestRuntimeIsolation_ExplainsAnIgnoredPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	home := t.TempDir()
	writePolicy(t, home, "allow_unsandboxed_runtimes: [codex]\n", 0o666)
	out, rep := runIsolation(t, home, nil, nil)
	if !strings.Contains(out, "router policy ignored") || !strings.Contains(out, "writable") || rep.Warnings != 1 {
		t.Errorf("a world-writable policy must be reported as ignored, got:\n%s", out)
	}
}
