package doctor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

// makeTmpHome creates a temporary HOME directory with a minimal .claude/ tree.
func makeTmpHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude", "projects"), 0755); err != nil {
		t.Fatalf("makeTmpHome: %v", err)
	}
	return dir
}

// writeFile creates a file at path with the given content, making parent dirs.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("writeFile mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
}

// noLookPath always returns "not found" for every command.
func noLookPath(cmd string) (string, error) {
	return "", &os.PathError{Op: "lookpath", Path: cmd, Err: os.ErrNotExist}
}

// singleLookPath returns a path only for the named commands.
func singleLookPath(found map[string]string) func(string) (string, error) {
	return func(cmd string) (string, error) {
		if p, ok := found[cmd]; ok {
			return p, nil
		}
		return "", &os.PathError{Op: "lookpath", Path: cmd, Err: os.ErrNotExist}
	}
}

// ---- required commands -------------------------------------------------------

func TestRequiredCommands_AllMissing(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)
	cfg := Config{
		HomeDir:  home,
		LookPath: noLookPath,
		Environ:  func(string) string { return "" },
		Writer:   &buf,
	}
	report, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// All three required commands absent → 3 errors.
	if report.Errors < 3 {
		t.Errorf("expected ≥3 errors for missing bash/git/jq; got %d\noutput:\n%s", report.Errors, buf.String())
	}
	out := buf.String()
	for _, cmd := range []string{"bash", "git", "jq"} {
		if !strings.Contains(out, "[err]") || !strings.Contains(out, cmd+": not found in PATH") {
			t.Errorf("expected [err] for %s missing; output:\n%s", cmd, out)
		}
	}
}

func TestRequiredCommands_AllPresent(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)
	cfg := Config{
		HomeDir: home,
		LookPath: singleLookPath(map[string]string{
			"bash": "/bin/bash",
			"git":  "/usr/bin/git",
			"jq":   "/usr/bin/jq",
		}),
		Environ: func(string) string { return "" },
		Writer:  &buf,
	}
	Run(cfg) //nolint:errcheck
	out := buf.String()
	if !strings.Contains(out, "  [ok]   bash: /bin/bash") {
		t.Errorf("expected [ok] for bash; output:\n%s", out)
	}
	// Verify the required commands section has no [err] lines for bash/git/jq.
	// (Other sections may still produce errors for missing .yakos pointer etc.)
	lines := strings.Split(out, "\n")
	inReqSection := false
	for _, line := range lines {
		if line == "Required commands" {
			inReqSection = true
			continue
		}
		if inReqSection && line == "" {
			break // blank line ends the section
		}
		if inReqSection && strings.Contains(line, "[err]") {
			t.Errorf("unexpected [err] in Required commands section: %q\nfull output:\n%s", line, out)
		}
	}
}

// ---- install pointer ---------------------------------------------------------

func TestInstallPointer_Missing(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)
	cfg := Config{
		HomeDir:  home,
		LookPath: noLookPath,
		Environ:  func(string) string { return "" },
		Writer:   &buf,
	}
	Run(cfg) //nolint:errcheck
	out := buf.String()
	if !strings.Contains(out, "Install pointer") {
		t.Errorf("Install pointer section missing; output:\n%s", out)
	}
	if !strings.Contains(out, ".yakos does not exist") {
		t.Errorf("expected '.yakos does not exist'; output:\n%s", out)
	}
}

func TestInstallPointer_PointsToValidRepo(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)

	// Create a fake repo with a lib/ directory.
	fakeRepo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fakeRepo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".yakos"), fakeRepo+"\n")

	cfg := Config{
		HomeDir:  home,
		LookPath: noLookPath,
		Environ:  func(string) string { return "" },
		Writer:   &buf,
	}
	Run(cfg) //nolint:errcheck
	out := buf.String()
	if !strings.Contains(out, "[ok]") || !strings.Contains(out, "→") {
		t.Errorf("expected [ok] with arrow for valid pointer; output:\n%s", out)
	}
}

func TestInstallPointer_PointsToNonExistentDir(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)
	writeFile(t, filepath.Join(home, ".yakos"), "/does/not/exist\n")

	cfg := Config{
		HomeDir:  home,
		LookPath: noLookPath,
		Environ:  func(string) string { return "" },
		Writer:   &buf,
	}
	report, _ := Run(cfg)
	if !strings.Contains(buf.String(), "[err]") {
		t.Errorf("expected [err] for nonexistent pointer target; output:\n%s", buf.String())
	}
	if report.Errors == 0 {
		t.Error("expected errors > 0")
	}
}

// ---- settings.json ----------------------------------------------------------

