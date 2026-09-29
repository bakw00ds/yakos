package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// runSandboxedGo runs the Go yakos binary with a fresh temp HOME, cwd and
// work dir, and no YAKOS_ROOT/YAKOS_LIB, so flag-error paths can be pinned
// without touching real state.
func runSandboxedGo(t *testing.T, args []string) (stdout, stderr string, exitCode int) {
	t.Helper()
	home := t.TempDir()
	cwd := t.TempDir()
	cmd := exec.Command(resolveGoBinary(), args...) //nolint:gosec
	cmd.Dir = cwd
	var env []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "YAKOS_ROOT="), strings.HasPrefix(kv, "YAKOS_LIB="),
			strings.HasPrefix(kv, "HOME="), strings.HasPrefix(kv, "YAKOS_WORK_DIR="),
			strings.HasPrefix(kv, "YAKOS_ALLOW_ROOT="), strings.HasPrefix(kv, "YAKOS_PROJECT_NAME="):
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "YAKOS_IMPL=go", "HOME="+home, "YAKOS_WORK_DIR="+cwd+"/work")
	cmd.Env = env
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), errOut.String(), code
}

// TestInstallKanban_FlagErrorText_BeforeAndAfterConversion pins the exact
// stderr and exit code of every flag-error path in cmd_install.go and
// cmd_kanban.go. The table was captured against the pre-conversion parsers
// (13e85ef) and must hold byte-for-byte after converting to cliflag.
func TestInstallKanban_FlagErrorText_BeforeAndAfterConversion(t *testing.T) {
	if _, err := os.Stat(resolveGoBinary()); err != nil {
		t.Skipf("Go yakos binary not found: %v (run `make build` first)", err)
	}
	cases := []flagErrorCase{
		// init
		{name: "init_project_missing", args: []string{"init", "n", "--project"}, wantStderr: "init: --project requires a path\n", wantExit: 1},
		{name: "init_template_missing", args: []string{"init", "n", "--template"}, wantStderr: "init: --template requires a kind (base, rails, go, python, node, rust, static-site)\n", wantExit: 1},
		{name: "init_unknown_flag", args: []string{"init", "--bogus"}, wantStderr: "init: unknown flag \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "init_double_dash", args: []string{"init", "--"}, wantStderr: "init: unknown flag \"--\" (try --help)\n", wantExit: 1},
		{name: "init_bare_equals", args: []string{"init", "--project="}, wantStderr: "init: unknown flag \"--project=\" (try --help)\n", wantExit: 1},
		{name: "init_extra_positional", args: []string{"init", "a", "b"}, wantStderr: "init: unexpected positional argument \"b\"\n", wantExit: 1},
		{name: "init_missing_name", args: []string{"init", "--project", "/x"}, wantSubstr: "init: missing <name>\n", wantExit: 1},
		{name: "init_missing_project", args: []string{"init", "n"}, wantSubstr: "init: --project <path> is required\n", wantExit: 1},
		// install
		{name: "install_unknown", args: []string{"install", "--bogus"}, wantStderr: "install: unknown argument \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "install_positional", args: []string{"install", "x"}, wantStderr: "install: unknown argument \"x\" (try --help)\n", wantExit: 1},
		{name: "install_double_dash", args: []string{"install", "--"}, wantStderr: "install: unknown argument \"--\" (try --help)\n", wantExit: 1},
		{name: "install_equals_bool", args: []string{"install", "--force=1"}, wantStderr: "install: unknown argument \"--force=1\" (try --help)\n", wantExit: 1},
		// uninstall
		{name: "uninstall_root_missing", args: []string{"uninstall", "--root"}, wantStderr: "uninstall: --root requires a path argument\n", wantExit: 1},
		{name: "uninstall_unknown", args: []string{"uninstall", "--bogus"}, wantStderr: "uninstall: unknown argument \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "uninstall_bare_equals", args: []string{"uninstall", "--root="}, wantStderr: "uninstall: unknown argument \"--root=\" (try --help)\n", wantExit: 1},
		{name: "uninstall_double_dash", args: []string{"uninstall", "--"}, wantStderr: "uninstall: unknown argument \"--\" (try --help)\n", wantExit: 1},
		{name: "uninstall_positional", args: []string{"uninstall", "x"}, wantStderr: "uninstall: unknown argument \"x\" (try --help)\n", wantExit: 1},
		// update
		{name: "update_unknown", args: []string{"update", "--bogus"}, wantStderr: "update: unknown argument \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "update_double_dash", args: []string{"update", "--"}, wantStderr: "update: unknown argument \"--\" (try --help)\n", wantExit: 1},
		{name: "update_positional", args: []string{"update", "x"}, wantStderr: "update: unknown argument \"x\" (try --help)\n", wantExit: 1},
		// upgrade
		{name: "upgrade_unknown", args: []string{"upgrade", "--bogus"}, wantStderr: "upgrade: unknown argument \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "upgrade_double_dash", args: []string{"upgrade", "--"}, wantStderr: "upgrade: unknown argument \"--\" (try --help)\n", wantExit: 1},
		// quickstart
		{name: "quickstart_runtime_missing", args: []string{"quickstart", "--runtime"}, wantStderr: "quickstart: --runtime requires an id\n", wantExit: 1},
		{name: "quickstart_unknown", args: []string{"quickstart", "--bogus"}, wantStderr: "quickstart: unknown flag \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "quickstart_double_dash", args: []string{"quickstart", "--"}, wantStderr: "quickstart: unknown flag \"--\" (try --help)\n", wantExit: 1},
		{name: "quickstart_bare_equals", args: []string{"quickstart", "--runtime="}, wantStderr: "quickstart: unknown flag \"--runtime=\" (try --help)\n", wantExit: 1},
		{name: "quickstart_positional", args: []string{"quickstart", "x"}, wantStderr: "quickstart: unexpected argument \"x\" (try --help)\n", wantExit: 1},
		// auth
		{name: "auth_unknown", args: []string{"auth", "status", "--bogus"}, wantStderr: "auth status: unknown flag \"--bogus\"\n", wantExit: 1},
		{name: "auth_double_dash", args: []string{"auth", "status", "--"}, wantStderr: "auth status: unknown flag \"--\"\n", wantExit: 1},
		{name: "auth_too_many", args: []string{"auth", "status", "a", "b"}, wantStderr: "auth status: too many positional args\n", wantExit: 1},
		{name: "auth_all_equals", args: []string{"auth", "status", "--all=1"}, wantStderr: "auth status: unknown flag \"--all=1\"\n", wantExit: 1},
		// migrate
		{name: "migrate_unknown", args: []string{"migrate", "--bogus"}, wantStderr: "migrate: unknown flag \"--bogus\" (try --help)\n", wantExit: 1},
		{name: "migrate_double_dash", args: []string{"migrate", "--"}, wantStderr: "migrate: unknown flag \"--\" (try --help)\n", wantExit: 1},
		{name: "migrate_too_many", args: []string{"migrate", "a", "b", "c"}, wantStderr: "migrate: too many positional args (try --help)\n", wantExit: 1},
		// kanban add
		{name: "kanban_add_category_missing", args: []string{"kanban", "add", "--category"}, wantStderr: "kanban add: --category needs a value\n", wantExit: 1},
		{name: "kanban_add_notes_missing", args: []string{"kanban", "add", "t", "--notes"}, wantStderr: "kanban add: --notes needs a value\n", wantExit: 1},
		{name: "kanban_add_unknown", args: []string{"kanban", "add", "--bogus"}, wantStderr: "kanban add: unknown option \"--bogus\"\n", wantExit: 1},
		{name: "kanban_add_bare_equals", args: []string{"kanban", "add", "--category="}, wantStderr: "kanban add: unknown option \"--category=\"\n", wantExit: 1},
		{name: "kanban_add_extra", args: []string{"kanban", "add", "a", "b"}, wantStderr: "kanban add: unexpected argument \"b\"\n", wantExit: 1},
		{name: "kanban_add_double_dash_is_title_then_extra", args: []string{"kanban", "add", "--", "x"}, wantStderr: "kanban add: unexpected argument \"x\"\n", wantExit: 1},
		{name: "kanban_add_title_then_double_dash", args: []string{"kanban", "add", "t", "--"}, wantStderr: "kanban add: unexpected argument \"--\"\n", wantExit: 1},
		{name: "kanban_add_no_title", args: []string{"kanban", "add"}, wantStderr: "kanban add: title required: yakos kanban add \"<title>\"\n", wantExit: 1},
		{name: "kanban_add_double_dash_only_ok", args: []string{"kanban", "add", "--"}, wantSubstr: "— -- (category: other)", wantExit: 0},
		{name: "kanban_add_equals_forms_ok", args: []string{"kanban", "add", "--category=c", "--notes=n", "T"}, wantSubstr: "— T (category: c)", wantExit: 0},
		{name: "kanban_add_interleaved_ok", args: []string{"kanban", "add", "T", "--category", "c"}, wantSubstr: "— T (category: c)", wantExit: 0},
		// kanban serve
		{name: "kanban_serve_port_missing", args: []string{"kanban", "serve", "--port"}, wantStderr: "kanban serve: --port needs a value\n", wantExit: 1},
		{name: "kanban_serve_host_missing", args: []string{"kanban", "serve", "--host"}, wantStderr: "kanban serve: --host needs a value\n", wantExit: 1},
		{name: "kanban_serve_port_nan", args: []string{"kanban", "serve", "--port", "abc"}, wantStderr: "kanban serve: --port must be a number\n", wantExit: 1},
		{name: "kanban_serve_port_nan_equals", args: []string{"kanban", "serve", "--port=abc"}, wantStderr: "kanban serve: --port must be a number\n", wantExit: 1},
		{name: "kanban_serve_first_bad_port_wins", args: []string{"kanban", "serve", "--port", "x", "--port", "5"}, wantStderr: "kanban serve: --port must be a number\n", wantExit: 1},
		{name: "kanban_serve_unknown", args: []string{"kanban", "serve", "--bogus"}, wantStderr: "kanban serve: unknown option \"--bogus\"\n", wantExit: 1},
		{name: "kanban_serve_positional", args: []string{"kanban", "serve", "x"}, wantStderr: "kanban serve: unknown option \"x\"\n", wantExit: 1},
		{name: "kanban_serve_double_dash", args: []string{"kanban", "serve", "--"}, wantStderr: "kanban serve: unknown option \"--\"\n", wantExit: 1},
		{name: "kanban_serve_bare_equals", args: []string{"kanban", "serve", "--host="}, wantStderr: "kanban serve: unknown option \"--host=\"\n", wantExit: 1},
		{name: "kanban_serve_warn_then_unknown", args: []string{"kanban", "serve", "--host", "0.0.0.0", "--bogus"},
			wantStderr: "kanban serve: binding 0.0.0.0 — the web UI is UNAUTHENTICATED and can mutate the board\nkanban serve: unknown option \"--bogus\"\n", wantExit: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runSandboxedGo(t, tc.args)
			if code != tc.wantExit {
				t.Errorf("exit = %d, want %d (stderr %q)", code, tc.wantExit, stderr)
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
