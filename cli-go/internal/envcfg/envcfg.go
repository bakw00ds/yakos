// Package envcfg is the Go port of cli/lib/env.sh (rank 29).
//
// It provides dev/test/prod environment management for yakOS projects.
// Environments are declared in <project>/.yakos.yml under the `environments:`
// key. Each environment maps to a git branch.
//
// # Synopsis
//
//	yakos env status                   # current branch → env mapping
//	yakos env promote <from> <to>      # PR from env's branch → to env's branch
//	yakos env validate                 # check .yakos.yml environments section
//	yakos env list                     # list configured envs
//
// # YAML shape expected in .yakos.yml
//
//	environments:
//	  dev:
//	    branch: main
//	  test:
//	    branch: staging
//	  prod:
//	    branch: production
//
// # PR tool detection
//
// Promote detects gh, glab, or falls back to plain git + URL guidance.
// The actual PR creation executes the tool via exec; in tests the ExecFn
// is injectable.
//
// # Atomic writes
//
// This subcommand is read-only (no writes to .yakos.yml). Promote spawns
// an external process and logs to stderr.
package envcfg

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// knownEnvs is the ordered set of standard environment names.
// Mirrors the bash implementation's `for env_name in dev test prod`.
var knownEnvs = []string{"dev", "test", "prod"}

// PRTool identifies which PR creation tool is available.
type PRTool string

const (
	PRToolGH   PRTool = "gh"
	PRToolGLab PRTool = "glab"
	PRToolGit  PRTool = "git"
)

// Config carries everything Run needs.
type Config struct {
	// Subcommand is one of: status, promote, validate, list, help.
	Subcommand string

	// PromoteFrom / PromoteTo are the env names for `env promote <from> <to>`.
	PromoteFrom string
	PromoteTo   string

	// ProjectDir overrides automatic project directory resolution.
	// When empty, resolution uses YAKOS_PROJECT_DIR env, cwd .yakos.yml,
	// or .project-path.
	ProjectDir string

	// HomeDir overrides os.Getenv("HOME") for path resolution.
	// When empty, os.Getenv("HOME") is used.
	HomeDir string

	// Now overrides time.Now() for deterministic tests.
	Now func() time.Time

	// ExecFn is used to run the PR tool (gh/glab). When nil, exec.Command is
	// used. Injectable for tests to avoid spawning real processes.
	ExecFn func(name string, args ...string) ([]byte, error)

	// GitFn is used to run git commands. When nil, exec.Command is used.
	GitFn func(args ...string) ([]byte, error)

	// PRToolOverride forces a specific PR tool (bypasses detection).
	// Only used in tests.
	PRToolOverride PRTool

	// Writer receives normal output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives advisory/warning messages (default: os.Stderr).
	ErrWriter io.Writer
}

// Result summarises what Run did.
type Result struct {
	// Subcommand that was executed.
	Subcommand string

	// ProjectDir is the resolved project directory.
	ProjectDir string

	// CurrentBranch is the active git branch (status subcommand).
	CurrentBranch string

	// MappedEnv is the environment name for the current branch, or empty.
	MappedEnv string

	// DetectedTool is the PR tool found (status subcommand).
	DetectedTool PRTool

	// Environments maps env name → branch (list/validate subcommands).
	Environments map[string]string

	// ValidationOK is true when validate found no errors.
	ValidationOK bool
}

// Run executes the env subcommand described by cfg.
func Run(cfg Config) (*Result, error) {
	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}
	ew := cfg.ErrWriter
	if ew == nil {
		ew = os.Stderr
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	res := &Result{Subcommand: cfg.Subcommand}

	switch cfg.Subcommand {
	case "status":
		return runStatus(cfg, res, w, ew)
	case "promote":
		return runPromote(cfg, res, w, ew)
	case "validate":
		return runValidate(cfg, res, w, ew)
	case "list":
		return runList(cfg, res, w, ew)
	case "help", "--help", "-h", "":
		PrintHelp(w)
		return res, nil
	default:
		return nil, fmt.Errorf("yakos env: unknown subcommand %q (try 'yakos env help')", cfg.Subcommand)
	}
}

