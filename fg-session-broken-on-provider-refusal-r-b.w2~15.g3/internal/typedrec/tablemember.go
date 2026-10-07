package typedrec

import (
	"fmt"
	"strconv"
)

// TableMember is the atomic placement lookup reply; State distinguishes a
// missing identity from a real, unplaced member. Row and Column name an owned
// cell only when State is placed.
type TableMember struct {
	Epoch, Revision    uint64
	State, Row, Column string
}

func ParseTableMember(reply []any) (TableMember, error) {
	var out TableMember
	bad := func() (TableMember, error) { return TableMember{}, fmt.Errorf("table member: malformed reply") }
	if len(reply) != 6 || reply[0] != "MEMBER" {
		return bad()
	}
	for i, target := range []*uint64{&out.Epoch, &out.Revision} {
		s, ok := reply[i+1].(string)
		if !ok {
			return bad()
		}
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != s {
			return bad()
		}
		*target = n
	}
	for i, target := range []*string{&out.State, &out.Row, &out.Column} {
		s, ok := reply[i+3].(string)
		if !ok {
			return bad()
		}
		*target = s
	}
	switch out.State {
	case "placed":
		if out.Row == "" || out.Column == "" {
			return bad()
		}
	case "missing", "unplaced":
		if out.Row != "" || out.Column != "" {
			return bad()
		}
	default:
		return bad()
	}
	return out, nil
}
