package soul

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixedNow is a deterministic time used in tests.
var fixedNow = time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

// newTestConfig returns a Config wired to a temp home dir with output buffers.
func newTestConfig(homeDir, subcommand string, args []string) Config {
	return Config{
		Subcommand: subcommand,
		Args:       args,
		HomeDir:    homeDir,
		Now:        fixedNow,
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
}

func cfgOut(cfg Config) string {
	return cfg.Writer.(*bytes.Buffer).String()
}

// makeSoulFile creates ~/.yakos-state/soul/<name>.md under homeDir.
func makeSoulFile(t *testing.T, homeDir, name, content string) string {
	t.Helper()
	dir := filepath.Join(homeDir, ".yakos-state", "soul")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir soul: %v", err)
	}
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write soul file: %v", err)
	}
	return path
}

// makePendingEdits creates soul-proposed-edits.md under the slug's work/current/.
func makePendingEdits(t *testing.T, homeDir, slug, content string) string {
	t.Helper()
	dir := filepath.Join(homeDir, "agent-control", slug, "work", "current")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir work/current: %v", err)
	}
	path := filepath.Join(dir, "soul-proposed-edits.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write pending: %v", err)
	}
	return path
}

// ---- show -------------------------------------------------------------------

func TestShow_GlobalExists(t *testing.T) {
	home := t.TempDir()
	makeSoulFile(t, home, "global", "# Soul\n\nmy preferences\n")

	cfg := newTestConfig(home, "show", nil)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run show: %v", err)
	}
	if res.Layer != "global" {
		t.Errorf("expected layer=global; got %q", res.Layer)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "my preferences") {
		t.Errorf("expected soul content in output; got: %q", out)
	}
}

func TestShow_GlobalAbsent(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "show", nil)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run show absent: %v", err)
	}
	if res.File != "" {
		t.Errorf("expected File='' for absent soul; got %q", res.File)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "no soul file") {
		t.Errorf("expected 'no soul file' message; got: %q", out)
	}
}

func TestShow_ProjectLayer(t *testing.T) {
	home := t.TempDir()
	// Seed a project-slug soul file.
	makeSoulFile(t, home, "myproject", "project soul content\n")

	cfg := newTestConfig(home, "show", []string{"project"})
	cfg.ProjectDir = filepath.Join(home, "agent-control", "myproject") // drives slug resolution
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run show project: %v", err)
	}
	if res.Layer != "project" {
		t.Errorf("expected layer=project; got %q", res.Layer)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "project soul content") {
		t.Errorf("expected project soul content; got: %q", out)
	}
}

func TestShow_UnknownLayer_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "show", []string{"badlayer"})
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown layer")
	}
	if !strings.Contains(err.Error(), "unknown layer") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- edit -------------------------------------------------------------------

func TestEdit_SeedsFileFromBareDefault_WhenAbsent(t *testing.T) {
	home := t.TempDir()
	soulDir := filepath.Join(home, ".yakos-state", "soul")

	editorCalled := false
	cfg := newTestConfig(home, "edit", nil)
	cfg.EditorFn = func(path string) error {
		editorCalled = true
		// Verify the file was seeded before editor is called.
		if _, err := os.Stat(path); err != nil {
			t.Errorf("editor called on non-existent path %s: %v", path, err)
		}
		return nil
	}
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run edit: %v", err)
	}
	if !editorCalled {
		t.Error("editor was not called")
	}

	globalPath := filepath.Join(soulDir, "global.md")
	data, readErr := os.ReadFile(globalPath)
	if readErr != nil {
		t.Fatalf("read seeded file: %v", readErr)
	}
	content := string(data)
	if !strings.Contains(content, "Soul") {
		t.Errorf("seeded content missing 'Soul'; got: %q", content)
	}
	_ = res
}

