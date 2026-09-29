package supervisorstream

// GlobMatchForTest exposes globMatch to the external test package.
func GlobMatchForTest(g, p string) bool { return globMatch(g, p) }
