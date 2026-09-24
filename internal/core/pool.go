package core

import (
	"context"
	"sync"
)

// MaxConcurrency bounds how many items FanOut works on at once. The work is
// mostly network-bound (a treeless git fetch per skill), so a modest fan-out
// brings the wall-clock time down to about the slowest single item without
// opening an unbounded number of connections to one remote.
const MaxConcurrency = 8

// FanOut runs do(i) for every i in [0,n) concurrently, at most MaxConcurrency
// at a time, and returns once every started call has finished. Once ctx is
// cancelled, calls that have not started yet are skipped.
func FanOut(ctx context.Context, n int, do func(i int)) {
	sem := make(chan struct{}, MaxConcurrency)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			// A free slot and a cancelled ctx can be ready together, and select
			// picks either; check again so no call starts after cancellation.
			if ctx.Err() != nil {
				return
			}
			do(i)
		}(i)
	}
	wg.Wait()
}
