package ui

import (
	"context"
	"os"
	"strings"
	"sync"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// maxConcurrentChecks bounds how many check functions run at once. Checks are
// network-bound (a treeless git fetch each), so a modest fan-out shrinks the
// wall-clock time to roughly the slowest single check without opening an
// unbounded number of connections to the remote.
const maxConcurrentChecks = 8

// Level classifies a finished check so RunChecks can pick the matching glyph and
// color. It mirrors the Successf/Warnf/Errorf severity convention.
type Level int

const (
	LevelSuccess Level = iota
	LevelWarn
	LevelError
)

// Result is the outcome of one check: a severity Level plus the line of text to
// show after the status glyph.
type Result struct {
	Level Level
	Text  string
}

// RunChecks runs check(ctx, i) for every label concurrently (bounded by
// maxConcurrentChecks) and displays one row per label, returning the per-row
// Results in input order.
//
// On a TTY each row starts as a live spinner next to its label and resolves in
// place to a colored glyph and the result text, so the user sees motion
// immediately and the rows settle as their checks finish. Off a TTY the checks
// still run concurrently, but render as plain ordered lines once all complete,
// keeping piped output deterministic. ctx cancellation tears down the work and
// the UI.
func RunChecks(ctx context.Context, labels []string, check func(ctx context.Context, i int) Result) []Result {
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
	return runChecksTUI(ctx, labels, check)
}

// fanOut runs do(i) for i in [0,n) concurrently, capped at maxConcurrentChecks
// in flight, and returns once every call has finished.
func fanOut(ctx context.Context, n int, do func(i int)) {
	sem := make(chan struct{}, maxConcurrentChecks)
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
			do(i)
		}(i)
	}
	wg.Wait()
}

// spawnCheckers launches the bounded fan-out of check(ctx, i) on a background
// goroutine, forwarding each finished row to msgs as a checkDoneMsg. A send is
// abandoned if ctx is cancelled first, so the goroutine never blocks after the
// program tears down. Both the spinner-only and the spinner+bar runners share
// this so their worker plumbing stays identical.
func spawnCheckers(ctx context.Context, n int, check func(ctx context.Context, i int) Result, msgs chan tea.Msg) {
	go func() {
		fanOut(ctx, n, func(i int) {
			res := check(ctx, i)
			select {
			case msgs <- checkDoneMsg{index: i, res: res}:
			case <-ctx.Done():
			}
		})
	}()
}

// printResult emits a finished check as a plain line, routing by severity the
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

// checkDoneMsg reports that the check for one row has finished.
type checkDoneMsg struct {
	index int
	res   Result
}

type checksModel struct {
	spinner   spinner.Model
	labels    []string
	results   []Result
	done      []bool
	remaining int
	msgs      chan tea.Msg
}

func (m checksModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, waitForMsg(m.msgs))
}

func (m checksModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case checkDoneMsg:
		m.results[msg.index] = msg.res
		if !m.done[msg.index] {
			m.done[msg.index] = true
			m.remaining--
		}
		if m.remaining <= 0 {
			return m, tea.Quit
		}
		return m, waitForMsg(m.msgs)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
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

func (m checksModel) View() tea.View {
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

func runChecksTUI(ctx context.Context, labels []string, check func(ctx context.Context, i int) Result) []Result {
	n := len(labels)
	msgs := make(chan tea.Msg)
	model := checksModel{
		spinner:   spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("6")))),
		labels:    labels,
		results:   make([]Result, n),
		done:      make([]bool, n),
		remaining: n,
		msgs:      msgs,
	}

	// Workers run under a child context so tearing down the program (a finished
	// run, a ctrl+c keypress, or parent cancellation) stops them and unblocks any
	// goroutine parked on the send below.
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()

	spawnCheckers(wctx, n, check, msgs)

	prog := tea.NewProgram(model, tea.WithOutput(os.Stderr), tea.WithContext(ctx))
	finalModel, err := prog.Run()
	cancel() // stop any worker still parked on a send before we return
	if err == nil {
		if fm, ok := finalModel.(checksModel); ok {
			return fm.results
		}
	}
	return model.results
}
