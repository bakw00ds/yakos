// update_parity_test.go — Phase 1 parity tests for `yakos update`.
//
// Design notes:
//
//  1. All tests use update.Run directly with temp dirs and a fake GitExec
//     injection point so no real git pull or network call occurs.
//
//  2. Help-text parity verifies load-bearing phrases from cli/lib/update.sh
//     usage() appear in the Go output.
//
//  3. The bash update.sh runs `git pull --ff-only` (or `git pull`) in
//     YAKOS_ROOT, then calls install.sh if HEAD changed. The Go port calls
//     refresh.CollectProjects + refresh.Run instead (refresh is the Go port
//     of the per-project sync that install.sh covers for hook drift).
//
// Critical scenarios verified:
//
//	(a) already-up-to-date        — HEAD unchanged; "Already up to date" printed
//	(b) head-changed              — commit log + lib diff printed
//	(c) dry-run                   — no git pull issued; [DRY RUN] tag in output
//	(d) not-a-git-repo            — error returned with "git repository" in msg
//	(e) allow-non-ff              — --ff-only absent from pull args
//	(f) ff-only-default           — --ff-only present by default
//	(g) pull-error                — exit with wrapped error
//	(h) changed-lib-files         — ChangedLibFiles list populated
//	(i) no-changed-lib-files      — empty diff → no lib section in output
//	(j) head-before-after-stored  — Result fields correct
//	(k) all-projects-dry-run      — --all + --dry-run reports project count
//	(l) writer-defaults-no-panic  — nil Writer/ErrWriter replaced without panic
//	(m) help-text                 — all load-bearing phrases from bash usage()
//	(n) projects-refreshed-zero   — ProjectsRefreshed=0 when --all not set
//	(o) already-up-to-date-no-log — no "Commits applied" when unchanged
//	(p) banner-present            — "yakos update" line always printed
//	(q) dry-run-tag               — [DRY RUN] appears when --dry-run set
//	(r) complete-banner           — "yakos update complete." when HEAD changed
package main

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

// newUpdateFakeRoot creates a minimal YAKOS_ROOT with a .git directory.
func newUpdateFakeRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	return root
}

// updateStubEntry is one canned response for a git sub-command key.
type updateStubEntry struct {
	output string
	err    error
}

// updateCallSequencer returns a GitExecFunc that plays back responses in order
// per first-arg key. When a key's list is exhausted, the last entry replays.
func updateCallSequencer(sequences map[string][]updateStubEntry) update.GitExecFunc {
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

// updateBaseConfig returns a Config with captured writers, isolated home dir.
func updateBaseConfig(t *testing.T, root string, git update.GitExecFunc) update.Config {
	t.Helper()
	return update.Config{
		YakosRoot: root,
		HomeDir:   t.TempDir(),
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
		GitExec:   git,
	}
}

// ---- (a) already-up-to-date -------------------------------------------------

// TestUpdateParity_AlreadyUpToDate verifies "Already up to date" when HEAD unchanged.
func TestUpdateParity_AlreadyUpToDate(t *testing.T) {
	root := newUpdateFakeRoot(t)
	const sha = "abc1234567890000"

	git := updateCallSequencer(map[string][]updateStubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: sha + "\n"},
			{output: sha + "\n"},
		},
		"pull":     {{output: "Already up to date.\n"}},
		"describe": {{output: "v0.50.0\n"}},
	})

	cfg := updateBaseConfig(t, root, git)
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

// ---- (b) head-changed -------------------------------------------------------

// TestUpdateParity_HeadChanged verifies commit log + lib diff when HEAD advances.
func TestUpdateParity_HeadChanged(t *testing.T) {
	root := newUpdateFakeRoot(t)

	git := updateCallSequencer(map[string][]updateStubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: "aaaa000000000000\n"},
			{output: "bbbb111111111111\n"},
		},
		"pull": {{output: "Updating aaaa000..bbbb111\nFast-forward\n"}},
		"log":  {{output: "bbbb111 feat(api): add /update endpoint\n"}},
		"diff": {{output: "lib/hooks/budget-guard.sh\n"}},
	})

	cfg := updateBaseConfig(t, root, git)
	res, err := update.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.AlreadyUpToDate {
		t.Error("expected AlreadyUpToDate=false")
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(out, "Commits applied") {
		t.Errorf("expected 'Commits applied'; got:\n%s", out)
	}
	if !strings.Contains(out, "lib/hooks/budget-guard.sh") {
		t.Errorf("expected lib file in output; got:\n%s", out)
	}
}

// ---- (c) dry-run ------------------------------------------------------------

// TestUpdateParity_DryRun verifies no git pull in dry-run mode.
func TestUpdateParity_DryRun(t *testing.T) {
	root := newUpdateFakeRoot(t)
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

	cfg := updateBaseConfig(t, root, git)
	cfg.DryRun = true

	res, err := update.Run(cfg)
	if err != nil {
		t.Fatalf("Run (dry-run): %v", err)
	}
	if pulled {
		t.Error("dry-run should not issue git pull")
	}
	if !res.DryRun {
		t.Error("Result.DryRun should be true")
	}
}

