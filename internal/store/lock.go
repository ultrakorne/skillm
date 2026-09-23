package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LockFileName is the base name of the lock file within Home.
const LockFileName = ".lock"

// lockTimeout is how long Lock waits for another holder before giving up, and
// lockPoll how often it retries meanwhile. Variables so tests can shorten them.
var (
	lockTimeout = 30 * time.Second
	lockPoll    = 50 * time.Millisecond
)

// errWouldBlock is returned by tryLock when another handle holds the lock.
var errWouldBlock = errors.New("lock held elsewhere")

// LockTimeoutError reports that Home stayed locked by another skillm operation
// for the whole wait. Holder describes that operation ("pid 123: skillm
// install foo") when it could be read, and is empty otherwise.
type LockTimeoutError struct {
	Path    string
	Holder  string
	Timeout time.Duration
}

func (e *LockTimeoutError) Error() string {
	who := "another skillm operation"
	if e.Holder != "" {
		who += " (" + e.Holder + ")"
	}
	return fmt.Sprintf("%s is still running after %s (it holds %s); wait for it to finish and try again", who, e.Timeout, e.Path)
}

// Lock takes the exclusive, cross-process lock on Home (an advisory lock on
// <home>/.lock: flock on unix, LockFileEx on Windows), creating Home if needed.
// Every command that saves config or state holds it from load to save, so two
// skillm processes (the CLI and a GUI, say) never interleave their writes.
// Read-only commands take no lock.
//
// Lock waits up to 30s for a current holder, then fails with a
// *LockTimeoutError naming it. The lock is released by the returned unlock
// (safe to call more than once) or when the process exits.
func Lock(home string) (unlock func(), err error) {
	if err := EnsureHome(home); err != nil {
		return nil, err
	}
	path := filepath.Join(home, LockFileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}

	deadline := time.Now().Add(lockTimeout)
	for {
		err := tryLock(f)
		if err == nil {
			break
		}
		if !errors.Is(err, errWouldBlock) {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, &LockTimeoutError{Path: path, Holder: readHolder(path), Timeout: lockTimeout}
		}
		time.Sleep(lockPoll)
	}

	// Record who holds the lock so a waiter can name it. Best-effort: the lock
	// itself is what matters.
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(holderLine()), 0)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			_ = f.Truncate(0)
			_ = unlockFile(f)
			f.Close()
		})
	}, nil
}

// holderLine describes this process for the lock file: its pid and command
// line, e.g. "pid 123: skillm install foo".
func holderLine() string {
	args := append([]string{filepath.Base(os.Args[0])}, os.Args[1:]...)
	return fmt.Sprintf("pid %d: %s", os.Getpid(), strings.Join(args, " "))
}

// readHolder returns the holder line the current lock holder wrote, or "".
func readHolder(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
