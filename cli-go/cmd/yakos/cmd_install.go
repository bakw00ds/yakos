package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/auth"
	"github.com/bakw00ds/yakos/internal/initialize"
	"github.com/bakw00ds/yakos/internal/install"
	"github.com/bakw00ds/yakos/internal/migrate"
	"github.com/bakw00ds/yakos/internal/passthrough"
	"github.com/bakw00ds/yakos/internal/quickstart"
	"github.com/bakw00ds/yakos/internal/selfupdate"
	"github.com/bakw00ds/yakos/internal/uninstall"
	"github.com/bakw00ds/yakos/internal/update"
	"github.com/bakw00ds/yakos/internal/version"
)

// runInit implements `yakos init` natively in Go.
//
// Usage mirrors cli/lib/init.sh exactly:
//
//	yakos init <name> --project <path> [--force] [--template <kind>]
//	                  [--dry-run] [--with-gate] [--multi-dev] [--help]
//
// The subcommand name in the dispatch switch is "init"; the Go package is
// named "initialize" to avoid the reserved word collision (package init is
// special in Go).
//
// Bash flags --with-gate and --multi-dev are accepted for CLI parity; the
// underlying operations (git hook installation, /var/lib/yakos coord
// provisioning) delegate to bash in Phase 1 and are not performed here.
// initialize.Run still writes the base project scaffold but returns a
// non-nil error for either flag, so this exits non-zero rather than
// silently succeeding as if the flag's work had been done.
func runInit(args []string) {
	name := ""
	project := ""
	template := ""
	force := false
	withGate := false
	multiDev := false
	dryRun := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			initialize.PrintHelp(os.Stdout)
			os.Exit(0)

		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "init: --project requires a path")
				os.Exit(1)
			}
			project = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			project = arg[10:]

		case arg == "--template":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "init: --template requires a kind (base, rails, go, python, node, rust, static-site)")
				os.Exit(1)
			}
			template = args[i]
		case len(arg) > 11 && arg[:11] == "--template=":
			template = arg[11:]

		case arg == "--force":
			force = true
		case arg == "--with-gate":
			withGate = true
		case arg == "--multi-dev":
			multiDev = true
		case arg == "--dry-run":
			dryRun = true

		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "init: unknown flag %q (try --help)\n", arg)
			os.Exit(1)

		default:
			if name == "" {
				name = arg
			} else {
				fmt.Fprintf(os.Stderr, "init: unexpected positional argument %q\n", arg)
				os.Exit(1)
			}
		}
	}

	if name == "" {
		initialize.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "init: missing <name>")
		os.Exit(1)
	}
	if project == "" {
		initialize.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "init: --project <path> is required")
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := initialize.Config{
		Name:        name,
		ProjectPath: project,
		Template:    template,
		Force:       force,
		WithGate:    withGate,
		MultiDev:    multiDev,
		DryRun:      dryRun,
		HomeDir:     home,
		Writer:      os.Stdout,
		ErrWriter:   os.Stderr,
	}

	if _, err := initialize.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "init: %v\n", err)
		os.Exit(1)
	}
}

