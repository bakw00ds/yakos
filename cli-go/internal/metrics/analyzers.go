package metrics

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/bakw00ds/yakos/internal/stackdetect"
)

// analyzer describes a single tool invocation that populates metric fields.
type analyzer struct {
	// Name is a unique identifier (used as key in ToolStatus).
	Name string
	// Tool is the executable name checked via LookPath.
	Tool string
	// Args are the command arguments.
	Args []string
	// Apply processes the tool's combined output (or run error) and updates m.
	// apply must tolerate non-zero exit codes (linters exit 1 on findings).
	Apply func(out string, runErr error, m *Metrics) error
}

// goBackendAnalyzers lists the [T] analyzers for the go-backend profile.
var goBackendAnalyzers = []analyzer{
	{
		Name: "go-test",
		Tool: "go",
		Args: []string{"test", "-cover", "-race", "./..."},
		Apply: func(out string, runErr error, m *Metrics) error {
			// Parse coverage percentage and test pass/fail.
			cov, passed, total := parseGoTestOutput(out)
			if cov >= 0 {
				m.Test.CoveragePct = floatPtr(cov)
			}
			// Race conditions: if -race output contains "DATA RACE", count them.
			raceCount := strings.Count(out, "DATA RACE")
			m.Test.RaceConditionsDetected = intPtr(raceCount)

			if total > 0 {
				if rate, ok := Rate(passed, total); ok {
					m.Test.TestPassRate = floatPtr(rate)
				}
			} else if runErr == nil {
				// No test output but no error → 1.0 pass rate.
				m.Test.TestPassRate = floatPtr(1.0)
			}
			return nil
		},
	},
	{
		Name: "go-vet",
		Tool: "go",
		Args: []string{"vet", "./..."},
		Apply: func(out string, runErr error, m *Metrics) error {
			// go vet exits non-zero on findings; count finding lines.
			count := 0
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") {
					count++
				}
			}
			m.CodeQuality.VetFindings = intPtr(count)
			return nil
		},
	},
	{
		Name: "golangci-lint",
		Tool: "golangci-lint",
		Args: []string{"run", "--out-format=line-number", "./..."},
		Apply: func(out string, runErr error, m *Metrics) error {
			count := 0
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "level=") {
					count++
				}
			}
			m.CodeQuality.LintFindings = intPtr(count)
			return nil
		},
	},
	{
		Name: "staticcheck",
		Tool: "staticcheck",
		Args: []string{"./..."},
		Apply: func(out string, runErr error, m *Metrics) error {
			count := 0
			for _, line := range strings.Split(out, "\n") {
				if strings.TrimSpace(line) != "" {
					count++
				}
			}
			m.CodeQuality.StaticcheckFindings = intPtr(count)
			return nil
		},
	},
	{
		Name: "gocyclo",
		Tool: "gocyclo",
		Args: []string{"-over", "10", "."},
		Apply: func(out string, runErr error, m *Metrics) error {
			// gocyclo prints one line per function over threshold.
			// Extract the max cyclomatic complexity from the first field.
			maxCC := 0
			for _, line := range strings.Split(out, "\n") {
				parts := strings.Fields(line)
				if len(parts) == 0 {
					continue
				}
				if n, err := strconv.Atoi(parts[0]); err == nil {
					if n > maxCC {
						maxCC = n
					}
				}
			}
			m.CodeQuality.CyclomaticMax = intPtr(maxCC)
			return nil
		},
	},
	{
		Name: "deadcode",
		Tool: "deadcode",
		Args: []string{"-test", "."},
		Apply: func(out string, runErr error, m *Metrics) error {
			count := 0
			for _, line := range strings.Split(out, "\n") {
				if strings.TrimSpace(line) != "" {
					count++
				}
			}
			m.DeadCode.DeadCodeSymbols = intPtr(count)
			return nil
		},
	},
	{
		Name: "gosec",
		Tool: "gosec",
		Args: []string{"-fmt", "json", "-quiet", "./..."},
		Apply: func(out string, runErr error, m *Metrics) error {
			return parseGosecJSON(out, m)
		},
	},
	{
		Name: "govulncheck",
		Tool: "govulncheck",
		Args: []string{"./..."},
		Apply: func(out string, runErr error, m *Metrics) error {
			// Count "Vulnerability #N:" lines.
			count := strings.Count(out, "Vulnerability #")
			m.Security.VulnCount = intPtr(count)
			return nil
		},
	},
}

