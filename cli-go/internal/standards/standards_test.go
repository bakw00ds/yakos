// Package standards_test holds unit tests for the standards port (rank 30).
//
// All tests use temp directories and injectable fns.
//
// Critical scenarios:
//
//	(a) list: .yakos.yml missing → error
//	(b) list: empty .yakos.yml → all disabled, no suggested
//	(c) list: web-app type → 6 suggested, correct states
//	(d) list: mixed enabled/disabled states shown correctly
//	(e) enable: unknown standard → error with hint
//	(f) enable: missing .yakos.yml → error
//	(g) enable: sets standard to true atomically
//	(h) enable: preserves other fields in .yakos.yml
//	(i) disable: sets standard to false atomically
//	(j) disable: no stray .tmp file after write
//	(k) check: no standards enabled → advisory
//	(l) check: enabled standards → playbook info printed
//	(m) check: only enabled standards appear in output
//	(n) init: .yakos.yml missing → error
//	(o) init: invalid type → error
//	(p) init: valid type → profile.type set + suggested offered
//	(q) init: declined suggested → nothing enabled
//	(r) init: accepted suggested → all suggested enabled
//	(s) unknown subcommand → error with hint
//	(t) help text contains key phrases
//	(u) all 6 standards appear in list output
//	(v) enable then disable → final state is false
package standards_test

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

func newCfg(t *testing.T, sub string) standards.Config {
	t.Helper()
	return standards.Config{
		Subcommand: sub,
		ProjectDir: t.TempDir(),
		HomeDir:    t.TempDir(),
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
}

func stdOut(cfg standards.Config) string { return cfg.Writer.(*bytes.Buffer).String() }

func writeYML(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(content), 0644); err != nil {
		t.Fatalf("write .yakos.yml: %v", err)
	}
}

