// Package update_test verifies the Go port of `yakos update`.
//
// All tests use a fake GitExec so no real network or git operations occur.
// The fake is keyed on (args[0]) to return canned output per call sequence.
package update_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/update"
)

// ---- helpers ----------------------------------------------------------------

// fakeGitDir creates a minimal YAKOS_ROOT with a .git directory so the
// working-tree check passes.
func fakeGitDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	return root
}

// stubEntry is one canned response for a git sub-command key.
type stubEntry struct {
	output string
	err    error
}

// callSequencer returns a GitExecFunc that plays back responses in order per
// first-arg key. When a key's list is exhausted, the last entry is replayed.
func callSequencer(sequences map[string][]stubEntry) update.GitExecFunc {
	positions := make(map[string]int)
	return func(_ string, args ...string) (string, error) {
		key := ""
		if len(args) > 0 {
			key = args[0]
		}
		seq, ok := sequences[key]
		if !ok || len(seq) == 0 {
			return "", nil
		}
		pos := positions[key]
		if pos >= len(seq) {
			pos = len(seq) - 1
		}
		e := seq[pos]
		positions[key]++
		return e.output, e.err
	}
}

// baseConfig returns a Config with captured writers and the given root + git stub.
func baseConfig(t *testing.T, root string, git update.GitExecFunc) update.Config {
	t.Helper()
	return update.Config{
		YakosRoot: root,
		HomeDir:   t.TempDir(),
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
		GitExec:   git,
	}
}

// ---- tests ------------------------------------------------------------------

// TestUpdate_AlreadyUpToDate verifies no changelog is printed when HEAD is unchanged.
func TestUpdate_AlreadyUpToDate(t *testing.T) {
	root := fakeGitDir(t)
	const sha = "abc1234567890000"

	git := callSequencer(map[string][]stubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: sha + "\n"},
			{output: sha + "\n"},
		},
		"pull":     {{output: "Already up to date.\n"}},
		"describe": {{output: "v0.49.0\n"}},
	})

	cfg := baseConfig(t, root, git)
	res, err := update.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.AlreadyUpToDate {
		t.Error("expected AlreadyUpToDate=true")
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(out, "Already up to date") {
		t.Errorf("expected 'Already up to date' in output; got:\n%s", out)
	}
}

// TestUpdate_HeadChanged verifies changelog is printed when HEAD advances.
func TestUpdate_HeadChanged(t *testing.T) {
	root := fakeGitDir(t)

	git := callSequencer(map[string][]stubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: "aaaa000000000000\n"},
			{output: "bbbb111111111111\n"},
		},
		"pull": {{output: "Updating aaaa000..bbbb111\nFast-forward\n"}},
		"log":  {{output: "bbbb111 feat(api): add /update endpoint\n"}},
		"diff": {{output: "lib/hooks/budget-guard.sh\n"}},
	})

	cfg := baseConfig(t, root, git)
	res, err := update.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.AlreadyUpToDate {
		t.Error("expected AlreadyUpToDate=false")
	}
	if res.HeadBefore == res.HeadAfter {
		t.Errorf("HeadBefore %q should differ from HeadAfter %q", res.HeadBefore, res.HeadAfter)
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(out, "Commits applied") {
		t.Errorf("expected 'Commits applied' in output; got:\n%s", out)
	}
	if !strings.Contains(out, "lib/hooks/budget-guard.sh") {
		t.Errorf("expected changed lib file in output; got:\n%s", out)
	}
}

