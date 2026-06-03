// Package teach is the Go port of cli/lib/teach.sh (rank 23).
//
// It appends a "Lessons learned" bullet to a project agent file so operators
// can evolve framework agents without forking the source. Each lesson is
// stamped with an ISO date prefix; the section is created when absent.
//
// # Synopsis
//
//	yakos teach <agent-name> <lesson-file> [flags]
//
// # Flags
//
//   - --project <path>   Project root (defaults to inference from ~/agent-control/).
//   - --section <name>   Heading to append under (default: "Lessons learned").
//   - --dry-run          Print what would be written; do not modify files.
//
// # Behaviour
//
//  1. Resolve the project agent file at <project>/.claude/agents/<name>.md.
//  2. Format the lesson: prepend the ISO date, dedent continuation lines.
//  3. Back up the agent file with a timestamp suffix (<file>.yakos-bak-<ts>).
//  4. Append the bullet under the target section heading, creating the
//     section at EOF when absent, using a two-pass awk-equivalent algorithm.
//  5. All writes are atomic (temp-rename, Decision A / Q8).
//
// # Project inference
//
// When --project is absent, infer from ~/agent-control/*/project-path-file
// (mirrors the bash compat.sh project-path inference). If no match is found,
// an error is returned directing the caller to supply --project explicitly.
//
// # Dry-run
//
// Prints the formatted lesson and the target file path to Writer; does not
// touch the filesystem.
package teach

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/timestamp"
)

// ---- public types -----------------------------------------------------------

// Config carries everything Run needs.
type Config struct {
	// AgentName is the agent id (the stem of the .md filename). Required.
	AgentName string

	// LessonFile is the path to a markdown file with the lesson body. Required.
	LessonFile string

	// ProjectDir is the absolute path to the project root (contains .claude/).
	// When empty, Run attempts to infer it from ~/agent-control/*/project-path.
	ProjectDir string

	// Section is the H2 heading under which the lesson is appended.
	// Defaults to "Lessons learned" when empty.
	Section string

	// DryRun when true prints what would happen without writing any files.
	DryRun bool

	// HomeDir overrides os.Getenv("HOME") for project inference.
	// When empty, os.Getenv("HOME") is used.
	HomeDir string

	// Now overrides time.Now() for the ISO date stamp. Tests inject a fixed time.
	Now time.Time

	// Writer receives normal output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives advisory/warning messages (default: os.Stderr).
	ErrWriter io.Writer
}

// Result summarises what Run did.
type Result struct {
	// AgentFile is the absolute path of the agent file that was modified (or
	// would have been modified in dry-run mode).
	AgentFile string

	// BackupFile is the path of the backup created before editing. Empty in
	// dry-run mode.
	BackupFile string

	// Section is the section heading that was targeted.
	Section string

	// SectionCreated is true when the section did not previously exist.
	SectionCreated bool
}

// ---- entry point ------------------------------------------------------------

// Run executes the teach command according to cfg. It returns a Result and any
// error encountered.
func Run(cfg Config) (*Result, error) {
	// Apply defaults.
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}
	if cfg.Section == "" {
		cfg.Section = "Lessons learned"
	}
	if cfg.Now.IsZero() {
		cfg.Now = time.Now()
	}
	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}

	// Validate required inputs.
	if cfg.AgentName == "" {
		return nil, fmt.Errorf("teach: <agent-name> required")
	}
	if cfg.LessonFile == "" {
		return nil, fmt.Errorf("teach: <lesson-file> required")
	}
	if _, err := os.Stat(cfg.LessonFile); err != nil {
		return nil, fmt.Errorf("teach: lesson file not found: %s", cfg.LessonFile)
	}

	// Infer project when not supplied.
	projectDir := cfg.ProjectDir
	if projectDir == "" {
		projectDir = inferProject(home)
	}
	if projectDir == "" {
		return nil, fmt.Errorf("teach: --project required (could not infer project from %s/agent-control/)", home)
	}

	// Resolve agent file.
	agentFile := filepath.Join(projectDir, ".claude", "agents", cfg.AgentName+".md")
	if _, err := os.Stat(agentFile); err != nil {
		return nil, fmt.Errorf(
			"teach: project agent not found at %s (run 'yakos agent new %s --extends <framework-name>' to bootstrap)",
			agentFile, cfg.AgentName,
		)
	}

	// Read lesson body.
	lessonData, err := os.ReadFile(cfg.LessonFile) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("teach: reading lesson file: %w", err)
	}
	lessonBody := strings.TrimRight(string(lessonData), "\n")

	// Format the lesson bullet:
	//   - **YYYY-MM-DD** — <first line>
	//     <continuation lines indented with 2 spaces>
	ts := cfg.Now.UTC().Format("2006-01-02")
	formatted := formatLesson(ts, lessonBody)

	res := &Result{
		AgentFile: agentFile,
		Section:   cfg.Section,
	}

	if cfg.DryRun {
		_, _ = fmt.Fprintf(cfg.Writer, "would append to %s under '## %s':\n", agentFile, cfg.Section)
		for _, line := range strings.Split(formatted, "\n") {
			_, _ = fmt.Fprintf(cfg.Writer, "  %s\n", line)
		}
		return res, nil
	}

	// Backup the agent file before editing.
	backupSuffix := timestamp.WinSafe(cfg.Now)
	backupFile := agentFile + ".yakos-bak-" + backupSuffix
	if err := copyFile(agentFile, backupFile); err != nil {
		return nil, fmt.Errorf("teach: backup %s: %w", agentFile, err)
	}
	res.BackupFile = backupFile

	// Read current agent content.
	current, err := os.ReadFile(agentFile) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("teach: reading agent file: %w", err)
	}

	// Splice the lesson into (or append to) the section.
	sectionHeading := "## " + cfg.Section
	updated, sectionCreated := spliceLesson(string(current), sectionHeading, formatted)
	res.SectionCreated = sectionCreated

	// Atomic write.
	if err := atomicWrite(agentFile, updated); err != nil {
		return nil, fmt.Errorf("teach: writing agent file: %w", err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "appended lesson to %s under '## %s'\n", agentFile, cfg.Section)
	_, _ = fmt.Fprintf(cfg.Writer, "  backup: %s\n", backupFile)

	return res, nil
}

