package bus

import (
	"context"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/redis/go-redis/v9"
)

// The roster is nova-config's friend and machine rows as `apply` wrote them
// into the store the bus reads (the sets `friends` and `machines`). Where the
// bus store is its own Redis (the fleet row's bus, fleet:bus), apply's copy
// lands in the sprint store alone, and the bus store's sets hold what was put
// there by hand: a friend whose row was added since is no known name to the
// bus, and every card dealt to her logs `a friend was not told of her card`.
// A sender that holds her row (nova-sprint's friend sync reads it from
// nova-config) makes her a name first: KnowFriends.

// FriendAdder is a Store that can add names to its set `friends`.
type FriendAdder interface {
	// AddFriends adds names to the set `friends` and says which were not
	// there, in one trip.
	AddFriends(ctx context.Context, names ...string) (added []string, err error)
}

// KnowFriends makes each of names, every one a nova-config friend row the
// caller holds, a friend on s's roster, and says which it added. The roster
// is read first, so a name already there costs one read and no write, and a
// store whose login may not write the set is asked to only when a name is
// missing. A store that cannot add (no FriendAdder) adds none, and the send
// after is checked against its roster as before. A name that is no name is
// refused, nothing written.
func KnowFriends(ctx context.Context, s Store, names ...string) ([]string, error) {
	var problems []string
	for _, n := range names {
		if p := CheckName(n); p != "" {
			problems = append(problems, p)
		}
	}
	if len(problems) > 0 {
		return nil, &Refusal{problems}
	}
	a, ok := s.(FriendAdder)
	if !ok || len(names) == 0 {
		return nil, nil
	}
	friends, machines, _, err := s.Members(ctx)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, n := range names {
		if !slices.Contains(friends, n) && !slices.Contains(machines, n) && !slices.Contains(missing, n) {
			missing = append(missing, n)
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	return a.AddFriends(ctx, missing...)
}

// AddFriends is one pipeline of SADD friends <name>, one per name: a name
// whose SADD answers 1 was not there.
func (r Redis) AddFriends(ctx context.Context, names ...string) ([]string, error) {
	pipe := r.C.Pipeline()
	cmds := make([]*redis.IntCmd, len(names))
	for i, n := range names {
		cmds[i] = pipe.SAdd(ctx, friendsKey, n)
	}
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return nil, err
	}
	var added []string
	for i, c := range cmds {
		if c.Val() == 1 {
			added = append(added, names[i])
		}
	}
	return added, nil
}

var _ FriendAdder = Redis{}
