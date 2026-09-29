package main

import (
	"os"
	"strings"
	"testing"
)

// TestWorkFlagErrorText_BeforeAndAfterConversion pins the exact stderr text
// and exit code cmd_work.go's hand-rolled flag loops produced before they
// were converted to cliflag.Set (runArchive, runSession, runCheckpoint
// clean, runSupervise tail/ack/ack-all, runPlan history/override/correlate/
// show, runWorkClose, runModelRouting). The table was first run against a
// binary built from the pre-conversion base and must keep passing
// byte-for-byte afterwards. Run it against another binary with
// YAKOS_GO_BINARY=<path>.
func TestWorkFlagErrorText_BeforeAndAfterConversion(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v (run `make build` first)", goBin, err)
	}
	home := t.TempDir()
	env := map[string]string{"HOME": home, "YAKOS_ROOT": "", "YAKOS_LIB": ""}

	cases := []flagErrorCase{
		// ---- archive ----
		{name: "archive_unknown_flag", args: []string{"archive", "--bogus"}, wantStderr: "archive: unknown flag \"--bogus\"\n", wantExit: 1},
		{name: "archive_double_dash", args: []string{"archive", "--"}, wantStderr: "archive: unknown flag \"--\"\n", wantExit: 1},
		{name: "archive_single_dash", args: []string{"archive", "-"}, wantStderr: "archive: unknown flag \"-\"\n", wantExit: 1},
		{name: "archive_too_many", args: []string{"archive", "a", "b", "c"}, wantStderr: "archive: too many positional args\n", wantExit: 1},
		{name: "archive_missing_project", args: []string{"archive", "--yes"}, wantSubstr: "archive: missing <project>\n", wantExit: 1},
		{name: "archive_missing_tag", args: []string{"archive", "proj", "-y"}, wantSubstr: "archive: missing <tag>\n", wantExit: 1},
		// ---- session ----
		{name: "session_unknown_flag", args: []string{"session", "list", "--bogus"}, wantStderr: "session: unknown flag \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "session_double_dash", args: []string{"session", "list", "--"}, wantStderr: "session: unknown flag \"--\" (try --help)\n", wantExit: 1},
		{name: "session_too_many", args: []string{"session", "list", "a", "b", "c"}, wantStderr: "session: too many positional args (try --help)\n", wantExit: 1},
		// ---- checkpoint clean ----
		{name: "checkpoint_clean_age_missing", args: []string{"checkpoint", "clean", "--age"}, wantStderr: "checkpoint clean: --age requires a number (days)\n", wantExit: 1},
		{name: "checkpoint_clean_age_bad", args: []string{"checkpoint", "clean", "--age", "x"}, wantStderr: "checkpoint clean: --age value \"x\" is not a positive integer\n", wantExit: 1},
		{name: "checkpoint_clean_age_eq_bad", args: []string{"checkpoint", "clean", "--age=0"}, wantStderr: "checkpoint clean: --age value \"0\" is not a positive integer\n", wantExit: 1},
		{name: "checkpoint_clean_age_bare_eq", args: []string{"checkpoint", "clean", "--age="}, wantStderr: "checkpoint clean: unknown flag \"--age=\" (try --help)\n", wantExit: 1},
		{name: "checkpoint_clean_unknown", args: []string{"checkpoint", "clean", "--bogus"}, wantStderr: "checkpoint clean: unknown flag \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "checkpoint_clean_positional", args: []string{"checkpoint", "clean", "pos"}, wantStderr: "checkpoint clean: unknown flag \"pos\" (try --help)\n", wantExit: 1},
		{name: "checkpoint_clean_double_dash", args: []string{"checkpoint", "clean", "--"}, wantStderr: "checkpoint clean: unknown flag \"--\" (try --help)\n", wantExit: 1},
		// ---- compact threshold ----
		{name: "compact_threshold_auto_missing", args: []string{"compact", "threshold", "--auto"}, wantStderr: "compact threshold: --auto requires a value (e.g. --auto 85)\n", wantExit: 1},
		{name: "compact_threshold_too_many", args: []string{"compact", "threshold", "1", "2"}, wantStderr: "compact threshold: too many arguments\n", wantExit: 1},
		// "--auto=85" is now recognized by cliflag (was an ordinary positional): with a
		// second positional it is "too many"; alone it sets the threshold (covered below).
		{name: "compact_threshold_auto_eq_two_positionals", args: []string{"compact", "threshold", "--auto=85", "1", "2"}, wantStderr: "compact threshold: too many arguments\n", wantExit: 1},
		{name: "compact_threshold_auto_bare_eq_is_positional", args: []string{"compact", "threshold", "--auto=", "1"}, wantStderr: "compact threshold: too many arguments\n", wantExit: 1},
		{name: "compact_threshold_auto_last_missing", args: []string{"compact", "threshold", "--auto", "85", "--auto"}, wantStderr: "compact threshold: --auto requires a value (e.g. --auto 85)\n", wantExit: 1},
		// ---- supervise ----
		{name: "supervise_tail_help_is_unknown_flag", args: []string{"supervise", "tail", "--help"}, wantStderr: "supervise tail: unknown flag \"--help\"\n", wantExit: 1},
		{name: "supervise_status_unknown", args: []string{"supervise", "status", "--bogus"}, wantStderr: "supervise status: unknown flag \"--bogus\"\n", wantExit: 1},
		{name: "supervise_tail_n_missing", args: []string{"supervise", "tail", "--n"}, wantStderr: "supervise tail: --n requires a value\n", wantExit: 1},
		{name: "supervise_tail_n_bad", args: []string{"supervise", "tail", "--n", "0"}, wantStderr: "supervise tail: --n value \"0\" is not a positive integer\n", wantExit: 1},
		{name: "supervise_tail_n_eq_bad", args: []string{"supervise", "tail", "--n=abc"}, wantStderr: "supervise tail: --n value \"abc\" is not a positive integer\n", wantExit: 1},
		{name: "supervise_tail_n_bare_eq", args: []string{"supervise", "tail", "--n="}, wantStderr: "supervise tail: unknown flag \"--n=\"\n", wantExit: 1},
		{name: "supervise_tail_unknown", args: []string{"supervise", "tail", "--bogus"}, wantStderr: "supervise tail: unknown flag \"--bogus\"\n", wantExit: 1},
		{name: "supervise_tail_double_dash", args: []string{"supervise", "tail", "--"}, wantStderr: "supervise tail: unknown flag \"--\"\n", wantExit: 1},
		{name: "supervise_tail_too_many", args: []string{"supervise", "tail", "a", "b"}, wantStderr: "supervise tail: too many positional args\n", wantExit: 1},
		{name: "supervise_ack_note_missing", args: []string{"supervise", "ack", "id1", "--note"}, wantStderr: "supervise ack: --note requires a value\n", wantExit: 1},
		{name: "supervise_ack_unknown", args: []string{"supervise", "ack", "id1", "--bogus"}, wantStderr: "supervise ack: unknown flag \"--bogus\"\n", wantExit: 1},
		{name: "supervise_ack_too_many", args: []string{"supervise", "ack", "id1", "p", "q"}, wantStderr: "supervise ack: too many positional args\n", wantExit: 1},
		{name: "supervise_ack_bare_eq", args: []string{"supervise", "ack", "id1", "--note="}, wantStderr: "supervise ack: unknown flag \"--note=\"\n", wantExit: 1},
		{name: "supervise_ack_all_note_missing", args: []string{"supervise", "ack-all", "--note"}, wantStderr: "supervise ack-all: --note requires a value\n", wantExit: 1},
		{name: "supervise_ack_all_unknown", args: []string{"supervise", "ack-all", "--bogus"}, wantStderr: "supervise ack-all: unknown flag \"--bogus\"\n", wantExit: 1},
		{name: "supervise_ack_all_too_many", args: []string{"supervise", "ack-all", "a", "b"}, wantStderr: "supervise ack-all: too many positional args\n", wantExit: 1},
		// ---- plan ----
		{name: "plan_show_unknown", args: []string{"plan", "show", "--bogus"}, wantStderr: "plan score show: unknown option \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "plan_show_extra", args: []string{"plan", "show", "id", "extra"}, wantStderr: "plan score show: unknown option \"extra\" (try --help)\n", wantExit: 1},
		{name: "plan_history_project_missing", args: []string{"plan", "history", "--project"}, wantStderr: "plan score history: --project requires a value\n", wantExit: 1},
		{name: "plan_history_limit_missing", args: []string{"plan", "history", "--limit"}, wantStderr: "plan score history: --limit requires a value\n", wantExit: 1},
		{name: "plan_history_limit_bad", args: []string{"plan", "history", "--limit", "x"}, wantStderr: "plan score history: --limit \"x\" must be a positive integer\n", wantExit: 1},
		{name: "plan_history_limit_eq_bad", args: []string{"plan", "history", "--limit=0"}, wantStderr: "plan score history: --limit value \"0\" must be a positive integer\n", wantExit: 1},
		{name: "plan_history_limit_bare_eq", args: []string{"plan", "history", "--limit="}, wantStderr: "plan score history: unknown option \"--limit=\" (try --help)\n", wantExit: 1},
		{name: "plan_history_first_bad_wins", args: []string{"plan", "history", "--limit", "x", "--limit=y"}, wantStderr: "plan score history: --limit \"x\" must be a positive integer\n", wantExit: 1},
		{name: "plan_history_first_bad_wins_eq", args: []string{"plan", "history", "--limit=y", "--limit", "x"}, wantStderr: "plan score history: --limit value \"y\" must be a positive integer\n", wantExit: 1},
		{name: "plan_history_unknown", args: []string{"plan", "history", "--bogus"}, wantStderr: "plan score history: unknown option \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "plan_history_double_dash", args: []string{"plan", "history", "--"}, wantStderr: "plan score history: unknown option \"--\" (try --help)\n", wantExit: 1},
		{name: "plan_history_positional", args: []string{"plan", "history", "pos"}, wantStderr: "plan score history: unknown option \"pos\" (try --help)\n", wantExit: 1},
		{name: "plan_override_missing_id", args: []string{"plan", "override"}, wantSubstr: "plan score override: <plan_id> required\n", wantExit: 1},
		{name: "plan_override_reason_missing", args: []string{"plan", "override", "p1", "--reason"}, wantStderr: "plan score override: --reason requires a value\n", wantExit: 1},
		{name: "plan_override_unknown", args: []string{"plan", "override", "p1", "--bogus"}, wantStderr: "plan score override: unknown option \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "plan_override_bare_eq", args: []string{"plan", "override", "p1", "--reason="}, wantStderr: "plan score override: unknown option \"--reason=\" (try --help)\n", wantExit: 1},
		{name: "plan_correlate_project_missing", args: []string{"plan", "correlate", "--project"}, wantStderr: "plan score correlate: --project requires a value\n", wantExit: 1},
		{name: "plan_correlate_since_missing", args: []string{"plan", "correlate", "--since"}, wantStderr: "plan score correlate: --since requires a value\n", wantExit: 1},
		{name: "plan_correlate_minn_missing", args: []string{"plan", "correlate", "--min-n"}, wantStderr: "plan score correlate: --min-n requires a value\n", wantExit: 1},
		{name: "plan_correlate_minn_bad", args: []string{"plan", "correlate", "--min-n", "x"}, wantStderr: "plan score correlate: --min-n \"x\" must be a positive integer\n", wantExit: 1},
		{name: "plan_correlate_minn_eq_bad", args: []string{"plan", "correlate", "--min-n=-1"}, wantStderr: "plan score correlate: --min-n value \"-1\" must be a positive integer\n", wantExit: 1}, // "=" form is now accepted (the old prefix check was dead code)
		{name: "plan_correlate_minn_zero", args: []string{"plan", "correlate", "--min-n", "0"}, wantStderr: "plan score correlate: --min-n \"0\" must be a positive integer\n", wantExit: 1},
		{name: "plan_correlate_minn_last_bad_wins", args: []string{"plan", "correlate", "--min-n", "3", "--min-n", "0"}, wantStderr: "plan score correlate: --min-n \"0\" must be a positive integer\n", wantExit: 1},
		{name: "plan_correlate_project_bare_eq", args: []string{"plan", "correlate", "--project="}, wantStderr: "plan score correlate: unknown option \"--project=\" (try --help)\n", wantExit: 1},
		{name: "plan_correlate_unknown", args: []string{"plan", "correlate", "--bogus"}, wantStderr: "plan score correlate: unknown option \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "plan_correlate_double_dash", args: []string{"plan", "correlate", "--"}, wantStderr: "plan score correlate: unknown option \"--\" (try --help)\n", wantExit: 1},
		// ---- work close ----
		{name: "work_close_planid_missing", args: []string{"work", "close", "--plan-id"}, wantStderr: "work close: --plan-id requires a value\n", wantExit: 1},
		{name: "work_close_project_missing", args: []string{"work", "close", "--project"}, wantStderr: "work close: --project requires a value\n", wantExit: 1},
		{name: "work_close_unknown", args: []string{"work", "close", "--bogus"}, wantStderr: "work close: unknown option \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "work_close_double_dash", args: []string{"work", "close", "--"}, wantStderr: "work close: unknown option \"--\" (try --help)\n", wantExit: 1},
		{name: "work_close_positional", args: []string{"work", "close", "pos"}, wantStderr: "work close: unknown option \"pos\" (try --help)\n", wantExit: 1},
		{name: "work_close_bare_eq", args: []string{"work", "close", "--plan-id="}, wantStderr: "work close: unknown option \"--plan-id=\" (try --help)\n", wantExit: 1},
		// ---- model-routing ----
		{name: "mr_eval_judge_missing", args: []string{"model-routing", "eval", "--judge"}, wantStderr: "model-routing eval: --judge requires a value\n", wantExit: 1},
		{name: "mr_eval_cost_missing", args: []string{"model-routing", "eval", "--max-cost-usd"}, wantStderr: "model-routing eval: --max-cost-usd requires a value\n", wantExit: 1},
		{name: "mr_eval_cost_bad", args: []string{"model-routing", "eval", "--max-cost-usd", "x"}, wantStderr: "model-routing eval: --max-cost-usd \"x\" must be a positive number\n", wantExit: 1},
		{name: "mr_eval_cost_eq_bad", args: []string{"model-routing", "eval", "--max-cost-usd=0"}, wantStderr: "model-routing eval: --max-cost-usd value \"0\" must be a positive number\n", wantExit: 1},
		{name: "mr_eval_cases_missing", args: []string{"model-routing", "eval", "--cases"}, wantStderr: "model-routing eval: --cases requires a value\n", wantExit: 1},
		{name: "mr_eval_project_missing", args: []string{"model-routing", "eval", "--project"}, wantStderr: "model-routing eval: --project requires a value\n", wantExit: 1},
		{name: "mr_eval_unknown", args: []string{"model-routing", "eval", "--bogus"}, wantStderr: "model-routing eval: unknown flag \"--bogus\"\n", wantExit: 1},
		{name: "mr_eval_double_dash", args: []string{"model-routing", "eval", "--"}, wantStderr: "model-routing eval: unknown flag \"--\"\n", wantExit: 1},
		{name: "mr_eval_unexpected", args: []string{"model-routing", "eval", "a", "b"}, wantStderr: "model-routing eval: unexpected argument \"b\"\n", wantExit: 1},
		{name: "mr_eval_bare_eq", args: []string{"model-routing", "eval", "--judge="}, wantStderr: "model-routing eval: unknown flag \"--judge=\"\n", wantExit: 1},
		{name: "mr_list_unexpected", args: []string{"model-routing", "list", "x"}, wantStderr: "model-routing list: unexpected argument \"x\"\n", wantExit: 1},
		{name: "mr_list_flag_unexpected", args: []string{"model-routing", "list", "--bogus"}, wantStderr: "model-routing list: unexpected argument \"--bogus\"\n", wantExit: 1},
		{name: "mr_show_unknown", args: []string{"model-routing", "show", "--bogus"}, wantStderr: "model-routing show: unknown flag \"--bogus\"\n", wantExit: 1},
		{name: "mr_show_unexpected", args: []string{"model-routing", "show", "a", "b"}, wantStderr: "model-routing show: unexpected argument \"b\"\n", wantExit: 1},
		{name: "mr_promote_unknown", args: []string{"model-routing", "promote", "--bogus"}, wantStderr: "model-routing promote: unknown flag \"--bogus\"\n", wantExit: 1},
		{name: "mr_promote_unexpected", args: []string{"model-routing", "promote", "a", "b"}, wantStderr: "model-routing promote: unexpected argument \"b\"\n", wantExit: 1},
		{name: "mr_promote_double_dash", args: []string{"model-routing", "promote", "--"}, wantStderr: "model-routing promote: unknown flag \"--\"\n", wantExit: 1},
		{name: "mr_reject_note_missing", args: []string{"model-routing", "reject", "--note"}, wantStderr: "model-routing reject: --note requires a value\n", wantExit: 1},
		{name: "mr_reject_unknown", args: []string{"model-routing", "reject", "--bogus"}, wantStderr: "model-routing reject: unknown flag \"--bogus\"\n", wantExit: 1},
		{name: "mr_reject_unexpected", args: []string{"model-routing", "reject", "a", "b"}, wantStderr: "model-routing reject: unexpected argument \"b\"\n", wantExit: 1},
		{name: "mr_history_unknown", args: []string{"model-routing", "history", "--bogus"}, wantStderr: "model-routing history: unknown flag \"--bogus\"\n", wantExit: 1},
		{name: "mr_history_unexpected", args: []string{"model-routing", "history", "a", "b"}, wantStderr: "model-routing history: unexpected argument \"b\"\n", wantExit: 1},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			stdout, stderr, code := runGoSplit(t, c.args, env)
			if code != c.wantExit {
				t.Errorf("exit = %d, want %d\nstdout=%q\nstderr=%q", code, c.wantExit, stdout, stderr)
			}
			if c.wantStderr != "" && stderr != c.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr, c.wantStderr)
			}
			if c.wantSubstr != "" && !strings.Contains(stderr, c.wantSubstr) {
				t.Errorf("stderr = %q, want substring %q", stderr, c.wantSubstr)
			}
		})
	}
}

