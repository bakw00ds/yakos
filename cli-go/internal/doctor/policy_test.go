package doctor

// policy_test.go — K-137: `yakos doctor --policy`, a report of risky
// configurations, one line each with a severity and a fix hint. It is a report,
// never a gate (the exit status stays 0), and it names environment variables and
// booleans only: no value, no token material.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

const (
	policyOAuthSecret = "POLICYSECRET0123456789"
	policyOAuthToken  = "sk-ant-oat01-" + policyOAuthSecret
	policyAPIKey      = "sk-ant-api03-policy-test-key"
)

// policyFixture is one fake machine for CheckPolicy.
type policyFixture struct {
	t     *testing.T
	home  string
	env   map[string]string
	found map[string]string // command -> path on PATH
	bash  bool              // the bash CLI tree is installed
	sdk   bool              // node and the sidecar bundle are installed
	// sdkEnabled: the console was started with --console-structured-questions. Only the
	// daemon knows; `yakos doctor` cannot, so it never sets it.
	sdkEnabled bool
}

func newPolicyFixture(t *testing.T) *policyFixture {
	t.Helper()
	return &policyFixture{t: t, home: t.TempDir(), env: map[string]string{}, found: map[string]string{}}
}

func (f *policyFixture) policyEnv() PolicyEnv {
	return PolicyEnv{
		Home:                 f.home,
		Getenv:               func(k string) string { return f.env[k] },
		LookPath:             singleLookPath(f.found),
		BashTreePresent:      f.bash,
		SDKSidecarSelectable: f.sdk,
		SDKSidecarEnabled:    f.sdkEnabled,
	}
}

func (f *policyFixture) check() []PolicyFinding { return CheckPolicy(f.policyEnv()) }

func byID(fs []PolicyFinding) map[string]PolicyFinding {
	m := map[string]PolicyFinding{}
	for _, f := range fs {
		m[f.ID] = f
	}
	return m
}

func ids(fs []PolicyFinding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.ID)
	}
	return out
}

func skipWithoutPosixModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits and symlinks are not meaningful here")
	}
}

func requireOneLine(t *testing.T, f PolicyFinding) {
	t.Helper()
	if f.Message == "" || f.Fix == "" {
		t.Errorf("%s: a finding carries a message and a fix hint, got %+v", f.ID, f)
	}
	if strings.ContainsAny(f.Message+f.Fix, "\r\n") {
		t.Errorf("%s: message and fix must each be one line: %q / %q", f.ID, f.Message, f.Fix)
	}
}

func TestCheckPolicy_NothingToReportOnACleanMachine(t *testing.T) {
	f := newPolicyFixture(t)
	if got := f.check(); len(got) != 0 {
		t.Fatalf("a machine with nothing risky must report nothing, got %v", ids(got))
	}
}

// ---- SDK sidecar selectable without ANTHROPIC_API_KEY -------------------------

func TestCheckPolicy_SDKSidecarInstalledButNotEnabledIsOnlyALowHeadsUp(t *testing.T) {
	// node and the bundle exist on every developer machine, so being installed is not a
	// risk by itself: the engine runs only when the console is started with
	// --console-structured-questions, and without a key it refuses rather than runs.
	f := newPolicyFixture(t)
	f.sdk = true
	got, ok := byID(f.check())["sdk-sidecar-no-api-key"]
	if !ok {
		t.Fatal("the SDK sidecar is installed and no ANTHROPIC_API_KEY is set: want sdk-sidecar-no-api-key")
	}
	requireOneLine(t, got)
	if got.Severity != PolicyLow {
		t.Errorf("severity = %s, want low while nothing says the engine is enabled", got.Severity)
	}
	for _, want := range []string{"ANTHROPIC_API_KEY", "CLI engine", "--console-structured-questions"} {
		if !strings.Contains(got.Message+" "+got.Fix, want) {
			t.Errorf("the finding must mention %q: %+v", want, got)
		}
	}
}

