// archive_test.go — unit tests for internal/archive.
//
// Coverage:
//
//	ValidTag*          (3)  — valid tags accepted, invalid tags rejected
//	CheckExpired*      (4)  — bypass checks: no file, clean, expired, unparseable
//	WorkDir*           (2)  — env-var path resolution
//	DiskUsage*         (3)  — formatSize unit/boundary checks
//	AppendNDJSON*      (2)  — creates file, appends correctly
//	CopyFile*          (2)  — happy path + missing-src error
//	Run_HappyPath      (1)  — full archive: dir moved, fresh current created, ledger written
//	Run_AutoTag        (1)  — conflict + auto-tag suffix
//	Run_ConflictNoAuto (1)  — conflict without --auto-tag → error
//	Run_ExpiredBypass  (1)  — expired bypass → error, no filesystem mutation
//	Run_MissingProject (1)  — project not found → error
//	Run_BadTag*        (2)  — invalid tag characters rejected
//	Run_NoCurrent      (1)  — missing work/current → error
//	Run_LedgerEntry    (1)  — sessions.ndjson receives correct JSON fields
//	ParseBypass*       (3)  — active entries section parsing edge cases
//
// Total: ≥ 15 unit tests (target per task brief).
package archive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- helpers ----------------------------------------------------------------

// newTestHome creates a temp home with ~/agent-control/<project>/work/current/
// and the required subdirs.  Returns (home, workDir, currentDir).
func newTestHome(t *testing.T, project string) (home, workDir, currentDir string) {
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
	return home, workDir, currentDir
}

// seedFile writes content into path (creates parent dirs as needed).
func seedFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newYakosRoot returns a temp dir that looks like a minimal YakosRoot with the
// hook-bypass template in place.
func newYakosRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	templateDir := filepath.Join(root, "lib", "settings")
	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", templateDir, err)
	}
	templatePath := filepath.Join(templateDir, "hook-bypass.template.md")
	if err := os.WriteFile(templatePath, []byte("# Active hook bypasses\n\n## Active entries\n\n"), 0644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	return root
}

// ---- ValidTag ---------------------------------------------------------------

func TestValidTag_AlphanumericAccepted(t *testing.T) {
	if !validTagRe.MatchString("v1-0-0") {
		t.Error("expected 'v1-0-0' to be a valid tag")
	}
}

func TestValidTag_DotsAndUnderscoreAccepted(t *testing.T) {
	if !validTagRe.MatchString("sprint_3.1") {
		t.Error("expected 'sprint_3.1' to be a valid tag")
	}
}

func TestValidTag_SlashRejected(t *testing.T) {
	if validTagRe.MatchString("feat/my-archive") {
		t.Error("expected 'feat/my-archive' to be rejected (contains '/')")
	}
}

func TestValidTag_SpaceRejected(t *testing.T) {
	if validTagRe.MatchString("my tag") {
		t.Error("expected 'my tag' to be rejected (contains space)")
	}
}

// ---- CheckExpiredBypasses ---------------------------------------------------

func TestCheckExpiredBypasses_NoFile(t *testing.T) {
	// File absent → should pass without error.
	if err := checkExpiredBypasses("/nonexistent/path/hook-bypass.md"); err != nil {
		t.Errorf("expected nil for missing file; got %v", err)
	}
}

func TestCheckExpiredBypasses_NoActiveEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hook-bypass.md")
	// File with no "## Active entries" section.
	content := "# Active hook bypasses\n\nNo entries yet.\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := checkExpiredBypasses(path); err != nil {
		t.Errorf("no active entries → expected nil; got %v", err)
	}
}

func TestCheckExpiredBypasses_ActiveButNotExpired(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hook-bypass.md")
	// Use a far-future expiry.
	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	content := "# Active hook bypasses\n\n## Active entries\n\n## bypass: my-bypass\n**Expires:** " + future + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := checkExpiredBypasses(path); err != nil {
		t.Errorf("non-expired bypass → expected nil; got %v", err)
	}
}

