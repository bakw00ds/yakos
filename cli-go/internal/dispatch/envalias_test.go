package dispatch

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/routerpolicy"
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

// route_reason is the router's and always set; the stamp appends to it.
// policy_sha is the sha of the trusted policy file, the same value the router
// computes (routerpolicy.Load).
func TestRun_Ledger_EnvAliasRowComposesReasonAndFileSHA(t *testing.T) {
	fakeRuntimeBin(t, "claude", "claude-stream-json-oneshot-SYNTHETIC.ndjson", "", 0) // sets its own HOME
	const policy = "gateway_classes: {subagent: haiku}\n"
	envAliasHome(t, policy)
	_, ev, _ := runWith(t, "claude", nil)
	sum := sha256.Sum256([]byte(policy))
	reason, _ := ev["route_reason"].(string)
	if !strings.HasPrefix(reason, "default chain: ") || !strings.HasSuffix(reason, "; env-alias") {
		t.Errorf("route_reason = %q, want the router's reason with \"; env-alias\" appended", reason)
	}
	assertField(t, ev, "route_rule", "R0")
	assertField(t, ev, "policy_sha", hex.EncodeToString(sum[:]))
	home, _ := os.UserHomeDir()
	if want := routerpolicy.FileSHA(filepath.Join(home, ".yakos-state")); want != hex.EncodeToString(sum[:]) {
		t.Errorf("FileSHA = %q, want %q", want, hex.EncodeToString(sum[:]))
	}
}

func TestRun_Ledger_NoEnvAliasMeansNoEnvAliasStamp(t *testing.T) {
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
			if r, _ := ev["route_reason"].(string); r == "" || strings.Contains(r, "env-alias") {
				t.Errorf("route_reason must be the router's alone: %q", r)
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
	if r, _ := ev["route_reason"].(string); strings.Contains(r, "env-alias") {
		t.Errorf("route_reason = %q on codex", r)
	}
}

// The router's route record is kept; the stamp only appends to the reason and
// never replaces a policy_sha the router set.
func TestStampEnvAlias_ComposesOntoAnExistingRouteRecord(t *testing.T) {
	envAliasHome(t, "gateway_classes: {subagent: haiku}\n")
	req := Request{RouteReason: "rule R1 matched", PolicySHA: "ab"}
	stampEnvAlias(&req, "claude")
	stampEnvAlias(&req, "claude") // idempotent
	if req.RouteReason != "rule R1 matched; env-alias" || req.PolicySHA != "ab" {
		t.Fatalf("got %+v", req)
	}
	empty := Request{}
	stampEnvAlias(&empty, "claude")
	if empty.RouteReason != "env-alias" || empty.PolicySHA == "" {
		t.Fatalf("empty reason: %+v", empty)
	}
}
