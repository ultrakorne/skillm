package cmd

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

// coreOptions builds the core.Options for a command from the global flags.
// The working directory is resolved only when withCwd is set, so a command
// that never looks at it does not fail when it is gone.
func coreOptions(withCwd bool) (core.Options, error) {
	home, err := store.Home(flagHome)
	if err != nil {
		return core.Options{}, err
	}
	opts := core.Options{Home: home, Force: flagForce, Yes: flagYes}
	if withCwd {
		cwd, err := os.Getwd()
		if err != nil {
			return core.Options{}, fmt.Errorf("determine working directory: %w", err)
		}
		opts.Cwd = cwd
	}
	return opts, nil
}

// termReporter renders core Events on the terminal: an EventBatch opens a
// ui.Checklist with one row per item, an EventItemDone resolves its row, and
// an EventLog prints through the ui helpers. Call Wait once the operation has
// returned, to let the checklist settle (or, off a TTY, print its rows).
type termReporter struct {
	ctx  context.Context
	opts ui.ChecklistOptions
	cl   *ui.Checklist
}

// newTermReporter returns a termReporter whose checklists use opts and are
// torn down when ctx is cancelled.
func newTermReporter(ctx context.Context, opts ui.ChecklistOptions) *termReporter {
	return &termReporter{ctx: ctx, opts: opts}
}

// Event implements core.Reporter.
func (r *termReporter) Event(ev core.Event) {
	switch ev.Type {
	case core.EventBatch:
		r.Wait()
		r.cl = ui.NewChecklist(r.ctx, ev.Items, r.opts)
	case core.EventItemDone:
		if r.cl != nil {
			r.cl.Done(ev.Index, ui.Result{Level: uiLevel(ev.Level), Text: ev.Text})
		}
	case core.EventLog:
		printLog(ev)
	}
}

// printLog prints an EventLog through the ui helper for its level.
func printLog(ev core.Event) {
	text := ev.Text + flagAdvice[ev.Code]
	switch ev.Level {
	case core.LevelSuccess:
		ui.Successf("%s", text)
	case core.LevelWarn:
		ui.Warnf("%s", text)
	case core.LevelError:
		ui.Errorf("%s", text)
	default:
		fmt.Fprintln(os.Stdout, text)
	}
}

// flagAdvice is the CLI's suffix for the event codes a flag can resolve.
// Core's Text never names a flag; the terminal adds which one to pass.
var flagAdvice = map[string]string{
	core.CodeLinkRefused:    " (pass --force to take it over)",
	core.CodeInstallBlocked: " (pass --force)",
}

// dropCodes forwards every Event to rep except those whose Code is listed.
type dropCodes struct {
	rep   core.Reporter
	codes []string
}

// Event implements core.Reporter.
func (d dropCodes) Event(ev core.Event) {
	if !slices.Contains(d.codes, ev.Code) {
		d.rep.Event(ev)
	}
}

// termLog prints the EventLogs of the core primitives cmd calls directly
// (VendorOne, UpsertLockEntry, …), which report nothing else. Each line goes
// out as soon as it is reported, exactly as the ui call it replaced did.
var termLog core.Reporter = logReporter{}

// logReporter is termLog's type: it prints EventLogs and drops the rest.
type logReporter struct{}

// Event implements core.Reporter.
func (logReporter) Event(ev core.Event) {
	if ev.Type == core.EventLog {
		printLog(ev)
	}
}

// Wait ends the open checklist, if any.
func (r *termReporter) Wait() {
	if r.cl != nil {
		r.cl.Wait()
		r.cl = nil
	}
}

// uiLevel maps a core Level to the checklist's row severity.
func uiLevel(l core.Level) ui.Level {
	switch l {
	case core.LevelWarn:
		return ui.LevelWarn
	case core.LevelError:
		return ui.LevelError
	default:
		return ui.LevelSuccess
	}
}

// runChecklist runs work for every label through core.FanOut and renders one
// checklist row per label (with a progress bar when bar is set), returning
// the Results in label order. It serves update, whose per-skill loop still
// lives in cmd, until that loop moves into core. Quitting the live view
// cancels the context work runs under.
func runChecklist(ctx context.Context, labels []string, bar bool, work func(ctx context.Context, i int) ui.Result) []ui.Result {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cl := ui.NewChecklist(ctx, labels, ui.ChecklistOptions{Bar: bar, OnAbort: cancel})
	core.FanOut(wctx, len(labels), func(i int) {
		cl.Done(i, work(wctx, i))
	})
	return cl.Wait()
}