func readYMLFile(t *testing.T, path string) map[string]interface{} {
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

// stubPrompts returns a PromptFn that feeds responses in order.
func stubPrompts(responses ...string) func(string) (string, error) {
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

const baseYML = `yakos: 0.9
profile:
  type: web-app
  standards:
    logging: true
    changelog-ui: false
    monitors: true
    feedback: false
    architecture-viz: false
    about-page: true
`

// ---- scenario (a): list .yakos.yml missing → error --------------------------

func TestStandardsParity_List_MissingYML_Error(t *testing.T) {
	cfg := newCfg(t, "list")
	_, err := standards.Run(cfg)
	if err == nil {
		t.Fatal("expected error when .yakos.yml missing")
	}
	if !strings.Contains(err.Error(), ".yakos.yml") {
		t.Errorf("expected .yakos.yml mention; got: %v", err)
	}
}

// ---- scenario (b): list empty .yakos.yml → all disabled ---------------------

func TestStandardsParity_List_Empty_AllDisabled(t *testing.T) {
	cfg := newCfg(t, "list")
	writeYML(t, cfg.ProjectDir, "yakos: 0.9\n")
	res, err := standards.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, name := range standards.KnownStandards() {
		if res.States[name] != "disabled" {
			t.Errorf("expected %q disabled; got %q", name, res.States[name])
		}
	}
}

// ---- scenario (c): list web-app type → 6 suggested --------------------------

func TestStandardsParity_List_WebApp_SixSuggested(t *testing.T) {
	cfg := newCfg(t, "list")
	writeYML(t, cfg.ProjectDir, "profile:\n  type: web-app\n")
	res, err := standards.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	count := 0
	for _, s := range standards.KnownStandards() {
		if res.Suggested[s] {
			count++
		}
	}
	if count != 6 {
		t.Errorf("expected 6 suggested for web-app; got %d", count)
	}
}

// ---- scenario (d): list mixed states shown correctly ------------------------

func TestStandardsParity_List_MixedStates(t *testing.T) {
	cfg := newCfg(t, "list")
	writeYML(t, cfg.ProjectDir, baseYML)
	res, err := standards.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.States["logging"] != "enabled" {
		t.Errorf("expected logging=enabled; got %q", res.States["logging"])
	}
	if res.States["changelog-ui"] != "disabled" {
		t.Errorf("expected changelog-ui=disabled; got %q", res.States["changelog-ui"])
	}
	o := stdOut(cfg)
	if !strings.Contains(o, "enabled") {
		t.Errorf("expected 'enabled' in output; got: %q", o)
	}
	if !strings.Contains(o, "disabled") {
		t.Errorf("expected 'disabled' in output; got: %q", o)
	}
}

// ---- scenario (e): enable unknown standard → error --------------------------

func TestStandardsParity_Enable_UnknownStandard_Error(t *testing.T) {
	cfg := newCfg(t, "enable")
	cfg.StandardName = "not-a-real-standard"
	writeYML(t, cfg.ProjectDir, "yakos: 0.9\n")
	_, err := standards.Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown standard")
	}
	if !strings.Contains(err.Error(), "unknown standard") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (f): enable missing .yakos.yml → error -----------------------

func TestStandardsParity_Enable_MissingYML_Error(t *testing.T) {
	cfg := newCfg(t, "enable")
	cfg.StandardName = "logging"
	_, err := standards.Run(cfg)
	if err == nil {
		t.Fatal("expected error when .yakos.yml missing")
	}
}

// ---- scenario (g): enable sets standard to true atomically ------------------

func TestStandardsParity_Enable_SetsTrue(t *testing.T) {
	cfg := newCfg(t, "enable")
	cfg.StandardName = "monitors"
	writeYML(t, cfg.ProjectDir, "yakos: 0.9\nprofile:\n  standards:\n    monitors: false\n")
	res, err := standards.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.StandardToggled != "monitors" {
		t.Errorf("expected StandardToggled='monitors'; got %q", res.StandardToggled)
	}

	ymlPath := filepath.Join(cfg.ProjectDir, ".yakos.yml")
	doc := readYMLFile(t, ymlPath)
	p, _ := doc["profile"].(map[string]interface{})
	s, _ := p["standards"].(map[string]interface{})
	if s["monitors"] != true {
		t.Errorf("expected monitors=true in .yakos.yml; got %v", s["monitors"])
	}

	// No stray .tmp file.
	if _, err := os.Stat(ymlPath + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("stray .tmp file should not exist after atomic write")
	}
}

// ---- scenario (h): enable preserves other fields ----------------------------

func TestStandardsParity_Enable_PreservesOtherFields(t *testing.T) {
	cfg := newCfg(t, "enable")
	cfg.StandardName = "feedback"
	content := "yakos: 0.9\ncustom_key: preserved\nprofile:\n  type: service\n  standards:\n    logging: true\n    feedback: false\n"
	writeYML(t, cfg.ProjectDir, content)
	if _, err := standards.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	raw, _ := os.ReadFile(filepath.Join(cfg.ProjectDir, ".yakos.yml"))
	doc := readYMLFile(t, filepath.Join(cfg.ProjectDir, ".yakos.yml"))

	if doc["yakos"] == nil {
		t.Errorf("expected yakos field preserved; doc: %s", raw)
	}
	p, _ := doc["profile"].(map[string]interface{})
	if p["type"] != "service" {
		t.Errorf("expected type=service preserved; got %v", p["type"])
	}
	s, _ := p["standards"].(map[string]interface{})
	if s["logging"] != true {
		t.Errorf("expected logging=true preserved; got %v", s["logging"])
	}
	if s["feedback"] != true {
		t.Errorf("expected feedback=true after enable; got %v", s["feedback"])
	}
}

// ---- scenario (i): disable sets standard to false ---------------------------

func TestStandardsParity_Disable_SetsFalse(t *testing.T) {
	cfg := newCfg(t, "disable")
	cfg.StandardName = "logging"
	writeYML(t, cfg.ProjectDir, "profile:\n  standards:\n    logging: true\n")
	if _, err := standards.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	ymlPath := filepath.Join(cfg.ProjectDir, ".yakos.yml")
	doc := readYMLFile(t, ymlPath)
	p, _ := doc["profile"].(map[string]interface{})
	s, _ := p["standards"].(map[string]interface{})
	if s["logging"] != false {
		t.Errorf("expected logging=false; got %v", s["logging"])
	}
}

// ---- scenario (j): disable no stray .tmp file -------------------------------

func TestStandardsParity_Disable_NoTmpFile(t *testing.T) {
	cfg := newCfg(t, "disable")
	cfg.StandardName = "monitors"
	writeYML(t, cfg.ProjectDir, "profile:\n  standards:\n    monitors: true\n")
	if _, err := standards.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ymlPath := filepath.Join(cfg.ProjectDir, ".yakos.yml")
	if _, err := os.Stat(ymlPath + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("stray .tmp file should not exist after atomic write")
	}
}

// ---- scenario (k): check no standards enabled → advisory --------------------

func TestStandardsParity_Check_NoEnabled_Advisory(t *testing.T) {
	cfg := newCfg(t, "check")
	writeYML(t, cfg.ProjectDir, "profile:\n  standards:\n    logging: false\n")
	res, err := standards.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.ActiveStandards) != 0 {
		t.Errorf("expected no active standards; got %v", res.ActiveStandards)
	}
	o := stdOut(cfg)
	if !strings.Contains(o, "no standards enabled") {
		t.Errorf("expected advisory; got: %q", o)
	}
	if !strings.Contains(o, "yakos standards enable") {
		t.Errorf("expected enable hint; got: %q", o)
	}
}

// ---- scenario (l): check enabled standards → playbook info ------------------

func TestStandardsParity_Check_EnabledStandards(t *testing.T) {
	cfg := newCfg(t, "check")
	writeYML(t, cfg.ProjectDir, "profile:\n  standards:\n    logging: true\n    monitors: true\n")
	res, err := standards.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.ActiveStandards) != 2 {
		t.Fatalf("expected 2 active standards; got %d: %v", len(res.ActiveStandards), res.ActiveStandards)
	}
	o := stdOut(cfg)
	if !strings.Contains(o, "== logging ==") {
		t.Errorf("expected '== logging ==' in output; got: %q", o)
	}
	if !strings.Contains(o, "== monitors ==") {
		t.Errorf("expected '== monitors ==' in output; got: %q", o)
	}
	if !strings.Contains(o, "playbook:") {
		t.Errorf("expected 'playbook:' in output; got: %q", o)
	}
	if !strings.Contains(o, "detects:") {
		t.Errorf("expected 'detects:' in output; got: %q", o)
	}
	if !strings.Contains(o, "scaffold:") {
		t.Errorf("expected 'scaffold:' in output; got: %q", o)
	}
}

