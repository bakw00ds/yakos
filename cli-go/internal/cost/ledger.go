package cost

import "math"

// K-136 ledger helpers shared by every reader that reports tokens and dollars
// (yakos cost, the Performance dashboard, the Cost tab, the metrics collector).
// They sit beside Event.SpendUSD and Event.Tokens (dispatchlog.go), which stay
// the only way to total dollars and tokens.

// IsLedger reports whether ev is a ledger event: a dispatch_finished event the
// Go dispatcher accounted, which carries a billing field. Bash-written rows and
// Go rows written before K-136 do not, and readers keep treating them as legacy
// rows (their dollar figure counts as spend, see CountsAsSpend).
func IsLedger(ev Event) bool { return ev.Billing != "" }

// APIEquivalentUSD is the event's api_equivalent_usd, what a subscription run
// would have cost at API rates as the harness reported it. It is informational:
// it is never spend and must never be added to a spend total. Like SpendUSD it
// returns 0 for a figure that is not a finite non-negative number, so one
// corrupt line cannot move a total.
func APIEquivalentUSD(ev Event) float64 {
	c := ev.APIEquivalentUSD
	if math.IsNaN(c) || c < 0 || c > 1e12 { // NaN, negative or absurd
		return 0
	}
	return c
}

// roundUSD rounds a dollar total to a micro-dollar so float summation noise
// (0.1 + 0.2) never reaches a report. A harness-reported cost is never that
// small, so nothing real is lost.
func roundUSD(v float64) float64 { return math.Round(v*1e6) / 1e6 }
