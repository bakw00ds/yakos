package team

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- isoTag -----------------------------------------------------------------

func TestIsoTag_Format(t *testing.T) {
	ts := time.Date(2026, 6, 2, 15, 4, 5, 0, time.UTC)
	got := isoTag(ts)
	want := "auto-20260602T150405Z"
	if got != want {
		t.Errorf("isoTag: got %q, want %q", got, want)
	}
}

func TestIsoTag_HasAutoPrefix(t *testing.T) {
	tag := isoTag(time.Now())
	if !strings.HasPrefix(tag, "auto-") {
		t.Errorf("isoTag should start with 'auto-'; got %q", tag)
	}
}

func TestIsoTag_AlphanumericSafe(t *testing.T) {
	tag := isoTag(time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC))
	for _, c := range tag {
		isLower := c >= 'a' && c <= 'z'
		isUpper := c >= 'A' && c <= 'Z'
		isDigit := c >= '0' && c <= '9'
		isDash := c == '-'
		if !isLower && !isUpper && !isDigit && !isDash {
			t.Errorf("isoTag contains non-alphanumeric/dash char %q in %q", c, tag)
		}
	}
}

func TestIsoTag_UniquePerSecond(t *testing.T) {
	ts1 := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC)
	ts2 := time.Date(2026, 6, 2, 10, 0, 1, 0, time.UTC)
	if isoTag(ts1) == isoTag(ts2) {
		t.Error("isoTag should differ for different seconds")
	}
}

// ---- trimNewline -------------------------------------------------------------

func TestTrimNewline_LF(t *testing.T) {
	if trimNewline("foo\n") != "foo" {
		t.Error("trimNewline should strip trailing LF")
	}
}

func TestTrimNewline_CRLF(t *testing.T) {
	if trimNewline("foo\r\n") != "foo" {
		t.Error("trimNewline should strip trailing CRLF")
	}
}

func TestTrimNewline_None(t *testing.T) {
	if trimNewline("foo") != "foo" {
		t.Error("trimNewline should not modify strings without trailing newline")
	}
}

func TestTrimNewline_Empty(t *testing.T) {
	if trimNewline("") != "" {
		t.Error("trimNewline should handle empty string")
	}
}

// ---- Restart: config validation and happy path ------------------------------

// buildControlDir creates a minimal agent-control/<project>/ structure in a
// temp home directory and returns (home, controlDir).
func buildControlDir(t *testing.T, project string) (home, controlDir string) {
	t.Helper()
	home = t.TempDir()
	controlDir = filepath.Join(home, "agent-control", project)
	if err := os.MkdirAll(controlDir, 0755); err != nil {
		t.Fatalf("mkdir controlDir: %v", err)
	}
	return home, controlDir
}

// mockArchiveFn is a test double for ArchiveFn that records calls.
type mockArchiveFn struct {
	calls []archiveCall
	err   error
}

type archiveCall struct {
	yakosRoot string
	project   string
	tag       string
}

func (m *mockArchiveFn) fn(yakosRoot, project, tag string) error {
	m.calls = append(m.calls, archiveCall{yakosRoot, project, tag})
	return m.err
}

