package store

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The archive (docs/SPEC-SPRINT.md section 11, streams; the owner, 2026-10-05: "I would
// like you to remove all the already landed work streams"): a stream whose every card
// has landed leaves the drawn work and merge tables and keeps its record. An archived
// stream is its two rows hidden by the table layer's row hide: each row stays in its
// table with every card placed in it, and stays in the folds, so the summary's landed
// and all, the cost column's footer, the ledger of landings, the roadmap and release
// records read it as before; where and the dashboard leave it out of the rows they
// draw, and where --archived draws it. stream unarchive shows the rows again. Neither
// moves a card, so neither waits for a STOPPED machine.

// RowShower is a backend that shows hidden rows again, as the table layer's row hide
// with hide off does: the undo of RowsHide (Mem and Redis).
type RowShower interface {
	RowsShow(ctx context.Context, table string, rows []string) error
}

// RowsShow shows hidden rows of the table again, under RowsAdd's epoch check; a row
// the table does not have, or one not hidden, is skipped.
func (m *Mem) RowsShow(_ context.Context, table string, rows []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls["rowsshow"]++
	t, err := m.table(table)
	if err != nil {
		return err
	}
	if err := m.writeEpoch(t); err != nil {
		return err
	}
	ep := t.at(m.active(t))
	for _, r := range rows {
		delete(ep.hidden, r)
	}
	t.rev++
	t.wrote[m.active(t)] = true
	t.changes = append(t.changes, memChange{epoch: m.active(t), before: t.rev - 1, after: t.rev, verb: "rows_hide"})
	return nil
}

// RowsShow shows hidden rows through the table layer's row hide, hide off.
func (r *Redis) RowsShow(ctx context.Context, table string, rows []string) error {
	_, err := ntable.RowsHide(ctx, r.C, table, false, rows, r.writeOpts())
	return err
}

// keyArchiveKept is the streams unarchived by hand (JSON, stream -> archiveMark): the
// tick archives such a stream again only once the counts it was shown at change.
const keyArchiveKept = "archive-kept"

// archiveMark is a stream's epoch and its work row's landed and all counts.
func archiveMark(epoch uint64, landed, all int64) string {
	return fmt.Sprintf("%d:%d:%d", epoch, landed, all)
}

// ArchivedTotals is what the archived streams hold, read off the work table's shape:
// the streams in row order, their landed cards, and their cost, the sum of their cost
// cells as money ("-" when none was priced).
type ArchivedTotals struct {
	Streams []string `json:"streams"`
	Landed  int64    `json:"landed"`
	Cost    string   `json:"cost"`
}

// Line is the one line that stands for the archived streams: "N archived streams, M
// cards landed, $X"; "" when none is archived.
func (a ArchivedTotals) Line() string {
	if len(a.Streams) == 0 {
		return ""
	}
	plural := func(n int64, one, other string) string {
		if n == 1 {
			return "1 " + one
		}
		return strconv.FormatInt(n, 10) + " " + other
	}
	return fmt.Sprintf("%s, %s, %s", plural(int64(len(a.Streams)), "archived stream", "archived streams"), plural(a.Landed, "card landed", "cards landed"), a.Cost)
}

// ArchivedOf is the archived streams' totals: the work table's hidden rows.
func ArchivedOf(work ntable.Table) ArchivedTotals {
	out := ArchivedTotals{Streams: []string{}, Cost: "-"}
	landed, cost := work.Column(sprint.Landed), work.Column(sprint.Cost)
	var usd []string
	for _, r := range work.Rows {
		if !r.Hidden {
			continue
		}
		out.Streams = append(out.Streams, r.Key)
		if landed >= 0 && landed < len(r.Cells) {
			out.Landed += r.Cells[landed].Count
		}
		if cost >= 0 {
			if v := strings.TrimPrefix(ntable.CellText(work.Columns, r, cost), "$"); v != "" && v != "-" {
				usd = append(usd, v)
			}
		}
	}
	if sum, ok := cardcost.Sum(usd...); ok && len(usd) > 0 {
		out.Cost = sprint.MoneyText(sum)
	}
	return out
}

// hiddenRows is the table's hidden rows.
func hiddenRows(t ntable.Table) map[string]bool {
	out := map[string]bool{}
	for _, r := range t.Rows {
		if r.Hidden {
			out[r.Key] = true
		}
	}
	return out
}

// ArchivedStreams is the archived streams, in the work table's row order.
func (st *Store) ArchivedStreams(ctx context.Context) ([]string, error) {
	shapes, err := st.shapes(ctx, []string{st.Names.Table(sprint.Work)})
	if err != nil {
		return nil, err
	}
	return ArchivedOf(shapes[0]).Streams, nil
}

// ArchiveStreams takes the named streams off the drawn work and merge tables
// (sprint.StreamArchive): refused, all or none and nothing written, for a stream that
// is no row, is archived already, or holds a card not landed. It moves no card.
func (st *Store) ArchiveStreams(ctx context.Context, names []string) ([]sprint.Refusal, error) {
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Work)})
	if err != nil {
		return nil, err
	}
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
	if err != nil {
		return nil, err
	}
	if refused := sprint.StreamArchive(s, hiddenRows(shapes[0]), names); len(refused) > 0 {
		return refused, nil
	}
	if err := st.hide(ctx, names); err != nil {
		return nil, err
	}
	// archived by hand: the mark of an earlier unarchive is spent
	return nil, st.keep(ctx, func(kept map[string]string) {
		for _, n := range names {
			delete(kept, n)
		}
	})
}

