package router

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/routerpolicy"
)

// writePolicy writes a policy file with mode into a fresh state dir.
func writePolicy(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	p := routerpolicy.Path(dir)
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return dir
}

const sixRules = `
rules:
  - match: {class: chat}
    action: {runtime: codex}
  - match: {domain: review}
    action: {runtime: codex, model: gpt-5.5, fallbacks: [claude]}
  - match: {agent: backend, class: chat}
    action: {runtime: claude, model: sonnet, fallbacks: [codex]}
  - match: {task_bytes_gt: 1000}
    action: {runtime: agy}
  - match: {tags: [bulk, cheap]}
    action: {runtime: agy, fallbacks: [codex, claude]}
  - action: {model: haiku}
    override_pins: true
`

// Rules are numbered R1..R6 in file order, and the first match wins.
func TestPolicy_RuleIDsAndFirstMatch(t *testing.T) {
	p := LoadPolicy(writePolicy(t, sixRules, 0o600))
	if len(p.Rules) != 6 || len(p.Warnings) != 0 {
		t.Fatalf("want 6 rules and no warnings, got %d, %v", len(p.Rules), p.Warnings)
	}
	cases := []struct {
		name string
		in   Input
		want string // "" = no rule
	}{
		{"R1 class chat", Input{Class: "chat", Agent: "x"}, "R1"},
		{"R1 outranks R3 on the same input", Input{Class: "chat", Agent: "backend"}, "R1"},
		{"R2 domain", Input{Class: "default", Domain: "review"}, "R2"},
		{"agent backend with class default misses R3, lands on the catch-all", Input{Class: "default", Agent: "backend"}, "R6"},
		{"R4 task size over", Input{Class: "default", TaskBytes: 1001}, "R4"},
		{"R4 task size equal does not match", Input{Class: "default", TaskBytes: 1000}, "R6"},
		{"R5 needs every tag", Input{Class: "default", Tags: []string{"bulk"}}, "R6"},
		{"R5 both tags", Input{Class: "default", Tags: []string{"cheap", "bulk"}}, "R5"},
		{"R6 catch-all", Input{Class: "default"}, "R6"},
	}
	for _, c := range cases {
		r, ok := p.Select(c.in)
		got := ""
		if ok {
			got = r.ID
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// R3 specifically: reachable only when R1 does not match, so test the rule alone.
func TestPolicy_R3AgentAndClass(t *testing.T) {
	p := LoadPolicy(writePolicy(t, "rules:\n  - {action: {model: haiku}, match: {class: x}}\n  - {action: {model: opus}, match: {class: x}}\n  - match: {agent: backend, class: chat}\n    action: {runtime: claude, model: sonnet, fallbacks: [codex]}\n", 0o600))
	if r, ok := p.Select(Input{Class: "chat", Agent: "backend"}); !ok || r.ID != "R3" || r.Action.Runtime != "claude" || r.Action.Model != "sonnet" || fmt.Sprint(r.Action.Fallbacks) != "[codex]" {
		t.Fatalf("got %+v ok=%v", r, ok)
	}
	if _, ok := p.Select(Input{Class: "chat", Agent: "other"}); ok {
		t.Error("a different agent must not match")
	}
	if _, ok := p.Select(Input{Class: "default", Agent: "backend"}); ok {
		t.Error("a different class must not match")
	}
}

func TestPolicy_NoFileNoRules(t *testing.T) {
	p := LoadPolicy(t.TempDir())
	if p.Active() || len(p.Warnings) != 0 || p.SHA != "" {
		t.Fatalf("a missing file is no policy: %+v", p)
	}
	if p := LoadPolicy(""); p.Active() || len(p.Warnings) != 0 {
		t.Fatalf("no state dir is no policy: %+v", p)
	}
}

func TestPolicy_SHAIsTheFileDigestAndOnlyWithRules(t *testing.T) {
	a := LoadPolicy(writePolicy(t, "rules:\n  - {match: {class: chat}, action: {runtime: codex}}\n", 0o600))
	b := LoadPolicy(writePolicy(t, "rules:\n  - {match: {class: chat}, action: {runtime: agy}}\n", 0o600))
	if len(a.SHA) != 64 || a.SHA == b.SHA {
		t.Fatalf("sha must be a 64-hex digest that follows the content: %q %q", a.SHA, b.SHA)
	}
	if only := LoadPolicy(writePolicy(t, "allow_unsandboxed_runtimes: [codex]\n", 0o600)); only.SHA != "" {
		t.Errorf("a file with no rules cites no policy sha, got %q", only.SHA)
	}
	if dropped := LoadPolicy(writePolicy(t, "rules:\n  - {action: {runtime: gemini}}\n", 0o600)); dropped.Active() || dropped.SHA != "" {
		t.Errorf("a policy whose every rule was dropped cites no policy sha, got %+v", dropped)
	}
}

// Trust: an untrusted file yields no rules and a warning that names no path.
func TestPolicy_UntrustedFileIsIgnoredWithPathFreeWarning(t *testing.T) {
	body := "rules:\n  - {match: {class: chat}, action: {runtime: codex}}\n"
	check := func(t *testing.T, dir string) {
		t.Helper()
		p := LoadPolicy(dir)
		if p.Active() {
			t.Fatalf("rules from an untrusted file must not apply: %+v", p.Rules)
		}
		if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "router policy ignored") {
			t.Fatalf("want one 'router policy ignored' warning, got %v", p.Warnings)
		}
		if strings.Contains(p.Warnings[0], dir) || strings.Contains(p.Warnings[0], os.TempDir()) || strings.Contains(p.Warnings[0], routerpolicy.FileName) {
			t.Errorf("the warning must not name a path: %q", p.Warnings[0])
		}
	}
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		real := filepath.Join(t.TempDir(), "real.yml")
		if err := os.WriteFile(real, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, routerpolicy.Path(dir)); err != nil {
			t.Skip("symlinks unavailable: ", err)
		}
		check(t, dir)
	})
	t.Run("group writable", func(t *testing.T) {
		skipWindows(t)
		check(t, writePolicy(t, body, 0o620))
	})
	t.Run("world writable", func(t *testing.T) {
		skipWindows(t)
		check(t, writePolicy(t, body, 0o666))
	})
	t.Run("another owner", func(t *testing.T) {
		// A second user cannot be made here; the loader's refusal is an
		// ErrUntrusted that carries the path, and the router must still strip it.
		dir := "/home/someone/.yakos-state"
		p := loadPolicyWith(dir, func(d string) (routerpolicy.File, error) {
			return routerpolicy.File{}, fmt.Errorf("%w: %s is owned by another user", routerpolicy.ErrUntrusted, routerpolicy.Path(d))
		})
		if p.Active() || len(p.Warnings) != 1 || strings.Contains(p.Warnings[0], "someone") || strings.Contains(p.Warnings[0], "yakos-state") {
			t.Fatalf("got %+v", p)
		}
	})
	t.Run("unreadable is also path-free", func(t *testing.T) {
		p := loadPolicyWith("/x", func(string) (routerpolicy.File, error) {
			return routerpolicy.File{}, errors.New("open /x/router-policy.yml: permission denied")
		})
		if p.Active() || len(p.Warnings) != 1 || strings.Contains(p.Warnings[0], "/x") {
			t.Fatalf("got %+v", p)
		}
	})
}

