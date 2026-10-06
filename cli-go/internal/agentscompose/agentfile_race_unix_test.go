//go:build unix

package agentscompose

// agentfile_race_unix_test.go — what is inspected is what is read (rev-324).
// readAgentFile used to inspect a path and then open the same path again. In
// between, a symlink retargeted to an outside file was followed, and a regular
// file swapped for a FIFO held open(2) for good. raceHook runs exactly there, so
// each swap happens at the worst moment every time, and the concurrent test at
// the end adds the swaps nobody scheduled.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type raceFixture struct {
	agents, secret string
	rules          fileRules
}

func newRaceFixture(t *testing.T) raceFixture {
	t.Helper()
	agents := t.TempDir()
	secret := filepath.Join(t.TempDir(), "credentials")
	writeFileT(t, secret, secretText+"\n")
	return raceFixture{
		agents: agents,
		secret: secret,
		rules:  fileRules{roots: []string{agents}, outside: AgentOutsideReason},
	}
}

func setRaceHook(t *testing.T, fn func(path string)) {
	t.Helper()
	raceHook = fn
	t.Cleanup(func() { raceHook = nil })
}

// readWithin runs readAgentFile and fails the test if it has not returned in d. A
// reader stuck in open(2) on a FIFO is released first.
func readWithin(t *testing.T, d time.Duration, path string, rules fileRules, fifo string) ([]byte, string, error) {
	t.Helper()
	type result struct {
		data []byte
		skip string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, skip, err := readAgentFile(path, rules)
		done <- result{data, skip, err}
	}()
	select {
	case r := <-done:
		return r.data, r.skip, r.err
	case <-time.After(d):
		releaseFIFOForTest(fifo)
		t.Fatalf("readAgentFile did not return within %s: it is blocked on a FIFO", d)
		return nil, "", nil
	}
}

// The link is retargeted to an outside file after the inspection. The file read is
// the one inspected, through the path the inspection resolved, so the outside
// file is never read.
func TestReadAgentFile_ARetargetedLinkDoesNotLeakTheOutsideFile(t *testing.T) {
	f := newRaceFixture(t)
	writeFileT(t, filepath.Join(f.agents, "in.md"), "IN-MARKER\n")
	link := filepath.Join(f.agents, "x.md")
	symlinkOrSkip(t, "in.md", link)
	setRaceHook(t, func(string) {
		_ = os.Remove(link)
		_ = os.Symlink(f.secret, link)
	})

	data, skip, err := readAgentFile(link, f.rules)
	if err != nil {
		t.Fatalf("readAgentFile = %v", err)
	}
	if strings.Contains(string(data), "TOPSECRET") {
		t.Fatalf("the outside file was read through the retargeted link: %q", data)
	}
	if skip != "" || !strings.Contains(string(data), "IN-MARKER") {
		t.Errorf("data = %q, skip = %q; want the inspected file's text", data, skip)
	}
}

// A directory on the way to the file is replaced by a link to an outside
// directory holding a file of the same name. The path now leads outside, whatever
// the last component is, so only the identity of what was opened can tell.
func TestReadAgentFile_ADirectorySwappedForALinkIsNotFollowed(t *testing.T) {
	f := newRaceFixture(t)
	real := filepath.Join(f.agents, "real")
	writeFileT(t, filepath.Join(real, "inner.md"), "IN-MARKER\n")
	link := filepath.Join(f.agents, "x.md")
	symlinkOrSkip(t, filepath.Join("real", "inner.md"), link)
	outsideDir := t.TempDir()
	writeFileT(t, filepath.Join(outsideDir, "inner.md"), secretText+"\n")
	setRaceHook(t, func(string) {
		_ = os.Rename(real, real+".orig")
		_ = os.Symlink(outsideDir, real)
	})

	data, skip, err := readAgentFile(link, f.rules)
	if err != nil {
		t.Fatalf("readAgentFile = %v", err)
	}
	if strings.Contains(string(data), "TOPSECRET") {
		t.Fatalf("the outside file was read through the swapped directory: %q", data)
	}
	if skip != ProblemChanged.warning("") {
		t.Errorf("skip = %q, want %q", skip, ProblemChanged.warning(""))
	}
}

// A regular file is swapped for a FIFO after the inspection. Opening it for
// reading would wait for a writer that never comes.
func TestReadAgentFile_AFileSwappedForAFIFOIsSkippedWithoutBlocking(t *testing.T) {
	f := newRaceFixture(t)
	path := filepath.Join(f.agents, "x.md")
	writeFileT(t, path, "IN-MARKER\n")
	setRaceHook(t, func(string) {
		_ = os.Remove(path)
		_ = syscall.Mkfifo(path, 0o600)
	})

	data, skip, err := readWithin(t, 5*time.Second, path, f.rules, path)
	if err != nil {
		t.Fatalf("readAgentFile = %v", err)
	}
	if len(data) != 0 || skip != ProblemChanged.warning("") {
		t.Errorf("data = %q, skip = %q, want a skip %q", data, skip, ProblemChanged.warning(""))
	}
}

