package runtime

import (
	"testing"

	"github.com/bakw00ds/yakos/internal/cost"
)

// TestBillingFor pins how a run's billing mode is read from the credentials the
// harness would inherit (K-136): an API credential of the harness's own provider
// means api, none means subscription, another provider's credential changes
// nothing, and an unknown runtime has no billing.
func TestBillingFor(t *testing.T) {
	cases := []struct {
		name    string
		runtime string
		env     []string
		want    string
	}{
		{"claude with no credential is a subscription", "claude", []string{"PATH=/bin", "HOME=/h"}, cost.BillingSubscription},
		{"claude with an API key", "claude", []string{"ANTHROPIC_API_KEY=k"}, cost.BillingAPI},
		{"claude with a gateway token", "claude", []string{"ANTHROPIC_AUTH_TOKEN=k"}, cost.BillingAPI},
		{"claude on Bedrock", "claude", []string{"CLAUDE_CODE_USE_BEDROCK=1"}, cost.BillingAPI},
		{"claude on Vertex", "claude", []string{"CLAUDE_CODE_USE_VERTEX=true"}, cost.BillingAPI},
		{"a deployment switch set to 0 is off", "claude", []string{"CLAUDE_CODE_USE_BEDROCK=0"}, cost.BillingSubscription},
		{"an empty key is not a key", "claude", []string{"ANTHROPIC_API_KEY="}, cost.BillingSubscription},
		{"claude ignores another provider's key", "claude", []string{"OPENAI_API_KEY=k", "GEMINI_API_KEY=k"}, cost.BillingSubscription},
		{"the SDK sidecar reads the same credentials", "claude-sdk", []string{"ANTHROPIC_API_KEY=k"}, cost.BillingAPI},
		{"codex with no credential", "codex", []string{"PATH=/bin"}, cost.BillingSubscription},
		{"codex with OPENAI_API_KEY", "codex", []string{"OPENAI_API_KEY=k"}, cost.BillingAPI},
		{"codex with CODEX_API_KEY", "codex", []string{"CODEX_API_KEY=k"}, cost.BillingAPI},
		{"codex ignores an Anthropic key", "codex", []string{"ANTHROPIC_API_KEY=k"}, cost.BillingSubscription},
		{"agy with GEMINI_API_KEY", "agy", []string{"GEMINI_API_KEY=k"}, cost.BillingAPI},
		{"agy with ANTIGRAVITY_API_KEY", "agy", []string{"ANTIGRAVITY_API_KEY=k"}, cost.BillingAPI},
		{"agy with no credential", "agy", nil, cost.BillingSubscription},
		{"a plugin runtime has no billing", "myplugin", []string{"ANTHROPIC_API_KEY=k"}, ""},
		{"key names match case-insensitively", "claude", []string{"anthropic_api_key=k"}, cost.BillingAPI},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := billingFromEnv(c.runtime, c.env); got != c.want {
				t.Fatalf("billingFromEnv(%q, %v) = %q, want %q", c.runtime, c.env, got, c.want)
			}
		})
	}
}

// BillingFor reads the process environment; it never returns a value derived from
// a credential, only one of the fixed constants.
func TestBillingFor_ReadsTheProcessEnvironmentAndReturnsAConstant(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "")
	t.Setenv("CLAUDE_CODE_USE_VERTEX", "")
	t.Setenv("CLAUDE_CODE_USE_FOUNDRY", "")
	if got := BillingFor("claude"); got != cost.BillingSubscription {
		t.Fatalf("no credential: %q", got)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-SENTINEL-do-not-leak")
	got := BillingFor("claude")
	if got != cost.BillingAPI {
		t.Fatalf("with a key: %q", got)
	}
	switch got {
	case cost.BillingAPI, cost.BillingSubscription, cost.BillingLocal, "":
	default:
		t.Fatalf("BillingFor returned %q, which is not one of the fixed constants", got)
	}
}

func TestSystemModelID(t *testing.T) {
	cases := []struct{ name, line, want string }{
		{"init line", `{"type":"system","subtype":"init","session_id":"s","model":"claude-opus-4-1-20250805"}`, "claude-opus-4-1-20250805"},
		{"system line without a model", `{"type":"system","subtype":"init"}`, ""},
		{"an assistant line is not a system line", `{"type":"assistant","message":{"model":"claude-x"}}`, ""},
		{"a model that is not an identifier", `{"type":"system","model":"x\ny; rm"}`, ""},
		{"an over-long model", `{"type":"system","model":"` + string(make([]byte, 0)) + `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`, ""},
		{"not json", `system model`, ""},
		{"empty", ``, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SystemModelID([]byte(c.line)); got != c.want {
				t.Fatalf("SystemModelID = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResultFailed(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{`{"type":"result","subtype":"success","is_error":false}`, false},
		{`{"type":"result","subtype":"success"}`, false},
		{`{"type":"result","subtype":"error_during_execution","is_error":true}`, true},
		{`{"type":"result","subtype":"error_max_turns"}`, true},
		{`{"type":"result","subtype":"success","is_error":true}`, true},
		{`{"type":"assistant","is_error":true}`, false}, // not a result line
		{`not json`, false},
	}
	for _, c := range cases {
		if got := ResultFailed([]byte(c.line)); got != c.want {
			t.Errorf("ResultFailed(%s) = %v, want %v", c.line, got, c.want)
		}
	}
}

// The streamed result frame reports the whole usage of the turn, cache tokens and
// duration included (K-136: tokens are the primary unit, and cache tokens are most
// of a claude turn). Before it, only input and output tokens were read.
func TestParseStreamLineWithUsage_ResultReportsCacheTokensAndDuration(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","duration_ms":4321,"total_cost_usd":0.0123,` +
		`"usage":{"input_tokens":120,"output_tokens":45,"cache_read_input_tokens":9000,"cache_creation_input_tokens":3000}}`)
	_, isResult, usd, u := ParseStreamLineWithUsage(line, map[int]struct{}{})
	if !isResult || usd != 0.0123 || u == nil {
		t.Fatalf("isResult=%v usd=%v usage=%v", isResult, usd, u)
	}
	want := cost.Usage{InputTokens: 120, OutputTokens: 45, CacheRead: 9000, CacheCreation: 3000, DurationMs: 4321, TotalCostUSD: 0.0123}
	if *u != want {
		t.Fatalf("usage = %+v, want %+v", *u, want)
	}
}
