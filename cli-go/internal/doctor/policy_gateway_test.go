package doctor

import (
	"strings"
	"testing"
)

func TestCheckPolicy_ListsActiveGatewayClasses(t *testing.T) {
	skipWithoutPosixModes(t)
	f := newPolicyFixture(t)
	writePolicy(t, f.home, "gateway_classes: {subagent: haiku, haiku: claude-haiku-4-5-20251001}\n", 0o600)
	got, ok := byID(f.check())["router-policy-gateway-classes"]
	if !ok {
		t.Fatal("want router-policy-gateway-classes")
	}
	requireOneLine(t, got)
	if got.Severity != PolicyLow {
		t.Errorf("severity = %s, want low", got.Severity)
	}
	for _, w := range []string{"haiku (ANTHROPIC_DEFAULT_HAIKU_MODEL=claude-haiku-4-5-20251001)", "subagent (CLAUDE_CODE_SUBAGENT_MODEL=haiku)", "~/.yakos-state/router-policy.yml"} {
		if !strings.Contains(got.Message, w) {
			t.Errorf("missing %q in %q", w, got.Message)
		}
	}
	if strings.Contains(got.Message, f.home) {
		t.Errorf("the home path must stay out: %q", got.Message)
	}
}

func TestCheckPolicy_GatewayClassesSaysTheOperatorEnvWins(t *testing.T) {
	skipWithoutPosixModes(t)
	f := newPolicyFixture(t)
	writePolicy(t, f.home, "gateway_classes: {subagent: haiku, opus: claude-opus-x}\n", 0o600)
	f.env["CLAUDE_CODE_SUBAGENT_MODEL"] = "claude-sonnet-secretish"
	got := byID(f.check())["router-policy-gateway-classes"]
	if !strings.Contains(got.Message, "your own environment wins for: subagent (CLAUDE_CODE_SUBAGENT_MODEL)") ||
		!strings.Contains(got.Message, "opus (ANTHROPIC_DEFAULT_OPUS_MODEL=claude-opus-x)") {
		t.Errorf("got %q", got.Message)
	}
	if strings.Contains(got.Message, "secretish") {
		t.Errorf("the operator's value must not be printed: %q", got.Message)
	}
	f.env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "x"
	if m := byID(f.check())["router-policy-gateway-classes"].Message; !strings.Contains(m, "sets nothing") {
		t.Errorf("got %q", m)
	}
}

func TestCheckPolicy_RefusedGatewayClassesKeyIsReported(t *testing.T) {
	skipWithoutPosixModes(t)
	f := newPolicyFixture(t)
	writePolicy(t, f.home, "gateway_classes: {subagent: gpt-6-astra}\n", 0o600)
	fs := byID(f.check())
	got, ok := fs["router-policy-gateway-classes-refused"]
	if !ok || got.Severity != PolicyMedium || strings.Contains(got.Message, "gpt-6") {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	if _, ok := fs["router-policy-gateway-classes"]; ok {
		t.Error("a refused key lists no active classes")
	}
}

func TestCheckPolicy_GatewayClassesDoNotHideTheSandboxFinding(t *testing.T) {
	skipWithoutPosixModes(t)
	f := newPolicyFixture(t)
	writePolicy(t, f.home, "allow_unsandboxed_runtimes: [codex]\ngateway_classes: {subagent: haiku}\n", 0o600)
	fs := byID(f.check())
	if _, ok := fs["router-policy-unsandboxed"]; !ok {
		t.Error("lost the sandbox finding")
	}
	if _, ok := fs["router-policy-gateway-classes"]; !ok {
		t.Error("lost the class listing")
	}
}