// ---- (d) not-a-git-repo -----------------------------------------------------

// TestUpdateParity_NotGitRepo verifies error when git check fails.
func TestUpdateParity_NotGitRepo(t *testing.T) {
	root := t.TempDir() // no .git directory

	git := func(_ string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "--git-dir" {
			return "", errors.New("not a git repository")
		}
		return "", nil
	}

	cfg := updateBaseConfig(t, root, git)
	_, err := update.Run(cfg)
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
	if !strings.Contains(err.Error(), "git repository") {
		t.Errorf("error should mention 'git repository'; got %q", err.Error())
	}
}

// ---- (e) allow-non-ff -------------------------------------------------------

// TestUpdateParity_AllowNonFF verifies --allow-non-ff suppresses --ff-only.
func TestUpdateParity_AllowNonFF(t *testing.T) {
	root := newUpdateFakeRoot(t)
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
				return "v0.50.0\n", nil
			}
		}
		return "", nil
	}

	cfg := updateBaseConfig(t, root, git)
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

// ---- (f) ff-only-default ----------------------------------------------------

// TestUpdateParity_FFOnlyDefault verifies --ff-only is the default.
func TestUpdateParity_FFOnlyDefault(t *testing.T) {
	root := newUpdateFakeRoot(t)
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
				return "v0.50.0\n", nil
			}
		}
		return "", nil
	}

	cfg := updateBaseConfig(t, root, git)
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

// ---- (g) pull-error ---------------------------------------------------------

// TestUpdateParity_PullError verifies git pull failure propagates as error.
func TestUpdateParity_PullError(t *testing.T) {
	root := newUpdateFakeRoot(t)

	git := updateCallSequencer(map[string][]updateStubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {{output: "ffff555555555555\n"}},
		"pull": {{
			output: "error: cannot fast-forward\n",
			err:    errors.New("exit status 128"),
		}},
	})

	cfg := updateBaseConfig(t, root, git)
	_, err := update.Run(cfg)
	if err == nil {
		t.Fatal("expected error for failing git pull")
	}
	if !strings.Contains(err.Error(), "git pull") {
		t.Errorf("error should mention 'git pull'; got %q", err.Error())
	}
}

// ---- (h) changed-lib-files --------------------------------------------------

// TestUpdateParity_ChangedLibFiles verifies ChangedLibFiles is populated.
func TestUpdateParity_ChangedLibFiles(t *testing.T) {
	root := newUpdateFakeRoot(t)

	git := updateCallSequencer(map[string][]updateStubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: "1111aaaaaaaaaaaa\n"},
			{output: "2222bbbbbbbbbbbb\n"},
		},
		"pull": {{output: "Fast-forward\n"}},
		"log":  {{output: "2222bbb refactor(hooks): update budget-guard\n"}},
		"diff": {{output: "lib/hooks/budget-guard.sh\nlib/hooks/secret-scan.sh\n"}},
	})

	cfg := updateBaseConfig(t, root, git)
	res, err := update.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.ChangedLibFiles) != 2 {
		t.Errorf("expected 2 changed lib files; got %d: %v", len(res.ChangedLibFiles), res.ChangedLibFiles)
	}
}

// ---- (i) no-changed-lib-files -----------------------------------------------

// TestUpdateParity_NoChangedLibFiles verifies no lib section when diff empty.
func TestUpdateParity_NoChangedLibFiles(t *testing.T) {
	root := newUpdateFakeRoot(t)

	git := updateCallSequencer(map[string][]updateStubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: "3333cccccccccccc\n"},
			{output: "4444dddddddddddd\n"},
		},
		"pull": {{output: "Fast-forward\n"}},
		"log":  {{output: "4444ddd docs: README\n"}},
		"diff": {{output: ""}},
	})

	cfg := updateBaseConfig(t, root, git)
	res, err := update.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.ChangedLibFiles) != 0 {
		t.Errorf("expected empty ChangedLibFiles; got %v", res.ChangedLibFiles)
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if strings.Contains(out, "Changed under lib/") {
		t.Error("output should not mention 'Changed under lib/' when diff is empty")
	}
}

// ---- (j) head-before-after-stored -------------------------------------------

// TestUpdateParity_HeadBeforeAfterStored verifies Result fields are correct.
func TestUpdateParity_HeadBeforeAfterStored(t *testing.T) {
	root := newUpdateFakeRoot(t)
	const before = "aaaa111111111111"
	const after = "bbbb222222222222"

	git := updateCallSequencer(map[string][]updateStubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: before + "\n"},
			{output: after + "\n"},
		},
		"pull": {{output: "Fast-forward\n"}},
		"log":  {{output: "bbbb222 feat: something\n"}},
		"diff": {{output: ""}},
	})

	cfg := updateBaseConfig(t, root, git)
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

// ---- (k) all-projects-dry-run -----------------------------------------------

