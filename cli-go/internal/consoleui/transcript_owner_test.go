package consoleui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
)

// FirstUserOwner names who started a conversation: the operator of its first
// user turn. Every dispatch asks (sec-324 F1), so it reads only as far as that
// turn, and it must agree with what the whole-file reader would have said.
func TestFirstUserOwner(t *testing.T) {
	write := func(t *testing.T, lines ...string) (*consoleui.Transcripts, string) {
		t.Helper()
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "chats"), 0o700); err != nil {
			t.Fatal(err)
		}
		if len(lines) > 0 {
			if err := os.WriteFile(filepath.Join(dir, "chats", "conv-1.ndjson"), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return consoleui.NewTranscripts(dir), "conv-1"
	}
	user := func(op string) string {
		return `{"session_id":"s","conversation_id":"conv-1","operator_id":"` + op + `","role":"user","text":"hi"}`
	}
	assistant := `{"session_id":"s","conversation_id":"conv-1","operator_id":"alice","role":"assistant","text":"yo"}`

	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"no transcript", nil, ""},
		{"the first user turn", []string{user("alice"), assistant, user("bob")}, "alice"},
		{"only assistant turns", []string{assistant, assistant}, ""},
		{"blank and damaged lines are skipped", []string{"", "not json", `{"role":"user"`, user("bob")}, "bob"},
		{"a user turn with no operator is skipped", []string{`{"role":"user","text":"x"}`, user("carol")}, "carol"},
		{"the last line needs no newline", []string{assistant, user("dave")}, "dave"},
		{"after a long first line", []string{strings.Replace(user("erin"), `"hi"`, `"`+strings.Repeat("x", 300<<10)+`"`, 1), user("frank")}, "erin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr, conv := write(t, tc.lines...)
			got, err := tr.FirstUserOwner(conv)
			if err != nil || got != tc.want {
				t.Errorf("FirstUserOwner = %q, %v; want %q", got, err, tc.want)
			}
		})
	}

	t.Run("an invalid conversation id is an error, not a path", func(t *testing.T) {
		tr := consoleui.NewTranscripts(t.TempDir())
		if _, err := tr.FirstUserOwner("../escape"); err == nil {
			t.Error("a path-shaped id was accepted")
		}
	})

	// A conversation of many megabytes is answered from its first line.
	t.Run("a large transcript", func(t *testing.T) {
		lines := []string{user("alice")}
		filler := assistant
		for i := 0; i < 20000; i++ {
			lines = append(lines, filler)
		}
		tr, conv := write(t, lines...)
		if got, err := tr.FirstUserOwner(conv); err != nil || got != "alice" {
			t.Errorf("FirstUserOwner = %q, %v; want alice", got, err)
		}
	})
}
