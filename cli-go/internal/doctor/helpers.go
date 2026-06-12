package doctor

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// defaultLookPath wraps exec.LookPath for use when cfg.LookPath is nil.
func defaultLookPath(cmd string) (string, error) {
	return exec.LookPath(cmd)
}

// sha256File returns the lowercase hex SHA-256 of the file at path.
func sha256File(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// isWritable returns true if the directory at path is writable by the current user.
// It does this by attempting to create a temp file inside the directory.
func isWritable(path string) bool {
	tmp := filepath.Join(path, ".yakos-write-check-tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY, 0600) //nolint:gosec
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(tmp)
	return true
}

// countDirs counts immediate subdirectories of dir.
func countDirs(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

// countGlob counts files matching the glob pattern rooted at dir.
// The pattern is a single-level glob (e.g. "*.ndjson") or a two-level
// pattern (e.g. "*/inboxes/*.json").
func countGlob(dir, pattern string) int {
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return 0
	}
	return len(matches)
}

// countFilesExcluding counts files in dir (non-recursive), excluding files
// whose names contain the given suffix.
func countFilesExcluding(dir, excludeSuffix string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if excludeSuffix != "" && strings.HasSuffix(e.Name(), excludeSuffix) {
			continue
		}
		n++
	}
	return n
}

// countFileLines counts non-empty lines in a file.
func countFileLines(path string) int {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	n := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			n++
		}
	}
	return n
}

// lastJSONLField reads the last line of a NDJSON file and extracts the named
// field's string value.
func lastJSONLField(path, field string) string {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	var lastLine string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if l := strings.TrimSpace(scanner.Text()); l != "" {
			lastLine = l
		}
	}
	if lastLine == "" {
		return ""
	}
	return jsonField(lastLine, field)
}

// jsonField extracts a string field from a JSON line.
func jsonField(line, field string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return ""
	}
	v, ok := m[field]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return ""
	}
	return s
}

// readLastNJSONLLines reads the last n non-empty lines from a NDJSON file.
// Returns lines in order (oldest first among the returned set).
func readLastNJSONLLines(path string, n int) []string {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if l := strings.TrimSpace(scanner.Text()); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// nowUTCString returns the current UTC time as an ISO-8601 string matching
// the format used by active-claims.json: "2006-01-02T15:04:05Z".
func nowUTCString() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

// toolVersion attempts to get the version of a tool by running `tool --version`
// and returning the first line of output. Returns "" on failure.
func toolVersion(tool string) string {
	cmd := exec.Command(tool, "--version") //nolint:gosec
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	// Strip carriage returns (Windows).
	line = strings.TrimRight(line, "\r")
	return line
}

// countMarkdownFiles counts .md files in dir that are NOT in excludeNames.
func countMarkdownFiles(dir string, excludeNames ...string) int {
	excludeSet := make(map[string]bool, len(excludeNames))
	for _, n := range excludeNames {
		excludeSet[n] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && !excludeSet[e.Name()] {
			n++
		}
	}
	return n
}

// hasObviousSecrets scans the project tree for well-known secret patterns.
// It is a best-effort grep; it does not replace a proper secret scanner.
func hasObviousSecrets(projectPath string) bool {
	patterns := []string{
		"sk-ant-", // Anthropic API key
		"AKIA",    // AWS access key
		"ghp_",    // GitHub personal access token
		"BEGIN RSA PRIVATE KEY",
		"BEGIN EC PRIVATE KEY",
	}
	found := false
	_ = filepath.Walk(projectPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || found {
			return nil
		}
		// Skip .git and binary-like files.
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		// Only scan text-likely files (skip large files).
		if info.Size() > 1024*1024 {
			return nil
		}
		data, rerr := os.ReadFile(path) //nolint:gosec
		if rerr != nil {
			return nil
		}
		content := string(data)
		for _, p := range patterns {
			if strings.Contains(content, p) {
				found = true
				return nil
			}
		}
		return nil
	})
	return found
}

// hasActiveBypass returns true if hook-bypass.md contains entries under
// "## Active entries". Mirrors bash's awk logic in doctor.sh.
func hasActiveBypass(path string) bool {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	active := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.TrimPrefix(line, "## ") == "Active entries" ||
			line == "## Active entries" {
			active = true
			continue
		}
		if active && strings.HasPrefix(line, "## bypass:") {
			return true
		}
	}
	return false
}

