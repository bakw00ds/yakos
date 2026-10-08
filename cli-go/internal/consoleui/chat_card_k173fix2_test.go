package consoleui_test

// chat_card_k173fix2_test.go: K-173 fix round 2. The whole Authorization value
// is redacted whatever the scheme, the cookie shapes of a pasted request are
// covered, and the knowledge pack is scanned for secret shapes only (a rules
// file may name ~/.ssh in prose) while the task text keeps the path matcher.

import (
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
)

func TestK173Fix2_WholeAuthorizationValueAndCookieShapes(t *testing.T) {
	cases := []struct{ name, text, secret string }{
		{"apikey scheme", "Authorization: ApiKey " + "Zk9xQ2xhbmRvbXZhbHVl", "Zk9xQ2xhbmRvbXZhbHVl"},
		{"ssws scheme", "Authorization: SSWS " + "00aBcDeFgHiJkLmNoP", "00aBcDeFgHiJkLmNoP"},
		{"key scheme", "authorization: Key " + "k3yv4lu3abcdef", "k3yv4lu3abcdef"},
		{"oauth signature", `Authorization: OAuth oauth_consumer_key="ck", oauth_signature="` + "s1gn4tur3Value%3D" + `"`, "s1gn4tur3Value%3D"},
		{"aws4 signature after comma", "Authorization: AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260101/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=" + "5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7", "5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7"},
		{"json array raw key", `{"Authorization":["` + "rawkeyvalue12345" + `"]}`, "rawkeyvalue12345"},
		{"go map raw key", "map[Authorization:[" + "rawkeyvalue12345" + "]]", "rawkeyvalue12345"},
		{"proxy authorization", "Proxy-Authorization: Custom " + "pr0xyS3cretValue", "pr0xyS3cretValue"},
		{"cgi cookie", "HTTP_COOKIE=" + "sessionid=9f8e7d6c5b4a3210", "9f8e7d6c5b4a3210"},
		{"curl -b", "curl -b 'sid=" + "abcdef0123456789" + "' https://example.test/", "abcdef0123456789"},
		{"curl --cookie", "curl --cookie sid=" + "abcdef0123456789" + " https://example.test/", "abcdef0123456789"},
		{"azure sas sig", "https://acct.blob.core.windows.net/c/b?sv=2022-11-02&sig=" + "dGVzdHNpZ25hdHVyZSUzRA%3D%3D" + "&se=2026", "dGVzdHNpZ25hdHVyZSUzRA"},
	}
	for _, c := range cases {
		if got, n := consoleui.ScanSecretsForTest(c.text); strings.Contains(got, c.secret) || n == 0 {
			t.Errorf("%s: secret survived the scan (%d): %q", c.name, n, got)
		}
		digest, _, n := consoleui.BuildHandoffDigestForTest([]consoleui.TranscriptEntry{{Role: consoleui.RoleUser, Text: c.text}}, "claude")
		if strings.Contains(digest, c.secret) || n == 0 {
			t.Errorf("%s: secret survived the digest (%d):\n%s", c.name, n, digest)
		}
	}
	for _, prose := range []string{"the authorization step comes first", "authorization: none", "git checkout -b feature/new-thing", "a cookie recipe: flour"} {
		if got, n := consoleui.ScanSecretsForTest(prose); n != 0 {
			t.Errorf("prose redacted: %q -> %q", prose, got)
		}
	}
}

// 1 MiB of header-like text redacts in linear time.
func TestK173Fix2_RedactionStaysLinear(t *testing.T) {
	for _, unit := range []string{
		"Authorization: " + strings.Repeat("a", 64) + "\n",
		strings.Repeat("authorization:[", 8) + "\n",
		"curl " + strings.Repeat("-b ", 20) + "\n",
		strings.Repeat("authorization=", 1000),
		strings.Repeat("_", 4096),
	} {
		text := strings.Repeat(unit, (1<<20)/len(unit)+1)[:1<<20]
		start := time.Now()
		consoleui.ScanSecretsForTest(text)
		if d := time.Since(start); d > 20*time.Second {
			t.Errorf("%.30q...: 1 MiB took %v", unit, d)
		}
	}
}

// A rules file that only names a guarded path in prose is not sensitive by
// itself: the routing rule moves the pane to codex. A credential in the pack
// still is. A guarded path in the task text still is.
func TestK173Fix2_PackScanIsSecretShapesOnly(t *testing.T) {
	prose := "# deploy\n\nThe release job reads the file ~/.ssh/" + "id_rsa and ." + "env.production.\n"
	t.Run("never-path prose in a rules file", func(t *testing.T) {
		r, _ := k173PackRun(t, prose, "say hello", "")
		if r["runtime"] != "codex" || r["class"] == "sensitive" {
			t.Errorf("route = %v, want codex and not sensitive", r)
		}
	})
	t.Run("credential in the pack", func(t *testing.T) {
		r, calls := k173PackRun(t, prose, "say hello", prose+"key: "+"AKIA"+"IOSFODNN7EXAMPLE\n")
		if r["runtime"] != "claude" || r["class"] != "sensitive" || len(calls) != 0 {
			t.Errorf("route = %v calls = %v, want claude/sensitive and no codex", r, calls)
		}
	})
	t.Run("never-path in the task text", func(t *testing.T) {
		r, calls := k173PackRun(t, "# deploy\n\nRun the release job.\n", "please cat ~/.ssh/"+"id_rsa", "")
		if r["runtime"] != "claude" || r["class"] != "sensitive" || len(calls) != 0 {
			t.Errorf("route = %v calls = %v, want claude/sensitive and no codex", r, calls)
		}
	})
}