// nodeAnalyzers lists the [T] analyzers for the node / react-native profile.
var nodeAnalyzers = []analyzer{
	{
		// eslint emits one finding per line when run with --format compact.
		// We capture both warning and error counts; the density metric uses
		// total warnings (warnings + errors) per 100 LOC.
		Name: "eslint",
		Tool: "eslint",
		Args: []string{".", "--format", "compact", "--ext", ".js,.ts,.jsx,.tsx", "--no-eslintrc", "--env", "es2020", "--parser-options", "ecmaVersion:2020"},
		Apply: func(out string, runErr error, m *Metrics) error {
			warnings, errors := parseESLintCompact(out)
			total := warnings + errors
			m.CodeQuality.LintFindings = intPtr(total)
			// Density = findings per 100 LOC. If LOC is 0 (not yet measured)
			// we set a raw count at density=0 rather than nil.
			loc := 0
			if m.SizeChurn.TotalLOC != nil {
				loc = *m.SizeChurn.TotalLOC
			}
			if loc > 0 {
				density := float64(total) / float64(loc) * 100.0
				m.CodeQuality.LintWarningDensity = floatPtr(density)
			} else {
				// LOC unknown — store density=0 to mark tool-ran.
				m.CodeQuality.LintWarningDensity = floatPtr(0)
			}
			return nil
		},
	},
	{
		// tsc --noEmit counts type errors; uses the project's own tsconfig.
		// Falls back gracefully when no tsconfig exists (tsc will error but
		// still emit diagnostic lines we can count).
		Name: "tsc",
		Tool: "tsc",
		Args: []string{"--noEmit"},
		Apply: func(out string, runErr error, m *Metrics) error {
			count := 0
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				// tsc diagnostic lines: "file.ts(row,col): error TS…"
				if strings.Contains(line, ": error TS") {
					count++
				}
			}
			m.CodeQuality.VetFindings = intPtr(count)
			return nil
		},
	},
	{
		// jscpd detects copy-paste duplication; --reporters json prints a JSON
		// report to stdout when combined with --output /dev/stdout.
		Name: "jscpd",
		Tool: "jscpd",
		Args: []string{".", "--reporters", "json", "--output", "/dev/stdout", "--silent"},
		Apply: func(out string, runErr error, m *Metrics) error {
			return parseJSCPDJSON(out, m)
		},
	},
	{
		// knip detects unused exports, dependencies, and files.
		// knip exits 1 when it finds issues (like a linter).
		Name: "knip",
		Tool: "knip",
		Args: []string{"--reporter", "compact"},
		Apply: func(out string, runErr error, m *Metrics) error {
			// Count non-empty output lines as dead-code symbols.
			count := 0
			for _, line := range strings.Split(out, "\n") {
				if strings.TrimSpace(line) != "" {
					count++
				}
			}
			m.DeadCode.DeadCodeSymbols = intPtr(count)
			return nil
		},
	},
	{
		// depcheck finds unused and missing dependencies in package.json.
		Name: "depcheck",
		Tool: "depcheck",
		Args: []string{".", "--json"},
		Apply: func(out string, runErr error, m *Metrics) error {
			return parseDepcheckJSON(out, m)
		},
	},
	{
		// npm audit --json reports CVEs by severity.
		Name: "npm-audit",
		Tool: "npm",
		Args: []string{"audit", "--json"},
		Apply: func(out string, runErr error, m *Metrics) error {
			return parseNPMAuditJSON(out, m)
		},
	},
}

