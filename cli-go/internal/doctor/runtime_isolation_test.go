package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooksinstall"
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

func writeProfileFile(t *testing.T, home, name, body string) string {
	t.Helper()
	dir := filepath.Join(home, ".yakos-state", "codex-home")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRuntimeIsolation_CodexHooksHints(t *testing.T) {
	codex := map[string]string{"codex": "/usr/bin/codex"}
	exe, err := hooksinstall.ResolveBinary("")
	if err != nil {
		t.Fatal(err)
	}
	cur, err := hooksinstall.RenderShapeFile(hooksinstall.HarnessCodex, exe)
	if err != nil {
		t.Fatal(err)
	}

	// Hooks installed but the profile is not what dispatch uses: skipped silently.
	home := t.TempDir()
	writeProfileFile(t, home, "hooks.json", string(cur))
	// (no auth, no API key: Effective is not isolated)
	out, rep := runIsolation(t, home, codex, nil)
	if !strings.Contains(out, "skip them silently") || rep.Warnings != 1 {
		t.Errorf("want the not-loaded warning, got %d warnings:\n%s", rep.Warnings, out)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "codex hooks") && strings.Contains(l, home) {
			t.Errorf("hooks line leaks the absolute home path: %s", l)
		}
	}

	// Loaded through an API key and current: ok, no warning.
	out, rep = runIsolation(t, home, codex, map[string]string{"OPENAI_API_KEY": "k"})
	if !strings.Contains(out, "bypass-hook-trust") || rep.Warnings != 0 {
		t.Errorf("want an ok line, got %d warnings:\n%s", rep.Warnings, out)
	}

	// Drift, direction 1: bytes that differ from the render are stale, and the
	// text says the gate is off (not "codex will report modified").
	writeProfileFile(t, home, "hooks.json", `{"hooks":{}}`)
	out, rep = runIsolation(t, home, codex, map[string]string{"OPENAI_API_KEY": "k"})
	if !strings.Contains(out, "differs from what this yakos installs") || !strings.Contains(out, "gate is OFF") || rep.Warnings != 1 {
		t.Errorf("want the stale warning, got %d warnings:\n%s", rep.Warnings, out)
	}

	// Drift, direction 2: an absolute-binary install of THIS binary is current
	// (the old check rendered with a bare name and called it stale).
	writeProfileFile(t, home, "hooks.json", string(cur))
	if out, rep = runIsolation(t, home, codex, map[string]string{"OPENAI_API_KEY": "k"}); rep.Warnings != 0 || !strings.Contains(out, "[ok]") {
		t.Errorf("a current absolute install must not warn, got %d:\n%s", rep.Warnings, out)
	}

	// An install for a binary that is gone: the hooks would fail open.
	gone := filepath.Join(t.TempDir(), "yakos")
	if err := os.WriteFile(gone, []byte("x"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	other, _ := hooksinstall.RenderShapeFile(hooksinstall.HarnessCodex, gone)
	writeProfileFile(t, home, "hooks.json", string(other))
	_ = os.Remove(gone)
	out, rep = runIsolation(t, home, codex, map[string]string{"OPENAI_API_KEY": "k"})
	if !strings.Contains(out, "no longer exists") || rep.Warnings < 1 {
		t.Errorf("want the missing-binary warning, got %d:\n%s", rep.Warnings, out)
	}

	// Right bytes but group-writable: not trusted.
	if runtime.GOOS != "windows" {
		dir := writeProfileFile(t, home, "hooks.json", string(cur))
		if err := os.Chmod(filepath.Join(dir, "hooks.json"), 0o666); err != nil { //nolint:gosec
			t.Fatal(err)
		}
		out, rep = runIsolation(t, home, codex, map[string]string{"OPENAI_API_KEY": "k"})
		if !strings.Contains(out, "will NOT trust") || rep.Warnings != 1 {
			t.Errorf("want the unsafe warning, got %d:\n%s", rep.Warnings, out)
		}
	}

	// No hooks file: nothing to say.
	if out, _ := runIsolation(t, t.TempDir(), codex, map[string]string{"OPENAI_API_KEY": "k"}); out != "" {
		t.Errorf("no hooks file should be silent, got %q", out)
	}
}
