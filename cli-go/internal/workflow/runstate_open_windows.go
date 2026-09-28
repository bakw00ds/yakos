//go:build windows

package workflow

import (
	"os"
	"syscall"
)

// openRunJSONForRead opens path for reading with FILE_SHARE_DELETE included
// in the share mode, unlike os.Open/os.OpenFile's default Windows share
// mode (FILE_SHARE_READ|FILE_SHARE_WRITE only — see syscall.Open in the Go
// standard library's syscall_windows.go).
//
// Without FILE_SHARE_DELETE, a read of run.json can itself be the reason a
// concurrent persistNow rename fails with a sharing violation (K-88; see
// persistRenameMaxWait's doc comment in runstate.go, which is the primary
// defense — retrying the writer). This is belt-and-suspenders on the read
// side, for the one read path this package fully controls (LoadRunState);
// it cannot reach every caller that might os.ReadFile(run.json) directly
// (e.g. a test polling loop), which is why the writer-side retry is what
// the fix actually depends on.
func openRunJSONForRead(path string) (*os.File, error) {
	pathp, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	sharemode := uint32(syscall.FILE_SHARE_READ | syscall.FILE_SHARE_WRITE | syscall.FILE_SHARE_DELETE)
	h, err := syscall.CreateFile(
		pathp,
		syscall.GENERIC_READ,
		sharemode,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
