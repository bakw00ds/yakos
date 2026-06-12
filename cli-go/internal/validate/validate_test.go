package validate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- frontmatter tests -------------------------------------------------------

func TestParseFrontmatter_Valid(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "agent.md")
	content := "---\nid: test\nrole: specialist\n---\n\n# Body\n"
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	fm, err := parseFrontmatter(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm["id"] != "test" {
		t.Errorf("expected id=test; got %v", fm["id"])
	}
}

func TestParseFrontmatter_NoFence(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "bad.md")
	if err := os.WriteFile(f, []byte("# No frontmatter\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := parseFrontmatter(f)
	if err == nil {
		t.Error("expected error for file with no --- fence")
	}
}

func TestParseFrontmatter_NoClosingFence(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "bad.md")
	if err := os.WriteFile(f, []byte("---\nid: foo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := parseFrontmatter(f)
	if err == nil {
		t.Error("expected error for file with no closing ---")
	}
}

func TestParseFrontmatter_InvalidYAML(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "bad.md")
	if err := os.WriteFile(f, []byte("---\n: bad: yaml: [\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := parseFrontmatter(f)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestHasFrontmatter(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		wantOK bool
	}{
		{"valid", "---\nid: x\n---\n# body", true},
		{"no opening", "# no fm\n", false},
		{"no closing", "---\nid: x\n# body\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hasFrontmatter([]byte(tc.input))
			if got != tc.wantOK {
				t.Errorf("hasFrontmatter(%q) = %v; want %v", tc.name, got, tc.wantOK)
			}
		})
	}
}

// ---- tree validation tests --------------------------------------------------

func makeAgentFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func makeTree(t *testing.T) (base string, cleanup func()) {
	t.Helper()
	tmp := t.TempDir()
	for _, sub := range []string{"agents", "skills", "rules"} {
		if err := os.MkdirAll(filepath.Join(tmp, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return tmp, func() {} // t.TempDir auto-cleans
}

// validAgentBody generates a minimal body with all required sections and
// exactly 100 lines (within agent budget 80-140).
func validAgentBody() string {
	var sb strings.Builder
	sb.WriteString("---\nid: tester\nrole: specialist\n---\n\n")
	sb.WriteString("# Test Agent\n\n")
	sb.WriteString("## Purpose\n\nTest purpose.\n\n")
	sb.WriteString("## Execution\n\nTest execution.\n\n")
	sb.WriteString("## Special rules\n\nTest special rules.\n\n")
	sb.WriteString("## Handling peer messages\n\nTest handling.\n\n")
	sb.WriteString("## Personality\n\nTest personality.\n\n")
	// Pad to ~100 lines.
	for sb.String() != "" && strings.Count(sb.String(), "\n") < 99 {
		sb.WriteString(".\n")
	}
	return sb.String()
}

func TestValidateTree_CleanAgent(t *testing.T) {
	base, _ := makeTree(t)
	makeAgentFile(t, filepath.Join(base, "agents"), "myagent.md", validAgentBody())

	var buf bytes.Buffer
	cfg := Config{
		YakosRoot: base,
		Writer:    &buf,
	}
	r := &Result{}
	validateTree(cfg, r, &buf, "framework", base)

	if r.Errors != 0 {
		t.Errorf("expected 0 errors; got %d\noutput:\n%s", r.Errors, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "[ok]") {
		t.Errorf("expected [ok] in output; got:\n%s", out)
	}
}

func TestValidateTree_BadFrontmatter(t *testing.T) {
	base, _ := makeTree(t)
	makeAgentFile(t, filepath.Join(base, "agents"), "bad.md", "# No frontmatter at all\n")

	var buf bytes.Buffer
	cfg := Config{YakosRoot: base, Writer: &buf}
	r := &Result{}
	validateTree(cfg, r, &buf, "framework", base)

	if r.Errors == 0 {
		t.Error("expected at least 1 error for bad frontmatter")
	}
	if !strings.Contains(buf.String(), "[err]") {
		t.Errorf("expected [err] in output; got:\n%s", buf.String())
	}
}

func TestValidateTree_EmptyDir(t *testing.T) {
	base, _ := makeTree(t)

	var buf bytes.Buffer
	cfg := Config{YakosRoot: base, Writer: &buf}
	r := &Result{}
	validateTree(cfg, r, &buf, "framework", base)

	if r.Errors != 0 {
		t.Errorf("expected 0 errors for empty dir; got %d", r.Errors)
	}
	if !strings.Contains(buf.String(), "no agents/skills/rules") {
		t.Errorf("expected empty-dir message; got:\n%s", buf.String())
	}
}

func TestValidateTree_NonexistentDir(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{YakosRoot: "/does/not/exist", Writer: &buf}
	r := &Result{}
	validateTree(cfg, r, &buf, "framework", "/does/not/exist/lib")

	if r.Errors != 0 {
		t.Errorf("expected 0 errors for nonexistent dir; got %d", r.Errors)
	}
	if !strings.Contains(buf.String(), "does not exist") {
		t.Errorf("expected 'does not exist' message; got:\n%s", buf.String())
	}
}

// ---- strict mode tests ------------------------------------------------------

func TestStrictMode_WarnBecomesError(t *testing.T) {
	base, _ := makeTree(t)
	// Create an agent with 50 lines (below the 80-line minimum budget).
	var short strings.Builder
	short.WriteString("---\nid: short\nrole: specialist\n---\n")
	for i := 0; i < 46; i++ {
		short.WriteString(".\n")
	}
	makeAgentFile(t, filepath.Join(base, "agents"), "short.md", short.String())

	var buf bytes.Buffer
	cfg := Config{YakosRoot: base, Strict: true, Writer: &buf}
	r := &Result{}
	validateTree(cfg, r, &buf, "framework", base)

	if r.Errors == 0 {
		t.Error("expected errors in strict mode for under-budget agent")
	}
	if !strings.Contains(buf.String(), "(strict mode)") {
		t.Errorf("expected '(strict mode)' in output; got:\n%s", buf.String())
	}
}

// ---- line budget tests ------------------------------------------------------

func TestLineBudget_AgentOverBudget(t *testing.T) {
	base, _ := makeTree(t)
	// Agent file with 150 lines (over 140 budget).
	var sb strings.Builder
	sb.WriteString("---\nid: big\nrole: specialist\n---\n")
	for i := 0; i < 146; i++ {
		sb.WriteString(".\n")
	}
	makeAgentFile(t, filepath.Join(base, "agents"), "big.md", sb.String())

	var buf bytes.Buffer
	cfg := Config{YakosRoot: base, Writer: &buf}
	r := &Result{}
	checkLineBudgets(cfg, r, &buf, base)

	if r.Warnings == 0 {
		t.Error("expected warning for over-budget agent")
	}
}

func TestLineBudget_SkillOK(t *testing.T) {
	base, _ := makeTree(t)
	skillDir := filepath.Join(base, "skills", "myskill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	// 100-line skill — within 80-350 budget.
	var sb strings.Builder
	sb.WriteString("---\nname: myskill\n---\n")
	for i := 0; i < 97; i++ {
		sb.WriteString(".\n")
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	cfg := Config{YakosRoot: base, Writer: &buf}
	r := &Result{}
	checkLineBudgets(cfg, r, &buf, base)

	if r.Warnings != 0 {
		t.Errorf("expected 0 warnings for in-budget skill; got %d", r.Warnings)
	}
}

// ---- eval case validation tests ---------------------------------------------

func TestValidateEvalCaseFile_Valid(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "case-01.json")
	content := `{
		"case_id": "test-01",
		"task": "do something",
		"rubric": {
			"criteria": [
				{"name": "correctness", "weight": 0.8, "type": "binary"},
				{"name": "quality",     "weight": 0.2, "type": "scalar_0_1"}
			]
		},
		"expected_outcomes": ["outcome 1"],
		"context_files": [],
		"max_duration_s": 120,
		"max_cost_usd": 0.5,
		"tags": ["go"]
	}`
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateEvalCaseFile(f); err != nil {
		t.Errorf("expected valid case; got: %v", err)
	}
}

func TestValidateEvalCaseFile_MissingFields(t *testing.T) {
	cases := []struct {
		name    string
		content string
		errSub  string
	}{
		{
			"missing case_id",
			`{"task":"x","rubric":{"criteria":[{"name":"a","weight":0.5,"type":"binary"}]},"expected_outcomes":["x"],"context_files":[],"max_duration_s":1,"max_cost_usd":1,"tags":[]}`,
			"case_id",
		},
		{
			"empty criteria",
			`{"case_id":"x","task":"x","rubric":{"criteria":[]},"expected_outcomes":["x"],"context_files":[],"max_duration_s":1,"max_cost_usd":1,"tags":[]}`,
			"non-empty",
		},
		{
			"bad criterion type",
			`{"case_id":"x","task":"x","rubric":{"criteria":[{"name":"a","weight":0.5,"type":"unknown"}]},"expected_outcomes":["x"],"context_files":[],"max_duration_s":1,"max_cost_usd":1,"tags":[]}`,
			"binary or scalar_0_1",
		},
		{
			"weight out of range",
			`{"case_id":"x","task":"x","rubric":{"criteria":[{"name":"a","weight":1.5,"type":"binary"}]},"expected_outcomes":["x"],"context_files":[],"max_duration_s":1,"max_cost_usd":1,"tags":[]}`,
			"(0,1]",
		},
		{
			"negative max_duration_s",
			`{"case_id":"x","task":"x","rubric":{"criteria":[{"name":"a","weight":0.5,"type":"binary"}]},"expected_outcomes":["x"],"context_files":[],"max_duration_s":-1,"max_cost_usd":1,"tags":[]}`,
			"> 0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			f := filepath.Join(tmp, "case-01.json")
			if err := os.WriteFile(f, []byte(tc.content), 0644); err != nil {
				t.Fatal(err)
			}
			err := validateEvalCaseFile(f)
			if err == nil {
				t.Errorf("expected error containing %q; got nil", tc.errSub)
				return
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Errorf("expected error %q; got %q", tc.errSub, err.Error())
			}
		})
	}
}

// ---- playbook reference tests -----------------------------------------------

func TestCheckPlaybookReferences_NoRefs(t *testing.T) {
	base, _ := makeTree(t)
	// Agent with a valid frontmatter but no playbook: references.
	makeAgentFile(t, filepath.Join(base, "agents"), "a.md",
		"---\nid: a\nrole: specialist\n---\n\n# Agent A\n")

	var buf bytes.Buffer
	cfg := Config{YakosRoot: base, Writer: &buf}
	r := &Result{}
	checkPlaybookReferences(cfg, r, &buf, base)

	if r.Errors != 0 {
		t.Errorf("expected 0 errors with no playbook refs; got %d\n%s", r.Errors, buf.String())
	}
}

func TestCheckPlaybookReferences_BrokenRef(t *testing.T) {
	base, _ := makeTree(t)
	content := "---\nid: a\nrole: specialist\nreferences:\n  - playbook:missing-playbook\n---\n\n# Agent A\n"
	makeAgentFile(t, filepath.Join(base, "agents"), "a.md", content)

	var buf bytes.Buffer
	cfg := Config{YakosRoot: base, Writer: &buf}
	r := &Result{}
	checkPlaybookReferences(cfg, r, &buf, base)

	if r.Errors == 0 {
		t.Error("expected error for broken playbook reference")
	}
}

// ---- JSON validation tests --------------------------------------------------

func TestIsValidJSON(t *testing.T) {
	tmp := t.TempDir()

	good := filepath.Join(tmp, "good.json")
	if err := os.WriteFile(good, []byte(`{"key":"value"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if !isValidJSON(good) {
		t.Error("expected good.json to be valid JSON")
	}

	bad := filepath.Join(tmp, "bad.json")
	if err := os.WriteFile(bad, []byte(`{bad json}`), 0644); err != nil {
		t.Fatal(err)
	}
	if isValidJSON(bad) {
		t.Error("expected bad.json to be invalid JSON")
	}
}

// ---- PrintSummary tests -----------------------------------------------------

func TestPrintSummary_ZeroErrors(t *testing.T) {
	var buf bytes.Buffer
	r := &Result{Errors: 0, Warnings: 2}
	code := PrintSummary(&buf, r)
	if code != 0 {
		t.Errorf("expected exit 0; got %d", code)
	}
	if !strings.Contains(buf.String(), "0 error(s), 2 warning(s)") {
		t.Errorf("unexpected summary: %s", buf.String())
	}
}

func TestPrintSummary_WithErrors(t *testing.T) {
	var buf bytes.Buffer
	r := &Result{Errors: 3, Warnings: 1}
	code := PrintSummary(&buf, r)
	if code != 1 {
		t.Errorf("expected exit 1; got %d", code)
	}
}
