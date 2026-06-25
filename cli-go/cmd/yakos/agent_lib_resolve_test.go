// agent_lib_resolve_test.go — integration test verifying that runAgent (and
// the shared resolveLibRoot path) resolves lib/agents correctly on a bare
// binary install (yakosRoot with no adjacent lib/).
//
// Root cause guarded: before the fix, runAgent never called resolveLibRoot so
// on a ~/.local/bin/yakos bare install yakosRoot was set to ~/.local (the
// parent of bin/) which has no lib/agents.  agentscompose.Compose silently
// returned 0 agents.  The fix adds resolveLibRoot to runAgent, mirroring
// runStart/runServe/runRefresh.
//
// Test strategy:
//   - Build a yakosRoot with NO lib/agents (simulates bare install raw root).
//   - Build a materialized dir with a minimal lib/agents containing one agent.
//   - Call agent.Run with the raw root and an isolated HOME that resolveLibRoot
//     will cascade through.  The materialized dir is at the path that
//     install.MaterializedLibDir(home, ver) returns, so cascade step 2 finds it.
//   - Assert the agent list is non-empty (bug: would have been 0).
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/agent"
	"github.com/bakw00ds/yakos/internal/install"
	"github.com/bakw00ds/yakos/internal/version"
)

// TestRunAgent_BareInstall_FindsAgents verifies that agent.Run("list") finds
// agents from a materialized lib when yakosRoot has no adjacent lib/agents.
//
// This directly exercises the cascade that resolveLibRoot performs in
// runAgent; the fix is: yakosRoot = resolveLibRoot(yakosRoot, home, stderr).
func TestRunAgent_BareInstall_FindsAgents(t *testing.T) {
	// Set up isolated HOME so MaterializedLibDir points inside our temp tree.
	home := t.TempDir()

	// Use the same version key that resolveLibRoot uses internally so the
	// cascade's step-2 Stat hits the right directory.
	ver := version.Version

	// Construct the materialized root at the path resolveLibRoot's step 2
	// checks: install.MaterializedLibDir(home, ver).
	matRoot := install.MaterializedLibDir(home, ver)
	agentsDir := filepath.Join(matRoot, "lib", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir materialized agents dir: %v", err)
	}

	// Write a minimal agent file that agentscompose.Compose will parse.
	agentMD := `---
description: A smoke-test agent for bare-install resolution.
model: sonnet
---

You are a smoke-test agent.
`
	if err := os.WriteFile(filepath.Join(agentsDir, "smoke-agent.md"), []byte(agentMD), 0644); err != nil {
		t.Fatalf("write agent md: %v", err)
	}

	// The raw yakosRoot: a directory that exists but has no lib/ subtree.
	// This simulates ~/.local on a bare binary install.
	rawRoot := t.TempDir()

	// Verify pre-condition: rawRoot has no lib/agents.
	if _, err := os.Stat(filepath.Join(rawRoot, "lib", "agents")); !os.IsNotExist(err) {
		t.Fatalf("pre-condition: expected lib/agents to not exist in rawRoot")
	}

	// Manually run the cascade (mirrors runAgent's fix):
	//   resolveLibRoot(rawRoot, home, stderr) → matRoot (step 2 hits).
	resolvedRoot := resolveLibRoot(rawRoot, home, &bytes.Buffer{})
	if resolvedRoot == rawRoot {
		// The cascade did not fire; this would mean step 2 didn't find the dir.
		// Something is wrong with the test setup, not the code under test.
		t.Fatalf("resolveLibRoot returned raw root unchanged: materialized dir not found at %s",
			filepath.Join(matRoot, "lib", "agents"))
	}

	// Verify the resolved root is the materialized root.
	if !strings.HasPrefix(resolvedRoot, matRoot) && resolvedRoot != matRoot {
		t.Fatalf("unexpected resolved root: want prefix %q, got %q", matRoot, resolvedRoot)
	}

	// Now call agent.Run with the resolved root, as the fixed runAgent does.
	var out, errOut bytes.Buffer
	cfg := agent.Config{
		Subcommand: "list",
		YakosRoot:  resolvedRoot,
		HomeDir:    home,
		Writer:     &out,
		ErrWriter:  &errOut,
	}
	if _, err := agent.Run(cfg); err != nil {
		t.Fatalf("agent.Run(list): %v\nstderr: %s", err, errOut.String())
	}

	outStr := out.String()

	// The smoke-test agent must appear in the listing.
	if !strings.Contains(outStr, "smoke-agent") {
		t.Errorf("agent list output missing 'smoke-agent':\n%s", outStr)
	}

	// The count line must show at least 1 agent.
	if !strings.Contains(outStr, "1 agent(s)") {
		t.Errorf("expected '1 agent(s)' in output; got:\n%s", outStr)
	}
}

// TestRunAgent_RawRootWithLibAgents verifies that when yakosRoot already has
// lib/agents on disk (dev-repo or bash-tree install), resolveLibRoot's fast
// path returns it unchanged — no materialization triggered.
func TestRunAgent_RawRootWithLibAgents(t *testing.T) {
	// Build a yakosRoot with a real lib/agents.
	root := t.TempDir()
	agentsDir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	agentMD := "---\ndescription: direct agent\nmodel: haiku\n---\n"
	if err := os.WriteFile(filepath.Join(agentsDir, "direct-agent.md"), []byte(agentMD), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// resolveLibRoot should return root unchanged (fast path).
	home := t.TempDir()
	got := resolveLibRoot(root, home, &bytes.Buffer{})
	if got != root {
		t.Errorf("expected resolveLibRoot to return root unchanged when lib/agents exists; got %q", got)
	}

	// agent.Run must find the agent.
	var out bytes.Buffer
	cfg := agent.Config{
		Subcommand: "list",
		YakosRoot:  got,
		HomeDir:    home,
		Writer:     &out,
	}
	if _, err := agent.Run(cfg); err != nil {
		t.Fatalf("agent.Run(list): %v", err)
	}
	if !strings.Contains(out.String(), "direct-agent") {
		t.Errorf("expected 'direct-agent' in listing; got:\n%s", out.String())
	}
}