// A regular file is swapped for a link to an outside file after the inspection.
func TestReadAgentFile_AFileSwappedForALinkIsNotFollowed(t *testing.T) {
	f := newRaceFixture(t)
	path := filepath.Join(f.agents, "x.md")
	writeFileT(t, path, "IN-MARKER\n")
	setRaceHook(t, func(string) {
		_ = os.Remove(path)
		_ = os.Symlink(f.secret, path)
	})

	data, skip, err := readAgentFile(path, f.rules)
	if err != nil {
		t.Fatalf("readAgentFile = %v", err)
	}
	if strings.Contains(string(data), "TOPSECRET") {
		t.Fatalf("the outside file was read through a link swapped in for the file: %q", data)
	}
	if skip != ProblemChanged.warning("") {
		t.Errorf("skip = %q, want %q", skip, ProblemChanged.warning(""))
	}
}

// The file is swapped for a link that leads back to the very file that was
// inspected, through another name. The identity check would pass, so only the open
// itself, which refuses a link in the last component, tells: no link is followed
// at that moment, wherever it leads.
func TestReadAgentFile_AFileSwappedForALinkBackToItselfIsNotFollowed(t *testing.T) {
	f := newRaceFixture(t)
	path := filepath.Join(f.agents, "x.md")
	writeFileT(t, path, "IN-MARKER\n")
	alias := filepath.Join(f.agents, "alias.md")
	setRaceHook(t, func(string) {
		_ = os.Link(path, alias)
		_ = os.Remove(path)
		_ = os.Symlink(alias, path)
	})

	data, skip, err := readAgentFile(path, f.rules)
	if err != nil {
		t.Fatalf("readAgentFile = %v", err)
	}
	if len(data) != 0 || skip != ProblemChanged.warning("") {
		t.Errorf("data = %q, skip = %q, want a skip %q: no link is followed at the open", data, skip, ProblemChanged.warning(""))
	}
}

// A file replaced by another regular file is not the file that was inspected.
func TestReadAgentFile_AFileReplacedByAnotherIsSkipped(t *testing.T) {
	f := newRaceFixture(t)
	path := filepath.Join(f.agents, "x.md")
	writeFileT(t, path, "IN-MARKER\n")
	other := filepath.Join(f.agents, "other.md")
	writeFileT(t, other, "OTHER-MARKER\n")
	setRaceHook(t, func(string) { _ = os.Rename(other, path) })

	data, skip, err := readAgentFile(path, f.rules)
	if err != nil {
		t.Fatalf("readAgentFile = %v", err)
	}
	if len(data) != 0 || skip != ProblemChanged.warning("") {
		t.Errorf("data = %q, skip = %q, want a skip %q", data, skip, ProblemChanged.warning(""))
	}
}

// The swaps nobody scheduled: a link flipped between an inside file and an
// outside one as fast as it can while the reader runs. It cannot fail the fixed
// code, so it only ever tells if a swap is missed that the hook tests do not name.
func TestReadAgentFile_ALinkFlippedConcurrentlyNeverLeaksTheOutsideFile(t *testing.T) {
	f := newRaceFixture(t)
	writeFileT(t, filepath.Join(f.agents, "in.md"), "IN-MARKER\n")
	link := filepath.Join(f.agents, "x.md")
	symlinkOrSkip(t, "in.md", link)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tmp := filepath.Join(f.agents, "flip.tmp")
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			target := "in.md"
			if i%2 == 1 {
				target = f.secret
			}
			_ = os.Remove(tmp)
			if os.Symlink(target, tmp) == nil {
				_ = os.Rename(tmp, link)
			}
		}
	}()
	defer func() { close(stop); wg.Wait() }()

	for i := 0; i < 400; i++ {
		data, _, _ := readAgentFile(link, f.rules)
		if strings.Contains(string(data), "TOPSECRET") {
			t.Fatalf("read %d returned the outside file: %q", i, data)
		}
	}
}

// A file that grows past the cap after the inspection. The inspection saw a few
// bytes, so only the bounded read and the check after it can tell, and the file
// is the one inspected, so the identity check does not.
func TestReadAgentFile_AFileThatGrowsPastTheCapAfterTheInspectionIsSkipped(t *testing.T) {
	f := newRaceFixture(t)
	path := filepath.Join(f.agents, "x.md")
	writeFileT(t, path, "IN-MARKER\n")
	setRaceHook(t, func(string) {
		grow, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return
		}
		defer func() { _ = grow.Close() }()
		_, _ = grow.Write([]byte(strings.Repeat("x", MaxAgentFileBytes)))
	})

	data, skip, err := readAgentFile(path, f.rules)
	if err != nil {
		t.Fatalf("readAgentFile = %v", err)
	}
	if len(data) != 0 || skip != ProblemTooLarge.warning("") {
		t.Errorf("data = %d bytes, skip = %q, want a skip %q", len(data), skip, ProblemTooLarge.warning(""))
	}
}
