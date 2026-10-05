// Package agentscompose is the Go port of cli/lib/agents-compose.sh.
//
// It produces a composed agent roster from framework agents (lib/agents/*.md)
// and project agents (<project>/.claude/agents/*.md), resolving `extends:`
// inheritance and giving project agents precedence on id collision.
//
// The composed roster is used by the dispatch orchestrator to materialize the
// --plugin-dir layout for the claude adapter (PR #15) and equivalent agent
// file layouts for codex and agy.
//
// ComposeSkills is the analogous composer for framework skills
// (lib/skills/<slug>/SKILL.md) and project skill overrides
// (<project>/.claude/skills/<slug>/SKILL.md).
package agentscompose

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/bakw00ds/yakos/internal/runtime"
)

// genericAgentPrompt is the minimal system prompt used when dispatching to a
// bare runtime name (e.g. "claude", "codex", "gemini") that has no specialist
// .md file in the composed roster. It provides a functional but intentionally
// minimal persona — no specialist constraints, no yakOS-specific lore.
const genericAgentPrompt = "You are a helpful AI assistant. Answer the user's request clearly and concisely."

// IsKnownRuntime reports whether name equals a known yakOS runtime identifier
// (claude, codex, agy). Used by the dispatch layer to decide whether a
// missing agent name should resolve to a generic catch-all rather than error.
func IsKnownRuntime(name string) bool {
	for _, r := range runtime.Known {
		if r == name {
			return true
		}
	}
	return false
}

// GenericAgentForRuntime returns a minimal ComposedAgent that runs on the
// named runtime with no specialist system prompt. It is used when the caller
// requests a bare runtime name ("claude", "codex", etc.) that has no
// corresponding agent .md file in the composed roster.
//
// The returned agent's ID equals name.  Its Runtime is left empty on purpose:
// the dispatch layer infers the runtime from the agent ID (an agent named
// after a runtime runs on it unless the caller overrides), see
// dispatch.resolve.go.  Model is left empty so the runtime picks its default.
//
// Callers must verify IsKnownRuntime(name) before calling this function; it
// panics on an unknown name to surface programming errors early.
func GenericAgentForRuntime(name string) ComposedAgent {
	if !IsKnownRuntime(name) {
		panic("agentscompose: GenericAgentForRuntime called with unknown runtime: " + name)
	}
	return ComposedAgent{
		ID:          name,
		Description: "Generic " + name + " agent (no specialist persona)",
		Prompt:      genericAgentPrompt,
	}
}

// ComposedAgent is a single resolved agent ready for materialization.
type ComposedAgent struct {
	// ID is the canonical agent identifier (filename stem, e.g. "backend").
	ID string

	// Description is a short one-line description derived from the ## Purpose
	// section or the first non-blank prose line. Max 200 chars.
	Description string

	// Prompt is the full system prompt body (after extends: resolution).
	Prompt string

	// Tools is the list of tool names from the frontmatter tools: field.
	// Empty slice means no tool restriction.
	Tools []string

	// Model is the resolved concrete Claude tier name (haiku|sonnet|opus|fable)
	// or "". Empty means the runtime picks its default. It is "" for a model
	// that is not a Claude tier (gpt-5, gemini-3.5); ModelRaw carries that id.
	// AgentToJSON reads only this field, which keeps the claude --agents
	// payload byte-stable (rule:cache-stability).
	Model string

	// ---- Routing fields (K-132) ---------------------------------------------
	//
	// Read from frontmatter so the dispatch layer can route on them. None of
	// them is part of the claude --agents JSON (AgentToJSON ignores them), so
	// adding or changing one never changes a cached prefix.

	// Runtime is the agent's pinned runtime (frontmatter `runtime:`), or "".
	// Only the id shape is checked here; whether the runtime exists and is
	// usable is the dispatch layer's call (yakos validate reports bad values).
	Runtime string

	// RuntimeFallback is the ordered frontmatter `runtime-fallback:` list. Order
	// is meaningful: the first available entry wins.
	RuntimeFallback []string

	// Domain is the frontmatter `domain:` tag, used for .yakos.yml per-domain
	// runtime rules.
	Domain string

	// ModelRaw is the frontmatter `model:` scalar as written (quotes and a
	// trailing comment removed), before alias expansion and tier validation. It
	// is how a non-Claude model id survives composition: `model: gpt-5` leaves
	// Model empty and ModelRaw "gpt-5".
	ModelRaw string

	// ModelPolicy is the frontmatter `model-policy:` value, unresolved. Parsed
	// for the router; dispatch does not apply it yet.
	ModelPolicy string

	// MaxCostPerTask (USD), MaxTokensPerTask and MaxDurationS are the
	// frontmatter `max-cost-per-task:`, `max-tokens-per-task:` and
	// `max-duration-s:` ceilings. 0 means unset (also for an unparsable,
	// negative or non-finite value). Parsed for the router; dispatch does not
	// enforce them yet.
	MaxCostPerTask   float64
	MaxTokensPerTask int
	MaxDurationS     int
}

