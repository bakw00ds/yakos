package supervisorstream

import "time"

// GlobMatchForTest exposes globMatch to the external test package.
func GlobMatchForTest(g, p string) bool { return globMatch(g, p) }

// SessionKeyForTest exposes sessionKey.
func SessionKeyForTest(id string) string { return sessionKey(id) }

// ResolveModelForTest exposes resolveModel.
func ResolveModelForTest(m string) (string, string) { return resolveModel(m) }

// LogExtraOrderForTest exposes the extras order list.
func LogExtraOrderForTest() []string { return append([]string(nil), logExtraOrder...) }

// RiskLabelsForTest exposes the bash-spelled risk labels and the pattern count.
func RiskLabelsForTest() ([]string, int) {
	return append([]string(nil), defaultRiskLabels...), len(defaultRiskPatterns)
}

// WrapLogOrderForTest exposes the wrapper's extras order.
func WrapLogOrderForTest() []string { return append([]string(nil), wrapLogOrder...) }

// SetLockBudgetForTest shortens the lock wait ceiling; the returned func restores it.
func SetLockBudgetForTest(d time.Duration) func() {
	old := lockBudget
	lockBudget = d
	return func() { lockBudget = old }
}
