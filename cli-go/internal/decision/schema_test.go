package decision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodSet = `schema_id: demo@1
version: 1
surface: demo
model: jev-1.13.0
may_block: false
max_state_bytes: 4096
state_fields: [tool, file_path]
questions:
  risk:
    type: choice
    instructions: Classify the call.
    criteria:
      benign: fine
      dangerous: destructive
  scope:
    type: noul
    instructions: The call is work toward the stated intent.
  sev:
    type: score
    instructions: How severe.
    criteria: [none, some, lots]
thresholds:
  risk: {min_confidence: 0.7}
`

func TestParseSet_Good(t *testing.T) {
	qs, errs := ParseSet("demo", []byte(goodSet))
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if qs.Hash != HashBytes([]byte(goodSet)) || len(qs.Hash) != 64 {
		t.Errorf("hash = %q", qs.Hash)
	}
	if _, ok := qs.Questions["risk"].Criteria.(map[string]string); !ok {
		t.Errorf("criteria must be normalised, got %T", qs.Questions["risk"].Criteria)
	}
	if _, ok := qs.Questions["sev"].Criteria.([]string); !ok {
		t.Errorf("score criteria must be []string, got %T", qs.Questions["sev"].Criteria)
	}
}

func TestParseSet_HashChangesWithAnyByte(t *testing.T) {
	a, _ := ParseSet("demo", []byte(goodSet))
	b, _ := ParseSet("demo", []byte(strings.Replace(goodSet, "0.7", "0.8", 1)))
	if a.Hash == b.Hash {
		t.Fatal("a tuned threshold must change the hash")
	}
}

func TestParseSet_Rejections(t *testing.T) {
	rep := func(old, new string) string { return strings.Replace(goodSet, old, new, 1) }
	cases := map[string]struct{ body, want string }{
		"alias model latest":  {rep("jev-1.13.0", "jev-latest"), "alias or unpinned"},
		"alias model preview": {rep("jev-1.13.0", "jev-preview"), "alias or unpinned"},
		"partial version":     {rep("jev-1.13.0", "jev-1.13"), "alias or unpinned"},
		"missing model":       {rep("model: jev-1.13.0\n", ""), "model is required"},
		"schema id mismatch":  {rep("schema_id: demo@1", "schema_id: other@1"), "does not match surface"},
		"version mismatch":    {rep("version: 1", "version: 2"), "does not match schema_id"},
		"bad qtype":           {rep("type: noul", "type: text"), "must be noul, choice, or score"},
		"choice one option":   {rep("      dangerous: destructive\n", ""), "at least 2 options"},
		"score one level":     {rep("[none, some, lots]", "[none]"), "2-10 levels"},
		"score 11 levels":     {rep("[none, some, lots]", "[a,b,c,d,e,f,g,h,i,j,k]"), "2-10 levels"},
		"noul bad criteria":   {rep("    instructions: The call is work toward the stated intent.\n", "    instructions: x\n    criteria: {maybe: y}\n"), "true or false"},
		"empty instructions":  {rep("Classify the call.", "''"), "instructions are required"},
		"unknown field":       {rep("may_block: false", "may_block: false\nmax_tokens: 5"), "max_tokens"},
		"threshold no q":      {rep("risk: {min_confidence: 0.7}", "ghost: {min_confidence: 0.7}"), "names no question"},
		"threshold range":     {rep("0.7", "1.7"), "[0,1]"},
		"no state_fields":     {rep("state_fields: [tool, file_path]", "state_fields: []"), "state_fields must list"},
		"bad state field":     {rep("[tool, file_path]", "[\"a b\"]"), "not a plain field name"},
		"cap zero":            {rep("max_state_bytes: 4096", "max_state_bytes: 0"), "max_state_bytes"},
		"cap above hard":      {rep("max_state_bytes: 4096", "max_state_bytes: 999999999"), "max_state_bytes"},
		"no questions":        {"schema_id: demo@1\nversion: 1\nsurface: demo\nmodel: jev-1.13.0\nmax_state_bytes: 10\nstate_fields: [a]\n", "questions must not be empty"},
		"not yaml":            {"::: {", "yaml"},
	}
	for name, c := range cases {
		_, errs := ParseSet("demo", []byte(c.body))
		if len(errs) == 0 {
			t.Errorf("%s: accepted", name)
			continue
		}
		joined := ""
		for _, e := range errs {
			joined += e.Error() + "\n"
		}
		if !strings.Contains(joined, c.want) {
			t.Errorf("%s: errors %q lack %q", name, joined, c.want)
		}
	}
	if _, errs := ParseSet("other", []byte(goodSet)); len(errs) == 0 {
		t.Error("surface must match the file name")
	}
	if _, errs := ParseSet("demo", []byte(strings.Repeat("#", MaxSchemaBytes+1))); len(errs) == 0 {
		t.Error("oversize file must be rejected")
	}
}