// runInstall implements `yakos install` natively in Go.
//
// Usage mirrors cli/lib/install.sh exactly:
//
//	yakos install [--force] [--dry-run]
//	yakos install --help
//
// Creates per-file symlinks under ~/.claude/{agents,skills,rules,playbooks}/
// pointing into the YakOS lib/ (repo or materialized embedded copy). Manages
// a launcher symlink at ~/.local/bin/yakos. Merges
// CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 into ~/.claude/settings.json.
//
// Root resolution (see install.ResolveRoot):
//  1. YAKOS_ROOT env → if it has lib/, use it (dev-with-repo mode).
//  2. Exe-adjacent root (inferred by main()) → if it has lib/, use it.
//  3. Embedded lib in binary → materialize to ~/.local/share/yakos/<ver>/
//     and use that (binary-only / curl|sh install).
//  4. None → error with actionable message.
func runInstall(yakosRoot string, args []string) {
	force := false
	dryRun := false

	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			install.PrintHelp(os.Stdout)
			os.Exit(0)
		case "--force":
			force = true
		case "--dry-run":
			dryRun = true
		default:
			fmt.Fprintf(os.Stderr, "install: unknown argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	// Resolve the framework root via the cascade: env → repo → embedded.
	// This replaces the old "YAKOS_ROOT must be set" hard requirement.
	resolvedRoot, err := install.ResolveRoot(yakosRoot, home, version.Version, force, dryRun, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	cfg := install.Config{
		YakosRoot: resolvedRoot,
		HomeDir:   home,
		Force:     force,
		DryRun:    dryRun,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	if _, err := install.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "install: %v\n", err)
		os.Exit(1)
	}
}

// runUninstall implements `yakos uninstall` natively in Go.
//
// Usage mirrors cli/lib/uninstall.sh exactly:
//
//	yakos uninstall [--restore-settings] [--root <path>] [--dry-run]
//	yakos uninstall --help
//
// Removes per-file symlinks under ~/.claude/{agents,skills,rules,playbooks}/
// that point into the YakOS repo. Removes the managed launcher symlink recorded
// in ~/.yakos-state/install-manifest. Removes ~/.yakos and the manifest.
// Handles settings.json according to the created-marker and --restore-settings.
//
// YAKOS_ROOT is not needed by uninstall (it reads ~/.yakos instead). The --root
// flag overrides the pointer file, mirroring the bash --root flag.
func runUninstall(args []string) {
	restoreSettings := false
	explicitRoot := ""
	dryRun := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			uninstall.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--restore-settings":
			restoreSettings = true
		case arg == "--dry-run":
			dryRun = true
		case arg == "--root":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "uninstall: --root requires a path argument")
				os.Exit(1)
			}
			explicitRoot = args[i]
		case len(arg) > 7 && arg[:7] == "--root=":
			explicitRoot = arg[7:]
		default:
			fmt.Fprintf(os.Stderr, "uninstall: unknown argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := uninstall.Config{
		HomeDir:         home,
		ExplicitRoot:    explicitRoot,
		RestoreSettings: restoreSettings,
		DryRun:          dryRun,
		Writer:          os.Stdout,
		ErrWriter:       os.Stderr,
	}

	if _, err := uninstall.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: %v\n", err)
		os.Exit(1)
	}
}

// runUpdate implements `yakos update` natively in Go.
//
// Install-type detection:
//   - Source / dev install (bash tree present at <yakosRoot>/cli/yakos):
//     Runs git pull --ff-only + optional per-project refresh.
//     Flags: --allow-non-ff, --all, --dry-run.
//   - Binary-only install (no bash tree, common curl|sh case):
//     Downloads the latest release from GitHub, verifies SHA-256, and
//     atomically replaces the running binary.
//     Flags: --check, --force, --dry-run.
//
// Mode override flags:
//
//	--binary   Force binary-update path regardless of bash tree presence.
//	--source   Force git-pull path regardless of bash tree presence.
//
// Common to both modes:
//
//	yakos update --check   — report latest version; apply nothing
//	yakos update --help    — print help and exit 0
//
// YAKOS_ROOT must be set in the environment (resolved from the binary
// location by main() when unset).
func runUpdate(yakosRoot string, args []string) {
	// Source-path flags.
	allowNonFF := false
	allProjects := false

	// Binary-path flags.
	checkOnly := false
	force := false

	// Common flags.
	dryRun := false

	// Mode override.
	forceBinary := false
	forceSource := false

	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			printUpdateHelp(os.Stdout)
			os.Exit(0)
		// Source-path flags.
		case "--allow-non-ff":
			allowNonFF = true
		case "--all":
			allProjects = true
		// Binary-path flags.
		case "--check":
			checkOnly = true
		case "--force":
			force = true
		// Common.
		case "--dry-run":
			dryRun = true
		// Mode override.
		case "--binary":
			forceBinary = true
		case "--source":
			forceSource = true
		default:
			fmt.Fprintf(os.Stderr, "update: unknown argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	// Resolve YAKOS_ROOT from env (bash entry-point may set it).
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	if yakosRoot == "" {
		fmt.Fprintln(os.Stderr, "update: YAKOS_ROOT is not set")
		os.Exit(1)
	}

	// --binary and --source are mutually exclusive; silently resolving them
	// would be confusing and could apply the wrong update path.
	if forceBinary && forceSource {
		fmt.Fprintln(os.Stderr, "update: --binary and --source are mutually exclusive; specify at most one")
		os.Exit(1)
	}

	// Determine install type.
	isBinaryInstall := !passthrough.BashYakosExists(yakosRoot)
	if forceBinary {
		isBinaryInstall = true
	}
	if forceSource {
		isBinaryInstall = false
	}

	if isBinaryInstall {
		runUpdateBinary(yakosRoot, checkOnly || dryRun, force)
		return
	}

	// Source / dev install: git pull path.
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	// --check on source path: just print current + latest and exit.
	if checkOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		latest, err := selfupdate.LatestRelease(ctx, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "update: %v\n", err)
			os.Exit(1)
		}
		cur, _ := version.Read(yakosRoot)
		fmt.Fprintf(os.Stdout, "current: %s\nlatest:  %s\n", cur, latest)
		return
	}

	cfg := update.Config{
		YakosRoot:   yakosRoot,
		HomeDir:     home,
		AllowNonFF:  allowNonFF,
		AllProjects: allProjects,
		DryRun:      dryRun,
		Writer:      os.Stdout,
		ErrWriter:   os.Stderr,
	}

	if _, err := update.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		os.Exit(1)
	}
}

