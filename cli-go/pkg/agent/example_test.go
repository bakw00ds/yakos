package agent_test

import (
	"bytes"
	"fmt"
	"os"

	"github.com/bakw00ds/yakos/pkg/agent"
)

// ExamplePrintHelp demonstrates writing the agent command help text.
func ExamplePrintHelp() {
	var buf bytes.Buffer
	agent.PrintHelp(&buf)
	if buf.Len() > 0 {
		fmt.Println("help text written")
	}
	// Output: help text written
}

// ExampleRun_lint demonstrates linting agents in a project.
// In real usage, YakosRoot and Project point to actual directories.
func ExampleRun_lint() {
	yakosRoot := os.Getenv("YAKOS_ROOT")
	projectDir := os.Getenv("CLAUDE_PROJECT_DIR")
	if yakosRoot == "" || projectDir == "" {
		fmt.Println("skip: YAKOS_ROOT or CLAUDE_PROJECT_DIR not set")
		return
	}

	result, err := agent.Run(agent.Config{
		Subcommand: "lint",
		YakosRoot:  yakosRoot,
		Project:    projectDir,
		Writer:     os.Stdout,
	})
	if err != nil {
		fmt.Printf("lint error: %v\n", err)
		return
	}
	fmt.Printf("errors=%d warnings=%d\n", result.Errors, result.Warnings)
	// Output is environment-dependent; example is illustrative only.
}
