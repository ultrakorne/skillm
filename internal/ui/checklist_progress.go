package ui

import (
	"context"
	"os"
	"strings"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// RunChecklistProgress runs check(ctx, i) for every label concurrently (bounded
// by maxConcurrentChecks) and renders one spinner row per label — exactly like
// RunChecks — with an aggregate progress bar underneath that fills as rows
// resolve. It returns the per-row Results in input order.
//
// It is the right fit for commands that, unlike read-only check, do per-skill
// work worth a completion gauge (e.g. update's clone-and-replace): the user
// sees every skill spinning at once and a single bar tracking how many are
// done.
//
// On a TTY each row starts as a live spinner and resolves in place to a colored
// glyph and result text as its check finishes; the bar advances with each
// resolution. The bar is shown only when there are at least progressThreshold
// labels — a single row needs no gauge. Off a TTY the checks still run
// concurrently but render as plain ordered lines once all complete, keeping
// piped output deterministic. ctx cancellation tears down the work and the UI.
func RunChecklistProgress(ctx context.Context, labels []string, check func(ctx context.Context, i int) Result) []Result {
	results := make([]Result, len(labels))
	if len(labels) == 0 {
		return results
	}
	if !IsTTY() {
		fanOut(ctx, len(labels), func(i int) { results[i] = check(ctx, i) })
		for _, r := range results {
			printResult(r)
		}
		return results
	}
	return runChecklistProgressTUI(ctx, labels, check)
}

type checklistProgressModel struct {
	spinner   spinner.Model
	bar       progress.Model
	showBar   bool
	finishing bool // all rows done; animating the bar to 100% before quitting
	labels    []string
	results   []Result
	done      []bool
	completed int
	remaining int
	msgs      chan tea.Msg
}

func (m checklistProgressModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, waitForMsg(m.msgs))
}

func (m checklistProgressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case checkDoneMsg:
		m.results[msg.index] = msg.res
		if !m.done[msg.index] {
			m.done[msg.index] = true
			m.remaining--
			m.completed++
		}

		// Not the last row: nudge the bar toward the new ratio and keep listening.
		if m.remaining > 0 {
			var cmds []tea.Cmd
			if m.showBar {
				pct := float64(m.completed) / float64(len(m.labels))
				cmds = append(cmds, m.bar.SetPercent(pct)) // pointer receiver; m.bar is addressable here
			}
			cmds = append(cmds, waitForMsg(m.msgs))
			return m, tea.Batch(cmds...)
		}

		// Last row done. With no bar there is nothing to settle, so quit now.
		// Otherwise drive the bar to 100% and let the FrameMsg handler quit once
		// the animation has fully landed, so the user sees it reach the end.
		if !m.showBar {
			return m, tea.Quit
		}
		m.finishing = true
		return m, m.bar.SetPercent(1)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case progress.FrameMsg:
		newBar, cmd := m.bar.Update(msg)
		m.bar = newBar
		if m.finishing && !m.bar.IsAnimating() {
			return m, tea.Quit
		}
		return m, cmd

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		}
		return m, nil

	default:
		return m, nil
	}
}

func (m checklistProgressModel) View() tea.View {
	var b strings.Builder
	for i, label := range m.labels {
		b.WriteString("  ")
		if m.done[i] {
			b.WriteString(glyphFor(m.results[i].Level))
			b.WriteByte(' ')
			b.WriteString(m.results[i].Text)
		} else {
			b.WriteString(m.spinner.View())
			b.WriteByte(' ')
			b.WriteString(label)
		}
		b.WriteByte('\n')
	}
	if m.showBar {
		b.WriteString("  " + m.bar.View() + "\n")
	}
	return tea.NewView(b.String())
}

func runChecklistProgressTUI(ctx context.Context, labels []string, check func(ctx context.Context, i int) Result) []Result {
	n := len(labels)
	msgs := make(chan tea.Msg)
	model := checklistProgressModel{
		spinner:   spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("6")))),
		bar:       progress.New(progress.WithDefaultBlend(), progress.WithWidth(40)),
		showBar:   n >= progressThreshold,
		labels:    labels,
		results:   make([]Result, n),
		done:      make([]bool, n),
		remaining: n,
		msgs:      msgs,
	}

	// Workers run under a child context so tearing down the program (a finished
	// run, a ctrl+c keypress, or parent cancellation) stops them and unblocks any
	// goroutine parked on a send.
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()

	spawnCheckers(wctx, n, check, msgs)

	prog := tea.NewProgram(model, tea.WithOutput(os.Stderr), tea.WithContext(ctx))
	finalModel, err := prog.Run()
	cancel() // stop any worker still parked on a send before we return
	if err == nil {
		if fm, ok := finalModel.(checklistProgressModel); ok {
			return fm.results
		}
	}
	return model.results
}
