package budget

// status_helpers_test.go: HasLimit and OverStop are what the supervisor hook's gate
// decides from (K-136), next to Enforce. Each limit that is configured counts on its
// own; one that is not configured never does.

import "testing"

func TestStatus_HasLimitAndOverStop(t *testing.T) {
	for _, c := range []struct {
		name      string
		s         Status
		has, over bool
	}{
		{"no limit of either kind", Status{}, false, false},
		{"no limit, but tokens were spent", Status{SpentTokens: 99_999_999, SpentUSD: 99}, false, false},
		{"dollars only, under the stop", Status{LimitUSD: 10, StopUSD: 20, SpentUSD: 15}, true, false},
		{"dollars only, at the stop", Status{LimitUSD: 10, StopUSD: 20, SpentUSD: 20}, true, true},
		{"tokens only, over the limit but under the stop", Status{LimitTokens: 1000, StopTokens: 2000, SpentTokens: 1500}, true, false},
		{"tokens only, at the stop", Status{LimitTokens: 1000, StopTokens: 2000, SpentTokens: 2000}, true, true},
		{"both, only the token stop reached", Status{LimitUSD: 10, StopUSD: 20, SpentUSD: 1, LimitTokens: 1000, StopTokens: 2000, SpentTokens: 2500}, true, true},
		{"both, only the dollar stop reached", Status{LimitUSD: 10, StopUSD: 20, SpentUSD: 25, LimitTokens: 1000, StopTokens: 2000, SpentTokens: 5}, true, true},
		{"both, neither stop reached", Status{LimitUSD: 10, StopUSD: 20, SpentUSD: 19, LimitTokens: 1000, StopTokens: 2000, SpentTokens: 1999}, true, false},
		{"a token limit that is not set never trips on spent tokens", Status{LimitUSD: 10, StopUSD: 10, SpentUSD: 1, SpentTokens: 99_999_999}, true, false},
		{"a dollar limit that is not set never trips on spent dollars", Status{LimitTokens: 1000, StopTokens: 1000, SpentTokens: 1, SpentUSD: 99}, true, false},
	} {
		if got := c.s.HasLimit(); got != c.has {
			t.Errorf("%s: HasLimit = %v, want %v", c.name, got, c.has)
		}
		if got := c.s.OverStop(); got != c.over {
			t.Errorf("%s: OverStop = %v, want %v", c.name, got, c.over)
		}
	}
}
