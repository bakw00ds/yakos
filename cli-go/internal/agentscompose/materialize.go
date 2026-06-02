package agentscompose

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaterializePluginDir writes the claude --plugin-dir layout under outDir.
//
// Layout (mirrors yk_rt_claude_write_plugin_dir in cli/lib/runtimes/claude.sh):
//
//	outDir/
//	  .claude-plugin/plugin.json     — manifest: {"name":"yakos","description":"..."}
//	  agents/<id>.md                 — one per ComposedAgent with native frontmatter
//
// The directory is wiped and rebuilt from scratch on each call (stale prevention).
// Returns the count of agent files written.
func MaterializePluginDir(roster []ComposedAgent, outDir string) (int, error) {
	// Wipe and recreate — prevents stale files from prior session.
	if err := os.RemoveAll(outDir); err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("materialize: remove %s: %w", outDir, err)
	}
	manifestDir := filepath.Join(outDir, ".claude-plugin")
	agentsDir := filepath.Join(outDir, "agents")
	for _, dir := range []string{manifestDir, agentsDir} {
		if err := os.MkdirAll(dir, 0755); err != nil { //nolint:gosec
			return 0, fmt.Errorf("materialize: mkdir %s: %w", dir, err)
		}
	}

	// Plugin manifest. Name "yakos" causes agents to register as
	// subagent_type "yakos:<id>" in the claude session.
	manifest := map[string]string{
		"name":        "yakos",
		"description": "yakOS agent roster (session-scoped; auto-generated)",
	}
	manifestBytes, _ := json.Marshal(manifest)
	manifestBytes = append(manifestBytes, '\n')
	manifestPath := filepath.Join(manifestDir, "plugin.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0644); err != nil { //nolint:gosec
		return 0, fmt.Errorf("materialize: write manifest: %w", err)
	}

	// Write one .md file per agent with native claude plugin frontmatter.
	for _, agent := range roster {
		if err := writeAgentFile(agentsDir, agent); err != nil {
			return 0, fmt.Errorf("materialize: write agent %s: %w", agent.ID, err)
		}
	}
	return len(roster), nil
}

// writeAgentFile writes a single agent .md file with claude plugin frontmatter.
//
// Frontmatter fields (per claude plugin spec):
//   - name: <id>
//   - description: <one-line>
//   - tools: <comma-separated> (optional)
//   - model: <tier> (optional)
func writeAgentFile(agentsDir string, agent ComposedAgent) error {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "name: %s\n", agent.ID)
	// Collapse newlines in description (must be single line in frontmatter).
	desc := strings.ReplaceAll(agent.Description, "\n", " ")
	fmt.Fprintf(&sb, "description: %s\n", desc)
	if len(agent.Tools) > 0 {
		fmt.Fprintf(&sb, "tools: %s\n", strings.Join(agent.Tools, ", "))
	}
	if agent.Model != "" {
		fmt.Fprintf(&sb, "model: %s\n", agent.Model)
	}
	sb.WriteString("---\n\n")
	sb.WriteString(agent.Prompt)
	sb.WriteByte('\n')

	path := filepath.Join(agentsDir, agent.ID+".md")
	return os.WriteFile(path, []byte(sb.String()), 0644) //nolint:gosec
}

// AgentToJSON converts a ComposedAgent to the single-agent JSON object format
// expected by claude's --agents flag:
//
//	{"<id>": {"description": "...", "prompt": "...", "tools": [...], "model": "..."}}
//
// The returned string is suitable for passing directly to ClaudeAdapter.Dispatch.
func AgentToJSON(agent ComposedAgent) (string, error) {
	inner := map[string]interface{}{
		"description": agent.Description,
		"prompt":      agent.Prompt,
	}
	if len(agent.Tools) > 0 {
		inner["tools"] = agent.Tools
	}
	if agent.Model != "" {
		inner["model"] = agent.Model
	}

	outer := map[string]interface{}{
		agent.ID: inner,
	}

	b, err := json.Marshal(outer)
	if err != nil {
		return "", fmt.Errorf("agentToJSON %s: %w", agent.ID, err)
	}
	return string(b), nil
}
