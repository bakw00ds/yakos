package main

import (
	"bytes"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/completion"
)

// completionExempt lists routed names deliberately absent from shell
// completion: machine-invoked entry points and answered-before-routing
// built-ins that are not something an operator tab-completes.
var completionExempt = map[string]string{
	"hook":           "invoked by Claude Code from settings.json, not by operators",
	"go-port-status": "internal port-progress diagnostic",
	"go":             "YAKOS_IMPL value, not a command",
	"bash":           "YAKOS_IMPL value, not a command",
}

// topLevelCommandNames returns every top-level command the Go router
// accepts: the switch args[0] cases in main.go (long flags and help/version
// aliases excluded) plus the commandRegistry names, sorted and de-duplicated.
func topLevelCommandNames(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	start := strings.Index(s, "switch args[0] {")
	if start < 0 {
		t.Fatal("main.go: `switch args[0] {` router not found")
	}
	end := strings.Index(s[start:], "\n\tdefault:")
	if end < 0 {
		t.Fatal("main.go: router default case not found")
	}
	seen := map[string]bool{}
	caseRE := regexp.MustCompile(`(?m)^\tcase ((?:"[^"]+"(?:, )?)+):`)
	for _, m := range caseRE.FindAllStringSubmatch(s[start:start+end], -1) {
		for _, n := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(m[1], -1) {
			seen[n[1]] = true
		}
	}
	for _, e := range commandRegistry {
		seen[e.Name] = true
	}
	var out []string
	for n := range seen {
		if strings.HasPrefix(n, "-") || n == "help" {
			continue
		}
		if _, skip := completionExempt[n]; skip {
			continue
		}
		out = append(out, n)
	}
	sort.Strings(out)
	if len(out) < 30 {
		t.Fatalf("parsed only %d top-level commands (%v); router parsing broke", len(out), out)
	}
	return out
}

// TestCompletionListsEveryTopLevelCommand keeps the embedded bash/zsh/fish
// templates in step with the command router: a command added to main.go
// without a completion entry (as `serve` was) fails here.
func TestCompletionListsEveryTopLevelCommand(t *testing.T) {
	names := topLevelCommandNames(t)
	shells := map[string]func(string) *regexp.Regexp{
		// bash: whitespace-delimited word inside the top_cmds assignment.
		"bash": func(n string) *regexp.Regexp {
			return regexp.MustCompile(`(?s)local top_cmds="(?:[^"]*\s)?` + regexp.QuoteMeta(n) + `[\s\\"]`)
		},
		// zsh: a 'name:description' _describe entry.
		"zsh": func(n string) *regexp.Regexp {
			return regexp.MustCompile(`(?m)^\s*'` + regexp.QuoteMeta(n) + `:`)
		},
		// fish: a top-level `-a 'name'` completion.
		"fish": func(n string) *regexp.Regexp {
			return regexp.MustCompile(`__fish_use_subcommand'\s+-a '` + regexp.QuoteMeta(n) + `'`)
		},
	}
	for shell, re := range shells {
		var buf bytes.Buffer
		if _, err := completion.Run(completion.Config{Subcommand: shell, Writer: &buf, ErrWriter: &bytes.Buffer{}}); err != nil {
			t.Fatalf("completion %s: %v", shell, err)
		}
		out := buf.String()
		for _, n := range names {
			if !re(n).MatchString(out) {
				t.Errorf("%s completion is missing top-level command %q", shell, n)
			}
		}
	}
}