func TestRestart_HappyPath(t *testing.T) {
	home, _ := buildControlDir(t, "myproject")
	mock := &mockArchiveFn{}
	var out bytes.Buffer

	cfg := Config{
		YakosRoot: "/fake/yakos",
		Project:   "myproject",
		HomeDir:   home,
		Now:       time.Date(2026, 6, 2, 10, 30, 0, 0, time.UTC),
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	res, err := Restart(cfg)
	if err != nil {
		t.Fatalf("Restart: unexpected error: %v", err)
	}

	if res.Project != "myproject" {
		t.Errorf("result.Project: got %q, want %q", res.Project, "myproject")
	}
	if res.Tag != "auto-20260602T103000Z" {
		t.Errorf("result.Tag: got %q, want %q", res.Tag, "auto-20260602T103000Z")
	}

	// Archive was called once with correct args.
	if len(mock.calls) != 1 {
		t.Fatalf("archive called %d times, want 1", len(mock.calls))
	}
	c := mock.calls[0]
	if c.yakosRoot != "/fake/yakos" {
		t.Errorf("archiveFn yakosRoot: got %q, want %q", c.yakosRoot, "/fake/yakos")
	}
	if c.project != "myproject" {
		t.Errorf("archiveFn project: got %q, want %q", c.project, "myproject")
	}
	if c.tag != "auto-20260602T103000Z" {
		t.Errorf("archiveFn tag: got %q, want %q", c.tag, "auto-20260602T103000Z")
	}
}

func TestRestart_ExplicitTag(t *testing.T) {
	home, _ := buildControlDir(t, "proj")
	mock := &mockArchiveFn{}
	var out bytes.Buffer

	cfg := Config{
		YakosRoot: "/y",
		Project:   "proj",
		Tag:       "release-1.0",
		HomeDir:   home,
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	res, err := Restart(cfg)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if res.Tag != "release-1.0" {
		t.Errorf("result.Tag: got %q, want %q", res.Tag, "release-1.0")
	}
	if mock.calls[0].tag != "release-1.0" {
		t.Errorf("archive tag: got %q, want %q", mock.calls[0].tag, "release-1.0")
	}
}

func TestRestart_MissingProject(t *testing.T) {
	home := t.TempDir() // no agent-control/<project>/ created
	mock := &mockArchiveFn{}
	var out bytes.Buffer

	cfg := Config{
		YakosRoot: "/y",
		Project:   "nonexistent",
		HomeDir:   home,
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	_, err := Restart(cfg)
	if err == nil {
		t.Fatal("Restart: expected error for missing project, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention project name; got %q", err.Error())
	}
	if len(mock.calls) != 0 {
		t.Error("archive should not be called when project is missing")
	}
}

func TestRestart_ArchiveError(t *testing.T) {
	home, _ := buildControlDir(t, "proj")
	archiveErr := errors.New("archive: disk full")
	mock := &mockArchiveFn{err: archiveErr}
	var out bytes.Buffer

	cfg := Config{
		YakosRoot: "/y",
		Project:   "proj",
		HomeDir:   home,
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	_, err := Restart(cfg)
	if err == nil {
		t.Fatal("Restart: expected error from archive, got nil")
	}
	if !strings.Contains(err.Error(), "archive failed") {
		t.Errorf("error should mention 'archive failed'; got %q", err.Error())
	}
}

func TestRestart_OutputContainsProject(t *testing.T) {
	home, _ := buildControlDir(t, "myproject")
	mock := &mockArchiveFn{}
	var out bytes.Buffer

	cfg := Config{
		YakosRoot: "/y",
		Project:   "myproject",
		HomeDir:   home,
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	if _, err := Restart(cfg); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "myproject") {
		t.Errorf("output should mention project name; got:\n%s", output)
	}
}

func TestRestart_OutputContainsRelaunhHint(t *testing.T) {
	home, _ := buildControlDir(t, "proj")
	mock := &mockArchiveFn{}
	var out bytes.Buffer

	cfg := Config{
		YakosRoot: "/y",
		Project:   "proj",
		HomeDir:   home,
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	if _, err := Restart(cfg); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "yakos start proj") {
		t.Errorf("output should contain 'yakos start proj'; got:\n%s", output)
	}
}

func TestRestart_ProjectPathFile(t *testing.T) {
	home, controlDir := buildControlDir(t, "proj")
	// Write a .project-path file.
	ppFile := filepath.Join(controlDir, ".project-path")
	if err := os.WriteFile(ppFile, []byte("/Users/alice/code/proj\n"), 0644); err != nil {
		t.Fatalf("write .project-path: %v", err)
	}

	mock := &mockArchiveFn{}
	var out bytes.Buffer

	cfg := Config{
		YakosRoot: "/y",
		Project:   "proj",
		HomeDir:   home,
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	res, err := Restart(cfg)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if res.ProjectPath != "/Users/alice/code/proj" {
		t.Errorf("result.ProjectPath: got %q, want %q", res.ProjectPath, "/Users/alice/code/proj")
	}
	if !strings.Contains(out.String(), "/Users/alice/code/proj") {
		t.Errorf("output should contain project path; got:\n%s", out.String())
	}
}

func TestRestart_NoProjectPathFile(t *testing.T) {
	home, _ := buildControlDir(t, "proj")
	mock := &mockArchiveFn{}
	var out bytes.Buffer

	cfg := Config{
		YakosRoot: "/y",
		Project:   "proj",
		HomeDir:   home,
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	res, err := Restart(cfg)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}

	// When .project-path is absent, a generic placeholder should be used.
	if res.ProjectPath != "<path-to-project-repo>" {
		t.Errorf("result.ProjectPath without .project-path: got %q, want placeholder", res.ProjectPath)
	}
}

func TestRestart_WriterDefaultsToStdout(t *testing.T) {
	home, _ := buildControlDir(t, "proj")
	mock := &mockArchiveFn{}

	// Writer is nil: should use os.Stdout without panicking.
	cfg := Config{
		YakosRoot: "/y",
		Project:   "proj",
		HomeDir:   home,
		Writer:    nil, // intentionally nil
		ArchiveFn: mock.fn,
	}

	// Redirect stdout to /dev/null for this test so we don't pollute test output.
	origStdout := os.Stdout
	null, _ := os.Open(os.DevNull)
	os.Stdout = null
	defer func() {
		os.Stdout = origStdout
		_ = null.Close()
	}()

	_, err := Restart(cfg)
	if err != nil {
		t.Fatalf("Restart with nil Writer: %v", err)
	}
}

func TestRestart_AutoTagFormat(t *testing.T) {
	home, _ := buildControlDir(t, "proj")
	mock := &mockArchiveFn{}
	var out bytes.Buffer

	fixedNow := time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC)
	cfg := Config{
		YakosRoot: "/y",
		Project:   "proj",
		HomeDir:   home,
		Now:       fixedNow,
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	res, err := Restart(cfg)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}

	// Tag must match bash's ct_iso_utc format: "auto-" + YYYYMMDDTHHMMSSz
	want := "auto-20260115T080000Z"
	if res.Tag != want {
		t.Errorf("auto-tag: got %q, want %q", res.Tag, want)
	}
}

func TestRestart_OutputContainsTag(t *testing.T) {
	home, _ := buildControlDir(t, "proj")
	mock := &mockArchiveFn{}
	var out bytes.Buffer

	cfg := Config{
		YakosRoot: "/y",
		Project:   "proj",
		Tag:       "sprint-42",
		HomeDir:   home,
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	if _, err := Restart(cfg); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if !strings.Contains(out.String(), "sprint-42") {
		t.Errorf("output should contain tag 'sprint-42'; got:\n%s", out.String())
	}
}

func TestRestart_ArchiveCalledOnce(t *testing.T) {
	home, _ := buildControlDir(t, "proj")
	mock := &mockArchiveFn{}
	var out bytes.Buffer

	cfg := Config{
		YakosRoot: "/y",
		Project:   "proj",
		HomeDir:   home,
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	if _, err := Restart(cfg); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if len(mock.calls) != 1 {
		t.Errorf("archive called %d times, want exactly 1", len(mock.calls))
	}
}

func TestRestart_OutputTeamArchivedLine(t *testing.T) {
	home, _ := buildControlDir(t, "proj")
	mock := &mockArchiveFn{}
	var out bytes.Buffer

	cfg := Config{
		YakosRoot: "/y",
		Project:   "proj",
		HomeDir:   home,
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	if _, err := Restart(cfg); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if !strings.Contains(out.String(), "Team archived") {
		t.Errorf("output should contain 'Team archived'; got:\n%s", out.String())
	}
}

func TestRestart_ResultProjectMatchesInput(t *testing.T) {
	home, _ := buildControlDir(t, "alpha-project")
	mock := &mockArchiveFn{}
	var out bytes.Buffer

	cfg := Config{
		YakosRoot: "/y",
		Project:   "alpha-project",
		HomeDir:   home,
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	res, err := Restart(cfg)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if res.Project != "alpha-project" {
		t.Errorf("result.Project: got %q, want %q", res.Project, "alpha-project")
	}
}

func TestRestart_HomeDefaultsFromEnv(t *testing.T) {
	home := t.TempDir()
	controlDir := filepath.Join(home, "agent-control", "envproj")
	if err := os.MkdirAll(controlDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Set HOME env to the temp dir.
	t.Setenv("HOME", home)

	mock := &mockArchiveFn{}
	var out bytes.Buffer

	// HomeDir is empty — should fall back to os.Getenv("HOME").
	cfg := Config{
		YakosRoot: "/y",
		Project:   "envproj",
		HomeDir:   "",
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	_, err := Restart(cfg)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
}

func TestRestart_TagPassedToArchive(t *testing.T) {
	home, _ := buildControlDir(t, "proj")
	mock := &mockArchiveFn{}
	var out bytes.Buffer

	cfg := Config{
		YakosRoot: "/yakos-root",
		Project:   "proj",
		Tag:       "hotfix-2026-06-01",
		HomeDir:   home,
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	if _, err := Restart(cfg); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if mock.calls[0].yakosRoot != "/yakos-root" {
		t.Errorf("archive yakosRoot: got %q, want %q", mock.calls[0].yakosRoot, "/yakos-root")
	}
	if mock.calls[0].tag != "hotfix-2026-06-01" {
		t.Errorf("archive tag: got %q, want %q", mock.calls[0].tag, "hotfix-2026-06-01")
	}
}

// TestRestart_ControlDirPath verifies the control dir path is derived as
// $HOME/agent-control/<project> and mentioned in error messages.
func TestRestart_ControlDirPath(t *testing.T) {
	home := t.TempDir()
	// Deliberately do NOT create the project control dir.

	mock := &mockArchiveFn{}
	var out bytes.Buffer

	cfg := Config{
		YakosRoot: "/y",
		Project:   "no-such-project",
		HomeDir:   home,
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	_, err := Restart(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	expectedPath := filepath.Join(home, "agent-control", "no-such-project")
	if !strings.Contains(err.Error(), expectedPath) {
		t.Errorf("error should contain control dir path %q; got %q", expectedPath, err.Error())
	}
}

func TestRestart_YakosRootPassedToArchive(t *testing.T) {
	home, _ := buildControlDir(t, "proj")
	mock := &mockArchiveFn{}
	var out bytes.Buffer

	cfg := Config{
		YakosRoot: "/custom/yakos",
		Project:   "proj",
		HomeDir:   home,
		Writer:    &out,
		ArchiveFn: mock.fn,
	}

	if _, err := Restart(cfg); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if mock.calls[0].yakosRoot != "/custom/yakos" {
		t.Errorf("archive got yakosRoot=%q, want %q", mock.calls[0].yakosRoot, "/custom/yakos")
	}
}
