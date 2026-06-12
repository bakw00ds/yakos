// Package memory is the Go port of cli/lib/memory.sh (rank 18).
//
// It manages the auto-memory system: MEMORY.md + individual memory files
// that live under ~/.claude/projects/<encoded>/memory/.
//
// # Subcommands
//
//   - list                          — print the MEMORY.md index + file listing
//   - read <slug>                   — print a single memory file's body
//   - write <slug> <type> <body>    — create or replace a memory file
//   - delete <slug>                 — remove a memory file and drop its index entry
//   - index-rebuild                 — rewrite MEMORY.md from existing files
//
// # Memory directory layout
//
//	~/.claude/projects/<encoded>/memory/
//	├── MEMORY.md                  # human-readable index; one line per entry
//	├── .schema-version            # schema sidecar (Decision A)
//	├── feedback-delegate.md       # each memory file
//	└── ...
//
// Each memory file uses YAML frontmatter followed by a markdown body:
//
//	---
//	name: human-readable title
//	description: one-line summary (appears in MEMORY.md index)
//	metadata:
//	  type: feedback|note|project|reference|user
//	---
//	Body text here.
//
// MEMORY.md index line format (byte-identical is load-bearing):
//
//   - [<name>](<slug>.md) — <description>
//
// # Schema version
//
// The sidecar file at .schema-version records the format generation.
// Current value: "memory/v1". Reads tolerate an absent sidecar.
package memory

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is written to .schema-version on every write (Decision A).
const SchemaVersion = "memory/v1"

// Config carries everything Run needs.
type Config struct {
	// MemoryDir is the absolute path to the memory directory
	// (~/.claude/projects/<encoded>/memory/).  Required.
	MemoryDir string

	// Subcommand is "list", "read", "write", "delete", or "index-rebuild".
	Subcommand string

	// Slug is the memory key (filename without .md extension).
	// Required for read, write, delete.
	Slug string

	// Type is the memory type written into metadata.type on write.
	// Required for write.
	Type string

	// Body is the markdown body written after the frontmatter on write.
	// Required for write.
	Body string

	// Name overrides the human-readable name in the frontmatter on write.
	// When empty, Slug is used.
	Name string

	// Description overrides the one-line description on write.
	// When empty, a generic placeholder is used.
	Description string

	// Now is injected for tests; zero value uses time.Now().
	Now time.Time

	// Writer receives normal output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives advisory/warning messages (default: os.Stderr).
	ErrWriter io.Writer
}

// MemoryFile represents a parsed memory file.
type MemoryFile struct {
	// Slug is the filename without .md extension.
	Slug string

	// Name is the frontmatter `name` field.
	Name string

	// Description is the frontmatter `description` field.
	Description string

	// Type is the frontmatter `metadata.type` field.
	Type string

	// Body is the text after the closing `---` delimiter.
	Body string
}

// frontmatter is the YAML struct for memory file headers.
type frontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Metadata    metadata `yaml:"metadata"`
}

type metadata struct {
	NodeType  string `yaml:"node_type"`
	Type      string `yaml:"type"`
	WrittenAt string `yaml:"written_at,omitempty"`
}

// IndexEntry is one parsed line from MEMORY.md.
type IndexEntry struct {
	Name        string
	Slug        string // filename without .md (extracted from link target)
	Description string
}

// Run executes the memory subcommand described by cfg.
func Run(cfg Config) error {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}

	switch cfg.Subcommand {
	case "list", "":
		return runList(cfg)
	case "read":
		return runRead(cfg)
	case "write":
		return runWrite(cfg)
	case "delete":
		return runDelete(cfg)
	case "index-rebuild":
		return runIndexRebuild(cfg)
	default:
		return fmt.Errorf("memory: unknown subcommand %q (try --help)", cfg.Subcommand)
	}
}

// PrintHelp writes the --help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos memory <subcommand> [args...]

Manage the auto-memory system at ~/.claude/projects/<encoded>/memory/.

Subcommands:
  list                          List all memory entries (MEMORY.md index + files).
  read <slug>                   Print a memory file's body.
  write <slug> <type> <body>    Create or replace a memory (type: feedback|note|
                                project|reference|user).
  delete <slug>                 Remove a memory file and its index entry.
  index-rebuild                 Rewrite MEMORY.md from existing files.

The memory directory is resolved from the encoded project path:
  ~/.claude/projects/<encoded>/memory/

Slug is the filename key without the .md extension (e.g. "delegate-to-roster"
for the file delegate-to-roster.md).

Examples:
  yakos memory list
  yakos memory read delegate-to-roster
  yakos memory write my-note feedback "Always delegate to the roster."
  yakos memory delete old-note
  yakos memory index-rebuild
