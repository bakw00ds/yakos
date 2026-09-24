package dirsize_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/dirsize"
)

func skipIfNoDu(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("du"); err != nil {
		t.Skip("du not available on PATH")
	}
}

func TestBytes_NonexistentDir_ReturnsZero(t *testing.T) {
	if got := dirsize.Bytes(filepath.Join(t.TempDir(), "does-not-exist")); got != 0 {
		t.Errorf("expected 0 for nonexistent dir; got %d", got)
	}
}

func TestBytes_EmptyStringDir_ReturnsZero(t *testing.T) {
	if got := dirsize.Bytes(""); got != 0 {
		t.Errorf("expected 0 for empty dir path; got %d", got)
	}
}

func TestBytes_FileNotDir_ReturnsZero(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := dirsize.Bytes(f); got != 0 {
		t.Errorf("expected 0 for a file path (not a directory); got %d", got)
	}
}

func TestBytes_NonEmptyDir_ReturnsPositive(t *testing.T) {
	skipIfNoDu(t)
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), make([]byte, 8192), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := dirsize.Bytes(tmp)
	if got <= 0 {
		t.Errorf("expected a positive size for a dir containing an 8KiB file; got %d", got)
	}
}

func TestBytes_MatchesDuDirectly(t *testing.T) {
	// Cross-check against whatever this package's own fallback chain
	// would compute by hand, using the same du binary — this is the
	// parity guarantee the package exists for (S-6 A-2a round 2 review
	// findings 1/2): identical numbers to bash's ct_dir_size_bytes
	// because it's literally the same underlying `du`.
	skipIfNoDu(t)
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), make([]byte, 16384), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := dirsize.Bytes(tmp)

	var want int64
	if out, err := exec.Command("du", "-sb", tmp).Output(); err == nil {
		want = parseFirstField(t, out)
	} else if out, err := exec.Command("du", "-sk", tmp).Output(); err == nil {
		want = parseFirstField(t, out) * 1024
	} else {
		t.Skip("neither du -sb nor du -sk succeeded on this platform")
	}

	if got != want {
		t.Errorf("Bytes(%q) = %d; want %d (matching direct du invocation)", tmp, got, want)
	}
}

func parseFirstField(t *testing.T, out []byte) int64 {
	t.Helper()
	lines := strings.SplitN(string(out), "\n", 2)
	fields := strings.Fields(lines[0])
	if len(fields) == 0 {
		t.Fatalf("no fields in du output %q", out)
	}
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		t.Fatalf("parse du output %q: %v", out, err)
	}
	return n
}
