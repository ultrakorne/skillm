package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

// Row is one line of the `skillm list` table. Every field is a
// pre-formatted string so the renderer stays presentation-only and the
// command layer owns all wording.
type Row struct {
	ID        string // skill id
	Source    string // origin (git url or local path)
	Installed string // scopes×agents read live from disk, e.g. "global: claude,codex"
	Kind      string // "git" or "local" — cheap to derive; update status lives in `skillm check`
}

// RenderSkillTable formats rows into a columnar view: ID | Source | Installed |
// Kind. On a TTY it draws a bordered, colorized table (the Kind cell tinted by
// meaning); off a TTY it emits a plain tab-separated grid so output stays pipe-
// and grep-friendly. An empty rows slice yields a short notice rather than an
// empty frame.
func RenderSkillTable(rows []Row) string {
	headers := []string{"ID", "Source", "Installed", "Kind"}

	if len(rows) == 0 {
		if IsTTY() {
			return faintStyle().Render("No skills in Home.")
		}
		return "No skills in Home."
	}

	if !IsTTY() {
		return renderPlain(headers, rows)
	}
	return renderStyled(headers, rows)
}

func renderPlain(headers []string, rows []Row) string {
	var b strings.Builder
	b.WriteString(strings.Join(headers, "\t"))
	b.WriteByte('\n')
	for _, r := range rows {
		b.WriteString(r.ID)
		b.WriteByte('\t')
		b.WriteString(r.Source)
		b.WriteByte('\t')
		b.WriteString(r.Installed)
		b.WriteByte('\t')
		b.WriteString(r.Kind)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderStyled(headers []string, rows []Row) string {
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")) // cyan
	cellStyle := lipgloss.NewStyle().Padding(0, 1)
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")) // dim border

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(borderStyle).
		Headers(headers...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle.Padding(0, 1)
			}
			base := cellStyle
			// Tint the Kind column (index 3) by meaning.
			if col == 3 && row >= 0 && row < len(rows) {
				return base.Foreground(kindColor(rows[row].Kind))
			}
			return base
		})

	// Constrain the table to the terminal so wide cells (long git URLs) wrap
	// within their column instead of overflowing the window. The resizer fits
	// columns plus borders inside this width; 0 (non-TTY/unknown) leaves the
	// table to size to content, as before.
	if w := TerminalWidth(); w > 0 {
		t.Width(w)
	}

	for _, r := range rows {
		t.Row(r.ID, r.Source, r.Installed, r.Kind)
	}
	return t.String()
}

func kindColor(kind string) color.Color {
	if kind == "local" {
		return lipgloss.Color("8") // dim — local skills have no upstream
	}
	return lipgloss.Color("6") // cyan — git-tracked
}

func faintStyle() lipgloss.Style {
	return lipgloss.NewStyle().Faint(true)
}