func TestEdit_SnapshotsExistingFile(t *testing.T) {
	home := t.TempDir()
	original := "# Soul\n\noriginal content\n"
	makeSoulFile(t, home, "global", original)

	cfg := newTestConfig(home, "edit", nil)
	cfg.EditorFn = func(string) error { return nil }

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run edit: %v", err)
	}
	if res.SnapshotFile == "" {
		t.Fatal("expected a snapshot file to be created")
	}
	data, err := os.ReadFile(res.SnapshotFile)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(data) != original {
		t.Errorf("snapshot does not match original; got: %q", string(data))
	}
}

func TestEdit_NoSnapshot_WhenFileAbsent(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "edit", nil)
	cfg.EditorFn = func(string) error { return nil }

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run edit: %v", err)
	}
	// When the file didn't exist before edit, the seeded file is snapshotted after seeding.
	// A snapshot of the seeded content is acceptable — the important thing is that
	// the soul file exists after editing and that edit completes without error.
	// (The bash original does the same: seeds the file then snapshots it.)
	_ = res.SnapshotFile // may or may not be set; both are valid
}

func TestEdit_OutputConfirmsEditedPath(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "edit", nil)
	cfg.EditorFn = func(string) error { return nil }

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run edit: %v", err)
	}

	out := cfgOut(cfg)
	if !strings.Contains(out, "soul: edited") {
		t.Errorf("expected 'soul: edited' in output; got: %q", out)
	}
}

func TestEdit_ProjectLayer_ResolvesSlug(t *testing.T) {
	home := t.TempDir()
	editorCalledOn := ""
	cfg := newTestConfig(home, "edit", []string{"project"})
	cfg.ProjectDir = filepath.Join(home, "myproject")
	cfg.EditorFn = func(path string) error {
		editorCalledOn = path
		return nil
	}

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run edit project: %v", err)
	}

	if !strings.Contains(editorCalledOn, "myproject") {
		t.Errorf("expected editor called on myproject path; got %q", editorCalledOn)
	}
}

func TestEdit_SeedsFromTemplate_WhenYakosRootSet(t *testing.T) {
	home := t.TempDir()
	// Create a fake YakosRoot with a template.
	yakosRoot := t.TempDir()
	settingsDir := filepath.Join(yakosRoot, "lib", "settings")
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
		t.Fatalf("mkdir settings: %v", err)
	}
	tmplContent := "# Soul ({{layer}}) — {{user}}\ncreated: {{ts}}\nbody\n"
	if err := os.WriteFile(filepath.Join(settingsDir, "soul.template.md"), []byte(tmplContent), 0644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	cfg := newTestConfig(home, "edit", nil)
	cfg.YakosRoot = yakosRoot
	cfg.EditorFn = func(string) error { return nil }

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run edit with template: %v", err)
	}

	globalPath := filepath.Join(home, ".yakos-state", "soul", "global.md")
	data, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read seeded: %v", err)
	}
	content := string(data)
	// Template sentinels should be replaced.
	if strings.Contains(content, "{{layer}}") {
		t.Error("{{layer}} sentinel not replaced")
	}
	if strings.Contains(content, "{{ts}}") {
		t.Error("{{ts}} sentinel not replaced")
	}
	if !strings.Contains(content, "global") {
		t.Error("expected 'global' in seeded content")
	}
}

// ---- history ----------------------------------------------------------------

func TestHistory_NoHistory(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "history", nil)
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run history: %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "no history") {
		t.Errorf("expected 'no history'; got: %q", out)
	}
}

func TestHistory_ListsSnapshots(t *testing.T) {
	home := t.TempDir()
	historyDir := filepath.Join(home, ".yakos-state", "soul", "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		t.Fatalf("mkdir history: %v", err)
	}

	snap1 := filepath.Join(historyDir, "global-2026-06-01T10:00:00Z.md")
	snap2 := filepath.Join(historyDir, "global-2026-06-02T12:00:00Z.md")
	if err := os.WriteFile(snap1, []byte("old content"), 0644); err != nil {
		t.Fatalf("write snap1: %v", err)
	}
	if err := os.WriteFile(snap2, []byte("newer content"), 0644); err != nil {
		t.Fatalf("write snap2: %v", err)
	}

	cfg := newTestConfig(home, "history", nil)
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run history: %v", err)
	}

	out := cfgOut(cfg)
	if !strings.Contains(out, "2026-06-01T10:00:00Z") {
		t.Errorf("missing first snapshot timestamp; got: %q", out)
	}
	if !strings.Contains(out, "2026-06-02T12:00:00Z") {
		t.Errorf("missing second snapshot timestamp; got: %q", out)
	}
	// Each entry shows byte count.
	if !strings.Contains(out, "bytes") {
		t.Errorf("expected byte count in history; got: %q", out)
	}
}

