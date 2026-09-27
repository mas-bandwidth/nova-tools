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
	// LabelWidth fixes the row-label column's width the same way (the
	// sprint's stream block keeps its names 25 wide); 0 fits the labels.
	LabelWidth int
	// HideZeroRows hides a row whose count cells are all zero and all read.
	HideZeroRows bool
	// Title is the table's name, printed in the top-left cell, the header
	// of the row-label column (Glenn 2026-09-27: "tables need a title";
	// "the title goes where 'row' is currently").
	Title string
}

// Render prints the table as fixed-width text: the header row of column
// labels, a rule, one line per row (its label, then its cells), and, when
// any column folds, a rule and the footer row. Text, members, first and
// last cells are left-aligned; count cells right-aligned; a column's cells
// are separated by " | " and the rule joins dashes with "-+-". A cell whose
// set did not come back prints "?", and so does a fold over it. The last
// column is padded only when it is right-aligned, so no line ends in a
// space. An empty table, and a table with no visible row, renders as the
// empty string with no newline, title or not (Glenn 2026-09-26 10:00 AM
// ET: an empty stream table is hidden with no extra newline; 2026-09-27:
// "When a table has no rows, it should automatically hide. When it has
// rows again, it should show").
//
// The row's label is always the first column (Glenn 2026-09-27, the live
// session: eight benches rendered as eight anonymous rows of numbers), put
// in front of every declared column, and the footer label prints under it
// instead of eating the first count column's fold. Its header is the
// table's name when the caller gives one (opts.Title), else "row". A text
// column is a column like any other: it prints the row's value (row set),
// blank when the row has none; it never stands in for the label (Stella's
// read of #4456: a value in a leading text column made the row's key
// vanish; "the row identity must still have its own visible cell").
func Render(t Table, opts RenderOpts) string {
	rows := make([]Row, 0, len(t.Rows))
	for _, r := range t.Rows {
		if r.Hidden || opts.HideZeroRows && allZero(t, r) {
			continue // a hidden row stays in the folds (Glenn 2026-09-27: "hide the rows a-z but keep them there logically")
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return ""
	}
	// cols is what is printed; src[j] is the definition's column behind
	// cols[j], or -1 for the row-label column put in front.
	// hidden columns stay in the table (read, and formulas use them) and
	// are not drawn (Glenn 2026-09-27: "Keep it, since the calculations
	// depend on it, but hide that column")
	cols := make([]Column, 0, len(t.Columns))
	src := make([]int, 0, len(t.Columns))
	for j, c := range t.Columns {
		if t.IsHidden(c.Name) {
			continue
		}
		cols = append(cols, c)
		src = append(src, j)
	}
	cols = append([]Column{{Label: "row", Projection: Text, Fold: None}}, cols...)
	src = append([]int{-1}, src...)
	n := len(cols)
	header := make([]string, n)
	body := make([][]string, len(rows))
	footer := make([]string, n)
	for j, c := range cols {
		header[j] = c.LabelOrName()
	}
	if opts.Title != "" {
		header[0] = opts.Title
	}
	for i, r := range rows {
		body[i] = make([]string, n)
		for j := range cols {
			switch {
			case src[j] < 0:
				body[i][j] = r.LabelOrKey()
			default:
				body[i][j] = CellText(t.Columns, r, src[j])
			}
		}
	}
	hasFooter := t.HasFooter()
	if hasFooter {
		for j, c := range cols {
			if src[j] >= 0 {
				footer[j] = foldText(t.Columns, c, t.Rows, src[j])
			}
		}
		footer[0] = t.Footer()
	}
	widths := make([]int, n)
	for j, c := range cols {
		w := c.Width
		if j == 0 {
			w = opts.LabelWidth
		} else if v, ok := opts.Widths[c.Name]; ok && v > 0 {
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
	for j, c := range cols {
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

// CellText returns a full, unpadded projected cell. Render and machine-readable
// show use the same value, including text, percentages and unread dependencies.
func CellText(cols []Column, r Row, j int) string {
	if j < 0 || j >= len(cols) {
		return "?"
	}
	c := cols[j]
	if IsFormula(c.Projection) {
		return formulaText(cols, c, r)
	}
	return cellText(c, r, j)
}

// cellText is one body cell as printed.
func cellText(c Column, r Row, j int) string {
	if c.Projection == Text {
		return r.Texts[c.Name] // blank when the row has no value
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
// formulaValue is a pct(<col>) cell's value over the row: the named count
// as a share of the row's count cells together; ok is false when the row
// a cell it needs did not come back. A known empty row is zero percent.
func formulaValue(cols []Column, c Column, r Row) (float64, bool) {
	arg := FormulaArg(c.Projection)
	var part, total int64
	for k, o := range cols {
		if o.Projection != Count {
			continue
		}
		if k >= len(r.Cells) || r.Cells[k].Unread {
			return 0, false
		}
		total += r.Cells[k].Count
		if o.Name == arg {
			part = r.Cells[k].Count
		}
	}
	if total == 0 {
		return 0, true
	}
	return 100 * float64(part) / float64(total), true
}

// formulaText prints a formula cell: a percentage with one decimal
// ("33.3%"), "0.0%" when the row has no tasks, "?" when a count it needs did
// not come back (Stella's read of #4456: an unread dependency propagates).
func formulaText(cols []Column, c Column, r Row) string {
	for k, o := range cols {
		if o.Projection == Count && (k >= len(r.Cells) || r.Cells[k].Unread) {
			return "?"
		}
	}
	v, ok := formulaValue(cols, c, r)
	if !ok {
		return "?"
	}
	return pctText(v)
}

// pctText prints a percentage with one decimal always, "50.0%" (Glenn
// 2026-09-27: "standardize on one decimal point of precision for the %,
// even if it is .0").
func pctText(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64) + "%"
}

// foldText is one footer cell as printed, over every row (hidden rows
// included: the fold is the column's, not the screen's).
func foldText(cols []Column, c Column, rows []Row, j int) string {
	switch c.Fold {
	case Pooled:
		// the share over every row together: the named counts summed over
		// all counts summed, never the mean of the rows' percentages
		arg := FormulaArg(c.Projection)
		var part, total int64
		for _, r := range rows {
			for k, o := range cols {
				if o.Projection != Count {
					continue
				}
				if k >= len(r.Cells) || r.Cells[k].Unread {
					return "?"
				}
				total += r.Cells[k].Count
				if o.Name == arg {
					part += r.Cells[k].Count
				}
			}
		}
		if total == 0 {
			return pctText(0)
		}
		return pctText(100 * float64(part) / float64(total))
	case Avg:
		var sum float64
		n := 0
		for _, r := range rows {
			if j >= len(r.Cells) || r.Cells[j].Unread {
				return "?"
			}
			sum += float64(r.Cells[j].Count)
			n++
		}
		if n == 0 {
			return "-"
		}
		return strings.TrimSuffix(strconv.FormatFloat(sum/float64(n), 'f', 1, 64), ".0")
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
