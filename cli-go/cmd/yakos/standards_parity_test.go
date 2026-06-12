// standards_parity_test.go — Phase 1 parity tests for `yakos standards` (rank 30).
//
// Design notes:
//
//  1. All tests call standards.Run directly with temp-dir paths and stub
//     PromptFn; no real user input or filesystem side-effects beyond temp dirs.
//
//  2. The bash standards.sh manages Plan 4 cross-project standards opt-ins:
//     list    — show all 6 standards + state
//     enable  — set profile.standards.<name> = true
//     disable — set profile.standards.<name> = false
//     check   — preview what active standards catch
//     init    — interactive profile + standards selection
//
//  3. Parity is verified behaviourally (output shape, filesystem effects,
//     error conditions) rather than byte-for-byte.
//
// Critical scenarios:
//
//	(a) list: shows all 6 standards
//	(b) list: enabled/disabled states reflected correctly
//	(c) enable: sets standard to true in .yakos.yml
//	(d) disable: sets standard to false in .yakos.yml
//	(e) enable: unknown standard → error
//	(f) check: no enabled → advisory + enable hint
//	(g) check: enabled → playbook lines printed
//	(h) init: valid type → profile.type updated
//	(i) init: invalid type → error
//	(j) init: accept suggested → suggested standards enabled
//	(k) init: decline suggested → not enabled
//	(l) unknown subcommand → error with hint
//	(m) help text key phrases
//	(n) standards entry in portedCommands
//	(o) enable: atomic write (no stray .tmp)
//	(p) list: suggested column reflects profile type
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/standards"
)

// ---- helpers ----------------------------------------------------------------

func newStdCfg(t *testing.T, sub string) standards.Config {
	t.Helper()
	return standards.Config{
		Subcommand: sub,
		ProjectDir: t.TempDir(),
		HomeDir:    t.TempDir(),
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
}

func stdOut2(cfg standards.Config) string { return cfg.Writer.(*bytes.Buffer).String() }

func writeStdYML(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(content), 0644); err != nil {
		t.Fatalf("write .yakos.yml: %v", err)
	}
}

func readStdYMLMap(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}

func stdPrompts(responses ...string) func(string) (string, error) {
	i := 0
	return func(p string) (string, error) {
		if i >= len(responses) {
			return "", nil
		}
		r := responses[i]
		i++
		return r, nil
	}
}

// ---- scenario (a): list shows all 6 standards --------------------------------

func TestStdParity_List_All6Standards(t *testing.T) {
	cfg := newStdCfg(t, "list")
	writeStdYML(t, cfg.ProjectDir, "yakos: 0.9\n")
	_, err := standards.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := stdOut2(cfg)
	for _, name := range standards.KnownStandards() {
		if !strings.Contains(o, name) {
			t.Errorf("expected %q in list output; got: %q", name, o)
		}
	}
}

// ---- scenario (b): list enabled/disabled states ----------------------------

func TestStdParity_List_States(t *testing.T) {
	cfg := newStdCfg(t, "list")
	writeStdYML(t, cfg.ProjectDir, "profile:\n  standards:\n    logging: true\n    monitors: false\n")
	res, err := standards.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.States["logging"] != "enabled" {
		t.Errorf("expected logging=enabled; got %q", res.States["logging"])
	}
	if res.States["monitors"] != "disabled" {
		t.Errorf("expected monitors=disabled; got %q", res.States["monitors"])
	}
}

// ---- scenario (c): enable sets standard to true ----------------------------

func TestStdParity_Enable_SetsTrue(t *testing.T) {
	cfg := newStdCfg(t, "enable")
	cfg.StandardName = "feedback"
	writeStdYML(t, cfg.ProjectDir, "yakos: 0.9\n")
	if _, err := standards.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	doc := readStdYMLMap(t, filepath.Join(cfg.ProjectDir, ".yakos.yml"))
	p, _ := doc["profile"].(map[string]interface{})
	s, _ := p["standards"].(map[string]interface{})
	if s["feedback"] != true {
		t.Errorf("expected feedback=true; got %v", s["feedback"])
	}
}

