package agentscompose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- splitFrontmatter -------------------------------------------------------

func TestSplitFrontmatter_WithFrontmatter(t *testing.T) {
	content := "---\nid: backend\nmodel: sonnet\n---\n\nBody text here.\n"
	fm, body := splitFrontmatter(content)
	if !strings.Contains(fm, "model: sonnet") {
		t.Errorf("frontmatter should contain 'model: sonnet', got %q", fm)
	}
	if !strings.Contains(body, "Body text here") {
		t.Errorf("body should contain 'Body text here', got %q", body)
	}
}

func TestSplitFrontmatter_NoFrontmatter(t *testing.T) {
	content := "Just prose\nno frontmatter"
	fm, body := splitFrontmatter(content)
	if fm != "" {
		t.Errorf("expected empty frontmatter, got %q", fm)
	}
	if body != content {
		t.Errorf("expected body == content, got %q", body)
	}
}

func TestSplitFrontmatter_EmptyFile(t *testing.T) {
	fm, body := splitFrontmatter("")
	if fm != "" || body != "" {
		t.Errorf("empty file: expected empty fm and body, got fm=%q body=%q", fm, body)
	}
}

// ---- parseFrontmatter -------------------------------------------------------

func TestParseFrontmatter_BasicFields(t *testing.T) {
	fm := "id: backend\nmodel: sonnet\nextends: base-agent"
	fields := parseFrontmatter(fm)
	if fields["id"] != "backend" {
		t.Errorf("id: got %q, want %q", fields["id"], "backend")
	}
	if fields["model"] != "sonnet" {
		t.Errorf("model: got %q, want %q", fields["model"], "sonnet")
	}
	if fields["extends"] != "base-agent" {
		t.Errorf("extends: got %q, want %q", fields["extends"], "base-agent")
	}
}

func TestParseFrontmatter_ToolsList(t *testing.T) {
	fm := "tools: [Read, Edit, Bash]"
	fields := parseFrontmatter(fm)
	if fields["tools"] != "[Read, Edit, Bash]" {
		t.Errorf("tools: got %q, want %q", fields["tools"], "[Read, Edit, Bash]")
	}
}

// ---- parseToolsList ---------------------------------------------------------

func TestParseToolsList_Valid(t *testing.T) {
	tools := parseToolsList("[Read, Edit, Bash]")
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d: %v", len(tools), tools)
	}
	if tools[0] != "Read" || tools[1] != "Edit" || tools[2] != "Bash" {
		t.Errorf("unexpected tools: %v", tools)
	}
}

func TestParseToolsList_Empty(t *testing.T) {
	tools := parseToolsList("")
	if tools != nil {
		t.Errorf("empty: expected nil, got %v", tools)
	}
}

func TestParseToolsList_NotList(t *testing.T) {
	tools := parseToolsList("Read")
	if tools != nil {
		t.Errorf("non-list: expected nil, got %v", tools)
	}
}

// ---- deriveDescription ------------------------------------------------------

func TestDeriveDescription_PurposeSection(t *testing.T) {
	body := "# Backend specialist\n\n## Purpose\n\nImplement server-side application code.\n\n## More"
	got := deriveDescription(body)
	if got != "Implement server-side application code." {
		t.Errorf("got %q, want %q", got, "Implement server-side application code.")
	}
}

func TestDeriveDescription_NoPurpose(t *testing.T) {
	body := "First prose line.\n\nSecond prose line."
	got := deriveDescription(body)
	if got != "First prose line." {
		t.Errorf("got %q, want %q", got, "First prose line.")
	}
}

func TestDeriveDescription_Truncates(t *testing.T) {
	long := strings.Repeat("x", 300)
	body := "## Purpose\n\n" + long
	got := deriveDescription(body)
	if len(got) != 200 {
		t.Errorf("expected length 200, got %d", len(got))
	}
}

// ---- Compose ----------------------------------------------------------------

