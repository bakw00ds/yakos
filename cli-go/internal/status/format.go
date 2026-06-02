package status

import (
	"fmt"
	"io"
	"strings"
)

// Format writes the dashboard report to w in the exact format produced by
// bash status.sh.
//
// bash template (status.sh lines 217-239):
//
//	echo ""
//	echo "Project: $PROJECT"
//	echo "  Control:     $CONTROL_DIR"
//	echo "  Last team:   $session_age_label"
//	echo "  Scratchpad:  ${scratch_size} current/  |  ${archive_size} archive/"
//	echo "  Plan:        ${plan_age}"
//	echo "  Contracts:   ${contracts_age}"
//	echo "  Decisions:   ${decisions_age}${decisions_warn}"
//	echo "  Hooks (current session):"
//	${hook_lines%$'\n'}
//	echo "  Bypasses:    ${bypass_count} active"
//	echo "  Messages:    ${mailbox_count} this session"
//	echo "  Memory:      ${memory_label}"
//	<optional session_warning>
//	<optional scratch_warning>
//	echo ""
func Format(w io.Writer, rpt *Report) error {
	lines := []string{
		"",
		fmt.Sprintf("Project: %s", rpt.Project),
		fmt.Sprintf("  Control:     %s", rpt.ControlDir),
		fmt.Sprintf("  Last team:   %s", rpt.SessionAge),
		fmt.Sprintf("  Scratchpad:  %s current/  |  %s archive/", rpt.ScratchSize, rpt.ArchiveSize),
		fmt.Sprintf("  Plan:        %s", rpt.PlanAge),
		fmt.Sprintf("  Contracts:   %s", rpt.ContractsAge),
		fmt.Sprintf("  Decisions:   %s%s", rpt.DecisionsAge, rpt.DecisionsWarn),
		"  Hooks (current session):",
	}

	// Hook lines: each one indented with 4 spaces, matching bash.
	for _, h := range rpt.Hooks {
		if h.Summary == "" {
			// Placeholder entry: "(no hook logs yet)"
			lines = append(lines, "    "+h.Name)
		} else {
			hookLine := fmt.Sprintf("    %s: %s", h.Name, h.Summary)
			if h.LastTS != "" {
				hookLine += fmt.Sprintf(" (last %s)", h.LastTS)
			}
			lines = append(lines, hookLine)
		}
	}

	lines = append(lines,
		fmt.Sprintf("  Bypasses:    %d active", rpt.BypassCount),
		fmt.Sprintf("  Messages:    %d this session", rpt.MailboxCount),
		fmt.Sprintf("  Memory:      %s", rpt.MemoryLabel),
	)

	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}

	if rpt.SessionWarn != "" {
		if _, err := fmt.Fprintf(w, "  ⚠ %s\n", rpt.SessionWarn); err != nil {
			return err
		}
	}
	if rpt.ScratchWarn != "" {
		if _, err := fmt.Fprintf(w, "  ⚠ %s\n", rpt.ScratchWarn); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintln(w)
	return err
}

// PrintHelp writes the help text for `yakos status` to w, byte-identical
// to bash status.sh --help output.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.Join([]string{
		"yakos status <project>",
		"",
		"Print a dashboard for a project under ~/agent-control/. Reports session",
		"age, scratchpad size, recent hook outcomes, bypass count, mailbox",
		"message count, and MEMORY.md state. Surfaces soft warnings on",
		"session age >4h or scratchpad >100MB.",
		"",
		"Usage: yakos status <project>",
		"",
	}, "\n"))
}