// PrintHelp writes the help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos teach <agent-name> <lesson-file> [flags]

Append a lesson to a project agent's body so the operator can evolve
framework agents without forking. Use this when an operator notices
a specialist consistently making a mistake the agent body could
have prevented.

Arguments:
  <agent-name>      The id of the project agent (must exist at
                    <project>/.claude/agents/<name>.md). To teach a
                    framework agent, run 'yakos agent new <name>
                    --extends <framework-name>' first to create the
                    project copy.
  <lesson-file>     Path to a markdown file with the lesson body.
                    Will be appended under '## Lessons learned'.

Flags:
  --project <path>  Project root (defaults to inferred from cwd).
  --section <name>  Section heading (default: 'Lessons learned').
  --dry-run         Print what would be appended; don't write.

Examples:
  yakos teach backend /tmp/dont-skip-audit-log.md
  yakos teach myapp-frontend ./lessons/typed-client-drift.md \
      --section "Project gotchas"
`)
}

// ---- formatting helpers -----------------------------------------------------

// formatLesson returns the formatted bullet string for the lesson.
//
// Output shape (no leading newline; the caller provides the separator):
//
//	- **YYYY-MM-DD** — <first line>
//	  <continuation line 1>
//	  <continuation line 2>
func formatLesson(dateStamp, body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 {
		return fmt.Sprintf("- **%s** — ", dateStamp)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "- **%s** — %s", dateStamp, lines[0])
	for _, line := range lines[1:] {
		sb.WriteByte('\n')
		if line == "" {
			sb.WriteString("  ")
		} else {
			sb.WriteString("  " + line)
		}
	}
	return sb.String()
}

// spliceLesson inserts formatted under sectionHeading in content.
//
// Two cases:
//  1. The heading already exists: insert formatted right before the next H2
//     (or at EOF if the section is the last heading).
//  2. The heading does not exist: append the heading + formatted at EOF.
//
// Returns the updated content and whether the section was newly created.
func spliceLesson(content, sectionHeading, formatted string) (updated string, created bool) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	// Find the section heading.
	sectionIdx := -1
	for i, line := range lines {
		if strings.TrimRight(line, " \t") == sectionHeading {
			sectionIdx = i
			break
		}
	}

	if sectionIdx == -1 {
		// Section absent: append at EOF.
		var sb strings.Builder
		sb.WriteString(content)
		// Ensure content ends with a newline before appending.
		if !strings.HasSuffix(content, "\n") {
			sb.WriteByte('\n')
		}
		sb.WriteString("\n" + sectionHeading + "\n")
		sb.WriteString("\n" + formatted + "\n")
		return sb.String(), true
	}

	// Section present: insert formatted before the next H2, or at EOF.
	// Find the insertion index: the first H2 after sectionIdx.
	insertBefore := -1
	for i := sectionIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimRight(lines[i], " \t")
		if strings.HasPrefix(trimmed, "## ") {
			insertBefore = i
			break
		}
	}

	// Build the output.
	var sb strings.Builder
	if insertBefore == -1 {
		// Section is the last H2: append at EOF.
		for _, line := range lines {
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
		sb.WriteString("\n" + formatted + "\n")
	} else {
		// Insert before the next H2.
		for i, line := range lines {
			if i == insertBefore {
				sb.WriteString("\n" + formatted + "\n\n")
			}
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
	}
	return sb.String(), false
}

// ---- filesystem helpers -----------------------------------------------------

// inferProject walks ~/agent-control/*/project-path files and returns the
// project directory that matches or contains the current working directory.
// Returns "" when no match is found.
func inferProject(home string) string {
	if home == "" {
		return ""
	}
	acRoot := filepath.Join(home, "agent-control")
	entries, err := os.ReadDir(acRoot)
	if err != nil {
		return ""
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	// Try to resolve symlinks on cwd for comparison.
	if r, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = r
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ppFile := filepath.Join(acRoot, e.Name(), ".project-path")
		data, err := os.ReadFile(ppFile) //nolint:gosec
		if err != nil {
			continue
		}
		p := strings.TrimSpace(string(data))
		if p == "" {
			continue
		}
		pr, err := filepath.EvalSymlinks(p)
		if err != nil {
			pr = p
		}
		if cwd == pr || strings.HasPrefix(cwd, pr+string(filepath.Separator)) {
			return pr
		}
	}
	return ""
}

// copyFile copies src to dst, creating dst if necessary.
func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// atomicWrite writes content to path using a temp-file + rename (Q8 / Decision A).
func atomicWrite(path, content string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".yakos-teach-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp: %w", err)
	}
	return nil
}