// buildFixtureAgents creates a temporary yakOS root with lib/agents/ containing
// the given agents. Each entry: (filename, content). Returns yakosRoot.
func buildFixtureAgents(t *testing.T, agents map[string]string) string {
	t.Helper()
	root := t.TempDir()
	agentsDir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", agentsDir, err)
	}
	for name, content := range agents {
		path := filepath.Join(agentsDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

func TestCompose_FrameworkOnly(t *testing.T) {
	root := buildFixtureAgents(t, map[string]string{
		"backend.md": "---\nid: backend\nmodel: sonnet\n---\n\n## Purpose\n\nBackend specialist.\n",
		"README.md":  "ignored",
	})

	roster, err := Compose(root, "")
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(roster) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(roster))
	}
	if roster[0].ID != "backend" {
		t.Errorf("ID: got %q, want %q", roster[0].ID, "backend")
	}
	if roster[0].Model != "sonnet" {
		t.Errorf("Model: got %q, want %q", roster[0].Model, "sonnet")
	}
}

func TestCompose_ProjectOverridesFramework(t *testing.T) {
	root := buildFixtureAgents(t, map[string]string{
		"backend.md": "---\nid: backend\nmodel: haiku\n---\n\n## Purpose\n\nFramework backend.\n",
	})

	// Project agent with same id "backend" but different model.
	proj := t.TempDir()
	projAgentsDir := filepath.Join(proj, ".claude", "agents")
	if err := os.MkdirAll(projAgentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projAgentsDir, "backend.md"),
		[]byte("---\nid: backend\nmodel: opus\n---\n\n## Purpose\n\nProject backend.\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	roster, err := Compose(root, proj)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	// Should have exactly one backend agent (project wins).
	var backendAgent *ComposedAgent
	for i := range roster {
		if roster[i].ID == "backend" {
			backendAgent = &roster[i]
		}
	}
	if backendAgent == nil {
		t.Fatal("backend agent not found in roster")
	}
	if backendAgent.Model != "opus" {
		t.Errorf("Project agent should override framework: Model=%q, want %q", backendAgent.Model, "opus")
	}
	if !strings.Contains(backendAgent.Prompt, "Project backend") {
		t.Errorf("Project body should win: Prompt=%q", backendAgent.Prompt)
	}
}

func TestCompose_AliasExpansion(t *testing.T) {
	root := buildFixtureAgents(t, map[string]string{
		"agent.md": "---\nid: agent\nmodel: balanced\n---\n\n## Purpose\n\nTest agent.\n",
	})

	roster, err := Compose(root, "")
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(roster) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(roster))
	}
	// "balanced" should expand to "sonnet".
	if roster[0].Model != "sonnet" {
		t.Errorf("alias 'balanced' should expand to 'sonnet', got %q", roster[0].Model)
	}
}

func TestCompose_SkipsLeadTemplate(t *testing.T) {
	root := buildFixtureAgents(t, map[string]string{
		"lead-template.md": "---\nid: lead\n---\n\nlead body",
		"backend.md":       "---\nid: backend\n---\n\n## Purpose\n\nbackend",
	})

	roster, err := Compose(root, "")
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for _, a := range roster {
		if a.ID == "lead-template" {
			t.Error("lead-template.md should be skipped")
		}
	}
}

func TestCompose_ExtendsResolution(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Framework base agent.
	if err := os.WriteFile(filepath.Join(agentsDir, "base.md"),
		[]byte("---\nid: base\n---\n\nBase body.\n"), 0644); err != nil {
		t.Fatalf("write base.md: %v", err)
	}
	// Project agent that extends base.
	proj := t.TempDir()
	projAgentsDir := filepath.Join(proj, ".claude", "agents")
	if err := os.MkdirAll(projAgentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projAgentsDir, "specialist.md"),
		[]byte("---\nid: specialist\nextends: base\n---\n\nProject body.\n"), 0644); err != nil {
		t.Fatalf("write specialist.md: %v", err)
	}

	roster, err := Compose(root, proj)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	var specialist *ComposedAgent
	for i := range roster {
		if roster[i].ID == "specialist" {
			specialist = &roster[i]
		}
	}
	if specialist == nil {
		t.Fatal("specialist agent not found")
	}
	// Should contain both base body and project body.
	if !strings.Contains(specialist.Prompt, "Base body") {
		t.Errorf("extended agent should contain base body; prompt=%q", specialist.Prompt)
	}
	if !strings.Contains(specialist.Prompt, "Project body") {
		t.Errorf("extended agent should contain project body; prompt=%q", specialist.Prompt)
	}
}

