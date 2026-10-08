package dispatch

import (
	"os"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// The line carries K-176's actor/agent/session_id and K-175's surface/auth_method
// together.
func TestConfigChangedLineCarriesBothAuditExtensions(t *testing.T) {
	dir := t.TempDir()
	au, err := OpenConfigAudit(Request{OperatorID: "alice", Surface: SurfaceConsole, AgentName: "backend", SessionID: "sess-1"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := au.Record(ConfigChange{File: "router-policy.yml", Action: "models.pin", Surface: SurfaceConsole, Actor: "operator-browser", AuthMethod: "session"}); err != nil {
		t.Fatal(err)
	}
	au.Close()
	b, _ := os.ReadFile(statepath.DispatchLogIn(dir))
	for _, want := range []string{`"surface":"console"`, `"actor":"operator-browser"`, `"auth_method":"session"`, `"agent":"backend"`, `"session_id":"sess-1"`, `"operator_id":"alice"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("line lacks %s: %s", want, b)
		}
	}
}