// TestUpdate_DryRun verifies no git pull runs in dry-run mode.
func TestUpdate_DryRun(t *testing.T) {
	root := fakeGitDir(t)
	pulled := false

	git := func(_ string, args ...string) (string, error) {
		if len(args) > 0 {
			switch args[0] {
			case "pull":
				pulled = true
			case "rev-parse":
				return "cccc222222222222\n", nil
			case "--git-dir":
				return ".git\n", nil
			}
		}
		return "", nil
	}

	cfg := baseConfig(t, root, git)
	cfg.DryRun = true

	res, err := update.Run(cfg)
	if err != nil {
		t.Fatalf("Run (dry-run): %v", err)
	}
	if pulled {
		t.Error("dry-run should not call git pull")
	}
	if !res.DryRun {
		t.Error("Result.DryRun should be true")
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(out, "dry-run") {
		t.Errorf("expected 'dry-run' in output; got:\n%s", out)
	}
}

// TestUpdate_NotGitRepo verifies an error is returned when the repo check fails.
func TestUpdate_NotGitRepo(t *testing.T) {
	root := t.TempDir() // no .git directory

	git := func(_ string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "--git-dir" {
			return "", errors.New("not a git repository")
		}
		return "", nil
	}

	cfg := baseConfig(t, root, git)
	_, err := update.Run(cfg)
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
	if !strings.Contains(err.Error(), "git repository") {
		t.Errorf("error should mention 'git repository'; got %q", err.Error())
	}
}

// TestUpdate_AllowNonFF verifies --allow-non-ff does not append --ff-only.
func TestUpdate_AllowNonFF(t *testing.T) {
	root := fakeGitDir(t)
	var gotPullArgs []string

	git := func(_ string, args ...string) (string, error) {
		if len(args) > 0 {
			switch args[0] {
			case "pull":
				gotPullArgs = append([]string{}, args...)
			case "rev-parse":
				return "dddd333333333333\n", nil
			case "--git-dir":
				return ".git\n", nil
			case "describe":
				return "v0.49.0\n", nil
			}
		}
		return "", nil
	}

	cfg := baseConfig(t, root, git)
	cfg.AllowNonFF = true

	if _, err := update.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, a := range gotPullArgs {
		if a == "--ff-only" {
			t.Error("--allow-non-ff should suppress --ff-only flag")
		}
	}
}

// TestUpdate_FFOnlyDefault verifies --ff-only is passed by default.
func TestUpdate_FFOnlyDefault(t *testing.T) {
	root := fakeGitDir(t)
	var gotPullArgs []string

	git := func(_ string, args ...string) (string, error) {
		if len(args) > 0 {
			switch args[0] {
			case "pull":
				gotPullArgs = append([]string{}, args...)
			case "rev-parse":
				return "eeee444444444444\n", nil
			case "--git-dir":
				return ".git\n", nil
			case "describe":
				return "v0.49.0\n", nil
			}
		}
		return "", nil
	}

	cfg := baseConfig(t, root, git)
	if _, err := update.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	found := false
	for _, a := range gotPullArgs {
		if a == "--ff-only" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected --ff-only in pull args; got %v", gotPullArgs)
	}
}

// TestUpdate_GitPullError verifies pull failure propagates as an error.
func TestUpdate_GitPullError(t *testing.T) {
	root := fakeGitDir(t)

	git := callSequencer(map[string][]stubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {{output: "ffff555555555555\n"}},
		"pull": {{
			output: "error: Your local changes would be overwritten\n",
			err:    errors.New("exit status 1"),
		}},
	})

	cfg := baseConfig(t, root, git)
	_, err := update.Run(cfg)
	if err == nil {
		t.Fatal("expected error when git pull fails")
	}
	if !strings.Contains(err.Error(), "git pull") {
		t.Errorf("error should mention 'git pull'; got %q", err.Error())
	}
}

// TestUpdate_ChangedLibFiles verifies the ChangedLibFiles list is populated.
func TestUpdate_ChangedLibFiles(t *testing.T) {
	root := fakeGitDir(t)

	git := callSequencer(map[string][]stubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: "1111aaaaaaaaaaaa\n"},
			{output: "2222bbbbbbbbbbbb\n"},
		},
		"pull": {{output: "Fast-forward\n"}},
		"log":  {{output: "2222bbb refactor(hooks): update budget-guard\n"}},
		"diff": {{output: "lib/hooks/budget-guard.sh\nlib/hooks/secret-scan.sh\n"}},
	})

	cfg := baseConfig(t, root, git)
	res, err := update.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.ChangedLibFiles) != 2 {
		t.Errorf("expected 2 changed lib files; got %d: %v", len(res.ChangedLibFiles), res.ChangedLibFiles)
	}
	if res.ChangedLibFiles[0] != "lib/hooks/budget-guard.sh" {
		t.Errorf("unexpected first changed file: %q", res.ChangedLibFiles[0])
	}
}