`)
}

// ---- subcommand: list -------------------------------------------------------

func runList(cfg Config) error {
	dir := cfg.MemoryDir
	if !dirExists(dir) {
		_, _ = fmt.Fprintf(cfg.Writer, "(no memory yet at %s)\n", dir)
		return nil
	}

	_, _ = fmt.Fprintf(cfg.Writer, "yakos memory — %s\n\n", dir)

	// Print MEMORY.md index if it exists.
	indexPath := filepath.Join(dir, "MEMORY.md")
	if fileExists(indexPath) {
		_, _ = fmt.Fprintln(cfg.Writer, "  Index (MEMORY.md):")
		data, err := os.ReadFile(indexPath) //nolint:gosec
		if err != nil {
			return fmt.Errorf("memory list: read MEMORY.md: %w", err)
		}
		lines := strings.Split(string(data), "\n")
		count := 0
		for _, line := range lines {
			if count >= 50 {
				break
			}
			_, _ = fmt.Fprintf(cfg.Writer, "    %s\n", line)
			count++
		}
		_, _ = fmt.Fprintln(cfg.Writer)
	}

	// List .md files (excluding MEMORY.md).
	files, err := memoryFiles(dir)
	if err != nil {
		return fmt.Errorf("memory list: %w", err)
	}
	_, _ = fmt.Fprintln(cfg.Writer, "  Files:")
	if len(files) == 0 {
		_, _ = fmt.Fprintln(cfg.Writer, "    (none)")
	}
	for _, f := range files {
		_, _ = fmt.Fprintf(cfg.Writer, "    %s\n", f)
	}
	return nil
}

// ---- subcommand: read -------------------------------------------------------

func runRead(cfg Config) error {
	if cfg.Slug == "" {
		return fmt.Errorf("memory read: <slug> required")
	}
	dir := cfg.MemoryDir
	path := resolveSlugPath(dir, cfg.Slug)
	if path == "" {
		return fmt.Errorf("memory read: %q not found in %s", cfg.Slug, dir)
	}
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return fmt.Errorf("memory read: %w", err)
	}
	_, _ = cfg.Writer.Write(data)
	return nil
}

// ---- subcommand: write ------------------------------------------------------

func runWrite(cfg Config) error {
	if cfg.Slug == "" {
		return fmt.Errorf("memory write: <slug> required")
	}
	if cfg.Type == "" {
		return fmt.Errorf("memory write: <type> required")
	}

	dir := cfg.MemoryDir
	if err := os.MkdirAll(dir, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("memory write: mkdir %s: %w", dir, err)
	}

	now := cfg.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	name := cfg.Name
	if name == "" {
		name = cfg.Slug
	}
	desc := cfg.Description
	if desc == "" {
		desc = cfg.Body
		// Truncate description to one line.
		if idx := strings.IndexByte(desc, '\n'); idx >= 0 {
			desc = desc[:idx]
		}
		// Truncate to 120 chars if needed.
		if len(desc) > 120 {
			desc = desc[:117] + "..."
		}
	}

	fm := frontmatter{
		Name:        name,
		Description: desc,
		Metadata: metadata{
			NodeType:  "memory",
			Type:      cfg.Type,
			WrittenAt: now.UTC().Format("2006-01-02T15:04Z"),
		},
	}

	fmBytes, err := yaml.Marshal(fm)
	if err != nil {
		return fmt.Errorf("memory write: marshal frontmatter: %w", err)
	}

	body := cfg.Body
	// Ensure body ends with a newline.
	if len(body) > 0 && body[len(body)-1] != '\n' {
		body += "\n"
	}

	content := "---\n" + string(fmBytes) + "---\n" + body

	target := filepath.Join(dir, cfg.Slug+".md")

	// Backup existing file.
	if fileExists(target) {
		bak := target + ".yakos-bak-" + now.UTC().Format("20060102T150405Z")
		if err := os.Rename(target, bak); err != nil {
			// Non-fatal — just warn.
			_, _ = fmt.Fprintf(cfg.ErrWriter, "memory write: backup warning: %v\n", err)
		}
	}

	if err := atomicWrite(target, []byte(content), 0644); err != nil {
		return fmt.Errorf("memory write: %w", err)
	}

	// Update MEMORY.md index.
	if err := upsertIndexEntry(dir, cfg.Slug, name, desc); err != nil {
		return fmt.Errorf("memory write: update index: %w", err)
	}

	// Write schema sidecar.
	if err := writeSchemaVersion(dir); err != nil {
		_, _ = fmt.Fprintf(cfg.ErrWriter, "memory write: schema sidecar: %v\n", err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "wrote %s\n", target)
	return nil
}

// ---- subcommand: delete -----------------------------------------------------

func runDelete(cfg Config) error {
	if cfg.Slug == "" {
		return fmt.Errorf("memory delete: <slug> required")
	}
	dir := cfg.MemoryDir
	path := resolveSlugPath(dir, cfg.Slug)
	if path == "" {
		return fmt.Errorf("memory delete: %q not found in %s", cfg.Slug, dir)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("memory delete: %w", err)
	}

	// Remove from MEMORY.md index.
	if err := removeIndexEntry(dir, cfg.Slug); err != nil {
		return fmt.Errorf("memory delete: update index: %w", err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "deleted %s\n", path)
	return nil
}

// ---- subcommand: index-rebuild ----------------------------------------------

func runIndexRebuild(cfg Config) error {
	dir := cfg.MemoryDir
	if !dirExists(dir) {
		return fmt.Errorf("memory index-rebuild: directory not found: %s", dir)
	}

	files, err := memoryFiles(dir)
	if err != nil {
		return fmt.Errorf("memory index-rebuild: %w", err)
	}

	// Parse each file's frontmatter to build index entries.
	var entries []IndexEntry
	for _, relPath := range files {
		absPath := filepath.Join(dir, relPath)
		slug := strings.TrimSuffix(relPath, ".md")

		mf, err := parseMemoryFile(absPath, slug)
		if err != nil {
			_, _ = fmt.Fprintf(cfg.ErrWriter, "memory index-rebuild: skip %s: %v\n", relPath, err)
			continue
		}

		name := mf.Name
		if name == "" {
			name = slug
		}
		desc := mf.Description
		if desc == "" {
			desc = "(no description)"
		}

		entries = append(entries, IndexEntry{
			Name:        name,
			Slug:        slug,
			Description: desc,
		})
	}

	// Build MEMORY.md content.
	var sb strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&sb, "- [%s](%s.md) — %s\n", e.Name, e.Slug, e.Description)
	}

	indexPath := filepath.Join(dir, "MEMORY.md")
	if err := atomicWrite(indexPath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("memory index-rebuild: write MEMORY.md: %w", err)
	}

	// Write schema sidecar.
	if err := writeSchemaVersion(dir); err != nil {
		_, _ = fmt.Fprintf(cfg.ErrWriter, "memory index-rebuild: schema sidecar: %v\n", err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "rebuilt MEMORY.md with %d entries in %s\n", len(entries), dir)
	return nil
}

// ---- MEMORY.md index helpers ------------------------------------------------

// ParseIndex parses the MEMORY.md index lines and returns the entries.
// Lines that do not match "- [name](slug.md) — description" are silently skipped.
func ParseIndex(indexPath string) ([]IndexEntry, error) {
	data, err := os.ReadFile(indexPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return ParseIndexBytes(data), nil
}

// ParseIndexBytes parses raw MEMORY.md bytes into index entries.
func ParseIndexBytes(data []byte) []IndexEntry {
	var entries []IndexEntry
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		e, ok := parseIndexLine(line)
		if ok {
			entries = append(entries, e)
		}
	}
	return entries
}

// parseIndexLine parses one MEMORY.md line of the form:
//
//   - [name](slug.md) — description
//
// Returns the parsed entry and true on success.
func parseIndexLine(line string) (IndexEntry, bool) {
	// Must start with "- ["
	if !strings.HasPrefix(line, "- [") {
		return IndexEntry{}, false
	}
	// Extract name between "- [" and "]("
	nameEnd := strings.Index(line, "](")
	if nameEnd < 0 {
		return IndexEntry{}, false
	}
	name := line[3:nameEnd]

	// Extract filename between "](" and ")"
	rest := line[nameEnd+2:]
	fileEnd := strings.IndexByte(rest, ')')
	if fileEnd < 0 {
		return IndexEntry{}, false
	}
	filename := rest[:fileEnd]
	slug := strings.TrimSuffix(filename, ".md")

	// Extract description after " — " (em dash variant) or " - ".
	rest = rest[fileEnd+1:]
	desc := ""
	if idx := strings.Index(rest, " \xe2\x80\x94 "); idx >= 0 { // em dash U+2014
		desc = strings.TrimSpace(rest[idx+len(" \xe2\x80\x94 "):])
	} else if idx := strings.Index(rest, " — "); idx >= 0 { // typographic em dash
		desc = strings.TrimSpace(rest[idx+len(" — "):])
	}

	return IndexEntry{Name: name, Slug: slug, Description: desc}, true
}

// BuildIndexLine formats one MEMORY.md index line (byte-identical to bash).
func BuildIndexLine(name, slug, description string) string {
	return fmt.Sprintf("- [%s](%s.md) — %s", name, slug, description)
}

// upsertIndexEntry adds or replaces the index line for slug in MEMORY.md.
func upsertIndexEntry(dir, slug, name, description string) error {
	indexPath := filepath.Join(dir, "MEMORY.md")

	newLine := BuildIndexLine(name, slug, description)
	filename := slug + ".md"

	// Read existing content.
	var lines []string
	if data, err := os.ReadFile(indexPath); err == nil { //nolint:gosec
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}

	// Check if the slug is already in the index (by filename reference).
	found := false
	for i, l := range lines {
		if strings.Contains(l, "]("+filename+")") {
			lines[i] = newLine
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, newLine)
	}

	content := strings.Join(lines, "\n") + "\n"
	return atomicWrite(indexPath, []byte(content), 0644)
}

// removeIndexEntry removes the index line matching slug from MEMORY.md.
func removeIndexEntry(dir, slug string) error {
	indexPath := filepath.Join(dir, "MEMORY.md")
	data, err := os.ReadFile(indexPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	filename := slug + ".md"
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "]("+filename+")") {
			continue
		}
		kept = append(kept, line)
	}

	// Trim trailing blank lines then add one trailing newline.
	for len(kept) > 0 && kept[len(kept)-1] == "" {
		kept = kept[:len(kept)-1]
	}

	var content string
	if len(kept) > 0 {
		content = strings.Join(kept, "\n") + "\n"
	}

	return atomicWrite(indexPath, []byte(content), 0644)
}

// ---- memory file parsing ----------------------------------------------------

// parseMemoryFile reads and parses a memory file at path.
func parseMemoryFile(path, slug string) (*MemoryFile, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, err
	}

	fm, body, err := splitFrontmatter(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}

	return &MemoryFile{
		Slug:        slug,
		Name:        fm.Name,
		Description: fm.Description,
		Type:        fm.Metadata.Type,
		Body:        body,
	}, nil
}

// splitFrontmatter splits a memory file into parsed frontmatter and body.
// Returns an empty frontmatter (not an error) if no YAML block is present.
func splitFrontmatter(content string) (frontmatter, string, error) {
	var fm frontmatter
	if !strings.HasPrefix(content, "---\n") {
		// No frontmatter block; treat entire content as body.
		return fm, content, nil
	}

	// Find closing delimiter.
	rest := content[4:] // skip "---\n"
	endIdx := strings.Index(rest, "\n---\n")
	if endIdx < 0 {
		// Try end-of-file delimiter.
		if strings.HasSuffix(rest, "\n---") {
			endIdx = len(rest) - 4
			yamlBlock := rest[:endIdx]
			if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
				return fm, "", fmt.Errorf("YAML parse: %w", err)
			}
			return fm, "", nil
		}
		// Malformed; treat everything as body.
		return fm, content, nil
	}

	yamlBlock := rest[:endIdx]
	body := rest[endIdx+5:] // skip "\n---\n"

	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return fm, "", fmt.Errorf("YAML parse: %w", err)
	}
	return fm, body, nil
}

// ---- schema sidecar ---------------------------------------------------------

// ReadSchemaVersion returns the schema version recorded in .schema-version.
// Returns "" when the sidecar is absent.
func ReadSchemaVersion(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, ".schema-version")) //nolint:gosec
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// writeSchemaVersion writes SchemaVersion to .schema-version in dir.
func writeSchemaVersion(dir string) error {
	return atomicWrite(
		filepath.Join(dir, ".schema-version"),
		[]byte(SchemaVersion+"\n"),
		0644,
	)
}

// ---- filesystem helpers -----------------------------------------------------

// memoryFiles returns sorted relative paths of .md files in dir, excluding MEMORY.md.
func memoryFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("readdir %s: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "MEMORY.md" || !strings.HasSuffix(name, ".md") {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// resolveSlugPath returns the full path for slug in dir, or "" if not found.
// Tries slug+".md" then slug verbatim (for files already ending in .md).
func resolveSlugPath(dir, slug string) string {
	candidate := filepath.Join(dir, slug+".md")
	if fileExists(candidate) {
		return candidate
	}
	candidate = filepath.Join(dir, slug)
	if fileExists(candidate) {
		return candidate
	}
	return ""
}

// dirExists returns true if path exists and is a directory.
func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// fileExists returns true if path exists and is a regular file.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// atomicWrite writes content to path via a temp-rename, ensuring no torn writes.
func atomicWrite(path string, content []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, mode); err != nil { //nolint:gosec
		return fmt.Errorf("write tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s → %s: %w", tmp, path, err)
	}
	return nil
}
