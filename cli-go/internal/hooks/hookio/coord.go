package hookio

import (
	"os"
	"path/filepath"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// CoordProjectName mirrors bash yakos_project_name: $YAKOS_PROJECT_NAME, else
// basename($CLAUDE_PROJECT_DIR), else basename of the physical cwd.
func CoordProjectName(in hooktype.HookInput) string {
	if p := in.Env["YAKOS_PROJECT_NAME"]; p != "" {
		return p
	}
	if d := in.Env["CLAUDE_PROJECT_DIR"]; d != "" {
		return filepath.Base(d)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	if r, err := filepath.EvalSymlinks(wd); err == nil {
		wd = r
	}
	return filepath.Base(wd)
}

// CoordDir mirrors bash yakos_coord_dir:
// ${YAKOS_COORD_ROOT:-/var/lib/yakos}/<project>/coord.
func CoordDir(in hooktype.HookInput) string {
	root := in.Env["YAKOS_COORD_ROOT"]
	if root == "" {
		root = "/var/lib/yakos"
	}
	return root + "/" + CoordProjectName(in) + "/coord"
}

// CoordEnabled mirrors bash yakos_coord_enabled: the coord dir exists (a
// symlink to a directory counts, as with `[ -d ]`) and is writable by this
// process (access(2) semantics, as with `[ -w ]`).
func CoordEnabled(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	return writable(dir, fi)
}
