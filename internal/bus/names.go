package bus

import (
	"context"
	"slices"
)

// Enroller is a Store whose roster can be told friends: their names added to
// the set `friends` (SADD), never one taken off. nova-config's apply writes the
// friend rows to the sprint store, and the bus store may be another Redis
// (the fleet runs it apart, NOVA_BUS_REDIS beside NOVA_SPRINT_REDIS), so the
// one who reads the rows (nova-sprint friend sync) tells the bus store
// (SPEC-BUS.md, the config). A row removed stays a known name here: the
// bus never deletes.
type Enroller interface {
	Enroll(ctx context.Context, friends ...string) error
}

var _ Enroller = Redis{}

// Enroll adds the friends to the set `friends`, in one trip.
func (r Redis) Enroll(ctx context.Context, friends ...string) error {
	if len(friends) == 0 {
		return nil
	}
	args := make([]any, len(friends))
	for i, f := range friends {
		args[i] = f
	}
	return r.C.SAdd(ctx, friendsKey, args...).Err()
}

// Enroll makes each of friends a known name of the bus, a friend owed a
// receipt: the ones the roster does not hold are added, and added is them,
// sorted. A name that is no name (CheckName) is refused and the rest are
// added all the same. A store that cannot be told (a test's fake) is told
// nothing and adds none. Two trips: the roster, then the add when one is
// missing.
func (b *Bus) Enroll(ctx context.Context, friends ...string) (added []string, err error) {
	e, ok := b.Store.(Enroller)
	if !ok {
		return nil, nil
	}
	var problems []string
	var named []string
	for _, f := range friends {
		if p := CheckName(f); p != "" {
			problems = append(problems, p)
			continue
		}
		named = append(named, f)
	}
	if len(named) > 0 {
		known, machines, _, err := b.Store.Members(ctx)
		if err != nil {
			return nil, err
		}
		known = append(known, machines...)
		for _, f := range slices.Compact(slices.Sorted(slices.Values(named))) {
			if !slices.Contains(known, f) {
				added = append(added, f)
			}
		}
		if err := e.Enroll(ctx, added...); err != nil {
			return nil, err
		}
	}
	if len(problems) > 0 {
		return added, &Refusal{problems}
	}
	return added, nil
}
