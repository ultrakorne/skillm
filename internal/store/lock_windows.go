//go:build windows

package store

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// The locked byte range sits far past the end of the file: Windows locks are
// mandatory, so locking the bytes that hold the holder line would stop a waiter
// from reading who holds the lock.
const (
	lockOffsetHigh = 1
	lockLen        = 1
)

// tryLock takes an exclusive LockFileEx lock on f without blocking, returning
// errWouldBlock when another handle holds it.
func tryLock(f *os.File) error {
	ol := &windows.Overlapped{OffsetHigh: lockOffsetHigh}
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, lockLen, 0, ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return errWouldBlock
	}
	return err
}

// unlockFile releases the lock taken by tryLock.
func unlockFile(f *os.File) error {
	ol := &windows.Overlapped{OffsetHigh: lockOffsetHigh}
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, lockLen, 0, ol)
}
