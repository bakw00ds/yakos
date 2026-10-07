package supervisorstream

import (
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
)

// runState is the per-session supervisor launch state (K-117), a key=value
// file shared with supervisor-stream.sh and the detached wrapper:
//
//	start      epoch of the in-flight run (absent/empty: none)
//	launches   ROUTINE launches this session (counted against the cap)
//	hlaunches  high-risk launches (bypass the cap, bounded by 3x the cap)
//	last       epoch of the last launch (a deferred launch: its planned start)
//	pending    triggers recorded since the last claim (previews in the pending file)
//	high       high-risk events not yet claimed by a run
//	caplog     1 once the cap was reported;  ceillog: the same for the ceiling
//	backoff    epoch until which launches pause (account session limit)
//	budgetlog  1 once the budget ceiling (dollars or tokens) was reported (separate from ceillog)
type runState struct {
	hasStart  bool
	start     int64
	launches  int
	hlaunches int
	last      int64
	pending   int
	high      int
	caplog    int
	ceillog   int
	backoff   int64
	budgetlog int // 1 once the budget ceiling, in dollars or in tokens, was reported (its own flag)
}

// sessionKey sanitizes a session id for use in a file name: every byte
// outside [A-Za-z0-9_-] becomes "_", capped at 64 bytes; empty becomes
// "nosession". Bytewise on purpose: bash does `tr -c ... '_' | head -c 64`.
func sessionKey(id string) string {
	b := []byte(id)
	if len(b) > 64 {
		// bash caps AFTER the replacement, which is length preserving bytewise.
		b = b[:64]
	}
	for i, c := range b {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			b[i] = '_'
		}
	}
	if len(b) == 0 {
		return "nosession"
	}
	return string(b)
}

func atoiLoose(v string) (int64, bool) {
	if v == "" || strings.Trim(v, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func loadRunState(path string) runState {
	var st runState
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return st
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		n, num := atoiLoose(v)
		if !num {
			n = 0
		}
		switch k {
		case "start":
			if num {
				st.hasStart, st.start = true, n
			}
		case "launches":
			st.launches = int(n)
		case "hlaunches":
			st.hlaunches = int(n)
		case "last":
			st.last = n
		case "pending":
			st.pending = int(n)
		case "high":
			st.high = int(n)
		case "caplog":
			st.caplog = int(n)
		case "ceillog":
			st.ceillog = int(n)
		case "backoff":
			st.backoff = n
		case "budgetlog":
			st.budgetlog = int(n)
		}
	}
	return st
}

func (st runState) save(path string) error {
	start := ""
	if st.hasStart {
		start = strconv.FormatInt(st.start, 10)
	}
	body := "start=" + start + "\nlaunches=" + strconv.Itoa(st.launches) +
		"\nhlaunches=" + strconv.Itoa(st.hlaunches) +
		"\nlast=" + strconv.FormatInt(st.last, 10) + "\npending=" + strconv.Itoa(st.pending) +
		"\nhigh=" + strconv.Itoa(st.high) + "\ncaplog=" + strconv.Itoa(st.caplog) +
		"\nceillog=" + strconv.Itoa(st.ceillog) + "\nbackoff=" + strconv.FormatInt(st.backoff, 10) +
		"\nbudgetlog=" + strconv.Itoa(st.budgetlog) + "\n"
	tmp := path + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return err
	}
	if err := renameReplace(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// appendPending adds one redacted event preview to the per-session pending
// file (owner-only), keeping the last 100 lines. Bash twin: _ss_pend_append.
func appendPending(path string, event map[string]any) {
	if event == nil {
		return
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	appendPendingLine(path, string(data))
}

// appendPendingLine appends one already-encoded preview line (a journal record
// carries its own). An empty line is not recorded.
func appendPendingLine(path, line string) { _ = appendPendingLines(path, []string{line}) }

// appendPendingLines appends already-encoded preview lines in one write and keeps
// the file to its last 100 lines once it passes 150. Empty lines are not recorded.
// The pending file lives in a directory the sandboxed model may be able to write
// to, so it is opened without following links and only a regular file is ever
// written, read or chmod-ed (through the descriptor); a link or other non-regular
// entry is refused with os.ErrInvalid.
func appendPendingLines(path string, lines []string) error {
	var b strings.Builder
	for _, l := range lines {
		if l != "" {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	if b.Len() == 0 {
		return nil
	}
	f, err := openRegularNoFollow(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(b.String()); err != nil {
		return err
	}
	_ = f.Chmod(0o600)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	raw, rerr := io.ReadAll(f)
	if rerr != nil {
		return nil
	}
	all := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(all) > 150 {
		tail := strings.Join(all[len(all)-100:], "\n") + "\n"
		tmp := path + ".tmp." + strconv.Itoa(os.Getpid())
		// a planted link at tmp is removed, never followed
		_ = os.Remove(tmp)
		if tf, terr := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|openNoFollow, 0o600); terr == nil { //nolint:gosec
			_, werr := tf.WriteString(tail)
			cerr := tf.Close()
			if werr != nil || cerr != nil || renameReplace(tmp, path) != nil {
				_ = os.Remove(tmp)
			}
		}
	}
	return nil
}
