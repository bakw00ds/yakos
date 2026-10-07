package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aliasHome returns a clean HOME holding a router policy with body (mode 600),
// or none when body is empty. Every test gets its own home, so the per-process
// load cache never leaks between tests.
func aliasHome(t *testing.T, body string) string {
	t.Helper()
	if os.PathSeparator == '\\' {
		t.Skip("POSIX modes")
	}
	home := useEmptyHome(t)
	for _, k := range []string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_DEFAULT_FABLE_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL", "YAKOS_MODEL_OVERRIDE", "YAKOS_DISPATCH_ENV_PASSTHROUGH",
	} {
		unsetenv(t, k)
	}
	if body != "" {
		writeAliasPolicy(t, home, body, 0o600)
	}
	return home
}

func writeAliasPolicy(t *testing.T, home, body string, mode os.FileMode) {
	t.Helper()
	dir := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "router-policy.yml")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

// envDiff returns the entries in got that are not in base, in order.
func envDiff(base, got []string) []string {
	have := map[string]bool{}
	for _, kv := range base {
		have[kv] = true
	}
	var out []string
	for _, kv := range got {
		// Each case runs in its own temporary home.
		if strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "USERPROFILE=") {
			continue
		}
		if !have[kv] {
			out = append(out, kv)
		}
	}
	return out
}

const subagentPolicy = "gateway_classes: {subagent: haiku}\n"

type aliasBuilder struct {
	name  string
	build func() []string
}

func claudeBuilders() []aliasBuilder {
	ctx := context.Background()
	a := &ClaudeAdapter{}
	return []aliasBuilder{
		{"buildEnv", func() []string { return buildEnv(DispatchRequest{}) }},
		{"ExecCmd", func() []string {
			return a.ExecCmd(ctx, DispatchRequest{Project: "/p", AgentName: "x", AgentJSON: "{}"}).Env
		}},
		{"buildEnvChat", func() []string { return buildEnvChat(ChatDispatchRequest{}) }},
		{"ChatExecCmd", func() []string { return a.ChatExecCmd(ctx, ChatDispatchRequest{Project: "/p", UserText: "hi"}).Env }},
		{"buildEnvInteractive", func() []string { return buildEnvInteractive("") }},
		{"InteractiveExecCmd", func() []string { return InteractiveExecCmd("/p", "sys", "", "").Env }},
	}
}

