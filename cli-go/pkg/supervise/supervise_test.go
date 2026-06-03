package supervise_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/pkg/supervise"
)

func TestRun_UnknownSubcommand(t *testing.T) {
	_, err := supervise.Run(supervise.Config{
		Subcommand: "nonexistent",
		HomeDir:    t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for unknown subcommand; got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention the unknown subcommand; got %q", err.Error())
	}
}

func TestRun_PendingNoProject(t *testing.T) {
	// pending with an empty project and no cwd inference
	// should return an error about project resolution.
	_, err := supervise.Run(supervise.Config{
		Subcommand: "pending",
		Project:    "doesnotexist-yakos-pkg-test",
		HomeDir:    t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for non-existent project; got nil")
	}
}

func TestRun_StatusNoProject(t *testing.T) {
	_, err := supervise.Run(supervise.Config{
		Subcommand: "status",
		Project:    "doesnotexist-yakos-pkg-test",
		HomeDir:    t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for non-existent project")
	}
}

func TestPrintHelp_WritesContent(t *testing.T) {
	var buf bytes.Buffer
	supervise.PrintHelp(&buf)
	if buf.Len() == 0 {
		t.Error("PrintHelp should write non-empty content")
	}
	if !strings.Contains(buf.String(), "supervise") {
		t.Errorf("help text should mention 'supervise'; got: %q", buf.String()[:100])
	}
}

func TestConfig_TypeAlias(t *testing.T) {
	var cfg supervise.Config
	if cfg.Subcommand != "" {
		t.Error("zero value Subcommand should be empty")
	}
	if cfg.Project != "" {
		t.Error("zero value Project should be empty")
	}
}

func TestResult_TypeAlias(t *testing.T) {
	var r supervise.Result
	if r.PendingCount != 0 {
		t.Error("zero value PendingCount should be 0")
	}
}
