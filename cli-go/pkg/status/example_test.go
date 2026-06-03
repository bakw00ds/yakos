package status_test

import (
	"fmt"
	"os"
	"time"

	"github.com/bakw00ds/yakos/pkg/status"
)

func ExampleRun() {
	// Query status for a project that doesn't exist — demonstrates that
	// Run returns a valid report with "absent" labels rather than an error.
	cfg := status.Config{
		Project: "example-project",
		HomeDir: "/tmp",
		Now:     time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}
	rpt, err := status.Run(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		return
	}
	fmt.Println(rpt.Project)
	fmt.Println(rpt.PlanAge)
	// Output:
	// example-project
	// absent
}

func ExampleFormat() {
	cfg := status.Config{
		Project: "demo",
		HomeDir: "/tmp",
		Now:     time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}
	rpt, _ := status.Run(cfg)

	// Discard output; demonstrate the call pattern.
	if err := status.Format(os.Stdout, rpt); err != nil {
		fmt.Fprintf(os.Stderr, "format: %v\n", err)
	}
}

func ExampleNewConfig() {
	cfg := status.NewConfig("myproject")
	fmt.Println(cfg.Project)
	// Output: myproject
}
