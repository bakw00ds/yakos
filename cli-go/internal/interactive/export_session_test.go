package interactive

// SetAfterStdinWriteHookExported installs the write-goroutine test seam and
// returns a restore func.
func SetAfterStdinWriteHookExported(f func()) (restore func()) {
	prev := afterStdinWriteHook
	afterStdinWriteHook = f
	return func() { afterStdinWriteHook = prev }
}

// ReapOnce runs one idle-reaper scan now, so a test does not depend on the
// reaper's ticker.
func (m *Manager) ReapOnce() { m.reapOnce() }
