package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

// tempHome creates a temp directory acting as HOME and returns it.
func tempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

// makeControlDir creates ~/agent-control/<project>/ and returns the home dir.
func makeControlDir(t *testing.T, home, project string) string {
	t.Helper()
	controlDir := filepath.Join(home, "agent-control", project)
	if err := os.MkdirAll(controlDir, 0755); err != nil {
		t.Fatalf("makeControlDir: %v", err)
	}
	return controlDir
}

// makeCurrent creates ~/agent-control/<project>/work/current/ and returns the
// path to work/current.
func makeCurrent(t *testing.T, home, project string) string {
	t.Helper()
	currentDir := filepath.Join(home, "agent-control", project, "work", "current")
	if err := os.MkdirAll(currentDir, 0755); err != nil {
		t.Fatalf("makeCurrent: %v", err)
	}
	return currentDir
}

// seedHistory writes the given events as NDJSON to the session-started-history file.
func seedHistory(t *testing.T, currentDir string, events []Event) {
	t.Helper()
	histPath := filepath.Join(currentDir, ".session-started-history.ndjson")
	f, err := os.Create(histPath)
	if err != nil {
		t.Fatalf("seedHistory create: %v", err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			t.Fatalf("seedHistory encode: %v", err)
		}
	}
}

// sampleEvents returns a slice of synthetic session events for testing.
func sampleEvents() []Event {
	return []Event{
		{
			Type:           "session_launched",
			TS:             "2026-05-01T10:00:00Z",
			Project:        "testproj",
			Repo:           "/tmp/repos/testproj",
			Runtime:        "claude",
			PermissionMode: "bypass",
			AgentCount:     3,
		},
		{
			Type:           "session_launched",
			TS:             "2026-05-02T12:00:00Z",
			Project:        "testproj",
			Repo:           "/tmp/repos/testproj",
			Runtime:        "codex",
			PermissionMode: "safe",
			AgentCount:     2,
		},
		{
			Type:           "session_launched",
			TS:             "2026-05-03T14:00:00Z",
			Project:        "testproj",
			Repo:           "/tmp/repos/testproj",
			Runtime:        "claude",
			PermissionMode: "bypass",
			AgentCount:     5,
		},
	}
}

// captureRun runs the session command and captures stdout+stderr.
func captureRun(t *testing.T, cfg Config) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cfg.Writer = &out
	cfg.ErrWriter = &errOut
	res, err := Run(cfg)
	_ = res
	return out.String(), errOut.String(), err
}

// ---- TestReadHistory --------------------------------------------------------

func TestReadHistory_Empty(t *testing.T) {
	events, err := readHistory("/nonexistent/path/file.ndjson")
	if err != nil {
		t.Fatalf("expected nil error for nonexistent file; got %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected empty slice; got %d events", len(events))
	}
}

func TestReadHistory_ValidLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.ndjson")
	evs := sampleEvents()
	seedHistory(t, dir, evs)
	// Rename to the expected name.
	_ = os.Rename(filepath.Join(dir, ".session-started-history.ndjson"), path)

	got, err := readHistory(path)
	if err != nil {
		t.Fatalf("readHistory: %v", err)
	}
	if len(got) != len(evs) {
		t.Fatalf("len mismatch: got %d, want %d", len(got), len(evs))
	}
	for i, ev := range got {
		if ev.TS != evs[i].TS {
			t.Errorf("event[%d].TS: got %q, want %q", i, ev.TS, evs[i].TS)
		}
		if ev.Runtime != evs[i].Runtime {
			t.Errorf("event[%d].Runtime: got %q, want %q", i, ev.Runtime, evs[i].Runtime)
		}
	}
}

