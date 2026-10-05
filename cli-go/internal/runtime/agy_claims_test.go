package runtime

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// agyContainmentSentence is how K-158 (2026-10-05) says agy dispatch may be
// described. It was measured, not assumed: under the flags yakOS passes, agy's
// Seatbelt profile blocks writes outside the workspace by default but not reads
// or network, and the model can lift the block itself. Wherever the docs or the
// comments describe agy's sandbox they carry this sentence, and none may call
// agy sandboxed or contained without saying it is not. Change the wording only
// from a newer K-158 verdict, and change it everywhere at once; this test names
// every place and how many copies each holds.
const agyContainmentSentence = "Under `--sandbox --dangerously-skip-permissions`, agy's macOS Seatbelt sandbox " +
	"blocks writes outside the workspace by default but leaves file reads and outbound network unrestricted, " +
	"and the model can escalate out of the sandbox at will via `run_command(BypassSandbox=true)`, " +
	"which `--dangerously-skip-permissions` auto-approves; agy dispatch is therefore not a containment boundary " +
	"for reads, network or writes and must only receive non-sensitive work or run inside an external OS sandbox (K-159)."

// agyClaimFiles lists every file that describes agy's sandbox, with the number of
// verbatim copies of the sentence it holds. A copy that drifts by a word stops
// counting, so the count fails; the two copies in the runtime matrix (the sandbox
// section and the agy section) cannot drift apart unnoticed.
var agyClaimFiles = []struct {
	rel    string
	copies int
}{
	{"docs/runtime-matrix.md", 2},
	{"CHANGELOG.md", 1},
	{"UPGRADING.md", 1},
	{"tests/fixtures/runtime-streams/adapter-argv-recordings.md", 1},
	{"cli-go/internal/runtime/agy.go", 1},
	{"cli-go/internal/runtime/sandbox.go", 1},
	{"cli/lib/runtimes/agy.sh", 1},
}

// Wordings from before K-158 reported. Any of them in these files means a
// description of agy that the measurements contradict.
var staleAgyWordings = []string{
	"containment under dedicated review",
	"agy dispatch is sandboxed",
	"agy is sandboxed",
	"sandboxed agy",
	"agy sandboxed by default",
	"agy's sandbox is probably weaker",
	"agy stays sandboxed",
}

var (
	reWhitespace    = regexp.MustCompile(`\s+`)
	reCommentMarker = regexp.MustCompile(`(?m)^[ \t]*(//|#)[ \t]?`)

	// A sentence ends at ". ", "! " or "? ", and for this scan also at ";" (a
	// clause about codex must not make a clause about agy look sandboxed).
	reSentenceBreak = regexp.MustCompile(`[.!?](?:\s+|$)|;\s`)

	reAgyWord   = regexp.MustCompile(`(?i)\bagy\b`)
	reClaimWord = regexp.MustCompile(`(?i)\b(?:sandboxed|contained)\b`)
	// A claim is qualified when a negation closes the text before it, with at
	// most four words between: "is not contained", "must not be treated as
	// contained". "unsandboxed" and "uncontained" are not claims, and do not match.
	reNegationBefore = regexp.MustCompile(`(?i)\b(?:not|never|no|isn't|aren't|cannot)\b(?:\W+\w+){0,4}\W*$`)
)

// claimUnits splits a file into the units the claim scan reads: a Markdown table
// row is kept whole, and every other paragraph (wrapped lines joined, comment
// markers left in) is cut into sentences. The verbatim K-158 sentence needs no
// exemption: it names agy but says neither "sandboxed" nor "contained".
func claimUnits(raw string) []string {
	var units, para []string
	flush := func() {
		if len(para) == 0 {
			return
		}
		text := reWhitespace.ReplaceAllString(strings.Join(para, " "), " ")
		units = append(units, reSentenceBreak.Split(text, -1)...)
		para = nil
	}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			flush()
		case strings.HasPrefix(trimmed, "|"):
			flush()
			units = append(units, reWhitespace.ReplaceAllString(trimmed, " "))
		default:
			para = append(para, trimmed)
		}
	}
	flush()
	return units
}

// agyClaimsWithoutQualifier returns every sentence that pairs agy with
// "sandboxed" or "contained" and does not negate the claim. The scan is a
// heuristic: it reads each such word and looks a few words back for a negation,
// so "agy is sandboxed and codex is not" is still caught, and a sentence that
// mentions both runtimes has to say which is which or be split.
func agyClaimsWithoutQualifier(raw string) []string {
	var bad []string
	for _, unit := range claimUnits(raw) {
		if !reAgyWord.MatchString(unit) {
			continue
		}
		for _, loc := range reClaimWord.FindAllStringIndex(unit, -1) {
			start := loc[0] - 80
			if start < 0 {
				start = 0
			}
			if !reNegationBefore.MatchString(unit[start:loc[0]]) {
				bad = append(bad, strings.TrimSpace(unit))
				break
			}
		}
	}
	return bad
}

