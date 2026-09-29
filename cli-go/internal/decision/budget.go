package decision

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

// BudgetState is the persisted spend: one UTC day's dollars and per-session
// call counts. Sessions from earlier days are dropped at rollover.
type BudgetState struct {
	Day      string         `json:"day"`
	USD      float64        `json:"usd"`
	Sessions map[string]int `json:"sessions"`
}

// Budget enforces a per-session call cap and a per-day dollar cap.
// Calls are counted when they are ATTEMPTED (Reserve), so a failing endpoint
// cannot be hammered past the cap; dollars are added from reported usage.
type Budget struct {
	Path               string
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

// Load reads the state, rolling it over when the stored day is not today.
// A missing file is an empty budget; a corrupt file is an error.
func (b *Budget) Load() (BudgetState, error) {
	st := BudgetState{Day: b.today(), Sessions: map[string]int{}}
	data, err := os.ReadFile(b.Path) //nolint:gosec // state-dir file
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, err
	}
	var got BudgetState
	if err := json.Unmarshal(data, &got); err != nil {
		return st, err
	}
	if got.Day != st.Day {
		return st, nil
	}
	if got.Sessions == nil {
		got.Sessions = map[string]int{}
	}
	return got, nil
}

// Check returns a ClassBudget error when either cap is already reached.
func (b *Budget) Check(session string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.check(session)
}

func (b *Budget) check(session string) error {
	st, err := b.Load()
	if err != nil {
		return newErr(ClassBudget, "budget state unreadable: %v", err)
	}
	if session == "" {
		session = "default"
	}
	// Epsilon: 0.09 + 0.01 must count as the $0.10 it is, not 0.0999…
	if st.USD+1e-9 >= b.MaxUSDPerDay {
		return newErr(ClassBudget, "daily cap $%.2f reached", b.MaxUSDPerDay)
	}
	if st.Sessions[session] >= b.MaxCallsPerSession {
		return newErr(ClassBudget, "session cap of %d calls reached", b.MaxCallsPerSession)
	}
	return nil
}

// Reserve checks the caps and, if clear, counts one call for the session.
func (b *Budget) Reserve(session string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.check(session); err != nil {
		return err
	}
	if session == "" {
		session = "default"
	}
	st, _ := b.Load()
	st.Sessions[session]++
	return b.save(st)
}

// AddUSD adds the cost of a completed call to today's spend.
func (b *Budget) AddUSD(usd float64) {
	if usd <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, _ := b.Load()
	st.USD += usd
	_ = b.save(st)
}

func (b *Budget) save(st BudgetState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return newErr(ClassBudget, "budget state not writable: %v", err)
	}
	if err := writeFileAtomic(b.Path, data); err != nil {
		// Fail closed: if the spend cannot be recorded, do not spend.
		return newErr(ClassBudget, "budget state not writable: %v", err)
	}
	return nil
}