func TestSettingsJSON_ValidWithAgentTeams(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)

	settings := map[string]any{
		"env": map[string]any{
			"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "1",
		},
	}
	data, _ := json.Marshal(settings)
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), string(data))

	cfg := Config{
		HomeDir:  home,
		LookPath: noLookPath,
		Environ:  func(string) string { return "" },
		Writer:   &buf,
	}
	report, _ := Run(cfg)
	out := buf.String()
	if !strings.Contains(out, "is valid JSON") {
		t.Errorf("expected 'is valid JSON'; output:\n%s", out)
	}
	if !strings.Contains(out, "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS = 1") {
		t.Errorf("expected agent teams ok; output:\n%s", out)
	}
	// No warnings from settings section.
	if report.Warnings > 0 {
		// Could be from other sections; just verify content.
		if strings.Contains(out, "agent teams disabled") {
			t.Errorf("unexpected 'agent teams disabled' warning; output:\n%s", out)
		}
	}
}

func TestSettingsJSON_MissingAgentTeams(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)

	settings := map[string]any{"env": map[string]any{}}
	data, _ := json.Marshal(settings)
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), string(data))

	cfg := Config{
		HomeDir:  home,
		LookPath: noLookPath,
		Environ:  func(string) string { return "" },
		Writer:   &buf,
	}
	report, _ := Run(cfg)
	if !strings.Contains(buf.String(), "agent teams disabled") {
		t.Errorf("expected 'agent teams disabled' warning; output:\n%s", buf.String())
	}
	if report.Warnings == 0 {
		t.Error("expected at least 1 warning")
	}
}

func TestSettingsJSON_InvalidJSON(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), "not json {{{")

	cfg := Config{
		HomeDir:  home,
		LookPath: noLookPath,
		Environ:  func(string) string { return "" },
		Writer:   &buf,
	}
	report, _ := Run(cfg)
	if !strings.Contains(buf.String(), "is not valid JSON") {
		t.Errorf("expected 'is not valid JSON'; output:\n%s", buf.String())
	}
	if report.Errors == 0 {
		t.Error("expected errors > 0 for invalid JSON")
	}
}

// ---- summary line ------------------------------------------------------------

func TestSummaryLine(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)
	cfg := Config{
		HomeDir:  home,
		LookPath: noLookPath,
		Environ:  func(string) string { return "" },
		Writer:   &buf,
	}
	Run(cfg) //nolint:errcheck
	out := buf.String()
	if !strings.Contains(out, "Summary:") {
		t.Errorf("expected 'Summary:' line; output:\n%s", out)
	}
}

// ---- hook drift -------------------------------------------------------------

func TestHookDrift_CleanAndDrifted(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)
	projectPath := t.TempDir()
	hooksDir := filepath.Join(projectPath, "scripts", "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a clean hook (hash matches).
	cleanHook := filepath.Join(hooksDir, "clean-hook.sh")
	writeFile(t, cleanHook, "#!/bin/bash\necho clean\n")
	cleanHash, _ := sha256File(cleanHook)
	writeFile(t, cleanHook+".framework-hash", cleanHash+"\n")

	// Write a drifted hook (hash does not match).
	driftedHook := filepath.Join(hooksDir, "drifted-hook.sh")
	writeFile(t, driftedHook, "#!/bin/bash\necho original\n")
	writeFile(t, driftedHook+".framework-hash", "deadbeef1234deadbeef1234deadbeef1234deadbeef1234deadbeef1234dead\n")

	// Write an unhashed hook (no sidecar).
	unhashedHook := filepath.Join(hooksDir, "unhashed-hook.sh")
	writeFile(t, unhashedHook, "#!/bin/bash\necho unhashed\n")

	cfg := Config{
		HomeDir:     home,
		ProjectPath: projectPath,
		LookPath:    noLookPath,
		Environ:     func(string) string { return "" },
		Writer:      &buf,
	}
	Run(cfg) //nolint:errcheck
	out := buf.String()

	if !strings.Contains(out, "DRIFT:") {
		t.Errorf("expected DRIFT notice for drifted hook; output:\n%s", out)
	}
	if !strings.Contains(out, "1 clean") {
		t.Errorf("expected '1 clean'; output:\n%s", out)
	}
	if !strings.Contains(out, "1 drifted") {
		t.Errorf("expected '1 drifted'; output:\n%s", out)
	}
	if !strings.Contains(out, "1 unhashed") {
		t.Errorf("expected '1 unhashed'; output:\n%s", out)
	}
	// Drift is informational, not an error: the DRIFT: lines should be [info], not [err].
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, "DRIFT:") && strings.Contains(line, "[err]") {
			t.Errorf("drift should be [info] not [err]: %q\nfull output:\n%s", line, out)
		}
	}
}