func repoRaw(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Skipf("%s not reachable from the package dir: %v", rel, err)
	}
	return string(b)
}

// repoText is repoRaw with comment markers at line starts removed and whitespace
// collapsed, so a sentence wrapped over several lines of a Markdown bullet or a
// Go or shell comment compares equal.
func repoText(t *testing.T, rel string) string {
	t.Helper()
	return reWhitespace.ReplaceAllString(reCommentMarker.ReplaceAllString(repoRaw(t, rel), ""), " ")
}

func TestAgyContainmentIsDescribedWithTheK158Wording(t *testing.T) {
	for _, f := range agyClaimFiles {
		text := repoText(t, f.rel)
		if got := strings.Count(text, agyContainmentSentence); got != f.copies {
			t.Errorf("%s must carry the K-158 sentence about agy's containment verbatim %d time(s), it has %d:\n%s",
				f.rel, f.copies, got, agyContainmentSentence)
		}
		lower := strings.ToLower(text)
		for _, stale := range staleAgyWordings {
			if strings.Contains(lower, stale) {
				t.Errorf("%s still says %q, which K-158 contradicts", f.rel, stale)
			}
		}
		for _, sentence := range agyClaimsWithoutQualifier(repoRaw(t, f.rel)) {
			t.Errorf("%s pairs agy with \"sandboxed\" or \"contained\" without saying it is not, which K-158 contradicts: %q",
				f.rel, sentence)
		}
	}
}

// TestAgyClaimScan pins what the scan flags and what it lets by, including the
// ways it was meant to be fooled: a drifted copy of the K-158 sentence, a table
// row, a sentence wrapped over lines, a comment, and codex and agy in one
// sentence.
func TestAgyClaimScan(t *testing.T) {
	driftedCopy := strings.Replace(agyContainmentSentence,
		"is therefore not a containment boundary for reads, network or writes",
		"is therefore contained for reads, network and writes", 1)
	if driftedCopy == agyContainmentSentence {
		t.Fatal("the drifted copy must differ from the sentence")
	}
	for _, tc := range []struct {
		name, text string
		flagged    bool
	}{
		{"agy sandboxed by default", "agy dispatch is sandboxed by default.", true},
		{"agy contained", "Agy is contained by --sandbox.", true},
		{"agy and codex stay sandboxed", "codex and agy stay sandboxed.", true},
		{"agy keeps the model contained", "The agy sandbox keeps the model contained.", true},
		{"table row", "| agy | sandboxed terminal | `--sandbox` |", true},
		{"wrapped over lines", "agy dispatch is\nsandboxed by default.", true},
		{"go comment", "// agy runs sandboxed.", true},
		{"shell comment", "# agy is contained here.", true},
		{"one claim negated, one not", "agy is sandboxed and not contained.", true},
		{"a drifted copy of the K-158 sentence", driftedCopy, true},

		{"negated", "agy gets `--sandbox` but is not contained (K-133, K-158).", false},
		{"bold negated", "**agy is not contained.**", false},
		{"codex clause apart from the agy clause", "codex dispatch is sandboxed by default; agy gets `--sandbox` but is not contained.", false},
		{"codex stays sandboxed, agy clause apart", "codex stays sandboxed; agy keeps --sandbox.", false},
		{"must not be treated as contained", "agy dispatch must not be treated as contained.", false},
		{"do not call agy sandboxed", "Do not call agy sandboxed.", false},
		{"codex table row", "| codex | sandboxed, cannot prompt | `exec --sandbox` |", false},
		{"codex row above an agy row", "| codex | sandboxed, cannot prompt |\n| agy | `--sandbox` passed |", false},
		{"unsandboxed is not a claim", "agy can run unsandboxed through the policy.", false},
		{"agy without a claim word", "agy gets `--sandbox`.", false},
		{"the verbatim K-158 sentence", agyContainmentSentence, false},
		{"the verbatim sentence wrapped in a comment", "// " + strings.ReplaceAll(agyContainmentSentence, ", agy's", ",\n// agy's"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := agyClaimsWithoutQualifier(tc.text)
			if tc.flagged && len(got) == 0 {
				t.Errorf("not flagged: %q", tc.text)
			}
			if !tc.flagged && len(got) != 0 {
				t.Errorf("flagged %q in %q", got, tc.text)
			}
		})
	}
}
