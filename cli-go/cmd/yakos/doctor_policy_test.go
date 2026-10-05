package main

// doctor_policy_test.go — K-137: `yakos doctor --policy` is wired through the
// real command, not just the doctor package. A new flag needs parsing in
// runDoctor and an entry in the command registry; package-level tests miss both,
// so these drive the built binary the way the other Go-native doctor tests do.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func policyBinary(t *testing.T) string {
	t.Helper()
	return requireBinaryAt(t, resolveGoBinary(), os.Getenv("CI"))
}

func writeStateFile(t *testing.T, home, name, body string, mode os.FileMode) {
	t.Helper()
	dir := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

func policyEnv(home string) map[string]string {
	return map[string]string{
		"HOME": home, "USERPROFILE": home,
		// No other source of findings leaks in from the machine running the test.
		"YAKOS_DISPATCH_LOG": "", "YAKOS_STATE_DIR": "", "YAKOS_MEMORY_DIR": "",
		"YAKOS_PLAN_QUALITY_LOG": "", "YAKOS_COORD_ROOT": "",
	}
}

func TestDoctor_GoNative_PolicyReportsAndExitsZero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	goBin := policyBinary(t)
	home := t.TempDir()
	writeStateFile(t, home, "router-policy.yml", "allow_unsandboxed_runtimes: [codex]\n", 0o600)
	env := policyEnv(home)
	env["YAKOS_STATE_DIR"] = "/relocated/SENTINELPATH"

	out, code := runGoDoctor(t, goBin, []string{"doctor", "--policy"}, env)
	if code != 0 {
		t.Fatalf("--policy is a report and must exit 0 whatever it finds; got %d\n%s", code, out)
	}
	for _, want := range []string{
		"Risky configurations",
		"[high]", "codex run WITHOUT their sandbox flags", "allow_unsandboxed_runtimes",
		"[medium]", "YAKOS_STATE_DIR is set in the environment",
		"Fix:", "Policy:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	for _, other := range []string{"Required commands", "Summary:"} {
		if strings.Contains(out, other) {
			t.Errorf("--policy runs only the policy report, but printed %q:\n%s", other, out)
		}
	}
	if strings.Contains(out, home) || strings.Contains(out, "SENTINELPATH") {
		t.Errorf("the report printed the absolute home path or an environment value:\n%s", out)
	}
}

func TestDoctor_GoNative_PolicyOnACleanHomeStillExitsZero(t *testing.T) {
	goBin := policyBinary(t)
	out, code := runGoDoctor(t, goBin, []string{"doctor", "--policy"}, policyEnv(t.TempDir()))
	if code != 0 || !strings.Contains(out, "Policy:") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
}

func TestDoctor_GoNative_PolicyRejectsMixedModesAndAProjectPath(t *testing.T) {
	goBin := policyBinary(t)
	env := policyEnv(t.TempDir())
	for _, args := range [][]string{
		{"doctor", "--policy", "--preflight"},
		{"doctor", "--policy", "--probe-runtime"},
		{"doctor", "--policy", "--production"},
		{"doctor", "--policy", "--probe-decision"},
		{"doctor", "--policy", "/some/project"},
	} {
		out, code := runGoDoctor(t, goBin, args, env)
		if code != 1 {
			t.Errorf("%v: exit %d, want 1 (a usage error)\n%s", args, code, out)
		}
		if strings.Contains(out, "Risky configurations") {
			t.Errorf("%v: a rejected invocation must not run the report:\n%s", args, out)
		}
	}
}

func TestDoctor_GoNative_HelpDocumentsPolicy(t *testing.T) {
	goBin := policyBinary(t)
	out, code := runGoDoctor(t, goBin, []string{"doctor", "--help"}, nil)
	if code != 0 {
		t.Fatalf("doctor --help exit %d:\n%s", code, out)
	}
	for _, want := range []string{
		"[--preflight] [--policy]\n\nExit code:",             // the usage line
		"If --policy is passed, ONLY the policy report runs", // the description
		"--policy always exits 0",                            // the exit-code contract
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor --help must document --policy; missing %q:\n%s", want, out)
		}
	}
}

