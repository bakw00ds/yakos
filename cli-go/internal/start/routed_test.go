package start

import (
	"bytes"
	"sort"
	"strings"
	"testing"
)

const testGWToken = "feedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedface"

func withToken(c *Config) { c.Routed = true; c.RoutedToken = testGWToken }

// The --routed child environment is the unrouted one plus the base URL, the hint
// switch and the gateway token, minus the operator's ANTHROPIC_API_KEY.
func TestRouted_ChildEnvGolden(t *testing.T) {
	extra := map[string]string{"ANTHROPIC_API_KEY": "sk-ant-api03-x", "ANTHROPIC_AUTH_TOKEN": "operator-own-token"}
	plain, _ := startWithEnv(t, "claude", "", extra, nil)
	routed, out := startWithEnv(t, "claude", "", extra, withToken)

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
	want := []string{"ANTHROPIC_AUTH_TOKEN=" + testGWToken, "ANTHROPIC_BASE_URL=http://127.0.0.1:7897", "CLAUDE_CODE_GATEWAY_HINT_HEADERS=1"}
	if strings.Join(added, "|") != strings.Join(want, "|") {
		t.Errorf("routed adds %v, want %v", added, want)
	}
	removed := diff(routed, plain)
	wantRemoved := []string{"ANTHROPIC_API_KEY=sk-ant-api03-x", "ANTHROPIC_AUTH_TOKEN=operator-own-token"}
	if strings.Join(removed, "|") != strings.Join(wantRemoved, "|") {
		t.Errorf("routed removed %v, want %v", removed, wantRemoved)
	}
	for _, e := range routed {
		if strings.HasPrefix(e, "ANTHROPIC_API_KEY=") {
			t.Errorf("the operator key reached the routed child: %s", e)
		}
	}
	if strings.Contains(out, testGWToken) {
		t.Error("the gateway token was printed")
	}
}

func TestRouted_NeedsAGatewayToken(t *testing.T) {
	if _, _, err := applyRouted("claude", nil, ""); err == nil {
		t.Error("applyRouted accepted an empty gateway token")
	}
}

func TestRouted_ReplacesOperatorBaseURLAndSaysSo(t *testing.T) {
	var errw bytes.Buffer
	env, _ := startWithEnv(t, "claude", "", map[string]string{
		"ANTHROPIC_BASE_URL": "https://gateway.corp.example", "CLAUDE_CODE_GATEWAY_HINT_HEADERS": "0",
	}, func(c *Config) { withToken(c); c.ErrWriter = &errw })
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
	if _, _, err := applyRouted("codex", nil, testGWToken); err == nil {
		t.Error("--routed accepted for codex")
	}
}
