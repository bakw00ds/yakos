package memory

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- helpers ----------------------------------------------------------------

// tempMemDir creates a temporary directory for a test and returns its path.
// It registers cleanup via t.Cleanup.
func tempMemDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "yakos-mem-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// seedFile writes content to dir/<name>.
func seedFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("seedFile %s: %v", name, err)
	}
	return path
}

// captureRun runs the memory command and returns (stdout, stderr, error).
func captureRun(t *testing.T, cfg Config) (string, string, error) {
	t.Helper()
	var stdout, stderr strings.Builder
	cfg.Writer = &stdout
	cfg.ErrWriter = &stderr
	err := Run(cfg)
	return stdout.String(), stderr.String(), err
}

// ---- TestParseIndexLine -----------------------------------------------------

func TestParseIndexLine_Valid(t *testing.T) {
	line := "- [Delegate to roster](feedback-delegate-to-roster.md) — always delegate"
	e, ok := parseIndexLine(line)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if e.Name != "Delegate to roster" {
		t.Errorf("Name: got %q want %q", e.Name, "Delegate to roster")
	}
	if e.Slug != "feedback-delegate-to-roster" {
		t.Errorf("Slug: got %q want %q", e.Slug, "feedback-delegate-to-roster")
	}
	if e.Description != "always delegate" {
		t.Errorf("Description: got %q want %q", e.Description, "always delegate")
	}
}

func TestParseIndexLine_NoDescription(t *testing.T) {
	line := "- [My note](my-note.md)"
	e, ok := parseIndexLine(line)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if e.Name != "My note" {
		t.Errorf("Name: got %q", e.Name)
	}
	if e.Slug != "my-note" {
		t.Errorf("Slug: got %q", e.Slug)
	}
	if e.Description != "" {
		t.Errorf("Description: expected empty, got %q", e.Description)
	}
}

func TestParseIndexLine_NotALink(t *testing.T) {
	_, ok := parseIndexLine("# Memory index")
	if ok {
		t.Error("expected ok=false for heading line")
	}
	_, ok = parseIndexLine("")
	if ok {
		t.Error("expected ok=false for empty line")
	}
	_, ok = parseIndexLine("some random text")
	if ok {
		t.Error("expected ok=false for non-link line")
	}
}

// ---- TestBuildIndexLine -----------------------------------------------------

func TestBuildIndexLine(t *testing.T) {
	got := BuildIndexLine("Delegate to roster", "feedback-delegate", "always delegate first")
	want := "- [Delegate to roster](feedback-delegate.md) — always delegate first"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// ---- TestParseIndexBytes ----------------------------------------------------

func TestParseIndexBytes_MultipleEntries(t *testing.T) {
	data := []byte("- [Note A](note-a.md) — about A\n- [Note B](note-b.md) — about B\n")
	entries := ParseIndexBytes(data)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries; got %d", len(entries))
	}
	if entries[0].Slug != "note-a" {
		t.Errorf("entries[0].Slug: got %q", entries[0].Slug)
	}
	if entries[1].Slug != "note-b" {
		t.Errorf("entries[1].Slug: got %q", entries[1].Slug)
	}
}

func TestParseIndexBytes_Empty(t *testing.T) {
	entries := ParseIndexBytes([]byte("# Memory index\n\n"))
	if len(entries) != 0 {
		t.Errorf("expected 0 entries; got %d", len(entries))
	}
}

// ---- TestSplitFrontmatter ---------------------------------------------------

func TestSplitFrontmatter_Full(t *testing.T) {
	content := "---\nname: My note\ndescription: short desc\nmetadata:\n  type: feedback\n---\nBody text here.\n"
	fm, body, err := splitFrontmatter(content)
	if err != nil {
		t.Fatalf("splitFrontmatter: %v", err)
	}
	if fm.Name != "My note" {
		t.Errorf("fm.Name: got %q", fm.Name)
	}
	if fm.Description != "short desc" {
		t.Errorf("fm.Description: got %q", fm.Description)
	}
	if fm.Metadata.Type != "feedback" {
		t.Errorf("fm.Metadata.Type: got %q", fm.Metadata.Type)
	}
	if !strings.Contains(body, "Body text here.") {
		t.Errorf("body missing expected text; got %q", body)
	}
}