// A bad rule is dropped on its own; the good ones stay and take their own ids.
func TestPolicy_InvalidRulesAreDropped(t *testing.T) {
	cases := []struct{ name, rule string }{
		{"unknown runtime", "{action: {runtime: gemini}}"},
		{"runtime with shell", "{action: {runtime: 'codex; rm'}}"},
		{"model with a space", "{action: {model: 'gpt 5'}}"},
		{"model with a leading dash", "{action: {model: '--yolo'}}"},
		{"model over 64 bytes", "{action: {model: " + strings.Repeat("a", 65) + "}}"},
		{"foreign model on claude", "{action: {runtime: claude, model: gpt-5.5}}"},
		{"unknown fallback", "{action: {runtime: codex, fallbacks: [gemini]}}"},
		{"empty action", "{match: {class: chat}, action: {}}"},
		{"no action", "{match: {class: chat}}"},
		{"negative size", "{match: {task_bytes_gt: -1}, action: {runtime: codex}}"},
		{"bad class", "{match: {class: 'a b'}, action: {runtime: codex}}"},
		{"bad tag", "{match: {tags: ['x y']}, action: {runtime: codex}}"},
		{"mistyped field", "{match: {task_bytes_gt: lots}, action: {runtime: codex}}"},
		{"not a mapping", "just a string"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := LoadPolicy(writePolicy(t, "rules:\n  - "+c.rule+"\n  - {action: {runtime: codex}}\n", 0o600))
			if len(p.Rules) != 1 || p.Rules[0].ID != "R1" || p.Rules[0].Action.Runtime != "codex" {
				t.Fatalf("the bad rule must drop and the good one become R1: %+v", p.Rules)
			}
			if len(p.Warnings) != 1 || strings.Contains(p.Warnings[0], os.TempDir()) {
				t.Fatalf("want one path-free warning, got %v", p.Warnings)
			}
		})
	}
}

func TestPolicy_RulesBeyondTheCapAndNonListAreIgnored(t *testing.T) {
	var b strings.Builder
	b.WriteString("rules:\n")
	for i := 0; i < 9; i++ {
		b.WriteString("  - {action: {model: haiku}}\n")
	}
	p := LoadPolicy(writePolicy(t, b.String(), 0o600))
	if len(p.Rules) != MaxRules || len(p.Warnings) != 1 {
		t.Fatalf("want %d rules and a cap warning, got %d, %v", MaxRules, len(p.Rules), p.Warnings)
	}
	q := LoadPolicy(writePolicy(t, "rules: nope\n", 0o600))
	if q.Active() || len(q.Warnings) != 1 {
		t.Fatalf("a non-list rules key: %+v", q)
	}
}

// The reader is shared: a policy with rules still answers allow_unsandboxed_runtimes
// through the same File, and a rules key never breaks it.
func TestPolicy_SharesTheFileWithAllowUnsandboxed(t *testing.T) {
	dir := writePolicy(t, "allow_unsandboxed_runtimes: [codex]\nrules:\n  - {action: {runtime: agy}}\nfuture_key: 1\n", 0o600)
	if ok, err := routerpolicy.AllowsUnsandboxed(dir, "codex"); err != nil || !ok {
		t.Fatalf("allow list: %v %v", ok, err)
	}
	if p := LoadPolicy(dir); len(p.Rules) != 1 {
		t.Fatalf("rules: %+v", p)
	}
}
