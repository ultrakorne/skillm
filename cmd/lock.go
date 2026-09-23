package cmd

import (
	"context"

	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

// lockHome takes Home's cross-process lock for op (e.g. "skillm install"),
// telling the user when it has to wait for another skillm process. Callers
// defer the returned unlock.
func lockHome(ctx context.Context, home, op string) (unlock func(), err error) {
	return store.Lock(ctx, home, op, func(holder string) {
		who := "another skillm operation"
		if holder != "" {
			who += " (" + holder + ")"
		}
		ui.Warnf("waiting for %s to finish…", who)
	})
}
