package hooksinstall

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBin makes an executable regular file and returns its resolved path.
func fakeBin(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "yakos")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// renderBin is an absolute path on the host OS (filepath.IsAbs is false for a
// POSIX literal on Windows).
var renderBin = func() string {
	if runtime.GOOS == "windows" {
		return `C:\opt\yakos\bin\yakos.exe`
	}
	return "/opt/yakos/bin/yakos"
}()

// jsonText is s as it appears inside a JSON string (backslashes doubled).
func jsonText(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }

// commandOf returns the command of hook name in a rendered hooks file.
func commandOf(t *testing.T, harness string, file []byte, name string) string {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(file, &doc); err != nil {
		t.Fatal(err)
	}
	inner := doc["hooks"]
	if harness == HarnessAgy {
		inner = doc[agyKey]
	}
	var ev struct {
		PreToolUse  []shapeGroup
		PostToolUse []shapeGroup
	}
	if err := json.Unmarshal(inner, &ev); err != nil {
		t.Fatal(err)
	}
	for _, g := range append(ev.PreToolUse, ev.PostToolUse...) {
		for _, h := range g.Hooks {
			if strings.Contains(h.Command, " "+name) {
				return h.Command
			}
		}
	}
	t.Fatalf("no command for %s in %s", name, file)
	return ""
}

