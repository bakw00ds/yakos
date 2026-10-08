package hookio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

type shellCase struct {
	Cmd  string   `json:"cmd"`
	Cwd  string   `json:"cwd"`
	Want []string `json:"want"`
	Dyn  int      `json:"dyn"`
}

func loadShellCases(t *testing.T) []shellCase {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", ".github", "fixtures", "hooks-shape", "shellwrites.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cs []shellCase
	if err := json.Unmarshal(b, &cs); err != nil {
		t.Fatal(err)
	}
	if len(cs) < 100 {
		t.Fatalf("only %d shell cases", len(cs))
	}
	return cs
}

// The corpus lives in a fixture file: the commands are deliberately the shapes
// that bypass a file-write gate.
func TestDecodeShellWritesCorpus(t *testing.T) {
	for _, c := range loadShellCases(t) {
		got := DecodeShellWrites(c.Cmd, c.Cwd)
		var paths []string
		dyn := 0
		for _, w := range got {
			if w.Dynamic {
				dyn++
			} else {
				paths = append(paths, w.Path)
			}
		}
		want := append([]string{}, c.Want...)
		sort.Strings(paths)
		sort.Strings(want)
		if strings.Join(paths, "\x00") != strings.Join(want, "\x00") || dyn != c.Dyn {
			t.Errorf("%q (cwd %q):\n  got  %q + %d dynamic\n  want %q + %d dynamic", c.Cmd, c.Cwd, paths, dyn, want, c.Dyn)
		}
	}
}

func TestDecodeShellWritesBounds(t *testing.T) {
	long := "echo x > a " + strings.Repeat("#", MaxShellCommandBytes)
	if got := DecodeShellWrites(long, ""); len(got) != 1 || !got[0].Dynamic {
		t.Errorf("oversize command: %+v", got)
	}
	// nesting beyond the cap is dynamic, not a stack overflow
	cmd := "echo x > .env"
	for i := 0; i < 20; i++ {
		cmd = "echo $(" + cmd + ")"
	}
	got := DecodeShellWrites(cmd, "")
	if len(got) == 0 || !got[len(got)-1].Dynamic {
		t.Errorf("deep nesting not reported dynamic: %+v", got)
	}
	// many targets are capped and the cap is reported
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("echo x > f" + strings.Repeat("a", i%7) + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + "; ")
	}
	got = DecodeShellWrites(b.String(), "")
	if len(got) > maxShellWrites+1 {
		t.Errorf("targets not capped: %d", len(got))
	}
	if !got[len(got)-1].Dynamic {
		t.Errorf("overflow not reported: %+v", got[len(got)-1])
	}
	// the rendering of a dynamic target is single-line and bounded
	for _, w := range DecodeShellWrites("echo x > \"$A\n\x00"+strings.Repeat("b", 500)+"\"", "") {
		if strings.ContainsAny(w.Path, "\n\x00") || len(w.Path) > maxRawPath+1 {
			t.Errorf("unsanitised dynamic path %q", w.Path)
		}
	}
}

// Garbage must never panic or hang.
func TestDecodeShellWritesNeverPanics(t *testing.T) {
	seeds := []string{"", "'", "\"", "`", "$(", "${", "$((", "<<", "<<EOF", "<<EOF\nbody", ">", ">>", "(((((", ")))", "\\", "$'\\x", "<(", ">(",
		"a=${", "cat <<'E\nx", "echo \"$(\"", "echo `", "{a,b", "x > {1..99999}", "$", "~", "sed -i", "cp", "tee", "python3 -c", "bash -c", "eval", "find -exec"}
	for _, s := range seeds {
		_ = DecodeShellWrites(s, "")
		_ = DecodeShellWrites("echo x "+s, "/w")
	}
	for i := 0; i < 2000; i++ {
		var b strings.Builder
		x := uint32(i*2654435761 + 1)
		for j := 0; j < 40; j++ {
			x = x*1664525 + 1013904223
			alpha := " \t\n'\"`$(){};&|<>\\#~=-.abcdef/!*"
			b.WriteByte(alpha[int(x>>24)%len(alpha)])
		}
		_ = DecodeShellWrites(b.String(), "")
	}
}

