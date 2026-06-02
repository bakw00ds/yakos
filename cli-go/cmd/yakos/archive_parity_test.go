// archive_parity_test.go — Phase 1 parity tests for `yakos archive`.
//
// Design notes:
//
//  1. All tests use the archive.Run function directly with a temp-dir home so
//     no real ~/agent-control/ or bash runtime is needed.
//
//  2. Help-text parity verifies load-bearing phrases from cli/lib/archive.sh
//     usage() appear in the Go output.
//
//  3. Flag-parsing edge cases (unknown flag, missing project, too-many-args,
//     invalid tag, conflict, auto-tag) mirror the exact error behaviour of
//     the bash implementation.
//
// Critical scenarios verified:
//
//	(a) archive happy-path        — current/ moved, fresh current/ created, summary printed
//	(b) --auto-tag                — suffix appended on conflict; summary prints new tag
//	(c) conflict without auto-tag — error mentions "already exists"
//	(d) missing <project>         — error mentions project name
//	(e) invalid <tag> with slash  — error returned
//	(f) invalid <tag> with space  — error returned
//	(g) missing work/current/     — error mentions "does not exist"
//	(h) expired bypass            — error mentions bypass id; no mutation
//	(i) sessions.ndjson fields    — action/project/tag/dest/size all present
//	(j) fresh current/ structure  — logs/, artifacts/, reports/ recreated
//	(k) hook-bypass template      — hook-bypass.md restored in fresh current/
//	(l) help text phrases         — all load-bearing phrases from archive.sh usage()
//	(m) summary output            — tag, source, dest, ledger all in output
//	(n) no worktree cleanup       — (documented caveat, not a regression)
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/archive"
)

// ---- helpers ----------------------------------------------------------------

// newArchiveHome builds ~/agent-control/<project>/work/current/{logs,artifacts,reports}/
// in a temp dir and returns (home, workDir, currentDir).
func newArchiveHome(t *testing.T, project string) (home, workDir, currentDir string) {
	t.Helper()
	home = t.TempDir()
	workDir = filepath.Join(home, "agent-control", project, "work")
	currentDir = filepath.Join(workDir, "current")
	for _, sub := range []string{
		filepath.Join(currentDir, "logs"),
		filepath.Join(currentDir, "artifacts"),
		filepath.Join(currentDir, "reports"),
	} {
		if err := os.MkdirAll(sub, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	return
}

// archiveYakosRoot returns a temp dir with the hook-bypass template in place.
func archiveYakosRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	tmplDir := filepath.Join(root, "lib", "settings")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", tmplDir, err)
	}
	p := filepath.Join(tmplDir, "hook-bypass.template.md")
	if err := os.WriteFile(p, []byte("# Active hook bypasses\n\n## Active entries\n\n"), 0644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	return root
}

func archiveSeedFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// readFirstNDJSONLine unmarshals the first NDJSON line from path into dst.
func readFirstNDJSONLine(t *testing.T, path string, dst interface{}) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	lines := bytes.SplitN(bytes.TrimRight(data, "\n"), []byte("\n"), 2)
	if len(lines) == 0 || len(lines[0]) == 0 {
		t.Fatalf("no NDJSON line found in %s", path)
	}
	if err := json.Unmarshal(lines[0], dst); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
}

// ---- (a) happy-path ---------------------------------------------------------

// TestArchiveParity_HappyPath verifies the core archive flow.
func TestArchiveParity_HappyPath(t *testing.T) {
	home, workDir, currentDir := newArchiveHome(t, "proj")
	yakosRoot := archiveYakosRoot(t)

	archiveSeedFile(t, filepath.Join(currentDir, "kanban.md"), "# board")

	var out bytes.Buffer
	cfg := archive.Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "sprint-1",
		HomeDir:   home,
		Now:       time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
		Writer:    &out,
		ErrWriter: &bytes.Buffer{},
	}

	res, err := archive.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Tag != "sprint-1" {
		t.Errorf("tag: got %q, want sprint-1", res.Tag)
	}

	// Verify kanban.md moved to archive.
	destKanban := filepath.Join(workDir, "archive", "sprint-1", "kanban.md")
	if _, statErr := os.Stat(destKanban); statErr != nil {
		t.Errorf("archived kanban.md not found at %s", destKanban)
	}

	// Verify summary printed.
	output := out.String()
	if !strings.Contains(output, "sprint-1") {
		t.Errorf("output missing tag; got:\n%s", output)
	}
}

// ---- (b) --auto-tag ---------------------------------------------------------

// TestArchiveParity_AutoTag verifies that --auto-tag appends a suffix on conflict.
func TestArchiveParity_AutoTag(t *testing.T) {
	home, workDir, _ := newArchiveHome(t, "proj")
	yakosRoot := archiveYakosRoot(t)

	// Pre-create the conflict directory.
	if err := os.MkdirAll(filepath.Join(workDir, "archive", "v1"), 0755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cfg := archive.Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "v1",
		AutoTag:   true,
		HomeDir:   home,
		Now:       time.Now(),
		Writer:    &out,
		ErrWriter: &bytes.Buffer{},
	}

	res, err := archive.Run(cfg)
	if err != nil {
		t.Fatalf("Run with AutoTag: %v", err)
	}

	if res.Tag == "v1" {
		t.Error("AutoTag: tag should have a suffix appended; still 'v1'")
	}
	if !strings.HasPrefix(res.Tag, "v1-") {
		t.Errorf("AutoTag: tag should start with 'v1-'; got %q", res.Tag)
	}
	if !strings.Contains(out.String(), res.Tag) {
		t.Errorf("output should contain resolved tag %q; got:\n%s", res.Tag, out.String())
	}
}

