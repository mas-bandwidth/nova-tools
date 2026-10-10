package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// ReadCells reads one row's cards in the named columns of a table: the
// shape, that row's cells, and a read set of what they hold. The cards come
// column by column, in work order within each.
func (st *Store) ReadCells(ctx context.Context, logical, row string, cols ...string) ([]*sprint.Card, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	// The row's cells are read at one revision of the table: a write between
	// the shape and the records (another step, a display cell) is read again,
	// after a jittered wait, as Load reads its tables again, never handed to
	// the caller as a failure (the tick's writes come every part now).
	var last *movedError
	r := st.retry(ctx)
	for r.next(LoadTries) {
		out, err := st.readCellsOnce(ctx, logical, row, cols...)
		var moved *movedError
		if errors.As(err, &moved) {
			last = moved
			continue
		}
		return out, err
	}
	return nil, fmt.Errorf("the tables are busy: table %s kept changing while it was read, %d reads in %s; nothing was changed; run the verb again",
		last.table, r.tries, r.slept().Round(time.Millisecond))
}

func (st *Store) readCellsOnce(ctx context.Context, logical, row string, cols ...string) ([]*sprint.Card, error) {
	name := st.Names.Table(logical)
	shapes, err := st.B.Shapes(ctx, []string{name})
	if err != nil {
		return nil, err
	}
	shape := shapes[0]
	var rows []ntable.Row
	for _, r := range shape.Rows {
		if r.Key == row {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return nil, nil
	}
	shape.Rows = rows
	// only the named columns' cells are read: every other set column is read as
	// text, which has no cell ids. A member's queue (queue --as, every worker's
	// poll) names ready, working and ctl, and its row's ok and failed cells hold
	// every card it ever finished: on 2026-10-10 reading them made the fleet
	// table's read sets 62% of the store's one thread (docs/SPEC-SPRINT.md
	// section 14, store-trips-pipelinedb-bb).
	shape.Columns = slices.Clone(shape.Columns)
	for k := range shape.Columns {
		if shape.Columns[k].HasSet() && !slices.Contains(cols, shape.Columns[k].Name) {
			shape.Columns[k].Projection = ntable.Text
		}
	}
	ids, err := st.B.CellIDs(ctx, []ntable.Table{shape})
	if err != nil {
		return nil, err
	}
	t := sprint.NewTable(logical)
	t.Revision = shape.Revision
	t.SetRows([]string{row})
	if err := st.readInto(ctx, t, ids[name], false); err != nil {
		return nil, err
	}
	var out []*sprint.Card
	for _, col := range cols {
		out = append(out, t.Cell(row, col)...)
	}
	return out, nil
}

// CardIDs is the stored id of every card placed on a table (its logical name) at the
// sprint's epoch: one read of the shape and one of the cells' ids, no record read.
func (st *Store) CardIDs(ctx context.Context, logical string) (map[string]bool, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	name := st.Names.Table(logical)
	shapes, err := st.B.Shapes(ctx, []string{name})
	if err != nil {
		return nil, err
	}
	ids, err := st.B.CellIDs(ctx, shapes)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids[name]))
	for _, id := range ids[name] {
		out[id] = true
	}
	return out, nil
}

// Packets is the packet of each work or read card: its primary, and the work
// cards before it or the one it reads, read by identity in one read set per
// table.
func (st *Store) Packets(ctx context.Context, cards []*sprint.Card) ([]sprint.Packet, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	var prim, work []string
	for _, c := range cards {
		p, earlier, w := sprint.PacketCards(c)
		prim = append(prim, p)
		work = append(work, earlier...)
		if w != "" {
			work = append(work, w)
		}
	}
	byID := func(cs []*sprint.Card) map[string]*sprint.Card {
		m := map[string]*sprint.Card{}
		for _, c := range cs {
			m[c.ID] = c
		}
		return m
	}
	ps, err := st.records(ctx, sprint.Work, prim)
	if err != nil {
		return nil, err
	}
	ws, err := st.records(ctx, sprint.Fleet, work)
	if err != nil {
		return nil, err
	}
	pt, ft := byID(ps), byID(ws)
	var out []sprint.Packet
	for _, c := range cards {
		p, earlier, w := sprint.PacketCards(c)
		var ec []*sprint.Card
		for _, id := range earlier {
			if x := ft[id]; x != nil {
				ec = append(ec, x)
			}
		}
		var wc *sprint.Card
		if w != "" {
			wc = ft[w]
		}
		pk := sprint.PacketOf(st.Names.Prefix, st.epoch, c, pt[p], ec, wc)
		if pk.Tier == "" {
			pk.Tier = sprint.DealtTier(c, pt[p]) // every packet names its tier, a route or none
		}
		out = append(out, pk)
	}
	return out, nil
}