// ---- scenario (d): disable sets standard to false --------------------------

func TestStdParity_Disable_SetsFalse(t *testing.T) {
	cfg := newStdCfg(t, "disable")
	cfg.StandardName = "changelog-ui"
	writeStdYML(t, cfg.ProjectDir, "profile:\n  standards:\n    changelog-ui: true\n")
	if _, err := standards.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	doc := readStdYMLMap(t, filepath.Join(cfg.ProjectDir, ".yakos.yml"))
	p, _ := doc["profile"].(map[string]interface{})
	s, _ := p["standards"].(map[string]interface{})
	if s["changelog-ui"] != false {
		t.Errorf("expected changelog-ui=false; got %v", s["changelog-ui"])
	}
}

// ---- scenario (e): enable unknown standard → error --------------------------

func TestStdParity_Enable_UnknownError(t *testing.T) {
	cfg := newStdCfg(t, "enable")
	cfg.StandardName = "not-a-real-standard"
	writeStdYML(t, cfg.ProjectDir, "yakos: 0.9\n")
	_, err := standards.Run(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown standard") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (f): check no enabled → advisory ------------------------------

func TestStdParity_Check_NoEnabled(t *testing.T) {
	cfg := newStdCfg(t, "check")
	writeStdYML(t, cfg.ProjectDir, "profile:\n  standards:\n    logging: false\n")
	res, err := standards.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.ActiveStandards) != 0 {
		t.Errorf("expected no active standards; got %v", res.ActiveStandards)
	}
	o := stdOut2(cfg)
	if !strings.Contains(o, "no standards enabled") {
		t.Errorf("expected advisory; got: %q", o)
	}
	if !strings.Contains(o, "yakos standards enable") {
		t.Errorf("expected enable hint; got: %q", o)
	}
}

// ---- scenario (g): check enabled → playbook info ---------------------------

func TestStdParity_Check_Enabled(t *testing.T) {
	cfg := newStdCfg(t, "check")
	writeStdYML(t, cfg.ProjectDir, "profile:\n  standards:\n    architecture-viz: true\n    about-page: true\n")
	res, err := standards.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.ActiveStandards) != 2 {
		t.Fatalf("expected 2 active; got %d", len(res.ActiveStandards))
	}
	o := stdOut2(cfg)
	if !strings.Contains(o, "== architecture-viz ==") {
		t.Errorf("expected '== architecture-viz ==' in output; got: %q", o)
	}
	if !strings.Contains(o, "playbook:") || !strings.Contains(o, "detects:") || !strings.Contains(o, "scaffold:") {
		t.Errorf("expected playbook/detects/scaffold in output; got: %q", o)
	}
}

// ---- scenario (h): init valid type → profile.type updated ------------------

func TestStdParity_Init_ValidType(t *testing.T) {
	cfg := newStdCfg(t, "init")
	writeStdYML(t, cfg.ProjectDir, "yakos: 0.9\n")
	cfg.PromptFn = stdPrompts("cli-tool", "n")
	res, err := standards.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.TypeSet {
		t.Errorf("expected TypeSet=true")
	}
	doc := readStdYMLMap(t, filepath.Join(cfg.ProjectDir, ".yakos.yml"))
	p, _ := doc["profile"].(map[string]interface{})
	if p["type"] != "cli-tool" {
		t.Errorf("expected type=cli-tool; got %v", p["type"])
	}
}

// ---- scenario (i): init invalid type → error --------------------------------

