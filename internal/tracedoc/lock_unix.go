//go:build !windows

package tracedoc

import (
	"os"
	"syscall"
)

// lockRange takes an exclusive advisory lock, blocking until it is free.

func lockRange(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlockRange(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
