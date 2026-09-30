package decision

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// PromotionMinSample is the minimum number of answered shadow decisions
// before a surface may be considered for promotion (K-111 design section 5).
const PromotionMinSample = 200

// Local verdicts a hook records beside the shadow answer.
const (
	LocalPass     = "pass"
	LocalEscalate = "escalate"
)

// ValidLocalVerdict reports whether v is an accepted --local value.
func ValidLocalVerdict(v string) bool { return v == LocalPass || v == LocalEscalate }

// ShadowEscalates maps a typed answer to the escalate/pass verdict the
// supervisor pre-filter would have acted on in prefilter mode: risk_class
// "dangerous" at or above its min_confidence, or bypasses_hard_control at or
// above its min_probability (design section 2.1). ok is false when the surface
// has no such mapping or the answer lacks the fields the mapping needs.
func ShadowEscalates(set *QuestionSet, answers map[string]AnswerSummary) (escalate, ok bool) {
	if set == nil || set.Surface != "supervisor-prefilter" {
		return false, false
	}
	rc, haveRC := answers["risk_class"]
	bp, haveBP := answers["bypasses_hard_control"]
	if !haveRC || !haveBP || rc.Choice == "" || bp.Noul == nil {
		return false, false
	}
	if rc.Choice == "dangerous" && rc.Confidence != nil && *rc.Confidence >= set.Thresholds["risk_class"].MinConfidence {
		return true, true
	}
	if *bp.Noul >= set.Thresholds["bypasses_hard_control"].MinProbability {
		return true, true
	}
	return false, true
}

// CompareReport is the shadow-vs-local agreement for one surface.
type CompareReport struct {
	Surface    string `json:"surface"`
	SchemaHash string `json:"schema_hash"`
	Model      string `json:"model"`

	Records   int `json:"records"`          // shadow records for this hash with a local verdict
	OtherHash int `json:"other_hash"`       // shadow records skipped: a different question-set hash
	NoLocal   int `json:"no_local_verdict"` // shadow records skipped: no local verdict recorded
	Answered  int `json:"answered"`         // provider returned a usable answer

	Errors map[string]int `json:"errors,omitempty"` // fail-open records by error class

	BothEscalate int `json:"both_escalate"`
	BothPass     int `json:"both_pass"`
	ShadowOnly   int `json:"shadow_only_escalate"` // provider would escalate, local passed
	LocalOnly    int `json:"local_only_escalate"`  // local escalated, provider would pass

	Agreement float64 `json:"agreement"` // (BothEscalate+BothPass)/Answered, 0 when Answered == 0
	// LocalEscalationRecall is BothEscalate/(BothEscalate+LocalOnly): how much
	// of what the local heuristic escalates the provider also flags.
	LocalEscalationRecall float64 `json:"local_escalation_recall"`

	RiskClass    map[string]int `json:"risk_class,omitempty"`    // provider risk_class distribution
	LocalTrigger map[string]int `json:"local_trigger,omitempty"` // local escalation trigger kinds

	MeanLatencyMS float64 `json:"mean_latency_ms"`
	P95LatencyMS  int64   `json:"p95_latency_ms"`
	CostUSD       float64 `json:"cost_usd"`

	MinSample     int  `json:"min_sample"`
	SampleReached bool `json:"sample_reached"`
}

// Compare reads a decision log and summarises shadow-vs-local agreement for
// set's surface and exact hash. Records from other hashes are counted but not
// mixed in: a verdict earned under one question wording says nothing about
// another. Unreadable or malformed lines are skipped. A missing log yields an
// empty report.
func Compare(logPath string, set *QuestionSet) (*CompareReport, error) {
	rep := &CompareReport{
		Surface: set.Surface, SchemaHash: set.Hash, Model: set.Model,
		Errors: map[string]int{}, RiskClass: map[string]int{}, LocalTrigger: map[string]int{},
		MinSample: PromotionMinSample,
	}
	f, err := os.Open(logPath) //nolint:gosec // state-dir file
	if err != nil {
		if os.IsNotExist(err) {
			return rep, nil
		}
		return nil, err
	}
	defer f.Close() //nolint:errcheck
	if err := compareFrom(f, set, rep); err != nil {
		return nil, err
	}
	return rep, nil
}

