package decision

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func shadowState(t *testing.T, policy string, mode os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	if policy != "" {
		p := filepath.Join(dir, PolicyFileName)
		if err := os.WriteFile(p, []byte(policy), 0o600); err != nil {
			t.Fatal(err)
		}
		if mode != 0 {
			if err := os.Chmod(p, mode); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func noEnv(string) string { return "" }

func projectWith(t *testing.T, yml string) string {
	t.Helper()
	d := t.TempDir()
	if yml != "" {
		if err := os.WriteFile(filepath.Join(d, ".yakos.yml"), []byte(yml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestResolveRoutingShadow(t *testing.T) {
	type tc struct {
		name    string
		policy  string
		mode    os.FileMode
		project string
		env     map[string]string
		want    bool
		detail  string
	}
	cases := []tc{
		{name: "default is off", want: false, detail: "off (default)"},
		{name: "policy false", policy: "routing_shadow: false\n", want: false, detail: "off (default)"},
		{name: "policy true", policy: "routing_shadow: true\n", want: true, detail: "on:"},
		{name: "a project file cannot turn it on", project: "decisions:\n  provider: jev\n  routing_shadow: true\n", want: false, detail: "off (default)"},
		{name: "project veto", policy: "routing_shadow: true\n", project: "decisions:\n  provider: none\n", want: false, detail: "opts out"},
		{name: "project surface off", policy: "routing_shadow: true\n", project: "decisions:\n  surfaces:\n    routing-tier:\n      mode: \"off\"\n", want: false, detail: "routing-tier surface off"},
		{name: "kill switch", policy: "routing_shadow: true\n", env: map[string]string{"YAKOS_DECISION_DISABLE": "1"}, want: false, detail: "YAKOS_DECISION_DISABLE=1"},
		{name: "unreadable project config fails closed", policy: "routing_shadow: true\n", project: "decisions: [unclosed\n", want: false, detail: "unreadable"},
		{name: "malformed policy fails closed", policy: "routing_shadow: [\n", want: false, detail: "unreadable"},
	}
	if runtime.GOOS != "windows" {
		cases = append(cases, tc{name: "group-writable policy is not trusted", policy: "routing_shadow: true\n", mode: 0o664, want: false, detail: "not trusted"})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			getenv := noEnv
			if c.env != nil {
				getenv = func(k string) string { return c.env[k] }
			}
			got := ResolveRoutingShadow(shadowState(t, c.policy, c.mode), projectWith(t, c.project), getenv)
			if got.Enabled != c.want || !strings.Contains(got.Detail, c.detail) {
				t.Errorf("Enabled=%v Detail=%q, want %v containing %q", got.Enabled, got.Detail, c.want, c.detail)
			}
			if !got.Enabled && got.Config.Provider != "" {
				t.Errorf("a disabled shadow carries a config: %+v", got.Config)
			}
		})
	}
}

// The policy key survives LoadPolicy only from the state directory.
func TestResolveRoutingShadow_PolicyCeilingsApply(t *testing.T) {
	st := shadowState(t, "routing_shadow: true\negress:\n  level: strict\nbudget:\n  max_calls_per_session: 7\n", 0)
	got := ResolveRoutingShadow(st, projectWith(t, "decisions:\n  budget:\n    max_calls_per_session: 9999\n  egress:\n    level: full\n"), noEnv)
	if !got.Enabled {
		t.Fatal(got.Detail)
	}
	if got.Config.Budget.MaxCallsPerSession != 7 || got.Config.Egress.Level != EgressStrict {
		t.Errorf("project loosened the ceiling: %+v", got.Config)
	}
}

func TestJevNoRetryMakesOneAttempt(t *testing.T) {
	j, rec := newServer(t, 429, 200)
	j.NoRetry = true
	if _, err := j.Decide(context.Background(), testRequest()); err == nil {
		t.Fatal("want the 429 error")
	}
	if n := int(atomic.LoadInt32(&rec.calls)); n != 1 {
		t.Errorf("%d attempts, want 1", n)
	}
}