func TestCheckExpiredBypasses_ExpiredEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hook-bypass.md")
	past := "2020-01-01T00:00:00Z"
	content := "# Active hook bypasses\n\n## Active entries\n\n## bypass: stale-bypass\n**Expires:** " + past + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	err := checkExpiredBypasses(path)
	if err == nil {
		t.Fatal("expected error for expired bypass; got nil")
	}
	if !strings.Contains(err.Error(), "stale-bypass") {
		t.Errorf("error should mention the bypass ID; got %q", err.Error())
	}
}

func TestCheckExpiredBypasses_UnparseableExpiry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hook-bypass.md")
	content := "# Active hook bypasses\n\n## Active entries\n\n## bypass: bad-date\n**Expires:** not-a-date\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	err := checkExpiredBypasses(path)
	if err == nil {
		t.Fatal("expected error for unparseable expiry; got nil")
	}
	if !strings.Contains(err.Error(), "unparseable") {
		t.Errorf("error should mention unparseable; got %q", err.Error())
	}
}

// ---- WorkDir resolution -----------------------------------------------------

func TestWorkDirFor_Default(t *testing.T) {
	// Unset env vars → canonical path.
	t.Setenv("YAKOS_WORK_DIR", "")
	t.Setenv("YAKOS_INPLACE_WORK", "")
	got := workDirFor("myproj", "/home/user")
	want := "/home/user/agent-control/myproj/work"
	if got != want {
		t.Errorf("workDirFor: got %q, want %q", got, want)
	}
}

func TestWorkDirFor_YakosWorkDirEnv(t *testing.T) {
	t.Setenv("YAKOS_WORK_DIR", "/custom/work")
	got := workDirFor("myproj", "/home/user")
	if got != "/custom/work" {
		t.Errorf("workDirFor: got %q, want /custom/work", got)
	}
}

// ---- DiskUsage / formatSize -------------------------------------------------

func TestFormatSize_Bytes(t *testing.T) {
	got := formatSize(512)
	if got != "512B" {
		t.Errorf("formatSize(512): got %q, want 512B", got)
	}
}

func TestFormatSize_Kilobytes(t *testing.T) {
	got := formatSize(2048)
	if got != "2.0K" {
		t.Errorf("formatSize(2048): got %q, want 2.0K", got)
	}
}

func TestFormatSize_Megabytes(t *testing.T) {
	got := formatSize(3 * 1024 * 1024)
	if got != "3.0M" {
		t.Errorf("formatSize(3MiB): got %q, want 3.0M", got)
	}
}

// ---- AppendNDJSON -----------------------------------------------------------

func TestAppendNDJSON_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.ndjson")

	entry := map[string]string{"action": "archive", "project": "p", "tag": "t1"}
	if err := appendNDJSON(path, entry); err != nil {
		t.Fatalf("appendNDJSON: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(bytes.TrimRight(data, "\n"), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got["action"] != "archive" {
		t.Errorf("action: got %q, want archive", got["action"])
	}
}

func TestAppendNDJSON_AppendsTwoEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.ndjson")

	e1 := map[string]string{"n": "1"}
	e2 := map[string]string{"n": "2"}
	if err := appendNDJSON(path, e1); err != nil {
		t.Fatal(err)
	}
	if err := appendNDJSON(path, e2); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 NDJSON lines; got %d", len(lines))
	}
}

// ---- CopyFile ---------------------------------------------------------------

func TestCopyFile_HappyPath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "subdir", "dst.txt")
	if err := os.WriteFile(src, []byte("hello archive"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello archive" {
		t.Errorf("copyFile: content mismatch: got %q", got)
	}
}

func TestCopyFile_MissingSrc(t *testing.T) {
	dir := t.TempDir()
	err := copyFile(filepath.Join(dir, "nonexistent.txt"), filepath.Join(dir, "dst.txt"))
	if err == nil {
		t.Error("expected error for missing src; got nil")
	}
}

// ---- Run: full integration --------------------------------------------------

