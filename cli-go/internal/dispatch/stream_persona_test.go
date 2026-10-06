package dispatch

// stream_persona_test.go — the console's one-shot chat path (RunStream) on claude
// refuses an agent persona over runtime.MaxPersonaBytes before a claude process
// is started, with the cap's error, and records the failed dispatch.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/runtime"
)

func TestRunStream_ClaudeOversizedPersonaIsRefusedBeforeSpawn(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Many short lines, as a real agent definition has them (the roster reader
	// refuses a single line over 1 MiB, so one huge line is not the shape to use).
	body := "---\nid: big\nmodel: sonnet\n---\n\n## Purpose\n\n" + strings.Repeat("persona text line\n", 5000)
	if err := os.WriteFile(filepath.Join(dir, "big.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// The fixture must really be over the cap, or this test proves nothing.
	roster, err := agentscompose.Compose(root, "")
	if err != nil || len(roster) != 1 {
		t.Fatalf("roster %v, err %v", roster, err)
	}
	if len(roster[0].Prompt) <= runtime.MaxPersonaBytes {
		t.Fatalf("fixture persona is %d bytes, want over %d", len(roster[0].Prompt), runtime.MaxPersonaBytes)
	}
	rec := fakeCLIs(t) // a recording stub claude, codex and agy first on PATH
	isolatedLogDir(t)
	svc := NewService(ServiceConfig{YakosRoot: root, WorkspaceRoot: t.TempDir(), OperatorID: "test-op"})

	var chunks int
	_, err = svc.RunStream(context.Background(), Params{Agent: "big", Task: "hi"}, func(StreamChunk) { chunks++ })
	if !errors.Is(err, runtime.ErrPersonaTooLarge) {
		t.Fatalf("RunStream = %v, want an error wrapping ErrPersonaTooLarge", err)
	}
	if got := invoked(t, rec); len(got) != 0 {
		t.Errorf("a runtime was started (%v) although the persona is over the cap", got)
	}
	if chunks != 0 {
		t.Errorf("%d chunks were emitted for a dispatch that never started", chunks)
	}
}
