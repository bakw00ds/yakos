package routerpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func skipIfNoPosixModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
}

// writePolicy writes body to <dir>/router-policy.yml with mode.
func writePolicy(t *testing.T, dir, body string, mode os.FileMode) string {
	t.Helper()
	p := Path(dir)
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile's mode is filtered by the umask; set it exactly.
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAllowsUnsandboxed_MissingFileAllowsNothing(t *testing.T) {
	dir := t.TempDir()
	ok, err := AllowsUnsandboxed(dir, "codex")
	if ok || err != nil {
		t.Fatalf("missing file: got (%v, %v), want (false, nil)", ok, err)
	}
}

func TestAllowsUnsandboxed_EmptyStateDirAllowsNothing(t *testing.T) {
	ok, err := AllowsUnsandboxed("", "codex")
	if ok || err != nil {
		t.Fatalf("empty state dir: got (%v, %v), want (false, nil)", ok, err)
	}
}

func TestAllowsUnsandboxed_ListedRuntimes(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "allow_unsandboxed_runtimes: [codex, AGY]\n", 0o600)
	for rt, want := range map[string]bool{
		"codex":  true,
		"agy":    true, // entry is matched case-insensitively
		"Codex":  true,
		"claude": false,
		"gemini": false,
		"":       false,
		"cod":    false, // no prefix matching
		"*":      false, // no wildcards
	} {
		got, err := AllowsUnsandboxed(dir, rt)
		if err != nil {
			t.Fatalf("%q: unexpected error %v", rt, err)
		}
		if got != want {
			t.Errorf("AllowsUnsandboxed(%q) = %v, want %v", rt, got, want)
		}
	}
}

func TestAllowsUnsandboxed_BlockListAndUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	// A policy written for a newer yakOS carries keys this loader does not know.
	writePolicy(t, dir, "rules:\n  - id: R0\n    match: {class: chat}\nallow_unsandboxed_runtimes:\n  - codex\nfuture_key: 1\n", 0o600)
	got, err := AllowsUnsandboxed(dir, "codex")
	if err != nil || !got {
		t.Fatalf("block list + unknown keys: got (%v, %v), want (true, nil)", got, err)
	}
	if got, _ := AllowsUnsandboxed(dir, "agy"); got {
		t.Error("agy must not be allowed when only codex is listed")
	}
}

func TestAllowsUnsandboxed_ReadableByOthersIsStillTrusted(t *testing.T) {
	skipIfNoPosixModes(t)
	dir := t.TempDir()
	// Same rule as the budget policy: only group/world WRITE disqualifies.
	writePolicy(t, dir, "allow_unsandboxed_runtimes: [codex]\n", 0o644)
	got, err := AllowsUnsandboxed(dir, "codex")
	if err != nil || !got {
		t.Fatalf("0644: got (%v, %v), want (true, nil)", got, err)
	}
}

// TestLoad_GroupOrWorldWritableIsUntrusted is the trust-check regression: a
// file another account can edit must never loosen the sandbox default.
func TestLoad_GroupOrWorldWritableIsUntrusted(t *testing.T) {
	skipIfNoPosixModes(t)
	for _, mode := range []os.FileMode{0o660, 0o620, 0o666, 0o602, 0o664} {
		dir := t.TempDir()
		writePolicy(t, dir, "allow_unsandboxed_runtimes: [codex, agy]\n", mode)
		got, err := AllowsUnsandboxed(dir, "codex")
		if got {
			t.Errorf("mode %o: group/world-writable policy was trusted", mode)
		}
		if !errors.Is(err, ErrUntrusted) {
			t.Errorf("mode %o: err = %v, want ErrUntrusted", mode, err)
		}
		if err != nil && !strings.Contains(err.Error(), "writable") {
			t.Errorf("mode %o: error should say why: %v", mode, err)
		}
	}
}

func TestLoad_SymlinkIsUntrusted(t *testing.T) {
	skipIfNoPosixModes(t)
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere.yml")
	if err := os.WriteFile(target, []byte("allow_unsandboxed_runtimes: [codex]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, Path(dir)); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	got, err := AllowsUnsandboxed(dir, "codex")
	if got || !errors.Is(err, ErrUntrusted) {
		t.Fatalf("symlinked policy: got (%v, %v), want (false, ErrUntrusted)", got, err)
	}
}

func TestLoad_DirectoryIsUntrusted(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(Path(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := AllowsUnsandboxed(dir, "codex")
	if got || !errors.Is(err, ErrUntrusted) {
		t.Fatalf("directory policy: got (%v, %v), want (false, ErrUntrusted)", got, err)
	}
}

func TestLoad_MalformedAndMistypedFailClosed(t *testing.T) {
	for name, body := range map[string]string{
		"not yaml":        "allow_unsandboxed_runtimes: [codex\n",
		"scalar not list": "allow_unsandboxed_runtimes: codex\n",
		"mapping":         "allow_unsandboxed_runtimes: {codex: true}\n",
		"duplicate key":   "allow_unsandboxed_runtimes: [agy]\nallow_unsandboxed_runtimes: [codex]\n",
	} {
		dir := t.TempDir()
		writePolicy(t, dir, body, 0o600)
		got, err := AllowsUnsandboxed(dir, "codex")
		if got {
			t.Errorf("%s: bad policy allowed codex", name)
		}
		if err == nil {
			t.Errorf("%s: expected a parse error so the caller can report it", name)
		}
	}
}

func TestLoad_OversizedFileIsRejected(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "allow_unsandboxed_runtimes: [codex]\n#"+strings.Repeat("x", maxPolicyBytes), 0o600)
	got, err := AllowsUnsandboxed(dir, "codex")
	if got || !errors.Is(err, ErrUntrusted) {
		t.Fatalf("oversized policy: got (%v, %v), want (false, ErrUntrusted)", got, err)
	}
}

func TestStateDir_IgnoresDispatchLogRelocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("YAKOS_DISPATCH_LOG", filepath.Join(t.TempDir(), "project-controlled"))
	want := filepath.Join(home, ".yakos-state")
	if got := StateDir(); got != want {
		t.Fatalf("StateDir() = %q, want %q (YAKOS_DISPATCH_LOG must not relocate the policy)", got, want)
	}
}

// Load hands the rules over undecoded and cites the digest of the bytes it read.
func TestLoad_RulesAndSHA(t *testing.T) {
	dir := t.TempDir()
	body := "allow_unsandboxed_runtimes: [codex]\nrules:\n  - {action: {runtime: codex}}\n  - 7\n"
	writePolicy(t, dir, body, 0o600)
	f, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	if f.SHA != hex.EncodeToString(sum[:]) {
		t.Errorf("SHA = %q", f.SHA)
	}
	if f.Rules.Kind != yaml.SequenceNode || len(f.Rules.Content) != 2 {
		t.Errorf("rules node = kind %v, %d items; a malformed item must not fail the file", f.Rules.Kind, len(f.Rules.Content))
	}
	if empty, _ := Load(t.TempDir()); empty.SHA != "" || empty.Rules.Kind != 0 {
		t.Errorf("a missing file has no sha or rules: %+v", empty)
	}
}
