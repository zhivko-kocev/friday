package cli

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// alignTable column-aligns a set of same-shaped rows the way lipgloss/table
// computes column widths — dynamically, from the widest cell actually
// present — replacing a hardcoded printf width (%-10s, %-16s, ...) that
// silently misaligns the moment a value runs longer than the magic number
// baked into the format string (D29). headers is optional: pass nil for a
// plain grid with no column-label row. aligns gives each column's
// horizontal alignment (lipgloss.Left/lipgloss.Right); columns past the end
// of aligns default to Left, so a caller only needs to name the right-
// aligned numeric columns.
//
// Border is fully disabled — friday's tables were never boxed — with a
// plain two-space gap between columns and no trailing padding on the last
// one. Coloring and the quiet/--no-color contract stay entirely with
// internal/output: this returns exactly one already width-aligned plain-text
// line per header (if any) and per row, in order, for the caller to print
// through output.Line/OK/Skip/Dim exactly as it did with the old printf
// padding — this function only computes layout.
//
// One line per row is a guarantee callers index on: doctor and status walk
// the result against parallel slices, so an extra line would shift every
// later row onto the wrong severity and eventually panic. lipgloss renders a
// cell containing a newline as two physical lines, so cells are flattened
// before they reach it.
//
// BorderHeader is deliberately the inverse of "do we have a header row":
// lipgloss/table v1.1.0 has a row-count bug where, with no header set and
// BorderColumn/BorderRow both false, BorderHeader(false) silently renders
// only the first data row (verified directly against the installed
// module — every other row vanishes with no error). BorderHeader(true)
// avoids that with no header present and adds no visible line since the
// border glyphs are empty; the same BorderHeader(true) with an actual
// header row instead inserts an unwanted blank separator line, so a real
// header needs BorderHeader(false). Each setting is exactly wrong for the
// other's row count, so this flips on whether headers is empty.
func alignTable(headers []string, rows [][]string, aligns ...lipgloss.Position) []string {
	cols := len(headers)
	if cols == 0 && len(rows) > 0 {
		cols = len(rows[0])
	}
	if cols == 0 {
		return nil
	}
	t := table.New().
		Border(lipgloss.Border{}).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderHeader(len(headers) == 0).
		BorderColumn(false).BorderRow(false).
		StyleFunc(func(_, col int) lipgloss.Style {
			s := lipgloss.NewStyle()
			if col < len(aligns) {
				s = s.Align(aligns[col])
			}
			if col < cols-1 {
				s = s.PaddingRight(2)
			}
			return s
		})
	if len(headers) > 0 {
		t = t.Headers(headers...)
	}
	if len(rows) > 0 {
		t = t.Rows(flattenCells(rows)...)
	}
	lines := strings.Split(t.String(), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return lines
}

// flattenCells replaces the line breaks that would cost alignTable its
// one-line-per-row guarantee. A copy, because the caller's rows are its own.
func flattenCells(rows [][]string) [][]string {
	out := make([][]string, len(rows))
	for i, row := range rows {
		cells := make([]string, len(row))
		for j, cell := range row {
			cells[j] = strings.ReplaceAll(strings.ReplaceAll(cell, "\r\n", " "), "\n", " ")
		}
		out[i] = cells
	}
	return out
}
