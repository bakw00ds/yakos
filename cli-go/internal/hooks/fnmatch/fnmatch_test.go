package fnmatch_test

import (
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/fnmatch"
)

func TestMatch(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"web/**", "web/a/b", true},
		{"web/*", "web/a/b", true},
		{"*.env", ".env", true},
		{"a?c", "abc", true},
		{"a[!x]c", "abc", true},
		{"a[!x]c", "axc", false},
		{"web/**", "webx", false},
	}
	for _, c := range cases {
		if got := fnmatch.Match(c.pat, c.name); got != c.want {
			t.Errorf("Match(%q,%q)=%v want %v", c.pat, c.name, got, c.want)
		}
	}
}
