package doctor

// policy_runtime_checks_test.go — K-137: the two policy checks that sit on top of
// K-132's helpers. A default-runtime file the dispatcher refuses (the report says
// why, without printing a path), and agy on PATH that does not look signed in.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/auth"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// writeDefaultRuntime writes <stateDir>/default-runtime with the given mode.
func writeDefaultRuntime(t *testing.T, stateDir, body string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(stateDir, "default-runtime")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// dispatchRefuses is what the dispatcher itself says about stateDir: auth.ReadDefaultRuntime
// returns a warning exactly when it refused a file that exists. The doctor must agree.
func dispatchRefuses(stateDir string) bool {
	_, warning := auth.ReadDefaultRuntime(stateDir)
	return warning != ""
}

// requireRefusalFinding checks the one finding a refused default-runtime file produces. Its
// message is dispatch's own warning for that directory behind a short prefix, word for word,
// so the two cannot drift: both come from auth.ReadDefaultRuntime.
func requireRefusalFinding(t *testing.T, f *policyFixture, stateDir string, wantInMessage ...string) PolicyFinding {
	t.Helper()
	got, ok := byID(f.check())["default-runtime-refused"]
	if !ok {
		t.Fatalf("a refused default-runtime file must be reported, got %v", ids(f.check()))
	}
	requireOneLine(t, got)
	if got.Severity != PolicyMedium {
		t.Errorf("severity = %s, want medium", got.Severity)
	}
	_, warning := auth.ReadDefaultRuntime(stateDir)
	if warning == "" {
		t.Fatal("dispatch accepts this file, so the doctor must not report it")
	}
	if want := "dispatch is " + warning; got.Message != want {
		t.Errorf("the message must be dispatch's own warning, word for word\n got: %q\nwant: %q", got.Message, want)
	}
	for _, want := range wantInMessage {
		if !strings.Contains(got.Message, want) {
			t.Errorf("the message must contain %q: %q", want, got.Message)
		}
	}
	if !strings.Contains(got.Fix, "yakos auth set-default") {
		t.Errorf("the fix must name the command that writes the file again: %q", got.Fix)
	}
	if strings.Contains(got.Message+got.Fix, f.home) || strings.Contains(got.Message+got.Fix, os.TempDir()) {
		t.Errorf("the report must not print a path: %+v", got)
	}
	return got
}

func TestCheckPolicy_DefaultRuntimeAbsentOrTrustedIsQuiet(t *testing.T) {
	skipWithoutPosixModes(t)
	for name, setup := range map[string]func(stateDir string){
		"absent":                 func(string) {},
		"trusted":                func(d string) { writeDefaultRuntime(t, d, "codex\n", 0o600) },
		"trusted, not a runtime": func(d string) { writeDefaultRuntime(t, d, "NOT A RUNTIME !!\n", 0o600) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newPolicyFixture(t)
			stateDir := filepath.Join(f.home, ".yakos-state")
			setup(stateDir)
			if _, ok := byID(f.check())["default-runtime-refused"]; ok {
				t.Errorf("nothing is refused here: %v", ids(f.check()))
			}
			if dispatchRefuses(stateDir) {
				t.Error("the dispatcher refuses it, so the doctor must too")
			}
		})
	}
}

func TestCheckPolicy_DefaultRuntimeRefusedFileIsReportedWithItsReason(t *testing.T) {
	skipWithoutPosixModes(t)
	for _, c := range []struct {
		name   string
		setup  func(t *testing.T, stateDir string)
		reason string
	}{
		{"group or world writable", func(t *testing.T, d string) { writeDefaultRuntime(t, d, "codex\n", 0o666) }, "is group or world writable"},
		{"a symlink", func(t *testing.T, d string) {
			target := writeDefaultRuntime(t, t.TempDir(), "codex\n", 0o600)
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(d, "default-runtime")); err != nil {
				t.Fatal(err)
			}
		}, "is a symlink"},
		{"not a regular file", func(t *testing.T, d string) {
			if err := os.MkdirAll(filepath.Join(d, "default-runtime"), 0o700); err != nil {
				t.Fatal(err)
			}
		}, "is not a regular file"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newPolicyFixture(t)
			stateDir := filepath.Join(f.home, ".yakos-state")
			c.setup(t, stateDir)
			requireRefusalFinding(t, f, stateDir,
				"ignoring the default runtime: the default-runtime file in the yakOS state directory "+c.reason)
		})
	}
}

