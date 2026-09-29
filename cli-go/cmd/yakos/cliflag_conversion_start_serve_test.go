package main

import (
	"os"
	"strings"
	"testing"
)

// TestStartServeDispatchFlagErrorText_BeforeAndAfterConversion pins the exact
// stderr text and exit code the hand-rolled parsers in cmd_start.go,
// cmd_serve.go (runServe, runEvents) and cmd_dispatch.go (runDispatch,
// runTeamRestart) produced before they were converted to internal/cliflag
// (K-87 B2). Every expectation below was captured from the pre-conversion
// binary (main@13e85ef) first; the conversion must leave them byte-identical.
//
// Only single-fault argv shapes are pinned. Multi-fault shapes (for example
// an unknown flag AND a later --help) are the documented cliflag ordering
// caveat and are deliberately not pinned. See runValidate in cmd_diag.go.
func TestStartServeDispatchFlagErrorText_BeforeAndAfterConversion(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q: %v (run `make build` first)", goBin, err)
	}

	type tc struct {
		name             string
		args             []string
		wantStderr       string
		wantSubstr       string
		wantNotSubstr    string
		wantStdoutSubstr string
		wantExit         int
	}
	cases := []tc{
		{name: "start_runtime_missing_value", args: []string{"start", "--runtime"}, wantStderr: "start: --runtime requires an id\n", wantExit: 1},
		{name: "start_console_addr_missing_value", args: []string{"start", "--console-addr"}, wantStderr: "start: --console-addr requires an address\n", wantExit: 1},
		{name: "start_ws_addr_missing_value", args: []string{"start", "--ws-addr"}, wantStderr: "start: --ws-addr requires an address\n", wantExit: 1},
		{name: "start_perf_addr_missing_value", args: []string{"start", "--perf-addr"}, wantStderr: "start: --perf-addr requires an address\n", wantExit: 1},
		{name: "start_console_bind_missing_value", args: []string{"start", "--console-bind"}, wantStderr: "start: --console-bind requires an address\n", wantExit: 1},
		{name: "start_console_external_host_missing_value", args: []string{"start", "--console-external-host"}, wantStderr: "start: --console-external-host requires a host[:port] value\n", wantExit: 1},
		{name: "start_ide_root_missing_value", args: []string{"start", "--ide-root"}, wantStderr: "start: --ide-root requires a path\n", wantExit: 1},
		{name: "start_resume_missing_value", args: []string{"start", "--resume"}, wantStderr: "start: --resume requires a session id\n", wantExit: 1},
		{name: "start_model_missing_value", args: []string{"start", "--model"}, wantStderr: "start: --model requires an alias\n", wantExit: 1},
		{name: "start_unknown_flag", args: []string{"start", "--bogus"}, wantStderr: "start: unknown flag \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "start_unknown_short_flag", args: []string{"start", "-z"}, wantStderr: "start: unknown flag \"-z\" (try --help)\n", wantExit: 1},
		{name: "start_extra_positional", args: []string{"start", "aa", "bb"}, wantStderr: "start: unexpected positional argument \"bb\"\n", wantExit: 1},
		{name: "start_bare_equals_unrecognized", args: []string{"start", "--runtime="}, wantStderr: "start: unknown flag \"--runtime=\" (try --help)\n", wantExit: 1},
		{name: "start_bare_equals_console_addr_unrecognized", args: []string{"start", "--console-addr="}, wantStderr: "start: unknown flag \"--console-addr=\" (try --help)\n", wantExit: 1},
		{name: "start_flag_after_positional_then_terminator", args: []string{"start", "zz-nonexistent-proj", "--", "--xx", "yy"}, wantSubstr: "not bootstrapped", wantNotSubstr: "unknown flag", wantExit: 1},
		{name: "start_terminator_swallows_flag_like", args: []string{"start", "--", "--bogus"}, wantNotSubstr: "unknown flag", wantExit: 1},
		{name: "start_terminator_after_flag_value_is_value", args: []string{"start", "--runtime", "--", "--foo"}, wantStderr: "start: unknown flag \"--foo\" (try --help)\n", wantExit: 1},
		{name: "start_unknown_flag_before_terminator", args: []string{"start", "--bogus", "--", "a"}, wantStderr: "start: unknown flag \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "serve_socket_missing_value", args: []string{"serve", "--socket"}, wantStderr: "serve: --socket requires a path\n", wantExit: 1},
		{name: "serve_pidfile_missing_value", args: []string{"serve", "--pidfile"}, wantStderr: "serve: --pidfile requires a path\n", wantExit: 1},
		{name: "serve_ws_addr_missing_value", args: []string{"serve", "--ws-addr"}, wantStderr: "serve: --ws-addr requires an address\n", wantExit: 1},
		{name: "serve_perf_addr_missing_value", args: []string{"serve", "--perf-addr"}, wantStderr: "serve: --perf-addr requires an address\n", wantExit: 1},
		{name: "serve_console_addr_missing_value", args: []string{"serve", "--console-addr"}, wantStderr: "serve: --console-addr requires an address\n", wantExit: 1},
		{name: "serve_console_bind_missing_value", args: []string{"serve", "--console-bind"}, wantStderr: "serve: --console-bind requires an address\n", wantExit: 1},
		{name: "serve_console_external_host_missing_value", args: []string{"serve", "--console-external-host"}, wantStderr: "serve: --console-external-host requires a host[:port] value\n", wantExit: 1},
		{name: "serve_console_bootstrap_cert_missing_value", args: []string{"serve", "--console-bootstrap-cert"}, wantStderr: "serve: --console-bootstrap-cert requires a name\n", wantExit: 1},
		{name: "serve_ide_root_missing_value", args: []string{"serve", "--ide-root"}, wantStderr: "serve: --ide-root requires a path\n", wantExit: 1},
		{name: "serve_unknown_flag", args: []string{"serve", "--bogus"}, wantStderr: "serve: unknown flag \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "serve_positional_reported_as_unknown_flag", args: []string{"serve", "pos"}, wantStderr: "serve: unknown flag \"pos\" (try --help)\n", wantExit: 1},
		{name: "serve_bare_equals_unrecognized", args: []string{"serve", "--socket="}, wantStderr: "serve: unknown flag \"--socket=\" (try --help)\n", wantExit: 1},
		{name: "serve_double_dash_unknown", args: []string{"serve", "--"}, wantStderr: "serve: unknown flag \"--\" (try --help)\n", wantExit: 1},
		{name: "serve_bool_equals_unrecognized", args: []string{"serve", "--detach=1"}, wantStderr: "serve: unknown flag \"--detach=1\" (try --help)\n", wantExit: 1},
		{name: "events_ws_addr_missing_value", args: []string{"events", "--ws-addr"}, wantStderr: "events: --ws-addr requires an address\n", wantExit: 1},
		{name: "events_topic_missing_value", args: []string{"events", "--topic"}, wantStderr: "events: --topic requires a topic string\n", wantExit: 1},
		{name: "events_since_rejected", args: []string{"events", "--since"}, wantStderr: "events: --since is not supported in Phase 2 (event replay deferred to Phase 3)\nevents: run without --since to receive live events from this moment forward\n", wantExit: 1},
		{name: "events_since_equals_unknown", args: []string{"events", "--since=5m"}, wantStderr: "events: unknown flag \"--since=5m\" (try --help)\n", wantExit: 1},
		{name: "events_unknown_flag", args: []string{"events", "--bogus"}, wantStderr: "events: unknown flag \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "events_positional_unknown_flag", args: []string{"events", "pos"}, wantStderr: "events: unknown flag \"pos\" (try --help)\n", wantExit: 1},
		{name: "events_bare_equals_unrecognized", args: []string{"events", "--topic="}, wantStderr: "events: unknown flag \"--topic=\" (try --help)\n", wantExit: 1},
		{name: "dispatch_runtime_missing_value", args: []string{"dispatch", "--runtime"}, wantStderr: "dispatch: --runtime requires an id\n", wantExit: 1},
		{name: "dispatch_model_missing_value", args: []string{"dispatch", "--model"}, wantStderr: "dispatch: --model requires a tier (haiku|sonnet|opus|fable)\n", wantExit: 1},
		{name: "dispatch_eval_run_id_missing_value", args: []string{"dispatch", "--eval-run-id"}, wantStderr: "dispatch: --eval-run-id requires an id string\n", wantExit: 1},
		{name: "dispatch_project_missing_value", args: []string{"dispatch", "--project"}, wantStderr: "dispatch: --project requires a path\n", wantExit: 1},
		{name: "dispatch_timeout_missing_value", args: []string{"dispatch", "--timeout"}, wantStderr: "dispatch: --timeout requires a number\n", wantExit: 1},
		{name: "dispatch_timeout_not_number", args: []string{"dispatch", "--timeout", "abc"}, wantStderr: "dispatch: --timeout value \"abc\" is not a number\n", wantExit: 1},
		{name: "dispatch_timeout_equals_not_number", args: []string{"dispatch", "--timeout=abc"}, wantStderr: "dispatch: --timeout value \"abc\" is not a number\n", wantExit: 1},
		{name: "dispatch_timeout_bad_then_good", args: []string{"dispatch", "--timeout", "abc", "--timeout", "5"}, wantStderr: "dispatch: --timeout value \"abc\" is not a number\n", wantExit: 1},
		{name: "dispatch_unknown_flag", args: []string{"dispatch", "--bogus"}, wantStderr: "dispatch: unknown flag \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "dispatch_too_many_positional", args: []string{"dispatch", "a", "b", "c"}, wantStderr: "dispatch: too many positional args (use --help)\n", wantExit: 1},
		{name: "dispatch_missing_agent", args: []string{"dispatch"}, wantSubstr: "dispatch: missing <agent-name>\n", wantExit: 1},
		{name: "dispatch_missing_task", args: []string{"dispatch", "some-agent"}, wantSubstr: "dispatch: missing <task-prompt>\n", wantExit: 1},
		{name: "dispatch_double_dash_not_a_terminator", args: []string{"dispatch", "--"}, wantStderr: "dispatch: unknown flag \"--\" (try --help)\n", wantExit: 1},
		{name: "dispatch_bare_equals_unrecognized", args: []string{"dispatch", "--model="}, wantStderr: "dispatch: unknown flag \"--model=\" (try --help)\n", wantExit: 1},
		{name: "team_restart_tag_missing_value", args: []string{"team", "restart", "--tag"}, wantStderr: "team restart: --tag requires a value\n", wantExit: 1},
		{name: "team_restart_unknown_flag", args: []string{"team", "restart", "--bogus"}, wantStderr: "team restart: unknown flag \"--bogus\"\n", wantExit: 1},
		{name: "team_restart_too_many_positional", args: []string{"team", "restart", "a", "b"}, wantStderr: "team restart: too many positional args\n", wantExit: 1},
		{name: "team_restart_missing_project", args: []string{"team", "restart"}, wantStderr: "team restart: missing <project> (try --help)\n", wantExit: 1},
		{name: "team_restart_double_dash_not_a_terminator", args: []string{"team", "restart", "--"}, wantStderr: "team restart: unknown flag \"--\"\n", wantExit: 1},
		{name: "team_restart_bare_equals_unrecognized", args: []string{"team", "restart", "--tag="}, wantStderr: "team restart: unknown flag \"--tag=\"\n", wantExit: 1},
		{name: "team_restart_help", args: []string{"team", "restart", "--help"}, wantStdoutSubstr: "yakos team restart <project>", wantExit: 0},
		{name: "team_restart_yes_help", args: []string{"team", "restart", "-y", "-h"}, wantStdoutSubstr: "yakos team restart <project>", wantExit: 0},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			stdout, stderr, exit := runGoSplit(t, c.args, map[string]string{"HOME": t.TempDir()})
			if exit != c.wantExit {
				t.Errorf("%v: exit = %d, want %d\nstderr:\n%s", c.args, exit, c.wantExit, stderr)
			}
			if c.wantStderr != "" && stderr != c.wantStderr {
				t.Errorf("%v: stderr =\n%q\nwant:\n%q", c.args, stderr, c.wantStderr)
			}
			if c.wantSubstr != "" && !strings.Contains(stderr, c.wantSubstr) {
				t.Errorf("%v: stderr =\n%s\nwant substring:\n%s", c.args, stderr, c.wantSubstr)
			}
			if c.wantNotSubstr != "" && strings.Contains(stderr, c.wantNotSubstr) {
				t.Errorf("%v: stderr unexpectedly contains %q:\n%s", c.args, c.wantNotSubstr, stderr)
			}
			if c.wantStdoutSubstr != "" && !strings.Contains(stdout, c.wantStdoutSubstr) {
				t.Errorf("%v: stdout =\n%s\nwant substring:\n%s", c.args, stdout, c.wantStdoutSubstr)
			}
		})
	}
}
