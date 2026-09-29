package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/registry"
)

func TestDegradedReasonMatchesBashText(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"empty":      {hookio.ErrEmptyStdin, "stdin was provided but empty (0 bytes) — expected a JSON hook payload"},
		"not json":   {fmt.Errorf("%w: boom", hookio.ErrNotJSON), "stdin did not parse as valid JSON"},
		"not object": {hookio.ErrNotObject, "stdin parsed as JSON but is not a JSON object (hook payloads are always an object)"},
		"other":      {errors.New("weird"), "weird"},
	}
	for name, c := range cases {
		if got := degradedReason(c.err); got != c.want {
			t.Errorf("%s: %q want %q", name, got, c.want)
		}
	}
	// End to end through the real decoder.
	for in, want := range map[string]string{
		"":        "stdin was provided but empty (0 bytes) — expected a JSON hook payload",
		"{nope":   "stdin did not parse as valid JSON",
		"[1,2,3]": "stdin parsed as JSON but is not a JSON object (hook payloads are always an object)",
		"null":    "stdin parsed as JSON but is not a JSON object (hook payloads are always an object)",
		"\"str\"": "stdin parsed as JSON but is not a JSON object (hook payloads are always an object)",
	} {
		_, err := hookio.DecodeBytes([]byte(in))
		if err == nil {
			t.Fatalf("%q decoded", in)
		}
		if got := degradedReason(err); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}

func TestDegradedBypassActiveIsExactScope(t *testing.T) {
	dir := t.TempDir()
	write := func(scope string) {
		body := "# Active hook bypasses\n\n## Active entries\n\n## bypass:x\n\n**Hook:** path-allowlist\n**Scope:** " + scope + "\n"
		if err := os.WriteFile(filepath.Join(dir, "hook-bypass.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if degradedBypassActive(dir, "path-allowlist") {
		t.Fatal("no file must not bypass")
	}
	write("degraded-input")
	if !degradedBypassActive(dir, "path-allowlist") {
		t.Fatal("exact sentinel scope must bypass")
	}
	for _, scope := range []string{"api/degraded-input.go", "not-degraded-input", "api/legacy/vendor.pem", ""} {
		write(scope)
		if degradedBypassActive(dir, "path-allowlist") {
			t.Errorf("scope %q must NOT count as the degraded-input sentinel", scope)
		}
	}
	if degradedBypassActive("", "path-allowlist") {
		t.Fatal("empty work dir must not bypass")
	}
}

func TestResolveHooksDirIsPhysicalPath(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real-hooks")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(root, "lib", "hooks")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolveHooksDir(root); got != want {
		t.Fatalf("resolveHooksDir=%q want physical %q (bash's pwd -P)", got, want)
	}
	// No lib/hooks anywhere: still returns <root>/lib/hooks, never "".
	empty := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if got := resolveHooksDir(empty); !strings.HasSuffix(got, filepath.Join("lib", "hooks")) {
		t.Fatalf("fallback=%q", got)
	}
}

// failDegraded exits the process, so it is exercised in a child process:
// the test binary re-executes itself into TestFailDegradedHelper.
func TestFailDegradedHelper(t *testing.T) {
	if os.Getenv("YAKOS_TEST_FAILDEGRADED_HELPER") != "1" {
		t.Skip("helper for TestFailDegraded; only runs in the child process")
	}
	entry := registry.Entry{Name: "path-allowlist", FailClosed: os.Getenv("YAKOS_TEST_FAILCLOSED") == "1"}
	failDegraded("path-allowlist", entry, os.Getenv("YAKOS_TEST_WORK"), "stdin did not parse as valid JSON")
}

func runFailDegraded(t *testing.T, failClosed bool, extraEnv ...string) (code int, stderr string, logLine string) {
	t.Helper()
	work := t.TempDir()
	for _, kv := range extraEnv {
		if strings.HasPrefix(kv, "BYPASS_SCOPE=") {
			body := "## Active entries\n\n## bypass:x\n\n**Hook:** path-allowlist\n**Scope:** " + strings.TrimPrefix(kv, "BYPASS_SCOPE=") + "\n"
			if err := os.WriteFile(filepath.Join(work, "hook-bypass.md"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestFailDegradedHelper$")
	fc := "0"
	if failClosed {
		fc = "1"
	}
	cmd.Env = append(os.Environ(), "YAKOS_TEST_FAILDEGRADED_HELPER=1", "YAKOS_TEST_FAILCLOSED="+fc, "YAKOS_TEST_WORK="+work, "YAKOS_HOOKS_FAIL_OPEN=")
	for _, kv := range extraEnv {
		if !strings.HasPrefix(kv, "BYPASS_SCOPE=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	var eb bytes.Buffer
	cmd.Stderr = &eb
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(work, "logs", "path-allowlist.ndjson"))
	return code, eb.String(), strings.TrimSpace(string(data))
}

func TestFailDegraded(t *testing.T) {
	// Fail-closed hook, no override: BLOCK record + 6 stderr lines + exit 2.
	code, stderr, log := runFailDegraded(t, true)
	if code != 2 {
		t.Fatalf("exit=%d want 2", code)
	}
	if !strings.HasPrefix(stderr, "path-allowlist: BLOCKED — cannot safely evaluate this tool call (stdin did not parse as valid JSON).\n") ||
		strings.Count(stderr, "\n") != 6 {
		t.Fatalf("stderr=%q", stderr)
	}
	for _, want := range []string{`"severity":"BLOCK"`, `"decision":"block"`, `"reason":"degraded input, failing closed: stdin did not parse as valid JSON"`, `"agent":"lead"`, `"session_id":""`, `"event":""`} {
		if !strings.Contains(log, want) {
			t.Errorf("log %s missing %s", log, want)
		}
	}

	// YAKOS_HOOKS_FAIL_OPEN=1: WARN record, 2 stderr lines, exit 0.
	code, stderr, log = runFailDegraded(t, true, "YAKOS_HOOKS_FAIL_OPEN=1")
	if code != 0 || strings.Count(stderr, "\n") != 2 || !strings.Contains(log, "YAKOS_HOOKS_FAIL_OPEN=1 override active") {
		t.Fatalf("fail-open: code=%d stderr=%q log=%s", code, stderr, log)
	}

	// Exact degraded-input bypass scope: WARN record, exit 0.
	code, stderr, log = runFailDegraded(t, true, "BYPASS_SCOPE=degraded-input")
	if code != 0 || !strings.Contains(log, "hook-bypass.md override active (scope: degraded-input)") || !strings.Contains(stderr, "scoped to 'degraded-input' is active") {
		t.Fatalf("bypass: code=%d stderr=%q log=%s", code, stderr, log)
	}

	// A substring scope must NOT bypass.
	code, _, _ = runFailDegraded(t, true, "BYPASS_SCOPE=api/degraded-input.go")
	if code != 2 {
		t.Fatalf("substring scope must still block, exit=%d", code)
	}

	// Telemetry hook: WARN on stderr, exit 0, no log record.
	code, stderr, log = runFailDegraded(t, false)
	if code != 0 || log != "" || !strings.Contains(stderr, "This hook is degraded for this event") {
		t.Fatalf("telemetry: code=%d stderr=%q log=%q", code, stderr, log)
	}
}
