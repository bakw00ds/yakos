package metrics

import (
	"math"
	"sort"
)

// floatPtr returns a pointer to a float64.
func floatPtr(f float64) *float64 { return &f }

// intPtr returns a pointer to an int.
func intPtr(i int) *int { return &i }

// Median returns the median of vals and ok=true.
// Returns 0, false when vals is empty.
func Median(vals []float64) (float64, bool) {
	if len(vals) == 0 {
		return 0, false
	}
	cp := make([]float64, len(vals))
	copy(cp, vals)
	sort.Float64s(cp)
	n := len(cp)
	if n%2 == 0 {
		return (cp[n/2-1] + cp[n/2]) / 2, true
	}
	return cp[n/2], true
}

// Mean returns the arithmetic mean of vals and ok=true.
// Returns 0, false when vals is empty.
func Mean(vals []float64) (float64, bool) {
	if len(vals) == 0 {
		return 0, false
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals)), true
}

// Percentile returns the p-th percentile (0–100) of vals using linear
// interpolation and ok=true.
// Returns 0, false when vals is empty or p is out of range.
func Percentile(vals []float64, p float64) (float64, bool) {
	if len(vals) == 0 || p < 0 || p > 100 {
		return 0, false
	}
	cp := make([]float64, len(vals))
	copy(cp, vals)
	sort.Float64s(cp)
	if p == 100 {
		return cp[len(cp)-1], true
	}
	rank := (p / 100) * float64(len(cp)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		return cp[lower], true
	}
	frac := rank - float64(lower)
	return cp[lower]*(1-frac) + cp[upper]*frac, true
}

// Rate returns numerator/denominator and ok=true.
// Returns 0, false when denominator is zero.
func Rate(numerator, denominator int) (float64, bool) {
	if denominator == 0 {
		return 0, false
	}
	return float64(numerator) / float64(denominator), true
}
