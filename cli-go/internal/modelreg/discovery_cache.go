package modelreg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// The on-disk cache of discovery results, <state dir>/model-discovery.json:
//
//	{"schema":1,"harnesses":{"agy":{"source":"agy models","probed_at":"...",
//	  "models":[{"id":"gemini-3.8-flash-high","name":"Gemini 3.8 Flash (High)"}]}}}
//
// It exists so a short-lived `yakos models list` can show availability without
// running agy, and so a restarted daemon starts with the last answer. It is a
// cache, never a source of authority: losing it costs one refresh, and a file
// that cannot be trusted is treated as absent.

const (
	cacheSchema = 1
	// maxCacheBytes bounds the file that is read. A listing of maxListedModels
	// entries is well under 100 KiB.
	maxCacheBytes = 1 << 20
	// maxClockSkew is how far into the future a snapshot's timestamp may be
	// before the snapshot is distrusted. A timestamp far ahead (a hand edit, a
	// broken clock) would otherwise keep the snapshot "fresh" for good and
	// suppress every refresh.
	maxClockSkew = 5 * time.Minute
)

type cacheFile struct {
	Schema    int                     `json:"schema"`
	Harnesses map[string]cacheHarness `json:"harnesses"`
}

type cacheHarness struct {
	Source   string            `json:"source"`
	ProbedAt time.Time         `json:"probed_at"`
	Models   []DiscoveredModel `json:"models"`
}

// cachePath is the cache file inside the state directory.
func cachePath(stateDir string) string { return filepath.Join(stateDir, DiscoveryFileName) }

// readCacheFile returns the usable snapshots in <stateDir>/model-discovery.json.
// A missing file, an untrusted one (a symlink, another user's, writable by group
// or others, in a directory with either property: statepath.ReadTrusted), an
// oversized, unparsable or wrong-schema one all yield no snapshots and no error:
// the cache must never be able to block anything.
//
// The content is checked as hard as a listing fresh from the command, because
// the file is outside the process's control: every id is run through ValidID
// again, names are sanitized again, the number of models is capped again, the
// source is the package's own constant (never the file's text), and a snapshot
// with no usable id or a timestamp that is zero or far in the future is dropped.
func readCacheFile(stateDir string, now time.Time) map[string]Snapshot {
	if stateDir == "" {
		return nil
	}
	data, err := statepath.ReadTrusted(cachePath(stateDir), maxCacheBytes+1)
	if err != nil || len(data) > maxCacheBytes {
		return nil
	}
	var f cacheFile
	if err := json.Unmarshal(data, &f); err != nil || f.Schema != cacheSchema {
		return nil
	}
	out := make(map[string]Snapshot)
	for harness, h := range f.Harnesses {
		if harness != agyHarness {
			continue // only agy has a listing wired in; anything else is not ours
		}
		if h.ProbedAt.IsZero() || h.ProbedAt.After(now.Add(maxClockSkew)) {
			continue
		}
		models := cleanListing(h.Models)
		if len(models) == 0 {
			continue
		}
		out[harness] = Snapshot{Harness: harness, Source: agySource, ProbedAt: h.ProbedAt.UTC(), Models: models}
	}
	return out
}

// cleanListing applies to a listing read back from disk the rules parseAgyModels
// applies to one read from a command: valid ids only, first of a repeat, at most
// maxListedModels, sanitized names.
func cleanListing(in []DiscoveredModel) []DiscoveredModel {
	seen := make(map[string]struct{}, len(in))
	out := make([]DiscoveredModel, 0, len(in))
	for _, m := range in {
		if !ValidID(m.ID) {
			continue
		}
		if _, dup := seen[m.ID]; dup {
			continue
		}
		if len(out) >= maxListedModels {
			break
		}
		seen[m.ID] = struct{}{}
		out = append(out, DiscoveredModel{ID: m.ID, Name: sanitizeText(m.Name, maxNameRunes)})
	}
	return out
}

// writeCacheFile writes snaps to <stateDir>/model-discovery.json atomically with
// mode 0600: a temporary file in the same directory, renamed over the target. The
// rename replaces whatever is at the path, including a symlink planted there (the
// link is replaced, never followed), and a reader sees the old file or the new
// one, never half of either. The directory is created 0700 or, when it exists,
// checked and tightened (statepath.SecureDir); a directory that cannot be made
// private is an error and nothing is written.
func writeCacheFile(stateDir string, snaps map[string]Snapshot) error {
	if err := statepath.SecureDir(stateDir); err != nil {
		return err
	}
	f := cacheFile{Schema: cacheSchema, Harnesses: make(map[string]cacheHarness, len(snaps))}
	for harness, s := range snaps {
		f.Harnesses[harness] = cacheHarness{Source: s.Source, ProbedAt: s.ProbedAt.UTC(), Models: s.Models}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(stateDir, ".model-discovery-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(name)
		}
	}()
	if err := statepath.SecureFile(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, cachePath(stateDir)); err != nil {
		return fmt.Errorf("replace %s: %w", DiscoveryFileName, err)
	}
	renamed = true
	return nil
}

// cloneSnapshot copies s so a caller cannot change what the Discoverer holds.
func cloneSnapshot(s Snapshot) Snapshot {
	s.Models = append([]DiscoveredModel(nil), s.Models...)
	return s
}