// ---- API keys ---------------------------------------------------------------

func TestAPIKeys_Set(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)
	cfg := Config{
		HomeDir:  home,
		LookPath: noLookPath,
		Environ: func(key string) string {
			if key == "OPENAI_API_KEY" {
				return "sk-test-123"
			}
			return ""
		},
		Writer: &buf,
	}
	Run(cfg) //nolint:errcheck
	out := buf.String()
	if !strings.Contains(out, "OPENAI_API_KEY: set") {
		t.Errorf("expected 'OPENAI_API_KEY: set'; output:\n%s", out)
	}
	if !strings.Contains(out, "ANTHROPIC_API_KEY: not set") {
		t.Errorf("expected 'ANTHROPIC_API_KEY: not set'; output:\n%s", out)
	}
}

// ---- auto-memory ------------------------------------------------------------

func TestAutoMemory_WithProjects(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)
	// Create a couple of project dirs.
	for _, p := range []string{"proj-a", "proj-b"} {
		if err := os.MkdirAll(filepath.Join(home, ".claude", "projects", p), 0755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := Config{
		HomeDir:  home,
		LookPath: noLookPath,
		Environ:  func(string) string { return "" },
		Writer:   &buf,
	}
	Run(cfg) //nolint:errcheck
	out := buf.String()
	if !strings.Contains(out, "2 project dir(s)") {
		t.Errorf("expected '2 project dir(s)'; output:\n%s", out)
	}
	if !strings.Contains(out, "projects/") {
		t.Errorf("expected trailing slash on projects/ path; output:\n%s", out)
	}
}

// ---- optional commands -------------------------------------------------------

func TestOptionalCommands_NonePresent(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)
	cfg := Config{
		HomeDir:  home,
		LookPath: noLookPath,
		Environ:  func(string) string { return "" },
		Writer:   &buf,
	}
	Run(cfg) //nolint:errcheck
	out := buf.String()
	// All optional commands absent → info messages, no errors.
	if !strings.Contains(out, "not present (compat handles macOS BSD fallback)") {
		t.Errorf("expected compat fallback message; output:\n%s", out)
	}
}

// ---- section ordering -------------------------------------------------------

func TestSectionOrdering(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)
	cfg := Config{
		HomeDir:  home,
		LookPath: noLookPath,
		Environ:  func(string) string { return "" },
		Writer:   &buf,
	}
	Run(cfg) //nolint:errcheck
	out := buf.String()

	// Verify sections appear in the correct order.
	sections := []string{
		"Required commands",
		"Optional commands",
		"Install pointer",
		"YakOS symlinks under",
		"settings.json",
		"Auto-memory",
		"Multi-dev coord",
		"Optional local/cross-model tooling",
		"External provider API keys",
		"Summary:",
	}
	prev := 0
	for _, section := range sections {
		idx := strings.Index(out, section)
		if idx < 0 {
			t.Errorf("section %q not found in output", section)
			continue
		}
		if idx < prev {
			t.Errorf("section %q appears before %q (out of order)", section, sections[prev])
		}
		prev = idx
	}
}

// ---- probe-runtime ----------------------------------------------------------

func TestProbeRuntime_NoProbeFile(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)
	cfg := Config{
		HomeDir:      home,
		LookPath:     noLookPath,
		Environ:      func(string) string { return "" },
		Writer:       &buf,
		ProbeRuntime: true,
	}
	Run(cfg) //nolint:errcheck
	out := buf.String()
	if !strings.Contains(out, "Claude Code runtime feature probe") {
		t.Errorf("expected runtime probe section; output:\n%s", out)
	}
	if !strings.Contains(out, "no probe file found") {
		t.Errorf("expected 'no probe file found'; output:\n%s", out)
	}
	if !strings.Contains(out, "TaskCreate / TaskList / TaskUpdate: NOT AVAILABLE") {
		t.Errorf("expected NOT AVAILABLE fallback; output:\n%s", out)
	}
}

