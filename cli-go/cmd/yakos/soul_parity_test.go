// soul_parity_test.go — Phase 1 parity tests for `yakos soul`.
//
// Design notes:
//
//  1. All tests call soul.Run directly with temp-dir paths; no bash runtime
//     or real ~/.yakos-state/soul directory is required.
//
//  2. The bash soul.sh manages operator-personal soul files: global layer
//     (~/.yakos-state/soul/global.md) and per-project layer
//     (~/.yakos-state/soul/<slug>.md). Subcommands: show, edit, history,
//     revert, pending, approve, reject.
//
//  3. Parity is verified behaviourally (output shape, error conditions,
//     filesystem effects) rather than byte-for-byte because timestamps and
//     temp paths differ per test run.
//
// Critical scenarios:
//
//	(a) show global — file exists → content printed
//	(b) show global — file absent → advisory message
//	(c) show project — resolves slug → correct content
//	(d) show unknown layer → error
//	(e) edit — seeds file from bare default when absent
//	(f) edit — snapshots existing file before editing
//	(g) edit — EditorFn called with correct path
//	(h) history — no snapshots → "(no history)"
//	(i) history — lists timestamps and byte counts
//	(j) revert — missing version → error
//	(k) revert — happy path; file updated; pre-revert snapshot created
//	(l) pending — no project slug → error
//	(m) pending — no pending file → advisory
//	(n) pending — lists ## edit: headings
//	(o) approve — not yet implemented message
//	(p) reject  — not yet implemented message
//	(q) unknown subcommand → error
//	(r) help text contains load-bearing phrases
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/soul"
)

// ---- helpers ----------------------------------------------------------------

var soulFixedNow = time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

func newSoulConfig(homeDir, subcommand string, args []string) soul.Config {
	return soul.Config{
		Subcommand: subcommand,
		Args:       args,
		HomeDir:    homeDir,
		Now:        soulFixedNow,
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
}

func soulOut(cfg soul.Config) string {
	return cfg.Writer.(*bytes.Buffer).String()
}

func makeSoulParityFile(t *testing.T, homeDir, name, content string) string {
	t.Helper()
	dir := filepath.Join(homeDir, ".yakos-state", "soul")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir soul: %v", err)
	}
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write soul: %v", err)
	}
	return path
}

func makeSoulPendingEdits(t *testing.T, homeDir, slug, content string) {
	t.Helper()
	dir := filepath.Join(homeDir, "agent-control", slug, "work", "current")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir work/current: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "soul-proposed-edits.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write pending: %v", err)
	}
}

// ---- scenario (a): show global, file exists ---------------------------------

func TestSoulParity_Show_GlobalExists(t *testing.T) {
	home := t.TempDir()
	makeSoulParityFile(t, home, "global", "# Soul\n\nmy soul content\n")

	cfg := newSoulConfig(home, "show", nil)
	res, err := soul.Run(cfg)
	if err != nil {
		t.Fatalf("Run show: %v", err)
	}
	if res.Layer != "global" {
		t.Errorf("expected layer=global; got %q", res.Layer)
	}
	out := soulOut(cfg)
	if !strings.Contains(out, "my soul content") {
		t.Errorf("expected soul content; got: %q", out)
	}
}

// ---- scenario (b): show global, file absent ---------------------------------

func TestSoulParity_Show_GlobalAbsent(t *testing.T) {
	home := t.TempDir()
	cfg := newSoulConfig(home, "show", nil)
	res, err := soul.Run(cfg)
	if err != nil {
		t.Fatalf("Run show absent: %v", err)
	}
	if res.File != "" {
		t.Errorf("expected File='' for absent soul; got %q", res.File)
	}
	out := soulOut(cfg)
	if !strings.Contains(out, "no soul file") {
		t.Errorf("expected 'no soul file' advisory; got: %q", out)
	}
	if !strings.Contains(out, "soul edit") {
		t.Errorf("expected edit hint in output; got: %q", out)
	}
}

// ---- scenario (c): show project, resolves slug ------------------------------

func TestSoulParity_Show_ProjectLayer(t *testing.T) {
	home := t.TempDir()
	makeSoulParityFile(t, home, "myprojslug", "project voice\n")

	cfg := newSoulConfig(home, "show", []string{"project"})
	cfg.ProjectDir = filepath.Join(home, "agent-control", "myprojslug")
	res, err := soul.Run(cfg)
	if err != nil {
		t.Fatalf("Run show project: %v", err)
	}
	if res.Layer != "project" {
		t.Errorf("expected layer=project; got %q", res.Layer)
	}
	out := soulOut(cfg)
	if !strings.Contains(out, "project voice") {
		t.Errorf("expected project content; got: %q", out)
	}
}

// ---- scenario (d): show unknown layer → error -------------------------------

func TestSoulParity_Show_UnknownLayer_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newSoulConfig(home, "show", []string{"invalid"})
	_, err := soul.Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown layer")
	}
	if !strings.Contains(err.Error(), "unknown layer") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (e): edit seeds file when absent ------------------------------

