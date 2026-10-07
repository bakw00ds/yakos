package router

// K-140 fixup tests: credential-file name forms, secret-only material, invisible
// characters, glob bounds, the deadline inside a chunk and the result memo.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/secretscan"
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

// 1 MiB of clean, token-dense text with the maximum 32 project globs finishes
// well inside the 2 s timeout and is not classified sensitive.
func TestSensitive_BenchmarkCleanMiBWith32Globs(t *testing.T) {
	var globs []string
	for i := 0; i < 16; i++ {
		globs = append(globs, fmt.Sprintf("*[!b][!b][!b]x%d*[!c]?[!d]b", i))
		// No literal to reject on (names are lower-cased, so no token ends in a plus): the matcher runs on most tokens.
		globs = append(globs, "*a*[!b]*?[!d]*[+]")
	}
	text := uniqueWords(1<<20, "internal/service/handler_%d_test.go refactor parser %d and wire config;")
	reasonCache.reset()
	start := time.Now()
	reason := SensitiveReason(Input{Material: []string{text}, NeverPaths: globs})
	d := time.Since(start)
	t.Logf("1 MiB clean text, 32 globs: %v reason=%q", d, reason)
	if reason != "" {
		t.Fatalf("reason = %q", reason)
	}
	if d > 2*time.Second {
		t.Errorf("took %v", d)
	}
	// The crafted worst case from the security review: 64 globs of chained
	// classes are dropped, so the scan is the defaults' cost.
	var evil []string
	for i := 0; i < 64; i++ {
		evil = append(evil, "*"+strings.Repeat("[!b]", 60)+"b")
	}
	dense := uniqueWords(1<<20, strings.Repeat("a", 900)+"%d%d ")
	reasonCache.reset()
	start = time.Now()
	r2 := SensitiveReason(Input{Material: []string{dense}, NeverPaths: evil})
	t.Logf("1 MiB dense tokens, 64 crafted globs: %v reason=%q", time.Since(start), r2)
	if time.Since(start) > 2500*time.Millisecond {
		t.Errorf("crafted globs took %v", time.Since(start))
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
