package hookio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
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
