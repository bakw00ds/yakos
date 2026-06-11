package metrics

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// gitRunner is an interface so git calls can be replaced in tests.
type gitRunner interface {
	// Run executes a git command with the given args in dir and returns
	// combined stdout+stderr output.
	Run(dir string, args ...string) (string, error)
}

// realGitRunner executes real git commands.
type realGitRunner struct{}

func (r realGitRunner) Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(bytes.TrimRight(out, "\n")), err
}

// gitInfo holds the git-derived fields for a snapshot.
type gitInfo struct {
	Commit string
	Branch string
}

// getGitInfo returns the current HEAD commit SHA and branch name.
// On error it returns placeholder strings rather than failing — metrics
// should still be written even when git is not available.
func getGitInfo(runner gitRunner, projectDir string) gitInfo {
	info := gitInfo{Commit: "unknown", Branch: "unknown"}

	if sha, err := runner.Run(projectDir, "rev-parse", "HEAD"); err == nil {
		info.Commit = strings.TrimSpace(sha)
	}
	if branch, err := runner.Run(projectDir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.Branch = strings.TrimSpace(branch)
	}
	return info
}

// deployTagRe matches semver tags like v1.2.3.4.
var deployTagRe = regexp.MustCompile(`^v\d+\.\d+\.\d+\.\d+$`)

// getDeployTags returns all git tags matching the deploy-tag pattern,
// along with their commit timestamps (RFC3339).
// Returns nil, nil when no tags match or git is unavailable.
func getDeployTags(runner gitRunner, projectDir string) ([]string, error) {
	out, err := runner.Run(projectDir, "tag", "--list")
	if err != nil {
		return nil, fmt.Errorf("git tag: %w", err)
	}
	var tags []string
	for _, t := range strings.Split(out, "\n") {
		t = strings.TrimSpace(t)
		if deployTagRe.MatchString(t) {
			tags = append(tags, t)
		}
	}
	return tags, nil
}

// getTagTimestamp returns the author timestamp of the commit a tag points at.
func getTagTimestamp(runner gitRunner, projectDir, tag string) (string, error) {
	return runner.Run(projectDir, "log", "-1", "--format=%aI", tag)
}

// emptyTreeSHA is the well-known git SHA for an empty tree object.
// Diffing against it gives the full set of lines introduced since the
// beginning of history, and is the correct base for a single-commit repo
// where root == HEAD (so root..HEAD is an empty diff).
const emptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// getChurnLines returns the total lines added+deleted across the last N commits
// using git numstat.  When the repo has fewer than N commits it falls back to
// diffing from the beginning of history via the empty-tree object.
// exec.Command does NOT invoke a shell, so shell substitutions like $(…) must
// never be passed as arguments — the root SHA is obtained via a separate
// gitRunner.Run call.
func getChurnLines(runner gitRunner, projectDir string, nCommits int) (int, error) {
	out, err := runner.Run(projectDir, "diff", "--numstat",
		fmt.Sprintf("HEAD~%d", nCommits), "HEAD")
	if err != nil {
		// Repo has fewer than nCommits.  Resolve the root commit first so we
		// can detect the single-commit case (root == HEAD → empty diff).
		rootSHA, revErr := runner.Run(projectDir, "rev-list", "--max-parents=0", "HEAD")
		if revErr != nil {
			return 0, fmt.Errorf("getChurnLines: rev-list: %w", revErr)
		}
		rootSHA = strings.TrimSpace(rootSHA)
		if rootSHA == "" {
			return 0, fmt.Errorf("getChurnLines: empty root SHA")
		}

		// headSHA is needed to detect root == HEAD.
		headSHA, headErr := runner.Run(projectDir, "rev-parse", "HEAD")
		if headErr != nil {
			return 0, fmt.Errorf("getChurnLines: rev-parse HEAD: %w", headErr)
		}
		headSHA = strings.TrimSpace(headSHA)

		base := rootSHA
		if rootSHA == headSHA {
			// Single-commit repo: diff from the empty tree to HEAD so we
			// count all lines introduced by the initial commit.
			base = emptyTreeSHA
		}

		out, err = runner.Run(projectDir, "diff", "--numstat", base, "HEAD")
		if err != nil {
			return 0, fmt.Errorf("getChurnLines: diff from root: %w", err)
		}
	}
	total := 0
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		added, _ := strconv.Atoi(parts[0])
		deleted, _ := strconv.Atoi(parts[1])
		total += added + deleted
	}
	return total, nil
}

// getLeadTimes returns the lead times in hours from each commit to the first
// containing deploy tag. Only considers commits reachable from deploy tags.
func getLeadTimes(runner gitRunner, projectDir string, tags []string) ([]float64, error) {
	if len(tags) == 0 {
		return nil, nil
	}

	// For each tag, get the list of commits it contains along with their dates.
	// lead_time = tag_date - commit_date
	// We compute per-tag: get tag date, get oldest commit in tag.
	// This is a simplified computation: median lead time = median (tag_ts - first commit in tag from HEAD~).

	var leadTimes []float64
	for _, tag := range tags {
		tagTS, err := getTagTimestamp(runner, projectDir, tag)
		if err != nil {
			continue
		}
		tagTS = strings.TrimSpace(tagTS)
		tagTime, err := parseISO(tagTS)
		if err != nil {
			continue
		}

		// Get the oldest commit in this tag's ancestry (first commit SHA in log).
		// Use a bounded log of 100 commits per tag to keep it fast.
		out, err := runner.Run(projectDir, "log", "--format=%aI", tag, "-100")
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) == 0 {
			continue
		}
		// Oldest commit is last in the log.
		oldestTS := strings.TrimSpace(lines[len(lines)-1])
		commitTime, err := parseISO(oldestTS)
		if err != nil {
			continue
		}
		hours := tagTime.Sub(commitTime).Hours()
		if hours < 0 {
			hours = 0
		}
		leadTimes = append(leadTimes, hours)
	}
	return leadTimes, nil
}
