package hooksinstall

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderShapeFileGoldenCommands(t *testing.T) {
	for _, h := range []string{HarnessCodex, HarnessAgy} {
		b, err := RenderShapeFile(h, "")
		if err != nil {
			t.Fatal(err)
		}
		b2, _ := RenderShapeFile(h, "")
		if !bytes.Equal(b, b2) {
			t.Fatalf("%s: not byte-stable", h)
		}
		for _, name := range []string{"budget-guard", "path-allowlist", "secret-scan", "supervisor-stream"} {
			want := `"command": "yakos hook run --shape ` + h + ` ` + name + `"`
			if !strings.Contains(string(b), want) {
				t.Errorf("%s: missing %s", h, want)
			}
		}
		if !json.Valid(b) || b[len(b)-1] != '\n' {
			t.Errorf("%s: invalid JSON or no trailing newline", h)
		}
		// Hash stability: nothing per-run or per-host in the text.
		for _, bad := range []string{"/tmp", "/private", "TMPDIR", "pid"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s: volatile text %q", h, bad)
			}
		}
	}
}

func TestRenderShapeFileRejectsBadInput(t *testing.T) {
	for _, bin := range []string{"yakos; rm -rf /", "a b", "$(x)", "`x`", "yakos\n"} {
		if _, err := RenderShapeFile(HarnessCodex, bin); err == nil {
			t.Errorf("binary %q accepted", bin)
		}
	}
	if _, err := RenderShapeFile("gemini", ""); err == nil {
		t.Error("unknown harness accepted")
	}
}

func TestInstallShapeRewritesOnlyWhenContentDiffers(t *testing.T) {
	dir := t.TempDir()
	p, changed, err := InstallShape(HarnessCodex, dir, "")
	if err != nil || !changed {
		t.Fatalf("first install: %v %v", err, changed)
	}
	fi1, _ := os.Stat(p)
	if fi1.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
		t.Errorf("mode = %v", fi1.Mode().Perm())
	}
	if _, changed, err = InstallShape(HarnessCodex, dir, ""); err != nil || changed {
		t.Fatalf("second install changed=%v err=%v", changed, err)
	}
	fi2, _ := os.Stat(p)
	if !os.SameFile(fi1, fi2) || !fi1.ModTime().Equal(fi2.ModTime()) {
		t.Error("unchanged install touched the file")
	}
	if ShapeDrift(HarnessCodex, dir, "") != "current" {
		t.Error("drift != current")
	}
	if err := os.WriteFile(p, []byte(`{"hooks":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if ShapeDrift(HarnessCodex, dir, "") != "stale" {
		t.Error("drift != stale")
	}
	if _, changed, _ = InstallShape(HarnessCodex, dir, ""); !changed {
		t.Fatal("stale file not refreshed")
	}
	want, _ := RenderShapeFile(HarnessCodex, "")
	if got, _ := os.ReadFile(p); !bytes.Equal(got, want) {
		t.Error("refreshed bytes differ from render")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("leftover files: %v", ents)
	}
	_ = os.Remove(p)
	if ShapeDrift(HarnessCodex, dir, "") != "missing" {
		t.Error("drift != missing")
	}
}

func TestInstallShapeAgyMergesAndIsStable(t *testing.T) {
	ws := t.TempDir()
	p := ShapeTarget(HarnessAgy, ws)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"mine":{"Stop":[{"command":"./s.sh"}],"enabled":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := InstallShape(HarnessAgy, ws, ""); err != nil || !changed {
		t.Fatalf("merge install: %v %v", err, changed)
	}
	var doc map[string]json.RawMessage
	got, _ := os.ReadFile(p)
	if err := json.Unmarshal(got, &doc); err != nil || doc["mine"] == nil || doc["yakos"] == nil {
		t.Fatalf("merge lost a key: %v %s", err, got)
	}
	if _, changed, _ := InstallShape(HarnessAgy, ws, ""); changed {
		t.Error("second merge rewrote the file")
	}
	if ShapeDrift(HarnessAgy, ws, "") != "current" {
		t.Error("merged file reported stale")
	}
}

func TestInstallShapeRefusals(t *testing.T) {
	ws := t.TempDir()
	p := ShapeTarget(HarnessAgy, ws)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(`[not an object`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InstallShape(HarnessAgy, ws, ""); err == nil {
		t.Error("overwrote an unparsable agy file")
	}
	if got, _ := os.ReadFile(p); string(got) != `[not an object` {
		t.Error("unparsable file was modified")
	}

	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "hooks.json")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, _, err := InstallShape(HarnessCodex, dir, ""); err == nil {
		t.Error("wrote through a symlink")
	}
	if got, _ := os.ReadFile(victim); string(got) != "keep" {
		t.Error("symlink target modified")
	}
	if _, _, err := InstallShape(HarnessCodex, "relative/dir", ""); err == nil {
		t.Error("relative dir accepted")
	}
}
