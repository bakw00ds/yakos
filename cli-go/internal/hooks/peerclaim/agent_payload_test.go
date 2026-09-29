package peerclaim_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bash peer-claim.sh reads the caller identity from the stdin payload
// (hi_sender_role: .agent_type, "yakos:" stripped, trimmed, "lead" when
// absent), never from an env var. The emitted claim_intent event's actor.agent
// is where that identity lands.
func TestAgentFromPayloadNotEnv(t *testing.T) {
	cases := []struct {
		name, payload, want string
	}{
		{"prefixed", `{"tool_name":"Edit","agent_type":"yakos:backend","tool_input":{"file_path":"a.go"}}`, `"agent":"backend"`},
		{"trimmed", `{"tool_name":"Edit","agent_type":"  go-api \n","tool_input":{"file_path":"a.go"}}`, `"agent":"go-api"`},
		{"absent means lead", `{"tool_name":"Edit","tool_input":{"file_path":"a.go"}}`, `"agent":"lead"`},
		{"empty means lead", `{"tool_name":"Edit","agent_type":"","tool_input":{"file_path":"a.go"}}`, `"agent":"lead"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmp := t.TempDir()
			if err := os.MkdirAll(filepath.Join(tmp, "proj", "coord"), 0o755); err != nil {
				t.Fatal(err)
			}
			in := gateInput(t, tmp)
			in.Payload = realPayload(t, c.payload)
			in.Env["YAKOS_AGENT_ROLE"] = "env-role" // bash never reads this
			out, err := gateHook(tmp).Run(context.Background(), in)
			if err != nil || out.ExitCode != 0 {
				t.Fatalf("err=%v exit=%d stderr=%s", err, out.ExitCode, out.Stderr)
			}
			data, err := os.ReadFile(filepath.Join(tmp, "proj", "coord", "activity.ndjson"))
			if err != nil {
				t.Fatalf("claim_intent not emitted: %v", err)
			}
			if !strings.Contains(string(data), c.want) {
				t.Fatalf("want %s in event, got %s", c.want, data)
			}
			if strings.Contains(string(data), "env-role") {
				t.Fatalf("YAKOS_AGENT_ROLE leaked into the event: %s", data)
			}
		})
	}
}