// PrintHelp writes the help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos env — dev/test/prod environment management

  yakos env status                  # current branch → env
  yakos env promote <from> <to>     # PR <from>'s branch → <to>'s branch
  yakos env validate                # check .yakos.yml environments
  yakos env list                    # list configured envs

Config lives in .yakos.yml under `+"`"+`environments:`+"`"+`.
See lib/rules/environment-discipline.md.
`)
}

// ---- subcommand implementations ---------------------------------------------

func runStatus(cfg Config, res *Result, w, ew io.Writer) (*Result, error) {
	projectDir, err := resolveProjectDir(cfg)
	if err != nil {
		return nil, err
	}
	res.ProjectDir = projectDir

	branch, err := currentBranch(cfg, projectDir)
	if err != nil {
		branch = "(unknown)"
	}
	res.CurrentBranch = branch

	envs, err := loadEnvironments(projectDir)
	if err != nil {
		envs = map[string]string{}
	}
	res.MappedEnv = envForBranch(envs, branch)
	res.Environments = envs

	tool := detectPRTool(cfg)
	res.DetectedTool = tool

	_, _ = fmt.Fprintf(w, "Project: %s\n", projectDir)
	_, _ = fmt.Fprintf(w, "Current branch: %s\n", branch)
	mapped := res.MappedEnv
	if mapped == "" {
		mapped = "<none>"
	}
	_, _ = fmt.Fprintf(w, "Mapped environment: %s\n", mapped)
	_, _ = fmt.Fprintf(w, "PR tool detected: %s\n", string(tool))
	_ = ew
	return res, nil
}

func runValidate(cfg Config, res *Result, w, ew io.Writer) (*Result, error) {
	projectDir, err := resolveProjectDir(cfg)
	if err != nil {
		return nil, err
	}
	res.ProjectDir = projectDir

	ymlPath := filepath.Join(projectDir, ".yakos.yml")
	if _, err := os.Stat(ymlPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("no .yakos.yml at %s", ymlPath)
	}

	// Check environments section exists.
	raw, err := os.ReadFile(ymlPath) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ymlPath, err)
	}
	if !hasEnvironmentsSection(raw) {
		_, _ = fmt.Fprintln(w, "  [info] no environments declared in .yakos.yml")
		res.ValidationOK = true
		return res, nil
	}
	_, _ = fmt.Fprintln(w, "  [ok] environments section present")

	envs, err := loadEnvironments(projectDir)
	if err != nil {
		return nil, fmt.Errorf("parse environments: %w", err)
	}
	res.Environments = envs

	for _, name := range knownEnvs {
		branch, ok := envs[name]
		if !ok || branch == "" {
			_, _ = fmt.Fprintf(w, "  [info] %s not configured\n", name)
			continue
		}
		_, _ = fmt.Fprintf(w, "  [ok] %s → branch %q\n", name, branch)
		// Check branch exists locally via git.
		if _, err := gitCmd(cfg, projectDir, "rev-parse", "--verify", branch); err != nil {
			_, _ = fmt.Fprintf(w, "  [warn] %s branch %q does not exist locally\n", name, branch)
		}
	}

	res.ValidationOK = true
	return res, nil
}

func runList(cfg Config, res *Result, w, ew io.Writer) (*Result, error) {
	projectDir, err := resolveProjectDir(cfg)
	if err != nil {
		return nil, err
	}
	res.ProjectDir = projectDir

	envs, err := loadEnvironments(projectDir)
	if err != nil {
		// Non-fatal: just list nothing.
		envs = map[string]string{}
	}
	res.Environments = envs

	for _, name := range knownEnvs {
		if branch, ok := envs[name]; ok && branch != "" {
			_, _ = fmt.Fprintf(w, "  %s → %s\n", name, branch)
		}
	}
	_ = ew
	return res, nil
}

func runPromote(cfg Config, res *Result, w, ew io.Writer) (*Result, error) {
	from := cfg.PromoteFrom
	to := cfg.PromoteTo
	if from == "" || to == "" {
		return nil, fmt.Errorf("yakos env promote: both <from> and <to> env names are required")
	}

	projectDir, err := resolveProjectDir(cfg)
	if err != nil {
		return nil, err
	}
	res.ProjectDir = projectDir

	envs, err := loadEnvironments(projectDir)
	if err != nil {
		return nil, fmt.Errorf("read environments: %w", err)
	}
	res.Environments = envs

	fromBranch, ok := envs[from]
	if !ok || fromBranch == "" {
		return nil, fmt.Errorf("no branch configured for env %q", from)
	}
	toBranch, ok2 := envs[to]
	if !ok2 || toBranch == "" {
		return nil, fmt.Errorf("no branch configured for env %q", to)
	}

	// Fetch to update remote tracking refs. Non-fatal on failure.
	if _, ferr := gitCmd(cfg, projectDir, "fetch", "origin", fromBranch); ferr != nil {
		_, _ = fmt.Fprintf(ew, "env promote: fetch warning: %v\n", ferr)
	}

	// Compare local vs remote.
	localHead, lerr := gitCmd(cfg, projectDir, "rev-parse", fromBranch)
	remoteHead, rerr := gitCmd(cfg, projectDir, "rev-parse", "origin/"+fromBranch)
	if lerr == nil && rerr == nil {
		local := strings.TrimSpace(string(localHead))
		remote := strings.TrimSpace(string(remoteHead))
		if local != remote {
			_, _ = fmt.Fprintf(ew, "env promote: WARN: local %s (%s) differs from origin (%s)\n", fromBranch, local, remote)
			_, _ = fmt.Fprintf(ew, "env promote: Push %s first, then re-run promote.\n", fromBranch)
			return nil, fmt.Errorf("local %s differs from origin; push first", fromBranch)
		}
	}

	ts := cfg.Now().UTC().Format(time.RFC3339)
	title := fmt.Sprintf("chore(%s): promote %s → %s at %s", to, fromBranch, toBranch, ts)

	// Build changelog.
	changelogOut, _ := gitCmd(cfg, projectDir, "log", "--oneline", toBranch+".."+fromBranch)
	changelog := strings.TrimSpace(string(changelogOut))
	if changelog == "" {
		changelog = "(no commits)"
	}

	body := fmt.Sprintf("Promotion: %s → %s\n\nGenerated by yakos env promote %s %s at %s.\n\nChangelog since last %s promotion:\n%s",
		fromBranch, toBranch, from, to, ts, to, changelog)

	tool := detectPRTool(cfg)
	res.DetectedTool = tool

	switch tool {
	case PRToolGH:
		out, err := execCmd(cfg, "gh", "pr", "create",
			"--base", toBranch,
			"--head", fromBranch,
			"--title", title,
			"--body", body)
		if err != nil {
			return nil, fmt.Errorf("gh pr create: %w", err)
		}
		_, _ = w.Write(out)

	case PRToolGLab:
		out, err := execCmd(cfg, "glab", "mr", "create",
			"--target-branch", toBranch,
			"--source-branch", fromBranch,
			"--title", title,
			"--description", body)
		if err != nil {
			return nil, fmt.Errorf("glab mr create: %w", err)
		}
		_, _ = w.Write(out)

	default:
		_, _ = fmt.Fprintf(ew, "env promote: No gh/glab detected. Open the PR manually:\n")
		_, _ = fmt.Fprintf(ew, "  base: %s\n", toBranch)
		_, _ = fmt.Fprintf(ew, "  head: %s\n", fromBranch)
		_, _ = fmt.Fprintf(ew, "  title: %s\n", title)
		_, _ = fmt.Fprintf(ew, "\n")
		_, _ = fmt.Fprintf(ew, "Suggested commands (if your remote is github):\n")
		_, _ = fmt.Fprintf(ew, "  git push origin %s\n", fromBranch)
		_, _ = fmt.Fprintf(ew, "  open https://github.com/<owner>/<repo>/compare/%s...%s\n", toBranch, fromBranch)
	}

	return res, nil
}

// ---- YAML helpers -----------------------------------------------------------

// yakosYML is the minimal shape we decode from .yakos.yml for the
// environments section.
type yakosYML struct {
	Environments map[string]envEntry `yaml:"environments"`
}

type envEntry struct {
	Branch string `yaml:"branch"`
}

// loadEnvironments reads .yakos.yml and returns a map of env name → branch.
func loadEnvironments(projectDir string) (map[string]string, error) {
	ymlPath := filepath.Join(projectDir, ".yakos.yml")
	raw, err := os.ReadFile(ymlPath) //nolint:gosec
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ymlPath, err)
	}

	var doc yakosYML
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ymlPath, err)
	}

	result := make(map[string]string, len(doc.Environments))
	for name, entry := range doc.Environments {
		result[name] = entry.Branch
	}
	return result, nil
}

// hasEnvironmentsSection returns true if the raw .yakos.yml bytes contain
// an `environments:` section header (mirrors the bash grep check).
func hasEnvironmentsSection(raw []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "environments:" {
			return true
		}
	}
	return false
}

// envForBranch returns the env name whose branch equals branch, or empty.
func envForBranch(envs map[string]string, branch string) string {
	for _, name := range knownEnvs {
		if b, ok := envs[name]; ok && b == branch {
			return name
		}
	}
	return ""
}

// ---- project dir resolution -------------------------------------------------

// resolveProjectDir resolves the project directory using:
//  1. cfg.ProjectDir (explicit override)
//  2. YAKOS_PROJECT_DIR env
//  3. cwd contains .yakos.yml
//  4. cwd contains .project-path
func resolveProjectDir(cfg Config) (string, error) {
	if cfg.ProjectDir != "" {
		return cfg.ProjectDir, nil
	}
	if v := os.Getenv("YAKOS_PROJECT_DIR"); v != "" {
		return v, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	if _, err := os.Stat(filepath.Join(cwd, ".yakos.yml")); err == nil {
		return cwd, nil
	}
	ppFile := filepath.Join(cwd, ".project-path")
	if data, err := os.ReadFile(ppFile); err == nil { //nolint:gosec
		return strings.TrimSpace(string(data)), nil
	}
	return "", fmt.Errorf("could not resolve project dir (run from project root or set YAKOS_PROJECT_DIR)")
}

// ---- git helpers ------------------------------------------------------------

// currentBranch returns the current git branch in projectDir.
func currentBranch(cfg Config, projectDir string) (string, error) {
	out, err := gitCmd(cfg, projectDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// gitCmd runs a git command in projectDir, using cfg.GitFn when set.
func gitCmd(cfg Config, projectDir string, args ...string) ([]byte, error) {
	if cfg.GitFn != nil {
		full := append([]string{"-C", projectDir}, args...)
		return cfg.GitFn(full...)
	}
	fullArgs := append([]string{"-C", projectDir}, args...)
	cmd := exec.Command("git", fullArgs...) //nolint:gosec
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// execCmd runs an external command (gh/glab), using cfg.ExecFn when set.
func execCmd(cfg Config, name string, args ...string) ([]byte, error) {
	if cfg.ExecFn != nil {
		return cfg.ExecFn(name, args...)
	}
	cmd := exec.Command(name, args...) //nolint:gosec
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// ---- PR tool detection ------------------------------------------------------

// detectPRTool checks for gh, glab, or falls back to git.
func detectPRTool(cfg Config) PRTool {
	if cfg.PRToolOverride != "" {
		return cfg.PRToolOverride
	}
	if _, err := exec.LookPath("gh"); err == nil {
		return PRToolGH
	}
	if _, err := exec.LookPath("glab"); err == nil {
		return PRToolGLab
	}
	return PRToolGit
}
