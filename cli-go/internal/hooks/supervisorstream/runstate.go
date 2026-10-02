package supervisorstream

import (
	"encoding/json"
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
		"\nceillog=" + strconv.Itoa(st.ceillog) + "\nbackoff=" + strconv.FormatInt(st.backoff, 10) + "\n"
	tmp := path + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
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
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec
	if err != nil {
		return
	}
	_, _ = f.Write(append(data, '\n'))
	_ = f.Close()
	_ = os.Chmod(path, 0o600)
	if all, rerr := os.ReadFile(path); rerr == nil { //nolint:gosec
		lines := strings.Split(strings.TrimRight(string(all), "\n"), "\n")
		if len(lines) > 150 {
			tail := strings.Join(lines[len(lines)-100:], "\n") + "\n"
			tmp := path + ".tmp." + strconv.Itoa(os.Getpid())
			if os.WriteFile(tmp, []byte(tail), 0o600) == nil {
				_ = os.Rename(tmp, path)
			}
		}
	}
}
