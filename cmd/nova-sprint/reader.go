package main

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A reader up is a reader a process serves (docs/SPEC-SPRINT.md section 6): its
// reads are read by a process that beats for it (a machine's reader loop, a
// bud's, a friend's harness, each asking queue --as <reader>), never by its
// row. On 2026-10-05 `reader up` of two readers printed OK, both
// were away on the next tick because nothing served them, and the coordinator
// believed it had added reading capacity. So reader up refuses a reader no
// process serves, naming the beat that would serve it, and the readers table
// shows each row served or unserved from its beat (sprint.ReaderServed).

// servedColumn is the readers table's served cell: served or unserved.
const servedColumn = "served"

// refuseUnserved is reader up's refusal of the named readers no process serves
// (sprint.ReaderServed), exit 1 and nothing written, each named with why and
// the beat that would serve it; 0 when every one is served. A store that keeps
// no beats holds every reader served.
func refuseUnserved(ctx context.Context, st *store.Store, verbName string, names []string, now time.Time, stderr io.Writer) (int, error) {
	beats, err := st.ReaderBeats(ctx, names)
	if err != nil || beats == nil {
		return 0, err
	}
	var why []string
	for _, n := range names {
		if b := beats[n]; !sprint.ReaderServed(b, now) {
			why = append(why, sprint.UnservedWhy(n, b, now)+"; "+sprint.ReaderServeRemedy(n))
		}
	}
	if len(why) == 0 {
		return 0, nil
	}
	fmt.Fprintf(stderr, "%s %s: no process serves the reader, so it would not read: %s; nothing was changed; a reader is up while a process beats for it, never by its row; once it beats, run: nova-sprint %s %s (or --unserved to release the hold now and leave it away until it beats)\n",
		prog, verbName, oneline.Escape(strings.Join(why, "; ")), verbName, strings.Join(names, " "))
	return 1, nil
}

// readersServed is the readers table with a served column after width, each
// row served or unserved from its beat (sprint.ReaderServedWord); a store that
// keeps no beats (beats nil) shows every row served.
func readersServed(t ntable.Table, beats map[string]sprint.Beat, now time.Time) ntable.Table {
	names := columnNames(t.Columns)
	at := len(names)
	if i := slices.Index(names, sprint.FieldWidth); i >= 0 {
		at = i + 1
	}
	cols := slices.Insert(slices.Clone(t.Columns), at, ntable.Column{Name: servedColumn, Projection: ntable.Text, Fold: ntable.None})
	rows := make([]ntable.Row, len(t.Rows))
	for i, r := range t.Rows {
		row := r
		row.Cells = slices.Insert(slices.Clone(r.Cells), min(at, len(r.Cells)), ntable.Cell{})
		row.Texts = maps.Clone(r.Texts)
		if row.Texts == nil {
			row.Texts = map[string]string{}
		}
		row.Texts[servedColumn] = "served"
		if beats != nil {
			row.Texts[servedColumn] = sprint.ReaderServedWord(beats[r.Key], now)
		}
		rows[i] = row
	}
	t.Columns, t.Rows = cols, rows
	return t
}

// servedSummary is the summed readers row's served cell: how many rows are
// served, of all.
func servedSummary(t ntable.Table) string {
	n := 0
	for _, r := range t.Rows {
		if r.Texts[servedColumn] == "served" {
			n++
		}
	}
	return fmt.Sprintf("%d/%d", n, len(t.Rows))
}
