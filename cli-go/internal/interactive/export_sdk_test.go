package interactive

// export_sdk_test.go — Test-only exports from the interactive package.
// This file is compiled only when running tests (the _test suffix in the
// filename causes the Go toolchain to treat it as a test helper file).
// It exposes unexported symbols to the external test package (interactive_test).

// ParseNodeMajorExported wraps parseNodeMajor for use in external tests.
func ParseNodeMajorExported(version string) int {
	return parseNodeMajor(version)
}