// TestWorkHelpFlagPrintsHelp pins that -h/--help still exits 0 with help on
// stdout for each converted command form.
func TestWorkHelpFlagPrintsHelp(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v", goBin, err)
	}
	env := map[string]string{"HOME": t.TempDir(), "YAKOS_ROOT": "", "YAKOS_LIB": ""}
	for _, args := range [][]string{
		{"archive", "-h"}, {"archive", "p", "t", "--help"},
		{"session", "list", "--help"},
		{"plan", "show", "--help"}, {"plan", "history", "-h"}, {"plan", "override", "p", "-h"}, {"plan", "correlate", "--help"},
		{"work", "close", "--help"},
		{"model-routing", "eval", "-h"}, {"model-routing", "list", "-h"}, {"model-routing", "show", "-h"},
		{"model-routing", "promote", "--help"}, {"model-routing", "reject", "-h"}, {"model-routing", "history", "--help"},
	} {
		stdout, stderr, code := runGoSplit(t, args, env)
		if code != 0 || stdout == "" || stderr != "" {
			t.Errorf("%v: code=%d stdout-empty=%v stderr=%q", args, code, stdout == "", stderr)
		}
	}
}

// TestWorkFlags_EqualsFormNowAccepted pins the deliberate behavior change of
// converting `compact threshold --auto` and `plan score correlate --min-n`:
// the "--name=value" spelling is recognized (both were "positional" /
// "unknown option" before) and `--min-n 0 --help` now prints help.
func TestWorkFlags_EqualsFormNowAccepted(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v (run `make build` first)", goBin, err)
	}
	env := map[string]string{"HOME": t.TempDir(), "YAKOS_ROOT": "", "YAKOS_LIB": ""}

	stdout, stderr, code := runGoSplit(t, []string{"compact", "threshold", "--auto=85"}, env)
	if code != 0 || !strings.Contains(stdout, "auto-compact threshold set to 85%") {
		t.Errorf("--auto=85: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, stderr, code = runGoSplit(t, []string{"compact", "threshold", "--auto", "86", "85"}, env)
	if code != 0 || !strings.Contains(stdout, "auto-compact threshold set to 86%") {
		t.Errorf("--auto 86 85: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	// Valid --min-n reaches the scorer (no log in the temp HOME) instead of "unknown option".
	_, stderr, code = runGoSplit(t, []string{"plan", "correlate", "--min-n=5"}, env)
	if code != 1 || strings.Contains(stderr, "unknown option") || !strings.Contains(stderr, "No records found") {
		t.Errorf("--min-n=5: exit=%d stderr=%q", code, stderr)
	}
	stdout, _, code = runGoSplit(t, []string{"plan", "correlate", "--min-n", "0", "--help"}, env)
	if code != 0 || !strings.Contains(stdout, "Subcommands:") {
		t.Errorf("--min-n 0 --help: exit=%d stdout=%q", code, stdout)
	}
}