// TestRun_HappyPath verifies the complete archive flow:
// work/current/ is moved, fresh current/ subdirs are created, ledger is written.
func TestRun_HappyPath(t *testing.T) {
	home, workDir, currentDir := newTestHome(t, "myproj")
	yakosRoot := newYakosRoot(t)

	// Seed a file in current/ so we can verify it moved.
	seedFile(t, filepath.Join(currentDir, "kanban.md"), "# board")

	var out, errOut bytes.Buffer
	cfg := Config{
		YakosRoot: yakosRoot,
		Project:   "myproj",
		Tag:       "v1.0",
		HomeDir:   home,
		Now:       time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
		Writer:    &out,
		ErrWriter: &errOut,
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Tag returned correctly.
	if res.Tag != "v1.0" {
		t.Errorf("tag: got %q, want v1.0", res.Tag)
	}

	// Source file moved to archive dest.
	destFile := filepath.Join(workDir, "archive", "v1.0", "kanban.md")
	if _, err := os.Stat(destFile); err != nil {
		t.Errorf("archived file not found at %s: %v", destFile, err)
	}

	// Fresh current/ exists with expected subdirs.
	for _, sub := range []string{"logs", "artifacts", "reports"} {
		dir := filepath.Join(currentDir, sub)
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("fresh current/%s missing: %v", sub, err)
		}
	}

	// sessions.ndjson written.
	if _, err := os.Stat(filepath.Join(workDir, "sessions.ndjson")); err != nil {
		t.Errorf("sessions.ndjson missing: %v", err)
	}

	// Output contains summary.
	if !strings.Contains(out.String(), "v1.0") {
		t.Errorf("output missing tag; got:\n%s", out.String())
	}
}

// TestRun_AutoTag verifies that --auto-tag appends a suffix on conflict.
func TestRun_AutoTag(t *testing.T) {
	home, workDir, _ := newTestHome(t, "proj")
	yakosRoot := newYakosRoot(t)

	// Pre-create the archive dest to force a conflict.
	conflictDest := filepath.Join(workDir, "archive", "sprint-3")
	if err := os.MkdirAll(conflictDest, 0755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cfg := Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "sprint-3",
		AutoTag:   true,
		HomeDir:   home,
		Now:       time.Now(),
		Writer:    &out,
		ErrWriter: &bytes.Buffer{},
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run with AutoTag: %v", err)
	}

	// Tag should have a suffix.
	if res.Tag == "sprint-3" {
		t.Error("expected auto-tag suffix; got original tag 'sprint-3'")
	}
	if !strings.HasPrefix(res.Tag, "sprint-3-") {
		t.Errorf("auto-tag should have 'sprint-3-' prefix; got %q", res.Tag)
	}
}

// TestRun_ConflictNoAutoTag verifies that a conflicting tag without --auto-tag returns an error.
func TestRun_ConflictNoAutoTag(t *testing.T) {
	home, workDir, _ := newTestHome(t, "proj")
	yakosRoot := newYakosRoot(t)

	// Pre-create the conflict.
	if err := os.MkdirAll(filepath.Join(workDir, "archive", "v2"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "v2",
		AutoTag:   false,
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for conflicting tag without --auto-tag")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention 'already exists'; got %q", err.Error())
	}
}

// TestRun_ExpiredBypass verifies that an expired bypass aborts the archive before
// any filesystem mutation.
func TestRun_ExpiredBypass(t *testing.T) {
	home, workDir, currentDir := newTestHome(t, "proj")
	yakosRoot := newYakosRoot(t)

	past := "2020-01-01T00:00:00Z"
	bypassContent := "# bypass\n\n## Active entries\n\n## bypass: old-bypass\n**Expires:** " + past + "\n"
	seedFile(t, filepath.Join(currentDir, "hook-bypass.md"), bypassContent)

	cfg := Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "v1",
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for expired bypass")
	}

	// work/archive/ should NOT have been created (abort before move).
	archiveDir := filepath.Join(workDir, "archive", "v1")
	if _, statErr := os.Stat(archiveDir); !os.IsNotExist(statErr) {
		t.Error("archive dest should not exist after expired-bypass abort")
	}
}

// TestRun_MissingProject verifies that a missing project control dir returns an error.
func TestRun_MissingProject(t *testing.T) {
	home := t.TempDir()
	yakosRoot := newYakosRoot(t)

	cfg := Config{
		YakosRoot: yakosRoot,
		Project:   "ghost-project",
		Tag:       "v1",
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing project")
	}
	if !strings.Contains(err.Error(), "ghost-project") {
		t.Errorf("error should mention project name; got %q", err.Error())
	}
}

