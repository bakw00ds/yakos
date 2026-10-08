package start

import (
	"bytes"
	"sort"
	"strings"
	"testing"
)

// The --routed child environment is exactly the unrouted one plus two entries.
func TestRouted_ChildEnvGolden(t *testing.T) {
	extra := map[string]string{"ANTHROPIC_API_KEY": "sk-ant-api03-x"}
	plain, _ := startWithEnv(t, "claude", "", extra, nil)
	routed, _ := startWithEnv(t, "claude", "", extra, func(c *Config) { c.Routed = true })

	// HOME differs per launch (each gets its own scratch home); everything else is compared.
	diff := func(a, b []string) []string {
		have := map[string]bool{}
		for _, e := range a {
			have[e] = true
		}
		var out []string
		for _, e := range b {
			if !have[e] && !strings.HasPrefix(e, "HOME=") {
				out = append(out, e)
			}
		}
		sort.Strings(out)
		return out
	}
	added := diff(plain, routed)
	want := []string{"ANTHROPIC_BASE_URL=http://127.0.0.1:7897", "CLAUDE_CODE_GATEWAY_HINT_HEADERS=1"}
	if strings.Join(added, "|") != strings.Join(want, "|") {
		t.Errorf("routed adds %v, want %v", added, want)
	}
	if removed := diff(routed, plain); len(removed) != 0 {
		t.Errorf("routed removed %v", removed)
	}
	if len(routed) != len(plain)+2 {
		t.Errorf("env sizes: plain %d routed %d", len(plain), len(routed))
	}
}

func TestRouted_ReplacesOperatorBaseURLAndSaysSo(t *testing.T) {
	var errw bytes.Buffer
	env, _ := startWithEnv(t, "claude", "", map[string]string{
		"ANTHROPIC_BASE_URL": "https://gateway.corp.example", "CLAUDE_CODE_GATEWAY_HINT_HEADERS": "0",
	}, func(c *Config) { c.Routed = true; c.ErrWriter = &errw })
	var urls, hints int
	for _, e := range env {
		switch {
		case strings.HasPrefix(e, "ANTHROPIC_BASE_URL="):
			urls++
			if e != "ANTHROPIC_BASE_URL="+RoutedBaseURL {
				t.Errorf("base url %q", e)
			}
		case strings.HasPrefix(e, "CLAUDE_CODE_GATEWAY_HINT_HEADERS="):
			hints++
			if e != "CLAUDE_CODE_GATEWAY_HINT_HEADERS=1" {
				t.Errorf("hint %q", e)
			}
		}
	}
	if urls != 1 || hints != 1 {
		t.Errorf("duplicate entries: %d base urls, %d hints", urls, hints)
	}
	if !strings.Contains(errw.String(), "replaces the ANTHROPIC_BASE_URL") || strings.Contains(errw.String(), "corp.example") {
		t.Errorf("note %q", errw.String())
	}
}

func TestRouted_NotSetWithoutTheFlagAndRefusedForOtherRuntimes(t *testing.T) {
	env, _ := startWithEnv(t, "claude", "", map[string]string{"ANTHROPIC_BASE_URL": "https://mine.example"}, nil)
	if !envHas(env, "ANTHROPIC_BASE_URL=https://mine.example") || envHas(env, "CLAUDE_CODE_GATEWAY_HINT_HEADERS=1") {
		t.Errorf("unrouted launch touched the gateway env: %v", env)
	}
	if _, _, err := applyRouted("codex", nil); err == nil {
		t.Error("--routed accepted for codex")
	}
}
