package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPolicyRuntimeVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	dir := t.TempDir()
	stub := "#!/bin/sh\necho 'fakeharness 4.5.6'\nhead -c 100000 /dev/zero | tr '\\0' x\n"
	if err := os.WriteFile(filepath.Join(dir, "fakeharness"), []byte(stub), 0o755); err != nil { //nolint:gosec // test stub
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got := policyRuntimeVersion(context.Background(), "fakeharness")
	if !strings.HasPrefix(got, "fakeharness 4.5.6") || len(got) > 512 {
		t.Errorf("version output = %d bytes %q", len(got), got[:20])
	}
	if got := policyRuntimeVersion(context.Background(), "no-such-harness-k144"); got != "" {
		t.Errorf("an absent CLI must give no version, got %q", got)
	}
}
