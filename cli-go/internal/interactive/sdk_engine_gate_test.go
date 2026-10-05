package interactive_test

// sdk_engine_gate_test.go — K-137: the Node Agent-SDK sidecar is hard-gated on
// ANTHROPIC_API_KEY.
//
// Anthropic does not allow a Pro/Max subscription's OAuth in the Agent SDK
// (terms of 2026-02-19), and before K-137 the sidecar ran on the operator's
// claude.ai login whenever no key was set. These tests prove, without a real
// SDK or network:
//
//   - SDKEngine.Start refuses before any process is spawned, with an error that
//     names the variable and the CLI engine and echoes no token material;
//   - a key that is really an OAuth token counts as no key;
//   - the process that IS spawned gets the key and no OAuth material;
//   - the shipped sidecar bundle refuses on its own (second anchor), so
//     launching node directly cannot bypass the Go check.
//
// The spawn tests re-execute this test binary as the "node" process (the
// standard os/exec helper-process pattern) so they need neither sh nor node.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/interactive"
	yakruntime "github.com/bakw00ds/yakos/internal/runtime"
)

const (
	gateAPIKey      = "sk-ant-api03-gate-test-key-not-real"
	gateOAuthSecret = "GATESECRET0123456789"
	gateOAuthToken  = "sk-ant-oat01-" + gateOAuthSecret
	helperEnvFlag   = "YAKOS_TEST_SIDECAR_HELPER" // YAKOS_ prefix: survives the env allowlist
	helperEnvOut    = "YAKOS_TEST_SIDECAR_OUT"
	helperTestName  = "TestSDKEngineGateHelperProcess"
)

// unsetenv removes key for the rest of the test and restores it afterwards.
func unsetenv(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		}
	})
}

// TestSDKEngineGateHelperProcess is not a test. When the flag variable is set
// it acts as the fake node sidecar: it records the NAMES of the variables it
// was given (never their values), emits the ready frame and waits for stdin to
// close. Without the flag it returns at once.
func TestSDKEngineGateHelperProcess(t *testing.T) {
	if os.Getenv(helperEnvFlag) != "1" {
		return
	}
	var names []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		names = append(names, name)
	}
	sort.Strings(names)
	if out := os.Getenv(helperEnvOut); out != "" {
		_ = os.WriteFile(out, []byte(strings.Join(names, "\n")+"\n"), 0o600)
	}
	_, _ = fmt.Fprintln(os.Stdout, `{"v":1,"kind":"ready"}`)
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

// newHelperEngine builds an engine that spawns this test binary as its node.
func newHelperEngine(t *testing.T, outFile string) *interactive.SDKEngine {
	t.Helper()
	t.Setenv(helperEnvFlag, "1")
	t.Setenv(helperEnvOut, outFile)
	eng, err := interactive.NewSDKEngine(interactive.SDKEngineParams{
		ConversationID:  "conv-gate",
		OwnerOperatorID: "op-gate",
		NodePath:        os.Args[0],
		BundlePath:      "-test.run=^" + helperTestName + "$",
	})
	if err != nil {
		t.Fatalf("NewSDKEngine: %v", err)
	}
	return eng
}

func startCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func spawned(outFile string) bool {
	_, err := os.Stat(outFile)
	return err == nil
}

func TestSDKEngineStart_RefusesWithoutAPIKeyBeforeSpawning(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
	}{
		{"unset", func(t *testing.T) { unsetenv(t, "ANTHROPIC_API_KEY") }},
		{"empty", func(t *testing.T) { t.Setenv("ANTHROPIC_API_KEY", "") }},
		{"blank", func(t *testing.T) { t.Setenv("ANTHROPIC_API_KEY", "   ") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			// A subscription login in the environment must not stand in for the key.
			t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", gateOAuthToken)
			out := filepath.Join(t.TempDir(), "spawned.txt")
			eng := newHelperEngine(t, out)

			err := eng.Start(startCtx(t))
			if err == nil {
				_ = eng.Close()
				t.Fatal("Start succeeded without ANTHROPIC_API_KEY: the sidecar would run on the claude.ai login")
			}
			if !errors.Is(err, yakruntime.ErrSDKAPIKeyRequired) {
				t.Errorf("err = %v, want it to wrap ErrSDKAPIKeyRequired", err)
			}
			msg := err.Error()
			for _, want := range []string{"ANTHROPIC_API_KEY", "CLI engine"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal must mention %q: %s", want, msg)
				}
			}
			if strings.Contains(msg, gateOAuthSecret) {
				t.Errorf("the refusal echoed token material: %s", msg)
			}
			if spawned(out) {
				t.Error("the sidecar process was spawned even though Start refused")
			}
		})
	}
}

func TestSDKEngineStart_RefusesAnOAuthTokenInTheAPIKeySlot(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", gateOAuthToken)
	out := filepath.Join(t.TempDir(), "spawned.txt")
	eng := newHelperEngine(t, out)

	err := eng.Start(startCtx(t))
	if err == nil {
		_ = eng.Close()
		t.Fatal("Start accepted a subscription OAuth token as the API key")
	}
	if !errors.Is(err, yakruntime.ErrSDKAPIKeyIsOAuthToken) {
		t.Errorf("err = %v, want ErrSDKAPIKeyIsOAuthToken", err)
	}
	if strings.Contains(err.Error(), gateOAuthSecret) {
		t.Errorf("the refusal echoed token material: %s", err.Error())
	}
	if spawned(out) {
		t.Error("the sidecar process was spawned with an OAuth token as its key")
	}
}