// TestUpdateParity_AllProjectsDryRun verifies --all + --dry-run output.
func TestUpdateParity_AllProjectsDryRun(t *testing.T) {
	root := newUpdateFakeRoot(t)
	home := t.TempDir()

	acDir := filepath.Join(home, "agent-control", "testproj")
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

// ---- (l) writer-defaults-no-panic -------------------------------------------

// TestUpdateParity_WriterDefaultsNoPanic verifies nil writers don't panic.
func TestUpdateParity_WriterDefaultsNoPanic(t *testing.T) {
	root := newUpdateFakeRoot(t)

	git := func(_ string, args ...string) (string, error) {
		if len(args) > 0 {
			switch args[0] {
			case "rev-parse":
				return "dddd555555555555\n", nil
			case "--git-dir":
				return ".git\n", nil
			case "describe":
				return "v0.50.0\n", nil
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
	}
	if _, err := update.Run(cfg); err != nil {
		t.Fatalf("Run with nil writers: %v", err)
	}
}

// ---- (m) help-text ----------------------------------------------------------

// TestUpdateParity_HelpText verifies all load-bearing phrases from bash usage().
func TestUpdateParity_HelpText(t *testing.T) {
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

// ---- (n) projects-refreshed-zero --------------------------------------------

// TestUpdateParity_ProjectsRefreshedZero verifies default zero when --all unset.
func TestUpdateParity_ProjectsRefreshedZero(t *testing.T) {
	root := newUpdateFakeRoot(t)

	git := updateCallSequencer(map[string][]updateStubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: "eeee666666666666\n"},
			{output: "ffff777777777777\n"},
		},
		"pull": {{output: "Fast-forward\n"}},
		"log":  {{output: "ffff777 feat: added\n"}},
		"diff": {{output: ""}},
	})

	cfg := updateBaseConfig(t, root, git)
	res, err := update.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ProjectsRefreshed != 0 {
		t.Errorf("expected ProjectsRefreshed=0; got %d", res.ProjectsRefreshed)
	}
}

// ---- (o) already-up-to-date-no-log ------------------------------------------

// TestUpdateParity_AlreadyUpToDateNoLog verifies no "Commits applied" line.
func TestUpdateParity_AlreadyUpToDateNoLog(t *testing.T) {
	root := newUpdateFakeRoot(t)
	const sha = "1234abcd12345678"

	git := updateCallSequencer(map[string][]updateStubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: sha + "\n"},
			{output: sha + "\n"},
		},
		"pull":     {{output: "Already up to date.\n"}},
		"describe": {{output: "v0.50.0\n"}},
	})

	cfg := updateBaseConfig(t, root, git)
	if _, err := update.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if strings.Contains(out, "Commits applied") {
		t.Error("'Commits applied' should not appear when already up to date")
	}
}

// ---- (p) banner-present -----------------------------------------------------

// TestUpdateParity_BannerPresent verifies "yakos update" line is always printed.
func TestUpdateParity_BannerPresent(t *testing.T) {
	root := newUpdateFakeRoot(t)

	git := func(_ string, args ...string) (string, error) {
		if len(args) > 0 {
			switch args[0] {
			case "rev-parse":
				return "aaaa000111222333\n", nil
			case "--git-dir":
				return ".git\n", nil
			case "describe":
				return "v0.50.0\n", nil
			}
		}
		return "", nil
	}

	cfg := updateBaseConfig(t, root, git)
	cfg.DryRun = true
	if _, err := update.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(out, "yakos update") {
		t.Errorf("expected 'yakos update' banner; got:\n%s", out)
	}
}

// ---- (q) dry-run-tag --------------------------------------------------------

// TestUpdateParity_DryRunTag verifies [DRY RUN] appears in banner.
func TestUpdateParity_DryRunTag(t *testing.T) {
	root := newUpdateFakeRoot(t)

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

	cfg := updateBaseConfig(t, root, git)
	cfg.DryRun = true
	if _, err := update.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(out, "[DRY RUN]") {
		t.Errorf("expected '[DRY RUN]' in output; got:\n%s", out)
	}
}

// ---- (r) complete-banner ----------------------------------------------------

// TestUpdateParity_CompleteBanner verifies "yakos update complete." when HEAD changed.
func TestUpdateParity_CompleteBanner(t *testing.T) {
	root := newUpdateFakeRoot(t)

	git := updateCallSequencer(map[string][]updateStubEntry{
		"--git-dir": {{output: ".git\n"}},
		"rev-parse": {
			{output: "cccc333444555666\n"},
			{output: "dddd444555666777\n"},
		},
		"pull": {{output: "Fast-forward\n"}},
		"log":  {{output: "dddd444 chore: deps\n"}},
		"diff": {{output: ""}},
	})

	cfg := updateBaseConfig(t, root, git)
	if _, err := update.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(out, "yakos update complete") {
		t.Errorf("expected 'yakos update complete' in output; got:\n%s", out)
	}
}
