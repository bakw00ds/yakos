package modelreg

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// Characters the sanitizer must remove or normalise. They are spelled as UTF-8
// byte escapes so the source stays plain ASCII: a literal BOM does not compile and
// the others are invisible in review.
const (
	zwsp       = "\xe2\x80\x8b"     // U+200B zero width space
	zwnj       = "\xe2\x80\x8c"     // U+200C zero width non-joiner
	zwj        = "\xe2\x80\x8d"     // U+200D zero width joiner
	wordJoiner = "\xe2\x81\xa0"     // U+2060
	bom        = "\xef\xbb\xbf"     // U+FEFF byte order mark
	lre        = "\xe2\x80\xaa"     // U+202A left-to-right embedding
	pdf        = "\xe2\x80\xac"     // U+202C pop directional formatting
	rlo        = "\xe2\x80\xae"     // U+202E right-to-left override
	lri        = "\xe2\x81\xa6"     // U+2066 left-to-right isolate
	pdi        = "\xe2\x81\xa9"     // U+2069 pop directional isolate
	nbsp       = "\xc2\xa0"         // U+00A0
	lsep       = "\xe2\x80\xa8"     // U+2028 line separator
	nel        = "\xc2\x85"         // U+0085 next line (whitespace)
	csiC1      = "\xc2\x9b"         // U+009B the 8-bit CSI introducer (a control)
	tagA       = "\xf3\xa0\x81\x81" // U+E0041 tag latin capital A
	tagB       = "\xf3\xa0\x81\x82" // U+E0042
	grin       = "\xf0\x9f\x98\x80" // U+1F600
	eacute     = "\xc3\xa9"         // U+00E9
	ouml       = "\xc3\xb6"         // U+00F6
)

// The real `agy models` output of agy 1.2.17, captured 2026-10-06 (stdout only).
func TestDiscoveryParse_RealSample(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "agy-models-1.2.17.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	models, dropped := parseAgyModels(raw)
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
	if len(models) != 18 {
		t.Fatalf("got %d models, want 18: %+v", len(models), models)
	}
	want := map[int]DiscoveredModel{
		0:  {ID: "gemini-3.8-flash-high", Name: "Gemini 3.8 Flash (High)"},
		9:  {ID: "gemini-3.1-pro-high", Name: "Gemini 3.1 Pro (High)"},
		12: {ID: "claude-opus-5-5-medium", Name: "Claude Opus 5.5 (Medium)"},
		17: {ID: "gpt-oss-120b-medium", Name: "GPT-OSS 120B (Medium)"},
	}
	for i, w := range want {
		if models[i] != w {
			t.Errorf("models[%d] = %+v, want %+v", i, models[i], w)
		}
	}
	for _, m := range models {
		if !ValidID(m.ID) {
			t.Errorf("kept an invalid id %q", m.ID)
		}
	}
}

