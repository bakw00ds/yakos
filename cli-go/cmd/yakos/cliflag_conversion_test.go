package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// runGoSplit runs the Go-native yakos binary with args, returning stdout and
// stderr separately (exact bytes, not merged) plus the exit code. Used by
// the byte-exact error-text tables below: cliflag.Set.Parse's missing-value
// text and the surrounding hand-written "unknown flag"/"unexpected
// argument" messages must not change one byte when cmd_diag.go and
// cmd_integration.go convert to cliflag (s6-structural-plan-2026-09-23.md
// §3.2 B2 behavior-neutrality requirement).
func runGoSplit(t *testing.T, args []string, extraEnv map[string]string) (stdout, stderr string, exitCode int) {
	t.Helper()
	goBin := resolveGoBinary()
	cmd := exec.Command(goBin, args...) //nolint:gosec
	cmd.Env = append(os.Environ(), "YAKOS_IMPL=go")
	for k, v := range extraEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	code := 0
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return out.String(), errOut.String(), code
}

// flagErrorCase is one (args -> exact stderr line + exit code) expectation.
type flagErrorCase struct {
	name       string
	args       []string
	env        map[string]string
	wantStderr string // exact full stderr, or "" to skip the exact check
	wantSubstr string // used instead of wantStderr when only a substring is stable
	wantExit   int
}

