package supervisorstream_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// TestSupervisorStream_LogRecordsUseBashSchema pins K-122: every record the
// Go twin writes carries bash's ho_log base fields in bash's order, and none
// of the old action/message names. A removed or renamed field fails here.
func TestSupervisorStream_LogRecordsUseBashSchema(t *testing.T) {
	work, proj := t.TempDir(), t.TempDir()
	writeYAML(t, proj, "supervisor:\n  score_every_n_calls: 1000\n")
	h := newHook(work, proj)
	for _, in := range []hooktype.HookInput{
		{Event: "PostToolUse", Tool: "Edit", Env: map[string]string{}, Payload: map[string]any{
			"session_id": "sid-1", "agent_type": "yakos:backend",
			"tool_input": map[string]any{"file_path": "api/main.go", "new_string": "x"}}},
		{Event: "PostToolUse", Tool: "Bash", Env: map[string]string{}, Payload: map[string]any{
			"session_id": "sid-2",
			"tool_input": map[string]any{"command": "rm -rf /tmp/x"}}},
	} {
		if _, err := h.Run(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(work, "logs", "supervisor-stream.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		t.Fatalf("want >=2 records, got %d", len(lines))
	}
	prefix := `{"ts":`
	for _, ln := range lines {
		if !strings.HasPrefix(ln, prefix) {
			t.Fatalf("record does not start with ts: %s", ln)
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"ts", "hook", "severity", "decision", "reason", "agent", "session_id", "event"} {
			if _, ok := rec[k]; !ok {
				t.Errorf("record missing %q: %s", k, ln)
			}
		}
		for _, k := range []string{"action", "message"} {
			if _, ok := rec[k]; ok {
				t.Errorf("record still has legacy %q: %s", k, ln)
			}
		}
		// Base fields appear in bash's order.
		last := -1
		for _, k := range []string{`"ts"`, `"hook"`, `"severity"`, `"decision"`, `"reason"`, `"agent"`, `"session_id"`, `"event"`} {
			i := bytes.Index([]byte(ln), []byte(k+":"))
			if i <= last {
				t.Errorf("base field %s out of order: %s", k, ln)
			}
			last = i
		}
	}
	var first map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &first)
	if first["agent"] != "backend" || first["session_id"] != "sid-1" || first["event"] != "PostToolUse" {
		t.Errorf("identity fields wrong: %v", first)
	}
}
