package main

import (
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// where and the archived streams (stream archive, store/stream_archive.go): an
// archived stream's rows are hidden rows of the work and merge tables, which the
// frame does not draw and the folds still count. By default where leaves them out of
// what it shows and says them in one line, "N archived streams, M cards landed, $X";
// where --archived shows them as any other stream.

// archivedView is the archived streams' totals, read off the work table's shape
// (shapes in sprint.ViewOrder); with all, their rows of the work and merge tables are
// shown in shapes from here on, so the frame and the view draw them.
func archivedView(shapes []ntable.Table, all bool) store.ArchivedTotals {
	arch := store.ArchivedOf(shapes[slices.Index(sprint.ViewOrder, sprint.Work)])
	if all {
		for _, t := range []string{sprint.Work, sprint.Merge} {
			rows := shapes[slices.Index(sprint.ViewOrder, t)].Rows
			for i := range rows {
				if slices.Contains(arch.Streams, rows[i].Key) {
					rows[i].Hidden = false
				}
			}
		}
	}
	return arch
}

// withArchivedLine is the work table's text with the archived streams' line under it,
// none when no stream is archived.
func withArchivedLine(table string, arch store.ArchivedTotals) string {
	line := arch.Line()
	if line == "" {
		return table
	}
	return strings.TrimRight(table, "\n") + "\n" + line + " (where --archived)\n"
}

// liveClocks is the stream clocks but the archived streams', all of them with all.
func liveClocks(clocks []sprint.StreamClock, arch store.ArchivedTotals, all bool) []sprint.StreamClock {
	if all || len(arch.Streams) == 0 {
		return clocks
	}
	return slices.DeleteFunc(slices.Clone(clocks), func(c sprint.StreamClock) bool { return slices.Contains(arch.Streams, c.Stream) })
}

// liveRows is where --rows's rows but the archived streams', all of them with all.
func liveRows(rows []primaryRow, arch *store.ArchivedTotals, all bool) []primaryRow {
	if all || arch == nil || len(arch.Streams) == 0 {
		return rows
	}
	return slices.DeleteFunc(rows, func(r primaryRow) bool { return slices.Contains(arch.Streams, r.Stream) })
}
