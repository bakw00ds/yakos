package workflow

// cron.go — the in-house 5-field cron parser behind workflow `triggers.cron`
// (K-152). No dependency: the grammar is small and the DST rules are the point.
//
// Grammar: "minute hour day-of-month month day-of-week". Each field is a
// comma list of `*`, `n`, `a-b`, with an optional `/step` (`*/5`, `10-30/5`).
// Month and weekday accept three-letter names (jan, mon). Weekday 7 is Sunday.
// When both day-of-month and day-of-week are restricted a day matches if EITHER
// does (Vixie cron); when either is `*` the other alone decides.
//
// Time is wall-clock time in a *time.Location the caller picks (the user's
// schedules file names it; default time.Local). Daylight-saving transitions:
//
//   - Spring forward (a wall time that does not exist, 02:30 on the gap day):
//     the fire happens ONCE, at the first instant after the gap (03:00). Several
//     skipped wall times (`*/15`) collapse into that one instant.
//   - Fall back (a wall time that happens twice, 01:30 on the repeat day): a
//     schedule with a fixed hour fires ONCE, at the first occurrence. A schedule
//     whose hour field is `*` (hourly, every N minutes) runs on elapsed time, so
//     it fires at BOTH occurrences and an interval schedule keeps its cadence.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// maxCronExprLen bounds an expression before it is parsed.
const maxCronExprLen = 256

// Cron is a parsed 5-field expression. The zero value matches nothing; build
// one with ParseCron.
type Cron struct {
	min, hour, dom, month, dow uint64
	domAny, dowAny, hourAny    bool
}

type cronField struct {
	name     string
	lo, hi   int
	names    []string // index i is the name of value lo+i (empty: no names)
	allowSun bool     // dow: 7 is accepted and folded to 0
}

var cronFields = [5]cronField{
	{name: "minute", lo: 0, hi: 59},
	{name: "hour", lo: 0, hi: 23},
	{name: "day-of-month", lo: 1, hi: 31},
	{name: "month", lo: 1, hi: 12, names: []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}},
	{name: "day-of-week", lo: 0, hi: 7, names: []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}, allowSun: true},
}

// ParseCron parses a 5-field cron expression.
func ParseCron(expr string) (*Cron, error) {
	if len(expr) > maxCronExprLen {
		return nil, fmt.Errorf("cron: expression longer than %d bytes", maxCronExprLen)
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return nil, fmt.Errorf("cron: want 5 fields (minute hour day-of-month month day-of-week), got %d", len(parts))
	}
	var sets [5]uint64
	var star [5]bool
	for i, p := range parts {
		s, st, err := parseCronField(cronFields[i], p)
		if err != nil {
			return nil, err
		}
		sets[i], star[i] = s, st
	}
	c := &Cron{min: sets[0], hour: sets[1], dom: sets[2], month: sets[3], dow: sets[4],
		domAny: star[2], dowAny: star[4]}
	c.hourAny = sets[1] == (1<<24)-1
	return c, nil
}

func parseCronField(f cronField, s string) (set uint64, star bool, err error) {
	if s == "" {
		return 0, false, fmt.Errorf("cron: empty %s field", f.name)
	}
	for _, item := range strings.Split(s, ",") {
		step := 1
		rng := item
		if i := strings.IndexByte(item, '/'); i >= 0 {
			rng = item[:i]
			n, e := strconv.Atoi(item[i+1:])
			if e != nil || n < 1 || n > f.hi {
				return 0, false, fmt.Errorf("cron: bad step in %s field", f.name)
			}
			step = n
		}
		lo, hi := f.lo, f.hi
		if f.allowSun {
			hi = 6 // `*` and `*/n` cover 0-6; 7 only as an explicit value
		}
		switch {
		case rng == "*":
			star = star || s[0] == '*' // Vixie: a field that starts with `*` is "any"
		case strings.Contains(rng, "-"):
			a, b, ok := strings.Cut(rng, "-")
			if !ok {
				return 0, false, fmt.Errorf("cron: bad range in %s field", f.name)
			}
			var e1, e2 error
			lo, e1 = cronValue(f, a)
			hi, e2 = cronValue(f, b)
			if e1 != nil || e2 != nil || lo > hi {
				return 0, false, fmt.Errorf("cron: bad range in %s field", f.name)
			}
		default:
			v, e := cronValue(f, rng)
			if e != nil {
				return 0, false, e
			}
			lo, hi = v, v
			if strings.Contains(item, "/") {
				hi = f.hi // `n/step` runs from n to the end, like Vixie cron
			}
		}
		for v := lo; v <= hi; v += step {
			if f.allowSun && v == 7 {
				set |= 1
				continue
			}
			set |= 1 << uint(v)
		}
	}
	if set == 0 {
		return 0, false, fmt.Errorf("cron: %s field matches nothing", f.name)
	}
	return set, star, nil
}

