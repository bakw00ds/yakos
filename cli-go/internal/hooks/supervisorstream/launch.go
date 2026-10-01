package supervisorstream

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// decisionsHeadCap mirrors bash `head -c 1500 decisions.md`.
const decisionsHeadCap = 1500

// noDecisionsText is what bash substitutes when decisions.md is unreadable.
const noDecisionsText = "(decisions.md not found; use the most recent user prompt as intent)"

// LaunchSpec describes one detached `yakos dispatch <agent> <task>` launch.
type LaunchSpec struct {
	// CLI is the yakos executable to run.
	CLI string
	// Args are the arguments after CLI: dispatch <agent> <task> --runtime R --model M.
	Args []string
	// StdoutPath / StderrPath are appended to (created when absent).
	StdoutPath string
	StderrPath string

	// Wrapper settings (K-117). The production launcher re-executes Self as
	// `hook supervisor-wrap`: a Go process (no bash, so it also works on
	// Windows) that runs Args under a wall-clock deadline, clears the
	// in-flight state and starts the follow-up for coalesced triggers. State
	// and Lock are the shared state file and mkdir lock, Pending the
	// per-session pending-events file and Log the hook ndjson log.
	Self                  string
	State, Lock, Log      string
	Pending               string
	Findings              string // supervisor-findings.ndjson (synthetic finding at the ceiling)
	DeadlineS, Cap, Ceil  int
	IntervalS, BackoffMin int
	DelayS                int
}

// Launcher starts spec detached and returns without waiting for it. The
// production launcher is launchDetached; tests inject a recording fake.
type Launcher func(spec LaunchSpec) error

// findCLI mirrors bash supervisor-stream.sh: $YAKOS_CLI, then
// $YAKOS_ROOT/cli/yakos (when a regular file), then `yakos` on PATH. Unlike a
// naive os.Executable fallback it never re-executes the running binary, which
// under `go test` would be the test binary itself.
func findCLI(env map[string]string) string {
	if v := env["YAKOS_CLI"]; v != "" {
		return v
	}
	if root := env["YAKOS_ROOT"]; root != "" {
		p := filepath.Join(root, "cli", "yakos")
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p
		}
	}
	return lookPath("yakos", env["PATH"])
}

// lookPath resolves name against the given PATH value (falling back to the
// process PATH when empty), like `command -v`.
func lookPath(name, pathVar string) string {
	if pathVar == "" {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
		return ""
	}
	names := []string{name}
	if runtime.GOOS == "windows" {
		// Windows resolves through PATHEXT (yakos.exe), and has no exec bit.
		names = nil
		exts := os.Getenv("PATHEXT")
		if exts == "" {
			exts = ".EXE;.CMD;.BAT"
		}
		for _, e := range strings.Split(exts, ";") {
			if e != "" {
				names = append(names, name+strings.ToLower(e))
			}
		}
	}
	for _, dir := range filepath.SplitList(pathVar) {
		if dir == "" {
			dir = "."
		}
		for _, n := range names {
			p := filepath.Join(dir, n)
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() &&
				(runtime.GOOS == "windows" || st.Mode().Perm()&0o111 != 0) {
				return p
			}
		}
	}
	return ""
}

// buildTask reproduces the bash supervisor task text byte for byte. Bash
// builds it via $(head -c 1500 decisions.md), so trailing newlines of the
// decisions head are stripped.
func buildTask(bufferPath, findingsPath, decisionsPath string, scoreEvery int) string {
	intent := noDecisionsText
	if data, err := os.ReadFile(decisionsPath); err == nil { //nolint:gosec
		if len(data) > decisionsHeadCap {
			data = data[:decisionsHeadCap]
		}
		intent = strings.TrimRight(string(data), "\n")
	}
	// Bash builds these from slash paths (Git-bash too); keep the text identical.
	bufferPath, findingsPath = filepath.ToSlash(bufferPath), filepath.ToSlash(findingsPath)
	return fmt.Sprintf("Read %s (the last 50 tool calls; focus on the most recent %d).\n"+
		"Apply the rubric in your persona. Write your finding as a single\n"+
		"JSON line appended to %s.\n\n"+
		"Stated intent of the active session: %s",
		bufferPath, scoreEvery, findingsPath, intent)
}

// launchDetached is the production Launcher: it starts the process in its own
// session/process group with stdout and stderr appended to the given files,
// and does not wait for it (the hook must return immediately).
func launchDetached(spec LaunchSpec) error {
	out, err := os.OpenFile(spec.StdoutPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec
	if err != nil {
		return err
	}
	defer out.Close()                                                                     //nolint:errcheck
	errf, err := os.OpenFile(spec.StderrPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec
	if err != nil {
		return err
	}
	defer errf.Close() //nolint:errcheck

	var cmd *exec.Cmd
	if spec.Self != "" && spec.State != "" {
		args := append([]string{"hook", "supervisor-wrap", spec.CLI}, spec.Args...)
		cmd = exec.Command(spec.Self, args...) //nolint:gosec
		cmd.Env = append(os.Environ(),
			"_SSW_STATE="+spec.State, "_SSW_LOCK="+spec.Lock, "_SSW_LOG="+spec.Log, "_SSW_PENDING="+spec.Pending, "_SSW_FINDINGS="+spec.Findings, "_SSW_CEIL="+strconv.Itoa(spec.Ceil),
			"_SSW_DEADLINE="+strconv.Itoa(spec.DeadlineS), "_SSW_CAP="+strconv.Itoa(spec.Cap),
			"_SSW_INTERVAL="+strconv.Itoa(spec.IntervalS), "_SSW_BACKOFF="+strconv.Itoa(spec.BackoffMin),
			"_SSW_DELAY="+strconv.Itoa(spec.DelayS))
	} else {
		cmd = exec.Command(spec.CLI, spec.Args...) //nolint:gosec
	}
	cmd.Stdout = out
	cmd.Stderr = errf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// selfExecutable returns the running yakos binary ("" when unknown, in which
// case the launcher runs the dispatch directly, unwrapped).
func selfExecutable() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}
