package main

import (
	"fmt"
	"io"
	"os"

	"github.com/bakw00ds/yakos/internal/codexhome"
	"github.com/bakw00ds/yakos/internal/hooksinstall"
)

// runHooksInstallHarness is `yakos hooks install --harness codex|agy`. codex
// defaults to the yakOS-owned CODEX_HOME profile and never to ~/.codex; agy
// needs --dir (the workspace). It returns the process exit code.
func runHooksInstallHarness(harness, dir, binary string, out, errw io.Writer) int {
	if harness != hooksinstall.HarnessCodex && harness != hooksinstall.HarnessAgy {
		fmt.Fprintf(errw, "hooks install: unknown --harness %q (codex | agy)\n", harness)
		return 1
	}
	home, _ := os.UserHomeDir()
	if dir == "" {
		if harness == hooksinstall.HarnessAgy {
			fmt.Fprintln(errw, "hooks install: --harness agy requires --dir <workspace>")
			return 1
		}
		dir = codexhome.ProfileDir(home)
		if dir == "" {
			fmt.Fprintln(errw, "hooks install: cannot resolve the yakOS codex profile (no home directory)")
			return 1
		}
	}
	path, changed, err := hooksinstall.InstallShape(harness, dir, binary)
	if err != nil {
		fmt.Fprintf(errw, "hooks install: %v\n", err)
		return 1
	}
	state := "unchanged"
	if changed {
		state = "wrote"
	}
	fmt.Fprintf(out, "hooks install %s: %s %s\n", harness, state, path)
	if harness == hooksinstall.HarnessCodex && dir == codexhome.ProfileDir(home) {
		if _, isolated := codexhome.Effective(home, os.Getenv); !isolated {
			fmt.Fprintln(out, "note: dispatch does not use the yakOS codex profile yet, so these hooks are not loaded. Run 'yakos auth login codex', or export OPENAI_API_KEY for the dispatching process.")
		}
	}
	return 0
}