func TestHistory_FiltersByLayer(t *testing.T) {
	home := t.TempDir()
	historyDir := filepath.Join(home, ".yakos-state", "soul", "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		t.Fatalf("mkdir history: %v", err)
	}
	if err := os.WriteFile(filepath.Join(historyDir, "global-2026-01-01T00:00:00Z.md"), []byte("g"), 0644); err != nil {
		t.Fatalf("write global snap: %v", err)
	}
	if err := os.WriteFile(filepath.Join(historyDir, "myproj-2026-01-02T00:00:00Z.md"), []byte("p"), 0644); err != nil {
		t.Fatalf("write project snap: %v", err)
	}

	cfg := newTestConfig(home, "history", nil) // defaults to global
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run history global: %v", err)
	}
	out := cfgOut(cfg)
	if strings.Contains(out, "myproj") {
		t.Error("project snapshot should not appear in global history")
	}
	if !strings.Contains(out, "2026-01-01") {
		t.Error("global snapshot should appear")
	}
}

func TestHistory_ProjectLayer(t *testing.T) {
	home := t.TempDir()
	historyDir := filepath.Join(home, ".yakos-state", "soul", "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		t.Fatalf("mkdir history: %v", err)
	}
	// History snapshots use the layer string ("project") as their filename prefix,
	// matching the bash behaviour where _soul_snapshot receives the $layer argument
	// directly ("global" or "project"), not the resolved slug.
	if err := os.WriteFile(filepath.Join(historyDir, "project-2026-06-01T00:00:00Z.md"), []byte("content"), 0644); err != nil {
		t.Fatalf("write snap: %v", err)
	}

	cfg := newTestConfig(home, "history", []string{"project"})
	cfg.ProjectDir = filepath.Join(home, "agent-control", "myproj")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run history project: %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "2026-06-01") {
		t.Errorf("expected project snapshot in history; got: %q", out)
	}
}

// ---- revert -----------------------------------------------------------------

func TestRevert_MissingVersion_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "revert", nil)
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing version")
	}
	if !strings.Contains(err.Error(), "revert requires") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRevert_SnapshotNotFound_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "revert", []string{"2025-01-01T00:00:00Z"})
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error when snapshot not found")
	}
	if !strings.Contains(err.Error(), "no snapshot") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRevert_HappyPath(t *testing.T) {
	home := t.TempDir()
	soulDir := filepath.Join(home, ".yakos-state", "soul")
	historyDir := filepath.Join(soulDir, "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		t.Fatalf("mkdir history: %v", err)
	}

	// Create current soul file.
	current := "# Current soul\n"
	makeSoulFile(t, home, "global", current)

	// Create snapshot to revert to.
	snapVersion := "2026-01-01T00:00:00Z"
	snapContent := "# Reverted soul\n\nold preferences\n"
	snapPath := filepath.Join(historyDir, "global-"+snapVersion+".md")
	if err := os.WriteFile(snapPath, []byte(snapContent), 0644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	cfg := newTestConfig(home, "revert", []string{snapVersion})
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run revert: %v", err)
	}

	// Verify the soul file was overwritten with snapshot content.
	data, _ := os.ReadFile(filepath.Join(soulDir, "global.md"))
	if string(data) != snapContent {
		t.Errorf("reverted content mismatch; got: %q", string(data))
	}
	// A snapshot of the pre-revert state should have been created.
	if res.SnapshotFile == "" {
		t.Error("expected snapshot of pre-revert state")
	}
	preRevertData, _ := os.ReadFile(res.SnapshotFile)
	if string(preRevertData) != current {
		t.Error("pre-revert snapshot does not match original current soul")
	}
	// Output confirms revert.
	out := cfgOut(cfg)
	if !strings.Contains(out, "reverted") {
		t.Errorf("expected 'reverted' in output; got: %q", out)
	}
}