// Compose walks lib/agents/*.md and <project>/.claude/agents/*.md, parses
// frontmatter, resolves extends:, and returns the fully composed roster.
// Project agents override framework agents on id collision (same semantics as
// agents-compose.sh:yk_agents_compose).
//
// Skips README.md and lead-template.md (template files not addressable as
// subagent_type).
func Compose(yakosRoot, project string) ([]ComposedAgent, error) {
	fwDir := filepath.Join(yakosRoot, "lib", "agents")
	projDir := ""
	if project != "" {
		candidate := filepath.Join(project, ".claude", "agents")
		if stat, err := os.Stat(candidate); err == nil && stat.IsDir() {
			projDir = candidate
		}
	}

	// Index by id: framework first, then project overrides.
	index := make(map[string]ComposedAgent)
	var order []string // tracks insertion order for stable output

	addDir := func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("agentscompose: read dir %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			base := e.Name()
			switch base {
			case "README.md", "lead-template.md":
				continue
			}
			id := strings.TrimSuffix(base, ".md")
			path := filepath.Join(dir, base)

			// An agent named after a runtime would shadow the generic agent of
			// that name, which is what `yakos dispatch codex` and the console's
			// default pane (agent claude, runtime auto) resolve to. A cloned
			// project could use it to send those to another vendor with a
			// frontmatter runtime: pin (sec-324 F4), so it is skipped.
			if IsKnownRuntime(id) {
				warnRuntimeNamedAgent(path, id)
				continue
			}

			agent, err := parseAgent(yakosRoot, id, path)
			if err != nil {
				return fmt.Errorf("agentscompose: parse %s: %w", path, err)
			}
			if _, exists := index[id]; !exists {
				order = append(order, id)
			}
			index[id] = agent
		}
		return nil
	}

	if err := addDir(fwDir); err != nil {
		return nil, err
	}
	if projDir != "" {
		if err := addDir(projDir); err != nil {
			return nil, err
		}
	}

	result := make([]ComposedAgent, 0, len(order))
	for _, id := range order {
		result = append(result, index[id])
	}
	return result, nil
}

// WarnWriter receives the notices Compose prints. Tests replace it.
var WarnWriter io.Writer = os.Stderr

// warnedPaths remembers which skipped files were already reported, so a daemon
// that composes the roster on every request says it once per file, not once per
// request.
var warnedPaths sync.Map

func warnRuntimeNamedAgent(path, id string) {
	if _, seen := warnedPaths.LoadOrStore(path, struct{}{}); seen {
		return
	}
	fmt.Fprintf(WarnWriter, "yakos: WARN: ignoring agent file %s: %q is a runtime name and would shadow the runtime's own agent; rename it\n", path, id)
}

