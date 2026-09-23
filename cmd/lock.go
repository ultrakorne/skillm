package cmd

import (
	"context"
	"fmt"

	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/protocol"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

// lockHome takes Home's cross-process lock for op (e.g. "skillm install"),
// telling the user when it has to wait for another skillm process: a warning
// on the terminal, or an info event (code "lock_wait") in JSON mode, where
// stdout carries only the protocol. Callers defer the returned unlock.
func lockHome(ctx context.Context, home, op string) (unlock func(), err error) {
	return store.Lock(ctx, home, op, func(holder string) {
		who := "another skillm operation"
		if holder != "" {
			who += " (" + holder + ")"
		}
		text := fmt.Sprintf("waiting for %s to finish…", who)
		if flagJSON {
			jsonOut().Event(core.Event{Type: core.EventLog, Level: core.LevelInfo, Code: protocol.CodeLockWait, Text: text})
			return
		}
		ui.Warnf("%s", text)
	})
}
