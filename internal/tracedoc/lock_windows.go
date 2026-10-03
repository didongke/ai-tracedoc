//go:build windows

package tracedoc

import (
	"os"
	"syscall"
	"unsafe"
)

// Windows has no flock(2). LockFileEx is the native equivalent, reached
// through LazyDLL so the build stays free of cgo and of external modules.
var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock = 0x00000002
	// Without FAIL_IMMEDIATELY, LockFileEx blocks until the range is free.
	lockfileFailImmediately = 0x00000001
)

type overlapped struct {
	Internal     uintptr
	InternalHigh uintptr
	Offset       uint32
	OffsetHigh   uint32
	HEvent       uintptr
}

// lockRange takes an exclusive lock on the first byte, blocking until it is
// free. The file must be open for reading and writing: LockFileEx rejects a
// handle without GENERIC_WRITE with ERROR_ACCESS_DENIED, which is why the
// lock file is opened O_RDWR and never O_APPEND.
func lockRange(f *os.File) error {
	var ol overlapped
	r, _, err := procLockFileEx.Call(
		f.Fd(),
		uintptr(lockfileExclusiveLock),
		0,
		1, 0, // lock 1 byte, low/high length
		uintptr(unsafe.Pointer(&ol)),
	)
	if r == 0 {
		return err
	}
	return nil
}

func unlockRange(f *os.File) error {
	var ol overlapped
	r, _, err := procUnlockFileEx.Call(
		f.Fd(),
		0,
		1, 0,
		uintptr(unsafe.Pointer(&ol)),
	)
	if r == 0 {
		return err
	}
	return nil
}
