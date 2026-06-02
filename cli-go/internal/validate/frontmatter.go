// Package validate implements schema + reference validation for yakOS lib/
// directories (agents, skills, rules). It is the Go port of cli/lib/validate.sh.
//
// The bash source is the authority during Phase 1 shadow mode; this package
// must produce byte-identical output for every fixture the parity tests cover.
package validate

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// parseFrontmatter extracts the YAML block between the opening and closing ---
// fences from a markdown file.  Returns the parsed map and nil error on
// success.
//
// The rules mirror validate_python_frontmatter in validate.sh:
//   - Line 1 must be exactly "---"
//   - Within the next 200 lines there must be a line that is exactly "---"
//   - The body between the fences must be valid YAML
func parseFrontmatter(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return nil, fmt.Errorf("no YAML frontmatter block")
	}

	// Find the closing --- within the first 200 lines (lines[1..199]).
	limit := 200
	if len(lines) < limit {
		limit = len(lines)
	}
	closeIdx := -1
	for i := 1; i < limit; i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		return nil, fmt.Errorf("no YAML frontmatter block")
	}

	body := strings.Join(lines[1:closeIdx], "\n")

	var out map[string]any
	if err := yaml.Unmarshal([]byte(body), &out); err != nil {
		return nil, fmt.Errorf("yaml parse error: %w", err)
	}
	return out, nil
}

// hasFrontmatter does a lightweight check: does the file start with --- and
// have a closing --- somewhere in the first 200 lines?  Used in the non-python
// fallback path in bash; in Go we always do the full parse, but this helper
// is retained for clarity in unit tests.
func hasFrontmatter(data []byte) bool {
	lines := bytes.SplitN(data, []byte("\n"), 201)
	if len(lines) == 0 {
		return false
	}
	first := bytes.TrimRight(lines[0], "\r")
	if string(first) != "---" {
		return false
	}
	limit := 200
	if len(lines) < limit {
		limit = len(lines)
	}
	for i := 1; i < limit; i++ {
		if string(bytes.TrimRight(lines[i], "\r")) == "---" {
			return true
		}
	}
	return false
}
