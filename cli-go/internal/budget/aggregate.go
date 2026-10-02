package budget

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/statepath"
)

const (
	aggregateFileName = "budget-spend.json"
	resetsFileName    = "budget-resets.json"
	lockFileName      = "budget.lock"
	logFileName       = "dispatch-log.ndjson"
	aggregateVersion  = 2
	headBytes         = 256
)

type agentSpend struct {
	Lifetime float64            `json:"lifetime"`
	Monthly  map[string]float64 `json:"monthly"`
	// Projects is spend by month then by the project recorded on the
	// dispatch_finished event (K-119 F2 per-project attribution).
	Projects map[string]map[string]float64 `json:"projects,omitempty"`
}

// aggregate is a derived cache of the dispatch-log: per-agent spend by local
// calendar month and lifetime, plus the byte offset already consumed from the
// current log. It is always rebuildable from the log; nothing in it is
// authoritative (resets live in a separate file).
type aggregate struct {
	Version int `json:"version"`
	// Zone fingerprints the local time zone the monthly buckets were cut in;
	// a change forces a rebuild so a TZ switch never leaves stale months.
	Zone    string                 `json:"zone"`
	Offset  int64                  `json:"offset"`
	HeadLen int                    `json:"head_len"`
	Head    string                 `json:"head"`
	Agents  map[string]*agentSpend `json:"agents"`
}

// zoneFingerprint identifies the local zone by its UTC offsets in mid-winter
// and mid-summer of a fixed reference year (which also captures DST rules).
func zoneFingerprint() string {
	_, w := time.Date(2020, 1, 15, 12, 0, 0, 0, time.Local).Zone()
	_, s := time.Date(2020, 7, 15, 12, 0, 0, 0, time.Local).Zone()
	return fmt.Sprintf("%d,%d", w, s)
}

func newAggregate() *aggregate {
	return &aggregate{Version: aggregateVersion, Zone: zoneFingerprint(), Agents: map[string]*agentSpend{}}
}

func (a *aggregate) add(agent, month, project string, usd float64) {
	s := a.Agents[agent]
	if s == nil {
		s = &agentSpend{Monthly: map[string]float64{}}
		a.Agents[agent] = s
	}
	s.Lifetime += usd
	s.Monthly[month] += usd
	if project != "" {
		if s.Projects == nil {
			s.Projects = map[string]map[string]float64{}
		}
		if s.Projects[month] == nil {
			s.Projects[month] = map[string]float64{}
		}
		s.Projects[month][project] += usd
	}
}

// projectSpend returns spend by project for agent in the window, summed over
// every month for lifetime.
func (a *aggregate) projectSpend(agent string, w Window, key string) map[string]float64 {
	out := map[string]float64{}
	s := a.Agents[agent]
	if s == nil {
		return out
	}
	for m, byProj := range s.Projects {
		if w == Monthly && m != key {
			continue
		}
		for p, v := range byProj {
			out[p] += v
		}
	}
	return out
}

// spend returns the raw (pre-reset) spend of agent in the window key.
func (a *aggregate) spend(agent string, w Window, key string) float64 {
	s := a.Agents[agent]
	if s == nil {
		return 0
	}
	if w == Lifetime {
		return s.Lifetime
	}
	return s.Monthly[key]
}

// WindowKey is the accounting bucket for t: the local calendar month, or the
// constant "lifetime".
func WindowKey(w Window, t time.Time) string {
	if w == Lifetime {
		return "lifetime"
	}
	return t.Local().Format("2006-01")
}

// NextWindowStart is the start of the next monthly window (local time); zero
// for lifetime.
func NextWindowStart(w Window, t time.Time) time.Time {
	if w == Lifetime {
		return time.Time{}
	}
	l := t.Local()
	return time.Date(l.Year(), l.Month()+1, 1, 0, 0, 0, 0, l.Location())
}

func readAggregate(dir string) *aggregate {
	data, err := readTrusted(filepath.Join(dir, aggregateFileName))
	if err != nil {
		return nil // missing or untrusted: rebuilt from the log
	}
	var a aggregate
	if err := json.Unmarshal(data, &a); err != nil || a.Version != aggregateVersion || a.Zone != zoneFingerprint() || a.Agents == nil || a.Offset < 0 {
		return nil // corrupt: rebuilt from the log
	}
	for _, s := range a.Agents {
		if s == nil || s.Monthly == nil {
			return nil
		}
	}
	return &a
}

func writeAggregate(dir string, a *aggregate) error {
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, aggregateFileName), data)
}

func hashHead(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// logState reports the current log size and whether agg's recorded head still
// matches the file (false means the log was rotated or replaced).
func logState(logPath string, agg *aggregate) (size int64, headOK bool, err error) {
	f, err := os.Open(logPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return 0, agg != nil && agg.Offset == 0 && agg.HeadLen == 0, nil
		}
		return 0, false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, false, err
	}
	size = fi.Size()
	if agg == nil {
		return size, false, nil
	}
	if size < agg.Offset || int64(agg.HeadLen) > size {
		return size, false, nil
	}
	if agg.HeadLen == 0 {
		return size, true, nil
	}
	buf := make([]byte, agg.HeadLen)
	if _, err := io.ReadFull(f, buf); err != nil {
		return size, false, nil
	}
	return size, hashHead(buf) == agg.Head, nil
}

