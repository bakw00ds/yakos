//go:build !windows

package validate

// standards_reader_unix_test.go — the whole-tree standards passes (shebang,
// header, TODO-only, dark-code, SKILL.md sections) read every .sh and .md under
// cli/ and lib/. They read through the same hardened reader as the rest of
// validate: a FIFO or a device is refused without being opened (it would hang
// `yakos validate` for good), and a symlink must stay inside its roots (a link
// out of the tree would have its content judged and reported). Each case runs
// under a deadline; a reader stuck in open(2) is released afterwards.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/agentscompose"
)

// todoOnly is a file the TODO-only pass would warn about if it read it.
var todoOnly = strings.Repeat("# TODO fix\n", 12)

func runStandards(t *testing.T, root, fifo string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	var buf bytes.Buffer
	cfg := Config{YakosRoot: root, Writer: &buf, ErrWriter: &buf}
	r := &Result{}
	within(t, 10*time.Second, fifo, func() { runStandardsChecks(cfg, r, &buf) })
	return buf.String()
}

// mentions reports the lines of out that name one of names, other than the
// dark-code finding, which lists a script by name without reading it.
func mentions(out string, names ...string) []string {
	var got []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "potential dark code") {
			continue
		}
		for _, n := range names {
			if strings.Contains(l, n) {
				got = append(got, l)
				break
			}
		}
	}
	return got
}

func mkfifoOrSkip(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}
}

func TestStandards_FIFOsAndDevicesAreRefusedWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "lib", "agents", "ok.md"), agentBody("ok"))
	agentFIFO := filepath.Join(root, "lib", "agents", "pipe.md")
	hookFIFO := filepath.Join(root, "lib", "hooks", "pipe.sh")
	skillFIFO := filepath.Join(root, "lib", "skills", "x", "SKILL.md")
	for _, p := range []string{agentFIFO, hookFIFO, skillFIFO} {
		mkfifoOrSkip(t, p)
	}
	symlinkOrSkip(t, "/dev/zero", filepath.Join(root, "lib", "agents", "zero.md"))
	symlinkOrSkip(t, "/dev/zero", filepath.Join(root, "lib", "hooks", "zero.sh"))

	out := runStandards(t, root, agentFIFO)
	for _, p := range []string{agentFIFO, hookFIFO, skillFIFO} {
		releaseFIFO(p)
	}
	if got := mentions(out, "pipe.", "zero.", "SKILL.md"); len(got) != 0 {
		t.Errorf("a refused entry was reported or read: %q", got)
	}
}

func TestStandards_ALinkOutOfTheTreeIsNotRead(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret.md")
	writeFile(t, secret, todoOnly)
	secretSh := filepath.Join(outside, "secret.sh")
	writeFile(t, secretSh, todoOnly)
	writeFile(t, filepath.Join(root, "lib", "agents", "ok.md"), agentBody("ok"))
	symlinkOrSkip(t, secret, filepath.Join(root, "lib", "agents", "leak.md"))
	symlinkOrSkip(t, secretSh, filepath.Join(root, "lib", "hooks", "leak.sh"))
	symlinkOrSkip(t, secret, filepath.Join(root, "lib", "skills", "x", "SKILL.md"))

	out := runStandards(t, root, secret)
	if got := mentions(out, "TODO-only", "leak.", "SKILL.md"); len(got) != 0 {
		t.Errorf("the content of a file outside the tree was judged or reported: %q", got)
	}
}

// A link that stays inside the tree is still read: the hardening must not turn
// the framework's own lib/hooks/<name>.sh -> legacy/<name>.sh links into silence.
func TestStandards_ALinkInsideTheTreeIsStillRead(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "lib", "hooks", "legacy", "real.sh"), todoOnly)
	symlinkOrSkip(t, "legacy/real.sh", filepath.Join(root, "lib", "hooks", "link.sh"))
	out := runStandards(t, root, root)
	if !strings.Contains(out, filepath.Join(root, "lib", "hooks", "link.sh")+": appears to be TODO-only") {
		t.Errorf("an in-tree link was not read:\n%s", out)
	}
}

func TestReadTreeFile_RefusalsArePathFree(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "very-secret-name.md")
	writeFile(t, secret, "x\n")
	link := filepath.Join(root, "lib", "agents", "leak.md")
	symlinkOrSkip(t, secret, link)
	_, err := readTreeFile(Config{YakosRoot: root}, link)
	if !errors.Is(err, agentscompose.ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
	if strings.Contains(err.Error(), "very-secret-name") || strings.Contains(err.Error(), outside) {
		t.Errorf("the refusal names the link target: %v", err)
	}
}

// A rules link out of the rules directory is read by neither the line-budget pass
// nor the playbook-reference pass: its line count and its references would be
// content of a file that is not a rule.
func TestRules_ALinkOutOfTheRulesDirectoryIsNotRead(t *testing.T) {
	root, proj, _ := agentFilesProject(t)
	outside := filepath.Join(t.TempDir(), "notes.md")
	writeFile(t, outside, strings.Repeat("line 111\n", 200)+"- playbook:very-secret-playbook\n")
	symlinkOrSkip(t, outside, filepath.Join(proj, ".claude", "rules", "leak.md"))
	out, _ := validateProject(t, root, proj)
	if strings.Contains(out, "201 lines") || strings.Contains(out, "very-secret-playbook") {
		t.Errorf("the content of a file outside the rules directory was used:\n%s", out)
	}
}