func TestDiscoveryParse_Table(t *testing.T) {
	long64 := strings.Repeat("a", 64)
	long65 := strings.Repeat("a", 65)
	cases := []struct {
		name        string
		in          string
		wantIDs     []string
		wantNames   []string // parallel to wantIDs when non-nil
		wantDropped int
	}{
		{name: "empty input", in: ""},
		{name: "only blanks and comments", in: "\n\n# agy models, captured 2026-10-06\n   \n  # indented comment\n"},
		{name: "plain tsv", in: "a-1\tName One\nb-2\tName Two\n",
			wantIDs: []string{"a-1", "b-2"}, wantNames: []string{"Name One", "Name Two"}},
		{name: "no trailing newline", in: "a-1\tName One",
			wantIDs: []string{"a-1"}, wantNames: []string{"Name One"}},
		{name: "crlf", in: "a-1\tName One\r\nb-2\tName Two\r\n",
			wantIDs: []string{"a-1", "b-2"}, wantNames: []string{"Name One", "Name Two"}},
		{name: "header comment then models", in: "# header\na-1\tOne\n",
			wantIDs: []string{"a-1"}, wantNames: []string{"One"}},
		// The format is tab-separated. A line with no tab is never a model, even when
		// the whole line would pass as an id: a one-word message on standard output
		// ("unauthorized", "loading") must not become a one-model listing.
		{name: "no tab, whole line is an id", in: "just-an-id\n", wantDropped: 1},
		{name: "one word on stdout", in: "unauthorized\n", wantDropped: 1},
		{name: "one word beside real lines", in: "a-1\tOne\nloading\nb-2\tTwo\n",
			wantIDs: []string{"a-1", "b-2"}, wantNames: []string{"One", "Two"}, wantDropped: 1},
		{name: "no tab, line is not an id", in: "Fetching available models...\nno models here\n", wantDropped: 2},
		{name: "only spaces between the id and the name", in: "a-1   Name One\n", wantDropped: 1},
		{name: "tab then empty name", in: "a-1\t\n", wantIDs: []string{"a-1"}, wantNames: []string{""}},
		{name: "cut at the FIRST tab only", in: "a-1\tName\twith\ttabs\n",
			wantIDs: []string{"a-1"}, wantNames: []string{"Name with tabs"}},
		{name: "spaces around the id", in: "  a-1  \t  Name  \n",
			wantIDs: []string{"a-1"}, wantNames: []string{"Name"}},
		{name: "duplicate id keeps the first and is not counted", in: "a-1\tFirst\na-1\tSecond\nb-2\tB\n",
			wantIDs: []string{"a-1", "b-2"}, wantNames: []string{"First", "B"}},

		{name: "id with a space", in: "bad id\tName\ngood\tName\n", wantIDs: []string{"good"}, wantNames: []string{"Name"}, wantDropped: 1},
		{name: "uppercase id", in: "Gemini-3\tName\n", wantDropped: 1},
		{name: "leading dash id", in: "-model\tName\n--help\tName\n", wantDropped: 2},
		{name: "semicolon id", in: "a;rm\tName\n", wantDropped: 1},
		{name: "path traversal id", in: "../etc/passwd\tName\n", wantDropped: 1},
		{name: "slash id", in: "a/b\tName\n", wantDropped: 1},
		{name: "NUL in id", in: "a\x00b\tName\n", wantDropped: 1},
		{name: "empty id before the tab", in: "\tName\n", wantDropped: 1},
		{name: "unicode id", in: ("m" + ouml + "del\tName\n"), wantDropped: 1},
		{name: "backtick and dollar", in: "a`id`\tN\n$(id)\tN\n", wantDropped: 2},
		{name: "65 chars is too long", in: long65 + "\tName\n", wantDropped: 1},
		{name: "64 chars is the limit", in: long64 + "\tName\n", wantIDs: []string{long64}, wantNames: []string{"Name"}},
		{name: "id alphabet", in: "a0._:-z\tName\n", wantIDs: []string{"a0._:-z"}, wantNames: []string{"Name"}},

		{name: "ansi colour in a name", in: "a-1\t\x1b[31mRed\x1b[0m Name\n",
			wantIDs: []string{"a-1"}, wantNames: []string{"Red Name"}},
		{name: "bidi override in a name", in: ("a-1\tName " + rlo + "emaN" + pdf + " end\n"),
			wantIDs: []string{"a-1"}, wantNames: []string{"Name emaN end"}},
		{name: "zero width and bom in a name", in: ("a-1\tN" + zwsp + "am" + bom + "e\n"),
			wantIDs: []string{"a-1"}, wantNames: []string{"Name"}},
		{name: "NUL in a name", in: "a-1\tNa\x00me\n",
			wantIDs: []string{"a-1"}, wantNames: []string{"Name"}},
		{name: "name capped at 80 runes", in: "a-1\t" + strings.Repeat(eacute, 200) + "\n",
			wantIDs: []string{"a-1"}, wantNames: []string{strings.Repeat(eacute, 80)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			models, dropped := parseAgyModels([]byte(tc.in))
			var ids, names []string
			for _, m := range models {
				ids = append(ids, m.ID)
				names = append(names, m.Name)
			}
			if !reflect.DeepEqual(ids, tc.wantIDs) {
				t.Errorf("ids = %q, want %q", ids, tc.wantIDs)
			}
			if tc.wantNames != nil && !reflect.DeepEqual(names, tc.wantNames) {
				t.Errorf("names = %q, want %q", names, tc.wantNames)
			}
			if dropped != tc.wantDropped {
				t.Errorf("dropped = %d, want %d", dropped, tc.wantDropped)
			}
			for _, m := range models {
				if !ValidID(m.ID) {
					t.Errorf("kept an invalid id %q", m.ID)
				}
			}
		})
	}
}

func TestDiscoveryParse_CapsTheNumberOfModels(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&sb, "model-%03d\tModel %d\n", i, i)
	}
	models, dropped := parseAgyModels([]byte(sb.String()))
	if len(models) != maxListedModels {
		t.Errorf("kept %d models, want %d", len(models), maxListedModels)
	}
	if dropped != 600-maxListedModels {
		t.Errorf("dropped = %d, want %d", dropped, 600-maxListedModels)
	}
	if models[0].ID != "model-000" || models[maxListedModels-1].ID != fmt.Sprintf("model-%03d", maxListedModels-1) {
		t.Errorf("the cap must keep the first %d lines in order, got %q .. %q", maxListedModels, models[0].ID, models[len(models)-1].ID)
	}
}

func TestDiscoveryParse_OneHugeLineIsDroppedNotHung(t *testing.T) {
	models, dropped := parseAgyModels([]byte(strings.Repeat("x", 300<<10)))
	if len(models) != 0 || dropped != 1 {
		t.Errorf("got %d models, %d dropped; want 0 and 1", len(models), dropped)
	}
}

