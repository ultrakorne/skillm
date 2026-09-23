package core

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestFanOutRunsEveryIndexConcurrently verifies FanOut invokes do exactly once
// per index, overlaps work (it is not serial), and never exceeds the cap.
func TestFanOutRunsEveryIndexConcurrently(t *testing.T) {
	const n = 20
	var mu sync.Mutex
	calls := make([]int, n)
	var inFlight, maxInFlight int32

	FanOut(context.Background(), n, func(i int) {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			old := atomic.LoadInt32(&maxInFlight)
			if cur <= old || atomic.CompareAndSwapInt32(&maxInFlight, old, cur) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond) // let peers overlap so concurrency is observable
		mu.Lock()
		calls[i]++
		mu.Unlock()
		atomic.AddInt32(&inFlight, -1)
	})

	for i, c := range calls {
		if c != 1 {
			t.Fatalf("index %d called %d times, want exactly 1", i, c)
		}
	}
	if maxInFlight < 2 {
		t.Fatalf("FanOut ran serially (max in-flight = %d); expected concurrency", maxInFlight)
	}
	if maxInFlight > MaxConcurrency {
		t.Fatalf("FanOut exceeded cap: max in-flight = %d > %d", maxInFlight, MaxConcurrency)
	}
}

// TestFanOutSkipsUnstartedAfterCancel verifies that once ctx is cancelled no
// new call starts.
func TestFanOutSkipsUnstartedAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls int32
	FanOut(ctx, 3*MaxConcurrency, func(int) { atomic.AddInt32(&calls, 1) })
	if calls != 0 {
		t.Fatalf("FanOut started %d calls after cancel, want 0", calls)
	}
}