func TestCheckPolicy_SDKSidecarEnabledWithoutAPIKeyIsMedium(t *testing.T) {
	f := newPolicyFixture(t)
	f.sdk, f.sdkEnabled = true, true
	got, ok := byID(f.check())["sdk-sidecar-no-api-key"]
	if !ok {
		t.Fatal("the console enabled the SDK sidecar and no ANTHROPIC_API_KEY is set: want sdk-sidecar-no-api-key")
	}
	requireOneLine(t, got)
	if got.Severity != PolicyMedium {
		t.Errorf("severity = %s, want medium: structured questions will fail", got.Severity)
	}
	if !strings.Contains(got.Message, "enabled") {
		t.Errorf("the finding must say the engine is enabled: %+v", got)
	}
	for _, want := range []string{"ANTHROPIC_API_KEY", "CLI engine"} {
		if !strings.Contains(got.Message+" "+got.Fix, want) {
			t.Errorf("the finding must mention %q: %+v", want, got)
		}
	}
}

func TestCheckPolicy_SDKSidecarEnabledImpliesInstalled(t *testing.T) {
	// A console that enabled the engine has a working factory, so it is installed even if
	// the caller forgot to say so.
	f := newPolicyFixture(t)
	f.sdkEnabled = true
	if _, ok := byID(f.check())["sdk-sidecar-no-api-key"]; !ok {
		t.Fatal("an enabled SDK sidecar with no key must be reported whether or not the caller also set Selectable")
	}
}

func TestCheckPolicy_SDKSidecarQuietWhenNotSelectableOrKeyed(t *testing.T) {
	f := newPolicyFixture(t)
	f.sdk = false
	if got := byID(f.check())["sdk-sidecar-no-api-key"]; got.ID != "" {
		t.Errorf("no node or bundle: the sidecar cannot be selected, nothing to report: %+v", got)
	}
	f.sdk = true
	f.env["ANTHROPIC_API_KEY"] = policyAPIKey
	if fs := f.check(); len(fs) != 0 {
		t.Errorf("a key is set: nothing to report, got %v", ids(fs))
	}
}

func TestCheckPolicy_SDKSidecarWithAnOAuthTokenAsTheKey(t *testing.T) {
	f := newPolicyFixture(t)
	f.sdk = true
	f.env["ANTHROPIC_API_KEY"] = policyOAuthToken
	got, ok := byID(f.check())["sdk-sidecar-oauth-api-key"]
	if !ok {
		t.Fatal("an OAuth token in ANTHROPIC_API_KEY must be reported")
	}
	requireOneLine(t, got)
	if strings.Contains(got.Message+got.Fix, policyOAuthSecret) {
		t.Errorf("the finding echoed token material: %+v", got)
	}
	if _, also := byID(f.check())["sdk-sidecar-no-api-key"]; also {
		t.Error("one cause, one finding")
	}
}

// ---- router policy: allow_unsandboxed_runtimes -------------------------------

func TestCheckPolicy_RouterPolicyUnsandboxesAHarness(t *testing.T) {
	skipWithoutPosixModes(t)
	f := newPolicyFixture(t)
	writePolicy(t, f.home, "allow_unsandboxed_runtimes: [codex, AGY, claude]\n", 0o600)
	got, ok := byID(f.check())["router-policy-unsandboxed"]
	if !ok {
		t.Fatal("allow_unsandboxed_runtimes names codex and agy: want router-policy-unsandboxed")
	}
	requireOneLine(t, got)
	if got.Severity != PolicyHigh {
		t.Errorf("severity = %s, want high", got.Severity)
	}
	if !strings.Contains(got.Message, "codex, agy") || strings.Contains(got.Message, "claude") {
		t.Errorf("name the harnesses it affects (codex, agy) and not claude: %q", got.Message)
	}
	if !strings.Contains(got.Message, "~/.yakos-state/router-policy.yml") {
		t.Errorf("name the file to edit: %q", got.Message)
	}
}

