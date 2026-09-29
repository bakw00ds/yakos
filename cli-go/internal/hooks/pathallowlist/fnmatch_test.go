package pathallowlist

import "testing"

// Expectations here were produced by bash itself:
//
//	case "$name" in $pattern) echo yes ;; *) echo no ;; esac
//
// (see the differential script referenced in the K-87 A-2b report).
func TestFnmatchMatchesBashCase(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"*", "", true},
		{"*", "a/b/c", true},
		{"*", ".hidden", true},
		{"api/*", "api/a/b/c", true},
		{"api/**", "api/a/b", true},
		{"api/**", "api", false},
		{"api/*", "api/", true},
		{"*.go", "a/b.go", true},
		{"*.go", "a/b.gox", false},
		{"?", "a", true},
		{"?", "", false},
		{"?", "ab", false},
		{"a?c", "abc", true},
		{"a?c", "a/c", true},
		{".env", ".env", true},
		{".env", ".envx", false},
		{".env.*", ".env.local", true},
		{"[abc]x", "bx", true},
		{"[abc]x", "dx", false},
		{"[!abc]x", "dx", true},
		{"[^abc]x", "dx", true},
		{"[^abc]x", "ax", false},
		{"[a-c]x", "bx", true},
		{"[a-c]x", "dx", false},
		{"[]]x", "]x", true},
		{"[!]]x", "ax", true},
		{"[[:digit:]]x", "5x", true},
		{"[[:digit:]]x", "ax", false},
		{"[[:alpha:][:digit:]]", "z", true},
		{"[a", "[a", true},
		{"[a", "a", false},
		{"a[", "a[", true},
		{`\*`, "*", true},
		{`\*`, "a", false},
		{`a\?b`, "a?b", true},
		{`a\?b`, "axb", false},
		{`\`, `\`, true},
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "aXXbYY", false},
		{"*a*a*a*b", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaac", false},
		{"", "", true},
		{"", "a", false},
		{"[a-]", "-", true},
		{"[a-]", "a", true},
		{"[-a]", "-", true},
	}
	for _, c := range cases {
		if got := fnmatch(c.pat, c.name); got != c.want {
			t.Errorf("fnmatch(%q, %q)=%v want %v", c.pat, c.name, got, c.want)
		}
	}
}

func TestLexicalNormalize(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"a":                 "a",
		"a/b/../c":          "a/c",
		"./a//b/./c/":       "a/b/c",
		"/a/b":              "a/b",
		"a/..":              "",
		"..":                "..",
		"../a":              "../a",
		"a/../../b":         "../b",
		"a/b/../../../x":    "../x",
		"../..":             "../..",
		"a/../..":           "..",
		"a/./b/../../c/../": "",
	}
	for in, want := range cases {
		if got := lexicalNormalize(in); got != want {
			t.Errorf("lexicalNormalize(%q)=%q want %q", in, got, want)
		}
	}
	for _, e := range []string{"..", "../x", "../../x"} {
		if !escapesRoot(e) {
			t.Errorf("escapesRoot(%q) should be true", e)
		}
	}
	for _, e := range []string{"", "a", "..a", "a/..", "..x/y"} {
		if escapesRoot(e) {
			t.Errorf("escapesRoot(%q) should be false", e)
		}
	}
}

func TestShellBasename(t *testing.T) {
	cases := map[string]string{"": "", "a": "a", "a/b": "b", "a/b/": "b", "/": "", "a//b": "b"}
	for in, want := range cases {
		if got := shellBasename(in); got != want {
			t.Errorf("shellBasename(%q)=%q want %q", in, got, want)
		}
	}
}

func TestIsWithin(t *testing.T) {
	cases := []struct {
		root, path string
		want       bool
	}{
		{"/p", "/p", true},
		{"/p", "/p/a", true},
		{"/p", "/pa", false},
		{"/p/", "/p/a", true},
		{"/", "/x", true},
		{"/p/a", "/p", false},
	}
	for _, c := range cases {
		if got := isWithin(c.root, c.path); got != c.want {
			t.Errorf("isWithin(%q,%q)=%v want %v", c.root, c.path, got, c.want)
		}
	}
}
