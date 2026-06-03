// Package update implements the Go port of `yakos update`.
//
// `yakos update` brings the framework up-to-date:
//
//  1. Verifies YAKOS_ROOT is a git working tree.
//  2. Captures HEAD before the pull.
//  3. Runs `git pull` (--ff-only by default; --allow-non-ff overrides).
//  4. Captures HEAD after the pull.
//  5. If HEAD changed: prints the commit log since the prior HEAD, lists
//     changed lib/ files, then runs refresh on each deployed project via
//     refresh.CollectProjects + refresh.Run.
//  6. If HEAD unchanged: prints an "already up to date" message and returns.
//
// The package accepts a GitExec injection point so tests never spawn a real
// git process.
package update

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/bakw00ds/yakos/internal/refresh"
)

// GitExecFunc is the signature for running a git command. It receives the
// repository root, the git sub-args (e.g. ["pull", "--ff-only"]), and
// returns combined stdout+stderr output plus any error.
//
// The default implementation uses exec.Command("git", "-C", root, args...).
type GitExecFunc func(root string, args ...string) (output string, err error)

// Config controls an update run.
type Config struct {
	// YakosRoot is the framework repo root ($YAKOS_ROOT). Must be set.
	YakosRoot string

	// HomeDir overrides $HOME for project discovery. If empty, os.Getenv("HOME") is used.
	HomeDir string

	// AllowNonFF, when true, runs `git pull` without --ff-only.
	// Mirrors the bash --allow-non-ff flag.
	AllowNonFF bool

	// AllProjects, when true, runs refresh.Run on every discovered project
	// after a successful pull. Mirrors the bash --all flag.
	AllProjects bool

	// DryRun, when true, skips the actual git pull and per-project refresh.
	// The pre-pull HEAD is used as both before and after so callers can
	// observe a no-change path without side effects.
	DryRun bool

	// Writer is the output destination. Defaults to os.Stdout.
	Writer io.Writer

	// ErrWriter is the error destination. Defaults to os.Stderr.
	ErrWriter io.Writer

	// GitExec is the git execution hook. If nil, the real git binary is used.
	// Inject a stub in tests to avoid real network calls.
	GitExec GitExecFunc
}

// Result summarises what happened.
type Result struct {
	// HeadBefore is the commit SHA before the pull.
	HeadBefore string

	// HeadAfter is the commit SHA after the pull (equal to HeadBefore when
	// already up to date or in dry-run mode).
	HeadAfter string

	// AlreadyUpToDate is true when HeadBefore == HeadAfter.
	AlreadyUpToDate bool

	// ChangedLibFiles lists paths under lib/ that changed (empty when already
	// up to date).
	ChangedLibFiles []string

	// ProjectsRefreshed is the number of projects that received a refresh run.
	ProjectsRefreshed int

	// DryRun mirrors Config.DryRun.
	DryRun bool
}