func TestProbeRuntime_WithProbeFile(t *testing.T) {
	var buf bytes.Buffer
	home := makeTmpHome(t)
	stateDir := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	probe := map[string]any{
		"timestamp":           "2026-05-01T10:00:00Z",
		"claude_code_version": "1.2.3",
		"tools": map[string]string{
			"TaskCreate": "available",
			"TaskList":   "unavailable",
			"TaskUpdate": "unavailable",
		},
	}
	data, _ := json.Marshal(probe)
	writeFile(t, filepath.Join(stateDir, "runtime-probe.json"), string(data))

	cfg := Config{
		HomeDir:      home,
		LookPath:     noLookPath,
		Environ:      func(string) string { return "" },
		Writer:       &buf,
		ProbeRuntime: true,
	}
	Run(cfg) //nolint:errcheck
	out := buf.String()
	if !strings.Contains(out, "TaskCreate: AVAILABLE") {
		t.Errorf("expected TaskCreate: AVAILABLE; output:\n%s", out)
	}
	if !strings.Contains(out, "TaskList: NOT AVAILABLE") {
		t.Errorf("expected TaskList: NOT AVAILABLE; output:\n%s", out)
	}
	if !strings.Contains(out, "Last probe: 2026-05-01T10:00:00Z") {
		t.Errorf("expected Last probe timestamp; output:\n%s", out)
	}
}

// ---- helper unit tests ------------------------------------------------------

func TestExtractFrontmatter(t *testing.T) {
	tests := []struct {
		content string
		want    string
		hasFM   bool
	}{
		{
			content: "---\ntools: []\nname: test\n---\n# Body",
			want:    "tools: []\nname: test",
			hasFM:   true,
		},
		{
			content: "# No frontmatter",
			want:    "",
			hasFM:   false,
		},
		{
			content: "---\ntools: [Read]\n---",
			want:    "tools: [Read]",
			hasFM:   true,
		},
	}
	for _, tc := range tests {
		got := extractFrontmatter(tc.content)
		if tc.hasFM && got == "" {
			t.Errorf("extractFrontmatter(%q) = empty; want non-empty", tc.content)
		}
		if !tc.hasFM && got != "" {
			t.Errorf("extractFrontmatter(%q) = %q; want empty", tc.content, got)
		}
		if tc.want != "" && !strings.Contains(got, strings.Split(tc.want, "\n")[0]) {
			t.Errorf("extractFrontmatter(%q) = %q; want to contain %q", tc.content, got, tc.want)
		}
	}
}

func TestIsEmptyToolsList(t *testing.T) {
	if !isEmptyToolsList("tools: []") {
		t.Error("expected true for 'tools: []'")
	}
	if isEmptyToolsList("tools: [Read, Write]") {
		t.Error("expected false for non-empty tools list")
	}
	if isEmptyToolsList("no tools key") {
		t.Error("expected false for missing tools key")
	}
}

func TestHasBudgetBlock(t *testing.T) {
	if !hasBudgetBlock("budget:\n  max: 10\n") {
		t.Error("expected true for 'budget:' key")
	}
	if hasBudgetBlock("supervisor:\n  enabled: true\n") {
		t.Error("expected false when no budget key")
	}
}

func TestParseSupervisorBlock(t *testing.T) {
	content := `
supervisor:
  enabled: true
  block_on_critical: true
  other: value
`
	enabled, blockCritical := parseSupervisorBlock(content)
	if !enabled {
		t.Error("expected enabled=true")
	}
	if !blockCritical {
		t.Error("expected blockOnCritical=true")
	}

	content2 := "supervisor:\n  enabled: true\n  block_on_critical: false\n"
	e2, b2 := parseSupervisorBlock(content2)
	if !e2 {
		t.Error("expected e2=true")
	}
	if b2 {
		t.Error("expected b2=false")
	}
}

func TestHasActiveBypass_NoActiveEntries(t *testing.T) {
	tmp := t.TempDir()
	// This file has a "## bypass:" line under Active entries → counts as active.
	path := filepath.Join(tmp, "hook-bypass.md")
	writeFile(t, path, "# Hook Bypass\n\n## Active entries\n\n## bypass: (no active)\n\n## Past entries\n")
	if !hasActiveBypass(path) {
		t.Error("expected hasActiveBypass=true for file with '## bypass:' line under Active entries")
	}

	// This file has no "## bypass:" lines → no active bypass.
	path2 := filepath.Join(tmp, "hook-bypass2.md")
	writeFile(t, path2, "# Hook Bypass\n\n## Active entries\n\n_(none)_\n\n## Past entries\n")
	if hasActiveBypass(path2) {
		t.Error("expected no active bypass in file without bypass: lines")
	}
}

// ---- exit code via report ---------------------------------------------------

func TestExitCode_ErrorsPresent(t *testing.T) {
	home := makeTmpHome(t)
	// Make settings.json invalid to guarantee an error.
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), "<<<not json>>>")
	cfg := Config{
		HomeDir:  home,
		LookPath: noLookPath,
		Environ:  func(string) string { return "" },
		Writer:   &bytes.Buffer{},
	}
	report, _ := Run(cfg)
	if report.Errors == 0 {
		t.Error("expected errors > 0 for invalid settings.json")
	}
}