func TestDiscoverySanitize_Table(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"plain", "Gemini 3.8 Flash (High)", 80, "Gemini 3.8 Flash (High)"},
		{"empty", "", 80, ""},
		{"max zero", "abc", 0, ""},
		{"max negative", "abc", -1, ""},
		{"csi colour", "\x1b[1;31mred\x1b[0m", 80, "red"},
		{"csi with intermediates", "a\x1b[?25lb", 80, "ab"},
		{"csi cursor move", "a\x1b[2Jb", 80, "ab"},
		{"osc terminated by BEL", "a\x1b]0;evil title\x07b", 80, "ab"},
		{"osc terminated by ST", "a\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\b", 80, "alinkb"},
		{"osc never terminated", "keep\x1b]0;rest of the line is a title", 80, "keep"},
		{"dcs string", "a\x1bPq1;2;3\x1b\\b", 80, "ab"},
		{"charset select", "a\x1b(Bb", 80, "ab"},
		{"two byte escape", "a\x1bcb", 80, "ab"},
		{"escape at end", "abc\x1b", 80, "abc"},
		{"csi at end without final", "abc\x1b[12", 80, "abc"},
		{"escape then newline", "a\x1b\nb", 80, "a b"},
		{"nul and c0", "a\x00b\x01c\x7fd", 80, "abcd"},
		{"c1 controls", "a\u0085b\u009bc", 80, "a bc"}, // U+0085 is whitespace (NEL), U+009B is a control
		{"bidi override and isolates", ("a" + lre + "b" + rlo + "c" + lri + "d" + pdi + "e"), 80, "abcde"},
		{"zero width joiner family", ("a" + zwsp + "b" + zwnj + "c" + zwj + "d" + wordJoiner + "e"), 80, "abcde"},
		{"tag characters", "a\U000E0041\U000E0042b", 80, "ab"},
		{"tabs and newlines collapse", "a\t\tb\n\nc\r\nd", 80, "a b c d"},
		{"nbsp and line separator collapse", ("a" + nbsp + lsep + "b"), 80, "a b"},
		{"leading and trailing space", "   a  b   ", 80, "a b"},
		{"only whitespace and controls", " \t\x00\n ", 80, ""},
		{"invalid utf8 removed", "a\xffb\xc3(c", 80, "ab(c"},
		{"cut at max", "abcdef", 3, "abc"},
		{"cut never leaves a trailing space", "abc def", 4, "abc"},
		{"cut keeps an inner space", "abc def", 5, "abc d"},
		{"counts runes not bytes", (eacute + eacute + eacute + eacute + eacute), 3, (eacute + eacute + eacute)},
		{"emoji is not removed", "ok \U0001F600", 80, "ok \U0001F600"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeText(tc.in, tc.max); got != tc.want {
				t.Errorf("sanitizeText(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

// FuzzDiscoveryParse holds the invariants the rest of the package relies on
// whatever bytes a command prints. `go test` runs the seeds; run it for longer
// with `go test -fuzz FuzzDiscoveryParse -fuzztime 30s ./internal/modelreg`.
func FuzzDiscoveryParse(f *testing.F) {
	sample, _ := os.ReadFile(filepath.Join("testdata", "agy-models-1.2.17.tsv"))
	for _, seed := range []string{
		string(sample),
		"a-1\t\x1b[31mRed\x1b[0m\n",
		"a-1\t\x1b]0;title\x07\n",
		"\x1b",
		"a\tb\tc\n\n\n#x\n",
		("../x\t" + rlo + "Name\n"),
		strings.Repeat("a-1\tN\n", 3),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		models, dropped := parseAgyModels([]byte(in))
		if dropped < 0 {
			t.Fatalf("negative dropped %d", dropped)
		}
		if len(models) > maxListedModels {
			t.Fatalf("kept %d models", len(models))
		}
		seen := map[string]bool{}
		for _, m := range models {
			if !ValidID(m.ID) {
				t.Fatalf("kept an invalid id %q from %q", m.ID, in)
			}
			if seen[m.ID] {
				t.Fatalf("kept %q twice", m.ID)
			}
			seen[m.ID] = true
			if !utf8.ValidString(m.Name) || utf8.RuneCountInString(m.Name) > maxNameRunes {
				t.Fatalf("name %q is not valid text within %d runes", m.Name, maxNameRunes)
			}
			if m.Name != strings.TrimSpace(m.Name) || strings.Contains(m.Name, "  ") {
				t.Fatalf("name %q has stray whitespace", m.Name)
			}
			for _, r := range m.Name {
				if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || (unicode.IsSpace(r) && r != ' ') {
					t.Fatalf("name %q holds %U", m.Name, r)
				}
			}
		}
	})
}
