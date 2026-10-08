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
	// triggerNonceCacheSize is how many accepted signatures are remembered.
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

// firstUse records sig and reports true the first time it is seen. Call it
// only for a signature that already verified, so unauthenticated traffic
// cannot fill the cache. The oldest entry is evicted past the size bound; the
// rate limit keeps the volume inside the window far below it.
func (g *triggerGuard) firstUse(sig string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seen == nil {
		g.seen = make(map[string]time.Time)
	}
	if _, dup := g.seen[sig]; dup {
		return false
	}
	g.seen[sig] = g.clock()
	g.order = append(g.order, sig)
	if len(g.order) > triggerNonceCacheSize {
		delete(g.seen, g.order[0])
		g.order = g.order[1:]
	}
	return true
}