func TestStdParity_Init_InvalidType(t *testing.T) {
	cfg := newStdCfg(t, "init")
	writeStdYML(t, cfg.ProjectDir, "yakos: 0.9\n")
	cfg.PromptFn = stdPrompts("badtype")
	_, err := standards.Run(cfg)
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (j): init accept suggested → standards enabled ----------------

func TestStdParity_Init_AcceptSuggested(t *testing.T) {
	cfg := newStdCfg(t, "init")
	writeStdYML(t, cfg.ProjectDir, "yakos: 0.9\n")
	cfg.PromptFn = stdPrompts("data-pipeline", "y")
	if _, err := standards.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	doc := readStdYMLMap(t, filepath.Join(cfg.ProjectDir, ".yakos.yml"))
	p, _ := doc["profile"].(map[string]interface{})
	s, _ := p["standards"].(map[string]interface{})
	// data-pipeline suggested: logging, monitors.
	for _, name := range []string{"logging", "monitors"} {
		if s[name] != true {
			t.Errorf("expected %q enabled; got %v", name, s[name])
		}
	}
}

// ---- scenario (k): init decline suggested → not enabled ---------------------

func TestStdParity_Init_DeclineSuggested(t *testing.T) {
	cfg := newStdCfg(t, "init")
	writeStdYML(t, cfg.ProjectDir, "yakos: 0.9\n")
	cfg.PromptFn = stdPrompts("library", "n")
	if _, err := standards.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	doc := readStdYMLMap(t, filepath.Join(cfg.ProjectDir, ".yakos.yml"))
	p, _ := doc["profile"].(map[string]interface{})
	s, _ := p["standards"].(map[string]interface{})
	for _, name := range []string{"architecture-viz", "about-page"} {
		if v, ok := s[name]; ok && v == true {
			t.Errorf("expected %q not enabled after decline; got %v", name, v)
		}
	}
}

// ---- scenario (l): unknown subcommand → error with hint --------------------

func TestStdParity_UnknownSubcommand(t *testing.T) {
	cfg := newStdCfg(t, "nope")
	_, err := standards.Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "yakos standards help") {
		t.Errorf("expected help hint; got: %v", err)
	}
}

// ---- scenario (m): help text key phrases ------------------------------------

func TestStdParity_HelpText(t *testing.T) {
	var buf bytes.Buffer
	standards.PrintHelp(&buf)
	help := buf.String()
	for _, phrase := range []string{
		"yakos standards",
		"list", "enable", "disable", "check", "init",
		"logging", "changelog-ui", "monitors",
		"feedback", "architecture-viz", "about-page",
	} {
		if !strings.Contains(help, phrase) {
			t.Errorf("help missing %q; got:\n%s", phrase, help)
		}
	}
}

// ---- scenario (n): standards entry in portedCommands -------------------------

func TestStdParity_PortedCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "standards" {
			return
		}
	}
	t.Error("expected 'standards' in portedCommands; not found")
}

// ---- scenario (o): enable atomic write (no stray .tmp) ----------------------

func TestStdParity_Enable_AtomicWrite(t *testing.T) {
	cfg := newStdCfg(t, "enable")
	cfg.StandardName = "monitors"
	writeStdYML(t, cfg.ProjectDir, "yakos: 0.9\n")
	if _, err := standards.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ymlPath := filepath.Join(cfg.ProjectDir, ".yakos.yml")
	if _, err := os.Stat(ymlPath + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("stray .tmp file should not exist after atomic write")
	}
}

// ---- scenario (p): list suggested column reflects profile type --------------

func TestStdParity_List_SuggestedColumn(t *testing.T) {
	cfg := newStdCfg(t, "list")
	writeStdYML(t, cfg.ProjectDir, "profile:\n  type: service\n")
	res, err := standards.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Service suggested: logging, monitors, architecture-viz.
	if !res.Suggested["logging"] {
		t.Errorf("expected logging suggested for service type")
	}
	if !res.Suggested["monitors"] {
		t.Errorf("expected monitors suggested for service type")
	}
	if !res.Suggested["architecture-viz"] {
		t.Errorf("expected architecture-viz suggested for service type")
	}
	// Not suggested for service.
	if res.Suggested["changelog-ui"] {
		t.Errorf("expected changelog-ui NOT suggested for service type")
	}
	if res.Suggested["feedback"] {
		t.Errorf("expected feedback NOT suggested for service type")
	}
}
