package supervisorstream

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	// Stdin, when non-empty, is fed to the child's standard input (the shadow
	// decision call reads its state there). It must stay small: it is written
	// to a pipe before the child starts, and the hook never waits.
	Stdin []byte
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

	cmd := exec.Command(spec.CLI, spec.Args...) //nolint:gosec
	cmd.Stdout = out
	cmd.Stderr = errf
	if len(spec.Stdin) > 0 {
		// A real pipe (an *os.File), written before Start: the hook exits right
		// after Start, so a copy goroutine (what a bytes.Reader would use)
		// would die with it and truncate the child's input.
		pr, pw, err := os.Pipe()
		if err != nil {
			return err
		}
		defer pr.Close() //nolint:errcheck
		if _, err := pw.Write(spec.Stdin); err != nil {
			pw.Close() //nolint:errcheck,gosec
			return err
		}
		pw.Close() //nolint:errcheck,gosec
		cmd.Stdin = pr
	}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
