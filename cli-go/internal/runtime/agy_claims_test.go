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
// agy sandboxed by default. Change the wording only from a newer K-158 verdict,
// and change it everywhere at once; this test names every place.
const agyContainmentSentence = "Under `--sandbox --dangerously-skip-permissions`, agy's macOS Seatbelt sandbox " +
	"blocks writes outside the workspace by default but leaves file reads and outbound network unrestricted, " +
	"and the model can escalate out of the sandbox at will via `run_command(BypassSandbox=true)`, " +
	"which `--dangerously-skip-permissions` auto-approves; agy dispatch is therefore not a containment boundary " +
	"for reads, network or writes and must only receive non-sensitive work or run inside an external OS sandbox (K-159)."

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
)

// repoText reads a repository file and returns it with comment markers at line
// starts removed and whitespace collapsed, so a sentence wrapped over several
// lines of a Markdown bullet or a Go or shell comment compares equal.
func repoText(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Skipf("%s not reachable from the package dir: %v", rel, err)
	}
	return reWhitespace.ReplaceAllString(reCommentMarker.ReplaceAllString(string(b), ""), " ")
}

func TestAgyContainmentIsDescribedWithTheK158Wording(t *testing.T) {
	for _, rel := range []string{
		"docs/runtime-matrix.md",
		"CHANGELOG.md",
		"UPGRADING.md",
		"tests/fixtures/runtime-streams/adapter-argv-recordings.md",
		"cli-go/internal/runtime/agy.go",
		"cli-go/internal/runtime/sandbox.go",
		"cli/lib/runtimes/agy.sh",
	} {
		text := repoText(t, rel)
		if !strings.Contains(text, agyContainmentSentence) {
			t.Errorf("%s must carry the K-158 sentence about agy's containment verbatim:\n%s", rel, agyContainmentSentence)
		}
		lower := strings.ToLower(text)
		for _, stale := range staleAgyWordings {
			if strings.Contains(lower, stale) {
				t.Errorf("%s still says %q, which K-158 contradicts", rel, stale)
			}
		}
	}
}