func compareFrom(r io.Reader, set *QuestionSet, rep *CompareReport) error {
	var latencies []int64
	var latencySum int64
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var rec Record
			if json.Unmarshal(line, &rec) == nil && rec.Type == "decision" && rec.Surface == set.Surface && rec.Mode == ModeShadow {
				switch {
				case rec.SchemaHash != set.Hash:
					rep.OtherHash++
				case !ValidLocalVerdict(rec.LocalVerdict):
					rep.NoLocal++
				default:
					rep.Records++
					latencies = append(latencies, rec.LatencyMS)
					latencySum += rec.LatencyMS
					rep.CostUSD += rec.CostUSD
					if rec.LocalVerdict == LocalEscalate {
						kind := rec.LocalTrigger
						if kind == "" {
							kind = "unknown"
						}
						rep.LocalTrigger[kind]++
					}
					if rec.Status != "ok" {
						cls := rec.ErrorClass
						if cls == "" {
							cls = rec.Status
						}
						rep.Errors[cls]++
					} else if esc, ok := ShadowEscalates(set, rec.Answers); !ok {
						rep.Errors["unusable_answer"]++
					} else {
						rep.Answered++
						if rc, has := rec.Answers["risk_class"]; has {
							rep.RiskClass[rc.Choice]++
						}
						local := rec.LocalVerdict == LocalEscalate
						switch {
						case esc && local:
							rep.BothEscalate++
						case !esc && !local:
							rep.BothPass++
						case esc:
							rep.ShadowOnly++
						default:
							rep.LocalOnly++
						}
					}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}
	if rep.Answered > 0 {
		rep.Agreement = float64(rep.BothEscalate+rep.BothPass) / float64(rep.Answered)
	}
	if d := rep.BothEscalate + rep.LocalOnly; d > 0 {
		rep.LocalEscalationRecall = float64(rep.BothEscalate) / float64(d)
	}
	if n := len(latencies); n > 0 {
		rep.MeanLatencyMS = float64(latencySum) / float64(n)
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		idx := (n*95 + 99) / 100 // ceil(0.95 n), 1-based
		if idx < 1 {
			idx = 1
		}
		rep.P95LatencyMS = latencies[idx-1]
	}
	rep.SampleReached = rep.Answered >= rep.MinSample
	return nil
}

// WriteText renders the report for a terminal.
func (r *CompareReport) WriteText(w io.Writer) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format+"\n", a...) }
	p("surface        %s  (model %s)", r.Surface, r.Model)
	p("question set   %s", r.SchemaHash)
	p("shadow calls   %d with a local verdict (%d from other question-set hashes, %d without a local verdict; not counted)", r.Records, r.OtherHash, r.NoLocal)
	p("answered       %d   fail-open %d%s", r.Answered, r.Records-r.Answered, formatCounts(r.Errors))
	if r.Answered == 0 {
		p("agreement      n/a (no answered shadow calls yet)")
	} else {
		p("agreement      %.1f%%  (%d of %d)", r.Agreement*100, r.BothEscalate+r.BothPass, r.Answered)
		p("  both escalate        %d", r.BothEscalate)
		p("  both pass            %d", r.BothPass)
		p("  shadow-only escalate %d   (candidates for review: the provider flagged what the local heuristic passed)", r.ShadowOnly)
		p("  local-only escalate  %d   (the provider would have passed what the local heuristic escalated)", r.LocalOnly)
		if r.BothEscalate+r.LocalOnly > 0 {
			p("local escalations the provider also flags: %.1f%%", r.LocalEscalationRecall*100)
		}
		if len(r.RiskClass) > 0 {
			p("risk_class     %s", strings.TrimSpace(formatCounts(r.RiskClass)))
		}
	}
	if len(r.LocalTrigger) > 0 {
		p("local triggers %s", strings.TrimSpace(formatCounts(r.LocalTrigger)))
	}
	p("latency        mean %.0f ms, p95 %d ms", r.MeanLatencyMS, r.P95LatencyMS)
	p("cost           $%.6f", r.CostUSD)
	state := "not reached"
	if r.SampleReached {
		state = "reached"
	}
	p("sample gate    %d of %d answered decisions (%s)", r.Answered, r.MinSample, state)
	p("promotion needs the sample gate, Jev-only precision >= 0.70 on labelled disagreements, and no labelled false benign on a local hit at >= 0.9 confidence (docs/decision-providers.md).")
}

func formatCounts(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	keys := sortedKeys(m)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return "  [" + strings.Join(parts, " ") + "]"
}
