package ui

import (
	"testing"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

// newTestChecklistProgress builds a model with n rows for driving Update
// directly. showBar mirrors the runner's gate (n >= progressThreshold).
func newTestChecklistProgress(n int) checklistProgressModel {
	labels := make([]string, n)
	for i := range labels {
		labels[i] = "skill"
	}
	return checklistProgressModel{
		spinner:   spinner.New(),
		bar:       progress.New(progress.WithWidth(40)),
		showBar:   n >= progressThreshold,
		labels:    labels,
		results:   make([]Result, n),
		done:      make([]bool, n),
		remaining: n,
		msgs:      make(chan tea.Msg, n),
	}
}

func ok(i int) checkDoneMsg {
	return checkDoneMsg{index: i, res: Result{Level: LevelSuccess, Text: "done"}}
}

// TestChecklistProgressFillsBarBeforeQuitting guards the reported bug: when the
// last row finished, the model quit immediately and the bar stopped short of
// 100%. The fix animates the bar to full and only quits once it has settled.
func TestChecklistProgressFillsBarBeforeQuitting(t *testing.T) {
	m := newTestChecklistProgress(2)

	// First of two rows: still waiting, not finishing.
	next, _ := m.Update(ok(0))
	m = next.(checklistProgressModel)
	if m.finishing {
		t.Fatal("entered finishing after only one of two rows completed")
	}

	// Last row: enter the finishing phase with a bar-animation command, not a
	// quit. Driving that command's frames must take the bar to 100% and only
	// then quit — the final IsAnimating check below catches a premature quit.
	next, cmd := m.Update(ok(1))
	m = next.(checklistProgressModel)
	if !m.finishing {
		t.Fatal("did not enter finishing after the last row completed")
	}
	if cmd == nil {
		t.Fatal("expected a bar-animation command after the last row, got nil")
	}

	quit := false
	for range 2000 {
		msg := cmd()
		if _, isQuit := msg.(tea.QuitMsg); isQuit {
			quit = true
			break
		}
		next, cmd = m.Update(msg)
		m = next.(checklistProgressModel)
		if cmd == nil {
			t.Fatal("animation produced no follow-up command before settling")
		}
	}
	if !quit {
		t.Fatal("model never quit; bar animation did not settle")
	}
	if m.bar.IsAnimating() {
		t.Fatal("model quit while the bar was still animating")
	}
}

// TestChecklistProgressNoBarQuitsImmediately verifies the single-row case (no
// bar to settle) quits as soon as its one row resolves.
func TestChecklistProgressNoBarQuitsImmediately(t *testing.T) {
	m := newTestChecklistProgress(1)
	if m.showBar {
		t.Fatal("a single row should not show a bar")
	}
	_, cmd := m.Update(ok(0))
	if cmd == nil {
		t.Fatal("expected a quit command after the only row, got nil")
	}
	if _, isQuit := cmd().(tea.QuitMsg); !isQuit {
		t.Fatal("single-row model did not quit immediately on completion")
	}
}
