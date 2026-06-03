package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- fixture helpers ----------------------------------------------------------

// buildFixture creates a temp directory tree with a lib/agents/ sub-dir
// containing the provided agent files (name → content). Returns the yakosRoot.
func buildFixture(t *testing.T, fwAgents map[string]string) string {
	t.Helper()
	root := t.TempDir()
	agentsDir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir lib/agents: %v", err)
	}
	for name, content := range fwAgents {
		if err := os.WriteFile(filepath.Join(agentsDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

// buildProjectDir creates <tmpDir>/.claude/agents/ with the given project
// agents and returns the project root.
func buildProjectDir(t *testing.T, projAgents map[string]string) string {
	t.Helper()
	proj := t.TempDir()
	agentsDir := filepath.Join(proj, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir .claude/agents: %v", err)
	}
	for name, content := range projAgents {
		if err := os.WriteFile(filepath.Join(agentsDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return proj
}

// runCapture runs Run with a bytes.Buffer as writer and returns its output.
func runCapture(t *testing.T, cfg Config) (string, *Result, error) {
	t.Helper()
	var buf bytes.Buffer
	cfg.Writer = &buf
	cfg.ErrWriter = &buf
	r, err := Run(cfg)
	return buf.String(), r, err
}

// ---- titleCaseName ------------------------------------------------------------

func TestTitleCaseName_Simple(t *testing.T) {
	if got := titleCaseName("backend"); got != "Backend" {
		t.Errorf("got %q, want %q", got, "Backend")
	}
}

func TestTitleCaseName_Kebab(t *testing.T) {
	if got := titleCaseName("my-backend"); got != "My Backend" {
		t.Errorf("got %q, want %q", got, "My Backend")
	}
}

func TestTitleCaseName_MultiWord(t *testing.T) {
	if got := titleCaseName("codex-helper-v2"); got != "Codex Helper V2" {
		t.Errorf("got %q, want %q", got, "Codex Helper V2")
	}
}

// ---- validateAgentName -------------------------------------------------------

func TestValidateAgentName_Valid(t *testing.T) {
	for _, name := range []string{"backend", "my-agent", "agent_v2", "A123"} {
		if err := validateAgentName(name); err != nil {
			t.Errorf("name %q should be valid; got error: %v", name, err)
		}
	}
}

func TestValidateAgentName_Invalid(t *testing.T) {
	for _, name := range []string{"my agent", "agent/bad", "agent.md"} {
		if err := validateAgentName(name); err == nil {
			t.Errorf("name %q should be invalid", name)
		}
	}
}

// ---- splitFrontmatter --------------------------------------------------------

func TestSplitFrontmatter_Basic(t *testing.T) {
	content := "---\nid: foo\n---\n\nBody here.\n"
	fm, body := splitFrontmatter(content)
	if !strings.Contains(fm, "id: foo") {
		t.Errorf("frontmatter should contain 'id: foo'; got %q", fm)
	}
	if !strings.Contains(body, "Body here") {
		t.Errorf("body should contain 'Body here'; got %q", body)
	}
}

func TestSplitFrontmatter_NoFrontmatter(t *testing.T) {
	content := "Just text\n"
	fm, body := splitFrontmatter(content)
	if fm != "" {
		t.Errorf("expected empty frontmatter; got %q", fm)
	}
	if body != content {
		t.Errorf("body should equal input; got %q", body)
	}
}

func TestSplitFrontmatter_EmptyFile(t *testing.T) {
	fm, body := splitFrontmatter("")
	if fm != "" || body != "" {
		t.Errorf("empty file: expected empty fm and body")
	}
}

// ---- parseFrontmatterSimple --------------------------------------------------

func TestParseFrontmatterSimple_Fields(t *testing.T) {
	fm := "id: backend\nrole: specialist\ndomain: api"
	fields := parseFrontmatterSimple(fm)
	if fields["id"] != "backend" {
		t.Errorf("id: got %q", fields["id"])
	}
	if fields["role"] != "specialist" {
		t.Errorf("role: got %q", fields["role"])
	}
	if fields["domain"] != "api" {
		t.Errorf("domain: got %q", fields["domain"])
	}
}

func TestParseFrontmatterSimple_List(t *testing.T) {
	fm := "tools: [Read, Edit, Bash]"
	fields := parseFrontmatterSimple(fm)
	if fields["tools"] != "[Read, Edit, Bash]" {
		t.Errorf("tools: got %q", fields["tools"])
	}
}

// ---- splitList ---------------------------------------------------------------

func TestSplitList_InlineYAML(t *testing.T) {
	parts := splitList("[claude, codex, gemini]")
	if len(parts) != 3 {
		t.Fatalf("expected 3; got %v", parts)
	}
	if parts[0] != "claude" || parts[1] != "codex" || parts[2] != "gemini" {
		t.Errorf("unexpected parts: %v", parts)
	}
}

func TestSplitList_Empty(t *testing.T) {
	parts := splitList("")
	if parts != nil {
		t.Errorf("expected nil; got %v", parts)
	}
}

func TestSplitList_SpaceSeparated(t *testing.T) {
	parts := splitList("claude codex")
	if len(parts) != 2 {
		t.Fatalf("expected 2; got %v", parts)
	}
}

// ---- hasPurposeSection -------------------------------------------------------

func TestHasPurposeSection_Present(t *testing.T) {
	body := "# Agent\n\n## Purpose\n\nDoes things.\n"
	if !hasPurposeSection(body) {
		t.Error("should detect ## Purpose")
	}
}

func TestHasPurposeSection_Absent(t *testing.T) {
	body := "# Agent\n\n## Execution\n\nDoes things.\n"
	if hasPurposeSection(body) {
		t.Error("should not detect ## Purpose")
	}
}

// ---- buildAgentTemplate -------------------------------------------------------

func TestBuildAgentTemplate_Basic(t *testing.T) {
	content := buildAgentTemplate("my-agent", "specialist", "api", "Read, Edit", "sonnet", "", "")
	if !strings.Contains(content, "id: my-agent") {
		t.Error("should contain id")
	}
	if !strings.Contains(content, "role: specialist") {
		t.Error("should contain role")
	}
	if !strings.Contains(content, "## Purpose") {
		t.Error("should contain ## Purpose")
	}
	if !strings.Contains(content, "## Execution") {
		t.Error("should contain ## Execution")
	}
	if !strings.Contains(content, "My Agent") {
		t.Error("should contain title-cased name")
	}
}

func TestBuildAgentTemplate_WithExtends(t *testing.T) {
	content := buildAgentTemplate("myagent", "specialist", "api", "Read", "sonnet", "backend", "")
	if !strings.Contains(content, "extends: backend") {
		t.Error("should contain extends field")
	}
}

func TestBuildAgentTemplate_WithRuntime(t *testing.T) {
	content := buildAgentTemplate("myagent", "specialist", "api", "Read", "sonnet", "", "codex")
	if !strings.Contains(content, "runtime: codex") {
		t.Error("should contain runtime field")
	}
}

func TestBuildAgentTemplate_ClaudeRuntimeOmitted(t *testing.T) {
	content := buildAgentTemplate("myagent", "specialist", "api", "Read", "sonnet", "", "claude")
	if strings.Contains(content, "runtime: claude") {
		t.Error("should omit runtime field when claude (default)")
	}
}

// ---- agent new ---------------------------------------------------------------

func TestRunNew_Basic(t *testing.T) {
	root := buildFixture(t, nil)
	proj := t.TempDir()

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "new",
		Name:       "my-agent",
		Project:    proj,
		Role:       "specialist",
		Domain:     "api",
		Model:      "sonnet",
		Tools:      "Read, Edit",
	}

	out, _, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	outFile := filepath.Join(proj, ".claude", "agents", "my-agent.md")
	if _, err := os.Stat(outFile); err != nil {
		t.Fatalf("output file not created: %v", err)
	}
	if !strings.Contains(out, "wrote "+outFile) {
		t.Errorf("expected 'wrote' line; got %q", out)
	}
}

func TestRunNew_AtomicWrite(t *testing.T) {
	// Verify no stale .tmp file after successful write.
	root := buildFixture(t, nil)
	proj := t.TempDir()

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "new",
		Name:       "atom-test",
		Project:    proj,
	}
	if _, _, err := runCapture(t, cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	tmpFile := filepath.Join(proj, ".claude", "agents", "atom-test.md.tmp")
	if _, err := os.Stat(tmpFile); err == nil {
		t.Error("temp file should be removed after successful write")
	}
}

func TestRunNew_ExistsNoForce(t *testing.T) {
	root := buildFixture(t, nil)
	proj := buildProjectDir(t, map[string]string{
		"existing.md": "---\nid: existing\n---\n\n# Body\n",
	})

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "new",
		Name:       "existing",
		Project:    proj,
	}
	_, _, err := runCapture(t, cfg)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' error; got %v", err)
	}
}

func TestRunNew_ExistsWithForce(t *testing.T) {
	root := buildFixture(t, nil)
	proj := buildProjectDir(t, map[string]string{
		"existing.md": "---\nid: existing\n---\n\n# Body\n",
	})

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "new",
		Name:       "existing",
		Project:    proj,
		Force:      true,
	}
	_, _, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("expected no error with --force; got %v", err)
	}
}

func TestRunNew_InvalidName(t *testing.T) {
	root := buildFixture(t, nil)
	proj := t.TempDir()
	cfg := Config{
		YakosRoot:  root,
		Subcommand: "new",
		Name:       "bad name",
		Project:    proj,
	}
	_, _, err := runCapture(t, cfg)
	if err == nil {
		t.Error("expected error for invalid name")
	}
}

func TestRunNew_UnknownRuntime(t *testing.T) {
	root := buildFixture(t, nil)
	proj := t.TempDir()
	cfg := Config{
		YakosRoot:  root,
		Subcommand: "new",
		Name:       "myagent",
		Project:    proj,
		Runtime:    "unknown-rt",
	}
	_, _, err := runCapture(t, cfg)
	if err == nil || !strings.Contains(err.Error(), "unknown runtime") {
		t.Errorf("expected 'unknown runtime' error; got %v", err)
	}
}

func TestRunNew_WithExtends(t *testing.T) {
	root := buildFixture(t, map[string]string{
		"backend.md": "---\nid: backend\nrole: specialist\ndomain: api\n---\n\n## Purpose\n\nBackend.\n",
	})
	proj := t.TempDir()

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "new",
		Name:       "mybackend",
		Project:    proj,
		Extends:    "backend",
	}
	_, _, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(proj, ".claude", "agents", "mybackend.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "extends: backend") {
		t.Error("file should contain 'extends: backend'")
	}
}

