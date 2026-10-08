//go:build unix

package agentscompose

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A FIFO is refused by both readers without being opened for good.
func TestReadAgentFileIn_AndReadRegularFile_FIFORefusedWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe.md")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no mkfifo: %v", err)
	}
	for name, read := range map[string]func() ([]byte, error){
		"ReadAgentFileIn": func() ([]byte, error) { return ReadAgentFileIn(fifo, []string{dir}) },
		"ReadRegularFile": func() ([]byte, error) { return ReadRegularFile(fifo) },
	} {
		done := make(chan error, 1)
		go func() { _, err := read(); done <- err }()
		select {
		case err := <-done:
			if !errors.Is(err, ErrRefused) {
				t.Errorf("%s: %v, want ErrRefused", name, err)
			}
		case <-time.After(5 * time.Second):
			releaseFIFOForTest(fifo)
			t.Fatalf("%s blocked on a FIFO", name)
		}
	}
}

// ReadRegularFile reads the file it inspected: a link retargeted after the check
// to a FIFO is skipped, and a regular file swapped for a FIFO does not block.
func TestReadRegularFile_SwapAfterInspectionIsNotRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.md")
	writeFileT(t, path, "IN-MARKER\n")
	setRaceHook(t, func(string) {
		_ = os.Remove(path)
		_ = syscall.Mkfifo(path, 0o600)
	})
	type res struct {
		data []byte
		err  error
	}
	done := make(chan res, 1)
	go func() { d, err := ReadRegularFile(path); done <- res{d, err} }()
	select {
	case r := <-done:
		if !errors.Is(r.err, ErrRefused) || len(r.data) != 0 {
			t.Errorf("data %q err %v, want ErrRefused", r.data, r.err)
		}
	case <-time.After(5 * time.Second):
		releaseFIFOForTest(path)
		t.Fatal("ReadRegularFile blocked on a FIFO swapped in after the check")
	}
}

// A link retargeted to a file outside the roots after the check is never read by
// ReadAgentFileIn: it opens the path the inspection resolved.
func TestReadAgentFileIn_RetargetedLinkDoesNotLeak(t *testing.T) {
	agents := t.TempDir()
	secret := filepath.Join(t.TempDir(), "credentials")
	writeFileT(t, secret, secretText+"\n")
	writeFileT(t, filepath.Join(agents, "in.md"), "IN-MARKER\n")
	link := filepath.Join(agents, "x.md")
	symlinkOrSkip(t, "in.md", link)
	setRaceHook(t, func(string) {
		_ = os.Remove(link)
		_ = os.Symlink(secret, link)
	})
	data, err := ReadAgentFileIn(link, []string{agents})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "TOPSECRET") {
		t.Fatalf("the outside file was read: %q", data)
	}
}