func TestCheckPolicy_RouterPolicyRefusedIsReportedAsRefused(t *testing.T) {
	skipWithoutPosixModes(t)
	for name, tc := range map[string]struct {
		setup func(t *testing.T, home string)
		want  string
	}{
		"group and world writable": {
			func(t *testing.T, home string) { writePolicy(t, home, "allow_unsandboxed_runtimes: [codex]\n", 0o666) },
			"writable",
		},
		"symlink": {
			func(t *testing.T, home string) {
				real := filepath.Join(t.TempDir(), "real.yml")
				if err := os.WriteFile(real, []byte("allow_unsandboxed_runtimes: [codex]\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				dir := filepath.Join(home, ".yakos-state")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(real, filepath.Join(dir, "router-policy.yml")); err != nil {
					t.Skip("symlinks unavailable")
				}
			},
			"symlink",
		},
		"not yaml": {
			// A string where a list belongs: yaml.v3's own error quotes the scalar.
			func(t *testing.T, home string) {
				writePolicy(t, home, "allow_unsandboxed_runtimes: SECRETCONTENT\n", 0o600)
			},
			"could not be parsed",
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newPolicyFixture(t)
			tc.setup(t, f.home)
			fs := byID(f.check())
			got, ok := fs["router-policy-refused"]
			if !ok {
				t.Fatalf("a refused file must be reported as refused, got %v", ids(f.check()))
			}
			requireOneLine(t, got)
			if got.Severity != PolicyMedium {
				t.Errorf("severity = %s, want medium", got.Severity)
			}
			if !strings.Contains(got.Message, tc.want) {
				t.Errorf("the reason %q is missing: %q", tc.want, got.Message)
			}
			if strings.Contains(got.Message+got.Fix, "SECRETCONTENT") {
				t.Errorf("file content leaked into the finding: %+v", got)
			}
			if strings.Contains(got.Message+got.Fix, f.home) {
				t.Errorf("the absolute home path leaked into the finding: %+v", got)
			}
			if _, bypass := fs["router-policy-unsandboxed"]; bypass {
				t.Error("a refused policy allows nothing: it must not also report an active bypass")
			}
		})
	}
}

func TestCheckPolicy_RouterPolicyThatNamesNoKnownRuntimeHasNoEffect(t *testing.T) {
	skipWithoutPosixModes(t)
	f := newPolicyFixture(t)
	writePolicy(t, f.home, "allow_unsandboxed_runtimes: [claude, mystery-runtime]\n", 0o600)
	fs := byID(f.check())
	got, ok := fs["router-policy-no-effect"]
	if !ok {
		t.Fatalf("a non-empty list that names no codex or agy must be flagged, got %v", ids(f.check()))
	}
	requireOneLine(t, got)
	if got.Severity != PolicyLow {
		t.Errorf("severity = %s, want low", got.Severity)
	}
	if strings.Contains(got.Message, "mystery-runtime") {
		t.Errorf("do not echo policy values: %q", got.Message)
	}
	if _, bypass := fs["router-policy-unsandboxed"]; bypass {
		t.Error("nothing is unsandboxed")
	}
}

func TestCheckPolicy_RouterPolicyAbsentOrEmptyIsQuiet(t *testing.T) {
	skipWithoutPosixModes(t)
	f := newPolicyFixture(t)
	if fs := f.check(); len(fs) != 0 {
		t.Errorf("no file: %v", ids(fs))
	}
	writePolicy(t, f.home, "allow_unsandboxed_runtimes: []\n", 0o600)
	if fs := f.check(); len(fs) != 0 {
		t.Errorf("an empty list: %v", ids(fs))
	}
}