// ---- MaterializePluginDir ---------------------------------------------------

func TestMaterializePluginDir_Basic(t *testing.T) {
	roster := []ComposedAgent{
		{
			ID:          "backend",
			Description: "Backend specialist",
			Prompt:      "You are a backend specialist.",
			Tools:       []string{"Read", "Edit", "Bash"},
			Model:       "sonnet",
		},
		{
			ID:          "security-reviewer",
			Description: "Security reviewer",
			Prompt:      "You review code for security issues.",
		},
	}

	outDir := t.TempDir()
	count, err := MaterializePluginDir(roster, outDir)
	if err != nil {
		t.Fatalf("MaterializePluginDir: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 agents written, got %d", count)
	}

	// Verify manifest exists and has correct name.
	manifestPath := filepath.Join(outDir, ".claude-plugin", "plugin.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifestData), `"yakos"`) {
		t.Errorf("manifest should contain 'yakos' name; got %s", manifestData)
	}

	// Verify backend.md exists and contains expected frontmatter.
	backendPath := filepath.Join(outDir, "agents", "backend.md")
	backendData, err := os.ReadFile(backendPath)
	if err != nil {
		t.Fatalf("read backend.md: %v", err)
	}
	backendContent := string(backendData)
	if !strings.Contains(backendContent, "name: backend") {
		t.Errorf("backend.md should contain 'name: backend'; got %s", backendContent)
	}
	if !strings.Contains(backendContent, "model: sonnet") {
		t.Errorf("backend.md should contain 'model: sonnet'; got %s", backendContent)
	}
	if !strings.Contains(backendContent, "tools: Read, Edit, Bash") {
		t.Errorf("backend.md should contain tools line; got %s", backendContent)
	}
	if !strings.Contains(backendContent, "You are a backend specialist") {
		t.Errorf("backend.md should contain prompt; got %s", backendContent)
	}
}

func TestMaterializePluginDir_IsIdempotent(t *testing.T) {
	roster := []ComposedAgent{
		{ID: "backend", Description: "Backend", Prompt: "Backend prompt."},
	}
	outDir := t.TempDir()

	// First materialization.
	if _, err := MaterializePluginDir(roster, outDir); err != nil {
		t.Fatalf("first MaterializePluginDir: %v", err)
	}

	// Second materialization should not error and should produce the same output.
	if _, err := MaterializePluginDir(roster, outDir); err != nil {
		t.Fatalf("second MaterializePluginDir: %v", err)
	}

	agentPath := filepath.Join(outDir, "agents", "backend.md")
	if _, err := os.Stat(agentPath); err != nil {
		t.Errorf("backend.md should exist after second materialization: %v", err)
	}
}

// ---- AgentToJSON ------------------------------------------------------------

func TestAgentToJSON_Basic(t *testing.T) {
	agent := ComposedAgent{
		ID:          "backend",
		Description: "Backend specialist",
		Prompt:      "You are a backend specialist.",
		Tools:       []string{"Read", "Edit"},
		Model:       "sonnet",
	}

	jsonStr, err := AgentToJSON(agent)
	if err != nil {
		t.Fatalf("AgentToJSON: %v", err)
	}
	if !strings.Contains(jsonStr, `"backend"`) {
		t.Errorf("JSON should contain agent ID; got %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"description"`) {
		t.Errorf("JSON should contain description; got %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"prompt"`) {
		t.Errorf("JSON should contain prompt; got %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"model":"sonnet"`) {
		t.Errorf("JSON should contain model:sonnet; got %s", jsonStr)
	}
}

func TestAgentToJSON_NoTools(t *testing.T) {
	agent := ComposedAgent{
		ID:          "agent",
		Description: "Test",
		Prompt:      "Prompt.",
	}
	jsonStr, err := AgentToJSON(agent)
	if err != nil {
		t.Fatalf("AgentToJSON: %v", err)
	}
	if strings.Contains(jsonStr, `"tools"`) {
		t.Errorf("JSON should not contain tools when nil; got %s", jsonStr)
	}
}