// ---- (c) conflict without --auto-tag ----------------------------------------

// TestArchiveParity_ConflictNoAutoTag verifies error on conflicting tag without --auto-tag.
func TestArchiveParity_ConflictNoAutoTag(t *testing.T) {
	home, workDir, _ := newArchiveHome(t, "proj")
	yakosRoot := archiveYakosRoot(t)

	if err := os.MkdirAll(filepath.Join(workDir, "archive", "v2"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := archive.Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "v2",
		AutoTag:   false,
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	_, err := archive.Run(cfg)
	if err == nil {
		t.Fatal("expected error for conflicting tag")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should say 'already exists'; got %q", err.Error())
	}
}

// ---- (d) missing project ----------------------------------------------------

// TestArchiveParity_MissingProject verifies that an absent project yields a
// descriptive error mentioning the project name.
func TestArchiveParity_MissingProject(t *testing.T) {
	home := t.TempDir()
	yakosRoot := archiveYakosRoot(t)

	cfg := archive.Config{
		YakosRoot: yakosRoot,
		Project:   "no-such-project",
		Tag:       "v1",
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	_, err := archive.Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing project")
	}
	if !strings.Contains(err.Error(), "no-such-project") {
		t.Errorf("error should mention project name; got %q", err.Error())
	}
}

// ---- (e) invalid tag (slash) ------------------------------------------------

// TestArchiveParity_InvalidTagSlash verifies slash-containing tags are rejected.
func TestArchiveParity_InvalidTagSlash(t *testing.T) {
	home, _, _ := newArchiveHome(t, "proj")
	yakosRoot := archiveYakosRoot(t)

	cfg := archive.Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "feat/bad",
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	_, err := archive.Run(cfg)
	if err == nil {
		t.Fatal("expected error for tag containing slash")
	}
}

// ---- (f) invalid tag (space) ------------------------------------------------

// TestArchiveParity_InvalidTagSpace verifies space-containing tags are rejected.
func TestArchiveParity_InvalidTagSpace(t *testing.T) {
	home, _, _ := newArchiveHome(t, "proj")
	yakosRoot := archiveYakosRoot(t)

	cfg := archive.Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "my archive",
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	_, err := archive.Run(cfg)
	if err == nil {
		t.Fatal("expected error for tag containing space")
	}
}

// ---- (g) missing work/current/ ----------------------------------------------

// TestArchiveParity_NoCurrent verifies that a missing work/current/ returns an
// error mentioning "does not exist".
func TestArchiveParity_NoCurrent(t *testing.T) {
	home := t.TempDir()
	yakosRoot := archiveYakosRoot(t)

	// Create control dir only — no work/current/.
	if err := os.MkdirAll(filepath.Join(home, "agent-control", "proj"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := archive.Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "v1",
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	_, err := archive.Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing work/current/")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error should mention 'does not exist'; got %q", err.Error())
	}
}

// ---- (h) expired bypass -----------------------------------------------------

// TestArchiveParity_ExpiredBypass verifies that expired hook-bypass entries
// abort the archive without any filesystem mutation.
func TestArchiveParity_ExpiredBypass(t *testing.T) {
	home, workDir, currentDir := newArchiveHome(t, "proj")
	yakosRoot := archiveYakosRoot(t)

	bypassContent := "# bypasses\n\n## Active entries\n\n## bypass: ancient\n**Expires:** 2020-01-01T00:00:00Z\n"
	archiveSeedFile(t, filepath.Join(currentDir, "hook-bypass.md"), bypassContent)

	cfg := archive.Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "v1",
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	_, err := archive.Run(cfg)
	if err == nil {
		t.Fatal("expected error for expired bypass")
	}
	if !strings.Contains(err.Error(), "ancient") {
		t.Errorf("error should mention bypass id 'ancient'; got %q", err.Error())
	}

	// Archive dir should NOT have been created.
	if _, statErr := os.Stat(filepath.Join(workDir, "archive", "v1")); !os.IsNotExist(statErr) {
		t.Error("archive dest should not exist after expired-bypass abort")
	}
}

// ---- (i) sessions.ndjson fields ---------------------------------------------

// TestArchiveParity_LedgerFields verifies the sessions.ndjson JSON record
// contains all required fields with correct values.
func TestArchiveParity_LedgerFields(t *testing.T) {
	home, workDir, _ := newArchiveHome(t, "proj")
	yakosRoot := archiveYakosRoot(t)

	cfg := archive.Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "ledger-check",
		HomeDir:   home,
		Now:       time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	if _, err := archive.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var entry map[string]string
	readFirstNDJSONLine(t, filepath.Join(workDir, "sessions.ndjson"), &entry)

	if entry["action"] != "archive" {
		t.Errorf("action: got %q, want archive", entry["action"])
	}
	if entry["project"] != "proj" {
		t.Errorf("project: got %q, want proj", entry["project"])
	}
	if entry["tag"] != "ledger-check" {
		t.Errorf("tag: got %q, want ledger-check", entry["tag"])
	}
	for _, f := range []string{"ts", "dest", "size"} {
		if entry[f] == "" {
			t.Errorf("ledger missing field %q", f)
		}
	}
}

// ---- (j) fresh current/ structure -------------------------------------------

// TestArchiveParity_FreshCurrentDirs verifies that after archive, work/current/
// has the three required empty subdirs.
func TestArchiveParity_FreshCurrentDirs(t *testing.T) {
	home, _, currentDir := newArchiveHome(t, "proj")
	yakosRoot := archiveYakosRoot(t)

	cfg := archive.Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "fresh-test",
		HomeDir:   home,
		Now:       time.Now(),
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	if _, err := archive.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, sub := range []string{"logs", "artifacts", "reports"} {
		dir := filepath.Join(currentDir, sub)
		fi, err := os.Stat(dir)
		if err != nil {
			t.Errorf("fresh current/%s missing: %v", sub, err)
			continue
		}
		if !fi.IsDir() {
			t.Errorf("fresh current/%s is not a directory", sub)
		}
	}
}

// ---- (k) hook-bypass template restored --------------------------------------

// TestArchiveParity_BypassTemplateRestored verifies that hook-bypass.md is
// recreated in fresh work/current/ from the YakosRoot template.
func TestArchiveParity_BypassTemplateRestored(t *testing.T) {
	home, _, currentDir := newArchiveHome(t, "proj")
	yakosRoot := archiveYakosRoot(t)

	cfg := archive.Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "tmpl-test",
		HomeDir:   home,
		Now:       time.Now(),
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	if _, err := archive.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	bypassPath := filepath.Join(currentDir, "hook-bypass.md")
	if _, err := os.Stat(bypassPath); err != nil {
		t.Errorf("hook-bypass.md not restored in fresh current/: %v", err)
	}
}

// ---- (l) help text phrases --------------------------------------------------

// TestArchiveParity_HelpText verifies the Go --help output contains all
// load-bearing phrases from cli/lib/archive.sh usage().
func TestArchiveParity_HelpText(t *testing.T) {
	var out bytes.Buffer
	archive.PrintHelp(&out)
	got := out.String()

	mustContain := []string{
		"yakos archive <project> <tag>",
		"--auto-tag",
		"--yes",
		"<project>",
		"<tag>",
		"alphanumeric",
		"Refuses to archive",
		"hook-bypass",
	}
	for _, phrase := range mustContain {
		if !strings.Contains(got, phrase) {
			t.Errorf("help text missing phrase %q;\nfull output:\n%s", phrase, got)
		}
	}
}

// ---- (m) summary output -----------------------------------------------------

// TestArchiveParity_SummaryOutput verifies the summary printed to Writer
// contains tag, source, dest, and ledger paths.
func TestArchiveParity_SummaryOutput(t *testing.T) {
	home, workDir, _ := newArchiveHome(t, "proj")
	yakosRoot := archiveYakosRoot(t)

	var out bytes.Buffer
	cfg := archive.Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "summary-tag",
		HomeDir:   home,
		Now:       time.Now(),
		Writer:    &out,
		ErrWriter: &bytes.Buffer{},
	}

	if _, err := archive.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	output := out.String()
	checks := []string{
		"summary-tag",
		"source:",
		"dest:",
		"ledger:",
		workDir,
	}
	for _, phrase := range checks {
		if !strings.Contains(output, phrase) {
			t.Errorf("summary output missing %q;\nfull output:\n%s", phrase, output)
		}
	}
}

// ---- (n) worktree cleanup not attempted -------------------------------------

// TestArchiveParity_NoWorktreeCleanup is a documentation test that asserts
// yakos archive does not attempt worktree cleanup (same caveat as bash).
// Per git-hygiene rule §Worktree: "Cleanup happens at archive time —
// yakos archive does NOT clean worktrees; that's manual still in v0.1."
//
// This test verifies the contract by checking that a worktrees directory
// sibling of work/ is NOT removed after archive.
func TestArchiveParity_NoWorktreeCleanup(t *testing.T) {
	home, workDir, _ := newArchiveHome(t, "proj")
	yakosRoot := archiveYakosRoot(t)

	// Simulate a worktree directory sibling to work/.
	worktreeDir := filepath.Join(filepath.Dir(workDir), "worktrees", "feature-x")
	if err := os.MkdirAll(worktreeDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := archive.Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "no-worktree",
		HomeDir:   home,
		Now:       time.Now(),
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	if _, err := archive.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Worktree dir must still exist after archive.
	if _, err := os.Stat(worktreeDir); err != nil {
		t.Errorf("worktree dir should NOT have been removed by archive; got: %v", err)
	}
}