func TestReadHistory_SkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.ndjson")
	content := `{"type":"session_launched","ts":"2026-05-01T10:00:00Z","runtime":"claude"}
not-json-at-all
{"type":"session_launched","ts":"2026-05-02T10:00:00Z","runtime":"codex"}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := readHistory(path)
	if err != nil {
		t.Fatalf("readHistory: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 valid events; got %d", len(got))
	}
}

// ---- TestResolveID ----------------------------------------------------------

func TestResolveID_EmptyIDReturnsLast(t *testing.T) {
	evs := sampleEvents()
	ev, idx, err := resolveID(evs, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := len(evs) - 1
	if idx != want {
		t.Errorf("idx: got %d, want %d", idx, want)
	}
	if ev.TS != evs[want].TS {
		t.Errorf("TS: got %q, want %q", ev.TS, evs[want].TS)
	}
}

func TestResolveID_NumericIndex(t *testing.T) {
	evs := sampleEvents()
	ev, idx, err := resolveID(evs, "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 1 {
		t.Errorf("idx: got %d, want 1", idx)
	}
	if ev.Runtime != "codex" {
		t.Errorf("Runtime: got %q, want codex", ev.Runtime)
	}
}

func TestResolveID_NumericIndexZero(t *testing.T) {
	evs := sampleEvents()
	ev, idx, err := resolveID(evs, "0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 0 {
		t.Errorf("idx: got %d, want 0", idx)
	}
	if ev.TS != evs[0].TS {
		t.Errorf("TS mismatch")
	}
}

func TestResolveID_NumericOutOfRange(t *testing.T) {
	evs := sampleEvents()
	_, _, err := resolveID(evs, "99")
	if err == nil {
		t.Fatal("expected error for out-of-range index")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("error should mention 'out of range'; got %q", err.Error())
	}
}

func TestResolveID_TSPrefix(t *testing.T) {
	evs := sampleEvents()
	ev, idx, err := resolveID(evs, "2026-05-02")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 1 {
		t.Errorf("idx: got %d, want 1", idx)
	}
	if ev.Runtime != "codex" {
		t.Errorf("Runtime: got %q, want codex", ev.Runtime)
	}
}

func TestResolveID_TSPrefixNoMatch(t *testing.T) {
	evs := sampleEvents()
	_, _, err := resolveID(evs, "2030-01-01")
	if err == nil {
		t.Fatal("expected error for non-matching prefix")
	}
	if !strings.Contains(err.Error(), "no session matching") {
		t.Errorf("error should say 'no session matching'; got %q", err.Error())
	}
}

func TestResolveID_EmptyEvents(t *testing.T) {
	_, _, err := resolveID(nil, "")
	if err == nil {
		t.Fatal("expected error for empty events")
	}
}

// ---- TestParseUint ----------------------------------------------------------

func TestParseUint_Valid(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want uint64
	}{
		{"0", 0},
		{"1", 1},
		{"42", 42},
		{"999", 999},
	} {
		got, ok := parseUint(tc.in)
		if !ok {
			t.Errorf("parseUint(%q): ok=false, want true", tc.in)
		}
		if got != tc.want {
			t.Errorf("parseUint(%q): got %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseUint_Invalid(t *testing.T) {
	for _, s := range []string{"", "abc", "-1", "1.5", "2026-05-01"} {
		_, ok := parseUint(s)
		if ok {
			t.Errorf("parseUint(%q): expected ok=false", s)
		}
	}
}

// ---- TestList ---------------------------------------------------------------

func TestList_MissingProject(t *testing.T) {
	home := tempHome(t)
	_, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "list",
		Project:    "no-such-project",
	})
	if err == nil {
		t.Fatal("expected error for missing project")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should say 'not found'; got %q", err.Error())
	}
}

func TestList_EmptyProject(t *testing.T) {
	home := tempHome(t)
	_, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "list",
		Project:    "",
	})
	if err == nil {
		t.Fatal("expected error for empty project")
	}
	if !strings.Contains(err.Error(), "<project> required") {
		t.Errorf("error should say '<project> required'; got %q", err.Error())
	}
}

func TestList_NoCurrent(t *testing.T) {
	home := tempHome(t)
	makeControlDir(t, home, "testproj")

	out, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "list",
		Project:    "testproj",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "testproj") {
		t.Errorf("output missing project name; got:\n%s", out)
	}
	if !strings.Contains(out, "no current session directory") {
		t.Errorf("output should note no current dir; got:\n%s", out)
	}
}

func TestList_WithCurrent(t *testing.T) {
	home := tempHome(t)
	currentDir := makeCurrent(t, home, "testproj")

	// seed decisions.md
	decPath := filepath.Join(currentDir, "decisions.md")
	if err := os.WriteFile(decPath, []byte("# Decisions\n\nfoo\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "list",
		Project:    "testproj",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "decisions.md") {
		t.Errorf("output should mention decisions.md; got:\n%s", out)
	}
}

func TestList_WithArchived(t *testing.T) {
	home := tempHome(t)
	makeControlDir(t, home, "testproj")
	archiveDir := filepath.Join(home, "agent-control", "testproj", "work", "archive", "sprint-1")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		t.Fatal(err)
	}

	out, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "list",
		Project:    "testproj",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "sprint-1") {
		t.Errorf("output should mention archived session 'sprint-1'; got:\n%s", out)
	}
}

func TestList_WithExports(t *testing.T) {
	home := tempHome(t)
	makeControlDir(t, home, "testproj")
	exportDir := filepath.Join(home, "agent-control", "testproj", "work", "exports")
	if err := os.MkdirAll(exportDir, 0755); err != nil {
		t.Fatal(err)
	}
	exportFile := filepath.Join(exportDir, "testproj-2026-05-01.tar.gz")
	if err := os.WriteFile(exportFile, []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}

	out, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "list",
		Project:    "testproj",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "testproj-2026-05-01.tar.gz") {
		t.Errorf("output should mention export; got:\n%s", out)
	}
}

// ---- TestInfo ---------------------------------------------------------------

func TestInfo_MissingProject(t *testing.T) {
	home := tempHome(t)
	_, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "info",
		Project:    "missing",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestInfo_NoHistory(t *testing.T) {
	home := tempHome(t)
	makeCurrent(t, home, "testproj")

	_, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "info",
		Project:    "testproj",
		ID:         "",
	})
	if err == nil {
		t.Fatal("expected error for empty history")
	}
	if !strings.Contains(err.Error(), "no sessions recorded") {
		t.Errorf("error should say 'no sessions recorded'; got %q", err.Error())
	}
}

func TestInfo_LastByDefault(t *testing.T) {
	home := tempHome(t)
	currentDir := makeCurrent(t, home, "testproj")
	evs := sampleEvents()
	seedHistory(t, currentDir, evs)

	out, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "info",
		Project:    "testproj",
		ID:         "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should show the last event (index 2, runtime=claude, agent_count=5).
	if !strings.Contains(out, "2026-05-03") {
		t.Errorf("expected ts from last event; got:\n%s", out)
	}
	if !strings.Contains(out, "5") {
		t.Errorf("expected agent_count=5; got:\n%s", out)
	}
}

func TestInfo_ByIndex(t *testing.T) {
	home := tempHome(t)
	currentDir := makeCurrent(t, home, "testproj")
	evs := sampleEvents()
	seedHistory(t, currentDir, evs)

	out, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "info",
		Project:    "testproj",
		ID:         "1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "codex") {
		t.Errorf("expected runtime=codex for index 1; got:\n%s", out)
	}
}

func TestInfo_ByTSPrefix(t *testing.T) {
	home := tempHome(t)
	currentDir := makeCurrent(t, home, "testproj")
	evs := sampleEvents()
	seedHistory(t, currentDir, evs)

	out, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "info",
		Project:    "testproj",
		ID:         "2026-05-01",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "3") { // agent_count=3
		t.Errorf("expected agent_count=3; got:\n%s", out)
	}
}

// ---- TestResume -------------------------------------------------------------

func TestResume_NoHistory(t *testing.T) {
	home := tempHome(t)
	makeCurrent(t, home, "testproj")

	_, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "resume",
		Project:    "testproj",
	})
	if err == nil {
		t.Fatal("expected error for empty history")
	}
}

func TestResume_PrintsStartFlags(t *testing.T) {
	home := tempHome(t)
	currentDir := makeCurrent(t, home, "testproj")
	seedHistory(t, currentDir, sampleEvents())

	out, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "resume",
		Project:    "testproj",
		ID:         "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should print yakos start command with --resume flag.
	if !strings.Contains(out, "yakos start testproj") {
		t.Errorf("output should contain 'yakos start testproj'; got:\n%s", out)
	}
	if !strings.Contains(out, "--resume") {
		t.Errorf("output should contain '--resume'; got:\n%s", out)
	}
}

func TestResume_SafeModePropagated(t *testing.T) {
	home := tempHome(t)
	currentDir := makeCurrent(t, home, "testproj")
	// Event 1 (index 1) has permission_mode=safe.
	seedHistory(t, currentDir, sampleEvents())

	out, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "resume",
		Project:    "testproj",
		ID:         "1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "--safe") {
		t.Errorf("safe mode session should include --safe in resume flags; got:\n%s", out)
	}
}

// ---- TestFork ---------------------------------------------------------------

func TestFork_NoHistory(t *testing.T) {
	home := tempHome(t)
	makeCurrent(t, home, "testproj")

	_, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "fork",
		Project:    "testproj",
	})
	if err == nil {
		t.Fatal("expected error for empty history")
	}
}

func TestFork_PrintsStartFlags(t *testing.T) {
	home := tempHome(t)
	currentDir := makeCurrent(t, home, "testproj")
	seedHistory(t, currentDir, sampleEvents())

	out, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "fork",
		Project:    "testproj",
		ID:         "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "yakos start testproj") {
		t.Errorf("output should contain 'yakos start testproj'; got:\n%s", out)
	}
	if !strings.Contains(out, "--fork-session") {
		t.Errorf("output should contain '--fork-session'; got:\n%s", out)
	}
}

// ---- TestUnknownSubcommand --------------------------------------------------

func TestUnknownSubcommand(t *testing.T) {
	home := tempHome(t)
	_, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "bogus",
		Project:    "proj",
	})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("error should say 'unknown subcommand'; got %q", err.Error())
	}
}

func TestEmptySubcommand(t *testing.T) {
	home := tempHome(t)
	_, _, err := captureRun(t, Config{
		HomeDir:    home,
		Subcommand: "",
		Project:    "proj",
	})
	if err == nil {
		t.Fatal("expected error for empty subcommand")
	}
	if !strings.Contains(err.Error(), "subcommand required") {
		t.Errorf("error should say 'subcommand required'; got %q", err.Error())
	}
}

// ---- TestPrintHelp ----------------------------------------------------------

func TestPrintHelp(t *testing.T) {
	var out bytes.Buffer
	PrintHelp(&out)
	got := out.String()

	mustContain := []string{
		"yakos session",
		"list",
		"info",
		"resume",
		"fork",
		"<project>",
		"session-started-history.ndjson",
		"Examples:",
	}
	for _, phrase := range mustContain {
		if !strings.Contains(got, phrase) {
			t.Errorf("help text missing phrase %q;\nfull output:\n%s", phrase, got)
		}
	}
}

// ---- TestHistoryPath --------------------------------------------------------

func TestHistoryPath(t *testing.T) {
	got := historyPath("/home/user/agent-control/myapp")
	want := filepath.FromSlash("/home/user/agent-control/myapp/work/current/.session-started-history.ndjson")
	if got != want {
		t.Errorf("historyPath: got %q, want %q", got, want)
	}
}
