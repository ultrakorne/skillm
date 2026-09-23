package ui

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

// silenceStdio redirects stdout and stderr to /dev/null for the duration of a
// test so the checklist's plain-path printing does not pollute test output.
// stdout stays a non-terminal, so IsTTY() remains false and the plain path runs.
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

// TestChecklistPlainPreservesOrder verifies that rows reported concurrently
// and out of order come back from Wait aligned to label order.
func TestChecklistPlainPreservesOrder(t *testing.T) {
	silenceStdio(t)

	labels := []string{"a", "b", "c", "d", "e"}
	cl := NewChecklist(context.Background(), labels, ChecklistOptions{})
	var wg sync.WaitGroup
	for i := range labels {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Finish later indices first to scramble completion order.
			time.Sleep(time.Duration(len(labels)-i) * time.Millisecond)
			cl.Done(i, Result{Level: LevelSuccess, Text: labels[i]})
		}(i)
	}
	wg.Wait()
	got := cl.Wait()

	if len(got) != len(labels) {
		t.Fatalf("got %d results, want %d", len(got), len(labels))
	}
	for i, r := range got {
		if r.Text != labels[i] {
			t.Fatalf("result[%d].Text = %q, want %q (order not preserved)", i, r.Text, labels[i])
		}
	}
}

// newTestChecklistModel builds a model with n rows for driving Update
// directly. showBar mirrors NewChecklist's gate (n >= progressThreshold).
func newTestChecklistModel(n int) checklistModel {
	labels := make([]string, n)
	for i := range labels {
		labels[i] = "skill"
	}
	return checklistModel{
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

// TestChecklistFillsBarBeforeQuitting guards the reported bug: when the last
// row finished, the model quit immediately and the bar stopped short of 100%.
// The fix animates the bar to full and only quits once it has settled.
func TestChecklistFillsBarBeforeQuitting(t *testing.T) {
	m := newTestChecklistModel(2)

	// First of two rows: still waiting, not finishing.
	next, _ := m.Update(ok(0))
	m = next.(checklistModel)
	if m.finishing {
		t.Fatal("entered finishing after only one of two rows completed")
	}

	// Last row: enter the finishing phase with a bar-animation command, not a
	// quit. Driving that command's frames must take the bar to 100% and only
	// then quit — the final IsAnimating check below catches a premature quit.
	next, cmd := m.Update(ok(1))
	m = next.(checklistModel)
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
		m = next.(checklistModel)
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

// TestChecklistNoBarQuitsImmediately verifies the single-row case (no bar to
// settle) quits as soon as its one row resolves.
func TestChecklistNoBarQuitsImmediately(t *testing.T) {
	m := newTestChecklistModel(1)
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

// TestChecklistEndQuitsWithRowsPending verifies the end of the work stops the
// live view even when some rows were never reported (e.g. cancelled work), so
// Wait cannot hang.
func TestChecklistEndQuitsWithRowsPending(t *testing.T) {
	m := newTestChecklistModel(2)
	next, _ := m.Update(ok(0))
	_, cmd := next.(checklistModel).Update(checklistEndMsg{})
	if cmd == nil {
		t.Fatal("expected a quit command at the end of the work, got nil")
	}
	if _, isQuit := cmd().(tea.QuitMsg); !isQuit {
		t.Fatal("model did not quit at the end of the work")
	}
}

// TestChecklistAbortOnlyWithRowsPending verifies a quit key marks the view
// aborted (so the caller cancels its work) only while rows are still pending.
func TestChecklistAbortOnlyWithRowsPending(t *testing.T) {
	quitKey := tea.KeyPressMsg{Code: 'q', Text: "q"}

	next, _ := newTestChecklistModel(2).Update(quitKey)
	if !next.(checklistModel).aborted {
		t.Fatal("quitting with rows pending did not mark the view aborted")
	}

	m := newTestChecklistModel(2)
	next, _ = m.Update(ok(0))
	next, _ = next.(checklistModel).Update(ok(1))
	next, _ = next.(checklistModel).Update(quitKey)
	if next.(checklistModel).aborted {
		t.Fatal("quitting after every row resolved marked the view aborted")
	}
}
