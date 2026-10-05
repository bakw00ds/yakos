package dispatch

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func readLines(t *testing.T, in []byte, cfg ReadLineLoopConfig) []string {
	t.Helper()
	var got []string
	ReadLineLoop(bytes.NewReader(in), cfg, func(line []byte) { got = append(got, string(line)) })
	return got
}

// Blank lines are dropped by default (every existing caller relies on it) and
// delivered when asked for, so plain text keeps its paragraph breaks.
func TestReadLineLoop_KeepEmptyLines(t *testing.T) {
	in := []byte("a\n\nb\r\n\r\nc")
	if got, want := readLines(t, in, ReadLineLoopConfig{}), []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("default: %q, want %q", got, want)
	}
	if got, want := readLines(t, in, ReadLineLoopConfig{KeepEmptyLines: true}), []string{"a", "", "b", "", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("KeepEmptyLines: %q, want %q", got, want)
	}
	// The newline that ends the last line does not make a trailing empty line.
	if got, want := readLines(t, []byte("x\n"), ReadLineLoopConfig{KeepEmptyLines: true}), []string{"x"}; !reflect.DeepEqual(got, want) {
		t.Errorf("trailing newline: %q, want %q", got, want)
	}
}

// OnOverlong fires once per line dropped for length, whether the line arrives
// across several reads or inside one.
func TestReadLineLoop_OnOverlong(t *testing.T) {
	long := strings.Repeat("x", maxStreamLineBytes+1)
	for name, bufSize := range map[string]int{"small reads": 64 * 1024, "one big read": 8 * 1024 * 1024} {
		t.Run(name, func(t *testing.T) {
			dropped := 0
			var got []string
			ReadLineLoop(strings.NewReader(long+"\nok\n"+long+"\nlast\n"),
				ReadLineLoopConfig{BufSize: bufSize, OnOverlong: func() { dropped++ }},
				func(line []byte) { got = append(got, string(line)) })
			if dropped != 2 {
				t.Errorf("OnOverlong calls = %d, want 2", dropped)
			}
			if want := []string{"ok", "last"}; !reflect.DeepEqual(got, want) {
				t.Errorf("lines = %q, want %q", got, want)
			}
		})
	}
}
