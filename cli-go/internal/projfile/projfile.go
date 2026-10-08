// Package projfile is the one reader of a project's .yakos.yml (K-164).
//
// A project directory is untrusted input: it can be a clone of a hostile repo.
// A plain os.ReadFile of its .yakos.yml follows a symlink to /dev/zero (gigabytes
// of memory) or blocks for good on a FIFO, and the hooks that read it run on
// every tool call. Every reader in the CLI goes through ReadFile or Read, and a
// file they refuse is treated as ABSENT by all of them, so two readers can never
// disagree about whether a project setting exists.
package projfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// MaxBytes caps how much of a project's .yakos.yml is read: 1 MiB, far above any
// real file. A longer file is refused, not truncated.
const MaxBytes = 1 << 20

// Name is the file this package reads.
const Name = ".yakos.yml"

// RefusedError says why a file that exists was not read. Its text names no path,
// so it is safe in a warning, a JSON status or a doctor line.
// Cause is the underlying I/O error when there is one; it may carry a path, so it
// is reachable through errors.Is and Unwrap but never through Error.
type RefusedError struct {
	Reason string
	Cause  error
}

func (e *RefusedError) Error() string { return e.Reason }
func (e *RefusedError) Unwrap() error { return e.Cause }

// IsRefused reports whether err is a refusal (as opposed to os.ErrNotExist).
func IsRefused(err error) bool {
	var r *RefusedError
	return errors.As(err, &r)
}

// Notice is the fixed, path-free line a hook or doctor prints for a refusal.
func Notice(err error) string {
	return Name + " ignored: " + err.Error() + "; its project settings are off"
}

func refuse(format string, a ...any) error { return &RefusedError{Reason: fmt.Sprintf(format, a...)} }

// pathFree strips the path an fs error carries, keeping only the cause.
func pathFree(err error) error {
	cause := err
	var pe *fs.PathError
	if errors.As(err, &pe) {
		cause = pe.Err
	}
	return &RefusedError{Reason: cause.Error(), Cause: err}
}

// afterLstat runs between the Lstat that vets the path and the open that reads
// it, and afterOpen between the descriptor check and the read. Both do nothing in
// production; a test swaps or grows the file there, the way a racing process
// would, to prove the open flags, the descriptor check and the read cap hold.
var (
	afterLstat = func(path string) {}
	afterOpen  = func(path string) {}
)

// Read reads <project>/.yakos.yml through ReadFile.
func Read(project string) ([]byte, error) { return ReadFile(filepath.Join(project, Name)) }

// ReadFile reads path and nothing that is not that file.
//
// It refuses a symlink, a FIFO, a device and any other file that is not regular,
// and a file over MaxBytes, with a *RefusedError that says why. A file that is not
// there is os.ErrNotExist, which callers treat as no configuration. The file is
// opened without blocking and without following a link, and is checked again after
// the open, so one swapped in after the Lstat can neither hold the open for good
// nor lead somewhere else. The read itself is bounded, so a file that grows is cut
// off at the cap.
func ReadFile(path string) ([]byte, error) {
	li, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, pathFree(err)
	}
	if err := checkInfo(li); err != nil {
		return nil, err
	}
	afterLstat(path)
	f, err := os.OpenFile(path, readFlags, 0) //nolint:gosec // inspected above, opened without following links
	if err != nil {
		if linkRefused(err) {
			return nil, refuse("is a symlink, which is not followed")
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, pathFree(err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, pathFree(err)
	}
	if err := checkInfo(fi); err != nil {
		return nil, err
	}
	if !os.SameFile(li, fi) {
		return nil, refuse("changed while it was being opened")
	}
	afterOpen(path)
	return readBounded(f)
}

// readBounded reads at most MaxBytes from r; one byte more is a refusal. It never
// reads past MaxBytes+1, however much r has.
func readBounded(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, pathFree(err)
	}
	if len(data) > MaxBytes {
		return nil, tooLarge()
	}
	return data, nil
}

func checkInfo(fi os.FileInfo) error {
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return refuse("is a symlink, which is not followed")
	case !fi.Mode().IsRegular():
		return refuse("is not a regular file")
	case fi.Size() > MaxBytes:
		return tooLarge()
	}
	return nil
}

func tooLarge() error { return refuse("is larger than %d bytes", MaxBytes) }