// pythonAnalyzers lists the [T] analyzers for the python profile.
var pythonAnalyzers = []analyzer{
	{
		// ruff outputs one finding per line in default format: "file:row:col: CODE msg"
		// It exits 1 when findings exist.
		Name: "ruff",
		Tool: "ruff",
		Args: []string{"check", "."},
		Apply: func(out string, runErr error, m *Metrics) error {
			count := 0
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "Found ") && !strings.HasPrefix(line, "All checks") {
					count++
				}
			}
			m.CodeQuality.LintFindings = intPtr(count)
			loc := 0
			if m.SizeChurn.TotalLOC != nil {
				loc = *m.SizeChurn.TotalLOC
			}
			if loc > 0 {
				density := float64(count) / float64(loc) * 100.0
				m.CodeQuality.LintWarningDensity = floatPtr(density)
			} else {
				m.CodeQuality.LintWarningDensity = floatPtr(0)
			}
			return nil
		},
	},
	{
		// mypy type checker; exits 1 on type errors.
		Name: "mypy",
		Tool: "mypy",
		Args: []string{".", "--ignore-missing-imports"},
		Apply: func(out string, runErr error, m *Metrics) error {
			count := 0
			for _, line := range strings.Split(out, "\n") {
				// mypy error lines: "file.py:row: error: …"
				if strings.Contains(line, ": error:") {
					count++
				}
			}
			m.CodeQuality.VetFindings = intPtr(count)
			return nil
		},
	},
	{
		// radon cc outputs cyclomatic complexity per function/method.
		// We compute the p90 across all scores.
		// radon cc -s . outputs: "filename\n    func (complexity): score"
		Name: "radon",
		Tool: "radon",
		Args: []string{"cc", "-s", "."},
		Apply: func(out string, runErr error, m *Metrics) error {
			return parseRadonCC(out, m)
		},
	},
	{
		// vulture detects unused code in Python.
		// It exits 0 even when findings exist (unlike most linters).
		Name: "vulture",
		Tool: "vulture",
		Args: []string{"."},
		Apply: func(out string, runErr error, m *Metrics) error {
			count := 0
			for _, line := range strings.Split(out, "\n") {
				if strings.TrimSpace(line) != "" {
					count++
				}
			}
			m.DeadCode.DeadCodeSymbols = intPtr(count)
			return nil
		},
	},
	{
		// deptry detects unused, missing, and misplaced dependencies.
		// Exits non-zero when issues are found.
		Name: "deptry",
		Tool: "deptry",
		Args: []string{"."},
		Apply: func(out string, runErr error, m *Metrics) error {
			// Count lines that look like dependency findings.
			// deptry format: "DEP001 'pkg': …" or similar.
			count := 0
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if line != "" && (strings.HasPrefix(line, "DEP0") || strings.Contains(line, "Detected")) {
					count++
				}
			}
			m.DeadCode.UnusedDeps = intPtr(count)
			return nil
		},
	},
	{
		// bandit -f json outputs structured SAST findings.
		Name: "bandit",
		Tool: "bandit",
		Args: []string{"-r", ".", "-f", "json", "-q"},
		Apply: func(out string, runErr error, m *Metrics) error {
			return parseBanditJSON(out, m)
		},
	},
	{
		// pip-audit scans installed packages for known CVEs.
		// When --format json is used it exits 1 if vulns are found.
		Name: "pip-audit",
		Tool: "pip-audit",
		Args: []string{"--format", "json"},
		Apply: func(out string, runErr error, m *Metrics) error {
			return parsePipAuditJSON(out, m)
		},
	},
}

