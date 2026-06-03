// Package mcp tests — Phase 1 parity coverage for `yakos mcp`.
//
// Design:
//   - No real python3 or filesystem side-effects beyond t.TempDir().
//   - ExecPython is always injected so tests pass on CI without python3.
//   - Atomic write (temp-rename) is exercised by verifying both the output
//     file and that no .tmp artifact is left behind.
//
// Scenarios:
//
//	(a) install: create fresh .mcp.json when none exists
//	(b) install: merge into existing valid .mcp.json
//	(c) install: existing entry refreshed (idempotent)
//	(d) install: reject invalid JSON in existing file
//	(e) install: error when server script missing
//	(f) install: backup file created when merging
//	(g) uninstall: entry removed
//	(h) uninstall: no-op when .mcp.json absent
//	(i) uninstall: no-op when entry not present
//	(j) uninstall: error when existing JSON invalid
//	(k) status: entry present
//	(l) status: .mcp.json absent
//	(m) status: entry not present but file exists
//	(n) probe: OK path
//	(o) probe: FAIL path
//	(p) unknown subcommand → error
//	(q) help subcommand → no error, help printed
//	(r) ParseArgs: --project flag
//	(s) ParseArgs: --project= form
//	(t) ParseArgs: unknown flag → error
//	(u) ParseArgs: unexpected positional arg → error
//	(v) GlobalMCPFile on non-Windows returns ~/.claude/mcp.json
//	(w) status: python importable reflected in result
package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- test helpers -----------------------------------------------------------

var fixedNow = time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

func pythonOK() error  { return nil }
func pythonFail() error { return errors.New("python not found") }

func newCfg(t *testing.T, sub string) Config {
	t.Helper()
	tmp := t.TempDir()
	return Config{
		Subcommand: sub,
		YakosRoot:  tmp, // server script won't exist by default
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
		ExecPython: pythonOK,
		Now:        func() time.Time { return fixedNow },
		CWD:        func() (string, error) { return tmp, nil },
	}
}

// createServerScript creates the fake server script tree so install can succeed.
func createServerScript(t *testing.T, yakosRoot string) string {
	t.Helper()
	dir := filepath.Join(yakosRoot, "cli", "lib", "mcp")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir server script dir: %v", err)
	}
	script := filepath.Join(dir, "yakos-mcp-server.py")
	if err := os.WriteFile(script, []byte("# fake\n"), 0644); err != nil {
		t.Fatalf("write server script: %v", err)
	}
	return script
}

func cfgOut(cfg Config) string { return cfg.Writer.(*bytes.Buffer).String() }

// readJSON reads and parses a mcpDoc from path.
func readMCPDoc(t *testing.T, path string) mcpDoc {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatalf("readMCPDoc: %v", err)
	}
	var doc mcpDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal mcpDoc: %v", err)
	}
	return doc
}

// ---- scenario (a): install creates fresh file -------------------------------

func TestMCPParity_Install_FreshFile(t *testing.T) {
	cfg := newCfg(t, "install")
	createServerScript(t, cfg.YakosRoot)

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run install: %v", err)
	}
	if !res.EntryPresent {
		t.Error("expected EntryPresent=true after fresh install")
	}

	doc := readMCPDoc(t, res.MCPFile)
	if _, ok := doc.MCPServers[ServerName]; !ok {
		t.Errorf("yakos-dispatch entry missing in %s", res.MCPFile)
	}

	// No .tmp artifact left behind.
	if _, err := os.Stat(res.MCPFile + ".tmp"); err == nil {
		t.Errorf("tmp file left behind at %s.tmp", res.MCPFile)
	}

	out := cfgOut(cfg)
	if !strings.Contains(out, "wrote") {
		t.Errorf("expected 'wrote' in output; got %q", out)
	}
}

// ---- scenario (b): install merges into existing file -----------------------