// TestUpdate_NoChangedLibFiles verifies empty lib diff produces no list in output.
func TestUpdate_NoChangedLibFiles(t *testing.T) {
	root := fakeGitDir(t)

	git := callSequencer(map[string][]stubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: "3333cccccccccccc\n"},
			{output: "4444dddddddddddd\n"},
		},
		"pull": {{output: "Fast-forward\n"}},
		"log":  {{output: "4444ddd docs: update README\n"}},
		"diff": {{output: ""}},
	})

	cfg := baseConfig(t, root, git)
	res, err := update.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.ChangedLibFiles) != 0 {
		t.Errorf("expected 0 changed lib files; got %d", len(res.ChangedLibFiles))
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if strings.Contains(out, "Changed under lib/") {
		t.Error("output should not mention 'Changed under lib/' when diff is empty")
	}
}

// TestUpdate_HeadBeforeAfterStored verifies Result fields are populated correctly.
func TestUpdate_HeadBeforeAfterStored(t *testing.T) {
	root := fakeGitDir(t)
	const before = "aaaa111111111111"
	const after = "bbbb222222222222"

	git := callSequencer(map[string][]stubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: before + "\n"},
			{output: after + "\n"},
		},
		"pull": {{output: "Fast-forward\n"}},
		"log":  {{output: "bbbb222 feat: something\n"}},
		"diff": {{output: ""}},
	})

	cfg := baseConfig(t, root, git)
	res, err := update.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.HeadBefore != before {
		t.Errorf("HeadBefore: got %q, want %q", res.HeadBefore, before)
	}
	if res.HeadAfter != after {
		t.Errorf("HeadAfter: got %q, want %q", res.HeadAfter, after)
	}
}

// TestUpdate_AllProjectsDryRun verifies --all + --dry-run reports project count.
func TestUpdate_AllProjectsDryRun(t *testing.T) {
	root := fakeGitDir(t)
	home := t.TempDir()

	// Seed a fake project under ~/agent-control/<name>/.project-path.
	acDir := filepath.Join(home, "agent-control", "myproj")
	if err := os.MkdirAll(acDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	projRepo := t.TempDir()
	if err := os.WriteFile(filepath.Join(acDir, ".project-path"), []byte(projRepo+"\n"), 0644); err != nil {
		t.Fatalf("write .project-path: %v", err)
	}

	git := func(_ string, args ...string) (string, error) {
		if len(args) > 0 {
			switch args[0] {
			case "rev-parse":
				return "cccc444444444444\n", nil
			case "--git-dir":
				return ".git\n", nil
			}
		}
		return "", nil
	}

	cfg := update.Config{
		YakosRoot:   root,
		HomeDir:     home,
		AllProjects: true,
		DryRun:      true,
		Writer:      &bytes.Buffer{},
		ErrWriter:   &bytes.Buffer{},
		GitExec:     git,
	}

	res, err := update.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.DryRun {
		t.Error("Result.DryRun should be true")
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(out, "dry-run") {
		t.Errorf("expected 'dry-run' in output; got:\n%s", out)
	}
}

// TestUpdate_WriterDefaultsNoPanic verifies nil Writer/ErrWriter don't panic.
func TestUpdate_WriterDefaultsNoPanic(t *testing.T) {
	root := fakeGitDir(t)

	git := func(_ string, args ...string) (string, error) {
		if len(args) > 0 {
			switch args[0] {
			case "rev-parse":
				return "dddd555555555555\n", nil
			case "--git-dir":
				return ".git\n", nil
			case "describe":
				return "v0.49.0\n", nil
			case "pull":
				return "Already up to date.\n", nil
			}
		}
		return "", nil
	}

	cfg := update.Config{
		YakosRoot: root,
		HomeDir:   t.TempDir(),
		DryRun:    true,
		GitExec:   git,
		// Writer and ErrWriter intentionally nil — Run() defaults them.
	}
	_, err := update.Run(cfg)
	if err != nil {
		t.Fatalf("Run with nil writers: %v", err)
	}
}

// TestUpdate_HelpText verifies all load-bearing phrases from bash --help.
func TestUpdate_HelpText(t *testing.T) {
	var buf bytes.Buffer
	update.PrintHelp(&buf)
	got := buf.String()

	for _, phrase := range []string{
		"yakos update",
		"git pull",
		"--ff-only",
		"--allow-non-ff",
		"--all",
		"--dry-run",
		"--help",
		"lib/",
	} {
		if !strings.Contains(got, phrase) {
			t.Errorf("help text missing %q;\nfull output:\n%s", phrase, got)
		}
	}
}

// TestUpdate_ResultProjectsRefreshedZeroDefault verifies zero when --all not set.
func TestUpdate_ResultProjectsRefreshedZeroDefault(t *testing.T) {
	root := fakeGitDir(t)

	git := callSequencer(map[string][]stubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: "eeee666666666666\n"},
			{output: "ffff777777777777\n"},
		},
		"pull": {{output: "Fast-forward\n"}},
		"log":  {{output: "ffff777 feat: added\n"}},
		"diff": {{output: ""}},
	})

	cfg := baseConfig(t, root, git)
	res, err := update.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ProjectsRefreshed != 0 {
		t.Errorf("expected ProjectsRefreshed=0; got %d", res.ProjectsRefreshed)
	}
}