// --policy has no bash equivalent, so main.go must not hand it to bash doctor.sh
// when YAKOS_IMPL is unset and the bash tree is present.
func TestIsDoctorForceGo_PolicyFlag(t *testing.T) {
	if !isDoctorForceGo("", []string{"doctor", "--policy"}) {
		t.Error("unset YAKOS_IMPL: `doctor --policy` must reach the Go implementation")
	}
	if !isDoctorForceGo("go", []string{"doctor", "--policy"}) {
		t.Error("YAKOS_IMPL=go: `doctor --policy` must reach the Go implementation")
	}
}

// fakeBin returns a directory holding executable stand-ins for the named
// commands, for use as the whole PATH of the doctor process.
func fakeBin(t *testing.T, names ...string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stand-ins for commands")
	}
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The two machine facts the doctor package cannot compute itself come from the
// command: where the executable lives (the bash tree the YAKOS_IMPL gate would
// route to) and whether node and the sidecar bundle exist. Drive both through
// the real binary.
func TestDoctor_GoNative_PolicySeesTheBashTreeAndHarnessesOnPath(t *testing.T) {
	goBin := policyBinary(t)
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(goBin)), "cli", "yakos")); err != nil {
		t.Skip("the binary does not sit in a checkout with the bash CLI tree")
	}
	home := t.TempDir()
	env := policyEnv(home)
	env["PATH"] = fakeBin(t, "codex", "agy")
	env["YAKOS_IMPL"] = "" // unset

	out, code := runGoDoctor(t, goBin, []string{"doctor", "--policy"}, env)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "yakos dispatch runs through the bash CLI") || !strings.Contains(out, "codex, agy WITHOUT their sandbox flags") {
		t.Errorf("YAKOS_IMPL unset with the bash tree and codex and agy installed must be reported:\n%s", out)
	}

	env["YAKOS_IMPL"] = "go"
	out, _ = runGoDoctor(t, goBin, []string{"doctor", "--policy"}, env)
	if strings.Contains(out, "runs through the bash CLI") {
		t.Errorf("YAKOS_IMPL=go sends dispatch to the Go dispatcher; nothing to report:\n%s", out)
	}
}

func TestDoctor_GoNative_PolicySeesTheSDKSidecarWhenNodeAndBundleExist(t *testing.T) {
	goBin := policyBinary(t)
	root := repoRootForParity(t)
	if _, err := os.Stat(filepath.Join(root, "cli-go", "internal", "interactive", "sidecar", "sidecar.bundle.js")); err != nil {
		t.Skip("sidecar bundle not present")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	bin := fakeBin(t)
	if err := os.Symlink(node, filepath.Join(bin, "node")); err != nil {
		t.Skipf("cannot link node: %v", err)
	}
	env := policyEnv(t.TempDir())
	env["PATH"] = bin
	env["YAKOS_ROOT"] = root
	env["ANTHROPIC_API_KEY"] = "" // unset

	out, code := runGoDoctor(t, goBin, []string{"doctor", "--policy"}, env)
	if code != 0 || !strings.Contains(out, "ANTHROPIC_API_KEY is not set") || !strings.Contains(out, "CLI engine") {
		t.Errorf("node and the bundle are present and no key is set: want the SDK sidecar finding (exit %d):\n%s", code, out)
	}

	env["ANTHROPIC_API_KEY"] = "sk-ant-api03-doctor-policy-test"
	out, _ = runGoDoctor(t, goBin, []string{"doctor", "--policy"}, env)
	if strings.Contains(out, "SDK sidecar") {
		t.Errorf("a key is set; nothing to report:\n%s", out)
	}
	if strings.Contains(out, "doctor-policy-test") {
		t.Errorf("the report printed the key:\n%s", out)
	}

	env["PATH"] = fakeBin(t) // no node
	env["ANTHROPIC_API_KEY"] = ""
	out, _ = runGoDoctor(t, goBin, []string{"doctor", "--policy"}, env)
	if strings.Contains(out, "SDK sidecar") {
		t.Errorf("no node on PATH: the sidecar cannot be selected; nothing to report:\n%s", out)
	}
}
