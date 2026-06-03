// Package completion implements the Go port of `yakos completion`.
//
// Emits shell tab-completion scripts and installs them to the conventional
// per-shell path.
//
// Subcommands:
//
//	bash     Print the bash completion script to stdout.
//	zsh      Print the zsh completion script to stdout.
//	fish     Print the fish completion script to stdout.
//	install  Auto-detect shell, write the completion file to a conventional
//	         path, and print the source-line for the operator's shell rc.
//
// The completion scripts are embedded at build time via //go:embed (Decision D
// in the port plan); the bash and zsh templates are the same files that the
// bash completion.sh reads from cli/completions/. Fish is a generated
// minimal template.
//
// Shell detection order for install:
//
//  1. YAKOS_COMPLETION_SHELL env var (override)
//  2. $SHELL (os.Getenv)
//  3. Error if undetectable
//
// Install path conventions:
//
//	bash: BASH_COMPLETION_USER_DIR or ~/.local/share/bash-completion/completions/yakos
//	zsh:  YAKOS_ZSH_COMPDIR or ~/.zsh/completions/_yakos
//	fish: XDG_CONFIG_HOME/fish/completions/yakos.fish or ~/.config/fish/completions/yakos.fish
package completion

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates/yakos.bash
var bashTemplate []byte

//go:embed templates/yakos.zsh
var zshTemplate []byte

//go:embed templates/yakos.fish
var fishTemplate []byte

// Config controls a completion Run invocation.
type Config struct {
	// Subcommand is one of: bash, zsh, fish, install.
	Subcommand string

	// ShellOverride can override YAKOS_COMPLETION_SHELL for install detection.
	// When empty, os.Getenv("YAKOS_COMPLETION_SHELL") is used.
	ShellOverride string

	// HomeDir overrides $HOME. If empty, os.Getenv("HOME") is used.
	HomeDir string

	// Environ is an optional function for reading environment variables.
	// If nil, os.Getenv is used.
	Environ func(string) string

	// Writer is the output destination. Defaults to os.Stdout.
	Writer io.Writer

	// ErrWriter is the error output destination. Defaults to os.Stderr.
	ErrWriter io.Writer
}

// Result is the structured outcome of a completion Run invocation.
type Result struct {
	Subcommand string
	// Shell is the detected/installed shell (bash/zsh/fish).
	Shell string
	// InstalledPath is the path where the completion was written (install only).
	InstalledPath string
}

// Run executes the completion subcommand described by cfg.
func Run(cfg Config) (*Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}
	env := cfg.Environ
	if env == nil {
		env = os.Getenv
	}
	home := cfg.HomeDir
	if home == "" {
		home = env("HOME")
	}
	if home == "" {
		home = "/tmp"
	}

	switch cfg.Subcommand {
	case "bash":
		if _, err := cfg.Writer.Write(bashTemplate); err != nil {
			return nil, fmt.Errorf("completion bash: write: %w", err)
		}
		return &Result{Subcommand: "bash", Shell: "bash"}, nil

	case "zsh":
		if _, err := cfg.Writer.Write(zshTemplate); err != nil {
			return nil, fmt.Errorf("completion zsh: write: %w", err)
		}
		return &Result{Subcommand: "zsh", Shell: "zsh"}, nil

	case "fish":
		if _, err := cfg.Writer.Write(fishTemplate); err != nil {
			return nil, fmt.Errorf("completion fish: write: %w", err)
		}
		return &Result{Subcommand: "fish", Shell: "fish"}, nil

	case "install":
		return runInstall(cfg, env, home)

	case "", "-h", "--help", "help":
		PrintHelp(cfg.Writer)
		return &Result{Subcommand: cfg.Subcommand}, nil

	default:
		return nil, fmt.Errorf("completion: unknown subcommand %q (try --help)", cfg.Subcommand)
	}
}

// PrintHelp prints the completion help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos completion <subcommand>

Emit or install shell tab-completion scripts for the yakos CLI.

Subcommands:
  bash         Print the bash completion script to stdout.
  zsh          Print the zsh completion script to stdout.
  fish         Print the fish completion script to stdout.
  install      Auto-detect your shell, write the completion file to a
               conventional path, and print the source-line for your
               shell rc.

