package refresh_test

import (
	"os"
	"testing"

	"github.com/bakw00ds/yakos/pkg/refresh"
)

func TestCollectProjects_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	projects := refresh.CollectProjects(dir)
	if len(projects) != 0 {
		t.Errorf("expected 0 projects in empty dir; got %d", len(projects))
	}
}

func TestCollectProjects_NonExistentDir(t *testing.T) {
	projects := refresh.CollectProjects("/tmp/definitely-does-not-exist-yakos-refresh-test")
	if len(projects) != 0 {
		t.Errorf("expected 0 projects for non-existent dir; got %d", len(projects))
	}
}

func TestRun_EmptyProjects_NoOp(t *testing.T) {
	yakosRoot := os.Getenv("YAKOS_ROOT")
	if yakosRoot == "" {
		t.Skip("YAKOS_ROOT not set")
	}

	report, err := refresh.Run(refresh.Config{
		YakosRoot:    yakosRoot,
		ProjectPaths: []string{},
		DryRun:       true,
		Writer:       os.Stdout,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Projects) != 0 {
		t.Errorf("expected 0 project reports; got %d", len(report.Projects))
	}
	if !report.DryRun {
		t.Error("expected DryRun=true in report")
	}
}

func TestConfig_TypeAlias(t *testing.T) {
	// Verify the type alias is transparent — zero value must be assignable.
	var cfg refresh.Config
	if cfg.YakosRoot != "" {
		t.Error("zero value YakosRoot should be empty")
	}
	if cfg.DryRun {
		t.Error("zero value DryRun should be false")
	}
}