func TestRenderShapeFileGoldenCommands(t *testing.T) {
	for _, h := range []string{HarnessCodex, HarnessAgy} {
		b, err := RenderShapeFile(h, renderBin)
		if err != nil {
			t.Fatal(err)
		}
		b2, _ := RenderShapeFile(h, renderBin)
		if !bytes.Equal(b, b2) {
			t.Fatalf("%s: not byte-stable", h)
		}
		for _, name := range []string{"budget-guard", "path-allowlist", "secret-scan", "supervisor-stream"} {
			cmd := commandOf(t, h, b, name)
			if !strings.Contains(cmd, "hook run --shape "+h+" "+name) || commandBinary(cmd) != renderBin {
				t.Errorf("%s/%s: command %q does not run %s", h, name, cmd, renderBin)
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
	for _, bin := range []string{"", "yakos", "./yakos", "bin/yakos", "../yakos", "/opt/a b/yakos", "/opt/x;y", "/opt/$(x)", "/opt/`x`", "/opt/yakos\n", "/opt/../bin/yakos", "/opt//yakos"} {
		if _, err := RenderShapeFile(HarnessCodex, bin); err == nil {
			t.Errorf("binary %q accepted", bin)
		}
	}
	if _, err := RenderShapeFile("gemini", renderBin); err == nil {
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
	exe, _ := ResolveBinary("")
	want, _ := RenderShapeFile(HarnessCodex, exe)
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

// H1: a workspace that commits .agents -> <elsewhere> must not redirect the
// install. Red before the OpenRoot change: the file was written into the target.
func TestInstallShapeRefusesSymlinkedAgentsDir(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "hooks.json")
	if err := os.WriteFile(victim, []byte(`{"theirs":{"enabled":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, ".agents")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, _, err := InstallShape(HarnessAgy, ws, fakeBin(t)); err == nil {
		t.Fatal("installed through a symlinked .agents")
	}
	if got, _ := os.ReadFile(victim); string(got) != `{"theirs":{"enabled":true}}` {
		t.Errorf("outside file modified: %s", got)
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 1 {
		t.Errorf("outside dir gained files: %v", ents)
	}
	// A dangling link, and a link to a file, are refused too.
	ws2 := t.TempDir()
	_ = os.Symlink(filepath.Join(outside, "nope"), filepath.Join(ws2, ".agents"))
	if _, _, err := InstallShape(HarnessAgy, ws2, fakeBin(t)); err == nil {
		t.Error("installed through a dangling .agents link")
	}
	if _, err := os.Stat(filepath.Join(outside, "nope")); err == nil {
		t.Error("dangling link target was created")
	}
}

// A link that stays inside the workspace is refused too: the install must not
// depend on where the link points.
func TestInstallShapeRefusesInWorkspaceAgentsLink(t *testing.T) {
	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(ws, ".agents")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, _, err := InstallShape(HarnessAgy, ws, fakeBin(t)); err == nil {
		t.Error("installed through an in-workspace .agents link")
	}
	if ents, _ := os.ReadDir(filepath.Join(ws, "real")); len(ents) != 0 {
		t.Errorf("link target gained files: %v", ents)
	}
}

func TestInstallShapeRefusesSymlinkedHooksFileInAgents(t *testing.T) {
	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "v")
	_ = os.WriteFile(victim, []byte("keep"), 0o600)
	if err := os.Symlink(victim, filepath.Join(ws, ".agents", "hooks.json")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, _, err := InstallShape(HarnessAgy, ws, fakeBin(t)); err == nil {
		t.Error("wrote through a symlinked hooks.json")
	}
	if got, _ := os.ReadFile(victim); string(got) != "keep" {
		t.Error("target modified")
	}
}

// H2: the command word is an absolute path; bare and relative values are
// refused, and the default is the running executable.
func TestInstallShapeWritesAbsoluteBinary(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBin(t)
	p, _, err := InstallShape(HarnessCodex, dir, bin)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if c := commandOf(t, HarnessCodex, got, "budget-guard"); commandBinary(c) != bin {
		t.Errorf("absolute path missing from %q (want %s)", c, bin)
	}
	for _, bad := range []string{"yakos", "./yakos", "bin/yakos", "../yakos"} {
		if _, _, err := InstallShape(HarnessCodex, t.TempDir(), bad); err == nil {
			t.Errorf("binary %q accepted", bad)
		}
	}
	// default = the running executable, symlink-resolved
	dir2 := t.TempDir()
	p2, _, err := InstallShape(HarnessCodex, dir2, "")
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	got2, _ := os.ReadFile(p2)
	if c := commandOf(t, HarnessCodex, got2, "budget-guard"); commandBinary(c) != exe {
		t.Errorf("default binary is not the running executable %s: %q", exe, c)
	}
}

func TestResolveBinaryChecks(t *testing.T) {
	d := t.TempDir()
	if _, err := ResolveBinary(d); err == nil {
		t.Error("directory accepted")
	}
	if _, err := ResolveBinary(filepath.Join(d, "missing")); err == nil {
		t.Error("missing binary accepted")
	}
	w := filepath.Join(d, "ww")
	_ = os.WriteFile(w, []byte("x"), 0o755)    //nolint:gosec
	if err := os.Chmod(w, 0o777); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if os.PathSeparator == '/' {
		if _, err := ResolveBinary(w); err == nil {
			t.Error("world-writable binary accepted")
		}
	}
	link := filepath.Join(d, "link")
	real := fakeBin(t)
	if err := os.Symlink(real, link); err == nil {
		got, err := ResolveBinary(link)
		if err != nil || got != real {
			t.Errorf("symlink not resolved: %q %v", got, err)
		}
	}
}

// H3 helper: the trust decision.
func TestCodexHooksTrusted(t *testing.T) {
	prof := filepath.Join(t.TempDir(), "codex-home")
	if CodexHooksTrusted(prof) {
		t.Error("missing profile trusted")
	}
	if err := os.MkdirAll(prof, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InstallShape(HarnessCodex, prof, ""); err != nil {
		t.Fatal(err)
	}
	if !CodexHooksTrusted(prof) {
		t.Fatal("fresh install not trusted")
	}
	p := ShapeTarget(HarnessCodex, prof)
	good, _ := os.ReadFile(p)

	// tampered command
	tampered := strings.Replace(string(good), "budget-guard", "budget-guard; touch /tmp/PWNED $(id)", 1)
	_ = os.WriteFile(p, []byte(tampered), 0o600)
	if CodexHooksTrusted(prof) || ShapeDrift(HarnessCodex, prof, "") != "stale" {
		t.Error("tampered file trusted")
	}
	// binary pointing elsewhere (a valid yakOS-shaped file for another binary)
	other, _ := RenderShapeFile(HarnessCodex, fakeBin(t))
	_ = os.WriteFile(p, other, 0o600)
	if CodexHooksTrusted(prof) {
		t.Error("file for a different binary trusted")
	}
	// right bytes, group/world writable
	_ = os.WriteFile(p, good, 0o600)
	if os.PathSeparator == '/' {
		_ = os.Chmod(p, 0o664) //nolint:gosec
		if CodexHooksTrusted(prof) || ShapeDrift(HarnessCodex, prof, "") != "unsafe" {
			t.Error("group-writable file trusted")
		}
		_ = os.Chmod(p, 0o600)
		_ = os.Chmod(prof, 0o770) //nolint:gosec
		if CodexHooksTrusted(prof) {
			t.Error("group-writable profile trusted")
		}
		_ = os.Chmod(prof, 0o700)
	}
	if !CodexHooksTrusted(prof) {
		t.Fatal("restored file not trusted")
	}
	// symlinked file with right bytes
	real := filepath.Join(t.TempDir(), "real.json")
	_ = os.WriteFile(real, good, 0o600)
	_ = os.Remove(p)
	if err := os.Symlink(real, p); err == nil && CodexHooksTrusted(prof) {
		t.Error("symlinked hooks.json trusted")
	}
}

func TestInspectShapeReportsMissingBinary(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBin(t)
	if _, _, err := InstallShape(HarnessCodex, dir, bin); err != nil {
		t.Fatal(err)
	}
	in := InspectShape(HarnessCodex, dir, bin)
	if in.State != "current" || in.Binary != bin || in.BinaryMissing {
		t.Fatalf("fresh: %+v", in)
	}
	_ = os.Remove(bin)
	in = InspectShape(HarnessCodex, dir, "")
	if in.Binary != bin || !in.BinaryMissing {
		t.Errorf("removed binary not reported: %+v", in)
	}
}

// M2: foreign agy entries are kept and reported, with hostile names quoted.
func TestInstallShapeAgyReportsForeignHooks(t *testing.T) {
	ws := t.TempDir()
	p := ShapeTarget(HarnessAgy, ws)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	hostile := "evil\u001b[2J\nname"
	doc, _ := json.Marshal(map[string]any{"mine": map[string]any{"enabled": true}, hostile: map[string]any{"enabled": true}, "yakos": map[string]any{"stale": true}})
	if err := os.WriteFile(p, doc, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := InstallShapeReport(HarnessAgy, ws, fakeBin(t))
	if err != nil || !res.Changed {
		t.Fatalf("%v %+v", err, res)
	}
	if len(res.Foreign) != 2 {
		t.Fatalf("foreign = %v", res.Foreign)
	}
	for _, n := range res.Foreign {
		if strings.ContainsAny(n, "\x1b\n") || n[0] != '"' {
			t.Errorf("name not quoted: %q", n)
		}
	}
	var m map[string]json.RawMessage
	got, _ := os.ReadFile(p)
	if json.Unmarshal(got, &m) != nil || m["mine"] == nil || m["yakos"] == nil {
		t.Errorf("foreign entry lost: %s", got)
	}
}

func TestBinaryCharsOKIsOSAware(t *testing.T) {
	win := `C:\Users\RUNNER~1\AppData\Local\Temp\go-build\yakos.test.exe`
	for _, c := range []struct {
		p, goos string
		want    bool
	}{
		{win, "windows", true},
		{win, "linux", false},
		{"/usr/local/bin/yakos", "linux", true},
		{"/usr/local/bin/yakos", "windows", true},
		{`C:\Program Files\yakos.exe`, "windows", false},
		{`C:\a;b\yakos.exe`, "windows", false},
		{`C:\a&b\yakos.exe`, "windows", false},
		{"/tmp/a b", "linux", false},
		{"/tmp/a;b", "linux", false},
		{"/tmp/~x", "linux", false},
		{"/tmp/a$b", "windows", false},
	} {
		if got := binaryCharsOK(c.p, c.goos); got != c.want {
			t.Errorf("binaryCharsOK(%q, %s) = %v, want %v", c.p, c.goos, got, c.want)
		}
	}
}

// K-170 (c): exact launcher text on Unix. A change here changes codex's hook
// hash, so it must be deliberate.
func TestShapeCommandGoldenUnix(t *testing.T) {
	cases := []struct{ harness, name, event, want string }{
		{"codex", "path-allowlist", "PreToolUse", `/bin/sh -c 'if [ -f "$0" ] && [ -x "$0" ]; then "$0" hook run --shape codex path-allowlist; rc=$?; if [ $rc -eq 0 ] || [ $rc -eq 2 ]; then exit $rc; fi; fi; echo "yakOS: the hook binary is missing, not executable or failed to run; refusing the tool call" >&2; exit 2' /opt/yakos/bin/yakos`},
		{"agy", "secret-scan", "PreToolUse", `/bin/sh -c 'if [ -f "$0" ] && [ -x "$0" ]; then out=$("$0" hook run --shape agy secret-scan) && { printf "%s\n" "$out"; exit 0; }; fi; printf "%s\n" "{\"decision\":\"deny\",\"reason\":\"yakOS: the hook binary is missing, not executable or failed to run; refusing the tool call\"}"; exit 0' /opt/yakos/bin/yakos`},
		{"codex", "supervisor-stream", "PostToolUse", `/bin/sh -c 'if [ -f "$0" ] && [ -x "$0" ]; then exec "$0" hook run --shape codex supervisor-stream; fi; echo "yakOS: the hook binary is missing or not executable; this hook was skipped" >&2; exit 0' /opt/yakos/bin/yakos`},
		{"agy", "supervisor-stream", "PostToolUse", `/bin/sh -c 'if [ -f "$0" ] && [ -x "$0" ]; then exec "$0" hook run --shape agy supervisor-stream; fi; echo "yakOS: the hook binary is missing or not executable; this hook was skipped" >&2; printf "{}\n"; exit 0' /opt/yakos/bin/yakos`},
	}
	for _, c := range cases {
		if got := shapeCommand("linux", c.harness, "/opt/yakos/bin/yakos", c.name, c.event); got != c.want {
			t.Errorf("%s/%s:\n got %s\nwant %s", c.harness, c.name, got, c.want)
		}
		if got := commandBinary(c.want); got != "/opt/yakos/bin/yakos" {
			t.Errorf("%s/%s: commandBinary = %q", c.harness, c.name, got)
		}
	}
	// Windows has no /bin/sh: the bare absolute path, as before.
	if got := shapeCommand("windows", "codex", `C:\y\yakos.exe`, "secret-scan", "PreToolUse"); got != `C:\y\yakos.exe hook run --shape codex secret-scan` {
		t.Errorf("windows command = %q", got)
	}
	if got := commandBinary(`C:\y\yakos.exe hook run --shape codex secret-scan`); got != `C:\y\yakos.exe` {
		t.Errorf("windows commandBinary = %q", got)
	}
}

// runLauncher plays the harness: sh -c <command> with stdin, from an unrelated
// cwd, and returns exit code, stdout and stderr.
func runLauncher(t *testing.T, command, stdin string) (int, string, string) {
	t.Helper()
	sh := os.Getenv("YAKOS_TEST_SH") // e.g. /bin/dash: the launcher must be POSIX sh
	if sh == "" {
		sh = "sh"
	}
	cmd := exec.Command(sh, "-c", command) //nolint:gosec
	cmd.Dir = t.TempDir()
	cmd.Stdin = strings.NewReader(stdin)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v", err)
	}
	return code, o.String(), e.String()
}

// stubBin writes an executable shell script that stands in for yakos.
func stubBin(t *testing.T, name, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), mode); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// K-170 (c): with the hook binary missing, not executable, a directory, or
// crashing, a PreToolUse hook REFUSES in each harness's own deny shape; a
// healthy binary's answer passes through unchanged; PostToolUse never blocks.
func TestLauncherFailsClosedForBothHarnesses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the launcher is a /bin/sh script; Windows keeps the bare command")
	}
	notExec := stubBin(t, "yakos", "exit 0", 0o644)
	dirBin := t.TempDir()
	missing := filepath.Join(t.TempDir(), "no-such-yakos")
	crash127 := stubBin(t, "yakos", "exit 127", 0o755)
	crash1 := stubBin(t, "yakos", "echo partial; exit 1", 0o755)
	signalled := stubBin(t, "yakos", "kill -9 $$", 0o755)

	isDeny := func(harness string, code int, out, errOut string) bool {
		if harness == HarnessCodex {
			return code == 2 && strings.Contains(errOut, "refusing the tool call")
		}
		var d struct{ Decision, Reason string }
		return code == 0 && json.Unmarshal([]byte(out), &d) == nil && d.Decision == "deny" && strings.Contains(d.Reason, "refusing the tool call")
	}
	for _, harness := range []string{HarnessCodex, HarnessAgy} {
		for name, bin := range map[string]string{"missing": missing, "not executable": notExec, "directory": dirBin, "exit 127": crash127, "exit 1": crash1, "killed": signalled} {
			cmd := shapeCommand("linux", harness, bin, "path-allowlist", "PreToolUse")
			code, out, errOut := runLauncher(t, cmd, "{}")
			if !isDeny(harness, code, out, errOut) {
				t.Errorf("%s/%s: not refused: exit %d stdout %q stderr %q", harness, name, code, out, errOut)
			}
			if harness == HarnessAgy && strings.Contains(out, "partial") {
				t.Errorf("%s/%s: a crashing binary's partial output reached the harness: %q", harness, name, out)
			}
			// A telemetry hook never blocks when the binary cannot start, and
			// says why it was skipped.
			if name != "missing" && name != "not executable" && name != "directory" {
				continue
			}
			post := shapeCommand("linux", harness, bin, "supervisor-stream", "PostToolUse")
			pc, pout, perr := runLauncher(t, post, "{}")
			if harness == HarnessCodex && pc != 0 || harness == HarnessAgy && (pc != 0 || strings.TrimSpace(pout) != "{}") {
				t.Errorf("%s/%s: PostToolUse blocked or malformed: exit %d out %q", harness, name, pc, pout)
			}
			if name == "missing" && !strings.Contains(perr, "skipped") {
				t.Errorf("%s: no skip notice: %q", harness, perr)
			}
		}
	}

	// A working binary is run with the right arguments and stdin, and its own
	// allow (0), deny (2) or JSON answer is passed through untouched.
	echo := stubBin(t, "yakos", `echo "args:$*"; cat >&2; exit ${STUB_EXIT:-0}`, 0o755)
	cmd := shapeCommand("linux", HarnessCodex, echo, "secret-scan", "PreToolUse")
	if code, out, errOut := runLauncher(t, cmd, "ENVELOPE"); code != 0 || out != "args:hook run --shape codex secret-scan\n" || errOut != "ENVELOPE" {
		t.Errorf("codex passthrough: exit %d out %q err %q", code, out, errOut)
	}
	deny2 := stubBin(t, "yakos", `echo blocked >&2; exit 2`, 0o755)
	if code, _, errOut := runLauncher(t, shapeCommand("linux", HarnessCodex, deny2, "secret-scan", "PreToolUse"), ""); code != 2 || errOut != "blocked\n" {
		t.Errorf("codex deny passthrough: exit %d err %q", code, errOut)
	}
	agyAnswer := stubBin(t, "yakos", `echo '{"decision":"allow"}'`, 0o755)
	if code, out, _ := runLauncher(t, shapeCommand("linux", HarnessAgy, agyAnswer, "secret-scan", "PreToolUse"), ""); code != 0 || out != "{\"decision\":\"allow\"}\n" {
		t.Errorf("agy passthrough: exit %d out %q", code, out)
	}
}

// The file Install writes carries the launcher, and a file Install wrote is
// recognised again (binary parsed back, state current).
func TestInstalledFileUsesLauncherAndRoundTrips(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("launcher is Unix-only")
	}
	bin := fakeBin(t)
	for _, harness := range []string{HarnessCodex, HarnessAgy} {
		dir := t.TempDir()
		p, _, err := InstallShape(harness, dir, bin)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(p)
		for _, name := range []string{"budget-guard", "path-allowlist", "secret-scan"} {
			if c := commandOf(t, harness, raw, name); !strings.HasPrefix(c, "/bin/sh -c '") || commandBinary(c) != bin {
				t.Errorf("%s/%s: not a launcher for %s: %q", harness, name, bin, c)
			}
		}
		in := InspectShape(harness, dir, bin)
		if in.State != "current" || in.Binary != bin {
			t.Errorf("%s: inspect = %+v", harness, in)
		}
		if err := os.Remove(bin); err != nil {
			t.Fatal(err)
		}
		if in := InspectShape(harness, dir, bin); !in.BinaryMissing {
			t.Errorf("%s: a deleted binary is not reported missing: %+v", harness, in)
		}
		bin = fakeBin(t)
	}
}