type finishedLine struct {
	Type    string `json:"type"`
	Ts      string `json:"ts"`
	Agent   string `json:"agent"`
	Project string `json:"project"`
	Usage   *struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
	} `json:"usage"`
}

var finishedMarker = []byte(`"dispatch_finished"`)

func monthOf(ts string) (string, bool) {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		if len(ts) >= 7 {
			return ts[:7], true
		}
		return "", false
	}
	return t.Local().Format("2006-01"), true
}

// scan folds the complete lines of r into agg and returns the bytes consumed.
// A trailing partial line (a writer mid-append) is left for the next pass.
func scan(r io.Reader, agg *aggregate) (int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	end := bytes.LastIndexByte(data, '\n') + 1
	for _, line := range bytes.Split(data[:end], []byte{'\n'}) {
		if !bytes.Contains(line, finishedMarker) {
			continue
		}
		var ev finishedLine
		if json.Unmarshal(line, &ev) != nil || ev.Type != "dispatch_finished" || ev.Agent == "" || ev.Usage == nil {
			continue
		}
		c := ev.Usage.TotalCostUSD
		if math.IsNaN(c) || math.IsInf(c, 0) || c <= 0 {
			continue
		}
		m, ok := monthOf(ev.Ts)
		if !ok {
			continue
		}
		agg.add(ev.Agent, m, ev.Project, c)
	}
	return int64(end), nil
}

// advance brings agg up to date with the log: incrementally when the recorded
// head still matches, otherwise by rebuilding from every dispatch-log*.ndjson
// (rotated archives included). It returns the new aggregate.
func advance(dir string, agg *aggregate) (*aggregate, error) {
	logPath := filepath.Join(dir, logFileName)
	size, headOK, err := logState(logPath, agg)
	if err != nil {
		return nil, err
	}
	if agg == nil || !headOK {
		agg = newAggregate()
		files, _ := cost.LogFiles(dir)
		for _, p := range files {
			if p == logPath {
				continue
			}
			f, err := os.Open(p) //nolint:gosec
			if err != nil {
				continue
			}
			_, _ = scan(f, agg)
			_ = f.Close()
		}
	}
	if size == 0 || size == agg.Offset && agg.HeadLen > 0 {
		return agg, nil
	}
	f, err := os.Open(logPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return agg, nil
		}
		return nil, err
	}
	defer f.Close()
	n, err := scan(io.NewSectionReader(f, agg.Offset, size-agg.Offset), agg)
	if err != nil {
		return nil, err
	}
	agg.Offset += n
	if agg.HeadLen == 0 {
		hl := headBytes
		if int64(hl) > agg.Offset {
			hl = int(agg.Offset)
		}
		buf := make([]byte, hl)
		if _, err := f.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		agg.HeadLen, agg.Head = hl, hashHead(buf)
	}
	return agg, nil
}

func current(dir string, agg *aggregate) (bool, error) {
	size, headOK, err := logState(filepath.Join(dir, logFileName), agg)
	if err != nil {
		return false, err
	}
	if agg == nil || !headOK {
		return false, nil
	}
	return size == agg.Offset, nil
}

// refresh returns an aggregate that reflects the whole log. The steady state
// is lock-free (read the cache, stat the log). When the log has grown or the
// cache is missing or corrupt, it takes the lock, folds in only the new
// bytes (or rebuilds), and rewrites the cache. If the lock cannot be taken
// the answer is computed in memory without caching.
func refresh(dir string) (*aggregate, error) {
	agg := readAggregate(dir)
	if ok, err := current(dir, agg); err != nil {
		return nil, err
	} else if ok {
		return agg, nil
	}
	unlock, lerr := acquire(dir, 2*time.Second)
	if lerr != nil {
		return advance(dir, agg)
	}
	defer unlock()
	return refreshLocked(dir)
}

// refreshLocked is refresh with the lock already held by the caller.
func refreshLocked(dir string) (*aggregate, error) {
	agg := readAggregate(dir) // another dispatch may have just updated it
	if ok, err := current(dir, agg); err != nil {
		return nil, err
	} else if ok {
		return agg, nil
	}
	agg, err := advance(dir, agg)
	if err != nil {
		return nil, err
	}
	if err := statepath.SecureDir(dir); err == nil {
		_ = writeAggregate(dir, agg) // a failed cache write only costs speed
	}
	return agg, nil
}

type resetRec struct {
	Window string  `json:"window"`
	Key    string  `json:"key"`
	USD    float64 `json:"usd"`
	At     string  `json:"at"`
}

// readTrusted reads a budget state file only when it passes the same trust
// check as the policy file (regular, ours, not group/world writable, not a
// symlink). A missing file is os.ErrNotExist.
func readTrusted(path string) ([]byte, error) {
	if _, err := trustCheck(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path) //nolint:gosec
}

// readResets returns the recorded resets. An untrusted file is ignored (a
// reset cannot be rebuilt from the log) and reported in err.
func readResets(dir string) (map[string]resetRec, error) {
	out := map[string]resetRec{}
	data, err := readTrusted(filepath.Join(dir, resetsFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	if json.Unmarshal(data, &out) != nil {
		return map[string]resetRec{}, nil
	}
	return out, nil
}
