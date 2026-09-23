package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortLockTimeout shrinks Lock's wait for the duration of a test.
func shortLockTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	orig := lockTimeout
	lockTimeout = d
	t.Cleanup(func() { lockTimeout = orig })
}

func TestLock_CreatesHomeAndLockFile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "nested", ".skillm")
	unlock, err := Lock(home)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer unlock()
	if _, err := os.Stat(filepath.Join(home, LockFileName)); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}
}

// A second locker waits while the first holds the lock, then gets it once the
// first releases.
func TestLock_SecondWaitsThenAcquires(t *testing.T) {
	shortLockTimeout(t, 5*time.Second)
	home := t.TempDir()

	unlock1, err := Lock(home)
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}

	const hold = 300 * time.Millisecond
	type result struct {
		unlock func()
		err    error
		waited time.Duration
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		u, err := Lock(home)
		done <- result{u, err, time.Since(start)}
	}()

	select {
	case r := <-done:
		t.Fatalf("second Lock returned while the first was held (err = %v)", r.err)
	case <-time.After(hold):
	}
	unlock1()

	r := <-done
	if r.err != nil {
		t.Fatalf("second Lock after release: %v", r.err)
	}
	defer r.unlock()
	if r.waited < hold {
		t.Errorf("second Lock waited %v, want at least %v", r.waited, hold)
	}
}

// A locker that never gets the lock gives up after the timeout with an error
// naming the holder.
func TestLock_TimesOutNamingHolder(t *testing.T) {
	shortLockTimeout(t, 200*time.Millisecond)
	home := t.TempDir()

	unlock, err := Lock(home)
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	defer unlock()

	start := time.Now()
	_, err = Lock(home)
	var lerr *LockTimeoutError
	if !errors.As(err, &lerr) {
		t.Fatalf("err = %v, want *LockTimeoutError", err)
	}
	if waited := time.Since(start); waited < 200*time.Millisecond {
		t.Errorf("gave up after %v, before the 200ms timeout", waited)
	}
	if want := fmt.Sprintf("pid %d:", os.Getpid()); !strings.HasPrefix(lerr.Holder, want) {
		t.Errorf("Holder = %q, want it to start with %q", lerr.Holder, want)
	}
	if !strings.Contains(err.Error(), lerr.Holder) {
		t.Errorf("error %q does not name the holder %q", err, lerr.Holder)
	}
}

// unlock is idempotent, and the lock is free again afterwards.
func TestLock_UnlockTwiceThenRelock(t *testing.T) {
	shortLockTimeout(t, 200*time.Millisecond)
	home := t.TempDir()

	unlock, err := Lock(home)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	unlock()
	unlock()

	unlock2, err := Lock(home)
	if err != nil {
		t.Fatalf("Lock after unlock: %v", err)
	}
	unlock2()
}
