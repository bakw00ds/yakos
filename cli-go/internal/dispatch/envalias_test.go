package dispatch

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func envAliasHome(t *testing.T, policy string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	if policy == "" {
		return
	}
	dir := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "router-policy.yml")
	if err := os.WriteFile(p, []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRun_Ledger_EnvAliasRowCarriesReasonAndTableSHA(t *testing.T) {
	fakeRuntimeBin(t, "claude", "claude-stream-json-oneshot-SYNTHETIC.ndjson", "", 0) // sets its own HOME
	envAliasHome(t, "gateway_classes: {subagent: haiku}\n")
	_, ev, _ := runWith(t, "claude", nil)
	sum := sha256.Sum256([]byte("CLAUDE_CODE_SUBAGENT_MODEL=haiku\n"))
	assertField(t, ev, "route_reason", "env-alias")
	assertField(t, ev, "policy_sha", hex.EncodeToString(sum[:]))
}

func TestRun_Ledger_NoEnvAliasMeansNoRouteFields(t *testing.T) {
	cases := map[string]func(t *testing.T){
		"no policy": func(t *testing.T) { envAliasHome(t, "") },
		"operator env wins": func(t *testing.T) {
			envAliasHome(t, "gateway_classes: {subagent: haiku}\n")
			t.Setenv("CLAUDE_CODE_SUBAGENT_MODEL", "mine")
		},
		"refused key": func(t *testing.T) { envAliasHome(t, "gateway_classes: {subagent: gpt-6-astra}\n") },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			fakeRuntimeBin(t, "claude", "claude-stream-json-oneshot-SYNTHETIC.ndjson", "", 0)
			setup(t)
			_, ev, _ := runWith(t, "claude", nil)
			if _, ok := ev["route_reason"]; ok {
				t.Errorf("route_reason must be absent: %v", ev["route_reason"])
			}
			if _, ok := ev["policy_sha"]; ok {
				t.Errorf("policy_sha must be absent: %v", ev["policy_sha"])
			}
		})
	}
}

// Aliases are a Claude Code mechanism: a codex run is never stamped.
func TestRun_Ledger_EnvAliasNotStampedOnOtherHarnesses(t *testing.T) {
	fakeRuntimeBin(t, "codex", "codex-exec-json-0.154.0-ok.ndjson", "", 0)
	envAliasHome(t, "gateway_classes: {subagent: haiku}\n")
	_, ev, _ := runWith(t, "codex", nil)
	if _, ok := ev["route_reason"]; ok {
		t.Errorf("route_reason = %v on codex", ev["route_reason"])
	}
}

// A route record the router already wrote is kept.
func TestStampEnvAlias_KeepsAnExistingRouteRecord(t *testing.T) {
	envAliasHome(t, "gateway_classes: {subagent: haiku}\n")
	req := Request{RouteReason: "rule:cheap", PolicySHA: "ab"}
	stampEnvAlias(&req, "claude")
	if req.RouteReason != "rule:cheap" || req.PolicySHA != "ab" {
		t.Fatalf("overwritten: %+v", req)
	}
}