func TestRunNew_ExtendsNotFound(t *testing.T) {
	root := buildFixture(t, nil)
	proj := t.TempDir()
	cfg := Config{
		YakosRoot:  root,
		Subcommand: "new",
		Name:       "myagent",
		Project:    proj,
		Extends:    "nonexistent",
	}
	_, _, err := runCapture(t, cfg)
	if err == nil {
		t.Error("expected error for nonexistent extends target")
	}
}

func TestRunNew_DefaultValues(t *testing.T) {
	root := buildFixture(t, nil)
	proj := t.TempDir()

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "new",
		Name:       "defaulttest",
		Project:    proj,
	}
	_, _, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(proj, ".claude", "agents", "defaulttest.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "role: specialist") {
		t.Error("default role should be 'specialist'")
	}
	if !strings.Contains(content, "domain: cross-cutting") {
		t.Error("default domain should be 'cross-cutting'")
	}
	if !strings.Contains(content, "model: sonnet") {
		t.Error("default model should be 'sonnet'")
	}
}

// ---- lint --------------------------------------------------------------------

func TestRunLint_Clean(t *testing.T) {
	root := buildFixture(t, map[string]string{
		"backend.md": "---\nid: backend\nrole: specialist\ndomain: api\n---\n\n## Purpose\n\nDoes API things.\n",
	})
	proj := buildProjectDir(t, map[string]string{
		"myagent.md": "---\nid: myagent\nrole: specialist\ndomain: api\n---\n\n## Purpose\n\nMy agent.\n",
	})

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "lint",
		Project:    proj,
	}
	out, r, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Errors != 0 {
		t.Errorf("expected 0 errors; got %d\noutput: %s", r.Errors, out)
	}
	if !strings.Contains(out, "[ok]") {
		t.Errorf("expected [ok] in output; got %q", out)
	}
}