// Run executes the update and returns a structured Result.
func Run(cfg Config) (*Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}
	if cfg.GitExec == nil {
		cfg.GitExec = defaultGitExec
	}

	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}

	// Step 1: verify YAKOS_ROOT is a git working tree.
	// Use `git --git-dir` (first arg is the flag, not a sub-command) so this
	// call is distinct from the `rev-parse HEAD` calls that follow — injection
	// stubs key on args[0].
	if _, err := cfg.GitExec(cfg.YakosRoot, "--git-dir"); err != nil {
		return nil, fmt.Errorf("update: %s is not a git repository (was YakOS installed from a git clone?): %w", cfg.YakosRoot, err)
	}

	// Step 2: capture HEAD before.
	headBefore, err := cfg.GitExec(cfg.YakosRoot, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("update: could not read HEAD: %w", err)
	}
	headBefore = strings.TrimSpace(headBefore)

	dryTag := ""
	if cfg.DryRun {
		dryTag = " [DRY RUN]"
	}
	_, _ = fmt.Fprintf(cfg.Writer, "yakos update%s\n\n", dryTag)

	// Step 3: git pull.
	headAfter := headBefore
	if !cfg.DryRun {
		pullArgs := []string{"pull"}
		if !cfg.AllowNonFF {
			pullArgs = append(pullArgs, "--ff-only")
		}
		pullOut, pullErr := cfg.GitExec(cfg.YakosRoot, pullArgs...)
		if pullErr != nil {
			_, _ = fmt.Fprintf(cfg.ErrWriter, "update: git pull failed: %v\n", pullErr)
			if pullOut != "" {
				_, _ = fmt.Fprintf(cfg.ErrWriter, "%s\n", pullOut)
			}
			return nil, fmt.Errorf("update: git pull: %w", pullErr)
		}
		if pullOut != "" {
			_, _ = fmt.Fprint(cfg.Writer, pullOut)
			if !strings.HasSuffix(pullOut, "\n") {
				_, _ = fmt.Fprintln(cfg.Writer)
			}
		}

		// Step 4: capture HEAD after.
		after, err := cfg.GitExec(cfg.YakosRoot, "rev-parse", "HEAD")
		if err != nil {
			return nil, fmt.Errorf("update: could not read HEAD after pull: %w", err)
		}
		headAfter = strings.TrimSpace(after)
	} else {
		_, _ = fmt.Fprintln(cfg.Writer, "[dry-run] would run: git pull"+func() string {
			if !cfg.AllowNonFF {
				return " --ff-only"
			}
			return ""
		}())
	}

	result := &Result{
		HeadBefore:      headBefore,
		HeadAfter:       headAfter,
		AlreadyUpToDate: headBefore == headAfter,
		DryRun:          cfg.DryRun,
	}

	// Already up to date.
	if result.AlreadyUpToDate {
		desc, _ := cfg.GitExec(cfg.YakosRoot, "describe", "--always", "--tags")
		desc = strings.TrimSpace(desc)
		if desc == "" {
			desc = headAfter
		}
		_, _ = fmt.Fprintf(cfg.Writer, "Already up to date at %s\n", desc)
		return result, nil
	}

	// Step 5: HEAD changed — print changelog excerpt.
	_, _ = fmt.Fprintf(cfg.Writer, "Pulled commits %s..%s\n\n", headBefore[:minLen(headBefore, 12)], headAfter[:minLen(headAfter, 12)])

	logOut, _ := cfg.GitExec(cfg.YakosRoot, "log", "--oneline", headBefore+".."+headAfter)
	if logOut != "" {
		_, _ = fmt.Fprintln(cfg.Writer, "Commits applied:")
		_, _ = fmt.Fprintln(cfg.Writer, strings.TrimRight(logOut, "\n"))
		_, _ = fmt.Fprintln(cfg.Writer)
	}

	// Print changed files under lib/.
	diffOut, _ := cfg.GitExec(cfg.YakosRoot, "diff", "--name-only", headBefore+".."+headAfter, "--", "lib/")
	var changedLib []string
	for _, line := range strings.Split(strings.TrimSpace(diffOut), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			changedLib = append(changedLib, line)
		}
	}
	result.ChangedLibFiles = changedLib

	if len(changedLib) > 0 {
		_, _ = fmt.Fprintln(cfg.Writer, "Changed under lib/ (consumed by install/uninstall):")
		for _, f := range changedLib {
			_, _ = fmt.Fprintf(cfg.Writer, "  %s\n", f)
		}
		_, _ = fmt.Fprintln(cfg.Writer)
	}

	// Per-project refresh (--all flag or always when HEAD changed and AllProjects is true).
	if cfg.AllProjects && !cfg.DryRun {
		projects := refresh.CollectProjects(home)
		if len(projects) == 0 {
			_, _ = fmt.Fprintln(cfg.Writer, "No deployed projects found to refresh.")
		} else {
			_, _ = fmt.Fprintf(cfg.Writer, "Refreshing %d deployed project(s)...\n\n", len(projects))
			rfCfg := refresh.Config{
				YakosRoot:    cfg.YakosRoot,
				ProjectPaths: projects,
				DryRun:       false,
				Writer:       cfg.Writer,
				ErrWriter:    cfg.ErrWriter,
				HomeDir:      home,
			}
			if _, err := refresh.Run(rfCfg); err != nil {
				_, _ = fmt.Fprintf(cfg.ErrWriter, "update: refresh error: %v\n", err)
			}
			result.ProjectsRefreshed = len(projects)
		}
	} else if cfg.AllProjects && cfg.DryRun {
		projects := refresh.CollectProjects(home)
		_, _ = fmt.Fprintf(cfg.Writer, "[dry-run] would refresh %d deployed project(s)\n", len(projects))
		result.ProjectsRefreshed = len(projects)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "yakos update complete.\n")
	return result, nil
}

// PrintHelp writes the help text for `yakos update` to w.
// The text mirrors the bash update.sh --help output (load-bearing phrases).
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos update — pull the framework repo and refresh symlinks

Runs:
  git pull --ff-only         in $YAKOS_ROOT
  (per-project refresh)      when --all is passed

Then prints commits applied since the previous HEAD and a list of
changed files under lib/ (which is what install/uninstall consume).

Options:
  --allow-non-ff   Allow non-fast-forward pulls (default: refuses).
  --all            After the framework update, discover every deployed
                   project and run yakos refresh on each. Surfaces drift
                   and refreshes per-project hook copies in one shot.
  --dry-run        Print what WOULD happen without running git pull or
                   project refresh.
  --help, -h       Print this help.
`)
}

// defaultGitExec runs git with -C <root> <args...> and returns combined output.
func defaultGitExec(root string, args ...string) (string, error) {
	fullArgs := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", fullArgs...) //nolint:gosec
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// minLen returns the smaller of n and len(s).
func minLen(s string, n int) int {
	if len(s) < n {
		return len(s)
	}
	return n
}
