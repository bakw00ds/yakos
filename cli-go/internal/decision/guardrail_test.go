package decision

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The console chat-runtime select must never offer Jev (ADR-0009 §3.5): a
// decision provider is not selectable for chat.
func TestConsoleRuntimesExcludeJev(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "consoleui", "dist", "app.js"))
	if err != nil {
		t.Skipf("console bundle not present: %v", err)
	}
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "const RUNTIMES") || strings.Contains(line, "const STREAMING_RUNTIMES") {
			found = true
			if strings.Contains(strings.ToLower(line), "jev") {
				t.Errorf("console runtime list offers jev: %s", line)
			}
		}
	}
	if !found {
		t.Skip("RUNTIMES declaration not found; bundle layout changed")
	}
}

// Every question set shipped in lib/decisions must validate, and the example
// mock fixture must satisfy the example set (so the documented quick start
// works).
func TestShippedQuestionSetsValid(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "lib", "decisions")
	sets := ValidateDir(dir, "")
	if len(sets) == 0 {
		t.Skip("lib/decisions not present")
	}
	for _, sf := range sets {
		if len(sf.Errs) != 0 {
			t.Errorf("%s: %v", sf.Path, sf.Errs)
		}
	}
	qs, err := LoadSet(dir, "supervisor-prefilter")
	if err != nil {
		t.Fatal(err)
	}
	if qs.MayBlock {
		t.Error("the shipped example must not be may_block")
	}
	mock := &Mock{Fixture: filepath.Join(dir, "examples", "supervisor-prefilter.mock.json")}
	if _, err := mock.Decide(context.Background(), Request{Surface: qs.Surface, Model: qs.Model, Questions: qs.Questions}); err != nil {
		t.Errorf("example mock fixture does not satisfy the example set: %v", err)
	}
	state, err := os.ReadFile(filepath.Join(dir, "examples", "supervisor-prefilter.state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	if err := json.Unmarshal(state, &st); err != nil {
		t.Fatal(err)
	}
	for k := range st {
		found := false
		for _, f := range qs.StateFields {
			found = found || f == k
		}
		if !found {
			t.Errorf("example state field %q is not in state_fields", k)
		}
	}
}
