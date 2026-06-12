// Package planscore is the Go port of cli/lib/plan-score.sh (rank 35).
//
// It gives operators visibility into plan-quality evaluation records stored
// in ~/.yakos-state/plan-quality-log.ndjson. Records are written by the
// plan-quality-eval skill; this package reads and summarises them.
//
// # Synopsis
//
//	yakos plan score show [<plan_id>]
//	yakos plan score history [--project <name>] [--limit <n>]
//	yakos plan score override <plan_id> --reason "<text>"
//	yakos plan score correlate [--project <p>] [--since <iso>] [--min-n <n>]
//
// # Log format
//
// Each line in plan-quality-log.ndjson is a JSON object. Relevant types:
//   - "plan_scored"        — emitted by plan-quality-eval skill
//   - "plan_score_override" — emitted by this package's override subcommand
//   - "plan_outcome"       — emitted by work close
//
// # Correlate
//
// The correlate subcommand joins plan_scored + plan_outcome records and
// computes per-dimension Pearson r against first_try_pass, scope-creep by
// aggregate-score quartile, and threshold → outcome rates. Requires at least
// --min-n (default 20) joined records.
//
// # State files
//
// Plan-blocked marker: <work>/current/.plan-blocked
// Log: ~/.yakos-state/plan-quality-log.ndjson (YAKOS_PLAN_QUALITY_LOG overrides)
package planscore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// defaultHistoryLimit is how many records history shows by default.
const defaultHistoryLimit = 20

// defaultMinN is the minimum joined records required for correlate.
const defaultMinN = 20

// ---- public types -----------------------------------------------------------

// Config carries everything Run needs.
type Config struct {
	// Subcommand is one of: show, history, override, correlate.
	Subcommand string

	// PlanID is the target plan identifier for show and override.
	PlanID string

	// Reason is required for override.
	Reason string

	// Project filters history/correlate by project name.
	Project string

	// Limit is the maximum number of history rows (default 20).
	Limit int

	// Since is the ISO-8601 cutoff for correlate (default: 90 days ago).
	Since string

	// MinN is the minimum joined record count for correlate (default 20).
	MinN int

	// PlanQualityLog overrides the default log path.
	// When empty, $YAKOS_PLAN_QUALITY_LOG or ~/.yakos-state/plan-quality-log.ndjson is used.
	PlanQualityLog string

	// CurrentDir overrides the work/current directory resolution.
	// When empty, the default resolution logic is used.
	CurrentDir string

	// HomeDir overrides $HOME for path resolution. When empty, os.Getenv("HOME") is used.
	HomeDir string

	// Now is injected for tests; zero value uses time.Now().
	Now time.Time

	// Writer receives normal output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives advisory/warning messages (default: os.Stderr).
	ErrWriter io.Writer
}

// Result summarises what Run did.
type Result struct {
	// Subcommand that was executed.
	Subcommand string

	// RecordsFound is the number of matching records processed.
	RecordsFound int

	// OverrideWritten is true when an override record was appended.
	OverrideWritten bool

	// BlockedMarkerRemoved is true when .plan-blocked was removed.
	BlockedMarkerRemoved bool

	// CorrelateN is the number of joined records used in correlate.
	CorrelateN int
}

// planScoredRecord is a single plan_scored line from the log.
type planScoredRecord struct {
	Type            string             `json:"type"`
	Ts              string             `json:"ts"`
	PlanID          string             `json:"plan_id"`
	Project         string             `json:"project"`
	Verdict         string             `json:"verdict"`
	AggregateScore  float64            `json:"aggregate_score"`
	Dissent         bool               `json:"dissent"`
	CostUSD         float64            `json:"cost_usd"`
	PerDimensionMed map[string]float64 `json:"per_dimension_median"`
}

// planOutcomeRecord is a single plan_outcome line from the log.
type planOutcomeRecord struct {
	Type            string   `json:"type"`
	Ts              string   `json:"ts"`
	PlanID          string   `json:"plan_id"`
	Project         string   `json:"project"`
	FirstTryPass    *bool    `json:"first_try_pass"`
	ScopeCreepRatio *float64 `json:"scope_creep_ratio"`
	ReworkCycles    *int     `json:"rework_cycles"`
}

