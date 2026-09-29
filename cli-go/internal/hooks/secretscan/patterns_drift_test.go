package secretscan

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The bash table (lib/hooks/lib/secret-patterns.sh) and the Go tables must
// stay identical: secret-scan and supervisor-stream redaction both use them.
func TestBashPatternTableMatchesGo(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "lib", "hooks", "lib", "secret-patterns.sh"))
	if err != nil {
		t.Skipf("bash table not reachable: %v", err)
	}
	src := string(data)
	entries := func(array string) []string {
		i := strings.Index(src, array+"=(")
		if i < 0 {
			t.Fatalf("%s not found", array)
		}
		block := src[i : i+strings.Index(src[i:], "\n)")]
		var out []string
		for _, m := range regexp.MustCompile(`(?m)^\s+'([^']*)'\s*$`).FindAllStringSubmatch(block, -1) {
			out = append(out, m[1][strings.Index(m[1], "|")+1:])
		}
		return out
	}
	var want []string
	for _, p := range DefaultPatterns {
		want = append(want, p.Source)
	}
	if got := entries("YAKOS_SECRET_PATTERNS"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("blocking table drift:\nbash %q\ngo   %q", got, want)
	}
	if got := entries("YAKOS_REDACT_EXTRA_PATTERNS"); strings.Join(got, "\n") != strings.Join(RedactExtraSources(), "\n") {
		t.Errorf("redaction-only table drift:\nbash %q\ngo   %q", got, RedactExtraSources())
	}
}

func TestRedactExtraShapes(t *testing.T) {
	for _, in := range []string{
		"curl -H 'Authorization: Bearer opaqueTokenValue123' x",
		"TOKEN=abcdefgh12345 run",
		`{"password": "hunter2hunter2"}`,
		"api_key: 0123456789abcdef",
		"X-Api-Key=abcdefgh1234",
	} {
		if out := Redact(in); !strings.Contains(out, RedactToken) || strings.Contains(out, "opaqueTokenValue123") || strings.Contains(out, "hunter2hunter2") || strings.Contains(out, "0123456789abcdef") || strings.Contains(out, "abcdefgh1") {
			t.Errorf("not redacted: %q -> %q", in, out)
		}
	}
	if out := Redact("token count: 5 items"); out != "token count: 5 items" {
		t.Errorf("over-redacted: %q", out)
	}
}

// Redaction-only rules must never block: secret-scan patterns stay untouched.
func TestExtraRulesNotInBlockingTable(t *testing.T) {
	for _, p := range DefaultPatterns {
		if strings.Contains(p.Source, "Bearer") || strings.Contains(p.Source, "[Bb]") {
			t.Errorf("generic rule leaked into blocking table: %s", p.Name)
		}
	}
}
