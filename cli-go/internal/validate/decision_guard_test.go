package validate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func agentWith(fm string) string {
	return "---\nid: tester\nrole: specialist\n" + fm + "---\n\n## Purpose\n\nx\n"
}

func runTree(t *testing.T, base string) (*Result, string) {
	t.Helper()
	var buf bytes.Buffer
	r := &Result{}
	validateTree(Config{YakosRoot: base, Writer: &buf}, r, &buf, "framework", base)
	return r, buf.String()
}

func TestDecisionGuards_RuntimeJevIsHardErrorNamingAgent(t *testing.T) {
	for name, fm := range map[string]string{
		"runtime":  "runtime: jev\n",
		"fallback": "runtime: claude\nruntime-fallback: [codex, jev]\n",
		"upper":    "runtime: JEV\n",
	} {
		base, _ := makeTree(t)
		makeAgentFile(t, filepath.Join(base, "agents"), "tester.md", agentWith(fm))
		r, out := runTree(t, base)
		if r.Errors == 0 || !strings.Contains(out, "agent tester") || !strings.Contains(out, "not a runtime") || !strings.Contains(out, "ADR-0009") {
			t.Errorf("%s: errors=%d\n%s", name, r.Errors, out)
		}
	}
}

func TestDecisionGuards_WriteToolsPlusProviderIsError(t *testing.T) {
	base, _ := makeTree(t)
	makeAgentFile(t, filepath.Join(base, "agents"), "tester.md", agentWith("decision-provider: mock\ntools: Read, Bash\n"))
	r, out := runTree(t, base)
	if r.Errors == 0 || !strings.Contains(out, "not provably read-only") {
		t.Fatalf("errors=%d\n%s", r.Errors, out)
	}
	// Read-only agent referencing a provider is not an error.
	base, _ = makeTree(t)
	makeAgentFile(t, filepath.Join(base, "agents"), "tester.md", agentWith("decision-provider: mock\ntools: Read, Grep\n"))
	if r, out := runTree(t, base); r.Errors != 0 {
		t.Fatalf("read-only reference must pass:\n%s", out)
	}
}

func TestDecisionGuards_CleanTreeAddsNoOutput(t *testing.T) {
	base, _ := makeTree(t)
	makeAgentFile(t, filepath.Join(base, "agents"), "tester.md", validAgentBody())
	_, with := runTree(t, base)
	var buf bytes.Buffer
	checkDecisionGuards(Config{}, &Result{}, &buf, base, nil)
	if buf.Len() != 0 {
		t.Fatalf("clean tree must emit nothing (bash parity), got %q", buf.String())
	}
	if strings.Contains(with, "decision") {
		t.Errorf("unexpected decision output:\n%s", with)
	}
}

const validDecisionYAML = `schema_id: demo@1
version: 1
surface: demo
model: jev-1.13.0
may_block: false
max_state_bytes: 2048
state_fields: [tool]
questions:
  q: {type: noul, instructions: "In scope."}
`

func TestDecisionGuards_QuestionSetValidation(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"valid":       {validDecisionYAML, ""},
		"alias model": {strings.Replace(validDecisionYAML, "jev-1.13.0", "jev-latest", 1), "alias or unpinned"},
		"bad type":    {strings.Replace(validDecisionYAML, "noul", "text", 1), "must be noul, choice, or score"},
		"may_block":   {strings.Replace(validDecisionYAML, "may_block: false", "may_block: true", 1), "promotion"},
		"not yaml":    {"::: {", "yaml"},
	}
	for name, c := range cases {
		t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir()) // no promotions on record
		base, _ := makeTree(t)
		if err := os.MkdirAll(filepath.Join(base, "decisions"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "decisions", "demo.yaml"), []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		makeAgentFile(t, filepath.Join(base, "agents"), "tester.md", validAgentBody())
		r, out := runTree(t, base)
		if c.want == "" {
			if r.Errors != 0 {
				t.Errorf("%s: unexpected errors:\n%s", name, out)
			}
			continue
		}
		if r.Errors == 0 || !strings.Contains(out, c.want) || !strings.Contains(out, "demo.yaml") {
			t.Errorf("%s: errors=%d want %q\n%s", name, r.Errors, c.want, out)
		}
	}
}
