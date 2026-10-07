package dispatch

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The no-policy matrix: every row is one dispatch the P0a chain decided before
// the router existed. The expected results are NOT computed by the code under
// test; they are the frozen golden testdata/route_p0a_golden.txt, generated once
// at base 24f0a6ba (see testdata/route_p0a_golden.gen.txt). This file holds only
// the inputs, so the generator can run unchanged on that base.

type matrixOverride struct {
	RuntimeOverride, RuntimeEnvDefault, ModelOverride, EvalRunID string
	RuntimeFallbackOptIn                                         []string
	// Fields the base did not have; base ignores them, so a row with them set
	// must give the same result there.
	Class          string
	TaskBytes      int64
	ConversationID string
}

var matrixProjects = []string{
	"", "default-runtime: codex\n",
	"per-domain:\n  code-review: agy\n  platform: codex\ndefault-fallback: [claude]\n",
	"default-runtime: gemini\n",
	"default-fallback: [codex]\n",
	"default-runtime: agy\ndefault-fallback: [codex, claude]\n",
}

var matrixProbes = map[string]func(string) probeResult{
	"all-up":    func(string) probeResult { return probeResult{OK: true} },
	"agy-down":  func(n string) probeResult { return probeResult{OK: n != "agy", Reason: "down"} },
	"codex-out": func(n string) probeResult { return probeResult{OK: n != "codex", Reason: "down"} },
	"only-agy":  func(n string) probeResult { return probeResult{OK: n == "agy", Reason: "down"} },
}

var matrixOverrides = []matrixOverride{
	{}, {RuntimeOverride: "codex"}, {RuntimeOverride: "auto"}, {RuntimeOverride: "agy", ModelOverride: "balanced"},
	{ModelOverride: "haiku"}, {RuntimeEnvDefault: "agy"}, {RuntimeFallbackOptIn: []string{"claude"}, RuntimeOverride: "codex"},
	{EvalRunID: "ev-1"}, {Class: "chat", TaskBytes: 5000, ConversationID: "conv"},
	{RuntimeOverride: "claude"}, {RuntimeOverride: "gemini"}, {ModelOverride: "gpt-5.5"}, {ModelOverride: "opus", RuntimeOverride: "agy"},
}

func matrixAgents(t *testing.T, root string) []string {
	t.Helper()
	var agents []string
	entries, err := os.ReadDir(filepath.Join(root, "lib", "agents"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		agents = append(agents, strings.TrimSuffix(e.Name(), ".md"))
	}
	return append(agents, "claude", "codex", "agy", "no-such-agent")
}

func matrixProbeNames() []string {
	var names []string
	for n := range matrixProbes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func matrixKey(pi int, probe, agent string, oi int) string {
	return fmt.Sprintf("p%d|%s|%s|o%d", pi, probe, agent, oi)
}

// matrixNorm replaces the per-run temp paths in a result with placeholders.
func matrixNorm(s, root, project string) string {
	s = strings.ReplaceAll(s, root, "<root>")
	return strings.ReplaceAll(s, project, "<project>")
}