func TestRunLint_MissingRequiredField(t *testing.T) {
	root := buildFixture(t, nil)
	proj := buildProjectDir(t, map[string]string{
		// Missing 'role' and 'domain'.
		"badagent.md": "---\nid: badagent\n---\n\n## Purpose\n\nBad.\n",
	})

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "lint",
		Project:    proj,
	}
	out, r, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Errors == 0 {
		t.Errorf("expected errors for missing fields; got 0\noutput: %s", out)
	}
}

func TestRunLint_MissingPurposeSection(t *testing.T) {
	root := buildFixture(t, nil)
	proj := buildProjectDir(t, map[string]string{
		"nopurpose.md": "---\nid: nopurpose\nrole: specialist\ndomain: api\n---\n\n## Execution\n\nDoes things.\n",
	})

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "lint",
		Project:    proj,
	}
	out, r, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Warnings == 0 {
		t.Errorf("expected warning for missing Purpose section\noutput: %s", out)
	}
}

func TestRunLint_UnknownRuntime(t *testing.T) {
	root := buildFixture(t, nil)
	proj := buildProjectDir(t, map[string]string{
		"badrt.md": "---\nid: badrt\nrole: specialist\ndomain: api\nruntime: unknown-rt\n---\n\n## Purpose\n\nBad.\n",
	})

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "lint",
		Project:    proj,
	}
	out, r, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Errors == 0 {
		t.Errorf("expected error for unknown runtime\noutput: %s", out)
	}
}

