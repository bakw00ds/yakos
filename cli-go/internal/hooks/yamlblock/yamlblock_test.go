package yamlblock_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/yamlblock"
)

func TestChildren(t *testing.T) {
	cases := []struct {
		name string
		yml  string
		want []yamlblock.KV
	}{
		{"absent", "other:\n  enabled: false\n", nil},
		{"simple", "plan_quality:\n  enabled: false\n  mode: block\n", []yamlblock.KV{{"enabled", "false"}, {"mode", "block"}}},
		{"comment quote crlf", "plan_quality:\r\n  mode: \"block\"   # strict\r\n  threshold: '0.9'\r\n", []yamlblock.KV{{"mode", "block"}, {"threshold", "0.9"}}},
		{"blank and comment lines never end the block", "plan_quality:\n\n# c\n  mode: block\n\n  # c2\n  threshold: 0.5\n", []yamlblock.KV{{"mode", "block"}, {"threshold", "0.5"}}},
		{"ends at next top-level key", "plan_quality:\n  mode: block\nother:\n  enabled: false\n", []yamlblock.KV{{"mode", "block"}}},
		{"child map keys do not bleed", "plan_quality:\n  panel:\n    enabled: false\n  mode: block\n", []yamlblock.KV{{"panel", ""}, {"mode", "block"}}},
		{"child map first then key", "plan_quality:\n  panel:\n    enabled: false\n    mode: x\n  threshold: 0.7\n", []yamlblock.KV{{"panel", ""}, {"threshold", "0.7"}}},
		{"sibling under a common parent ends the block", "parent:\n  plan_quality:\n    mode: block\n  sibling:\n    enabled: false\n", []yamlblock.KV{{"mode", "block"}}},
		{"sibling at child indent under parent", "parent:\n  plan_quality:\n    mode: block\n  enabled: false\n", []yamlblock.KV{{"mode", "block"}}},
		{"tab indent", "plan_quality:\n\tmode: block\n\t\tnested: x\n", []yamlblock.KV{{"mode", "block"}}},
		{"prefix is not the block", "plan_quality_extra:\n  enabled: false\n", nil},
		{"space before colon", "plan_quality :\n  mode: block\n", []yamlblock.KV{{"mode", "block"}}},
		{"inline flow map has no children", "plan_quality: {enabled: false}\nmode: block\n", nil},
		{"broken yaml elsewhere is irrelevant", "broken: [unclosed\nplan_quality:\n  enabled: false\n", []yamlblock.KV{{"enabled", "false"}}},
		{"first block only", "plan_quality:\n  mode: a\nplan_quality:\n  mode: b\n", []yamlblock.KV{{"mode", "a"}}},
		{"dedent inside block is skipped", "plan_quality:\n    mode: block\n  enabled: false\n    threshold: 0.5\n", []yamlblock.KV{{"mode", "block"}, {"threshold", "0.5"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := yamlblock.Children([]byte(c.yml), "plan_quality")
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Children()=%#v want %#v", got, c.want)
			}
		})
	}
}

func TestLast(t *testing.T) {
	yml := "plan_quality:\n  enabled: true\n  enabled:\n  enabled: false\n"
	if v, ok := yamlblock.Last([]byte(yml), "plan_quality", "enabled"); !ok || v != "false" {
		t.Fatalf("later duplicate must win, got %q %v", v, ok)
	}
	yml = "plan_quality:\n  enabled: false\n  enabled:\n"
	if v, ok := yamlblock.Last([]byte(yml), "plan_quality", "enabled"); !ok || v != "false" {
		t.Fatalf("empty value must not override, got %q %v", v, ok)
	}
	if _, ok := yamlblock.Last([]byte("plan_quality:\n  mode: block\n"), "plan_quality", "enabled"); ok {
		t.Fatal("absent key must report ok=false")
	}
}

// K-110: invalid UTF-8 is read as bytes; every key after it is still returned,
// matching the bash reader (which runs awk under LC_ALL=C).
func TestChildrenInvalidUTF8(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "hooks", "yakos-invalid-utf8.yml"))
	if err != nil {
		t.Skipf("fixture not reachable: %v", err)
	}
	want := []yamlblock.KV{
		{"enabled", "false"}, {"owner", "caf\xe9"}, {"junk", "\xff\xfe"}, {"mode", "block"}, {"threshold", "0.9"},
	}
	got := yamlblock.Children(data, "plan_quality")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Children()=%#v want %#v", got, want)
	}
	if v, ok := yamlblock.Last(data, "plan_quality", "threshold"); !ok || v != "0.9" {
		t.Errorf("Last threshold = %q,%v", v, ok)
	}
}
