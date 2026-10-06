package dispatch

// account_hostile_test.go: what a harness reports about itself can neither make
// the ledger lie nor cost it an event (K-136, security review of #330). These go
// all the way through Account.Finish to the log, because a value that cannot be
// encoded is only noticed when the line is written.

import (
	"math"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/cost"
)

// finishOne finishes one dispatch for claude with res, through the real writer,
// and returns the dispatch_finished event it wrote. It fails when the pair was
// not written: a lost finished event is exactly what these tests guard against.
func finishOne(t *testing.T, res Result) map[string]interface{} {
	t.Helper()
	logDir := isolatedLogDir(t)
	NewAccount(Request{AgentName: "a", Runtime: "claude", Project: "/p", Surface: SurfaceREST}).Finish(res)
	events := readDispatchLog(t, logDir)
	if len(events) != 2 || events[0]["type"] != "dispatch_started" || events[1]["type"] != "dispatch_finished" {
		t.Fatalf("the dispatch must leave a started/finished pair, got %v", events)
	}
	return events[1]
}

// A negative figure in the usage object is recorded as zero, so one hostile or
// corrupt row can never subtract from a window's token or dollar sum.
func TestAccount_NegativeUsageIsRecordedAsZero(t *testing.T) {
	ev := finishOne(t, Result{Runtime: "claude", Usage: &cost.Usage{
		InputTokens: -1000, OutputTokens: -7, CacheRead: -5, CacheCreation: -9, DurationMs: -3, TotalCostUSD: -2.5,
	}})
	u := usageOf(t, ev)
	for _, k := range []string{"input_tokens", "output_tokens", "cache_read", "cache_creation", "duration_ms", "total_cost_usd"} {
		if u[k] != float64(0) {
			t.Errorf("usage.%s = %v, want 0: a negative figure is clamped", k, u[k])
		}
	}
	if _, has := ev["cost_source"]; has {
		t.Errorf("a negative cost is no reported cost: cost_source = %v", ev["cost_source"])
	}
	if _, has := ev["api_equivalent_usd"]; has {
		t.Errorf("a negative cost is no API-equivalent: %v", ev["api_equivalent_usd"])
	}
}

// A cost that is not a finite number would make the whole event unencodable, and
// the finished event (and the tokens it carries) would be lost. It is recorded as
// zero and the event is written, for every billing mode.
func TestAccount_NonFiniteCostStillWritesTheFinishedEvent(t *testing.T) {
	for _, billing := range []string{"subscription", "api"} {
		for name, bad := range map[string]float64{"+Inf": math.Inf(1), "-Inf": math.Inf(-1), "NaN": math.NaN(), "huge": 1e300} {
			t.Run(billing+"/"+name, func(t *testing.T) {
				if billing == "api" {
					apiKeyEnv(t, "claude")
				}
				ev := finishOne(t, Result{Runtime: "claude", Usage: &cost.Usage{InputTokens: 11, OutputTokens: 4, TotalCostUSD: bad}})
				assertField(t, ev, "billing", billing)
				u := usageOf(t, ev)
				if u["total_cost_usd"] != float64(0) || u["input_tokens"] != float64(11) || u["output_tokens"] != float64(4) {
					t.Errorf("the tokens are kept and the unusable cost is zero: %v", u)
				}
				if _, has := ev["api_equivalent_usd"]; has {
					t.Errorf("an unusable cost is not an API-equivalent: %v", ev["api_equivalent_usd"])
				}
				if _, has := ev["cost_source"]; has {
					t.Errorf("an unusable cost has no source: %v", ev["cost_source"])
				}
			})
		}
	}
}

// The clamp does not touch a legitimate figure: a subscription row keeps its
// API-equivalent and every token count, and an api row keeps its spend.
func TestAccount_LegitimateUsageIsUntouchedByTheClamp(t *testing.T) {
	ev := finishOne(t, Result{Runtime: "claude", Usage: &cost.Usage{InputTokens: 120, OutputTokens: 45, CacheRead: 9000, CacheCreation: 3000, DurationMs: 800, TotalCostUSD: 0.5}})
	u := usageOf(t, ev)
	if u["input_tokens"] != float64(120) || u["output_tokens"] != float64(45) || u["cache_read"] != float64(9000) ||
		u["cache_creation"] != float64(3000) || u["duration_ms"] != float64(800) {
		t.Errorf("usage = %v", u)
	}
	if ev["api_equivalent_usd"] != 0.5 || ev["cost_source"] != "harness" {
		t.Errorf("a subscription row keeps the harness figure as its API-equivalent: %v", ev)
	}

	apiKeyEnv(t, "claude")
	ev = finishOne(t, Result{Runtime: "claude", Usage: &cost.Usage{InputTokens: 3, TotalCostUSD: 0.5}})
	if u := usageOf(t, ev); u["total_cost_usd"] != 0.5 || u["input_tokens"] != float64(3) {
		t.Errorf("an api row keeps its spend: %v", u)
	}
}

