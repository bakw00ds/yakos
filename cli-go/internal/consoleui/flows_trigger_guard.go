package consoleui

// flows_trigger_guard.go — replay and rate controls for the webhook trigger
// (K-152): a per-workflow sliding-window rate limit and a bounded cache of
// signatures already accepted inside the timestamp window.

import (
	"sync"
	"time"
)

const (
	// triggerRatePerMin is the most requests per workflow per minute.
	triggerRatePerMin = 6
	// triggerMaxTrackedNames bounds the rate-limit map; past it new names are
	// limited until old windows drain.
	triggerMaxTrackedNames = 1024
	// triggerReplayWindow is how far a request timestamp may be from now.
	triggerReplayWindow = 5 * time.Minute
	// triggerNonceCacheSize is how many live accepted signatures are remembered;
	// past it new requests are refused, never an older entry evicted.
	triggerNonceCacheSize = 1000
)

type triggerGuard struct {
	mu    sync.Mutex
	now   func() time.Time // nil = time.Now
	hits  map[string][]time.Time
	seen  map[string]time.Time // signature -> accepted at
	order []string             // FIFO of signatures for eviction
}

func (g *triggerGuard) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// allow records one request for name and reports whether it is within the
// rate limit. It is applied before any enablement check so a 429 reveals
// nothing about configuration.
func (g *triggerGuard) allow(name string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock()
	if g.hits == nil {
		g.hits = make(map[string][]time.Time)
	}
	cut := now.Add(-time.Minute)
	if _, known := g.hits[name]; !known && len(g.hits) >= triggerMaxTrackedNames {
		for k, ts := range g.hits {
			if len(ts) == 0 || !ts[len(ts)-1].After(cut) {
				delete(g.hits, k)
			}
		}
		if len(g.hits) >= triggerMaxTrackedNames {
			return false
		}
	}
	kept := g.hits[name][:0]
	for _, t := range g.hits[name] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= triggerRatePerMin {
		g.hits[name] = kept
		return false
	}
	g.hits[name] = append(kept, now)
	return true
}

// fresh reports whether ts is within the replay window of now.
func (g *triggerGuard) fresh(ts time.Time) bool {
	d := g.clock().Sub(ts)
	if d < 0 {
		d = -d
	}
	return d <= triggerReplayWindow
}

// replayed reports whether sig was already accepted. It never records, so a
// request that is later refused (rate limit) leaves the cache untouched.
func (g *triggerGuard) replayed(sig string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, dup := g.seen[sig]
	return dup
}

// record remembers sig for a request that is proceeding and reports false if
// the signature was already recorded (a concurrent duplicate) or the cache is
// full of live entries. Call it only for a signature that verified and passed
// the rate limit. Entries older than twice the replay window are dropped (a
// timestamp may be skewed either way, so a signature can stay replayable that
// long); a live entry is never evicted. If the cache is still full the
// request fails closed (404) rather than making room.
func (g *triggerGuard) record(sig string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seen == nil {
		g.seen = make(map[string]time.Time)
	}
	if _, dup := g.seen[sig]; dup {
		return false
	}
	now := g.clock()
	cut := now.Add(-2 * triggerReplayWindow)
	for len(g.order) > 0 && !g.seen[g.order[0]].After(cut) {
		delete(g.seen, g.order[0])
		g.order = g.order[1:]
	}
	if len(g.order) >= triggerNonceCacheSize {
		return false
	}
	g.seen[sig] = now
	g.order = append(g.order, sig)
	return true
}