// runUpdateBinary runs the self-update path for binary-only installs.
// dryRun covers both --dry-run and --check (no write; just report).
func runUpdateBinary(yakosRoot string, dryRun, force bool) {
	// Two independent timeout bounds govern this operation:
	//   - metadata fetches (GitHub API, checksums.txt): bounded by the metadata
	//     client's own 15 s client.Timeout (BuildDefaultClient), independent of
	//     this context deadline.
	//   - binary download: client.Timeout is 0 on the download client, so this
	//     context deadline is the effective cancellation mechanism.  10 minutes
	//     gives a 256 MiB binary room on a slow link (~3 Mbit/s) while still
	//     providing a DoS bound.  The 256 MiB LimitReader is the size cap.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Determine the running version.  For binary-only installs the ldflags
	// variable is the authoritative source; fall back to the VERSION file
	// if present (dev build without ldflags).
	currentVersion := strings.TrimSpace(version.Version)
	if currentVersion == "" {
		if v, err := version.Read(yakosRoot); err == nil {
			// Strip the " (go)" suffix that Read appends.
			currentVersion = strings.TrimSuffix(strings.TrimSpace(v), " (go)")
			currentVersion = strings.TrimSpace(currentVersion)
		}
	}

	// Resolve the executable path upfront so the operator can see what will
	// be replaced before any network activity begins (MEDIUM-2).
	exePath, exeErr := os.Executable()
	if exeErr == nil {
		exePath, exeErr = filepath.EvalSymlinks(exePath)
	}
	fmt.Fprintf(os.Stdout, "update mode: binary\n")
	if exeErr == nil {
		fmt.Fprintf(os.Stdout, "target binary: %s\n", exePath)
	}
	if currentVersion != "" {
		fmt.Fprintf(os.Stdout, "current version: %s\n", currentVersion)
	}

	res, err := selfupdate.Apply(ctx, selfupdate.Opts{
		CurrentVersion: currentVersion,
		Force:          force,
		DryRun:         dryRun,
		Writer:         os.Stdout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		os.Exit(1)
	}

	// NIT-6: both branches printed the same line; collapse to a single check.
	if !res.AlreadyUpToDate {
		fmt.Fprintf(os.Stdout, "latest version: %s\n", res.NewVersion)
	}

	// Re-provision: the new binary embeds the new lib; exec it to materialize
	// the updated framework files and re-point ~/.claude symlinks.  Skip when
	// already up to date (nothing changed) or in dry-run/check mode.
	if !dryRun && !res.AlreadyUpToDate {
		reprovisionViaSelf(res.ExePath, "update")
	}
}

// printUpdateHelp writes the combined help text for `yakos update`.
func printUpdateHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos update — update yakOS to the latest release

Auto-detects install type:
  binary install  → downloads latest GitHub release + verifies SHA-256
                    + atomically replaces the running binary
  source install  → git pull --ff-only in $YAKOS_ROOT + optional refresh

Mode override (mutually exclusive):
  --binary         Force binary-update path (even if bash tree is present).
  --source         Force git-pull path (even if bash tree is absent).

Binary-install options:
  --check          Report whether an update is available; apply nothing.
  --force          Reinstall latest even when already up to date.
  --dry-run        Print what WOULD happen; write nothing.

Source-install options:
  --allow-non-ff   Allow non-fast-forward git pull.
  --all            After the framework update, discover every deployed
                   project and run yakos refresh on each.
  --dry-run        Print what WOULD happen without running git pull or
                   project refresh.

Common options:
  --help, -h       Print this help.
`)
}

// reprovisionViaSelf execs the binary at exePath (the freshly-placed new
// binary) with the "install" subcommand so the new embedded lib is
// materialized and ~/.claude symlinks are re-pointed to the new version.
// The exec replaces the current process; on success this function never
// returns.  On error a warning is printed and the function returns (the
// caller can continue or exit as appropriate).
//
// cmdLabel is the subcommand name used in warning messages ("update" or
// "upgrade").
//
// If exePath is empty (e.g. the exe resolver failed) the function falls back
// to os.Executable so the best-available path is used.
func reprovisionViaSelf(exePath, cmdLabel string) {
	exe := exePath
	if exe == "" {
		if e, err := os.Executable(); err == nil {
			exe = e
		}
	}
	if exe == "" {
		fmt.Fprintf(os.Stderr, "%s: warning: could not determine binary path for re-provision; run `yakos install` manually\n", cmdLabel)
		return
	}

	fmt.Fprintf(os.Stdout, "\nRe-provisioning framework (materializing embedded lib + refreshing ~/.claude symlinks)...\n")
	if err := execSelf(exe, []string{exe, "install", "--force"}); err != nil {
		// execSelf only returns on non-Unix or on error.
		fmt.Fprintf(os.Stderr, "%s: warning: re-provision exec failed: %v\n", cmdLabel, err)
		fmt.Fprintf(os.Stderr, "  Run `yakos install --force` manually to complete the upgrade.\n")
	}
}

// runUpgrade implements `yakos upgrade`.
//
// Upgrade is the operator-facing command to go from an older installed version
// to the latest, fully provisioned.  It:
//
//  1. Calls selfupdate.Apply to download the latest release from GitHub,
//     verify the SHA-256 checksum, and atomically replace the running binary.
//  2. Execs the new binary with `yakos install --force` to re-materialize the
//     embedded lib and re-point ~/.claude symlinks to the new version.
//
// If already on the latest version, step 1 reports "already up to date" and
// step 2 is still run idempotently (re-provision is a no-op when the lib is
// already current).
//
// Flags: --force (reinstall even if up to date), --dry-run / --check (report
// only; write nothing), --help.
//
// Note on source installs: upgrade is designed for binary-only installs.  On
// a source install (bash tree present), it prints a warning and suggests
// `yakos update` instead.
func runUpgrade(yakosRoot string, args []string) {
	force := false
	dryRun := false
	checkOnly := false

	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			printUpgradeHelp(os.Stdout)
			os.Exit(0)
		case "--force":
			force = true
		case "--dry-run":
			dryRun = true
		case "--check":
			checkOnly = true
		default:
			fmt.Fprintf(os.Stderr, "upgrade: unknown argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	// Resolve YAKOS_ROOT from env.
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}

	// Warn on source installs: upgrade targets binary-only installs.
	if passthrough.BashYakosExists(yakosRoot) {
		fmt.Fprintln(os.Stderr, "upgrade: this appears to be a source install (bash tree detected).")
		fmt.Fprintln(os.Stderr, "  Use `yakos update` to pull the latest from git and refresh.")
		fmt.Fprintln(os.Stderr, "  Use `yakos upgrade --force` to force a binary upgrade anyway.")
		if !force {
			os.Exit(1)
		}
	}

	// --check: just report current vs. latest; apply nothing.
	if checkOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		latest, err := selfupdate.LatestRelease(ctx, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "upgrade: %v\n", err)
			os.Exit(1)
		}
		currentVersion := strings.TrimSpace(version.Version)
		if currentVersion == "" {
			if v, err := version.Read(yakosRoot); err == nil {
				currentVersion = strings.TrimSuffix(strings.TrimSpace(v), " (go)")
				currentVersion = strings.TrimSpace(currentVersion)
			}
		}
		fmt.Fprintf(os.Stdout, "current: %s\nlatest:  %s\n", currentVersion, latest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Determine the running version.
	currentVersion := strings.TrimSpace(version.Version)
	if currentVersion == "" {
		if v, err := version.Read(yakosRoot); err == nil {
			currentVersion = strings.TrimSuffix(strings.TrimSpace(v), " (go)")
			currentVersion = strings.TrimSpace(currentVersion)
		}
	}

	// Resolve the executable path so the operator can see what will be replaced.
	exePath, exeErr := os.Executable()
	if exeErr == nil {
		exePath, exeErr = filepath.EvalSymlinks(exePath)
	}
	fmt.Fprintf(os.Stdout, "yakos upgrade\n\n")
	if exeErr == nil {
		fmt.Fprintf(os.Stdout, "  Binary:          %s\n", exePath)
	}
	if currentVersion != "" {
		fmt.Fprintf(os.Stdout, "  Current version: %s\n", currentVersion)
	}
	fmt.Fprintf(os.Stdout, "\nStep 1: downloading latest release...\n")

	res, err := selfupdate.Apply(ctx, selfupdate.Opts{
		CurrentVersion: currentVersion,
		Force:          force,
		DryRun:         dryRun,
		Writer:         os.Stdout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "upgrade: %v\n", err)
		os.Exit(1)
	}

	if !res.AlreadyUpToDate && !dryRun {
		fmt.Fprintf(os.Stdout, "  Updated:         %s → %s\n", res.OldVersion, res.NewVersion)
	}

	// Step 2: re-provision via the new binary (exec replaces this process).
	// Even when already up to date we still re-provision so the command is
	// idempotent and safe to re-run after any failed previous upgrade.
	if dryRun {
		fmt.Fprintf(os.Stdout, "\n[dry-run] Step 2: would exec new binary with `yakos install --force` to re-provision\n")
		return
	}

	fmt.Fprintf(os.Stdout, "\nStep 2: re-provisioning framework...\n")
	newExe := res.ExePath
	if newExe == "" {
		if e, exeErr2 := os.Executable(); exeErr2 == nil {
			newExe = e
		}
	}
	reprovisionViaSelf(newExe, "upgrade")

	// reprovisionViaSelf only returns on error (exec failed).  Print a fallback
	// hint so the operator knows what to run manually.
	fmt.Fprintf(os.Stdout, "\nBinary updated. Run `yakos install --force` to complete re-provisioning.\n")
}

// printUpgradeHelp writes the help text for `yakos upgrade`.
func printUpgradeHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos upgrade — download the latest release and fully re-provision

Downloads the latest yakOS release from GitHub, verifies the SHA-256
checksum, atomically replaces the running binary, then execs the new
binary with `+"`yakos install --force`"+` to materialize the updated embedded
framework lib and refresh ~/.claude/{agents,skills,rules,playbooks}
symlinks to the new version.

This is the recommended command for in-place upgrades of binary-only
(curl|sh) installs.  It is equivalent to re-running the curl installer
but without requiring curl — all network I/O is native Go.

For source/dev installs (git clone), use `+"`yakos update`"+` instead.

Options:
  --force          Reinstall latest even when already on the current version.
                   Also suppresses the source-install warning.
  --check          Report current vs. latest versions; apply nothing.
  --dry-run        Print what would happen; write nothing.
  --help, -h       Print this help.

Curl one-liner (equivalent):
  curl -fsSL https://raw.githubusercontent.com/bakw00ds/yakos/main/scripts/install.sh | sh
`)
}

