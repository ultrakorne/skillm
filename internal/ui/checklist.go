package ui

import (
	"context"
	"os"
	"strings"
	"sync"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Level classifies a finished row so the checklist can pick the matching
// glyph and color. It mirrors the Successf/Warnf/Errorf severity convention.
type Level int

const (
	LevelSuccess Level = iota
	LevelWarn
	LevelError
)

// Result is the outcome of one row: a severity Level plus the line of text to
// show after the status glyph.
type Result struct {
	Level Level
	Text  string
}

// ChecklistOptions tunes a Checklist.
type ChecklistOptions struct {
	// Bar adds an aggregate progress bar under the rows that fills as they
	// resolve, shown only when there are at least progressThreshold rows (a
	// single row needs no gauge).
	Bar bool
	// OnAbort is called when the user quits the live view (q, esc, ctrl+c)
	// before every row resolved, so the caller can cancel the work behind it.
	OnAbort func()
}

// Checklist renders one row per label for work the caller runs: the caller
// reports each finished row with Done and calls Wait once the work is over.
// It does no work itself.
//
// On a TTY every row starts as a live spinner next to its label and resolves
// in place to a colored glyph and the result text when Done reports it. Off a
// TTY nothing is drawn while the work runs; Wait prints the resolved rows as
// plain lines in label order, keeping piped output deterministic.
type Checklist struct {
	labels []string

	// Plain mode.
	mu       sync.Mutex
	results  []Result
	resolved []bool

	// TTY mode: msgs feeds the program, exited closes when it has quit.
	tty    bool
	msgs   chan tea.Msg
	exited chan struct{}
	final  []Result
}

// NewChecklist starts a checklist for labels. ctx cancellation tears the live
// view down.
func NewChecklist(ctx context.Context, labels []string, opts ChecklistOptions) *Checklist {
	c := &Checklist{
		labels:   labels,
		results:  make([]Result, len(labels)),
		resolved: make([]bool, len(labels)),
	}
	if len(labels) == 0 || !IsTTY() {
		return c
	}

	c.tty = true
	c.msgs = make(chan tea.Msg)
	c.exited = make(chan struct{})
	n := len(labels)
	model := checklistModel{
		spinner:   spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("6")))),
		showBar:   opts.Bar && n >= progressThreshold,
		labels:    labels,
		results:   make([]Result, n),
		done:      make([]bool, n),
		remaining: n,
		msgs:      c.msgs,
	}
	if model.showBar {
		model.bar = progress.New(progress.WithDefaultBlend(), progress.WithWidth(40))
	}
	prog := tea.NewProgram(model, tea.WithOutput(os.Stderr), tea.WithContext(ctx))
	go func() {
		defer close(c.exited)
		finalModel, err := prog.Run()
		fm, ok := finalModel.(checklistModel)
		if shouldAbort(fm, ok, err != nil) && opts.OnAbort != nil {
			opts.OnAbort()
		}
		if err != nil || !ok {
			return
		}
		c.final = fm.results
	}()
	return c
}

// shouldAbort reports whether the caller's outstanding work should be
// cancelled given how the live view ended: the user aborted it (q/esc/
// ctrl+c) before every row resolved, or the renderer itself quit early
// (an error, or a final model of the wrong type) without ever getting the
// chance to report an abort. Either way, rows may still be unresolved, so
// the caller must stop waiting on them rather than run them to completion
// unobserved.
func shouldAbort(fm checklistModel, ok, hadErr bool) bool {
	return hadErr || !ok || fm.aborted
}

// Done reports that row i finished with r. It is safe to call from several
// goroutines, and returns immediately once the live view has quit.
func (c *Checklist) Done(i int, r Result) {
	if !c.tty {
		c.mu.Lock()
		c.results[i], c.resolved[i] = r, true
		c.mu.Unlock()
		return
	}
	select {
	case c.msgs <- checkDoneMsg{index: i, res: r}:
	case <-c.exited:
	}
}

// Wait ends the checklist after the work is over and returns the per-row
// Results in label order; a row Done never reported is the zero Result. On a
// TTY it waits for the live view to settle (a row never reported stops it
// early); off a TTY it prints the resolved rows.
func (c *Checklist) Wait() []Result {
	if !c.tty {
		c.mu.Lock()
		defer c.mu.Unlock()
		for i, r := range c.results {
			if c.resolved[i] {
				printResult(r)
			}
		}
		return c.results
	}
	select {
	case c.msgs <- checklistEndMsg{}:
	case <-c.exited:
	}
	<-c.exited
	if c.final == nil {
		return make([]Result, len(c.labels))
	}
	return c.final
}

// printResult emits a finished row as a plain line, routing by severity the
// same way the Successf/Warnf/Errorf helpers do.
func printResult(r Result) {
	switch r.Level {
	case LevelError:
		Errorf("%s", r.Text)
	case LevelWarn:
		Warnf("%s", r.Text)
	default:
		Successf("%s", r.Text)
	}
}

// checkDoneMsg reports that one row has finished.
type checkDoneMsg struct {
	index int
	res   Result
}

// checklistEndMsg reports that the work is over, whether or not every row
// was reported.
type checklistEndMsg struct{}

type checklistModel struct {
	spinner   spinner.Model
	bar       progress.Model
	showBar   bool
	finishing bool // all rows done; animating the bar to 100% before quitting
	aborted   bool // the user quit before every row resolved
	labels    []string
	results   []Result
	done      []bool
	completed int
	remaining int
	msgs      chan tea.Msg
}

func (m checklistModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, waitForMsg(m.msgs))
}

func (m checklistModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

	case checklistEndMsg:
		// The work ended with rows unreported (e.g. it was cancelled): there
		// is nothing left to wait for.
		return m, tea.Quit

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
			m.aborted = m.remaining > 0
			return m, tea.Quit
		}
		return m, nil

	default:
		return m, nil
	}
}

func (m checklistModel) View() tea.View {
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

// glyphFor renders the leading status glyph for a finished row, reusing the
// styles shared with the Successf/Warnf/Errorf print helpers.
func glyphFor(l Level) string {
	switch l {
	case LevelError:
		return styleError.Render("✗")
	case LevelWarn:
		return styleWarn.Render("!")
	default:
		return styleSuccess.Render("✓")
	}
}