// rustAnalyzers lists the [T] analyzers for the rust profile.
var rustAnalyzers = []analyzer{
	{
		// cargo clippy --message-format=json emits JSON diagnostic records.
		// Exits 1 when warnings exist (in default mode).
		Name: "cargo-clippy",
		Tool: "cargo",
		Args: []string{"clippy", "--message-format=json", "--all-targets", "--all-features"},
		Apply: func(out string, runErr error, m *Metrics) error {
			return parseCargoClippyJSON(out, m)
		},
	},
	{
		// cargo audit --json scans Cargo.lock for known advisories.
		Name: "cargo-audit",
		Tool: "cargo",
		Args: []string{"audit", "--json"},
		Apply: func(out string, runErr error, m *Metrics) error {
			return parseCargoAuditJSON(out, m)
		},
	},
	{
		// cargo deny check covers licenses, advisories, and bans.
		// We parse advisory violations into VulnCount.
		Name: "cargo-deny",
		Tool: "cargo",
		Args: []string{"deny", "check"},
		Apply: func(out string, runErr error, m *Metrics) error {
			// cargo deny outputs lines like "error[…]: advisory …" for violations.
			count := 0
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "error[") {
					count++
				}
			}
			// Accumulate into VulnCount (cargo-audit may have already set it).
			existing := 0
			if m.Security.VulnCount != nil {
				existing = *m.Security.VulnCount
			}
			m.Security.VulnCount = intPtr(existing + count)
			return nil
		},
	},
	{
		// cargo-machete detects unused dependencies in Cargo.toml.
		// It exits 1 when unused deps are found.
		Name: "cargo-machete",
		Tool: "cargo-machete",
		Args: []string{},
		Apply: func(out string, runErr error, m *Metrics) error {
			// cargo-machete output: "crate_name" one per line for unused deps.
			count := 0
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				// Skip lines that look like headers or empty.
				if line != "" && !strings.Contains(line, "🔍") && !strings.Contains(line, "Found") && !strings.HasPrefix(line, "Note:") {
					count++
				}
			}
			m.DeadCode.UnusedDeps = intPtr(count)
			return nil
		},
	},
}

// crossCuttingAnalyzers run on every project regardless of profile.
var crossCuttingAnalyzers = []analyzer{
	{
		Name: "gitleaks",
		Tool: "gitleaks",
		Args: []string{"detect", "--source", ".", "--no-git", "--report-format", "json", "--exit-code", "0"},
		Apply: func(out string, runErr error, m *Metrics) error {
			// gitleaks JSON is a list of findings.
			var findings []json.RawMessage
			if err := json.Unmarshal([]byte(out), &findings); err != nil {
				// Not valid JSON — try to count lines as fallback.
				count := 0
				for _, line := range strings.Split(out, "\n") {
					if strings.TrimSpace(line) != "" {
						count++
					}
				}
				m.Security.SecretScanHits = intPtr(count)
				return nil
			}
			m.Security.SecretScanHits = intPtr(len(findings))
			return nil
		},
	},
}

// analyzerListFor returns the list of analyzers applicable to the given
// profiles. cross-cutting analyzers are always included.
func analyzerListFor(profiles []stackdetect.Profile) []analyzer {
	var list []analyzer
	if stackdetect.Has(profiles, stackdetect.ProfileGoBackend) {
		list = append(list, goBackendAnalyzers...)
	}
	if stackdetect.Has(profiles, stackdetect.ProfileNode) ||
		stackdetect.Has(profiles, stackdetect.ProfileReactNative) {
		list = append(list, nodeAnalyzers...)
	}
	if stackdetect.Has(profiles, stackdetect.ProfilePython) {
		list = append(list, pythonAnalyzers...)
	}
	if stackdetect.Has(profiles, stackdetect.ProfileRust) {
		list = append(list, rustAnalyzers...)
	}
	list = append(list, crossCuttingAnalyzers...)
	return list
}

// analyzerConcurrency is the max number of concurrent analyzer invocations.
const analyzerConcurrency = 4