// logTextStripped is what logText must remove, by name. The characters are built
// from their code points so that no invisible character sits in this source file.
var logTextStripped = map[string]rune{
	"C0 escape":               0x1b,
	"carriage return":         0x0d,
	"tab":                     0x09,
	"DEL":                     0x7f,
	"C1 lower edge":           0x80,
	"C1 NEL":                  0x85,
	"C1 CSI":                  0x9b,
	"C1 upper edge":           0x9f,
	"soft hyphen":             0xad,
	"arabic letter mark":      0x61c,
	"zero-width space":        0x200b,
	"left-to-right mark":      0x200e,
	"right-to-left mark":      0x200f,
	"line separator":          0x2028,
	"paragraph separator":     0x2029,
	"left-to-right embedding": 0x202a,
	"right-to-left embedding": 0x202b,
	"pop directional":         0x202c,
	"left-to-right override":  0x202d,
	"right-to-left override":  0x202e,
	"word joiner":             0x2060,
	"left-to-right isolate":   0x2066,
	"right-to-left isolate":   0x2067,
	"first strong isolate":    0x2068,
	"pop directional isolate": 0x2069,
	"byte order mark":         0xfeff,
}

// logText removes what could rewrite a line on a terminal or hide text from a
// reader: C0 and C1 controls, bidirectional overrides and isolates, zero-width
// and other format characters, and line and paragraph separators. Ordinary text,
// accents and non-Latin scripts are kept.
func TestLogText_StripsC1AndBidiControls(t *testing.T) {
	for name, cp := range logTextStripped {
		in := "a" + string(cp) + "b"
		if got := logText(in, 256); got != "ab" {
			t.Errorf("%s (U+%04X): logText(%q) = %q, want %q", name, cp, in, got, "ab")
		}
	}
	if got := logText("a"+string([]byte{0xff})+"b", 256); got != "ab" {
		t.Errorf("invalid UTF-8 must be dropped: %q", got)
	}
	if got := logText("a"+string(rune(0x202e))+string(rune(0x2066))+string(rune(0x9b))+string(rune(0x85))+"b", 256); got != "ab" {
		t.Errorf("several in a row: %q", got)
	}
	for _, keep := range []string{"matched rule coding-heavy", "h" + string(rune(0xe9)) + "llo", "日本語のルール", "Omega " + string(rune(0x3a9)) + " 100%", "a, b; c: d"} {
		if got := logText(keep, 256); got != keep {
			t.Errorf("logText(%q) = %q, ordinary text must be kept", keep, got)
		}
	}
}

// The same cleaning applies to what the router's rule and reason become in the event.
func TestBuildFinished_RouteReasonLosesC1AndBidiControls(t *testing.T) {
	rlo, csi, lri, pdi, nel := string(rune(0x202e)), string(rune(0x9b)), string(rune(0x2066)), string(rune(0x2069)), string(rune(0x85))
	ev := buildFinished(Request{
		AgentName: "a", Runtime: "claude", Project: "/p",
		RouteRule:   "rule" + rlo + "-reversed",
		RouteReason: "because" + csi + "2J " + lri + "hidden" + pdi + " " + nel + "next line",
	}, Result{}, fixedTime)
	if ev.RouteRule != "rule-reversed" {
		t.Errorf("route_rule = %q", ev.RouteRule)
	}
	if ev.RouteReason != "because2J hidden next line" {
		t.Errorf("route_reason = %q", ev.RouteReason)
	}
	if strings.ContainsAny(ev.RouteReason+ev.RouteRule, rlo+csi+lri+pdi+nel) {
		t.Errorf("a control survived: %q %q", ev.RouteRule, ev.RouteReason)
	}
}
