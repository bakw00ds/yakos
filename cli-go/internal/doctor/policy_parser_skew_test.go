package doctor

import (
	"context"
	"strings"
	"testing"
)

// K-144: codex or agy at a version other than the one the stream parsers were
// recorded against is a low hint naming the cause of a missing tool card.
func TestCheckParserSkew(t *testing.T) {
	versions := map[string]string{"codex": "codex-cli 0.154.0\n", "agy": "1.9.0\n"}
	got := checkParserSkew(PolicyEnv{RuntimeVersion: func(_ context.Context, id string) string { return versions[id] }})
	if len(got) != 1 || got[0].ID != "parser-version-skew:agy" || got[0].Severity != PolicyLow {
		t.Fatalf("findings = %+v, want one low finding for agy", got)
	}
	if !strings.Contains(got[0].Message, "1.9.0") || !strings.Contains(got[0].Message, "1.3.1") {
		t.Errorf("message lacks the two versions: %q", got[0].Message)
	}

	versions["agy"] = "1.3.1"
	if got := checkParserSkew(PolicyEnv{RuntimeVersion: func(_ context.Context, id string) string { return versions[id] }}); len(got) != 0 {
		t.Errorf("recorded versions must report nothing: %+v", got)
	}
	// Absent CLI, unreadable output, or no probe: silent.
	for _, probe := range []func(context.Context, string) string{nil, func(context.Context, string) string { return "" }, func(context.Context, string) string { return "garbage" }} {
		if got := checkParserSkew(PolicyEnv{RuntimeVersion: probe}); len(got) != 0 {
			t.Errorf("findings = %+v", got)
		}
	}
}