func TestMCPParity_Install_MergesExisting(t *testing.T) {
	cfg := newCfg(t, "install")
	createServerScript(t, cfg.YakosRoot)

	// Pre-create .mcp.json with a different server.
	mcpFile := filepath.Join(cfg.YakosRoot, ".mcp.json")
	existing := mcpDoc{
		MCPServers: map[string]mcpEntry{
			"other-server": {Command: "other", Args: []string{"arg"}},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(mcpFile, data, 0644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run install: %v", err)
	}

	doc := readMCPDoc(t, res.MCPFile)
	if _, ok := doc.MCPServers[ServerName]; !ok {
		t.Error("yakos-dispatch entry missing after merge")
	}
	if _, ok := doc.MCPServers["other-server"]; !ok {
		t.Error("other-server was not preserved after merge")
	}
}

// ---- scenario (c): install: existing entry refreshed -----------------------

func TestMCPParity_Install_RefreshesExistingEntry(t *testing.T) {
	cfg := newCfg(t, "install")
	createServerScript(t, cfg.YakosRoot)

	// First install.
	if _, err := Run(cfg); err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Second install (refresh).
	cfg2 := newCfg(t, "install")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.CWD = cfg.CWD

	res, err := Run(cfg2)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if !res.EntryPresent {
		t.Error("expected EntryPresent=true after refresh")
	}
}

// ---- scenario (d): install rejects invalid JSON ----------------------------

func TestMCPParity_Install_InvalidJSONRejects(t *testing.T) {
	cfg := newCfg(t, "install")
	createServerScript(t, cfg.YakosRoot)

	mcpFile := filepath.Join(cfg.YakosRoot, ".mcp.json")
	if err := os.WriteFile(mcpFile, []byte("not json {{{"), 0644); err != nil {
		t.Fatalf("write bad json: %v", err)
	}

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (e): install errors when server script missing ---------------

func TestMCPParity_Install_MissingScript_Error(t *testing.T) {
	cfg := newCfg(t, "install")
	// Do NOT create server script.

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing server script")
	}
	if !strings.Contains(err.Error(), "server script missing") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (f): install creates backup when merging ---------------------

func TestMCPParity_Install_BackupCreated(t *testing.T) {
	cfg := newCfg(t, "install")
	createServerScript(t, cfg.YakosRoot)

	mcpFile := filepath.Join(cfg.YakosRoot, ".mcp.json")
	existing := mcpDoc{MCPServers: map[string]mcpEntry{"x": {Command: "x"}}}
	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(mcpFile, data, 0644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	// There should be a backup file matching .yakos-bak- pattern.
	entries, _ := os.ReadDir(cfg.YakosRoot)
	found := false
	for _, e := range entries {
		if strings.Contains(e.Name(), ".yakos-bak-") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected backup file with .yakos-bak- in name")
	}
}

// ---- scenario (g): uninstall removes entry ---------------------------------

func TestMCPParity_Uninstall_RemovesEntry(t *testing.T) {
	cfg := newCfg(t, "install")
	createServerScript(t, cfg.YakosRoot)

	if _, err := Run(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}

	cfg2 := newCfg(t, "uninstall")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.CWD = cfg.CWD

	res, err := Run(cfg2)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if res.EntryPresent {
		t.Error("expected EntryPresent=false after uninstall")
	}

	doc := readMCPDoc(t, res.MCPFile)
	if _, ok := doc.MCPServers[ServerName]; ok {
		t.Error("yakos-dispatch entry still present after uninstall")
	}
}

// ---- scenario (h): uninstall is no-op when file absent --------------------

func TestMCPParity_Uninstall_NoFile_Noop(t *testing.T) {
	cfg := newCfg(t, "uninstall")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("uninstall (no file): %v", err)
	}
	if res.EntryPresent {
		t.Error("expected EntryPresent=false")
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "nothing to do") {
		t.Errorf("expected 'nothing to do'; got %q", out)
	}
}

// ---- scenario (i): uninstall is no-op when entry absent -------------------

func TestMCPParity_Uninstall_EntryAbsent_Noop(t *testing.T) {
	cfg := newCfg(t, "uninstall")

	mcpFile := filepath.Join(cfg.YakosRoot, ".mcp.json")
	doc := mcpDoc{MCPServers: map[string]mcpEntry{"other": {Command: "other"}}}
	data, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(mcpFile, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if res.EntryPresent {
		t.Error("expected EntryPresent=false")
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "not present") {
		t.Errorf("expected 'not present'; got %q", out)
	}
}

// ---- scenario (j): uninstall errors on invalid JSON -----------------------

func TestMCPParity_Uninstall_InvalidJSON_Error(t *testing.T) {
	cfg := newCfg(t, "uninstall")

	mcpFile := filepath.Join(cfg.YakosRoot, ".mcp.json")
	if err := os.WriteFile(mcpFile, []byte("not json"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (k): status shows entry present ------------------------------

func TestMCPParity_Status_EntryPresent(t *testing.T) {
	cfg := newCfg(t, "install")
	createServerScript(t, cfg.YakosRoot)
	if _, err := Run(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}

	cfg2 := newCfg(t, "status")
	cfg2.YakosRoot = cfg.YakosRoot
	cfg2.CWD = cfg.CWD

	res, err := Run(cfg2)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !res.EntryPresent {
		t.Error("expected EntryPresent=true")
	}
	out := cfgOut(cfg2)
	if !strings.Contains(out, "PRESENT") {
		t.Errorf("expected PRESENT in output; got %q", out)
	}
}

// ---- scenario (l): status with no .mcp.json -------------------------------

func TestMCPParity_Status_NoFile(t *testing.T) {
	cfg := newCfg(t, "status")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if res.EntryPresent {
		t.Error("expected EntryPresent=false")
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "not present") {
		t.Errorf("expected 'not present'; got %q", out)
	}
}

// ---- scenario (m): status with file but entry absent ----------------------

func TestMCPParity_Status_FileExistsEntryAbsent(t *testing.T) {
	cfg := newCfg(t, "status")

	mcpFile := filepath.Join(cfg.YakosRoot, ".mcp.json")
	doc := mcpDoc{MCPServers: map[string]mcpEntry{"other": {Command: "x"}}}
	data, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(mcpFile, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if res.EntryPresent {
		t.Error("expected EntryPresent=false")
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "NOT INSTALLED") {
		t.Errorf("expected NOT INSTALLED; got %q", out)
	}
}

// ---- scenario (n): probe OK ------------------------------------------------

func TestMCPParity_Probe_OK(t *testing.T) {
	cfg := newCfg(t, "probe")
	cfg.ExecPython = pythonOK
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("probe OK: %v", err)
	}
	if !res.PythonMCPImportable {
		t.Error("expected PythonMCPImportable=true")
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "OK") {
		t.Errorf("expected OK in output; got %q", out)
	}
}

// ---- scenario (o): probe FAIL ----------------------------------------------

func TestMCPParity_Probe_Fail(t *testing.T) {
	cfg := newCfg(t, "probe")
	cfg.ExecPython = pythonFail
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("probe FAIL: %v", err)
	}
	if res.PythonMCPImportable {
		t.Error("expected PythonMCPImportable=false")
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "FAIL") {
		t.Errorf("expected FAIL in output; got %q", out)
	}
	if !strings.Contains(out, "pip install mcp") {
		t.Errorf("expected install hint; got %q", out)
	}
}

// ---- scenario (p): unknown subcommand → error ------------------------------

func TestMCPParity_UnknownSubcommand_Error(t *testing.T) {
	cfg := newCfg(t, "bogus")
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (q): help subcommand ----------------------------------------

func TestMCPParity_Help_NoError(t *testing.T) {
	cfg := newCfg(t, "help")
	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	out := cfgOut(cfg)
	for _, phrase := range []string{"install", "uninstall", "status", "probe", "yakos-dispatch"} {
		if !strings.Contains(out, phrase) {
			t.Errorf("help missing phrase %q; got: %q", phrase, out)
		}
	}
}

// ---- scenario (r): ParseArgs --project flag --------------------------------

func TestMCPParity_ParseArgs_ProjectFlag(t *testing.T) {
	proj, err := ParseArgs("install", []string{"--project", "/some/path"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if proj != "/some/path" {
		t.Errorf("expected /some/path; got %q", proj)
	}
}

// ---- scenario (s): ParseArgs --project= form -------------------------------

func TestMCPParity_ParseArgs_ProjectEquals(t *testing.T) {
	proj, err := ParseArgs("status", []string{"--project=/tmp/foo"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if proj != "/tmp/foo" {
		t.Errorf("expected /tmp/foo; got %q", proj)
	}
}

// ---- scenario (t): ParseArgs unknown flag → error --------------------------

func TestMCPParity_ParseArgs_UnknownFlag_Error(t *testing.T) {
	_, err := ParseArgs("install", []string{"--bogus"})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (u): ParseArgs unexpected positional ------------------------

func TestMCPParity_ParseArgs_UnexpectedPositional_Error(t *testing.T) {
	_, err := ParseArgs("install", []string{"somedir"})
	if err == nil {
		t.Fatal("expected error for unexpected positional")
	}
	if !strings.Contains(err.Error(), "unexpected arg") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (v): GlobalMCPFile -------------------------------------------

func TestMCPParity_GlobalMCPFile_Unix(t *testing.T) {
	path := GlobalMCPFile("/home/user")
	if !strings.Contains(path, ".claude") || !strings.HasSuffix(path, "mcp.json") {
		t.Errorf("unexpected global path: %q", path)
	}
}

// ---- scenario (w): status python result reflected in Result ---------------

func TestMCPParity_Status_PythonReflectedInResult(t *testing.T) {
	cfg := newCfg(t, "status")
	cfg.ExecPython = pythonFail
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if res.PythonMCPImportable {
		t.Error("expected PythonMCPImportable=false when python fails")
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "MISSING") {
		t.Errorf("expected MISSING in status output; got %q", out)
	}
}

// ---- PrintHelp coverage ---------------------------------------------------

func TestMCPParity_PrintHelp_Phrases(t *testing.T) {
	var buf bytes.Buffer
	PrintHelp(&buf)
	help := buf.String()
	for _, phrase := range []string{
		"yakos mcp",
		"install",
		"uninstall",
		"status",
		"probe",
		"yakos-dispatch",
		"docs/mcp-integration.md",
		"--project",
	} {
		if !strings.Contains(help, phrase) {
			t.Errorf("help missing phrase %q", phrase)
		}
	}
}
