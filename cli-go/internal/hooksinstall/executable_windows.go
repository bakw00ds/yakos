//go:build windows

package hooksinstall

import (
	"os"
	"strings"
)

// executableExtensions is the set of file extensions treated as executable on Windows.
// Any regular file with one of these extensions (or no extension at all) is considered
// an executable candidate for hook installation.
var executableExtensions = map[string]bool{
	".sh":   true,
	".bash": true,
	".py":   true,
	".ps1":  true,
	".cmd":  true,
	".bat":  true,
	".exe":  true,
}

// isExecutable reports whether fi represents an executable file on Windows.
// On Windows, file mode bits do not carry executable information, so we fall
// back to extension matching: .sh, .bash, .py, .ps1, .cmd, .bat, .exe, or
// no extension at all, are all treated as executable candidates.
func isExecutable(fi os.FileInfo) bool {
	if fi.IsDir() {
		return false
	}
	name := fi.Name()
	dot := strings.LastIndex(name, ".")
	if dot == -1 {
		// No extension — treat as executable (e.g. a Unix-style script without .sh).
		return true
	}
	ext := strings.ToLower(name[dot:])
	return executableExtensions[ext]
}
