package router

// K-140 fixup tests: credential-file name forms, secret-only material, invisible
// characters, glob bounds, the deadline inside a chunk and the result memo.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/secretscan"
	"github.com/bakw00ds/yakos/internal/projectcfg"
)

func TestSensitive_NameForms(t *testing.T) {
	nbsp, emdash := string(rune(0xa0)), string(rune(0x2014))
	long := strings.Repeat("a/", 600) + ".env"
	longHead := ".env/" + strings.Repeat("a/", 600)
	forms := map[string]string{
		"at mention":    "please read @.env now",
		"emphasis":      "check **.env** please",
		"underscores":   "see _.env_ for it",
		"nbsp":          "open server.pem" + nbsp + "and send",
		"nbsp aws":      "open ~/.aws/credentials" + nbsp + "next",
		"em dash":       "cat ~/.netrc" + emdash + "then stop",
		"and and":       "cat ~/.netrc&&echo done",
		"long tail":     "x " + long,
		"long head":     "x " + longHead,
		"fullwidth":     "see \uff08.env\uff09",
		"trailing dot":  "it is in .env.",
		"ecdsa":         "copy ~/.ssh/id_ecdsa over",
		"dsa":           "copy ~/.ssh/id_dsa over",
		"kube":          "cat ~/.kube/config",
		"pypirc":        "edit ~/.pypirc",
		"npmrc":         "edit ~/.npmrc",
		"ed25519":       "copy ~/.ssh/id_ed25519",
		"tfvars":        "plan with prod.tfvars",
		"gcloud":        "use ~/.config/gcloud/application_default_credentials.json",
		"backslash":     `type C:\Users\x\.kube\config`,
		"markdown code": "run `cat .env`",
	}
	for name, text := range forms {
		if got := Classify(Input{Material: []string{text}}); got != ClassSensitive {
			t.Errorf("%s: %.60q classified %q", name, text, got)
		}
	}
	for _, text := range []string{"read the environment notes", "see config in the kube docs", "an envelope", "ssh keys guide"} {
		if got := Classify(Input{Material: []string{text}}); got != ClassDefault {
			t.Errorf("%q classified %q", text, got)
		}
	}
}

// Agent prompts say "never edit .env*": names are not scanned there, secrets are.
func TestSensitive_SecretOnlyIgnoresNamesKeepsPatterns(t *testing.T) {
	prose := "Never edit `.env*`, ~/.aws/credentials or server.pem."
	if got := Classify(Input{SecretOnly: []string{prose}}); got != ClassDefault {
		t.Errorf("prose in SecretOnly = %q", got)
	}
	if got := Classify(Input{Material: []string{prose}}); got != ClassSensitive {
		t.Errorf("prose in Material = %q", got)
	}
	if got := Classify(Input{SecretOnly: []string{"key " + secretCorpus()["aws"]}}); got != ClassSensitive {
		t.Errorf("secret in SecretOnly = %q", got)
	}
	// A caller class does not hide a secret that is only in SecretOnly.
	if got := Classify(Input{Class: "chat", SecretOnly: []string{secretCorpus()["github"]}}); got != ClassSensitive {
		t.Errorf("class hid a SecretOnly secret: %q", got)
	}
}

func TestSensitive_InvisibleCharactersDoNotHideAKey(t *testing.T) {
	key := "AKIA" + "IOSFODNN7EXAMPLE"
	for _, z := range []string{"\u200b", "\u200c", "\u200d", "\u2060", "\ufeff", "\u00ad"} {
		text := "key " + key[:6] + z + key[6:]
		if Classify(Input{Material: []string{text}}) != ClassSensitive {
			t.Errorf("U+%04X hid the key", []rune(z)[0])
		}
		if Classify(Input{Material: []string{"read ." + z + "env"}}) != ClassSensitive {
			t.Errorf("U+%04X hid .env", []rune(z)[0])
		}
	}
}

