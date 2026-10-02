package doctor

import "github.com/bakw00ds/yakos/internal/refresh"

// checkProjectRules warns when a project's managed specialist rules are
// missing, edited, marker-stripped or stale (K-116). Framed dispatch runs
// claude with --setting-sources project, so these files are the only way a
// dispatched specialist sees the framework rules.
func (r *runner) checkProjectRules() {
	issues := refresh.CheckProjectRules(r.cfg.YakosRoot, r.cfg.ProjectPath)
	if len(issues) == 0 {
		return
	}
	writeln(r, "Project rules (.claude/rules)")
	for _, i := range issues {
		r.warn(SectionProjectRules, "%s", i.String())
	}
	writeln(r, "")
}