// runQuickstart implements `yakos quickstart` natively in Go.
//
// Usage mirrors cli/lib/quickstart.sh exactly:
//
//	yakos quickstart [--runtime <id>] [--multi-dev] [--safe] [--allow-root] [--dry-run]
//	yakos quickstart --help
//
// Detects the current state and runs only what is needed:
//  1. yakOS not installed → yakos install
//  2. cwd is a git repo not yet bootstrapped → yakos init
//  3. project already bootstrapped → yakos start
//
// Each step delegates to the corresponding Go package (install, initialize, start).
// Idempotent; safe to re-run against an already-onboarded project.
func runQuickstart(yakosRoot string, args []string) {
	runtime := ""
	multiDev := false
	safe := false
	allowRoot := false
	dryRun := false

	// Honor YAKOS_ALLOW_ROOT env as equivalent to --allow-root.
	if os.Getenv("YAKOS_ALLOW_ROOT") == "1" {
		allowRoot = true
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			quickstart.PrintHelp(os.Stdout)
			os.Exit(0)

		case arg == "--runtime":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "quickstart: --runtime requires an id")
				os.Exit(1)
			}
			runtime = args[i]
		case len(arg) > 10 && arg[:10] == "--runtime=":
			runtime = arg[10:]

		case arg == "--multi-dev":
			multiDev = true
		case arg == "--safe":
			safe = true
		case arg == "--allow-root":
			allowRoot = true
		case arg == "--dry-run":
			dryRun = true

		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "quickstart: unknown flag %q (try --help)\n", arg)
			os.Exit(1)

		default:
			fmt.Fprintf(os.Stderr, "quickstart: unexpected argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	// Resolve YAKOS_ROOT from env (bash entry-point may set it).
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	if yakosRoot == "" {
		fmt.Fprintln(os.Stderr, "quickstart: YAKOS_ROOT is not set")
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := quickstart.Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		Runtime:   runtime,
		MultiDev:  multiDev,
		Safe:      safe,
		AllowRoot: allowRoot,
		DryRun:    dryRun,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	if _, err := quickstart.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "quickstart: %v\n", err)
		os.Exit(1)
	}
}