func TestSensitive_ProjectGlobsAreBounded(t *testing.T) {
	heavy := "*" + strings.Repeat("[!b]", 60) + "b"
	got := mergedNeverPaths([]string{heavy, "internal/billing/*"})
	has := func(s string) bool {
		for _, g := range got {
			if g == s {
				return true
			}
		}
		return false
	}
	if has(strings.ToLower(heavy)) || !has("internal/billing/*") {
		t.Errorf("heavy glob kept or a simple one dropped")
	}
	var many []string
	for i := 0; i < 100; i++ {
		many = append(many, fmt.Sprintf("p%d/*", i))
	}
	base := len(mergedNeverPaths(nil))
	if n := len(mergedNeverPaths(many)) - base; n != 32 {
		t.Errorf("project globs kept = %d, want 32", n)
	}
}

// The deadline is checked inside a chunk, not only between chunks.
func TestSensitive_DeadlineInsideAChunk(t *testing.T) {
	text := strings.Repeat("word ", 2000)
	if got := chunkNamesNeverPath(text, compileNever(mergedNeverPaths(nil)), time.Now().Add(-time.Second)); got != ReasonTimeout {
		t.Errorf("past deadline = %q", got)
	}
	if got := chunkNamesNeverPath(text, compileNever(mergedNeverPaths(nil)), time.Now().Add(time.Minute)); got != "" {
		t.Errorf("clean text = %q", got)
	}
}

// One scan serves the pre-check, Classify and ClassifyReason: after the first,
// a broken pattern table is not consulted.
func TestSensitive_ResultIsMemoisedByContent(t *testing.T) {
	reasonCache.reset()
	in := Input{Material: []string{"a harmless task"}}
	if Classify(in) != ClassDefault {
		t.Fatal("setup")
	}
	old := secretscan.DefaultPatterns[0].Regex
	secretscan.DefaultPatterns[0].Regex = nil
	t.Cleanup(func() { secretscan.DefaultPatterns[0].Regex = old; reasonCache.reset() })
	if c, why := ClassifyReason(in); c != ClassDefault || why != "" {
		t.Errorf("same content rescanned: %q/%q", c, why)
	}
	// Different content is a different key and does scan (and fails closed).
	if c, why := ClassifyReason(Input{Material: []string{"another task"}}); c != ClassSensitive || why != ReasonError {
		t.Errorf("new content: %q/%q", c, why)
	}
	// A timeout is never remembered.
	reasonCache.reset()
	secretscan.DefaultPatterns[0].Regex = old
	oldT := scanTimeout
	scanTimeout = -time.Second
	if SensitiveReason(in) != ReasonTimeout {
		t.Fatal("setup timeout")
	}
	scanTimeout = oldT
	if SensitiveReason(in) != "" {
		t.Error("timeout was cached")
	}
}

// benchGlobs is the largest legal project list: 32 globs, half of them shaped
// so the matcher (not the literal prefilter) runs on most tokens.
func benchGlobs() []string {
	var globs []string
	for i := 0; i < 16; i++ {
		globs = append(globs, fmt.Sprintf("*[!b][!b][!b]x%d*[!c]?[!d]b", i))
		// No literal to reject on (names are lower-cased, so no token ends in a plus).
		globs = append(globs, "*a*[!b]*?[!d]*[+]")
	}
	return globs
}

// 1 MiB of clean, token-dense text with the maximum 32 project globs is not
// classified sensitive. The outcome is asserted, not wall time: the race
// detector and shared runners make a clock assertion against the production
// timeout flaky (it failed CI at 2.0 s). The number lives in BenchmarkSensitive*.
func TestSensitive_CleanMiBWith32GlobsIsDefault(t *testing.T) {
	old := scanTimeout
	scanTimeout = time.Minute
	t.Cleanup(func() { scanTimeout = old; reasonCache.reset() })
	text := uniqueWords(1<<20, "internal/service/handler_%d_test.go refactor parser %d and wire config;")
	reasonCache.reset()
	if reason := SensitiveReason(Input{Material: []string{text}, NeverPaths: benchGlobs()}); reason != "" {
		t.Fatalf("reason = %q", reason)
	}
}