// planOverrideRecord is written to the log by the override subcommand.
type planOverrideRecord struct {
	Type   string `json:"type"`
	Ts     string `json:"ts"`
	PlanID string `json:"plan_id"`
	Reason string `json:"reason"`
	User   string `json:"user"`
}

// planBlockedMarker is the JSON content of .plan-blocked.
type planBlockedMarker struct {
	PlanID string `json:"plan_id"`
}

// ---- entry point ------------------------------------------------------------

// Run executes the plan score subcommand according to cfg.
func Run(cfg Config) (*Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}

	logPath := cfg.resolveLogPath()

	switch cfg.Subcommand {
	case "show":
		return runShow(cfg, logPath)
	case "history":
		return runHistory(cfg, logPath)
	case "override":
		return runOverride(cfg, logPath)
	case "correlate":
		return runCorrelate(cfg, logPath)
	default:
		return nil, fmt.Errorf("plan score: unknown subcommand %q (try 'yakos plan score help')", cfg.Subcommand)
	}
}

// PrintHelp writes the help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos plan score <subcommand> [args...]

Subcommands:
  show [<plan_id>]
      Pretty-print the most recent scoring record (or the most recent
      record for a specific plan_id).

  history [--project <name>] [--limit <n>]
      Tabular history: ts | plan_id | verdict | aggregate | dissent | cost_usd
      Sorted newest-first. Default limit: 20.

  override <plan_id> --reason "<text>"
      Mark a blocked plan as reviewed. Appends an override record to the
      log and removes work/current/.plan-blocked if it matches the plan_id.
      Requires --reason. Refuses if plan_id has no existing scored record.

  correlate [--project <p>] [--since <iso>] [--min-n <n>]
      Join plan_scored + plan_outcome records by plan_id and emit a
      report showing per-dimension Pearson r against first_try_pass,
      scope-creep by score quartile, and threshold -> outcome rates.
      Requires at least --min-n (default 20) joined records.