Manual install (if you'd rather):

  # bash:
  yakos completion bash > ~/.local/share/bash-completion/completions/yakos
  # then in ~/.bashrc:
  [ -f ~/.local/share/bash-completion/completions/yakos ] && \
      . ~/.local/share/bash-completion/completions/yakos

  # zsh:
  mkdir -p ~/.zsh/completions
  yakos completion zsh > ~/.zsh/completions/_yakos
  # then in ~/.zshrc (before compinit):
  fpath=(~/.zsh/completions $fpath)
  autoload -U compinit && compinit

  # fish:
  mkdir -p ~/.config/fish/completions
  yakos completion fish > ~/.config/fish/completions/yakos.fish
`)
}

// runInstall detects the shell and writes the completion script.
func runInstall(cfg Config, env func(string) string, home string) (*Result, error) {
	// Shell detection: YAKOS_COMPLETION_SHELL > cfg.ShellOverride > $SHELL.
	detected := cfg.ShellOverride
	if detected == "" {
		detected = env("YAKOS_COMPLETION_SHELL")
	}
	if detected == "" {
		shell := env("SHELL")
		switch {
		case strings.HasSuffix(shell, "/bash") || shell == "bash":
			detected = "bash"
		case strings.HasSuffix(shell, "/zsh") || shell == "zsh":
			detected = "zsh"
		case strings.HasSuffix(shell, "/fish") || shell == "fish":
			detected = "fish"
		default:
			return nil, fmt.Errorf(
				"completion install: could not detect shell from $SHELL=%q. "+
					"Override with YAKOS_COMPLETION_SHELL=bash|zsh|fish", shell)
		}
	}

	switch detected {
	case "bash":
		return installBash(cfg, env, home)
	case "zsh":
		return installZsh(cfg, env, home)
	case "fish":
		return installFish(cfg, env, home)
	default:
		return nil, fmt.Errorf("completion install: unsupported shell %q (bash, zsh, or fish)", detected)
	}
}

func installBash(cfg Config, env func(string) string, home string) (*Result, error) {
	dstDir := env("BASH_COMPLETION_USER_DIR")
	if dstDir == "" {
		dstDir = filepath.Join(home, ".local", "share", "bash-completion", "completions")
	}
	if err := os.MkdirAll(dstDir, 0755); err != nil { //nolint:gosec
		return nil, fmt.Errorf("completion install: mkdir %s: %w", dstDir, err)
	}
	dst := filepath.Join(dstDir, "yakos")
	if err := writeFile(dst, bashTemplate); err != nil {
		return nil, fmt.Errorf("completion install: %w", err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "yakos completion install — installed bash completion at:\n")
	_, _ = fmt.Fprintf(cfg.Writer, "  %s\n\n", dst)
	_, _ = fmt.Fprintf(cfg.Writer, "If the file isn't already auto-sourced by your bash-completion setup,\n")
	_, _ = fmt.Fprintf(cfg.Writer, "add this to your ~/.bashrc:\n\n")
	_, _ = fmt.Fprintf(cfg.Writer, "  [ -f %q ] && . %q\n\n", dst, dst)
	_, _ = fmt.Fprintf(cfg.Writer, "Then open a new shell (or 'source ~/.bashrc'). Try 'yakos <TAB><TAB>'.\n")

	return &Result{Subcommand: "install", Shell: "bash", InstalledPath: dst}, nil
}

func installZsh(cfg Config, env func(string) string, home string) (*Result, error) {
	dstDir := env("YAKOS_ZSH_COMPDIR")
	if dstDir == "" {
		dstDir = filepath.Join(home, ".zsh", "completions")
	}
	if err := os.MkdirAll(dstDir, 0755); err != nil { //nolint:gosec
		return nil, fmt.Errorf("completion install: mkdir %s: %w", dstDir, err)
	}
	dst := filepath.Join(dstDir, "_yakos")
	if err := writeFile(dst, zshTemplate); err != nil {
		return nil, fmt.Errorf("completion install: %w", err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "yakos completion install — installed zsh completion at:\n")
	_, _ = fmt.Fprintf(cfg.Writer, "  %s\n\n", dst)
	_, _ = fmt.Fprintf(cfg.Writer, "To activate, add this to your ~/.zshrc BEFORE 'compinit':\n\n")
	_, _ = fmt.Fprintf(cfg.Writer, "  fpath=(%s $fpath)\n", dstDir)
	_, _ = fmt.Fprintf(cfg.Writer, "  autoload -U compinit && compinit\n\n")
	_, _ = fmt.Fprintf(cfg.Writer, "Then open a new shell. Try 'yakos <TAB><TAB>'.\n")

	return &Result{Subcommand: "install", Shell: "zsh", InstalledPath: dst}, nil
}

func installFish(cfg Config, env func(string) string, home string) (*Result, error) {
	dstDir := env("XDG_CONFIG_HOME")
	if dstDir == "" {
		dstDir = filepath.Join(home, ".config")
	}
	dstDir = filepath.Join(dstDir, "fish", "completions")
	if err := os.MkdirAll(dstDir, 0755); err != nil { //nolint:gosec
		return nil, fmt.Errorf("completion install: mkdir %s: %w", dstDir, err)
	}
	dst := filepath.Join(dstDir, "yakos.fish")
	if err := writeFile(dst, fishTemplate); err != nil {
		return nil, fmt.Errorf("completion install: %w", err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "yakos completion install — installed fish completion at:\n")
	_, _ = fmt.Fprintf(cfg.Writer, "  %s\n\n", dst)
	_, _ = fmt.Fprintf(cfg.Writer, "Fish auto-sources files from ~/.config/fish/completions/.\n")
	_, _ = fmt.Fprintf(cfg.Writer, "Open a new shell and try 'yakos <TAB>'.\n")

	return &Result{Subcommand: "install", Shell: "fish", InstalledPath: dst}, nil
}

// writeFile writes data to path with 0644 permissions.
// It does NOT use atomic rename here since completion scripts are not
// sensitive state — they are re-generated on every install call and
// it's acceptable to have a partial write visible to a shell that loads
// completions between the write and rename. (The file is small, < 10 KB.)
func writeFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0644); err != nil { //nolint:gosec
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