// TestDiagIntegration_FlagErrorText_BeforeAndAfterConversion pins the exact
// error text cmd_diag.go's and cmd_integration.go's hand-rolled parsers
// produce today, captured before converting those five/four functions to
// cliflag.Set (runValidate, runCost, runStatus, runDoctor, runRefresh;
// runHooksInstall, runHooksLint, runWorkflowRun, runWorkflowResume). Every
// case here must still pass, byte-for-byte, after the conversion commits —
// that is what "behavior-neutral" means for this PR.
func TestDiagIntegration_FlagErrorText_BeforeAndAfterConversion(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v (run `make build` first)", goBin, err)
	}

	repoRoot := repoRootForParity(t)

	cases := []flagErrorCase{
		// ---- validate ------------------------------------------------
		{
			name:       "validate_unknown_flag",
			args:       []string{"validate", "--bogus"},
			wantStderr: "validate: unknown flag \"--bogus\"\n",
			wantExit:   1,
		},
		// ---- cost ------------------------------------------------
		{
			name:       "cost_since_missing_value",
			args:       []string{"cost", "--since"},
			wantStderr: "cost: --since requires a date\n",
			wantExit:   1,
		},
		{
			name:       "cost_by_missing_value",
			args:       []string{"cost", "--by"},
			wantStderr: "cost: --by requires an axis\n",
			wantExit:   1,
		},
		{
			name:       "cost_unknown_flag",
			args:       []string{"cost", "--bogus"},
			wantStderr: "cost: unknown flag \"--bogus\"\n",
			wantExit:   1,
		},
		{
			name:       "cost_unexpected_positional",
			args:       []string{"cost", "bogus"},
			wantStderr: "cost: unexpected argument \"bogus\"\n",
			wantExit:   1,
		},
		// ---- status ------------------------------------------------
		{
			name:       "status_unknown_flag",
			args:       []string{"status", "--bogus"},
			wantStderr: "status: unknown flag \"--bogus\"\n",
			wantExit:   1,
		},
		{
			name:       "status_too_many_positional",
			args:       []string{"status", "a", "b"},
			wantStderr: "status: too many positional args\n",
			wantExit:   1,
		},
		// ---- doctor ------------------------------------------------
		{
			name:       "doctor_unknown_flag",
			args:       []string{"doctor", "--bogus"},
			wantStderr: "doctor: unknown flag \"--bogus\"\n",
			wantExit:   1,
		},
		{
			name:       "doctor_too_many_positional",
			args:       []string{"doctor", "a", "b"},
			wantStderr: "doctor: too many positional args\n",
			wantExit:   1,
		},
		{
			name: "doctor_fix_rejected",
			args: []string{"doctor", "--fix"},
			wantStderr: "doctor: --fix is not yet implemented in the Go port (see ideas wishlist rank 5)\n" +
				"  Use 'YAKOS_IMPL=bash yakos doctor --fix' to reach the bash implementation.\n",
			wantExit: 1,
		},
		// ---- refresh ------------------------------------------------
		{
			name:       "refresh_unknown_flag",
			args:       []string{"refresh", "--bogus"},
			env:        map[string]string{"YAKOS_ROOT": repoRoot},
			wantStderr: "refresh: unknown argument \"--bogus\" (try --help)\n",
			wantExit:   1,
		},
		{
			name:       "refresh_unexpected_positional",
			args:       []string{"refresh", "bogus"},
			env:        map[string]string{"YAKOS_ROOT": repoRoot},
			wantStderr: "refresh: unknown argument \"bogus\" (try --help)\n",
			wantExit:   1,
		},
		{
			name:       "refresh_project_missing_value",
			args:       []string{"refresh", "--project"},
			env:        map[string]string{"YAKOS_ROOT": repoRoot},
			wantStderr: "refresh: --project requires a path\n",
			wantExit:   1,
		},
		// ---- hooks install / lint ------------------------------------
		{
			name:       "hooks_install_project_missing_value",
			args:       []string{"hooks", "install", "codex", "--project"},
			wantStderr: "hooks install: --project requires a path\n",
			wantExit:   1,
		},
		{
			name:       "hooks_install_missing_runtime",
			args:       []string{"hooks", "install"},
			wantSubstr: "hooks install: <runtime> required\n",
			wantExit:   1,
		},
		{
			name:       "hooks_lint_hooks_dir_missing_value",
			args:       []string{"hooks", "lint", "--hooks-dir"},
			wantStderr: "hooks lint: --hooks-dir requires a path\n",
			wantExit:   1,
		},
		{
			name:       "hooks_lint_unknown_arg",
			args:       []string{"hooks", "lint", "--bogus"},
			wantStderr: "hooks lint: unknown arg \"--bogus\"\n",
			wantExit:   1,
		},
		// ---- workflow run / resume ------------------------------------
		{
			name:       "workflow_run_run_id_missing_value",
			args:       []string{"workflow", "run", "myflow", "--run-id"},
			wantStderr: "workflow run: --run-id requires a value\n",
			wantExit:   1,
		},
		{
			name:       "workflow_run_operator_missing_value",
			args:       []string{"workflow", "run", "myflow", "--operator"},
			wantStderr: "workflow run: --operator requires a value\n",
			wantExit:   1,
		},
		{
			name:       "workflow_run_missing_name",
			args:       []string{"workflow", "run"},
			wantStderr: "workflow run: workflow name is required\n",
			wantExit:   1,
		},
		{
			name:       "workflow_resume_prior_run_id_missing_value",
			args:       []string{"workflow", "resume", "myflow", "--prior-run-id"},
			wantStderr: "workflow resume: --prior-run-id requires a value\n",
			wantExit:   1,
		},
		{
			name:       "workflow_resume_new_run_id_missing_value",
			args:       []string{"workflow", "resume", "myflow", "--prior-run-id", "x", "--new-run-id"},
			wantStderr: "workflow resume: --new-run-id requires a value\n",
			wantExit:   1,
		},

		// ---- "--" terminator: NOT special for any of the nine ---------
		//
		// s6-b2-review-2026-09-23.md finding 1: none of these nine
		// functions' pre-conversion loops ever treated a bare "--" as a
		// terminator -- every one fell into its generic unrecognized-token
		// branch and reported it like any other unmatched "-..." token.
		// cliflag.Set.AllowTerminator defaults to false and none of these
		// nine Sets set it, so this must stay true after conversion. One
		// case per converted command.
		{
			name:       "validate_double_dash_not_a_terminator",
			args:       []string{"validate", "--"},
			wantStderr: "validate: unknown flag \"--\"\n",
			wantExit:   1,
		},
		{
			name:       "cost_double_dash_not_a_terminator",
			args:       []string{"cost", "--"},
			wantStderr: "cost: unknown flag \"--\"\n",
			wantExit:   1,
		},
		{
			name:       "status_double_dash_not_a_terminator",
			args:       []string{"status", "--"},
			wantStderr: "status: unknown flag \"--\"\n",
			wantExit:   1,
		},
		{
			name:       "doctor_double_dash_not_a_terminator",
			args:       []string{"doctor", "--"},
			wantStderr: "doctor: unknown flag \"--\"\n",
			wantExit:   1,
		},
		{
			name:       "refresh_double_dash_not_a_terminator",
			args:       []string{"refresh", "--"},
			env:        map[string]string{"YAKOS_ROOT": repoRoot},
			wantStderr: "refresh: unknown argument \"--\" (try --help)\n",
			wantExit:   1,
		},
		{
			name:       "hooks_install_double_dash_not_a_terminator",
			args:       []string{"hooks", "install", "--"},
			wantStderr: "hooks install: unknown flag \"--\"\n",
			wantExit:   1,
		},
		{
			name:       "hooks_lint_double_dash_not_a_terminator",
			args:       []string{"hooks", "lint", "--"},
			wantStderr: "hooks lint: unknown arg \"--\"\n",
			wantExit:   1,
		},
		{
			name:       "workflow_run_double_dash_not_a_terminator",
			args:       []string{"workflow", "run", "--"},
			wantStderr: "workflow run: unknown argument \"--\"\n",
			wantExit:   1,
		},
		{
			name:       "workflow_resume_double_dash_not_a_terminator",
			args:       []string{"workflow", "resume", "--"},
			wantStderr: "workflow resume: unknown argument \"--\"\n",
			wantExit:   1,
		},

		// ---- disclosed incidental fix (finding 4) ----------------------
		{
			name:       "hooks_install_empty_positional_does_not_panic",
			args:       []string{"hooks", "install", ""},
			wantSubstr: "hooks install: <runtime> required\n",
			wantExit:   1,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			_, stderr, exit := runGoSplit(t, c.args, c.env)
			if exit != c.wantExit {
				t.Errorf("%v: exit = %d, want %d\nstderr:\n%s", c.args, exit, c.wantExit, stderr)
			}
			if c.wantStderr != "" && stderr != c.wantStderr {
				t.Errorf("%v: stderr =\n%q\nwant:\n%q", c.args, stderr, c.wantStderr)
			}
			if c.wantSubstr != "" && !strings.Contains(stderr, c.wantSubstr) {
				t.Errorf("%v: stderr =\n%s\nwant substring:\n%s", c.args, stderr, c.wantSubstr)
			}
		})
	}
}