// UnarchiveStreams shows the named archived streams on the work and merge tables
// again (sprint.StreamUnarchive), all or none. The tick does not archive such a
// stream again until a card is added to it or lands in it.
func (st *Store) UnarchiveStreams(ctx context.Context, names []string) ([]sprint.Refusal, error) {
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Work)})
	if err != nil {
		return nil, err
	}
	work := shapes[0]
	var rows []string
	for _, r := range work.Rows {
		rows = append(rows, r.Key)
	}
	if refused := sprint.StreamUnarchive(rows, hiddenRows(work), names); len(refused) > 0 {
		return refused, nil
	}
	if err := st.show(ctx, names); err != nil {
		return nil, err
	}
	counts := map[string][2]int64{}
	for _, r := range work.Rows {
		counts[r.Key] = rowCounts(work, r)
	}
	return nil, st.keep(ctx, func(kept map[string]string) {
		for _, n := range names {
			kept[n] = archiveMark(work.Epoch, counts[n][0], counts[n][1])
		}
	})
}

// hide hides the streams' rows of the merge table, then of the work table: a stream
// is archived once its work row is hidden (ArchivedOf), so one stopped between the
// two is not archived and archives whole when run again.
func (st *Store) hide(ctx context.Context, streams []string) error {
	for _, t := range []string{sprint.Merge, sprint.Work} {
		if err := st.B.RowsHide(ctx, st.Names.Table(t), streams); err != nil {
			return fmt.Errorf("hiding the rows of %s: %w", st.Names.Table(t), err)
		}
	}
	return nil
}

// show shows the streams' rows of the work table, then of the merge table.
func (st *Store) show(ctx context.Context, streams []string) error {
	shower, ok := st.B.(RowShower)
	if !ok {
		return fmt.Errorf("this store cannot show a hidden row again")
	}
	for _, t := range []string{sprint.Work, sprint.Merge} {
		if err := shower.RowsShow(ctx, st.Names.Table(t), streams); err != nil {
			return fmt.Errorf("showing the rows of %s: %w", st.Names.Table(t), err)
		}
	}
	return nil
}

// keep changes the record of the streams unarchived by hand.
func (st *Store) keep(ctx context.Context, change func(map[string]string)) error {
	kept := map[string]string{}
	if err := st.getJSON(ctx, keyArchiveKept, &kept); err != nil {
		return err
	}
	change(kept)
	return st.putJSON(ctx, keyArchiveKept, kept)
}

// rowCounts is a work row's landed cards and all its cards.
func rowCounts(work ntable.Table, r ntable.Row) [2]int64 {
	var out [2]int64
	j := work.Column(sprint.Landed)
	for k, c := range work.Columns {
		if !c.HasSet() || k >= len(r.Cells) {
			continue
		}
		out[1] += r.Cells[k].Count
		if k == j {
			out[0] += r.Cells[k].Count
		}
	}
	return out
}