func TestSDKEngineStart_GateAppliesToTheProviderSeamToo(t *testing.T) {
	unsetenv(t, "ANTHROPIC_API_KEY")
	var calls atomic.Int32
	eng, err := interactive.NewSDKEngineWithProvider(
		interactive.SDKEngineParams{ConversationID: "conv-seam", OwnerOperatorID: "op"},
		func() *exec.Cmd {
			calls.Add(1)
			return exec.Command(os.Args[0], "-test.run=^$")
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(startCtx(t)); !errors.Is(err, yakruntime.ErrSDKAPIKeyRequired) {
		t.Fatalf("Start err = %v, want ErrSDKAPIKeyRequired", err)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("the command provider ran %d time(s) before the gate refused", n)
	}
}

func TestSDKEngineStart_SpawnsWithTheKeyAndWithoutOAuthMaterial(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", gateAPIKey)
	t.Setenv("ANTHROPIC_BASE_URL", "https://gateway.example")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", gateOAuthToken)
	t.Setenv("CLAUDE_CODE_OAUTH_REFRESH_TOKEN", "sk-ant-ort01-"+gateOAuthSecret)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", gateOAuthToken) // an OAuth token filed as a gateway token
	t.Setenv("OPENAI_API_KEY", "sk-openai-must-not-reach-the-sidecar")
	out := filepath.Join(t.TempDir(), "spawned.txt")
	eng := newHelperEngine(t, out)

	if err := eng.Start(startCtx(t)); err != nil {
		t.Fatalf("Start with a key set: %v", err)
	}
	defer func() { _ = eng.Close() }()

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the sidecar did not record its environment: %v", err)
	}
	got := map[string]bool{}
	for _, name := range strings.Fields(string(raw)) {
		got[name] = true
	}
	for _, want := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"} {
		if !got[want] {
			t.Errorf("the sidecar did not receive %s", want)
		}
	}
	for _, banned := range []string{
		"CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_REFRESH_TOKEN", "ANTHROPIC_AUTH_TOKEN", "OPENAI_API_KEY",
	} {
		if got[banned] {
			t.Errorf("the sidecar received %s", banned)
		}
	}
}

// --- the shipped bundle: second anchor ---------------------------------------

// sidecarBundle returns node and the committed bundle, or skips.
func sidecarBundle(t *testing.T) (node, bundle string) {
	t.Helper()
	node, err := interactive.FindNodeBinary()
	if err != nil {
		t.Skipf("node not usable: %v", err)
	}
	bundle, err = filepath.Abs(filepath.Join("sidecar", "sidecar.bundle.js"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bundle); err != nil {
		t.Skipf("sidecar bundle not present: %v", err)
	}
	return node, bundle
}

// envWithout copies the environment minus every ANTHROPIC_ and CLAUDE_ variable,
// points HOME at an empty directory so that a regression which started the real
// SDK still could not read the operator's ~/.claude login, and appends extra.
func envWithout(home string, extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "ANTHROPIC_") || strings.HasPrefix(upper, "CLAUDE_") ||
			upper == "HOME" || upper == "USERPROFILE" {
			continue
		}
		env = append(env, kv)
	}
	return append(env, append([]string{"HOME=" + home, "USERPROFILE=" + home}, extra...)...)
}

func TestSidecarBundle_RefusesToStartWithoutAnAPIKey(t *testing.T) {
	node, bundle := sidecarBundle(t)
	for _, tc := range []struct {
		name   string
		extra  []string
		secret string // must not appear on stderr
	}{
		{"unset", nil, ""},
		{"empty", []string{"ANTHROPIC_API_KEY="}, ""},
		{"blank", []string{"ANTHROPIC_API_KEY=   "}, ""},
		{"subscription token only", []string{"CLAUDE_CODE_OAUTH_TOKEN=" + gateOAuthToken}, gateOAuthSecret},
		{"oauth token as the key", []string{"ANTHROPIC_API_KEY=" + gateOAuthToken}, gateOAuthSecret},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, node, bundle)
			cmd.Env = envWithout(t.TempDir(), tc.extra...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			cmd.Stdin = strings.NewReader("") // closed stdin: a running sidecar would exit too, but never emit a refusal

			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("the sidecar exited without a failure status (err=%v); it must refuse to start\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
			}
			if code := exitErr.ExitCode(); code != 78 {
				t.Errorf("exit code = %d, want 78 (EX_CONFIG)\nstderr: %s", code, stderr.String())
			}
			if strings.Contains(stdout.String(), `"ready"`) {
				t.Errorf("the sidecar announced ready without a key: %s", stdout.String())
			}
			lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
			if len(lines) != 1 || !strings.Contains(lines[0], "ANTHROPIC_API_KEY") || !strings.Contains(lines[0], "refusing to start") {
				t.Errorf("stderr must be one line naming the refusal and ANTHROPIC_API_KEY, got %d line(s): %q", len(lines), stderr.String())
			}
			if tc.secret != "" && strings.Contains(stderr.String()+stdout.String(), tc.secret) {
				t.Errorf("token material was echoed: %q", stderr.String())
			}
		})
	}
}