func TestDecodeShellWritesComplexityCapIsReported(t *testing.T) {
	cmd := strings.Repeat("a ", 25000) + "; echo x > .env"
	if len(cmd) > MaxShellCommandBytes {
		t.Fatal("test command is over the byte cap; it must hit the token cap")
	}
	got := DecodeShellWrites(cmd, "")
	found := false
	for _, w := range got {
		if w.Dynamic && strings.Contains(w.Path, "too complex") {
			found = true
		}
	}
	if !found {
		t.Errorf("token cap hit silently: %+v", got)
	}
}

func TestDecodeShellWritesLargeInputIsBounded(t *testing.T) {
	start := time.Now()
	big := "echo x > a " + strings.Repeat("y", 1<<20)
	got := DecodeShellWrites(big, "")
	if len(got) != 1 || !got[0].Dynamic {
		t.Errorf("1 MiB command: %+v", got)
	}
	// the largest command that is analysed, in several hostile shapes
	for _, unit := range []string{"$(", "'", "\"", "<<E\n", "a|", "`", "$'\\x", "[[ "} {
		n := MaxShellCommandBytes / len(unit)
		_ = DecodeShellWrites(strings.Repeat(unit, n), "")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("decoding took %v", d)
	}
}

func TestDecodeShellWritesRecoversFromAPanic(t *testing.T) {
	shellDecodeFault = func() { panic("boom") }
	defer func() { shellDecodeFault = nil }()
	got := DecodeShellWrites("echo x", "")
	if len(got) != 1 || !got[0].Dynamic || !strings.Contains(got[0].Path, "decoder failed") {
		t.Errorf("panic became %+v", got)
	}
}

func TestShellWriteInputsUnreadableCommandIsDynamic(t *testing.T) {
	mk := func(cmd any) []hooktype.HookInput {
		return []hooktype.HookInput{{Event: "PreToolUse", Tool: "Bash", Payload: map[string]any{"tool_input": map[string]any{"command": cmd}}}}
	}
	for name, cmd := range map[string]any{
		"object":          map[string]any{"a": "echo x > f"},
		"number":          7.0,
		"bool":            true,
		"argv non-string": []any{"tee", "f", 5.0},
		"argv nested":     []any{"bash", []any{"-c"}},
	} {
		got := ShellWriteInputs(mk(cmd))
		if len(got) != 1 || !strings.Contains(ToolFilePath(got[0]), "cannot be read") || got[0].Payload["tool_input"].(map[string]any)[ShellDynamicKey] != true {
			t.Errorf("%s: %+v", name, got)
		}
	}
	for name, cmd := range map[string]any{"empty": "", "nil": nil} {
		if got := ShellWriteInputs(mk(cmd)); len(got) != 0 {
			t.Errorf("%s: produced %+v", name, got)
		}
	}
	// the command may arrive under codex's "cmd" or agy's "CommandLine" too
	for _, key := range []string{"cmd", "CommandLine"} {
		in := []hooktype.HookInput{{Event: "PreToolUse", Tool: "Bash", Payload: map[string]any{"tool_input": map[string]any{key: "echo x > f"}}}}
		if got := ShellWriteInputs(in); len(got) != 1 || ToolFilePath(got[0]) != "f" {
			t.Errorf("%s: %+v", key, got)
		}
	}
}

// agy's run_command reaches the shell decoder through DecodeShape.
func TestAgyRunCommandIsAShellTool(t *testing.T) {
	ins, err := DecodeShape(ShapeAgy, []byte(`{"workspacePaths":["/w"],"toolCall":{"name":"run_command","args":{"CommandLine":"echo x > .env"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := ShellWriteInputs(ins)
	if len(got) != 1 || ToolFilePath(got[0]) != ".env" || got[0].Tool != "Write" {
		t.Errorf("agy run_command: %+v", got)
	}
}