// parseAgent reads, parses, and resolves a single agent .md file.
func parseAgent(yakosRoot, id, path string) (ComposedAgent, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return ComposedAgent{}, fmt.Errorf("read: %w", err)
	}
	content := string(data)

	fm, body := splitFrontmatter(content)
	fields := parseFrontmatter(fm)

	// Resolve extends: inheritance — prepend framework template body.
	if extendsName := fields["extends"]; extendsName != "" {
		fwFile := filepath.Join(yakosRoot, "lib", "agents", extendsName+".md")
		fwData, err := os.ReadFile(fwFile) //nolint:gosec
		if err == nil {
			_, fwBody := splitFrontmatter(string(fwData))
			body = fwBody + "\n\n---\n\n" + body
		}
		// If the framework file doesn't exist, use the project body alone
		// (matches agents-compose.sh:yk_agents_resolve_extends behavior).
	}

	// Model alias expansion (PR #32/#39): translate semantic aliases to concrete tiers.
	model := runtime.ResolveAlias(fields["model"])
	if model != "" && !runtime.ValidateTier(model) {
		// Unknown model after alias resolution: omit (matches bash WARN + omit).
		model = ""
	}

	// Tool parsing.
	tools := parseToolsList(fields["tools"])

	// Description: derive from ## Purpose section.
	desc := deriveDescription(body)
	if desc == "" {
		desc = "Agent: " + id
	}

	return ComposedAgent{
		ID:          id,
		Description: desc,
		Prompt:      strings.TrimSpace(body),
		Tools:       tools,
		Model:       model,

		// Routing fields (K-132). The model above is resolved from the raw
		// frontmatter string exactly as before; ModelRaw is the same value
		// with quotes and a trailing comment removed.
		Runtime:          fmRuntimeID(fields["runtime"]),
		RuntimeFallback:  fmRuntimeList(fields["runtime-fallback"]),
		Domain:           fmScalar(fields["domain"]),
		ModelRaw:         fmScalar(fields["model"]),
		ModelPolicy:      fmScalar(fields["model-policy"]),
		MaxCostPerTask:   fmFloat(fields["max-cost-per-task"]),
		MaxTokensPerTask: fmInt(fields["max-tokens-per-task"]),
		MaxDurationS:     fmInt(fields["max-duration-s"]),
	}, nil
}

// runtimeIDRe is the shape of a runtime identifier taken from frontmatter. It
// is deliberately the same alphabet as projectcfg's: the value reaches log
// lines and error text, so nothing outside [a-z0-9._-] is kept.
var runtimeIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// fmScalar returns a frontmatter scalar as YAML would read it for the simple
// `key: value` lines parseFrontmatter produces: surrounding whitespace, one
// pair of matching quotes and a trailing ` # comment` are removed.
func fmScalar(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if q := v[0]; q == '"' || q == '\'' {
		// Quoted: the value ends at the closing quote; anything after it
		// (a comment) is dropped.
		if end := strings.IndexByte(v[1:], q); end >= 0 {
			return v[1 : 1+end]
		}
		return strings.TrimSpace(v[1:])
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	} else if v[0] == '#' {
		return ""
	}
	return strings.TrimSpace(v)
}

// fmRuntimeID returns raw as a runtime id, or "" when it is empty or not shaped
// like one.
func fmRuntimeID(raw string) string {
	v := fmScalar(raw)
	if !runtimeIDRe.MatchString(v) {
		return ""
	}
	return v
}

// fmRuntimeList parses an inline `[a, b]` runtime list, keeping order, dropping
// entries that are not runtime ids and repeated entries.
func fmRuntimeList(raw string) []string {
	items := parseToolsList(stripTrailingComment(raw))
	if len(items) == 0 {
		return nil
	}
	var out []string
	seen := make(map[string]struct{}, len(items))
	for _, it := range items {
		id := fmRuntimeID(it)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// stripTrailingComment removes a ` # comment` that follows an inline list.
func stripTrailingComment(raw string) string {
	v := strings.TrimSpace(raw)
	if i := strings.LastIndex(v, "]"); i >= 0 {
		return v[:i+1]
	}
	return v
}

// fmFloat parses a non-negative, finite frontmatter number; anything else is 0.
func fmFloat(raw string) float64 {
	f, err := strconv.ParseFloat(fmScalar(raw), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0
	}
	return f
}

// fmInt parses a non-negative frontmatter integer; anything else is 0.
func fmInt(raw string) int {
	n, err := strconv.Atoi(fmScalar(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// splitFrontmatter splits a markdown file into (frontmatter, body).
// frontmatter is the YAML between the opening and closing --- markers.
// body is everything after the closing ---.
// If there is no frontmatter, frontmatter is "" and body is the full content.
func splitFrontmatter(content string) (frontmatter, body string) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", content
	}

	// Find the closing ---.
	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx < 0 {
		return "", content
	}

	fm := strings.Join(lines[1:closeIdx], "\n")
	bd := strings.Join(lines[closeIdx+1:], "\n")
	return fm, bd
}

// parseFrontmatter parses simple key: value YAML lines from frontmatter.
// Returns a map of key → raw value. Lists (tools: [a, b]) are stored as-is.
// This is a simplified parser matching agents-compose.sh:yk_agents_fm_get.
func parseFrontmatter(fm string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(fm, "\n") {
		idx := strings.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if key != "" {
			result[key] = val
		}
	}
	return result
}

// parseToolsList parses a YAML inline list string like "[Read, Edit, Bash]"
// into a slice of tool names. Returns nil if the value is not a list.
func parseToolsList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw[0] != '[' || raw[len(raw)-1] != ']' {
		return nil
	}
	inner := raw[1 : len(raw)-1]
	parts := strings.Split(inner, ",")
	tools := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			tools = append(tools, t)
		}
	}
	return tools
}

