package main

import (
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// where and the archived streams (stream archive, docs/SPEC-SPRINT.md section 1): the
// table layer draws no hidden row, so the text frame shows the live streams and, under
// the work table, one line for the archived ones; the JSON view drops their rows unless
// --archived, while landed, all, the summary and the footers count them always.

// archivedLine is the work table's text with the archived streams' line under it:
// "107 archived streams, 2,843 cards landed, $1234.56"; the text as it is with none.
func archivedLine(text string, a *sprint.ArchiveSum) string {
	if a == nil {
		return text
	}
	return strings.TrimRight(text, "\n") + "\n" + a.Line() + "\n"
}

// dropArchived takes the archived streams' rows out of the view: their work and merge
// rows, their clocks, their costs and their primaries' rows (--rows). Archived keeps
// their names and their totals.
func (v *whereView) dropArchived() {
	if v.Archived == nil {
		return
	}
	for _, t := range []string{sprint.Work, sprint.Merge} {
		for _, name := range v.Archived.Streams {
			delete(v.Tables[t], name)
		}
	}
	for _, name := range v.Archived.Streams {
		delete(v.StreamCosts, name)
	}
	v.Streams = slices.DeleteFunc(v.Streams, func(c sprint.StreamClock) bool { return v.Archived.Has(c.Stream) })
	v.Rows = slices.DeleteFunc(v.Rows, func(r primaryRow) bool { return v.Archived.Has(r.Stream) })
}
