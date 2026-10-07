package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// implSection renders the "Implementation" section for one environment.
func implSection(t *testing.T, root string, impl string) (string, *Report) {
	t.Helper()
	var buf bytes.Buffer
	r := &runner{
		cfg:       Config{Environ: func(k string) string { return map[string]string{"YAKOS_IMPL": impl}[k] }},
		w:         &buf,
		yakosRoot: root,
		report:    &Report{},
	}
	r.checkImplementation()
	return buf.String(), r.report
}

func bashTreeRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cli", "yakos"), []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	return root
}

// The doctor names the implementation dispatch runs on (K-143). Goldens pin the
// wording; YAKOS_UPDATE_GOLDEN=1 rewrites them.
func TestDoctorImplementationLineGolden(t *testing.T) {
	with, without := bashTreeRoot(t), t.TempDir()
	cases := []struct{ name, root, impl string }{
		{"unset-bash-tree", with, ""},
		{"unset-go-only", without, ""},
		{"go", with, "go"},
		{"bash", with, "bash"},
		{"bash-no-tree", without, "bash"},
	}
	var got strings.Builder
	for _, c := range cases {
		out, _ := implSection(t, c.root, c.impl)
		got.WriteString("## " + c.name + "\n" + out)
	}
	path := filepath.Join("testdata", "implementation.golden")
	if os.Getenv("YAKOS_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(got.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden (YAKOS_UPDATE_GOLDEN=1 writes it): %v", err)
	}
	if string(want) != got.String() {
		t.Errorf("implementation section differs from its golden:\n--- want\n%s--- got\n%s", want, got.String())
	}
}

// A bash choice is a warning (no router, no sandbox flags); the default is not,
// and the variable's value is never printed.
func TestDoctorImplementationSeverityAndNoValue(t *testing.T) {
	root := bashTreeRoot(t)
	if _, rep := implSection(t, root, ""); rep.Warnings != 0 {
		t.Errorf("the default must not warn: %+v", rep)
	}
	if _, rep := implSection(t, root, "bash"); rep.Warnings != 1 {
		t.Errorf("YAKOS_IMPL=bash must warn once: %+v", rep)
	}
	const marker = "SENTINELIMPL0123"
	if out, _ := implSection(t, root, marker); strings.Contains(out, marker) {
		t.Errorf("the value reached the output: %s", out)
	}
}
