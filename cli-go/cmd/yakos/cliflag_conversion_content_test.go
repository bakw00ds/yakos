package main

import (
	"os"
	"strings"
	"testing"
)

// TestContentFlagErrorText_BeforeAndAfterConversion pins the exact stderr and
// exit code of every hand-rolled flag loop in cmd_content.go (agent
// new/lint/diff/list/docs, plugin install/remove/validate/register, teach,
// skill candidates/promote/reject). The table was captured against the
// pre-cliflag binary (13e85ef) and must pass unchanged after conversion.
// Every case exits before any real work, and runs under a throwaway HOME.
func TestContentFlagErrorText_BeforeAndAfterConversion(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v (run `make build` first)", goBin, err)
	}
	home := t.TempDir()
	env := map[string]string{"HOME": home}

	q := func(s string) string { return "\"" + s + "\"" }
	cases := []flagErrorCase{
		// ---- agent new ----
		{name: "agent_new_runtime_missing", args: []string{"agent", "new", "--runtime"}, wantStderr: "agent new: --runtime requires an id\n", wantExit: 1},
		{name: "agent_new_project_missing", args: []string{"agent", "new", "--project"}, wantStderr: "agent new: --project requires a path\n", wantExit: 1},
		{name: "agent_new_extends_missing", args: []string{"agent", "new", "--extends"}, wantStderr: "agent new: --extends requires an id\n", wantExit: 1},
		{name: "agent_new_role_missing", args: []string{"agent", "new", "--role"}, wantStderr: "agent new: --role requires a value\n", wantExit: 1},
		{name: "agent_new_domain_missing", args: []string{"agent", "new", "--domain"}, wantStderr: "agent new: --domain requires a value\n", wantExit: 1},
		{name: "agent_new_model_missing", args: []string{"agent", "new", "--model"}, wantStderr: "agent new: --model requires a value\n", wantExit: 1},
		{name: "agent_new_tools_missing", args: []string{"agent", "new", "--tools"}, wantStderr: "agent new: --tools requires a value\n", wantExit: 1},
		{name: "agent_new_unknown_flag", args: []string{"agent", "new", "--bogus"}, wantStderr: "agent new: unknown flag " + q("--bogus") + "\n", wantExit: 1},
		{name: "agent_new_double_dash", args: []string{"agent", "new", "--"}, wantStderr: "agent new: unknown flag " + q("--") + "\n", wantExit: 1},
		{name: "agent_new_bare_equals", args: []string{"agent", "new", "--runtime="}, wantStderr: "agent new: unknown flag " + q("--runtime=") + "\n", wantExit: 1},
		{name: "agent_new_too_many_pos", args: []string{"agent", "new", "a", "b"}, wantStderr: "agent new: too many positional args\n", wantExit: 1},
		{name: "agent_new_name_required", args: []string{"agent", "new", "--force"}, wantSubstr: "agent new: <name> required\n", wantExit: 1},
		{name: "agent_new_equals_then_missing", args: []string{"agent", "new", "--role=x", "--model"}, wantStderr: "agent new: --model requires a value\n", wantExit: 1},
		// ---- agent lint ----
		{name: "agent_lint_unknown_flag", args: []string{"agent", "lint", "--bogus"}, wantStderr: "agent lint: unknown flag " + q("--bogus") + "\n", wantExit: 1},
		{name: "agent_lint_double_dash", args: []string{"agent", "lint", "--"}, wantStderr: "agent lint: unknown flag " + q("--") + "\n", wantExit: 1},
		{name: "agent_lint_too_many_pos", args: []string{"agent", "lint", "a", "b"}, wantStderr: "agent lint: too many positional args\n", wantExit: 1},
		{name: "agents_lint_too_many_pos", args: []string{"agents", "lint", "a", "b"}, wantStderr: "agent lint: too many positional args\n", wantExit: 1},
		// ---- agent diff ----
		{name: "agent_diff_project_missing", args: []string{"agent", "diff", "--project"}, wantStderr: "agent diff: --project requires a path\n", wantExit: 1},
		{name: "agent_diff_unknown_flag", args: []string{"agent", "diff", "--bogus"}, wantStderr: "agent diff: unknown flag " + q("--bogus") + "\n", wantExit: 1},
		{name: "agent_diff_double_dash", args: []string{"agent", "diff", "--"}, wantStderr: "agent diff: unknown flag " + q("--") + "\n", wantExit: 1},
		{name: "agent_diff_too_many_pos", args: []string{"agent", "diff", "a", "b"}, wantStderr: "agent diff: too many positional args\n", wantExit: 1},
		{name: "agent_diff_name_required", args: []string{"agent", "diff", "--project=/x"}, wantStderr: "agent diff: <name> required\n", wantExit: 1},
		// ---- agent list ----
		{name: "agent_list_project_missing", args: []string{"agent", "list", "--project"}, wantStderr: "agent list: --project requires a path\n", wantExit: 1},
		{name: "agent_list_unknown_flag", args: []string{"agent", "list", "--bogus"}, wantStderr: "agent list: unknown flag " + q("--bogus") + "\n", wantExit: 1},
		{name: "agent_list_double_dash", args: []string{"agent", "list", "--"}, wantStderr: "agent list: unknown flag " + q("--") + "\n", wantExit: 1},
		{name: "agent_list_unexpected_arg", args: []string{"agent", "list", "x"}, wantStderr: "agent list: unexpected argument " + q("x") + "\n", wantExit: 1},
		// ---- agent docs ----
		{name: "agent_docs_format_missing", args: []string{"agent", "docs", "--format"}, wantStderr: "agent docs: --format requires md or html\n", wantExit: 1},
		{name: "agent_docs_format_bad_space", args: []string{"agent", "docs", "--format", "pdf"}, wantStderr: "agent docs: unknown format " + q("pdf") + " (md or html)\n", wantExit: 1},
		{name: "agent_docs_format_bad_equals", args: []string{"agent", "docs", "--format=pdf"}, wantStderr: "agent docs: unknown format " + q("pdf") + "\n", wantExit: 1},
		{name: "agent_docs_format_bare_equals", args: []string{"agent", "docs", "--format="}, wantStderr: "agent docs: unknown flag " + q("--format=") + "\n", wantExit: 1},
		{name: "agent_docs_project_missing", args: []string{"agent", "docs", "--project"}, wantStderr: "agent docs: --project requires a path\n", wantExit: 1},
		{name: "agent_docs_out_missing", args: []string{"agent", "docs", "--out"}, wantStderr: "agent docs: --out requires a path\n", wantExit: 1},
		{name: "agent_docs_unknown_flag", args: []string{"agent", "docs", "--bogus"}, wantStderr: "agent docs: unknown flag " + q("--bogus") + "\n", wantExit: 1},
		{name: "agent_docs_double_dash", args: []string{"agent", "docs", "--"}, wantStderr: "agent docs: unknown flag " + q("--") + "\n", wantExit: 1},
		{name: "agent_docs_unexpected_arg", args: []string{"agent", "docs", "x"}, wantStderr: "agent docs: unexpected argument " + q("x") + "\n", wantExit: 1},
		// ---- plugin ----
		{name: "plugin_install_id_missing", args: []string{"plugin", "install", "--id"}, wantStderr: "plugin install: --id requires a value\n", wantExit: 1},
		{name: "plugin_install_unknown_flag", args: []string{"plugin", "install", "--bogus"}, wantStderr: "plugin install: unknown flag " + q("--bogus") + " (try --help)\n", wantExit: 1},
		{name: "plugin_install_double_dash", args: []string{"plugin", "install", "--"}, wantStderr: "plugin install: unknown flag " + q("--") + " (try --help)\n", wantExit: 1},
		{name: "plugin_install_bare_equals", args: []string{"plugin", "install", "--id="}, wantStderr: "plugin install: unknown flag " + q("--id=") + " (try --help)\n", wantExit: 1},
		{name: "plugin_install_too_many_pos", args: []string{"plugin", "install", "a", "b"}, wantStderr: "plugin install: too many positional args (try --help)\n", wantExit: 1},
		{name: "plugin_remove_unknown_flag", args: []string{"plugin", "remove", "--bogus"}, wantStderr: "plugin remove: unknown flag " + q("--bogus") + " (try --help)\n", wantExit: 1},
		{name: "plugin_remove_double_dash", args: []string{"plugin", "remove", "--"}, wantStderr: "plugin remove: unknown flag " + q("--") + " (try --help)\n", wantExit: 1},
		{name: "plugin_remove_too_many_pos", args: []string{"plugin", "remove", "a", "b"}, wantStderr: "plugin remove: too many positional args (try --help)\n", wantExit: 1},
		{name: "plugin_validate_id_missing", args: []string{"plugin", "validate", "--id"}, wantStderr: "plugin validate: --id requires a value\n", wantExit: 1},
		{name: "plugin_validate_unknown_flag", args: []string{"plugin", "validate", "--bogus"}, wantStderr: "plugin validate: unknown flag " + q("--bogus") + " (try --help)\n", wantExit: 1},
		{name: "plugin_validate_double_dash", args: []string{"plugin", "validate", "--"}, wantStderr: "plugin validate: unknown flag " + q("--") + " (try --help)\n", wantExit: 1},
		{name: "plugin_validate_too_many_pos", args: []string{"plugin", "validate", "a", "b"}, wantStderr: "plugin validate: too many positional args (try --help)\n", wantExit: 1},
		{name: "plugin_register_unknown_flag", args: []string{"plugin", "register", "--bogus"}, wantStderr: "plugin register: unknown flag " + q("--bogus") + " (try --help)\n", wantExit: 1},
		{name: "plugin_register_double_dash", args: []string{"plugin", "register", "--"}, wantStderr: "plugin register: unknown flag " + q("--") + " (try --help)\n", wantExit: 1},
		{name: "plugin_register_too_many_pos", args: []string{"plugin", "register", "a", "b", "c"}, wantStderr: "plugin register: too many positional args (try --help)\n", wantExit: 1},
		// ---- teach ----
		{name: "teach_project_missing", args: []string{"teach", "--project"}, wantStderr: "teach: --project requires a path\n", wantExit: 1},
		{name: "teach_section_missing", args: []string{"teach", "--section"}, wantStderr: "teach: --section requires a name\n", wantExit: 1},
		{name: "teach_unknown_flag", args: []string{"teach", "--bogus"}, wantStderr: "teach: unknown flag " + q("--bogus") + " (try --help)\n", wantExit: 1},
		{name: "teach_double_dash", args: []string{"teach", "--"}, wantStderr: "teach: unknown flag " + q("--") + " (try --help)\n", wantExit: 1},
		{name: "teach_bare_equals", args: []string{"teach", "--section="}, wantStderr: "teach: unknown flag " + q("--section=") + " (try --help)\n", wantExit: 1},
		{name: "teach_too_many_pos", args: []string{"teach", "a", "b", "c"}, wantStderr: "teach: too many positional args (try --help)\n", wantExit: 1},
		{name: "teach_agent_required", args: []string{"teach", "--dry-run"}, wantSubstr: "teach: <agent-name> required\n", wantExit: 1},
		{name: "teach_lesson_required", args: []string{"teach", "a", "--dry-run"}, wantSubstr: "teach: <lesson-file> required\n", wantExit: 1},
		// ---- skill ----
		{name: "skill_candidates_unknown_flag", args: []string{"skill", "candidates", "--bogus"}, wantStderr: "skill candidates: unknown flag " + q("--bogus") + " (try --help)\n", wantExit: 1},
		{name: "skill_candidates_double_dash", args: []string{"skill", "candidates", "--"}, wantStderr: "skill candidates: unknown flag " + q("--") + " (try --help)\n", wantExit: 1},
		{name: "skill_candidates_positional", args: []string{"skill", "candidates", "x"}, wantStderr: "skill candidates: unknown flag " + q("x") + " (try --help)\n", wantExit: 1},
		{name: "skill_candidates_help_flag", args: []string{"skill", "candidates", "-h"}, wantStderr: "skill candidates: unknown flag " + q("-h") + " (try --help)\n", wantExit: 1},
		{name: "skill_promote_project_missing", args: []string{"skill", "promote", "--project"}, wantStderr: "skill promote: --project requires a path\n", wantExit: 1},
		{name: "skill_promote_unknown_flag", args: []string{"skill", "promote", "--bogus"}, wantStderr: "skill promote: unknown flag " + q("--bogus") + " (try --help)\n", wantExit: 1},
		{name: "skill_promote_help_flag_is_unknown", args: []string{"skill", "promote", "-h"}, wantStderr: "skill promote: unknown flag " + q("-h") + " (try --help)\n", wantExit: 1},
		{name: "skill_promote_double_dash", args: []string{"skill", "promote", "--"}, wantStderr: "skill promote: unknown flag " + q("--") + " (try --help)\n", wantExit: 1},
		{name: "skill_promote_too_many_pos", args: []string{"skill", "promote", "a", "b"}, wantStderr: "skill promote: too many positional args\n", wantExit: 1},
		{name: "skill_promote_slug_required", args: []string{"skill", "promote", "--global"}, wantSubstr: "skill promote: <slug> required\n", wantExit: 1},
		{name: "skill_reject_reason_missing", args: []string{"skill", "reject", "--reason"}, wantStderr: "skill reject: --reason requires a value\n", wantExit: 1},
		{name: "skill_reject_unknown_flag", args: []string{"skill", "reject", "--bogus"}, wantStderr: "skill reject: unknown flag " + q("--bogus") + " (try --help)\n", wantExit: 1},
		{name: "skill_reject_double_dash", args: []string{"skill", "reject", "--"}, wantStderr: "skill reject: unknown flag " + q("--") + " (try --help)\n", wantExit: 1},
		{name: "skill_reject_bare_equals", args: []string{"skill", "reject", "--reason="}, wantStderr: "skill reject: unknown flag " + q("--reason=") + " (try --help)\n", wantExit: 1},
		{name: "skill_reject_too_many_pos", args: []string{"skill", "reject", "a", "b"}, wantStderr: "skill reject: too many positional args\n", wantExit: 1},
		{name: "skill_reject_slug_required", args: []string{"skill", "reject", "--reason=x"}, wantSubstr: "skill reject: <slug> required\n", wantExit: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runGoSplit(t, tc.args, env)
			if code != tc.wantExit {
				t.Errorf("exit = %d, want %d (stderr=%q)", code, tc.wantExit, stderr)
			}
			if tc.wantStderr != "" && stderr != tc.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr, tc.wantStderr)
			}
			if tc.wantSubstr != "" && !strings.Contains(stderr, tc.wantSubstr) {
				t.Errorf("stderr = %q, want substring %q", stderr, tc.wantSubstr)
			}
		})
	}
}

// TestContentFlagHelpAndSuccessPaths pins that -h/--help still exit 0 with
// non-empty stdout for every converted subcommand, in any argv position
// before an error-free tail.
func TestContentFlagHelpAndSuccessPaths(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v (run `make build` first)", goBin, err)
	}
	env := map[string]string{"HOME": t.TempDir()}
	for _, args := range [][]string{
		{"agent", "new", "-h"}, {"agent", "new", "--help"},
		{"agent", "lint", "--help"}, {"agent", "diff", "--help"}, {"agent", "list", "-h"},
		{"agent", "docs", "--help"}, {"agent", "docs", "-h"},
		{"plugin", "install", "--help"}, {"plugin", "remove", "-h"},
		{"plugin", "validate", "--help"}, {"plugin", "register", "-h"},
		{"teach", "--help"}, {"teach", "-h"},
	} {
		stdout, stderr, code := runGoSplit(t, args, env)
		if code != 0 || stdout == "" {
			t.Errorf("%v: exit=%d stdout-len=%d stderr=%q, want exit 0 with help on stdout", args, code, len(stdout), stderr)
		}
	}
}