// A relocated state directory must never make the report (or dispatch) believe a
// planted policy: the policy is read from $HOME/.yakos-state only (K-129).
func TestCheckPolicy_PolicyInARelocatedStateDirIsNotRead(t *testing.T) {
	skipWithoutPosixModes(t)
	f := newPolicyFixture(t)
	planted := t.TempDir()
	if err := os.WriteFile(filepath.Join(planted, "router-policy.yml"), []byte("allow_unsandboxed_runtimes: [codex, agy]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.env["YAKOS_DISPATCH_LOG"] = planted
	fs := byID(f.check())
	if _, bypass := fs["router-policy-unsandboxed"]; bypass {
		t.Error("YAKOS_DISPATCH_LOG relocated the policy read: a project could plant its own bypass")
	}
	if _, ok := fs["state-path-override:YAKOS_DISPATCH_LOG"]; !ok {
		t.Error("the relocation itself must be reported")
	}
}

// ---- bash dispatch -------------------------------------------------------------

func TestCheckPolicy_BashDispatchRunsHarnessesWithoutTheirSandbox(t *testing.T) {
	cases := []struct {
		name  string
		impl  string
		bash  bool
		found map[string]string
		want  bool
		named string
	}{
		{"unset impl, bash tree, codex and agy", "", true, map[string]string{"codex": "/x/codex", "agy": "/x/agy"}, true, "codex, agy"},
		{"unset impl, bash tree, agy only", "", true, map[string]string{"agy": "/x/agy"}, true, "agy"},
		{"explicit bash impl", "bash", true, map[string]string{"codex": "/x/codex"}, true, "codex"},
		{"explicit bash impl without the bash tree errors out instead of running", "bash", false, map[string]string{"codex": "/x/codex"}, false, ""},
		{"impl go", "go", true, map[string]string{"codex": "/x/codex", "agy": "/x/agy"}, false, ""},
		{"unset impl, no bash tree", "", false, map[string]string{"codex": "/x/codex"}, false, ""},
		{"bash route but neither harness installed", "", true, nil, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPolicyFixture(t)
			f.bash = tc.bash
			f.env["YAKOS_IMPL"] = tc.impl
			for k, v := range tc.found {
				f.found[k] = v
			}
			// Keep unrelated checks out of the way.
			f.env["OPENAI_API_KEY"] = "set"
			got, ok := byID(f.check())["bash-dispatch-unsandboxed"]
			if ok != tc.want {
				t.Fatalf("reported = %v, want %v (%v)", ok, tc.want, ids(f.check()))
			}
			if !ok {
				return
			}
			requireOneLine(t, got)
			if got.Severity != PolicyHigh {
				t.Errorf("severity = %s, want high", got.Severity)
			}
			if !strings.Contains(got.Message, tc.named) {
				t.Errorf("name the installed harnesses %q: %q", tc.named, got.Message)
			}
			if !strings.Contains(got.Fix, "YAKOS_IMPL=go") {
				t.Errorf("the fix is YAKOS_IMPL=go: %q", got.Fix)
			}
		})
	}
}

// YAKOS_IMPL is project-settable (a committed .claude/settings.json env block can set
// it, K-129), so whatever it holds must never reach the report: the finding names the
// variable and one of two fixed states, never a value.
func TestCheckPolicy_BashDispatchNeverPrintsTheYakosImplValue(t *testing.T) {
	const marker = "SENTINELIMPLVALUE0123"
	cases := []struct {
		impl string
		want string // fixed wording the finding must use instead
	}{
		{marker, "YAKOS_IMPL is not set to go"},
		{"go-" + marker, "YAKOS_IMPL is not set to go"},
		{"  " + marker + "  ", "YAKOS_IMPL is not set to go"},
		{"bash", "YAKOS_IMPL=bash"},
		{"", "YAKOS_IMPL is not set to go"},
	}
	for _, tc := range cases {
		f := newPolicyFixture(t)
		f.bash = true
		f.found["codex"] = "/x/codex"
		f.env["YAKOS_IMPL"] = tc.impl
		f.env["OPENAI_API_KEY"] = "set" // keep the codex profile finding out of the way
		got, ok := byID(f.check())["bash-dispatch-unsandboxed"]
		if !ok {
			t.Fatalf("YAKOS_IMPL=%q: want the bash-dispatch finding", tc.impl)
		}
		if strings.Contains(got.Message+got.Fix, marker) {
			t.Errorf("YAKOS_IMPL=%q: the finding printed the variable's value: %+v", tc.impl, got)
		}
		if !strings.Contains(got.Message, tc.want) {
			t.Errorf("YAKOS_IMPL=%q: want the fixed wording %q, got %q", tc.impl, tc.want, got.Message)
		}
	}
}

// ---- codex profile -------------------------------------------------------------

func TestCheckPolicy_CodexOnPathWithoutAYakosProfile(t *testing.T) {
	f := newPolicyFixture(t)
	f.found["codex"] = "/x/codex"
	got, ok := byID(f.check())["codex-shared-login"]
	if !ok {
		t.Fatal("codex on PATH with no yakOS profile must be reported")
	}
	requireOneLine(t, got)
	if got.Severity != PolicyLow {
		t.Errorf("severity = %s, want low", got.Severity)
	}
	if !strings.Contains(got.Fix, "yakos auth login codex") {
		t.Errorf("the fix is yakos auth login codex: %q", got.Fix)
	}
}

func TestCheckPolicy_CodexQuietWithAProfileAnAPIKeyOrNoCodex(t *testing.T) {
	f := newPolicyFixture(t)
	if _, flagged := byID(f.check())["codex-shared-login"]; flagged {
		t.Error("codex is not installed")
	}
	f.found["codex"] = "/x/codex"
	f.env["OPENAI_API_KEY"] = "sk-x"
	if _, flagged := byID(f.check())["codex-shared-login"]; flagged {
		t.Error("an API key has no shared auth.json to isolate")
	}
	delete(f.env, "OPENAI_API_KEY")
	dir := filepath.Join(f.home, ".yakos-state", "codex-home")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, flagged := byID(f.check())["codex-shared-login"]; flagged {
		t.Error("the yakOS profile holds a login")
	}
}

// ---- state path overrides ------------------------------------------------------

func TestCheckPolicy_StatePathOverridesAreReportedByNameNeverByValue(t *testing.T) {
	names := []string{
		"YAKOS_DISPATCH_LOG", "YAKOS_STATE_DIR", "YAKOS_MEMORY_DIR", "YAKOS_PLAN_QUALITY_LOG",
		"YAKOS_MR_STATE_DIR", "YAKOS_MR_EVAL_LOG", "YAKOS_MR_CANDIDATES", "YAKOS_MR_HISTORY",
		"YAKOS_MR_GRAVEYARD", "YAKOS_MR_BACKUPS_DIR", "YAKOS_COORD_ROOT", "YAKOS_WORK_DIR",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			f := newPolicyFixture(t)
			f.env[name] = "/relocated/SENTINELVALUE"
			fs := f.check()
			if len(fs) != 1 || fs[0].ID != "state-path-override:"+name {
				t.Fatalf("want exactly state-path-override:%s, got %v", name, ids(fs))
			}
			got := fs[0]
			requireOneLine(t, got)
			if got.Severity != PolicyMedium {
				t.Errorf("severity = %s, want medium", got.Severity)
			}
			if !strings.Contains(got.Message, name) {
				t.Errorf("name the variable: %q", got.Message)
			}
			if strings.Contains(got.Message+got.Fix, "SENTINELVALUE") || strings.Contains(got.Message+got.Fix, "/relocated") {
				t.Errorf("the value must never be printed: %+v", got)
			}
			if !strings.Contains(got.Fix, "unset "+name) {
				t.Errorf("the fix is to unset it: %q", got.Fix)
			}
			f.env[name] = ""
			if fs := f.check(); len(fs) != 0 {
				t.Errorf("an empty value is not an override: %v", ids(fs))
			}
		})
	}
}

