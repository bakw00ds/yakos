package budget

// tocount_test.go: toCount is the aggregate's own guard on a parsed token count (K-136).
// cost.Event.Tokens applies the same bound for the other readers, so a missing guard here
// could not be seen through the aggregate; this pins it directly.

import (
	"math"
	"testing"
)

func TestToCount_BoundsAHostileCount(t *testing.T) {
	for name, tc := range map[string]struct {
		in   float64
		want int64
	}{
		"zero":                 {0, 0},
		"an ordinary count":    {1234, 1234},
		"a fraction truncates": {12.9, 12},
		"the largest allowed":  {float64(int64(1) << 40), int64(1) << 40},
		"one past the bound":   {float64(int64(1)<<40) + 4096, 0},
		"far past the bound":   {1e30, 0},
		"negative":             {-1, 0},
		"a large negative":     {-1e30, 0},
		"NaN":                  {math.NaN(), 0},
		"+Inf":                 {math.Inf(1), 0},
		"-Inf":                 {math.Inf(-1), 0},
	} {
		if got := toCount(tc.in); got != tc.want {
			t.Errorf("%s: toCount(%v) = %d, want %d", name, tc.in, got, tc.want)
		}
	}
}
