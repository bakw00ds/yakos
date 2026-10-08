package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// clearCallerMarkers empties every environment marker callerIdentity reads, so a
// test does not inherit the agent context it runs in.
func clearCallerMarkers(t *testing.T) {
	t.Helper()
	for _, k := range []string{"YAKOS_AGENT_TYPE", "CLAUDE_SESSION_ID", "CLAUDE_CODE_SESSION_ID", "CLAUDECODE", "CLAUDE_PROJECT_DIR"} {
		t.Setenv(k, "")
	}
}

// K-176: the config_changed line names the calling agent and session, and
// whether the caller looked like an agent, not only the OS user.
func TestConfigChangedRecordsAgentAndSession(t *testing.T) {
	clearCallerMarkers(t)
	t.Setenv("YAKOS_AGENT_TYPE", "backend")
	t.Setenv("CLAUDE_SESSION_ID", "sess-176")
	r := newModelsRig(t)
	if code, out, errs := r.do("disable", "gpt-5.5"); code != 0 {
		t.Fatalf("exit %d out=%q err=%q", code, out, errs)
	}
	lines := configChangedLines(r.stateDir)
	if len(lines) != 1 {
		t.Fatalf("lines = %v", lines)
	}
	l := lines[0]
	if l["actor"] != "agent" || l["agent"] != "backend" || l["session_id"] != "sess-176" || l["operator_id"] == "" {
		t.Errorf("identity not recorded: %v", l)
	}
}

func TestConfigChangedForARouterPolicyWriteCarriesTheIdentityToo(t *testing.T) {
	clearCallerMarkers(t)
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-rp")
	env, _ := polEnv(t, "")
	rules := filepath.Join(t.TempDir(), "rules.yml")
	if err := os.WriteFile(rules, []byte("- match: {class: chat}\n  action: {runtime: claude, model: sonnet}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errs := runPolicy(t, env, "set", "--rules-file", rules); code != 0 {
		t.Fatalf("exit %d out=%q err=%q", code, out, errs)
	}
	lines := configChangedLines(lastStateDir)
	if len(lines) != 1 || lines[0]["actor"] != "agent" || lines[0]["session_id"] != "sess-rp" {
		t.Errorf("audit = %v", lines)
	}
}

func TestConfigChangedFromAPlainShellIsTheOperator(t *testing.T) {
	clearCallerMarkers(t)
	r := newModelsRig(t)
	if code, _, errs := r.do("disable", "gpt-5.5"); code != 0 {
		t.Fatalf("exit %d err=%q", code, errs)
	}
	l := configChangedLines(r.stateDir)[0]
	if l["actor"] != "operator" {
		t.Errorf("actor = %v", l["actor"])
	}
	if _, ok := l["agent"]; ok {
		t.Errorf("agent recorded for a plain shell: %v", l)
	}
	if _, ok := l["session_id"]; ok {
		t.Errorf("session recorded for a plain shell: %v", l)
	}
}

// A hostile session or agent name from the environment never reaches the log.
func TestConfigChangedDropsHostileIdentity(t *testing.T) {
	clearCallerMarkers(t)
	t.Setenv("YAKOS_AGENT_TYPE", "a\nb")
	t.Setenv("CLAUDE_SESSION_ID", "x\",\"actor\":\"operator")
	r := newModelsRig(t)
	if code, _, errs := r.do("disable", "gpt-5.5"); code != 0 {
		t.Fatalf("exit %d err=%q", code, errs)
	}
	b, _ := os.ReadFile(statepath.DispatchLogIn(r.stateDir))
	if strings.Count(strings.TrimSpace(string(b)), "\n") != 0 {
		t.Fatalf("the log gained a line: %q", b)
	}
	l := configChangedLines(r.stateDir)[0]
	if l["actor"] != "agent" || l["agent"] != nil || l["session_id"] != nil {
		t.Errorf("hostile identity leaked: %v", l)
	}
}