// ---- ordering, determinism, serialisation ----------------------------------------

func TestCheckPolicy_OrderedBySeverityThenIDAndDeterministic(t *testing.T) {
	skipWithoutPosixModes(t)
	f := newPolicyFixture(t)
	f.sdk, f.sdkEnabled = true, true                                       // medium: sdk-sidecar-no-api-key
	f.found["codex"] = "/x/codex"                                          // low: codex-shared-login
	f.bash = true                                                          // high: bash-dispatch-unsandboxed
	f.env["YAKOS_STATE_DIR"] = "/x"                                        // medium: state-path-override
	writePolicy(t, f.home, "allow_unsandboxed_runtimes: [codex]\n", 0o600) // high
	first := f.check()
	want := []string{
		"bash-dispatch-unsandboxed", "router-policy-unsandboxed", // high, by id
		"sdk-sidecar-no-api-key", "state-path-override:YAKOS_STATE_DIR", // medium, by id
		"codex-shared-login", // low
	}
	if !reflect.DeepEqual(ids(first), want) {
		t.Fatalf("order = %v\nwant    %v", ids(first), want)
	}
	for i := 0; i < 5; i++ {
		if again := f.check(); !reflect.DeepEqual(first, again) {
			t.Fatalf("the report is not deterministic:\n%v\n%v", first, again)
		}
	}
}

