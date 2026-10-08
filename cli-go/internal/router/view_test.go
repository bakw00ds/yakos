package router

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/routerpolicy"
)

func stateWithPolicy(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if content != "" {
		if err := os.WriteFile(routerpolicy.Path(dir), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestViewOfMissingFileIsEmptyNotNull(t *testing.T) {
	v := ViewOf(stateWithPolicy(t, ""))
	if v.Present || v.SHA != "" || v.Rules == nil || v.Pins == nil || v.AllowUnsandboxedRuntimes == nil || v.Warnings == nil || v.GatewayClasses == nil {
		t.Errorf("view = %+v; every list must be present", v)
	}
}

func TestViewOfShowsRulesPinsAndPrivilegedKeys(t *testing.T) {
	v := ViewOf(stateWithPolicy(t, "allow_unsandboxed_runtimes: [codex]\nhooks_endpoint: true\nopenai_endpoint: true\n"+
		"gateway_classes: {opus: claude-opus-5-5-high}\n"+
		"rules:\n  - {match: {agent: a}, action: {runtime: codex, model: gpt-5.5}, override_pins: true}\n  - {match: {class: chat}, action: {runtime: claude}}\n"))
	if !v.Present || len(v.SHA) != 64 || len(v.Rules) != 2 || v.Rules[1].ID != "R2" {
		t.Fatalf("view = %+v", v)
	}
	if len(v.Pins) != 1 || v.Pins[0].Agent != "a" || !v.HooksEndpoint || !v.OpenAIEndpoint || len(v.AllowUnsandboxedRuntimes) != 1 {
		t.Errorf("view = %+v", v)
	}
	if len(v.GatewayClasses) != 1 || v.GatewayClasses[0].Env != "ANTHROPIC_DEFAULT_OPUS_MODEL" {
		t.Errorf("gateway classes = %+v", v.GatewayClasses)
	}
}

func TestViewOfAnUntrustedFileIsEmptyWithAWarningAndNoPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a group/world-writable file cannot be fabricated with os.Chmod on Windows")
	}
	dir := stateWithPolicy(t, "hooks_endpoint: true\n")
	if err := os.Chmod(routerpolicy.Path(dir), 0o666); err != nil {
		t.Fatal(err)
	}
	v := ViewOf(dir)
	if v.Present || v.HooksEndpoint || len(v.Warnings) != 1 || strings.Contains(v.Warnings[0], dir) {
		t.Errorf("view = %+v", v)
	}
}

func TestCheckPolicyRefusesADroppedRule(t *testing.T) {
	good, _ := routerpolicy.Parse([]byte("rules:\n  - {match: {agent: a}, action: {runtime: claude}}\n"))
	bad, _ := routerpolicy.Parse([]byte("rules:\n  - {match: {agent: a}, action: {runtime: nosuch}}\n"))
	if err := CheckPolicy(good); err != nil {
		t.Errorf("good: %v", err)
	}
	if err := CheckPolicy(bad); err == nil {
		t.Error("a rule the router would drop was accepted")
	}
}

func TestSensitiveViewAndKnownClasses(t *testing.T) {
	if s := Sensitive(); s.Class != ClassSensitive || s.SecretPatterns == 0 || s.NeverPathPatterns == 0 {
		t.Errorf("sensitive = %+v", s)
	}
	got := strings.Join(KnownClasses(stateWithPolicy(t, "rules:\n  - {match: {class: chat}, action: {runtime: claude}}\n")), ",")
	if got != "chat,default,fable,haiku,opus,sonnet,subagent" {
		t.Errorf("classes = %s", got)
	}
}
