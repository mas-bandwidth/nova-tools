package verbs

import (
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The small helpers the verbs share: a record's fields and place, an epoch as
// a number, and a local refusal naming its ids. One definition each.

// fieldOK is a record's field and whether it is present.
func fieldOK(r sprintfn.Record, name string) (string, bool) {
	f, ok := r.Fields[name]
	if !ok || !f.Present {
		return "", false
	}
	return f.Value, true
}

// fieldOf is a record's field, "" when absent.
func fieldOf(r sprintfn.Record, name string) string {
	v, _ := fieldOK(r, name)
	return v
}

// placeOf is a record's place: its row and column, "" when it has none.
func placeOf(r sprintfn.Record) (row, col string) {
	if !r.Exists || r.Place == nil {
		return "", ""
	}
	return r.Place.Row, r.Place.Col
}

// colOf is the column a record is placed in, "" when it has no place.
func colOf(r sprintfn.Record) string {
	_, col := placeOf(r)
	return col
}

// placeText is a record's place as "row:col", or the word for a record with
// none, for a refusal's text.
func placeText(r sprintfn.Record) string {
	switch {
	case !r.Exists:
		return "no record"
	case r.Place == nil:
		return "off the table"
	}
	return r.Place.Row + ":" + r.Place.Col
}

// epochOf is a wire epoch as a number, an error when it is not one.
func epochOf(d tset.Decimal) (uint64, error) {
	n, ok := undec(d)
	if !ok {
		return 0, fmt.Errorf("the read's epoch %q is not a number", d)
	}
	return n, nil
}

// epochNum is a wire epoch as a number, 0 when it is not one (the read refuses
// it first).
func epochNum(d tset.Decimal) uint64 {
	n, _ := undec(d)
	return n
}

// refuseIDs is refuseLocal naming the cards (or members) it refuses in
// Detail.IDs, as a refused step names its ids (1.5.3).
func refuseIDs(verb, code string, ids []string, format string, args ...any) *Refused {
	rf := refuseLocal(verb, code, format, args...)
	rf.Refusal.Detail.IDs = append([]string{}, ids...)
	return rf
}
