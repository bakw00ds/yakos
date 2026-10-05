package runtime

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/bakw00ds/yakos/internal/routerpolicy"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// Sandbox-by-default for the third-party harnesses (K-133).
//
// codex and agy used to be dispatched with their approvals and sandbox
// switched off, so any task text or file the model read could drive arbitrary
// commands as the operator. They now run sandboxed unless the operator
// explicitly allows otherwise in the owner-only ~/.yakos-state/router-policy.yml
// (allow_unsandboxed_runtimes). A project .yakos.yml cannot enable it, and the
// file is read from $HOME/.yakos-state only (see statepath.TrustedDir).
const (
	// codexSandboxMode is the codex sandbox policy used for every dispatch.
	codexSandboxMode = "workspace-write"

	// codexApprovalPolicy is the approval policy paired with the sandbox.
	// "never" cannot prompt, which a headless exec has no way to answer; a
	// command that needs more than the sandbox allows simply fails.
	codexApprovalPolicy = "never"
)

// sandboxNoteWriter receives the one-line notes about the sandbox decision.
// Replaced in tests.
var sandboxNoteWriter io.Writer = os.Stderr

// sandboxNotes records which notes were already printed, so a daemon that
// builds thousands of commands says each thing once per process.
var sandboxNotes sync.Map

func noteOnce(key, format string, args ...any) {
	if _, loaded := sandboxNotes.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	_, _ = fmt.Fprintf(sandboxNoteWriter, format, args...)
}

// unsandboxedAllowed reports whether the operator's trusted router policy
// allows runtimeName to run without its sandbox. It prints one stderr note per
// process when bypass is active, and one when a policy file was found but
// ignored (so an operator who edited it knows why the sandbox stayed on).
func unsandboxedAllowed(runtimeName string) bool {
	dir := statepath.TrustedDir()
	if dir == "" {
		return false
	}
	ok, err := routerpolicy.AllowsUnsandboxed(dir, runtimeName)
	if err != nil {
		noteOnce("ignored:"+runtimeName, "yakos: %s stays sandboxed; ignoring router policy: %v\n", runtimeName, err)
		return false
	}
	if ok {
		noteOnce("bypass:"+runtimeName,
			"yakos: %s is running WITHOUT its sandbox: %s lists it in allow_unsandboxed_runtimes\n",
			runtimeName, routerpolicy.Path(dir))
	}
	return ok
}

// adapterWorkDir is the directory a codex or agy subprocess runs in: the
// server-set worktree override when present, otherwise the project. The
// sandbox's writable workspace is this directory, and project-scoped agent
// files (.codex/agents, .agents/skills) are looked up from it.
func adapterWorkDir(override, project string) string {
	if override != "" {
		return override
	}
	return project
}