// Byte-exact env diff per builder: without a policy the env is the filtered
// parent env; with one it gains exactly the policy's lines and nothing else.
func TestGatewayAliases_EveryClaudeBuilderGainsExactlyThePolicyLines(t *testing.T) {
	const all = `gateway_classes:
  subagent: haiku
  opus: claude-opus-4-1-20250805
  haiku: claude-haiku-4-5-20251001
`
	want := []string{
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=claude-haiku-4-5-20251001",
		"ANTHROPIC_DEFAULT_OPUS_MODEL=claude-opus-4-1-20250805",
		"CLAUDE_CODE_SUBAGENT_MODEL=haiku",
	}
	for _, b := range claudeBuilders() {
		t.Run(b.name, func(t *testing.T) {
			aliasHome(t, "")
			none := b.build()
			for _, kv := range none {
				if strings.HasPrefix(kv, "ANTHROPIC_DEFAULT_") || strings.HasPrefix(kv, "CLAUDE_CODE_SUBAGENT_MODEL") {
					t.Fatalf("no policy must set no alias, got %q", kv)
				}
			}
			aliasHome(t, all)
			got := envDiff(none, b.build())
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("env diff\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// The operator's own variable wins, in every builder, and appears once.
func TestGatewayAliases_OperatorEnvWins(t *testing.T) {
	for _, b := range claudeBuilders() {
		t.Run(b.name, func(t *testing.T) {
			aliasHome(t, "gateway_classes: {subagent: haiku, opus: claude-opus-x}\n")
			t.Setenv("CLAUDE_CODE_SUBAGENT_MODEL", "claude-sonnet-mine")
			env := b.build()
			if got := envValues(env, "CLAUDE_CODE_SUBAGENT_MODEL"); len(got) != 1 || got[0] != "claude-sonnet-mine" {
				t.Errorf("CLAUDE_CODE_SUBAGENT_MODEL = %v, want only the operator's value", got)
			}
			if got := envValues(env, "ANTHROPIC_DEFAULT_OPUS_MODEL"); len(got) != 1 || got[0] != "claude-opus-x" {
				t.Errorf("a class the operator did not set must still apply, got %v", got)
			}
		})
	}
}

func TestGatewayAliases_ResolveReportsWhatItSetAndWhatTheOperatorOverrode(t *testing.T) {
	home := aliasHome(t, "gateway_classes: {subagent: haiku, opus: claude-opus-x}\n")
	state := filepath.Join(home, ".yakos-state")
	g := ResolveGatewayAliases(state, []string{"ANTHROPIC_DEFAULT_OPUS_MODEL=claude-mine"})
	if len(g.Set) != 1 || g.Set[0].Class != "subagent" || len(g.Overridden) != 1 || g.Overridden[0].Class != "opus" {
		t.Fatalf("got %+v", g)
	}
	if g.SHA != g.Set.SHA() || g.SHA == "" {
		t.Errorf("sha must be the digest of what was set, got %q", g.SHA)
	}
	// An empty operator value is not an override.
	g = ResolveGatewayAliases(state, []string{"ANTHROPIC_DEFAULT_OPUS_MODEL="})
	if len(g.Set) != 2 {
		t.Errorf("an empty variable is unset, got %+v", g)
	}
	// Everything overridden: nothing set, so no sha for the ledger.
	g = ResolveGatewayAliases(state, []string{"anthropic_default_opus_model=a", "CLAUDE_CODE_SUBAGENT_MODEL=b"})
	if len(g.Set) != 0 || g.SHA != "" || len(g.Env()) != 0 {
		t.Errorf("got %+v", g)
	}
}

// Cache stability: the table is read once per process, so the env never varies
// between turns even if the file changes under a running daemon.
func TestGatewayAliases_LoadedOncePerProcess(t *testing.T) {
	home := aliasHome(t, subagentPolicy)
	first := buildEnvChat(ChatDispatchRequest{})
	writeAliasPolicy(t, home, "gateway_classes: {subagent: sonnet, opus: claude-opus-x}\n", 0o600)
	second := buildEnvChat(ChatDispatchRequest{})
	if strings.Join(first, "\n") != strings.Join(second, "\n") {
		t.Fatalf("env changed between turns:\n%v\n%v", envDiff(first, second), envDiff(second, first))
	}
	if got := envValues(second, "CLAUDE_CODE_SUBAGENT_MODEL"); len(got) != 1 || got[0] != "haiku" {
		t.Fatalf("got %v", got)
	}
}

// A refused policy or key fails closed to no aliasing and says why once,
// without a path.
func TestGatewayAliases_RefusedFailsClosedWithAPathFreeNote(t *testing.T) {
	cases := map[string]func(t *testing.T) string{
		"non-Claude id": func(t *testing.T) string { return aliasHome(t, "gateway_classes: {subagent: gpt-6-astra}\n") },
		"unknown class": func(t *testing.T) string { return aliasHome(t, "gateway_classes: {compaction: haiku}\n") },
		"untrusted file": func(t *testing.T) string {
			h := aliasHome(t, "")
			writeAliasPolicy(t, h, subagentPolicy, 0o666)
			return h
		},
		"unparsable file": func(t *testing.T) string { return aliasHome(t, "gateway_classes: [unterminated\n") },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			home := setup(t)
			notes := captureSandboxNotes(t)
			for i := 0; i < 3; i++ {
				env := buildEnv(DispatchRequest{})
				for _, kv := range env {
					if strings.HasPrefix(kv, "ANTHROPIC_DEFAULT_") || strings.HasPrefix(kv, "CLAUDE_CODE_SUBAGENT_MODEL") {
						t.Fatalf("a refused policy must set no alias, got %q", kv)
					}
				}
			}
			out := notes.String()
			if strings.Count(out, "gateway_classes") != 1 {
				t.Errorf("want exactly one note, got %q", out)
			}
			if strings.Contains(out, home) || strings.Contains(out, "gpt-6") || strings.Contains(out, "unterminated") {
				t.Errorf("note leaks a path or a value: %q", out)
			}
		})
	}
}

// The allowlist passes the new names for claude only.
func TestGatewayAliases_AllowlistPassesTheNamesForClaudeOnly(t *testing.T) {
	base := []string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL=a", "ANTHROPIC_DEFAULT_SONNET_MODEL=b", "ANTHROPIC_DEFAULT_HAIKU_MODEL=c",
		"ANTHROPIC_DEFAULT_FABLE_MODEL=d", "CLAUDE_CODE_SUBAGENT_MODEL=e", "PATH=/bin",
	}
	if got := FilterEnvFor("claude", base); len(got) != len(base) {
		t.Errorf("claude must keep all %d, got %v", len(base), got)
	}
	got := FilterEnvFor("codex", base)
	if len(got) != 1 || got[0] != "PATH=/bin" {
		t.Errorf("codex must not receive Claude Code model variables, got %v", got)
	}
}

// The K-137 sidecar strip drops OAuth material and leaves the alias names.
func TestGatewayAliases_SDKSidecarStripLeavesTheNames(t *testing.T) {
	base := []string{
		"ANTHROPIC_API_KEY=sk-ant-api03-fake", "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-fake",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=claude-haiku-4-5-20251001", "CLAUDE_CODE_SUBAGENT_MODEL=haiku",
	}
	got, err := SDKSidecarEnv(base)
	if err != nil {
		t.Fatal(err)
	}
	if v := envValues(got, "ANTHROPIC_DEFAULT_HAIKU_MODEL"); len(v) != 1 || v[0] != "claude-haiku-4-5-20251001" {
		t.Errorf("haiku alias lost: %v", got)
	}
	if v := envValues(got, "CLAUDE_CODE_SUBAGENT_MODEL"); len(v) != 1 || v[0] != "haiku" {
		t.Errorf("subagent alias lost: %v", got)
	}
	if v := envValues(got, "CLAUDE_CODE_OAUTH_TOKEN"); len(v) != 0 {
		t.Errorf("OAuth token must be stripped: %v", got)
	}
}

// Non-Claude adapters never get the aliases.
func TestGatewayAliases_NotAppliedToOtherHarnesses(t *testing.T) {
	aliasHome(t, subagentPolicy)
	for name, env := range map[string][]string{"codex": buildEnvCodex(DispatchRequest{}), "agy": buildEnvAgy(DispatchRequest{})} {
		if v := envValues(env, "CLAUDE_CODE_SUBAGENT_MODEL"); len(v) != 0 {
			t.Errorf("%s got %v", name, v)
		}
	}
}

func TestClaudeGatewayAliases_MatchesTheBuilders(t *testing.T) {
	aliasHome(t, "gateway_classes: {subagent: haiku}\n")
	g := ClaudeGatewayAliases()
	if len(g.Set) != 1 || g.SHA == "" || strings.Join(g.Env(), ",") != "CLAUDE_CODE_SUBAGENT_MODEL=haiku" {
		t.Fatalf("got %+v", g)
	}
	t.Setenv("CLAUDE_CODE_SUBAGENT_MODEL", "mine")
	if g := ClaudeGatewayAliases(); len(g.Set) != 0 || g.SHA != "" {
		t.Fatalf("operator env must win in the ledger view too: %+v", g)
	}
}