func TestCheckPolicy_DefaultRuntimeInAnUntrustedDirectoryNamesTheDirectory(t *testing.T) {
	skipWithoutPosixModes(t)
	t.Run("world writable directory", func(t *testing.T) {
		f := newPolicyFixture(t)
		stateDir := filepath.Join(f.home, ".yakos-state")
		writeDefaultRuntime(t, stateDir, "codex\n", 0o600)
		if err := os.Chmod(stateDir, 0o777); err != nil {
			t.Fatal(err)
		}
		requireRefusalFinding(t, f, stateDir, "ignoring the default runtime: the yakOS state directory is group or world writable")
	})
	t.Run("symlinked directory", func(t *testing.T) {
		f := newPolicyFixture(t)
		real := t.TempDir()
		writeDefaultRuntime(t, real, "codex\n", 0o600)
		stateDir := filepath.Join(f.home, ".yakos-state")
		if err := os.Symlink(real, stateDir); err != nil {
			t.Fatal(err)
		}
		requireRefusalFinding(t, f, stateDir, "ignoring the default runtime: the yakOS state directory is a symlink")
	})
}

func TestCheckPolicy_DefaultRuntimeIsReadWhereDispatchReadsIt(t *testing.T) {
	skipWithoutPosixModes(t)
	// statepath.Dir() honours YAKOS_DISPATCH_LOG, so that is the file dispatch reads.
	t.Run("relocated and refused", func(t *testing.T) {
		f := newPolicyFixture(t)
		relocated := t.TempDir()
		writeDefaultRuntime(t, relocated, "codex\n", 0o666)
		f.env["YAKOS_DISPATCH_LOG"] = relocated
		requireRefusalFinding(t, f, relocated, "ignoring the default runtime: the default-runtime file in the yakOS state directory is group or world writable")
	})
	t.Run("relocated and trusted while the home one is refused", func(t *testing.T) {
		f := newPolicyFixture(t)
		writeDefaultRuntime(t, filepath.Join(f.home, ".yakos-state"), "codex\n", 0o666) // not what dispatch reads now
		relocated := t.TempDir()
		writeDefaultRuntime(t, relocated, "codex\n", 0o600)
		f.env["YAKOS_DISPATCH_LOG"] = relocated
		if _, ok := byID(f.check())["default-runtime-refused"]; ok {
			t.Errorf("dispatch reads the relocated, trusted file: %v", ids(f.check()))
		}
		if dispatchRefuses(relocated) {
			t.Error("the dispatcher refuses the relocated file, so the doctor must too")
		}
	})
}

func TestCheckPolicy_DefaultRuntimeFallsBackToTheTempDirectoryWithoutAHome(t *testing.T) {
	skipWithoutPosixModes(t)
	// statepath.Dir() falls back to <temp dir>/.yakos-state when no home can be determined,
	// so that is where dispatch reads the default runtime and where the doctor must look.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("HOME", "")
	t.Setenv("YAKOS_DISPATCH_LOG", "")
	stateDir := statepath.Dir()
	if filepath.Dir(stateDir) != filepath.Clean(tmp) {
		t.Skipf("this platform has no TMPDIR-based fallback: statepath.Dir() = %q", stateDir)
	}
	writeDefaultRuntime(t, stateDir, "codex\n", 0o666)

	got := byID(CheckPolicy(PolicyEnv{Getenv: func(string) string { return "" }}))
	finding, ok := got["default-runtime-refused"]
	if !ok {
		t.Fatalf("with no home the doctor must read the file dispatch reads, in the temp directory; got %v", got)
	}
	_, warning := auth.ReadDefaultRuntime(stateDir)
	if warning == "" || finding.Message != "dispatch is "+warning {
		t.Errorf("the finding must be dispatch's own warning for that directory: %q, dispatch says %q", finding.Message, warning)
	}
	if strings.Contains(finding.Message, tmp) {
		t.Errorf("the report must not print the path: %q", finding.Message)
	}
}

// ---- the same, through Run: what `yakos doctor --policy` actually calls ----------------

