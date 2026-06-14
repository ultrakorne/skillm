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
	ID     string // skill id
	Source string // origin (git url or local path)
	Linked string // scopes×agents read live from disk, e.g. "global: claude,codex"
	Status string // up-to-date | update available | local | untracked
}

const (
	statusUpdateAvailable = "update available"
	statusLocal           = "local"
	statusUntracked       = "untracked"
)

// RenderSkillTable formats rows into a columnar view: ID | Source | Linked |
// Status. On a TTY it draws a bordered, colorized table (status cells tinted
// by meaning); off a TTY it emits a plain tab-separated grid so output stays
// pipe- and grep-friendly. An empty rows slice yields a short notice rather
// than an empty frame.
func RenderSkillTable(rows []Row) string {
	headers := []string{"ID", "Source", "Linked", "Status"}

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
		b.WriteString(r.Linked)
		b.WriteByte('\t')
		b.WriteString(r.Status)
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
			// Tint the Status column (index 3) by meaning.
			if col == 3 && row >= 0 && row < len(rows) {
				return base.Foreground(statusColor(rows[row].Status))
			}
			return base
		})

	for _, r := range rows {
		t.Row(r.ID, r.Source, r.Linked, r.Status)
	}
	return t.String()
}

func statusColor(status string) color.Color {
	switch status {
	case statusUpdateAvailable:
		return lipgloss.Color("3") // yellow
	case statusUntracked:
		return lipgloss.Color("1") // red
	case statusLocal:
		return lipgloss.Color("8") // dim
	default: // up-to-date and anything else
		return lipgloss.Color("2") // green
	}
}

func faintStyle() lipgloss.Style {
	return lipgloss.NewStyle().Faint(true)
}
