package supervise_test

import (
	"bytes"
	"fmt"

	"github.com/bakw00ds/yakos/pkg/supervise"
)

// ExamplePrintHelp demonstrates writing the supervisor help text.
func ExamplePrintHelp() {
	var buf bytes.Buffer
	supervise.PrintHelp(&buf)
	// PrintHelp writes several lines; just verify it produced output.
	if buf.Len() > 0 {
		fmt.Println("help text written")
	}
	// Output: help text written
}

// ExampleRun_pending illustrates querying pending supervisor findings.
// In real usage, Project points to an initialized yakOS project slug.
func ExampleRun_pending() {
	// This example uses a non-existent project and will error gracefully.
	_, err := supervise.Run(supervise.Config{
		Subcommand: "pending",
		Project:    "example-project",
		HomeDir:    "/tmp/yakos-example-nonexistent",
	})
	if err != nil {
		fmt.Println("ok: project not found as expected")
		return
	}
	fmt.Println("pending findings returned")
	// Output: ok: project not found as expected
}
