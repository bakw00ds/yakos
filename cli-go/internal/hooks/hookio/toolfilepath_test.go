package hookio_test

import (
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// TestToolFilePath mirrors bash hi_file_path:
// .tool_input.file_path // .tool_input.notebook_path, never a top-level key.
func TestToolFilePath(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{"write", map[string]any{"tool_input": map[string]any{"file_path": "/p/a.go", "content": "x"}}, "/p/a.go"},
		{"notebook", map[string]any{"tool_input": map[string]any{"notebook_path": "/p/n.ipynb", "new_source": "x"}}, "/p/n.ipynb"},
		{"file_path wins", map[string]any{"tool_input": map[string]any{"file_path": "/a", "notebook_path": "/b"}}, "/a"},
		{"empty string does not fall through (jq //)", map[string]any{"tool_input": map[string]any{"file_path": "", "notebook_path": "/b"}}, ""},
		{"top-level path ignored", map[string]any{"path": "/x", "file_path": "/y"}, ""},
		{"no tool_input", map[string]any{}, ""},
		{"tool_input not an object", map[string]any{"tool_input": "x"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hookio.ToolFilePath(hooktype.HookInput{Payload: c.payload})
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}
