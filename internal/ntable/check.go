package ntable

import (
	"context"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// CheckReport covers both directions of record/set membership in one instant.
type CheckReport struct{ Epoch, Revision, Members, Cells uint64 }

// Check is an explicit maintenance operation: it scans the member namespace and
// the epoch's owned cells atomically, including orphan records and hidden cells.
// Its cost scales with the namespace; it is not run on the mutation hot path.
func Check(ctx context.Context, c redis.Cmdable, name string) (CheckReport, error) {
	reply, err := (operation{table: name}).call(ctx, c, FnCheck, true)
	if err != nil {
		return CheckReport{}, err
	}
	if len(reply) != 5 || fmt.Sprint(reply[0]) != "CHECK" {
		return CheckReport{}, fmt.Errorf("table %q: malformed check reply", name)
	}
	var out CheckReport
	for i, p := range []*uint64{&out.Epoch, &out.Revision, &out.Members, &out.Cells} {
		*p, err = strconv.ParseUint(fmt.Sprint(reply[i+1]), 10, 64)
		if err != nil {
			return CheckReport{}, err
		}
	}
	return out, nil
}
