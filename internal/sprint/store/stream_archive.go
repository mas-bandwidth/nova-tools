package store

import (
	"context"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Stream archive (sprint.StreamArchive, docs/SPEC-SPRINT.md section 11): a stream's
// rows of the work and merge tables hidden as the table layer hides a row, every card
// and count kept. It is a write of the rows alone, never of a card, so it runs on a
// RUNNING machine: a step planned on the table's revision before it is sent again on
// the fresh one, as after any row change.

// keyArchive is the archive record's key, under the deployment's prefix.
const keyArchive = "archive" // STRING, the archive record (JSON)

// archiveRecord is what the tick reads of the coordinator's archive verbs: the landed
// count of each stream unarchived by hand, at the epoch it was unarchived in. The tick
// leaves such a stream shown until another of its cards lands (sprint.ArchiveDue).
type archiveRecord struct {
	Epoch uint64           `json:"epoch"`
	Kept  map[string]int64 `json:"kept,omitempty"`
}

// readArchive is the archive record of the epoch: empty when there is none, it is of
// another epoch, or the store keeps no records.
func (st *Store) readArchive(ctx context.Context, epoch uint64) (archiveRecord, error) {
	var r archiveRecord
	if _, err := st.kv(); err != nil {
		return archiveRecord{Epoch: epoch}, nil
	}
	if err := st.getJSON(ctx, keyArchive, &r); err != nil {
		return r, err
	}
	if r.Epoch != epoch || r.Kept == nil {
		r = archiveRecord{Epoch: epoch, Kept: map[string]int64{}}
	}
	return r, nil
}

// writeArchive writes the record; a store that keeps no records keeps none.
func (st *Store) writeArchive(ctx context.Context, r archiveRecord) error {
	if _, err := st.kv(); err != nil {
		return nil
	}
	return st.putJSON(ctx, keyArchive, r)
}

// ArchiveStreams is stream archive (archive true) or stream unarchive: the rule
// (sprint.StreamArchive, sprint.StreamUnarchive), all or none for the streams named,
// then each stream's rows of the work and merge tables hidden or shown. It is the
// refusals, nothing written, when the rule refuses.
func (st *Store) ArchiveStreams(ctx context.Context, streams []string, archive bool) ([]sprint.Refusal, error) {
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
	if err != nil {
		return nil, err
	}
	// the work table as the next pump leaves it: a landing or an add queued while the
	// machine runs is the stream's already
	q, err := st.B.QueueRead(ctx)
	if err != nil {
		return nil, err
	}
	s = sprint.WithQueue(s, q)
	rule := sprint.StreamUnarchive
	if archive {
		rule = sprint.StreamArchive
	}
	if refused := rule(s, streams); len(refused) > 0 {
		return refused, nil
	}
	rec, err := st.readArchive(ctx, s.Epoch)
	if err != nil {
		return nil, err
	}
	for _, name := range streams {
		delete(rec.Kept, name)
		if !archive {
			rec.Kept[name] = int64(len(s.Work.Cell(name, sprint.Landed)))
		}
	}
	// the record first: a tick between the two writes reads the unarchived streams kept
	if err := st.writeArchive(ctx, rec); err != nil {
		return nil, err
	}
	return nil, st.hideStreams(ctx, s, streams, archive)
}

// hideStreams hides (or shows) each stream's row of the work and the merge table, of
// each table only the rows it has.
func (st *Store) hideStreams(ctx context.Context, s *sprint.Snapshot, streams []string, hide bool) error {
	for _, t := range []*sprint.Table{s.Work, s.Merge} {
		var rows []string
		for _, name := range streams {
			if t.HasRow(name) {
				rows = append(rows, name)
			}
		}
		if len(rows) == 0 {
			continue
		}
		write := st.B.RowsShow
		if hide {
			write = st.B.RowsHide
		}
		if err := write(ctx, st.Names.Table(t.Name), rows); err != nil {
			return err
		}
	}
	return nil
}

// keepArchive is the tick's archive (sprint.ArchiveDue, sprint.ArchiveStray), from the
// work and merge tables' shapes, one exchange when nothing is due: every stream whose
// last card has landed and on which nothing waits is archived, and the coordinator
// told once (sprint.NStreamArchived); an archived stream that holds a card not landed
// is shown again. A change queued while the machine runs may add to a stream, so
// nothing is archived while the queue holds one; the pump drains it first.
func (st *Store) keepArchive(ctx context.Context) error {
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Work), st.Names.Table(sprint.Merge)})
	if err != nil {
		return err
	}
	work, merge := shapes[0], shapes[1]
	stray := sprint.ArchiveStray(work, merge)
	due := sprint.ArchiveDue(work, merge, nil)
	if len(due) == 0 && len(stray) == 0 {
		return nil
	}
	s := &sprint.Snapshot{Work: sprint.NewTable(sprint.Work), Merge: sprint.NewTable(sprint.Merge)}
	for i, t := range []*sprint.Table{s.Work, s.Merge} {
		for _, r := range shapes[i].Rows {
			t.SetRows(append(t.Rows(), r.Key))
		}
	}
	if len(stray) > 0 {
		if err := st.hideStreams(ctx, s, stray, false); err != nil {
			return err
		}
	}
	if len(due) == 0 {
		return nil
	}
	f, err := st.B.ReadFence(ctx)
	if err != nil || f.Queued > 0 {
		return err
	}
	rec, err := st.readArchive(ctx, work.Epoch)
	if err != nil {
		return err
	}
	if due = sprint.ArchiveDue(work, merge, rec.Kept); len(due) == 0 {
		return nil
	}
	for _, name := range due {
		delete(rec.Kept, name)
	}
	if err := st.writeArchive(ctx, rec); err != nil {
		return err
	}
	if err := st.hideStreams(ctx, s, due, true); err != nil {
		return err
	}
	return st.tellTick(ctx, "stream archive", sprint.NStreamArchived,
		"streams="+strings.Join(due, ",")+": every card landed and nothing waits on it; its cards, cost and landings are kept",
		"nova-sprint stream unarchive <stream> shows one again")
}
