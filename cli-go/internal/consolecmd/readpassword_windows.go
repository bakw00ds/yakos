//go:build windows

package consolecmd

// readpassword_windows.go — no-echo terminal password reading on Windows.
//
// Uses the Windows Console API via syscall to disable echo on the input handle,
// reads a line, then restores the original mode.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const (
	enableEchoInput      = 0x0004
	enableLineInput      = 0x0002
	enableProcessedInput = 0x0001
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
)

// readPasswordNoEcho reads a password from stdin without echoing it (Windows).
func readPasswordNoEcho(stderr io.Writer) (string, error) {
	// Get the stdin handle.
	handle := syscall.Handle(os.Stdin.Fd())

	// Get current console mode.
	var mode uint32
	r, _, err := procGetConsoleMode.Call(uintptr(handle), uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		// Not a console (piped input); fall back to plain read.
		reader := bufio.NewReader(os.Stdin)
		line, rerr := reader.ReadString('\n')
		if rerr != nil && rerr != io.EOF {
			return "", fmt.Errorf("readPasswordNoEcho: stdin fallback: %w", rerr)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	// Disable echo while keeping line-input and processed-input enabled.
	newMode := mode &^ enableEchoInput
	procSetConsoleMode.Call(uintptr(handle), uintptr(newMode))
	defer procSetConsoleMode.Call(uintptr(handle), uintptr(mode))

	reader := bufio.NewReader(os.Stdin)
	line, rerr := reader.ReadString('\n')
	if rerr != nil && rerr != io.EOF {
		return "", fmt.Errorf("readPasswordNoEcho: read: %w", rerr)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
