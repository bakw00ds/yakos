package cost

import (
	"fmt"
	"io"
)

// The ledger layout of `yakos cost` (K-136). Tokens are the primary unit, so a
// log that holds ledger events (rows the Go dispatcher accounted, see IsLedger)
// prints the real token counts after the seven columns every log has always had,
// and dollars only where there are some:
//
//	key count ok fail dur(s) est_in_tok est_out_tok | in out cache tokens | usd | api_equiv
//
//   - in, out and cache are the counts as logged (cache is cache_read plus
//     cache_creation); tokens is their sum, the only figure comparable across
//     harnesses. est_in_tok and est_out_tok stay where they were: they are size
//     estimates, never counted in the new columns.
//   - usd appears only when some row has api spend (Event.SpendUSD). Subscription
//     and local runs never contribute, so a codex or agy row shows "-".
//   - api_equiv appears only when some row has an api-equivalent figure, and says
//     in a footnote that it is not spend.
//
// A log with no ledger event never reaches this file: PrintTable and PrintJSON
// print the original layout for it, byte for byte.

// printLedgerTable writes the column header, rows, TOTAL row and footnotes of the
// ledger layout. The title lines are written by PrintTable.
func printLedgerTable(w io.Writer, rpt Report) error {
	var showUSD, showEquiv bool
	for _, r := range rpt.Rows {
		if r.SpendUSD > 0 {
			showUSD = true
		}
		if r.APIEquivUSD > 0 {
			showEquiv = true
		}
	}

	// The legacy prefix keeps its widths; every new column is right-aligned in 10.
	const (
		legacyHead = "  %-24s %6s %5s %5s %8s %10s %10s"
		legacyRow  = "  %-24s %6d %5d %5d %8s %10d %10d"
		legacyTot  = "  %-24s %6d %5s %5s %8s %10d %10d"
		tokHead    = " %10s %10s %10s %10s"
		tokRow     = " %10d %10d %10d %10d"
		moneyCell  = " %10s"
	)
	headFmt, rowFmt, totFmt := legacyHead+tokHead, legacyRow+tokRow, legacyTot+tokRow
	headArgs := []any{"key", "count", "ok", "fail", "dur(s)", "est_in_tok", "est_out_tok", "in", "out", "cache", "tokens"}
	sepArgs := []any{"------------------------", "------", "-----", "-----", "--------", "----------", "----------",
		"----------", "----------", "----------", "----------"}
	if showUSD {
		headFmt, rowFmt, totFmt = headFmt+moneyCell, rowFmt+moneyCell, totFmt+moneyCell
		headArgs, sepArgs = append(headArgs, "usd"), append(sepArgs, "----------")
	}
	if showEquiv {
		headFmt, rowFmt, totFmt = headFmt+moneyCell, rowFmt+moneyCell, totFmt+moneyCell
		headArgs, sepArgs = append(headArgs, "api_equiv"), append(sepArgs, "----------")
	}

	if _, err := fmt.Fprintf(w, headFmt+"\n", headArgs...); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, headFmt+"\n", sepArgs...); err != nil {
		return err
	}

	var totalDur, totalSpend, totalEquiv float64
	var totalEstIn, totalEstOut int64
	var totalTok TokenTotals
	for _, row := range rpt.Rows {
		key := row.Key
		if len(key) > 24 {
			key = key[:24]
		}
		args := []any{key, row.Count, row.OK, row.Fail, formatDurationS(row.TotalDurationS),
			row.TotalInTokens, row.TotalOutTokens,
			row.Tokens.Input, row.Tokens.Output, row.Tokens.CacheRead + row.Tokens.CacheCreation, row.Tokens.Total()}
		if showUSD {
			args = append(args, usdCell(row.SpendUSD))
		}
		if showEquiv {
			args = append(args, usdCell(row.APIEquivUSD))
		}
		if _, err := fmt.Fprintf(w, rowFmt+"\n", args...); err != nil {
			return err
		}
		totalDur += row.TotalDurationS
		totalEstIn += row.TotalInTokens
		totalEstOut += row.TotalOutTokens
		totalTok = totalTok.Add(row.Tokens)
		totalSpend += row.SpendUSD
		totalEquiv += row.APIEquivUSD
	}

	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	args := []any{"TOTAL", rpt.Events, "", "", formatDurationS(totalDur), totalEstIn, totalEstOut,
		totalTok.Input, totalTok.Output, totalTok.CacheRead + totalTok.CacheCreation, totalTok.Total()}
	if showUSD {
		args = append(args, usdCell(roundUSD(totalSpend)))
	}
	if showEquiv {
		args = append(args, usdCell(roundUSD(totalEquiv)))
	}
	if _, err := fmt.Fprintf(w, totFmt+"\n", args...); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "  tokens = in + out + cache, as logged; est_in_tok and est_out_tok are size estimates and are not counted."); err != nil {
		return err
	}
	if showEquiv {
		if _, err := fmt.Fprintln(w, "  api_equiv = what subscription runs would have cost at API rates; it is not spend and is not part of usd."); err != nil {
			return err
		}
	}
	return nil
}

// usdCell formats a dollar figure for the usd and api_equiv columns: "-" when
// there is none, so a subscription or local row never shows a dollar sign's
// worth of noise, and "<0.0001" for a figure too small for four decimals.
func usdCell(v float64) string {
	switch {
	case v <= 0:
		return "-"
	case v < 0.0001:
		return "<0.0001"
	}
	return fmt.Sprintf("%.4f", v)
}

// JSONTokens is the tokens object of a ledger row in --json: the counts as
// logged, and their sum.
type JSONTokens struct {
	Input         int64 `json:"input"`
	Output        int64 `json:"output"`
	CacheRead     int64 `json:"cache_read"`
	CacheCreation int64 `json:"cache_creation"`
	Total         int64 `json:"total"`
}

// LedgerJSONRow is a --json row of a report with ledger events: every key of
// JSONRow, unchanged and in the same order, then the real counts (tokens) and the
// dollars. usd is api spend only and api_equivalent_usd is informational, never
// spend; each is omitted when it is 0.
type LedgerJSONRow struct {
	JSONRow
	Tokens           JSONTokens `json:"tokens"`
	USD              float64    `json:"usd,omitempty"`
	APIEquivalentUSD float64    `json:"api_equivalent_usd,omitempty"`
}

// LedgerJSONOutput is the --json document of a report with ledger events.
type LedgerJSONOutput struct {
	Events int64           `json:"events"`
	Rows   []LedgerJSONRow `json:"rows"`
}

func ledgerJSON(rpt Report) LedgerJSONOutput {
	rows := make([]LedgerJSONRow, len(rpt.Rows))
	for i, r := range rpt.Rows {
		rows[i] = LedgerJSONRow{
			JSONRow: legacyJSONRow(r),
			Tokens: JSONTokens{
				Input:         r.Tokens.Input,
				Output:        r.Tokens.Output,
				CacheRead:     r.Tokens.CacheRead,
				CacheCreation: r.Tokens.CacheCreation,
				Total:         r.Tokens.Total(),
			},
			USD:              r.SpendUSD,
			APIEquivalentUSD: r.APIEquivUSD,
		}
	}
	return LedgerJSONOutput{Events: rpt.Events, Rows: rows}
}