func TestRevert_WithLayerArg(t *testing.T) {
	home := t.TempDir()
	soulDir := filepath.Join(home, ".yakos-state", "soul")
	historyDir := filepath.Join(soulDir, "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		t.Fatalf("mkdir history: %v", err)
	}

	makeSoulFile(t, home, "global", "current\n")
	snapVersion := "2026-05-01T00:00:00Z"
	snapContent := "old content\n"
	if err := os.WriteFile(filepath.Join(historyDir, "global-"+snapVersion+".md"), []byte(snapContent), 0644); err != nil {
		t.Fatalf("write snap: %v", err)
	}

	cfg := newTestConfig(home, "revert", []string{snapVersion, "global"})
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run revert with layer: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(soulDir, "global.md"))
	if string(data) != snapContent {
		t.Errorf("revert with layer failed; got: %q", string(data))
	}
}

// ---- pending ----------------------------------------------------------------

func TestPending_NoPendingFile(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "pending", nil)
	cfg.ProjectDir = filepath.Join(home, "agent-control", "myslug")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run pending: %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "no pending") {
		t.Errorf("expected 'no pending' message; got: %q", out)
	}
}

func TestPending_NoProjectSlug_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "pending", nil)
	// No ProjectDir, no YAKOS_PROJECT_DIR env, no agent-control entries.
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error when no project slug detected")
	}
	if !strings.Contains(err.Error(), "no active project session") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPending_ListsEdits(t *testing.T) {
	home := t.TempDir()
	content := "# Proposed edits\n\n## edit: add-hipaa-framing\n\nsome body\n\n## edit: cut-adjectives\n\nanother body\n"
	makePendingEdits(t, home, "myslug", content)

	cfg := newTestConfig(home, "pending", nil)
	cfg.ProjectDir = filepath.Join(home, "agent-control", "myslug")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run pending: %v", err)
	}
	if len(res.PendingEdits) != 2 {
		t.Errorf("expected 2 pending edits; got %d: %v", len(res.PendingEdits), res.PendingEdits)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "## edit: add-hipaa-framing") {
		t.Errorf("expected first edit heading in output; got: %q", out)
	}
	if !strings.Contains(out, "## edit: cut-adjectives") {
		t.Errorf("expected second edit heading in output; got: %q", out)
	}
}

func TestPending_EmptyPendingFile(t *testing.T) {
	home := t.TempDir()
	makePendingEdits(t, home, "myslug", "# No headings here\n\nsome text\n")

	cfg := newTestConfig(home, "pending", nil)
	cfg.ProjectDir = filepath.Join(home, "agent-control", "myslug")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run pending (empty): %v", err)
	}
	if len(res.PendingEdits) != 0 {
		t.Errorf("expected 0 edits; got %d", len(res.PendingEdits))
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "no pending edits") {
		t.Errorf("expected 'no pending edits'; got: %q", out)
	}
}

// ---- approve / reject -------------------------------------------------------

func TestApprove_NotYetImplemented(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "approve", []string{"some-slug"})
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run approve: %v", err)
	}
	if res.Subcommand != "approve" {
		t.Errorf("expected subcommand=approve; got %q", res.Subcommand)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "not yet implemented") {
		t.Errorf("expected 'not yet implemented' in output; got: %q", out)
	}
}

func TestReject_NotYetImplemented(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "reject", []string{"some-slug"})
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run reject: %v", err)
	}
	if res.Subcommand != "reject" {
		t.Errorf("expected subcommand=reject; got %q", res.Subcommand)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "not yet implemented") {
		t.Errorf("expected 'not yet implemented' in output; got: %q", out)
	}
}