// ---- scenario (m): check only enabled standards appear ----------------------

func TestStandardsParity_Check_OnlyEnabled(t *testing.T) {
	cfg := newCfg(t, "check")
	writeYML(t, cfg.ProjectDir, "profile:\n  standards:\n    logging: true\n    feedback: false\n")
	_, err := standards.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := stdOut(cfg)
	if strings.Contains(o, "== feedback ==") {
		t.Errorf("disabled 'feedback' should not appear in check output; got: %q", o)
	}
}

// ---- scenario (n): init .yakos.yml missing → error --------------------------

func TestStandardsParity_Init_MissingYML_Error(t *testing.T) {
	cfg := newCfg(t, "init")
	cfg.PromptFn = stubPrompts("web-app", "n")
	_, err := standards.Run(cfg)
	if err == nil {
		t.Fatal("expected error when .yakos.yml missing")
	}
	if !strings.Contains(err.Error(), ".yakos.yml") {
		t.Errorf("expected .yakos.yml mention; got: %v", err)
	}
}

// ---- scenario (o): init invalid type → error --------------------------------

func TestStandardsParity_Init_InvalidType_Error(t *testing.T) {
	cfg := newCfg(t, "init")
	writeYML(t, cfg.ProjectDir, "yakos: 0.9\n")
	cfg.PromptFn = stubPrompts("not-a-valid-type")
	_, err := standards.Run(cfg)
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (p): init valid type → profile.type set ----------------------

func TestStandardsParity_Init_ValidType_TypeSet(t *testing.T) {
	cfg := newCfg(t, "init")
	writeYML(t, cfg.ProjectDir, "yakos: 0.9\nprofile:\n  type: library\n")
	cfg.PromptFn = stubPrompts("service", "n") // pick service, decline suggested
	res, err := standards.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.TypeSet {
		t.Errorf("expected TypeSet=true")
	}
	doc := readYMLFile(t, filepath.Join(cfg.ProjectDir, ".yakos.yml"))
	p, _ := doc["profile"].(map[string]interface{})
	if p["type"] != "service" {
		t.Errorf("expected type=service in .yakos.yml; got %v", p["type"])
	}
}

// ---- scenario (q): init declined suggested → nothing enabled ----------------

func TestStandardsParity_Init_Declined_NothingEnabled(t *testing.T) {
	cfg := newCfg(t, "init")
	writeYML(t, cfg.ProjectDir, "yakos: 0.9\nprofile:\n  type: library\n")
	cfg.PromptFn = stubPrompts("service", "n")
	if _, err := standards.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	doc := readYMLFile(t, filepath.Join(cfg.ProjectDir, ".yakos.yml"))
	p, _ := doc["profile"].(map[string]interface{})
	s, _ := p["standards"].(map[string]interface{})
	for _, name := range []string{"logging", "monitors", "architecture-viz"} {
		if v, ok := s[name]; ok && v == true {
			t.Errorf("expected %q not enabled after decline; got %v", name, v)
		}
	}
}

// ---- scenario (r): init accepted suggested → suggested enabled --------------

func TestStandardsParity_Init_Accepted_SuggestedEnabled(t *testing.T) {
	cfg := newCfg(t, "init")
	writeYML(t, cfg.ProjectDir, "yakos: 0.9\nprofile:\n  type: library\n")
	cfg.PromptFn = stubPrompts("service", "y")
	if _, err := standards.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	doc := readYMLFile(t, filepath.Join(cfg.ProjectDir, ".yakos.yml"))
	p, _ := doc["profile"].(map[string]interface{})
	s, _ := p["standards"].(map[string]interface{})
	// Service suggested: logging, monitors, architecture-viz.
	for _, name := range []string{"logging", "monitors", "architecture-viz"} {
		if s[name] != true {
			t.Errorf("expected %q enabled after accept; got %v", name, s[name])
		}
	}
}

// ---- scenario (s): unknown subcommand → error with hint ---------------------

func TestStandardsParity_UnknownSubcommand_Error(t *testing.T) {
	cfg := newCfg(t, "bogus")
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

// ---- scenario (t): help text key phrases ------------------------------------

func TestStandardsParity_HelpText_KeyPhrases(t *testing.T) {
	var buf bytes.Buffer
	standards.PrintHelp(&buf)
	help := buf.String()
	for _, phrase := range []string{
		"yakos standards",
		"list",
		"enable",
		"disable",
		"check",
		"init",
		"logging",
		"changelog-ui",
		"monitors",
		"feedback",
		"architecture-viz",
		"about-page",
	} {
		if !strings.Contains(help, phrase) {
			t.Errorf("help missing %q; got:\n%s", phrase, help)
		}
	}
}

// ---- scenario (u): all 6 standards appear in list output --------------------

func TestStandardsParity_List_All6Standards(t *testing.T) {
	cfg := newCfg(t, "list")
	writeYML(t, cfg.ProjectDir, "yakos: 0.9\n")
	_, err := standards.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := stdOut(cfg)
	for _, name := range standards.KnownStandards() {
		if !strings.Contains(o, name) {
			t.Errorf("expected %q in list output; got: %q", name, o)
		}
	}
}

// ---- scenario (v): enable then disable → final state false ------------------

func TestStandardsParity_EnableThenDisable(t *testing.T) {
	dir := t.TempDir()
	writeYML(t, dir, "yakos: 0.9\n")
	home := t.TempDir()

	enable := standards.Config{
		Subcommand:   "enable",
		StandardName: "about-page",
		ProjectDir:   dir,
		HomeDir:      home,
		Writer:       &bytes.Buffer{},
		ErrWriter:    &bytes.Buffer{},
	}
	if _, err := standards.Run(enable); err != nil {
		t.Fatalf("enable: %v", err)
	}

	disable := standards.Config{
		Subcommand:   "disable",
		StandardName: "about-page",
		ProjectDir:   dir,
		HomeDir:      home,
		Writer:       &bytes.Buffer{},
		ErrWriter:    &bytes.Buffer{},
	}
	if _, err := standards.Run(disable); err != nil {
		t.Fatalf("disable: %v", err)
	}

	doc := readYMLFile(t, filepath.Join(dir, ".yakos.yml"))
	p, _ := doc["profile"].(map[string]interface{})
	s, _ := p["standards"].(map[string]interface{})
	if s["about-page"] != false {
		t.Errorf("expected about-page=false after enable+disable; got %v", s["about-page"])
	}
}
