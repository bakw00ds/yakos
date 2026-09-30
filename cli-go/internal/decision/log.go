package decision

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// AnswerSummary is the logged form of an answer: the typed verdict and its
// confidence, not the state that produced it.
type AnswerSummary struct {
	Type       string   `json:"type"`
	Choice     string   `json:"choice,omitempty"`
	Score      *float64 `json:"score,omitempty"`
	Noul       *float64 `json:"noul,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
}

// Record is one decision-log line. There is deliberately NO field that can
// hold request state, question text, or a credential: the raw state is never
// logged (ADR-0009 §6, rule:secret-handling). StateBytes and Redactions are
// counts only.
type Record struct {
	Type         string                   `json:"type"`
	TS           string                   `json:"ts"`
	ID           string                   `json:"id"`
	Surface      string                   `json:"surface"`
	SchemaID     string                   `json:"schema_id"`
	SchemaHash   string                   `json:"schema_hash"`
	Provider     string                   `json:"provider"`
	Model        string                   `json:"model"`
	Mode         string                   `json:"mode"` // shadow | prefilter
	Session      string                   `json:"session,omitempty"`
	StateBytes   int                      `json:"state_bytes"`
	Redactions   int                      `json:"redactions"`
	LatencyMS    int64                    `json:"latency_ms"`
	InputTokens  int                      `json:"input_tokens"`
	OutputTokens int                      `json:"output_tokens"`
	CostUSD      float64                  `json:"cost_usd"`
	Status       string                   `json:"status"` // ok | <error class>
	ErrorClass   string                   `json:"provider_error_class,omitempty"`
	Answers      map[string]AnswerSummary `json:"answers,omitempty"`
	// LocalVerdict / LocalTrigger record what the caller's own deterministic
	// heuristic decided for the same event ("pass" or "escalate", and the
	// trigger kind), so shadow and local verdicts can be compared later
	// (`yakos decide compare`). Both are omitted when the caller gave none.
	LocalVerdict string `json:"local_verdict,omitempty"`
	LocalTrigger string `json:"local_trigger,omitempty"`
	// Tag labels calls that are not real traffic (e.g. "smoke"); compare
	// leaves tagged records out of its promotion evidence by default.
	Tag string `json:"tag,omitempty"`
}

// SummarizeAnswers drops probabilities/legends; keeps the verdicts.
func SummarizeAnswers(in map[string]Answer) map[string]AnswerSummary {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]AnswerSummary, len(in))
	for id, a := range in {
		out[id] = AnswerSummary{Type: a.Type, Choice: a.Choice, Score: a.Score, Noul: a.Noul, Confidence: a.Confidence}
	}
	return out
}

// Logger appends Records to decision-log.ndjson (0600, in the state dir, NOT
// the dispatch-log). Each record is one O_APPEND write of a single line;
// POSIX makes appends of this size atomic enough for concurrent hooks, and it
// stays portable to Windows (no flock).
type Logger struct {
	Path string
	mu   sync.Mutex
}

// NewLogger returns a Logger for the default state dir when path is empty.
func NewLogger(path string) *Logger {
	if path == "" {
		path = filepath.Join(statepath.Dir(), LogFileName)
	}
	return &Logger{Path: path}
}

// Append writes one record. Errors are returned for tests and ignored by
// Engine: logging can never change a decision outcome.
func (l *Logger) Append(r Record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := statepath.SecureDir(filepath.Dir(l.Path)); err != nil {
		return err
	}
	f, err := os.OpenFile(l.Path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // state-dir file
	if err != nil {
		return fmt.Errorf("decision log: %w", err)
	}
	defer f.Close()
	if err := statepath.SecureFile(f); err != nil {
		return err
	}
	_, err = f.Write(line)
	return err
}

// sortedKeys is used by deterministic serializers in this package.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