// TestRun_BadTag_Slash verifies that a tag with a slash is rejected.
func TestRun_BadTag_Slash(t *testing.T) {
	home, _, _ := newTestHome(t, "proj")
	yakosRoot := newYakosRoot(t)

	cfg := Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "feat/archive",
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for tag with slash")
	}
	if !strings.Contains(err.Error(), "alphanumeric") {
		t.Errorf("error should mention alphanumeric constraint; got %q", err.Error())
	}
}

// TestRun_BadTag_Space verifies that a tag with a space is rejected.
func TestRun_BadTag_Space(t *testing.T) {
	home, _, _ := newTestHome(t, "proj")
	yakosRoot := newYakosRoot(t)

	cfg := Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "my tag",
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for tag with space")
	}
}

// TestRun_NoCurrent verifies that a missing work/current/ returns an error.
func TestRun_NoCurrent(t *testing.T) {
	home := t.TempDir()
	yakosRoot := newYakosRoot(t)

	// Create control dir but NOT work/current/.
	if err := os.MkdirAll(filepath.Join(home, "agent-control", "proj"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "v1",
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing work/current/")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error should mention 'does not exist'; got %q", err.Error())
	}
}

// TestRun_LedgerEntry verifies that the sessions.ndjson entry has the correct
// JSON fields: ts, action, project, tag, dest, size.
func TestRun_LedgerEntry(t *testing.T) {
	home, workDir, _ := newTestHome(t, "proj")
	yakosRoot := newYakosRoot(t)

	cfg := Config{
		YakosRoot: yakosRoot,
		Project:   "proj",
		Tag:       "ledger-test",
		HomeDir:   home,
		Now:       time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(workDir, "sessions.ndjson"))
	if err != nil {
		t.Fatalf("ReadFile sessions.ndjson: %v", err)
	}

	var entry map[string]string
	if err := json.Unmarshal(bytes.TrimRight(data, "\n"), &entry); err != nil {
		t.Fatalf("Unmarshal sessions.ndjson: %v", err)
	}

	checks := map[string]string{
		"action":  "archive",
		"project": "proj",
		"tag":     "ledger-test",
	}
	for k, want := range checks {
		if got := entry[k]; got != want {
			t.Errorf("ledger[%q]: got %q, want %q", k, got, want)
		}
	}

	requiredFields := []string{"ts", "dest", "size"}
	for _, f := range requiredFields {
		if entry[f] == "" {
			t.Errorf("ledger missing field %q", f)
		}
	}
}

// ---- ParseBypassEntries edge cases ------------------------------------------

// TestParseBypassEntries_EmptyFile verifies that an empty bypass file yields no entries.
func TestParseBypassEntries_EmptyFile(t *testing.T) {
	entries := parseActiveBypassEntries(strings.NewReader(""))
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for empty file; got %d", len(entries))
	}
}

// TestParseBypassEntries_NoActiveSection verifies entries before the active
// section header are not parsed (mirrors the bash awk guard).
func TestParseBypassEntries_NoActiveSection(t *testing.T) {
	content := "## bypass: sneaky\n**Expires:** 2020-01-01T00:00:00Z\n"
	entries := parseActiveBypassEntries(strings.NewReader(content))
	if len(entries) != 0 {
		t.Errorf("entries before '## Active entries' should be ignored; got %d", len(entries))
	}
}

// TestParseBypassEntries_MultipleEntries verifies multiple bypass blocks are parsed.
func TestParseBypassEntries_MultipleEntries(t *testing.T) {
	content := `## Active entries

## bypass: alpha
**Expires:** 2020-01-01T00:00:00Z

## bypass: beta
**Expires:** 2020-06-01T00:00:00Z
`
	entries := parseActiveBypassEntries(strings.NewReader(content))
	if len(entries) != 2 {
		t.Errorf("expected 2 entries; got %d", len(entries))
	}
	if entries[0].id != "alpha" {
		t.Errorf("entry[0].id: got %q, want alpha", entries[0].id)
	}
	if entries[1].id != "beta" {
		t.Errorf("entry[1].id: got %q, want beta", entries[1].id)
	}
}