func TestSoulParity_Edit_SeedsWhenAbsent(t *testing.T) {
	home := t.TempDir()
	soulDir := filepath.Join(home, ".yakos-state", "soul")

	cfg := newSoulConfig(home, "edit", nil)
	cfg.EditorFn = func(string) error { return nil }

	if _, err := soul.Run(cfg); err != nil {
		t.Fatalf("Run edit: %v", err)
	}

	globalPath := filepath.Join(soulDir, "global.md")
	if _, err := os.Stat(globalPath); err != nil {
		t.Fatalf("expected soul file to be seeded; stat: %v", err)
	}
	data, _ := os.ReadFile(globalPath)
	if !strings.Contains(string(data), "Soul") {
		t.Error("seeded content missing 'Soul'")
	}
}

// ---- scenario (f): edit snapshots existing file before edit -----------------

func TestSoulParity_Edit_SnapshotsExistingFile(t *testing.T) {
	home := t.TempDir()
	original := "# Soul\n\noriginal\n"
	makeSoulParityFile(t, home, "global", original)

	cfg := newSoulConfig(home, "edit", nil)
	cfg.EditorFn = func(string) error { return nil }

	res, err := soul.Run(cfg)
	if err != nil {
		t.Fatalf("Run edit: %v", err)
	}
	if res.SnapshotFile == "" {
		t.Fatal("expected snapshot to be created")
	}
	snapData, _ := os.ReadFile(res.SnapshotFile)
	if string(snapData) != original {
		t.Errorf("snapshot mismatch; got: %q", string(snapData))
	}
}

// ---- scenario (g): edit calls EditorFn with correct path --------------------

func TestSoulParity_Edit_EditorCalledWithCorrectPath(t *testing.T) {
	home := t.TempDir()
	editorPath := ""
	cfg := newSoulConfig(home, "edit", nil)
	cfg.EditorFn = func(path string) error {
		editorPath = path
		return nil
	}

	if _, err := soul.Run(cfg); err != nil {
		t.Fatalf("Run edit: %v", err)
	}

	expectedSuffix := filepath.Join(".yakos-state", "soul", "global.md")
	if !strings.HasSuffix(editorPath, expectedSuffix) {
		t.Errorf("editor called with wrong path; got %q, want suffix %q", editorPath, expectedSuffix)
	}
}

// ---- scenario (h): history — no snapshots → advisory -----------------------

func TestSoulParity_History_NoSnapshots(t *testing.T) {
	home := t.TempDir()
	cfg := newSoulConfig(home, "history", nil)
	if _, err := soul.Run(cfg); err != nil {
		t.Fatalf("Run history: %v", err)
	}
	out := soulOut(cfg)
	if !strings.Contains(out, "no history") {
		t.Errorf("expected 'no history'; got: %q", out)
	}
}

// ---- scenario (i): history — lists timestamps and byte counts ---------------