// deriveDescription extracts the first non-blank line under "## Purpose" or,
// if no Purpose section exists, the first non-blank prose line. Truncated to
// 200 chars. Mirrors yk_agents_derive_description.
func deriveDescription(body string) string {
	lines := strings.Split(body, "\n")
	inPurpose := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")) == "Purpose" {
				inPurpose = true
				continue
			}
			if inPurpose {
				break // left Purpose section without finding text
			}
			continue
		}
		if inPurpose && trimmed != "" {
			if len(trimmed) > 200 {
				return trimmed[:200]
			}
			return trimmed
		}
	}
	// No Purpose section: return first non-blank non-header line.
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if len(trimmed) > 200 {
				return trimmed[:200]
			}
			return trimmed
		}
	}
	return ""
}

// ComposedSkill is a single resolved skill entry for the /api/skills response.
type ComposedSkill struct {
	// Slug is the directory name (e.g. "a11y-scan").
	Slug string

	// Name is from the frontmatter `name:` field, falling back to Slug.
	Name string

	// Description is from the frontmatter `description:` field.
	Description string

	// Source is "framework" or "project".
	Source string
}

// ComposeSkills walks lib/skills/<slug>/SKILL.md (framework skills) and
// <project>/.claude/skills/<slug>/SKILL.md (project skills), parses
// frontmatter for name and description, and returns a deterministically
// ordered slice (sorted by slug). Project skills override framework skills
// on slug collision, mirroring how Compose handles agent project-overrides.
//
// Returns an empty (non-nil) slice when either directory is absent — callers
// should not treat a missing skills dir as an error.
func ComposeSkills(yakosRoot, project string) ([]ComposedSkill, error) {
	fwDir := filepath.Join(yakosRoot, "lib", "skills")
	projDir := ""
	if project != "" {
		candidate := filepath.Join(project, ".claude", "skills")
		if stat, err := os.Stat(candidate); err == nil && stat.IsDir() {
			projDir = candidate
		}
	}

	// index by slug; source tracks whether it came from framework or project.
	type entry struct {
		skill  ComposedSkill
		source string // "framework" or "project"
	}
	index := make(map[string]entry)

	addSkillsDir := func(dir, source string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("agentscompose: read skills dir %s: %w", dir, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			slug := e.Name()
			skillPath := filepath.Join(dir, slug, "SKILL.md")
			data, err := os.ReadFile(skillPath) //nolint:gosec
			if err != nil {
				if os.IsNotExist(err) {
					continue // dir exists but no SKILL.md — skip silently
				}
				return fmt.Errorf("agentscompose: read %s: %w", skillPath, err)
			}

			fm, _ := splitFrontmatter(string(data))
			fields := parseFrontmatter(fm)

			name := fields["name"]
			if name == "" {
				name = slug
			}
			description := fields["description"]

			index[slug] = entry{
				skill: ComposedSkill{
					Slug:        slug,
					Name:        name,
					Description: description,
					Source:      source,
				},
				source: source,
			}
		}
		return nil
	}

	if err := addSkillsDir(fwDir, "framework"); err != nil {
		return nil, err
	}
	if projDir != "" {
		if err := addSkillsDir(projDir, "project"); err != nil {
			return nil, err
		}
	}

	// Deterministic ordering by slug (rule:cache-stability).
	slugs := make([]string, 0, len(index))
	for slug := range index {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	result := make([]ComposedSkill, 0, len(slugs))
	for _, slug := range slugs {
		result = append(result, index[slug].skill)
	}
	return result, nil
}