// runAnalyzerList runs the given analyzers against projectDir, populating m
// and status. Missing tools are recorded as "tool-missing" and skipped.
// Bounded concurrency via a semaphore. Mutex protects m and status.
//
// This function signature is split from the profile-dispatch logic so that
// the missing-tool/null path is unit-testable with bogus tool names.
func runAnalyzerList(projectDir string, queue []analyzer, m *Metrics, status map[string]string) {
	sem := make(chan struct{}, analyzerConcurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, a := range queue {
		a := a // capture
		// LookPath gate: if the tool is missing, record and skip.
		toolPath, err := exec.LookPath(a.Tool)
		if err != nil {
			mu.Lock()
			status[a.Name] = "tool-missing"
			mu.Unlock()
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			out, runErr := runTool(toolPath, a.Args, projectDir)

			mu.Lock()
			defer mu.Unlock()

			applyErr := a.Apply(out, runErr, m)
			if applyErr != nil {
				status[a.Name] = fmt.Sprintf("error: %v", applyErr)
			} else {
				status[a.Name] = "ok"
			}
		}()
	}
	wg.Wait()
}

// runTool executes the tool at toolPath with args in dir, returning combined
// output and the error. Non-zero exit is not treated as a hard error here —
// linters legitimately exit non-zero.
func runTool(toolPath string, args []string, dir string) (string, error) {
	cmd := exec.Command(toolPath, args...) //nolint:gosec
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// parseGoTestOutput parses `go test -cover` output.
// Returns coverage%, passed count, total count.
// coverage returns -1 when not found.
func parseGoTestOutput(out string) (coverage float64, passed, total int) {
	coverage = -1
	covRe := regexp.MustCompile(`coverage:\s+([\d.]+)%`)
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "ok") || strings.Contains(line, "--- PASS") {
			passed++
			total++
		} else if strings.Contains(line, "--- FAIL") {
			total++
		}
		if m := covRe.FindStringSubmatch(line); len(m) == 2 {
			if f, err := strconv.ParseFloat(m[1], 64); err == nil {
				coverage = f
			}
		}
	}
	return
}

// gosecIssue is a single gosec finding.
type gosecIssue struct {
	Severity string `json:"severity"`
}

// gosecReport is the top-level gosec JSON output.
type gosecReport struct {
	Issues []gosecIssue `json:"Issues"`
}

// parseESLintCompact parses eslint --format compact output and returns the
// count of warnings and errors. Each diagnostic line contains "warning" or
// "error" as the severity token.
func parseESLintCompact(out string) (warnings, errors int) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// compact format: "file: line col, severity - message (rule)"
		// Summary line: "X problems (Y errors, Z warnings)"
		if strings.HasPrefix(line, "/") || strings.HasPrefix(line, ".") {
			if strings.Contains(line, " warning ") || strings.Contains(line, " warning-") {
				warnings++
			} else if strings.Contains(line, " error ") || strings.Contains(line, " error-") {
				errors++
			}
		}
	}
	return
}

// jscpdJSON is the subset of the jscpd JSON report we care about.
type jscpdJSON struct {
	Statistics struct {
		Total struct {
			Percentage float64 `json:"percentage"`
		} `json:"total"`
	} `json:"statistics"`
}

// parseJSCPDJSON parses jscpd --reporters json output and sets DuplicationPct.
func parseJSCPDJSON(out string, m *Metrics) error {
	// jscpd may emit progress lines before the JSON object; find the first '{'.
	idx := strings.Index(out, "{")
	if idx < 0 {
		// No JSON found — jscpd found nothing or output nothing.
		m.CodeQuality.DuplicationPct = floatPtr(0)
		return nil
	}
	var report jscpdJSON
	if err := json.Unmarshal([]byte(out[idx:]), &report); err != nil {
		return fmt.Errorf("jscpd: parse JSON: %w", err)
	}
	m.CodeQuality.DuplicationPct = floatPtr(report.Statistics.Total.Percentage)
	return nil
}

// depcheckJSON is the subset of the depcheck JSON report we care about.
type depcheckJSON struct {
	Dependencies    []string            `json:"dependencies"`
	DevDependencies []string            `json:"devDependencies"`
	Missing         map[string][]string `json:"missing"`
}