// TestHooksLintBareEqualsHooksDirMatchesDefault pins
// s6-b2-review-2026-09-23.md finding 2: `hooks lint --hooks-dir=` (a bare
// equals, empty value) must behave exactly like `hooks lint` with no
// --hooks-dir at all, reproducing the pre-conversion strings.HasPrefix
// parser's quirk (it accepted the empty value and fell back to the default
// dir below) rather than the magic-length-check quirk the other eight
// converted flags reproduce. cliflag.Spec.AllowEmpty on runHooksLint's
// --hooks-dir Spec is what makes this an explicit, tested decision instead
// of a silent gap — see cmd_integration.go's runHooksLint for the Spec.
//
// Compares full stdout+stderr+exit code between the bare-equals form and no
// flag at all (rather than pinning literal lint output, which depends on
// lib/hooks/ contents) so this stays correct as that directory's contents
// change.
func TestHooksLintBareEqualsHooksDirMatchesDefault(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v (run `make build` first)", goBin, err)
	}
	repoRoot := repoRootForParity(t)
	env := map[string]string{"YAKOS_ROOT": repoRoot}

	wantOut, wantErr, wantExit := runGoSplit(t, []string{"hooks", "lint"}, env)
	gotOut, gotErr, gotExit := runGoSplit(t, []string{"hooks", "lint", "--hooks-dir="}, env)

	if strings.Contains(gotErr, "unknown arg") {
		t.Fatalf("`hooks lint --hooks-dir=` was rejected as unrecognized: stderr=%q", gotErr)
	}
	if gotExit != wantExit {
		t.Errorf("exit = %d, want %d (same as `hooks lint` with no flag)", gotExit, wantExit)
	}
	if gotOut != wantOut {
		t.Errorf("stdout =\n%q\nwant (same as `hooks lint` with no flag):\n%q", gotOut, wantOut)
	}
	if gotErr != wantErr {
		t.Errorf("stderr =\n%q\nwant (same as `hooks lint` with no flag):\n%q", gotErr, wantErr)
	}
}
