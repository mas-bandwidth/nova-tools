// streams.go: the stream block as a nova-table (internal/ntable). Glenn
// 2026-09-27: "The work stream table is really just a series of ordered
// sets, per-cell" / "and the value printed, happens to be for each cell,
// |s|". The streams table's rows are the streams of ws:order and its cells
// are BOUND to the sets the card model owns, ws:<s>:<state> under the
// epoch (ws.KeyAt), one text column for the stream's name and one count
// column per state of ws.Stream, each folding to its sum in the footer row
// total. Nothing is copied: the sprint tick reads every cell through
// ntable.QueueCells in its one pipeline (the ws.CellReader seam of
// ws.CountsReader, so the headline and the block are one count, #4411),
// renders the block through ntable.Render, and the sprint loop binds the
// table in the store (ntable.Bind) whenever its shape moves, so `nova-table
// render streams` prints the same block from the same sets. A row's
// excluded member is the stream's sentinel: the stream's stop, not work,
// counted nowhere (#4318).
package table

import (
	"context"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// StreamsTable is the streams table's name in the store.
const StreamsTable = "streams"

// StreamsOwner is the verb that writes the sets the streams table's cells
// are bound to: a write through nova-table is refused naming it.
const StreamsOwner = "nova-sprint task move"

// StreamsRenderOpts renders the block as the sprint table always has:
// every all-zero row hidden (the whole block when none is left); the
// widths are the definition's (`nova-table render streams --hide-zero-rows`
// prints the same bytes).
var StreamsRenderOpts = ntable.RenderOpts{HideZeroRows: true}

// StreamsDefinition is the streams table with no rows: stream:text:none 25
// wide, then waiting, ready, working, review, merging and landed as
// count:sum as wide as their headers, footer total.
func StreamsDefinition() ntable.Table {
	t := ntable.Table{Name: StreamsTable, FooterLabel: ntable.DefaultFooter}
	t.Columns = append(t.Columns, ntable.Column{Name: "stream", Label: "stream", Projection: ntable.Text, Fold: ntable.None, Width: nameWidth})
	for _, state := range WSStates {
		t.Columns = append(t.Columns, ntable.Column{Name: state, Projection: ntable.Count, Fold: ntable.Sum})
	}
	return t
}

// StreamsShape is the streams table for the streams of ws:order under
// epoch: one row per stream, its label the stream's name, every count cell
// bound to ws.KeyAt(epoch, stream, state), its sentinel excluded.
func StreamsShape(streams []string, epoch uint64) ntable.Table {
	t := StreamsDefinition()
	for _, s := range streams {
		r := ntable.NewRow(t, s)
		r.Exclude, r.Owner = ws.SentinelID(s), StreamsOwner
		for j, state := range WSStates {
			r.Cells[j+1] = ntable.Cell{Key: ws.KeyAt(epoch, s, state), Bound: true}
		}
		t.Rows = append(t.Rows, r)
	}
	return t
}

// StreamsOf is the streams table filled from the one count: one row per
// stream of counts, its cells' counts and unread marks those rows carry.
// The block renders from it, so a snapshot built from counts alone (a
// failed tick's last good rows, a test's) prints the same block as a tick's.
func StreamsOf(counts ws.SprintCounts) ntable.Table {
	t := StreamsDefinition()
	for _, sc := range counts.Streams {
		r := ntable.NewRow(t, sc.Stream)
		r.Exclude, r.Owner = ws.SentinelID(sc.Stream), StreamsOwner
		for j := range WSStates {
			r.Cells[j+1] = ntable.Cell{Key: ws.KeyAt(counts.Epoch, sc.Stream, WSStates[j]), Bound: true, Count: sc.Cells[j], Unread: sc.Unread[j]}
		}
		t.Rows = append(t.Rows, r)
	}
	return t
}

// streamCells is the SprintReader's ws.CellReader: the stream cells read
// through the streams table in the tick's one pipeline.
type streamCells struct{}

func (s streamCells) Queue(ctx context.Context, pipe redis.Pipeliner, epoch uint64, streams []string) ws.CellsCmd {
	q := &streamCellsCmd{t: StreamsShape(streams, epoch)}
	q.cmd = ntable.QueueCells(ctx, pipe, &q.t)
	return q
}

type streamCellsCmd struct {
	t   ntable.Table
	cmd *ntable.CellsCmd
}

// Rows fills the table's cells and hands them to the one count as stream
// rows: the block renders from those (StreamsOf), and the loop binds the
// same shape.
func (q *streamCellsCmd) Rows() ([]ws.StreamCounts, error) {
	q.cmd.Result()
	rows := make([]ws.StreamCounts, 0, len(q.t.Rows))
	for _, r := range q.t.Rows {
		row := ws.StreamCounts{Stream: r.Key}
		for j := range WSStates {
			cell := r.Cells[j+1]
			row.Cells[j], row.Unread[j] = cell.Count, cell.Unread
		}
		rows = append(rows, row)
	}
	return rows, nil
}