func BenchmarkSensitiveCleanMiBWith32Globs(b *testing.B) {
	text := uniqueWords(1<<20, "internal/service/handler_%d_test.go refactor parser %d and wire config;")
	in := Input{Material: []string{text}, NeverPaths: benchGlobs()}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reasonCache.reset()
		if r := SensitiveReason(in); r != "" {
			b.Fatalf("reason = %q", r)
		}
	}
}

// The crafted shapes from the security reviews. Round 1: 64 globs of 60 chained
// classes. Round 2 (N1): 32 globs `*[!` + 240 x c + `]?[+]`, four metacharacters
// and 248 bytes, which passed the old count bound and cost ~170 ms per decorated
// token. Both are now dropped by the bounds, so the scan costs the defaults only
// and the planted credential file is still found.
func TestSensitive_CraftedGlobsAreDroppedAndFast(t *testing.T) {
	var evil1, evil2 []string
	for i := 0; i < 64; i++ {
		evil1 = append(evil1, "*"+strings.Repeat("[!b]", 60)+"b")
	}
	for i := 0; i < 32; i++ {
		evil2 = append(evil2, "*[!"+strings.Repeat("c", 240)+"]?[+]")
	}
	for name, evil := range map[string][]string{"chained": evil1, "long-bracket": evil2} {
		for _, g := range evil {
			if projectcfg.NeverPathSimpleEnough(g) {
				t.Fatalf("%s glob accepted: %.30q", name, g)
			}
		}
		dense := uniqueWords(1<<20, strings.Repeat("a", 900)+"%d%d ")
		reasonCache.reset()
		start := time.Now()
		r := SensitiveReason(Input{Material: []string{dense}, NeverPaths: evil})
		d := time.Since(start)
		t.Logf("%s: %v reason=%q", name, d, r)
		// Outcome is exact; the clock bound is 10x the local cost (a hang, not a tuning).
		if r != "" && r != ReasonTimeout {
			t.Errorf("%s: reason %q", name, r)
		}
		if r == "" && d > 2*time.Second {
			t.Errorf("%s took %v", name, d)
		}
		reasonCache.reset()
		if got := SensitiveReason(Input{Material: []string{dense + " cat ~/.netrc"}, NeverPaths: evil}); got != ReasonNeverPath && got != ReasonTimeout {
			t.Errorf("%s: planted netrc gave %q", name, got)
		}
	}
}

// The deadline is checked on every token, not every few.
func TestSensitive_DeadlineIsCheckedPerToken(t *testing.T) {
	old := scanTimeout
	scanTimeout = -time.Second
	t.Cleanup(func() { scanTimeout = old; reasonCache.reset() })
	reasonCache.reset()
	if got := chunkNamesNeverPath("one two", compileNever(nil), time.Now().Add(-time.Second)); got != ReasonTimeout {
		t.Errorf("2 tokens past the deadline = %q", got)
	}
}

// N2: a PEM header with a non-breaking (or other Unicode) space is still a key.
func TestSensitive_PEMHeaderWithUnicodeSpace(t *testing.T) {
	for _, sp := range []string{"\u00a0", "\u2003", "\u3000"} {
		for _, where := range []string{"Material", "SecretOnly"} {
			text := "-----BEGIN RSA" + sp + "PRIVATE KEY-----"
			in := Input{Material: []string{text}}
			if where == "SecretOnly" {
				in = Input{SecretOnly: []string{text}}
			}
			if r := SensitiveReason(in); r != ReasonSecret {
				t.Errorf("%U in %s: %q", []rune(sp)[0], where, r)
			}
		}
	}
}

// uniqueWords builds n bytes of text whose tokens are all distinct (the result
// memo must not flatter the benchmark).
func uniqueWords(n int, format string) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, format, i, i)
	}
	return b.String()[:n]
}