func TestRunLint_ExtendsNotFound(t *testing.T) {
	root := buildFixture(t, nil) // no framework agents
	proj := buildProjectDir(t, map[string]string{
		"missingext.md": "---\nid: missingext\nrole: specialist\ndomain: api\nextends: nonexistent\n---\n\n## Purpose\n\nHello.\n",
	})

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "lint",
		Project:    proj,
	}
	out, r, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Errors == 0 {
		t.Errorf("expected error for missing extends target\noutput: %s", out)
	}
}

func TestRunLint_NoProjDir(t *testing.T) {
	root := buildFixture(t, nil)
	proj := t.TempDir() // no .claude/agents/

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "lint",
		Project:    proj,
	}
	out, r, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Errors != 0 {
		t.Errorf("expected 0 errors for empty project; got %d\noutput: %s", r.Errors, out)
	}
	if !strings.Contains(out, "nothing to lint") {
		t.Errorf("expected 'nothing to lint' message\noutput: %s", out)
	}
}

func TestRunLint_IgnoresReadmeMD(t *testing.T) {
	root := buildFixture(t, nil)
	proj := buildProjectDir(t, map[string]string{
		"README.md": "This is a readme, not an agent.",
	})

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "lint",
		Project:    proj,
	}
	out, r, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Errors != 0 {
		t.Errorf("README.md should be ignored; got %d errors\noutput: %s", r.Errors, out)
	}
}

func TestRunLint_RuntimeFallbackValid(t *testing.T) {
	root := buildFixture(t, nil)
	proj := buildProjectDir(t, map[string]string{
		"multi.md": "---\nid: multi\nrole: specialist\ndomain: api\nruntime-fallback: [claude, codex]\n---\n\n## Purpose\n\nMulti-runtime.\n",
	})

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "lint",
		Project:    proj,
	}
	_, r, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Errors != 0 {
		t.Errorf("expected 0 errors for valid runtime-fallback; got %d", r.Errors)
	}
}

// ---- diff --------------------------------------------------------------------

func TestRunDiff_NoExtends(t *testing.T) {
	root := buildFixture(t, nil)
	proj := buildProjectDir(t, map[string]string{
		"standalone.md": "---\nid: standalone\nrole: specialist\ndomain: api\n---\n\n## Purpose\n\nStandalone.\n",
	})

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "diff",
		Name:       "standalone",
		Project:    proj,
	}
	out, _, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "does not extend") {
		t.Errorf("expected 'does not extend' message; got %q", out)
	}
}

func TestRunDiff_WithExtends(t *testing.T) {
	root := buildFixture(t, map[string]string{
		"base.md": "---\nid: base\n---\n\n## Purpose\n\nBase purpose.\n",
	})
	proj := buildProjectDir(t, map[string]string{
		"specialist.md": "---\nid: specialist\nextends: base\nrole: specialist\ndomain: api\n---\n\n## Purpose\n\nProject purpose.\n",
	})

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "diff",
		Name:       "specialist",
		Project:    proj,
	}
	out, _, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "agent diff: specialist (extends: base)") {
		t.Errorf("expected diff header; got %q", out)
	}
}

func TestRunDiff_NotFound(t *testing.T) {
	root := buildFixture(t, nil)
	proj := t.TempDir()

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "diff",
		Name:       "nonexistent",
		Project:    proj,
	}
	_, _, err := runCapture(t, cfg)
	if err == nil {
		t.Error("expected error for nonexistent agent file")
	}
}

// ---- list --------------------------------------------------------------------

