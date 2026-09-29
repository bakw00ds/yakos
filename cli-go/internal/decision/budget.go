package decision

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/statepath"
)

func isWindows() bool { return runtime.GOOS == "windows" }

// PricePerMInputTokensUSD is the documented early-access price: $0.042 per
// million input tokens, output tokens free (https://docs.typesafe.ai/models).
// It may change; the budget cap bounds the blast radius if it does.
const PricePerMInputTokensUSD = 0.042

// CostUSD computes the estimated cost of a call from reported usage.
func CostUSD(u Usage) float64 {
	return float64(u.InputTokens) * PricePerMInputTokensUSD / 1e6
}

// BudgetState is the spend for one UTC day, summed from the ledger.
type BudgetState struct {
	Day      string         `json:"day"`
	USD      float64        `json:"usd"`
	Sessions map[string]int `json:"sessions"`
}

// ledgerLine is one append-only record. Reservations carry C=1, spend records
// carry U. Sums are taken on read, so concurrent hook processes can never
// overwrite each other's counts (a read-modify-write file lost 91% of them).
type ledgerLine struct {
	S string  `json:"s,omitempty"`
	C int     `json:"c,omitempty"`
	U float64 `json:"u,omitempty"`
}

// Budget enforces a per-session call cap and a per-UTC-day dollar cap.
//
// State is one append-only NDJSON ledger per day (<Path minus .json>-<day>.ndjson),
// written with a single O_APPEND write per record, which the OS makes atomic
// for small lines across processes. A call is counted when it is ATTEMPTED
// (Reserve appends first, then sums), so the cap is never exceeded under concurrency
// (a call or two near it may be refused early) and a failing endpoint cannot be hammered past it. Dollars are appended from
// reported usage after the call, so in-flight calls can overshoot the dollar
// cap by at most (concurrent calls x one call's cost, well under $0.002).
//
// Any read or write failure refuses the call: if spend cannot be recorded, do
// not spend. `yakos doctor --probe-decision --live` makes one call that is NOT
// counted against this budget.
type Budget struct {
	Path               string // base path; the day's ledger is derived from it
	MaxCallsPerSession int
	MaxUSDPerDay       float64
	Now                func() time.Time
	mu                 sync.Mutex
}

// NewBudget returns a Budget at path (state-dir default when empty) with the
// documented defaults for any zero cap.
func NewBudget(path string, maxCalls int, maxUSD float64) *Budget {
	if path == "" {
		path = filepath.Join(statepath.Dir(), BudgetFileName)
	}
	if maxCalls <= 0 {
		maxCalls = DefaultMaxCallsPerSession
	}
	if maxUSD <= 0 {
		maxUSD = DefaultMaxUSDPerDay
	}
	return &Budget{Path: path, MaxCallsPerSession: maxCalls, MaxUSDPerDay: maxUSD}
}

func (b *Budget) today() string {
	n := time.Now
	if b.Now != nil {
		n = b.Now
	}
	return n().UTC().Format("2006-01-02")
}

func (b *Budget) base() string { return strings.TrimSuffix(b.Path, ".json") }

// LedgerPath returns today's ledger file.
func (b *Budget) LedgerPath() string { return b.base() + "-" + b.today() + ".ndjson" }

func sessionKey(s string) string {
	if s == "" {
		return "default"
	}
	if len(s) > 128 {
		return s[:128]
	}
	return s
}

// Load sums today's ledger. A missing ledger is an empty budget; an
// unparsable line is an error (callers fail closed).
func (b *Budget) Load() (BudgetState, error) {
	st := BudgetState{Day: b.today(), Sessions: map[string]int{}}
	data, err := os.ReadFile(b.LedgerPath()) //nolint:gosec // state-dir file
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var l ledgerLine
		if err := json.Unmarshal(line, &l); err != nil {
			return st, fmt.Errorf("corrupt budget ledger line")
		}
		if l.C < 0 || l.U < 0 {
			return st, fmt.Errorf("corrupt budget ledger line")
		}
		st.USD += l.U
		if l.C > 0 {
			st.Sessions[l.S] += l.C
		}
	}
	return st, sc.Err()
}

func (b *Budget) append(l ledgerLine) error {
	line, err := json.Marshal(l)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if err := statepath.SecureDir(filepath.Dir(b.Path)); err != nil {
		return err
	}
	f, err := os.OpenFile(b.LedgerPath(), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // state-dir file
	if err != nil {
		return err
	}
	defer f.Close()
	if err := statepath.SecureFile(f); err != nil {
		return err
	}
	_, err = f.Write(line)
	return err
}

// Check returns a ClassBudget error when either cap is already reached. It
// does not reserve anything.
func (b *Budget) Check(session string) error {
	st, err := b.Load()
	if err != nil {
		return newErr(ClassBudget, "budget ledger unreadable: %v", err)
	}
	return b.over(st, sessionKey(session), 1)
}

func (b *Budget) over(st BudgetState, session string, extra int) error {
	// Epsilon: 0.09 + 0.01 must count as the $0.10 it is, not 0.0999…
	if st.USD+1e-9 >= b.MaxUSDPerDay {
		return newErr(ClassBudget, "daily cap $%.2f reached", b.MaxUSDPerDay)
	}
	if st.Sessions[session]+extra > b.MaxCallsPerSession {
		return newErr(ClassBudget, "session cap of %d calls reached", b.MaxCallsPerSession)
	}
	return nil
}

// Reserve counts one call for the session (append first, then sum) and
// refuses when the caps are exceeded. A refused call stays counted.
func (b *Budget) Reserve(session string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	session = sessionKey(session)
	b.cleanup()
	if err := b.append(ledgerLine{S: session, C: 1}); err != nil {
		// Fail closed: if the reservation cannot be recorded, do not spend.
		return newErr(ClassBudget, "budget ledger not writable: %v", err)
	}
	st, err := b.Load()
	if err != nil {
		return newErr(ClassBudget, "budget ledger unreadable: %v", err)
	}
	return b.over(st, session, 0)
}

// AddUSD records the cost of a completed call.
func (b *Budget) AddUSD(usd float64) {
	if usd <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	_ = b.append(ledgerLine{U: usd})
}

// cleanup removes ledgers older than two days (best effort).
func (b *Budget) cleanup() {
	matches, _ := filepath.Glob(b.base() + "-*.ndjson")
	cutoff := b.today()
	if t, err := time.Parse("2006-01-02", cutoff); err == nil {
		cutoff = t.AddDate(0, 0, -2).Format("2006-01-02")
	}
	for _, m := range matches {
		day := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), filepath.Base(b.base())+"-"), ".ndjson")
		if len(day) == 10 && day < cutoff {
			_ = os.Remove(m)
		}
	}
}