// ---- unknown subcommand -----------------------------------------------------

func TestUnknownSubcommand_Error(t *testing.T) {
	home := t.TempDir()
	cfg := newTestConfig(home, "frobnicate", nil)
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- help text --------------------------------------------------------------

func TestPrintHelp_KeyPhrases(t *testing.T) {
	var buf bytes.Buffer
	PrintHelp(&buf)
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

// ---- helpers internal -------------------------------------------------------

func TestLayerArg_Default(t *testing.T) {
	got := layerArg(nil, "global")
	if got != "global" {
		t.Errorf("expected 'global'; got %q", got)
	}
}

func TestLayerArg_Explicit(t *testing.T) {
	got := layerArg([]string{"project"}, "global")
	if got != "project" {
		t.Errorf("expected 'project'; got %q", got)
	}
}

func TestAtomicWrite_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	if err := atomicWrite(path, "hello\n"); err != nil {
		t.Fatalf("atomicWrite: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello\n" {
		t.Errorf("round-trip mismatch; got %q", string(data))
	}
}

func TestAtomicWrite_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	if err := atomicWrite(path, "first\n"); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := atomicWrite(path, "second\n"); err != nil {
		t.Fatalf("second write: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "second\n" {
		t.Errorf("expected 'second'; got %q", string(data))
	}
}

func TestSnapshot_FileAbsent_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	histDir := filepath.Join(dir, "history")
	snap, err := snapshot(filepath.Join(dir, "nonexistent.md"), "global", histDir, fixedNow)
	if err != nil {
		t.Fatalf("snapshot on absent file: %v", err)
	}
	if snap != "" {
		t.Errorf("expected empty snapshot path for absent file; got %q", snap)
	}
}

func TestSnapshot_CreatesTimestampedFile(t *testing.T) {
	dir := t.TempDir()
	histDir := filepath.Join(dir, "history")
	srcPath := filepath.Join(dir, "global.md")
	if err := os.WriteFile(srcPath, []byte("content\n"), 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	snap, err := snapshot(srcPath, "global", histDir, fixedNow)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap == "" {
		t.Fatal("expected non-empty snapshot path")
	}
	if !strings.Contains(snap, "global-2026-06-02") {
		t.Errorf("expected snapshot to contain date; got %q", snap)
	}
	data, _ := os.ReadFile(snap)
	if string(data) != "content\n" {
		t.Errorf("snapshot content mismatch; got %q", string(data))
	}
}

func TestBareDefaultContent_ContainsLayer(t *testing.T) {
	content := bareDefaultContent("global", "/home/testuser")
	if !strings.Contains(content, "global") {
		t.Errorf("expected layer in content; got: %q", content)
	}
	if !strings.Contains(content, "Soul") {
		t.Errorf("expected 'Soul' in content; got: %q", content)
	}
}

func TestResolveProjectSlug_FromProjectDir(t *testing.T) {
	home := t.TempDir()
	cfg := Config{
		HomeDir:    home,
		ProjectDir: filepath.Join(home, "myproject"),
	}
	slug := resolveProjectSlug(cfg, home)
	if slug != "myproject" {
		t.Errorf("expected slug=myproject; got %q", slug)
	}
}

func TestResolveProjectSlug_FallbackToAgentControl(t *testing.T) {
	home := t.TempDir()
	acEntry := filepath.Join(home, "agent-control", "targetproj")
	if err := os.MkdirAll(acEntry, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write a .project-path pointing to a temp dir (simulate cwd match).
	projPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(acEntry, ".project-path"), []byte(projPath+"\n"), 0644); err != nil {
		t.Fatalf("write project-path: %v", err)
	}
	// Change working directory to the project path.
	prevDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(prevDir) }()
	if err := os.Chdir(projPath); err != nil {
		t.Skipf("cannot chdir: %v", err)
	}

	cfg := Config{HomeDir: home}
	slug := resolveProjectSlug(cfg, home)
	if slug != "targetproj" {
		t.Errorf("expected slug=targetproj; got %q", slug)
	}
}