func TestSplitFrontmatter_NoFrontmatter(t *testing.T) {
	content := "Just body text without frontmatter.\n"
	fm, body, err := splitFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.Name != "" {
		t.Errorf("expected empty name; got %q", fm.Name)
	}
	if body != content {
		t.Errorf("body should be full content; got %q", body)
	}
}

func TestSplitFrontmatter_EmptyBody(t *testing.T) {
	content := "---\nname: note\ndescription: d\n---\n"
	fm, body, err := splitFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.Name != "note" {
		t.Errorf("fm.Name: got %q", fm.Name)
	}
	_ = body // body is empty string — that is valid
}

// ---- TestSchemaVersion ------------------------------------------------------

func TestSchemaVersionRoundTrip(t *testing.T) {
	dir := tempMemDir(t)
	if err := writeSchemaVersion(dir); err != nil {
		t.Fatalf("writeSchemaVersion: %v", err)
	}
	got := ReadSchemaVersion(dir)
	if got != SchemaVersion {
		t.Errorf("got %q; want %q", got, SchemaVersion)
	}
}

func TestReadSchemaVersion_Absent(t *testing.T) {
	dir := tempMemDir(t)
	got := ReadSchemaVersion(dir)
	if got != "" {
		t.Errorf("expected empty string for absent sidecar; got %q", got)
	}
}

// ---- TestList ---------------------------------------------------------------

