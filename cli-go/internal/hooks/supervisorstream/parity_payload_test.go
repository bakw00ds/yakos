package supervisorstream_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

func ssProject(t *testing.T, allowlist string) (work, proj string) {
	t.Helper()
	work, proj = t.TempDir(), t.TempDir()
	writeYAML(t, proj, "supervisor:\n  enabled: true\n")
	if allowlist != "" {
		if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proj, ".claude", "path-allowlist.json"), []byte(allowlist), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return work, proj
}

func ssRun(t *testing.T, work, proj, raw string, env map[string]string) hooktype.HookOutput {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	out, err := newHook(work, proj).Run(context.Background(),
		hooktype.HookInput{Event: "PreToolUse", Tool: "Edit", Payload: payload, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func lastBuffered(t *testing.T, work string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(work, "supervisor-buffer.ndjson"))
	if err != nil {
		t.Fatalf("buffer not written: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var ev map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &ev); err != nil {
		t.Fatal(err)
	}
	return ev
}

// bash: agent="$(hi_sender_role)"; session_id="$(hi_session_id)" — both from
// the payload. Env values (YAKOS_AGENT_ROLE, CLAUDE_SESSION_ID) are ignored.
func TestAgentAndSessionFromPayloadNotEnv(t *testing.T) {
	work, proj := ssProject(t, "")
	ssRun(t, work, proj,
		`{"session_id":"sess-payload","agent_type":"yakos:backend","tool_input":{"file_path":"a.go","new_string":"x"}}`,
		map[string]string{"YAKOS_AGENT_ROLE": "env-role", "CLAUDE_SESSION_ID": "sess-env"})
	ev := lastBuffered(t, work)
	if ev["agent"] != "backend" || ev["session_id"] != "sess-payload" {
		t.Fatalf("agent=%v session_id=%v, want backend / sess-payload", ev["agent"], ev["session_id"])
	}
}

func TestAgentLeadAndEmptySessionWhenAbsent(t *testing.T) {
	work, proj := ssProject(t, "")
	ssRun(t, work, proj, `{"tool_input":{"file_path":"a.go","new_string":"x"}}`,
		map[string]string{"YAKOS_AGENT_ROLE": "env-role", "CLAUDE_SESSION_ID": "sess-env"})
	ev := lastBuffered(t, work)
	if ev["agent"] != "lead" || ev["session_id"] != "" {
		t.Fatalf("agent=%v session_id=%v, want lead / empty", ev["agent"], ev["session_id"])
	}
}
