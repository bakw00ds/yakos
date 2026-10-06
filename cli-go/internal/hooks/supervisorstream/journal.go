package supervisorstream

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Journal records (K-128).
//
// Dropping a tick under load is fail-open: a lost increment shifts every later
// score-every threshold and a lost high-risk trigger is never recorded. A hook
// whose lock wait expired therefore leaves a small owner-only record that the
// next lock holder folds in (docs/supervisor-mode.md, "Lock protocol"):
//
//	<counter>.add.<pid>.<id> "1": one increment owed to the counter. The holder
//	                         that folds it counts it and, if that moves the
//	                         counter past a score-every multiple, covers the
//	                         crossing (the hook that owed it is long gone).
//	<state>.add.<pid>.<id>   "high=<0|1>", then the event preview: one trigger
//	                         owed to the session's run state (pending +1, high +1
//	                         when flagged, preview appended to the pending file).
//	                         Folded by the next gate holder of the session.
//
// A record is created exclusively (O_EXCL: a planted symlink is never followed),
// read with bounded reads, folded only when complete and well formed (a
// half-written one waits for the next fold), at most maxFold counter or
// maxFoldGate trigger records per fold (a fold reads each record under the lock;
// the rest wait for the next holder), and removed only after the file it was
// folded into has been written. At most maxWaiting records of a kind wait at once.
// Trigger records are folded by the next gate holder of the session and by the
// session's wrapper. A launch the crossing owed is not promised: the next run (the
// next crossing) covers the recorded trigger. Bash twin: _ss_journal_tick,
// _ss_journal_gate, _ss_fold_adds, _ss_fold_gate.
const (
	maxFold     = 256   // counter records folded at once
	maxFoldGate = 32    // trigger records folded at once (each is read, appended and counted under the lock)
	maxWaiting  = 512   // records of one kind that may wait; past it a hook drops its tick, as before K-128
	tickMax     = 8     // bytes read of a counter record
	gateEvMax   = 16384 // bytes read of a gate record's event line
)

// journalSeq makes names unique within a process: the clock alone is too coarse
// (microseconds on some platforms) for two records written back to back.
var journalSeq atomic.Uint64

func journalName(base string) string {
	return fmt.Sprintf("%s.add.%d.%d.%d", base, os.Getpid(), time.Now().UnixNano(), journalSeq.Add(1))
}

// writeJournal creates a new record next to base, exclusively, and reports
// whether it did. A name that already exists (a recycled pid and a clock tick, in
// theory) gets a new name, so a record is never lost to a collision; the storage
// is bounded by maxWaiting.
func writeJournal(base string, data []byte) bool {
	if len(journalFiles(base)) >= maxWaiting {
		return false
	}
	for i := 0; i < 5; i++ {
		err := writeExclusive(journalName(base), data)
		if err == nil {
			return true
		}
		if !errors.Is(err, fs.ErrExist) {
			return false
		}
	}
	return false
}

func writeExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// journalTick leaves a "+1" record for the next holder of the counter lock.
func journalTick(counterFile string) bool {
	return writeJournal(counterFile, []byte("1\n"))
}

// journalGate leaves a trigger record for the next gate holder of the session.
func journalGate(statePath string, high bool, event map[string]any) bool {
	body := "high=0\n"
	if high {
		body = "high=1\n"
	}
	if event != nil {
		if b, err := json.Marshal(event); err == nil {
			body += string(b)
		}
	}
	return writeJournal(statePath, []byte(body+"\n"))
}

// journalFiles lists the regular files named <base>.add.* next to base, sorted.
// A symlink or a directory is skipped, never followed.
func journalFiles(base string) []string {
	prefix := filepath.Base(base) + ".add."
	dir := filepath.Dir(base)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) && e.Type().IsRegular() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// readHead returns at most max bytes of the file.
func readHead(path string, max int) (string, bool) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return "", false
	}
	defer f.Close() //nolint:errcheck
	buf := make([]byte, max)
	n, _ := io.ReadFull(f, buf)
	return string(buf[:n]), true
}

// foldTicks returns the complete "+1" records for the counter, to be removed by
// the caller once the new counter is written.
func foldTicks(counterFile string) []string {
	var good []string
	for _, f := range journalFiles(counterFile) {
		head, ok := readHead(f, tickMax)
		if !ok {
			continue
		}
		if line, _, _ := strings.Cut(head, "\n"); line == "1" {
			good = append(good, f)
			if len(good) >= maxFold {
				break
			}
		}
	}
	return good
}

// foldGate folds the session's trigger records into st (pending, high) and the
// pending file, at most maxFoldGate at a time. The caller removes the returned
// files after st is saved.
func foldGate(statePath, pendingPath string, st *runState) []string {
	var good, lines []string
	for _, f := range journalFiles(statePath) {
		head, ok := readHead(f, 8+gateEvMax+1)
		if !ok {
			continue
		}
		flag, rest, _ := strings.Cut(head, "\n")
		if flag != "high=0" && flag != "high=1" {
			continue
		}
		ev, _, _ := strings.Cut(rest, "\n")
		st.pending++
		if flag == "high=1" {
			st.high++
		}
		lines = append(lines, ev)
		good = append(good, f)
		if len(good) >= maxFoldGate {
			break
		}
	}
	appendPendingLines(pendingPath, lines)
	return good
}

func removeFiles(files []string) {
	for _, f := range files {
		_ = os.Remove(f)
	}
}
