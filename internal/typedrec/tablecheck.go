package typedrec

import (
	"fmt"
	"strconv"
)

// TableCheck is the table maintenance function's RESP report. Counts are
// canonical decimal strings so uint64 values survive Lua and RESP exactly.
type TableCheck struct{ Epoch, Revision, Members, Cells uint64 }

// ParseTableCheck decodes the complete ns_table_check reply.
func ParseTableCheck(reply []any) (TableCheck, error) {
	var out TableCheck
	if len(reply) != 5 {
		return out, fmt.Errorf("table check wants 5 reply fields, got %d", len(reply))
	}
	if tag, ok := reply[0].(string); !ok || tag != "CHECK" {
		return out, fmt.Errorf("table check has an invalid reply tag")
	}
	for i, target := range []*uint64{&out.Epoch, &out.Revision, &out.Members, &out.Cells} {
		s, ok := reply[i+1].(string)
		if !ok {
			return TableCheck{}, fmt.Errorf("table check field %d wants a decimal string", i+1)
		}
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != s {
			return TableCheck{}, fmt.Errorf("table check field %d has invalid uint64 %q", i+1, s)
		}
		*target = n
	}
	return out, nil
}
