package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHooksInstallHarnessErrorsAndWarnings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	var out, errb bytes.Buffer

	// A bare or relative --binary is refused, with one "hooks install:" prefix.
	for _, bin := range []string{"yakos", "./yakos"} {
		out.Reset()
		errb.Reset()
		if code := runHooksInstallHarness("agy", ws, bin, &out, &errb); code != 1 {
			t.Fatalf("binary %q: exit %d", bin, code)
		}
		if n := strings.Count(errb.String(), "hooks install:"); n != 1 {
			t.Errorf("binary %q: prefix appears %d times: %q", bin, n, errb.String())
		}
	}
	if _, err := os.Stat(filepath.Join(ws, ".agents")); err == nil {
		t.Error("a refused install created .agents")
	}

	// Foreign entries are kept and listed, hostile names quoted.
	if err := os.Mkdir(filepath.Join(ws, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc, _ := json.Marshal(map[string]any{"theirs": map[string]any{"enabled": true}, "x\x1b[31m\nred": map[string]any{}})
	if err := os.WriteFile(filepath.Join(ws, ".agents", "hooks.json"), doc, 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := runHooksInstallHarness("agy", ws, "", &out, &errb); code != 0 {
		t.Fatalf("install: exit %d %s", code, errb.String())
	}
	w := errb.String()
	if !strings.Contains(w, "warning") || !strings.Contains(w, `"theirs"`) || strings.ContainsAny(strings.TrimSuffix(w, "\n"), "\x1b\n") {
		t.Errorf("foreign warning missing or unsafe: %q", w)
	}
}
