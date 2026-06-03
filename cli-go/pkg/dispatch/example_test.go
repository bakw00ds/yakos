package dispatch_test

import (
	"context"
	"fmt"
	"os"

	"github.com/bakw00ds/yakos/pkg/dispatch"
)

// Example_run demonstrates the basic usage of dispatch.Run.
// In real usage, yakosRoot and project point to actual directories.
func Example_run() {
	// Resolve paths from the environment.
	yakosRoot := os.Getenv("YAKOS_ROOT")
	projectDir := os.Getenv("CLAUDE_PROJECT_DIR")
	if yakosRoot == "" || projectDir == "" {
		fmt.Println("skip: YAKOS_ROOT or CLAUDE_PROJECT_DIR not set")
		return
	}

	result, err := dispatch.Run(context.Background(), dispatch.Request{
		Agent:     "backend",
		Task:      "add a health-check endpoint",
		Project:   projectDir,
		YakosRoot: yakosRoot,
		Runtime:   "claude",
		Model:     "sonnet",
	})
	if err != nil {
		fmt.Printf("dispatch error: %v\n", err)
		return
	}

	fmt.Printf("exit_code=%d model=%s\n", result.ExitCode, result.ModelResolved)
	// Output is environment-dependent; example is illustrative only.
}
