package teach

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/agentscompose"
)

// teach reads the agent through agentscompose's reader before it backs up or edits
// anything: a refused file is neither copied nor written.
func TestRun_RefusesWhatTheRosterReaderRefuses(t *testing.T) {
	for _, tc := range []string{"symlink", "oversize"} {
		t.Run(tc, func(t *testing.T) {
			dir := t.TempDir()
			agents := filepath.Join(dir, ".claude", "agents")
			if err := os.MkdirAll(agents, 0o755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(agents, "a.md")
			var outside string
			if tc == "symlink" {
				outside = filepath.Join(t.TempDir(), "creds.md")
				if err := os.WriteFile(outside, []byte("TOPSECRET\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, target); err != nil {
					t.Skipf("no symlinks here: %v", err)
				}
			} else if err := os.WriteFile(target, make([]byte, agentscompose.MaxAgentFileBytes+1), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := newBufferedConfig(dir, "a", makeLessonFile(t, t.TempDir(), "lesson"))
			cfg.YakosRoot = t.TempDir()
			_, err := Run(cfg)
			if !errors.Is(err, agentscompose.ErrRefused) {
				t.Fatalf("err = %v, want ErrRefused", err)
			}
			if outside != "" {
				if b, _ := os.ReadFile(outside); string(b) != "TOPSECRET\n" {
					t.Errorf("the link target was edited: %q", b)
				}
			}
			ents, _ := os.ReadDir(agents)
			for _, e := range ents {
				if strings.Contains(e.Name(), "yakos-bak") {
					t.Errorf("a backup of a refused file was made: %s", e.Name())
				}
			}
		})
	}
}
