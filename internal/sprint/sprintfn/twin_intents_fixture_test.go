package sprintfn

import (
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// fixRec is a record of a fixture world: its place (an empty col is a record
// kept with no place), its revision and its application fields.
type fixRec struct {
	row, col string
	rev      string
	fields   map[string]string
}

// fixWorld is a derive world made of fixtures: the work table's records by id
// and the entries of the step. It is what the pure tests and the tests that
// hold the Go phase equal to the Lua run the phase over, so that both read the
// same state.
type fixWorld struct {
	recs    map[string]fixRec
	entries []tset.Entry
}

func (w *fixWorld) records(ids, fields []string) ([]tset.MemberRecord, *Refusal) {
	out := make([]tset.MemberRecord, len(ids))
	for i, id := range ids {
		r, ok := w.recs[id]
		if !ok {
			out[i] = tset.MemberRecord{ID: id}
			continue
		}
		rec := tset.MemberRecord{ID: id, Exists: true, Epoch: "0", Revision: tset.Decimal(r.rev), Fields: map[string]tset.FieldValue{}}
		if r.col != "" {
			rec.Place = &tset.CellPlace{Row: r.row, Col: r.col}
		}
		for _, f := range fields {
			v, present := r.fields[f]
			rec.Fields[f] = tset.FieldValue{Present: present, Value: v}
		}
		out[i] = rec
	}
	return out, nil
}

func (w *fixWorld) created(card string) (createdCard, bool) { return createdIn(w.entries, card) }

// fixState is the call's State over a keyspace the test has written.
func fixState(ks *keyspace, nowMS string) *State {
	return &State{Prefix: testPrefix, Epoch: "0", NowMS: tset.Decimal(nowMS), Names: sprint.Names{Prefix: testPrefix}, Keys: &Keys{ks: ks}}
}
