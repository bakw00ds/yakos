package dispatch

import (
	"os"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/statepath"
)

func TestConfigAuditWritesToTheGivenDirNotTheEnvOverride(t *testing.T) {
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	dir := t.TempDir()
	au, err := OpenConfigAudit(Request{OperatorID: "op", Surface: SurfaceCLI}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := au.Record(ConfigChange{File: "router-policy.yml", Action: "x", Surface: SurfaceCLI}); err != nil {
		t.Fatal(err)
	}
	au.Close()
	b, err := os.ReadFile(statepath.DispatchLogIn(dir))
	if err != nil || !strings.Contains(string(b), `"type":"config_changed"`) {
		t.Errorf("log = %q err=%v", b, err)
	}
}

func TestOpenConfigAuditFailsWhenTheLogCannotBeOpened(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(statepath.DispatchLogIn(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenConfigAudit(Request{}, dir); err == nil {
		t.Error("opened a directory as the log")
	}
	if _, err := OpenConfigAudit(Request{}, ""); err == nil {
		t.Error("opened with no state directory")
	}
}

func TestConfigChangedLineCarriesActorAgentAndSession(t *testing.T) {
	a := NewAccount(Request{OperatorID: "op", AgentName: "backend", SessionID: "sess-1"})
	line, err := a.configChangedLine(ConfigChange{File: "model-registry.yml", Action: "models.enable", Surface: SurfaceCLI, Actor: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"actor":"agent"`, `"agent":"backend"`, `"session_id":"sess-1"`, `"operator_id":"op"`} {
		if !strings.Contains(string(line), want) {
			t.Errorf("line %s lacks %s", line, want)
		}
	}
	// An empty identity is omitted and a bad one is dropped, never logged.
	line, _ = NewAccount(Request{OperatorID: "op", AgentName: "a b", SessionID: "x\ny"}).configChangedLine(ConfigChange{File: "router-policy.yml", Action: "x", Actor: "Bad Actor"})
	for _, bad := range []string{`"actor"`, `"agent"`, `"session_id"`} {
		if strings.Contains(string(line), bad) {
			t.Errorf("line %s holds %s", line, bad)
		}
	}
}

func TestConfigAuditStampsItsActorWhenTheChangeNamesNone(t *testing.T) {
	dir := t.TempDir()
	au, err := OpenConfigAudit(Request{OperatorID: "op"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	au.Actor = "agent"
	if err := au.Record(ConfigChange{File: "router-policy.yml", Action: "x"}); err != nil {
		t.Fatal(err)
	}
	au.Close()
	b, _ := os.ReadFile(statepath.DispatchLogIn(dir))
	if !strings.Contains(string(b), `"actor":"agent"`) {
		t.Errorf("log = %q", b)
	}
}
