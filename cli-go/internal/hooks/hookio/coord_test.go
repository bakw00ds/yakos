package hookio_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

func TestCoordDirResolution(t *testing.T) {
	mk := func(env map[string]string) hooktype.HookInput { return hooktype.HookInput{Env: env} }
	if got := hookio.CoordDir(mk(map[string]string{"YAKOS_COORD_ROOT": "/r", "YAKOS_PROJECT_NAME": "p"})); got != "/r/p/coord" {
		t.Fatalf("got %s", got)
	}
	if got := hookio.CoordDir(mk(map[string]string{"CLAUDE_PROJECT_DIR": "/a/b/proj"})); got != "/var/lib/yakos/proj/coord" {
		t.Fatalf("got %s", got)
	}
}

func TestCoordEnabledFile(t *testing.T) {
	d := t.TempDir()
	if hookio.CoordEnabled(filepath.Join(d, "missing")) {
		t.Fatal("missing dir enabled")
	}
	f := filepath.Join(d, "f")
	_ = os.WriteFile(f, nil, 0o644)
	if hookio.CoordEnabled(f) {
		t.Fatal("regular file enabled")
	}
	if !hookio.CoordEnabled(d) {
		t.Fatal("writable dir not enabled")
	}
}