func cronValue(f cronField, s string) (int, error) {
	if len(f.names) > 0 {
		ls := strings.ToLower(s)
		for i, n := range f.names {
			if ls == n {
				return f.lo + i, nil
			}
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < f.lo || v > f.hi {
		return 0, fmt.Errorf("cron: %s value %q out of range %d-%d", f.name, s, f.lo, f.hi)
	}
	return v, nil
}

// dayMatches reports whether the calendar day matches the date fields.
func (c *Cron) dayMatches(t time.Time) bool {
	if c.month&(1<<uint(t.Month())) == 0 {
		return false
	}
	domOK := c.dom&(1<<uint(t.Day())) != 0
	dowOK := c.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case c.domAny && c.dowAny:
		return true
	case c.domAny:
		return dowOK
	case c.dowAny:
		return domOK
	default:
		return domOK || dowOK
	}
}

// maxCronDays bounds the day scan: 8 years covers every leap-day schedule.
const maxCronDays = 366 * 8

// Next returns the first fire instant strictly after `after`, evaluating the
// expression as wall-clock time in loc (nil means UTC), or the zero time when
// none exists within eight years (an impossible date like Feb 31).
func (c *Cron) Next(after time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := after.In(loc)
	// Start one day early: a skipped wall time maps forward to the gap end, so
	// a wall time on the previous date can still fire after `after`.
	y, m, d := local.AddDate(0, 0, -1).Date()
	for i := 0; i < maxCronDays; i++ {
		day := time.Date(y, m, d+i, 12, 0, 0, 0, loc) // noon: never inside a gap
		if !c.dayMatches(day) {
			continue
		}
		dy, dm, dd := day.Date()
		var best time.Time
		for h := 0; h < 24; h++ {
			if c.hour&(1<<uint(h)) == 0 {
				continue
			}
			for mi := 0; mi < 60; mi++ {
				if c.min&(1<<uint(mi)) == 0 {
					continue
				}
				for _, at := range c.instants(dy, dm, dd, h, mi, loc) {
					if at.After(after) && (best.IsZero() || at.Before(best)) {
						best = at
					}
				}
			}
		}
		if !best.IsZero() {
			return best
		}
	}
	return time.Time{}
}

// instants resolves one wall-clock time to the instants it fires at, applying
// the package's DST rules.
func (c *Cron) instants(y int, mo time.Month, d, h, mi int, loc *time.Location) []time.Time {
	wall := time.Date(y, mo, d, h, mi, 0, 0, time.UTC) // the wall time, as if UTC
	var found []time.Time
	seen := map[int]bool{}
	for _, probe := range []time.Time{wall.AddDate(0, 0, -1), wall.AddDate(0, 0, 1)} {
		_, off := probe.In(loc).Zone()
		if seen[off] {
			continue
		}
		seen[off] = true
		at := wall.Add(-time.Duration(off) * time.Second)
		l := at.In(loc)
		if l.Year() == y && l.Month() == mo && l.Day() == d && l.Hour() == h && l.Minute() == mi {
			found = append(found, at)
		}
	}
	switch len(found) {
	case 0: // spring-forward gap: fire once at the first instant after it
		return []time.Time{gapEnd(wall, loc)}
	case 1:
		return found
	}
	if found[1].Before(found[0]) {
		found[0], found[1] = found[1], found[0]
	}
	if c.hourAny { // fall-back repeat: elapsed-time schedules run both passes
		return found
	}
	return found[:1]
}

// gapEnd returns the first instant at which the wall clock has moved past the
// non-existent wall time `wall` (given as if UTC).
func gapEnd(wall time.Time, loc *time.Location) time.Time {
	// The transition lies within a day either side of the wall time, read as
	// an instant. Walk minute by minute to the first instant whose wall clock
	// is at or after `wall`.
	t := wall.AddDate(0, 0, -1).Add(-14 * time.Hour)
	_, before := t.In(loc).Zone()
	for i := 0; i < 3*24*60; i++ {
		t = t.Add(time.Minute)
		if _, off := t.In(loc).Zone(); off != before {
			return t
		}
	}
	return wall // unreachable for real zones
}
