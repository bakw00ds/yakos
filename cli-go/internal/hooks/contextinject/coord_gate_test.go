package contextinject_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func peerSessions(t *testing.T, root string, names ...string) string {
	t.Helper()
	coord := filepath.Join(root, "proj", "coord")
	sess := filepath.Join(coord, "sessions")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(sess, n), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return coord
}

func peerOutput(t *testing.T, root string, env map[string]string) string {
	t.Helper()
	writeYAML(t, root, "context_inject:\n  enabled: true\n")
	in := makeInput("UserPromptSubmit", "", env)
	out, err := newHook(root, root).Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return string(out.Stderr)
}

func coordEnv(root string) map[string]string {
	return map[string]string{"YAKOS_COORD_ROOT": root, "YAKOS_PROJECT_NAME": "proj"}
}

// bash gates on yakos_coord_enabled (coord dir exists + writable); there is
// no YAKOS_COORD_ENABLED switch, so a present coord dir alone must enable it.
func TestPeerSectionFromCoordDirAlone(t *testing.T) {
	root := t.TempDir()
	peerSessions(t, root, "a@h-1.ndjson", "b@h-2.ndjson")
	if got := peerOutput(t, root, coordEnv(root)); !strings.Contains(got, "Multi-dev: 2 peer session(s)") {
		t.Fatalf("want Multi-dev line, got %q", got)
	}
}

// The old env gate is gone: YAKOS_COORD_ENABLED=1 / YAKOS_COORD_DIR do not
// enable anything when the bash-resolved coord dir is absent.
func TestOldEnvGateDoesNotEnable(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	coord := peerSessions(t, other, "a@h-1.ndjson", "b@h-2.ndjson")
	env := coordEnv(filepath.Join(root, "nocoord"))
	env["YAKOS_COORD_ENABLED"] = "1"
	env["YAKOS_COORD_DIR"] = coord
	if got := peerOutput(t, root, env); strings.Contains(got, "Multi-dev") {
		t.Fatalf("env gate must be ignored, got %q", got)
	}
}

func TestPeerSectionUnwritableCoordDirDisabled(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs non-root POSIX")
	}
	root := t.TempDir()
	coord := peerSessions(t, root, "a@h-1.ndjson", "b@h-2.ndjson")
	if err := os.Chmod(coord, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(coord, 0o755) })
	if got := peerOutput(t, root, coordEnv(root)); strings.Contains(got, "Multi-dev") {
		t.Fatalf("read-only coord dir must disable the section, got %q", got)
	}
}

// `find sessions -name '*.ndjson'` is recursive and a single session is not
// "multi-dev".
func TestPeerCountRecursiveAndThreshold(t *testing.T) {
	root := t.TempDir()
	coord := peerSessions(t, root, "a@h-1.ndjson")
	if got := peerOutput(t, root, coordEnv(root)); strings.Contains(got, "Multi-dev") {
		t.Fatalf("one session must not report Multi-dev, got %q", got)
	}
	nested := filepath.Join(coord, "sessions", "archive")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "old@h-9.ndjson"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := peerOutput(t, root, coordEnv(root)); !strings.Contains(got, "Multi-dev: 2 peer session(s)") {
		t.Fatalf("nested session file must count like find, got %q", got)
	}
}
