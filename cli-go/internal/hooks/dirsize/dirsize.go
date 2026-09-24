// Package dirsize is a faithful Go port of lib/hooks/lib/compat.sh's
// ct_dir_size_bytes — used by session-end-check and team-lifecycle to
// compute scratchpad_size_bytes for their session-summary records.
//
// bash's ct_dir_size_bytes:
//
//	ct_dir_size_bytes() {
//	    local dir="$1"
//	    [ -d "$dir" ] || { printf '0'; return; }
//	    local n
//	    n="$(du -sb "$dir" 2>/dev/null | awk 'NR==1 {print $1}')"
//	    if [ -n "$n" ] && [ "$n" -eq "$n" ] 2>/dev/null; then
//	        printf '%s' "$n"
//	        return
//	    fi
//	    n="$(du -sk "$dir" 2>/dev/null | awk 'NR==1 {print $1}')"
//	    if [ -n "$n" ] && [ "$n" -eq "$n" ] 2>/dev/null; then
//	        printf '%d' "$((n * 1024))"
//	    else
//	        printf '0'
//	    fi
//	}
//
// GNU du has -b (apparent size in bytes); BSD/macOS du does not (and
// rejects the flag), so the kilobyte fallback is load-bearing on macOS —
// meaning the result is NOT a byte-exact sum of file sizes there, it's
// 1024-multiple-rounded, filesystem-block-accounting du output. A
// from-scratch Go directory walk (summing os.FileInfo.Size()) would
// compute a DIFFERENT number than bash on macOS and could even differ
// from GNU du -b on Linux (directory-entry overhead, sparse files,
// hardlinks). Shelling out to the SAME du binary bash calls is the only
// way to guarantee byte-identical numbers across both CI runner OSes
// (this repo's parity matrix runs macOS + Ubuntu) without hand-modeling
// each platform's du semantics — see S-6 A-2a round 2 review findings
// 1/2.
package dirsize

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Bytes returns the directory size in bytes, matching ct_dir_size_bytes
// exactly: du -sb first (GNU apparent-size), falling back to du -sk * 1024
// (BSD/macOS, and any GNU du invocation that failed -b for another
// reason), falling back to 0 if both du invocations fail or dir doesn't
// exist. Never returns an error — this is a best-effort telemetry field.
func Bytes(dir string) int64 {
	if dir == "" {
		return 0
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return 0
	}

	if n, ok := duFirstField(dir, "-sb"); ok {
		return n
	}
	if n, ok := duFirstField(dir, "-sk"); ok {
		return n * 1024
	}
	return 0
}

// duFirstField runs `du <flag> <dir>` and parses the first whitespace-
// separated field of its first output line as an integer — the Go
// equivalent of `du <flag> "$dir" 2>/dev/null | awk 'NR==1 {print $1}'`
// plus bash's `[ "$n" -eq "$n" ] 2>/dev/null` integer-sanity check.
func duFirstField(dir, flag string) (int64, bool) {
	cmd := exec.Command("du", flag, dir) //nolint:gosec
	out, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	if !scanner.Scan() {
		return 0, false
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) == 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
