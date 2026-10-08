package workflow_test

import (
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/workflow"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("tz database unavailable: %v", err)
	}
	return loc
}

// seq returns the next n fire instants after start, formatted in UTC.
func seq(t *testing.T, expr string, start time.Time, loc *time.Location, n int) []string {
	t.Helper()
	c, err := workflow.ParseCron(expr)
	if err != nil {
		t.Fatalf("ParseCron(%q): %v", expr, err)
	}
	var out []string
	at := start
	for i := 0; i < n; i++ {
		at = c.Next(at, loc)
		if at.IsZero() {
			out = append(out, "none")
			break
		}
		out = append(out, at.UTC().Format("2006-01-02T15:04Z"))
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCronNext_Table(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	at := func(s string, loc *time.Location) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		name  string
		expr  string
		start time.Time
		loc   *time.Location
		want  []string
	}{
		{"every five minutes", "*/5 * * * *", at("2026-06-01 10:02", time.UTC), time.UTC,
			[]string{"2026-06-01T10:05Z", "2026-06-01T10:10Z"}},
		{"strictly after", "0 9 * * *", at("2026-06-01 09:00", time.UTC), time.UTC,
			[]string{"2026-06-02T09:00Z"}},
		{"weekdays", "0 9 * * mon-fri", at("2026-06-05 10:00", time.UTC), time.UTC, // Fri
			[]string{"2026-06-08T09:00Z", "2026-06-09T09:00Z"}},
		{"dom or dow", "0 0 13 * fri", at("2026-06-01 00:00", time.UTC), time.UTC,
			[]string{"2026-06-05T00:00Z", "2026-06-12T00:00Z", "2026-06-13T00:00Z"}},
		{"sunday as 7", "0 0 * * 7", at("2026-06-01 00:00", time.UTC), time.UTC, // Mon
			[]string{"2026-06-07T00:00Z"}},
		{"leap day", "0 0 29 2 *", at("2026-03-01 00:00", time.UTC), time.UTC,
			[]string{"2028-02-29T00:00Z"}},
		{"impossible date", "0 0 31 2 *", at("2026-03-01 00:00", time.UTC), time.UTC,
			[]string{"none"}},

		// Spring forward, 2026-03-08 02:00 -> 03:00 in New York.
		{"gap: fixed time fires once at the gap end", "30 2 * * *", at("2026-03-07 12:00", ny), ny,
			[]string{"2026-03-08T07:00Z", "2026-03-09T06:30Z"}},
		{"gap: skipped interval collapses to one fire", "*/15 2 * * *", at("2026-03-08 00:00", ny), ny,
			[]string{"2026-03-08T07:00Z", "2026-03-09T06:00Z"}},
		{"gap: later wall times unaffected", "0 9 * * *", at("2026-03-07 12:00", ny), ny,
			[]string{"2026-03-08T13:00Z", "2026-03-09T13:00Z"}}, // 09:00 EST then 09:00 EDT

		// Fall back, 2026-11-01 02:00 EDT -> 01:00 EST in New York.
		{"fold: fixed hour fires once, first occurrence", "30 1 * * *", at("2026-11-01 00:00", ny), ny,
			[]string{"2026-11-01T05:30Z", "2026-11-02T06:30Z"}},
		{"fold: hourly runs on elapsed time", "0 * * * *", at("2026-11-01 00:30", ny), ny,
			[]string{"2026-11-01T05:00Z", "2026-11-01T06:00Z", "2026-11-01T07:00Z"}},
		{"fold: every 30 minutes keeps cadence", "*/30 * * * *", at("2026-11-01 00:45", ny), ny,
			[]string{"2026-11-01T05:00Z", "2026-11-01T05:30Z", "2026-11-01T06:00Z", "2026-11-01T06:30Z", "2026-11-01T07:00Z"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := seq(t, tc.expr, tc.start, tc.loc, len(tc.want))
			if !eq(got, tc.want) {
				t.Fatalf("%q after %s:\n got  %v\n want %v", tc.expr, tc.start, got, tc.want)
			}
		})
	}
}

func TestParseCron_Rejects(t *testing.T) {
	for _, expr := range []string{
		"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * 32 * *",
		"* * * 13 *", "* * * * 8", "*/0 * * * *", "*/x * * * *", "5-1 * * * *", "a * * * *",
		"1,,2 * * * *", "- * * * *", "* * * foo *",
	} {
		if _, err := workflow.ParseCron(expr); err == nil {
			t.Errorf("ParseCron(%q) accepted, want error", expr)
		}
	}
	if _, err := workflow.ParseCron(string(make([]byte, 300))); err == nil {
		t.Error("an over-long expression must be refused")
	}
}

func TestParseCron_Accepts(t *testing.T) {
	for _, expr := range []string{
		"* * * * *", "0 0 1 1 *", "*/15 9-17 * * mon-fri", "0,30 8,20 * jan,jul *", "10-50/10 * * * *", "5/20 * * * *",
	} {
		if _, err := workflow.ParseCron(expr); err != nil {
			t.Errorf("ParseCron(%q): %v", expr, err)
		}
	}
}

// Across seven months in zones with unusual transitions (southern hemisphere,
// half-hour DST, a skipped calendar day), fire times must strictly increase,
// terminate, and never repeat an instant.
func TestCronNext_MonotonicAcrossOddZones(t *testing.T) {
	exprs := []string{"30 2 * * *", "0 * * * *", "30 1 * * *", "0 0 * * *", "59 23 * * *", "*/7 1-3 * * *"}
	zones := []string{"America/New_York", "Australia/Lord_Howe", "Pacific/Apia", "Europe/London", "America/Sao_Paulo", "Asia/Kolkata"}
	for _, z := range zones {
		loc := mustLoc(t, z)
		for _, e := range exprs {
			c, err := workflow.ParseCron(e)
			if err != nil {
				t.Fatal(err)
			}
			at := time.Date(2011, 10, 15, 0, 0, 0, 0, time.UTC) // spans NY fall-back, Apia's skipped day, spring-forward, southern fall-back
			end := at.AddDate(0, 7, 0)
			for n := 0; at.Before(end); n++ {
				next := c.Next(at, loc)
				if next.IsZero() || !next.After(at) {
					t.Fatalf("%s %q: Next(%s) = %s, want a later instant", z, e, at, next)
				}
				if n > 40000 {
					t.Fatalf("%s %q: runaway", z, e)
				}
				at = next
			}
		}
	}
}
