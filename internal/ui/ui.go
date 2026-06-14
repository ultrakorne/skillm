// Package ui holds skillm's presentation layer: lipgloss tables and styles,
// huh interactive pickers, a bubbles progress bar, and the styled print
// helpers used by the cobra commands.
//
// Every entry point in this package AUTO-DEGRADES when stdout is not a
// terminal (PLAN §4): colors and spinners are dropped, and the interactive
// prompts refuse to run, returning an error that names the flag a caller can
// pass instead. This keeps skillm deterministic in CI and dotfile bootstraps.
package ui

import (
	"os"

	"github.com/charmbracelet/x/term"
	"github.com/mattn/go-isatty"
)

// IsTTY reports whether stdout is an interactive terminal. All styling and
// prompting decisions in this package key off this single check so behaviour
// is consistent across commands.
func IsTTY() bool {
	fd := os.Stdout.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// TerminalWidth returns the width in cells of the stdout terminal, or 0 when
// stdout is not a terminal or its size can't be determined. Renderers treat 0
// as "unbounded" and only constrain their layout when a positive width is
// known, so tables fit the window instead of overflowing it.
func TerminalWidth() int {
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w <= 0 {
		return 0
	}
	return w
}
