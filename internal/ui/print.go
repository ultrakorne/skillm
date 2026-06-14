package ui

import (
	"fmt"
	"os"

	"charm.land/lipgloss/v2"
)

// Styled print helpers. On a TTY they colorize a leading glyph; otherwise they
// emit plain text so logs and pipes stay clean. Success/Warn go to stdout,
// Error goes to stderr (matching common CLI conventions).

var (
	styleSuccess = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true) // green
	styleWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true) // yellow
	styleError   = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true) // red
)

// Successf prints a success line to stdout (green check on a TTY).
func Successf(format string, a ...any) {
	emit(os.Stdout, styleSuccess, "✓", format, a...)
}

// Warnf prints a warning line to stdout (yellow bang on a TTY).
func Warnf(format string, a ...any) {
	emit(os.Stdout, styleWarn, "!", format, a...)
}

// Errorf prints an error line to stderr (red cross on a TTY). It does not
// terminate the program; callers decide how to handle the failure.
func Errorf(format string, a ...any) {
	emit(os.Stderr, styleError, "✗", format, a...)
}

func emit(w *os.File, style lipgloss.Style, glyph, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if IsTTY() {
		fmt.Fprintf(w, "%s %s\n", style.Render(glyph), msg)
		return
	}
	fmt.Fprintf(w, "%s %s\n", glyph, msg)
}
