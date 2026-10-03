//go:build windows

package tracedoc

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// Replacing a file that another process holds open for even a moment -- an
// indexer, a virus scanner, a sync client -- fails on Windows with a sharing
// violation that clears on its own. The Python original used os.replace and
// had no retry, so the failure surfaced as a lost state file.
const (
	errAccessDenied     = syscall.Errno(5)
	errSharingViolation = syscall.Errno(32)
	errLockViolation    = syscall.Errno(33)
)

func isTransientReplaceError(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case errAccessDenied, errSharingViolation, errLockViolation:
		return true
	}
	return false
}

// replaceFile is the atomic replace used to publish the state file: rename
// onto the destination, retrying briefly while the destination is held open
// by someone else.
func replaceFile(oldpath, newpath string) error {
	var err error
	for attempt := 0; attempt < 10; attempt++ {
		if err = os.Rename(oldpath, newpath); err == nil {
			return nil
		}
		if !isTransientReplaceError(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	return err
}
