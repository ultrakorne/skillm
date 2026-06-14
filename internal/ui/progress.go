package ui

import (
	"os"

	"charm.land/bubbles/v2/progress"
	tea "charm.land/bubbletea/v2"
)

// progressThreshold is the smallest total that warrants a live progress bar.
// Below this, or off a TTY, work runs plainly with no animation (PLAN §3
// update: "shows a bar when there is enough work to warrant it").
const progressThreshold = 2

// RunProgress executes work, optionally rendering a bubbles/progress bar.
//
// work is given a report callback it invokes with the cumulative number of
// completed units (0..total); the final value need not equal total. work runs
// on a background goroutine while the bar animates; its error (if any) is
// returned after the UI tears down.
//
// The bar is shown only when stdout is a TTY and total >= progressThreshold.
// Otherwise work runs synchronously on the caller's goroutine with a no-op
// reporter, so non-interactive callers get identical side effects without any
// terminal output.
func RunProgress(total int, work func(report func(done int)) error) error {
	if work == nil {
		return nil
	}
	if !IsTTY() || total < progressThreshold {
		return work(func(int) {})
	}
	return runProgressBar(total, work)
}

// progressMsg carries a cumulative completed count from work into the model.
type progressMsg int

// workDoneMsg signals that work has returned; its error is stored for the
// caller to read after the program exits.
type workDoneMsg struct{ err error }

type progressModel struct {
	bar   progress.Model
	total int
	msgs  chan tea.Msg
	err   error
}

func newProgressModel(total int, msgs chan tea.Msg) progressModel {
	return progressModel{
		bar:   progress.New(progress.WithDefaultBlend(), progress.WithWidth(40)),
		total: total,
		msgs:  msgs,
	}
}

func (m progressModel) Init() tea.Cmd {
	return waitForMsg(m.msgs)
}

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case progressMsg:
		var pct float64
		if m.total > 0 {
			pct = float64(msg) / float64(m.total)
		}
		if pct > 1 {
			pct = 1
		}
		cmd := m.bar.SetPercent(pct) // pointer receiver; m.bar is addressable here
		return m, tea.Batch(cmd, waitForMsg(m.msgs))

	case workDoneMsg:
		m.err = msg.err
		cmd := m.bar.SetPercent(1)
		return m, tea.Sequence(cmd, tea.Quit)

	case progress.FrameMsg:
		newBar, cmd := m.bar.Update(msg)
		m.bar = newBar
		return m, cmd

	default:
		return m, nil
	}
}

func (m progressModel) View() tea.View {
	return tea.NewView("  " + m.bar.View() + "\n")
}

// waitForMsg blocks on the work channel for the next message to feed the
// program. The channel is closed by RunProgress once work finishes, after the
// final workDoneMsg has been delivered.
func waitForMsg(msgs chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-msgs
		if !ok {
			return nil
		}
		return msg
	}
}

func runProgressBar(total int, work func(report func(done int)) error) error {
	msgs := make(chan tea.Msg)
	model := newProgressModel(total, msgs)

	prog := tea.NewProgram(model, tea.WithOutput(os.Stderr))

	go func() {
		report := func(done int) {
			msgs <- progressMsg(done)
		}
		err := work(report)
		msgs <- workDoneMsg{err: err}
		close(msgs)
	}()

	finalModel, runErr := prog.Run()
	if runErr != nil {
		return runErr
	}
	if pm, ok := finalModel.(progressModel); ok {
		return pm.err
	}
	return nil
}