// auditAgents checks .md files in agentsDir for YAML frontmatter declaring
// a "tools:" key. Returns (agentCount, missingTools, emptyTools).
func auditAgents(agentsDir string) (agentCount, missingTools, emptyTools int) {
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(agentsDir, e.Name())
		data, rerr := os.ReadFile(path) //nolint:gosec
		if rerr != nil {
			continue
		}
		agentCount++
		fm := extractFrontmatter(string(data))
		if fm == "" {
			missingTools++
			continue
		}
		if !strings.Contains(fm, "tools:") {
			missingTools++
		} else if isEmptyToolsList(fm) {
			emptyTools++
		}
	}
	return
}

// extractFrontmatter extracts the YAML frontmatter block (between --- delimiters).
// Returns "" if no valid frontmatter is present.
func extractFrontmatter(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	var fmLines []string
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "---" {
			return strings.Join(fmLines, "\n")
		}
		fmLines = append(fmLines, l)
	}
	return ""
}

// isEmptyToolsList returns true when the frontmatter contains "tools: []".
func isEmptyToolsList(fm string) bool {
	for _, line := range strings.Split(fm, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "tools:") {
			after := strings.TrimSpace(strings.TrimPrefix(trimmed, "tools:"))
			if after == "[]" {
				return true
			}
		}
	}
	return false
}

// hasBudgetBlock returns true when the YAML content contains a top-level
// "budget:" key (mirrors bash's grep '^[[:space:]]*budget:').
func hasBudgetBlock(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "budget:") {
			return true
		}
	}
	return false
}

// parseSupervisorBlock returns (enabled, blockOnCritical) by scanning for
// "supervisor:" block with "enabled: true" and "block_on_critical: true".
func parseSupervisorBlock(content string) (enabled, blockOnCritical bool) {
	inSuper := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "supervisor:") {
			inSuper = true
			continue
		}
		if inSuper {
			// Detect end of block (dedented key).
			if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && !strings.HasPrefix(line, "#") {
				inSuper = false
				continue
			}
			t := strings.TrimSpace(line)
			if t == "enabled: true" {
				enabled = true
			}
			if t == "block_on_critical: true" {
				blockOnCritical = true
			}
		}
	}
	return
}

// hookDriftResult mirrors deploydrift.HookResult without depending on the
// deploydrift package from within the doctor package.
// We call deploydrift via the checkHookDriftDir function below.
type hookDriftResult struct {
	RelPath      string
	State        int // 0=clean, 1=drifted, 2=unhashed
	ExpectedHash string
	ActualHash   string
}

type hookDriftReport struct {
	Results  []hookDriftResult
	Clean    int
	Drifted  int
	Unhashed int
}

// checkHookDriftDir reimplements the drift check inline (doctor.go calls this
// to avoid a circular dependency through deploydrift's own import of os).
// The deploydrift package is intended for use by refresh (rank 6); doctor
// calls its own lightweight version here so the dependency graph stays clean.
func checkHookDriftDir(hooksDir, projectDir string) (*hookDriftReport, error) {
	entries, err := os.ReadDir(hooksDir)
	if err != nil {
		return nil, fmt.Errorf("reading hooks dir %s: %w", hooksDir, err)
	}

	report := &hookDriftReport{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".framework-hash") ||
			name == ".gitkeep" || name == "README.md" {
			continue
		}

		absPath := filepath.Join(hooksDir, name)
		relPath := absPath
		if projectDir != "" {
			if rel, rerr := filepath.Rel(projectDir, absPath); rerr == nil {
				relPath = rel
			}
		}

		actualHash, herr := sha256File(absPath)
		if herr != nil {
			continue
		}

		hashFile := absPath + ".framework-hash"
		expectedBytes, ferr := os.ReadFile(hashFile) //nolint:gosec
		if ferr != nil {
			report.Results = append(report.Results, hookDriftResult{
				RelPath:    relPath,
				State:      2, // unhashed
				ActualHash: actualHash,
			})
			report.Unhashed++
			continue
		}

		expected := strings.TrimSpace(string(expectedBytes))
		if expected == actualHash {
			report.Results = append(report.Results, hookDriftResult{
				RelPath:      relPath,
				State:        0, // clean
				ExpectedHash: expected,
				ActualHash:   actualHash,
			})
			report.Clean++
		} else {
			report.Results = append(report.Results, hookDriftResult{
				RelPath:      relPath,
				State:        1, // drifted
				ExpectedHash: expected,
				ActualHash:   actualHash,
			})
			report.Drifted++
		}
	}
	return report, nil
}
