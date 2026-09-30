package store

import (
	"context"

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

// Packets is the packet of each work or read card: its primary, and the work
// card before it or the one it reads, read by identity in one read set per
// table.
func (st *Store) Packets(ctx context.Context, cards []*sprint.Card) ([]sprint.Packet, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	var prim, work []string
	for _, c := range cards {
		p, prev, w := sprint.PacketCards(c)
		prim = append(prim, p)
		for _, id := range []string{prev, w} {
			if id != "" {
				work = append(work, id)
			}
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
		p, prev, w := sprint.PacketCards(c)
		var pc, wc *sprint.Card
		if prev != "" {
			pc = ft[prev]
		}
		if w != "" {
			wc = ft[w]
		}
		out = append(out, sprint.PacketOf(st.Names.Prefix, st.epoch, c, pt[p], pc, wc))
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
}

// CardOf reads a primary and every card of it by identity.
func (st *Store) CardOf(ctx context.Context, id string) (CardInfo, error) {
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
	s, err := st.Load(ctx, []string{sprint.Work}, func(*sprint.Snapshot) map[string][]string {
		return map[string][]string{sprint.Work: append([]string{id}, sprint.Split(v.Primary.F("needs"))...)}
	})
	if err != nil {
		return v, err
	}
	v.Needs, v.NeededBy = sprint.NeedsOf(s, id)
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

func (st *Store) records(ctx context.Context, logical string, ids []string) ([]*sprint.Card, error) {
	var out []*sprint.Card
	for start := 0; start < len(ids); start += ntable.LimitReadSetMembers {
		end := min(start+ntable.LimitReadSetMembers, len(ids))
		rs, err := st.readSet(ctx, st.Names.Table(logical), st.sids(ids[start:end]))
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
