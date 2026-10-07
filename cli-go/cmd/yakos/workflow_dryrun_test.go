package main

// K-142: `yakos workflow run <name> --dry-run` prints where the router would run
// each node and dispatches nothing. Driven in process (no exec of bin/yakos).

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/workflow"
)

func TestWorkflowDryRun_PrintsPlannedRoutes(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("shell stubs and a POSIX state directory")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-test-not-real")
	t.Setenv("YAKOS_RUNTIME", "")
	state := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	pol := filepath.Join(state, "router-policy.yml")
	if err := os.WriteFile(pol, []byte("rules:\n  - match: {agent: plain}\n    action: {runtime: codex, model: gpt-5.4}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, n := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(bin, n), []byte("#!/bin/sh\necho 'DISPATCHED' >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "lib", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := "---\nid: plain\ndomain: misc\nmodel: sonnet\n---\n\n## Purpose\n\nx\n"
	if err := os.WriteFile(filepath.Join(root, "lib", "agents", "plain.md"), []byte(agent), 0o644); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(t.TempDir(), "w.yaml")
	yml := "version: 1\nname: dry\nnodes:\n" +
		"  - {id: a, agent: plain, runtime: auto, model: auto, prompt: hi, output_limit: 100}\n" +
		"  - {id: b, agent: plain, runtime: claude, prompt: hi, output_limit: 100}\n" +
		"  - {id: c, agent: nosuch, runtime: auto, prompt: hi, output_limit: 100}\n"
	if err := os.WriteFile(wfPath, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.Load(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := workflow.Validate(wf); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	ok := printWorkflowRoutePlan(context.Background(), &out, wf, root, t.TempDir())
	s := out.String()
	if ok {
		t.Error("an unroutable node must make the dry run report failure")
	}
	for _, want := range []string{
		"a ", "runtime=codex model=gpt-5.4 rule=R1 pinned=-",
		"runtime=claude", "pinned=runtime",
		"node \"c\"",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "DISPATCHED") {
		t.Error("dry run executed a runtime")
	}
	if entries, _ := os.ReadDir(os.Getenv("YAKOS_DISPATCH_LOG")); len(entries) != 0 {
		t.Error("dry run wrote a ledger event")
	}
}