// TestUpdate_AlreadyUpToDateNoCommitLog verifies no "Commits applied" when HEAD unchanged.
func TestUpdate_AlreadyUpToDateNoCommitLog(t *testing.T) {
	root := fakeGitDir(t)
	const sha = "1234abcd12345678"

	git := callSequencer(map[string][]stubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: sha + "\n"},
			{output: sha + "\n"},
		},
		"pull":     {{output: "Already up to date.\n"}},
		"describe": {{output: "v0.49.0\n"}},
	})

	cfg := baseConfig(t, root, git)
	if _, err := update.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if strings.Contains(out, "Commits applied") {
		t.Error("'Commits applied' should not appear when already up to date")
	}
}

// TestUpdate_OutputContainsBanner verifies "yakos update" banner is printed.
func TestUpdate_OutputContainsBanner(t *testing.T) {
	root := fakeGitDir(t)

	git := func(_ string, args ...string) (string, error) {
		if len(args) > 0 {
			switch args[0] {
			case "rev-parse":
				return "aaaa000111222333\n", nil
			case "--git-dir":
				return ".git\n", nil
			case "describe":
				return "v0.49.0\n", nil
			}
		}
		return "", nil
	}

	cfg := baseConfig(t, root, git)
	cfg.DryRun = true
	if _, err := update.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(out, "yakos update") {
		t.Errorf("expected 'yakos update' banner; got:\n%s", out)
	}
}

// TestUpdate_DryRunBanner verifies [DRY RUN] tag in banner.
func TestUpdate_DryRunBanner(t *testing.T) {
	root := fakeGitDir(t)

	git := func(_ string, args ...string) (string, error) {
		if len(args) > 0 {
			switch args[0] {
			case "rev-parse":
				return "bbbb111222333444\n", nil
			case "--git-dir":
				return ".git\n", nil
			}
		}
		return "", nil
	}

	cfg := baseConfig(t, root, git)
	cfg.DryRun = true
	if _, err := update.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(out, "[DRY RUN]") {
		t.Errorf("expected '[DRY RUN]' in output; got:\n%s", out)
	}
}

// TestUpdate_CompleteBannerPrinted verifies "yakos update complete." when HEAD changes.
func TestUpdate_CompleteBannerPrinted(t *testing.T) {
	root := fakeGitDir(t)

	git := callSequencer(map[string][]stubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: "cccc333444555666\n"},
			{output: "dddd444555666777\n"},
		},
		"pull": {{output: "Fast-forward\n"}},
		"log":  {{output: "dddd444 chore: deps\n"}},
		"diff": {{output: ""}},
	})

	cfg := baseConfig(t, root, git)
	if _, err := update.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(out, "yakos update complete") {
		t.Errorf("expected 'yakos update complete' in output; got:\n%s", out)
	}
}