// parseDepcheckJSON parses depcheck --json output and sets UnusedDeps.
func parseDepcheckJSON(out string, m *Metrics) error {
	idx := strings.Index(out, "{")
	if idx < 0 {
		m.DeadCode.UnusedDeps = intPtr(0)
		return nil
	}
	var report depcheckJSON
	if err := json.Unmarshal([]byte(out[idx:]), &report); err != nil {
		return fmt.Errorf("depcheck: parse JSON: %w", err)
	}
	unused := len(report.Dependencies) + len(report.DevDependencies)
	m.DeadCode.UnusedDeps = intPtr(unused)
	return nil
}

// npmAuditJSON captures the vulnerability summary from npm audit --json.
// npm v7+ uses a "metadata.vulnerabilities" block; v6 uses a "metadata.vulnerabilities"
// with different keys. We handle both by trying v7 first then v6.
type npmAuditV7JSON struct {
	Metadata struct {
		Vulnerabilities struct {
			Info     int `json:"info"`
			Low      int `json:"low"`
			Moderate int `json:"moderate"`
			High     int `json:"high"`
			Critical int `json:"critical"`
			Total    int `json:"total"`
		} `json:"vulnerabilities"`
	} `json:"metadata"`
}

// parseNPMAuditJSON parses npm audit --json output and sets CVECountBySeverity.
func parseNPMAuditJSON(out string, m *Metrics) error {
	idx := strings.Index(out, "{")
	if idx < 0 {
		m.Security.CVECountBySeverity = map[string]int{}
		return nil
	}
	var report npmAuditV7JSON
	if err := json.Unmarshal([]byte(out[idx:]), &report); err != nil {
		// Malformed output — mark empty rather than error so a single bad
		// invocation doesn't void all other analyzers.
		m.Security.CVECountBySeverity = map[string]int{}
		return nil
	}
	v := report.Metadata.Vulnerabilities
	counts := map[string]int{
		"info":     v.Info,
		"low":      v.Low,
		"moderate": v.Moderate,
		"high":     v.High,
		"critical": v.Critical,
	}
	// Discard zero-count entries to keep the map tidy while preserving
	// the non-nil invariant (empty map = ran and found nothing).
	clean := make(map[string]int)
	for k, c := range counts {
		if c > 0 {
			clean[k] = c
		}
	}
	m.Security.CVECountBySeverity = clean
	return nil
}

// radonCCLine captures the complexity score from a radon cc -s output line.
// Lines look like: "    M 42:4 some_func - A (3)"  where the last parenthesised
// number is the complexity score.
var radonScoreRe = regexp.MustCompile(`\((\d+)\)\s*$`)

// parseRadonCC parses radon cc -s output and sets CyclomaticComplexityP90.
func parseRadonCC(out string, m *Metrics) error {
	var scores []float64
	for _, line := range strings.Split(out, "\n") {
		ms := radonScoreRe.FindStringSubmatch(strings.TrimSpace(line))
		if len(ms) == 2 {
			if n, err := strconv.Atoi(ms[1]); err == nil {
				scores = append(scores, float64(n))
			}
		}
	}
	if len(scores) == 0 {
		m.CodeQuality.CyclomaticComplexityP90 = floatPtr(0)
		return nil
	}
	if p90, ok := Percentile(scores, 90); ok {
		m.CodeQuality.CyclomaticComplexityP90 = floatPtr(p90)
	}
	return nil
}

// banditIssue is a single bandit finding.
type banditIssue struct {
	IssueSeverity string `json:"issue_severity"`
}

// banditReport is the top-level bandit JSON output.
type banditReport struct {
	Results []banditIssue `json:"results"`
}