// CardInfo is everything about one primary: its record, its work cards and
// read cards of every attempt (placed or kept), its merge record and the
// judgments open on it.
type CardInfo struct {
	Primary *sprint.Card
	Work    []*sprint.Card
	Reads   []*sprint.Card
	Merge   *sprint.Card
	Open    []sprint.Open
	// Needs is each need with its state; NeededBy the primaries that need it.
	Needs    []sprint.NeedState
	NeededBy []string
	// Hold is what holds the primary now (CardHeld only; nil when it could not be told),
	// and Column the work table's placed cards, as the one read saw them: the place in line.
	Hold   *sprint.Hold
	Column []*sprint.Card
}

// CardOf reads a primary and every card of it by identity.
func (st *Store) CardOf(ctx context.Context, id string) (CardInfo, error) {
	return st.cardOf(ctx, id, false)
}

// CardHeld is CardOf with what holds the primary and the work table's column, from the
// one read of the tables the card's needs, its place in line and its hold all share: the
// work table is read whole once, never again for each of them.
func (st *Store) CardHeld(ctx context.Context, id string) (CardInfo, error) {
	return st.cardOf(ctx, id, true)
}

func (st *Store) cardOf(ctx context.Context, id string, held bool) (CardInfo, error) {
	var v CardInfo
	st, err := st.pin(ctx)
	if err != nil {
		return v, err
	}
	rs, err := st.readSet(ctx, st.Names.Table(sprint.Work), []string{st.sid(id)})
	if err != nil {
		return v, err
	}
	m, ok := rs.Member(st.sid(id))
	if !ok {
		return v, nil
	}
	v.Primary = card(m)
	tables := []string{sprint.Work}
	if held {
		// a fence that cannot be read tells no hold, as Held's error did the card
		if f, err := st.B.ReadFence(ctx); err == nil && f.Pending != nil {
			v.Hold = &sprint.Hold{ID: id, Place: id, Why: pendingWhy(f)}
		} else if err == nil {
			tables = All
		}
	}
	s, err := st.Load(ctx, tables, func(s *sprint.Snapshot) map[string][]string {
		want := append([]string{id}, sprint.Split(v.Primary.F("needs"))...)
		if len(tables) > 1 {
			want = append(want, sprint.ResolveExtras(s)...)
		}
		return map[string][]string{sprint.Work: want}
	})
	if err != nil {
		return v, err
	}
	v.Needs, v.NeededBy = sprint.NeedsOf(s, id)
	v.Column = s.Work.Column(sprint.States...)
	if len(tables) > 1 {
		if hs, err := st.heldState(ctx, s, nil); err == nil {
			hd := sprint.Holder(hs, s.Now, id)
			v.Hold = &hd
		}
	}
	attempts := v.Primary.Int("attempt")
	if attempts > 0 {
		var ids []string
		for k := 1; k <= attempts; k++ {
			ids = append(ids, sprint.WorkCardID(id, k))
		}
		if v.Work, err = st.records(ctx, sprint.Fleet, ids); err != nil {
			return v, err
		}
		shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Readers)})
		if err != nil {
			return v, err
		}
		ids = nil
		for k := 1; k <= attempts; k++ {
			for _, r := range shapes[0].Rows {
				ids = append(ids, sprint.ReadCardID(id, k, r.Key))
			}
		}
		if len(ids) > 0 {
			if v.Reads, err = st.records(ctx, sprint.Readers, ids); err != nil {
				return v, err
			}
		}
		// the read cards on the fleet table (a friend's, and a member's while read cards are
		// on: sprint read_cards.go), one per reader per attempt, on its row
		fleet, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Fleet)})
		if err != nil {
			return v, err
		}
		ids = nil
		for k := 1; k <= attempts; k++ {
			for _, r := range fleet[0].Rows {
				name := r.Key
				if f, ok := sprint.FriendOfRow(r.Key); ok {
					name = f
				}
				ids = append(ids, sprint.ReadCardID(id, k, name))
			}
		}
		if len(ids) > 0 {
			reads, err := st.records(ctx, sprint.Fleet, ids)
			if err != nil {
				return v, err
			}
			v.Reads = append(v.Reads, reads...)
		}
	}
	ms, err := st.records(ctx, sprint.Merge, []string{id})
	if err != nil {
		return v, err
	}
	if len(ms) == 1 {
		v.Merge = ms[0]
	}
	open, err := st.B.OpenNotes(ctx)
	if err != nil {
		return v, err
	}
	open, _ = sprint.SplitOpen(open)
	for _, o := range open {
		if o.Subject() == id || contains(o.Note.Primaries, id) {
			v.Open = append(v.Open, o)
		}
	}
	return v, nil
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// Records reads the named records of a table by identity, placed or kept, in one read
// set per ntable.LimitReadSetMembers ids; an id with no record is left out.
func (st *Store) Records(ctx context.Context, logical string, ids []string) ([]*sprint.Card, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	return st.records(ctx, logical, ids)
}