// runAuth implements `yakos auth` natively in Go.
//
// Usage mirrors cli/lib/auth.sh exactly:
//
//	yakos auth status [<runtime>]      — report cli + auth state
//	yakos auth login <runtime>         — print login instructions / exec runtime login
//	yakos auth logout <runtime>        — best-effort credential removal
//	yakos auth set-default <runtime>   — persist default runtime
//	yakos auth --help                  — print help and exit 0
//
// OS keychain access uses github.com/zalando/go-keyring, which abstracts
// macOS Keychain Services, Linux secret-service (D-Bus), and Windows DPAPI.
// Keyring access degrades gracefully when the service is unavailable.
func runAuth(args []string) {
	sub := ""
	target := ""
	asDefault := false
	doAll := false

	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			auth.PrintHelp(os.Stdout)
			os.Exit(0)
		default:
			sub = args[0]
			args = args[1:]
		}
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			auth.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--as-default":
			asDefault = true
		case arg == "--all":
			doAll = true
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "auth %s: unknown flag %q\n", sub, arg)
			os.Exit(1)
		default:
			if target == "" {
				target = arg
			} else {
				fmt.Fprintf(os.Stderr, "auth %s: too many positional args\n", sub)
				os.Exit(1)
			}
		}
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := auth.Config{
		HomeDir:    home,
		Subcommand: sub,
		Target:     target,
		AsDefault:  asDefault,
		All:        doAll,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := auth.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "auth: %v\n", err)
		os.Exit(1)
	}
}

