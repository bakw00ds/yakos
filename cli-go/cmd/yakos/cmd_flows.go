package main

// cmd_flows.go: `yakos flows schedule enable|disable <workflow>` (K-172).
//
// It is the writer of the trusted schedules file that turns a workflow's
// declared cron and webhook triggers on. Running it is the operator's consent:
// it pins the workflow file's SHA-256, so a later edit stops the trigger until
// the command is run again. The write goes through workflow.EnableSchedule /
// DisableSchedule (statepath.EditYAML: trust-checked read, owner-only 0600 atomic
// rename) and is recorded in the dispatch log like the other policy writers.
// No message prints a path.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bakw00ds/yakos/internal/cliflag"
	"github.com/bakw00ds/yakos/internal/statepath"
	"github.com/bakw00ds/yakos/internal/workflow"
)

// isFlowsForceGo reports whether this invocation is `yakos flows ...`. It has no
// bash equivalent, so shadow mode must not hand it to the bash CLI.
func isFlowsForceGo(args []string) bool {
	return len(args) > 0 && args[0] == "flows"
}

func printFlowsHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos flows schedule <enable|disable> <workflow> [flags]

Turn a workflow's declared triggers (triggers.cron, triggers.webhook) on or off.
A declaration in the workflow file never fires by itself; this command writes the
operator's enablement to the trusted schedules file in ~/.yakos-state, keyed by
the project's canonical (symlink-resolved) path, mode 0600, replaced atomically.

Subcommands:
    schedule enable <workflow> [--cron] [--webhook] [--project DIR]
                          Enable the named triggers (default: every trigger the
                          workflow declares). Pins the workflow file's SHA-256:
                          editing the file stops the trigger until you run enable
                          again. Review the file first; running this is your consent.
    schedule disable <workflow> [--project DIR]
                          Remove the workflow's entry.

A webhook also needs its secret: put it in ~/.yakos-state/webhook-secrets/<secret_env>
(mode 0600, the directory 0700), or in the daemon's environment. The file wins.

Flags:
    --cron                enable: only the cron trigger.
    --webhook             enable: only the webhook trigger.
    --project DIR         The project (default: the current directory). The
                          workflow is read from DIR/work/current/workflows/.
`)
}

func runFlows(args []string) {
	cwd, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "flows: cannot resolve the current directory")
		os.Exit(1)
	}
	os.Exit(flowsMain(args, os.Stdout, os.Stderr, cwd, statepath.TrustedDir()))
}

// flowsMain is runFlows with its streams and surroundings injected.
func flowsMain(args []string, stdout, stderr io.Writer, cwd, stateDir string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printFlowsHelp(stdout)
		if len(args) == 0 {
			return 1
		}
		return 0
	}
	fail := func(format string, a ...any) int {
		_, _ = fmt.Fprintf(stderr, "flows: %s\n", fmt.Sprintf(format, a...))
		return 1
	}
	if args[0] != "schedule" || len(args) < 2 {
		return fail("usage: yakos flows schedule <enable|disable> <workflow>")
	}
	sub := args[1]
	if sub != "enable" && sub != "disable" {
		return fail("unknown schedule subcommand %q (enable | disable)", sub)
	}
	var help, cron, web bool
	var project string
	specs := []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--project", Kind: cliflag.String, Str: &project, ValueDesc: "a path"},
	}
	if sub == "enable" {
		specs = append(specs,
			cliflag.Spec{Name: "--cron", Kind: cliflag.Bool, Bool: &cron},
			cliflag.Spec{Name: "--webhook", Kind: cliflag.Bool, Bool: &web})
	}
	set := &cliflag.Set{Cmd: "flows schedule " + sub, Specs: specs}
	pos, err := set.Parse(args[2:])
	if err != nil {
		return fail("%v", err)
	}
	if help {
		printFlowsHelp(stdout)
		return 0
	}
	if len(pos) != 1 {
		return fail("usage: yakos flows schedule %s <workflow>", sub)
	}
	name := pos[0]
	if err := workflow.ValidateID("workflow name", name); err != nil {
		return fail("%v", err)
	}
	if stateDir == "" {
		return fail("no home directory, so no schedules file to write")
	}
	root := cwd
	if project != "" {
		root = project
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}

	au := openAudit(stderr, stateDir)
	if au == nil {
		return 1
	}
	defer au.Close()
	const file = "schedules.yml" // the audit label; the real file name carries a path hash

	if sub == "disable" {
		res, err := workflow.DisableSchedule(root, name)
		if errors.Is(err, workflow.ErrScheduleNoop) {
			_, _ = fmt.Fprintf(stdout, "unchanged: %s is not enabled\n", name)
			return 0
		}
		if err != nil {
			return fail("%v", err)
		}
		return reportWrite(stdout, stderr, au, file, "flows.schedule.disable", "disabled "+name, res)
	}

	wf, sha, err := workflow.LoadFile(filepath.Join(root, "work", "current", "workflows", name+".yaml"))
	if err != nil {
		return fail("cannot load workflow %q", name)
	}
	if err := workflow.Validate(wf); err != nil {
		return fail("workflow %q is invalid: %v", name, err)
	}
	if wf.Name != name {
		return fail("workflow file declares the name %q, not %q", wf.Name, name)
	}
	ch := workflow.ScheduleChange{Cron: cron, Webhook: web}
	if !cron && !web { // default: everything the workflow declares
		ch.Cron = wf.Triggers != nil && wf.Triggers.Cron != ""
		ch.Webhook = wf.Triggers != nil && wf.Triggers.Webhook != nil
	}
	if !ch.Cron && !ch.Webhook {
		return fail("workflow %q declares no triggers", name)
	}
	res, err := workflow.EnableSchedule(root, wf, sha, ch)
	if err != nil {
		return fail("%v", err)
	}
	return reportWrite(stdout, stderr, au, file, "flows.schedule.enable",
		fmt.Sprintf("enabled %s (%s), pinned to workflow sha %s", name, ch, sha[:12]), res)
}
