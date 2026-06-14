package ui

import (
	"errors"
	"strings"
	"testing"
)

// The test binary's stdout is not a terminal, so IsTTY() is false here and we
// exercise every auto-degraded (non-interactive) code path.

func TestIsTTYFalseUnderTest(t *testing.T) {
	if IsTTY() {
		t.Fatal("IsTTY() = true under `go test`; expected non-terminal stdout")
	}
}

func TestRenderSkillTableEmpty(t *testing.T) {
	got := RenderSkillTable(nil)
	if !strings.Contains(got, "No skills") {
		t.Fatalf("empty table = %q, want a 'No skills' notice", got)
	}
}

func TestRenderSkillTablePlain(t *testing.T) {
	rows := []Row{
		{ID: "alpha", Source: "https://example.com/repo", Linked: "global: claude", Status: "up-to-date"},
		{ID: "beta", Source: "/tmp/local", Linked: "-", Status: "local"},
	}
	got := RenderSkillTable(rows)

	// Plain (non-TTY) output: header line + tab-separated cells, no ANSI escapes.
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("non-TTY table contains ANSI escapes: %q", got)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (header + 2 rows): %q", len(lines), got)
	}
	header := strings.Split(lines[0], "\t")
	want := []string{"ID", "Source", "Linked", "Status"}
	if len(header) != len(want) {
		t.Fatalf("header columns = %v, want %v", header, want)
	}
	for i := range want {
		if header[i] != want[i] {
			t.Fatalf("header[%d] = %q, want %q", i, header[i], want[i])
		}
	}
	if !strings.Contains(lines[1], "alpha") || !strings.Contains(lines[1], "up-to-date") {
		t.Fatalf("row 1 missing expected cells: %q", lines[1])
	}
	if !strings.Contains(lines[2], "beta") || !strings.Contains(lines[2], "local") {
		t.Fatalf("row 2 missing expected cells: %q", lines[2])
	}
}

func TestSelectSkillsNonTTY(t *testing.T) {
	got, err := SelectSkills("Pick skills", []Option{{Label: "a", Value: "a"}})
	if err == nil {
		t.Fatal("SelectSkills on non-TTY: want error, got nil")
	}
	if got != nil {
		t.Fatalf("SelectSkills on non-TTY: want nil selection, got %v", got)
	}
	if !strings.Contains(err.Error(), "--all") {
		t.Fatalf("SelectSkills error %q should name the --all escape hatch", err)
	}
}

func TestSelectAgentsNonTTY(t *testing.T) {
	_, err := SelectAgents([]string{"claude", "codex"}, []string{"claude"})
	if err == nil {
		t.Fatal("SelectAgents on non-TTY: want error, got nil")
	}
	if !strings.Contains(err.Error(), "non-interactive") {
		t.Fatalf("SelectAgents error %q should mention non-interactive", err)
	}
}

func TestConfirmNonTTY(t *testing.T) {
	ok, err := Confirm("Proceed?")
	if err == nil {
		t.Fatal("Confirm on non-TTY: want error, got nil")
	}
	if ok {
		t.Fatal("Confirm on non-TTY: want false, got true")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("Confirm error %q should name the --yes flag", err)
	}
}

func TestRunProgressPlainExecutesWork(t *testing.T) {
	var reported []int
	called := false
	err := RunProgress(5, func(report func(done int)) error {
		called = true
		for i := 1; i <= 5; i++ {
			report(i)
		}
		reported = append(reported, 5)
		return nil
	})
	if err != nil {
		t.Fatalf("RunProgress returned error: %v", err)
	}
	if !called {
		t.Fatal("RunProgress did not invoke work")
	}
	if len(reported) != 1 {
		t.Fatalf("work side effects not observed: %v", reported)
	}
}

func TestRunProgressPropagatesError(t *testing.T) {
	sentinel := errors.New("boom")
	err := RunProgress(10, func(report func(done int)) error {
		report(1)
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("RunProgress error = %v, want %v", err, sentinel)
	}
}

func TestRunProgressNilWork(t *testing.T) {
	if err := RunProgress(100, nil); err != nil {
		t.Fatalf("RunProgress(nil work) = %v, want nil", err)
	}
}

func TestRunProgressTinyTotalRunsPlainly(t *testing.T) {
	// total < threshold must still execute work (just without a bar).
	called := false
	err := RunProgress(1, func(report func(done int)) error {
		called = true
		report(1)
		return nil
	})
	if err != nil {
		t.Fatalf("RunProgress(tiny) error: %v", err)
	}
	if !called {
		t.Fatal("RunProgress(tiny) did not run work")
	}
}

func TestPrintHelpersNoPanic(t *testing.T) {
	// Smoke test: helpers must not panic on a non-TTY and format args.
	Successf("ok %s", "added")
	Warnf("careful %d", 3)
	Errorf("failed: %v", errors.New("x"))
}
