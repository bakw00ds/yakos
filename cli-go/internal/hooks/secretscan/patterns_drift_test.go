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
		// Entries are single-quoted, or double-quoted with \" escapes when the
		// pattern itself holds a single quote.
		for _, m := range regexp.MustCompile(`(?m)^\s+(?:'([^']*)'|"((?:[^"\\]|\\")*)")\s*$`).FindAllStringSubmatch(block, -1) {
			e := m[1]
			if e == "" {
				e = strings.ReplaceAll(m[2], `\"`, `"`)
			}
			out = append(out, e[strings.Index(e, "|")+1:])
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
	if got := entries("YAKOS_REDACT_BLOCK_PATTERNS"); strings.Join(got, "\n") != strings.Join(RedactBlockSources(), "\n") {
		t.Errorf("redaction block table drift:\nbash %q\ngo   %q", got, RedactBlockSources())
	}
	if got := entries("YAKOS_REDACT_KEEP_PATTERNS"); strings.Join(got, "\n") != strings.Join(RedactKeepSources(), "\n") {
		t.Errorf("keep-context table drift:\nbash %q\ngo   %q", got, RedactKeepSources())
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

// K-110: curl basic auth, scheme://user:pass@host URLs and PEM bodies are
// redacted; benign look-alikes are left alone.
func TestRedactCredentialShapesK110(t *testing.T) {
	pemBody := "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7"
	pem := "-----BEGIN RSA PRIVATE KEY-----\n" + pemBody + "\nAbCdEf123456\n-----END RSA PRIVATE KEY-----"
	for _, tc := range []struct{ in, secret string }{
		{"curl -u alice:s3cretPw https://x.example/api", "s3cretPw"},
		{"curl -fsSu alice:s3cretPw https://x.example", "s3cretPw"},
		{"curl --user alice:s3cretPw https://x.example", "s3cretPw"},
		{"curl --user=alice:s3cretPw https://x.example", "s3cretPw"},
		{"curl -ualice:s3cretPw https://x.example", "s3cretPw"},
		{"curl -fsSualice:s3cretPw https://x.example", "s3cretPw"},
		{"curl -u 9lives:s3cretPw https://x.example", "s3cretPw"},
		{`curl -u "alice:pw one sTail" https://x.example`, "sTail"},
		{`curl -u 'alice:pw one sTail' https://x.example`, "sTail"},
		{`curl -u"alice:pw one sTail" https://x.example`, "sTail"},
		{"curl -U alice:s3cretPw https://x.example", "s3cretPw"},
		{"curl --proxy-user alice:s3cretPw https://x.example", "s3cretPw"},
		{"curl --proxy-user=alice:s3cretPw https://x.example", "s3cretPw"},
		{"curl -s -X POST -u 1admin:s3cretPw https://x.example", "s3cretPw"},
		{"wget --user=1admin:s3cretPw https://x.example", "s3cretPw"},
		{"redis-cli -u redis://:s3cretPw@cache:6379", "s3cretPw"},
		{"echo '-----BEGIN PGP PRIVATE KEY BLOCK-----\nlQPGBFk2pgpBodyLine\n-----END PGP PRIVATE KEY BLOCK-----'", "lQPGBFk2pgpBodyLine"},
		{"git clone https://bob:hunter2pass@github.com/o/r.git", "hunter2pass"},
		{"psql postgres://svc:pgPassw0rd@db:5432/app", "pgPassw0rd"},
		{"echo '" + pem + "' > k.pem", pemBody},
		{"echo '" + pem + "' > k.pem", "AbCdEf123456"},
		// Truncated block (no END marker): everything after the header goes.
		{"-----BEGIN PRIVATE KEY-----\n" + pemBody, pemBody},
	} {
		out := Redact(tc.in)
		if strings.Contains(out, tc.secret) || !strings.Contains(out, RedactToken) {
			t.Errorf("not redacted: %q -> %q", tc.in, out)
		}
		// Only the credential goes: the command and its flag stay readable.
		if strings.HasPrefix(tc.in, "curl") || strings.HasPrefix(tc.in, "wget") {
			if !strings.HasPrefix(out, strings.Fields(tc.in)[0]) || !strings.Contains(out, " -") {
				t.Errorf("command/flag lost from preview: %q -> %q", tc.in, out)
			}
		}
	}
	for _, benign := range []string{
		"sort -u a.txt",
		"docker run -u 1000:1000 img",
		"docker run --user 1000:1000 img",
		"sort -u 12:30",
		"ls -lu a:b",
		"git push -u origin main",
		"open https://example.com/a/b",
		"ssh git@github.com",
		"curl -s https://example.com",
	} {
		if out := Redact(benign); out != benign {
			t.Errorf("over-redacted: %q -> %q", benign, out)
		}
	}
	// Text after a complete PEM block survives.
	if out := Redact(pem + "\ntrailing-text"); !strings.Contains(out, "trailing-text") {
		t.Errorf("text after PEM lost: %q", out)
	}
}

func TestRedactKeepsCurlCommandAndFlag(t *testing.T) {
	cases := map[string]string{
		"curl -s -u alice:s3cretPw https://x.example":    "curl -s -u [REDACTED] https://x.example",
		`curl -u "alice:pw one sTail" https://x.example`: "curl -u [REDACTED] https://x.example",
		"curl --proxy-user alice:s3cretPw https://p":     "curl --proxy-user [REDACTED] https://p",
		"wget --user=alice:s3cretPw https://x.example":   "wget --user=[REDACTED] https://x.example",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every credential on the line goes, not just the last one (the kept prefix is
// greedy, so the rule is applied until nothing changes), and the command stays.
func TestRedactKeepMultipleAndContextFreeForms(t *testing.T) {
	for _, tc := range []struct {
		in      string
		secrets []string
		keep    string
	}{
		{"curl -u a:PW1 -u b:PW2 https://x.example", []string{"PW1", "PW2"}, "curl -u "},
		{"curl -ua:PW1 -ub:PW2 https://x.example", []string{"PW1", "PW2"}, "curl -u"},
		{"curl -U proxy:PW1 -u u:PW2 https://x.example", []string{"PW1", "PW2"}, "curl -U "},
		{"wget --proxy-user=p:PW1 --user=u:PW2 https://x.example", []string{"PW1", "PW2"}, "wget --proxy-user="},
		{"curl -s -u a:PW1 --proxy-user p:PW2 -u c:PW3 -u d:PW4 https://x", []string{"PW1", "PW2", "PW3", "PW4"}, "curl -s -u "},
		{"CURL -u u:PW1 https://x.example", []string{"PW1"}, "CURL -u "},
		{"x=curl; $x -u u:PW1 https://x.example", []string{"PW1"}, "$x -u "},
		{"http --auth u:PW1 https://x.example", []string{"PW1"}, "http --auth "},
		{"http -a u:PW1 https://x.example", []string{"PW1"}, "http -a "},
		{"xh -a u:PW1 https://x.example", []string{"PW1"}, "xh -a "},
		{"https --auth=u:PW1 https://x.example", []string{"PW1"}, "https --auth="},
	} {
		out := Redact(tc.in)
		for _, sec := range tc.secrets {
			if strings.Contains(out, sec) {
				t.Errorf("%s survived: %q -> %q", sec, tc.in, out)
			}
		}
		if !strings.Contains(out, tc.keep) || !strings.Contains(out, RedactToken) {
			t.Errorf("command/flag lost or nothing redacted: %q -> %q (want %q kept)", tc.in, out, tc.keep)
		}
	}
	for _, benign := range []string{"docker run -u 1000:1000 img", "docker run --user 1000:1000 img", "sort -u 12:30", "ls -lu a:b", "git push -u origin main"} {
		if out := Redact(benign); out != benign {
			t.Errorf("over-redacted %q -> %q", benign, out)
		}
	}
}
