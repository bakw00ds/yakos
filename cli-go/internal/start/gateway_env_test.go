package start

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeStartPolicy(t *testing.T, home, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dir := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "router-policy.yml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
}

func startWithEnv(t *testing.T, rt string, policy string, extra map[string]string, mutate func(*Config)) (execEnv []string, out string) {
	t.Helper()
	home, _, _ := newFakeProject(t, "aliasapp")
	if policy != "" {
		writeStartPolicy(t, home, policy)
	}
	env := map[string]string{"HOME": home, "PATH": os.Getenv("PATH")}
	for k, v := range extra {
		env[k] = v
	}
	var stdout bytes.Buffer
	called := false
	cfg := Config{
		Name: "aliasapp", HomeDir: home, Runtime: rt, NoAgents: true,
		Now:    time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
		Writer: &stdout, ErrWriter: &bytes.Buffer{}, Env: env,
		ExecFn: func(_ string, _ []string, e []string) error {
			called = true
			execEnv = e
			return nil
		},
		ConsoleProbeFn: func(string) bool { return false },
	}
	if mutate != nil {
		mutate(&cfg)
	}
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called && !cfg.PrintEnv {
		t.Fatal("ExecFn was not called")
	}
	return execEnv, stdout.String()
}

func envHas(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func hasAliasName(env []string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, "ANTHROPIC_DEFAULT_") || strings.HasPrefix(e, "CLAUDE_CODE_SUBAGENT_MODEL=") {
			return true
		}
	}
	return false
}

func TestStart_ChildEnvCarriesTheClassAliases(t *testing.T) {
	env, _ := startWithEnv(t, "claude", "gateway_classes: {subagent: haiku, opus: claude-opus-4-1-20250805}\n", nil, nil)
	for _, want := range []string{"CLAUDE_CODE_SUBAGENT_MODEL=haiku", "ANTHROPIC_DEFAULT_OPUS_MODEL=claude-opus-4-1-20250805"} {
		if !envHas(env, want) {
			t.Errorf("child env lacks %s: %v", want, env)
		}
	}
}

func TestStart_NoPolicyMeansNoAliases(t *testing.T) {
	env, _ := startWithEnv(t, "claude", "", nil, nil)
	if hasAliasName(env) {
		t.Fatalf("no policy must set no alias: %v", env)
	}
}

func TestStart_OperatorEnvWinsAndRefusedKeyAppliesNothing(t *testing.T) {
	env, _ := startWithEnv(t, "claude", "gateway_classes: {subagent: haiku}\n",
		map[string]string{"CLAUDE_CODE_SUBAGENT_MODEL": "claude-sonnet-mine"}, nil)
	n := 0
	for _, e := range env {
		if strings.HasPrefix(e, "CLAUDE_CODE_SUBAGENT_MODEL=") {
			n++
			if e != "CLAUDE_CODE_SUBAGENT_MODEL=claude-sonnet-mine" {
				t.Errorf("the operator's value must win, got %s", e)
			}
		}
	}
	if n != 1 {
		t.Errorf("want exactly one entry, got %d: %v", n, env)
	}
	env, _ = startWithEnv(t, "claude", "gateway_classes: {subagent: gpt-6-astra}\n", nil, nil)
	if hasAliasName(env) {
		t.Fatalf("a refused key must set nothing: %v", env)
	}
}

func TestStart_AliasesAreForClaudeOnly(t *testing.T) {
	env, _ := startWithEnv(t, "codex", "gateway_classes: {subagent: haiku}\n", nil, nil)
	if hasAliasName(env) {
		t.Fatalf("codex must not get Claude Code variables: %v", env)
	}
}

func TestStart_PrintEnvShowsNamesAndValuesWithoutLaunching(t *testing.T) {
	env, out := startWithEnv(t, "claude", "gateway_classes: {subagent: haiku, opus: claude-opus-x}\n",
		map[string]string{"ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-mine"}, func(c *Config) { c.PrintEnv = true })
	if env != nil {
		t.Error("--print-env must not launch the runtime")
	}
	if !strings.Contains(out, "CLAUDE_CODE_SUBAGENT_MODEL=haiku") {
		t.Errorf("missing the subagent line: %q", out)
	}
	if strings.Contains(out, "ANTHROPIC_DEFAULT_OPUS_MODEL=claude-opus-x") || !strings.Contains(out, "ANTHROPIC_DEFAULT_OPUS_MODEL not set: your environment") {
		t.Errorf("an operator-overridden class must be reported, not printed as set: %q", out)
	}
	if strings.Contains(out, "claude-mine") {
		t.Errorf("the operator's own value must not be echoed: %q", out)
	}
	_, out = startWithEnv(t, "claude", "", nil, func(c *Config) { c.PrintEnv = true })
	if !strings.Contains(out, "no gateway_classes aliases are active") {
		t.Errorf("got %q", out)
	}
	_, out = startWithEnv(t, "codex", "gateway_classes: {subagent: haiku}\n", nil, func(c *Config) { c.PrintEnv = true })
	if !strings.Contains(out, "claude runtime only") {
		t.Errorf("got %q", out)
	}
}

// K-165: with HOME empty the launcher fills HomeDir with a shared temp path. The
// policy there must not be read; the feature is off, as in dispatch. A scratch
// directory stands in for /tmp and holds a valid policy that would set aliases.
func TestGatewayStateDirEmptyHomeIsOff(t *testing.T) {
	fake := t.TempDir()
	writeStartPolicy(t, fake, "gateway_classes: {subagent: haiku}\n")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	cfg := Config{HomeDir: fake}
	if got := gatewayStateDir(cfg, nil); got != "" {
		t.Fatalf("gatewayStateDir with HOME empty = %q, want \"\"", got)
	}
	var out bytes.Buffer
	printGatewayEnv(&out, "claude", gatewayStateDir(cfg, nil), map[string]string{})
	if strings.Contains(out.String(), "=") || !strings.Contains(out.String(), "no gateway_classes aliases are active") {
		t.Fatalf("aliases applied from an untrusted home:\n%s", out.String())
	}
}

func TestGatewayStateDirInjectedEmptyHomeIsOff(t *testing.T) {
	if got := gatewayStateDir(Config{Env: map[string]string{}}, map[string]string{}); got != "" {
		t.Fatalf("got %q, want \"\"", got)
	}
}

func TestGatewayStateDirProductionUsesTrustedDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	other := t.TempDir()
	want := filepath.Join(home, ".yakos-state")
	if got := gatewayStateDir(Config{HomeDir: other}, nil); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
