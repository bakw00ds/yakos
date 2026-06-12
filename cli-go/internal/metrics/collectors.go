package metrics

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/cost"
)

// collectEfficiency reads dispatch log events and populates the efficiency
// and dispatch metrics on m.
func collectEfficiency(stateDir, projectDir string, since string, m *Metrics) {
	files, err := cost.LogFiles(stateDir)
	if err != nil || len(files) == 0 {
		return
	}

	ch := cost.StreamFiles(files, since)

	var costs []float64
	var tokens []float64
	totalCost := 0.0
	totalCount := 0
	successCount := 0

	for ev := range ch {
		// Filter to current project. Event.Project is an absolute path.
		// Match by absolute equality first, fall back to filepath.Base.
		if projectDir != "" && ev.Project != "" {
			if ev.Project != projectDir && filepath.Base(ev.Project) != filepath.Base(projectDir) {
				continue
			}
		}

		totalCount++
		if ev.ExitCode == 0 {
			successCount++
		}

		var evCost float64
		var evTokens float64

		if ev.Usage != nil {
			evCost = ev.Usage.TotalCostUSD
			evTokens = float64(ev.Usage.InputTokens + ev.Usage.OutputTokens)
		} else {
			// Fall back to estimated tokens.
			evTokens = float64(ev.EstInputTokens + ev.EstOutputTokens)
			// No cost estimate without Usage.
		}

		if evCost > 0 {
			costs = append(costs, evCost)
			totalCost += evCost
		}
		if evTokens > 0 {
			tokens = append(tokens, evTokens)
		}
	}

	if totalCount > 0 {
		m.Efficiency.TaskCount = intPtr(totalCount)

		if totalCost > 0 {
			m.Efficiency.TotalCostUSD = floatPtr(totalCost)
		}
		if med, ok := Median(costs); ok {
			m.Efficiency.MedianCostPerTaskUSD = floatPtr(med)
		}
		if mean, ok := Mean(costs); ok {
			m.Efficiency.MeanCostPerTaskUSD = floatPtr(mean)
		}
		if med, ok := Median(tokens); ok {
			m.Efficiency.MedianTokensPerTask = floatPtr(med)
		}
		if mean, ok := Mean(tokens); ok {
			m.Efficiency.MeanTokensPerTask = floatPtr(mean)
		}

		// first_try_success_rate: fraction with exit_code==0.
		// This is a lossy proxy — no per-task correlation id exists in the
		// dispatch log schema to identify true retry groups.
		if rate, ok := Rate(successCount, totalCount); ok {
			m.Dispatch.FirstTrySuccessRate = floatPtr(rate)
		}
	}
}

// collectModelRouting reads model-routing-candidates.ndjson and populates
// the model_routing metrics on m.
func collectModelRouting(stateDir string, m *Metrics) {
	path := filepath.Join(stateDir, "model-routing-candidates.ndjson")
	f, err := os.Open(path) //nolint:gosec
	if os.IsNotExist(err) {
		// File absent → null (per decision #3)
		return
	}
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	type candidateRecord struct {
		Agent                      string  `json:"agent"`
		SuggestedModel             string  `json:"suggested_model"`
		EstimatedMonthlySavingsUSD float64 `json:"estimated_monthly_savings_usd"`
	}

	agentsWithCandidate := make(map[string]bool)
	totalSavings := 0.0
	allAgents := make(map[string]bool)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec candidateRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		allAgents[rec.Agent] = true
		if rec.SuggestedModel != "" {
			// Agent has an open downgrade candidate → not right-sized.
			agentsWithCandidate[rec.Agent] = true
			totalSavings += rec.EstimatedMonthlySavingsUSD
		}
	}

	totalAgents := len(allAgents)
	if totalAgents == 0 {
		return
	}

	m.ModelRouting.TotalSuggestedMonthlySavingsUSD = floatPtr(totalSavings)

	rightSized := totalAgents - len(agentsWithCandidate)
	if rate, ok := Rate(rightSized, totalAgents); ok {
		m.ModelRouting.RightSizedPct = floatPtr(rate)
	}
}

