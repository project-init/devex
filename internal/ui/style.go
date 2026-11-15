package ui

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

// The palette uses the 16 basic ANSI colors so it follows the user's terminal theme.
var (
	headingStyle = lipgloss.NewStyle().Bold(true)
	accentStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	goodStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	badStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	dimStyle     = lipgloss.NewStyle().Faint(true)
	cellStyle    = lipgloss.NewStyle().Padding(0, 1)
)

// Heading styles text for stdout as a heading.
func (u *UI) Heading(text string) string { return render(u.outStyled, headingStyle, text) }

// Accent styles text for stdout as a highlight.
func (u *UI) Accent(text string) string { return render(u.outStyled, accentStyle, text) }

// Good styles text for stdout as a success.
func (u *UI) Good(text string) string { return render(u.outStyled, goodStyle, text) }

// Warning styles text for stdout as a warning.
func (u *UI) Warning(text string) string { return render(u.outStyled, warnStyle, text) }

// Bad styles text for stdout as a failure.
func (u *UI) Bad(text string) string { return render(u.outStyled, badStyle, text) }

// Dim styles text for stdout as secondary.
func (u *UI) Dim(text string) string { return render(u.outStyled, dimStyle, text) }

func (u *UI) errStyle(style lipgloss.Style, text string) string {
	return render(u.errStyled, style, text)
}

func render(styled bool, style lipgloss.Style, text string) string {
	if !styled {
		return text
	}
	return style.Render(text)
}

// Table writes rows under headers to stdout: a bordered table on a styled terminal, and
// space-aligned columns otherwise, which scripts can split on whitespace.
func (u *UI) Table(headers []string, rows [][]string) {
	if !u.outStyled {
		writer := tabwriter.NewWriter(u.out, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(writer, strings.Join(headers, "\t"))
		for _, row := range rows {
			_, _ = fmt.Fprintln(writer, strings.Join(row, "\t"))
		}
		_ = writer.Flush()
		return
	}
	rendered := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(dimStyle).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return headingStyle.Padding(0, 1)
			}
			return cellStyle
		}).
		Render()
	_, _ = fmt.Fprintln(u.out, rendered)
}
