// Command yakos is the Go port of the yakOS CLI.
//
// During the bash-to-Go transition, this binary operates in "shadow mode":
// it handles a small set of Go-native commands and proxies everything else
// to the existing bash yakos at cli/yakos. This makes the Go binary a drop-in
// replacement that callers can adopt incrementally.
//
// Shadow mode conventions:
//   - yakos --version  → prints VERSION + " (go)" suffix
//   - yakos --help     → proxies to bash, prefixes with a transition note
//   - yakos go-port-status → lists ported vs proxied subcommands (Go-only)
//   - yakos <anything> → proxied to bash yakos with the same args
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/bakw00ds/yakos/internal/passthrough"
	"github.com/bakw00ds/yakos/internal/version"
)

// portedCommands lists subcommands implemented natively in Go. During the
// bootstrap phase this list is empty. Add entries here as subcommands are
// ported in subsequent dispatch tasks.
var portedCommands = []portedCommand{
	// Example entry once a command is ported:
	// {Name: "kanban", Since: "0.40.0", Notes: "full feature parity"},
}

type portedCommand struct {
	Name  string
	Since string // version in which Go impl shipped
	Notes string
}

func main() {
	// Determine the repo root from the executable location.
	// The binary is built to <repo-root>/bin/yakos, so root = exe/../..
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "yakos: could not resolve executable path: %v\n", err)
		os.Exit(1)
	}
	yakosRoot := filepath.Dir(filepath.Dir(exe))

	args := os.Args[1:]

	// Route built-in Go commands before falling through to bash passthrough.
	if len(args) == 0 {
		// No args: proxy to bash (it prints usage).
		exitWith(passthrough.Run(yakosRoot, args))
	}

	switch args[0] {
	case "--version", "-v":
		runVersion(yakosRoot)
	case "--help", "-h":
		runHelp(yakosRoot, args)
	case "go-port-status":
		runPortStatus()
	default:
		// Shadow-mode passthrough: forward everything to bash yakos.
		exitWith(passthrough.Run(yakosRoot, args))
	}
}

// runVersion prints the version string read from the VERSION file plus the
// "(go)" transition suffix.
func runVersion(yakosRoot string) {
	v, err := version.Read(yakosRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yakos: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(v)
}

// runHelp proxies --help to bash yakos with a transition note prepended so
// operators know they are running the Go binary.
func runHelp(yakosRoot string, args []string) {
	fmt.Fprintln(os.Stderr, "(yakos Go binary — wrapping bash yakos for unported commands)")
	fmt.Fprintln(os.Stderr, "")
	exitWith(passthrough.Run(yakosRoot, args))
}

// runPortStatus lists every subcommand and whether it is natively implemented
// in Go or still proxied to bash. Output is written directly to os.Stdout;
// write errors are treated as fatal because stdout failure is unrecoverable.
func runPortStatus() {
	mustPrint := func(s string) {
		if _, err := fmt.Fprint(os.Stdout, s); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
	}

	mustPrint("yakos go-port-status — subcommand migration tracker\n\n")

	if len(portedCommands) == 0 {
		mustPrint("  No subcommands ported to Go yet.\n")
		mustPrint("  All commands proxy to the bash yakos at cli/yakos.\n")
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "  COMMAND\tSINCE\tNOTES"); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
		for _, cmd := range portedCommands {
			if _, err := fmt.Fprintf(w, "  %s\t%s\t%s\n", cmd.Name, cmd.Since, cmd.Notes); err != nil {
				fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
				os.Exit(1)
			}
		}
		if err := w.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
	}

	mustPrint(fmt.Sprintf("\n  Ported: %d / total subcommands tracked: %d\n",
		len(portedCommands), len(portedCommands)))
}

// exitWith calls os.Exit with the code returned by passthrough.Run.
// It prints any error to stderr and exits 1 on passthrough failure.
func exitWith(code int, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "yakos: passthrough error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}
