package budget

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MaxProjectFileBytes caps how much of a project's .yakos.yml is read: 1 MiB,
// far above any real file. A longer file is refused, not truncated.
const MaxProjectFileBytes = 1 << 20

// readProjectFile reads <project>/.yakos.yml and nothing that is not that file.
//
// It refuses a symlink, a FIFO, a device and any other file that is not regular,
// and a file over MaxProjectFileBytes, with an error that says why. A file that
// is not there is os.ErrNotExist, which the caller treats as no configuration.
// The file is opened without blocking and without following a link, and is
// checked again after the open, so one swapped in after the Lstat can neither
// hold the open for good nor lead somewhere else. The read itself is bounded, so
// a file that grows is cut off at the cap.
// afterProjectLstat runs between the Lstat that vets the path and the open that
// reads it. It does nothing in production; a test swaps the file there, the way a
// racing process would, to prove the open flags and the descriptor check hold.
var afterProjectLstat = func(path string) {}

func readProjectFile(project string) ([]byte, error) {
	path := filepath.Join(project, ".yakos.yml")
	li, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err := checkProjectFileInfo(li); err != nil {
		return nil, err
	}
	afterProjectLstat(path)
	f, err := os.OpenFile(path, projectReadFlags, 0) //nolint:gosec // inspected above, opened without following links
	if err != nil {
		if projectLinkRefused(err) {
			return nil, errors.New("is a symlink, which is not followed")
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := checkProjectFileInfo(fi); err != nil {
		return nil, err
	}
	if !os.SameFile(li, fi) {
		return nil, errors.New("changed while it was being opened")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxProjectFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxProjectFileBytes {
		return nil, tooLargeErr()
	}
	return data, nil
}

func checkProjectFileInfo(fi os.FileInfo) error {
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return errors.New("is a symlink, which is not followed")
	case !fi.Mode().IsRegular():
		return errors.New("is not a regular file")
	case fi.Size() > MaxProjectFileBytes:
		return tooLargeErr()
	}
	return nil
}

func tooLargeErr() error {
	return fmt.Errorf("is larger than %d bytes", MaxProjectFileBytes)
}
