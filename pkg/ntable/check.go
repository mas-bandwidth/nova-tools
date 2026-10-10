package ntable

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
	"github.com/redis/go-redis/v9"
)

// CheckReport covers both directions of record/set membership in one instant.
type CheckReport = typedrec.TableCheck

// Check is an explicit maintenance operation: it scans the member namespace and
// the epoch's owned cells atomically, including orphan records and hidden cells.
// Its cost scales with the namespace; it is not run on the mutation hot path.
func Check(ctx context.Context, c redis.Cmdable, name string) (CheckReport, error) {
	reply, err := (operation{table: name}).call(ctx, c, FnCheck, true)
	if err != nil {
		return CheckReport{}, err
	}
	report, err := typedrec.ParseTableCheck(reply)
	if err != nil {
		return CheckReport{}, fmt.Errorf("table %q: %w", name, err)
	}
	return report, nil
}

// MemberLocation is an indexed placement in this table, or missing/unplaced.
type MemberLocation = typedrec.TableMember

// MemberFind reads and verifies one member's owned placement atomically. It
// does not search external bindings; Check audits the complete namespace.
func MemberFind(ctx context.Context, c redis.Cmdable, name, id string) (MemberLocation, error) {
	reply, err := (operation{table: name, member: id}).call(ctx, c, FnMemberFind, true, id)
	if err != nil {
		return MemberLocation{}, err
	}
	loc, err := typedrec.ParseTableMember(reply)
	if err != nil {
		return MemberLocation{}, fmt.Errorf("table %q member %q: %w", name, id, err)
	}
	return loc, nil
}
