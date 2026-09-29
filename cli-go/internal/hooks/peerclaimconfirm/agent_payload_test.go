package peerclaimconfirm_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// bash peer-claim-confirm.sh: YAKOS_AGENT_ID="$(hi_sender_role)" — the agent
// comes from the payload's agent_type, not from YAKOS_AGENT_ROLE.
func TestConfirmAgentFromPayloadNotEnv(t *testing.T) {
	cases := []struct{ name, payload, want string }{
		{"prefixed", `{"agent_type":"yakos:database","tool_input":{"file_path":"a.sql","content":"x"}}`, `"agent":"database"`},
		{"absent means lead", `{"tool_input":{"file_path":"a.sql","content":"x"}}`, `"agent":"lead"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			coord := filepath.Join(root, "proj", "coord")
			if err := os.MkdirAll(coord, 0o755); err != nil {
				t.Fatal(err)
			}
			h := newHook(root, coord)
			in := hooktype.HookInput{Event: "PostToolUse", Tool: "Write",
				Payload: realPayload(t, c.payload),
				Env:     map[string]string{"YAKOS_AGENT_ROLE": "env-role", "CLAUDE_PROJECT_DIR": "/x"}}
			if out, err := h.Run(context.Background(), in); err != nil || out.ExitCode != 0 {
				t.Fatalf("err=%v exit=%d", err, out.ExitCode)
			}
			data, err := os.ReadFile(filepath.Join(coord, "activity.ndjson"))
			if err != nil {
				t.Fatalf("claim_confirmed not emitted: %v", err)
			}
			if !strings.Contains(string(data), c.want) || strings.Contains(string(data), "env-role") {
				t.Fatalf("want %s, no env-role; got %s", c.want, data)
			}
		})
	}
}