func TestLoadSet_SurfaceTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "demo.yaml"), []byte(goodSet), 0o600)
	for _, s := range []string{"../demo", "a/b", "", "DEMO", "demo.yaml", "-x", ".."} {
		if _, err := LoadSet(dir, s); err == nil {
			t.Errorf("surface %q accepted", s)
		}
	}
	if _, err := LoadSet(dir, "demo"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateDir_MayBlockNeedsPromotionForExactHash(t *testing.T) {
	dir := t.TempDir()
	body := strings.Replace(goodSet, "may_block: false", "may_block: true", 1)
	_ = os.WriteFile(filepath.Join(dir, "demo.yaml"), []byte(body), 0o600)
	promo := filepath.Join(t.TempDir(), "p.ndjson")

	res := ValidateDir(dir, promo) // no log at all
	if len(res) != 1 || len(res[0].Errs) == 0 {
		t.Fatalf("may_block without promotion must fail: %+v", res)
	}
	h := HashBytes([]byte(body))
	_ = os.WriteFile(promo, []byte(`{"surface":"demo","schema_hash":"deadbeef"}`+"\n"), 0o600)
	if len(ValidateDir(dir, promo)[0].Errs) == 0 {
		t.Fatal("promotion for a different hash must not count")
	}
	_ = os.WriteFile(promo, []byte(`{"surface":"other","schema_hash":"`+h+`"}`+"\n"), 0o600)
	if len(ValidateDir(dir, promo)[0].Errs) == 0 {
		t.Fatal("promotion for a different surface must not count")
	}
	_ = os.WriteFile(promo, []byte("garbage\n"+`{"surface":"demo","schema_hash":"`+h+`"}`+"\n"), 0o600)
	if errs := ValidateDir(dir, promo)[0].Errs; len(errs) != 0 {
		t.Fatalf("recorded promotion must pass: %v", errs)
	}
	if len(ValidateDir(dir, "")[0].Errs) == 0 {
		t.Fatal("empty promotions path fails closed")
	}
}

func TestValidateDir_SortedAndMissingDir(t *testing.T) {
	if ValidateDir(filepath.Join(t.TempDir(), "nope"), "") != nil {
		t.Error("missing dir yields nothing")
	}
	dir := t.TempDir()
	for _, n := range []string{"b", "a", "c"} {
		_ = os.WriteFile(filepath.Join(dir, n+".yaml"), []byte(strings.ReplaceAll(goodSet, "demo", n)), 0o600)
	}
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o600)
	res := ValidateDir(dir, "")
	if len(res) != 3 || !strings.HasSuffix(res[0].Path, "a.yaml") || !strings.HasSuffix(res[2].Path, "c.yaml") {
		t.Fatalf("not sorted / wrong set: %+v", res)
	}
	for _, r := range res {
		if len(r.Errs) != 0 {
			t.Errorf("%s: %v", r.Path, r.Errs)
		}
	}
}

func TestCheckAgentFrontmatter(t *testing.T) {
	cases := []struct {
		name string
		fm   map[string]any
		want int // number of errors
		sub  string
	}{
		{"clean", map[string]any{"runtime": "claude", "tools": "Read, Grep"}, 0, ""},
		{"runtime jev", map[string]any{"runtime": "jev", "tools": "Read"}, 1, "not a runtime"},
		{"runtime jev caps", map[string]any{"runtime": "JEV"}, 1, "ADR-0009"},
		{"fallback jev", map[string]any{"runtime": "claude", "runtime-fallback": []any{"codex", "jev"}}, 1, "runtime-fallback"},
		{"fallback jev string", map[string]any{"runtime-fallback": "[claude, jev]"}, 1, "runtime-fallback"},
		{"jev + Bash", map[string]any{"runtime": "jev", "tools": "Read, Bash"}, 2, "Edit/Write/Bash"},
		{"provider + Edit", map[string]any{"decision-provider": "jev", "tools": []any{"Read", "Edit"}}, 1, "Edit/Write/Bash"},
		{"provider + Write list", map[string]any{"decision_provider": "mock", "tools": "[Write]"}, 1, "Edit/Write/Bash"},
		{"provider read-only", map[string]any{"decision-provider": "mock", "tools": "Read, Grep"}, 0, ""},
		{"provider none + Bash", map[string]any{"decision-provider": "none", "tools": "Bash"}, 0, ""},
		{"jevelin substring is not jev", map[string]any{"runtime": "jevelin"}, 0, ""},
	}
	for _, c := range cases {
		got := CheckAgentFrontmatter("a", c.fm)
		if len(got) != c.want {
			t.Errorf("%s: %d errors %v, want %d", c.name, len(got), got, c.want)
		}
		if c.sub != "" && !strings.Contains(strings.Join(got, "|"), c.sub) {
			t.Errorf("%s: %v lacks %q", c.name, got, c.sub)
		}
		for _, g := range got {
			if !strings.Contains(g, "agent a:") {
				t.Errorf("%s: message must name the agent: %q", c.name, g)
			}
		}
	}
}