// mergeLive is a merge row's cards not merged: queued, stuck and returned.
func mergeLive(merge ntable.Table, r ntable.Row) int64 {
	var n int64
	for _, col := range []string{sprint.Queued, sprint.Stuck, sprint.Returned} {
		if k := merge.Column(col); k >= 0 && k < len(r.Cells) {
			n += r.Cells[k].Count
		}
	}
	return n
}

// ArchiveDue is the tick's archive, planned off the work and merge tables' shapes:
// the streams to archive, every one a shown stream that holds at least one card,
// every card it holds landed (its work row's cards all in landed, its merge row's
// none queued, stuck or returned), nothing queued for it (queued: the rows the work
// table's queue names) and no unarchive by hand at its counts as they are (kept); and
// the archived streams to show again, each holding a card not landed.
func ArchiveDue(work, merge ntable.Table, queued map[string]bool, kept map[string]string) (archive, show []string) {
	mrows := map[string]ntable.Row{}
	for _, r := range merge.Rows {
		mrows[r.Key] = r
	}
	for _, r := range work.Rows {
		n := rowCounts(work, r)
		live := n[1] - n[0] + mergeLive(merge, mrows[r.Key])
		switch {
		case r.Hidden && live > 0:
			show = append(show, r.Key)
		case r.Hidden || live > 0 || n[0] == 0 || queued[r.Key]:
		case kept[r.Key] == archiveMark(work.Epoch, n[0], n[1]):
		default:
			archive = append(archive, r.Key)
		}
	}
	return archive, show
}

// queuedRows is the rows the work table's queue names: a change queued while the
// machine runs that creates or moves a card into a stream waits behind it.
func queuedRows(q []sprint.QueuedChange) map[string]bool {
	out := map[string]bool{}
	for _, c := range q {
		e := c.Entry
		if e == nil {
			continue
		}
		if e.Create != nil {
			out[e.Create.Row] = true
		}
		if e.Move != nil {
			out[e.Move.Row] = true
		}
	}
	return out
}

// archiveLanded is the tick's archive (ArchiveDue): it hides the streams due, shows
// the archived streams that hold a card not landed again, and says each once, as
// information, in one happened note to the coordinator. It is the streams it
// archived and showed.
func (st *Store) archiveLanded(ctx context.Context) (archived, shown []string, err error) {
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Work), st.Names.Table(sprint.Merge)})
	if err != nil {
		return nil, nil, err
	}
	f, err := st.B.ReadFence(ctx)
	if err != nil {
		return nil, nil, err
	}
	queued := map[string]bool{}
	if f.Queued > 0 {
		q, err := st.B.QueueRead(ctx)
		if err != nil {
			return nil, nil, err
		}
		queued = queuedRows(q)
	}
	kept := map[string]string{}
	if err := st.getJSON(ctx, keyArchiveKept, &kept); err != nil {
		return nil, nil, err
	}
	archive, show := ArchiveDue(shapes[0], shapes[1], queued, kept)
	if len(show) > 0 {
		if err := st.show(ctx, show); err != nil {
			return nil, nil, err
		}
		if err := st.tellTick(ctx, "stream archive", sprint.NStreamUnarchived, "shown again, each holding a card not landed: "+streamList(show), ""); err != nil {
			return nil, show, err
		}
	}
	if len(archive) > 0 {
		if err := st.hide(ctx, archive); err != nil {
			return nil, show, err
		}
		if err := st.tellTick(ctx, "stream archive", sprint.NStreamArchived, "archived, every card landed and nothing waiting behind: "+streamList(archive)+"; the cards, their cost and their landings are kept", "nova-sprint where --archived; nova-sprint stream unarchive <stream>"); err != nil {
			return archive, show, err
		}
	}
	return archive, show, nil
}

// streamList names the streams, at most twenty, the rest counted.
func streamList(streams []string) string {
	const most = 20
	if len(streams) <= most {
		return strings.Join(streams, ",")
	}
	return strings.Join(slices.Clone(streams[:most]), ",") + fmt.Sprintf(" and %d more", len(streams)-most)
}