func (st *Store) records(ctx context.Context, logical string, ids []string) ([]*sprint.Card, error) {
	return st.storedRecords(ctx, logical, st.sids(ids))
}

// storedRecords is records by stored id (sprint.StoredID), as a cell read gives them: an id
// already bound to its epoch is never bound to it again.
func (st *Store) storedRecords(ctx context.Context, logical string, ids []string) ([]*sprint.Card, error) {
	var out []*sprint.Card
	for start := 0; start < len(ids); start += ntable.LimitReadSetMembers {
		end := min(start+ntable.LimitReadSetMembers, len(ids))
		rs, err := st.readSet(ctx, st.Names.Table(logical), ids[start:end])
		if err != nil {
			return nil, err
		}
		for _, m := range rs.Members {
			out = append(out, card(m))
		}
	}
	return out, nil
}

func card(m ntable.ReadSetMember) *sprint.Card {
	c := &sprint.Card{ID: sprint.CardID(m.ID), Score: m.Score, Rev: m.Revision, Fields: m.Fields}
	if m.Placed {
		c.Row, c.Col = m.Row, m.Col
	}
	return c
}

// Dealt is what `where --json --cards` adds to the view: every work card dealt to a fleet
// row and not finished (in the ready or working cell of a machine's row or a friend's), the
// work table as its properties read (the dealt bound a deadline counts), and the open
// judgments naming one of those cards' primaries.
type Dealt struct {
	Cards []*sprint.Card
	Work  *sprint.Table
	Open  []sprint.Open
	// Merging is every primary in the work table's merging column, with its head: the queue a
	// program reads with heads in one call (merge --landed names a card by id and head).
	Merging []*sprint.Card
}

// Dealt reads the cards dealt and not finished: one read of the work and fleet shapes, one of
// the ready and working cells' ids, one read set of their records (never a done card, so the
// read is bounded by the fleet's width, not by the sprint's cards), and the open judgments.
func (st *Store) Dealt(ctx context.Context) (Dealt, error) {
	var d Dealt
	st, err := st.pin(ctx)
	if err != nil {
		return d, err
	}
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Work), st.Names.Table(sprint.Fleet)})
	if err != nil {
		return d, err
	}
	d.Work = sprint.NewTable(sprint.Work)
	d.Work.SetProps(shapes[0].Props)
	inFlight := []string{string(sprint.Ready), string(sprint.Working)}
	ids, err := st.B.CellIDs(ctx, []ntable.Table{cellsOf(shapes[1], inFlight), cellsOf(shapes[0], []string{string(sprint.Merging)})})
	if err != nil {
		return d, err
	}
	if d.Merging, err = st.storedRecords(ctx, sprint.Work, ids[shapes[0].Name]); err != nil {
		return d, err
	}
	// the cell read gives stored ids: read as they are, never bound to the epoch a second
	// time, which found no card at any epoch after the first (the live store's epoch 15)
	cards, err := st.storedRecords(ctx, sprint.Fleet, ids[shapes[1].Name])
	if err != nil {
		return d, err
	}
	primaries := map[string]bool{}
	for _, c := range cards {
		if slices.Contains(inFlight, c.Col) {
			d.Cards = append(d.Cards, c)
			primaries[c.F(sprint.PrimaryField)] = true
		}
	}
	if len(d.Cards) == 0 {
		return d, nil
	}
	open, err := st.B.OpenNotes(ctx)
	if err != nil {
		return d, err
	}
	open, _ = sprint.SplitOpen(open)
	for _, o := range open {
		if primaries[o.Subject()] || slices.ContainsFunc(o.Note.Primaries, func(p string) bool { return primaries[p] }) {
			d.Open = append(d.Open, o)
		}
	}
	return d, nil
}

// cellsOf is a shape with only the named columns, each row's cells cut to match: a cell
// read of it reads those columns' sets and no other.
func cellsOf(t ntable.Table, cols []string) ntable.Table {
	v := t
	v.Columns = nil
	var keep []int
	for j, c := range t.Columns {
		if slices.Contains(cols, c.Name) {
			keep = append(keep, j)
			v.Columns = append(v.Columns, c)
		}
	}
	v.Rows = make([]ntable.Row, len(t.Rows))
	for i, r := range t.Rows {
		cells := make([]ntable.Cell, 0, len(keep))
		for _, j := range keep {
			if j < len(r.Cells) {
				cells = append(cells, r.Cells[j])
			}
		}
		r.Cells = cells
		v.Rows[i] = r
	}
	return v
}
