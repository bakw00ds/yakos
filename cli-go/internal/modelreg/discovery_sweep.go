package modelreg

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// A probe runs agy in a private directory inside the secured state directory and
// removes it when the probe ends. A probe that is killed (kill -9, a crash, a power
// cut) never gets to, so the next probe sweeps what earlier ones left behind.
//
// The sweep is conservative in three ways, because the state directory is shared by
// every yakOS process of the user and a probe may be running in another of them:
//
//   - It leaves a directory whose owner is alive. The name carries the owning
//     process's pid (.discover-<pid>-<n>), and a live pid keeps the directory. A pid
//     can be reused, so a directory older than staleWorkDirAge is a leftover whatever
//     its pid says (a probe is bounded by its timeout, minutes at most).
//   - It touches only real directories this user owns. The entries come from one
//     ReadDir and are examined as the listing reports them, not followed: a symlink
//     with a leftover's name is left alone and nothing behind it is touched, and a
//     regular file is not a work directory. Removal does not follow links either.
//   - It removes at most maxSweptPerProbe directories per probe, so a state directory
//     with a great many leftovers is cleaned over several probes, not in one.
//
// A name with no pid (written by an earlier build) is removed only when it is old.

const (
	// workDirPrefix starts the name of every probe's working directory.
	workDirPrefix = ".discover-"
	// staleWorkDirAge is how old a probe's working directory must be to be a leftover
	// even though its owner's pid is alive (pids are reused).
	staleWorkDirAge = time.Hour
	// maxSweptPerProbe bounds the work one probe spends on leftovers.
	maxSweptPerProbe = 32
)

// workDirRe matches a probe's working directory: .discover-<pid>-<n>, or the older
// .discover-<n> with no pid.
var workDirRe = regexp.MustCompile(`^\.discover-(?:([0-9]{1,10})-)?[0-9]+$`)

// workDirPattern is the os.MkdirTemp pattern for this process's probes.
func workDirPattern() string { return workDirPrefix + strconv.Itoa(os.Getpid()) + "-*" }

// workDirOwnedByMe reports whether fi belongs to the current user. A variable so a
// test can model a directory another user owns. The sweep goes by the wall clock and
// the directories' real modification times, not by the Discoverer's clock: that one
// decides what a snapshot's age is.
var workDirOwnedByMe = statepath.OwnedByCurrentUser

// sweepStaleWorkDirs removes the leftovers of earlier probes from stateDir. It is
// best effort and silent: a directory that cannot be removed is not the probe's
// concern.
func sweepStaleWorkDirs(stateDir string) {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return
	}
	now := time.Now()
	removed := 0
	for _, e := range entries {
		if removed >= maxSweptPerProbe {
			return
		}
		m := workDirRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		// DirEntry.Info reports a symlink as itself, never its target. Only a plain
		// directory passes: its type bits are exactly ModeDir, so a symlink, a Windows
		// mount point (ModeIrregular), a file or a device with the name is left alone.
		fi, err := e.Info()
		if err != nil || fi.Mode().Type() != os.ModeDir || !workDirOwnedByMe(fi) {
			continue
		}
		old := now.Sub(fi.ModTime()) > staleWorkDirAge
		if m[1] != "" {
			if pid, err := strconv.Atoi(m[1]); err == nil && processAlive(pid) && !old {
				continue // another probe's live directory
			}
		} else if !old {
			continue // no owner recorded: only its age says it is a leftover
		}
		if os.RemoveAll(filepath.Join(stateDir, e.Name())) == nil {
			removed++
		}
	}
}
