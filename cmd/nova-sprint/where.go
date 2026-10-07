package main

import (
	"cmp"
	"maps"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// heldColumnName is where's work-table column marking a held stream, and heldField
// its field on the row where --json carries it. The stream's own state is on the
// merge row, which where hides by default, so a hold showed nowhere on the frame
// the coordinator reads (fix 2 of the defects of 2026-10-07: a card's story said
// "stream X is held" while where marked no stream).
const heldColumnName = "held"

// heldColumn is the work table with the held column added: a stream the coordinator
// holds reads `held <reason>` (the reason the control card keeps, FieldHeldReason),
// every other row `-`. A frame with no held stream is left exactly as it was, so the
// column appears only where a hold is, and the stored view a person reads does not
// change. No cell is stored and no count folds it, as perLandedColumn's column: it
// is the row's mark in the frame alone. mark is the same value by stream, for where
// --json's row field.
func heldColumn(t ntable.Table, clocks []sprint.StreamClock) (ntable.Table, map[string]string) {
	mark := map[string]string{}
	for _, c := range clocks {
		if c.Held {
			mark[c.Stream] = cmp.Or(c.Reason, "held")
		}
	}
	if len(mark) == 0 {
		return t, mark
	}
	t.Columns = append(slices.Clone(t.Columns), ntable.Column{Name: heldColumnName, Projection: ntable.Text, Fold: ntable.None})
	rows := make([]ntable.Row, len(t.Rows))
	for i, r := range t.Rows {
		texts := map[string]string{}
		maps.Copy(texts, r.Texts)
		if why, ok := mark[r.Key]; ok {
			texts[heldColumnName] = "held " + why
		} else {
			texts[heldColumnName] = "-"
		}
		r.Texts = texts
		rows[i] = r
	}
	t.Rows = rows
	return t, mark
}
