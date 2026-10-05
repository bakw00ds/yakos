//go:build !windows

package agentscompose

// compose_extends_unix_test.go — an `extends:` that reaches a FIFO must not open
// it, whether the value is rejected (it names a path) or the template is one.

import (
	"path/filepath"
	"testing"
	"time"
)

// The value names a FIFO by a path. It is rejected on its shape, before any file
// is touched, so Compose returns at once and the pipe is never opened.
func TestCompose_ABadExtendsValueNamingAFIFONeverOpensIt(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	fifo := filepath.Join(root, "lib", "pipe.md")
	mkfifoOrSkip(t, fifo)
	bad := filepath.Join(agents, "bad.md")
	writeFileT(t, bad, "---\nid: bad\nextends: ../pipe\n---\n\n## Purpose\n\nBad.\n")

	roster, err := composeWithin(t, 5*time.Second, root, project, fifo)
	if err != nil {
		t.Fatalf("Compose = %v", err)
	}
	if got := rosterString(roster); got != "backend,helper" {
		t.Errorf("roster = %q, want backend,helper", got)
	}
	requireWarning(t, warnings, bad, `extends value "../pipe" is not a bare agent id`)
}

// The value is a bare id but the template is a FIFO, or a link to one.
func TestCompose_ATemplateThatIsAFIFOIsNeverOpened(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	fifo := filepath.Join(root, "lib", "agents", "pipe.md")
	mkfifoOrSkip(t, fifo)
	other := filepath.Join(root, "pipe2")
	mkfifoOrSkip(t, other)
	symlinkOrSkip(t, other, filepath.Join(root, "lib", "agents", "pipelink.md"))
	direct := filepath.Join(agents, "direct.md")
	writeFileT(t, direct, "---\nid: direct\nextends: pipe\n---\n\n## Purpose\n\nD.\n")
	linked := filepath.Join(agents, "linked.md")
	writeFileT(t, linked, "---\nid: linked\nextends: pipelink\n---\n\n## Purpose\n\nL.\n")

	roster, err := composeWithin(t, 5*time.Second, root, project, fifo, other)
	if err != nil {
		t.Fatalf("Compose = %v", err)
	}
	if got := rosterString(roster); got != "backend,helper" {
		t.Errorf("roster = %q, want backend,helper", got)
	}
	requireLine(t, warnings, direct, `extends "pipe": not a regular file`)
	requireLine(t, warnings, linked, `extends "pipelink": symlink does not resolve to a regular file`)
}