// collectDORA computes DORA signals from git tags.
// A single git for-each-ref call fetches all tag names + timestamps;
// filtering is done in Go rather than spawning O(tags) git processes.
func collectDORA(runner gitRunner, projectDir string, since string, m *Metrics) {
	entries, err := getDeployTagsWithTimestamps(runner, projectDir)
	if err != nil || len(entries) == 0 {
		return
	}

	// Filter by since window in Go — no additional git spawns needed.
	if since != "" {
		sinceTime, parseErr := parseISO(since)
		if parseErr == nil {
			var filtered []tagEntry
			for _, e := range entries {
				t, err := parseISO(e.TS)
				if err != nil {
					continue
				}
				if !t.Before(sinceTime) {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
		}
	}

	m.DORA.DeployFrequency = intPtr(len(entries))

	// Extract tag names for getLeadTimes (which needs per-tag log for ancestry).
	tagNames := make([]string, len(entries))
	for i, e := range entries {
		tagNames[i] = e.Name
	}
	leadTimes, err := getLeadTimes(runner, projectDir, tagNames)
	if err == nil {
		if med, ok := Median(leadTimes); ok {
			m.DORA.LeadTimeMedianHours = floatPtr(med)
		}
	}
}

// collectSizeChurn populates size/churn metrics by walking source files
// (bounded walk) and running git numstat.
func collectSizeChurn(runner gitRunner, projectDir string, m *Metrics) {
	loc, fileCount := countLOC(projectDir)
	if fileCount > 0 {
		m.SizeChurn.TotalLOC = intPtr(loc)
		m.SizeChurn.FileCount = intPtr(fileCount)
	}

	churn, err := getChurnLines(runner, projectDir, 30)
	if err == nil {
		m.SizeChurn.ChurnLinesLast30 = intPtr(churn)
	}
}

// sourceExts is the set of file extensions counted as source LOC.
var sourceExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".py": true, ".rb": true, ".rs": true, ".java": true, ".kt": true,
	".cs": true, ".swift": true, ".dart": true, ".c": true, ".cpp": true,
	".h": true, ".hpp": true,
}

// skipLOCDirs mirrors stackdetect skip list plus test/docs dirs.
var skipLOCDirs = map[string]bool{
	"vendor": true, "node_modules": true, ".git": true, "target": true,
	"dist": true, "build": true, "_build": true, ".build": true,
	"__pycache__": true, ".next": true, ".cache": true,
	".yakos": true,
}

// countLOC does a bounded walk counting total non-empty lines in source files.
func countLOC(projectDir string) (loc, fileCount int) {
	_ = walkLOC(projectDir, 0, &loc, &fileCount)
	return
}

func walkLOC(dir string, depth int, loc, fileCount *int) error {
	if depth > 6 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if skipLOCDirs[name] {
				continue
			}
			_ = walkLOC(filepath.Join(dir, name), depth+1, loc, fileCount)
			continue
		}
		ext := filepath.Ext(name)
		if !sourceExts[ext] {
			continue
		}
		lines := countFileLines(filepath.Join(dir, name))
		if lines > 0 {
			*fileCount++
			*loc += lines
		}
	}
	return nil
}

func countFileLines(path string) int {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	count := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		if len(strings.TrimSpace(scanner.Text())) > 0 {
			count++
		}
	}
	return count
}

// hookBypassRe matches a bypass entry heading.
var hookBypassRe = regexp.MustCompile(`^## bypass:`)

// collectHookBypass parses work/current/hook-bypass.md and counts non-expired
// bypass entries under the "## Active entries" heading.
// Mirrors the parser logic in lib/hooks/lib/hook-output.sh ho_check_bypass.
func collectHookBypass(projectWorkDir string, now time.Time, m *Metrics) {
	path := filepath.Join(projectWorkDir, "work", "current", "hook-bypass.md")
	data, err := os.ReadFile(path) //nolint:gosec
	if os.IsNotExist(err) {
		// File absent → null (per decision #6)
		return
	}
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")
	inActiveSection := false
	count := 0
	var currentBypassExpires string
	inBypass := false

	for _, line := range lines {
		if line == "## Active entries" {
			inActiveSection = true
			continue
		}
		if !inActiveSection {
			continue
		}
		// bypass: entries are ## bypass:<id> — check before the generic ## stop.
		if hookBypassRe.MatchString(line) {
			// Finalise previous bypass entry if any.
			if inBypass && !isExpired(currentBypassExpires, now) {
				count++
			}
			inBypass = true
			currentBypassExpires = ""
			continue
		}
		// Stop at any non-bypass ## section.
		if strings.HasPrefix(line, "## ") {
			// Finalise current bypass entry if any.
			if inBypass && !isExpired(currentBypassExpires, now) {
				count++
			}
			inBypass = false
			inActiveSection = false
			continue
		}
		if inBypass && strings.HasPrefix(line, "**Expires:**") {
			currentBypassExpires = strings.TrimPrefix(line, "**Expires:**")
			currentBypassExpires = strings.TrimSpace(currentBypassExpires)
		}
	}
	// Handle last bypass entry.
	if inBypass && !isExpired(currentBypassExpires, now) {
		count++
	}

	m.Dispatch.HookBypassCount = intPtr(count)
}

// isExpired returns true if the expires string represents a time before now.
// Returns false (not expired) on any parse error.
func isExpired(expires string, now time.Time) bool {
	if expires == "" {
		return false
	}
	t, err := parseISO(expires)
	if err != nil {
		return false
	}
	return t.Before(now)
}
