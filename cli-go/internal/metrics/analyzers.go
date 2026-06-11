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
	// Other profiles detected but no tool sets in Phase-1.
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
