package refresh

// K-81: plan-quality-gate.sh was split into a PreToolUse fail-closed gate
// (plan-quality-gate.sh) and a PostToolUse scorer (plan-quality-score.sh).
// Projects refreshed before the split have plan-quality-gate.sh registered
// under BOTH events; refresh must retire the PostToolUse one and add the
// scorer, idempotently.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// hookRegs flattens settings.json into "Event [matcher] command" lines.
func hookRegs(t *testing.T, settingsJSON []byte) []string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(settingsJSON, &doc); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	var out []string
	hm := hooksMap(doc)
	for event, entries := range hm {
		for _, e := range asList(entries) {
			em := toMap(e)
			for _, h := range asList(hooksList(em)) {
				out = append(out, event+" ["+matcherOf(em)+"] "+commandOf(toMap(h)))
			}
		}
	}
	return out
}

func countRegs(regs []string, event, script string) int {
	n := 0
	for _, r := range regs {
		if strings.HasPrefix(r, event+" [") && strings.HasSuffix(r, "/"+script) {
			n++
		}
	}
	return n
}

func oldLayoutFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(hooksImplRepoRoot(t), "tests", "fixtures", "refresh", "proj-plan-quality-old-layout", ".claude", "settings.json")) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func realTemplate(t *testing.T) string {
	t.Helper()
	return filepath.Join(hooksImplRepoRoot(t), "lib", "settings", "settings.template.json")
}