func TestSoulParity_History_ListsSnapshots(t *testing.T) {
	home := t.TempDir()
	histDir := filepath.Join(home, ".yakos-state", "soul", "history")
	if err := os.MkdirAll(histDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Use win-safe timestamp format (no colons, sortable).
	ts1 := "20260501T100000Z"
	ts2 := "20260601T140000Z"
	if err := os.WriteFile(filepath.Join(histDir, "global-"+ts1+".md"), []byte("older"), 0644); err != nil {
		t.Fatalf("write snap1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(histDir, "global-"+ts2+".md"), []byte("newer content"), 0644); err != nil {
		t.Fatalf("write snap2: %v", err)
	}

	cfg := newSoulConfig(home, "history", nil)
	if _, err := soul.Run(cfg); err != nil {
		t.Fatalf("Run history: %v", err)
	}
	out := soulOut(cfg)
	if !strings.Contains(out, ts1) {
		t.Errorf("missing ts1 %q; got: %q", ts1, out)
	}
	if !strings.Contains(out, ts2) {
		t.Errorf("missing ts2 %q; got: %q", ts2, out)
	}
	if !strings.Contains(out, "bytes") {
		t.Errorf("expected byte count; got: %q", out)
	}
	// Verify ordering: older before newer.
	if strings.Index(out, ts1) > strings.Index(out, ts2) {
		t.Error("older snapshot should appear before newer snapshot")
	}
}

// ---- scenario (j): revert — missing version → error ------------------------

func TestSoulParity_Revert_MissingVersion_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newSoulConfig(home, "revert", nil)
	_, err := soul.Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing version arg")
	}
	if !strings.Contains(err.Error(), "revert requires") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (k): revert happy path ----------------------------------------

func TestSoulParity_Revert_HappyPath(t *testing.T) {
	home := t.TempDir()
	soulDir := filepath.Join(home, ".yakos-state", "soul")
	histDir := filepath.Join(soulDir, "history")
	if err := os.MkdirAll(histDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	current := "# Current\n\ncurrent content\n"
	makeSoulParityFile(t, home, "global", current)

	// Use win-safe timestamp format.
	snapVersion := "20260101T000000Z"
	oldContent := "# Old\n\nold content\n"
	if err := os.WriteFile(filepath.Join(histDir, "global-"+snapVersion+".md"), []byte(oldContent), 0644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	cfg := newSoulConfig(home, "revert", []string{snapVersion})
	res, err := soul.Run(cfg)
	if err != nil {
		t.Fatalf("Run revert: %v", err)
	}

	// Soul file should now have old content.
	data, _ := os.ReadFile(filepath.Join(soulDir, "global.md"))
	if string(data) != oldContent {
		t.Errorf("expected old content after revert; got: %q", string(data))
	}
	// Pre-revert snapshot created.
	if res.SnapshotFile == "" {
		t.Error("expected pre-revert snapshot")
	}
	preRevert, _ := os.ReadFile(res.SnapshotFile)
	if string(preRevert) != current {
		t.Error("pre-revert snapshot should contain the previous current content")
	}
	// Output confirms.
	out := soulOut(cfg)
	if !strings.Contains(out, "reverted") {
		t.Errorf("expected 'reverted' in output; got: %q", out)
	}
}

// ---- scenario (l): pending — no project slug → error -----------------------

func TestSoulParity_Pending_NoSlug_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newSoulConfig(home, "pending", nil)
	// No ProjectDir, no YAKOS_PROJECT_DIR, no agent-control entries.
	_, err := soul.Run(cfg)
	if err == nil {
		t.Fatal("expected error when no project slug detectable")
	}
	if !strings.Contains(err.Error(), "no active project session") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (m): pending — no pending file → advisory --------------------

func TestSoulParity_Pending_NoPendingFile(t *testing.T) {
	home := t.TempDir()
	cfg := newSoulConfig(home, "pending", nil)
	cfg.ProjectDir = filepath.Join(home, "agent-control", "myslug")
	if _, err := soul.Run(cfg); err != nil {
		t.Fatalf("Run pending: %v", err)
	}
	out := soulOut(cfg)
	if !strings.Contains(out, "no pending") {
		t.Errorf("expected 'no pending' advisory; got: %q", out)
	}
}

// ---- scenario (n): pending — lists ## edit: headings -----------------------

func TestSoulParity_Pending_ListsEdits(t *testing.T) {
	home := t.TempDir()
	content := "# Proposals\n\n## edit: add-hipaa-framing\n\nbody1\n\n## edit: terse-voice\n\nbody2\n"
	makeSoulPendingEdits(t, home, "myslug", content)

	cfg := newSoulConfig(home, "pending", nil)
	cfg.ProjectDir = filepath.Join(home, "agent-control", "myslug")
	res, err := soul.Run(cfg)
	if err != nil {
		t.Fatalf("Run pending: %v", err)
	}
	if len(res.PendingEdits) != 2 {
		t.Errorf("expected 2 pending edits; got %d", len(res.PendingEdits))
	}
	out := soulOut(cfg)
	if !strings.Contains(out, "add-hipaa-framing") {
		t.Errorf("missing first edit slug; got: %q", out)
	}
	if !strings.Contains(out, "terse-voice") {
		t.Errorf("missing second edit slug; got: %q", out)
	}
}

// ---- scenario (o): approve — not yet implemented ---------------------------

func TestSoulParity_Approve_NotYetImplemented(t *testing.T) {
	home := t.TempDir()
	cfg := newSoulConfig(home, "approve", []string{"some-slug"})
	res, err := soul.Run(cfg)
	if err != nil {
		t.Fatalf("Run approve: %v", err)
	}
	if res.Subcommand != "approve" {
		t.Errorf("expected subcommand=approve; got %q", res.Subcommand)
	}
	out := soulOut(cfg)
	if !strings.Contains(out, "not yet implemented") {
		t.Errorf("expected 'not yet implemented' message; got: %q", out)
	}
}

// ---- scenario (p): reject — not yet implemented ----------------------------

func TestSoulParity_Reject_NotYetImplemented(t *testing.T) {
	home := t.TempDir()
	cfg := newSoulConfig(home, "reject", []string{"some-slug"})
	if _, err := soul.Run(cfg); err != nil {
		t.Fatalf("Run reject: %v", err)
	}
	out := soulOut(cfg)
	if !strings.Contains(out, "not yet implemented") {
		t.Errorf("expected 'not yet implemented'; got: %q", out)
	}
}

// ---- scenario (q): unknown subcommand → error -------------------------------

func TestSoulParity_UnknownSubcommand_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newSoulConfig(home, "bogus", nil)
	_, err := soul.Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (r): help text contains key phrases --------------------------

func TestSoulParity_HelpText_KeyPhrases(t *testing.T) {
	var buf bytes.Buffer
	soul.PrintHelp(&buf)
	help := buf.String()

	phrases := []string{
		"yakos soul",
		"show",
		"edit",
		"history",
		"revert",
		"pending",
		"approve",
		"reject",
		"global",
		"project",
		"~/.yakos-state/soul",
	}
	for _, p := range phrases {
		if !strings.Contains(help, p) {
			t.Errorf("help text missing phrase %q", p)
		}
	}
}
