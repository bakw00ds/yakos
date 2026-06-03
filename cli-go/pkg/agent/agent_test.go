package agent_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/pkg/agent"
)

func TestRun_UnknownSubcommand(t *testing.T) {
	_, err := agent.Run(agent.Config{
		Subcommand: "bogus",
	})
	if err == nil {
		t.Fatal("expected error for unknown subcommand; got nil")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should mention the unknown subcommand; got %q", err.Error())
	}
}

func TestRun_NewMissingName(t *testing.T) {
	_, err := agent.Run(agent.Config{
		Subcommand: "new",
		// Name intentionally absent
	})
	if err == nil {
		t.Fatal("expected error for missing agent name; got nil")
	}
}

func TestPrintHelp_WritesContent(t *testing.T) {
	var buf bytes.Buffer
	agent.PrintHelp(&buf)
	if buf.Len() == 0 {
		t.Error("PrintHelp should write non-empty content")
	}
	output := buf.String()
	if !strings.Contains(output, "agent") {
		t.Errorf("help text should mention 'agent'; got %q", output[:min(100, len(output))])
	}
}

func TestConfig_TypeAlias(t *testing.T) {
	var cfg agent.Config
	if cfg.Subcommand != "" {
		t.Error("zero value Subcommand should be empty")
	}
	if cfg.Force {
		t.Error("zero value Force should be false")
	}
	if cfg.JSON {
		t.Error("zero value JSON should be false")
	}
}

func TestResult_TypeAlias(t *testing.T) {
	var r agent.Result
	if r.Errors != 0 {
		t.Error("zero value Errors should be 0")
	}
	if r.Warnings != 0 {
		t.Error("zero value Warnings should be 0")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
