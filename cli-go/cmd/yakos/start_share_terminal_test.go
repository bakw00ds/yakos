// start_share_terminal_test.go — CLI-layer regression tests for --share-terminal
// and --direct flags.
//
// These tests were added in v0.49.0.1 to close the gap discovered in v0.49.0.0:
// the flags existed in start.Config and serve.Config but were NEVER wired into
// the CLI flag parsers (runStart / runServe).  Any test here that exercises a
// code path from the CLI layer (flag-parse → Config population) would have
// FAILED against the pre-fix code.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/start"
)

// ---- buildServeArgs ----------------------------------------------------------

// TestBuildServeArgs_ShareTerminalForwarded verifies that shareTerminal=true in
// serveArgsInput produces --share-terminal in the output args.
//
// This is a pure-function test; it would have FAILED before the fix because the
// serveArgsInput struct did not have a shareTerminal field and buildServeArgs
// never emitted --share-terminal.
func TestBuildServeArgs_ShareTerminalForwarded(t *testing.T) {
	got := buildServeArgs(serveArgsInput{shareTerminal: true})
	found := false
	for _, a := range got {
		if a == "--share-terminal" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("buildServeArgs with shareTerminal=true: --share-terminal not in output: %v", got)
	}
}

// TestBuildServeArgs_ShareTerminalNotForwardedWhenFalse verifies that
// shareTerminal=false produces NO --share-terminal flag (no noise).
func TestBuildServeArgs_ShareTerminalNotForwardedWhenFalse(t *testing.T) {
	got := buildServeArgs(serveArgsInput{})
	for _, a := range got {
		if a == "--share-terminal" {
			t.Errorf("buildServeArgs without shareTerminal: --share-terminal unexpectedly in output: %v", got)
		}
	}
}

// ---- printServeHelp ----------------------------------------------------------

// TestServeHelp_ContainsShareTerminal verifies that printServeHelp output
// mentions --share-terminal.  Would have FAILED before the help text was added.
func TestServeHelp_ContainsShareTerminal(t *testing.T) {
	var buf bytes.Buffer
	printServeHelp(&buf)
	if !strings.Contains(buf.String(), "--share-terminal") {
		t.Error("printServeHelp: output does not mention --share-terminal")
	}
}

// ---- start.PrintHelp ---------------------------------------------------------

// TestStartHelp_ContainsShareTerminal verifies that start.PrintHelp output
// mentions --share-terminal.  Would have FAILED before the help text was added.
func TestStartHelp_ContainsShareTerminal(t *testing.T) {
	var buf bytes.Buffer
	start.PrintHelp(&buf)
	if !strings.Contains(buf.String(), "--share-terminal") {
		t.Error("start.PrintHelp: output does not mention --share-terminal")
	}
}

// TestStartHelp_ContainsDirect verifies that start.PrintHelp output
// mentions --direct.  Would have FAILED before the help text was added.
func TestStartHelp_ContainsDirect(t *testing.T) {
	var buf bytes.Buffer
	start.PrintHelp(&buf)
	if !strings.Contains(buf.String(), "--direct") {
		t.Error("start.PrintHelp: output does not mention --direct")
	}
}

// ---- binary acceptance (requires built binary) --------------------------------

// TestStartFlags_ShareTerminalAndDirectAccepted verifies that the runStart flag
// parser does NOT reject --share-terminal or --direct.
//
// This test would have FAILED against the pre-fix code because the flags were
// unrecognized: runStart's switch fell through to:
//
//	fmt.Fprintf(os.Stderr, "start: unknown flag %q (try --help)\n", arg)
//	os.Exit(1)
func TestStartFlags_ShareTerminalAndDirectAccepted(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q (run 'make build' first): %v", goBin, err)
	}

	home, _ := setupStartProject(t, "sharetermtest")
	extraEnv := map[string]string{"HOME": home}

	// --share-terminal --direct --dry-run: must exit 0, not "unknown flag".
	out, exitCode := runGoStart(t, goBin,
		[]string{"start", "sharetermtest", "--share-terminal", "--direct", "--dry-run", "--no-agents"},
		extraEnv)
	if exitCode != 0 {
		t.Fatalf("start --share-terminal --direct --dry-run: expected exit 0; got %d\noutput:\n%s",
			exitCode, out)
	}
	if strings.Contains(out, "unknown flag") {
		t.Errorf("start --share-terminal --direct --dry-run: output contains 'unknown flag':\n%s", out)
	}
}

// TestStartFlags_ShareTerminalAloneAccepted verifies --share-terminal alone is accepted.
func TestStartFlags_ShareTerminalAloneAccepted(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q (run 'make build' first): %v", goBin, err)
	}

	home, _ := setupStartProject(t, "sharetermtest2")
	extraEnv := map[string]string{"HOME": home}

	out, exitCode := runGoStart(t, goBin,
		[]string{"start", "sharetermtest2", "--share-terminal", "--dry-run", "--no-agents"},
		extraEnv)
	if exitCode != 0 {
		t.Fatalf("start --share-terminal --dry-run: expected exit 0; got %d\noutput:\n%s",
			exitCode, out)
	}
	if strings.Contains(out, "unknown flag") {
		t.Errorf("start --share-terminal --dry-run: output contains 'unknown flag':\n%s", out)
	}
}

// TestServeFlags_ShareTerminalAccepted verifies that runServe's flag parser does
// NOT reject --share-terminal.
//
// This test would have FAILED against the pre-fix code because --share-terminal
// was not in runServe's switch, falling through to:
//
//	fmt.Fprintf(os.Stderr, "serve: unknown flag %q (try --help)\n", args[i])
//	os.Exit(1)
func TestServeFlags_ShareTerminalAccepted(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q (run 'make build' first): %v", goBin, err)
	}

	// `yakos serve --share-terminal --help` must exit 0 and not error.
	// Using --help so the serve command exits immediately without binding.
	out, exitCode := runGoStart(t, goBin,
		[]string{"serve", "--share-terminal", "--help"},
		map[string]string{})
	if exitCode != 0 {
		t.Fatalf("serve --share-terminal --help: expected exit 0; got %d\noutput:\n%s",
			exitCode, out)
	}
	if strings.Contains(out, "unknown flag") {
		t.Errorf("serve --share-terminal --help: output contains 'unknown flag':\n%s", out)
	}
	// Help output should mention --share-terminal.
	if !strings.Contains(out, "--share-terminal") {
		t.Errorf("serve --help: --share-terminal not in help output:\n%s", out)
	}
}
