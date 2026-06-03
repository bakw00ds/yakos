// mcp_parity_test.go — Phase 1 parity tests for `yakos mcp`.
//
// Design notes:
//
//  1. All tests call mcp.Run directly with temp-dir paths; no real python3
//     or network calls are required.
//
//  2. ExecPython is always injected so tests pass on CI without python3.
//
//  3. Parity is verified behaviourally (output shape, error conditions,
//     filesystem effects) rather than byte-for-byte.
//
// Critical scenarios:
//
//	(a) mcp entry in portedCommands
//	(b) install: fresh .mcp.json created
//	(c) install: existing file merged
//	(d) uninstall: entry removed
//	(e) status: entry presence reflected in output
//	(f) probe OK
//	(g) probe FAIL
//	(h) ParseArgs round-trip
//	(i) unknown subcommand → error
//	(j) help: key phrases in output
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/mcp"
)

// ---- scenario (a): portedCommands entry -------------------------------------

func TestMCPParity_PortedCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "mcp" {
			return
		}
	}
	t.Error("expected 'mcp' in portedCommands; not found")
}

// ---- helpers ----------------------------------------------------------------

func newMCPCfg(t *testing.T, sub string) mcp.Config {
	t.Helper()
	tmp := t.TempDir()
	return mcp.Config{
		Subcommand: sub,
		YakosRoot:  tmp,
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
		ExecPython: func() error { return nil },
		Now:        func() time.Time { return time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC) },
		CWD:        func() (string, error) { return tmp, nil },
	}
}

func mcpOut(cfg mcp.Config) string { return cfg.Writer.(*bytes.Buffer).String() }

func mcpCreateServerScript(t *testing.T, yakosRoot string) {
	t.Helper()
	dir := filepath.Join(yakosRoot, "cli", "lib", "mcp")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yakos-mcp-server.py"), []byte("# fake\n"), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}
}

// ---- scenario (b): install creates fresh .mcp.json -------------------------

func TestMCPParity_Install_FreshFile(t *testing.T) {
	cfg := newMCPCfg(t, "install")
	mcpCreateServerScript(t, cfg.YakosRoot)

	res, err := mcp.Run(cfg)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !res.EntryPresent {
		t.Error("expected EntryPresent=true")
	}

	data, err := os.ReadFile(res.MCPFile) //nolint:gosec
	if err != nil {
		t.Fatalf("read mcp.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if _, ok := servers["yakos-dispatch"]; !ok {
		t.Error("yakos-dispatch entry missing")
	}
}

// ---- scenario (c): install merges into existing file -----------------------

func TestMCPParity_Install_MergesOtherServers(t *testing.T) {
	cfg := newMCPCfg(t, "install")
	mcpCreateServerScript(t, cfg.YakosRoot)

	// Pre-create file with another server.
	mcpFile := filepath.Join(cfg.YakosRoot, ".mcp.json")
	existing := map[string]any{
		"mcpServers": map[string]any{
			"other": map[string]any{"command": "x"},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(mcpFile, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := mcp.Run(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}

	merged, _ := os.ReadFile(mcpFile) //nolint:gosec
	var doc map[string]any
	json.Unmarshal(merged, &doc) //nolint:errcheck
	servers, _ := doc["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Error("other server was not preserved")
	}
	if _, ok := servers["yakos-dispatch"]; !ok {
		t.Error("yakos-dispatch entry missing after merge")
	}
}

// ---- scenario (d): uninstall removes entry ---------------------------------

func TestMCPParity_Uninstall_RemovesEntry(t *testing.T) {
	// Install first.
	cfg := newMCPCfg(t, "install")
	mcpCreateServerScript(t, cfg.YakosRoot)
	if _, err := mcp.Run(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Uninstall.
	cfg2 := newMCPCfg(t, "uninstall")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.CWD = cfg.CWD

	res, err := mcp.Run(cfg2)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if res.EntryPresent {
		t.Error("expected EntryPresent=false after uninstall")
	}
	out := mcpOut(cfg2)
	if !strings.Contains(out, "removed") {
		t.Errorf("expected 'removed' in output; got %q", out)
	}
}

// ---- scenario (e): status entry presence -----------------------------------

func TestMCPParity_Status_EntryPresence(t *testing.T) {
	cfg := newMCPCfg(t, "install")
	mcpCreateServerScript(t, cfg.YakosRoot)
	if _, err := mcp.Run(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}

	cfg2 := newMCPCfg(t, "status")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.CWD = cfg.CWD

	res, err := mcp.Run(cfg2)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !res.EntryPresent {
		t.Error("expected EntryPresent=true")
	}
	out := mcpOut(cfg2)
	if !strings.Contains(out, "PRESENT") {
		t.Errorf("expected PRESENT in output; got %q", out)
	}
}

// ---- scenario (f): probe OK ------------------------------------------------

func TestMCPParity_Probe_OK(t *testing.T) {
	cfg := newMCPCfg(t, "probe")
	cfg.ExecPython = func() error { return nil }
	res, err := mcp.Run(cfg)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !res.PythonMCPImportable {
		t.Error("expected PythonMCPImportable=true")
	}
	out := mcpOut(cfg)
	if !strings.Contains(out, "OK") {
		t.Errorf("expected OK; got %q", out)
	}
}

// ---- scenario (g): probe FAIL ----------------------------------------------

func TestMCPParity_Probe_Fail(t *testing.T) {
	cfg := newMCPCfg(t, "probe")
	cfg.ExecPython = func() error { return errors.New("no python") }
	res, err := mcp.Run(cfg)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.PythonMCPImportable {
		t.Error("expected PythonMCPImportable=false")
	}
	out := mcpOut(cfg)
	if !strings.Contains(out, "FAIL") {
		t.Errorf("expected FAIL; got %q", out)
	}
}

// ---- scenario (h): ParseArgs round-trip ------------------------------------

func TestMCPParity_ParseArgs_RoundTrip(t *testing.T) {
	p, err := mcp.ParseArgs("install", []string{"--project", "/some/path"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if p != "/some/path" {
		t.Errorf("expected /some/path; got %q", p)
	}

	// Empty args = use CWD.
	p2, err := mcp.ParseArgs("status", nil)
	if err != nil {
		t.Fatalf("ParseArgs empty: %v", err)
	}
	if p2 != "" {
		t.Errorf("expected empty project; got %q", p2)
	}
}

// ---- scenario (i): unknown subcommand → error ------------------------------

func TestMCPParity_UnknownSubcommand_Error(t *testing.T) {
	cfg := newMCPCfg(t, "bogus")
	_, err := mcp.Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (j): help output --------------------------------------------

func TestMCPParity_Help_KeyPhrases(t *testing.T) {
	var buf bytes.Buffer
	mcp.PrintHelp(&buf)
	help := buf.String()
	for _, phrase := range []string{
		"yakos mcp",
		"install",
		"uninstall",
		"status",
		"probe",
		"yakos-dispatch",
		"--project",
	} {
		if !strings.Contains(help, phrase) {
			t.Errorf("help missing phrase %q", phrase)
		}
	}
}