func TestPolicyFinding_SerialisesForTheConsole(t *testing.T) {
	b, err := json.Marshal(PolicyFinding{ID: "x", Severity: PolicyHigh, Message: "m", Fix: "f"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"id":"x","severity":"high","message":"m","fix":"f"}`; got != want {
		t.Errorf("json = %s, want %s", got, want)
	}
}

// ---- the doctor mode -------------------------------------------------------------

func TestRun_PolicyOnlyPrintsTheReportAndNeverFailsTheRun(t *testing.T) {
	skipWithoutPosixModes(t)
	home := t.TempDir()
	writePolicy(t, home, "allow_unsandboxed_runtimes: [codex]\n", 0o600)
	env := map[string]string{
		"HOME": home, "YAKOS_DISPATCH_LOG": "/relocated/SENTINELPATH",
		"ANTHROPIC_API_KEY": policyOAuthToken, "CLAUDE_CODE_OAUTH_TOKEN": policyOAuthToken,
		"YAKOS_IMPL": "",
	}
	var buf bytes.Buffer
	rep, err := Run(Config{
		Writer: &buf, ErrWriter: &buf, HomeDir: home,
		Environ:                    func(k string) string { return env[k] },
		LookPath:                   singleLookPath(map[string]string{"codex": "/x/codex", "agy": "/x/agy"}),
		PolicyOnly:                 true,
		PolicyBashTreePresent:      true,
		PolicySDKSidecarSelectable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if rep.Errors != 0 || rep.Warnings != 0 {
		t.Errorf("--policy is a report, not a gate: errors=%d warnings=%d", rep.Errors, rep.Warnings)
	}
	if len(rep.Policy) < 4 {
		t.Errorf("expected several findings on this machine, got %v", ids(rep.Policy))
	}
	for _, want := range []string{"yakos doctor", "Risky configurations", "[high]", "[medium]", "[low]", "Fix:", "Policy:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	for _, other := range []string{"Required commands", "Optional commands", "YakOS symlinks", "Summary:"} {
		if strings.Contains(out, other) {
			t.Errorf("--policy runs ONLY the policy report, but printed %q:\n%s", other, out)
		}
	}
	for _, secret := range []string{"SENTINELPATH", policyOAuthSecret, home} {
		if strings.Contains(out, secret) {
			t.Errorf("the report printed a value it must never print (%q):\n%s", secret, out)
		}
	}
	// One finding per line.
	findingLines := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  [high]") || strings.HasPrefix(line, "  [medium]") || strings.HasPrefix(line, "  [low]") {
			findingLines++
		}
	}
	if findingLines != len(rep.Policy) {
		t.Errorf("%d finding lines for %d findings:\n%s", findingLines, len(rep.Policy), out)
	}
}

func TestRun_PolicyOnlyOnACleanMachineSaysSo(t *testing.T) {
	home := t.TempDir()
	var buf bytes.Buffer
	rep, err := Run(Config{
		Writer: &buf, ErrWriter: &buf, HomeDir: home,
		Environ:    func(string) string { return "" },
		LookPath:   noLookPath,
		PolicyOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Policy) != 0 || !strings.Contains(buf.String(), "no risky configuration found") {
		t.Errorf("clean machine: findings=%v output:\n%s", ids(rep.Policy), buf.String())
	}
}

// Without --policy the default report is byte-for-byte what it was: the policy
// section is opt-in, like --preflight.
func TestRun_DefaultReportHasNoPolicySection(t *testing.T) {
	home := makeTmpHome(t)
	var buf bytes.Buffer
	if _, err := Run(Config{
		Writer: &buf, ErrWriter: &buf, HomeDir: home,
		Environ:  func(string) string { return "" },
		LookPath: noLookPath,
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "Risky configurations") {
		t.Errorf("the default report must not include the policy section:\n%s", buf.String())
	}
}