// runMigrate implements `yakos migrate` natively in Go.
//
// Usage mirrors cli/lib/migrate.sh (rank 21), adapted to target the
// sidecar schema-version files managed by the kanban and memory packages
// (Decision A, go-port-decisions-2026-06-02.md).
//
//	yakos migrate status              — show schema version for each sidecar format
//	yakos migrate up [<format>]       — apply pending migrations (no-op in Phase 1)
//	yakos migrate down [<format>]     — error; deferred to Phase 1.5
//	yakos migrate --dry-run           — print what WOULD be done without writing
//	yakos migrate --help              — print help and exit 0
//
// The work directory is resolved from YAKOS_WORK_DIR or
// $HOME/agent-control/$YAKOS_PROJECT_NAME/work. The memory directory is
// resolved from YAKOS_MEMORY_DIR or ~/.claude/projects/memory/.
func runMigrate(args []string) {
	sub := ""
	format := ""
	dryRun := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			migrate.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--dry-run":
			dryRun = true
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "migrate: unknown flag %q (try --help)\n", arg)
			os.Exit(1)
		default:
			if sub == "" {
				sub = arg
			} else if format == "" {
				format = arg
			} else {
				fmt.Fprintln(os.Stderr, "migrate: too many positional args (try --help)")
				os.Exit(1)
			}
		}
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	workDir := os.Getenv("YAKOS_WORK_DIR")
	if workDir == "" {
		if proj := os.Getenv("YAKOS_PROJECT_NAME"); proj != "" {
			workDir = filepath.Join(home, "agent-control", proj, "work")
		}
	}

	memDir := os.Getenv("YAKOS_MEMORY_DIR")

	cfg := migrate.Config{
		Subcommand: sub,
		Format:     format,
		WorkDir:    workDir,
		MemoryDir:  memDir,
		HomeDir:    home,
		DryRun:     dryRun,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := migrate.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
}
