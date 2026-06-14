package ui

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// silenceStdio redirects stdout and stderr to /dev/null for the duration of a
// test so RunChecks' plain-path printing does not pollute test output. stdout
// stays a non-terminal, so IsTTY() remains false and the plain path runs.
func silenceStdio(t *testing.T) {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	t.Cleanup(func() {
		os.Stdout, os.Stderr = oldOut, oldErr
		_ = devnull.Close()
	})
}

// TestFanOutRunsEveryIndexConcurrently verifies fanOut invokes do exactly once
// per index, overlaps work (it is not serial), and never exceeds the cap.
func TestFanOutRunsEveryIndexConcurrently(t *testing.T) {
	const n = 20
	var mu sync.Mutex
	calls := make([]int, n)
	var inFlight, maxInFlight int32

	fanOut(context.Background(), n, func(i int) {
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
		t.Fatalf("fanOut ran serially (max in-flight = %d); expected concurrency", maxInFlight)
	}
	if maxInFlight > maxConcurrentChecks {
		t.Fatalf("fanOut exceeded cap: max in-flight = %d > %d", maxInFlight, maxConcurrentChecks)
	}
}

// TestRunChecksPreservesOrder verifies that, although checks run concurrently
// and finish out of order, RunChecks returns results aligned to input order.
func TestRunChecksPreservesOrder(t *testing.T) {
	silenceStdio(t)

	labels := []string{"a", "b", "c", "d", "e"}
	got := RunChecks(context.Background(), labels, func(_ context.Context, i int) Result {
		// Finish later indices first to scramble completion order.
		time.Sleep(time.Duration(len(labels)-i) * time.Millisecond)
		return Result{Level: LevelSuccess, Text: labels[i]}
	})

	if len(got) != len(labels) {
		t.Fatalf("got %d results, want %d", len(got), len(labels))
	}
	for i, r := range got {
		if r.Text != labels[i] {
			t.Fatalf("result[%d].Text = %q, want %q (order not preserved)", i, r.Text, labels[i])
		}
	}
}