func TestRunList_Basic(t *testing.T) {
	root := buildFixture(t, map[string]string{
		"backend.md": "---\nid: backend\nmodel: sonnet\n---\n\n## Purpose\n\nBackend specialist.\n",
	})

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "list",
	}
	out, _, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "backend") {
		t.Errorf("expected 'backend' in output; got %q", out)
	}
	if !strings.Contains(out, "1 agent(s)") {
		t.Errorf("expected agent count; got %q", out)
	}
}

func TestRunList_JSON(t *testing.T) {
	root := buildFixture(t, map[string]string{
		"backend.md": "---\nid: backend\nmodel: sonnet\n---\n\n## Purpose\n\nBackend specialist.\n",
	})

	cfg := Config{
		YakosRoot:  root,
		Subcommand: "list",
		JSON:       true,
	}
	out, _, err := runCapture(t, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, `"id"`) {
		t.Errorf("expected JSON with 'id' field; got %q", out)
	}
	if !strings.Contains(out, `"backend"`) {
		t.Errorf("expected 'backend' in JSON; got %q", out)
	}
}

// ---- docs --------------------------------------------------------------------

func TestRenderDocsMD_Basic(t *testing.T) {
	root := buildFixture(t, map[string]string{
		"backend.md": "---\nid: backend\nmodel: sonnet\n---\n\n## Purpose\n\nBackend specialist.\n",
	})

	var buf bytes.Buffer
	err := RenderDocs(DocsConfig{
		YakosRoot: root,
		Format:    DocsFormatMD,
		Writer:    &buf,
	})
	if err != nil {
		t.Fatalf("RenderDocs: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "# yakOS Agent Reference") {
		t.Errorf("expected title; got %q", out)
	}
	if !strings.Contains(out, "`backend`") {
		t.Errorf("expected backend section; got %q", out)
	}
}

func TestRenderDocsHTML_Basic(t *testing.T) {
	root := buildFixture(t, map[string]string{
		"backend.md": "---\nid: backend\nmodel: sonnet\n---\n\n## Purpose\n\nBackend specialist.\n",
	})

	var buf bytes.Buffer
	err := RenderDocs(DocsConfig{
		YakosRoot: root,
		Format:    DocsFormatHTML,
		Writer:    &buf,
	})
	if err != nil {
		t.Fatalf("RenderDocs: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "<!DOCTYPE html>") {
		t.Errorf("expected HTML doctype; got %q", out)
	}
	if !strings.Contains(out, "<code>backend</code>") {
		t.Errorf("expected backend in HTML; got %q", out)
	}
}

func TestRenderDocsHTML_Escaping(t *testing.T) {
	if got := htmlEscape("<script>alert('x')</script>"); strings.Contains(got, "<script>") {
		t.Errorf("htmlEscape should escape < and >; got %q", got)
	}
}

// ---- unknown subcommand ------------------------------------------------------

func TestRun_UnknownSubcommand(t *testing.T) {
	cfg := Config{
		Subcommand: "bogus",
	}
	var buf bytes.Buffer
	cfg.Writer = &buf
	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error for unknown subcommand")
	}
}

// ---- lcsLineDiff + unifiedDiff -----------------------------------------------

func TestLcsLineDiff_Identical(t *testing.T) {
	lines := []string{"a", "b", "c"}
	edits := lcsLineDiff(lines, lines)
	for _, e := range edits {
		if e.kind != ' ' {
			t.Errorf("identical inputs should produce only context lines; got kind=%c text=%q", e.kind, e.text)
		}
	}
}

func TestLcsLineDiff_Insert(t *testing.T) {
	a := []string{"a", "c"}
	b := []string{"a", "b", "c"}
	edits := lcsLineDiff(a, b)
	var inserts int
	for _, e := range edits {
		if e.kind == '+' {
			inserts++
		}
	}
	if inserts != 1 {
		t.Errorf("expected 1 insert; got %d", inserts)
	}
}

func TestUnifiedDiff_NoDiff(t *testing.T) {
	s := "line1\nline2\nline3\n"
	if got := unifiedDiff(s, s); got != "" {
		t.Errorf("identical strings should produce empty diff; got %q", got)
	}
}

func TestUnifiedDiff_HasHunkHeader(t *testing.T) {
	a := "line1\nline2\nline3\n"
	b := "line1\nchanged\nline3\n"
	got := unifiedDiff(a, b)
	if !strings.Contains(got, "@@") {
		t.Errorf("diff should contain hunk header @@; got %q", got)
	}
}
