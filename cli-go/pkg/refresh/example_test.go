package refresh_test

import (
	"fmt"
	"os"

	"github.com/bakw00ds/yakos/pkg/refresh"
)

// ExampleCollectProjects demonstrates project discovery.
func ExampleCollectProjects() {
	// CollectProjects is safe to call with any directory.
	// Returns an empty slice when no projects exist.
	projects := refresh.CollectProjects(os.TempDir())
	fmt.Printf("found %d project(s)\n", len(projects))
	// Output: found 0 project(s)
}

// ExampleRun demonstrates running a dry-run refresh.
func ExampleRun() {
	yakosRoot := os.Getenv("YAKOS_ROOT")
	if yakosRoot == "" {
		fmt.Println("skip: YAKOS_ROOT not set")
		return
	}

	report, err := refresh.Run(refresh.Config{
		YakosRoot:    yakosRoot,
		ProjectPaths: []string{}, // no projects → no-op
		DryRun:       true,
		Writer:       os.Stdout,
	})
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Printf("refreshed %d project(s), dry_run=%v\n", len(report.Projects), report.DryRun)
	// Output is environment-dependent; example is illustrative only.
}