// Run replaces an empty home with "/tmp" so its other sections have a path to print. The policy
// report must not inherit that stand-in: dispatch resolves its state directory from the real
// environment, and with no home that is a directory under the temp directory (statepath.Dir).
func TestRun_PolicyOnlyWithoutAHomeReadsTheFileDispatchReads(t *testing.T) {
	skipWithoutPosixModes(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("HOME", "")
	t.Setenv("YAKOS_DISPATCH_LOG", "")
	stateDir := statepath.Dir()
	if filepath.Dir(stateDir) != filepath.Clean(tmp) {
		t.Skipf("this platform has no TMPDIR-based fallback: statepath.Dir() = %q", stateDir)
	}
	writeDefaultRuntime(t, stateDir, "codex\n", 0o666)
	// A router policy sitting in that same temp state directory must NOT be read: dispatch reads
	// the policy from $HOME/.yakos-state only (statepath.TrustedDir is empty without a home, and
	// then nothing is allowed), never from a directory under a shared temp directory.
	writePolicy(t, tmp, "allow_unsandboxed_runtimes: [codex]\n", 0o600)

	var buf bytes.Buffer
	rep, err := Run(Config{Writer: &buf, ErrWriter: &buf, PolicyOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	got := byID(rep.Policy)
	finding, ok := got["default-runtime-refused"]
	if !ok {
		t.Fatalf("through Run the doctor must report the refusal dispatch makes; got %v", ids(rep.Policy))
	}
	if _, warning := auth.ReadDefaultRuntime(stateDir); finding.Message != "dispatch is "+warning {
		t.Errorf("the finding must be dispatch's own warning: %q vs %q", finding.Message, warning)
	}
	for id := range got {
		if strings.HasPrefix(id, "router-policy") {
			t.Errorf("without a home dispatch reads no router policy, so the doctor reports none; got %s", id)
		}
	}
	if strings.Contains(buf.String(), tmp) {
		t.Errorf("the printed report must not carry a path:\n%s", buf.String())
	}
}

// When the caller gives no home directory, Run reads HOME from the environment it was given,
// not from the process: an injected environment decides, as it does for every other check.
func TestRun_PolicyOnlyReadsTheHomeFromTheInjectedEnvironment(t *testing.T) {
	skipWithoutPosixModes(t)
	injected := t.TempDir()
	writeDefaultRuntime(t, filepath.Join(injected, ".yakos-state"), "codex\n", 0o666)
	t.Setenv("HOME", t.TempDir()) // the process's own home must not decide
	t.Setenv("YAKOS_DISPATCH_LOG", "")

	var buf bytes.Buffer
	rep, err := Run(Config{
		Writer: &buf, ErrWriter: &buf, PolicyOnly: true,
		Environ: func(k string) string {
			if k == "HOME" {
				return injected
			}
			return ""
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := byID(rep.Policy)["default-runtime-refused"]; !ok {
		t.Errorf("the HOME in the injected environment holds a refused default-runtime file and must be read; got %v", ids(rep.Policy))
	}
}

// A policy path must never be resolved against the working directory: with no home the
// doctor reads no router policy at all, so a .yakos-state sitting in the current directory,
// which a project controls, is not read either.
func TestRun_PolicyOnlyWithoutAHomeNeverReadsAPolicyFromTheWorkingDirectory(t *testing.T) {
	skipWithoutPosixModes(t)
	cwd := t.TempDir()
	writePolicy(t, cwd, "allow_unsandboxed_runtimes: [codex]\n", 0o600) // cwd/.yakos-state/router-policy.yml
	t.Chdir(cwd)
	t.Setenv("HOME", "")
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("YAKOS_DISPATCH_LOG", "")

	var buf bytes.Buffer
	rep, err := Run(Config{Writer: &buf, ErrWriter: &buf, PolicyOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Policy {
		if strings.HasPrefix(f.ID, "router-policy") {
			t.Errorf("a policy in the working directory must not be read: %s", f.ID)
		}
	}
}

// ---- agy sign-in ---------------------------------------------------------------

func agyProbeFixture(t *testing.T, p RuntimeProbe) (*policyFixture, *struct {
	id       string
	deadline bool
	calls    int
}) {
	t.Helper()
	f := newPolicyFixture(t)
	seen := &struct {
		id       string
		deadline bool
		calls    int
	}{}
	f.probe = func(ctx context.Context, id string) RuntimeProbe {
		seen.id, seen.calls = id, seen.calls+1
		_, seen.deadline = ctx.Deadline()
		return p
	}
	return f, seen
}

func TestCheckPolicy_AgyOnPathButNotSignedIn(t *testing.T) {
	f, seen := agyProbeFixture(t, RuntimeProbe{CLIPresent: true})
	got, ok := byID(f.check())["agy-not-signed-in"]
	if !ok {
		t.Fatalf("agy is on PATH and not signed in: want agy-not-signed-in, got %v", ids(f.check()))
	}
	requireOneLine(t, got)
	if got.Severity != PolicyLow {
		t.Errorf("severity = %s, want low", got.Severity)
	}
	for _, want := range []string{"agy", "signed in"} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("the message must mention %q: %q", want, got.Message)
		}
	}
	if !strings.Contains(got.Fix, "yakos auth login agy") {
		t.Errorf("the fix must name the login command: %q", got.Fix)
	}
	if seen.id != "agy" {
		t.Errorf("the probe was asked about %q, want agy", seen.id)
	}
	if !seen.deadline {
		t.Error("the probe must get a context with a deadline")
	}
	// K-158: agy's --sandbox is not containment, so no finding about agy may call it
	// sandboxed or contained.
	for _, banned := range []string{"sandbox", "contain"} {
		if strings.Contains(strings.ToLower(got.Message+got.Fix), banned) {
			t.Errorf("the agy finding must not use %q (K-158): %+v", banned, got)
		}
	}
}

func TestCheckPolicy_AgyFixDoesNotClaimItReadsNoCredential(t *testing.T) {
	// auth.checkAuth reads yakOS's own keyring entry to test that one exists, so the report
	// may say it prints no credential, never that it reads none.
	f, _ := agyProbeFixture(t, RuntimeProbe{CLIPresent: true})
	got := byID(f.check())["agy-not-signed-in"]
	if strings.Contains(strings.ToLower(got.Fix), "reads no credential") {
		t.Errorf("the check reads a keyring entry; the fix must not say it reads no credential: %q", got.Fix)
	}
	if !strings.Contains(got.Fix, "prints no credential") {
		t.Errorf("the fix must say what is true, that the check prints no credential: %q", got.Fix)
	}
}

func TestCheckPolicy_AgySignInIsQuietWhenThereIsNothingToSay(t *testing.T) {
	for name, p := range map[string]RuntimeProbe{
		"agy not installed": {},
		"signed in":         {CLIPresent: true, Authed: true},
	} {
		t.Run(name, func(t *testing.T) {
			f, seen := agyProbeFixture(t, p)
			if _, ok := byID(f.check())["agy-not-signed-in"]; ok {
				t.Errorf("nothing to report: %v", ids(f.check()))
			}
			if seen.calls != 1 {
				t.Errorf("the probe must be asked exactly once, got %d", seen.calls)
			}
		})
	}
	t.Run("no probe supplied", func(t *testing.T) {
		f := newPolicyFixture(t)
		if got := f.check(); len(got) != 0 {
			t.Errorf("without a probe the check is silent, got %v", ids(got))
		}
	})
}

func TestCheckPolicy_AgySignInShowsWhatTheProbeCouldNotSettle(t *testing.T) {
	f, _ := agyProbeFixture(t, RuntimeProbe{CLIPresent: true, Note: "the OS keyring did not answer within 2s"})
	got := byID(f.check())["agy-not-signed-in"]
	if !strings.Contains(got.Message, "the OS keyring did not answer within 2s") {
		t.Errorf("the probe's note belongs in the message: %q", got.Message)
	}
	requireOneLine(t, got)
}

func TestCheckPolicy_AgySignInCannotHangTheReport(t *testing.T) {
	old := agyProbeTimeout
	agyProbeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { agyProbeTimeout = old })
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	f := newPolicyFixture(t)
	f.probe = func(ctx context.Context, id string) RuntimeProbe {
		<-release // a probe that ignores its context
		return RuntimeProbe{CLIPresent: true}
	}
	done := make(chan []PolicyFinding, 1)
	go func() { done <- f.check() }()
	select {
	case got := <-done:
		if _, ok := byID(got)["agy-not-signed-in"]; ok {
			t.Error("a probe that never answered must not produce a finding")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CheckPolicy hung on a probe that ignores its context")
	}
}

func TestRun_PolicyOnlyUsesTheCallersSignInProbe(t *testing.T) {
	var asked string
	var buf bytes.Buffer
	rep, err := Run(Config{
		Writer: &buf, ErrWriter: &buf, HomeDir: t.TempDir(),
		Environ:    func(string) string { return "" },
		LookPath:   singleLookPath(map[string]string{}),
		PolicyOnly: true,
		PolicyProbeRuntime: func(ctx context.Context, id string) RuntimeProbe {
			asked = id
			return RuntimeProbe{CLIPresent: true}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if asked != "agy" {
		t.Errorf("the report must ask the caller's probe about agy, asked %q", asked)
	}
	if _, ok := byID(rep.Policy)["agy-not-signed-in"]; !ok {
		t.Errorf("the finding must reach the report, got %v", ids(rep.Policy))
	}
	if !strings.Contains(buf.String(), "agy is on PATH but does not look signed in") {
		t.Errorf("the printed report must carry the finding:\n%s", buf.String())
	}
}
