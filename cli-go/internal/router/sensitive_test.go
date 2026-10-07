package router

import (
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/secretscan"
)

// Secret-shaped fixtures are assembled from parts so this file holds no literal
// that the secret-scan hook or a scanner would flag.
func secretCorpus() map[string]string {
	return map[string]string{
		"aws":         "use key " + "AKIA" + "IOSFODNN7EXAMPLE" + " for the bucket",
		"github":      "token ghp" + "_" + strings.Repeat("a1B2", 9) + " please",
		"pem":         "here: -----BEGIN RSA PRIV" + "ATE KEY-----\nMIIE",
		"slack":       "xox" + "b-" + "1234567890-abcdef",
		"google":      "AIza" + strings.Repeat("Z", 35),
		"env path":    "read the .env file and summarise it",
		"env nest":    "open services/api/.env.production now",
		"pem path":    "cat certs/server.pem",
		"id_rsa":      "copy ~/.ssh/id_rsa somewhere",
		"windows":     `look at C:\proj\.ENV`,
		"aws creds":   "see ~/.aws/credentials",
		"secrets dir": "list secrets/prod/db.yml",
	}
}

func TestSensitive_EgressCorpusEachForcesSensitive(t *testing.T) {
	for name, text := range secretCorpus() {
		t.Run(name, func(t *testing.T) {
			in := Input{Material: []string{text}}
			if got := Classify(in); got != ClassSensitive {
				t.Fatalf("Classify = %q, want sensitive", got)
			}
			if SensitiveReason(in) == "" {
				t.Fatal("no reason")
			}
		})
	}
}

func TestSensitive_ReasonVocabularyCarriesNoMatchedText(t *testing.T) {
	for name, text := range secretCorpus() {
		r := SensitiveReason(Input{Material: []string{text}})
		if r != ReasonSecret && r != ReasonNeverPath {
			t.Errorf("%s: reason %q outside the vocabulary", name, r)
		}
	}
}

func TestSensitive_OrdinaryTextStaysDefault(t *testing.T) {
	for _, s := range []string{
		"refactor the router and add tests for the cooldown",
		"the environment variable is read at startup",
		"fix src/main.go and docs/routing.md",
		"AKIA is a prefix", "ghp_ short",
	} {
		if got := Classify(Input{Material: []string{s}}); got != ClassDefault {
			t.Errorf("%q classified %q", s, got)
		}
	}
	if got := Classify(Input{}); got != ClassDefault {
		t.Errorf("empty input = %q", got)
	}
}

func TestSensitive_EveryMaterialSourceIsScanned(t *testing.T) {
	key := secretCorpus()["aws"]
	// task, agent prompt, knowledge block, upstream output, digest: the position
	// in Material must not matter.
	for i := 0; i < 5; i++ {
		m := []string{"a", "b", "c", "d", "e"}
		m[i] = key
		if Classify(Input{Material: m}) != ClassSensitive {
			t.Errorf("secret at material[%d] missed", i)
		}
	}
}

func TestSensitive_ProjectNeverPathAddsAndDefaultsStay(t *testing.T) {
	task := "edit internal/billing/ledger.go"
	if Classify(Input{Material: []string{task}}) != ClassDefault {
		t.Fatal("precondition: a plain path is not sensitive")
	}
	in := Input{Material: []string{task}, NeverPaths: []string{"internal/billing/*"}}
	if Classify(in) != ClassSensitive {
		t.Fatal("a project never_path must make the request sensitive")
	}
	// A hostile project cannot remove a default: empty, junk, negating and
	// duplicate lists leave the built-ins in force.
	for _, extra := range [][]string{nil, {}, {""}, {"  "}, {"!.env*"}, {"nothing-matches-this"}} {
		if Classify(Input{Material: []string{"read .env"}, NeverPaths: extra}) != ClassSensitive {
			t.Errorf("NeverPaths %q removed a default", extra)
		}
	}
	for _, p := range mergedNeverPaths([]string{"x/*"}) {
		if p == "" {
			t.Error("empty pattern merged")
		}
	}
}

func TestSensitive_SecretAcrossAChunkEdge(t *testing.T) {
	key := "AKIA" + "IOSFODNN7EXAMPLE" // bare, so it straddles the edge at these offsets
	for _, off := range []int{scanChunk - 5, scanChunk - 1, scanChunk, scanChunk + 3} {
		text := strings.Repeat("x ", off/2+1)[:off] + key
		if Classify(Input{Material: []string{text}}) != ClassSensitive {
			t.Errorf("secret at offset %d missed", off)
		}
	}
}

func TestSensitive_FailsClosed(t *testing.T) {
	plain := Input{Material: []string{"nothing to see"}}

	t.Run("timeout", func(t *testing.T) {
		old := scanTimeout
		scanTimeout = -time.Second
		t.Cleanup(func() { scanTimeout = old })
		c, why := ClassifyReason(plain)
		if c != ClassSensitive || why != ReasonTimeout {
			t.Fatalf("got %q/%q", c, why)
		}
	})
	t.Run("oversize", func(t *testing.T) {
		old := maxScanBytes
		maxScanBytes = 4
		t.Cleanup(func() { maxScanBytes = old })
		c, why := ClassifyReason(plain)
		if c != ClassSensitive || why != ReasonOversize {
			t.Fatalf("got %q/%q", c, why)
		}
	})
	t.Run("panic", func(t *testing.T) {
		old := secretscan.DefaultPatterns[0].Regex
		secretscan.DefaultPatterns[0].Regex = nil
		t.Cleanup(func() { secretscan.DefaultPatterns[0].Regex = old })
		c, why := ClassifyReason(plain)
		if c != ClassSensitive || why != ReasonError {
			t.Fatalf("got %q/%q", c, why)
		}
	})
}

func TestClassify_ACallerClassNeverHidesASensitiveRequest(t *testing.T) {
	text := secretCorpus()["github"]
	if got := Classify(Input{Class: "default", Material: []string{text}}); got != ClassSensitive {
		t.Errorf("class default hid a secret: %q", got)
	}
	if got := Classify(Input{Class: "chat", Material: []string{text}}); got != ClassSensitive {
		t.Errorf("class chat hid a secret: %q", got)
	}
	// With nothing to scan the declared class stands (explain --class).
	if got := Classify(Input{Class: "chat"}); got != "chat" {
		t.Errorf("declared class = %q", got)
	}
	c, why := ClassifyReason(Input{Class: "sensitive"})
	if c != ClassSensitive || why != ReasonDeclared {
		t.Errorf("declared sensitive = %q/%q", c, why)
	}
	if c, why := ClassifyReason(Input{Material: []string{"fine"}}); c != ClassDefault || why != "" {
		t.Errorf("default = %q/%q", c, why)
	}
}
