package cost

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

// PrintTable writes the human-readable cost table to w, matching the bash
// output format exactly.
//
// bash template (lines 148-157 of cost.sh):
//
//	printf '  %-24s %6s %5s %5s %8s %10s %10s\n' "key" "count" "ok" "fail" ...
//	awk '{ printf "  %-24s %6s %5s %5s %8s %10s %10s\n", $1, $2, $3, $4, $5, $6, $7 }'
func PrintTable(w io.Writer, rpt Report, since, by string) error {
	header := "yakos cost"
	if rpt.Events == 1 {
		header += fmt.Sprintf(" — %d dispatch event(s)", rpt.Events)
	} else {
		header += fmt.Sprintf(" — %d dispatch event(s)", rpt.Events)
	}
	if since != "" {
		header += fmt.Sprintf(" since %s", since)
	}
	header += fmt.Sprintf(" by %s", by)
	if _, err := fmt.Fprintln(w, header); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	// Column headers.
	if _, err := fmt.Fprintf(w, "  %-24s %6s %5s %5s %8s %10s %10s\n",
		"key", "count", "ok", "fail", "dur(s)", "est_in_tok", "est_out_tok"); err != nil {
		return err
	}
	// Separator row.
	if _, err := fmt.Fprintf(w, "  %-24s %6s %5s %5s %8s %10s %10s\n",
		"------------------------", "------", "-----", "-----", "--------", "----------", "----------"); err != nil {
		return err
	}

	// Data rows.
	for _, row := range rpt.Rows {
		key := row.Key
		if len(key) > 24 {
			key = key[:24]
		}
		durStr := formatDurationS(row.TotalDurationS)
		if _, err := fmt.Fprintf(w, "  %-24s %6d %5d %5d %8s %10d %10d\n",
			key, row.Count, row.OK, row.Fail, durStr,
			row.TotalInTokens, row.TotalOutTokens); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	// Totals row.
	var totalDur float64
	var totalIn, totalOut int64
	for _, r := range rpt.Rows {
		totalDur += r.TotalDurationS
		totalIn += r.TotalInTokens
		totalOut += r.TotalOutTokens
	}
	durStr := formatDurationS(totalDur)
	if _, err := fmt.Fprintf(w, "  %-24s %6d %5s %5s %8s %10d %10d\n",
		"TOTAL", rpt.Events, "", "", durStr, totalIn, totalOut); err != nil {
		return err
	}

	return nil
}

// formatDurationS formats a float64 duration in seconds the way jq does:
// jq emits integers without a decimal point, and floats with up to 10 sig
// digits.  Since duration_s is always written as an integer in the log
// (the bash dispatch.sh writes it as an integer), we replicate jq's integer
// output for whole numbers.
func formatDurationS(d float64) string {
	if d == math.Trunc(d) && !math.IsInf(d, 0) && !math.IsNaN(d) {
		return fmt.Sprintf("%d", int64(d))
	}
	// Floating-point: use jq's default representation (up to 6 significant
	// digits, no trailing zeros except one after the dot).
	return strings.TrimRight(fmt.Sprintf("%g", d), "0")
}

// JSONOutput is the shape of the --json output, matching bash exactly.
type JSONOutput struct {
	Events int64     `json:"events"`
	Rows   []JSONRow `json:"rows"`
}

// JSONRow is one row in the --json output.
type JSONRow struct {
	Key            string  `json:"key"`
	Count          int64   `json:"count"`
	OK             int64   `json:"ok"`
	Fail           int64   `json:"fail"`
	TotalDurationS float64 `json:"total_duration_s"`
	TotalInTokens  int64   `json:"total_in_tokens"`
	TotalOutTokens int64   `json:"total_out_tokens"`
}

// PrintJSON writes machine-readable JSON output to w.
// Format matches bash: {"events": N, "rows": [...]}
func PrintJSON(w io.Writer, rpt Report) error {
	rows := make([]JSONRow, len(rpt.Rows))
	for i, r := range rpt.Rows {
		rows[i] = JSONRow(r)
	}
	out := JSONOutput{Events: rpt.Events, Rows: rows}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

// PrintNoFiles writes the "no log files found" message (bash: files array empty).
func PrintNoFiles(w io.Writer, emitJSON bool, logDir string) error {
	if emitJSON {
		_, err := fmt.Fprintln(w, `{"events":0,"rows":[]}`)
		return err
	}
	_, err := fmt.Fprintf(w, "no dispatch-log.ndjson found at %s (no dispatches yet)\n", logDir)
	return err
}

// PrintNoEvents writes the "no events after filter" message (bash: n_events == 0).
func PrintNoEvents(w io.Writer, emitJSON bool, since string) error {
	if emitJSON {
		_, err := fmt.Fprintln(w, `{"events":0,"rows":[]}`)
		return err
	}
	_, err := fmt.Fprintf(w, "no dispatch_finished events match the filter (since=%s)\n", since)
	return err
}
