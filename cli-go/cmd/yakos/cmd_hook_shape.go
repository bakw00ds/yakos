package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/registry"
	"github.com/bakw00ds/yakos/internal/hooks/shaperun"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// shapeDeps builds the shaperun dependencies for this process: the same
// work-dir resolution as `yakos hook run`, except that when
// CLAUDE_PROJECT_DIR is unset the project is the envelope's workspace, not the
// process cwd (agy runs hooks from its .agents directory). With bound set (the
// hooks endpoint's nonce-bound project) it is the project and nothing else is
// consulted. With none of them, the project is "" and shaperun refuses the
// call for a fail-closed hook; the process cwd is never used.
func shapeDeps(yakosRoot, bound string) shaperun.Deps {
	return shaperun.Deps{
		Env:       snapshotEnv(),
		FailOpen:  os.Getenv("YAKOS_HOOKS_FAIL_OPEN") == "1",
		YakosRoot: yakosRoot,
		Resolve: func(workDir string) (registry.Config, string) {
			project := bound
			if project == "" {
				project = os.Getenv("CLAUDE_PROJECT_DIR")
			}
			if project == "" && filepath.IsAbs(workDir) {
				project = workDir
			}
			if project == "" {
				return registry.Config{}, ""
			}
			wcd, proj := resolveHookWorkDirsFor(project)
			return registry.Config{
				WorkCurrentDir: wcd,
				ProjectDir:     proj,
				StateDir:       statepath.Dir(),
				HooksDir:       resolveHooksDir(yakosRoot),
			}, wcd
		},
	}
}

// runHookShape is `yakos hook run --shape codex|agy <name>`: the entrypoint
// the hooks.json files written by `yakos hooks install --harness` call. It
// always runs the Go hook (no bash tier: a harness on a bare host may have no
// bash, and the command text must stay byte-stable for codex's hook hash).
func runHookShape(yakosRoot, shape, name string) {
	if !shaperun.Known(name) {
		fmt.Fprintf(os.Stderr, "yakos hook run: unknown hook %q (try 'yakos hook list')\n", name)
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, hookio.MaxShapeBytes+1))
	if err != nil {
		data = nil // undecodable: shaperun applies the fail-closed posture
	}
	deps := shapeDeps(yakosRoot, "")
	deps.Agent = os.Getenv("YAKOS_AGENT_TYPE") // set by dispatch; see runtime.AgentTypeEnv
	resp := shaperun.Run(context.Background(), shape, name, data, deps)
	_, _ = os.Stdout.Write(resp.Stdout)
	_, _ = os.Stderr.Write(resp.Stderr)
	os.Exit(resp.ExitCode)
}