Log file: ~/.yakos-state/plan-quality-log.ndjson
`)
}

// ---- config helpers ---------------------------------------------------------

func (cfg Config) resolveLogPath() string {
	if cfg.PlanQualityLog != "" {
		return cfg.PlanQualityLog
	}
	if env := os.Getenv("YAKOS_PLAN_QUALITY_LOG"); env != "" {
		return env
	}
	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}
	return filepath.Join(home, ".yakos-state", "plan-quality-log.ndjson")
}

func (cfg Config) now() time.Time {
	if !cfg.Now.IsZero() {
		return cfg.Now
	}
	return time.Now()
}

// ---- subcommand: show -------------------------------------------------------

func runShow(cfg Config, logPath string) (*Result, error) {
	res := &Result{Subcommand: "show"}

	if !fileHasContent(logPath) {
		_, _ = fmt.Fprintf(cfg.ErrWriter, "No plan-quality-log.ndjson found at %s\n", logPath)
		_, _ = fmt.Fprintln(cfg.ErrWriter, "Run 'yakos skill plan-quality-eval <plan.md>' first.")
		return nil, fmt.Errorf("plan score show: no records")
	}

	var record *planScoredRecord

	if cfg.PlanID != "" {
		// Find the most recent plan_scored record matching plan_id.
		records, err := readScoredRecords(logPath, cfg.PlanID, "")
		if err != nil {
			return nil, err
		}
		if len(records) == 0 {
			return nil, fmt.Errorf("plan score show: no record found for plan_id: %s", cfg.PlanID)
		}
		record = &records[len(records)-1]
	} else {
		// Find the most recent plan_scored record overall.
		records, err := readScoredRecords(logPath, "", "")
		if err != nil {
			return nil, err
		}
		if len(records) == 0 {
			return nil, fmt.Errorf("plan score show: no plan_scored records in log")
		}
		record = &records[len(records)-1]
	}

	res.RecordsFound = 1
	renderScoreRecord(cfg.Writer, record)
	return res, nil
}

// renderScoreRecord pretty-prints a plan_scored record.
func renderScoreRecord(w io.Writer, r *planScoredRecord) {
	_, _ = fmt.Fprintf(w, "Plan Quality Score\n")
	_, _ = fmt.Fprintf(w, "==================\n")
	_, _ = fmt.Fprintf(w, "plan_id:         %s\n", r.PlanID)
	_, _ = fmt.Fprintf(w, "ts:              %s\n", r.Ts)
	_, _ = fmt.Fprintf(w, "verdict:         %s\n", r.Verdict)
	_, _ = fmt.Fprintf(w, "aggregate_score: %.2f\n", r.AggregateScore)
	_, _ = fmt.Fprintf(w, "dissent:         %v\n", r.Dissent)
	_, _ = fmt.Fprintf(w, "cost_usd:        %.4f\n", r.CostUSD)
	if r.Project != "" {
		_, _ = fmt.Fprintf(w, "project:         %s\n", r.Project)
	}
	if len(r.PerDimensionMed) > 0 {
		_, _ = fmt.Fprintf(w, "\nPer-dimension medians:\n")
		// Sort for stable output.
		dims := make([]string, 0, len(r.PerDimensionMed))
		for d := range r.PerDimensionMed {
			dims = append(dims, d)
		}
		sort.Strings(dims)
		for _, d := range dims {
			_, _ = fmt.Fprintf(w, "  %-44s %.2f\n", d, r.PerDimensionMed[d])
		}
	}
}

// ---- subcommand: history ----------------------------------------------------

func runHistory(cfg Config, logPath string) (*Result, error) {
	res := &Result{Subcommand: "history"}

	limit := cfg.Limit
	if limit <= 0 {
		limit = defaultHistoryLimit
	}

	if !fileHasContent(logPath) {
		_, _ = fmt.Fprintln(cfg.Writer, "No records found. Run 'yakos skill plan-quality-eval <plan.md>' first.")
		return res, nil
	}

	records, err := readScoredRecords(logPath, "", cfg.Project)
	if err != nil {
		return nil, err
	}

	// Reverse: newest-first.
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}

	if len(records) > limit {
		records = records[:limit]
	}

	res.RecordsFound = len(records)

	// Print header.
	_, _ = fmt.Fprintf(cfg.Writer, "%-26s  %-28s  %-7s  %-9s  %-7s  %-10s\n",
		"ts", "plan_id", "verdict", "aggregate", "dissent", "cost_usd")
	_, _ = fmt.Fprintln(cfg.Writer, strings.Repeat("-", 26)+"  "+strings.Repeat("-", 28)+"  "+
		strings.Repeat("-", 7)+"  "+strings.Repeat("-", 9)+"  "+
		strings.Repeat("-", 7)+"  "+strings.Repeat("-", 10))

	for i := range records {
		r := &records[i]
		ts := r.Ts
		if len(ts) > 24 {
			ts = ts[:24]
		}
		pid := r.PlanID
		if len(pid) > 28 {
			pid = pid[:28]
		}
		_, _ = fmt.Fprintf(cfg.Writer, "%-26s  %-28s  %-7s  %-9.4g  %-7v  %-10.4f\n",
			ts, pid, r.Verdict, r.AggregateScore, r.Dissent, r.CostUSD)
	}

	return res, nil
}

// ---- subcommand: override ---------------------------------------------------

func runOverride(cfg Config, logPath string) (*Result, error) {
	res := &Result{Subcommand: "override"}

	if cfg.PlanID == "" {
		return nil, fmt.Errorf("plan score override: <plan_id> is required")
	}
	if cfg.Reason == "" {
		return nil, fmt.Errorf("plan score override: --reason is required")
	}

	// Verify at least one existing scored record.
	existing, err := readScoredRecords(logPath, cfg.PlanID, "")
	if err != nil {
		return nil, err
	}
	if len(existing) == 0 {
		_, _ = fmt.Fprintf(cfg.ErrWriter, "plan score override: no existing record for plan_id '%s'\n", cfg.PlanID)
		_, _ = fmt.Fprintln(cfg.ErrWriter, "Use 'yakos plan score history' to see recorded plan IDs.")
		return nil, fmt.Errorf("plan score override: no record for plan_id %q", cfg.PlanID)
	}

	// Append override record.
	ts := cfg.now().UTC().Format(time.RFC3339)
	user := os.Getenv("USER")
	if user == "" {
		user = "unknown"
	}

	rec := planOverrideRecord{
		Type:   "plan_score_override",
		Ts:     ts,
		PlanID: cfg.PlanID,
		Reason: cfg.Reason,
		User:   user,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("plan score override: marshal: %w", err)
	}

	if err := appendToLog(logPath, line); err != nil {
		return nil, fmt.Errorf("plan score override: write log: %w", err)
	}

	res.OverrideWritten = true
	_, _ = fmt.Fprintf(cfg.Writer, "Override recorded for plan_id: %s\n", cfg.PlanID)

	// Remove .plan-blocked marker if it matches this plan_id.
	currentDir, err := resolveCurrentDir(cfg)
	if err == nil && currentDir != "" {
		blockedMarker := filepath.Join(currentDir, ".plan-blocked")
		markerPlanID, readErr := readBlockedMarkerPlanID(blockedMarker)
		if readErr == nil {
			// Remove if plan_id matches or marker has no plan_id.
			if markerPlanID == cfg.PlanID || markerPlanID == "" {
				if removeErr := os.Remove(blockedMarker); removeErr == nil {
					res.BlockedMarkerRemoved = true
					_, _ = fmt.Fprintln(cfg.Writer, ".plan-blocked marker removed — dispatch gate lifted.")
				}
			} else {
				_, _ = fmt.Fprintf(cfg.Writer,
					"Note: .plan-blocked marker exists for plan_id '%s' (not '%s'); not removed.\n",
					markerPlanID, cfg.PlanID)
			}
		}
	}

	return res, nil
}

// ---- subcommand: correlate --------------------------------------------------

// dimensionNames lists the six plan-quality dimensions in the fixed order used
// by the correlation report (matching plan-score.sh).
var dimensionNames = []string{
	"acceptance_criteria_specificity",
	"assumption_surfacing",
	"decomposition_granularity",
	"dependency_clarity",
	"domain_boundaries_respected",
	"risk_rollback_honesty",
}

// joinedRow is one joined (plan_scored + plan_outcome) record.
type joinedRow struct {
	planID       string
	aggregate    float64
	dims         [6]float64
	scopeCreep   float64 // -1 means absent
	firstTryPass int     // 1=true, 0=false, -1=absent
}

func runCorrelate(cfg Config, logPath string) (*Result, error) {
	res := &Result{Subcommand: "correlate"}

	if !fileHasContent(logPath) {
		_, _ = fmt.Fprintf(cfg.ErrWriter, "No records found in %s\n", logPath)
		return nil, fmt.Errorf("plan score correlate: no records")
	}

	minN := cfg.MinN
	if minN <= 0 {
		minN = defaultMinN
	}

	// Default since: 90 days ago.
	since := cfg.Since
	if since == "" {
		since = cfg.now().UTC().Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	}

	// Read all scored records (keyed by plan_id → latest).
	scoredByID, err := readScoredRecordsMap(logPath, cfg.Project, since)
	if err != nil {
		return nil, err
	}

	// Read all outcome records.
	outcomes, err := readOutcomeRecords(logPath, cfg.Project)
	if err != nil {
		return nil, err
	}

	// Join.
	var rows []joinedRow
	for i := range outcomes {
		o := &outcomes[i]
		s, ok := scoredByID[o.PlanID]
		if !ok {
			continue
		}

		row := joinedRow{
			planID:       o.PlanID,
			aggregate:    s.AggregateScore,
			scopeCreep:   -1,
			firstTryPass: -1,
		}

		// Per-dimension scores.
		for di, dname := range dimensionNames {
			if v, exists := s.PerDimensionMed[dname]; exists {
				row.dims[di] = v
			}
		}

		if o.ScopeCreepRatio != nil {
			row.scopeCreep = *o.ScopeCreepRatio
		}
		if o.FirstTryPass != nil {
			if *o.FirstTryPass {
				row.firstTryPass = 1
			} else {
				row.firstTryPass = 0
			}
		}

		rows = append(rows, row)
	}

	res.CorrelateN = len(rows)

	if len(rows) < minN {
		_, _ = fmt.Fprintf(cfg.ErrWriter,
			"plan score correlate: only %d joined records found (minimum is %d).\n",
			len(rows), minN)
		_, _ = fmt.Fprintln(cfg.ErrWriter, "  Collect more plan outcomes via 'yakos work close' before running correlate.")
		_, _ = fmt.Fprintf(cfg.ErrWriter, "  To lower the minimum: --min-n <n>\n")
		return nil, fmt.Errorf("plan score correlate: insufficient data (need %d, have %d)", minN, len(rows))
	}

	renderCorrelate(cfg.Writer, rows, since, len(rows))
	return res, nil
}

// renderCorrelate writes the correlation report.
func renderCorrelate(w io.Writer, rows []joinedRow, since string, n int) {
	_, _ = fmt.Fprintf(w, "Plan-quality vs outcomes (n=%d joined plans, since %s):\n\n", n, since)

	// Per-dimension Pearson r against first_try_pass.
	_, _ = fmt.Fprintln(w, "  Per-dimension Pearson r against first_try_pass:")
	maxR := math.Inf(-1)
	maxDim := -1
	rs := make([]float64, 6)
	for di := range dimensionNames {
		r := pearsonR(rows, di)
		rs[di] = r
		if r > maxR {
			maxR = r
			maxDim = di
		}
	}
	for di, dname := range dimensionNames {
		tag := ""
		if di == maxDim {
			tag = "    *** highest predictor"
		}
		_, _ = fmt.Fprintf(w, "    %-44s r=%+.2f%s\n", dname, rs[di], tag)
	}
	_, _ = fmt.Fprintln(w)

	// Scope-creep by aggregate-score quartile.
	var validRows []joinedRow
	for _, row := range rows {
		if row.scopeCreep >= 0 {
			validRows = append(validRows, row)
		}
	}
	if len(validRows) >= 4 {
		_, _ = fmt.Fprintln(w, "  Scope-creep by aggregate-score quartile:")
		renderQuartiles(w, validRows)
		_, _ = fmt.Fprintln(w)
	}

	// Threshold → first-try-pass rate.
	threshold := 0.75
	var belowN, belowPass, aboveN, abovePass int
	for _, row := range rows {
		if row.firstTryPass == -1 {
			continue
		}
		if row.aggregate < threshold {
			belowN++
			if row.firstTryPass == 1 {
				belowPass++
			}
		} else {
			aboveN++
			if row.firstTryPass == 1 {
				abovePass++
			}
		}
	}
	belowRate := "n/a"
	aboveRate := "n/a"
	if belowN > 0 {
		belowRate = fmt.Sprintf("%d%%", belowPass*100/belowN)
	}
	if aboveN > 0 {
		aboveRate = fmt.Sprintf("%d%%", abovePass*100/aboveN)
	}
	_, _ = fmt.Fprintf(w, "  Below threshold (<%.2f) → first-try-pass rate:  %s\n", threshold, belowRate)
	_, _ = fmt.Fprintf(w, "  At or above threshold     → first-try-pass rate:  %s\n", aboveRate)
}

// pearsonR computes the Pearson correlation coefficient between dim[di] and
// firstTryPass across rows where firstTryPass != -1.
func pearsonR(rows []joinedRow, di int) float64 {
	var n int
	var sx, sy, sxx, syy, sxy float64
	for _, row := range rows {
		if row.firstTryPass == -1 {
			continue
		}
		x := row.dims[di]
		y := float64(row.firstTryPass)
		n++
		sx += x
		sy += y
		sxx += x * x
		syy += y * y
		sxy += x * y
	}
	if n < 2 {
		return 0
	}
	num := float64(n)*sxy - sx*sy
	den := math.Sqrt((float64(n)*sxx - sx*sx) * (float64(n)*syy - sy*sy))
	if den == 0 {
		return 0
	}
	return num / den
}

// renderQuartiles prints the scope-creep by aggregate-score quartile table.
func renderQuartiles(w io.Writer, rows []joinedRow) {
	// Sort rows by aggregate to find quartile boundaries.
	sorted := make([]float64, len(rows))
	for i, r := range rows {
		sorted[i] = r.aggregate
	}
	sort.Float64s(sorted)
	m := len(sorted)
	q1Max := sorted[m/4]
	q2Max := sorted[m/2]
	q3Max := sorted[3*m/4]

	var qSum [5]float64
	var qCnt [5]int
	for _, row := range rows {
		q := 4
		switch {
		case row.aggregate <= q1Max:
			q = 1
		case row.aggregate <= q2Max:
			q = 2
		case row.aggregate <= q3Max:
			q = 3
		}
		qSum[q] += row.scopeCreep
		qCnt[q]++
	}

	qNames := [5]string{"", "Q1 (lowest scores) ", "Q2                 ", "Q3                 ", "Q4 (highest scores)"}
	for q := 1; q <= 4; q++ {
		mean := 0.0
		if qCnt[q] > 0 {
			mean = qSum[q] / float64(qCnt[q])
		}
		_, _ = fmt.Fprintf(w, "    %s: mean creep %.1f×  (n=%d)\n", qNames[q], mean, qCnt[q])
	}
}

// ---- log readers ------------------------------------------------------------

// readScoredRecords reads all plan_scored records from logPath, optionally
// filtering by planID and/or project. Results are in file order (oldest first).
func readScoredRecords(logPath, planID, project string) ([]planScoredRecord, error) {
	f, err := os.Open(logPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("plan score: open log: %w", err)
	}
	defer func() { _ = f.Close() }()

	var records []planScoredRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Fast pre-screen before full parse.
		if !strings.Contains(line, `"plan_scored"`) {
			continue
		}
		if planID != "" && !strings.Contains(line, planID) {
			continue
		}
		var rec planScoredRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Type != "plan_scored" {
			continue
		}
		if planID != "" && rec.PlanID != planID {
			continue
		}
		if project != "" && rec.Project != project {
			continue
		}
		records = append(records, rec)
	}
	return records, scanner.Err()
}

// readScoredRecordsMap reads all plan_scored records and returns the latest
// per plan_id (for correlate join). Filters by project and since (ISO string).
func readScoredRecordsMap(logPath, project, since string) (map[string]planScoredRecord, error) {
	records, err := readScoredRecords(logPath, "", project)
	if err != nil {
		return nil, err
	}
	result := make(map[string]planScoredRecord)
	for _, rec := range records {
		if since != "" && rec.Ts < since {
			continue
		}
		// Overwrite → last record wins (newest for each plan_id).
		result[rec.PlanID] = rec
	}
	return result, nil
}

// readOutcomeRecords reads all plan_outcome records from logPath.
func readOutcomeRecords(logPath, project string) ([]planOutcomeRecord, error) {
	f, err := os.Open(logPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("plan score: open log: %w", err)
	}
	defer func() { _ = f.Close() }()

	var records []planOutcomeRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !strings.Contains(line, `"plan_outcome"`) {
			continue
		}
		var rec planOutcomeRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Type != "plan_outcome" {
			continue
		}
		if project != "" && rec.Project != project {
			continue
		}
		records = append(records, rec)
	}
	return records, scanner.Err()
}

// appendToLog appends a JSON line to the log file using O_APPEND for
// cross-process safety (matches the bash pattern).
func appendToLog(logPath string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()
	_, err = fmt.Fprintf(f, "%s\n", line)
	return err
}

// readBlockedMarkerPlanID reads the plan_id from a .plan-blocked JSON marker.
// Returns ("", nil) when the file does not exist. Returns ("", err) on I/O error.
// Returns (planID, nil) when the file is valid JSON with a plan_id field.
// Returns ("", nil) for non-JSON files (matches bash: empty plan_id → remove).
func readBlockedMarkerPlanID(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return "", err // file does not exist or unreadable
	}
	var m planBlockedMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return "", nil // non-JSON: treat as no plan_id
	}
	return m.PlanID, nil
}

// resolveCurrentDir returns the work/current directory for the session.
// If cfg.CurrentDir is set, it is used directly. Otherwise the resolution
// follows the same priority chain as yakos paths.sh.
func resolveCurrentDir(cfg Config) (string, error) {
	if cfg.CurrentDir != "" {
		return cfg.CurrentDir, nil
	}
	// YAKOS_INPLACE_WORK + CLAUDE_PROJECT_DIR.
	if os.Getenv("YAKOS_INPLACE_WORK") == "1" {
		if pd := os.Getenv("CLAUDE_PROJECT_DIR"); pd != "" {
			return filepath.Join(pd, "work", "current"), nil
		}
	}
	// $HOME/agent-control/$YAKOS_PROJECT_NAME/work/current.
	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	proj := os.Getenv("YAKOS_PROJECT_NAME")
	if proj != "" && home != "" {
		return filepath.Join(home, "agent-control", proj, "work", "current"), nil
	}
	return "", fmt.Errorf("cannot resolve current dir")
}

// fileHasContent returns true when path exists and has at least one byte.
func fileHasContent(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}
