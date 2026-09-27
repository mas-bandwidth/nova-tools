package ntable

import (
	"strconv"
	"strings"
)

// RenderOpts is what Render is told beyond the table.
type RenderOpts struct {
	// Widths fixes a column's width by name (a minimum: a longer cell is
	// not cut), over the column's own Width; 0 leaves it as wide as its
	// widest cell.
	Widths map[string]int
	// HideZeroRows hides a row whose count cells are all zero and all read.
	HideZeroRows bool
}

// Render prints the table as fixed-width text: the header row of column
// labels, a rule, one line per row (its label, then its cells), and, when
// any column folds, a rule and the footer row. Text, members, first and
// last cells are left-aligned; count cells right-aligned; a column's cells
// are separated by " | " and the rule joins dashes with "-+-". A cell whose
// set did not come back prints "?", and so does a fold over it. The last
// column is padded only when it is right-aligned, so no line ends in a
// space. An empty table, and a table with no visible row, renders as the
// empty string with no newline (Glenn 2026-09-26 10:00 AM ET: an empty
// stream table is hidden with no extra newline).
func Render(t Table, opts RenderOpts) string {
	rows := make([]Row, 0, len(t.Rows))
	for _, r := range t.Rows {
		if opts.HideZeroRows && allZero(t, r) {
			continue
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return ""
	}
	n := len(t.Columns)
	header := make([]string, n)
	body := make([][]string, len(rows))
	footer := make([]string, n)
	for j, c := range t.Columns {
		header[j] = c.LabelOrName()
	}
	for i, r := range rows {
		body[i] = make([]string, n)
		for j, c := range t.Columns {
			body[i][j] = cellText(c, r, j)
		}
	}
	hasFooter := t.HasFooter()
	if hasFooter {
		for j, c := range t.Columns {
			footer[j] = foldText(c, t.Rows, j)
		}
		footer[0] = t.Footer()
	}
	widths := make([]int, n)
	for j, c := range t.Columns {
		w := c.Width
		if v, ok := opts.Widths[c.Name]; ok && v > 0 {
			w = v
		}
		if w == 0 {
			w = len(header[j])
			for i := range body {
				w = max(w, len(body[i][j]))
			}
			if hasFooter {
				w = max(w, len(footer[j]))
			}
		}
		widths[j] = w
	}
	right := make([]bool, n)
	for j, c := range t.Columns {
		right[j] = c.Projection == Count
	}
	var b, l strings.Builder
	line := func(cells []string, footerRow bool) {
		l.Reset()
		for j, s := range cells {
			if j > 0 {
				l.WriteString(" | ")
			}
			last := j == n-1
			switch {
			case footerRow && j == 0:
				pad(&l, s, widths[j], false, last)
			default:
				pad(&l, s, widths[j], right[j], last)
			}
		}
		// a blank left-aligned last cell leaves no trailing space
		b.WriteString(strings.TrimRight(l.String(), " "))
		b.WriteByte('\n')
	}
	rule := func() {
		for j, w := range widths {
			if j > 0 {
				b.WriteString("-+-")
			}
			b.WriteString(strings.Repeat("-", w))
		}
		b.WriteByte('\n')
	}
	line(header, false)
	rule()
	for _, cells := range body {
		line(cells, false)
	}
	if hasFooter {
		rule()
		line(footer, true)
	}
	return b.String()
}

// pad writes s in a field of width w, right- or left-aligned; a
// left-aligned last field is written as it is.
func pad(b *strings.Builder, s string, w int, right, last bool) {
	fill := w - len(s)
	if fill < 0 {
		fill = 0
	}
	switch {
	case right:
		b.WriteString(strings.Repeat(" ", fill))
		b.WriteString(s)
	case last:
		b.WriteString(s)
	default:
		b.WriteString(s)
		b.WriteString(strings.Repeat(" ", fill))
	}
}

// allZero says every count cell of r is 0 and read; a row with no count
// cell is never all-zero.
func allZero(t Table, r Row) bool {
	counts := 0
	for j, c := range t.Columns {
		if c.Projection != Count || j >= len(r.Cells) {
			continue
		}
		counts++
		if r.Cells[j].Unread || r.Cells[j].Count != 0 {
			return false
		}
	}
	return counts > 0
}

// cellText is one body cell as printed.
func cellText(c Column, r Row, j int) string {
	if c.Projection == Text {
		return r.LabelOrKey()
	}
	if j >= len(r.Cells) {
		return "?"
	}
	cell := r.Cells[j]
	if cell.Unread {
		return "?"
	}
	switch c.Projection {
	case Count:
		return strconv.FormatInt(cell.Count, 10)
	case Members:
		if len(cell.Members) == 0 {
			return "-"
		}
		names := make([]string, len(cell.Members))
		for i, m := range cell.Members {
			names[i] = m.Member
		}
		return strings.Join(names, ",")
	case First:
		if len(cell.Members) == 0 {
			return "-"
		}
		return cell.Members[0].Member
	case Last:
		if len(cell.Members) == 0 {
			return "-"
		}
		return cell.Members[len(cell.Members)-1].Member
	}
	return "?"
}

// foldText is one footer cell as printed, over every row (hidden rows
// included: a fold is the column's, not the screen's).
func foldText(c Column, rows []Row, j int) string {
	switch c.Fold {
	case Sum, Max:
		var v int64
		for _, r := range rows {
			if j >= len(r.Cells) || r.Cells[j].Unread {
				return "?"
			}
			n := r.Cells[j].Count
			if c.Fold == Sum {
				v += n
			} else if n > v {
				v = n
			}
		}
		return strconv.FormatInt(v, 10)
	case Union:
		seen := map[string]bool{}
		var names []string
		for _, r := range rows {
			if j >= len(r.Cells) || r.Cells[j].Unread {
				return "?"
			}
			for _, m := range r.Cells[j].Members {
				if !seen[m.Member] {
					seen[m.Member] = true
					names = append(names, m.Member)
				}
			}
		}
		if len(names) == 0 {
			return "-"
		}
		return strings.Join(names, ",")
	}
	return ""
}
