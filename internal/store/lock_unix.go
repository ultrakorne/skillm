//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package store

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLock takes an exclusive flock on f without blocking, returning
// errWouldBlock when another open file description holds it.
func tryLock(f *os.File) error {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EWOULDBLOCK):
			return errWouldBlock
		default:
			return err
		}
	}
}

// unlockFile releases the flock taken by tryLock.
func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