func TestList_EmptyDir(t *testing.T) {
	dir := tempMemDir(t)
	out, _, err := captureRun(t, Config{MemoryDir: dir, Subcommand: "list"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, dir) {
		t.Errorf("expected dir path in output; got %q", out)
	}
}

func TestList_NonexistentDir(t *testing.T) {
	dir := "/tmp/yakos-test-nonexistent-" + t.Name()
	out, _, err := captureRun(t, Config{MemoryDir: dir, Subcommand: "list"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "no memory yet") {
		t.Errorf("expected 'no memory yet' message; got %q", out)
	}
}

func TestList_WithFiles(t *testing.T) {
	dir := tempMemDir(t)
	seedFile(t, dir, "MEMORY.md", "- [Note A](note-a.md) — about A\n")
	seedFile(t, dir, "note-a.md", "---\nname: Note A\ndescription: about A\n---\nBody\n")

	out, _, err := captureRun(t, Config{MemoryDir: dir, Subcommand: "list"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "note-a.md") {
		t.Errorf("expected note-a.md in output; got %q", out)
	}
	if !strings.Contains(out, "Index (MEMORY.md)") {
		t.Errorf("expected 'Index (MEMORY.md)' in output; got %q", out)
	}
}

// ---- TestRead ---------------------------------------------------------------

func TestRead_ExistingSlug(t *testing.T) {
	dir := tempMemDir(t)
	content := "---\nname: My Note\ndescription: test\n---\nHello memory.\n"
	seedFile(t, dir, "my-note.md", content)

	out, _, err := captureRun(t, Config{MemoryDir: dir, Subcommand: "read", Slug: "my-note"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != content {
		t.Errorf("got %q; want %q", out, content)
	}
}

func TestRead_SlugWithMdExtension(t *testing.T) {
	dir := tempMemDir(t)
	content := "body\n"
	seedFile(t, dir, "my-note.md", content)

	// Slug with .md suffix should still resolve.
	out, _, err := captureRun(t, Config{MemoryDir: dir, Subcommand: "read", Slug: "my-note.md"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != content {
		t.Errorf("got %q; want %q", out, content)
	}
}

func TestRead_MissingSlug(t *testing.T) {
	dir := tempMemDir(t)
	_, _, err := captureRun(t, Config{MemoryDir: dir, Subcommand: "read", Slug: "does-not-exist"})
	if err == nil {
		t.Fatal("expected error for missing slug; got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found'; got %q", err.Error())
	}
}

func TestRead_EmptySlug(t *testing.T) {
	dir := tempMemDir(t)
	_, _, err := captureRun(t, Config{MemoryDir: dir, Subcommand: "read", Slug: ""})
	if err == nil {
		t.Fatal("expected error for empty slug; got nil")
	}
}

// ---- TestWrite --------------------------------------------------------------

func TestWrite_NewFile(t *testing.T) {
	dir := tempMemDir(t)
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

	out, _, err := captureRun(t, Config{
		MemoryDir:  dir,
		Subcommand: "write",
		Slug:       "my-note",
		Type:       "feedback",
		Body:       "Always delegate.\n",
		Name:       "My Note",
		Description: "delegate always",
		Now:        now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "wrote") {
		t.Errorf("expected 'wrote' in output; got %q", out)
	}

	// File exists.
	target := filepath.Join(dir, "my-note.md")
	if !fileExists(target) {
		t.Errorf("expected file %s to exist", target)
	}

	// File has correct frontmatter.
	data, _ := os.ReadFile(target)
	content := string(data)
	if !strings.Contains(content, "name: My Note") {
		t.Errorf("missing name in frontmatter; content: %q", content)
	}
	if !strings.Contains(content, "type: feedback") {
		t.Errorf("missing type in frontmatter; content: %q", content)
	}
	if !strings.Contains(content, "Always delegate.") {
		t.Errorf("missing body; content: %q", content)
	}

	// MEMORY.md updated.
	indexPath := filepath.Join(dir, "MEMORY.md")
	if !fileExists(indexPath) {
		t.Error("expected MEMORY.md to be created")
	}
	indexData, _ := os.ReadFile(indexPath)
	if !strings.Contains(string(indexData), "my-note.md") {
		t.Errorf("expected my-note.md in MEMORY.md; got %q", string(indexData))
	}

	// Schema sidecar written.
	if ReadSchemaVersion(dir) != SchemaVersion {
		t.Errorf("schema sidecar missing or wrong")
	}
}

func TestWrite_Overwrite(t *testing.T) {
	dir := tempMemDir(t)
	now := time.Now().UTC()

	// Write once.
	_ = Run(Config{
		MemoryDir:  dir,
		Subcommand: "write",
		Slug:       "my-note",
		Type:       "feedback",
		Body:       "Original.\n",
		Now:        now,
		Writer:     io.Discard,
		ErrWriter:  io.Discard,
	})

	// Write again (overwrite).
	out, _, err := captureRun(t, Config{
		MemoryDir:  dir,
		Subcommand: "write",
		Slug:       "my-note",
		Type:       "note",
		Body:       "Updated.\n",
		Now:        now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "wrote") {
		t.Errorf("expected 'wrote' in output; got %q", out)
	}

	// New content.
	data, _ := os.ReadFile(filepath.Join(dir, "my-note.md"))
	if !strings.Contains(string(data), "Updated.") {
		t.Errorf("expected updated body; got %q", string(data))
	}

	// MEMORY.md has exactly one entry for the slug.
	indexData, _ := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	count := strings.Count(string(indexData), "my-note.md")
	if count != 1 {
		t.Errorf("expected exactly 1 index entry for my-note.md; got %d", count)
	}
}

func TestWrite_MissingSlug(t *testing.T) {
	dir := tempMemDir(t)
	_, _, err := captureRun(t, Config{
		MemoryDir:  dir,
		Subcommand: "write",
		Slug:       "",
		Type:       "feedback",
		Body:       "body",
	})
	if err == nil {
		t.Fatal("expected error for empty slug")
	}
}

func TestWrite_MissingType(t *testing.T) {
	dir := tempMemDir(t)
	_, _, err := captureRun(t, Config{
		MemoryDir:  dir,
		Subcommand: "write",
		Slug:       "my-note",
		Type:       "",
		Body:       "body",
	})
	if err == nil {
		t.Fatal("expected error for empty type")
	}
}

func TestWrite_AutoDescription(t *testing.T) {
	dir := tempMemDir(t)
	body := "First line is the description.\nSecond line.\n"

	if err := Run(Config{
		MemoryDir:  dir,
		Subcommand: "write",
		Slug:       "auto-desc",
		Type:       "note",
		Body:       body,
		Now:        time.Now().UTC(),
		Writer:     io.Discard,
		ErrWriter:  io.Discard,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "auto-desc.md"))
	content := string(data)
	// description should be the first line (truncated if needed)
	if !strings.Contains(content, "First line is the description.") {
		t.Errorf("expected first line as description; got %q", content)
	}
}

func TestWrite_AtomicWrite(t *testing.T) {
	// Verify no .tmp file left over after successful write.
	dir := tempMemDir(t)
	if err := Run(Config{
		MemoryDir:  dir,
		Subcommand: "write",
		Slug:       "atomic-test",
		Type:       "note",
		Body:       "body\n",
		Now:        time.Now().UTC(),
		Writer:     io.Discard,
		ErrWriter:  io.Discard,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No .tmp files should exist.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover .tmp file: %s", e.Name())
		}
	}
}

// ---- TestDelete -------------------------------------------------------------

func TestDelete_ExistingFile(t *testing.T) {
	dir := tempMemDir(t)
	seedFile(t, dir, "note-a.md", "---\nname: Note A\ndescription: d\n---\nbody\n")
	seedFile(t, dir, "MEMORY.md", "- [Note A](note-a.md) — d\n")

	out, _, err := captureRun(t, Config{MemoryDir: dir, Subcommand: "delete", Slug: "note-a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "deleted") {
		t.Errorf("expected 'deleted' in output; got %q", out)
	}

	// File removed.
	if fileExists(filepath.Join(dir, "note-a.md")) {
		t.Error("expected note-a.md to be removed")
	}

	// MEMORY.md entry removed.
	data, _ := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if strings.Contains(string(data), "note-a.md") {
		t.Errorf("expected note-a.md removed from MEMORY.md; got %q", string(data))
	}
}

func TestDelete_MissingFile(t *testing.T) {
	dir := tempMemDir(t)
	_, _, err := captureRun(t, Config{MemoryDir: dir, Subcommand: "delete", Slug: "does-not-exist"})
	if err == nil {
		t.Fatal("expected error for missing slug")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found'; got %q", err.Error())
	}
}

func TestDelete_EmptySlug(t *testing.T) {
	dir := tempMemDir(t)
	_, _, err := captureRun(t, Config{MemoryDir: dir, Subcommand: "delete", Slug: ""})
	if err == nil {
		t.Fatal("expected error for empty slug")
	}
}

// ---- TestIndexRebuild -------------------------------------------------------

func TestIndexRebuild_FromFiles(t *testing.T) {
	dir := tempMemDir(t)
	seedFile(t, dir, "note-a.md", "---\nname: Note A\ndescription: about A\nmetadata:\n  type: feedback\n---\nBody A.\n")
	seedFile(t, dir, "note-b.md", "---\nname: Note B\ndescription: about B\nmetadata:\n  type: note\n---\nBody B.\n")

	out, _, err := captureRun(t, Config{MemoryDir: dir, Subcommand: "index-rebuild"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "2 entries") {
		t.Errorf("expected '2 entries' in output; got %q", out)
	}

	data, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("MEMORY.md not created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "note-a.md") {
		t.Errorf("expected note-a.md in MEMORY.md; got %q", content)
	}
	if !strings.Contains(content, "note-b.md") {
		t.Errorf("expected note-b.md in MEMORY.md; got %q", content)
	}
}

func TestIndexRebuild_NoFiles(t *testing.T) {
	dir := tempMemDir(t)
	out, _, err := captureRun(t, Config{MemoryDir: dir, Subcommand: "index-rebuild"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "0 entries") {
		t.Errorf("expected '0 entries' in output; got %q", out)
	}
}

func TestIndexRebuild_NonexistentDir(t *testing.T) {
	_, _, err := captureRun(t, Config{
		MemoryDir:  "/tmp/yakos-test-nonexistent-rebuild",
		Subcommand: "index-rebuild",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestIndexRebuild_SchemaVersionWritten(t *testing.T) {
	dir := tempMemDir(t)
	_ = Run(Config{
		MemoryDir:  dir,
		Subcommand: "index-rebuild",
		Writer:     io.Discard,
		ErrWriter:  io.Discard,
	})
	if ReadSchemaVersion(dir) != SchemaVersion {
		t.Error("expected schema sidecar to be written by index-rebuild")
	}
}

// ---- TestRoundTrip ----------------------------------------------------------

func TestRoundTrip_WriteReadDelete(t *testing.T) {
	dir := tempMemDir(t)
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)

	// Write.
	if err := Run(Config{
		MemoryDir:   dir,
		Subcommand:  "write",
		Slug:        "round-trip",
		Type:        "project",
		Body:        "Round-trip body.\n",
		Name:        "Round Trip",
		Description: "a round trip test",
		Now:         now,
		Writer:      io.Discard,
		ErrWriter:   io.Discard,
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read.
	var out strings.Builder
	if err := Run(Config{
		MemoryDir:  dir,
		Subcommand: "read",
		Slug:       "round-trip",
		Writer:     &out,
		ErrWriter:  io.Discard,
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(out.String(), "Round-trip body.") {
		t.Errorf("read body missing; got %q", out.String())
	}

	// Delete.
	if err := Run(Config{
		MemoryDir:  dir,
		Subcommand: "delete",
		Slug:       "round-trip",
		Writer:     io.Discard,
		ErrWriter:  io.Discard,
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// File gone.
	if fileExists(filepath.Join(dir, "round-trip.md")) {
		t.Error("expected file to be deleted")
	}
}

// ---- TestUnknownSubcommand --------------------------------------------------

func TestUnknownSubcommand(t *testing.T) {
	dir := tempMemDir(t)
	_, _, err := captureRun(t, Config{MemoryDir: dir, Subcommand: "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("error should mention 'unknown subcommand'; got %q", err.Error())
	}
}

// ---- TestMemoryFiles --------------------------------------------------------

func TestMemoryFiles_ExcludesMEMORYmd(t *testing.T) {
	dir := tempMemDir(t)
	seedFile(t, dir, "MEMORY.md", "index\n")
	seedFile(t, dir, "note-a.md", "body\n")
	seedFile(t, dir, "note-b.md", "body\n")

	files, err := memoryFiles(dir)
	if err != nil {
		t.Fatalf("memoryFiles: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 files; got %d: %v", len(files), files)
	}
	for _, f := range files {
		if f == "MEMORY.md" {
			t.Error("MEMORY.md should be excluded")
		}
	}
}

func TestMemoryFiles_SortedOrder(t *testing.T) {
	dir := tempMemDir(t)
	seedFile(t, dir, "zzz.md", "z\n")
	seedFile(t, dir, "aaa.md", "a\n")
	seedFile(t, dir, "mmm.md", "m\n")

	files, err := memoryFiles(dir)
	if err != nil {
		t.Fatalf("memoryFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files; got %d", len(files))
	}
	if files[0] != "aaa.md" || files[1] != "mmm.md" || files[2] != "zzz.md" {
		t.Errorf("wrong sort order: %v", files)
	}
}

// ---- TestUpsertIndexEntry ---------------------------------------------------

func TestUpsertIndexEntry_Append(t *testing.T) {
	dir := tempMemDir(t)
	seedFile(t, dir, "MEMORY.md", "- [A](a.md) — about A\n")

	if err := upsertIndexEntry(dir, "b", "B", "about B"); err != nil {
		t.Fatalf("upsertIndexEntry: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	content := string(data)
	if !strings.Contains(content, "a.md") {
		t.Error("existing entry a.md should be preserved")
	}
	if !strings.Contains(content, "b.md") {
		t.Error("new entry b.md should be appended")
	}
}

func TestUpsertIndexEntry_UpdateExisting(t *testing.T) {
	dir := tempMemDir(t)
	seedFile(t, dir, "MEMORY.md", "- [Old name](my-slug.md) — old description\n")

	if err := upsertIndexEntry(dir, "my-slug", "New name", "new description"); err != nil {
		t.Fatalf("upsertIndexEntry: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	content := string(data)
	if strings.Contains(content, "Old name") {
		t.Error("old entry should be replaced")
	}
	if !strings.Contains(content, "New name") {
		t.Error("new entry should replace old")
	}
	// Only one entry for the slug.
	if strings.Count(content, "my-slug.md") != 1 {
		t.Errorf("expected exactly 1 reference to my-slug.md; content: %q", content)
	}
}

func TestUpsertIndexEntry_CreatesMEMORYmd(t *testing.T) {
	dir := tempMemDir(t)
	if err := upsertIndexEntry(dir, "new-slug", "New Entry", "desc"); err != nil {
		t.Fatalf("upsertIndexEntry: %v", err)
	}
	if !fileExists(filepath.Join(dir, "MEMORY.md")) {
		t.Error("expected MEMORY.md to be created")
	}
}

// ---- TestRemoveIndexEntry ---------------------------------------------------

func TestRemoveIndexEntry_Removes(t *testing.T) {
	dir := tempMemDir(t)
	seedFile(t, dir, "MEMORY.md", "- [A](a.md) — a\n- [B](b.md) — b\n")

	if err := removeIndexEntry(dir, "a"); err != nil {
		t.Fatalf("removeIndexEntry: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	content := string(data)
	if strings.Contains(content, "a.md") {
		t.Errorf("a.md should be removed; content: %q", content)
	}
	if !strings.Contains(content, "b.md") {
		t.Error("b.md should remain")
	}
}

func TestRemoveIndexEntry_AbsentFile(t *testing.T) {
	dir := tempMemDir(t)
	// No MEMORY.md — should not error.
	if err := removeIndexEntry(dir, "nonexistent"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

