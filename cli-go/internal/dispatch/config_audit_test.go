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
