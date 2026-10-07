//go:build windows

package knowledge

// openNonblock is 0 on Windows: opening a named pipe through a path does not
// wait for a writer, and the regular-file check refuses it afterwards.
const openNonblock = 0