func TestMergeSettings_PlanQualitySplitMigration(t *testing.T) {
	old := oldLayoutFixture(t)
	before := hookRegs(t, old)
	if countRegs(before, "PostToolUse", "plan-quality-gate.sh") != 1 || countRegs(before, "PreToolUse", "plan-quality-gate.sh") != 1 {
		t.Fatalf("fixture is not the old layout: %v", before)
	}

	dep := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(dep, old, 0o644); err != nil {
		t.Fatal(err)
	}

	stats, err := MergeSettingsFiles(realTemplate(t), dep, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Removed != 1 || stats.Added != 1 {
		t.Fatalf("first run: want removed=1 added=1, got %+v", stats)
	}
	first, _ := os.ReadFile(dep) //nolint:gosec
	regs := hookRegs(t, first)
	if n := countRegs(regs, "PostToolUse", "plan-quality-gate.sh"); n != 0 {
		t.Errorf("old PostToolUse plan-quality-gate.sh still registered (%d)", n)
	}
	if n := countRegs(regs, "PostToolUse", "plan-quality-score.sh"); n != 1 {
		t.Errorf("want exactly 1 PostToolUse plan-quality-score.sh, got %d", n)
	}
	if n := countRegs(regs, "PreToolUse", "plan-quality-gate.sh"); n != 1 {
		t.Errorf("want the PreToolUse gate registered exactly once, got %d", n)
	}
	if n := countRegs(regs, "PostToolUse", "my-local-hook.sh"); n != 1 {
		t.Errorf("project-local PostToolUse hook must survive, got %d", n)
	}

	// Second run: byte-stable, zero changes.
	stats2, err := MergeSettingsFiles(realTemplate(t), dep, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats2.Added != 0 || stats2.Removed != 0 {
		t.Errorf("second run must be a no-op, got %+v", stats2)
	}
	second, _ := os.ReadFile(dep) //nolint:gosec
	if !bytes.Equal(first, second) {
		t.Error("settings.json not byte-stable across a second refresh")
	}
}

// The Go-form registration (`yakos hook run plan-quality-gate` under
// PostToolUse, written by --hooks-impl go) is the same retired hook.
func TestMergeSettings_PlanQualitySplitMigration_GoForm(t *testing.T) {
	tmp := t.TempDir()
	dep := filepath.Join(tmp, "settings.json")
	tpl := filepath.Join(tmp, "template.json")
	if err := os.WriteFile(tpl, []byte(`{"hooks":{
	  "PreToolUse":[{"matcher":"TeamCreate|Agent","hooks":[{"type":"command","command":"${CLAUDE_PROJECT_DIR}/scripts/hooks/plan-quality-gate.sh"}]}],
	  "PostToolUse":[{"matcher":"Edit|Write|MultiEdit","hooks":[{"type":"command","command":"${CLAUDE_PROJECT_DIR}/scripts/hooks/plan-quality-score.sh"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dep, []byte(`{"hooks":{
	  "PreToolUse":[{"matcher":"TeamCreate|Agent","hooks":[{"type":"command","command":"/opt/yakos/bin/yakos hook run plan-quality-gate"}]}],
	  "PostToolUse":[{"matcher":"Edit|Write|MultiEdit","hooks":[{"type":"command","command":"/opt/yakos/bin/yakos hook run plan-quality-gate"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeSettingsFiles(tpl, dep, false, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dep) //nolint:gosec
	regs := hookRegs(t, got)
	if strings.Contains(strings.Join(regs, "\n"), "PostToolUse [Edit|Write|MultiEdit] /opt/yakos/bin/yakos hook run plan-quality-gate") {
		t.Errorf("Go-form PostToolUse plan-quality-gate not retired: %v", regs)
	}
	if countRegs(regs, "PostToolUse", "plan-quality-score.sh") != 1 {
		t.Errorf("scorer not added: %v", regs)
	}
	if n := strings.Count(strings.Join(regs, "\n"), "PreToolUse [TeamCreate|Agent]"); n != 1 {
		t.Errorf("PreToolUse gate must stay registered exactly once (template form), got %d: %v", n, regs)
	}
}

// A retired pair is only removed when the template does not ship it.
func TestMergeSettings_RetiredRegistrationKeptIfTemplateShipsIt(t *testing.T) {
	tmp := t.TempDir()
	body := `{"hooks":{"PostToolUse":[{"matcher":"Edit","hooks":[{"type":"command","command":"${CLAUDE_PROJECT_DIR}/scripts/hooks/plan-quality-gate.sh"}]}]}}`
	tpl, dep := filepath.Join(tmp, "t.json"), filepath.Join(tmp, "s.json")
	_ = os.WriteFile(tpl, []byte(body), 0o644)
	_ = os.WriteFile(dep, []byte(body), 0o644)
	stats, err := MergeSettingsFiles(tpl, dep, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Removed != 0 || stats.Added != 0 {
		t.Fatalf("template-shipped registration must be untouched, got %+v", stats)
	}
}

// Go and bash refresh must agree byte-for-byte on the migrated file.
func TestMergeSettings_PlanQualitySplitMigration_BashParity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash refresh not exercised on windows")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}
	root := hooksImplRepoRoot(t)
	old := oldLayoutFixture(t)

	// Go result.
	goDep := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(goDep, old, 0o644)
	if _, err := MergeSettingsFiles(realTemplate(t), goDep, false, nil); err != nil {
		t.Fatal(err)
	}
	goOut, _ := os.ReadFile(goDep) //nolint:gosec

	// Bash result.
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "project")
	_ = os.MkdirAll(filepath.Join(proj, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(proj, ".claude", "settings.json"), old, 0o644)
	cmd := exec.Command("bash", filepath.Join(root, "cli", "lib", "refresh.sh"), "--project", proj)
	cmd.Env = append(os.Environ(), "HOME="+filepath.Join(tmp, "home"), "YAKOS_ROOT="+root, "YAKOS_LIB="+filepath.Join(root, "cli", "lib"))
	_ = os.MkdirAll(filepath.Join(tmp, "home"), 0o755)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash refresh: %v\n%s", err, out)
	}
	bashOut, _ := os.ReadFile(filepath.Join(proj, ".claude", "settings.json")) //nolint:gosec
	if !bytes.Equal(goOut, bashOut) {
		t.Fatalf("Go and bash refresh disagree on the migrated settings.json\n%s", byteDiff(bashOut, goOut))
	}
}