// parseBanditJSON parses bandit -f json output and populates SASTFindingsBySeverity.
func parseBanditJSON(out string, m *Metrics) error {
	idx := strings.Index(out, "{")
	if idx < 0 {
		m.Security.SASTFindingsBySeverity = map[string]int{}
		return nil
	}
	var report banditReport
	if err := json.Unmarshal([]byte(out[idx:]), &report); err != nil {
		return fmt.Errorf("bandit: parse JSON: %w", err)
	}
	counts := make(map[string]int)
	for _, issue := range report.Results {
		sev := strings.ToLower(issue.IssueSeverity)
		counts[sev]++
	}
	m.Security.SASTFindingsBySeverity = counts
	return nil
}

// pipAuditVuln captures a single pip-audit vulnerability record.
type pipAuditVuln struct {
	ID string `json:"id"`
}

// pipAuditDep is a single dependency record from pip-audit --format json.
type pipAuditDep struct {
	Vulns []pipAuditVuln `json:"vulns"`
}

// parsePipAuditJSON parses pip-audit --format json output and sets VulnCount.
// The JSON schema is: [{"name": "pkg", "version": "x", "vulns": [{...}]}, ...]
func parsePipAuditJSON(out string, m *Metrics) error {
	idx := strings.Index(out, "[")
	if idx < 0 {
		m.Security.VulnCount = intPtr(0)
		return nil
	}
	var deps []pipAuditDep
	if err := json.Unmarshal([]byte(out[idx:]), &deps); err != nil {
		return fmt.Errorf("pip-audit: parse JSON: %w", err)
	}
	total := 0
	for _, d := range deps {
		total += len(d.Vulns)
	}
	m.Security.VulnCount = intPtr(total)
	return nil
}

// cargoMessage is a single JSON message from cargo clippy --message-format=json.
// Cargo emits one JSON object per line (NDJSON).
type cargoMessage struct {
	Reason  string `json:"reason"`
	Message *struct {
		Level string `json:"level"`
	} `json:"message"`
}

// parseCargoClippyJSON parses cargo clippy --message-format=json NDJSON output
// and populates LintFindings with the count of warning-level diagnostics.
func parseCargoClippyJSON(out string, m *Metrics) error {
	warnings := 0
	errors := 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var msg cargoMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Reason != "compiler-message" || msg.Message == nil {
			continue
		}
		switch msg.Message.Level {
		case "warning":
			warnings++
		case "error":
			errors++
		}
	}
	m.CodeQuality.LintFindings = intPtr(warnings + errors)
	return nil
}

// cargoAuditJSON captures the vulnerability summary from cargo audit --json.
type cargoAuditJSON struct {
	Vulnerabilities struct {
		Count int `json:"count"`
	} `json:"vulnerabilities"`
}

// parseCargoAuditJSON parses cargo audit --json output and sets VulnCount.
func parseCargoAuditJSON(out string, m *Metrics) error {
	idx := strings.Index(out, "{")
	if idx < 0 {
		m.Security.VulnCount = intPtr(0)
		return nil
	}
	var report cargoAuditJSON
	if err := json.Unmarshal([]byte(out[idx:]), &report); err != nil {
		return fmt.Errorf("cargo audit: parse JSON: %w", err)
	}
	m.Security.VulnCount = intPtr(report.Vulnerabilities.Count)
	return nil
}

// parseGosecJSON parses gosec -fmt json output and populates security metrics.
func parseGosecJSON(out string, m *Metrics) error {
	// gosec may prefix output before the JSON. Find the first '{'.
	idx := strings.Index(out, "{")
	if idx < 0 {
		// No JSON found — gosec may have printed nothing (no issues).
		m.Security.SASTFindingsBySeverity = map[string]int{}
		return nil
	}
	var report gosecReport
	if err := json.Unmarshal([]byte(out[idx:]), &report); err != nil {
		return fmt.Errorf("gosec: parse JSON: %w", err)
	}
	counts := make(map[string]int)
	for _, issue := range report.Issues {
		sev := strings.ToLower(issue.Severity)
		counts[sev]++
	}
	m.Security.SASTFindingsBySeverity = counts
	return nil
}
