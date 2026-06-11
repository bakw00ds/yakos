package metrics

import (
	"os"
	"path/filepath"
)

// ResolveProjectDir returns the project directory to use for metrics, using
// the same precedence as internal/standards:
//
//  1. Explicit override (cfg.ProjectDir)
//  2. YAKOS_PROJECT_DIR environment variable
//  3. Upward walk from cwd looking for .yakos.yml
//  4. ~/agent-control/*/.project-path walk
//
// Returns "" if no project can be resolved.
func ResolveProjectDir(override string) string {
	if override != "" {
		return override
	}
	if v := os.Getenv("YAKOS_PROJECT_DIR"); v != "" {
		return v
	}

	// Walk up from cwd for .yakos.yml
	cwd, err := os.Getwd()
	if err == nil {
		if dir := walkUpForYakosYML(cwd); dir != "" {
			return dir
		}
	}

	// Walk ~/agent-control/*/.project-path
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}
	if dir := walkAgentControl(home); dir != "" {
		return dir
	}

	return ""
}

// walkUpForYakosYML walks up from dir looking for .yakos.yml.
func walkUpForYakosYML(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".yakos.yml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// walkAgentControl looks for a .project-path file under ~/agent-control/*/
// that points to a directory containing .yakos.yml.
func walkAgentControl(home string) string {
	acDir := filepath.Join(home, "agent-control")
	entries, err := os.ReadDir(acDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ppFile := filepath.Join(acDir, e.Name(), ".project-path")
		data, err := os.ReadFile(ppFile) //nolint:gosec
		if err != nil {
			continue
		}
		projectPath := string(data)
		// Trim whitespace/newlines.
		for len(projectPath) > 0 && (projectPath[len(projectPath)-1] == '\n' || projectPath[len(projectPath)-1] == '\r' || projectPath[len(projectPath)-1] == ' ') {
			projectPath = projectPath[:len(projectPath)-1]
		}
		if projectPath == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(projectPath, ".yakos.yml")); err == nil {
			return projectPath
		}
	}
	return ""
}

// ResolveStateDir returns the yakos state directory.
// Precedence: YAKOS_DISPATCH_LOG env (it's a directory) → ~/.yakos-state.
func ResolveStateDir(home string) string {
	if v := os.Getenv("YAKOS_DISPATCH_LOG"); v != "" {
		return v
	}
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}
	return filepath.Join(home, ".yakos-state")
}
