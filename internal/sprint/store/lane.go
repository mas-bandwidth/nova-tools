package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The lanes (sprint/lane.go; docs/SPEC-SPRINT.md section 18): one record per lane
// kind under the deployment's prefix, lanes:<kind>, outside the tables and the fence,
// holding every machine's holders and queue. A take or a give is one read of the
// width (the work table's go_lanes property), one read of the record and one write
// of it; the server runs the workers' verbs one at a time (run --listen), so no two
// steps of the record interleave. It works while the machine is RUNNING or STOPPED.

// laneKey is a lane kind's record.
func laneKey(kind string) string { return "lanes:" + kind }

// laneWhy is why the kind, the machine or the worker is refused, every problem at
// once; "" when none is.
func laneWhy(kind, machine, who string) string {
	var why []string
	if !slices.Contains(sprint.LaneKinds, kind) {
		why = append(why, "a lane kind is one of "+strings.Join(sprint.LaneKinds, ", ")+", found "+kind)
	}
	if !sprint.ValidID(machine) {
		why = append(why, "a machine name wants letters, digits, _ and -: "+machine)
	}
	if !sprint.ValidID(who) {
		why = append(why, "a worker name wants letters, digits, _ and -: "+who)
	}
	return strings.Join(why, "; ")
}

// LaneWidth is the lanes of a kind each machine has: the work table's go_lanes, else
// sprint.LaneWidthDefault (no sprint, or no setting).
func (st *Store) LaneWidth(ctx context.Context) (int, error) {
	pinned, err := st.pin(ctx)
	if err != nil {
		return 0, err
	}
	shapes, err := pinned.B.Shapes(ctx, []string{pinned.Names.Table(sprint.Work)})
	if refusalCode(err) == "NOTABLE" {
		return sprint.LaneWidthDefault, nil // no sprint yet: the default
	}
	if err != nil {
		return 0, err
	}
	var v string
	var set bool
	if len(shapes) > 0 {
		v, set = shapes[0].Props[sprint.PropGoLanes]
	}
	return sprint.LaneWidth(v, set), nil
}

// lanes reads a kind's record; none is no lane held.
func (st *Store) lanes(ctx context.Context, kind string) (sprint.Lanes, KV, error) {
	kv, err := st.rootKV()
	if err != nil {
		return nil, nil, err
	}
	out := sprint.Lanes{}
	raw, ok, err := kv.GetKey(ctx, laneKey(kind))
	if err != nil || !ok {
		return out, kv, err
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, kv, fmt.Errorf("the %s lanes record cannot be read (%v); every holder takes again within %s and waits", kind, err, sprint.LaneWaitFor)
	}
	return out, kv, nil
}

// LaneStep is a take (give false) or a give of who on machine's lanes of kind at the
// store's clock: the answer, with the record written.
func (st *Store) LaneStep(ctx context.Context, kind, machine, who string, give bool) (sprint.LaneAnswer, error) {
	if why := laneWhy(kind, machine, who); why != "" {
		return sprint.LaneAnswer{}, fmt.Errorf("%s", why)
	}
	width, err := st.LaneWidth(ctx)
	if err != nil {
		return sprint.LaneAnswer{}, err
	}
	ls, kv, err := st.lanes(ctx, kind)
	if err != nil {
		return sprint.LaneAnswer{}, err
	}
	now := st.now().UTC()
	var ans sprint.LaneAnswer
	if give {
		ls, ans = ls.Give(machine, who, width, now)
	} else {
		ls, ans = ls.Take(machine, who, width, now)
	}
	b, err := json.Marshal(ls)
	if err != nil {
		return ans, err
	}
	return ans, kv.SetKey(ctx, laneKey(kind), string(b))
}

// LaneRows is every machine's lanes of every kind at the store's clock, the timeouts
// applied as a reader sees them (nothing is written): lane list, where --json --cards. A
// store that keeps no records has no lanes.
func (st *Store) LaneRows(ctx context.Context) ([]sprint.LaneRow, error) {
	if _, err := st.rootKV(); err != nil {
		return nil, nil // ignored: a store that keeps no records has no lanes to show
	}
	width, err := st.LaneWidth(ctx)
	if err != nil {
		return nil, err
	}
	out := []sprint.LaneRow{}
	for _, kind := range sprint.LaneKinds {
		ls, _, err := st.lanes(ctx, kind)
		if err != nil {
			return nil, err
		}
		out = append(out, ls.Rows(kind, width, st.now().UTC())...)
	}
	return out, nil
}
