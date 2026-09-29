package hookio

import (
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

func inputOf(t *testing.T, raw string) hooktype.HookInput {
	t.Helper()
	in, err := DecodeBytes([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	in.Env = map[string]string{"YAKOS_AGENT_ROLE": "env-role", "CLAUDE_SESSION_ID": "env-sid"}
	return in
}

// Table mirrors bash hi_sender_role / hi_session_id (jq -r '.x // empty').
func TestSenderRoleAndSessionIDMirrorBash(t *testing.T) {
	cases := []struct {
		raw, role, sid string
	}{
		{`{}`, "lead", ""},
		{`{"agent_type":"","session_id":""}`, "lead", ""},
		{`{"agent_type":null,"session_id":null}`, "lead", ""},
		{`{"agent_type":false}`, "lead", ""},
		{`{"agent_type":"backend","session_id":"s1"}`, "backend", "s1"},
		{`{"agent_type":"yakos:backend"}`, "backend", ""},
		{`{"agent_type":" \t yakos:backend \n"}`, "backend", ""},
		{`{"agent_type":"yakos: x"}`, " x", ""}, // trim happens before the prefix strip, as in bash
		{`{"agent_type":"   "}`, "", ""},        // whitespace-only is non-empty for the fallback, then trims to empty
		{`{"agent_type":"yakos:yakos:a"}`, "yakos:a", ""},
		{`{"agent_type":"myns:helper"}`, "myns:helper", ""},
		{`{"agent_type":7,"session_id":12}`, "7", "12"}, // jq -r renders non-strings
	}
	for _, c := range cases {
		in := inputOf(t, c.raw)
		if got := SenderRole(in); got != c.role {
			t.Errorf("%s: SenderRole=%q want %q", c.raw, got, c.role)
		}
		if got := SessionID(in); got != c.sid {
			t.Errorf("%s: SessionID=%q want %q", c.raw, got, c.sid)
		}
	}
}
