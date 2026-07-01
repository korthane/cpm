package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// tableCell is one pre-formatted cell. The style is applied after padding is
// computed on the plain text, so styled cells stay aligned.
type tableCell struct {
	text  string
	style lipgloss.Style
}

// tableColumn is one vertical slice of the comparison table: header lines on
// top, then one cell per row. All columns of a table carry the same number of
// header lines and rows.
type tableColumn struct {
	header []tableCell
	cells  []tableCell
}

// comparisonTable renders scrollable profile columns plus a pinned identity
// column. The pinned column is always drawn on the right; the profile columns
// show a window starting at scroll, with ◀/▶ indicators when columns are
// hidden. This layout is shared by the plugins and (later) MCP tabs.
type comparisonTable struct {
	profiles []tableColumn
	pinned   tableColumn
	scroll   int
	width    int
}

const (
	defaultWidth       = 80
	minProfileColWidth = 12
	maxProfileColWidth = 28
	columnGap          = "  "
	pinnedSeparator    = " │ "
	// gutterWidth reserves space either side of the profile area for the
	// scroll indicators so toggling them does not shift the columns.
	gutterWidth = 2
)

func (t comparisonTable) render() string {
	if len(t.profiles) == 0 {
		return ""
	}
	width := t.width
	if width <= 0 {
		width = defaultWidth
	}

	// The pinned column takes what it needs, capped at half the screen so at
	// least one profile column always fits beside it.
	pinnedW := min(columnWidth(t.pinned, 1), width/2)
	widths := make([]int, len(t.profiles))
	for i, col := range t.profiles {
		widths[i] = min(columnWidth(col, minProfileColWidth), maxProfileColWidth)
	}

	avail := width - pinnedW - lipgloss.Width(pinnedSeparator) - 2*gutterWidth
	scroll := min(max(t.scroll, 0), len(t.profiles)-1)
	visible, used := visibleColumns(widths, scroll, avail)

	leftHidden := scroll > 0
	rightHidden := visible[len(visible)-1] < len(t.profiles)-1

	var b strings.Builder
	for line := range headerLineCount(t) {
		left, right := "  ", "  "
		// Indicators live on the first header line only.
		if line == 0 && leftHidden {
			left = "◀ "
		}
		if line == 0 && rightHidden {
			right = " ▶"
		}
		t.writeLine(&b, visible, widths, used, pinnedW, left, right,
			func(c tableColumn) tableCell { return lineAt(c.header, line) })
	}
	b.WriteString(strings.Repeat("─", min(width, used+2*gutterWidth+lipgloss.Width(pinnedSeparator)+pinnedW)))
	b.WriteByte('\n')
	for row := range len(t.pinned.cells) {
		t.writeLine(&b, visible, widths, used, pinnedW, "  ", "  ",
			func(c tableColumn) tableCell { return lineAt(c.cells, row) })
	}
	return b.String()
}

// visibleColumns picks the run of columns starting at scroll that fits in
// avail; the first column is always included even when it alone overflows.
func visibleColumns(widths []int, scroll, avail int) (visible []int, used int) {
	for i := scroll; i < len(widths); i++ {
		need := widths[i]
		if len(visible) > 0 {
			need += lipgloss.Width(columnGap)
		}
		if used+need > avail && len(visible) > 0 {
			break
		}
		visible = append(visible, i)
		used += need
	}
	return visible, used
}

func (t comparisonTable) writeLine(b *strings.Builder, visible, widths []int, used, pinnedW int,
	left, right string, pick func(tableColumn) tableCell,
) {
	b.WriteString(left)
	lineW := 0
	for n, i := range visible {
		if n > 0 {
			b.WriteString(columnGap)
			lineW += lipgloss.Width(columnGap)
		}
		b.WriteString(padCell(pick(t.profiles[i]), widths[i]))
		lineW += widths[i]
	}
	// Pad the profile area to its full width so the pinned column lines up
	// even when the last visible column is the narrow one.
	b.WriteString(strings.Repeat(" ", max(0, used-lineW)))
	b.WriteString(right)
	b.WriteString(pinnedSeparator)
	// No trailing padding: the pinned column ends the line.
	cell := pick(t.pinned)
	b.WriteString(cell.style.Render(truncate(cell.text, pinnedW)))
	b.WriteByte('\n')
}

func headerLineCount(t comparisonTable) int {
	lines := len(t.pinned.header)
	for _, c := range t.profiles {
		lines = max(lines, len(c.header))
	}
	return lines
}

func lineAt(cells []tableCell, i int) tableCell {
	if i < len(cells) {
		return cells[i]
	}
	return tableCell{}
}

func columnWidth(c tableColumn, minW int) int {
	w := minW
	for _, cell := range c.header {
		w = max(w, lipgloss.Width(cell.text))
	}
	for _, cell := range c.cells {
		w = max(w, lipgloss.Width(cell.text))
	}
	return w
}

func padCell(c tableCell, w int) string {
	text := truncate(c.text, w)
	padding := strings.Repeat(" ", max(0, w-lipgloss.Width(text)))
	return c.style.Render(text) + padding
}

// truncate shortens s to at most w display cells, ending in an ellipsis when
// it had to cut.
func truncate(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > w-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	b.WriteRune('…')
	return b.String()
}
