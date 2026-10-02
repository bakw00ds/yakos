package supervisorstream

// GlobMatchForTest exposes globMatch to the external test package.
func GlobMatchForTest(g, p string) bool { return globMatch(g, p) }

// SessionKeyForTest exposes sessionKey.
func SessionKeyForTest(id string) string { return sessionKey(id) }

// ResolveModelForTest exposes resolveModel.
func ResolveModelForTest(m string) (string, string) { return resolveModel(m) }
