package interactive

// SetAfterStdinWriteHookExported installs the write-goroutine test seam and
// returns a restore func.
func SetAfterStdinWriteHookExported(f func()) (restore func()) {
	prev := afterStdinWriteHook
	afterStdinWriteHook = f
	return func() { afterStdinWriteHook = prev }
}
