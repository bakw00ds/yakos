package refresh

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func acProject(t *testing.T, settings, yml string) (proj, file string) {
	t.Helper()
	proj = t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	file = filepath.Join(proj, ".claude", "settings.json")
	if err := os.WriteFile(file, []byte(settings), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if yml != "" {
		if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte(yml), 0o644); err != nil { //nolint:gosec
			t.Fatal(err)
		}
	}
	return proj, file
}

func acValue(t *testing.T, file string) (any, bool) {
	t.Helper()
	b, err := os.ReadFile(file) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	v, ok := m["autoCompactWindow"]
	return v, ok
}

func TestAutoCompact_DefaultWritesWindowAndIsIdempotent(t *testing.T) {
	proj, file := acProject(t, `{"hooks":{}}`, "")
	st, changed, err := applyAutoCompact(proj, file, false)
	if err != nil || !changed || !strings.Contains(st, "150000 (default)") {
		t.Fatalf("first run: %q %v %v", st, changed, err)
	}
	if v, _ := acValue(t, file); v != float64(150000) {
		t.Fatalf("window = %v", v)
	}
	before, _ := os.ReadFile(file) //nolint:gosec
	if _, changed, _ = applyAutoCompact(proj, file, false); changed {
		t.Fatal("second run changed the file")
	}
	after, _ := os.ReadFile(file) //nolint:gosec
	if string(before) != string(after) {
		t.Fatal("second run rewrote the bytes")
	}
}

func TestAutoCompact_HandEditKeptUnlessYMLNamesValue(t *testing.T) {
	proj, file := acProject(t, `{"autoCompactWindow":300000}`, "")
	if _, changed, _ := applyAutoCompact(proj, file, false); changed {
		t.Fatal("hand-edited value was overwritten by the default")
	}
	if v, _ := acValue(t, file); v != float64(300000) {
		t.Fatalf("hand edit lost: %v", v)
	}
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("auto_compact_window: 200k # lead\n"), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if _, changed, err := applyAutoCompact(proj, file, false); err != nil || !changed {
		t.Fatalf("explicit value did not apply: %v %v", changed, err)
	}
	if v, _ := acValue(t, file); v != float64(200000) {
		t.Fatalf("window = %v", v)
	}
}

func TestAutoCompact_OffRemovesKeyAndDryRunWritesNothing(t *testing.T) {
	proj, file := acProject(t, `{"autoCompactWindow":150000}`, "auto_compact_window: off\n")
	if _, changed, _ := applyAutoCompact(proj, file, true); !changed {
		t.Fatal("dry-run did not report the removal")
	}
	if _, ok := acValue(t, file); !ok {
		t.Fatal("dry-run removed the key")
	}
	if _, _, err := applyAutoCompact(proj, file, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := acValue(t, file); ok {
		t.Fatal("off left the key in place")
	}
	// off stays off: a second run neither re-adds nor rewrites.
	if st, changed, _ := applyAutoCompact(proj, file, false); changed || st != "off" {
		t.Fatalf("off not stable: %q %v", st, changed)
	}
}

func TestAutoCompact_RejectsOutOfRange(t *testing.T) {
	for _, v := range []string{"50k", "2m", "abc"} {
		proj, file := acProject(t, `{}`, "auto_compact_window: "+v+"\n")
		if _, _, err := applyAutoCompact(proj, file, false); err == nil {
			t.Errorf("%s accepted", v)
		}
		if _, ok := acValue(t, file); ok {
			t.Errorf("%s written despite the error", v)
		}
	}
}

func TestAutoCompact_InsertKeepsFormattingOfTheRestOfTheFile(t *testing.T) {
	orig := "{\n    \"hooks\": {},\n    \"env\": {\"A\": \"1\"}\n}\n"
	proj, file := acProject(t, orig, "")
	if _, _, err := applyAutoCompact(proj, file, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file) //nolint:gosec
	want := "{\n  \"autoCompactWindow\": 150000,\n    \"hooks\": {},\n    \"env\": {\"A\": \"1\"}\n}\n"
	if string(got) != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestAutoCompact_KeepsFileMode(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o640} {
		proj, file := acProject(t, `{"hooks":{}}`, "")
		if err := os.Chmod(file, mode); err != nil {
			t.Fatal(err)
		}
		if _, changed, err := applyAutoCompact(proj, file, false); err != nil || !changed {
			t.Fatal(err, changed)
		}
		if fi, _ := os.Stat(file); fi.Mode().Perm() != mode {
			t.Errorf("mode %v became %v", mode, fi.Mode().Perm())
		}
	}
}

func TestAutoCompact_InvalidYMLAbortsRefresh(t *testing.T) {
	proj, home := fixtureProject(t, "proj-missing-settings")
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("auto_compact_window: 50k\n"), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	before := readSettings(t, proj)
	_, err := runImpl(t, proj, home, "")
	if err == nil || !strings.Contains(err.Error(), "auto_compact_window") {
		t.Fatalf("want an auto_compact_window error, got %v", err)
	}
	if string(readSettings(t, proj)) != string(before) {
		t.Fatal("settings.json written despite the error")
	}
}
